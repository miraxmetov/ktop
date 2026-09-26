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
	if got := byName["broken"]; got.Status != "CrashLoopBackOff" || got.Severity != Bad {
		t.Errorf("broken: got %q severity %v", got.Status, got.Severity)
	}
	if got := byName["half"]; got.Status != "NotReady 1/2" || got.Severity != Warn {
		t.Errorf("half: got %q severity %v", got.Status, got.Severity)
	}
	if got := byName["dying"]; got.Status != "Terminating" {
		t.Errorf("dying: got %q", got.Status)
	}
	if got := byName["job"]; got.Status != "Completed" {
		t.Errorf("job: got %q", got.Status)
	}
}

func TestRowsComeBackInAlphabeticalOrder(t *testing.T) {
	rows := testRows(t)

	for i := 1; i < len(rows); i++ {
		if rows[i-1].Name > rows[i].Name {
			t.Fatalf("%q must come before %q", rows[i].Name, rows[i-1].Name)
		}
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

func TestFilterLevel(t *testing.T) {
	rows := []Row{
		{Name: "hot", Worst: 95},
		{Name: "warm", Worst: 80},
		{Name: "edge-crit", Worst: 90},
		{Name: "edge-warn", Worst: 75},
		{Name: "calm", Worst: 10},
		{Name: "unknown", Worst: -1},
	}

	critical := FilterLevel(rows, LevelCritical, DimAll)
	if len(critical) != 2 || critical[0].Name != "hot" || critical[1].Name != "edge-crit" {
		t.Errorf("critical: %v", critical)
	}

	warning := FilterLevel(rows, LevelWarning, DimAll)
	if len(warning) != 2 || warning[0].Name != "warm" || warning[1].Name != "edge-warn" {
		t.Errorf("warning: %v", warning)
	}

	if all := FilterLevel(rows, LevelAll, DimAll); len(all) != len(rows) {
		t.Errorf("LevelAll must keep every row, got %d", len(all))
	}
}

func TestClassifyPerDimension(t *testing.T) {
	row := Row{Severity: Bad, CPUPct: 30, MemPct: 95, Worst: 95}

	if got := Classify(row, DimStatus); got != LevelCritical {
		t.Errorf("a bad status is critical, got %v", got)
	}
	if got := Classify(row, DimCPU); got != LevelAll {
		t.Errorf("30%% cpu is neither, got %v", got)
	}
	if got := Classify(row, DimMemory); got != LevelCritical {
		t.Errorf("95%% memory is critical, got %v", got)
	}

	warm := Row{Severity: Warn, CPUPct: 80, MemPct: 10, Worst: 80}
	if got := Classify(warm, DimStatus); got != LevelWarning {
		t.Errorf("a warning status, got %v", got)
	}
	if got := Classify(warm, DimCPU); got != LevelWarning {
		t.Errorf("80%% cpu is a warning, got %v", got)
	}
	if got := Classify(warm, DimMemory); got != LevelAll {
		t.Errorf("10%% memory is neither, got %v", got)
	}

	missing := Row{Severity: Good, CPUPct: -1, MemPct: -1, Worst: -1}
	for _, dimension := range []Dimension{DimAll, DimStatus, DimCPU, DimMemory} {
		if got := Classify(missing, dimension); got != LevelAll {
			t.Errorf("a pod without metrics must stay out of the counters, got %v", got)
		}
	}
}

func TestCountAndFilterFollowTheDimension(t *testing.T) {
	rows := []Row{
		{Name: "crashing", Severity: Bad, CPUPct: 5, MemPct: 5, Worst: 5},
		{Name: "cpu-hot", Severity: Good, CPUPct: 95, MemPct: 5, Worst: 95},
		{Name: "mem-warm", Severity: Good, CPUPct: 5, MemPct: 80, Worst: 80},
		{Name: "pending", Severity: Warn, CPUPct: -1, MemPct: -1, Worst: -1},
	}

	if crit, warn := Count(rows, DimStatus); crit != 1 || warn != 1 {
		t.Errorf("by status: %d critical, %d warning", crit, warn)
	}
	if crit, warn := Count(rows, DimCPU); crit != 1 || warn != 0 {
		t.Errorf("by cpu: %d critical, %d warning", crit, warn)
	}
	if crit, warn := Count(rows, DimMemory); crit != 0 || warn != 1 {
		t.Errorf("by memory: %d critical, %d warning", crit, warn)
	}

	got := FilterLevel(rows, LevelCritical, DimStatus)
	if len(got) != 1 || got[0].Name != "crashing" {
		t.Errorf("status filter: %v", got)
	}
	got = FilterLevel(rows, LevelWarning, DimMemory)
	if len(got) != 1 || got[0].Name != "mem-warm" {
		t.Errorf("memory filter: %v", got)
	}
}

func TestSortKeepsNameGroupsTogether(t *testing.T) {
	rows := []Row{
		{Name: "web-7d4f8c6b9-p8lqm", CPUPct: 96, Worst: 96},
		{Name: "api-worker-5f7c9d8b4-xk2vn", CPUPct: 10, Worst: 10},
		{Name: "api-gateway-6b8d7c9f5-2mkqp", CPUPct: 91, Worst: 91},
		{Name: "api-worker-5f7c9d8b4-aaaaa", CPUPct: 80, Worst: 80},
	}
	Sort(rows)

	want := "api-gateway-6b8d7c9f5-2mkqp api-worker-5f7c9d8b4-aaaaa api-worker-5f7c9d8b4-xk2vn web-7d4f8c6b9-p8lqm"
	if names(rows) != want {
		t.Fatalf("pods must group by name:\n got %s\nwant %s", names(rows), want)
	}

	rows[0].CPUPct, rows[0].Worst = 99, 99
	rows[3].CPUPct, rows[3].Worst = 1, 1
	Sort(rows)
	if names(rows) != want {
		t.Errorf("load must not pull a pod out of its group: %s", names(rows))
	}
}

func TestSortByColumn(t *testing.T) {
	now := time.Now()
	rows := []Row{
		{Name: "b", CPU: 300, HasCPU: true, Restarts: 1, Severity: Good, LastRestart: now.Add(-time.Hour)},
		{Name: "a", CPU: 100, HasCPU: true, Restarts: 9, Severity: Bad},
		{Name: "c", CPU: 200, HasCPU: true, Restarts: 5, Severity: Warn, LastRestart: now},
	}

	SortBy(rows, "cpu", OrderDesc, OrderAsc)
	if names(rows) != "b c a" {
		t.Errorf("cpu descending: %s", names(rows))
	}

	SortBy(rows, "cpu", OrderAsc, OrderAsc)
	if names(rows) != "a c b" {
		t.Errorf("cpu ascending: %s", names(rows))
	}

	SortBy(rows, "restarts", OrderDesc, OrderAsc)
	if names(rows) != "a c b" {
		t.Errorf("restarts descending: %s", names(rows))
	}

	SortBy(rows, "status", OrderDesc, OrderAsc)
	if names(rows) != "a c b" {
		t.Errorf("worst status first: %s", names(rows))
	}

	SortBy(rows, "last_restart", OrderDesc, OrderAsc)
	if names(rows) != "c b a" {
		t.Errorf("newest restart first: %s", names(rows))
	}

	rows[0].CPU, rows[1].CPU, rows[2].CPU = 7, 7, 7
	SortBy(rows, "cpu", OrderDesc, OrderAsc)
	if names(rows) != "a b c" {
		t.Errorf("equal values fall back to the name: %s", names(rows))
	}
}

func TestSortByNoneFallsBackToNames(t *testing.T) {
	rows := []Row{
		{Name: "web-2", Worst: 5},
		{Name: "api-9", Worst: 95},
		{Name: "api-1", Worst: 80},
	}

	SortBy(rows, "cpu", OrderNone, OrderAsc)
	if names(rows) != "api-1 api-9 web-2" {
		t.Errorf("without a chosen column the table is alphabetical: %s", names(rows))
	}
}

func names(rows []Row) string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return strings.Join(out, " ")
}

func TestNameOrderIsIndependentOfTheColumnSort(t *testing.T) {
	fresh := func() []Row {
		return []Row{
			{Name: "api-1", CPU: 100, HasCPU: true},
			{Name: "web-1", CPU: 300, HasCPU: true},
			{Name: "api-2", CPU: 100, HasCPU: true},
		}
	}

	rows := fresh()
	SortBy(rows, "", OrderNone, OrderAsc)
	if names(rows) != "api-1 api-2 web-1" {
		t.Errorf("alphabetical by default: %s", names(rows))
	}

	rows = fresh()
	SortBy(rows, "", OrderNone, OrderDesc)
	if names(rows) != "web-1 api-2 api-1" {
		t.Errorf("against the alphabet: %s", names(rows))
	}

	rows = fresh()
	SortBy(rows, "cpu", OrderDesc, OrderAsc)
	if names(rows) != "web-1 api-1 api-2" {
		t.Errorf("cpu first, then names: %s", names(rows))
	}

	rows = fresh()
	SortBy(rows, "cpu", OrderDesc, OrderDesc)
	if names(rows) != "web-1 api-2 api-1" {
		t.Errorf("the name order only breaks ties: %s", names(rows))
	}
}

func TestFilterDoesNotReorderTheSource(t *testing.T) {
	source := []Row{{Name: "web-1"}, {Name: "api-1"}}

	filtered := Filter(source, "")
	SortBy(filtered, "", OrderNone, OrderAsc)

	if source[0].Name != "web-1" {
		t.Errorf("sorting the view must leave the source alone: %v", names(source))
	}
	if names(filtered) != "api-1 web-1" {
		t.Errorf("the view itself is sorted: %v", names(filtered))
	}
}
