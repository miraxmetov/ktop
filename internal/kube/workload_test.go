package kube

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func ownedPod(name, owner, ownerKind string, restarts int32, limits corev1.ResourceList) *corev1.Pod {
	p := pod(name, corev1.PodRunning, limits, corev1.ContainerStatus{
		Name:         "app",
		Ready:        true,
		RestartCount: restarts,
		State:        corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			ExitCode: 137, Reason: "OOMKilled", FinishedAt: metav1.NewTime(time.Now().Add(-time.Hour)),
		}},
	})
	p.UID = types.UID(name)
	p.OwnerReferences = []metav1.OwnerReference{{Kind: ownerKind, Name: owner}}
	return p
}

func deployment(name string, ready, desired int32, age time.Duration) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "production",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-age)),
		},
		Spec:   appsv1.DeploymentSpec{Replicas: &desired},
		Status: appsv1.DeploymentStatus{ReadyReplicas: ready},
	}
}

func replicaSet(name, owner string, ready, desired int32) *appsv1.ReplicaSet {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "production"},
		Spec:       appsv1.ReplicaSetSpec{Replicas: &desired},
		Status:     appsv1.ReplicaSetStatus{ReadyReplicas: ready, Replicas: desired},
	}
	if owner != "" {
		rs.OwnerReferences = []metav1.OwnerReference{{Kind: "Deployment", Name: owner}}
	}
	return rs
}

func TestDeploymentRowsAggregateTheirPods(t *testing.T) {
	limits := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("500m"),
		corev1.ResourceMemory: resource.MustParse("512Mi"),
	}
	pods := k8sfake.NewSimpleClientset(
		deployment("api", 2, 2, 50*time.Hour),
		replicaSet("api-abc", "api", 2, 2),
		ownedPod("api-abc-1", "api-abc", "ReplicaSet", 3, limits),
		ownedPod("api-abc-2", "api-abc", "ReplicaSet", 4, limits),
	)
	metrics := metricsClient(
		podMetrics("api-abc-1", "100m", "128Mi"),
		podMetrics("api-abc-2", "300m", "256Mi"),
	)

	client := NewWithClients(pods, metrics)
	res, err := client.Rows(context.Background(), "production", KindDeployment)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("want one deployment, got %d", len(res.Rows))
	}

	row := res.Rows[0]
	if row.Name != "api" || row.Status != "2/2 ready" || row.Severity != Good {
		t.Errorf("row: %+v", row)
	}
	if row.CPU != 400 || row.Mem != 384 {
		t.Errorf("usage must be summed: %.0fm %.0fMi", row.CPU, row.Mem)
	}
	if row.CPULimit != 1000 || row.MemLimit != 1024 {
		t.Errorf("limits must be summed: %.0fm %.0fMi", row.CPULimit, row.MemLimit)
	}
	if row.CPUPct < 39.9 || row.CPUPct > 40.1 {
		t.Errorf("cpu%%: %.1f", row.CPUPct)
	}
	if row.Restarts != 7 {
		t.Errorf("restarts must be summed: %d", row.Restarts)
	}
	if row.Created.IsZero() {
		t.Error("created must come from the deployment")
	}
	if row.LastRestart.IsZero() {
		t.Error("the newest pod restart must be carried up")
	}
	if len(row.Pods) != 2 {
		t.Errorf("the row must remember its pods: %v", row.Pods)
	}
}

func TestWorkloadStatusFollowsReadiness(t *testing.T) {
	pods := k8sfake.NewSimpleClientset(
		deployment("half", 1, 3, time.Hour),
		deployment("down", 0, 2, time.Hour),
		deployment("idle", 0, 0, time.Hour),
	)
	client := NewWithClients(pods, metricsClient())

	res, err := client.Rows(context.Background(), "production", KindDeployment)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	byName := rowsByName(res.Rows)

	if got := byName["half"]; got.Severity != Warn || got.Status != "1/3 ready" {
		t.Errorf("half: %q %v", got.Status, got.Severity)
	}
	if got := byName["down"]; got.Severity != Bad {
		t.Errorf("nothing ready must read as broken: %v", got.Severity)
	}
	if got := byName["idle"]; got.Severity != Muted || got.Status != "scaled to zero" {
		t.Errorf("idle: %q %v", got.Status, got.Severity)
	}
}

func TestOtherKindsGroupByTheirOwnOwner(t *testing.T) {
	daemon := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "node-agent", Namespace: "production"},
		Status:     appsv1.DaemonSetStatus{NumberReady: 2, DesiredNumberScheduled: 2},
	}
	stateful := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "production"},
		Spec:       appsv1.StatefulSetSpec{Replicas: int32p(1)},
		Status:     appsv1.StatefulSetStatus{ReadyReplicas: 1},
	}
	pods := k8sfake.NewSimpleClientset(
		daemon, stateful,
		replicaSet("web-xyz", "", 1, 1),
		ownedPod("node-agent-a", "node-agent", "DaemonSet", 0, nil),
		ownedPod("node-agent-b", "node-agent", "DaemonSet", 1, nil),
		ownedPod("db-0", "db", "StatefulSet", 0, nil),
		ownedPod("web-xyz-1", "web-xyz", "ReplicaSet", 2, nil),
	)
	client := NewWithClients(pods, metricsClient())

	for _, c := range []struct {
		kind  Kind
		name  string
		pods  int
		total int
	}{
		{KindDaemonSet, "node-agent", 2, 1},
		{KindStatefulSet, "db", 1, 1},
		{KindReplicaSet, "web-xyz", 1, 1},
	} {
		res, err := client.Rows(context.Background(), "production", c.kind)
		if err != nil {
			t.Fatalf("%v: %v", c.kind, err)
		}
		if len(res.Rows) != c.total {
			t.Fatalf("%v: want %d rows, got %d", c.kind, c.total, len(res.Rows))
		}
		row := res.Rows[0]
		if row.Name != c.name || len(row.Pods) != c.pods {
			t.Errorf("%v: %q with %v", c.kind, row.Name, row.Pods)
		}
	}
}

func TestKindNames(t *testing.T) {
	if got := KindPod.String(); got != "Pods" {
		t.Errorf("got %q", got)
	}
	if got := KindStatefulSet.Column(); got != "STATEFULSET" {
		t.Errorf("got %q", got)
	}
	if kind, ok := KindByName("DaemonSets"); !ok || kind != KindDaemonSet {
		t.Errorf("got %v %v", kind, ok)
	}
	if _, ok := KindByName("Nodes"); ok {
		t.Error("unknown names must be refused")
	}
	if len(Kinds()) != 5 {
		t.Errorf("five kinds, got %d", len(Kinds()))
	}
	if strings.Join([]string{KindPod.String(), KindDeployment.String()}, " ") != "Pods Deployments" {
		t.Error("names must read as plurals")
	}
}

func int32p(v int32) *int32 { return &v }

func TestWorkloadDetailReadsInThreeForms(t *testing.T) {
	now := time.Now()
	deploy := deployment("api", 2, 3, 50*time.Hour)
	deploy.Labels = map[string]string{"app": "api"}
	deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}}
	deploy.Spec.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType}
	deploy.Spec.Template.Spec = corev1.PodSpec{Containers: []corev1.Container{{
		Name:  "app",
		Image: "registry.example.com/api:1.4.2",
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
		},
	}}}
	deploy.ManagedFields = []metav1.ManagedFieldsEntry{{Manager: "kubectl"}}

	client := NewWithClients(k8sfake.NewSimpleClientset(deploy), metricsClient())
	detail, err := client.Workload(context.Background(), "production", KindDeployment, "api")
	if err != nil {
		t.Fatalf("Workload: %v", err)
	}

	pods := []string{"api-abc-1", "api-abc-2"}
	described := strings.Join(detail.Describe(pods, now), "\n")
	for _, want := range []string{
		"DEPLOYMENT", "name", "api", "ready", "2 of 3", "created", "strategy", "RollingUpdate",
		"selector", "app=api", "LABELS", "TEMPLATE", "registry.example.com/api:1.4.2",
		"limits", "cpu 500m", "PODS", "api-abc-1",
	} {
		if !strings.Contains(described, want) {
			t.Errorf("description misses %q:\n%s", want, described)
		}
	}

	textual := strings.Join(detail.Textual(pods, now), " ")
	for _, want := range []string{
		"Deployment api lives in namespace production", "only 2 of 3 replicas are ready",
		"RollingUpdate", "limited to cpu 500m", "owns 2 pods",
	} {
		if !strings.Contains(textual, want) {
			t.Errorf("prose misses %q:\n%s", want, textual)
		}
	}

	body, err := detail.YAML()
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	manifest := strings.Join(body, "\n")
	if !strings.Contains(manifest, "name: api") {
		t.Errorf("yaml must carry the object:\n%s", manifest)
	}
	if strings.Contains(manifest, "managedFields") {
		t.Error("managedFields must be stripped")
	}
}

func TestWorkloadDetailReportsAMissingObject(t *testing.T) {
	client := NewWithClients(k8sfake.NewSimpleClientset(), metricsClient())

	_, err := client.Workload(context.Background(), "production", KindDeployment, "gone")
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "deployment gone is gone from namespace production") {
		t.Errorf("message: %v", err)
	}
}
