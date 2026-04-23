package prom

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
)

// defaultURLs are tried in order when no --prometheus-url is provided.
var defaultURLs = []string{
	"https://thanos-querier.openshift-monitoring.svc:9091",
	"https://prometheus-k8s.openshift-monitoring.svc:9091",
	"https://thanos-querier.openshift-user-workload-monitoring.svc:9091",
}

// Discover returns a working Prometheus Client or nil.
//
// Order of preference:
//  1. overrideURL (from --prometheus-url flag) — full TLS verification
//  2. thanosRouteURL — external Route, full TLS verification
//  3. defaultURLs — in-cluster .svc endpoints, TLS skip (self-signed cluster CA)
//
// External URLs (1 and 2) never use InsecureSkipVerify so bearer tokens are
// not sent to endpoints with invalid certificates. Only in-cluster .svc
// endpoints skip verification.
//
// When nil is returned a warning has already been printed to stderr.
func Discover(ctx context.Context, overrideURL, thanosRouteURL, bearerToken string) Client {
	if overrideURL != "" {
		if !probeHealthy(ctx, overrideURL, bearerToken, false) {
			fmt.Fprintf(os.Stderr, "warning: prometheus override URL unreachable: %s\n", overrideURL)
			return nil
		}
		c, err := New(overrideURL, bearerToken, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: prometheus override URL unusable: %v\n", err)
			return nil
		}
		return c
	}

	// External Thanos route: enforce TLS so the bearer token is safe.
	if thanosRouteURL != "" {
		if probeHealthy(ctx, thanosRouteURL, bearerToken, false) {
			if c, err := New(thanosRouteURL, bearerToken, false); err == nil {
				return c
			}
		}
	}

	// In-cluster service endpoints: skip TLS (self-signed cluster CA is expected).
	for _, u := range defaultURLs {
		if probeHealthy(ctx, u, bearerToken, true) {
			if c, err := New(u, bearerToken, true); err == nil {
				return c
			}
		}
	}

	fmt.Fprintln(os.Stderr, "warning: no Prometheus endpoint reachable; metrics-based scanners (over-provisioned, unused-routes) will be skipped")
	return nil
}

// probeHealthy does a lightweight GET to /-/healthy with a short timeout.
// Pass insecureTLS=true only for in-cluster endpoints that use self-signed certs.
func probeHealthy(ctx context.Context, url, token string, insecureTLS bool) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/-/healthy", nil)
	if err != nil {
		return false
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	transport := http.RoundTripper(http.DefaultTransport)
	if insecureTLS {
		transport = insecureTransport()
	}
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode < 300
}
