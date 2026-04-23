package scanner

import (
	"testing"
	"time"
)

func TestPromDuration(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{7 * 24 * time.Hour, "168h"},
		{24 * time.Hour, "24h"},
		{90 * time.Minute, "90m"},
		{30 * time.Minute, "30m"},
		{45 * time.Second, "45s"},
	} {
		if got := promDuration(tc.d); got != tc.want {
			t.Errorf("promDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestFmtWindow(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{7 * 24 * time.Hour, "7d"},
		{24 * time.Hour, "1d"},
		{90 * time.Minute, "90m"},
		{30 * time.Minute, "30m"},
		{45 * time.Second, "45s"},
	} {
		if got := fmtWindow(tc.d); got != tc.want {
			t.Errorf("fmtWindow(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
