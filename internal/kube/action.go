package kube

import (
	"context"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (c *Client) Restart(ctx context.Context, namespace string, kind Kind, name string, pods []string) error {
	switch kind {
	case KindPod:
		return c.DeletePod(ctx, namespace, name, false)
	case KindReplicaSet:
		return c.recreatePods(ctx, namespace, kind, name, pods, false)
	}

	patch := []byte(fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{%q:%q}}}}}`,
		restartedAt, time.Now().Format(time.RFC3339)))

	apps := c.pods.AppsV1()
	var err error

	switch kind {
	case KindDeployment:
		_, err = apps.Deployments(namespace).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	case KindDaemonSet:
		_, err = apps.DaemonSets(namespace).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	case KindStatefulSet:
		_, err = apps.StatefulSets(namespace).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	}
	if err != nil {
		return fmt.Errorf("%s", ExplainRestart(err, namespace, kind, name, c.Host))
	}
	return nil
}

func (c *Client) Terminate(ctx context.Context, namespace string, kind Kind, name string, pods []string) error {
	if kind == KindPod {
		return c.DeletePod(ctx, namespace, name, true)
	}
	return c.recreatePods(ctx, namespace, kind, name, pods, true)
}

func (c *Client) recreatePods(ctx context.Context, namespace string, kind Kind, name string, pods []string, force bool) error {
	if len(pods) == 0 {
		return fmt.Errorf("%s %s owns no pods right now", singular(kind), name)
	}
	for _, pod := range pods {
		if err := c.DeletePod(ctx, namespace, pod, force); err != nil {
			return err
		}
	}
	return nil
}

const restartedAt = "kubectl.kubernetes.io/restartedAt"

func singular(kind Kind) string {
	return strings.ToLower(strings.TrimSuffix(kind.String(), "s"))
}
