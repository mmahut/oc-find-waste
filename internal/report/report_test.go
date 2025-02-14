package report_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmahut/oc-find-waste/internal/report"
	"github.com/mmahut/oc-find-waste/internal/scanner"
)

var baseOpts = report.Options{
	Namespace: "test-ns",
	Window:    "7d",
	Pricing:   "on-prem",
	NoColor:   true,
	Output:    "text",
}

func TestRenderText_NoFindings(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Render(&buf, nil, baseOpts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "No idle resources found") {
		t.Errorf("expected empty-state message, got:\n%s", out)
	}
}

func TestRenderText_WithFinding(t *testing.T) {
	findings := []scanner.Finding{
		{
			Kind:       "Deployment",
			Namespace:  "test-ns",
			Name:       "legacy-worker",
			Reason:     "scaled to 0 (resource age: 47d)",
			Severity:   scanner.SeverityWarning,
			Suggestion: "if no longer used, delete the Deployment",
		},
	}
	var buf bytes.Buffer
	if err := report.Render(&buf, findings, baseOpts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	checks := []string{
		"Deployment",
		"legacy-worker",
		"scaled to 0",
		"delete the Deployment",
		"Findings: 1",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
}

func TestRenderJSON(t *testing.T) {
	findings := []scanner.Finding{
		{
			Kind:      "Deployment",
			Namespace: "test-ns",
			Name:      "idle",
			Reason:    "scaled to 0 (resource age: 10d)",
			Severity:  scanner.SeverityWarning,
		},
	}
	opts := baseOpts
	opts.Output = "json"
	var buf bytes.Buffer
	if err := report.Render(&buf, findings, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out []scanner.Finding
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("JSON does not round-trip: %v\nraw: %s", err, buf.String())
	}
	if len(out) != 1 || out[0].Name != "idle" {
		t.Errorf("unexpected round-trip result: %+v", out)
	}
}
