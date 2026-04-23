package pricing_test

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/mmahut/oc-find-waste/internal/pricing"
)

func TestLoadBuiltins(t *testing.T) {
	for _, name := range []string{"aws", "azure", "gcp", "on-prem"} {
		t.Run(name, func(t *testing.T) {
			p, err := pricing.Load(name)
			if err != nil {
				t.Fatalf("Load(%q) failed: %v", name, err)
			}
			if p.Name == "" {
				t.Error("loaded profile has empty Name")
			}
			if p.Description == "" {
				t.Error("loaded profile has empty Description")
			}
			if p.CPUCoreHour < 0 || p.MemGBHour < 0 || p.PVCGBMonth < 0 {
				t.Error("loaded profile has negative values")
			}
		})
	}
}

func TestLoadCustomFile(t *testing.T) {
	yaml := `
name: test-profile
description: "unit test profile"
cpu_core_hour: 0.10
memory_gb_hour: 0.01
pvc_gb_month: 0.05
`
	f := filepath.Join(t.TempDir(), "custom.yaml")
	if err := os.WriteFile(f, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := pricing.Load(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name != "test-profile" {
		t.Errorf("got name %q, want test-profile", p.Name)
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	f := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(f, []byte("{ not: valid: yaml: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := pricing.Load(f)
	if err == nil {
		t.Fatal("expected error for malformed YAML, got nil")
	}
}

func TestLoadMissingName(t *testing.T) {
	content := "description: \"no name\"\ncpu_core_hour: 0.01\n"
	f := filepath.Join(t.TempDir(), "noname.yaml")
	os.WriteFile(f, []byte(content), 0o600) //nolint
	_, err := pricing.Load(f)
	if err == nil {
		t.Fatal("expected error for missing name, got nil")
	}
}

func TestLoadNegativeValues(t *testing.T) {
	content := "name: bad\ndescription: bad\ncpu_core_hour: -1\nmemory_gb_hour: 0\npvc_gb_month: 0\n"
	f := filepath.Join(t.TempDir(), "negative.yaml")
	os.WriteFile(f, []byte(content), 0o600) //nolint
	_, err := pricing.Load(f)
	if err == nil {
		t.Fatal("expected error for negative value, got nil")
	}
}

func TestLoadNotFound(t *testing.T) {
	_, err := pricing.Load("does-not-exist")
	if err == nil {
		t.Fatal("expected error for missing profile, got nil")
	}
}

func TestPVCMonthlyUSD(t *testing.T) {
	p, _ := pricing.Load("aws")
	// 50 GB * $0.08/GB/month = $4.00
	got := p.PVCMonthlyUSD(50)
	want := 50 * 0.08
	if math.Abs(got-want) > 0.001 {
		t.Errorf("PVCMonthlyUSD(50) = %.4f, want %.4f", got, want)
	}
}

func TestWorkloadMonthlyUSD(t *testing.T) {
	p, _ := pricing.Load("aws")
	// 1 core, 2 GB, 730h: (1*0.0416 + 2*0.0056) * 730
	got := p.WorkloadMonthlyUSD(1, 2)
	want := (1*0.0416 + 2*0.0056) * 730
	if math.Abs(got-want) > 0.001 {
		t.Errorf("WorkloadMonthlyUSD(1, 2) = %.4f, want %.4f", got, want)
	}
}

func TestOnPremZeroCost(t *testing.T) {
	p, _ := pricing.Load("on-prem")
	if p.PVCMonthlyUSD(100) != 0 {
		t.Error("on-prem PVC cost should be 0")
	}
	if p.WorkloadMonthlyUSD(4, 8) != 0 {
		t.Error("on-prem workload cost should be 0")
	}
}
