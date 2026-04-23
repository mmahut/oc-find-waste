package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	osappsv1client "github.com/openshift/client-go/apps/clientset/versioned"
	osbuildv1client "github.com/openshift/client-go/build/clientset/versioned"
	osimagev1client "github.com/openshift/client-go/image/clientset/versioned"
	osroutev1client "github.com/openshift/client-go/route/clientset/versioned"
	"github.com/spf13/cobra"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/mmahut/oc-find-waste/internal/ocp"
	"github.com/mmahut/oc-find-waste/internal/pricing"
	"github.com/mmahut/oc-find-waste/internal/prom"
	"github.com/mmahut/oc-find-waste/internal/report"
	"github.com/mmahut/oc-find-waste/internal/scanner"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(2)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "oc-find-waste",
		Short: "Scan an OpenShift/Kubernetes namespace for wasted resources",
		Long: `oc-find-waste is a read-only CLI that scans a namespace for idle workloads,
orphaned storage, over-provisioned pods, and unused Routes, then reports an
estimated monthly cost of the waste.`,
	}
	root.AddCommand(newScanCmd())
	return root
}

type scanOptions struct {
	kubeconfig    string
	namespace     string
	allNamespaces bool
	window        string
	timeout       string
	pricing       string
	prometheusURL string
	skip          []string
	only          []string
	output        string
	noColor       bool
	rightsize     bool
	verbose       bool
}

func newScanCmd() *cobra.Command {
	opts := &scanOptions{}

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan a namespace for wasted resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScan(opts)
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.kubeconfig, "kubeconfig", "", "path to kubeconfig (defaults to $KUBECONFIG or ~/.kube/config)")
	f.StringVarP(&opts.namespace, "namespace", "n", "", "namespace to scan (defaults to current context namespace)")
	f.BoolVarP(&opts.allNamespaces, "all-namespaces", "A", false, "scan every namespace the user can list")
	f.StringVar(&opts.window, "window", "7d", "lookback window for metrics-based scanners (e.g. 7d, 24h)")
	f.StringVar(&opts.timeout, "timeout", "2m", "maximum time to wait for all scanners to complete (e.g. 2m, 90s)")
	f.StringVar(&opts.pricing, "pricing", "on-prem", "pricing profile name (aws, azure, gcp, on-prem) or path to YAML file")
	f.StringVar(&opts.prometheusURL, "prometheus-url", "", "override Prometheus endpoint (auto-detected by default)")
	f.StringArrayVar(&opts.skip, "skip", nil, "scanner names to skip (repeatable)")
	f.StringArrayVar(&opts.only, "only", nil, "scanner names to run exclusively (repeatable)")
	f.StringVarP(&opts.output, "output", "o", "text", "output format: text or json")
	f.BoolVar(&opts.noColor, "no-color", false, "disable ANSI colors")
	f.BoolVar(&opts.rightsize, "rightsize", false, "print suggested resource patch YAML after the report (does not apply changes)")
	f.BoolVarP(&opts.verbose, "verbose", "v", false, "log per-scanner progress to stderr")

	return cmd
}

var dayRe = regexp.MustCompile(`(\d+)d`)

// parseWindow accepts Go durations plus a "d" suffix for days (e.g. "7d", "1d12h").
func parseWindow(s string) (time.Duration, error) {
	expanded := dayRe.ReplaceAllStringFunc(s, func(m string) string {
		n, _ := strconv.Atoi(dayRe.FindStringSubmatch(m)[1])
		return fmt.Sprintf("%dh", n*24)
	})
	d, err := time.ParseDuration(expanded)
	if err != nil {
		return 0, fmt.Errorf("invalid --window %q: use a Go duration or days suffix (e.g. 7d, 24h, 1d12h)", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("--window must be positive, got %q", s)
	}
	return d, nil
}

func runScan(opts *scanOptions) error {
	rest.SetDefaultWarningHandler(rest.NoWarnings{})

	timeout, err := time.ParseDuration(opts.timeout)
	if err != nil || timeout <= 0 {
		return fmt.Errorf("invalid --timeout %q: use a Go duration (e.g. 2m, 90s)", opts.timeout)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	window, err := parseWindow(opts.window)
	if err != nil {
		return err
	}

	pricingProfile, err := pricing.Load(opts.pricing)
	if err != nil {
		return fmt.Errorf("loading pricing profile: %w", err)
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if opts.kubeconfig != "" {
		loadingRules.ExplicitPath = opts.kubeconfig
	}
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	restConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		return fmt.Errorf("building kubeconfig: %w", err)
	}

	if opts.allNamespaces && opts.namespace != "" {
		return fmt.Errorf("--namespace and --all-namespaces are mutually exclusive")
	}

	ns := opts.namespace
	if !opts.allNamespaces && ns == "" {
		var err2 error
		ns, _, err2 = kubeConfig.Namespace()
		if err2 != nil {
			return fmt.Errorf("resolving namespace: %w", err2)
		}
	}

	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("creating kubernetes client: %w", err)
	}

	var appsClient osappsv1client.Interface
	var imageClient osimagev1client.Interface
	var buildClient osbuildv1client.Interface
	var routeClient osroutev1client.Interface
	if ocp.IsOpenShift(client.Discovery()) {
		if opts.verbose {
			fmt.Fprintln(os.Stderr, "OpenShift APIs detected")
		}
		appsClient, err = ocp.NewAppsClient(restConfig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not create OpenShift apps client: %v\n", err)
		}
		imageClient, err = ocp.NewImageClient(restConfig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not create OpenShift image client: %v\n", err)
		}
		buildClient, err = ocp.NewBuildClient(restConfig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not create OpenShift build client: %v\n", err)
		}
		routeClient, err = ocp.NewRouteClient(restConfig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not create OpenShift route client: %v\n", err)
		}
	} else {
		ocp.LogIfNotOpenShift(opts.verbose)
	}

	// Extract bearer token for Prometheus (same token the kube client uses).
	bearerToken := restConfig.BearerToken

	// Look up the external thanos-querier Route so workstation users don't need --prometheus-url.
	var thanosRouteURL string
	if appsClient != nil { // only on OpenShift
		thanosRouteURL = ocp.ThanosRouteURL(ctx, restConfig)
	}

	promClient := prom.Discover(ctx, opts.prometheusURL, thanosRouteURL, bearerToken)
	if opts.verbose && promClient != nil {
		fmt.Fprintln(os.Stderr, "Prometheus endpoint reachable")
	}

	allScanners := []scanner.Scanner{
		scanner.NewScaledToZero(client, appsClient),
		scanner.NewCompletedJobs(client),
		scanner.NewOrphanedPVCs(client, pricingProfile),
		scanner.NewUnusedImageStreams(client, imageClient, buildClient),
		scanner.NewOverProvisioned(client, promClient, pricingProfile, window),
		scanner.NewUnusedRoutes(routeClient, promClient, window),
	}
	enabled := filterScanners(allScanners, opts.only, opts.skip)

	// Build the list of namespaces to scan.
	var namespaces []string
	if opts.allNamespaces {
		nsList, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("listing namespaces: %w", err)
		}
		for i := range nsList.Items {
			namespaces = append(namespaces, nsList.Items[i].Name)
		}
	} else {
		namespaces = []string{ns}
	}

	var findings []scanner.Finding
	hadErr := false

	for _, namespace := range namespaces {
		for _, s := range enabled {
			if opts.verbose {
				fmt.Fprintf(os.Stderr, "[%s] running scanner: %s\n", namespace, s.Name())
			}
			ff, scanErr := s.Scan(ctx, namespace)
			if scanErr != nil {
				if opts.allNamespaces && (k8serrors.IsForbidden(scanErr) || k8serrors.IsUnauthorized(scanErr)) {
					if opts.verbose {
						fmt.Fprintf(os.Stderr, "[%s] scanner %s: skipped (no access)\n", namespace, s.Name())
					}
					continue
				}
				fmt.Fprintf(os.Stderr, "warning: [%s] scanner %s: %v\n", namespace, s.Name(), scanErr)
				hadErr = true
				continue
			}
			findings = append(findings, ff...)
		}
	}

	reportOpts := report.Options{
		Namespace:     ns,
		AllNamespaces: opts.allNamespaces,
		Window:        opts.window,
		Pricing:       opts.pricing,
		NoColor:       opts.noColor,
		Output:        opts.output,
		Rightsize:     opts.rightsize,
	}
	if err := report.Render(os.Stdout, findings, reportOpts); err != nil {
		return fmt.Errorf("rendering report: %w", err)
	}

	if hadErr {
		os.Exit(1)
	}
	return nil
}

func filterScanners(all []scanner.Scanner, only, skip []string) []scanner.Scanner {
	if len(only) > 0 {
		onlySet := make(map[string]bool, len(only))
		for _, n := range only {
			onlySet[n] = true
		}
		var result []scanner.Scanner
		for _, s := range all {
			if onlySet[s.Name()] {
				result = append(result, s)
			}
		}
		return result
	}
	if len(skip) > 0 {
		skipSet := make(map[string]bool, len(skip))
		for _, n := range skip {
			skipSet[n] = true
		}
		var result []scanner.Scanner
		for _, s := range all {
			if !skipSet[s.Name()] {
				result = append(result, s)
			}
		}
		return result
	}
	return all
}
