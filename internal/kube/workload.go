package kube

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Kind int

const (
	KindPod Kind = iota
	KindDeployment
	KindReplicaSet
	KindDaemonSet
	KindStatefulSet
)

var kindNames = map[Kind]string{
	KindPod:         "Pods",
	KindDeployment:  "Deployments",
	KindReplicaSet:  "ReplicaSets",
	KindDaemonSet:   "DaemonSets",
	KindStatefulSet: "StatefulSets",
}

var kindColumns = map[Kind]string{
	KindPod:         "POD",
	KindDeployment:  "DEPLOYMENT",
	KindReplicaSet:  "REPLICASET",
	KindDaemonSet:   "DAEMONSET",
	KindStatefulSet: "STATEFULSET",
}

func Kinds() []Kind {
	return []Kind{KindPod, KindDeployment, KindReplicaSet, KindDaemonSet, KindStatefulSet}
}

func (k Kind) String() string {
	return kindNames[k]
}

func (k Kind) Column() string {
	return kindColumns[k]
}

func KindByName(name string) (Kind, bool) {
	for kind, text := range kindNames {
		if text == name {
			return kind, true
		}
	}
	return KindPod, false
}

type workload struct {
	name     string
	ready    int32
	desired  int32
	created  time.Time
	severity Severity
	status   string
}

func (c *Client) workloads(ctx context.Context, namespace string, kind Kind) ([]workload, error) {
	apps := c.pods.AppsV1()

	switch kind {
	case KindDeployment:
		list, err := apps.Deployments(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		out := make([]workload, 0, len(list.Items))
		for i := range list.Items {
			item := &list.Items[i]
			out = append(out, newWorkload(item.Name, item.Status.ReadyReplicas,
				replicas(item.Spec.Replicas), item.CreationTimestamp.Time))
		}
		return out, nil

	case KindReplicaSet:
		list, err := apps.ReplicaSets(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		out := make([]workload, 0, len(list.Items))
		for i := range list.Items {
			item := &list.Items[i]
			if replicas(item.Spec.Replicas) == 0 && item.Status.Replicas == 0 {
				continue
			}
			out = append(out, newWorkload(item.Name, item.Status.ReadyReplicas,
				replicas(item.Spec.Replicas), item.CreationTimestamp.Time))
		}
		return out, nil

	case KindDaemonSet:
		list, err := apps.DaemonSets(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		out := make([]workload, 0, len(list.Items))
		for i := range list.Items {
			item := &list.Items[i]
			out = append(out, newWorkload(item.Name, item.Status.NumberReady,
				item.Status.DesiredNumberScheduled, item.CreationTimestamp.Time))
		}
		return out, nil

	case KindStatefulSet:
		list, err := apps.StatefulSets(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		out := make([]workload, 0, len(list.Items))
		for i := range list.Items {
			item := &list.Items[i]
			out = append(out, newWorkload(item.Name, item.Status.ReadyReplicas,
				replicas(item.Spec.Replicas), item.CreationTimestamp.Time))
		}
		return out, nil
	}
	return nil, nil
}

func newWorkload(name string, ready, desired int32, created time.Time) workload {
	w := workload{name: name, ready: ready, desired: desired, created: created}
	w.status = fmt.Sprintf("%d/%d ready", ready, desired)

	switch {
	case desired == 0:
		w.severity = Muted
		w.status = "scaled to zero"
	case ready == 0:
		w.severity = Bad
	case ready < desired:
		w.severity = Warn
	default:
		w.severity = Good
	}
	return w
}

func replicas(value *int32) int32 {
	if value == nil {
		return 1
	}
	return *value
}

func (c *Client) ownerIndex(ctx context.Context, namespace string, kind Kind) (map[string]string, error) {
	if kind != KindDeployment {
		return nil, nil
	}

	list, err := c.pods.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	index := make(map[string]string, len(list.Items))
	for i := range list.Items {
		set := &list.Items[i]
		for _, owner := range set.OwnerReferences {
			if owner.Kind == "Deployment" {
				index[set.Name] = owner.Name
			}
		}
	}
	return index, nil
}

func ownerOf(pod *corev1.Pod, kind Kind, replicaSets map[string]string) string {
	for _, owner := range pod.OwnerReferences {
		switch kind {
		case KindReplicaSet:
			if owner.Kind == "ReplicaSet" {
				return owner.Name
			}
		case KindDaemonSet:
			if owner.Kind == "DaemonSet" {
				return owner.Name
			}
		case KindStatefulSet:
			if owner.Kind == "StatefulSet" {
				return owner.Name
			}
		case KindDeployment:
			if owner.Kind == "ReplicaSet" {
				return replicaSets[owner.Name]
			}
		}
	}
	return ""
}

func aggregate(workloads []workload, pods []Row, owners map[string][]int) []Row {
	rows := make([]Row, 0, len(workloads))

	for _, w := range workloads {
		row := Row{
			Name:     w.name,
			Status:   w.status,
			Severity: w.severity,
			Created:  w.created,
			Ready:    int(w.ready),
			Desired:  int(w.desired),
			CPUPct:   -1,
			MemPct:   -1,
			Worst:    -1,
		}

		cpuLimited, memLimited := true, true
		for _, index := range owners[w.name] {
			pod := pods[index]
			row.Pods = append(row.Pods, pod.Name)
			row.Restarts += pod.Restarts
			row.NewRestarts += pod.NewRestarts
			row.OOMs += pod.OOMs

			if pod.HasCPU {
				row.HasCPU = true
				row.CPU += pod.CPU
			}
			if pod.HasMem {
				row.HasMem = true
				row.Mem += pod.Mem
			}
			if pod.CPULimit > 0 {
				row.CPULimit += pod.CPULimit
			} else {
				cpuLimited = false
			}
			if pod.MemLimit > 0 {
				row.MemLimit += pod.MemLimit
			} else {
				memLimited = false
			}
			if pod.LastRestart.After(row.LastRestart) {
				row.LastRestart = pod.LastRestart
			}
		}

		if len(row.Pods) == 0 || !cpuLimited {
			row.CPULimit = 0
		}
		if len(row.Pods) == 0 || !memLimited {
			row.MemLimit = 0
		}
		if row.HasCPU && row.CPULimit > 0 {
			row.CPUPct = 100 * row.CPU / row.CPULimit
		}
		if row.HasMem && row.MemLimit > 0 {
			row.MemPct = 100 * row.Mem / row.MemLimit
		}
		if row.CPUPct > row.Worst {
			row.Worst = row.CPUPct
		}
		if row.MemPct > row.Worst {
			row.Worst = row.MemPct
		}
		rows = append(rows, row)
	}
	return rows
}
