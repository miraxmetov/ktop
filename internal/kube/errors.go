package kube

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
)

func ExplainPods(err error, namespace, host string) string {
	if err == nil {
		return ""
	}
	switch {
	case apierrors.IsForbidden(err):
		return fmt.Sprintf("no permission to list pods in namespace %s", namespace)
	case apierrors.IsUnauthorized(err):
		return "cluster rejected the credentials from your kubeconfig"
	case apierrors.IsNotFound(err):
		return fmt.Sprintf("namespace %s not found in this cluster", namespace)
	}
	return transportText(err, host, fmt.Sprintf("cannot list pods in namespace %s", namespace))
}

func ExplainWorkloads(err error, namespace string, kind Kind, host string) string {
	if err == nil {
		return ""
	}
	what := strings.ToLower(kind.String())
	switch {
	case apierrors.IsForbidden(err):
		return fmt.Sprintf("no permission to list %s in namespace %s", what, namespace)
	case apierrors.IsUnauthorized(err):
		return "cluster rejected the credentials from your kubeconfig"
	}
	return transportText(err, host, fmt.Sprintf("cannot list %s in namespace %s", what, namespace))
}

func ExplainWorkload(err error, namespace string, kind Kind, name, host string) string {
	if err == nil {
		return ""
	}
	what := strings.ToLower(strings.TrimSuffix(kind.String(), "s"))
	switch {
	case apierrors.IsNotFound(err):
		return fmt.Sprintf("%s %s is gone from namespace %s", what, name, namespace)
	case apierrors.IsForbidden(err):
		return fmt.Sprintf("no permission to read %s %s", what, name)
	}
	return transportText(err, host, fmt.Sprintf("cannot read %s %s", what, name))
}

func ExplainPod(err error, namespace, name, host string) string {
	if err == nil {
		return ""
	}
	if apierrors.IsNotFound(err) {
		return fmt.Sprintf("pod %s is gone from namespace %s", name, namespace)
	}
	if apierrors.IsForbidden(err) {
		return fmt.Sprintf("no permission to read pod %s", name)
	}
	return transportText(err, host, fmt.Sprintf("cannot read pod %s", name))
}

func ExplainLogs(err error, pod, container string) string {
	if err == nil {
		return ""
	}
	switch {
	case apierrors.IsForbidden(err):
		return "no permission to read the logs of " + pod
	case apierrors.IsNotFound(err):
		return "pod " + pod + " is gone"
	case apierrors.IsBadRequest(err):
		return "container " + or(container, "?") + " has no logs yet"
	}
	return "cannot follow the logs: " + firstLine(err.Error())
}

func or(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func ExplainMetrics(err error, namespace string) string {
	if err == nil {
		return ""
	}
	switch {
	case apierrors.IsForbidden(err):
		return "no permission to read pod metrics, CPU and MEM hidden"
	case apierrors.IsUnauthorized(err):
		return "credentials rejected for pod metrics, CPU and MEM hidden"
	case apierrors.IsNotFound(err), meta.IsNoMatchError(err), apierrors.IsServiceUnavailable(err):
		return "metrics-server unavailable, CPU and MEM hidden"
	}
	return "metrics unavailable, CPU and MEM hidden"
}

func ExplainNamespaces(err error) string {
	if err == nil {
		return ""
	}
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
		return "no permission to list namespaces, type a name and press Enter"
	}
	return "cannot list namespaces, type a name and press Enter"
}

type ConfigError struct {
	Message string
	Hint    string
}

func (e *ConfigError) Error() string {
	return e.Message
}

func ExplainConfig(err error) *ConfigError {
	if err == nil {
		return nil
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "no configuration has been provided"),
		strings.Contains(text, "no such file or directory"):
		return &ConfigError{
			Message: "no kubeconfig found",
			Hint:    "set KUBECONFIG variable or create one at ~/.kube/config",
		}
	case strings.Contains(text, "context") &&
		(strings.Contains(text, "was not found") || strings.Contains(text, "does not exist")):
		return &ConfigError{
			Message: "kube context not found in your kubeconfig",
			Hint:    "run kubectl config get-contexts to list the ones you have",
		}
	}
	return &ConfigError{Message: "kubeconfig: " + firstLine(text)}
}

func transportText(err error, host, fallback string) string {
	if errors.Is(err, context.DeadlineExceeded) || apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) {
		return fmt.Sprintf("cluster did not answer in time (%s)", shortHost(host))
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		text := urlErr.Error()
		switch {
		case strings.Contains(text, "no such host"):
			return fmt.Sprintf("cannot resolve the cluster address %s", shortHost(host))
		case strings.Contains(text, "connection refused"):
			return fmt.Sprintf("cluster at %s refused the connection", shortHost(host))
		case strings.Contains(text, "certificate"):
			return fmt.Sprintf("TLS certificate rejected for %s", shortHost(host))
		case strings.Contains(text, "exec"):
			return "credential plugin from your kubeconfig failed, run kubectl once to refresh it"
		}
		return fmt.Sprintf("cannot reach the cluster at %s", shortHost(host))
	}
	if strings.Contains(err.Error(), "exec") && strings.Contains(err.Error(), "credential") {
		return "credential plugin from your kubeconfig failed, run kubectl once to refresh it"
	}
	if status := apierrors.APIStatus(nil); errors.As(err, &status) {
		return fmt.Sprintf("%s: %s", fallback, firstLine(status.Status().Message))
	}
	return fmt.Sprintf("%s: %s", fallback, firstLine(err.Error()))
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexAny(text, "\n"); i >= 0 {
		text = text[:i]
	}
	if len(text) > 90 {
		text = text[:90]
	}
	return text
}

func shortHost(host string) string {
	if host == "" {
		return "the cluster"
	}
	if u, err := url.Parse(host); err == nil && u.Host != "" {
		return u.Host
	}
	return host
}
