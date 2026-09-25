package kube

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

type Severity int

const (
	Good Severity = iota
	Warn
	Bad
	Muted
)

const (
	WarnPct = 75.0
	CritPct = 90.0
)

type Row struct {
	Name        string
	Status      string
	Severity    Severity
	CPU         float64
	CPULimit    float64
	Mem         float64
	MemLimit    float64
	HasCPU      bool
	HasMem      bool
	Restarts    int
	NewRestarts int
	OOMs        int
	ExitCode    *int32
	ExitReason  string
	LastRestart time.Time
	CPUPct      float64
	MemPct      float64
	Worst       float64
	Problem     bool
}

type Client struct {
	pods       kubernetes.Interface
	metrics    metricsv.Interface
	counts     *counters
	Context    string
	Host       string
	Kubeconfig string
}

type Result struct {
	Rows []Row
	Note string
}

var badWaiting = map[string]bool{
	"CrashLoopBackOff":           true,
	"ImagePullBackOff":           true,
	"ErrImagePull":               true,
	"CreateContainerError":       true,
	"CreateContainerConfigError": true,
	"InvalidImageName":           true,
}

func New(contextName string) (*Client, string, error) {
	return NewWithPath("", contextName)
}

func NewWithPath(path, contextName string) (*Client, string, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			return nil, "default", &ConfigError{
				Message: "cannot read " + path,
				Hint:    "check the path, or pick another file from the list",
			}
		}
		rules.ExplicitPath = path
	}
	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)

	namespace, _, err := cc.Namespace()
	if err != nil || namespace == "" {
		namespace = "default"
	}

	cfg, err := cc.ClientConfig()
	if err != nil {
		return nil, namespace, ExplainConfig(err)
	}
	cfg.UserAgent = "ktop"
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, namespace, ExplainConfig(err)
	}
	ms, err := metricsv.NewForConfig(cfg)
	if err != nil {
		return nil, namespace, ExplainConfig(err)
	}

	client := &Client{
		pods:       cs,
		metrics:    ms,
		counts:     newCounters(),
		Host:       cfg.Host,
		Kubeconfig: kubeconfigPath(rules),
	}
	if raw, err := cc.RawConfig(); err == nil {
		client.Context = raw.CurrentContext
		if contextName != "" {
			client.Context = contextName
		}
	}
	return client, namespace, nil
}

func kubeconfigPath(rules *clientcmd.ClientConfigLoadingRules) string {
	if rules.ExplicitPath != "" {
		return rules.ExplicitPath
	}
	for _, candidate := range rules.Precedence {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func NewWithClients(pods kubernetes.Interface, metrics metricsv.Interface) *Client {
	return &Client{pods: pods, metrics: metrics, counts: newCounters()}
}

type usage struct {
	cpu float64
	mem float64
}

func (c *Client) Namespaces(ctx context.Context) ([]string, error) {
	list, err := c.pods.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list.Items))
	for _, ns := range list.Items {
		names = append(names, ns.Name)
	}
	sort.Strings(names)
	return names, nil
}

func (c *Client) Rows(ctx context.Context, namespace string) (Result, error) {
	var result Result

	pods, err := c.pods.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, errors.New(ExplainPods(err, namespace, c.Host))
	}

	used := map[string]usage{}
	if c.metrics != nil {
		list, err := c.metrics.MetricsV1beta1().PodMetricses(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			result.Note = ExplainMetrics(err, namespace)
		} else {
			for _, m := range list.Items {
				var u usage
				for _, container := range m.Containers {
					cpu := container.Usage.Cpu()
					mem := container.Usage.Memory()
					u.cpu += float64(cpu.MilliValue())
					u.mem += float64(mem.Value()) / (1024 * 1024)
				}
				used[m.Name] = u
			}
		}
	}

	observed := c.counts.observe(namespace, pods.Items)

	rows := make([]Row, 0, len(pods.Items))
	for i := range pods.Items {
		pod := &pods.Items[i]
		rows = append(rows, buildRow(pod, used, observed[identity(pod)]))
	}
	Sort(rows)
	result.Rows = rows
	return result, nil
}

type Level int

const (
	LevelAll Level = iota
	LevelWarning
	LevelCritical
)

type Dimension int

const (
	DimAll Dimension = iota
	DimStatus
	DimCPU
	DimMemory
)

func Classify(r Row, dimension Dimension) Level {
	switch dimension {
	case DimStatus:
		switch r.Severity {
		case Bad:
			return LevelCritical
		case Warn:
			return LevelWarning
		}
		return LevelAll
	case DimCPU:
		return pctLevel(r.CPUPct)
	case DimMemory:
		return pctLevel(r.MemPct)
	}
	return pctLevel(r.Worst)
}

func pctLevel(pct float64) Level {
	switch {
	case pct >= CritPct:
		return LevelCritical
	case pct >= WarnPct:
		return LevelWarning
	}
	return LevelAll
}

func Count(rows []Row, dimension Dimension) (int, int) {
	crit, warn := 0, 0
	for _, r := range rows {
		switch Classify(r, dimension) {
		case LevelCritical:
			crit++
		case LevelWarning:
			warn++
		}
	}
	return crit, warn
}

func FilterLevel(rows []Row, level Level, dimension Dimension) []Row {
	if level == LevelAll {
		return rows
	}
	filtered := make([]Row, 0, len(rows))
	for _, r := range rows {
		if Classify(r, dimension) == level {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func Filter(rows []Row, query string) []Row {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return rows
	}
	filtered := make([]Row, 0, len(rows))
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.Name), query) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func MatchNamespaces(names []string, query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return names
	}
	matches := make([]string, 0, len(names))
	for _, name := range names {
		if strings.Contains(strings.ToLower(name), query) {
			matches = append(matches, name)
		}
	}
	return matches
}

func buildRow(pod *corev1.Pod, used map[string]usage, seen podCounter) Row {
	row := Row{Name: pod.Name, CPUPct: -1, MemPct: -1, Worst: -1}
	row.Status, row.Severity = podStatus(pod)

	cpuLimited, memLimited := len(pod.Spec.Containers) > 0, len(pod.Spec.Containers) > 0
	for i := range pod.Spec.Containers {
		limits := pod.Spec.Containers[i].Resources.Limits
		if cpu, ok := limits[corev1.ResourceCPU]; ok {
			row.CPULimit += float64(cpu.MilliValue())
		} else {
			cpuLimited = false
		}
		if mem, ok := limits[corev1.ResourceMemory]; ok {
			row.MemLimit += float64(mem.Value()) / (1024 * 1024)
		} else {
			memLimited = false
		}
	}
	if !cpuLimited {
		row.CPULimit = 0
	}
	if !memLimited {
		row.MemLimit = 0
	}

	row.NewRestarts = seen.restarts
	row.OOMs = seen.ooms

	for _, cs := range pod.Status.ContainerStatuses {
		row.Restarts += int(cs.RestartCount)
		term := cs.LastTerminationState.Terminated
		if term == nil {
			term = cs.State.Terminated
		}
		if term == nil {
			continue
		}
		if !term.FinishedAt.IsZero() && term.FinishedAt.Time.After(row.LastRestart) {
			row.LastRestart = term.FinishedAt.Time
			code := term.ExitCode
			row.ExitCode = &code
			row.ExitReason = term.Reason
		}
	}

	if u, ok := used[pod.Name]; ok {
		row.CPU, row.Mem = u.cpu, u.mem
		row.HasCPU, row.HasMem = true, true
		if row.CPULimit > 0 {
			row.CPUPct = 100 * row.CPU / row.CPULimit
		}
		if row.MemLimit > 0 {
			row.MemPct = 100 * row.Mem / row.MemLimit
		}
	}

	row.Worst = -1
	if row.CPUPct > row.Worst {
		row.Worst = row.CPUPct
	}
	if row.MemPct > row.Worst {
		row.Worst = row.MemPct
	}
	row.Problem = row.Restarts > 0 || row.OOMs > 0 ||
		row.Severity == Bad || row.Severity == Warn || row.Worst >= WarnPct
	return row
}

func podStatus(pod *corev1.Pod) (string, Severity) {
	if pod.DeletionTimestamp != nil {
		return "Terminating", Warn
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil && w.Reason != "" {
			if badWaiting[w.Reason] {
				return w.Reason, Bad
			}
			return w.Reason, Warn
		}
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if t := cs.State.Terminated; t != nil && t.Reason != "" && t.Reason != "Completed" {
			return t.Reason, Bad
		}
	}
	switch pod.Status.Phase {
	case corev1.PodRunning:
		ready := 0
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Ready {
				ready++
			}
		}
		total := len(pod.Status.ContainerStatuses)
		if total > 0 && ready < total {
			return fmt.Sprintf("NotReady %d/%d", ready, total), Warn
		}
		return "Running", Good
	case corev1.PodPending, corev1.PodUnknown:
		return string(pod.Status.Phase), Warn
	case corev1.PodSucceeded:
		return "Completed", Muted
	}
	return string(pod.Status.Phase), Bad
}

func Sort(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Problem != b.Problem {
			return a.Problem
		}
		if rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		return a.Name < b.Name
	})
}

func rank(r Row) int {
	switch Classify(r, DimAll) {
	case LevelCritical:
		return 0
	case LevelWarning:
		return 1
	}
	return 2
}
