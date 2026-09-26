package kube

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

func (c *Client) Pod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	pod, err := c.pods.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("%s", ExplainPod(err, namespace, name, c.Host))
	}
	return pod, nil
}

func (c *Client) DeletePod(ctx context.Context, namespace, name string, force bool) error {
	options := metav1.DeleteOptions{}
	if force {
		grace := int64(0)
		options.GracePeriodSeconds = &grace
	}
	if err := c.pods.CoreV1().Pods(namespace).Delete(ctx, name, options); err != nil {
		return fmt.Errorf("%s", ExplainPod(err, namespace, name, c.Host))
	}
	return nil
}

func YAML(pod *corev1.Pod) ([]string, error) {
	clean := pod.DeepCopy()
	clean.ManagedFields = nil
	delete(clean.Annotations, "kubectl.kubernetes.io/last-applied-configuration")
	if len(clean.Annotations) == 0 {
		clean.Annotations = nil
	}

	body, err := yaml.Marshal(clean)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(body), "\n"), "\n"), nil
}

func Describe(pod *corev1.Pod, now time.Time) []string {
	out := []string{"POD"}
	status, _ := podStatus(pod)
	ready, total := readyCount(pod)

	out = append(out,
		field("name", pod.Name),
		field("namespace", pod.Namespace),
		field("status", fmt.Sprintf("%s (%d/%d ready)", status, ready, total)),
		field("node", or(pod.Spec.NodeName, "not scheduled")),
		field("pod ip", or(pod.Status.PodIP, "-")),
		field("created", timestamp(pod.CreationTimestamp.Time, now)),
		field("QoS class", or(string(pod.Status.QOSClass), "-")),
		field("service account", or(pod.Spec.ServiceAccountName, "-")),
		field("restart policy", string(pod.Spec.RestartPolicy)),
	)
	if owner := ownerText(pod); owner != "" {
		out = append(out, field("owner", owner))
	}
	if pod.DeletionTimestamp != nil {
		out = append(out, field("deleting since", timestamp(pod.DeletionTimestamp.Time, now)))
	}

	if len(pod.Labels) > 0 {
		out = append(out, "", "LABELS")
		out = append(out, pairs(pod.Labels)...)
	}
	if annotations := withoutNoise(pod.Annotations); len(annotations) > 0 {
		out = append(out, "", "ANNOTATIONS")
		out = append(out, pairs(annotations)...)
	}

	out = append(out, "", "CONTAINERS")
	statuses := map[string]corev1.ContainerStatus{}
	for _, cs := range pod.Status.ContainerStatuses {
		statuses[cs.Name] = cs
	}
	for i := range pod.Spec.Containers {
		out = append(out, containerLines(&pod.Spec.Containers[i], statuses[pod.Spec.Containers[i].Name], now)...)
	}

	if len(pod.Status.Conditions) > 0 {
		out = append(out, "", "CONDITIONS")
		for _, condition := range pod.Status.Conditions {
			text := string(condition.Status)
			if condition.Reason != "" {
				text += " (" + condition.Reason + ")"
			}
			out = append(out, field(string(condition.Type), text))
		}
	}

	if len(pod.Spec.Volumes) > 0 {
		out = append(out, "", "VOLUMES")
		for _, volume := range pod.Spec.Volumes {
			out = append(out, field(volume.Name, volumeKind(volume)))
		}
	}
	return out
}

func containerLines(spec *corev1.Container, status corev1.ContainerStatus, now time.Time) []string {
	out := []string{"  " + spec.Name}
	out = append(out,
		subfield("image", spec.Image),
		subfield("state", containerState(status, now)),
		subfield("restarts", fmt.Sprintf("%d", status.RestartCount)),
		subfield("requests", resourceText(spec.Resources.Requests)),
		subfield("limits", resourceText(spec.Resources.Limits)),
	)
	if len(spec.Ports) > 0 {
		ports := make([]string, 0, len(spec.Ports))
		for _, port := range spec.Ports {
			ports = append(ports, fmt.Sprintf("%d/%s", port.ContainerPort, or(string(port.Protocol), "TCP")))
		}
		out = append(out, subfield("ports", strings.Join(ports, ", ")))
	}
	if len(spec.VolumeMounts) > 0 {
		mounts := make([]string, 0, len(spec.VolumeMounts))
		for _, mount := range spec.VolumeMounts {
			mounts = append(mounts, mount.MountPath)
		}
		out = append(out, subfield("mounts", strings.Join(mounts, ", ")))
	}
	if len(spec.Env) > 0 {
		out = append(out, subfield("env", fmt.Sprintf("%d variables", len(spec.Env))))
	}
	return out
}

func containerState(status corev1.ContainerStatus, now time.Time) string {
	switch {
	case status.State.Running != nil:
		return "running since " + timestamp(status.State.Running.StartedAt.Time, now)
	case status.State.Waiting != nil:
		text := "waiting: " + status.State.Waiting.Reason
		if status.State.Waiting.Message != "" {
			text += " (" + firstLine(status.State.Waiting.Message) + ")"
		}
		return text
	case status.State.Terminated != nil:
		terminated := status.State.Terminated
		return fmt.Sprintf("terminated: %s, exit %d, %s",
			or(terminated.Reason, "-"), terminated.ExitCode, timestamp(terminated.FinishedAt.Time, now))
	}
	return "-"
}

func resourceText(list corev1.ResourceList) string {
	if len(list) == 0 {
		return "not set"
	}
	parts := make([]string, 0, len(list))
	if cpu, ok := list[corev1.ResourceCPU]; ok {
		parts = append(parts, "cpu "+cpu.String())
	}
	if mem, ok := list[corev1.ResourceMemory]; ok {
		parts = append(parts, "memory "+mem.String())
	}
	for name, quantity := range list {
		if name == corev1.ResourceCPU || name == corev1.ResourceMemory {
			continue
		}
		parts = append(parts, string(name)+" "+quantity.String())
	}
	return strings.Join(parts, ", ")
}

func volumeKind(volume corev1.Volume) string {
	switch {
	case volume.ConfigMap != nil:
		return "configMap " + volume.ConfigMap.Name
	case volume.Secret != nil:
		return "secret " + volume.Secret.SecretName
	case volume.PersistentVolumeClaim != nil:
		return "pvc " + volume.PersistentVolumeClaim.ClaimName
	case volume.EmptyDir != nil:
		return "emptyDir"
	case volume.HostPath != nil:
		return "hostPath " + volume.HostPath.Path
	case volume.Projected != nil:
		return "projected"
	}
	return "-"
}

func ownerText(pod *corev1.Pod) string {
	for _, owner := range pod.OwnerReferences {
		return owner.Kind + "/" + owner.Name
	}
	return ""
}

func readyCount(pod *corev1.Pod) (int, int) {
	ready := 0
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Ready {
			ready++
		}
	}
	return ready, len(pod.Status.ContainerStatuses)
}

func withoutNoise(annotations map[string]string) map[string]string {
	if len(annotations) == 0 {
		return nil
	}
	out := make(map[string]string, len(annotations))
	for key, value := range annotations {
		if key == "kubectl.kubernetes.io/last-applied-configuration" {
			continue
		}
		out[key] = value
	}
	return out
}

func pairs(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, field(key, firstLine(values[key])))
	}
	return out
}

func field(name, value string) string {
	return fmt.Sprintf("  %-18s %s", name, value)
}

func subfield(name, value string) string {
	return fmt.Sprintf("    %-16s %s", name, value)
}

func timestamp(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return fmt.Sprintf("%s (%s ago)", t.Local().Format("2006-01-02 15:04:05"), ago(now.Sub(t)))
}

func ago(d time.Duration) string {
	seconds := int(d.Seconds())
	if seconds < 0 {
		seconds = 0
	}
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm%ds", seconds/60, seconds%60)
	case seconds < 86400:
		return fmt.Sprintf("%dh%dm", seconds/3600, (seconds%3600)/60)
	}
	return fmt.Sprintf("%dd%dh", seconds/86400, (seconds%86400)/3600)
}

func FirstContainer(pod *corev1.Pod) string {
	if len(pod.Spec.Containers) == 0 {
		return ""
	}
	return pod.Spec.Containers[0].Name
}

func (c *Client) FollowLogs(ctx context.Context, namespace, pod, container string, tail int64) (io.ReadCloser, error) {
	request := c.pods.CoreV1().Pods(namespace).GetLogs(pod, &corev1.PodLogOptions{
		Container:  container,
		Follow:     true,
		TailLines:  &tail,
		Timestamps: true,
	})
	stream, err := request.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s", ExplainLogs(err, pod, container))
	}
	return stream, nil
}

func SplitLogLine(line string) (string, string) {
	stamp, rest, found := strings.Cut(line, " ")
	if !found {
		return "", line
	}
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return "", line
	}
	return at.Local().Format("15:04:05"), rest
}

func Textual(pod *corev1.Pod, now time.Time) []string {
	status, _ := podStatus(pod)
	ready, total := readyCount(pod)

	where := "is not scheduled yet"
	if pod.Spec.NodeName != "" {
		where = "runs on node " + pod.Spec.NodeName
		if pod.Status.PodIP != "" {
			where += " with address " + pod.Status.PodIP
		}
	}

	born := "of unknown age"
	if !pod.CreationTimestamp.IsZero() {
		born = "created " + ago(now.Sub(pod.CreationTimestamp.Time)) + " ago"
	}
	if owner := ownerText(pod); owner != "" {
		born += " by " + owner
	}

	out := []string{
		fmt.Sprintf("Pod %s lives in namespace %s, %s and %s.", pod.Name, pod.Namespace, born, where),
		fmt.Sprintf("It is %s with %d of %d containers ready, quality of service %s, restart policy %s.",
			status, ready, total, or(string(pod.Status.QOSClass), "unknown"),
			strings.ToLower(or(string(pod.Spec.RestartPolicy), "always"))),
	}

	statuses := map[string]corev1.ContainerStatus{}
	for _, cs := range pod.Status.ContainerStatuses {
		statuses[cs.Name] = cs
	}
	for i := range pod.Spec.Containers {
		out = append(out, "", containerSentence(&pod.Spec.Containers[i], statuses[pod.Spec.Containers[i].Name], now))
	}

	if problems := problemSentence(pod); problems != "" {
		out = append(out, "", problems)
	}
	if len(pod.Labels) > 0 {
		out = append(out, "", "Labels: "+joinPairs(pod.Labels)+".")
	}
	return out
}

func containerSentence(spec *corev1.Container, status corev1.ContainerStatus, now time.Time) string {
	text := fmt.Sprintf("Container %s runs %s", spec.Name, or(spec.Image, "an unnamed image"))

	switch {
	case status.State.Running != nil:
		text += ", up for " + ago(now.Sub(status.State.Running.StartedAt.Time))
	case status.State.Waiting != nil:
		text += ", waiting because of " + or(status.State.Waiting.Reason, "an unknown reason")
	case status.State.Terminated != nil:
		text += fmt.Sprintf(", terminated with code %d (%s)",
			status.State.Terminated.ExitCode, or(status.State.Terminated.Reason, "no reason given"))
	}

	if status.RestartCount == 1 {
		text += ", restarted once"
	} else if status.RestartCount > 1 {
		text += fmt.Sprintf(", restarted %d times", status.RestartCount)
	}

	limits := resourceText(spec.Resources.Limits)
	if limits == "not set" {
		text += ", with no limits set"
	} else {
		text += ", limited to " + limits
	}
	if requests := resourceText(spec.Resources.Requests); requests != "not set" {
		text += " and asking for " + requests
	}
	return text + "."
}

func problemSentence(pod *corev1.Pod) string {
	troubles := make([]string, 0, 2)
	for _, cs := range pod.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil && w.Reason != "" {
			troubles = append(troubles, cs.Name+" is stuck in "+w.Reason)
		}
		if t := cs.LastTerminationState.Terminated; t != nil && t.Reason == "OOMKilled" {
			troubles = append(troubles, cs.Name+" was killed for using too much memory")
		}
	}
	if len(troubles) == 0 {
		return ""
	}
	return "Worth a look: " + strings.Join(troubles, ", ") + "."
}

func joinPairs(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+values[key])
	}
	return strings.Join(parts, ", ")
}
