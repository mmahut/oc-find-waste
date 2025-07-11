package pricing

import (
	"embed"
	"fmt"
	"os"

	"sigs.k8s.io/yaml"
)

//go:embed profiles/*.yaml
var profileFS embed.FS

// Profile holds per-unit pricing rates for a cloud or on-prem environment.
type Profile struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	CPUCoreHour float64 `json:"cpu_core_hour"`
	MemGBHour   float64 `json:"memory_gb_hour"`
	PVCGBMonth  float64 `json:"pvc_gb_month"`
}

// Load returns the named built-in profile (e.g. "aws", "on-prem") or reads
// the file at nameOrPath if no built-in matches.
func Load(nameOrPath string) (*Profile, error) {
	data, err := profileFS.ReadFile("profiles/" + nameOrPath + ".yaml")
	if err == nil {
		return parse(nameOrPath, data)
	}
	data, err = os.ReadFile(nameOrPath)
	if err != nil {
		return nil, fmt.Errorf("pricing profile %q not found as built-in or file: %w", nameOrPath, err)
	}
	return parse(nameOrPath, data)
}

// BuiltinNames returns the keys of all embedded profiles.
func BuiltinNames() []string {
	entries, _ := profileFS.ReadDir("profiles")
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		n := e.Name()
		if len(n) > 5 && n[len(n)-5:] == ".yaml" {
			names = append(names, n[:len(n)-5])
		}
	}
	return names
}

func parse(src string, data []byte) (*Profile, error) {
	var p Profile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parsing pricing profile %q: %w", src, err)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("pricing profile %q: missing 'name' field", src)
	}
	if p.Description == "" {
		return nil, fmt.Errorf("pricing profile %q: missing 'description' field", src)
	}
	if p.CPUCoreHour < 0 || p.MemGBHour < 0 || p.PVCGBMonth < 0 {
		return nil, fmt.Errorf("pricing profile %q: all values must be non-negative", src)
	}
	return &p, nil
}

// WorkloadMonthlyUSD returns the estimated monthly cost for a workload given
// CPU cores and memory in GiB, assuming 730 hours per month.
func (p *Profile) WorkloadMonthlyUSD(cpuCores, memGiB float64) float64 {
	return (cpuCores*p.CPUCoreHour + memGiB*p.MemGBHour) * 730
}

// PVCMonthlyUSD returns the estimated monthly storage cost for a PVC of sizeGiB.
func (p *Profile) PVCMonthlyUSD(sizeGiB float64) float64 {
	return sizeGiB * p.PVCGBMonth
}
