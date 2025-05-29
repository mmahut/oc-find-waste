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
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/mmahut/oc-find-waste/internal/ocp"
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
	ctx := context.Background()

	if _, err := parseWindow(opts.window); err != nil {
		return err
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

	ns := opts.namespace
	if ns == "" {
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
	} else {
		ocp.LogIfNotOpenShift(opts.verbose)
	}

	allScanners := []scanner.Scanner{
		scanner.NewScaledToZero(client, appsClient),
		scanner.NewCompletedJobs(client),
		scanner.NewOrphanedPVCs(client),
		scanner.NewUnusedImageStreams(client, imageClient, buildClient),
	}
	enabled := filterScanners(allScanners, opts.only, opts.skip)

	var findings []scanner.Finding
	hadErr := false

	for _, s := range enabled {
		if opts.verbose {
			fmt.Fprintf(os.Stderr, "running scanner: %s\n", s.Name())
		}
		ff, scanErr := s.Scan(ctx, ns)
		if scanErr != nil {
			fmt.Fprintf(os.Stderr, "warning: scanner %s: %v\n", s.Name(), scanErr)
			hadErr = true
			continue
		}
		findings = append(findings, ff...)
	}

	reportOpts := report.Options{
		Namespace: ns,
		Window:    opts.window,
		Pricing:   opts.pricing,
		NoColor:   opts.noColor,
		Output:    opts.output,
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
