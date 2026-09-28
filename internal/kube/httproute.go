package kube

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

var httpRoutes = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "httproutes",
}

func (c *Client) httpRouteRows(ctx context.Context, namespace string) (Result, error) {
	var result Result

	if c.dyn == nil {
		return result, errors.New("this build cannot read HTTP routes")
	}

	list, err := c.dyn.Resource(httpRoutes).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, errors.New(ExplainTraffic(err, KindHTTPRoute, namespace, c.Host))
	}
	endpoints := c.endpointsByService(ctx, namespace)

	rows := make([]Row, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		route := readRoute(item)

		row := blankRow(item.GetName(), item.GetCreationTimestamp().Time)
		row.Info = strings.Join(route.parents, ", ") + ", " + plural(len(route.rules), "rule")
		row.Ready, row.Desired = 0, len(route.backends)

		starved := make([]string, 0, 2)
		for _, backend := range route.backends {
			if endpoints[backend].ready == 0 {
				starved = append(starved, backend)
			} else {
				row.Ready++
			}
		}

		switch {
		case route.refused != "":
			row.Status, row.Severity = route.refused, Bad
		case len(starved) > 0:
			row.Status, row.Severity = "no endpoints behind "+strings.Join(starved, ", "), Bad
		case len(route.conditions) == 0:
			row.Status, row.Severity = "no gateway has taken it", Warn
		default:
			row.Status, row.Severity = "routing to "+plural(len(route.backends), "service"), Good
		}
		rows = append(rows, row)
	}

	Sort(rows)
	result.Rows = rows
	return result, nil
}

type route struct {
	parents    []string
	hostnames  []string
	rules      []routeRule
	backends   []string
	conditions []routeCondition
	refused    string
}

type routeRule struct {
	matches  []string
	backends []string
}

type routeCondition struct {
	parent  string
	kind    string
	status  string
	reason  string
	message string
}

func readRoute(item *unstructured.Unstructured) route {
	var out route

	parents, _, _ := unstructured.NestedSlice(item.Object, "spec", "parentRefs")
	for _, entry := range parents {
		ref, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name := text(ref, "name")
		if section := text(ref, "sectionName"); section != "" {
			name += "/" + section
		}
		out.parents = append(out.parents, or(name, "unnamed gateway"))
	}
	if len(out.parents) == 0 {
		out.parents = []string{"no gateway"}
	}

	hosts, _, _ := unstructured.NestedStringSlice(item.Object, "spec", "hostnames")
	out.hostnames = hosts

	seen := map[string]bool{}
	rules, _, _ := unstructured.NestedSlice(item.Object, "spec", "rules")
	for _, entry := range rules {
		rule, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		var current routeRule
		matches, _, _ := unstructured.NestedSlice(rule, "matches")
		for _, raw := range matches {
			match, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			path, _, _ := unstructured.NestedMap(match, "path")
			value := text(path, "value")
			kind := strings.ToLower(strings.TrimSuffix(text(path, "type"), "Match"))
			if value == "" {
				value = "/"
			}
			if method := text(match, "method"); method != "" {
				value = method + " " + value
			}
			current.matches = append(current.matches, strings.TrimSpace(kind+" "+value))
		}
		if len(current.matches) == 0 {
			current.matches = []string{"everything"}
		}

		backends, _, _ := unstructured.NestedSlice(rule, "backendRefs")
		for _, raw := range backends {
			backend, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name := text(backend, "name")
			if name == "" {
				continue
			}
			label := name
			if port, found, _ := unstructured.NestedInt64(backend, "port"); found {
				label = fmt.Sprintf("%s:%d", name, port)
			}
			if weight, found, _ := unstructured.NestedInt64(backend, "weight"); found {
				label += fmt.Sprintf(" (weight %d)", weight)
			}
			current.backends = append(current.backends, label)

			if !seen[name] {
				seen[name] = true
				out.backends = append(out.backends, name)
			}
		}
		out.rules = append(out.rules, current)
	}
	sort.Strings(out.backends)

	status, _, _ := unstructured.NestedSlice(item.Object, "status", "parents")
	for _, entry := range status {
		parent, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		ref, _, _ := unstructured.NestedMap(parent, "parentRef")
		name := or(text(ref, "name"), "gateway")

		conditions, _, _ := unstructured.NestedSlice(parent, "conditions")
		for _, raw := range conditions {
			condition, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			read := routeCondition{
				parent:  name,
				kind:    text(condition, "type"),
				status:  text(condition, "status"),
				reason:  text(condition, "reason"),
				message: text(condition, "message"),
			}
			out.conditions = append(out.conditions, read)

			if read.status != "True" && out.refused == "" {
				out.refused = strings.ToLower(read.kind) + ": " + or(read.reason, "refused")
			}
		}
	}
	return out
}

func text(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _, _ := unstructured.NestedString(values, key)
	return value
}

func (c *Client) httpRouteDetail(ctx context.Context, namespace, name string) (*TrafficDetail, error) {
	if c.dyn == nil {
		return nil, errors.New("this build cannot read HTTP routes")
	}

	item, err := c.dyn.Resource(httpRoutes).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("HTTP route %s is gone from namespace %s", name, namespace)
		}
		return nil, errors.New(ExplainTraffic(err, KindHTTPRoute, namespace, c.Host))
	}

	read := readRoute(item)
	endpoints := c.endpointsByService(ctx, namespace)

	detail := &TrafficDetail{
		Kind:  KindHTTPRoute,
		Meta:  metav1.ObjectMeta{Name: item.GetName(), Namespace: item.GetNamespace(), Labels: item.GetLabels()},
		raw:   item,
		notes: routeNotes(read, endpoints),
	}
	detail.body = func(now time.Time) []string { return describeRoute(item, read, endpoints, now) }
	detail.prose = func(now time.Time) []string { return tellRoute(item, read, endpoints, now) }
	return detail, nil
}

func routeNotes(read route, endpoints map[string]endpointCount) []string {
	out := []string{
		note("attached to", strings.Join(read.parents, ", ")),
		note("hosts", or(strings.Join(read.hostnames, ", "), "every host of its gateway")),
		note("backends", or(strings.Join(read.backends, ", "), "none")),
	}

	if read.refused != "" {
		out = append(out, note("mind that", "the gateway refused it, "+read.refused))
	}
	if len(read.conditions) == 0 {
		out = append(out, note("mind that", "no gateway reports on it yet, so it may route nothing"))
	}
	for _, backend := range read.backends {
		if endpoints[backend].ready == 0 {
			out = append(out, note("mind that", "nothing answers behind "+backend))
		}
	}
	return out
}

func describeRoute(item *unstructured.Unstructured, read route, endpoints map[string]endpointCount, now time.Time) []string {
	out := []string{
		"HTTPROUTE",
		field("name", item.GetName()),
		field("namespace", item.GetNamespace()),
		field("gateways", strings.Join(read.parents, ", ")),
		field("hosts", or(strings.Join(read.hostnames, ", "), "*")),
		field("created", timestamp(item.GetCreationTimestamp().Time, now)),
		"",
		"ROUTES",
	}

	if len(read.rules) == 0 {
		out = append(out, field("none", "this route matches nothing"))
	}
	for i, rule := range read.rules {
		out = append(out, field(fmt.Sprintf("rule %d", i+1), strings.Join(rule.matches, ", ")))
		for _, backend := range rule.backends {
			name := strings.SplitN(backend, ":", 2)[0]
			count := endpoints[name]
			out = append(out, subfield("to", fmt.Sprintf("%s (%d/%d endpoints)", backend, count.ready, count.total)))
		}
		if len(rule.backends) == 0 {
			out = append(out, subfield("to", "nothing"))
		}
	}

	out = append(out, "", "GATEWAYS")
	if len(read.conditions) == 0 {
		out = append(out, field("none", "no gateway has reported on this route"))
	}
	for _, condition := range read.conditions {
		value := condition.status
		if condition.reason != "" {
			value += " (" + condition.reason + ")"
		}
		out = append(out, field(condition.parent+" "+condition.kind, value))
	}
	return out
}

func tellRoute(item *unstructured.Unstructured, read route, endpoints map[string]endpointCount, now time.Time) []string {
	out := []string{
		fmt.Sprintf("HTTPRoute %s hangs off %s in namespace %s and was created %s ago.",
			item.GetName(), strings.Join(read.parents, ", "), item.GetNamespace(),
			ago(now.Sub(item.GetCreationTimestamp().Time))),
		fmt.Sprintf("It answers for %s and forwards to %s across %s.",
			or(strings.Join(read.hostnames, ", "), "every host of its gateway"),
			or(strings.Join(read.backends, ", "), "nothing"),
			plural(len(read.rules), "rule")),
	}

	if read.refused != "" {
		out = append(out, "Worth a look: the gateway refused it, "+read.refused+".")
	}
	for _, backend := range read.backends {
		if endpoints[backend].ready == 0 {
			out = append(out, fmt.Sprintf("Worth a look: service %s has no ready endpoint, so that rule answers with an error.", backend))
		}
	}
	return out
}

func routeYAML(item *unstructured.Unstructured) ([]string, error) {
	clean := item.DeepCopy()
	clean.SetManagedFields(nil)

	annotations := clean.GetAnnotations()
	delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
	if len(annotations) == 0 {
		annotations = nil
	}
	clean.SetAnnotations(annotations)

	body, err := yaml.Marshal(clean.Object)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(body), "\n"), "\n"), nil
}
