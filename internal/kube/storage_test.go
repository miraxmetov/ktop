package kube

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func volumeClaim(name string, phase corev1.PersistentVolumeClaimPhase) *corev1.PersistentVolumeClaim {
	class := "fast"
	out := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "production"},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &class,
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: phase},
	}
	if phase == corev1.ClaimBound {
		out.Spec.VolumeName = "pvc-1"
		out.Status.Capacity = corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")}
	}
	return out
}

func persistentVolume(name string, phase corev1.PersistentVolumePhase) *corev1.PersistentVolume {
	out := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PersistentVolumeSpec{
			StorageClassName:              "fast",
			AccessModes:                   []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
			Capacity:                      corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{Driver: "csi.example.com", VolumeHandle: "vol-42"},
			},
		},
		Status: corev1.PersistentVolumeStatus{Phase: phase},
	}
	if phase == corev1.VolumeBound {
		out.Spec.ClaimRef = &corev1.ObjectReference{Namespace: "production", Name: "data-api"}
	}
	return out
}

func TestClaimRowsReadTheirPhase(t *testing.T) {
	client := NewWithClients(k8sfake.NewSimpleClientset(
		volumeClaim("data-api", corev1.ClaimBound),
		volumeClaim("data-cache", corev1.ClaimPending),
		volumeClaim("data-gone", corev1.ClaimLost),
	), metricsClient())

	result, err := client.Rows(context.Background(), "production", KindVolumeClaim)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(result.Rows) != 3 {
		t.Fatalf("rows: %v", result.Rows)
	}

	for _, c := range []struct {
		name     string
		status   string
		severity Severity
	}{
		{"data-api", "bound to pvc-1", Good},
		{"data-cache", "pending, nothing has been provisioned", Warn},
		{"data-gone", "lost, its volume is gone", Bad},
	} {
		found := false
		for _, row := range result.Rows {
			if row.Name != c.name {
				continue
			}
			found = true
			if row.Status != c.status || row.Severity != c.severity {
				t.Errorf("%s: %q %v", c.name, row.Status, row.Severity)
			}
			if !strings.Contains(row.Info, "fast") {
				t.Errorf("%s: info %q", c.name, row.Info)
			}
		}
		if !found {
			t.Errorf("%s is missing", c.name)
		}
	}
}

func TestVolumeRowsAndGaugeFollowTheState(t *testing.T) {
	client := NewWithClients(k8sfake.NewSimpleClientset(
		persistentVolume("pvc-1", corev1.VolumeBound),
		persistentVolume("pvc-2", corev1.VolumeReleased),
		persistentVolume("pvc-3", corev1.VolumeFailed),
	), metricsClient())

	result, err := client.Rows(context.Background(), "", KindVolume)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}

	for _, row := range result.Rows {
		switch row.Name {
		case "pvc-1":
			if row.Severity != Good || !strings.Contains(row.Status, "bound to production/data-api") {
				t.Errorf("a bound volume: %q %v", row.Status, row.Severity)
			}
		case "pvc-2":
			if row.Severity != Warn || !strings.Contains(row.Status, "released") {
				t.Errorf("a released volume: %q %v", row.Status, row.Severity)
			}
		case "pvc-3":
			if row.Severity != Bad {
				t.Errorf("a failed volume must be critical: %q %v", row.Status, row.Severity)
			}
		}
	}

	detail, err := client.BrowseObject(context.Background(), KindVolume, "", "pvc-1")
	if err != nil {
		t.Fatalf("BrowseObject: %v", err)
	}

	gauge := detail.Gauge()
	if gauge == nil {
		t.Fatal("a volume must carry a gauge")
	}
	if gauge.Percent() != 100 || !strings.Contains(gauge.Note, "claimed whole") {
		t.Errorf("without node stats the gauge says what it measures: %.0f%% %q", gauge.Percent(), gauge.Note)
	}

	body := strings.Join(detail.Describe(time.Now()), "\n")
	for _, want := range []string{"VOLUME", "on release", "Delete", "BACKING STORE", "csi.example.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("the description misses %q:\n%s", want, body)
		}
	}

	free, err := client.BrowseObject(context.Background(), KindVolume, "", "pvc-2")
	if err != nil {
		t.Fatalf("BrowseObject: %v", err)
	}
	if free.Gauge().Percent() != 0 {
		t.Errorf("a volume nothing claims reads empty: %.0f%%", free.Gauge().Percent())
	}
}

func TestStorageClassRowsCountWhatAsksForThem(t *testing.T) {
	expansion := true
	class := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "fast",
			Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"},
		},
		Provisioner:          "csi.example.com",
		AllowVolumeExpansion: &expansion,
	}
	idle := &storagev1.StorageClass{
		ObjectMeta:  metav1.ObjectMeta{Name: "slow"},
		Provisioner: "csi.example.com",
	}

	client := NewWithClients(k8sfake.NewSimpleClientset(
		class, idle, volumeClaim("data-api", corev1.ClaimBound),
	), metricsClient())

	result, err := client.Rows(context.Background(), "", KindStorageClass)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}

	for _, row := range result.Rows {
		switch row.Name {
		case "fast":
			if row.Severity != Good || !strings.Contains(row.Status, "1 volume claimed") {
				t.Errorf("a class in use: %q %v", row.Status, row.Severity)
			}
			if !strings.Contains(row.Status, "default") {
				t.Errorf("the default class says so: %q", row.Status)
			}
		case "slow":
			if row.Severity != Muted || row.Status != "nothing uses it" {
				t.Errorf("an idle class: %q %v", row.Status, row.Severity)
			}
		}
	}

	detail, err := client.BrowseObject(context.Background(), KindStorageClass, "", "fast")
	if err != nil {
		t.Fatalf("BrowseObject: %v", err)
	}
	if detail.Gauge() != nil {
		t.Error("a storage class has nothing to fill")
	}
	notes := strings.Join(detail.Notes(), "\n")
	if !strings.Contains(notes, "provisioner") || !strings.Contains(notes, "expansion") {
		t.Errorf("notes: %s", notes)
	}
}
