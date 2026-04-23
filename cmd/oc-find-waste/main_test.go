package main

import "testing"

func TestValidateOutput(t *testing.T) {
	for _, tc := range []struct {
		input   string
		wantErr bool
	}{
		{"text", false},
		{"json", false},
		{"JSON", true}, // case-sensitive
		{"xml", true},
		{"", true},
	} {
		err := validateOutput(tc.input)
		if (err != nil) != tc.wantErr {
			t.Errorf("validateOutput(%q) error=%v, wantErr=%v", tc.input, err, tc.wantErr)
		}
	}
}
