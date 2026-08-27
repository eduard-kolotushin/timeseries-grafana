package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

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
	app, err := newApp(context.Background(), backend.AppInstanceSettings{}, store)
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
