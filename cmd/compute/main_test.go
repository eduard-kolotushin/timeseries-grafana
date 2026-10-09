package main

import (
	"bytes"
	"strings"
	"testing"
)

// The flag set is the binary's whole interface, so its refusals are the contract: an
// unknown flag exits nonzero, -h exits zero, and a missing token is refused before any
// listener is opened — the same rule ServeCompute enforces in-process.
func TestRunFlagsAndMissingToken(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		env        map[string]string
		wantCode   int
		wantStderr string
	}{
		{name: "unknown flag", args: []string{"--nope"}, wantCode: 1},
		{name: "help", args: []string{"-h"}, wantCode: 0},
		{name: "missing token", env: map[string]string{"FORECAST_COMPUTE_TOKEN": ""}, wantCode: 1, wantStderr: "FORECAST_COMPUTE_TOKEN"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			var stderr bytes.Buffer
			getenv := func(key string) string {
				if v, ok := tc.env[key]; ok {
					return v
				}
				return ""
			}
			if got := run(tc.args, getenv, &stderr); got != tc.wantCode {
				t.Fatalf("exit=%d want %d (stderr=%s)", got, tc.wantCode, stderr.String())
			}
			if tc.wantStderr != "" && !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Fatalf("stderr=%q want %q", stderr.String(), tc.wantStderr)
			}
		})
	}
}
