package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/eduard-kolotushin/timeseries"
	forecast "github.com/eduard-kolotushin/timeseries-forecast"
)

var errUnknownModel = errors.New("forecast: unknown model")

// ForecastRequest is the JSON body for POST /forecast.
type ForecastRequest struct {
	Times   []int64         `json:"times"`
	Values  []nullableFloat `json:"values"`
	Model   string          `json:"model"`
	Horizon int             `json:"horizon"`
	Alpha   float64         `json:"alpha"`
	Beta    float64         `json:"beta"`
	Period  int             `json:"period"`
}

// ForecastResponse is the JSON body returned by POST /forecast.
type ForecastResponse struct {
	Times  []int64         `json:"times"`
	Values []nullableFloat `json:"values"`
}

type nullableFloat float64

func (f nullableFloat) MarshalJSON() ([]byte, error) {
	if math.IsNaN(float64(f)) {
		return []byte("null"), nil
	}
	return json.Marshal(float64(f))
}

func (f *nullableFloat) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*f = nullableFloat(math.NaN())
		return nil
	}
	var v float64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*f = nullableFloat(v)
	return nil
}

func runForecast(in ForecastRequest) (ForecastResponse, error) {
	if len(in.Times) != len(in.Values) {
		return ForecastResponse{}, timeseries.ErrLengthMismatch
	}
	times := make([]time.Time, len(in.Times))
	values := make([]float64, len(in.Values))
	for i := range in.Times {
		times[i] = time.UnixMilli(in.Times[i]).UTC()
		values[i] = float64(in.Values[i])
	}
	s, err := timeseries.New(times, values)
	if err != nil {
		return ForecastResponse{}, err
	}

	model := in.Model
	if model == "" {
		model = "holt"
	}
	alpha := in.Alpha
	if alpha == 0 {
		alpha = 0.8
	}
	beta := in.Beta
	if beta == 0 {
		beta = 0.2
	}
	period := in.Period
	if period == 0 {
		period = 7
	}

	var fitted forecast.Fitted
	switch model {
	case "naive":
		fitted, err = forecast.FitNaive(s)
	case "mean":
		fitted, err = forecast.FitMean(s)
	case "drift":
		fitted, err = forecast.FitDrift(s)
	case "seasonal":
		fitted, err = forecast.FitSeasonalNaive(s, period)
	case "ses":
		fitted, err = forecast.FitSES(s, alpha)
	case "holt":
		fitted, err = forecast.FitHolt(s, alpha, beta)
	default:
		return ForecastResponse{}, fmt.Errorf("%w: %s", errUnknownModel, model)
	}
	if err != nil {
		return ForecastResponse{}, err
	}

	out, err := fitted.Forecast(in.Horizon)
	if err != nil {
		return ForecastResponse{}, err
	}
	resp := ForecastResponse{
		Times:  make([]int64, out.Len()),
		Values: make([]nullableFloat, out.Len()),
	}
	ts := out.Times()
	vs := out.Values()
	for i := range ts {
		resp.Times[i] = ts[i].UnixMilli()
		resp.Values[i] = nullableFloat(vs[i])
	}
	return resp, nil
}

func httpStatusFor(err error) int {
	switch {
	case errors.Is(err, errUnknownModel),
		errors.Is(err, forecast.ErrEmpty),
		errors.Is(err, forecast.ErrHorizon),
		errors.Is(err, forecast.ErrNoFrequency),
		errors.Is(err, forecast.ErrInvalidAlpha),
		errors.Is(err, forecast.ErrInvalidPeriod),
		errors.Is(err, forecast.ErrTooShort),
		errors.Is(err, timeseries.ErrLengthMismatch),
		errors.Is(err, timeseries.ErrUnsorted),
		errors.Is(err, timeseries.ErrDuplicateTime):
		return 400
	default:
		return 500
	}
}
