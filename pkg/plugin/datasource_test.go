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
			connect: func(_ context.Context, dsn string) (SnapshotStore, func()) {
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
			connect: func(_ context.Context, dsn string) (SnapshotStore, func()) {
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
		want, err := emitForecast(fitted, ForecastRequest{From: 4000, To: 5000, Level: 0.95})
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
