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
	Times    []int64         `json:"times"`
	Values   []nullableFloat `json:"values"`
	Model    string          `json:"model"`
	From     int64           `json:"from"`
	To       int64           `json:"to"`
	Alpha    float64         `json:"alpha"`
	Beta     float64         `json:"beta"`
	Period   int             `json:"period"`
	Season   string          `json:"season"`
	Calendar string          `json:"calendar"`
	Level    float64         `json:"level"`
}

// ForecastResponse is the JSON body returned by POST /forecast.
type ForecastResponse struct {
	Times  []int64         `json:"times"`
	Values []nullableFloat `json:"values"`
	Lower  []nullableFloat `json:"lower,omitempty"`
	Upper  []nullableFloat `json:"upper,omitempty"`
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
	case "baseline":
		var cal *forecast.Calendar
		cal, err = forecast.CalendarByName(in.Calendar)
		if err != nil {
			return ForecastResponse{}, err
		}
		fitted, err = forecast.FitSeasonalBaseline(s, parseSeason(in.Season), cal)
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

	from := time.UnixMilli(in.From).UTC()
	to := time.UnixMilli(in.To).UTC()
	out, err := fitted.ForecastRange(from, to)
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
	if in.Level != 0 {
		lower, upper, err := fitted.ForecastIntervalRange(from, to, in.Level)
		if err != nil {
			return ForecastResponse{}, err
		}
		resp.Lower = toNullable(lower)
		resp.Upper = toNullable(upper)
	}
	return resp, nil
}

func toNullable(s timeseries.Series[float64]) []nullableFloat {
	vs := s.Values()
	out := make([]nullableFloat, len(vs))
	for i, v := range vs {
		out[i] = nullableFloat(v)
	}
	return out
}

func parseSeason(s string) forecast.Seasonality {
	switch s {
	case "", "hour":
		return forecast.SeasonHour
	case "day":
		return forecast.SeasonDay
	case "week":
		return forecast.SeasonHourOfWeek
	case "minute-week":
		return forecast.SeasonMinuteOfWeek
	default:
		return 0
	}
}

func httpStatusFor(err error) int {
	switch {
	case errors.Is(err, errUnknownModel),
		errors.Is(err, forecast.ErrEmpty),
		errors.Is(err, forecast.ErrHorizon),
		errors.Is(err, forecast.ErrNoFrequency),
		errors.Is(err, forecast.ErrInvalidAlpha),
		errors.Is(err, forecast.ErrInvalidPeriod),
		errors.Is(err, forecast.ErrInvalidSeason),
		errors.Is(err, forecast.ErrUnknownCalendar),
		errors.Is(err, forecast.ErrTooShort),
		errors.Is(err, forecast.ErrInvalidLevel),
		errors.Is(err, forecast.ErrRange),
		errors.Is(err, forecast.ErrEmptyRange),
		errors.Is(err, timeseries.ErrLengthMismatch),
		errors.Is(err, timeseries.ErrUnsorted),
		errors.Is(err, timeseries.ErrDuplicateTime):
		return 400
	default:
		return 500
	}
}
