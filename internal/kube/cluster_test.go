package kube

import (
	"context"
	"strings"
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

func node(name string, cores int64, gigabytes int64, ready bool) *corev1.Node {
	status, reason := corev1.ConditionTrue, "KubeletReady"
	if !ready {
		status, reason = corev1.ConditionFalse, "KubeletNotReady"
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Labels:            map[string]string{"node-role.kubernetes.io/worker": ""},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-48 * time.Hour)),
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewQuantity(cores, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(gigabytes*1024*1024*1024, resource.BinarySI),
				corev1.ResourcePods:   *resource.NewQuantity(110, resource.DecimalSI),
			},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status, Reason: reason}},
			NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: "v1.30.2"},
		},
	}
}

func nodeMetrics(name string, milli int64, megabytes int64) metricsapi.NodeMetrics {
	return metricsapi.NodeMetrics{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Usage: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewMilliQuantity(milli, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(megabytes*1024*1024, resource.BinarySI),
		},
	}
}

func nodeMetricsClient(items ...metricsapi.NodeMetrics) *metricsfake.Clientset {
	cs := metricsfake.NewSimpleClientset()
	cs.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &metricsapi.NodeMetricsList{Items: items}, nil
	})
	return cs
}

func TestNodeRowsMeasureAgainstAllocatable(t *testing.T) {
	onNode := pod("api-1", corev1.PodRunning, nil, corev1.ContainerStatus{Name: "app"})
	onNode.Spec.NodeName = "worker-01"

	client := NewWithClients(
		k8sfake.NewSimpleClientset(node("worker-01", 8, 32, true), node("worker-02", 8, 32, false), onNode),
		nodeMetricsClient(nodeMetrics("worker-01", 6200, 26*1024)),
	)

	result, err := client.Rows(context.Background(), "production", KindNode)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("rows: %v", result.Rows)
	}

	first := result.Rows[0]
	if first.Name != "worker-01" || first.Status != "Ready" || first.Severity != Good {
		t.Errorf("a ready node: %+v", first)
	}
	if got := first.CPUPct; got < 77 || got > 78 {
		t.Errorf("cpu against allocatable: %.1f", got)
	}
	if got := first.MemPct; got < 81 || got > 82 {
		t.Errorf("memory against allocatable: %.1f", got)
	}
	if first.Ready != 1 || first.Desired != 110 {
		t.Errorf("pods on the node: %d of %d", first.Ready, first.Desired)
	}
	if !strings.Contains(first.Info, "worker") || !strings.Contains(first.Info, "v1.30.2") {
		t.Errorf("roles and version: %q", first.Info)
	}

	if second := result.Rows[1]; second.Severity != Bad || second.Status != "KubeletNotReady" {
		t.Errorf("a node that is not ready must be critical: %+v", second)
	}
}

func TestQuotaRowsMeasureUsedAgainstHard(t *testing.T) {
	quota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "team", Namespace: "production"},
		Status: corev1.ResourceQuotaStatus{
			Hard: corev1.ResourceList{
				corev1.ResourceLimitsCPU:    resource.MustParse("16"),
				corev1.ResourceLimitsMemory: resource.MustParse("32Gi"),
				corev1.ResourcePods:         resource.MustParse("40"),
			},
			Used: corev1.ResourceList{
				corev1.ResourceLimitsCPU:    resource.MustParse("15"),
				corev1.ResourceLimitsMemory: resource.MustParse("20Gi"),
				corev1.ResourcePods:         resource.MustParse("31"),
			},
		},
	}

	client := NewWithClients(k8sfake.NewSimpleClientset(quota), metricsClient())
	result, err := client.Rows(context.Background(), "production", KindResourceQuota)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}

	row := result.Rows[0]
	if got := row.CPUPct; got < 93 || got > 94 {
		t.Errorf("cpu against hard: %.1f", got)
	}
	if got := row.MemPct; got < 62 || got > 63 {
		t.Errorf("memory against hard: %.1f", got)
	}
	if row.Severity != Bad || row.Status != "at the limit" {
		t.Errorf("a quota at 94%% is critical: %q %v", row.Status, row.Severity)
	}
	if row.Ready != 31 || row.Desired != 40 {
		t.Errorf("pods of the quota: %d of %d", row.Ready, row.Desired)
	}
}

func TestNodeDetailReadsWithMeters(t *testing.T) {
	client := NewWithClients(
		k8sfake.NewSimpleClientset(node("worker-01", 8, 32, true)),
		nodeMetricsClient(nodeMetrics("worker-01", 6200, 26*1024)),
	)

	detail, err := client.ClusterObject(context.Background(), KindNode, "", "worker-01")
	if err != nil {
		t.Fatalf("ClusterObject: %v", err)
	}

	body := strings.Join(detail.Describe(time.Now()), "\n")
	for _, want := range []string{"NODE", "LOAD", "cpu", "memory", "pods", "CONDITIONS", "MACHINE", "v1.30.2"} {
		if !strings.Contains(body, want) {
			t.Errorf("the description misses %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "[") || !strings.Contains(body, "%") {
		t.Errorf("the load must read as meters:\n%s", body)
	}

	prose := strings.Join(detail.Textual(time.Now()), " ")
	if !strings.Contains(prose, "Node worker-01 is Ready") {
		t.Errorf("prose: %s", prose)
	}
	if manifest, err := detail.YAML(); err != nil || !strings.Contains(strings.Join(manifest, "\n"), "name: worker-01") {
		t.Errorf("yaml: %v %v", manifest, err)
	}
}

func TestLimitRangeRowsSayWhatTheyCap(t *testing.T) {
	item := &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{Name: "defaults", Namespace: "production"},
		Spec: corev1.LimitRangeSpec{Limits: []corev1.LimitRangeItem{{
			Type:    corev1.LimitTypeContainer,
			Default: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
		}}},
	}

	client := NewWithClients(k8sfake.NewSimpleClientset(item), metricsClient())
	result, err := client.Rows(context.Background(), "production", KindLimitRange)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}

	row := result.Rows[0]
	if row.Info != "Container" || row.Status != "capping 1 type" {
		t.Errorf("row: %+v", row)
	}

	detail, err := client.ClusterObject(context.Background(), KindLimitRange, "production", "defaults")
	if err != nil {
		t.Fatalf("ClusterObject: %v", err)
	}
	if body := strings.Join(detail.Describe(time.Now()), "\n"); !strings.Contains(body, "cpu 500m") {
		t.Errorf("the description misses the default:\n%s", body)
	}
}
