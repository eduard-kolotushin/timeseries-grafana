package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"

	forecast "github.com/eduard-kolotushin/timeseries-forecast"
	"github.com/eduard-kolotushin/timeseries-grafana/pkg/store"
)

func clearLimitEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"FORECAST_MAX_INFLIGHT",
		store.PluginEnvPrefixApp + "MAX_INFLIGHT",
		store.PluginEnvPrefixDatasource + "MAX_INFLIGHT",
	}
	for _, k := range keys {
		t.Setenv(k, "")
	}
}

func TestParseMaxInflight(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{in: "", want: 0},
		{in: "  ", want: 0},
		{in: "8", want: 8},
		{in: "0", want: defaultMaxInflight},
		{in: "-1", want: defaultMaxInflight},
		{in: "nope", want: defaultMaxInflight},
	} {
		if got := parseMaxInflight(tc.in); got != tc.want {
			t.Fatalf("parseMaxInflight(%q)=%d want %d", tc.in, got, tc.want)
		}
	}
}

func TestMaxInflightFrom(t *testing.T) {
	jsonN, _ := json.Marshal(map[string]any{"maxInflight": 8})
	for _, tc := range []struct {
		name string
		env  map[string]string
		cfg  map[string]string
		json []byte
		want int
	}{
		{name: "default", want: defaultMaxInflight},
		{name: "FORECAST_MAX_INFLIGHT", env: map[string]string{"FORECAST_MAX_INFLIGHT": "2"}, want: 2},
		{
			name: "FORECAST wins over GF_PLUGIN",
			env:  map[string]string{"FORECAST_MAX_INFLIGHT": "3", store.PluginEnvPrefixApp + "MAX_INFLIGHT": "9"},
			want: 3,
		},
		{name: "GF_PLUGIN", env: map[string]string{store.PluginEnvPrefixApp + "MAX_INFLIGHT": "6"}, want: 6},
		{name: "ini", cfg: map[string]string{"max_inflight": "5"}, want: 5},
		{name: "jsonData", json: jsonN, want: 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearLimitEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			ctx := context.Background()
			if tc.cfg != nil {
				ctx = backend.WithGrafanaConfig(ctx, backend.NewGrafanaCfg(tc.cfg))
			}
			if got := maxInflightFrom(ctx, tc.json); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}

func TestWorkLimiterBusy(t *testing.T) {
	lim := newWorkLimiter(1)
	release, err := lim.try(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lim.try(context.Background()); !errors.Is(err, errBusy) {
		t.Fatalf("second try: %v", err)
	}
	release()
	if _, err := lim.try(context.Background()); err != nil {
		t.Fatalf("after release: %v", err)
	}
}

func TestWorkLimiterCanceled(t *testing.T) {
	lim := newWorkLimiter(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := lim.try(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestRecoverHTTP(t *testing.T) {
	rr := httptest.NewRecorder()
	func() {
		defer recoverHTTP(rr)
		panic("boom")
	}()
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestHttpStatusForLoadErrors(t *testing.T) {
	if httpStatusFor(errBusy) != http.StatusTooManyRequests {
		t.Fatalf("busy=%d", httpStatusFor(errBusy))
	}
	if httpStatusFor(errTrainTooLong) != http.StatusRequestEntityTooLarge {
		t.Fatalf("train=%d", httpStatusFor(errTrainTooLong))
	}
	// The two caps added for F1/F2 are 413-class on every path: the app POST
	// answers them directly, and dataStatusFor carries the datasource path's.
	if httpStatusFor(errTrainBodyTooLarge) != http.StatusRequestEntityTooLarge {
		t.Fatalf("body points=%d", httpStatusFor(errTrainBodyTooLarge))
	}
	if httpStatusFor(errWindowTooManyPoints) != http.StatusRequestEntityTooLarge {
		t.Fatalf("window=%d", httpStatusFor(errWindowTooManyPoints))
	}
	if dataStatusFor(errWindowTooManyPoints) != backend.Status(413) {
		t.Fatalf("data window=%d", dataStatusFor(errWindowTooManyPoints))
	}
	if dataStatusFor(errBusy) != backend.StatusTooManyRequests {
		t.Fatalf("data busy=%d", dataStatusFor(errBusy))
	}
	// The library's own cap error maps to the same 413 on both paths, so a window
	// that reaches the library before the plugin's check is answered identically.
	if httpStatusFor(forecast.ErrTooManyPoints) != http.StatusRequestEntityTooLarge {
		t.Fatalf("library window=%d", httpStatusFor(forecast.ErrTooManyPoints))
	}
	if dataStatusFor(forecast.ErrTooManyPoints) != backend.Status(413) {
		t.Fatalf("library data window=%d", dataStatusFor(forecast.ErrTooManyPoints))
	}
	// The panel key set's cap is a request-shape bound, not a size cap: the plan's
	// "existing 400 path" is what it answers, and a 500 here would make the overlay
	// treat a client-side bug as a fatal load limit.
	if httpStatusFor(errTooManyPanelKeys) != http.StatusBadRequest {
		t.Fatalf("panel keys=%d", httpStatusFor(errTooManyPanelKeys))
	}
	if dataStatusFor(errTooManyPanelKeys) != backend.StatusBadRequest {
		t.Fatalf("data panel keys=%d", dataStatusFor(errTooManyPanelKeys))
	}
}

// TestCheckForecastWindowSignedGrid pins the plugin's mirror of the library's
// signed window arithmetic: k <= 0 is a backcast on the same grid, so a past
// window is counted instead of being clamped to k >= 1 (which would let an
// arbitrarily wide past window through to the library).
func TestCheckForecastWindowSignedGrid(t *testing.T) {
	const last, step = int64(1000), int64(100)
	tests := []struct {
		name        string
		from, to    int64
		wantTooMany bool
	}{
		{"future window", 1100, 1250, false},            // k = 1..2
		{"straddling the last point", 850, 1250, false}, // k = -1..2
		{"past only", 500, 900, false},                  // k = -5..-1
		{"exactly the last point", 1000, 1000, false},   // k = 0
		{"off-grid in the past", 950, 950, false},       // no grid point, the library answers
		{"inverted", 1200, 1100, false},
		{"one over the cap forward", 1100, last + step*(maxForecastPoints+1), true},
		{"one over the cap backward", last - step*maxForecastPoints, 1000, true},
		{"exactly the cap backward", last - step*(maxForecastPoints-1), 1000, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkForecastWindow(last, step, tt.from, tt.to)
			if tt.wantTooMany {
				if !errors.Is(err, errWindowTooManyPoints) {
					t.Fatalf("err = %v, want errWindowTooManyPoints", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
}

func TestCheckPanelKeys(t *testing.T) {
	atCap := make([]string, maxPanelKeys)
	for i := range atCap {
		atCap[i] = strings.Repeat("a", 64)
	}
	if err := checkPanelKeys(atCap); err != nil {
		t.Fatalf("a set at the cap must be accepted: %v", err)
	}
	if err := checkPanelKeys(append(atCap, strings.Repeat("b", 64))); !errors.Is(err, errTooManyPanelKeys) {
		t.Fatalf("a set above the cap must be errTooManyPanelKeys, got %v", err)
	}
}
