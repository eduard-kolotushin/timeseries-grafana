package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

type mockCallResourceResponseSender struct {
	response *backend.CallResourceResponse
}

func (s *mockCallResourceResponseSender) Send(response *backend.CallResourceResponse) error {
	s.response = response
	return nil
}

func TestCallResource(t *testing.T) {
	inst, err := NewApp(context.Background(), backend.AppInstanceSettings{})
	if err != nil {
		t.Fatalf("new app: %s", err)
	}
	app, ok := inst.(*App)
	if !ok {
		t.Fatal("inst must be of type *App")
	}

	holtBody, _ := json.Marshal(ForecastRequest{
		Times:  []int64{0, 1000, 2000, 3000},
		Values: []nullableFloat{1, 2, 3, 4},
		Model:  "holt",
		From:   4000,
		To:     5000,
		Alpha:  1,
		Beta:   1,
	})
	naiveBody, _ := json.Marshal(ForecastRequest{
		Times:  []int64{0, 1000, 2000, 3000},
		Values: []nullableFloat{1, 2, 3, 4},
		Model:  "naive",
		From:   4000,
		To:     5000,
	})
	meanBody, _ := json.Marshal(ForecastRequest{
		Times:  []int64{0, 1000, 2000, 3000},
		Values: []nullableFloat{1, 2, 3, 4},
		Model:  "mean",
		From:   4000,
		To:     4000,
	})
	driftBody, _ := json.Marshal(ForecastRequest{
		Times:  []int64{0, 1000, 2000, 3000},
		Values: []nullableFloat{1, 2, 3, 4},
		Model:  "drift",
		From:   4000,
		To:     5000,
	})
	badModel, _ := json.Marshal(ForecastRequest{
		Times:  []int64{0, 1000},
		Values: []nullableFloat{1, 2},
		Model:  "arima",
		From:   2000,
		To:     2000,
	})
	baselineTimes := make([]int64, 48)
	baselineVals := make([]nullableFloat, 48)
	for i := range 48 {
		baselineTimes[i] = int64(i) * 3_600_000
		baselineVals[i] = nullableFloat(float64(i))
	}
	baselineBody, _ := json.Marshal(ForecastRequest{
		Times:    baselineTimes,
		Values:   baselineVals,
		Model:    "baseline",
		From:     48 * 3_600_000,
		To:       48 * 3_600_000,
		Season:   "hour",
		Calendar: "ru",
	})
	badCalendar, _ := json.Marshal(ForecastRequest{
		Times:    baselineTimes,
		Values:   baselineVals,
		Model:    "baseline",
		From:     48 * 3_600_000,
		To:       48 * 3_600_000,
		Season:   "hour",
		Calendar: "us",
	})
	minuteWeekBody, _ := json.Marshal(ForecastRequest{
		Times:  baselineTimes,
		Values: baselineVals,
		Model:  "baseline",
		From:   48 * 3_600_000,
		To:     48 * 3_600_000,
		Season: "minute-week",
	})
	intervalBody, _ := json.Marshal(ForecastRequest{
		Times:  []int64{0, 1000, 2000, 3000},
		Values: []nullableFloat{1, 2, 3, 4},
		Model:  "naive",
		From:   4000,
		To:     5000,
		Level:  0.95,
	})
	badLevel, _ := json.Marshal(ForecastRequest{
		Times:  []int64{0, 1000, 2000, 3000},
		Values: []nullableFloat{1, 2, 3, 4},
		Model:  "naive",
		From:   4000,
		To:     4000,
		Level:  1.5,
	})
	skipAheadBody, _ := json.Marshal(ForecastRequest{
		Times:  []int64{0, 1000, 2000, 3000},
		Values: []nullableFloat{1, 2, 3, 4},
		Model:  "holt",
		From:   5000,
		To:     6000,
		Alpha:  1,
		Beta:   1,
	})
	emptyWindowBody, _ := json.Marshal(ForecastRequest{
		Times:  []int64{0, 1000, 2000, 3000},
		Values: []nullableFloat{1, 2, 3, 4},
		Model:  "naive",
		From:   0,
		To:     2000,
	})
	invertedBody, _ := json.Marshal(ForecastRequest{
		Times:  []int64{0, 1000, 2000, 3000},
		Values: []nullableFloat{1, 2, 3, 4},
		Model:  "naive",
		From:   5000,
		To:     4000,
	})

	for _, tc := range []struct {
		name      string
		method    string
		path      string
		body      []byte
		expStatus int
		check     func(t *testing.T, body []byte)
	}{
		{
			name:      "get ping 200",
			method:    http.MethodGet,
			path:      "ping",
			expStatus: http.StatusOK,
		},
		{
			name:      "get forecast 405",
			method:    http.MethodGet,
			path:      "forecast",
			expStatus: http.StatusMethodNotAllowed,
		},
		{
			name:      "holt golden",
			method:    http.MethodPost,
			path:      "forecast",
			body:      holtBody,
			expStatus: http.StatusOK,
			check: func(t *testing.T, body []byte) {
				var got ForecastResponse
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatal(err)
				}
				if len(got.Values) != 2 || float64(got.Values[0]) != 5 || float64(got.Values[1]) != 6 {
					t.Fatalf("holt values=%v", got.Values)
				}
				if got.Times[0] != 4000 || got.Times[1] != 5000 {
					t.Fatalf("holt times=%v", got.Times)
				}
			},
		},
		{
			name:      "naive golden",
			method:    http.MethodPost,
			path:      "forecast",
			body:      naiveBody,
			expStatus: http.StatusOK,
			check: func(t *testing.T, body []byte) {
				var got ForecastResponse
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatal(err)
				}
				if float64(got.Values[0]) != 4 || float64(got.Values[1]) != 4 {
					t.Fatalf("naive values=%v", got.Values)
				}
			},
		},
		{
			name:      "mean golden",
			method:    http.MethodPost,
			path:      "forecast",
			body:      meanBody,
			expStatus: http.StatusOK,
			check: func(t *testing.T, body []byte) {
				var got ForecastResponse
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatal(err)
				}
				if float64(got.Values[0]) != 2.5 {
					t.Fatalf("mean values=%v", got.Values)
				}
			},
		},
		{
			name:      "drift golden",
			method:    http.MethodPost,
			path:      "forecast",
			body:      driftBody,
			expStatus: http.StatusOK,
			check: func(t *testing.T, body []byte) {
				var got ForecastResponse
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatal(err)
				}
				if float64(got.Values[0]) != 5 || float64(got.Values[1]) != 6 {
					t.Fatalf("drift values=%v", got.Values)
				}
			},
		},
		{
			name:      "unknown model 400",
			method:    http.MethodPost,
			path:      "forecast",
			body:      badModel,
			expStatus: http.StatusBadRequest,
		},
		{
			name:      "baseline hour ru 200",
			method:    http.MethodPost,
			path:      "forecast",
			body:      baselineBody,
			expStatus: http.StatusOK,
			check: func(t *testing.T, body []byte) {
				var got ForecastResponse
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatal(err)
				}
				if len(got.Times) != 1 || len(got.Values) != 1 {
					t.Fatalf("baseline len times=%d values=%d", len(got.Times), len(got.Values))
				}
			},
		},
		{
			name:      "unknown calendar 400",
			method:    http.MethodPost,
			path:      "forecast",
			body:      badCalendar,
			expStatus: http.StatusBadRequest,
		},
		{
			name:      "baseline minute-week 200",
			method:    http.MethodPost,
			path:      "forecast",
			body:      minuteWeekBody,
			expStatus: http.StatusOK,
			check: func(t *testing.T, body []byte) {
				var got ForecastResponse
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatal(err)
				}
				if len(got.Times) != 1 || len(got.Values) != 1 {
					t.Fatalf("minute-week len times=%d values=%d", len(got.Times), len(got.Values))
				}
			},
		},
		{
			name:      "naive interval 200",
			method:    http.MethodPost,
			path:      "forecast",
			body:      intervalBody,
			expStatus: http.StatusOK,
			check: func(t *testing.T, body []byte) {
				var got ForecastResponse
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatal(err)
				}
				if len(got.Lower) != len(got.Values) || len(got.Upper) != len(got.Values) {
					t.Fatalf("interval len values=%d lower=%d upper=%d", len(got.Values), len(got.Lower), len(got.Upper))
				}
				for i := range got.Values {
					v, lo, hi := float64(got.Values[i]), float64(got.Lower[i]), float64(got.Upper[i])
					if lo > v || hi < v {
						t.Fatalf("k=%d %v not in [%v, %v]", i+1, v, lo, hi)
					}
				}
			},
		},
		{
			name:      "invalid level 400",
			method:    http.MethodPost,
			path:      "forecast",
			body:      badLevel,
			expStatus: http.StatusBadRequest,
		},
		{
			name:      "holt skip-ahead 200",
			method:    http.MethodPost,
			path:      "forecast",
			body:      skipAheadBody,
			expStatus: http.StatusOK,
			check: func(t *testing.T, body []byte) {
				var got ForecastResponse
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatal(err)
				}
				if len(got.Values) != 2 || float64(got.Values[0]) != 6 || float64(got.Values[1]) != 7 {
					t.Fatalf("skip-ahead values=%v", got.Values)
				}
				if got.Times[0] != 5000 || got.Times[1] != 6000 {
					t.Fatalf("skip-ahead times=%v", got.Times)
				}
			},
		},
		{
			name:      "empty window 400",
			method:    http.MethodPost,
			path:      "forecast",
			body:      emptyWindowBody,
			expStatus: http.StatusBadRequest,
		},
		{
			name:      "inverted range 400",
			method:    http.MethodPost,
			path:      "forecast",
			body:      invertedBody,
			expStatus: http.StatusBadRequest,
		},
		{
			name:      "get non existing handler 404",
			method:    http.MethodGet,
			path:      "not_found",
			expStatus: http.StatusNotFound,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r mockCallResourceResponseSender
			err := app.CallResource(context.Background(), &backend.CallResourceRequest{
				Method: tc.method,
				Path:   tc.path,
				Body:   tc.body,
			}, &r)
			if err != nil {
				t.Fatalf("CallResource error: %s", err)
			}
			if r.response == nil {
				t.Fatal("no response received from CallResource")
			}
			if tc.expStatus != r.response.Status {
				t.Errorf("status want %d got %d body=%s", tc.expStatus, r.response.Status, r.response.Body)
			}
			if tc.check != nil {
				tc.check(t, bytes.TrimSpace(r.response.Body))
			}
		})
	}
}

func TestForecastCache(t *testing.T) {
	store := newMemoryStore()
	app, err := newApp(context.Background(), backend.AppInstanceSettings{}, store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	other := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	saveBody, _ := json.Marshal(ForecastRequest{
		Times:    []int64{0, 1000, 2000, 3000},
		Values:   []nullableFloat{1, 2, 3, 4},
		Model:    "naive",
		From:     4000,
		To:       5000,
		CacheKey: key,
	})
	probeBody, _ := json.Marshal(ForecastRequest{
		Model:    "naive",
		From:     4000,
		To:       5000,
		CacheKey: key,
	})
	retrainProbe, _ := json.Marshal(ForecastRequest{
		Model:    "naive",
		From:     4000,
		To:       5000,
		CacheKey: key,
		Retrain:  true,
	})
	overwrite, _ := json.Marshal(ForecastRequest{
		Times:    []int64{0, 1000, 2000, 3000},
		Values:   []nullableFloat{10, 20, 30, 40},
		Model:    "naive",
		From:     4000,
		To:       5000,
		CacheKey: key,
	})
	badKey, _ := json.Marshal(ForecastRequest{
		Model:    "naive",
		From:     4000,
		To:       5000,
		CacheKey: "not-hex",
	})
	otherKey, _ := json.Marshal(ForecastRequest{
		Model:    "naive",
		From:     4000,
		To:       5000,
		CacheKey: other,
	})

	call := func(org int64, body []byte) (int, ForecastResponse) {
		t.Helper()
		var r mockCallResourceResponseSender
		err := app.CallResource(context.Background(), &backend.CallResourceRequest{
			PluginContext: backend.PluginContext{OrgID: org},
			Method:        http.MethodPost,
			Path:          "forecast",
			Body:          body,
		}, &r)
		if err != nil {
			t.Fatal(err)
		}
		var got ForecastResponse
		if r.response.Status == http.StatusOK {
			if err := json.Unmarshal(bytes.TrimSpace(r.response.Body), &got); err != nil {
				t.Fatal(err)
			}
		}
		return r.response.Status, got
	}

	status, got := call(1, probeBody)
	if status != http.StatusOK || !got.NeedTrain {
		t.Fatalf("miss: status=%d needTrain=%v body=%+v", status, got.NeedTrain, got)
	}

	status, got = call(1, saveBody)
	if status != http.StatusOK || got.NeedTrain || len(got.Values) != 2 || float64(got.Values[0]) != 4 {
		t.Fatalf("save: status=%d got=%+v", status, got)
	}

	status, got = call(1, probeBody)
	if status != http.StatusOK || !got.Cached || got.NeedTrain || float64(got.Values[0]) != 4 {
		t.Fatalf("hit: status=%d got=%+v", status, got)
	}

	status, got = call(2, probeBody)
	if status != http.StatusOK || !got.NeedTrain {
		t.Fatalf("org isolation: status=%d got=%+v", status, got)
	}

	status, got = call(1, otherKey)
	if status != http.StatusOK || !got.NeedTrain {
		t.Fatalf("other key: status=%d got=%+v", status, got)
	}

	status, got = call(1, retrainProbe)
	if status != http.StatusOK || !got.NeedTrain {
		t.Fatalf("retrain probe: status=%d got=%+v", status, got)
	}

	status, got = call(1, overwrite)
	if status != http.StatusOK || float64(got.Values[0]) != 40 {
		t.Fatalf("overwrite: status=%d got=%+v", status, got)
	}
	status, got = call(1, probeBody)
	if status != http.StatusOK || float64(got.Values[0]) != 40 {
		t.Fatalf("after overwrite: status=%d got=%+v", status, got)
	}

	status, _ = call(1, badKey)
	if status != http.StatusBadRequest {
		t.Fatalf("bad key status=%d", status)
	}
}

func TestForecastLoadLimits(t *testing.T) {
	app, err := newApp(context.Background(), backend.AppInstanceSettings{}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	call := func(body []byte) int {
		t.Helper()
		var r mockCallResourceResponseSender
		if err := app.CallResource(context.Background(), &backend.CallResourceRequest{
			Method: http.MethodPost,
			Path:   "forecast",
			Body:   body,
		}, &r); err != nil {
			t.Fatal(err)
		}
		return r.response.Status
	}

	t.Run("train too long 413", func(t *testing.T) {
		prev := maxTrainPoints
		maxTrainPoints = 2
		t.Cleanup(func() { maxTrainPoints = prev })
		body, _ := json.Marshal(ForecastRequest{
			Times:  []int64{0, 1000, 2000},
			Values: []nullableFloat{1, 2, 3},
			Model:  "naive",
			From:   3000,
			To:     3000,
		})
		if status := call(body); status != http.StatusRequestEntityTooLarge {
			t.Fatalf("status=%d", status)
		}
	})

	t.Run("body too large 413", func(t *testing.T) {
		app.maxBody = 32
		t.Cleanup(func() { app.maxBody = maxForecastBodyBytes })
		body := []byte(`{"model":"naive","from":1,"to":2,"times":[0,1],"values":[1,2],"pad":"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}`)
		if status := call(body); status != http.StatusRequestEntityTooLarge {
			t.Fatalf("status=%d body too small to trip cap? len=%d", status, len(body))
		}
	})

	t.Run("busy 429", func(t *testing.T) {
		app.limit = newWorkLimiter(1)
		release, err := app.limit.try(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		body, _ := json.Marshal(ForecastRequest{
			Times:  []int64{0, 1000, 2000, 3000},
			Values: []nullableFloat{1, 2, 3, 4},
			Model:  "naive",
			From:   4000,
			To:     4000,
		})
		if status := call(body); status != http.StatusTooManyRequests {
			t.Fatalf("status=%d", status)
		}
	})

	t.Run("needTrain skip limiter", func(t *testing.T) {
		store := newMemoryStore()
		cached, err := newApp(context.Background(), backend.AppInstanceSettings{}, store, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		cached.limit = newWorkLimiter(1)
		release, err := cached.limit.try(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		key := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		body, _ := json.Marshal(ForecastRequest{
			Model:    "naive",
			From:     4000,
			To:       5000,
			CacheKey: key,
		})
		var r mockCallResourceResponseSender
		if err := cached.CallResource(context.Background(), &backend.CallResourceRequest{
			PluginContext: backend.PluginContext{OrgID: 1},
			Method:        http.MethodPost,
			Path:          "forecast",
			Body:          body,
		}, &r); err != nil {
			t.Fatal(err)
		}
		if r.response.Status != http.StatusOK {
			t.Fatalf("status=%d body=%s", r.response.Status, r.response.Body)
		}
		var got ForecastResponse
		if err := json.Unmarshal(bytes.TrimSpace(r.response.Body), &got); err != nil {
			t.Fatal(err)
		}
		if !got.NeedTrain {
			t.Fatalf("got=%+v", got)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := app.dispatchForecast(ctx, 1, ForecastRequest{
			Times:  []int64{0, 1000, 2000, 3000},
			Values: []nullableFloat{1, 2, 3, 4},
			Model:  "naive",
			From:   4000,
			To:     4000,
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	})
}

// TestForecastScheduleDue pins the probe contract: a stored snapshot answers the
// overlay until the panel's schedule comes due, and a due row forces a refresh
// even for a spec-less row the scheduler itself cannot retrain.
func TestForecastScheduleDue(t *testing.T) {
	clearStoreEnv(t)
	ctx := context.Background()
	store := newMemoryStore()
	sched := newMemSchedules()
	key := strings.Repeat("ef", 32)
	seedSnapshot(t, store, 1, key, ForecastRequest{
		Times:  []int64{0, 1000, 2000, 3000},
		Values: []nullableFloat{1, 2, 3, 4},
		Model:  "naive",
	})
	app, err := newApp(ctx, backend.AppInstanceSettings{}, store, sched, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Dispose)

	probe, _ := json.Marshal(ForecastRequest{Model: "naive", From: 4000, To: 5000, CacheKey: key})
	call := func() ForecastResponse {
		t.Helper()
		var r mockCallResourceResponseSender
		if err := app.CallResource(ctx, &backend.CallResourceRequest{
			PluginContext: backend.PluginContext{OrgID: 1},
			Method:        http.MethodPost,
			Path:          "forecast",
			Body:          probe,
		}, &r); err != nil {
			t.Fatal(err)
		}
		if r.response.Status != http.StatusOK {
			t.Fatalf("status=%d body=%s", r.response.Status, r.response.Body)
		}
		var got ForecastResponse
		if err := json.Unmarshal(bytes.TrimSpace(r.response.Body), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	sched.seed(ScheduleRow{
		OrgID: 1, Scope: scopePanel, Key: key, Cron: "0 3 * * *", Timezone: "UTC",
		Enabled: true, NextRunAt: time.Now().Add(time.Hour),
	})
	got := call()
	if !got.Cached || got.NeedTrain {
		t.Fatalf("future schedule: got=%+v", got)
	}

	sched.seed(ScheduleRow{
		OrgID: 1, Scope: scopePanel, Key: key, Cron: "0 3 * * *", Timezone: "UTC",
		Enabled: true, NextRunAt: time.Now().Add(-time.Minute),
	})
	got = call()
	if !got.NeedTrain || len(got.Values) != 0 {
		t.Fatalf("due schedule: got=%+v", got)
	}

	// A disabled schedule is not the overlay's problem.
	sched.seed(ScheduleRow{
		OrgID: 1, Scope: scopePanel, Key: key, Cron: "0 3 * * *", Timezone: "UTC",
		Enabled: false, NextRunAt: time.Now().Add(-time.Minute),
	})
	if got = call(); !got.Cached {
		t.Fatalf("disabled schedule: got=%+v", got)
	}
}

// A probe never runs the training query, so it cannot write a spec — but it can
// still say which panel is asking. That is what identifies a row written before the
// plugin stored provenance, since the cron path has no panel context to identify it.
func TestForecastProbeIdentifiesScheduleRow(t *testing.T) {
	clearStoreEnv(t)
	ctx := context.Background()
	key := strings.Repeat("9a", 32)
	legacy := json.RawMessage(`{"datasourceUid":"druid","queries":[{"refId":"A","builder":{"queryType":"timeseries"}}],"from":1,"to":2,"seriesName":"value","lookback":"21d","model":"baseline","season":"minute-week"}`)
	sched := newMemSchedules()
	sched.seed(ScheduleRow{
		OrgID: 1, Scope: scopePanel, Key: key, Cron: "0 3 * * *", Timezone: "UTC", Enabled: true,
		Spec: legacy, NextRunAt: time.Now().Add(-time.Minute),
	})
	app := schedulesApp(t, sched)

	probe := func(prov *PanelProvenance) ForecastResponse {
		t.Helper()
		body, _ := json.Marshal(ForecastRequest{Model: "baseline", From: 4000, To: 5000, CacheKey: key, Provenance: prov})
		status, raw := callRoute(t, app, adminCtx(1), http.MethodPost, "forecast", body)
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, raw)
		}
		var got ForecastResponse
		if err := json.Unmarshal(bytes.TrimSpace(raw), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	specOf := func() (retrainSpec, []byte) {
		t.Helper()
		rows, err := sched.List(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 {
			t.Fatalf("rows=%+v", rows)
		}
		spec, err := parseRetrainSpec(rows[0].Spec)
		if err != nil {
			t.Fatal(err)
		}
		return spec, rows[0].Spec
	}

	// A due row needs training, which is also the request that identifies it.
	if got := probe(&PanelProvenance{PanelID: 7, PanelTitle: "CPU", DashboardUID: "dash-1", QuerySummary: "Druid: minuteweek · minute"}); !got.NeedTrain {
		t.Fatalf("due row: %+v", got)
	}
	spec, _ := specOf()
	if spec.PanelID != 7 || spec.PanelTitle != "CPU" || spec.DashboardUID != "dash-1" {
		t.Fatalf("probe did not identify the row: %+v", spec)
	}
	if spec.QuerySummary != "Druid: minuteweek · minute" {
		t.Fatalf("probe did not label the row's query: %+v", spec)
	}
	// The merge adds identity only: the queries the scheduler replays and the model
	// fields the refit needs are still there.
	if len(spec.Queries) == 0 || spec.Model != "baseline" || spec.Season != "minute-week" || spec.SeriesName != "value" {
		t.Fatalf("the merge damaged the spec: %+v", spec)
	}
	// A second probe with the same identity writes nothing: a dashboard view is not a
	// reason to touch the row.
	_, before := specOf()
	if got := probe(&PanelProvenance{PanelID: 7, PanelTitle: "CPU", DashboardUID: "dash-1", QuerySummary: "Druid: minuteweek · minute"}); !got.NeedTrain {
		t.Fatalf("second probe: %+v", got)
	}
	if _, after := specOf(); string(after) != string(before) {
		t.Fatalf("identical provenance rewrote the spec: before=%s after=%s", before, after)
	}

	// A probe from a frontend that sends no provenance leaves the row alone.
	_, before = specOf()
	if got := probe(nil); !got.NeedTrain {
		t.Fatalf("third probe: %+v", got)
	}
	if _, after := specOf(); string(after) != string(before) {
		t.Fatalf("spec rewritten without provenance: before=%s after=%s", before, after)
	}

	// A probe is not a fit: it must never create a row, or the scheduler would claim
	// one whose query objects it does not have.
	other := ForecastRequest{Model: "baseline", From: 4000, To: 5000, CacheKey: strings.Repeat("9b", 32), Provenance: &PanelProvenance{PanelID: 9, PanelTitle: "Other", DashboardUID: "dash-2"}}
	body, _ := json.Marshal(other)
	if status, _ := callRoute(t, app, adminCtx(1), http.MethodPost, "forecast", body); status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	if rows, _ := sched.List(ctx, 1); len(rows) != 1 {
		t.Fatalf("a probe created a row: %+v", rows)
	}
}

// TestForecastRecordsTrainSource pins the fit half of the schedule contract: a
// trained overlay stores the browser's trainSource so the backend can re-fit it
// later, and a later retrain extends that row's schedule instead of resetting it.
func TestForecastRecordsTrainSource(t *testing.T) {
	clearRetrainEnv(t)
	clearStoreEnv(t)
	ctx := context.Background()
	sched := newMemSchedules()
	app, err := newApp(ctx, backend.AppInstanceSettings{}, newMemoryStore(), sched, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Dispose)

	key := strings.Repeat("12", 32)
	fit := func(src *TrainSource) int {
		t.Helper()
		body, _ := json.Marshal(ForecastRequest{
			Times:       []int64{0, 1000, 2000, 3000},
			Values:      []nullableFloat{1, 2, 3, 4},
			Model:       "naive",
			Alpha:       0.4,
			Beta:        0.15,
			Period:      12,
			From:        4000,
			To:          5000,
			CacheKey:    key,
			TrainSource: src,
		})
		var r mockCallResourceResponseSender
		if err := app.CallResource(ctx, &backend.CallResourceRequest{
			PluginContext: backend.PluginContext{OrgID: 3},
			Method:        http.MethodPost,
			Path:          "forecast",
			Body:          body,
		}, &r); err != nil {
			t.Fatal(err)
		}
		return r.response.Status
	}

	// A frontend that predates trainSource fits and caches, but schedules nothing.
	if status := fit(nil); status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	rows, err := sched.List(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("a fit without trainSource scheduled something: %+v", rows)
	}

	src := &TrainSource{
		DatasourceUID: "ds-uid",
		Queries:       json.RawMessage(`[{"refId":"A","intervalMs":60000,"maxDataPoints":43200}]`),
		From:          1_700_000_000_000,
		To:            1_700_100_000_000,
		SeriesName:    "series-1",
		PanelID:       7,
		PanelTitle:    "CPU",
		DashboardUID:  "dash-1",
		QuerySummary:  "PromQL: up",
	}
	if status := fit(src); status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	rows, err = sched.List(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%+v", rows)
	}
	row := rows[0]
	if row.Scope != scopePanel || row.Key != key || !row.Enabled {
		t.Fatalf("row=%+v", row)
	}
	if row.Cron != defaultRetrainCron || row.Timezone != "UTC" {
		t.Fatalf("default schedule=%+v", row)
	}
	if !row.NextRunAt.After(time.Now()) {
		t.Fatalf("next run is not in the future: %s", row.NextRunAt)
	}
	spec, err := parseRetrainSpec(row.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if string(spec.Queries) != string(src.Queries) || spec.SeriesName != "series-1" || spec.Model != "naive" {
		t.Fatalf("spec=%+v", spec)
	}
	// The scheduler re-fits from this spec alone, so the panel's model parameters have to
	// be stored with it: without them a cron retrain fits the backend defaults instead.
	if spec.Alpha != 0.4 || spec.Beta != 0.15 || spec.Period != 12 {
		t.Fatalf("spec parameters were dropped: %+v", spec)
	}
	// The Retrain schedules table identifies a row by the dashboard panel that trained
	// it, so the provenance the panel sent has to survive into the stored spec.
	if spec.PanelID != 7 || spec.PanelTitle != "CPU" || spec.DashboardUID != "dash-1" || spec.QuerySummary != "PromQL: up" {
		t.Fatalf("provenance was dropped from the spec: %+v", spec)
	}

	// An admin's cron and enable state survive the next retrain of the same panel.
	sched.seed(ScheduleRow{
		OrgID: 3, Scope: scopePanel, Key: key, Cron: "*/2 * * * *", Timezone: "Europe/Moscow",
		Enabled: false, Spec: row.Spec, NextRunAt: time.Now().Add(time.Hour),
	})
	if status := fit(src); status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	rows, _ = sched.List(ctx, 3)
	if len(rows) != 1 || rows[0].Cron != "*/2 * * * *" || rows[0].Timezone != "Europe/Moscow" {
		t.Fatalf("retrain reset the schedule: %+v", rows)
	}
	if rows[0].Enabled {
		t.Fatalf("a browser fit re-enabled a schedule the admin turned off: %+v", rows[0])
	}
}
