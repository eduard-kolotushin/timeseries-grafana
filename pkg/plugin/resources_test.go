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
		Times:   []int64{0, 1000, 2000, 3000},
		Values:  []nullableFloat{1, 2, 3, 4},
		Model:   "holt",
		Horizon: 2,
		Alpha:   1,
		Beta:    1,
	})
	naiveBody, _ := json.Marshal(ForecastRequest{
		Times:   []int64{0, 1000, 2000, 3000},
		Values:  []nullableFloat{1, 2, 3, 4},
		Model:   "naive",
		Horizon: 2,
	})
	meanBody, _ := json.Marshal(ForecastRequest{
		Times:   []int64{0, 1000, 2000, 3000},
		Values:  []nullableFloat{1, 2, 3, 4},
		Model:   "mean",
		Horizon: 1,
	})
	driftBody, _ := json.Marshal(ForecastRequest{
		Times:   []int64{0, 1000, 2000, 3000},
		Values:  []nullableFloat{1, 2, 3, 4},
		Model:   "drift",
		Horizon: 2,
	})
	badModel, _ := json.Marshal(ForecastRequest{
		Times:   []int64{0, 1000},
		Values:  []nullableFloat{1, 2},
		Model:   "arima",
		Horizon: 1,
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
		Horizon:  1,
		Season:   "hour",
		Calendar: "ru",
	})
	badCalendar, _ := json.Marshal(ForecastRequest{
		Times:    baselineTimes,
		Values:   baselineVals,
		Model:    "baseline",
		Horizon:  1,
		Season:   "hour",
		Calendar: "us",
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
