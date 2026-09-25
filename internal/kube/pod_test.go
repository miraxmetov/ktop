package kube

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func inspectable(now time.Time) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "api-worker-1",
			Namespace:         "production",
			CreationTimestamp: metav1.NewTime(now.Add(-90 * time.Minute)),
			Labels:            map[string]string{"app": "api", "release": "v12"},
			Annotations: map[string]string{
				"kubectl.kubernetes.io/last-applied-configuration": "{...}",
				"vault.hashicorp.com/agent-inject":                 "true",
			},
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "api-worker"}},
			ManagedFields:   []metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
		},
		Spec: corev1.PodSpec{
			NodeName:           "worker-02",
			ServiceAccountName: "api",
			RestartPolicy:      corev1.RestartPolicyAlways,
			Containers: []corev1.Container{{
				Name:  "app",
				Image: "registry.example.com/api:1.4.2",
				Ports: []corev1.ContainerPort{{ContainerPort: 8000, Protocol: corev1.ProtocolTCP}},
				Env:   []corev1.EnvVar{{Name: "A"}, {Name: "B"}},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
				},
				VolumeMounts: []corev1.VolumeMount{{Name: "config", MountPath: "/etc/app"}},
			}},
			Volumes: []corev1.Volume{{
				Name:         "config",
				VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{}},
			}},
		},
		Status: corev1.PodStatus{
			Phase:     corev1.PodRunning,
			PodIP:     "10.42.3.17",
			QOSClass:  corev1.PodQOSBurstable,
			StartTime: &metav1.Time{Time: now.Add(-90 * time.Minute)},
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:         "app",
				Ready:        true,
				RestartCount: 3,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{
					StartedAt: metav1.NewTime(now.Add(-20 * time.Minute)),
				}},
			}},
		},
	}
}

func TestDescribeReadsLikeProse(t *testing.T) {
	now := time.Now()
	text := strings.Join(Describe(inspectable(now), now), "\n")

	for _, want := range []string{
		"POD", "name", "api-worker-1", "namespace", "production", "status", "Running (1/1 ready)",
		"node", "worker-02", "pod ip", "10.42.3.17", "qos class", "Burstable",
		"owner", "ReplicaSet/api-worker",
		"LABELS", "app", "release",
		"ANNOTATIONS", "vault.hashicorp.com/agent-inject",
		"CONTAINERS", "registry.example.com/api:1.4.2", "running since", "restarts", "3",
		"requests", "cpu 100m", "limits", "not set", "ports", "8000/TCP", "mounts", "/etc/app",
		"env", "2 variables",
		"CONDITIONS", "Ready", "True",
		"VOLUMES", "configMap",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("description misses %q:\n%s", want, text)
		}
	}

	if strings.Contains(text, "last-applied-configuration") {
		t.Error("the noisy annotation must be dropped")
	}
	if strings.Contains(text, "apiVersion") {
		t.Error("the readable form is not yaml")
	}
}

func TestYAMLIsCleanedUp(t *testing.T) {
	now := time.Now()
	lines, err := YAML(inspectable(now))
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	text := strings.Join(lines, "\n")

	if !strings.Contains(text, "name: api-worker-1") || !strings.Contains(text, "image: registry.example.com/api:1.4.2") {
		t.Errorf("yaml must carry the pod:\n%s", text)
	}
	if strings.Contains(text, "managedFields") {
		t.Error("managedFields must be stripped")
	}
	if strings.Contains(text, "last-applied-configuration") {
		t.Error("the noisy annotation must be stripped")
	}
}

func TestPodFetchesByName(t *testing.T) {
	now := time.Now()
	client := NewWithClients(k8sfake.NewSimpleClientset(inspectable(now)), metricsClient())

	pod, err := client.Pod(context.Background(), "production", "api-worker-1")
	if err != nil {
		t.Fatalf("Pod: %v", err)
	}
	if pod.Name != "api-worker-1" {
		t.Errorf("got %q", pod.Name)
	}

	if _, err := client.Pod(context.Background(), "production", "gone"); err == nil {
		t.Error("a missing pod must be reported")
	} else if strings.Contains(err.Error(), "&") {
		t.Errorf("the error must stay readable: %v", err)
	}
}

func TestTextualReadsAsSentences(t *testing.T) {
	now := time.Now()
	pod := inspectable(now)
	pod.Status.ContainerStatuses[0].LastTerminationState = corev1.ContainerState{
		Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137},
	}

	text := strings.Join(Textual(pod, now), " ")

	for _, want := range []string{
		"Pod api-worker-1 lives in namespace production",
		"created 1h30m ago by ReplicaSet/api-worker",
		"runs on node worker-02 with address 10.42.3.17",
		"It is Running with 1 of 1 containers ready",
		"quality of service Burstable",
		"restart policy always",
		"Container app runs registry.example.com/api:1.4.2",
		"restarted 3 times",
		"with no limits set",
		"asking for cpu 100m",
		"killed for using too much memory",
		"Labels: app=api, release=v12",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("prose misses %q:\n%s", want, text)
		}
	}

	if strings.Contains(text, "apiVersion") || strings.Contains(text, "  name  ") {
		t.Error("the textual form is neither yaml nor a field list")
	}
	if lines := Textual(pod, now); len(lines) > 12 {
		t.Errorf("the prose must stay short, got %d lines", len(lines))
	}
}

func TestFirstContainer(t *testing.T) {
	now := time.Now()
	if got := FirstContainer(inspectable(now)); got != "app" {
		t.Errorf("got %q", got)
	}
	if got := FirstContainer(&corev1.Pod{}); got != "" {
		t.Errorf("a pod without containers has none, got %q", got)
	}
}

func TestFollowLogsReadsTheStream(t *testing.T) {
	now := time.Now()
	client := NewWithClients(k8sfake.NewSimpleClientset(inspectable(now)), metricsClient())

	stream, err := client.FollowLogs(context.Background(), "production", "api-worker-1", "app", 10)
	if err != nil {
		t.Fatalf("FollowLogs: %v", err)
	}
	defer stream.Close()

	body := make([]byte, 64)
	n, _ := stream.Read(body)
	if n == 0 {
		t.Error("the stream must carry something")
	}
}

func TestSplitLogLine(t *testing.T) {
	stamp, text := SplitLogLine("2026-09-25T18:04:11.123456789Z level=info msg=\"served\"")
	if stamp == "" {
		t.Fatalf("a timestamped line must give its time, got %q / %q", stamp, text)
	}
	if text != `level=info msg="served"` {
		t.Errorf("message: %q", text)
	}

	stamp, text = SplitLogLine("plain line without a stamp")
	if stamp != "" || text != "plain line without a stamp" {
		t.Errorf("an unstamped line passes through: %q / %q", stamp, text)
	}

	stamp, text = SplitLogLine("")
	if stamp != "" || text != "" {
		t.Errorf("empty stays empty: %q / %q", stamp, text)
	}
}
