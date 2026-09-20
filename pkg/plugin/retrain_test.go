package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
		Season:   "minute-week",
		Calendar: "",
		Lookback: "2h",
	})
	if err != nil {
		panic(err)
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

	for _, tc := range []struct {
		name     string
		token    string
		wantAuth string
	}{
		{name: "no token sends no header"},
		{name: "token sends a bearer header", token: "sa-token", wantAuth: "Bearer sa-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got = fetchRequest{}
			poster := &grafanaPoster{url: srv.URL, token: tc.token, client: srv.Client()}
			row := ScheduleRow{Scope: scopePanel, Key: "k", Spec: testTrainSpec()}
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
			if got.Body.From != "1000" || got.Body.To != "2000" {
				t.Fatalf("range=%s..%s", got.Body.From, got.Body.To)
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

	t.Run("status error", func(t *testing.T) {
		bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "nope", http.StatusUnauthorized)
		}))
		defer bad.Close()
		poster := &grafanaPoster{url: bad.URL, client: bad.Client()}
		if _, err := poster.fetchFrames(context.Background(), ScheduleRow{Spec: testTrainSpec()}); err == nil {
			t.Fatal("expected an error")
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
	named := data.Frames{data.NewFrame("A",
		data.NewField("Time", nil, minutes(3)),
		data.NewField("other", nil, []*float64{f64ptr(1), f64ptr(2), f64ptr(3)}),
		data.NewField("series-1", nil, []*float64{f64ptr(10), nil, f64ptr(30)}),
	)}
	for _, tc := range []struct {
		name       string
		frames     data.Frames
		spec       retrainSpec
		wantValues []float64
		wantTimes  int
		wantErr    string
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
			name:       "first numeric field when nothing matches",
			frames:     named,
			spec:       retrainSpec{TrainSource: TrainSource{SeriesName: "absent"}},
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

type fakePoster struct {
	points int
	step   time.Duration
	err    error
	calls  int
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
		data.NewField("Value", nil, values),
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
		app.retrainOne(ctx, newRow())
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

	t.Run("fetch failure records the error and retries after the lease", func(t *testing.T) {
		app, store, sched := newRetrainApp(&fakePoster{err: errors.New("upstream down")}, newWorkLimiter(1))
		before := time.Now()
		app.retrainOne(ctx, newRow())
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
		app.retrainOne(ctx, newRow())
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
		app.retrainOne(ctx, row)
		finishes := sched.finished()
		if len(finishes) != 1 || !strings.HasPrefix(finishes[0].Status, "error:") {
			t.Fatalf("finishes=%+v", finishes)
		}
	})

	t.Run("baseline rows are never retrained by the plugin", func(t *testing.T) {
		app, _, sched := newRetrainApp(&fakePoster{points: 6, step: time.Minute}, newWorkLimiter(1))
		row := newRow()
		row.Scope = scopeBaseline
		app.retrainOne(ctx, row)
		finishes := sched.finished()
		if len(finishes) != 1 || !strings.HasPrefix(finishes[0].Status, "error:") {
			t.Fatalf("finishes=%+v", finishes)
		}
	})
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
