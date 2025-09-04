package prom

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	promapi "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

// Client is the interface scanners use for Prometheus queries.
// It is an interface so tests can inject a fake without a real Prometheus.
type Client interface {
	// RangeP95 runs an instant query (window already embedded in the PromQL)
	// and returns map[labelValue]p95Value.
	RangeP95(ctx context.Context, query string, window time.Duration) (map[string]float64, error)
	// Increase runs an instant query and returns map[labelValue]increaseValue.
	Increase(ctx context.Context, query string, window time.Duration) (map[string]float64, error)
}

type promClient struct {
	api promv1.API
}

// New creates a Client that queries the given Prometheus URL with an optional
// bearer token. TLS verification is skipped for in-cluster endpoints that use
// self-signed certificates signed by the cluster CA.
func New(url, bearerToken string) (Client, error) {
	rt := promapi.DefaultRoundTripper
	if bearerToken != "" {
		rt = &bearerRT{token: bearerToken, inner: rt}
	}
	cfg := promapi.Config{Address: url, RoundTripper: rt}
	c, err := promapi.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("building prometheus client for %s: %w", url, err)
	}
	return &promClient{api: promv1.NewAPI(c)}, nil
}

func (c *promClient) RangeP95(ctx context.Context, query string, _ time.Duration) (map[string]float64, error) {
	return c.instant(ctx, query)
}

func (c *promClient) Increase(ctx context.Context, query string, _ time.Duration) (map[string]float64, error) {
	return c.instant(ctx, query)
}

func (c *promClient) instant(ctx context.Context, query string) (map[string]float64, error) {
	result, warnings, err := c.api.Query(ctx, query, time.Now())
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "prometheus warning: %s\n", w)
	}
	if err != nil {
		return nil, fmt.Errorf("prometheus query %q: %w", query, err)
	}

	vec, ok := result.(model.Vector)
	if !ok {
		return nil, fmt.Errorf("unexpected prometheus result type %T (want Vector)", result)
	}

	out := make(map[string]float64, len(vec))
	for _, sample := range vec {
		for lname, lval := range sample.Metric {
			if lname != model.MetricNameLabel {
				out[string(lval)] = float64(sample.Value)
				break
			}
		}
	}
	return out, nil
}

// bearerRT injects a Bearer token into every request.
type bearerRT struct {
	token string
	inner http.RoundTripper
}

func (b *bearerRT) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)
	return b.inner.RoundTrip(req)
}
