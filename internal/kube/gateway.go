package kube

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var gateways = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "gateways",
}

type gateway struct {
	class      string
	addresses  []string
	listeners  []listener
	routes     int
	conditions []routeCondition
	refused    string
}

type listener struct {
	name     string
	port     int64
	protocol string
	hostname string
	routes   int
	refused  string
}

func (c *Client) gatewayRows(ctx context.Context, namespace string) (Result, error) {
	var result Result

	if c.dyn == nil {
		return result, errors.New("this build cannot read gateways")
	}

	list, err := c.dyn.Resource(gateways).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, errors.New(ExplainTraffic(err, KindGateway, namespace, c.Host))
	}

	rows := make([]Row, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		read := readGateway(item)

		row := blankRow(item.GetName(), item.GetCreationTimestamp().Time)
		row.Info = read.class + ", " + plural(len(read.listeners), "listener")
		row.Ready, row.Desired = read.routes, read.routes

		switch {
		case read.refused != "":
			row.Status, row.Severity = read.refused, Bad
		case len(read.addresses) == 0:
			row.Status, row.Severity = "no address yet", Warn
		case read.routes == 0:
			row.Status, row.Severity = "no route attached", Warn
		default:
			row.Status, row.Severity = "carrying "+plural(read.routes, "route"), Good
		}
		rows = append(rows, row)
	}

	Sort(rows)
	result.Rows = rows
	return result, nil
}

func readGateway(item *unstructured.Unstructured) gateway {
	var out gateway

	out.class, _, _ = unstructured.NestedString(item.Object, "spec", "gatewayClassName")
	if out.class == "" {
		out.class = "no class"
	}

	listeners, _, _ := unstructured.NestedSlice(item.Object, "spec", "listeners")
	for _, entry := range listeners {
		raw, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		port, _, _ := unstructured.NestedInt64(raw, "port")
		out.listeners = append(out.listeners, listener{
			name:     text(raw, "name"),
			port:     port,
			protocol: text(raw, "protocol"),
			hostname: text(raw, "hostname"),
		})
	}

	addresses, _, _ := unstructured.NestedSlice(item.Object, "status", "addresses")
	for _, entry := range addresses {
		raw, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if value := text(raw, "value"); value != "" {
			out.addresses = append(out.addresses, value)
		}
	}

	conditions, _, _ := unstructured.NestedSlice(item.Object, "status", "conditions")
	for _, entry := range conditions {
		raw, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		read := routeCondition{
			parent:  item.GetName(),
			kind:    text(raw, "type"),
			status:  text(raw, "status"),
			reason:  text(raw, "reason"),
			message: text(raw, "message"),
		}
		out.conditions = append(out.conditions, read)
		if read.status != "True" && out.refused == "" {
			out.refused = strings.ToLower(read.kind) + ": " + or(read.reason, "refused")
		}
	}

	reported, _, _ := unstructured.NestedSlice(item.Object, "status", "listeners")
	for _, entry := range reported {
		raw, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		attached, _, _ := unstructured.NestedInt64(raw, "attachedRoutes")
		out.routes += int(attached)

		name := text(raw, "name")
		for i := range out.listeners {
			if out.listeners[i].name != name {
				continue
			}
			out.listeners[i].routes = int(attached)

			listenerConditions, _, _ := unstructured.NestedSlice(raw, "conditions")
			for _, item := range listenerConditions {
				condition, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if text(condition, "status") != "True" && out.listeners[i].refused == "" {
					out.listeners[i].refused = strings.ToLower(text(condition, "type")) + ": " +
						or(text(condition, "reason"), "refused")
				}
			}
		}
	}
	return out
}

func (c *Client) gatewayDetail(ctx context.Context, namespace, name string) (*TrafficDetail, error) {
	if c.dyn == nil {
		return nil, errors.New("this build cannot read gateways")
	}

	item, err := c.dyn.Resource(gateways).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("gateway %s is gone from namespace %s", name, namespace)
		}
		return nil, errors.New(ExplainTraffic(err, KindGateway, namespace, c.Host))
	}

	read := readGateway(item)
	detail := &TrafficDetail{
		Kind:  KindGateway,
		Meta:  metav1.ObjectMeta{Name: item.GetName(), Namespace: item.GetNamespace(), Labels: item.GetLabels()},
		raw:   item,
		notes: gatewayNotes(read),
		links: gatewayLinks(read),
	}
	detail.body = func(now time.Time) []string { return describeGateway(item, read, now) }
	detail.prose = func(now time.Time) []string { return tellGateway(item, read, now) }
	return detail, nil
}

func gatewayNotes(read gateway) []string {
	out := []string{
		note("class", read.class),
		note("addresses", or(strings.Join(read.addresses, ", "), "none yet")),
		note("listeners", listenerSummary(read)),
		note("routes", plural(read.routes, "route")+" attached"),
	}

	if read.refused != "" {
		out = append(out, note("mind that", "the controller refused it, "+read.refused))
	}
	if len(read.addresses) == 0 {
		out = append(out, note("mind that", "no address has been assigned, nothing reaches it yet"))
	}
	for _, l := range read.listeners {
		if l.refused != "" {
			out = append(out, note("mind that", "listener "+l.name+" is not serving, "+l.refused))
		}
	}
	return out
}

func listenerSummary(read gateway) string {
	parts := make([]string, 0, len(read.listeners))
	for _, l := range read.listeners {
		parts = append(parts, fmt.Sprintf("%s/%d", strings.ToLower(l.protocol), l.port))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func gatewayLinks(read gateway) []Link {
	out := make([]Link, 0, len(read.listeners))
	for _, l := range read.listeners {
		host := l.hostname
		if host == "" {
			if len(read.addresses) == 0 {
				continue
			}
			host = read.addresses[0]
		}
		out = append(out, Link{Text: host, URL: listenerURL(l, host)})
	}
	return out
}

func listenerURL(l listener, host string) string {
	scheme := "http"
	port := ""

	switch strings.ToUpper(l.protocol) {
	case "HTTPS", "TLS":
		scheme = "https"
		if l.port != 443 {
			port = fmt.Sprintf(":%d", l.port)
		}
	default:
		if l.port != 80 {
			port = fmt.Sprintf(":%d", l.port)
		}
	}
	return scheme + "://" + host + port + "/"
}

func describeGateway(item *unstructured.Unstructured, read gateway, now time.Time) []string {
	out := []string{
		"GATEWAY",
		field("name", item.GetName()),
		field("namespace", item.GetNamespace()),
		field("class", read.class),
		field("addresses", or(strings.Join(read.addresses, ", "), "none yet")),
		field("routes", fmt.Sprintf("%d", read.routes)),
		field("created", timestamp(item.GetCreationTimestamp().Time, now)),
		"",
		"LISTENERS",
	}

	if len(read.listeners) == 0 {
		out = append(out, field("none", "this gateway listens nowhere"))
	}
	for _, l := range read.listeners {
		out = append(out, field(or(l.name, "listener"),
			fmt.Sprintf("%s/%d on %s", l.protocol, l.port, or(l.hostname, "any host"))))
		out = append(out, subfield("routes", fmt.Sprintf("%d attached", l.routes)))
		if l.refused != "" {
			out = append(out, subfield("refused", l.refused))
		}
	}

	out = append(out, "", "CONDITIONS")
	if len(read.conditions) == 0 {
		out = append(out, field("none", "the controller has not reported yet"))
	}
	for _, condition := range read.conditions {
		value := condition.status
		if condition.reason != "" {
			value += " (" + condition.reason + ")"
		}
		out = append(out, field(condition.kind, value))
	}
	return out
}

func tellGateway(item *unstructured.Unstructured, read gateway, now time.Time) []string {
	out := []string{
		fmt.Sprintf("Gateway %s belongs to class %s in namespace %s and was created %s ago.",
			item.GetName(), read.class, item.GetNamespace(),
			ago(now.Sub(item.GetCreationTimestamp().Time))),
		fmt.Sprintf("It listens on %s and carries %s.",
			listenerSummary(read), plural(read.routes, "route")),
	}

	if len(read.addresses) > 0 {
		out = append(out, "It answers at "+strings.Join(read.addresses, ", ")+".")
	} else {
		out = append(out, "Worth a look: no address has been assigned to it yet.")
	}
	if read.refused != "" {
		out = append(out, "Worth a look: the controller refused it, "+read.refused+".")
	}
	return out
}
