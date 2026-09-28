package kube

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

type Link struct {
	Text string
	URL  string
}

type TrafficDetail struct {
	Kind   Kind
	Meta   metav1.ObjectMeta
	object runtime.Object
	raw    *unstructured.Unstructured
	notes  []string
	links  []Link
	gauge  *Gauge
	body   func(now time.Time) []string
	prose  func(now time.Time) []string
}

func (c *Client) TrafficObject(ctx context.Context, kind Kind, namespace, name string) (*TrafficDetail, error) {
	options := metav1.GetOptions{}

	switch kind {
	case KindService:
		item, err := c.pods.CoreV1().Services(namespace).Get(ctx, name, options)
		if err != nil {
			return nil, fmt.Errorf("%s", ExplainWorkload(err, namespace, kind, name, c.Host))
		}
		slices := c.slicesOf(ctx, namespace, name)
		detail := &TrafficDetail{Kind: kind, Meta: item.ObjectMeta, object: item, notes: serviceNotes(item, slices)}
		detail.body = func(now time.Time) []string { return describeService(item, slices, now) }
		detail.prose = func(now time.Time) []string { return tellService(item, slices, now) }
		return detail, nil

	case KindEndpointSlice:
		item, err := c.pods.DiscoveryV1().EndpointSlices(namespace).Get(ctx, name, options)
		if err != nil {
			return nil, fmt.Errorf("%s", ExplainWorkload(err, namespace, kind, name, c.Host))
		}
		detail := &TrafficDetail{Kind: kind, Meta: item.ObjectMeta, object: item, notes: sliceNotes(item)}
		detail.body = func(now time.Time) []string { return describeSlice(item, now) }
		detail.prose = func(now time.Time) []string { return tellSlice(item, now) }
		return detail, nil

	case KindIngress:
		item, err := c.pods.NetworkingV1().Ingresses(namespace).Get(ctx, name, options)
		if err != nil {
			return nil, fmt.Errorf("%s", ExplainWorkload(err, namespace, kind, name, c.Host))
		}
		endpoints := c.endpointsByService(ctx, namespace)
		detail := &TrafficDetail{Kind: kind, Meta: item.ObjectMeta, object: item,
			notes: ingressNotes(item, endpoints), links: ingressLinks(item)}
		detail.body = func(now time.Time) []string { return describeIngress(item, endpoints, now) }
		detail.prose = func(now time.Time) []string { return tellIngress(item, endpoints, now) }
		return detail, nil

	case KindHTTPRoute:
		return c.httpRouteDetail(ctx, namespace, name)

	case KindGateway:
		return c.gatewayDetail(ctx, namespace, name)

	case KindNetworkPolicy:
		item, err := c.pods.NetworkingV1().NetworkPolicies(namespace).Get(ctx, name, options)
		if err != nil {
			return nil, fmt.Errorf("%s", ExplainWorkload(err, namespace, kind, name, c.Host))
		}
		held := c.podsUnder(ctx, namespace, item.Spec.PodSelector)
		detail := &TrafficDetail{Kind: kind, Meta: item.ObjectMeta, object: item, notes: policyNotes(item, held)}
		detail.body = func(now time.Time) []string { return describePolicy(item, held, now) }
		detail.prose = func(now time.Time) []string { return tellPolicy(item, held, now) }
		return detail, nil
	}
	return nil, fmt.Errorf("%s cannot be inspected", kind)
}

func (c *Client) slicesOf(ctx context.Context, namespace, service string) []discoveryv1.EndpointSlice {
	list, err := c.pods.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: discoveryv1.LabelServiceName + "=" + service,
	})
	if err != nil {
		return nil
	}
	return list.Items
}

func (c *Client) podsUnder(ctx context.Context, namespace string, selector metav1.LabelSelector) []string {
	list, err := c.pods.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}

	out := make([]string, 0, 8)
	for i := range list.Items {
		if selects(selector, list.Items[i].Labels) {
			out = append(out, list.Items[i].Name)
		}
	}
	return out
}

func (d *TrafficDetail) Notes() []string {
	return d.notes
}

func (d *TrafficDetail) Links() []Link {
	return d.links
}

func (d *TrafficDetail) Gauge() *Gauge {
	return d.gauge
}

func (d *TrafficDetail) Describe(now time.Time) []string {
	out := d.body(now)
	if len(d.Meta.Labels) > 0 {
		out = append(out, "", "LABELS")
		for _, key := range sortedKeys(d.Meta.Labels) {
			out = append(out, field(key, d.Meta.Labels[key]))
		}
	}
	return out
}

func (d *TrafficDetail) Textual(now time.Time) []string {
	return d.prose(now)
}

func (d *TrafficDetail) YAML() ([]string, error) {
	if d.raw != nil {
		return routeYAML(d.raw)
	}

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

func countSlices(slices []discoveryv1.EndpointSlice) endpointCount {
	var count endpointCount
	for i := range slices {
		ready, total := sliceEndpoints(&slices[i])
		count.ready += ready
		count.total += total
	}
	return count
}

func serviceNotes(item *corev1.Service, slices []discoveryv1.EndpointSlice) []string {
	count := countSlices(slices)
	status, _ := endpointStatus(count, "no endpoints at all")

	out := []string{
		note("reachable at", serviceAddress(item)),
		note("endpoints", status),
		note("ports", or(servicePorts(item), "none")),
	}
	if len(item.Spec.Selector) > 0 {
		out = append(out, note("selects", joinPairs(item.Spec.Selector)))
	}

	switch {
	case item.Spec.Type == corev1.ServiceTypeExternalName:
		out = append(out, note("mind that", "this service is a DNS alias, it has no endpoints of its own"))
	case count.total == 0 && len(item.Spec.Selector) > 0:
		out = append(out, note("mind that", "nothing matches its selector, traffic to it is refused"))
	case count.ready == 0 && count.total > 0:
		out = append(out, note("mind that", "every endpoint is unready, traffic to it is refused"))
	case count.ready < count.total:
		out = append(out, note("mind that", fmt.Sprintf("%d of %d endpoints are not taking traffic", count.total-count.ready, count.total)))
	}
	return out
}

func serviceAddress(item *corev1.Service) string {
	switch {
	case item.Spec.Type == corev1.ServiceTypeExternalName:
		return item.Spec.ExternalName
	case item.Spec.ClusterIP == corev1.ClusterIPNone:
		return "no cluster IP, this service is headless"
	}

	address := item.Spec.ClusterIP
	for _, ingress := range item.Status.LoadBalancer.Ingress {
		address += ", " + or(ingress.Hostname, ingress.IP)
	}
	return address
}

func describeService(item *corev1.Service, slices []discoveryv1.EndpointSlice, now time.Time) []string {
	count := countSlices(slices)
	status, _ := endpointStatus(count, "none")

	out := []string{
		"SERVICE",
		field("name", item.Name),
		field("namespace", item.Namespace),
		field("type", string(item.Spec.Type)),
		field("cluster ip", or(item.Spec.ClusterIP, "-")),
		field("endpoints", status),
		field("created", timestamp(item.CreationTimestamp.Time, now)),
	}
	if len(item.Spec.Selector) > 0 {
		out = append(out, field("selector", joinPairs(item.Spec.Selector)))
	}
	if item.Spec.SessionAffinity != "" {
		out = append(out, field("affinity", string(item.Spec.SessionAffinity)))
	}
	if item.Spec.ExternalTrafficPolicy != "" {
		out = append(out, field("traffic policy", string(item.Spec.ExternalTrafficPolicy)))
	}

	out = append(out, "", "PORTS")
	if len(item.Spec.Ports) == 0 {
		out = append(out, field("none", "this service forwards nothing"))
	}
	for _, port := range item.Spec.Ports {
		name := or(port.Name, string(port.Protocol))
		text := fmt.Sprintf("%d → %s", port.Port, port.TargetPort.String())
		if port.NodePort > 0 {
			text += fmt.Sprintf(", node port %d", port.NodePort)
		}
		out = append(out, field(name, text))
	}

	out = append(out, "", "BEHIND IT")
	if count.total == 0 {
		out = append(out, field("none", "nothing serves this address"))
	}
	for i := range slices {
		for _, endpoint := range slices[i].Endpoints {
			state := "ready"
			if endpoint.Conditions.Ready != nil && !*endpoint.Conditions.Ready {
				state = "not ready"
			}
			target := "-"
			if endpoint.TargetRef != nil {
				target = endpoint.TargetRef.Name
			}
			out = append(out, field(strings.Join(endpoint.Addresses, ","), target+" ("+state+")"))
		}
	}
	return out
}

func tellService(item *corev1.Service, slices []discoveryv1.EndpointSlice, now time.Time) []string {
	count := countSlices(slices)

	out := []string{
		fmt.Sprintf("Service %s is a %s in namespace %s, created %s ago and reachable at %s.",
			item.Name, item.Spec.Type, item.Namespace, ago(now.Sub(item.CreationTimestamp.Time)),
			serviceAddress(item)),
	}
	if ports := servicePorts(item); ports != "" {
		out = append(out, "It forwards "+ports+".")
	}

	switch {
	case item.Spec.Type == corev1.ServiceTypeExternalName:
		out = append(out, "Being an alias, it keeps no endpoints of its own.")
	case count.total == 0:
		out = append(out, "Worth a look: nothing stands behind it, so traffic to it is refused.")
	case count.ready == 0:
		out = append(out, fmt.Sprintf("Worth a look: all %d endpoints are unready, so traffic to it is refused.", count.total))
	case count.ready < count.total:
		out = append(out, fmt.Sprintf("%d of its %d endpoints take traffic, the rest are not ready.", count.ready, count.total))
	default:
		out = append(out, fmt.Sprintf("All %s behind it are ready.", plural(count.total, "endpoint")))
	}
	return out
}

func sliceNotes(item *discoveryv1.EndpointSlice) []string {
	ready, total := sliceEndpoints(item)
	status, _ := endpointStatus(endpointCount{ready: ready, total: total}, "empty")

	out := []string{
		note("serves", or(item.Labels[discoveryv1.LabelServiceName], "no service")),
		note("endpoints", status),
		note("addresses", string(item.AddressType)),
	}
	if ready < total {
		out = append(out, note("mind that", fmt.Sprintf("%d of them are not taking traffic", total-ready)))
	}
	return out
}

func describeSlice(item *discoveryv1.EndpointSlice, now time.Time) []string {
	out := []string{
		"ENDPOINTSLICE",
		field("name", item.Name),
		field("namespace", item.Namespace),
		field("service", or(item.Labels[discoveryv1.LabelServiceName], "-")),
		field("address type", string(item.AddressType)),
		field("created", timestamp(item.CreationTimestamp.Time, now)),
		"",
		"PORTS",
	}

	if len(item.Ports) == 0 {
		out = append(out, field("none", "every port is served"))
	}
	for _, port := range item.Ports {
		number := "-"
		if port.Port != nil {
			number = fmt.Sprintf("%d", *port.Port)
		}
		name := "port"
		if port.Name != nil && *port.Name != "" {
			name = *port.Name
		}
		out = append(out, field(name, number))
	}

	out = append(out, "", "ENDPOINTS")
	if len(item.Endpoints) == 0 {
		out = append(out, field("none", "this slice is empty"))
	}
	for _, endpoint := range item.Endpoints {
		state := "ready"
		if endpoint.Conditions.Ready != nil && !*endpoint.Conditions.Ready {
			state = "not ready"
		}
		target := "-"
		if endpoint.TargetRef != nil {
			target = endpoint.TargetRef.Name
		}
		node := ""
		if endpoint.NodeName != nil {
			node = " on " + *endpoint.NodeName
		}
		out = append(out, field(strings.Join(endpoint.Addresses, ","), target+" ("+state+")"+node))
	}
	return out
}

func tellSlice(item *discoveryv1.EndpointSlice, now time.Time) []string {
	ready, total := sliceEndpoints(item)
	return []string{
		fmt.Sprintf("EndpointSlice %s serves %s in namespace %s and was created %s ago.",
			item.Name, or(item.Labels[discoveryv1.LabelServiceName], "no service"),
			item.Namespace, ago(now.Sub(item.CreationTimestamp.Time))),
		fmt.Sprintf("It holds %s, %d of them ready to take traffic.", plural(total, "endpoint"), ready),
	}
}

func ingressNotes(item *networkingv1.Ingress, endpoints map[string]endpointCount) []string {
	out := []string{
		note("class", ingressClass(item)),
		note("hosts", or(strings.Join(ingressHosts(item), ", "), "none")),
		note("address", or(ingressAddress(item), "none yet")),
	}

	starved := make([]string, 0, 2)
	for _, backend := range ingressBackends(item) {
		if endpoints[backend].ready == 0 {
			starved = append(starved, backend)
		}
	}
	if len(starved) > 0 {
		out = append(out, note("mind that", "nothing answers behind "+strings.Join(starved, ", ")))
	}
	if len(item.Status.LoadBalancer.Ingress) == 0 {
		out = append(out, note("mind that", "the controller has not given it an address yet"))
	}
	return out
}

func ingressLinks(item *networkingv1.Ingress) []Link {
	secured := map[string]bool{}
	for _, tls := range item.Spec.TLS {
		for _, host := range tls.Hosts {
			secured[host] = true
		}
	}

	out := make([]Link, 0, 4)
	seen := map[string]bool{}

	add := func(host, path string) {
		if host == "" || host == "*" {
			host = ingressAddress(item)
		}
		if host == "" {
			return
		}
		scheme := "http"
		if secured[host] {
			scheme = "https"
		}
		url := scheme + "://" + host + or(path, "/")
		if seen[url] {
			return
		}
		seen[url] = true
		out = append(out, Link{Text: host + or(path, "/"), URL: url})
	}

	for _, rule := range item.Spec.Rules {
		add(rule.Host, "/")
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			add(rule.Host, path.Path)
		}
	}
	return out
}

func ingressAddress(item *networkingv1.Ingress) string {
	parts := make([]string, 0, len(item.Status.LoadBalancer.Ingress))
	for _, address := range item.Status.LoadBalancer.Ingress {
		parts = append(parts, or(address.Hostname, address.IP))
	}
	return strings.Join(parts, ", ")
}

func describeIngress(item *networkingv1.Ingress, endpoints map[string]endpointCount, now time.Time) []string {
	out := []string{
		"INGRESS",
		field("name", item.Name),
		field("namespace", item.Namespace),
		field("class", ingressClass(item)),
		field("address", or(ingressAddress(item), "none yet")),
		field("created", timestamp(item.CreationTimestamp.Time, now)),
		"",
		"ROUTES",
	}

	for _, rule := range item.Spec.Rules {
		host := or(rule.Host, "*")
		if rule.HTTP == nil {
			out = append(out, field(host, "no paths"))
			continue
		}
		for _, path := range rule.HTTP.Paths {
			target := "-"
			if path.Backend.Service != nil {
				target = path.Backend.Service.Name + backendPort(path.Backend)
				count := endpoints[path.Backend.Service.Name]
				target += fmt.Sprintf(" (%d/%d endpoints)", count.ready, count.total)
			}
			out = append(out, field(host+or(path.Path, "/"), target))
		}
	}
	if len(item.Spec.Rules) == 0 {
		out = append(out, field("none", "this ingress routes nothing"))
	}

	if len(item.Spec.TLS) > 0 {
		out = append(out, "", "TLS")
		for _, tls := range item.Spec.TLS {
			out = append(out, field(or(tls.SecretName, "no secret"), strings.Join(tls.Hosts, ", ")))
		}
	}
	return out
}

func backendPort(backend networkingv1.IngressBackend) string {
	if backend.Service == nil {
		return ""
	}
	if backend.Service.Port.Name != "" {
		return ":" + backend.Service.Port.Name
	}
	if backend.Service.Port.Number > 0 {
		return fmt.Sprintf(":%d", backend.Service.Port.Number)
	}
	return ""
}

func tellIngress(item *networkingv1.Ingress, endpoints map[string]endpointCount, now time.Time) []string {
	out := []string{
		fmt.Sprintf("Ingress %s belongs to class %s in namespace %s and was created %s ago.",
			item.Name, ingressClass(item), item.Namespace, ago(now.Sub(item.CreationTimestamp.Time))),
		fmt.Sprintf("It answers for %s and routes to %s.",
			or(strings.Join(ingressHosts(item), ", "), "no host"),
			or(strings.Join(ingressBackends(item), ", "), "no service")),
	}

	for _, backend := range ingressBackends(item) {
		if endpoints[backend].ready == 0 {
			out = append(out, fmt.Sprintf("Worth a look: service %s has no ready endpoint, so that route answers with an error.", backend))
		}
	}
	if len(item.Status.LoadBalancer.Ingress) == 0 {
		out = append(out, "Worth a look: no address has been assigned to it yet.")
	}
	return out
}

func policyNotes(item *networkingv1.NetworkPolicy, held []string) []string {
	out := []string{
		note("holds", plural(len(held), "pod")),
		note("selector", or(joinPairs(item.Spec.PodSelector.MatchLabels), "every pod in the namespace")),
		note("governs", policyTypes(item)),
	}
	if len(held) == 0 {
		out = append(out, note("mind that", "no pod matches its selector, so it shapes nothing"))
	}
	if len(item.Spec.Ingress) == 0 && holdsType(item, networkingv1.PolicyTypeIngress) {
		out = append(out, note("mind that", "it allows no ingress at all, every incoming connection is denied"))
	}
	return out
}

func holdsType(item *networkingv1.NetworkPolicy, want networkingv1.PolicyType) bool {
	if len(item.Spec.PolicyTypes) == 0 {
		return want == networkingv1.PolicyTypeIngress
	}
	for _, kind := range item.Spec.PolicyTypes {
		if kind == want {
			return true
		}
	}
	return false
}

func describePolicy(item *networkingv1.NetworkPolicy, held []string, now time.Time) []string {
	out := []string{
		"NETWORKPOLICY",
		field("name", item.Name),
		field("namespace", item.Namespace),
		field("selector", or(joinPairs(item.Spec.PodSelector.MatchLabels), "every pod")),
		field("governs", policyTypes(item)),
		field("created", timestamp(item.CreationTimestamp.Time, now)),
		"",
		"INGRESS RULES",
	}

	if len(item.Spec.Ingress) == 0 {
		out = append(out, field("none", "nothing may come in"))
	}
	for i, rule := range item.Spec.Ingress {
		out = append(out, field(fmt.Sprintf("rule %d", i+1), peerText(rule.From)+portText(rule.Ports)))
	}

	out = append(out, "", "EGRESS RULES")
	if len(item.Spec.Egress) == 0 {
		out = append(out, field("none", "this policy does not shape outgoing traffic"))
	}
	for i, rule := range item.Spec.Egress {
		out = append(out, field(fmt.Sprintf("rule %d", i+1), peerText(rule.To)+portText(rule.Ports)))
	}

	out = append(out, "", "PODS HELD")
	if len(held) == 0 {
		out = append(out, field("none", "its selector matches nothing"))
	}
	for _, name := range held {
		out = append(out, field(name, ""))
	}
	return out
}

func peerText(peers []networkingv1.NetworkPolicyPeer) string {
	if len(peers) == 0 {
		return "from anywhere"
	}

	parts := make([]string, 0, len(peers))
	for _, peer := range peers {
		switch {
		case peer.IPBlock != nil:
			parts = append(parts, peer.IPBlock.CIDR)
		case peer.NamespaceSelector != nil && peer.PodSelector != nil:
			parts = append(parts, "pods "+joinPairs(peer.PodSelector.MatchLabels)+
				" in namespaces "+joinPairs(peer.NamespaceSelector.MatchLabels))
		case peer.NamespaceSelector != nil:
			parts = append(parts, "namespaces "+or(joinPairs(peer.NamespaceSelector.MatchLabels), "all"))
		case peer.PodSelector != nil:
			parts = append(parts, "pods "+or(joinPairs(peer.PodSelector.MatchLabels), "all"))
		}
	}
	return strings.Join(parts, "; ")
}

func portText(ports []networkingv1.NetworkPolicyPort) string {
	if len(ports) == 0 {
		return ", any port"
	}

	parts := make([]string, 0, len(ports))
	for _, port := range ports {
		text := "any"
		if port.Port != nil {
			text = port.Port.String()
		}
		if port.Protocol != nil {
			text += "/" + string(*port.Protocol)
		}
		parts = append(parts, text)
	}
	return ", port " + strings.Join(parts, ", ")
}

func tellPolicy(item *networkingv1.NetworkPolicy, held []string, now time.Time) []string {
	out := []string{
		fmt.Sprintf("NetworkPolicy %s shapes %s traffic in namespace %s and was created %s ago.",
			item.Name, policyTypes(item), item.Namespace, ago(now.Sub(item.CreationTimestamp.Time))),
		fmt.Sprintf("Its selector holds %s.", plural(len(held), "pod")),
	}
	if len(held) == 0 {
		out = append(out, "Worth a look: matching nothing, this policy has no effect at all.")
	}
	if len(item.Spec.Ingress) == 0 && holdsType(item, networkingv1.PolicyTypeIngress) {
		out = append(out, "Worth a look: with no ingress rule it denies every incoming connection.")
	}
	return out
}

func note(name, value string) string {
	return fmt.Sprintf("  %-14s %s", name, value)
}
