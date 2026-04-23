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

	wh := fmt.Sprintf("%dh", int(s.window.Hours()))
	query := fmt.Sprintf(
		`sum by (route) (increase(haproxy_backend_http_total_requests{exported_namespace=%q}[%s]))`,
		namespace, wh)

	traffic, err := s.prom.Increase(ctx, query, s.window, "route")
	if err != nil {
		return nil, fmt.Errorf("querying haproxy traffic: %w", err)
	}

	routes, err := s.routeClient.RouteV1().Routes(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing routes: %w", err)
	}

	// A nil map means Prometheus returned no results at all — HAProxy metrics may
	// not be exposed or may use a different label set. Reporting every route as
	// unused would be misleading; warn and skip instead.
	// An empty (non-nil) map means HAProxy is working but no route in this
	// namespace received any traffic — those routes are still legitimate findings.
	if traffic == nil && len(routes.Items) > 0 {
		fmt.Fprintf(os.Stderr, "warning: [%s] no HAProxy traffic metrics found; skipping unused-routes scan\n", namespace)
		return nil, nil
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
