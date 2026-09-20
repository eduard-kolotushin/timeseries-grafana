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
	"sort"
	"strconv"
	"strings"
	"sync"
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

	// retrainAuthFailures is how many consecutive ticks Grafana may refuse this
	// plugin's credentials before the ticker ends itself. A wrong
	// FORECAST_GRAFANA_URL or token is a configuration error that retrying
	// cannot fix, and the overlay's needTrain path still retrains the rows.
	retrainAuthFailures = 3
)

// errGrafanaUnauthorized marks a /api/ds/query reply of 401/403: the scheduler's
// own credentials are wrong, not the panel's query. runScheduler counts these to
// decide whether unattended retraining can work at all.
var errGrafanaUnauthorized = errors.New("forecast: grafana refused the scheduler credentials (401/403)")

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
	Model    string  `json:"model"`
	Alpha    float64 `json:"alpha,omitempty"`
	Beta     float64 `json:"beta,omitempty"`
	Period   int     `json:"period,omitempty"`
	Season   string  `json:"season"`
	Calendar string  `json:"calendar"`
	Lookback string  `json:"lookback"`
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
	// orgID is the Grafana org this process's credential belongs to, and the only
	// one its queries can resolve datasources in.
	orgID(ctx context.Context) (int64, error)
}

// grafanaPoster posts the stored query objects to Grafana's own query API. It
// never inspects them: pkg/ carries no datasource-specific field or type name,
// and the type-keyed rewrite stays in the overlay frontend.
type grafanaPoster struct {
	url    string
	token  string
	client *http.Client
	// now is the claim-time clock a relative train window is resolved against.
	// NewGrafanaPoster leaves it nil and clock() falls back to time.Now.
	now func() time.Time
	// mu guards the resolved org. The credential does not change under a running
	// plugin, so it is looked up once instead of once per tick.
	mu  sync.Mutex
	org int64
}

func newGrafanaPoster(cfg retrainConfig) *grafanaPoster {
	return &grafanaPoster{
		url:    strings.TrimRight(cfg.GrafanaURL, "/"),
		token:  cfg.Token,
		client: &http.Client{Timeout: frameFetchTimeout},
	}
}

func (p *grafanaPoster) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// orgID reports the org of the credential this scheduler posts with, read from
// Grafana's own /api/org. A datasourceUid is resolved inside the requesting org, so
// this is what stops a retrain of one org's row from being fetched (or failing) as
// another's: panelClaimSQL claims only this org's rows.
func (p *grafanaPoster) orgID(ctx context.Context) (int64, error) {
	p.mu.Lock()
	cached := p.org
	p.mu.Unlock()
	if cached != 0 {
		return cached, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url+"/api/org", nil)
	if err != nil {
		return 0, err
	}
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxDSQueryReplyBytes))
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return 0, fmt.Errorf("%w: /api/org status %d", errGrafanaUnauthorized, resp.StatusCode)
		}
		return 0, fmt.Errorf("forecast: /api/org status %d", resp.StatusCode)
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, fmt.Errorf("forecast: /api/org: %w", err)
	}
	if out.ID == 0 {
		return 0, errors.New("forecast: /api/org returned no org id")
	}
	p.mu.Lock()
	p.org = out.ID
	p.mu.Unlock()
	return out.ID, nil
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
	from, to, kind := spec.From, spec.To, "stored"
	if spec.Relative && spec.LookbackMs > 0 {
		// The browser trained on a lookback it resolved against the panel's own
		// now, so this retrain resolves the same lookback against the claim
		// clock: replaying the stored absolute pair would refetch the identical
		// range every cron tick and republish the same model forever.
		now := p.clock().UTC().UnixMilli()
		from, to, kind = now-spec.LookbackMs, now, "relative"
	}
	log.DefaultLogger.Debug("retrain train window", "key", row.Key, "window", kind, "from", from, "to", to)
	body, err := json.Marshal(struct {
		Queries json.RawMessage `json:"queries"`
		From    string          `json:"from"`
		To      string          `json:"to"`
	}{
		Queries: spec.Queries,
		From:    strconv.FormatInt(from, 10),
		To:      strconv.FormatInt(to, 10),
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
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("%w: status %d: %s", errGrafanaUnauthorized, resp.StatusCode, detail)
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
	timeField, valueField, err := pickSeries(frames, spec.SeriesName)
	if err != nil {
		return timeseries.Series[float64]{}, err
	}
	// Cap before allocating anything: the reply is bounded only by
	// maxDSQueryReplyBytes, so a misconfigured datasource could otherwise ask for
	// millions of time.Time / float64 slots before the length was ever checked.
	if err := checkTrainLen(timeField.Len(), valueField.Len()); err != nil {
		return timeseries.Series[float64]{}, err
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

// pickSeries resolves the time and value fields to fit. A spec that names a
// series accepts that series' field name or its Grafana display name; an unnamed
// spec (the worker's rows do not use this path) takes the first frame carrying
// both fields.
//
// A name the reply does not carry is only fatal when the reply offers a choice:
// the browser's display name and this process's can disagree (a datasource
// version, a different frame set), and a reply with exactly one candidate is
// unambiguous, so accepting it keeps a labeled panel retraining. Two candidates
// must fail — fitting the wrong one would overwrite the snapshot the panel's
// cache key points at, with last_status 'ok'.
func pickSeries(frames data.Frames, name string) (timeField, valueField *data.Field, err error) {
	var (
		anyTime, anyValue     *data.Field
		namedTime, namedValue *data.Field
		candidates            int
	)
	for _, fr := range frames {
		if fr == nil {
			continue
		}
		t, numeric := frameFields(fr)
		if t == nil {
			continue
		}
		for _, f := range numeric {
			candidates++
			if anyValue == nil {
				anyTime, anyValue = t, f
			}
			if namedValue == nil && name != "" && (f.Name == name || fieldDisplayName(f, fr, frames) == name) {
				namedTime, namedValue = t, f
			}
		}
	}
	switch {
	case namedValue != nil:
		return namedTime, namedValue, nil
	case name == "" && anyValue != nil:
		return anyTime, anyValue, nil
	case name != "" && candidates == 1:
		return anyTime, anyValue, nil
	case name != "":
		return nil, nil, fmt.Errorf("forecast: /api/ds/query reply has no series named %q", name)
	default:
		return nil, nil, errors.New("forecast: no time series in /api/ds/query reply")
	}
}

// timeSeriesValueFieldName and timeSeriesTimeFieldName are the names
// fieldDisplayName special-cases, mirroring @grafana/data's
// TIME_SERIES_VALUE_FIELD_NAME and TIME_SERIES_TIME_FIELD_NAME. The SDK exports
// the value name (data.TimeSeriesValueFieldName); "Time" has no Go constant.
const (
	timeSeriesValueFieldName = data.TimeSeriesValueFieldName
	timeSeriesTimeFieldName  = "Time"
)

// fieldDisplayName is a faithful Go mirror of @grafana/data's
// getFieldDisplayName(field, frame, allFrames) — the name the browser used when
// it trained, which is what a stored trainSource.seriesName holds. Frames decoded
// from /api/ds/query carry no display-name state, which is exactly the browser's
// state on its first call for each field, so this always computes: the
// config.displayName override, then config.displayNameFromDS, then a time field
// without labels, then the frame-name / field-name / label parts, then the
// duplicate-name index.
//
// The one preference it cannot mirror is @grafana/data's " (comparison)" suffix:
// that comes from frame.meta.timeCompare.isTimeShiftQuery, and the Go SDK's
// data.FrameMeta does not expose a timeCompare field, so a time-compare reply is
// named here without it. Overlay training frames never carry one.
func fieldDisplayName(f *data.Field, fr *data.Frame, allFrames data.Frames) string {
	if f.Config != nil && f.Config.DisplayName != "" {
		return f.Config.DisplayName
	}
	if fr != nil && f.Config != nil && f.Config.DisplayNameFromDS != "" {
		return f.Config.DisplayNameFromDS
	}
	if f.Type().Time() && len(f.Labels) == 0 {
		// A time field is a name, not a series: never a frame or label part.
		if f.Name != "" {
			return f.Name
		}
		return timeSeriesTimeFieldName
	}
	parts := make([]string, 0, 3)
	frameNameAdded, labelsAdded := false, false
	if fr != nil && fr.Name != "" && frameNamesDiffer(allFrames) {
		parts = append(parts, fr.Name)
		frameNameAdded = true
	}
	if f.Name != "" && f.Name != timeSeriesValueFieldName {
		parts = append(parts, f.Name)
	}
	if len(f.Labels) > 0 && fr != nil {
		frames := allFrames
		if len(frames) == 0 {
			frames = data.Frames{fr}
		}
		if key := singleLabelName(frames); key == "" {
			if all := formatLabels(f.Labels); all != "" {
				parts = append(parts, all)
				labelsAdded = true
			}
		} else if v := f.Labels[key]; v != "" {
			parts = append(parts, v)
			labelsAdded = true
		}
	}
	if fr != nil && !frameNameAdded && !labelsAdded && f.Name == timeSeriesValueFieldName && fr.Name != "" {
		// A bare "Value" column has nothing of its own to be called after.
		parts = append(parts, fr.Name)
	}
	var displayName string
	switch {
	case len(parts) > 0:
		displayName = strings.Join(parts, " ")
	case f.Name != "":
		displayName = f.Name
	default:
		displayName = timeSeriesValueFieldName
	}
	if displayName == f.Name {
		return uniqueFieldName(f, fr)
	}
	return displayName
}

// frameNamesDiffer reports whether the reply's frames are named differently,
// which is when the frame name is needed to tell their series apart.
func frameNamesDiffer(frames data.Frames) bool {
	for i := 1; i < len(frames); i++ {
		prev, cur := frames[i-1], frames[i]
		if prev != nil && cur != nil && prev.Name != cur.Name {
			return true
		}
	}
	return false
}

// uniqueFieldName mirrors @grafana/data's getUniqueFieldName: the index Grafana
// appends when two fields of one frame share a name ("value 1", "value 2").
func uniqueFieldName(f *data.Field, fr *data.Frame) string {
	dupes := 0
	foundSelf := false
	if fr != nil {
		for _, other := range fr.Fields {
			if other == nil {
				continue
			}
			if f == other {
				foundSelf = true
				if dupes > 0 {
					dupes++
					break
				}
				continue
			}
			if f.Name == other.Name {
				dupes++
				if foundSelf {
					break
				}
			}
		}
	}
	if dupes > 0 {
		return f.Name + " " + strconv.Itoa(dupes)
	}
	return f.Name
}

// singleLabelName mirrors @grafana/data's getSingleLabelName: the one label key
// every labeled field across all frames shares, or "" when there is no such key
// (including when no field carries labels).
func singleLabelName(frames data.Frames) string {
	single := ""
	for _, fr := range frames {
		if fr == nil {
			continue
		}
		for _, f := range fr.Fields {
			if f == nil {
				continue
			}
			for key := range f.Labels {
				if single == "" {
					single = key
					continue
				}
				if key != single {
					return ""
				}
			}
		}
	}
	return single
}

// formatLabels mirrors @grafana/data's formatLabels: keys sorted, `{k="v"}`.
func formatLabels(labels data.Labels) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(k)
		b.WriteString(`="`)
		b.WriteString(labels[k])
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// frameFields splits a frame into its time field and its numeric fields. A frame
// without a time field carries no series to fit: its numbers are statistics, not
// a time series, and must never be fitted.
func frameFields(fr *data.Frame) (timeField *data.Field, numeric []*data.Field) {
	numeric = make([]*data.Field, 0, len(fr.Fields))
	for _, f := range fr.Fields {
		if f == nil {
			continue
		}
		switch {
		case timeField == nil && f.Type().Time():
			timeField = f
		case f.Type().Numeric():
			numeric = append(numeric, f)
		}
	}
	return timeField, numeric
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

// tickResult is what one tick of the scheduler observed. It is what lets
// runScheduler tell "Grafana refuses us" from "nothing was due".
type tickResult struct {
	// fetched is true when a row's /api/ds/query call got past the auth wall:
	// the configured URL and token can produce training data.
	fetched bool
	// denied is true when such a call came back 401/403.
	denied bool
}

func (r tickResult) or(o tickResult) tickResult {
	return tickResult{fetched: r.fetched || o.fetched, denied: r.denied || o.denied}
}

// tickFromErr classifies one retrain attempt for the ticker. Only an auth
// refusal means the scheduler cannot work at all; every other outcome (including
// a non-auth fetch error) proves the request reached Grafana.
func tickFromErr(err error) tickResult {
	if errors.Is(err, errGrafanaUnauthorized) {
		return tickResult{denied: true}
	}
	return tickResult{fetched: true}
}

// authGuard counts consecutive ticks in which Grafana refused this plugin's
// credentials. Only the scheduler's own calls count: a tick that got past the
// fetch clears the count, and a tick with nothing due is no evidence either way.
type authGuard struct{ denied int }

// watch folds one tick in and reports whether the scheduler should end. A tick
// that got past the fetch proves the credentials work, so it resets the count —
// otherwise three refusals spread over a week would disable a healthy scheduler.
func (g *authGuard) watch(tick tickResult) bool {
	switch {
	case tick.fetched:
		g.denied = 0
	case tick.denied:
		g.denied++
		return g.denied >= retrainAuthFailures
	}
	return false
}

// runScheduler retrains due panel rows on a ticker, with no browser attached.
// Claiming is fleet-wide and lease-based, so every Grafana replica may run this
// without coordinating: SKIP LOCKED hands each due row to exactly one process.
//
// Three consecutive ticks that Grafana refuses with 401/403 end the ticker: an
// unusable FORECAST_GRAFANA_URL / FORECAST_GRAFANA_TOKEN is a configuration
// error that no amount of retrying fixes, and logging it every tick forever
// would be noise. The overlay's needTrain path keeps retraining the rows either
// way.
func (a *App) runScheduler(ctx context.Context) {
	ticker := time.NewTicker(a.retrain.Tick)
	defer ticker.Stop()
	var guard authGuard
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !guard.watch(a.retrainDue(ctx)) {
				continue
			}
			log.DefaultLogger.Error(
				"forecast: scheduler disabled, Grafana refused its credentials",
				"failures", guard.denied,
				"hint", "configure FORECAST_GRAFANA_URL and FORECAST_GRAFANA_TOKEN, or set FORECAST_RETRAIN_ENABLED=false",
			)
			return
		}
	}
}

// retrainDue claims this process's share of the due rows and retrains them. The
// owner is resolved once and used for the claim and for every finish, so a
// lease that expired while a row was being retrained cannot release the claim a
// newer owner holds.
func (a *App) retrainDue(ctx context.Context) tickResult {
	owner := retrainOwner()
	// One credential, one org: Grafana resolves the stored queries' datasourceUids
	// inside the org the credential belongs to, so a claim that reached another
	// org's row would fetch that row's frames as this org's series — or fail — and
	// then store the result under the other org's key. Only this org's rows are
	// claimed; another org's row stays due and is refreshed by the overlay's
	// needTrain path, which runs with that org's own request context.
	org, err := a.poster.orgID(ctx)
	if err != nil {
		log.DefaultLogger.Error("retrain org", "err", err.Error())
		return tickFromErr(err)
	}
	rows, err := a.sched.Claim(ctx, org, owner, a.retrain.Lease, retrainClaimBatch)
	if err != nil {
		log.DefaultLogger.Error("retrain claim", "err", err.Error())
		return tickResult{}
	}
	var out tickResult
	for _, row := range rows {
		if ctx.Err() != nil {
			return out
		}
		out = out.or(a.retrainOne(ctx, owner, row))
	}
	return out
}

func (a *App) retrainOne(ctx context.Context, owner string, row ScheduleRow) tickResult {
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
	if ferr := a.sched.Finish(ctx, owner, row.OrgID, row.Scope, row.Key, next, status); ferr != nil {
		log.DefaultLogger.Error("retrain finish", "scope", row.Scope, "key", row.Key, "status", status, "err", ferr.Error())
	} else {
		log.DefaultLogger.Info("retrain", "scope", row.Scope, "key", row.Key, "status", status, "dur", time.Since(started).Round(time.Millisecond).String())
	}
	return tickFromErr(err)
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
	ts := series.Times()
	vs := series.Values()
	in := ForecastRequest{
		Times:    make([]int64, len(ts)),
		Values:   make([]nullableFloat, len(vs)),
		Model:    spec.Model,
		Alpha:    spec.Alpha,
		Beta:     spec.Beta,
		Period:   spec.Period,
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
