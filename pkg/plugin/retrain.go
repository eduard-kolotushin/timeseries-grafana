package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/eduard-kolotushin/timeseries"
	forecast "github.com/eduard-kolotushin/timeseries-forecast"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

const (
	defaultRetrainEnabled = true
	defaultRetrainTick    = 30 * time.Second
	defaultRetrainLease   = 5 * time.Minute
	defaultRetrainCron    = "0 3 * * *"
	defaultGrafanaURL     = "http://127.0.0.1:3000"

	// retrainClaimBatch bounds one tick's work: each claimed row costs one
	// datasource query plus a fit, and the compute limiter is the tighter bound
	// anyway (a busy slot retries on the next tick).
	retrainClaimBatch = 4
	// frameFetchTimeout bounds one /api/ds/query call, so a hung Grafana query
	// cannot hold a claim for the whole lease.
	frameFetchTimeout = 60 * time.Second
	// maxDSQueryReplyBytes caps one decoder buffer. A bounded train window
	// (~20k points per series) is far below it; the cap only exists so a
	// misconfigured datasource cannot exhaust plugin memory.
	maxDSQueryReplyBytes = 64 << 20
)

// retrainConfig is the scheduler's resolved configuration. Every field falls
// back through storeLookup's FORECAST_* → GF_PLUGIN_* → grafana.ini → jsonData
// precedence, so the Configuration page, the ini file and the process env are
// interchangeable.
type retrainConfig struct {
	Enabled    bool
	Tick       time.Duration
	Lease      time.Duration
	Cron       string
	Timezone   string
	GrafanaURL string
	Token      string
}

func computeRetrain(ctx context.Context, settings backend.AppInstanceSettings) retrainConfig {
	jd := map[string]any{}
	if len(settings.JSONData) > 0 {
		_ = json.Unmarshal(settings.JSONData, &jd)
	}
	look := storeLookup{
		getenv: os.Getenv,
		cfg:    backend.GrafanaConfigFromContext(ctx),
		json:   jd,
	}
	token := look.get("FORECAST_GRAFANA_TOKEN", "GRAFANA_TOKEN", "grafana_token", "grafanaToken")
	if token == "" && settings.DecryptedSecureJSONData != nil {
		token = strings.TrimSpace(settings.DecryptedSecureJSONData["grafanaToken"])
	}
	cronSpec := look.get("FORECAST_RETRAIN_CRON", "RETRAIN_CRON", "retrain_cron", "retrainCron")
	if cronSpec == "" {
		cronSpec = defaultRetrainCron
	}
	url := look.get("FORECAST_GRAFANA_URL", "GRAFANA_URL", "grafana_url", "grafanaUrl")
	if url == "" {
		url = defaultGrafanaURL
	}
	return retrainConfig{
		Enabled:    parseBool(look.get("FORECAST_RETRAIN_ENABLED", "RETRAIN_ENABLED", "retrain_enabled", "retrainEnabled"), defaultRetrainEnabled),
		Tick:       parseDuration(look.get("FORECAST_RETRAIN_TICK", "RETRAIN_TICK", "retrain_tick", "retrainTick"), defaultRetrainTick),
		Lease:      parseDuration(look.get("FORECAST_RETRAIN_LEASE", "RETRAIN_LEASE", "retrain_lease", "retrainLease"), defaultRetrainLease),
		Cron:       cronSpec,
		Timezone:   jsonField(jd, "retrainTimezone"),
		GrafanaURL: url,
		Token:      token,
	}
}

// parseBool keeps the default for anything it cannot read, so a typo in
// retrain_enabled cannot silently turn off unattended retraining.
func parseBool(s string, def bool) bool {
	if strings.TrimSpace(s) == "" {
		return def
	}
	v, err := strconv.ParseBool(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return v
}

func parseDuration(s string, def time.Duration) time.Duration {
	if strings.TrimSpace(s) == "" {
		return def
	}
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil || d <= 0 {
		return def
	}
	return d
}

// retrainSpec is the stored forecast.retrain.spec for a panel row: the
// browser's trainSource, verbatim, plus the model fields the scheduler needs to
// re-fit with no browser attached. The worker writes the same model fields for
// its baseline rows. TrainSource is embedded, so the JSON is flat — exactly what
// Grafana's own /api/ds/query envelope consumes.
type retrainSpec struct {
	TrainSource
	Model    string `json:"model"`
	Season   string `json:"season"`
	Calendar string `json:"calendar"`
	Lookback string `json:"lookback"`
}

func parseRetrainSpec(raw []byte) (retrainSpec, error) {
	if len(raw) == 0 {
		return retrainSpec{}, errors.New("forecast: schedule row has no spec")
	}
	var spec retrainSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return retrainSpec{}, fmt.Errorf("forecast: schedule spec: %w", err)
	}
	if len(spec.Queries) == 0 {
		return retrainSpec{}, errors.New("forecast: schedule spec has no queries")
	}
	return spec, nil
}

// framePoster fetches one schedule row's training frames through Grafana. It is
// a seam so the scheduler can be exercised without a Grafana.
type framePoster interface {
	fetchFrames(ctx context.Context, row ScheduleRow) (data.Frames, error)
}

// grafanaPoster posts the stored query objects to Grafana's own query API. It
// never inspects them: pkg/ carries no datasource-specific field or type name,
// and the type-keyed rewrite stays in the overlay frontend.
type grafanaPoster struct {
	url    string
	token  string
	client *http.Client
}

func newGrafanaPoster(cfg retrainConfig) *grafanaPoster {
	return &grafanaPoster{
		url:    strings.TrimRight(cfg.GrafanaURL, "/"),
		token:  cfg.Token,
		client: &http.Client{Timeout: frameFetchTimeout},
	}
}

// dsQueryResponse is the subset of the /api/ds/query reply the scheduler reads.
// Frames decode through the SDK's own data.Frame unmarshaller.
type dsQueryResponse struct {
	Results map[string]struct {
		Status int         `json:"status"`
		Error  string      `json:"error"`
		Frames data.Frames `json:"frames"`
	} `json:"results"`
}

func (p *grafanaPoster) fetchFrames(ctx context.Context, row ScheduleRow) (data.Frames, error) {
	spec, err := parseRetrainSpec(row.Spec)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(struct {
		Queries json.RawMessage `json:"queries"`
		From    string          `json:"from"`
		To      string          `json:"to"`
	}{
		Queries: spec.Queries,
		From:    strconv.FormatInt(spec.From, 10),
		To:      strconv.FormatInt(spec.To, 10),
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url+"/api/ds/query", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxDSQueryReplyBytes))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) >= maxDSQueryReplyBytes {
		return nil, fmt.Errorf("forecast: /api/ds/query reply exceeds %d bytes", int64(maxDSQueryReplyBytes))
	}
	if resp.StatusCode != http.StatusOK {
		detail := strings.TrimSpace(string(raw))
		if len(detail) > 200 {
			detail = detail[:200]
		}
		return nil, fmt.Errorf("forecast: /api/ds/query status %d: %s", resp.StatusCode, detail)
	}
	var out dsQueryResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("forecast: /api/ds/query: %w", err)
	}
	frames := make(data.Frames, 0, len(out.Results))
	for _, res := range out.Results {
		if res.Error != "" {
			return nil, fmt.Errorf("forecast: /api/ds/query: %s", res.Error)
		}
		frames = append(frames, res.Frames...)
	}
	if len(frames) == 0 {
		return nil, errors.New("forecast: /api/ds/query returned no frames")
	}
	return frames, nil
}

// seriesFromFrames extracts the training series. The spec's seriesName picks the
// field when the reply carries more than one; otherwise the first numeric field
// of the first frame that has a time field wins. Null and NaN points are dropped,
// and a minute-of-week baseline additionally refuses a series whose last step is
// not one minute: that seasonality is indexed by minute, so any other grid would
// be fitted into the wrong slots.
func seriesFromFrames(frames data.Frames, spec retrainSpec) (timeseries.Series[float64], error) {
	timeField, valueField, ok := pickSeries(frames, spec.SeriesName)
	if !ok {
		return timeseries.Series[float64]{}, errors.New("forecast: no time series in /api/ds/query reply")
	}
	n := min(timeField.Len(), valueField.Len())
	times := make([]time.Time, 0, n)
	values := make([]float64, 0, n)
	for i := range n {
		ts, ok := timeField.At(i).(time.Time)
		if !ok {
			continue
		}
		v, ok := numericAt(valueField, i)
		if !ok || math.IsNaN(v) {
			continue
		}
		times = append(times, ts.UTC())
		values = append(values, v)
	}
	if len(times) >= 2 && parseSeason(spec.Season) == forecast.SeasonMinuteOfWeek {
		if step := times[len(times)-1].Sub(times[len(times)-2]); step != time.Minute {
			return timeseries.Series[float64]{}, fmt.Errorf("forecast: minute-week retrain needs a 1m series, last step is %s", step)
		}
	}
	return timeseries.New(times, values)
}

// pickSeries returns the time field of the first frame that also carries a
// numeric field, preferring the frame whose numeric field the spec named.
func pickSeries(frames data.Frames, name string) (timeField, valueField *data.Field, ok bool) {
	var anyTime, anyValue *data.Field
	for _, fr := range frames {
		if fr == nil {
			continue
		}
		t, named, any := frameFields(fr, name)
		if t == nil {
			continue
		}
		if name != "" && named != nil {
			return t, named, true
		}
		if anyTime == nil && any != nil {
			anyTime, anyValue = t, any
		}
	}
	if anyTime == nil {
		return nil, nil, false
	}
	return anyTime, anyValue, true
}

func frameFields(fr *data.Frame, name string) (timeField, named, any *data.Field) {
	for _, f := range fr.Fields {
		if f == nil {
			continue
		}
		switch {
		case timeField == nil && f.Type().Time():
			timeField = f
		case f.Type().Numeric():
			if any == nil {
				any = f
			}
			if named == nil && f.Name == name {
				named = f
			}
		}
	}
	return timeField, named, any
}

func numericAt(f *data.Field, i int) (float64, bool) {
	switch v := f.At(i).(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case *float64:
		if v == nil {
			return 0, false
		}
		return *v, true
	case *float32:
		if v == nil {
			return 0, false
		}
		return float64(*v), true
	case *int64:
		if v == nil {
			return 0, false
		}
		return float64(*v), true
	case *int32:
		if v == nil {
			return 0, false
		}
		return float64(*v), true
	default:
		return 0, false
	}
}

// runScheduler retrains due panel rows on a ticker, with no browser attached.
// Claiming is fleet-wide and lease-based, so every Grafana replica may run this
// without coordinating: SKIP LOCKED hands each due row to exactly one process.
func (a *App) runScheduler(ctx context.Context) {
	ticker := time.NewTicker(a.retrain.Tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.retrainDue(ctx)
		}
	}
}

func (a *App) retrainDue(ctx context.Context) {
	rows, err := a.sched.Claim(ctx, retrainOwner(), a.retrain.Lease, retrainClaimBatch)
	if err != nil {
		log.DefaultLogger.Error("retrain claim", "err", err.Error())
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		a.retrainOne(ctx, row)
	}
}

func (a *App) retrainOne(ctx context.Context, row ScheduleRow) {
	started := time.Now()
	err := a.trainFromSpec(ctx, row)
	status := "ok"
	// A failed retrain backs off for one lease; a busy compute slot only costs a
	// tick, because the panel's own query is waiting on the same limiter.
	next := time.Now().Add(a.retrain.Lease)
	switch {
	case err != nil:
		status = "error: " + err.Error()
		if errors.Is(err, errBusy) {
			next = time.Now().Add(a.retrain.Tick)
		}
	default:
		when, nerr := nextRun(row.Cron, row.Timezone, time.Now())
		if nerr != nil {
			status = "error: " + nerr.Error()
		} else {
			next = when
		}
	}
	if ferr := a.sched.Finish(ctx, row.Scope, row.Key, next, status); ferr != nil {
		log.DefaultLogger.Error("retrain finish", "scope", row.Scope, "key", row.Key, "status", status, "err", ferr.Error())
		return
	}
	log.DefaultLogger.Info("retrain", "scope", row.Scope, "key", row.Key, "status", status, "dur", time.Since(started).Round(time.Millisecond).String())
}

// trainFromSpec re-runs the fit the browser did, from the stored spec alone.
// The frame fetch stays outside the compute limiter, so a slow datasource does
// not hold a Fit slot; only Fit and SnapshotOf are limited.
func (a *App) trainFromSpec(ctx context.Context, row ScheduleRow) error {
	if row.Scope != scopePanel {
		return fmt.Errorf("forecast: %q is not a panel schedule", row.Scope)
	}
	if a.store == nil {
		return errNoStore
	}
	spec, err := parseRetrainSpec(row.Spec)
	if err != nil {
		return err
	}
	frames, err := a.poster.fetchFrames(ctx, row)
	if err != nil {
		return err
	}
	series, err := seriesFromFrames(frames, spec)
	if err != nil {
		return err
	}
	if err := checkTrainLen(series.Len(), series.Len()); err != nil {
		return err
	}
	ts := series.Times()
	vs := series.Values()
	in := ForecastRequest{
		Times:    make([]int64, len(ts)),
		Values:   make([]nullableFloat, len(vs)),
		Model:    spec.Model,
		Season:   spec.Season,
		Calendar: spec.Calendar,
		From:     spec.From,
		To:       spec.To,
	}
	for i := range ts {
		in.Times[i] = ts[i].UnixMilli()
		in.Values[i] = nullableFloat(vs[i])
	}
	fitted, err := runLimited(ctx, a.computeLimit(), func() (forecast.Fitted, error) {
		return fitRequest(in)
	})
	if err != nil {
		return err
	}
	snap, err := forecast.SnapshotOf(fitted)
	if err != nil {
		return err
	}
	return a.store.Put(ctx, row.OrgID, row.Key, snap)
}

// retrainOwner names the claim holder in forecast.retrain.claimed_by, so a
// stuck lease can be traced back to a process.
func retrainOwner() string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return host + ":" + strconv.Itoa(os.Getpid())
}
