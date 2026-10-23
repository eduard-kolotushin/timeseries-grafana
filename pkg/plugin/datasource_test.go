package plugin

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	forecast "github.com/eduard-kolotushin/timeseries-forecast"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

func seedSnapshot(t *testing.T, store SnapshotStore, orgID int64, key string, in ForecastRequest) forecast.Fitted {
	t.Helper()
	fitted, err := fitRequest(in)
	if err != nil {
		t.Fatalf("fit: %v", err)
	}
	snap, err := forecast.SnapshotOf(fitted)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if err := store.Put(context.Background(), orgID, key, snap); err != nil {
		t.Fatalf("put: %v", err)
	}
	return fitted
}

func queryJSON(t *testing.T, kind, key string, level float64) []byte {
	t.Helper()
	b, err := json.Marshal(forecastQueryJSON{Kind: kind, CacheKey: key, Level: level})
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	return b
}

func queryJSONLabels(t *testing.T, kind, key string, labels map[string]string) []byte {
	t.Helper()
	b, err := json.Marshal(forecastQueryJSON{Kind: kind, CacheKey: key, SeriesLabels: labels})
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	return b
}

func TestQueryData(t *testing.T) {
	ctx := context.Background()
	const orgID int64 = 1
	key := strings.Repeat("ab", 32)
	fit := ForecastRequest{
		Times:  []int64{0, 1000, 2000, 3000},
		Values: []nullableFloat{1, 2, 3, 4},
		Model:  "naive",
	}
	from := time.UnixMilli(4000).UTC()
	to := time.UnixMilli(5000).UTC()

	t.Run("miss is needTrain without fitting", func(t *testing.T) {
		store := newMemoryStore()
		ds, err := newDatasource(ctx, backend.DataSourceInstanceSettings{}, store)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := ds.QueryData(ctx, &backend.QueryDataRequest{
			PluginContext: backend.PluginContext{OrgID: orgID},
			Queries: []backend.DataQuery{{
				RefID:     "B",
				JSON:      queryJSON(t, "forecast", key, 0),
				TimeRange: backend.TimeRange{From: from, To: to},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		got := resp.Responses["B"]
		if got.Error == nil || !strings.Contains(got.Error.Error(), "needTrain") {
			t.Fatalf("error=%v", got.Error)
		}
		if len(got.Frames) != 0 {
			t.Fatalf("frames=%d", len(got.Frames))
		}
	})

	t.Run("nil store is needTrain", func(t *testing.T) {
		ds := &Datasource{}
		resp, err := ds.QueryData(ctx, &backend.QueryDataRequest{
			PluginContext: backend.PluginContext{OrgID: orgID},
			Queries: []backend.DataQuery{{
				RefID:     "B",
				JSON:      queryJSON(t, "forecast", key, 0),
				TimeRange: backend.TimeRange{From: from, To: to},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		got := resp.Responses["B"]
		if got.Error == nil || !strings.Contains(got.Error.Error(), "needTrain") {
			t.Fatalf("error=%v", got.Error)
		}
	})

	t.Run("nil store uses datasource jsonData DSN", func(t *testing.T) {
		mem := newMemoryStore()
		seedSnapshot(t, mem, orgID, key, fit)
		dsHost, err := json.Marshal(map[string]any{"storeHost": "from-ds"})
		if err != nil {
			t.Fatal(err)
		}
		ds := &Datasource{
			jsonData: dsHost,
			connect: func(_ context.Context, dsn string, _ storeTLSConfig) (SnapshotStore, func()) {
				if !strings.Contains(dsn, "@from-ds:5432/") {
					t.Fatalf("dsn=%s", dsn)
				}
				return mem, nil
			},
		}
		resp, err := ds.QueryData(ctx, &backend.QueryDataRequest{
			PluginContext: backend.PluginContext{OrgID: orgID},
			Queries: []backend.DataQuery{{
				RefID:     "B",
				JSON:      queryJSON(t, "forecast", key, 0),
				TimeRange: backend.TimeRange{From: from, To: to},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Responses["B"].Error != nil {
			t.Fatalf("error=%v", resp.Responses["B"].Error)
		}
	})

	t.Run("nil store uses AppInstanceSettings DSN", func(t *testing.T) {
		mem := newMemoryStore()
		seedSnapshot(t, mem, orgID, key, fit)
		appHost, err := json.Marshal(map[string]any{"storeHost": "from-app"})
		if err != nil {
			t.Fatal(err)
		}
		ds := &Datasource{
			connect: func(_ context.Context, dsn string, _ storeTLSConfig) (SnapshotStore, func()) {
				if !strings.Contains(dsn, "@from-app:5432/") {
					t.Fatalf("dsn=%s", dsn)
				}
				return mem, nil
			},
		}
		resp, err := ds.QueryData(ctx, &backend.QueryDataRequest{
			PluginContext: backend.PluginContext{
				OrgID: orgID,
				AppInstanceSettings: &backend.AppInstanceSettings{
					JSONData: appHost,
				},
			},
			Queries: []backend.DataQuery{{
				RefID:     "B",
				JSON:      queryJSON(t, "forecast", key, 0),
				TimeRange: backend.TimeRange{From: from, To: to},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Responses["B"].Error != nil {
			t.Fatalf("error=%v", resp.Responses["B"].Error)
		}
	})

	t.Run("invalid cacheKey", func(t *testing.T) {
		ds, err := newDatasource(ctx, backend.DataSourceInstanceSettings{}, newMemoryStore())
		if err != nil {
			t.Fatal(err)
		}
		resp, err := ds.QueryData(ctx, &backend.QueryDataRequest{
			PluginContext: backend.PluginContext{OrgID: orgID},
			Queries: []backend.DataQuery{{
				RefID:     "B",
				JSON:      queryJSON(t, "forecast", "not-a-key", 0),
				TimeRange: backend.TimeRange{From: from, To: to},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		got := resp.Responses["B"]
		if got.Error == nil || !strings.Contains(got.Error.Error(), "cacheKey") {
			t.Fatalf("error=%v", got.Error)
		}
	})

	t.Run("unknown kind", func(t *testing.T) {
		ds, err := newDatasource(ctx, backend.DataSourceInstanceSettings{}, newMemoryStore())
		if err != nil {
			t.Fatal(err)
		}
		resp, err := ds.QueryData(ctx, &backend.QueryDataRequest{
			PluginContext: backend.PluginContext{OrgID: orgID},
			Queries: []backend.DataQuery{{
				RefID:     "B",
				JSON:      queryJSON(t, "residual", key, 0),
				TimeRange: backend.TimeRange{From: from, To: to},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		got := resp.Responses["B"]
		if got.Error == nil || !strings.Contains(got.Error.Error(), "kind") {
			t.Fatalf("error=%v", got.Error)
		}
	})

	t.Run("hit forecast lower upper", func(t *testing.T) {
		store := newMemoryStore()
		fitted := seedSnapshot(t, store, orgID, key, fit)
		want, err := emitForecast(fitted, 4000, 5000, 0.95)
		if err != nil {
			t.Fatal(err)
		}
		ds, err := newDatasource(ctx, backend.DataSourceInstanceSettings{}, store)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := ds.QueryData(ctx, &backend.QueryDataRequest{
			PluginContext: backend.PluginContext{OrgID: orgID},
			Queries: []backend.DataQuery{
				{RefID: "B", JSON: queryJSON(t, "forecast", key, 0), TimeRange: backend.TimeRange{From: from, To: to}},
				{RefID: "C", JSON: queryJSON(t, "lower", key, 0.95), TimeRange: backend.TimeRange{From: from, To: to}},
				{RefID: "D", JSON: queryJSON(t, "upper", key, 0.95), TimeRange: backend.TimeRange{From: from, To: to}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertFrameValues(t, resp.Responses["B"], want.Values)
		assertFrameValues(t, resp.Responses["C"], want.Lower)
		assertFrameValues(t, resp.Responses["D"], want.Upper)
	})

	// A rule that queries the metric and this baseline together pairs them by
	// label, so a query that names its series must label the answer — and a query
	// that does not must stay exactly the unlabelled frame it always was.
	t.Run("seriesLabels label the answer", func(t *testing.T) {
		store := newMemoryStore()
		seedSnapshot(t, store, orgID, key, fit)
		ds, err := newDatasource(ctx, backend.DataSourceInstanceSettings{}, store)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name   string
			json   []byte
			labels map[string]string
		}{
			{
				name:   "named series",
				json:   queryJSONLabels(t, "forecast", key, map[string]string{"host": "a"}),
				labels: map[string]string{"host": "a"},
			},
			{name: "unnamed series", json: queryJSON(t, "forecast", key, 0)},
		} {
			resp, err := ds.QueryData(ctx, &backend.QueryDataRequest{
				PluginContext: backend.PluginContext{OrgID: orgID},
				Queries: []backend.DataQuery{{
					RefID:     "B",
					JSON:      tc.json,
					TimeRange: backend.TimeRange{From: from, To: to},
				}},
			})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			got := resp.Responses["B"]
			if got.Error != nil {
				t.Fatalf("%s: error=%v", tc.name, got.Error)
			}
			if len(got.Frames) != 1 {
				t.Fatalf("%s: frames=%d", tc.name, len(got.Frames))
			}
			labels := got.Frames[0].Fields[1].Labels
			if len(labels) != len(tc.labels) {
				t.Fatalf("%s: labels=%v want=%v", tc.name, labels, tc.labels)
			}
			for k, v := range tc.labels {
				if labels[k] != v {
					t.Fatalf("%s: label %q=%q want %q", tc.name, k, labels[k], v)
				}
			}
			if got.Frames[0].Fields[0].Labels != nil {
				t.Fatalf("%s: time field labels=%v", tc.name, got.Frames[0].Fields[0].Labels)
			}
		}
	})

	t.Run("busy is 429", func(t *testing.T) {
		store := newMemoryStore()
		seedSnapshot(t, store, orgID, key, fit)
		ds, err := newDatasource(ctx, backend.DataSourceInstanceSettings{}, store)
		if err != nil {
			t.Fatal(err)
		}
		ds.limit = newWorkLimiter(1)
		release, err := ds.limit.try(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		resp, err := ds.QueryData(ctx, &backend.QueryDataRequest{
			PluginContext: backend.PluginContext{OrgID: orgID},
			Queries: []backend.DataQuery{{
				RefID:     "B",
				JSON:      queryJSON(t, "forecast", key, 0),
				TimeRange: backend.TimeRange{From: from, To: to},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		got := resp.Responses["B"]
		if got.Error == nil || !strings.Contains(got.Error.Error(), "busy") {
			t.Fatalf("error=%v", got.Error)
		}
		if got.Status != backend.StatusTooManyRequests {
			t.Fatalf("status=%v", got.Status)
		}
	})

	t.Run("oversize query JSON", func(t *testing.T) {
		prev := maxQueryJSONBytes
		maxQueryJSONBytes = 8
		t.Cleanup(func() { maxQueryJSONBytes = prev })
		ds, err := newDatasource(ctx, backend.DataSourceInstanceSettings{}, newMemoryStore())
		if err != nil {
			t.Fatal(err)
		}
		resp, err := ds.QueryData(ctx, &backend.QueryDataRequest{
			PluginContext: backend.PluginContext{OrgID: orgID},
			Queries: []backend.DataQuery{{
				RefID:     "B",
				JSON:      queryJSON(t, "forecast", key, 0),
				TimeRange: backend.TimeRange{From: from, To: to},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		got := resp.Responses["B"]
		if got.Error == nil || !strings.Contains(got.Error.Error(), "too large") {
			t.Fatalf("error=%v", got.Error)
		}
	})
}

// TestQueryDataBackcast pins the datasource half of the signed grid: a window
// that ends at or before the snapshot's last timestamp restores the model and
// serves in-sample points, with the baseline's per-key bands and null bounds for
// every other model.
func TestQueryDataBackcast(t *testing.T) {
	ctx := context.Background()
	const orgID int64 = 1
	key := strings.Repeat("cd", 32)
	start := time.Date(2026, 1, 12, 10, 0, 0, 0, time.UTC) // Monday 10:00
	times := []int64{
		start.UnixMilli(),
		start.Add(time.Hour).UnixMilli(),
		start.Add(24 * time.Hour).UnixMilli(),
		start.Add(25 * time.Hour).UnixMilli(),
	}
	values := []nullableFloat{8, 1, 12, 1}
	priorMonday := start.AddDate(0, 0, -7)
	last := start.Add(25 * time.Hour)

	store := newMemoryStore()
	seedSnapshot(t, store, orgID, key, ForecastRequest{
		Times: times, Values: values, Model: "baseline", Season: "hour",
	})
	ds, err := newDatasource(ctx, backend.DataSourceInstanceSettings{}, store)
	if err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, cacheKey, kind string, level float64, from, to time.Time) backend.DataResponse {
		t.Helper()
		resp, err := ds.QueryData(ctx, &backend.QueryDataRequest{
			PluginContext: backend.PluginContext{OrgID: orgID},
			Queries: []backend.DataQuery{{
				RefID:     "B",
				JSON:      queryJSON(t, kind, cacheKey, level),
				TimeRange: backend.TimeRange{From: from, To: to},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return resp.Responses["B"]
	}

	t.Run("baseline serves in-sample values and bands", func(t *testing.T) {
		got := run(t, key, "forecast", 0, priorMonday, last)
		if got.Error != nil {
			t.Fatalf("error=%v", got.Error)
		}
		if len(got.Frames) != 1 {
			t.Fatalf("frames=%d", len(got.Frames))
		}
		field := got.Frames[0].Fields[1]
		if field.Len() != 194 {
			t.Fatalf("len=%d want 194 hourly points", field.Len())
		}
		if ts, ok := got.Frames[0].Fields[0].At(0).(time.Time); !ok || !ts.Equal(priorMonday) {
			t.Fatalf("first time = %v, want %v", got.Frames[0].Fields[0].At(0), priorMonday)
		}
		if v, ok := field.At(0).(*float64); !ok || *v != 10 {
			t.Fatalf("prior Monday 10:00 value = %v, want the bucket mean 10", field.At(0))
		}

		lower := run(t, key, "lower", 0.95, priorMonday, last)
		if lower.Error != nil {
			t.Fatalf("error=%v", lower.Error)
		}
		wantLo := 10 - math.Sqrt2*math.Erfinv(0.95)*2*math.Sqrt(1.5)
		if v, ok := lower.Frames[0].Fields[1].At(0).(*float64); !ok || math.Abs(*v-wantLo) > 1e-9 {
			t.Fatalf("lower = %v, want %v", lower.Frames[0].Fields[1].At(0), wantLo)
		}
		upper := run(t, key, "upper", 0.95, priorMonday, last)
		if upper.Error != nil {
			t.Fatalf("error=%v", upper.Error)
		}
		wantHi := 10 + math.Sqrt2*math.Erfinv(0.95)*2*math.Sqrt(1.5)
		if v, ok := upper.Frames[0].Fields[1].At(0).(*float64); !ok || math.Abs(*v-wantHi) > 1e-9 {
			t.Fatalf("upper = %v, want %v", upper.Frames[0].Fields[1].At(0), wantHi)
		}
	})

	// The alerting/backtest use case: a window wholly before the snapshot's last
	// timestamp restores the same per-bucket state, not a needTrain or an error.
	t.Run("a fully past window serves the buckets", func(t *testing.T) {
		got := run(t, key, "forecast", 0, priorMonday, priorMonday.Add(time.Hour))
		if got.Error != nil {
			t.Fatalf("error=%v", got.Error)
		}
		field := got.Frames[0].Fields[1]
		if field.Len() != 2 {
			t.Fatalf("len=%d, want 2", field.Len())
		}
		for i, want := range []float64{10, 1} {
			if v, ok := field.At(i).(*float64); !ok || *v != want {
				t.Fatalf("point %d = %v, want %v", i, field.At(i), want)
			}
		}
	})

	holtKey := strings.Repeat("ef", 32)
	seedSnapshot(t, store, orgID, holtKey, ForecastRequest{
		Times: times, Values: values, Model: "holt", Alpha: 0.5, Beta: 0.5,
	})
	t.Run("non-baseline in-sample bounds are null", func(t *testing.T) {
		got := run(t, holtKey, "lower", 0.95, priorMonday, last)
		if got.Error != nil {
			t.Fatalf("error=%v", got.Error)
		}
		field := got.Frames[0].Fields[1]
		if field.Len() != 194 {
			t.Fatalf("len=%d want 194 hourly points", field.Len())
		}
		for i := range field.Len() {
			if v, ok := field.At(i).(*float64); !ok || v != nil {
				t.Fatalf("in-sample bound %d = %v, want null", i, field.At(i))
			}
		}
	})

	t.Run("off-grid past window answers a reason", func(t *testing.T) {
		at := last.Add(30 * time.Minute)
		got := run(t, key, "forecast", 0, at, at)
		if got.Error == nil || !strings.Contains(got.Error.Error(), forecast.ErrEmptyRange.Error()) {
			t.Fatalf("error=%v", got.Error)
		}
		if got.Status != backend.StatusBadRequest {
			t.Fatalf("status=%v", got.Status)
		}
	})

	// The cap is symmetric on the restore path too: the snapshot's own grid
	// bounds a backcast before Restore or any forecast allocation.
	capKey := strings.Repeat("12", 32)
	seedSnapshot(t, store, orgID, capKey, ForecastRequest{
		Times: []int64{0, 1}, Values: []nullableFloat{1, 2}, Model: "naive",
	})
	t.Run("an over-wide past window is 413", func(t *testing.T) {
		got := run(t, capKey, "forecast", 0, time.UnixMilli(1-maxForecastPoints), time.UnixMilli(1))
		if got.Error == nil || !strings.Contains(got.Error.Error(), errWindowTooManyPoints.Error()) {
			t.Fatalf("error=%v", got.Error)
		}
		if got.Status != backend.Status(413) {
			t.Fatalf("status=%v", got.Status)
		}
	})
}

// TestQueryDataCaps pins the two answers the alerting datasource owes a caller
// whose query cannot be served: an absent cacheKey is the miss the overlay also
// gets (the query editor writes the key asynchronously, so a just-added row arrives
// without one), and a window past the point cap is a 413-class reason rather than a
// 500 or a multi-gigabyte allocation.
func TestQueryDataCaps(t *testing.T) {
	ctx := context.Background()
	const orgID int64 = 1
	key := strings.Repeat("3e", 32)
	// A 1 ms grid so a window at the cap is arithmetic, not a gigabyte.
	fit := ForecastRequest{Times: []int64{0, 1}, Values: []nullableFloat{1, 2}, Model: "naive"}

	query := func(t *testing.T, store SnapshotStore, jsonBody []byte, from, to time.Time) backend.DataResponse {
		t.Helper()
		ds, err := newDatasource(ctx, backend.DataSourceInstanceSettings{}, store)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := ds.QueryData(ctx, &backend.QueryDataRequest{
			PluginContext: backend.PluginContext{OrgID: orgID},
			Queries: []backend.DataQuery{{
				RefID:     "B",
				JSON:      jsonBody,
				TimeRange: backend.TimeRange{From: from, To: to},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return resp.Responses["B"]
	}

	t.Run("an absent cacheKey is the miss the overlay gets", func(t *testing.T) {
		store := newMemoryStore()
		seedSnapshot(t, store, orgID, key, fit)
		got := query(t, store, queryJSON(t, "forecast", "", 0),
			time.UnixMilli(2).UTC(), time.UnixMilli(2).UTC())
		if got.Error == nil || got.Error.Error() != msgNeedTrain {
			t.Fatalf("error=%v want %q", got.Error, msgNeedTrain)
		}
		if len(got.Frames) != 0 {
			t.Fatalf("frames=%d", len(got.Frames))
		}
	})

	t.Run("a malformed cacheKey is still a validation error", func(t *testing.T) {
		store := newMemoryStore()
		got := query(t, store, queryJSON(t, "forecast", "not-a-key", 0),
			time.UnixMilli(2).UTC(), time.UnixMilli(2).UTC())
		if got.Error == nil || got.Error.Error() != errInvalidCacheKey.Error() {
			t.Fatalf("error=%v", got.Error)
		}
	})

	t.Run("a window over the cap is refused", func(t *testing.T) {
		store := newMemoryStore()
		seedSnapshot(t, store, orgID, key, fit)
		got := query(t, store, queryJSON(t, "forecast", key, 0),
			time.UnixMilli(2).UTC(), time.UnixMilli(1+maxForecastPoints+1).UTC())
		if got.Error == nil || got.Error.Error() != errWindowTooManyPoints.Error() {
			t.Fatalf("error=%v", got.Error)
		}
		if got.Status != backend.Status(413) {
			t.Fatalf("status=%v", got.Status)
		}
	})

	t.Run("a window whose last point is at the cap is served", func(t *testing.T) {
		store := newMemoryStore()
		seedSnapshot(t, store, orgID, key, fit)
		at := time.UnixMilli(1 + maxForecastPoints).UTC()
		got := query(t, store, queryJSON(t, "forecast", key, 0), at, at)
		if got.Error != nil {
			t.Fatalf("error=%v", got.Error)
		}
		if len(got.Frames) != 1 || got.Frames[0].Fields[1].Len() != 1 {
			t.Fatalf("frames=%+v", got.Frames)
		}
	})
}

func assertFrameValues(t *testing.T, dr backend.DataResponse, want []nullableFloat) {
	t.Helper()
	if dr.Error != nil {
		t.Fatalf("error=%v", dr.Error)
	}
	if len(dr.Frames) != 1 {
		t.Fatalf("frames=%d", len(dr.Frames))
	}
	field := dr.Frames[0].Fields[1]
	if field.Len() != len(want) {
		t.Fatalf("len got=%d want=%d", field.Len(), len(want))
	}
	for i := range want {
		w := float64(want[i])
		raw := field.At(i)
		if math.IsNaN(w) {
			if raw != nil {
				t.Fatalf("i=%d got %v want nil/NaN", i, raw)
			}
			continue
		}
		n, ok := raw.(*float64)
		if !ok || n == nil {
			t.Fatalf("i=%d type %T value %v", i, raw, raw)
		}
		if *n != w {
			t.Fatalf("i=%d got %v want %v", i, *n, w)
		}
	}
}
