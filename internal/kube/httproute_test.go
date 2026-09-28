package kube

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func httpRoute(name string, backend string, accepted bool) *unstructured.Unstructured {
	status := "True"
	if !accepted {
		status = "False"
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata": map[string]any{
			"name":      name,
			"namespace": "production",
		},
		"spec": map[string]any{
			"parentRefs": []any{map[string]any{"name": "public-gateway"}},
			"hostnames":  []any{"shop.example.com"},
			"rules": []any{map[string]any{
				"matches": []any{map[string]any{
					"path": map[string]any{"type": "PathPrefix", "value": "/api"},
				}},
				"backendRefs": []any{map[string]any{"name": backend, "port": int64(80)}},
			}},
		},
		"status": map[string]any{
			"parents": []any{map[string]any{
				"parentRef": map[string]any{"name": "public-gateway"},
				"conditions": []any{map[string]any{
					"type": "Accepted", "status": status, "reason": "Accepted",
					"message": "route accepted",
				}},
			}},
		},
	}}
}

func routeClient(objects ...runtime.Object) *Client {
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{httpRoutes: "HTTPRouteList"}, objects...)

	pods := k8sfake.NewSimpleClientset(
		service("api", map[string]string{"app": "api"}),
		slice("api-abc", "api", true),
	)
	return NewWithClients(pods, metricsClient()).WithDynamic(dyn)
}

func TestHTTPRouteRowsFollowTheirGatewayAndBackends(t *testing.T) {
	client := routeClient(httpRoute("shop", "api", true))

	result, err := client.Rows(context.Background(), "production", KindHTTPRoute)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("rows: %v", result.Rows)
	}

	row := result.Rows[0]
	if row.Severity != Good || !strings.Contains(row.Status, "routing to 1 service") {
		t.Errorf("a healthy route: %q %v", row.Status, row.Severity)
	}
	if !strings.Contains(row.Info, "public-gateway") {
		t.Errorf("info: %q", row.Info)
	}

	refused := routeClient(httpRoute("shop", "api", false))
	result, err = refused.Rows(context.Background(), "production", KindHTTPRoute)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if row := result.Rows[0]; row.Severity != Bad || !strings.Contains(row.Status, "accepted") {
		t.Errorf("a refused route: %q %v", row.Status, row.Severity)
	}

	starved := routeClient(httpRoute("shop", "gone", true))
	result, err = starved.Rows(context.Background(), "production", KindHTTPRoute)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if row := result.Rows[0]; row.Severity != Bad || !strings.Contains(row.Status, "no endpoints behind gone") {
		t.Errorf("a route with nothing behind it: %q %v", row.Status, row.Severity)
	}
}

func TestHTTPRouteDetailReadsInThreeForms(t *testing.T) {
	client := routeClient(httpRoute("shop", "api", true))

	detail, err := client.TrafficObject(context.Background(), KindHTTPRoute, "production", "shop")
	if err != nil {
		t.Fatalf("TrafficObject: %v", err)
	}

	notes := strings.Join(detail.Notes(), "\n")
	for _, want := range []string{"attached to", "public-gateway", "hosts", "shop.example.com", "backends", "api"} {
		if !strings.Contains(notes, want) {
			t.Errorf("the notes miss %q:\n%s", want, notes)
		}
	}

	body := strings.Join(detail.Describe(time.Now()), "\n")
	for _, want := range []string{"HTTPROUTE", "ROUTES", "prefix /api", "api:80", "GATEWAYS", "Accepted"} {
		if !strings.Contains(body, want) {
			t.Errorf("the description misses %q:\n%s", want, body)
		}
	}
	if prose := strings.Join(detail.Textual(time.Now()), " "); !strings.Contains(prose, "HTTPRoute shop hangs off public-gateway") {
		t.Errorf("prose: %s", prose)
	}
	manifest, err := detail.YAML()
	if err != nil || !strings.Contains(strings.Join(manifest, "\n"), "name: shop") {
		t.Errorf("yaml: %v %v", manifest, err)
	}
}

func TestHTTPRoutesWithoutTheAPIReadPlainly(t *testing.T) {
	client := NewWithClients(k8sfake.NewSimpleClientset(&corev1.Pod{}), metricsClient())

	if _, err := client.Rows(context.Background(), "production", KindHTTPRoute); err == nil {
		t.Fatal("without a dynamic client ktop must say so")
	}
}
