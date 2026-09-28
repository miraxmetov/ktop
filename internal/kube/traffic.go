package kube

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type endpointCount struct {
	ready int
	total int
}

func (c *Client) endpointsByService(ctx context.Context, namespace string) map[string]endpointCount {
	out := map[string]endpointCount{}

	list, err := c.pods.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return out
	}
	for i := range list.Items {
		slice := &list.Items[i]
		name := slice.Labels[discoveryv1.LabelServiceName]
		if name == "" {
			continue
		}
		count := out[name]
		ready, total := sliceEndpoints(slice)
		count.ready += ready
		count.total += total
		out[name] = count
	}
	return out
}

func sliceEndpoints(slice *discoveryv1.EndpointSlice) (int, int) {
	ready, total := 0, 0
	for _, endpoint := range slice.Endpoints {
		total++
		if endpoint.Conditions.Ready == nil || *endpoint.Conditions.Ready {
			ready++
		}
	}
	return ready, total
}

func endpointStatus(count endpointCount, empty string) (string, Severity) {
	switch {
	case count.total == 0:
		return empty, Bad
	case count.ready == 0:
		return fmt.Sprintf("0/%d endpoints", count.total), Bad
	case count.ready < count.total:
		return fmt.Sprintf("%d/%d endpoints", count.ready, count.total), Warn
	}
	return fmt.Sprintf("%d/%d endpoints", count.ready, count.total), Good
}

func (c *Client) serviceRows(ctx context.Context, namespace string) (Result, error) {
	var result Result

	list, err := c.pods.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, errors.New(ExplainTraffic(err, KindService, namespace, c.Host))
	}
	endpoints := c.endpointsByService(ctx, namespace)

	rows := make([]Row, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		row := blankRow(item.Name, item.CreationTimestamp.Time)
		row.Info = string(item.Spec.Type) + " " + servicePorts(item)

		count := endpoints[item.Name]
		row.Ready, row.Desired = count.ready, count.total

		switch {
		case item.Spec.Type == corev1.ServiceTypeExternalName:
			row.Status, row.Severity = "points at "+item.Spec.ExternalName, Muted
		case len(item.Spec.Selector) == 0:
			row.Status, row.Severity = "no selector, endpoints are set by hand", Muted
			if count.total > 0 {
				row.Status, row.Severity = endpointStatus(count, "")
			}
		default:
			row.Status, row.Severity = endpointStatus(count, "no endpoints")
		}
		rows = append(rows, row)
	}

	Sort(rows)
	result.Rows = rows
	return result, nil
}

func servicePorts(item *corev1.Service) string {
	parts := make([]string, 0, len(item.Spec.Ports))
	for _, port := range item.Spec.Ports {
		text := fmt.Sprintf("%d", port.Port)
		if port.TargetPort.String() != "" && port.TargetPort.String() != text {
			text += "→" + port.TargetPort.String()
		}
		if port.NodePort > 0 {
			text += fmt.Sprintf(" (node %d)", port.NodePort)
		}
		parts = append(parts, text+"/"+string(port.Protocol))
	}
	return strings.Join(parts, ", ")
}

func (c *Client) endpointSliceRows(ctx context.Context, namespace string) (Result, error) {
	var result Result

	list, err := c.pods.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, errors.New(ExplainTraffic(err, KindEndpointSlice, namespace, c.Host))
	}

	rows := make([]Row, 0, len(list.Items))
	for i := range list.Items {
		slice := &list.Items[i]
		ready, total := sliceEndpoints(slice)

		row := blankRow(slice.Name, slice.CreationTimestamp.Time)
		row.Ready, row.Desired = ready, total
		row.Info = "for " + or(slice.Labels[discoveryv1.LabelServiceName], "no service") +
			", " + string(slice.AddressType)
		row.Status, row.Severity = endpointStatus(endpointCount{ready: ready, total: total}, "empty")
		rows = append(rows, row)
	}

	Sort(rows)
	result.Rows = rows
	return result, nil
}

func (c *Client) ingressRows(ctx context.Context, namespace string) (Result, error) {
	var result Result

	list, err := c.pods.NetworkingV1().Ingresses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, errors.New(ExplainTraffic(err, KindIngress, namespace, c.Host))
	}
	endpoints := c.endpointsByService(ctx, namespace)

	rows := make([]Row, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		row := blankRow(item.Name, item.CreationTimestamp.Time)
		row.Info = ingressClass(item) + ", " + plural(len(ingressHosts(item)), "host")

		starved := make([]string, 0, 2)
		for _, backend := range ingressBackends(item) {
			if endpoints[backend].ready == 0 {
				starved = append(starved, backend)
			}
		}

		switch {
		case len(starved) > 0:
			row.Status, row.Severity = "no endpoints behind "+strings.Join(starved, ", "), Bad
		case len(item.Status.LoadBalancer.Ingress) == 0:
			row.Status, row.Severity = "no address yet", Warn
		default:
			row.Status, row.Severity = "routing to "+plural(len(ingressBackends(item)), "service"), Good
		}
		row.Ready, row.Desired = len(ingressBackends(item))-len(starved), len(ingressBackends(item))
		rows = append(rows, row)
	}

	Sort(rows)
	result.Rows = rows
	return result, nil
}

func ingressClass(item *networkingv1.Ingress) string {
	if item.Spec.IngressClassName != nil && *item.Spec.IngressClassName != "" {
		return *item.Spec.IngressClassName
	}
	if class := item.Annotations["kubernetes.io/ingress.class"]; class != "" {
		return class
	}
	return "no class"
}

func ingressHosts(item *networkingv1.Ingress) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(item.Spec.Rules))
	for _, rule := range item.Spec.Rules {
		host := or(rule.Host, "*")
		if !seen[host] {
			seen[host] = true
			out = append(out, host)
		}
	}
	return out
}

func ingressBackends(item *networkingv1.Ingress) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 4)

	add := func(backend *networkingv1.IngressBackend) {
		if backend == nil || backend.Service == nil || seen[backend.Service.Name] {
			return
		}
		seen[backend.Service.Name] = true
		out = append(out, backend.Service.Name)
	}

	add(item.Spec.DefaultBackend)
	for _, rule := range item.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for i := range rule.HTTP.Paths {
			add(&rule.HTTP.Paths[i].Backend)
		}
	}
	sort.Strings(out)
	return out
}

func (c *Client) networkPolicyRows(ctx context.Context, namespace string) (Result, error) {
	var result Result

	list, err := c.pods.NetworkingV1().NetworkPolicies(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, errors.New(ExplainTraffic(err, KindNetworkPolicy, namespace, c.Host))
	}

	pods, _ := c.pods.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})

	rows := make([]Row, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		selected := 0
		if pods != nil {
			for j := range pods.Items {
				if selects(item.Spec.PodSelector, pods.Items[j].Labels) {
					selected++
				}
			}
		}

		row := blankRow(item.Name, item.CreationTimestamp.Time)
		row.Ready, row.Desired = selected, selected
		row.Info = policyTypes(item) + ", " + plural(len(item.Spec.Ingress)+len(item.Spec.Egress), "rule")
		row.Status, row.Severity = fmt.Sprintf("holds %s", plural(selected, "pod")), Good
		if selected == 0 {
			row.Status, row.Severity = "selects no pod", Warn
		}
		rows = append(rows, row)
	}

	Sort(rows)
	result.Rows = rows
	return result, nil
}

func policyTypes(item *networkingv1.NetworkPolicy) string {
	parts := make([]string, 0, len(item.Spec.PolicyTypes))
	for _, kind := range item.Spec.PolicyTypes {
		parts = append(parts, strings.ToLower(string(kind)))
	}
	if len(parts) == 0 {
		return "ingress"
	}
	return strings.Join(parts, " and ")
}

func selects(selector metav1.LabelSelector, labels map[string]string) bool {
	for key, want := range selector.MatchLabels {
		if labels[key] != want {
			return false
		}
	}
	return true
}

func blankRow(name string, created time.Time) Row {
	return Row{Name: name, Created: created, CPUPct: -1, MemPct: -1, Worst: -1}
}
