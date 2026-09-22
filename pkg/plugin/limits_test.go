package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"

	forecast "github.com/eduard-kolotushin/timeseries-forecast"
)

func clearLimitEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"FORECAST_MAX_INFLIGHT",
		gfPluginPrefix + "MAX_INFLIGHT",
		gfPluginDSPrefix + "MAX_INFLIGHT",
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
			env:  map[string]string{"FORECAST_MAX_INFLIGHT": "3", gfPluginPrefix + "MAX_INFLIGHT": "9"},
			want: 3,
		},
		{name: "GF_PLUGIN", env: map[string]string{gfPluginPrefix + "MAX_INFLIGHT": "6"}, want: 6},
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

// The SDK's own default receive limit is the body cap's twin, and a body at the cap then
// fails the transport before the handler can answer 413.
func TestGRPCSettingsClearsTheBodyCap(t *testing.T) {
	got := GRPCSettings().MaxReceiveMsgSize
	if got <= int(maxForecastBodyBytes) {
		t.Fatalf("gRPC receive limit %d does not clear the %d byte body cap", got, maxForecastBodyBytes)
	}
	if int64(got) <= int64(defaultMaxForecastBody) {
		t.Fatalf("gRPC receive limit %d does not clear the SDK's own default of %d", got, defaultMaxForecastBody)
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
}
