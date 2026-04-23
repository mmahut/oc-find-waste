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
//  1. overrideURL (from --prometheus-url flag) — used as-is, no probing
//  2. thanosRouteURL — external thanos-querier Route discovered by the caller
//  3. defaultURLs — in-cluster service endpoints tried in order
//
// When nil is returned a warning has already been printed to stderr.
func Discover(ctx context.Context, overrideURL, thanosRouteURL, bearerToken string) Client {
	if overrideURL != "" {
		// User-supplied URL: enforce TLS verification so the bearer token is
		// never sent to an endpoint with an invalid certificate.
		c, err := New(overrideURL, bearerToken, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: prometheus override URL unusable: %v\n", err)
			return nil
		}
		return c
	}

	candidates := defaultURLs
	if thanosRouteURL != "" {
		candidates = append([]string{thanosRouteURL}, candidates...)
	}

	for _, u := range candidates {
		if probeHealthy(ctx, u, bearerToken) {
			// Already probed with InsecureSkipVerify; use the same policy for
			// queries so in-cluster self-signed certs don't cause x509 errors.
			c, err := New(u, bearerToken, true)
			if err == nil {
				return c
			}
		}
	}

	fmt.Fprintln(os.Stderr, "warning: no Prometheus endpoint reachable; metrics-based scanners (over-provisioned, unused-routes) will be skipped")
	return nil
}

// probeHealthy does a lightweight GET to /-/healthy with a short timeout.
// It skips TLS verification because in-cluster endpoints use self-signed certs.
func probeHealthy(ctx context.Context, url, token string) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/-/healthy", nil)
	if err != nil {
		return false
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := (&http.Client{Transport: insecureTransport()}).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 300
}
