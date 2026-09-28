package kube

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func service(name string, selector map[string]string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "production"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "10.43.0.12",
			Selector:  selector,
			Ports: []corev1.ServicePort{{
				Name: "http", Port: 80, Protocol: corev1.ProtocolTCP,
				TargetPort: intstr.FromInt32(8080),
			}},
		},
	}
}

func slice(name, serviceName string, ready ...bool) *discoveryv1.EndpointSlice {
	out := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "production",
			Labels: map[string]string{discoveryv1.LabelServiceName: serviceName},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
	}
	for i, state := range ready {
		value := state
		out.Endpoints = append(out.Endpoints, discoveryv1.Endpoint{
			Addresses:  []string{"10.42.1." + string(rune('1'+i))},
			Conditions: discoveryv1.EndpointConditions{Ready: &value},
			TargetRef:  &corev1.ObjectReference{Kind: "Pod", Name: serviceName + "-pod"},
		})
	}
	return out
}

func TestServiceRowsCountTheirEndpoints(t *testing.T) {
	client := NewWithClients(k8sfake.NewSimpleClientset(
		service("api", map[string]string{"app": "api"}),
		service("orphan", map[string]string{"app": "gone"}),
		slice("api-abc", "api", true, false),
	), metricsClient())

	result, err := client.Rows(context.Background(), "production", KindService)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("rows: %v", result.Rows)
	}

	api := result.Rows[0]
	if api.Status != "1/2 endpoints" || api.Severity != Warn {
		t.Errorf("a half-ready service: %q %v", api.Status, api.Severity)
	}
	if api.Ready != 1 || api.Desired != 2 {
		t.Errorf("endpoints: %d of %d", api.Ready, api.Desired)
	}
	if !strings.Contains(api.Info, "ClusterIP") || !strings.Contains(api.Info, "80") {
		t.Errorf("info: %q", api.Info)
	}

	if orphan := result.Rows[1]; orphan.Status != "no endpoints" || orphan.Severity != Bad {
		t.Errorf("a service nothing answers for must be critical: %q %v", orphan.Status, orphan.Severity)
	}
}

func TestServiceDetailWarnsAboutUnreadyEndpoints(t *testing.T) {
	client := NewWithClients(k8sfake.NewSimpleClientset(
		service("api", map[string]string{"app": "api"}),
		slice("api-abc", "api", true, false),
	), metricsClient())

	detail, err := client.TrafficObject(context.Background(), KindService, "production", "api")
	if err != nil {
		t.Fatalf("TrafficObject: %v", err)
	}

	notes := strings.Join(detail.Notes(), "\n")
	for _, want := range []string{"reachable at", "10.43.0.12", "endpoints", "1/2", "selects", "app=api", "mind that"} {
		if !strings.Contains(notes, want) {
			t.Errorf("the notes miss %q:\n%s", want, notes)
		}
	}

	body := strings.Join(detail.Describe(time.Now()), "\n")
	for _, want := range []string{"SERVICE", "PORTS", "BEHIND IT", "api-pod"} {
		if !strings.Contains(body, want) {
			t.Errorf("the description misses %q:\n%s", want, body)
		}
	}
	if prose := strings.Join(detail.Textual(time.Now()), " "); !strings.Contains(prose, "1 of its 2 endpoints take traffic") {
		t.Errorf("prose: %s", prose)
	}
	if manifest, err := detail.YAML(); err != nil || !strings.Contains(strings.Join(manifest, "\n"), "name: api") {
		t.Errorf("yaml: %v %v", manifest, err)
	}
}

func TestIngressRowsFollowTheirBackends(t *testing.T) {
	class := "nginx"
	rules := []networkingv1.IngressRule{{
		Host: "shop.example.com",
		IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
			Paths: []networkingv1.HTTPIngressPath{{
				Path: "/",
				Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
					Name: "api", Port: networkingv1.ServiceBackendPort{Number: 80},
				}},
			}},
		}},
	}}

	live := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "public", Namespace: "production"},
		Spec:       networkingv1.IngressSpec{IngressClassName: &class, Rules: rules},
		Status: networkingv1.IngressStatus{LoadBalancer: networkingv1.IngressLoadBalancerStatus{
			Ingress: []networkingv1.IngressLoadBalancerIngress{{IP: "203.0.113.10"}},
		}},
	}

	client := NewWithClients(k8sfake.NewSimpleClientset(
		live, service("api", map[string]string{"app": "api"}), slice("api-abc", "api", true),
	), metricsClient())

	result, err := client.Rows(context.Background(), "production", KindIngress)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if row := result.Rows[0]; row.Severity != Good || !strings.Contains(row.Status, "routing to") {
		t.Errorf("a healthy ingress: %q %v", row.Status, row.Severity)
	}

	starved := NewWithClients(k8sfake.NewSimpleClientset(live), metricsClient())
	result, err = starved.Rows(context.Background(), "production", KindIngress)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if row := result.Rows[0]; row.Severity != Bad || !strings.Contains(row.Status, "no endpoints behind api") {
		t.Errorf("an ingress with nothing behind it: %q %v", row.Status, row.Severity)
	}
}

func TestNetworkPolicyRowsCountWhatTheyHold(t *testing.T) {
	held := pod("api-1", corev1.PodRunning, nil, corev1.ContainerStatus{Name: "app"})
	held.Labels = map[string]string{"app": "api"}

	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "api-allow", Namespace: "production"},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
		},
	}
	idle := policy.DeepCopy()
	idle.Name = "idle"
	idle.Spec.PodSelector = metav1.LabelSelector{MatchLabels: map[string]string{"app": "nothing"}}

	client := NewWithClients(k8sfake.NewSimpleClientset(policy, idle, held), metricsClient())
	result, err := client.Rows(context.Background(), "production", KindNetworkPolicy)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}

	if row := result.Rows[0]; row.Status != "holds 1 pod" || row.Severity != Good {
		t.Errorf("a policy that holds a pod: %q %v", row.Status, row.Severity)
	}
	if row := result.Rows[1]; row.Status != "selects no pod" || row.Severity != Warn {
		t.Errorf("a policy that holds nothing: %q %v", row.Status, row.Severity)
	}

	detail, err := client.TrafficObject(context.Background(), KindNetworkPolicy, "production", "api-allow")
	if err != nil {
		t.Fatalf("TrafficObject: %v", err)
	}
	notes := strings.Join(detail.Notes(), "\n")
	if !strings.Contains(notes, "holds") || !strings.Contains(notes, "1 pod") {
		t.Errorf("notes: %s", notes)
	}
	if body := strings.Join(detail.Describe(time.Now()), "\n"); !strings.Contains(body, "nothing may come in") {
		t.Errorf("a policy with no rule denies everything:\n%s", body)
	}
}

func TestEndpointSliceRowsReadTheirEndpoints(t *testing.T) {
	client := NewWithClients(k8sfake.NewSimpleClientset(slice("api-abc", "api", true, true)), metricsClient())

	result, err := client.Rows(context.Background(), "production", KindEndpointSlice)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	row := result.Rows[0]
	if row.Status != "2/2 endpoints" || row.Severity != Good {
		t.Errorf("row: %q %v", row.Status, row.Severity)
	}
	if !strings.Contains(row.Info, "for api") {
		t.Errorf("info: %q", row.Info)
	}
}
