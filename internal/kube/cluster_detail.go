package kube

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

type ClusterDetail struct {
	Kind   Kind
	Meta   metav1.ObjectMeta
	object runtime.Object
	body   func(now time.Time) []string
	prose  func(now time.Time) []string
}

func (c *Client) ClusterObject(ctx context.Context, kind Kind, namespace, name string) (*ClusterDetail, error) {
	core := c.pods.CoreV1()
	options := metav1.GetOptions{}

	switch kind {
	case KindNode:
		item, err := core.Nodes().Get(ctx, name, options)
		if err != nil {
			return nil, fmt.Errorf("%s", ExplainWorkload(err, "", kind, name, c.Host))
		}
		used, pods := c.nodeUsage(ctx, name)
		detail := &ClusterDetail{Kind: kind, Meta: item.ObjectMeta, object: item}
		detail.body = func(now time.Time) []string { return describeNode(item, used, pods, now) }
		detail.prose = func(now time.Time) []string { return tellNode(item, used, pods, now) }
		return detail, nil

	case KindNamespace:
		item, err := core.Namespaces().Get(ctx, name, options)
		if err != nil {
			return nil, fmt.Errorf("%s", ExplainWorkload(err, "", kind, name, c.Host))
		}
		counts := c.podPhases(ctx, name)
		detail := &ClusterDetail{Kind: kind, Meta: item.ObjectMeta, object: item}
		detail.body = func(now time.Time) []string { return describeNamespace(item, counts, now) }
		detail.prose = func(now time.Time) []string { return tellNamespace(item, counts, now) }
		return detail, nil

	case KindResourceQuota:
		item, err := core.ResourceQuotas(namespace).Get(ctx, name, options)
		if err != nil {
			return nil, fmt.Errorf("%s", ExplainWorkload(err, namespace, kind, name, c.Host))
		}
		detail := &ClusterDetail{Kind: kind, Meta: item.ObjectMeta, object: item}
		detail.body = func(now time.Time) []string { return describeQuota(item, now) }
		detail.prose = func(now time.Time) []string { return tellQuota(item, now) }
		return detail, nil

	case KindLimitRange:
		item, err := core.LimitRanges(namespace).Get(ctx, name, options)
		if err != nil {
			return nil, fmt.Errorf("%s", ExplainWorkload(err, namespace, kind, name, c.Host))
		}
		detail := &ClusterDetail{Kind: kind, Meta: item.ObjectMeta, object: item}
		detail.body = func(now time.Time) []string { return describeLimitRange(item, now) }
		detail.prose = func(now time.Time) []string { return tellLimitRange(item, now) }
		return detail, nil
	}
	return nil, fmt.Errorf("%s cannot be inspected", kind)
}

func (c *Client) nodeUsage(ctx context.Context, name string) (usage, int) {
	var used usage
	if c.metrics != nil {
		if m, err := c.metrics.MetricsV1beta1().NodeMetricses().Get(ctx, name, metav1.GetOptions{}); err == nil {
			used.cpu = float64(m.Usage.Cpu().MilliValue())
			used.mem = float64(m.Usage.Memory().Value()) / (1024 * 1024)
		}
	}

	pods := 0
	if list, err := c.pods.CoreV1().Pods("").List(ctx, metav1.ListOptions{}); err == nil {
		for i := range list.Items {
			item := &list.Items[i]
			if item.Spec.NodeName == name && item.Status.Phase != corev1.PodSucceeded {
				pods++
			}
		}
	}
	return used, pods
}

func (c *Client) podPhases(ctx context.Context, namespace string) map[string]int {
	counts := map[string]int{}
	list, err := c.pods.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return counts
	}
	for i := range list.Items {
		counts[string(list.Items[i].Status.Phase)]++
		counts["all"]++
	}
	return counts
}

func (d *ClusterDetail) YAML() ([]string, error) {
	clean := d.object.DeepCopyObject()
	if meta, ok := clean.(interface {
		SetManagedFields([]metav1.ManagedFieldsEntry)
		GetAnnotations() map[string]string
		SetAnnotations(map[string]string)
	}); ok {
		meta.SetManagedFields(nil)
		annotations := meta.GetAnnotations()
		delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
		if len(annotations) == 0 {
			annotations = nil
		}
		meta.SetAnnotations(annotations)
	}

	body, err := yaml.Marshal(clean)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(body), "\n"), "\n"), nil
}

func (d *ClusterDetail) Describe(now time.Time) []string {
	out := d.body(now)
	if len(d.Meta.Labels) > 0 {
		out = append(out, "", "LABELS")
		for _, key := range sortedKeys(d.Meta.Labels) {
			out = append(out, field(key, d.Meta.Labels[key]))
		}
	}
	return out
}

func (d *ClusterDetail) Textual(now time.Time) []string {
	return d.prose(now)
}

func describeNode(node *corev1.Node, used usage, pods int, now time.Time) []string {
	status, _ := nodeStatus(node)
	cpu := float64(node.Status.Allocatable.Cpu().MilliValue())
	mem := float64(node.Status.Allocatable.Memory().Value()) / (1024 * 1024)
	capacity := int(node.Status.Allocatable.Pods().Value())

	out := []string{
		"NODE",
		field("name", node.Name),
		field("status", status),
		field("roles", or(nodeRoles(node), "none")),
		field("created", timestamp(node.CreationTimestamp.Time, now)),
		"",
		"LOAD",
		field("cpu", meterLine(used.cpu, cpu, fmt.Sprintf("%.2f of %.0f cores", used.cpu/1000, cpu/1000))),
		field("memory", meterLine(used.mem, mem, fmt.Sprintf("%.1f of %.1f GiB", used.mem/1024, mem/1024))),
		field("pods", meterLine(float64(pods), float64(capacity), fmt.Sprintf("%d of %d", pods, capacity))),
		"",
		"CONDITIONS",
	}

	for _, condition := range node.Status.Conditions {
		text := string(condition.Status)
		if condition.Reason != "" {
			text += " (" + condition.Reason + ")"
		}
		out = append(out, field(string(condition.Type), text))
	}

	out = append(out, "", "MACHINE",
		field("kubelet", node.Status.NodeInfo.KubeletVersion),
		field("runtime", node.Status.NodeInfo.ContainerRuntimeVersion),
		field("os", node.Status.NodeInfo.OSImage),
		field("kernel", node.Status.NodeInfo.KernelVersion),
		field("architecture", node.Status.NodeInfo.Architecture),
	)

	if len(node.Status.Addresses) > 0 {
		out = append(out, "", "ADDRESSES")
		for _, address := range node.Status.Addresses {
			out = append(out, field(string(address.Type), address.Address))
		}
	}
	if len(node.Spec.Taints) > 0 {
		out = append(out, "", "TAINTS")
		for _, taint := range node.Spec.Taints {
			out = append(out, field(taint.Key, or(taint.Value, "-")+" ("+string(taint.Effect)+")"))
		}
	}
	return out
}

func tellNode(node *corev1.Node, used usage, pods int, now time.Time) []string {
	status, _ := nodeStatus(node)
	cpu := float64(node.Status.Allocatable.Cpu().MilliValue())
	mem := float64(node.Status.Allocatable.Memory().Value()) / (1024 * 1024)
	capacity := int(node.Status.Allocatable.Pods().Value())

	out := []string{
		fmt.Sprintf("Node %s is %s, joined the cluster %s ago and runs %s.",
			node.Name, status, ago(now.Sub(node.CreationTimestamp.Time)), node.Status.NodeInfo.KubeletVersion),
	}

	if used.cpu > 0 && cpu > 0 {
		out = append(out, fmt.Sprintf("It is using %.2f of its %.0f allocatable cores (%.0f%%) and %.1f of %.1f GiB (%.0f%%).",
			used.cpu/1000, cpu/1000, 100*used.cpu/cpu, used.mem/1024, mem/1024, 100*used.mem/mem))
	} else {
		out = append(out, "Its load is unknown, metrics-server did not answer for this node.")
	}

	out = append(out, fmt.Sprintf("It hosts %d of the %d pods it has room for.", pods, capacity))
	if node.Spec.Unschedulable {
		out = append(out, "Worth a look: it is cordoned, so the scheduler places nothing new on it.")
	}
	for _, condition := range node.Status.Conditions {
		if condition.Type != corev1.NodeReady && condition.Status == corev1.ConditionTrue {
			out = append(out, fmt.Sprintf("Worth a look: %s is true (%s).", condition.Type, condition.Message))
		}
	}
	return out
}

func describeNamespace(ns *corev1.Namespace, counts map[string]int, now time.Time) []string {
	out := []string{
		"NAMESPACE",
		field("name", ns.Name),
		field("status", string(ns.Status.Phase)),
		field("created", timestamp(ns.CreationTimestamp.Time, now)),
		field("pods", fmt.Sprintf("%d", counts["all"])),
		"",
		"PODS BY PHASE",
	}

	phases := make([]string, 0, len(counts))
	for phase := range counts {
		if phase != "all" {
			phases = append(phases, phase)
		}
	}
	sort.Strings(phases)

	if len(phases) == 0 {
		out = append(out, field("none", "the namespace is empty"))
	}
	for _, phase := range phases {
		out = append(out, field(phase, fmt.Sprintf("%d", counts[phase])))
	}
	return out
}

func tellNamespace(ns *corev1.Namespace, counts map[string]int, now time.Time) []string {
	out := []string{
		fmt.Sprintf("Namespace %s is %s and was created %s ago.",
			ns.Name, strings.ToLower(string(ns.Status.Phase)), ago(now.Sub(ns.CreationTimestamp.Time))),
	}
	if counts["all"] == 0 {
		return append(out, "It holds no pods at the moment.")
	}
	out = append(out, fmt.Sprintf("It holds %d pods, %d of them running.", counts["all"], counts["Running"]))
	if failed := counts["Failed"]; failed > 0 {
		out = append(out, fmt.Sprintf("Worth a look: %d of them failed.", failed))
	}
	return out
}

func describeQuota(quota *corev1.ResourceQuota, now time.Time) []string {
	out := []string{
		"RESOURCEQUOTA",
		field("name", quota.Name),
		field("namespace", quota.Namespace),
		field("created", timestamp(quota.CreationTimestamp.Time, now)),
		field("scope", quotaScopes(quota)),
		"",
		"USED OF HARD",
	}

	names := make([]string, 0, len(quota.Status.Hard))
	for name := range quota.Status.Hard {
		names = append(names, string(name))
	}
	sort.Strings(names)

	if len(names) == 0 {
		out = append(out, field("none", "this quota caps nothing"))
	}
	for _, name := range names {
		hard := quota.Status.Hard[corev1.ResourceName(name)]
		used := quota.Status.Used[corev1.ResourceName(name)]
		out = append(out, field(name, meterLine(quantity(used), quantity(hard),
			fmt.Sprintf("%s of %s", used.String(), hard.String()))))
	}
	return out
}

func tellQuota(quota *corev1.ResourceQuota, now time.Time) []string {
	row := quotaRow(quota)
	out := []string{
		fmt.Sprintf("ResourceQuota %s caps %s in namespace %s and was created %s ago.",
			quota.Name, plural(len(quota.Status.Hard), "resource"), quota.Namespace,
			ago(now.Sub(quota.CreationTimestamp.Time))),
	}

	if row.Worst < 0 {
		return append(out, "It tracks nothing that ktop measures in percent.")
	}
	out = append(out, fmt.Sprintf("Its tightest resource sits at %.0f%% of what it allows.", row.Worst))
	if row.Worst >= WarnPct {
		out = append(out, "Worth a look: new pods in this namespace may be refused soon.")
	}
	return out
}

func describeLimitRange(item *corev1.LimitRange, now time.Time) []string {
	out := []string{
		"LIMITRANGE",
		field("name", item.Name),
		field("namespace", item.Namespace),
		field("created", timestamp(item.CreationTimestamp.Time, now)),
	}

	for _, limit := range item.Spec.Limits {
		out = append(out, "", strings.ToUpper(string(limit.Type)))
		for _, pair := range []struct {
			name string
			list corev1.ResourceList
		}{
			{"default limit", limit.Default},
			{"default request", limit.DefaultRequest},
			{"min", limit.Min},
			{"max", limit.Max},
			{"ratio", limit.MaxLimitRequestRatio},
		} {
			if len(pair.list) > 0 {
				out = append(out, field(pair.name, quantities(pair.list)))
			}
		}
	}
	return out
}

func tellLimitRange(item *corev1.LimitRange, now time.Time) []string {
	out := []string{
		fmt.Sprintf("LimitRange %s was created %s ago and shapes %s in namespace %s.",
			item.Name, ago(now.Sub(item.CreationTimestamp.Time)),
			plural(len(item.Spec.Limits), "kind of object"), item.Namespace),
	}
	for _, limit := range item.Spec.Limits {
		if len(limit.Default) > 0 {
			out = append(out, fmt.Sprintf("A %s without limits of its own gets %s.",
				strings.ToLower(string(limit.Type)), quantities(limit.Default)))
		}
	}
	return out
}

func quantities(list corev1.ResourceList) string {
	names := make([]string, 0, len(list))
	for name := range list {
		names = append(names, string(name))
	}
	sort.Strings(names)

	parts := make([]string, 0, len(names))
	for _, name := range names {
		value := list[corev1.ResourceName(name)]
		parts = append(parts, name+" "+value.String())
	}
	return strings.Join(parts, ", ")
}

func quantity(value resource.Quantity) float64 {
	if value.MilliValue() < 1000 {
		return float64(value.MilliValue())
	}
	return float64(value.Value())
}

func nodeRoles(node *corev1.Node) string {
	roles := make([]string, 0, 2)
	for label := range node.Labels {
		if role := strings.TrimPrefix(label, "node-role.kubernetes.io/"); role != label && role != "" {
			roles = append(roles, role)
		}
	}
	sort.Strings(roles)
	return strings.Join(roles, ", ")
}

func meterLine(used, total float64, label string) string {
	if total <= 0 {
		return label
	}
	pct := 100 * used / total
	return fmt.Sprintf("%s %.0f%%  %s", Bar(pct, 20), pct, label)
}

func Bar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	filled := int(float64(width)*pct/100 + 0.5)
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("│", filled) + strings.Repeat(" ", width-filled) + "]"
}

func sortedKeys(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
