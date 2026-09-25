package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	metricsapi "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

func crashing(name string, uid types.UID, restarts int32, reason string) *corev1.Pod {
	p := pod(name, corev1.PodRunning, limited("1", "1Gi"), corev1.ContainerStatus{
		Name:         "app",
		Ready:        true,
		RestartCount: restarts,
		State:        corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			ExitCode:   137,
			Reason:     reason,
			FinishedAt: metav1.Now(),
		}},
	})
	p.UID = uid
	return p
}

func poll(t *testing.T, client *Client) map[string]Row {
	t.Helper()
	res, err := client.Rows(context.Background(), "production")
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	return rowsByName(res.Rows)
}

func TestCountersStartAtZeroAndCountOnlyNewRestarts(t *testing.T) {
	first := crashing("api", "uid-1", 7, "OOMKilled")
	pods := k8sfake.NewSimpleClientset(first)
	client := NewWithClients(pods, metricsClient())

	row := poll(t, client)["api"]
	if row.Restarts != 7 {
		t.Fatalf("lifetime total must come from the pod, got %d", row.Restarts)
	}
	if row.NewRestarts != 0 || row.OOMs != 0 {
		t.Fatalf("session counters must start at zero, got %d/%d", row.NewRestarts, row.OOMs)
	}

	if row := poll(t, client)["api"]; row.NewRestarts != 0 || row.Restarts != 7 {
		t.Fatalf("an unchanged pod must not move the counters, got %d +%d", row.Restarts, row.NewRestarts)
	}

	updated := crashing("api", "uid-1", 9, "OOMKilled")
	if _, err := pods.CoreV1().Pods("production").Update(context.Background(), updated, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if row := poll(t, client)["api"]; row.Restarts != 9 || row.NewRestarts != 2 || row.OOMs != 2 {
		t.Fatalf("two OOM restarts: got total %d, session %d, ooms %d", row.Restarts, row.NewRestarts, row.OOMs)
	}

	plain := crashing("api", "uid-1", 10, "Error")
	if _, err := pods.CoreV1().Pods("production").Update(context.Background(), plain, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update: %v", err)
	}
	row = poll(t, client)["api"]
	if row.Restarts != 10 || row.NewRestarts != 3 {
		t.Errorf("restart counters: got total %d, session %d", row.Restarts, row.NewRestarts)
	}
	if row.OOMs != 2 {
		t.Errorf("a non-OOM restart must not raise the OOM counter: got %d, want 2", row.OOMs)
	}
}

func TestCountersFollowTheContainerNotThePodName(t *testing.T) {
	pods := k8sfake.NewSimpleClientset(crashing("api", "uid-1", 4, "OOMKilled"))
	client := NewWithClients(pods, metricsClient())
	poll(t, client)

	updated := crashing("api", "uid-1", 6, "OOMKilled")
	pods.CoreV1().Pods("production").Update(context.Background(), updated, metav1.UpdateOptions{})
	if row := poll(t, client)["api"]; row.Restarts != 6 || row.NewRestarts != 2 {
		t.Fatalf("counters before the pod is replaced: total %d, session %d", row.Restarts, row.NewRestarts)
	}

	if err := pods.CoreV1().Pods("production").Delete(context.Background(), "api", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	poll(t, client)

	replacement := crashing("api", "uid-2", 3, "OOMKilled")
	if _, err := pods.CoreV1().Pods("production").Create(context.Background(), replacement, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if row := poll(t, client)["api"]; row.Restarts != 3 || row.NewRestarts != 0 {
		t.Fatalf("a recreated pod starts its own session count: total %d, session %d", row.Restarts, row.NewRestarts)
	}
}

func TestCountersKeepNamespacesApart(t *testing.T) {
	staging := crashing("api", "uid-9", 1, "OOMKilled")
	staging.Namespace = "staging"
	pods := k8sfake.NewSimpleClientset(crashing("api", "uid-1", 1, "OOMKilled"), staging)
	client := NewWithClients(pods, metricsClient())

	poll(t, client)
	if _, err := client.Rows(context.Background(), "staging"); err != nil {
		t.Fatalf("Rows(staging): %v", err)
	}

	updated := crashing("api", "uid-1", 2, "OOMKilled")
	pods.CoreV1().Pods("production").Update(context.Background(), updated, metav1.UpdateOptions{})

	if row := poll(t, client)["api"]; row.NewRestarts != 1 || row.Restarts != 2 {
		t.Fatalf("watching another namespace must not reset the baseline: total %d, session %d",
			row.Restarts, row.NewRestarts)
	}
}

func sidecarPod(name string, appLimits, sidecarLimits corev1.ResourceList) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "production"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app", Resources: corev1.ResourceRequirements{Limits: appLimits}},
			{Name: "vault-agent", Resources: corev1.ResourceRequirements{Limits: sidecarLimits}},
		}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{
			{Name: "app", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			{Name: "vault-agent", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
		}},
	}
}

func twoContainerMetrics(name, appCPU, appMem, sidecarCPU, sidecarMem string) metricsapi.PodMetrics {
	return metricsapi.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "production"},
		Containers: []metricsapi.ContainerMetrics{
			{Name: "app", Usage: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse(appCPU), corev1.ResourceMemory: resource.MustParse(appMem)}},
			{Name: "vault-agent", Usage: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse(sidecarCPU), corev1.ResourceMemory: resource.MustParse(sidecarMem)}},
		},
	}
}

func TestPartialLimitsGiveNoPercentage(t *testing.T) {
	pods := k8sfake.NewSimpleClientset(
		sidecarPod("unlimited-app", nil, limited("50m", "64Mi")),
	)
	metrics := metricsClient(twoContainerMetrics("unlimited-app", "1m", "128296Ki", "1m", "28912Ki"))

	client := NewWithClients(pods, metrics)
	row := poll(t, client)["unlimited-app"]

	if row.CPUPct != -1 || row.MemPct != -1 {
		t.Fatalf("a sidecar limit says nothing about the pod: cpu %.1f%%, mem %.1f%%", row.CPUPct, row.MemPct)
	}
	if row.Worst != -1 {
		t.Errorf("such a pod must stay out of the counters, got worst %.1f", row.Worst)
	}
	if !row.HasCPU || !row.HasMem {
		t.Error("usage itself must still be shown")
	}
	if row.CPULimit != 0 || row.MemLimit != 0 {
		t.Errorf("an incomplete limit must not be reported: %.0f / %.0f", row.CPULimit, row.MemLimit)
	}
}

func TestLimitsOnEveryContainerAreSummed(t *testing.T) {
	pods := k8sfake.NewSimpleClientset(
		sidecarPod("limited-app", limited("200m", "256Mi"), limited("50m", "64Mi")),
	)
	metrics := metricsClient(twoContainerMetrics("limited-app", "100m", "128Mi", "25m", "32Mi"))

	client := NewWithClients(pods, metrics)
	row := poll(t, client)["limited-app"]

	if row.CPULimit != 250 || row.MemLimit != 320 {
		t.Fatalf("limits: %.0fm cpu, %.0fMi memory", row.CPULimit, row.MemLimit)
	}
	if row.CPUPct < 49.9 || row.CPUPct > 50.1 {
		t.Errorf("cpu%%: %.2f", row.CPUPct)
	}
	if row.MemPct < 49.9 || row.MemPct > 50.1 {
		t.Errorf("mem%%: %.2f", row.MemPct)
	}
}

func TestOneSidedLimitsAreIndependent(t *testing.T) {
	cpuOnly := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}
	pods := k8sfake.NewSimpleClientset(sidecarPod("cpu-only", cpuOnly, cpuOnly))
	metrics := metricsClient(twoContainerMetrics("cpu-only", "25m", "64Mi", "25m", "64Mi"))

	client := NewWithClients(pods, metrics)
	row := poll(t, client)["cpu-only"]

	if row.CPUPct < 24.9 || row.CPUPct > 25.1 {
		t.Errorf("cpu is limited everywhere, so it has a percentage: %.2f", row.CPUPct)
	}
	if row.MemPct != -1 {
		t.Errorf("memory has no limit anywhere, so it has none: %.2f", row.MemPct)
	}
}
