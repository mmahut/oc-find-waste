package scanner_test

import (
	"context"
	"strings"
	"testing"
	"time"

	routev1 "github.com/openshift/api/route/v1"
	osroutefake "github.com/openshift/client-go/route/clientset/versioned/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/mmahut/oc-find-waste/internal/scanner"
)

func route(name, ns string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
	}
}

func TestUnusedRoutes_NilClients(t *testing.T) {
	s := scanner.NewUnusedRoutes(nil, nil, 7*24*time.Hour)
	findings, err := s.Scan(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if findings != nil {
		t.Errorf("expected nil findings with nil clients")
	}
}

func TestUnusedRoutes_NilPromClient(t *testing.T) {
	rc := osroutefake.NewClientset(route("my-route", "test"))
	s := scanner.NewUnusedRoutes(rc, nil, 7*24*time.Hour)
	findings, err := s.Scan(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if findings != nil {
		t.Errorf("expected nil findings with nil prom")
	}
}

func TestUnusedRoutes_EmptyNamespace(t *testing.T) {
	rc := osroutefake.NewClientset()
	prom := &fakePromClient{cpu: map[string]float64{}, mem: map[string]float64{}}
	s := scanner.NewUnusedRoutes(rc, prom, 7*24*time.Hour)
	findings, err := s.Scan(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("got %d findings, want 0", len(findings))
	}
}

func TestUnusedRoutes_Finding_ZeroTraffic(t *testing.T) {
	rc := osroutefake.NewClientset([]runtime.Object{route("old-admin", "test")}...)
	// Route exists but Prometheus returns 0 requests for it.
	prom := &fakePromRouteClient{data: map[string]float64{"old-admin": 0}}
	s := scanner.NewUnusedRoutes(rc, prom, 7*24*time.Hour)
	findings, err := s.Scan(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	f := findings[0]
	if f.Kind != "Route" {
		t.Errorf("Kind = %q, want Route", f.Kind)
	}
	if f.Name != "old-admin" {
		t.Errorf("Name = %q, want old-admin", f.Name)
	}
	if !strings.Contains(f.Reason, "0 requests") {
		t.Errorf("Reason missing '0 requests': %q", f.Reason)
	}
	if !strings.Contains(f.Reason, "7d") {
		t.Errorf("Reason missing window: %q", f.Reason)
	}
}

func TestUnusedRoutes_Finding_AbsentFromMetrics(t *testing.T) {
	// Route is listed but has no HAProxy metric at all — also a finding.
	rc := osroutefake.NewClientset([]runtime.Object{route("ghost-route", "test")}...)
	prom := &fakePromRouteClient{data: map[string]float64{}} // absent
	s := scanner.NewUnusedRoutes(rc, prom, 7*24*time.Hour)
	findings, err := s.Scan(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
}

func TestUnusedRoutes_NoFinding_HasTraffic(t *testing.T) {
	rc := osroutefake.NewClientset([]runtime.Object{route("active-app", "test")}...)
	prom := &fakePromRouteClient{data: map[string]float64{"active-app": 12345}}
	s := scanner.NewUnusedRoutes(rc, prom, 7*24*time.Hour)
	findings, err := s.Scan(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("got %d findings, want 0 (route has traffic)", len(findings))
	}
}

func TestUnusedRoutes_MixedTraffic(t *testing.T) {
	rc := osroutefake.NewClientset([]runtime.Object{
		route("active-app", "test"),
		route("idle-app", "test"),
	}...)
	prom := &fakePromRouteClient{data: map[string]float64{
		"active-app": 5000,
		"idle-app":   0,
	}}
	s := scanner.NewUnusedRoutes(rc, prom, 7*24*time.Hour)
	findings, err := s.Scan(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	if findings[0].Name != "idle-app" {
		t.Errorf("Name = %q, want idle-app", findings[0].Name)
	}
}

// fakePromRouteClient routes all Increase calls to the data map.
type fakePromRouteClient struct {
	data map[string]float64
}

func (f *fakePromRouteClient) RangeP95(_ context.Context, _ string, _ time.Duration, _ string) (map[string]float64, error) {
	return nil, nil
}

func (f *fakePromRouteClient) Increase(_ context.Context, _ string, _ time.Duration, _ string) (map[string]float64, error) {
	return f.data, nil
}
