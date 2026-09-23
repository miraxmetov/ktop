package kube

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	metricsapi "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

func limited(cpu, mem string) corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse(mem),
	}
}

func pod(name string, phase corev1.PodPhase, limits corev1.ResourceList, status corev1.ContainerStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "production"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Resources: corev1.ResourceRequirements{Limits: limits}}},
		},
		Status: corev1.PodStatus{Phase: phase, ContainerStatuses: []corev1.ContainerStatus{status}},
	}
}

func podMetrics(name, cpu, mem string) metricsapi.PodMetrics {
	return metricsapi.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "production"},
		Containers: []metricsapi.ContainerMetrics{{
			Name:  "app",
			Usage: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(mem)},
		}},
	}
}

func metricsClient(items ...metricsapi.PodMetrics) *metricsfake.Clientset {
	cs := metricsfake.NewSimpleClientset()
	cs.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &metricsapi.PodMetricsList{Items: items}, nil
	})
	return cs
}

func rowsByName(rows []Row) map[string]Row {
	byName := map[string]Row{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	return byName
}

func testRows(t *testing.T) []Row {
	t.Helper()
	finished := metav1.NewTime(time.Now().Add(-4 * time.Minute))

	oom := pod("api", corev1.PodRunning, limited("1", "1Gi"), corev1.ContainerStatus{
		Name: "app", Ready: true, RestartCount: 7,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			ExitCode: 137, Reason: "OOMKilled", FinishedAt: finished,
		}},
	})
	healthy := pod("web", corev1.PodRunning, limited("1", "1Gi"), corev1.ContainerStatus{
		Name: "app", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	})
	crashing := pod("broken", corev1.PodPending, limited("1", "1Gi"), corev1.ContainerStatus{
		Name: "app", RestartCount: 12,
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
	})
	nolimit := pod("nolimit", corev1.PodRunning, nil, corev1.ContainerStatus{
		Name: "app", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	})
	done := pod("job", corev1.PodSucceeded, nil, corev1.ContainerStatus{
		Name:  "app",
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0, Reason: "Completed"}},
	})
	notReady := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "half", Namespace: "production"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "a", Resources: corev1.ResourceRequirements{Limits: limited("500m", "512Mi")}},
			{Name: "b", Resources: corev1.ResourceRequirements{Limits: limited("500m", "512Mi")}},
		}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{
			{Name: "a", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			{Name: "b", Ready: false, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
		}},
	}
	terminating := pod("dying", corev1.PodRunning, limited("1", "1Gi"), corev1.ContainerStatus{
		Name: "app", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	})
	deleted := metav1.NewTime(time.Now())
	terminating.DeletionTimestamp = &deleted

	pods := k8sfake.NewSimpleClientset(oom, healthy, crashing, nolimit, done, notReady, terminating)
	metrics := metricsClient(
		podMetrics("api", "940m", "1840Mi"),
		podMetrics("web", "210m", "612Mi"),
		podMetrics("nolimit", "3m", "128Mi"),
		podMetrics("half", "250m", "256Mi"),
	)

	client := NewWithClients(pods, metrics)
	res, err := client.Rows(context.Background(), "production")
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if res.Note != "" {
		t.Fatalf("unexpected note: %q", res.Note)
	}
	return res.Rows
}

func TestRowsPercentagesAndFlags(t *testing.T) {
	rows := testRows(t)
	if len(rows) != 7 {
		t.Fatalf("want 7 rows, got %d", len(rows))
	}
	byName := rowsByName(rows)

	api := byName["api"]
	if api.CPUPct < 93.9 || api.CPUPct > 94.1 {
		t.Errorf("api cpu%%: want ~94, got %.2f", api.CPUPct)
	}
	if api.MemPct < 179.6 || api.MemPct > 179.8 {
		t.Errorf("api mem%%: want ~179.7, got %.2f", api.MemPct)
	}
	if api.Restarts != 7 {
		t.Errorf("restart total comes from the pod itself, got %d", api.Restarts)
	}
	if api.NewRestarts != 0 || api.OOMs != 0 {
		t.Errorf("session counters start at zero, got %d/%d", api.NewRestarts, api.OOMs)
	}
	if api.ExitCode == nil || *api.ExitCode != 137 || api.ExitReason != "OOMKilled" {
		t.Errorf("api exit: got %v %q", api.ExitCode, api.ExitReason)
	}
	if api.LastRestart.IsZero() {
		t.Error("api last restart: want a timestamp")
	}

	if got := byName["nolimit"]; got.CPUPct != -1 || got.MemPct != -1 || !got.HasCPU {
		t.Errorf("nolimit: want usage without percentages, got %+v", got)
	}
	if got := byName["web"]; got.Problem {
		t.Error("web: healthy pod must not be flagged")
	}
	if got := byName["broken"]; got.Status != "CrashLoopBackOff" || got.Severity != Bad {
		t.Errorf("broken: got %q severity %v", got.Status, got.Severity)
	}
	if got := byName["half"]; got.Status != "NotReady 1/2" || got.Severity != Warn {
		t.Errorf("half: got %q severity %v", got.Status, got.Severity)
	}
	if got := byName["dying"]; got.Status != "Terminating" {
		t.Errorf("dying: got %q", got.Status)
	}
	if got := byName["job"]; got.Status != "Completed" || got.Problem {
		t.Errorf("job: got %q problem=%v", got.Status, got.Problem)
	}
}

func TestRowsSortProblemsFirst(t *testing.T) {
	rows := testRows(t)
	seenHealthy := false
	for _, r := range rows {
		if !r.Problem {
			seenHealthy = true
			continue
		}
		if seenHealthy {
			t.Fatalf("problem pod %q sorted after a healthy pod", r.Name)
		}
	}
	if rows[0].Name != "api" {
		t.Errorf("hottest problem pod first: want api, got %q", rows[0].Name)
	}
}

func TestRowsWithoutMetrics(t *testing.T) {
	healthy := pod("web", corev1.PodRunning, limited("1", "1Gi"), corev1.ContainerStatus{
		Name: "app", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	})
	client := NewWithClients(k8sfake.NewSimpleClientset(healthy), metricsClient())
	res, err := client.Rows(context.Background(), "production")
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(res.Rows))
	}
	if res.Rows[0].HasCPU || res.Rows[0].CPUPct != -1 || res.Rows[0].Worst != -1 {
		t.Errorf("without metrics: want empty usage, got %+v", res.Rows[0])
	}
}

func metricsfakeForbidden() *metricsfake.Clientset {
	cs := metricsfake.NewSimpleClientset()
	cs.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, forbidden("pods")
	})
	return cs
}
