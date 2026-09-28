package kube

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Gauge struct {
	Used  float64
	Total float64
	Label string
	Note  string
}

func (g *Gauge) Percent() float64 {
	if g == nil || g.Total <= 0 {
		return -1
	}
	return 100 * g.Used / g.Total
}

func (c *Client) BrowseObject(ctx context.Context, kind Kind, namespace, name string) (*TrafficDetail, error) {
	if kind.Group() == GroupStorage {
		return c.storageObject(ctx, kind, namespace, name)
	}
	return c.TrafficObject(ctx, kind, namespace, name)
}

func (c *Client) storageObject(ctx context.Context, kind Kind, namespace, name string) (*TrafficDetail, error) {
	core := c.pods.CoreV1()
	options := metav1.GetOptions{}

	switch kind {
	case KindVolumeClaim:
		item, err := core.PersistentVolumeClaims(namespace).Get(ctx, name, options)
		if err != nil {
			return nil, fmt.Errorf("%s", ExplainWorkload(err, namespace, kind, name, c.Host))
		}
		gauge := claimGauge(item, c.volumeStats(ctx, namespace)[name])
		detail := &TrafficDetail{Kind: kind, Meta: item.ObjectMeta, object: item,
			notes: claimNotes(item, gauge), gauge: gauge}
		detail.body = func(now time.Time) []string { return describeClaim(item, now) }
		detail.prose = func(now time.Time) []string { return tellClaim(item, gauge, now) }
		return detail, nil

	case KindVolume:
		item, err := core.PersistentVolumes().Get(ctx, name, options)
		if err != nil {
			return nil, fmt.Errorf("%s", ExplainWorkload(err, "", kind, name, c.Host))
		}
		var stat volumeStat
		if item.Spec.ClaimRef != nil {
			stat = c.volumeStats(ctx, item.Spec.ClaimRef.Namespace)[item.Spec.ClaimRef.Name]
		}
		gauge := volumeGauge(item, stat)
		detail := &TrafficDetail{Kind: kind, Meta: item.ObjectMeta, object: item,
			notes: volumeNotes(item, gauge), gauge: gauge}
		detail.body = func(now time.Time) []string { return describeVolume(item, now) }
		detail.prose = func(now time.Time) []string { return tellVolume(item, gauge, now) }
		return detail, nil

	case KindStorageClass:
		item, err := c.pods.StorageV1().StorageClasses().Get(ctx, name, options)
		if err != nil {
			return nil, fmt.Errorf("%s", ExplainWorkload(err, "", kind, name, c.Host))
		}
		claims := c.claimsOfClass(ctx, name)
		detail := &TrafficDetail{Kind: kind, Meta: item.ObjectMeta, object: item,
			notes: classNotes(item, claims)}
		detail.body = func(now time.Time) []string { return describeClass(item, claims, now) }
		detail.prose = func(now time.Time) []string { return tellClass(item, claims, now) }
		return detail, nil
	}
	return nil, errors.New("this kind cannot be inspected")
}

func (c *Client) claimsOfClass(ctx context.Context, class string) []string {
	list, err := c.pods.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}

	out := make([]string, 0, 8)
	for i := range list.Items {
		item := &list.Items[i]
		if storageClassOf(item.Spec.StorageClassName) == class {
			out = append(out, item.Namespace+"/"+item.Name)
		}
	}
	return out
}

func claimGauge(item *corev1.PersistentVolumeClaim, stat volumeStat) *Gauge {
	if stat.capacity > 0 {
		return &Gauge{
			Used:  stat.used,
			Total: stat.capacity,
			Label: fmt.Sprintf("%.1f of %.1f GiB", stat.used/(1024*1024*1024), stat.capacity/(1024*1024*1024)),
			Note:  "written on the node",
		}
	}

	size, ok := item.Status.Capacity[corev1.ResourceStorage]
	if !ok {
		return nil
	}
	asked := item.Spec.Resources.Requests[corev1.ResourceStorage]
	return &Gauge{
		Used:  float64(asked.Value()),
		Total: float64(size.Value()),
		Label: fmt.Sprintf("%.1f of %.1f GiB", gigabytes(asked), gigabytes(size)),
		Note:  "asked for, the node reports nothing",
	}
}

func volumeGauge(item *corev1.PersistentVolume, stat volumeStat) *Gauge {
	size, ok := item.Spec.Capacity[corev1.ResourceStorage]
	if !ok {
		return nil
	}

	if stat.capacity > 0 {
		return &Gauge{
			Used:  stat.used,
			Total: stat.capacity,
			Label: fmt.Sprintf("%.1f of %.1f GiB", stat.used/(1024*1024*1024), stat.capacity/(1024*1024*1024)),
			Note:  "written on the node",
		}
	}

	used := 0.0
	note := "nothing claims it yet"
	if item.Spec.ClaimRef != nil {
		used = float64(size.Value())
		note = "claimed whole, the node reports nothing"
	}
	return &Gauge{
		Used:  used,
		Total: float64(size.Value()),
		Label: fmt.Sprintf("%.1f GiB", gigabytes(size)),
		Note:  note,
	}
}

func claimNotes(item *corev1.PersistentVolumeClaim, gauge *Gauge) []string {
	status, _ := claimStatus(item)

	out := []string{
		note("state", status),
		note("class", storageClassOf(item.Spec.StorageClassName)),
		note("size", claimSize(item)),
		note("access", accessModes(item.Spec.AccessModes)),
	}
	if gauge != nil {
		out = append(out, note("filled", fmt.Sprintf("%.0f%%, %s", gauge.Percent(), gauge.Label)))
	}

	switch {
	case item.Status.Phase == corev1.ClaimPending:
		out = append(out, note("mind that", "nothing has been provisioned for it yet"))
	case item.Status.Phase == corev1.ClaimLost:
		out = append(out, note("mind that", "its volume is gone, the data with it"))
	case gauge != nil && gauge.Percent() >= CritPct:
		out = append(out, note("mind that", "the volume is nearly full"))
	}
	return out
}

func volumeNotes(item *corev1.PersistentVolume, gauge *Gauge) []string {
	status, _ := volumeStatus(item)

	out := []string{
		note("state", status),
		note("class", storageClassOf(&item.Spec.StorageClassName)),
		note("size", volumeSize(item)),
		note("access", accessModes(item.Spec.AccessModes)),
		note("on release", string(item.Spec.PersistentVolumeReclaimPolicy)),
	}
	if gauge != nil {
		out = append(out, note("filled", fmt.Sprintf("%.0f%%, %s (%s)", gauge.Percent(), gauge.Label, gauge.Note)))
	}

	switch {
	case item.Status.Phase == corev1.VolumeFailed:
		out = append(out, note("mind that", or(item.Status.Reason, "this volume cannot be used")))
	case item.Status.Phase == corev1.VolumeReleased:
		out = append(out, note("mind that", "its claim is gone; what happens next is the reclaim policy"))
	case gauge != nil && gauge.Percent() >= CritPct && gauge.Note == "written on the node":
		out = append(out, note("mind that", "the volume is nearly full"))
	}
	return out
}

func classNotes(item *storagev1.StorageClass, claims []string) []string {
	out := []string{
		note("provisioner", item.Provisioner),
		note("claims", plural(len(claims), "volume claim")),
		note("on release", string(*orPolicy(item.ReclaimPolicy))),
		note("binding", string(*orBinding(item.VolumeBindingMode))),
	}
	if item.AllowVolumeExpansion != nil && *item.AllowVolumeExpansion {
		out = append(out, note("expansion", "volumes on this class can grow"))
	}
	if item.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
		out = append(out, note("default", "claims without a class land here"))
	}
	if len(claims) == 0 {
		out = append(out, note("mind that", "nothing in the cluster asks for this class"))
	}
	return out
}

func orPolicy(policy *corev1.PersistentVolumeReclaimPolicy) *corev1.PersistentVolumeReclaimPolicy {
	if policy != nil {
		return policy
	}
	fallback := corev1.PersistentVolumeReclaimDelete
	return &fallback
}

func orBinding(mode *storagev1.VolumeBindingMode) *storagev1.VolumeBindingMode {
	if mode != nil {
		return mode
	}
	fallback := storagev1.VolumeBindingImmediate
	return &fallback
}

func describeClaim(item *corev1.PersistentVolumeClaim, now time.Time) []string {
	status, _ := claimStatus(item)

	out := []string{
		"VOLUMECLAIM",
		field("name", item.Name),
		field("namespace", item.Namespace),
		field("state", status),
		field("class", storageClassOf(item.Spec.StorageClassName)),
		field("size", claimSize(item)),
		field("access", accessModes(item.Spec.AccessModes)),
		field("volume", or(item.Spec.VolumeName, "none yet")),
		field("created", timestamp(item.CreationTimestamp.Time, now)),
	}

	if len(item.Status.Conditions) > 0 {
		out = append(out, "", "CONDITIONS")
		for _, condition := range item.Status.Conditions {
			value := string(condition.Status)
			if condition.Reason != "" {
				value += " (" + condition.Reason + ")"
			}
			out = append(out, field(string(condition.Type), value))
		}
	}
	return out
}

func tellClaim(item *corev1.PersistentVolumeClaim, gauge *Gauge, now time.Time) []string {
	status, _ := claimStatus(item)

	out := []string{
		fmt.Sprintf("VolumeClaim %s in namespace %s is %s and was created %s ago.",
			item.Name, item.Namespace, status, ago(now.Sub(item.CreationTimestamp.Time))),
		fmt.Sprintf("It asks for %s on class %s, %s.",
			claimSize(item), storageClassOf(item.Spec.StorageClassName), accessModes(item.Spec.AccessModes)),
	}

	if gauge != nil && gauge.Percent() >= 0 {
		out = append(out, fmt.Sprintf("It holds %s, %.0f%% of what it has.", gauge.Label, gauge.Percent()))
		if gauge.Percent() >= CritPct {
			out = append(out, "Worth a look: it is nearly full.")
		}
	}
	if item.Status.Phase == corev1.ClaimPending {
		out = append(out, "Worth a look: nothing has been provisioned for it yet.")
	}
	return out
}

func describeVolume(item *corev1.PersistentVolume, now time.Time) []string {
	status, _ := volumeStatus(item)

	out := []string{
		"VOLUME",
		field("name", item.Name),
		field("state", status),
		field("class", storageClassOf(&item.Spec.StorageClassName)),
		field("size", volumeSize(item)),
		field("access", accessModes(item.Spec.AccessModes)),
		field("on release", string(item.Spec.PersistentVolumeReclaimPolicy)),
		field("created", timestamp(item.CreationTimestamp.Time, now)),
	}

	if ref := item.Spec.ClaimRef; ref != nil {
		out = append(out, field("claim", ref.Namespace+"/"+ref.Name))
	}

	out = append(out, "", "BACKING STORE")
	switch {
	case item.Spec.CSI != nil:
		out = append(out,
			field("driver", item.Spec.CSI.Driver),
			field("handle", item.Spec.CSI.VolumeHandle))
		if item.Spec.CSI.FSType != "" {
			out = append(out, field("filesystem", item.Spec.CSI.FSType))
		}
	case item.Spec.HostPath != nil:
		out = append(out, field("host path", item.Spec.HostPath.Path))
	case item.Spec.NFS != nil:
		out = append(out, field("nfs", item.Spec.NFS.Server+":"+item.Spec.NFS.Path))
	case item.Spec.Local != nil:
		out = append(out, field("local path", item.Spec.Local.Path))
	default:
		out = append(out, field("kind", "not one ktop reads in detail"))
	}
	return out
}

func tellVolume(item *corev1.PersistentVolume, gauge *Gauge, now time.Time) []string {
	status, _ := volumeStatus(item)

	out := []string{
		fmt.Sprintf("Volume %s is %s, holds %s on class %s and was created %s ago.",
			item.Name, status, volumeSize(item), storageClassOf(&item.Spec.StorageClassName),
			ago(now.Sub(item.CreationTimestamp.Time))),
	}

	if gauge != nil && gauge.Note == "written on the node" {
		out = append(out, fmt.Sprintf("The node reports %s written, %.0f%% of it.", gauge.Label, gauge.Percent()))
		if gauge.Percent() >= CritPct {
			out = append(out, "Worth a look: it is nearly full.")
		}
	} else {
		out = append(out, "How much of it is written is unknown, the node keeps no stats ktop may read.")
	}

	if item.Status.Phase == corev1.VolumeReleased {
		out = append(out, fmt.Sprintf("Worth a look: it was released, and its reclaim policy is %s.",
			item.Spec.PersistentVolumeReclaimPolicy))
	}
	return out
}

func describeClass(item *storagev1.StorageClass, claims []string, now time.Time) []string {
	out := []string{
		"STORAGECLASS",
		field("name", item.Name),
		field("provisioner", item.Provisioner),
		field("on release", string(*orPolicy(item.ReclaimPolicy))),
		field("binding", string(*orBinding(item.VolumeBindingMode))),
		field("created", timestamp(item.CreationTimestamp.Time, now)),
	}

	if len(item.Parameters) > 0 {
		out = append(out, "", "PARAMETERS")
		for _, key := range sortedKeys(item.Parameters) {
			out = append(out, field(key, item.Parameters[key]))
		}
	}

	out = append(out, "", "CLAIMS")
	if len(claims) == 0 {
		out = append(out, field("none", "nothing asks for this class"))
	}
	for _, claim := range claims {
		out = append(out, field(claim, ""))
	}
	return out
}

func tellClass(item *storagev1.StorageClass, claims []string, now time.Time) []string {
	out := []string{
		fmt.Sprintf("StorageClass %s provisions through %s and was created %s ago.",
			item.Name, item.Provisioner, ago(now.Sub(item.CreationTimestamp.Time))),
		fmt.Sprintf("Volumes on it are bound %s and %s on release, and %s asks for it.",
			strings.ToLower(string(*orBinding(item.VolumeBindingMode))),
			strings.ToLower(string(*orPolicy(item.ReclaimPolicy))),
			plural(len(claims), "volume claim")),
	}

	if len(claims) == 0 {
		out = append(out, "Worth a look: nothing in the cluster uses it.")
	}
	return out
}
