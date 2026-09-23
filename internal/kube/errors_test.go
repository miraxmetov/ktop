package kube

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func forbidden(resource string) error {
	return apierrors.NewForbidden(schema.GroupResource{Resource: resource}, "",
		errors.New(`pods is forbidden: User "reader" cannot list resource "pods" in API group "" in the namespace "production"`))
}

func TestExplainPods(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"forbidden", forbidden("pods"), "no permission to list pods in namespace production"},
		{"unauthorized", apierrors.NewUnauthorized("bad token"), "cluster rejected the credentials from your kubeconfig"},
		{"not found", apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, "production"),
			"namespace production not found in this cluster"},
		{"timeout", context.DeadlineExceeded, "cluster did not answer in time (api.example.com)"},
		{"dns", &url.Error{Op: "Get", URL: "https://api.example.com", Err: errors.New("dial tcp: lookup api.example.com: no such host")},
			"cannot resolve the cluster address api.example.com"},
		{"refused", &url.Error{Op: "Get", URL: "https://api.example.com", Err: errors.New("dial tcp 10.0.0.1:443: connect: connection refused")},
			"cluster at api.example.com refused the connection"},
		{"tls", &url.Error{Op: "Get", URL: "https://api.example.com", Err: errors.New("x509: certificate signed by unknown authority")},
			"TLS certificate rejected for api.example.com"},
	}
	for _, c := range cases {
		got := ExplainPods(c.err, "production", "https://api.example.com")
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestExplainPodsNeverLeaksGoSyntax(t *testing.T) {
	err := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("network is unreachable")}
	got := ExplainPods(err, "production", "https://api.example.com")
	for _, bad := range []string{"*", "&", "{", "0x"} {
		if strings.Contains(got, bad) {
			t.Errorf("message %q contains Go syntax %q", got, bad)
		}
	}
	if got == "" {
		t.Error("want a human readable message")
	}
}

func TestExplainMetrics(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{forbidden("pods"), "no permission to read pod metrics, CPU and MEM hidden"},
		{apierrors.NewNotFound(schema.GroupResource{Group: "metrics.k8s.io", Resource: "pods"}, ""),
			"metrics-server unavailable, CPU and MEM hidden"},
		{apierrors.NewServiceUnavailable("no endpoints"), "metrics-server unavailable, CPU and MEM hidden"},
		{errors.New("boom"), "metrics unavailable, CPU and MEM hidden"},
	}
	for _, c := range cases {
		if got := ExplainMetrics(c.err, "production"); got != c.want {
			t.Errorf("ExplainMetrics(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestExplainConfig(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{errors.New("invalid configuration: no configuration has been provided, try setting KUBERNETES_MASTER environment variable"),
			"no kubeconfig found, set KUBECONFIG or create ~/.kube/config"},
		{errors.New(`stat /home/u/.kube/config: no such file or directory`),
			"no kubeconfig found, set KUBECONFIG or create ~/.kube/config"},
		{errors.New(`context "staging" was not found`), "kube context not found in your kubeconfig"},
		{errors.New(`context "staging" does not exist`), "kube context not found in your kubeconfig"},
	}
	for _, c := range cases {
		if got := ExplainConfig(c.err); got != c.want {
			t.Errorf("ExplainConfig(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestRowsReportsForbiddenPods(t *testing.T) {
	pods := k8sfake.NewSimpleClientset()
	pods.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, forbidden("pods")
	})
	client := NewWithClients(pods, metricsClient())
	client.Host = "https://api.example.com"

	_, err := client.Rows(context.Background(), "production")
	if err == nil {
		t.Fatal("want an error")
	}
	if err.Error() != "no permission to list pods in namespace production" {
		t.Errorf("got %q", err.Error())
	}
}

func TestRowsReportsMissingMetricsAsNote(t *testing.T) {
	healthy := pod("web", corev1.PodRunning, limited("1", "1Gi"), corev1.ContainerStatus{
		Name: "app", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	})
	metrics := metricsfakeForbidden()
	client := NewWithClients(k8sfake.NewSimpleClientset(healthy), metrics)

	res, err := client.Rows(context.Background(), "production")
	if err != nil {
		t.Fatalf("pods must still be listed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(res.Rows))
	}
	if res.Note != "no permission to read pod metrics, CPU and MEM hidden" {
		t.Errorf("note: got %q", res.Note)
	}
}

func TestNamespaces(t *testing.T) {
	client := NewWithClients(k8sfake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "production"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}},
	), metricsClient())

	names, err := client.Namespaces(context.Background())
	if err != nil {
		t.Fatalf("Namespaces: %v", err)
	}
	want := []string{"default", "kube-system", "production"}
	if len(names) != len(want) {
		t.Fatalf("got %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v, want %v", names, want)
		}
	}
}

func TestFilter(t *testing.T) {
	rows := []Row{{Name: "api-worker-1"}, {Name: "API-Gateway"}, {Name: "web-7d4"}}

	if got := Filter(rows, ""); len(got) != 3 {
		t.Errorf("empty query must keep every row, got %d", len(got))
	}
	if got := Filter(rows, "api"); len(got) != 2 {
		t.Errorf("case insensitive match: got %d", len(got))
	}
	if got := Filter(rows, "  web "); len(got) != 1 || got[0].Name != "web-7d4" {
		t.Errorf("trimmed query: got %v", got)
	}
	if got := Filter(rows, "zzz"); len(got) != 0 {
		t.Errorf("no match must return nothing, got %d", len(got))
	}
}

func TestMatchNamespaces(t *testing.T) {
	names := []string{"default", "production", "prod-blue", "kube-system"}

	if got := MatchNamespaces(names, "prod"); len(got) != 2 || got[0] != "production" {
		t.Errorf("got %v", got)
	}
	if got := MatchNamespaces(names, "KUBE"); len(got) != 1 || got[0] != "kube-system" {
		t.Errorf("case insensitive: got %v", got)
	}
	if got := MatchNamespaces(names, ""); len(got) != 4 {
		t.Errorf("empty query keeps all: got %v", got)
	}
}
