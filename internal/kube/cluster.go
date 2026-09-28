package kube

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *Client) nodeRows(ctx context.Context) (Result, error) {
	var result Result

	list, err := c.pods.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, errors.New(ExplainCluster(err, KindNode, c.Host))
	}

	used := map[string]usage{}
	if c.metrics != nil {
		metrics, err := c.metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
		if err != nil {
			result.Note = ExplainMetrics(err, "")
		} else {
			for _, m := range metrics.Items {
				used[m.Name] = usage{
					cpu: float64(m.Usage.Cpu().MilliValue()),
					mem: float64(m.Usage.Memory().Value()) / (1024 * 1024),
				}
			}
		}
	}

	hosted := map[string]int{}
	if pods, err := c.pods.CoreV1().Pods("").List(ctx, metav1.ListOptions{}); err == nil {
		for i := range pods.Items {
			pod := &pods.Items[i]
			if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
				continue
			}
			hosted[pod.Spec.NodeName]++
		}
	}

	rows := make([]Row, 0, len(list.Items))
	for i := range list.Items {
		rows = append(rows, nodeRow(&list.Items[i], used[list.Items[i].Name], hosted[list.Items[i].Name]))
	}

	Sort(rows)
	result.Rows = rows
	return result, nil
}

func nodeRow(node *corev1.Node, used usage, pods int) Row {
	row := Row{
		Name:    node.Name,
		Info:    nodeInfo(node),
		Created: node.CreationTimestamp.Time,
		Ready:   pods,
		Desired: int(node.Status.Allocatable.Pods().Value()),
		CPUPct:  -1,
		MemPct:  -1,
		Worst:   -1,
	}

	row.CPULimit = float64(node.Status.Allocatable.Cpu().MilliValue())
	row.MemLimit = float64(node.Status.Allocatable.Memory().Value()) / (1024 * 1024)
	if used.cpu > 0 || used.mem > 0 {
		row.HasCPU, row.HasMem = true, true
		row.CPU, row.Mem = used.cpu, used.mem
	}
	if row.HasCPU && row.CPULimit > 0 {
		row.CPUPct = 100 * row.CPU / row.CPULimit
	}
	if row.HasMem && row.MemLimit > 0 {
		row.MemPct = 100 * row.Mem / row.MemLimit
	}
	row.Worst = max(row.CPUPct, row.MemPct)
	row.Status, row.Severity = nodeStatus(node)
	return row
}

func nodeStatus(node *corev1.Node) (string, Severity) {
	ready, reason := conditionOf(node, corev1.NodeReady)
	pressure := ""

	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady || condition.Status != corev1.ConditionTrue {
			continue
		}
		pressure = string(condition.Type)
	}

	switch {
	case ready == corev1.ConditionFalse:
		return or(reason, "NotReady"), Bad
	case ready != corev1.ConditionTrue:
		return "Unknown", Bad
	case node.Spec.Unschedulable:
		return "Ready,SchedulingDisabled", Warn
	case pressure != "":
		return pressure, Warn
	}
	return "Ready", Good
}

func conditionOf(node *corev1.Node, want corev1.NodeConditionType) (corev1.ConditionStatus, string) {
	for _, condition := range node.Status.Conditions {
		if condition.Type == want {
			return condition.Status, condition.Reason
		}
	}
	return corev1.ConditionUnknown, ""
}

func nodeInfo(node *corev1.Node) string {
	roles := make([]string, 0, 2)
	for label := range node.Labels {
		if role := strings.TrimPrefix(label, "node-role.kubernetes.io/"); role != label && role != "" {
			roles = append(roles, role)
		}
	}
	sort.Strings(roles)

	if len(roles) == 0 {
		return node.Status.NodeInfo.KubeletVersion
	}
	return strings.Join(roles, ",") + " " + node.Status.NodeInfo.KubeletVersion
}

func (c *Client) namespaceRows(ctx context.Context) (Result, error) {
	var result Result

	list, err := c.pods.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, errors.New(ExplainNamespaces(err))
	}

	used := map[string]usage{}
	if c.metrics != nil {
		metrics, err := c.metrics.MetricsV1beta1().PodMetricses("").List(ctx, metav1.ListOptions{})
		if err != nil {
			result.Note = ExplainMetrics(err, "")
		} else {
			for _, m := range metrics.Items {
				u := used[m.Namespace]
				for _, container := range m.Containers {
					u.cpu += float64(container.Usage.Cpu().MilliValue())
					u.mem += float64(container.Usage.Memory().Value()) / (1024 * 1024)
				}
				used[m.Namespace] = u
			}
		}
	}

	live, ready := map[string]int{}, map[string]int{}
	if pods, err := c.pods.CoreV1().Pods("").List(ctx, metav1.ListOptions{}); err == nil {
		for i := range pods.Items {
			pod := &pods.Items[i]
			live[pod.Namespace]++
			if podReady(pod) {
				ready[pod.Namespace]++
			}
		}
	}

	rows := make([]Row, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		row := Row{
			Name:    item.Name,
			Created: item.CreationTimestamp.Time,
			Ready:   ready[item.Name],
			Desired: live[item.Name],
			CPUPct:  -1,
			MemPct:  -1,
			Worst:   -1,
			Status:  string(item.Status.Phase),
		}
		if u, ok := used[item.Name]; ok {
			row.HasCPU, row.HasMem = true, true
			row.CPU, row.Mem = u.cpu, u.mem
		}

		row.Severity = Good
		switch {
		case item.Status.Phase == corev1.NamespaceTerminating:
			row.Severity = Warn
		case live[item.Name] == 0:
			row.Severity = Muted
			row.Status = "empty"
		case ready[item.Name] < live[item.Name]:
			row.Severity = Warn
			row.Status = fmt.Sprintf("%d/%d ready", ready[item.Name], live[item.Name])
		}
		rows = append(rows, row)
	}

	Sort(rows)
	result.Rows = rows
	return result, nil
}

func podReady(pod *corev1.Pod) bool {
	if pod.Status.Phase == corev1.PodSucceeded {
		return true
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func (c *Client) quotaRows(ctx context.Context, namespace string) (Result, error) {
	var result Result

	list, err := c.pods.CoreV1().ResourceQuotas(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, errors.New(ExplainCluster(err, KindResourceQuota, c.Host))
	}

	rows := make([]Row, 0, len(list.Items))
	for i := range list.Items {
		rows = append(rows, quotaRow(&list.Items[i]))
	}

	Sort(rows)
	result.Rows = rows
	return result, nil
}

func quotaRow(quota *corev1.ResourceQuota) Row {
	row := Row{
		Name:    quota.Name,
		Created: quota.CreationTimestamp.Time,
		CPUPct:  -1,
		MemPct:  -1,
		Worst:   -1,
	}

	row.CPU, row.CPULimit, row.HasCPU = quotaPair(quota, corev1.ResourceLimitsCPU, corev1.ResourceRequestsCPU)
	row.Mem, row.MemLimit, row.HasMem = quotaPair(quota, corev1.ResourceLimitsMemory, corev1.ResourceRequestsMemory)
	if row.HasCPU && row.CPULimit > 0 {
		row.CPUPct = 100 * row.CPU / row.CPULimit
	}
	if row.HasMem && row.MemLimit > 0 {
		row.MemPct = 100 * row.Mem / row.MemLimit
	}
	row.Worst = max(row.CPUPct, row.MemPct)

	if pods, ok := quota.Status.Hard[corev1.ResourcePods]; ok {
		row.Desired = int(pods.Value())
		row.Ready = int(quota.Status.Used.Pods().Value())
	}
	row.Info = quotaScopes(quota)

	row.Status, row.Severity = "within the quota", Good
	switch {
	case len(quota.Status.Hard) == 0:
		row.Status, row.Severity = "nothing capped", Muted
	case row.Worst >= CritPct:
		row.Status, row.Severity = "at the limit", Bad
	case row.Worst >= WarnPct:
		row.Status, row.Severity = "close to the limit", Warn
	}
	return row
}

func quotaPair(quota *corev1.ResourceQuota, names ...corev1.ResourceName) (float64, float64, bool) {
	for _, name := range names {
		hard, ok := quota.Status.Hard[name]
		if !ok {
			continue
		}
		used := quota.Status.Used[name]
		if strings.HasSuffix(string(name), "cpu") {
			return float64(used.MilliValue()), float64(hard.MilliValue()), true
		}
		return megabytes(used), megabytes(hard), true
	}
	return 0, 0, false
}

func megabytes(value resource.Quantity) float64 {
	return float64(value.Value()) / (1024 * 1024)
}

func quotaScopes(quota *corev1.ResourceQuota) string {
	scopes := make([]string, 0, len(quota.Spec.Scopes))
	for _, scope := range quota.Spec.Scopes {
		scopes = append(scopes, string(scope))
	}
	if len(scopes) == 0 {
		return "every pod"
	}
	return strings.Join(scopes, ",")
}

func (c *Client) limitRangeRows(ctx context.Context, namespace string) (Result, error) {
	var result Result

	list, err := c.pods.CoreV1().LimitRanges(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, errors.New(ExplainCluster(err, KindLimitRange, c.Host))
	}

	rows := make([]Row, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		row := Row{
			Name:     item.Name,
			Created:  item.CreationTimestamp.Time,
			CPUPct:   -1,
			MemPct:   -1,
			Worst:    -1,
			Desired:  len(item.Spec.Limits),
			Info:     limitTypes(item),
			Status:   "capping " + plural(len(item.Spec.Limits), "type"),
			Severity: Good,
		}
		if len(item.Spec.Limits) == 0 {
			row.Status, row.Severity = "nothing capped", Muted
		}
		rows = append(rows, row)
	}

	Sort(rows)
	result.Rows = rows
	return result, nil
}

func limitTypes(item *corev1.LimitRange) string {
	types := make([]string, 0, len(item.Spec.Limits))
	for _, limit := range item.Spec.Limits {
		types = append(types, string(limit.Type))
	}
	return strings.Join(types, ",")
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
