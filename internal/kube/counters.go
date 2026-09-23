package kube

import (
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

type podKey struct {
	namespace string
	uid       types.UID
}

type podCounter struct {
	containers map[string]int32
	restarts   int
	ooms       int
}

type counters struct {
	mu   sync.Mutex
	pods map[podKey]*podCounter
}

func newCounters() *counters {
	return &counters{pods: map[podKey]*podCounter{}}
}

func (c *counters) observe(namespace string, pods []corev1.Pod) map[types.UID]podCounter {
	c.mu.Lock()
	defer c.mu.Unlock()

	seen := make(map[podKey]bool, len(pods))
	observed := make(map[types.UID]podCounter, len(pods))

	for i := range pods {
		pod := &pods[i]
		key := podKey{namespace: namespace, uid: identity(pod)}
		seen[key] = true

		state := c.pods[key]
		if state == nil {
			state = &podCounter{containers: make(map[string]int32)}
			c.pods[key] = state
		}

		for _, cs := range pod.Status.ContainerStatuses {
			previous, known := state.containers[cs.Name]
			state.containers[cs.Name] = cs.RestartCount
			if !known || cs.RestartCount <= previous {
				continue
			}
			added := int(cs.RestartCount - previous)
			state.restarts += added
			if oomKilled(cs) {
				state.ooms += added
			}
		}
		observed[identity(pod)] = podCounter{restarts: state.restarts, ooms: state.ooms}
	}

	for key := range c.pods {
		if key.namespace == namespace && !seen[key] {
			delete(c.pods, key)
		}
	}
	return observed
}

func identity(pod *corev1.Pod) types.UID {
	if pod.UID != "" {
		return pod.UID
	}
	return types.UID(pod.Namespace + "/" + pod.Name)
}

func oomKilled(cs corev1.ContainerStatus) bool {
	if t := cs.LastTerminationState.Terminated; t != nil && t.Reason == "OOMKilled" {
		return true
	}
	if t := cs.State.Terminated; t != nil && t.Reason == "OOMKilled" {
		return true
	}
	return false
}
