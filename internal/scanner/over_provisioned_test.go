package scanner_test

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/mmahut/oc-find-waste/internal/pricing"
	"github.com/mmahut/oc-find-waste/internal/scanner"
)

// fakePromClient implements prom.Client for testing.
type fakePromClient struct {
	cpu map[string]float64
	mem map[string]float64
}

func (f *fakePromClient) RangeP95(_ context.Context, query string, _ time.Duration) (map[string]float64, error) {
	if strings.Contains(query, "cpu") {
		return f.cpu, nil
	}
	return f.mem, nil
}

func (f *fakePromClient) Increase(_ context.Context, _ string, _ time.Duration) (map[string]float64, error) {
	return nil, nil
}

func oldPod(name, ns string, cpuReq, memReq string, ownerKind, ownerName string, ownerUID string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         ns,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-48 * time.Hour)},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(cpuReq),
							corev1.ResourceMemory: resource.MustParse(memReq),
						},
					},
				},
			},
		},
	}
	if ownerKind != "" {
		pod.OwnerReferences = []metav1.OwnerReference{
			{Kind: ownerKind, Name: ownerName, UID: types.UID("uid-" + ownerName)},
		}
	}
	return pod
}

func replicaSet(name, ns, depName string) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: depName},
			},
		},
	}
}

func deployment(name, ns string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
}

func TestOverProvisioned_Empty(t *testing.T) {
	client := fake.NewSimpleClientset()
	prom := &fakePromClient{cpu: map[string]float64{}, mem: map[string]float64{}}
	s := scanner.NewOverProvisioned(client, prom, nil, 7*24*time.Hour)
	findings, err := s.Scan(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("got %d findings, want 0", len(findings))
	}
}

func TestOverProvisioned_NilProm(t *testing.T) {
	client := fake.NewSimpleClientset()
	s := scanner.NewOverProvisioned(client, nil, nil, 7*24*time.Hour)
	findings, err := s.Scan(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if findings != nil {
		t.Errorf("expected nil findings with nil prom client")
	}
}

func TestOverProvisioned_Finding(t *testing.T) {
	// Pod requests 2000m CPU, 4Gi RAM; p95 is 180m CPU, 600Mi RAM (both < 30%).
	pod := oldPod("web-pod", "test", "2000m", "4Gi", "ReplicaSet", "web-rs", "uid-web-rs")
	rs := replicaSet("web-rs", "test", "web-app")
	dep := deployment("web-app", "test", 3)

	client := fake.NewSimpleClientset([]runtime.Object{pod, rs, dep}...)
	prom := &fakePromClient{
		cpu: map[string]float64{"web-pod": 0.18},      // 180m
		mem: map[string]float64{"web-pod": 629145600}, // 600Mi
	}

	s := scanner.NewOverProvisioned(client, prom, nil, 7*24*time.Hour)
	findings, err := s.Scan(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	f := findings[0]
	if f.Kind != "Deployment" {
		t.Errorf("Kind = %q, want Deployment", f.Kind)
	}
	if f.Name != "web-app" {
		t.Errorf("Name = %q, want web-app", f.Name)
	}
	if !strings.Contains(f.Detail, "2000m") {
		t.Errorf("Detail missing requested CPU: %q", f.Detail)
	}
	if !strings.Contains(f.Detail, "p95 usage") {
		t.Errorf("Detail missing p95 usage: %q", f.Detail)
	}
}

func TestOverProvisioned_NoFindingWhenAdequate(t *testing.T) {
	// Pod requests 2000m CPU; p95 is 800m (40% > 30% threshold) — not over-provisioned.
	pod := oldPod("web-pod", "test", "2000m", "4Gi", "", "", "")

	client := fake.NewSimpleClientset([]runtime.Object{pod}...)
	prom := &fakePromClient{
		cpu: map[string]float64{"web-pod": 0.80},            // 800m = 40% of 2000m
		mem: map[string]float64{"web-pod": 1.5 * (1 << 30)}, // 1.5Gi = 37.5% of 4Gi
	}

	s := scanner.NewOverProvisioned(client, prom, nil, 7*24*time.Hour)
	findings, err := s.Scan(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("got %d findings, want 0 (resource usage is adequate)", len(findings))
	}
}

func TestOverProvisioned_TooYoung(t *testing.T) {
	// Pod younger than 24h should be skipped.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "new-pod",
			Namespace:         "test",
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-1 * time.Hour)},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2000m"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
				},
			},
		},
	}

	client := fake.NewSimpleClientset([]runtime.Object{pod}...)
	prom := &fakePromClient{
		cpu: map[string]float64{"new-pod": 0.01},
		mem: map[string]float64{"new-pod": 1},
	}

	s := scanner.NewOverProvisioned(client, prom, nil, 7*24*time.Hour)
	findings, err := s.Scan(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("got %d findings, want 0 (pod too young)", len(findings))
	}
}

func TestOverProvisioned_Cost(t *testing.T) {
	profile, err := pricing.Load("aws")
	if err != nil {
		t.Fatalf("loading aws profile: %v", err)
	}

	// 2000m CPU, 4Gi RAM requested; p95 180m CPU, 600Mi RAM.
	// suggest = ceil(p95 * 1.5): ceil(0.27) = 1 core, ceil(900Mi) = 1Gi.
	// wasted CPU = 2.0 - 0.27 = 1.73 cores; wasted mem = 4Gi - 1Gi = 3Gi.
	pod := oldPod("web-pod", "test", "2000m", "4Gi", "", "", "")

	client := fake.NewSimpleClientset([]runtime.Object{pod}...)
	prom := &fakePromClient{
		cpu: map[string]float64{"web-pod": 0.18},      // 180m
		mem: map[string]float64{"web-pod": 629145600}, // 600Mi
	}

	s := scanner.NewOverProvisioned(client, prom, profile, 7*24*time.Hour)
	findings, err := s.Scan(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	if findings[0].MonthlyCost <= 0 {
		t.Errorf("MonthlyCost = %.4f, want > 0", findings[0].MonthlyCost)
	}
	if !strings.Contains(findings[0].Suggestion, "$") {
		t.Errorf("Suggestion missing cost: %q", findings[0].Suggestion)
	}
}

func TestFmtHelpers(t *testing.T) {
	// Indirectly tested via finding Detail strings.
	pod := oldPod("p", "test", "2", "2Gi", "", "", "")
	client := fake.NewSimpleClientset([]runtime.Object{pod}...)
	prom := &fakePromClient{
		cpu: map[string]float64{"p": 0.1},
		mem: map[string]float64{"p": 100 * (1 << 20)}, // 100Mi
	}
	s := scanner.NewOverProvisioned(client, prom, nil, 7*24*time.Hour)
	findings, err := s.Scan(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	// 2 cores → "2000m"
	if !strings.Contains(findings[0].Detail, "2000m") {
		t.Errorf("expected 2000m in detail, got %q", findings[0].Detail)
	}
	// 2Gi → "2Gi" (fmtMem rounds up with GiB path)
	if !strings.Contains(findings[0].Detail, "2Gi") {
		t.Errorf("expected 2Gi in detail, got %q", findings[0].Detail)
	}
	// p95 100Mi
	if !strings.Contains(findings[0].Detail, "100Mi") {
		t.Errorf("expected 100Mi in detail, got %q", findings[0].Detail)
	}
	_ = math.Pi // keep math import if needed later
}

func TestOverProvisioned_Patch(t *testing.T) {
	// Single container pod: patch should name the container and set suggested requests.
	pod := oldPod("web-pod", "test", "2000m", "4Gi", "", "", "")
	client := fake.NewSimpleClientset([]runtime.Object{pod}...)
	prom := &fakePromClient{
		cpu: map[string]float64{"web-pod": 0.18},      // 180m
		mem: map[string]float64{"web-pod": 629145600}, // 600Mi
	}

	s := scanner.NewOverProvisioned(client, prom, nil, 7*24*time.Hour)
	findings, err := s.Scan(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	patch := findings[0].Patch
	if patch == "" {
		t.Fatal("Patch is empty")
	}
	if !strings.Contains(patch, "kubectl patch") {
		t.Errorf("Patch missing kubectl command: %q", patch)
	}
	if !strings.Contains(patch, "name: app") {
		t.Errorf("Patch missing container name: %q", patch)
	}
	if !strings.Contains(patch, "cpu:") {
		t.Errorf("Patch missing cpu field: %q", patch)
	}
	if !strings.Contains(patch, "memory:") {
		t.Errorf("Patch missing memory field: %q", patch)
	}
}
