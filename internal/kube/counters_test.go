package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
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
