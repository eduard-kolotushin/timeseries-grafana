package plugin

import "testing"

// The Retrain schedules table shows a row's stored lookback, so it has to read like
// 21d rather than 504h0m0s; 21d is also the form the stored specs and this package's
// fixtures already used. Nothing parses the string back.
func TestLookbackStringRendersWholeUnits(t *testing.T) {
	tests := []struct {
		name string
		ms   int64
		want string
	}{
		{"no window", 0, ""},
		{"negative", -1, ""},
		{"whole days", 21 * 24 * 60 * 60 * 1000, "21d"},
		{"one day", 24 * 60 * 60 * 1000, "1d"},
		{"whole hours", 6 * 60 * 60 * 1000, "6h"},
		{"whole minutes", 30 * 60 * 1000, "30m"},
		{"sub-minute", 90 * 1000, "1m30s"},
		{"whole minutes beyond an hour", (2*60 + 15) * 60 * 1000, "135m"},
		{"fraction of a second", 90_500, "1m30.5s"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := lookbackString(tc.ms); got != tc.want {
				t.Fatalf("lookbackString(%d)=%q, want %q", tc.ms, got, tc.want)
			}
		})
	}
}
