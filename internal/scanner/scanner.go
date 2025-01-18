package scanner

import "context"

type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityCritical
)

type Finding struct {
	Kind        string   `json:"kind"`
	Namespace   string   `json:"namespace"`
	Name        string   `json:"name"`
	Reason      string   `json:"reason"`
	Detail      string   `json:"detail,omitempty"`
	MonthlyCost float64  `json:"monthly_cost_usd,omitempty"`
	Suggestion  string   `json:"suggestion,omitempty"`
	Severity    Severity `json:"severity"`
	Patch       string   `json:"patch,omitempty"`
}

type Scanner interface {
	Name() string
	Scan(ctx context.Context, namespace string) ([]Finding, error)
}
