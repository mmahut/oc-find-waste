package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/fatih/color"

	"github.com/mmahut/oc-find-waste/internal/scanner"
)

// Options controls how the report is rendered.
type Options struct {
	Namespace     string
	AllNamespaces bool
	Window        string
	Pricing       string
	NoColor       bool
	Output        string // "text" or "json"
	Rightsize     bool   // print patch YAML after the report summary
}

// Render writes the report for findings to w.
func Render(w io.Writer, findings []scanner.Finding, opts Options) error {
	if opts.Output == "json" {
		return renderJSON(w, findings)
	}
	return renderText(w, findings, opts)
}

func renderJSON(w io.Writer, findings []scanner.Finding) error {
	if findings == nil {
		findings = []scanner.Finding{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(findings)
}

func renderText(w io.Writer, findings []scanner.Finding, opts Options) error {
	if opts.NoColor {
		color.NoColor = true
	}

	bold := color.New(color.Bold).SprintFunc()
	dim := color.New(color.Faint).SprintFunc()
	arrow := color.New(color.FgCyan).SprintFunc()
	cost := color.New(color.FgYellow).SprintFunc()
	good := color.New(color.FgGreen, color.Bold).SprintFunc()

	if opts.AllNamespaces {
		fmt.Fprintf(w, "Scanning all namespaces\n")
	} else {
		fmt.Fprintf(w, "Scanning namespace: %s\n", bold(opts.Namespace))
	}
	fmt.Fprintf(w, "Window: %s  │  Pricing: %s\n\n", opts.Window, opts.Pricing)

	if len(findings) == 0 {
		fmt.Fprintln(w, good("✓ No idle resources found."))
		return nil
	}

	if opts.AllNamespaces {
		renderTextByNamespace(w, findings, bold, dim, arrow, cost)
	} else {
		renderTextByKind(w, findings, bold, dim, arrow, cost)
	}

	sep := strings.Repeat("─", 45)

	fmt.Fprintln(w, sep)

	var totalCost, savingsCost float64
	for _, f := range findings {
		totalCost += f.MonthlyCost
	}
	// savings: only findings where Patch/suggestion implies a lower cost
	// For now, totalCost == waste (all findings contribute); savings is same.
	savingsCost = totalCost

	fmt.Fprintf(w, "Findings: %d\n", len(findings))
	if totalCost > 0 {
		fmt.Fprintf(w, "Estimated monthly waste: %s\n", cost(fmt.Sprintf("$%.2f", totalCost)))
		if savingsCost > 0 {
			pct := 100.0
			fmt.Fprintf(w, "Potential savings:       %s  (%.0f%%)\n",
				cost(fmt.Sprintf("$%.2f", savingsCost)), pct)
		}
	}

	if opts.Rightsize {
		var patches []scanner.Finding
		for _, f := range findings {
			if f.Patch != "" {
				patches = append(patches, f)
			}
		}
		if len(patches) > 0 {
			fmt.Fprintln(w)
			fmt.Fprintln(w, bold("# Suggested resource patches"))
			for _, f := range patches {
				fmt.Fprintf(w, "\n%s\n", f.Patch)
			}
		}
	}

	return nil
}

type colorFunc func(a ...interface{}) string

func renderTextByKind(w io.Writer, findings []scanner.Finding, bold, dim, arrow, cost colorFunc) {
	var kinds []string
	byKind := make(map[string][]scanner.Finding)
	for _, f := range findings {
		if _, seen := byKind[f.Kind]; !seen {
			kinds = append(kinds, f.Kind)
		}
		byKind[f.Kind] = append(byKind[f.Kind], f)
	}
	for _, k := range kinds {
		fmt.Fprintln(w, bold(k))
		printFindings(w, byKind[k], dim, arrow, cost)
		fmt.Fprintln(w)
	}
}

func renderTextByNamespace(w io.Writer, findings []scanner.Finding, bold, dim, arrow, cost colorFunc) {
	var namespaces []string
	byNS := make(map[string][]scanner.Finding)
	for _, f := range findings {
		if _, seen := byNS[f.Namespace]; !seen {
			namespaces = append(namespaces, f.Namespace)
		}
		byNS[f.Namespace] = append(byNS[f.Namespace], f)
	}
	for _, ns := range namespaces {
		fmt.Fprintln(w, bold("Namespace: "+ns))
		renderTextByKind(w, byNS[ns], bold, dim, arrow, cost)
	}
}

func printFindings(w io.Writer, findings []scanner.Finding, dim, arrow, cost colorFunc) {
	for i, f := range findings {
		line := fmt.Sprintf("  %-30s %s", f.Name, f.Reason)
		if f.MonthlyCost > 0 {
			line += fmt.Sprintf("  %s", cost(fmt.Sprintf("$%.2f/mo", f.MonthlyCost)))
		}
		fmt.Fprintln(w, line)
		if f.Detail != "" {
			fmt.Fprintln(w, dim("    "+f.Detail))
		}
		if f.Suggestion != "" {
			fmt.Fprintln(w, "    "+arrow("→")+" "+f.Suggestion)
		}
		if i < len(findings)-1 {
			fmt.Fprintln(w)
		}
	}
}
