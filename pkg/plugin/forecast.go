package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"time"

	"github.com/eduard-kolotushin/timeseries"
	forecast "github.com/eduard-kolotushin/timeseries-forecast"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

var (
	errUnknownModel    = errors.New("forecast: unknown model")
	errInvalidCacheKey = errors.New("forecast: cacheKey must be 64 lowercase hex chars")
	cacheKeyPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// TrainSource is the training window exactly as the browser produced it. The
// query objects are opaque here — pkg/ carries no datasource-specific field or
// type name — and the backend replays them verbatim through Grafana's own query
// API when the schedule fires with no browser attached.
type TrainSource struct {
	DatasourceUID string          `json:"datasourceUid"`
	Queries       json.RawMessage `json:"queries"`
	From          int64           `json:"from"`
	To            int64           `json:"to"`
	SeriesName    string          `json:"seriesName,omitempty"`
	// Relative marks From/To as "the browser resolved a lookback against the
	// panel's own now": a cron retrain re-resolves [now - LookbackMs, now] at
	// claim time, so the window moves with the clock instead of refetching one
	// frozen range forever. From/To stay the stored absolute pair and are the
	// only correct window for a panel whose picker held real dates, and the
	// fallback for rows an older frontend wrote.
	Relative   bool  `json:"relative,omitempty"`
	LookbackMs int64 `json:"lookbackMs,omitempty"`
	// Provenance for the schedules table's Source column: which dashboard panel
	// trained this row and on what query. Identification only — no field here may
	// enter the cacheKey fingerprint, or adding one would orphan every stored
	// snapshot the panel can still reach.
	PanelID      int    `json:"panelId,omitempty"`
	PanelTitle   string `json:"panelTitle,omitempty"`
	DashboardUID string `json:"dashboardUid,omitempty"`
	QuerySummary string `json:"querySummary,omitempty"`
}

// ForecastRequest is the JSON body for POST /forecast.
type ForecastRequest struct {
	Times       []int64         `json:"times"`
	Values      []nullableFloat `json:"values"`
	Model       string          `json:"model"`
	From        int64           `json:"from"`
	To          int64           `json:"to"`
	Alpha       float64         `json:"alpha"`
	Beta        float64         `json:"beta"`
	Period      int             `json:"period"`
	Season      string          `json:"season"`
	Calendar    string          `json:"calendar"`
	Level       float64         `json:"level"`
	CacheKey    string          `json:"cacheKey"`
	Retrain     bool            `json:"retrain"`
	TrainSource *TrainSource    `json:"trainSource,omitempty"`
	// Provenance identifies the panel on both a probe and a fit, so a row written
	// before the plugin recorded it is identified on the next dashboard load. It is
	// deliberately outside trainSource: a probe never runs the training query.
	Provenance *PanelProvenance `json:"provenance,omitempty"`
}

// PanelProvenance is the panel's own identity, as the overlay knows it from
// PanelProps and the dashboard URL. Field tags match TrainSource's, because both
// write the same keys into a schedule row's spec.
type PanelProvenance struct {
	PanelID      int    `json:"panelId,omitempty"`
	PanelTitle   string `json:"panelTitle,omitempty"`
	DashboardUID string `json:"dashboardUid,omitempty"`
	QuerySummary string `json:"querySummary,omitempty"`
}

// empty reports whether the overlay resolved nothing, so a merge would be a no-op.
func (p PanelProvenance) empty() bool {
	return p.PanelID == 0 && p.PanelTitle == "" && p.DashboardUID == "" && p.QuerySummary == ""
}

// ForecastResponse is the JSON body returned by POST /forecast.
type ForecastResponse struct {
	Times     []int64         `json:"times,omitempty"`
	Values    []nullableFloat `json:"values,omitempty"`
	Lower     []nullableFloat `json:"lower,omitempty"`
	Upper     []nullableFloat `json:"upper,omitempty"`
	NeedTrain bool            `json:"needTrain,omitempty"`
	Cached    bool            `json:"cached,omitempty"`
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

// dispatchForecast routes one POST /forecast. The inflight limiter bounds CPU
// work only (Fit, Restore, ForecastRange); snapshot store I/O runs outside it
// so a slow Postgres does not hold compute slots and turn into 429s.
func (a *App) dispatchForecast(ctx context.Context, orgID int64, in ForecastRequest) (ForecastResponse, error) {
	if in.CacheKey != "" && !cacheKeyPattern.MatchString(in.CacheKey) {
		return ForecastResponse{}, errInvalidCacheKey
	}
	if err := checkTrainLen(len(in.Times), len(in.Values)); err != nil {
		return ForecastResponse{}, err
	}
	hasTimes := len(in.Times) > 0 || len(in.Values) > 0
	// Identify on the read paths only. A request that carries training points writes
	// the whole spec a few lines below (recordPanelSchedule) with the same provenance,
	// and a Retrain probe is answered by such a fit, so merging here would be a
	// redundant Postgres round trip on the request the user is waiting for. What is
	// left is the cached-load path, which already reads this row (Due, Get).
	if in.Provenance != nil && in.CacheKey != "" && a.sched != nil && !hasTimes && !in.Retrain {
		a.identifyPanel(ctx, orgID, in.CacheKey, *in.Provenance)
	}
	if in.CacheKey == "" {
		return runLimited(ctx, a.computeLimit(), func() (ForecastResponse, error) {
			return fitAndEmit(in)
		})
	}
	if hasTimes {
		type fitOut struct {
			resp ForecastResponse
			snap forecast.Snapshot
		}
		out, err := runLimited(ctx, a.computeLimit(), func() (fitOut, error) {
			fitted, err := fitRequest(in)
			if err != nil {
				return fitOut{}, err
			}
			resp, err := emitForecast(fitted, in.From, in.To, in.Level)
			if err != nil {
				return fitOut{}, err
			}
			var snap forecast.Snapshot
			if a.store != nil {
				if snap, err = forecast.SnapshotOf(fitted); err != nil {
					return fitOut{}, err
				}
			}
			return fitOut{resp: resp, snap: snap}, nil
		})
		if err != nil {
			return ForecastResponse{}, err
		}
		if a.store != nil {
			if err := a.store.Put(ctx, orgID, in.CacheKey, out.snap); err != nil {
				return ForecastResponse{}, err
			}
			a.recordPanelSchedule(ctx, orgID, in)
		}
		return out.resp, nil
	}
	if in.Retrain || a.store == nil {
		return ForecastResponse{NeedTrain: true}, nil
	}
	// A due schedule means a retrain is owed, so the overlay refreshes the
	// snapshot even for a row the scheduler cannot retrain itself (no spec).
	if a.sched != nil {
		due, err := a.sched.Due(ctx, orgID, in.CacheKey, time.Now())
		if err != nil {
			log.DefaultLogger.Warn("schedule due", "key", in.CacheKey, "err", err.Error())
		} else if due {
			return ForecastResponse{NeedTrain: true}, nil
		}
	}
	snap, ok, err := a.store.Get(ctx, orgID, in.CacheKey)
	if err != nil {
		return ForecastResponse{}, err
	}
	if !ok {
		return ForecastResponse{NeedTrain: true}, nil
	}
	return runLimited(ctx, a.computeLimit(), func() (ForecastResponse, error) {
		fitted, err := forecast.Restore(snap)
		if err != nil {
			return ForecastResponse{}, err
		}
		out, err := emitForecast(fitted, in.From, in.To, in.Level)
		if err != nil {
			return ForecastResponse{}, err
		}
		out.Cached = true
		return out, nil
	})
}

// identifyPanel writes the panel's identity into an existing schedule row's spec.
// A probe reaches here without ever running the training query, which is the point:
// a row whose spec predates this field is identified on the next dashboard load
// instead of waiting for a refit the scheduler cannot do for it (it has no panel
// context). Like every other schedule write, it may never fail the query.
func (a *App) identifyPanel(ctx context.Context, orgID int64, key string, prov PanelProvenance) {
	if prov.empty() {
		return
	}
	if err := a.sched.Identify(ctx, orgID, key, prov); err != nil {
		log.DefaultLogger.Warn("schedule identify", "key", key, "err", err.Error())
	}
}

// recordPanelSchedule registers (or refreshes) the trained panel's row in the
// schedule table, which is what lets the backend re-fit it later with no browser
// open. Nothing here may fail the fit the user is waiting on: a scheduler or
// store error is logged and the response stands.
func (a *App) recordPanelSchedule(ctx context.Context, orgID int64, in ForecastRequest) {
	if a.sched == nil || in.TrainSource == nil {
		return
	}
	cronSpec, timezone, enabled := a.retrain.Cron, a.retrain.Timezone, true
	// An existing row's cron and enable state are the admin's, not the defaults: a
	// browser retrain must extend the row, never reset when it fires or switch off a
	// schedule an admin turned off. One row is read by key: listing the table would
	// transfer and decode every worker baseline row to find it.
	if row, ok, err := a.sched.Row(ctx, orgID, scopePanel, in.CacheKey); err != nil {
		log.DefaultLogger.Warn("schedule row", "err", err.Error())
	} else if ok {
		cronSpec, timezone, enabled = row.Cron, row.Timezone, row.Enabled
	}
	next, err := nextRun(cronSpec, timezone, time.Now())
	if err != nil {
		log.DefaultLogger.Error("schedule cron", "cron", cronSpec, "timezone", timezone, "err", err.Error())
		return
	}
	spec, err := json.Marshal(retrainSpec{
		TrainSource: *in.TrainSource,
		Model:       in.Model,
		Alpha:       in.Alpha,
		Beta:        in.Beta,
		Period:      in.Period,
		Season:      in.Season,
		Calendar:    in.Calendar,
		Lookback:    lookbackString(in.TrainSource.To - in.TrainSource.From),
	})
	if err != nil {
		log.DefaultLogger.Error("schedule spec", "key", in.CacheKey, "err", err.Error())
		return
	}
	err = a.sched.Upsert(ctx, orgID, ScheduleRow{
		Scope:     scopePanel,
		Key:       in.CacheKey,
		Cron:      cronSpec,
		Timezone:  normalizedTimezone(timezone),
		Enabled:   enabled,
		Spec:      spec,
		NextRunAt: next,
	})
	if err != nil {
		log.DefaultLogger.Error("schedule upsert", "key", in.CacheKey, "err", err.Error())
	}
}

// lookbackString renders the stored training width for the Retrain schedules
// table: whole days and hours read as 21d / 6h, and anything else as a Go
// duration. It is display only — a cron retrain re-resolves the window from
// lookbackMs / from / to, never from this string.
func lookbackString(ms int64) string {
	if ms <= 0 {
		return ""
	}
	const (
		dayMs  = int64(24 * time.Hour / time.Millisecond)
		hourMs = int64(time.Hour / time.Millisecond)
		minMs  = int64(time.Minute / time.Millisecond)
	)
	switch {
	case ms%dayMs == 0:
		return fmt.Sprintf("%dd", ms/dayMs)
	case ms%hourMs == 0:
		return fmt.Sprintf("%dh", ms/hourMs)
	case ms%minMs == 0:
		return fmt.Sprintf("%dm", ms/minMs)
	}
	return (time.Duration(ms) * time.Millisecond).String()
}

func fitAndEmit(in ForecastRequest) (ForecastResponse, error) {
	fitted, err := fitRequest(in)
	if err != nil {
		return ForecastResponse{}, err
	}
	return emitForecast(fitted, in.From, in.To, in.Level)
}

func fitRequest(in ForecastRequest) (forecast.Fitted, error) {
	if len(in.Times) != len(in.Values) {
		return nil, timeseries.ErrLengthMismatch
	}
	times := make([]time.Time, len(in.Times))
	values := make([]float64, len(in.Values))
	for i := range in.Times {
		times[i] = time.UnixMilli(in.Times[i]).UTC()
		values[i] = float64(in.Values[i])
	}
	s, err := timeseries.New(times, values)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		fitted, err = forecast.FitSeasonalBaseline(s, parseSeason(in.Season), cal)
	case "ses":
		fitted, err = forecast.FitSES(s, alpha)
	case "holt":
		fitted, err = forecast.FitHolt(s, alpha, beta)
	default:
		return nil, fmt.Errorf("%w: %s", errUnknownModel, model)
	}
	if err != nil {
		return nil, err
	}
	return fitted, nil
}

// emitForecast runs ForecastRange (and ForecastIntervalRange when level != 0)
// over [fromMs, toMs] and encodes the result for JSON.
func emitForecast(fitted forecast.Fitted, fromMs, toMs int64, level float64) (ForecastResponse, error) {
	from := time.UnixMilli(fromMs).UTC()
	to := time.UnixMilli(toMs).UTC()
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
	if level != 0 {
		lower, upper, err := fitted.ForecastIntervalRange(from, to, level)
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
	case errors.Is(err, errBusy):
		return http.StatusTooManyRequests
	case errors.Is(err, errTrainTooLong), errors.Is(err, errBodyTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, context.Canceled):
		return 499
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusRequestTimeout
	case errors.Is(err, errUnknownModel),
		errors.Is(err, errInvalidCacheKey),
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
		errors.Is(err, forecast.ErrUnknownSnapshot),
		errors.Is(err, forecast.ErrInvalidSnapshot),
		errors.Is(err, timeseries.ErrLengthMismatch),
		errors.Is(err, timeseries.ErrUnsorted),
		errors.Is(err, timeseries.ErrDuplicateTime):
		return 400
	default:
		return 500
	}
}
