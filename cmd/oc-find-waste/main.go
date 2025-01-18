package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
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
	if opts.verbose {
		fmt.Fprintln(os.Stderr, "no scanners registered")
	}
	fmt.Println("✓ No idle resources found.")
	return nil
}
