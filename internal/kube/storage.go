package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type volumeStat struct {
	used     float64
	capacity float64
}

func (c *Client) claimRows(ctx context.Context, namespace string) (Result, error) {
	var result Result

	list, err := c.pods.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, errors.New(ExplainTraffic(err, KindVolumeClaim, namespace, c.Host))
	}
	stats := c.volumeStats(ctx, namespace)

	rows := make([]Row, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		row := blankRow(item.Name, item.CreationTimestamp.Time)
		row.Info = storageClassOf(item.Spec.StorageClassName) + ", " + claimSize(item)
		row.Status, row.Severity = claimStatus(item)

		if stat, ok := stats[item.Name]; ok && stat.capacity > 0 {
			row.HasMem = true
			row.Mem, row.MemLimit = stat.used/(1024*1024), stat.capacity/(1024*1024)
			row.MemPct = 100 * stat.used / stat.capacity
			row.Worst = row.MemPct
		}
		rows = append(rows, row)
	}

	Sort(rows)
	result.Rows = rows
	return result, nil
}

func claimStatus(item *corev1.PersistentVolumeClaim) (string, Severity) {
	switch item.Status.Phase {
	case corev1.ClaimBound:
		return "bound to " + item.Spec.VolumeName, Good
	case corev1.ClaimPending:
		return "pending, nothing has been provisioned", Warn
	case corev1.ClaimLost:
		return "lost, its volume is gone", Bad
	}
	return string(item.Status.Phase), Warn
}

func claimSize(item *corev1.PersistentVolumeClaim) string {
	if size, ok := item.Status.Capacity[corev1.ResourceStorage]; ok {
		return size.String()
	}
	if size, ok := item.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
		return size.String() + " asked for"
	}
	return "no size"
}

func storageClassOf(name *string) string {
	if name == nil || *name == "" {
		return "no class"
	}
	return *name
}

func (c *Client) volumeRows(ctx context.Context) (Result, error) {
	var result Result

	list, err := c.pods.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, errors.New(ExplainCluster(err, KindVolume, c.Host))
	}
	stats := c.volumeStats(ctx, "")

	rows := make([]Row, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		row := blankRow(item.Name, item.CreationTimestamp.Time)
		row.Info = storageClassOf(&item.Spec.StorageClassName) + ", " + volumeSize(item)
		row.Status, row.Severity = volumeStatus(item)

		if item.Spec.ClaimRef != nil {
			if stat, ok := stats[item.Spec.ClaimRef.Name]; ok && stat.capacity > 0 {
				row.HasMem = true
				row.Mem, row.MemLimit = stat.used/(1024*1024), stat.capacity/(1024*1024)
				row.MemPct = 100 * stat.used / stat.capacity
				row.Worst = row.MemPct
			}
		}
		rows = append(rows, row)
	}

	Sort(rows)
	result.Rows = rows
	return result, nil
}

func volumeStatus(item *corev1.PersistentVolume) (string, Severity) {
	switch item.Status.Phase {
	case corev1.VolumeBound:
		if item.Spec.ClaimRef != nil {
			return "bound to " + item.Spec.ClaimRef.Namespace + "/" + item.Spec.ClaimRef.Name, Good
		}
		return "bound", Good
	case corev1.VolumeAvailable:
		return "available, nothing claims it", Muted
	case corev1.VolumeReleased:
		return "released, its claim is gone", Warn
	case corev1.VolumeFailed:
		return "failed, " + or(item.Status.Reason, "the volume cannot be used"), Bad
	case corev1.VolumePending:
		return "pending", Warn
	}
	return string(item.Status.Phase), Warn
}

func volumeSize(item *corev1.PersistentVolume) string {
	if size, ok := item.Spec.Capacity[corev1.ResourceStorage]; ok {
		return size.String()
	}
	return "no size"
}

func (c *Client) storageClassRows(ctx context.Context) (Result, error) {
	var result Result

	list, err := c.pods.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, errors.New(ExplainCluster(err, KindStorageClass, c.Host))
	}
	claims, _ := c.pods.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})

	rows := make([]Row, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		row := blankRow(item.Name, item.CreationTimestamp.Time)
		row.Info = item.Provisioner

		used := 0
		if claims != nil {
			for j := range claims.Items {
				if storageClassOf(claims.Items[j].Spec.StorageClassName) == item.Name {
					used++
				}
			}
		}
		row.Ready, row.Desired = used, used
		row.Status, row.Severity = fmt.Sprintf("%s claimed", plural(used, "volume")), Good

		switch {
		case used == 0:
			row.Status, row.Severity = "nothing uses it", Muted
		case item.Annotations["storageclass.kubernetes.io/is-default-class"] == "true":
			row.Status = row.Status + ", the default class"
		}
		rows = append(rows, row)
	}

	Sort(rows)
	result.Rows = rows
	return result, nil
}

func (c *Client) volumeStats(ctx context.Context, namespace string) map[string]volumeStat {
	out := map[string]volumeStat{}

	pods, err := c.pods.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return out
	}

	nodes := map[string]bool{}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Spec.NodeName == "" {
			continue
		}
		for _, volume := range pod.Spec.Volumes {
			if volume.PersistentVolumeClaim != nil {
				nodes[pod.Spec.NodeName] = true
				break
			}
		}
	}

	for node := range nodes {
		body, err := c.pods.CoreV1().RESTClient().Get().
			Resource("nodes").Name(node).SubResource("proxy").
			Suffix("stats", "summary").DoRaw(ctx)
		if err != nil {
			continue
		}

		var summary struct {
			Pods []struct {
				VolumeStats []struct {
					Name          string `json:"name"`
					UsedBytes     int64  `json:"usedBytes"`
					CapacityBytes int64  `json:"capacityBytes"`
					PVCRef        struct {
						Name      string `json:"name"`
						Namespace string `json:"namespace"`
					} `json:"pvcRef"`
				} `json:"volume"`
			} `json:"pods"`
		}
		if err := json.Unmarshal(body, &summary); err != nil {
			continue
		}

		for _, pod := range summary.Pods {
			for _, volume := range pod.VolumeStats {
				name := volume.PVCRef.Name
				if name == "" {
					continue
				}
				if namespace != "" && volume.PVCRef.Namespace != namespace {
					continue
				}
				out[name] = volumeStat{used: float64(volume.UsedBytes), capacity: float64(volume.CapacityBytes)}
			}
		}
	}
	return out
}

func gigabytes(value resource.Quantity) float64 {
	return float64(value.Value()) / (1024 * 1024 * 1024)
}

func accessModes(modes []corev1.PersistentVolumeAccessMode) string {
	parts := make([]string, 0, len(modes))
	for _, mode := range modes {
		parts = append(parts, string(mode))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}
