package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

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

func runScan(opts *scanOptions) error {
	ctx := context.Background()

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

	allScanners := []scanner.Scanner{
		scanner.NewScaledToZero(client),
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
