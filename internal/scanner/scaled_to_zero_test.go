package scanner_test

import (
	"context"
	"strings"
	"testing"
	"time"

	osappsv1 "github.com/openshift/api/apps/v1"
	osappsfake "github.com/openshift/client-go/apps/clientset/versioned/fake"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/mmahut/oc-find-waste/internal/scanner"
)

func int32Ptr(i int32) *int32 { return &i }

func TestScaledToZero(t *testing.T) {
	longAgo := metav1.NewTime(time.Now().Add(-48 * 24 * time.Hour))

	tests := []struct {
		name      string
		objects   []runtime.Object
		wantCount int
		wantKind  string
		wantWord  string
	}{
		{
			name:      "empty namespace",
			objects:   nil,
			wantCount: 0,
		},
		{
			name: "deployment scaled to zero",
			objects: []runtime.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name: "idle-app", Namespace: "test",
						CreationTimestamp: longAgo,
					},
					Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(0)},
				},
			},
			wantCount: 1,
			wantKind:  "Deployment",
			wantWord:  "scaled to 0",
		},
		{
			name: "deployment running — no finding",
			objects: []runtime.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{Name: "busy-app", Namespace: "test"},
					Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(2)},
				},
			},
			wantCount: 0,
		},
		{
			name: "statefulset scaled to zero",
			objects: []runtime.Object{
				&appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name: "idle-db", Namespace: "test",
						CreationTimestamp: longAgo,
					},
					Spec: appsv1.StatefulSetSpec{Replicas: int32Ptr(0)},
				},
			},
			wantCount: 1,
			wantKind:  "StatefulSet",
			wantWord:  "associated PVCs",
		},
		{
			name: "deployment scaled to zero but HPA-managed — no finding",
			objects: []runtime.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{Name: "hpa-app", Namespace: "test", CreationTimestamp: longAgo},
					Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(0)},
				},
				&autoscalingv1.HorizontalPodAutoscaler{
					ObjectMeta: metav1.ObjectMeta{Name: "hpa-app-hpa", Namespace: "test"},
					Spec: autoscalingv1.HorizontalPodAutoscalerSpec{
						ScaleTargetRef: autoscalingv1.CrossVersionObjectReference{Kind: "Deployment", Name: "hpa-app"},
					},
				},
			},
			wantCount: 0,
		},
		{
			name: "deployment with nil replicas — no finding",
			objects: []runtime.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{Name: "default-app", Namespace: "test"},
					Spec:       appsv1.DeploymentSpec{Replicas: nil},
				},
			},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientset(tt.objects...)
			s := scanner.NewScaledToZero(client, nil) // nil = no OCP
			findings, err := s.Scan(context.Background(), "test")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(findings) != tt.wantCount {
				t.Errorf("got %d findings, want %d", len(findings), tt.wantCount)
			}
			if len(findings) == 0 {
				return
			}
			if tt.wantKind != "" && findings[0].Kind != tt.wantKind {
				t.Errorf("kind: got %q, want %q", findings[0].Kind, tt.wantKind)
			}
			if tt.wantWord != "" && !strings.Contains(findings[0].Suggestion+findings[0].Reason, tt.wantWord) {
				t.Errorf("expected %q in finding text, got reason=%q suggestion=%q",
					tt.wantWord, findings[0].Reason, findings[0].Suggestion)
			}
		})
	}
}

func TestScaledToZero_OpenShift(t *testing.T) {
	longAgo := metav1.NewTime(time.Now().Add(-10 * 24 * time.Hour))

	t.Run("dc scaled to zero — finding", func(t *testing.T) {
		k8sClient := fake.NewClientset()
		ocpClient := osappsfake.NewClientset(
			&osappsv1.DeploymentConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "idle-dc", Namespace: "test",
					CreationTimestamp: longAgo,
				},
				Spec: osappsv1.DeploymentConfigSpec{Replicas: 0},
			},
		)
		s := scanner.NewScaledToZero(k8sClient, ocpClient)
		findings, err := s.Scan(context.Background(), "test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(findings) != 1 {
			t.Fatalf("got %d findings, want 1", len(findings))
		}
		if findings[0].Kind != "DeploymentConfig" {
			t.Errorf("kind: got %q, want DeploymentConfig", findings[0].Kind)
		}
	})

	t.Run("dc running — no finding", func(t *testing.T) {
		k8sClient := fake.NewClientset()
		ocpClient := osappsfake.NewClientset(
			&osappsv1.DeploymentConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "active-dc", Namespace: "test"},
				Spec:       osappsv1.DeploymentConfigSpec{Replicas: 3},
			},
		)
		s := scanner.NewScaledToZero(k8sClient, ocpClient)
		findings, err := s.Scan(context.Background(), "test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(findings) != 0 {
			t.Errorf("got %d findings, want 0", len(findings))
		}
	})

	t.Run("nil appsClient — no dc scanning", func(t *testing.T) {
		k8sClient := fake.NewClientset()
		s := scanner.NewScaledToZero(k8sClient, nil)
		findings, err := s.Scan(context.Background(), "test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(findings) != 0 {
			t.Errorf("got %d findings, want 0", len(findings))
		}
	})
}
