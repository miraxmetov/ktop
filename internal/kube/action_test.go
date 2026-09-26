package kube

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestRestartRollsOutAWorkloadInstead(t *testing.T) {
	cases := []struct {
		kind     Kind
		resource string
		template func(*k8sfake.Clientset) map[string]string
	}{
		{KindDeployment, "deployments", func(cs *k8sfake.Clientset) map[string]string {
			out, err := cs.AppsV1().Deployments("production").Get(context.Background(), "api", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			return out.Spec.Template.Annotations
		}},
		{KindDaemonSet, "daemonsets", func(cs *k8sfake.Clientset) map[string]string {
			out, err := cs.AppsV1().DaemonSets("production").Get(context.Background(), "api", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			return out.Spec.Template.Annotations
		}},
		{KindStatefulSet, "statefulsets", func(cs *k8sfake.Clientset) map[string]string {
			out, err := cs.AppsV1().StatefulSets("production").Get(context.Background(), "api", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			return out.Spec.Template.Annotations
		}},
	}

	for _, c := range cases {
		cs := k8sfake.NewSimpleClientset(workloadObject(c.kind))
		client := NewWithClients(cs, metricsClient())

		if err := client.Restart(context.Background(), "production", c.kind, "api", []string{"api-abc-1"}); err != nil {
			t.Fatalf("%s: Restart: %v", c.kind, err)
		}

		if stamp := c.template(cs)[restartedAt]; stamp == "" {
			t.Errorf("%s: the pod template must carry a restart stamp", c.kind)
		}
		for _, action := range cs.Actions() {
			if action.GetVerb() == "delete" {
				t.Errorf("%s: a rollout must not delete anything", c.kind)
			}
			if action.GetVerb() == "patch" && action.GetResource().Resource != c.resource {
				t.Errorf("%s: patched %s instead", c.kind, action.GetResource().Resource)
			}
		}
	}
}

func TestRestartOfAReplicaSetRecreatesItsPods(t *testing.T) {
	cs := k8sfake.NewSimpleClientset(
		pod("api-abc-1", corev1.PodRunning, nil, corev1.ContainerStatus{Name: "app"}),
		pod("api-abc-2", corev1.PodRunning, nil, corev1.ContainerStatus{Name: "app"}),
	)
	client := NewWithClients(cs, metricsClient())

	err := client.Restart(context.Background(), "production", KindReplicaSet, "api-abc",
		[]string{"api-abc-1", "api-abc-2"})
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}

	for _, name := range []string{"api-abc-1", "api-abc-2"} {
		_, err := cs.CoreV1().Pods("production").Get(context.Background(), name, metav1.GetOptions{})
		if !apierrors.IsNotFound(err) {
			t.Errorf("%s must be gone, got %v", name, err)
		}
	}
	for _, grace := range deleteGraces(cs) {
		if grace != nil {
			t.Errorf("a restart deletes gracefully, got grace %d", *grace)
		}
	}
}

func TestTerminateForcesEveryPodOfAWorkload(t *testing.T) {
	cs := k8sfake.NewSimpleClientset(pod("api-abc-1", corev1.PodRunning, nil, corev1.ContainerStatus{Name: "app"}))
	client := NewWithClients(cs, metricsClient())

	if err := client.Terminate(context.Background(), "production", KindDeployment, "api", []string{"api-abc-1"}); err != nil {
		t.Fatalf("Terminate: %v", err)
	}

	graces := deleteGraces(cs)
	if len(graces) != 1 {
		t.Fatalf("one pod must be deleted, got %d", len(graces))
	}
	if graces[0] == nil || *graces[0] != 0 {
		t.Errorf("Terminate must not wait: %v", graces[0])
	}
}

func TestActionsOnAWorkloadWithoutPodsSaySo(t *testing.T) {
	client := NewWithClients(k8sfake.NewSimpleClientset(), metricsClient())

	err := client.Terminate(context.Background(), "production", KindDeployment, "api", nil)
	if err == nil || !strings.Contains(err.Error(), "owns no pods") {
		t.Fatalf("error: %v", err)
	}
}

func TestRestartReportsARefusalInPlainWords(t *testing.T) {
	cs := k8sfake.NewSimpleClientset(workloadObject(KindDeployment))
	cs.PrependReactor("patch", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Group: "apps", Resource: "deployments"}, "api", nil)
	})
	client := NewWithClients(cs, metricsClient())

	err := client.Restart(context.Background(), "production", KindDeployment, "api", nil)
	if err == nil || err.Error() != "no permission to restart deployment api" {
		t.Fatalf("error: %v", err)
	}
}

func workloadObject(kind Kind) runtime.Object {
	meta := metav1.ObjectMeta{Name: "api", Namespace: "production"}
	one := int32(1)

	switch kind {
	case KindDaemonSet:
		return &appsv1.DaemonSet{ObjectMeta: meta}
	case KindStatefulSet:
		return &appsv1.StatefulSet{ObjectMeta: meta, Spec: appsv1.StatefulSetSpec{Replicas: &one}}
	}
	return &appsv1.Deployment{ObjectMeta: meta, Spec: appsv1.DeploymentSpec{Replicas: &one}}
}

func deleteGraces(cs *k8sfake.Clientset) []*int64 {
	out := make([]*int64, 0, 2)
	for _, action := range cs.Actions() {
		if del, ok := action.(k8stesting.DeleteActionImpl); ok {
			out = append(out, del.DeleteOptions.GracePeriodSeconds)
		}
	}
	return out
}
