package kube

import (
	"context"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func gatewayObject(name string, programmed bool, routes int64) *unstructured.Unstructured {
	status := "True"
	if !programmed {
		status = "False"
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "Gateway",
		"metadata":   map[string]any{"name": name, "namespace": "production"},
		"spec": map[string]any{
			"gatewayClassName": "cilium",
			"listeners": []any{
				map[string]any{"name": "http", "port": int64(80), "protocol": "HTTP", "hostname": "shop.example.com"},
				map[string]any{"name": "https", "port": int64(443), "protocol": "HTTPS", "hostname": "shop.example.com"},
			},
		},
		"status": map[string]any{
			"addresses": []any{map[string]any{"type": "IPAddress", "value": "203.0.113.10"}},
			"conditions": []any{
				map[string]any{"type": "Accepted", "status": "True", "reason": "Accepted"},
				map[string]any{"type": "Programmed", "status": status, "reason": "Programmed"},
			},
			"listeners": []any{
				map[string]any{"name": "https", "attachedRoutes": routes,
					"conditions": []any{map[string]any{"type": "Accepted", "status": "True", "reason": "Accepted"}}},
			},
		},
	}}
}

func gatewayClient(objects ...*unstructured.Unstructured) *Client {
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			gateways:   "GatewayList",
			httpRoutes: "HTTPRouteList",
		})

	for _, item := range objects {
		resource := gateways
		if item.GetKind() == "HTTPRoute" {
			resource = httpRoutes
		}
		if err := dyn.Tracker().Create(resource, item, item.GetNamespace()); err != nil {
			panic(err)
		}
	}
	return NewWithClients(k8sfake.NewSimpleClientset(), metricsClient()).WithDynamic(dyn)
}

func TestGatewayRowsFollowTheirController(t *testing.T) {
	client := gatewayClient(gatewayObject("public", true, 3))

	result, err := client.Rows(context.Background(), "production", KindGateway)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	row := result.Rows[0]
	if row.Severity != Good || row.Status != "carrying 3 routes" {
		t.Errorf("a working gateway: %q %v", row.Status, row.Severity)
	}
	if !strings.Contains(row.Info, "cilium") || !strings.Contains(row.Info, "2 listeners") {
		t.Errorf("info: %q", row.Info)
	}

	refused := gatewayClient(gatewayObject("public", false, 3))
	result, err = refused.Rows(context.Background(), "production", KindGateway)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if row := result.Rows[0]; row.Severity != Bad || !strings.Contains(row.Status, "programmed") {
		t.Errorf("a refused gateway: %q %v", row.Status, row.Severity)
	}

	idle := gatewayClient(gatewayObject("public", true, 0))
	result, err = idle.Rows(context.Background(), "production", KindGateway)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if row := result.Rows[0]; row.Severity != Warn || row.Status != "no route attached" {
		t.Errorf("a gateway nothing hangs off: %q %v", row.Status, row.Severity)
	}
}

func TestGatewayDetailCarriesItsListenersAndLinks(t *testing.T) {
	client := gatewayClient(gatewayObject("public", true, 2))

	detail, err := client.TrafficObject(context.Background(), KindGateway, "production", "public")
	if err != nil {
		t.Fatalf("TrafficObject: %v", err)
	}

	notes := strings.Join(detail.Notes(), "\n")
	for _, want := range []string{"class", "cilium", "addresses", "203.0.113.10", "listeners", "http/80"} {
		if !strings.Contains(notes, want) {
			t.Errorf("the notes miss %q:\n%s", want, notes)
		}
	}

	body := strings.Join(detail.Describe(time.Now()), "\n")
	for _, want := range []string{"GATEWAY", "LISTENERS", "HTTPS/443", "CONDITIONS", "Programmed"} {
		if !strings.Contains(body, want) {
			t.Errorf("the description misses %q:\n%s", want, body)
		}
	}

	links := detail.Links()
	if len(links) != 2 {
		t.Fatalf("one link per listener: %v", links)
	}
	if links[0].URL != "http://shop.example.com/" || links[1].URL != "https://shop.example.com/" {
		t.Errorf("links: %v", links)
	}
}

func TestRoutesTakeTheirSchemeFromTheGateway(t *testing.T) {
	client := gatewayClient(gatewayObject("public-gateway", true, 1), httpRoute("shop", "api", true))

	detail, err := client.TrafficObject(context.Background(), KindHTTPRoute, "production", "shop")
	if err != nil {
		t.Fatalf("TrafficObject: %v", err)
	}

	links := detail.Links()
	if len(links) != 2 {
		t.Fatalf("the host and its path: %v", links)
	}
	for _, link := range links {
		if !strings.HasPrefix(link.URL, "https://shop.example.com") {
			t.Errorf("a gateway that terminates TLS gives https links: %v", links)
		}
	}
	if links[1].URL != "https://shop.example.com/api" {
		t.Errorf("the path must hang off the host: %q", links[1].URL)
	}
}
