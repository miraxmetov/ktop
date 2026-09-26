package kube

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

type Detail struct {
	Kind      Kind
	Meta      metav1.ObjectMeta
	Ready     int32
	Desired   int32
	Updated   int32
	Available int32
	Selector  map[string]string
	Strategy  string
	Template  corev1.PodSpec
	object    runtime.Object
}

func (c *Client) Workload(ctx context.Context, namespace string, kind Kind, name string) (*Detail, error) {
	apps := c.pods.AppsV1()
	options := metav1.GetOptions{}

	switch kind {
	case KindDeployment:
		item, err := apps.Deployments(namespace).Get(ctx, name, options)
		if err != nil {
			return nil, fmt.Errorf("%s", ExplainWorkload(err, namespace, kind, name, c.Host))
		}
		return &Detail{
			Kind: kind, Meta: item.ObjectMeta, object: item,
			Ready: item.Status.ReadyReplicas, Desired: replicas(item.Spec.Replicas),
			Updated: item.Status.UpdatedReplicas, Available: item.Status.AvailableReplicas,
			Selector: item.Spec.Selector.MatchLabels, Strategy: string(item.Spec.Strategy.Type),
			Template: item.Spec.Template.Spec,
		}, nil

	case KindReplicaSet:
		item, err := apps.ReplicaSets(namespace).Get(ctx, name, options)
		if err != nil {
			return nil, fmt.Errorf("%s", ExplainWorkload(err, namespace, kind, name, c.Host))
		}
		return &Detail{
			Kind: kind, Meta: item.ObjectMeta, object: item,
			Ready: item.Status.ReadyReplicas, Desired: replicas(item.Spec.Replicas),
			Available: item.Status.AvailableReplicas,
			Selector:  item.Spec.Selector.MatchLabels, Template: item.Spec.Template.Spec,
		}, nil

	case KindDaemonSet:
		item, err := apps.DaemonSets(namespace).Get(ctx, name, options)
		if err != nil {
			return nil, fmt.Errorf("%s", ExplainWorkload(err, namespace, kind, name, c.Host))
		}
		return &Detail{
			Kind: kind, Meta: item.ObjectMeta, object: item,
			Ready: item.Status.NumberReady, Desired: item.Status.DesiredNumberScheduled,
			Updated: item.Status.UpdatedNumberScheduled, Available: item.Status.NumberAvailable,
			Selector: item.Spec.Selector.MatchLabels, Strategy: string(item.Spec.UpdateStrategy.Type),
			Template: item.Spec.Template.Spec,
		}, nil

	case KindStatefulSet:
		item, err := apps.StatefulSets(namespace).Get(ctx, name, options)
		if err != nil {
			return nil, fmt.Errorf("%s", ExplainWorkload(err, namespace, kind, name, c.Host))
		}
		return &Detail{
			Kind: kind, Meta: item.ObjectMeta, object: item,
			Ready: item.Status.ReadyReplicas, Desired: replicas(item.Spec.Replicas),
			Updated: item.Status.UpdatedReplicas, Available: item.Status.AvailableReplicas,
			Selector: item.Spec.Selector.MatchLabels, Strategy: string(item.Spec.UpdateStrategy.Type),
			Template: item.Spec.Template.Spec,
		}, nil
	}
	return nil, fmt.Errorf("%s cannot be inspected", kind)
}

func (d *Detail) YAML() ([]string, error) {
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

func (d *Detail) Describe(pods []string, now time.Time) []string {
	out := []string{strings.ToUpper(strings.TrimSuffix(d.Kind.String(), "s"))}
	out = append(out,
		field("name", d.Meta.Name),
		field("namespace", d.Meta.Namespace),
		field("ready", fmt.Sprintf("%d of %d", d.Ready, d.Desired)),
		field("created", timestamp(d.Meta.CreationTimestamp.Time, now)),
		field("QoS class", QOSOf(d.Template)),
	)
	if d.Updated > 0 {
		out = append(out, field("updated", fmt.Sprintf("%d", d.Updated)))
	}
	if d.Available > 0 {
		out = append(out, field("available", fmt.Sprintf("%d", d.Available)))
	}
	if d.Strategy != "" {
		out = append(out, field("strategy", d.Strategy))
	}
	if owner := ownerName(d.Meta); owner != "" {
		out = append(out, field("owner", owner))
	}
	if len(d.Selector) > 0 {
		out = append(out, field("selector", joinPairs(d.Selector)))
	}

	if len(d.Meta.Labels) > 0 {
		out = append(out, "", "LABELS")
		out = append(out, pairs(d.Meta.Labels)...)
	}
	if annotations := withoutNoise(d.Meta.Annotations); len(annotations) > 0 {
		out = append(out, "", "ANNOTATIONS")
		out = append(out, pairs(annotations)...)
	}

	out = append(out, "", "TEMPLATE")
	for i := range d.Template.Containers {
		container := &d.Template.Containers[i]
		out = append(out, "  "+container.Name,
			subfield("image", container.Image),
			subfield("requests", resourceText(container.Resources.Requests)),
			subfield("limits", resourceText(container.Resources.Limits)),
		)
		if len(container.Ports) > 0 {
			ports := make([]string, 0, len(container.Ports))
			for _, port := range container.Ports {
				ports = append(ports, fmt.Sprintf("%d/%s", port.ContainerPort, or(string(port.Protocol), "TCP")))
			}
			out = append(out, subfield("ports", strings.Join(ports, ", ")))
		}
	}

	if len(pods) > 0 {
		out = append(out, "", "PODS")
		for _, name := range pods {
			out = append(out, "  "+name)
		}
	}
	return out
}

func (d *Detail) Textual(pods []string, now time.Time) []string {
	what := strings.ToLower(strings.TrimSuffix(d.Kind.String(), "s"))

	state := "all of its replicas are ready"
	switch {
	case d.Desired == 0:
		state = "it is scaled to zero"
	case d.Ready == 0:
		state = "none of its replicas are ready"
	case d.Ready < d.Desired:
		state = fmt.Sprintf("only %d of %d replicas are ready", d.Ready, d.Desired)
	}

	out := []string{
		fmt.Sprintf("%s %s lives in namespace %s, created %s ago, and %s.",
			strings.Title(what), d.Meta.Name, d.Meta.Namespace, ago(now.Sub(d.Meta.CreationTimestamp.Time)), state),
	}
	if d.Strategy != "" {
		out[0] += fmt.Sprintf(" Updates roll out with the %s strategy.", d.Strategy)
	}
	out[0] += fmt.Sprintf(" Its pods run with %s quality of service.", QOSOf(d.Template))

	for i := range d.Template.Containers {
		container := &d.Template.Containers[i]
		text := fmt.Sprintf("Its containers run %s", or(container.Image, "an unnamed image"))
		if limits := resourceText(container.Resources.Limits); limits == "not set" {
			text += ", with no limits set"
		} else {
			text += ", limited to " + limits
		}
		out = append(out, "", text+".")
	}

	if len(pods) > 0 {
		out = append(out, "", fmt.Sprintf("It currently owns %d pods: %s.", len(pods), strings.Join(pods, ", ")))
	}
	return out
}

func QOSOf(spec corev1.PodSpec) string {
	if len(spec.Containers) == 0 {
		return "-"
	}

	guaranteed, any := true, false
	for i := range spec.Containers {
		resources := spec.Containers[i].Resources
		for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
			limit, hasLimit := resources.Limits[name]
			request, hasRequest := resources.Requests[name]

			if hasLimit || hasRequest {
				any = true
			}
			if !hasLimit || (hasRequest && limit.Cmp(request) != 0) {
				guaranteed = false
			}
		}
	}

	switch {
	case !any:
		return "BestEffort"
	case guaranteed:
		return "Guaranteed"
	}
	return "Burstable"
}

func ownerName(meta metav1.ObjectMeta) string {
	for _, owner := range meta.OwnerReferences {
		return owner.Kind + "/" + owner.Name
	}
	return ""
}

var _ = appsv1.Deployment{}
