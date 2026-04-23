package scanner

import (
	"context"
	"fmt"
	"os"
	"time"

	osroutev1client "github.com/openshift/client-go/route/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/mmahut/oc-find-waste/internal/prom"
)

type unusedRoutesScanner struct {
	routeClient osroutev1client.Interface
	prom        prom.Client
	window      time.Duration
}

// NewUnusedRoutes creates a scanner for Routes with zero HAProxy traffic over
// the window. Pass nil for either argument to no-op gracefully.
func NewUnusedRoutes(routeClient osroutev1client.Interface, promClient prom.Client, window time.Duration) Scanner {
	return &unusedRoutesScanner{
		routeClient: routeClient,
		prom:        promClient,
		window:      window,
	}
}

func (s *unusedRoutesScanner) Name() string { return "unused-routes" }

func (s *unusedRoutesScanner) Scan(ctx context.Context, namespace string) ([]Finding, error) {
	if s.routeClient == nil || s.prom == nil {
		return nil, nil
	}

	routes, err := s.routeClient.RouteV1().Routes(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing routes: %w", err)
	}
	if len(routes.Items) == 0 {
		return nil, nil
	}

	// Probe whether HAProxy is exporting metrics at all on this cluster.
	// The per-namespace traffic query legitimately returns an empty result when
	// routes are present but received no traffic, so we cannot use it as a signal
	// for "HAProxy absent". A cluster-wide count query is authoritative: if it
	// returns no samples, HAProxy metrics do not exist and flagging every route as
	// unused would be misleading.
	probe, err := s.prom.Increase(ctx, `count by (job) (haproxy_backend_http_total_requests)`, s.window, "job")
	if err != nil {
		return nil, fmt.Errorf("probing haproxy metrics: %w", err)
	}
	if len(probe) == 0 {
		fmt.Fprintf(os.Stderr, "warning: [%s] no HAProxy metrics found on cluster; skipping unused-routes scan\n", namespace)
		return nil, nil
	}

	wh := fmt.Sprintf("%dh", int(s.window.Hours()))
	query := fmt.Sprintf(
		`sum by (route) (increase(haproxy_backend_http_total_requests{exported_namespace=%q}[%s]))`,
		namespace, wh)

	traffic, err := s.prom.Increase(ctx, query, s.window, "route")
	if err != nil {
		return nil, fmt.Errorf("querying haproxy traffic: %w", err)
	}

	windowDays := fmt.Sprintf("%.0fd", s.window.Hours()/24)

	var findings []Finding
	for i := range routes.Items {
		r := &routes.Items[i]
		if requests, seen := traffic[r.Name]; seen && requests > 0 {
			continue
		}
		findings = append(findings, Finding{
			Kind:       "Route",
			Namespace:  namespace,
			Name:       r.Name,
			Reason:     fmt.Sprintf("0 requests / %s", windowDays),
			Severity:   SeverityInfo,
			Suggestion: "if the application is decommissioned, delete the Route",
		})
	}
	return findings, nil
}
