package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

func f64ptr(v float64) *float64 { return &v }

func TestNextRun(t *testing.T) {
	now := time.Date(2026, 1, 15, 12, 34, 56, 0, time.UTC)
	for _, tc := range []struct {
		cron     string
		timezone string
		want     string
	}{
		{cron: "0 3 * * *", timezone: "UTC", want: "2026-01-16T03:00:00Z"},
		{cron: "*/5 * * * *", timezone: "UTC", want: "2026-01-15T12:35:00Z"},
		{cron: "@daily", timezone: "UTC", want: "2026-01-16T00:00:00Z"},
		{cron: "@hourly", timezone: "UTC", want: "2026-01-15T13:00:00Z"},
		{cron: "0 0 1 1 *", timezone: "UTC", want: "2027-01-01T00:00:00Z"},
		{cron: "0 3 * * *", timezone: "", want: "2026-01-16T03:00:00Z"},
		// Moscow is UTC+3, so a wall-clock schedule lands three hours earlier in UTC.
		{cron: "0 3 * * *", timezone: "Europe/Moscow", want: "2026-01-16T00:00:00Z"},
		{cron: "*/5 * * * *", timezone: "Europe/Moscow", want: "2026-01-15T12:35:00Z"},
		{cron: "@daily", timezone: "Europe/Moscow", want: "2026-01-15T21:00:00Z"},
		{cron: "@hourly", timezone: "Europe/Moscow", want: "2026-01-15T13:00:00Z"},
		{cron: "0 0 1 1 *", timezone: "Europe/Moscow", want: "2026-12-31T21:00:00Z"},
	} {
		t.Run(tc.cron+" "+tc.timezone, func(t *testing.T) {
			got, err := nextRun(tc.cron, tc.timezone, now)
			if err != nil {
				t.Fatal(err)
			}
			if got.Format(time.RFC3339) != tc.want {
				t.Fatalf("got %s want %s", got.Format(time.RFC3339), tc.want)
			}
			if !got.After(now) {
				t.Fatalf("%s is not after %s", got, now)
			}
		})
	}

	for _, tc := range []struct {
		name     string
		cron     string
		timezone string
		want     error
	}{
		{name: "empty cron", cron: "", timezone: "UTC", want: errInvalidCron},
		{name: "bad cron", cron: "nope", timezone: "UTC", want: errInvalidCron},
		{name: "bad timezone", cron: "0 3 * * *", timezone: "Mars/Olympus", want: errInvalidTimezone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := nextRun(tc.cron, tc.timezone, now); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
}

func clearRetrainEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"FORECAST_RETRAIN_ENABLED",
		"FORECAST_RETRAIN_TICK",
		"FORECAST_RETRAIN_LEASE",
		"FORECAST_RETRAIN_CRON",
		"FORECAST_GRAFANA_URL",
		"FORECAST_GRAFANA_TOKEN",
		gfPluginPrefix + "RETRAIN_ENABLED",
		gfPluginPrefix + "RETRAIN_TICK",
		gfPluginPrefix + "RETRAIN_LEASE",
		gfPluginPrefix + "RETRAIN_CRON",
		gfPluginPrefix + "GRAFANA_URL",
		gfPluginPrefix + "GRAFANA_TOKEN",
		gfPluginDSPrefix + "RETRAIN_CRON",
	}
	for _, k := range keys {
		t.Setenv(k, "")
	}
}

func TestComputeRetrain(t *testing.T) {
	jsonAll, _ := json.Marshal(map[string]any{
		"retrainCron":     "*/10 * * * *",
		"retrainTick":     "45s",
		"retrainLease":    "2m",
		"retrainEnabled":  false,
		"grafanaUrl":      "http://from-json:3000",
		"retrainTimezone": "Europe/Moscow",
	})
	for _, tc := range []struct {
		name     string
		env      map[string]string
		cfg      map[string]string
		settings backend.AppInstanceSettings
		want     retrainConfig
	}{
		{
			name: "defaults",
			want: retrainConfig{
				Enabled: true, Tick: defaultRetrainTick, Lease: defaultRetrainLease,
				Cron: defaultRetrainCron, Timezone: "", GrafanaURL: defaultGrafanaURL,
			},
		},
		{
			name: "env wins over everything",
			env: map[string]string{
				"FORECAST_RETRAIN_ENABLED":      "false",
				"FORECAST_RETRAIN_TICK":         "15s",
				"FORECAST_RETRAIN_LEASE":        "90s",
				"FORECAST_RETRAIN_CRON":         "*/2 * * * *",
				"FORECAST_GRAFANA_URL":          "http://from-env:3000",
				"FORECAST_GRAFANA_TOKEN":        "env-token",
				gfPluginPrefix + "RETRAIN_CRON": "*/30 * * * *",
			},
			settings: backend.AppInstanceSettings{JSONData: jsonAll},
			want: retrainConfig{
				Enabled: false, Tick: 15 * time.Second, Lease: 90 * time.Second,
				Cron: "*/2 * * * *", Timezone: "Europe/Moscow", GrafanaURL: "http://from-env:3000", Token: "env-token",
			},
		},
		{
			name: "ini after GF_PLUGIN env",
			cfg: map[string]string{
				"retrain_cron": "*/20 * * * *",
				"retrain_tick": "10s",
				"grafana_url":  "http://from-ini:3000",
			},
			want: retrainConfig{
				Enabled: true, Tick: 10 * time.Second, Lease: defaultRetrainLease,
				Cron: "*/20 * * * *", GrafanaURL: "http://from-ini:3000",
			},
		},
		{
			name:     "jsonData last, secure token",
			settings: backend.AppInstanceSettings{JSONData: jsonAll, DecryptedSecureJSONData: map[string]string{"grafanaToken": "secret"}},
			want: retrainConfig{
				Enabled: false, Tick: 45 * time.Second, Lease: 2 * time.Minute,
				Cron: "*/10 * * * *", Timezone: "Europe/Moscow", GrafanaURL: "http://from-json:3000", Token: "secret",
			},
		},
		{
			name: "bad values keep the defaults",
			env: map[string]string{
				"FORECAST_RETRAIN_TICK":  "-5s",
				"FORECAST_RETRAIN_LEASE": "not-a-duration",
			},
			want: retrainConfig{
				Enabled: true, Tick: defaultRetrainTick, Lease: defaultRetrainLease,
				Cron: defaultRetrainCron, GrafanaURL: defaultGrafanaURL,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearRetrainEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			ctx := context.Background()
			if tc.cfg != nil {
				ctx = backend.WithGrafanaConfig(ctx, backend.NewGrafanaCfg(tc.cfg))
			}
			if got := computeRetrain(ctx, tc.settings); got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

// dsReply mirrors the /api/ds/query envelope using the SDK's own frame
// marshaller, so the fake Grafana below answers with the real wire format.
type dsReply struct {
	Results map[string]*dsReplyResult `json:"results"`
}

type dsReplyResult struct {
	Status int         `json:"status"`
	Error  string      `json:"error,omitempty"`
	Frames data.Frames `json:"frames,omitempty"`
}

func testTrainSpec() []byte {
	raw, err := json.Marshal(retrainSpec{
		TrainSource: TrainSource{
			DatasourceUID: "ds-uid",
			Queries:       json.RawMessage(`[{"refId":"A","intervalMs":60000}]`),
			From:          1_000,
			To:            2_000,
			SeriesName:    "series-1",
		},
		Model:    "baseline",
		Alpha:    0.5,
		Beta:     0.1,
		Period:   24,
		Season:   "minute-week",
		Calendar: "",
		Lookback: "2h",
	})
	if err != nil {
		panic(err)
	}
	return raw
}

// testSpecRaw is testTrainSpec with mutate applied, for rows that differ from the
// default spec in exactly one way.
func testSpecRaw(t *testing.T, mutate func(*retrainSpec)) []byte {
	t.Helper()
	var spec retrainSpec
	if err := json.Unmarshal(testTrainSpec(), &spec); err != nil {
		t.Fatal(err)
	}
	mutate(&spec)
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestFetchFrames(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0).UTC()
	reply := dsReply{Results: map[string]*dsReplyResult{
		"A": {Status: 200, Frames: data.Frames{data.NewFrame("A",
			data.NewField("Time", nil, []time.Time{t0, t0.Add(time.Minute), t0.Add(2 * time.Minute)}),
			data.NewField("other", nil, []*float64{f64ptr(1), f64ptr(2), nil}),
		)}},
		"B": {Status: 200, Frames: data.Frames{data.NewFrame("B",
			data.NewField("Time", nil, []time.Time{t0, t0.Add(time.Minute)}),
			data.NewField("series-1", nil, []*float64{f64ptr(10), f64ptr(20)}),
		)}},
	}}
	raw, err := json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}

	// fetchRequest records what the poster actually sent, so the assertions below
	// cover the wire contract rather than the poster's internals.
	type fetchRequest struct {
		Method string
		Path   string
		CT     string
		Auth   string
		Body   struct {
			Queries json.RawMessage `json:"queries"`
			From    string          `json:"from"`
			To      string          `json:"to"`
		}
	}
	var got fetchRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got.Method = r.Method
		got.Path = r.URL.Path
		got.CT = r.Header.Get("Content-Type")
		got.Auth = r.Header.Get("Authorization")
		if err := json.Unmarshal(body, &got.Body); err != nil {
			t.Errorf("request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	// claimTime is the scheduler's clock for the relative-window row below.
	claimTime := time.UnixMilli(1_800_000_000_000)
	for _, tc := range []struct {
		name     string
		token    string
		wantAuth string
		spec     []byte
		clock    time.Time
		wantFrom string
		wantTo   string
	}{
		{
			// A panel whose picker held absolute dates is retrained on exactly those
			// dates, whatever the clock says.
			name: "an absolute spec replays the stored window", wantFrom: "1000", wantTo: "2000",
		},
		{
			name: "token sends a bearer header", token: "sa-token", wantAuth: "Bearer sa-token",
			wantFrom: "1000", wantTo: "2000",
		},
		{
			// The stored pair is only the browser's last resolution; a lookback window
			// must move with the clock or every cron tick refetches the same range.
			name: "a relative spec re-resolves the window at claim time",
			spec: testSpecRaw(t, func(s *retrainSpec) {
				s.Relative, s.LookbackMs = true, int64(6*time.Hour/time.Millisecond)
			}),
			clock:    claimTime,
			wantFrom: strconv.FormatInt(claimTime.UnixMilli()-6*3_600_000, 10),
			wantTo:   strconv.FormatInt(claimTime.UnixMilli(), 10),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got = fetchRequest{}
			rowSpec := tc.spec
			if rowSpec == nil {
				rowSpec = testTrainSpec()
			}
			clock := tc.clock
			if clock.IsZero() {
				clock = time.Now()
			}
			poster := &grafanaPoster{url: srv.URL, token: tc.token, client: srv.Client(), now: func() time.Time { return clock }}
			row := ScheduleRow{Scope: scopePanel, Key: "k", Spec: rowSpec}
			frames, err := poster.fetchFrames(context.Background(), row)
			if err != nil {
				t.Fatal(err)
			}
			if got.Method != http.MethodPost || got.Path != "/api/ds/query" {
				t.Fatalf("request %s %s", got.Method, got.Path)
			}
			if got.CT != "application/json" || got.Auth != tc.wantAuth {
				t.Fatalf("content-type=%q auth=%q want %q", got.CT, got.Auth, tc.wantAuth)
			}
			// The stored query objects travel verbatim: pkg/ never rewrites them.
			if string(got.Body.Queries) != `[{"refId":"A","intervalMs":60000}]` {
				t.Fatalf("queries=%s", got.Body.Queries)
			}
			if got.Body.From != tc.wantFrom || got.Body.To != tc.wantTo {
				t.Fatalf("range=%s..%s want %s..%s", got.Body.From, got.Body.To, tc.wantFrom, tc.wantTo)
			}
			if len(frames) != 2 {
				t.Fatalf("frames=%d", len(frames))
			}
			spec, err := parseRetrainSpec(row.Spec)
			if err != nil {
				t.Fatal(err)
			}
			series, err := seriesFromFrames(frames, spec)
			if err != nil {
				t.Fatal(err)
			}
			if series.Len() != 2 {
				t.Fatalf("len=%d", series.Len())
			}
			if ts := series.Times(); !ts[0].Equal(t0) || !ts[1].Equal(t0.Add(time.Minute)) {
				t.Fatalf("times=%v", ts)
			}
			if vs := series.Values(); vs[0] != 10 || vs[1] != 20 {
				t.Fatalf("values=%v", vs)
			}
		})
	}

	// Grafana refusing the scheduler's own credentials has to be tellable apart
	// from every other fetch failure: runScheduler counts exactly these.
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run("auth refusal "+strconv.Itoa(status), func(t *testing.T) {
			bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "nope", status)
			}))
			defer bad.Close()
			poster := &grafanaPoster{url: bad.URL, client: bad.Client()}
			_, err := poster.fetchFrames(context.Background(), ScheduleRow{Spec: testTrainSpec()})
			if !errors.Is(err, errGrafanaUnauthorized) {
				t.Fatalf("err=%v is not an auth refusal", err)
			}
		})
	}

	t.Run("status error", func(t *testing.T) {
		bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "nope", http.StatusBadGateway)
		}))
		defer bad.Close()
		poster := &grafanaPoster{url: bad.URL, client: bad.Client()}
		_, err := poster.fetchFrames(context.Background(), ScheduleRow{Spec: testTrainSpec()})
		if err == nil || errors.Is(err, errGrafanaUnauthorized) {
			t.Fatalf("err=%v must not read as an auth refusal", err)
		}
	})

	t.Run("query error", func(t *testing.T) {
		bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			body, _ := json.Marshal(dsReply{Results: map[string]*dsReplyResult{"A": {Status: 500, Error: "datasource exploded"}}})
			_, _ = w.Write(body)
		}))
		defer bad.Close()
		poster := &grafanaPoster{url: bad.URL, client: bad.Client()}
		_, err := poster.fetchFrames(context.Background(), ScheduleRow{Spec: testTrainSpec()})
		if err == nil || !strings.Contains(err.Error(), "datasource exploded") {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestSeriesFromFrames(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0).UTC()
	minutes := func(n int) []time.Time {
		out := make([]time.Time, n)
		for i := range out {
			out[i] = t0.Add(time.Duration(i) * time.Minute)
		}
		return out
	}
	// What every Grafana SQL datasource's reply decodes into: a *time.Time column.
	nullableMinutes := func(n int) []*time.Time {
		out := make([]*time.Time, n)
		for i := range out {
			t := t0.Add(time.Duration(i) * time.Minute)
			out[i] = &t
		}
		return out
	}
	stamp := func(t time.Time) *time.Time { return &t }
	named := data.Frames{data.NewFrame("A",
		data.NewField("Time", nil, minutes(3)),
		data.NewField("other", nil, []*float64{f64ptr(1), f64ptr(2), f64ptr(3)}),
		data.NewField("series-1", nil, []*float64{f64ptr(10), nil, f64ptr(30)}),
	)}
	// A Prometheus-shaped reply: one bare "Value" column whose identity is its
	// labels, which is the name Grafana displays and the browser stored.
	prom := data.Frames{data.NewFrame("",
		data.NewField("Time", nil, minutes(2)),
		data.NewField("Value", data.Labels{"instance": "10.0.0.1", "job": "api"}, []*float64{f64ptr(1), f64ptr(2)}),
	)}
	// Two numeric fields that share a name: Grafana disambiguates them by index.
	dupeNamed := data.Frames{data.NewFrame("A",
		data.NewField("Time", nil, minutes(2)),
		data.NewField("value", nil, []*float64{f64ptr(1), f64ptr(2)}),
		data.NewField("value", nil, []*float64{f64ptr(3), f64ptr(4)}),
	)}
	renamed := data.Frames{data.NewFrame("A",
		data.NewField("Time", nil, minutes(2)),
		data.NewField("metric", nil, []*float64{f64ptr(5), f64ptr(6)}).SetConfig(&data.FieldConfig{DisplayName: "My Series"}),
	)}
	nullableTime := data.Frames{data.NewFrame("A",
		data.NewField("time", nil, nullableMinutes(3)),
		data.NewField("value", nil, []*float64{f64ptr(1), f64ptr(2), f64ptr(3)}),
	)}
	// A NULL timestamp drops its own point and nothing else.
	nullTimestamp := data.Frames{data.NewFrame("A",
		data.NewField("time", nil, []*time.Time{stamp(t0), nil, stamp(t0.Add(2 * time.Minute))}),
		data.NewField("value", nil, []*float64{f64ptr(1), f64ptr(2), f64ptr(3)}),
	)}
	// One point over the train cap: the reply is bounded only by the decoder
	// buffer, so the refusal has to come before the slices are allocated.
	oversize := func() data.Frames {
		n := maxTrainPoints + 1
		times := make([]time.Time, n)
		values := make([]*float64, n)
		for i := range n {
			times[i] = t0.Add(time.Duration(i) * time.Minute)
			values[i] = f64ptr(float64(i))
		}
		return data.Frames{data.NewFrame("A", data.NewField("Time", nil, times), data.NewField("v", nil, values))}
	}()
	for _, tc := range []struct {
		name       string
		frames     data.Frames
		spec       retrainSpec
		wantValues []float64
		wantTimes  int
		wantErr    string
		wantIs     error
	}{
		{
			name:       "named field wins over the first numeric one",
			frames:     named,
			spec:       retrainSpec{TrainSource: TrainSource{SeriesName: "series-1"}},
			wantValues: []float64{10, 30},
			wantTimes:  2,
		},
		{
			name:       "null points are dropped",
			frames:     named,
			spec:       retrainSpec{TrainSource: TrainSource{SeriesName: "series-1"}},
			wantValues: []float64{10, 30},
			wantTimes:  2,
		},
		{
			name:       "a nullable time field is read, not dropped",
			frames:     nullableTime,
			spec:       retrainSpec{TrainSource: TrainSource{SeriesName: "value"}},
			wantValues: []float64{1, 2, 3},
			wantTimes:  3,
		},
		{
			name:       "a null timestamp drops only its own point",
			frames:     nullTimestamp,
			spec:       retrainSpec{TrainSource: TrainSource{SeriesName: "value"}},
			wantValues: []float64{1, 3},
			wantTimes:  2,
		},
		{
			name:   "a named series that is absent is refused, not substituted",
			frames: named,
			spec:   retrainSpec{TrainSource: TrainSource{SeriesName: "absent"}},
			// Fitting the first numeric field instead would publish a different series
			// under this panel's cache key with last_status ok.
			wantErr: `no series named "absent"`,
		},
		{
			name: "a display name matches when the field name does not",
			frames: data.Frames{data.NewFrame("A",
				data.NewField("Time", nil, minutes(2)),
				data.NewField("Value #A", nil, []*float64{f64ptr(7), f64ptr(8)}).SetConfig(&data.FieldConfig{DisplayNameFromDS: "series-1"}),
			)},
			spec:       retrainSpec{TrainSource: TrainSource{SeriesName: "series-1"}},
			wantValues: []float64{7, 8},
			wantTimes:  2,
		},
		{
			// What the browser stores for a labeled Prometheus series, and what the
			// old field-name-only mirror could never match.
			name:       "a label-derived display name matches",
			frames:     prom,
			spec:       retrainSpec{TrainSource: TrainSource{SeriesName: `{instance="10.0.0.1", job="api"}`}},
			wantValues: []float64{1, 2},
			wantTimes:  2,
		},
		{
			name:       "the duplicate index disambiguates same-named fields",
			frames:     dupeNamed,
			spec:       retrainSpec{TrainSource: TrainSource{SeriesName: "value 2"}},
			wantValues: []float64{3, 4},
			wantTimes:  2,
		},
		{
			name:       "a config.displayName override matches",
			frames:     renamed,
			spec:       retrainSpec{TrainSource: TrainSource{SeriesName: "My Series"}},
			wantValues: []float64{5, 6},
			wantTimes:  2,
		},
		{
			// A display name can drift between the browser and this process (another
			// datasource version, a different frame set). One candidate is not a
			// choice, so it is taken rather than blocking the row forever.
			name:       "a lone candidate stands in for an unmatched name",
			frames:     prom,
			spec:       retrainSpec{TrainSource: TrainSource{SeriesName: "labels the reply does not carry"}},
			wantValues: []float64{1, 2},
			wantTimes:  2,
		},
		{
			name:   "a frame over MAX_TRAIN_POINTS is refused",
			frames: oversize,
			spec:   retrainSpec{TrainSource: TrainSource{SeriesName: "v"}},
			wantIs: errTrainTooLong,
		},
		{
			name:       "an unnamed spec takes the first time series",
			frames:     named,
			spec:       retrainSpec{},
			wantValues: []float64{1, 2, 3},
			wantTimes:  3,
		},
		{
			name: "frame without a time field is skipped",
			frames: data.Frames{
				data.NewFrame("stats", data.NewField("value", nil, []*float64{f64ptr(9)})),
				data.NewFrame("B", data.NewField("Time", nil, minutes(2)), data.NewField("v", nil, []*float64{f64ptr(4), f64ptr(5)})),
			},
			spec:       retrainSpec{},
			wantValues: []float64{4, 5},
			wantTimes:  2,
		},
		{
			name: "no time series",
			frames: data.Frames{
				data.NewFrame("stats", data.NewField("value", nil, []*float64{f64ptr(9)})),
			},
			wantErr: "no time series",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			series, err := seriesFromFrames(tc.frames, tc.spec)
			if tc.wantIs != nil {
				if !errors.Is(err, tc.wantIs) {
					t.Fatalf("err=%v want %v", err, tc.wantIs)
				}
				return
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if series.Len() != tc.wantTimes {
				t.Fatalf("len=%d want %d", series.Len(), tc.wantTimes)
			}
			for i, want := range tc.wantValues {
				if got := series.Values()[i]; got != want {
					t.Fatalf("value[%d]=%v want %v", i, got, want)
				}
			}
		})
	}

	// A minute-of-week baseline is indexed by minute, so a gapped series would be
	// fitted into the wrong slots and must fail loudly instead.
	gapped := data.Frames{data.NewFrame("A",
		data.NewField("Time", nil, []time.Time{t0, t0.Add(time.Minute), t0.Add(5 * time.Minute)}),
		data.NewField("v", nil, []*float64{f64ptr(1), f64ptr(2), f64ptr(3)}),
	)}
	if _, err := seriesFromFrames(gapped, retrainSpec{Season: "minute-week"}); err == nil {
		t.Fatal("expected a non-1m rejection")
	}
	if _, err := seriesFromFrames(gapped, retrainSpec{Season: "hour"}); err != nil {
		t.Fatalf("hourly seasonality must accept a gapped series: %v", err)
	}
}

// TestSeriesFromFramesNullableTimeReply pins the wire case the scheduler actually
// meets: a frame decoded from a Grafana SQL datasource's /api/ds/query reply,
// whose time column is marked nullable. json.Unmarshal into dsQueryResponse is
// the same path fetchFrames takes.
func TestSeriesFromFramesNullableTimeReply(t *testing.T) {
	const reply = `{"results":{"A":{"status":200,"frames":[{"schema":{"fields":[` +
		`{"name":"time","type":"time","typeInfo":{"frame":"time.Time","nullable":true}},` +
		`{"name":"value","type":"number","typeInfo":{"frame":"float64","nullable":true}}]},` +
		`"data":{"values":[[1700000000000,1700000060000,1700000120000],[1,2,3]]}}]}}}`
	var out dsQueryResponse
	if err := json.Unmarshal([]byte(reply), &out); err != nil {
		t.Fatal(err)
	}
	frames := out.Results["A"].Frames
	if len(frames) != 1 || len(frames[0].Fields) != 2 {
		t.Fatalf("decoded %d frames", len(frames))
	}
	// The decode is the point: a reply that says "nullable" must come back as a
	// nullable time field, or this test would pass with the bug still in place.
	if got := frames[0].Fields[0].Type(); got != data.FieldTypeNullableTime {
		t.Fatalf("time field type = %v, want %v", got, data.FieldTypeNullableTime)
	}
	series, err := seriesFromFrames(frames, retrainSpec{TrainSource: TrainSource{SeriesName: "value"}})
	if err != nil {
		t.Fatal(err)
	}
	if series.Len() != 3 {
		t.Fatalf("len=%d want 3", series.Len())
	}
	for i, want := range []float64{1, 2, 3} {
		if got := series.Values()[i]; got != want {
			t.Fatalf("value[%d]=%v want %v", i, got, want)
		}
	}
}

type fakePoster struct {
	points int
	step   time.Duration
	err    error
	calls  int
	// org is what /api/org answers; 0 stands for org 1, where the scheduler tests
	// seed their rows. orgErr makes the lookup itself fail.
	org    int64
	orgErr error
}

func (p *fakePoster) orgID(context.Context) (int64, error) {
	if p.orgErr != nil {
		return 0, p.orgErr
	}
	if p.org != 0 {
		return p.org, nil
	}
	return 1, nil
}

func (p *fakePoster) fetchFrames(_ context.Context, _ ScheduleRow) (data.Frames, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	t0 := time.Unix(1_700_000_000, 0).UTC()
	times := make([]time.Time, p.points)
	values := make([]*float64, p.points)
	for i := range p.points {
		times[i] = t0.Add(time.Duration(i) * p.step)
		values[i] = f64ptr(float64(i))
	}
	return data.Frames{data.NewFrame("A",
		data.NewField("Time", nil, times),
		// The name the spec asks for: testTrainSpec trains on "series-1".
		data.NewField("series-1", nil, values),
	)}, nil
}

func TestRetrainOne(t *testing.T) {
	const key = "1111111111111111111111111111111111111111111111111111111111111111"
	ctx := context.Background()
	newRow := func() ScheduleRow {
		return ScheduleRow{
			OrgID: 7, Scope: scopePanel, Key: key,
			Cron: "*/5 * * * *", Timezone: "UTC", Spec: testTrainSpec(),
		}
	}
	newRetrainApp := func(poster framePoster, lim *workLimiter) (*App, *memoryStore, *memSchedules) {
		store, sched := newMemoryStore(), newMemSchedules()
		return &App{
			store:   store,
			sched:   sched,
			poster:  poster,
			retrain: retrainConfig{Enabled: true, Cron: "*/5 * * * *", Timezone: "UTC", Lease: time.Minute, Tick: time.Second},
			limit:   lim,
		}, store, sched
	}

	t.Run("ok writes a snapshot and the next run", func(t *testing.T) {
		app, store, sched := newRetrainApp(&fakePoster{points: 6, step: time.Minute}, newWorkLimiter(1))
		before := time.Now()
		app.retrainOne(ctx, retrainOwner(), newRow())
		finishes := sched.finished()
		if len(finishes) != 1 {
			t.Fatalf("finishes=%+v", finishes)
		}
		if finishes[0].Status != "ok" || finishes[0].Scope != scopePanel || finishes[0].Key != key {
			t.Fatalf("finish=%+v", finishes[0])
		}
		if want, _ := nextRun("*/5 * * * *", "UTC", before); finishes[0].Next.Before(want) {
			t.Fatalf("next=%s before the next */5 slot %s", finishes[0].Next, want)
		}
		if _, ok, err := store.Get(ctx, 7, key); err != nil || !ok {
			t.Fatalf("snapshot ok=%v err=%v", ok, err)
		}
	})

	t.Run("the spec's model parameters reach the fit", func(t *testing.T) {
		// A scheduled refit must reproduce the model the panel asked for: identical
		// snapshots for two different alphas means the spec's parameters were ignored
		// and the fit silently fell back to the backend defaults.
		snapshotFor := func(alpha float64) string {
			app, store, sched := newRetrainApp(&fakePoster{points: 6, step: time.Minute}, newWorkLimiter(1))
			row := newRow()
			var spec retrainSpec
			if err := json.Unmarshal(row.Spec, &spec); err != nil {
				t.Fatal(err)
			}
			spec.Model, spec.Season, spec.Alpha = "ses", "", alpha
			raw, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			row.Spec = raw
			app.retrainOne(ctx, retrainOwner(), row)
			snap, ok, err := store.Get(ctx, 7, key)
			if err != nil || !ok {
				t.Fatalf("snapshot ok=%v err=%v (finishes=%+v)", ok, err, sched.finished())
			}
			out, err := json.Marshal(snap)
			if err != nil {
				t.Fatal(err)
			}
			return string(out)
		}
		if snapshotFor(0.5) == snapshotFor(0.7) {
			t.Fatal("alpha from the stored spec did not reach the fit")
		}
	})

	t.Run("fetch failure records the error and retries after the lease", func(t *testing.T) {
		app, store, sched := newRetrainApp(&fakePoster{err: errors.New("upstream down")}, newWorkLimiter(1))
		before := time.Now()
		app.retrainOne(ctx, retrainOwner(), newRow())
		finishes := sched.finished()
		if len(finishes) != 1 {
			t.Fatalf("finishes=%+v", finishes)
		}
		if finishes[0].Status != "error: upstream down" {
			t.Fatalf("status=%q", finishes[0].Status)
		}
		if !finishes[0].Next.After(before) {
			t.Fatalf("next=%s is not in the future", finishes[0].Next)
		}
		if _, ok, _ := store.Get(ctx, 7, key); ok {
			t.Fatal("a failed retrain must not publish a snapshot")
		}
	})

	t.Run("busy slot retries on the next tick instead of the lease", func(t *testing.T) {
		lim := newWorkLimiter(1)
		release, err := lim.try(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		app, _, sched := newRetrainApp(&fakePoster{points: 6, step: time.Minute}, lim)
		before := time.Now()
		app.retrainOne(ctx, retrainOwner(), newRow())
		finishes := sched.finished()
		if len(finishes) != 1 || !strings.Contains(finishes[0].Status, errBusy.Error()) {
			t.Fatalf("finishes=%+v", finishes)
		}
		if finishes[0].Next.After(before.Add(time.Minute)) {
			t.Fatalf("next=%s waited out the lease", finishes[0].Next)
		}
	})

	t.Run("unparsable spec fails without retraining", func(t *testing.T) {
		app, _, sched := newRetrainApp(&fakePoster{points: 6, step: time.Minute}, newWorkLimiter(1))
		row := newRow()
		row.Spec = json.RawMessage(`{"model":"baseline"}`)
		app.retrainOne(ctx, retrainOwner(), row)
		finishes := sched.finished()
		if len(finishes) != 1 || !strings.HasPrefix(finishes[0].Status, "error:") {
			t.Fatalf("finishes=%+v", finishes)
		}
	})

	t.Run("baseline rows are never retrained by the plugin", func(t *testing.T) {
		app, _, sched := newRetrainApp(&fakePoster{points: 6, step: time.Minute}, newWorkLimiter(1))
		row := newRow()
		row.Scope = scopeBaseline
		app.retrainOne(ctx, retrainOwner(), row)
		finishes := sched.finished()
		if len(finishes) != 1 || !strings.HasPrefix(finishes[0].Status, "error:") {
			t.Fatalf("finishes=%+v", finishes)
		}
	})
}

// TestRetrainDueClaimsOnlyItsOwnOrg pins the org boundary of a scheduled retrain.
// The scheduler holds one Grafana credential and Grafana resolves a stored query's
// datasourceUid inside that credential's org, so claiming another org's row would
// fetch its frames as the wrong org's series — or fail forever — and store the
// result under that org's cache key. The row it does not claim stays due, which is
// what keeps it refreshed by its own org's overlay load.
func TestRetrainDueClaimsOnlyItsOwnOrg(t *testing.T) {
	const (
		ownKey   = "6666666666666666666666666666666666666666666666666666666666666666"
		otherKey = "7777777777777777777777777777777777777777777777777777777777777777"
	)
	ctx := context.Background()
	past := time.Now().Add(-time.Minute)
	row := func(org int64, key string) ScheduleRow {
		return ScheduleRow{OrgID: org, Scope: scopePanel, Key: key, Cron: "*/5 * * * *", Timezone: "UTC", Enabled: true, Spec: testTrainSpec(), NextRunAt: past}
	}
	poster := &fakePoster{points: 6, step: time.Minute, org: 2}
	store, sched := newMemoryStore(), newMemSchedules()
	app := &App{
		store: store, sched: sched, poster: poster,
		retrain: retrainConfig{Enabled: true, Cron: "*/5 * * * *", Timezone: "UTC", Lease: time.Minute, Tick: time.Second},
		limit:   newWorkLimiter(1),
	}
	sched.seed(row(1, otherKey))
	sched.seed(row(2, ownKey))

	app.retrainDue(ctx)

	if poster.calls != 1 {
		t.Fatalf("fetched %d rows, want only this org's", poster.calls)
	}
	finishes := sched.finished()
	if len(finishes) != 1 || finishes[0].Key != ownKey {
		t.Fatalf("finishes=%+v, want only %s", finishes, ownKey)
	}
	if _, ok, err := store.Get(ctx, 2, ownKey); err != nil || !ok {
		t.Fatalf("this org's snapshot ok=%v err=%v", ok, err)
	}
	if _, ok, _ := store.Get(ctx, 1, otherKey); ok {
		t.Fatal("another org's key was given a snapshot built from this org's credential")
	}
	if due, err := sched.Due(ctx, 1, otherKey, time.Now()); err != nil || !due {
		t.Fatalf("the other org's row is no longer due (due=%v err=%v): its overlay would never refresh it", due, err)
	}
}

// TestRetrainDueWithoutAnOrgClaimsNothing pins what happens when Grafana will not
// say which org the credential belongs to: the scheduler must claim nothing rather
// than guess an org, and a refusal must count toward the auth guard so a bad token
// still disables it instead of retrying forever.
func TestRetrainDueWithoutAnOrgClaimsNothing(t *testing.T) {
	const key = "8888888888888888888888888888888888888888888888888888888888888888"
	ctx := context.Background()
	poster := &fakePoster{points: 6, step: time.Minute, orgErr: fmt.Errorf("%w: /api/org status 401", errGrafanaUnauthorized)}
	store, sched := newMemoryStore(), newMemSchedules()
	app := &App{
		store: store, sched: sched, poster: poster,
		retrain: retrainConfig{Enabled: true, Cron: "*/5 * * * *", Timezone: "UTC", Lease: time.Minute, Tick: time.Second},
		limit:   newWorkLimiter(1),
	}
	sched.seed(ScheduleRow{OrgID: 1, Scope: scopePanel, Key: key, Cron: "*/5 * * * *", Timezone: "UTC", Enabled: true, Spec: testTrainSpec(), NextRunAt: time.Now().Add(-time.Minute)})

	out := app.retrainDue(ctx)

	if poster.calls != 0 || len(sched.finished()) != 0 {
		t.Fatalf("claimed work without knowing the org: calls=%d finishes=%+v", poster.calls, sched.finished())
	}
	if !out.denied {
		t.Fatal("a refused /api/org must count as an auth refusal, or the ticker retries a bad token forever")
	}
}

func TestRetrainDueClaimsAndSkipsLeasedRows(t *testing.T) {
	const key = "2222222222222222222222222222222222222222222222222222222222222222"
	poster := &fakePoster{points: 6, step: time.Minute}
	app, _, sched := func() (*App, *memoryStore, *memSchedules) {
		store, sched := newMemoryStore(), newMemSchedules()
		return &App{
			store: store, sched: sched, poster: poster,
			retrain: retrainConfig{Enabled: true, Cron: "*/5 * * * *", Timezone: "UTC", Lease: time.Minute, Tick: time.Second},
			limit:   newWorkLimiter(1),
		}, store, sched
	}()
	past := time.Now().Add(-time.Minute)
	sched.seed(ScheduleRow{OrgID: 1, Scope: scopePanel, Key: key, Cron: "*/5 * * * *", Timezone: "UTC", Enabled: true, Spec: testTrainSpec(), NextRunAt: past})
	// A due row the scheduler cannot fetch (no spec) stays due for the overlay.
	sched.seed(ScheduleRow{OrgID: 1, Scope: scopePanel, Key: "specless", Cron: "*/5 * * * *", Timezone: "UTC", Enabled: true, NextRunAt: past})

	app.retrainDue(context.Background())
	if poster.calls != 1 {
		t.Fatalf("retrained %d rows, want the one with a spec", poster.calls)
	}
	finishes := sched.finished()
	if len(finishes) != 1 || finishes[0].Key != key {
		t.Fatalf("finishes=%+v", finishes)
	}

	// The finished row is no longer due, so a second tick retrains nothing.
	app.retrainDue(context.Background())
	if poster.calls != 1 {
		t.Fatalf("second tick retrained another row: %d", poster.calls)
	}
}

// TestSchedulerOutlivesItsCreatingRequest pins the scheduler's context lifetime:
// instancemgmt builds the App from the first RPC's context, which is cancelled as
// soon as that request ends, so a scheduler derived from it would never tick.
func TestSchedulerOutlivesItsCreatingRequest(t *testing.T) {
	clearRetrainEnv(t)
	clearStoreEnv(t)
	t.Setenv("FORECAST_RETRAIN_TICK", "5ms")
	ctx, cancel := context.WithCancel(context.Background())
	sched := newMemSchedules()
	app, err := newApp(ctx, backend.AppInstanceSettings{}, newMemoryStore(), sched, &fakePoster{points: 6, step: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Dispose)
	// The creating request finishes here, and the ticker must survive it.
	cancel()
	sched.seed(ScheduleRow{
		OrgID: 1, Scope: scopePanel, Key: "3333333333333333333333333333333333333333333333333333333333333333",
		Cron: "*/5 * * * *", Timezone: "UTC", Enabled: true, Spec: testTrainSpec(),
		NextRunAt: time.Now().Add(-time.Minute),
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(sched.finished()) > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the scheduler never ran after its creating context was cancelled")
}

// TestAuthGuardStopsOnlyOnConsecutiveRefusals pins the counting rule behind the
// auto-disable: only Grafana's own 401/403 refusals accumulate, and any tick
// that got past the fetch starts the count over.
func TestAuthGuardStopsOnlyOnConsecutiveRefusals(t *testing.T) {
	denied := tickResult{denied: true}
	fetched := tickResult{fetched: true}
	idle := tickResult{}
	for _, tc := range []struct {
		name  string
		ticks []tickResult
		want  bool
	}{
		{
			name:  "three refusals in a row stop it",
			ticks: []tickResult{denied, denied, denied},
			want:  true,
		},
		{
			name:  "a fetch that got past the wall resets the count",
			ticks: []tickResult{denied, denied, fetched, denied, denied},
			want:  false,
		},
		{
			name:  "a tick with nothing due neither counts nor resets",
			ticks: []tickResult{denied, idle, denied, idle, denied},
			want:  true,
		},
		{
			name:  "a datasource error is not a refusal",
			ticks: []tickResult{denied, fetched, denied, fetched, denied, fetched},
			want:  false,
		},
		{
			name:  "fewer than the threshold keeps it alive",
			ticks: []tickResult{denied, denied},
			want:  false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var g authGuard
			stopped := false
			for _, tick := range tc.ticks {
				if g.watch(tick) {
					stopped = true
					break
				}
			}
			if stopped != tc.want {
				t.Fatalf("stopped=%v want %v after %d ticks", stopped, tc.want, len(tc.ticks))
			}
		})
	}
}

// TestRunSchedulerStops pins the Dispose contract: canceling the app must end
// the ticker goroutine, or every instance reload leaks a ticker that keeps
// claiming rows for the life of the Grafana process.
func TestRunSchedulerStops(t *testing.T) {
	app, _, _ := func() (*App, *memoryStore, *memSchedules) {
		store, sched := newMemoryStore(), newMemSchedules()
		return &App{
			store: store, sched: sched, poster: &fakePoster{points: 6, step: time.Minute},
			retrain: retrainConfig{Enabled: true, Cron: "*/5 * * * *", Timezone: "UTC", Lease: time.Minute, Tick: time.Millisecond},
			limit:   newWorkLimiter(1),
		}, store, sched
	}()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		app.runScheduler(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("scheduler did not stop after cancel")
	}
}

// TestRunSchedulerStopsOnAuthRefusal pins the documented invariant that a
// scheduler Grafana will not authenticate (401/403) ends itself instead of
// retrying every tick for the life of the process, while a datasource that is
// merely broken keeps it running.
func TestRunSchedulerStopsOnAuthRefusal(t *testing.T) {
	const key = "4444444444444444444444444444444444444444444444444444444444444444"
	newApp := func(poster framePoster) (*App, *memSchedules) {
		store, sched := newMemoryStore(), newMemSchedules()
		sched.seed(ScheduleRow{
			OrgID: 1, Scope: scopePanel, Key: key, Cron: "@every 1s", Timezone: "UTC",
			Enabled: true, Spec: testTrainSpec(), NextRunAt: time.Now().Add(-time.Minute),
		})
		return &App{
			store: store, sched: sched, poster: poster,
			// The short lease makes the failed row due again a few ticks later, so
			// three consecutive refusals happen in well under a second.
			retrain: retrainConfig{Enabled: true, Cron: "@every 1s", Timezone: "UTC", Lease: 20 * time.Millisecond, Tick: 2 * time.Millisecond},
			limit:   newWorkLimiter(1),
		}, sched
	}
	for _, tc := range []struct {
		name      string
		poster    *fakePoster
		wantStop  bool
		wantCalls int
	}{
		{
			name:      "three auth refusals end the ticker",
			poster:    &fakePoster{err: fmt.Errorf("%w: status 401", errGrafanaUnauthorized)},
			wantStop:  true,
			wantCalls: retrainAuthFailures,
		},
		{
			name:     "a broken datasource keeps it ticking",
			poster:   &fakePoster{err: errors.New("datasource exploded")},
			wantStop: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, sched := newApp(tc.poster)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() {
				app.runScheduler(ctx)
				close(done)
			}()

			if !tc.wantStop {
				// Attempts are observed through the store (guarded), because the
				// poster is only ever read once its goroutine has stopped.
				deadline := time.Now().Add(5 * time.Second)
				for len(sched.finished()) < 2 && time.Now().Before(deadline) {
					time.Sleep(5 * time.Millisecond)
				}
				if len(sched.finished()) < 2 {
					t.Fatalf("only %d attempts: the ticker is not running", len(sched.finished()))
				}
				select {
				case <-done:
					t.Fatalf("a datasource error disabled the scheduler after %d attempts", len(sched.finished()))
				default:
				}
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("scheduler did not stop after cancel")
				}
				return
			}

			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatalf("the scheduler kept ticking against an auth refusal (%d attempts)", len(sched.finished()))
			}
			if tc.poster.calls != tc.wantCalls {
				t.Fatalf("fetch attempts=%d want %d", tc.poster.calls, tc.wantCalls)
			}
		})
	}
}

// TestRetrainReplyBounds pins what one /api/ds/query reply may cost the
// scheduler: a body over maxDSQueryReplyBytes is refused outright, and a frame
// over MAX_TRAIN_POINTS is refused before it is fitted. Neither may publish a
// snapshot.
func TestRetrainReplyBounds(t *testing.T) {
	const key = "5555555555555555555555555555555555555555555555555555555555555555"
	ctx := context.Background()

	oversizeBody := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		chunk := bytes.Repeat([]byte("x"), 1<<16)
		for written := int64(0); written <= maxDSQueryReplyBytes; written += int64(len(chunk)) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer oversizeBody.Close()

	// The reply that violates the train cap has to arrive over the wire: it is the
	// decoder that bounds what the datasource can cost.
	bigPoints := maxTrainPoints + 1
	bigTimes := make([]time.Time, bigPoints)
	bigValues := make([]float64, bigPoints)
	t0 := time.Unix(1_700_000_000, 0).UTC()
	for i := range bigPoints {
		bigTimes[i] = t0.Add(time.Duration(i) * time.Minute)
		bigValues[i] = float64(i)
	}
	bigReply, err := json.Marshal(dsReply{Results: map[string]*dsReplyResult{
		"A": {Status: 200, Frames: data.Frames{data.NewFrame("A",
			data.NewField("Time", nil, bigTimes),
			data.NewField("series-1", nil, bigValues),
		)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	bigFrame := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bigReply)
	}))
	defer bigFrame.Close()

	for _, tc := range []struct {
		name       string
		poster     framePoster
		wantIs     error
		wantPhrase string
	}{
		{
			name:       "a reply over maxDSQueryReplyBytes is refused",
			poster:     &grafanaPoster{url: oversizeBody.URL, client: oversizeBody.Client()},
			wantPhrase: "reply exceeds",
		},
		{
			name:       "a frame over MAX_TRAIN_POINTS is refused",
			poster:     &grafanaPoster{url: bigFrame.URL, client: bigFrame.Client()},
			wantIs:     errTrainTooLong,
			wantPhrase: errTrainTooLong.Error(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, sched := newMemoryStore(), newMemSchedules()
			app := &App{
				store: store, sched: sched, poster: tc.poster,
				retrain: retrainConfig{Enabled: true, Cron: "*/5 * * * *", Timezone: "UTC", Lease: time.Minute, Tick: time.Second},
				limit:   newWorkLimiter(1),
			}
			row := ScheduleRow{OrgID: 7, Scope: scopePanel, Key: key, Cron: "*/5 * * * *", Timezone: "UTC", Spec: testTrainSpec()}
			err := app.trainFromSpec(ctx, row)
			if err == nil || !strings.Contains(err.Error(), tc.wantPhrase) {
				t.Fatalf("err=%v want %q", err, tc.wantPhrase)
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Fatalf("err=%v is not %v", err, tc.wantIs)
			}
			// The scheduler path must reach the same verdict and publish nothing.
			app.retrainOne(ctx, retrainOwner(), row)
			finishes := sched.finished()
			if len(finishes) != 1 || !strings.Contains(finishes[0].Status, tc.wantPhrase) {
				t.Fatalf("finishes=%+v want %q", finishes, tc.wantPhrase)
			}
			if _, ok, err := store.Get(ctx, 7, key); err != nil || ok {
				t.Fatalf("snapshot published: ok=%v err=%v", ok, err)
			}
		})
	}
}
