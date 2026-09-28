package kube

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func TestSummaryCountsWhatTheNamespaceHolds(t *testing.T) {
	limits := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("1"),
		corev1.ResourceMemory: resource.MustParse("1Gi"),
	}

	healthy := ownedPod("api-abc-1", "api-abc", "ReplicaSet", 0, limits)
	crashing := ownedPod("api-abc-2", "api-abc", "ReplicaSet", 7, limits)
	crashing.Status.ContainerStatuses[0].State = corev1.ContainerState{
		Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
	}

	quota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "team", Namespace: "production"},
		Status: corev1.ResourceQuotaStatus{
			Hard: corev1.ResourceList{corev1.ResourceLimitsCPU: resource.MustParse("16")},
			Used: corev1.ResourceList{corev1.ResourceLimitsCPU: resource.MustParse("15")},
		},
	}
	ranges := &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{Name: "defaults", Namespace: "production"},
		Spec:       corev1.LimitRangeSpec{Limits: []corev1.LimitRangeItem{{Type: corev1.LimitTypeContainer}}},
	}

	client := NewWithClients(
		k8sfake.NewSimpleClientset(healthy, crashing, deployment("api", 1, 2, time.Hour), replicaSet("api-abc", "api", 1, 2), quota, ranges),
		metricsClient(podMetrics("api-abc-1", "500m", "512Mi"), podMetrics("api-abc-2", "300m", "256Mi")),
	)

	summary, err := client.Summary(context.Background(), "production")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}

	if summary.Pods != 2 || summary.Running != 1 || summary.Broken != 1 {
		t.Errorf("pods: %d total, %d ready, %d broken", summary.Pods, summary.Running, summary.Broken)
	}
	if summary.Restarts != 7 || summary.Restarted != "api-abc-2" || summary.RestartsOf != 7 {
		t.Errorf("restarts: %+v", summary)
	}

	body := strings.Join(summary.Lines(), "\n")
	for _, want := range []string{
		"pods", "2 (1 ready, 1 in trouble)",
		"workloads", "1 deployment (one short of its replicas)", "1 replica set",
		"usage", "restarts", "7 in total", "restarted most", "api-abc-2, 7 times",
		"quotas", "team at 94% of its tightest limit", "limit ranges", "defaults",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the summary misses %q:\n%s", want, body)
		}
	}
}

func TestSummaryOfAnEmptyNamespaceStaysShort(t *testing.T) {
	client := NewWithClients(k8sfake.NewSimpleClientset(), metricsClient())

	summary, err := client.Summary(context.Background(), "production")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}

	lines := summary.Lines()
	if len(lines) != 1 || !strings.Contains(lines[0], "none") {
		t.Errorf("an empty namespace: %v", lines)
	}
	if got := StatusText(summary.Rows, LevelAll, DimAll, KindPod); got != "Nothing is wrong in this namespace." {
		t.Errorf("status line: %q", got)
	}
}
