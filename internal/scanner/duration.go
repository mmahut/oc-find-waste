package scanner

import (
	"fmt"
	"time"
)

// promDuration formats d as a PromQL duration string using the coarsest
// whole unit: hours, then minutes, then seconds.
// Examples: 7d→"168h", 90m→"90m", 30m→"30m", 45s→"45s".
func promDuration(d time.Duration) string {
	if h := int64(d.Hours()); time.Duration(h)*time.Hour == d {
		return fmt.Sprintf("%dh", h)
	}
	if m := int64(d.Minutes()); time.Duration(m)*time.Minute == d {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%ds", int64(d.Seconds()))
}

// fmtWindow formats d for human-readable output (finding Reason strings).
// Prefers days, then hours, then minutes, then seconds.
// Examples: 7*24h→"7d", 24h→"1d", 90m→"90m", 30m→"30m".
func fmtWindow(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}
