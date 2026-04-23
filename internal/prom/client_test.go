package prom_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mmahut/oc-find-waste/internal/prom"
)

// fakePromResponse builds a minimal Prometheus instant query JSON response.
func fakePromResponse(labelName, labelValue string, value float64) []byte {
	resp := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "vector",
			"result": []map[string]any{
				{
					"metric": map[string]string{labelName: labelValue},
					"value":  []any{time.Now().Unix(), fmt.Sprintf("%g", value)},
				},
			},
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

// promServer returns a test server that responds to Prometheus API calls.
func promServer(t *testing.T, labelName, labelValue string, value float64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fakePromResponse(labelName, labelValue, value))
	}))
}

func TestClientRangeP95(t *testing.T) {
	srv := promServer(t, "pod", "my-pod", 0.18)
	defer srv.Close()

	c, err := prom.New(srv.URL, "", false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := c.RangeP95(context.Background(), "test_query", 7*24*time.Hour)
	if err != nil {
		t.Fatalf("RangeP95: %v", err)
	}
	if v, ok := result["my-pod"]; !ok || v != 0.18 {
		t.Errorf("got %v, want {my-pod: 0.18}", result)
	}
}

func TestClientIncrease(t *testing.T) {
	srv := promServer(t, "route", "my-route", 42)
	defer srv.Close()

	c, err := prom.New(srv.URL, "", false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := c.Increase(context.Background(), "test_query", 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Increase: %v", err)
	}
	if v, ok := result["my-route"]; !ok || v != 42 {
		t.Errorf("got %v, want {my-route: 42}", result)
	}
}

func TestDiscoverOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/-/healthy" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fakePromResponse("pod", "p", 1))
	}))
	defer srv.Close()

	c := prom.Discover(context.Background(), srv.URL, "", "")
	if c == nil {
		t.Fatal("expected non-nil client with valid override URL")
	}
}

func TestDiscoverAllFail(t *testing.T) {
	// No override, no thanos route, default in-cluster URLs unreachable.
	c := prom.Discover(context.Background(), "", "", "")
	if c != nil {
		t.Errorf("expected nil client when all endpoints fail, got %v", c)
	}
}

func TestDiscoverThanosRoute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/-/healthy" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fakePromResponse("pod", "p", 1))
	}))
	defer srv.Close()

	// Pass the test server as the thanos route URL (no override, no token).
	c := prom.Discover(context.Background(), "", srv.URL, "")
	if c == nil {
		t.Fatal("expected non-nil client when thanos route URL is reachable")
	}
}

// tlsPromServer returns an httptest.NewTLSServer that mimics a Prometheus
// endpoint with a self-signed certificate (the same situation as in-cluster
// OpenShift monitoring endpoints).
func tlsPromServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/-/healthy" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fakePromResponse("pod", "tls-pod", 0.42))
	}))
}

// TestSelfSignedTLS verifies that New() can query a Prometheus endpoint that
// presents a self-signed certificate. This would fail with x509 errors if
// New() used the default TLS-verifying transport.
func TestSelfSignedTLS(t *testing.T) {
	srv := tlsPromServer(t)
	defer srv.Close()

	c, err := prom.New(srv.URL, "", true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := c.RangeP95(context.Background(), "test_query", 7*24*time.Hour)
	if err != nil {
		t.Fatalf("RangeP95 against self-signed TLS server: %v", err)
	}
	if v, ok := result["tls-pod"]; !ok || v != 0.42 {
		t.Errorf("got %v, want {tls-pod: 0.42}", result)
	}
}

// TestDiscoverSelfSignedTLS verifies that Discover() returns a working client
// when the only reachable endpoint uses a self-signed certificate.
func TestDiscoverSelfSignedTLS(t *testing.T) {
	srv := tlsPromServer(t)
	defer srv.Close()

	c := prom.Discover(context.Background(), "", srv.URL, "")
	if c == nil {
		t.Fatal("expected non-nil client for self-signed TLS endpoint")
	}
	result, err := c.RangeP95(context.Background(), "test_query", 7*24*time.Hour)
	if err != nil {
		t.Fatalf("RangeP95 after Discover with self-signed TLS: %v", err)
	}
	if _, ok := result["tls-pod"]; !ok {
		t.Errorf("expected tls-pod in result, got %v", result)
	}
}
