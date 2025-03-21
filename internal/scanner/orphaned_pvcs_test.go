package scanner_test

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/mmahut/oc-find-waste/internal/scanner"
)

func boundPVC(name, size string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test"},
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(size),
				},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
	}
}

func podMounting(pvcName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "test"},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "data",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				},
			},
		},
	}
}

func TestOrphanedPVCs(t *testing.T) {
	tests := []struct {
		name      string
		objects   []runtime.Object
		wantCount int
		wantSize  string
	}{
		{
			name:      "empty namespace",
			wantCount: 0,
		},
		{
			name:      "bound PVC not mounted by any pod — finding",
			objects:   []runtime.Object{boundPVC("lonely-data", "50Gi")},
			wantCount: 1,
			wantSize:  "50Gi",
		},
		{
			name: "bound PVC mounted by a pod — no finding",
			objects: []runtime.Object{
				boundPVC("active-data", "10Gi"),
				podMounting("active-data"),
			},
			wantCount: 0,
		},
		{
			name: "pending PVC — no finding",
			objects: []runtime.Object{
				func() *corev1.PersistentVolumeClaim {
					pvc := boundPVC("pending-data", "10Gi")
					pvc.Status.Phase = corev1.ClaimPending
					return pvc
				}(),
			},
			wantCount: 0,
		},
		{
			name: "two bound PVCs, one mounted",
			objects: []runtime.Object{
				boundPVC("orphan", "20Gi"),
				boundPVC("in-use", "10Gi"),
				podMounting("in-use"),
			},
			wantCount: 1,
			wantSize:  "20Gi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(tt.objects...)
			s := scanner.NewOrphanedPVCs(client)
			findings, err := s.Scan(context.Background(), "test")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(findings) != tt.wantCount {
				t.Errorf("got %d findings, want %d", len(findings), tt.wantCount)
			}
			if tt.wantSize != "" && len(findings) > 0 {
				if !strings.Contains(findings[0].Reason, tt.wantSize) {
					t.Errorf("expected size %q in reason %q", tt.wantSize, findings[0].Reason)
				}
			}
		})
	}
}
