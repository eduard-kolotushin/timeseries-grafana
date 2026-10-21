package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/eduard-kolotushin/timeseries"
	forecast "github.com/eduard-kolotushin/timeseries-forecast"
	"github.com/eduard-kolotushin/timeseries-grafana/pkg/vault"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

const (
	queryKindForecast = "forecast"
	queryKindLower    = "lower"
	queryKindUpper    = "upper"
	msgNeedTrain      = "needTrain: train on the Forecast overlay panel first"
	msgStoreOff       = "snapshot store not configured (needTrain)"
)

var errUnknownKind = errors.New("forecast: unknown query kind")

type Datasource struct {
	mu          sync.Mutex
	store       SnapshotStore
	close       func()
	vaultCancel func()
	jsonData    []byte
	secure      map[string]string
	connect     func(context.Context, string, storeTLSConfig) (SnapshotStore, func())
	limit       *workLimiter
}

var (
	_ backend.QueryDataHandler      = (*Datasource)(nil)
	_ backend.CheckHealthHandler    = (*Datasource)(nil)
	_ instancemgmt.InstanceDisposer = (*Datasource)(nil)
)

// NewDatasource creates a QueryData handler that Restores fitted snapshots.
func NewDatasource(ctx context.Context, settings backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	return newDatasource(ctx, settings, nil)
}

func newDatasource(ctx context.Context, settings backend.DataSourceInstanceSettings, store SnapshotStore) (*Datasource, error) {
	ds := &Datasource{
		store:    store,
		jsonData: settings.JSONData,
		secure:   settings.DecryptedSecureJSONData,
		limit:    newWorkLimiter(maxInflightFrom(ctx, settings.JSONData)),
	}
	if store == nil {
		tlsCfg, cancel := ds.storeTLS(ctx, settings.JSONData, settings.DecryptedSecureJSONData)
		ds.vaultCancel = cancel
		ds.store, ds.close = ds.connectFn()(ctx, storeDSNFrom(ctx, settings.JSONData, settings.DecryptedSecureJSONData), tlsCfg)
	}
	return ds, nil
}

func (d *Datasource) connectFn() func(context.Context, string, storeTLSConfig) (SnapshotStore, func()) {
	if d.connect != nil {
		return d.connect
	}
	return connectStore
}

// storeTLS resolves the same Vault settings the DSN uses and selects the store's
// certificate source: a Vault manager when the store role is configured,
// otherwise local files. With mTLS on and neither source the returned config is
// enabled but empty, so opening the pool reports the missing source instead of
// silently connecting in plaintext.
func (d *Datasource) storeTLS(ctx context.Context, jsonData []byte, secure map[string]string) (storeTLSConfig, func()) {
	jd := map[string]any{}
	if len(jsonData) > 0 {
		_ = json.Unmarshal(jsonData, &jd)
	}
	look := storeLookup{
		getenv: os.Getenv,
		cfg:    backend.GrafanaConfigFromContext(ctx),
		json:   jd,
	}
	vc, err := vaultFromLookup(look, backend.AppInstanceSettings{JSONData: jsonData, DecryptedSecureJSONData: secure})
	if err != nil {
		// vaultFromLookup keeps the partial view on error: the switches and file
		// paths are set, so a store that asked for mTLS below is enabled with no
		// usable source and the pool refuses it instead of connecting in plaintext.
		log.DefaultLogger.Error("forecast datasource: vault configuration refused", "err", err.Error())
	}
	if vc == nil || !vc.StoreMTLS {
		return storeTLSConfig{}, nil
	}
	tlsCfg := storeTLSConfig{
		Enabled:    true,
		Files:      vc.StoreFiles,
		ServerName: vc.StoreServerName,
	}
	switch {
	case vc.StoreRole != "" && vc.Client != nil:
		mgrCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		mgr := vault.NewManager(vc.Client, vc.PKIMount, vc.StoreRole, vc.StoreCN, vc.CertTTL)
		if err := mgr.Start(mgrCtx); err != nil {
			log.DefaultLogger.Error("forecast datasource: vault store issue failed", "err", err.Error())
		}
		tlsCfg.Manager = mgr
		return tlsCfg, cancel
	case vc.StoreFiles.ClientConfigured():
		return tlsCfg, nil
	default:
		log.DefaultLogger.Error("forecast datasource: store mTLS has no certificate source", "err", "FORECAST_VAULT_PKI_STORE_ROLE or FORECAST_STORE_CLIENT_CERT/KEY is required with FORECAST_STORE_MTLS=true")
		return tlsCfg, nil
	}
}

func (d *Datasource) ensureStore(ctx context.Context, pCtx backend.PluginContext) SnapshotStore {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.store != nil {
		return d.store
	}
	jsonData, secure := storeJSONFromContext(d.jsonData, d.secure, pCtx.AppInstanceSettings)
	tlsCfg, cancel := d.storeTLS(ctx, jsonData, secure)
	d.vaultCancel = cancel
	d.store, d.close = d.connectFn()(ctx, storeDSNFrom(ctx, jsonData, secure), tlsCfg)
	return d.store
}

func (d *Datasource) Dispose() {
	// ensureStore assigns store and close under d.mu, so the handle has to be
	// read under it too: Dispose can run while a query is still connecting.
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.vaultCancel != nil {
		d.vaultCancel()
	}
	if d.close != nil {
		d.close()
	}
}

func (d *Datasource) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	var pCtx backend.PluginContext
	if req != nil {
		pCtx = req.PluginContext
	}
	store := d.ensureStore(ctx, pCtx)
	if store == nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: msgStoreOff,
		}, nil
	}
	orgID := pCtx.OrgID
	_, _, err := store.Get(ctx, orgID, "0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: err.Error(),
		}, nil
	}
	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: "ok",
	}, nil
}

type forecastQueryJSON struct {
	Kind     string  `json:"kind"`
	CacheKey string  `json:"cacheKey"`
	Level    float64 `json:"level"`
	// SeriesLabels names the series the answer stands for, so a rule that also
	// queries the metric can pair this baseline with it by label instead of
	// broadcasting to every series the metric query returned.
	SeriesLabels map[string]string `json:"seriesLabels"`
}

// computeLimit returns the per-instance limiter, creating the default one once
// for instances built without newDatasource (tests). It never hands out a
// fresh, unshared limiter per call, which would silently disable the bound.
func (d *Datasource) computeLimit() *workLimiter {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.limit == nil {
		d.limit = newWorkLimiter(defaultMaxInflight)
	}
	return d.limit
}

func (d *Datasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (resp *backend.QueryDataResponse, err error) {
	resp = backend.NewQueryDataResponse()
	defer func() {
		if rec := recover(); rec != nil {
			for _, q := range req.Queries {
				if _, ok := resp.Responses[q.RefID]; !ok {
					resp.Responses[q.RefID] = backend.ErrDataResponse(backend.StatusInternal, "forecast: panic")
				}
			}
		}
	}()
	orgID := req.PluginContext.OrgID
	store := d.ensureStore(ctx, req.PluginContext)
	for _, q := range req.Queries {
		resp.Responses[q.RefID] = d.queryOne(ctx, store, orgID, q)
	}
	return resp, nil
}

func (d *Datasource) queryOne(ctx context.Context, store SnapshotStore, orgID int64, q backend.DataQuery) (out backend.DataResponse) {
	defer func() {
		if rec := recover(); rec != nil {
			out = backend.ErrDataResponse(backend.StatusInternal, "forecast: panic")
		}
	}()
	if len(q.JSON) > maxQueryJSONBytes {
		return backend.ErrDataResponse(backend.Status(http.StatusRequestEntityTooLarge), errBodyTooLarge.Error())
	}
	var in forecastQueryJSON
	if err := json.Unmarshal(q.JSON, &in); err != nil {
		return backend.ErrDataResponse(backend.StatusBadRequest, err.Error())
	}
	kind := in.Kind
	if kind == "" {
		kind = queryKindForecast
	}
	if kind != queryKindForecast && kind != queryKindLower && kind != queryKindUpper {
		return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("%v: %s", errUnknownKind, kind))
	}
	// The query editor writes cacheKey asynchronously, so a just-added row arrives
	// with no key at all. That is not a validation error: answer it exactly like a
	// miss, so the panel shows the same "train on the overlay first" reason. A key
	// that is present but malformed is still a validation error.
	if in.CacheKey == "" {
		return backend.ErrDataResponseWithSource(backend.StatusBadRequest, backend.ErrorSourcePlugin, msgNeedTrain)
	}
	if !cacheKeyPattern.MatchString(in.CacheKey) {
		return backend.ErrDataResponse(backend.StatusBadRequest, errInvalidCacheKey.Error())
	}
	if store == nil {
		return backend.ErrDataResponseWithSource(backend.StatusBadRequest, backend.ErrorSourcePlugin, msgStoreOff)
	}
	level := in.Level
	if kind != queryKindForecast && level == 0 {
		level = 0.95
	}
	// nil unless the query names a series, so an unnamed restore emits exactly
	// the unlabelled frame it always did.
	var labels data.Labels
	if len(in.SeriesLabels) > 0 {
		labels = data.Labels(in.SeriesLabels)
	}
	// Store I/O stays outside the inflight limiter so a slow Postgres does not
	// hold compute slots; the limiter bounds Restore + ForecastRange only.
	snap, ok, err := store.Get(ctx, orgID, in.CacheKey)
	if err != nil {
		return backend.ErrDataResponse(backend.StatusInternal, err.Error())
	}
	if !ok {
		return backend.ErrDataResponseWithSource(backend.StatusBadRequest, backend.ErrorSourcePlugin, msgNeedTrain)
	}
	from := q.TimeRange.From.UTC()
	to := q.TimeRange.To.UTC()
	// The snapshot carries the fitted grid, so a window that would emit too many
	// points is refused here — as a 413-class reason, not a 500 — before Restore.
	if err := checkForecastWindow(snap.Last, snap.Step/int64(time.Millisecond), from.UnixMilli(), to.UnixMilli()); err != nil {
		return backend.ErrDataResponse(dataStatusFor(err), err.Error())
	}
	resp, err := runLimited(ctx, d.computeLimit(), func() (backend.DataResponse, error) {
		fitted, err := forecast.Restore(snap)
		if err != nil {
			return backend.ErrDataResponse(backend.StatusBadRequest, err.Error()), nil
		}
		series, err := seriesForKind(fitted, kind, from, to, level)
		if err != nil {
			return backend.ErrDataResponse(dataStatusFor(err), err.Error()), nil
		}
		return backend.DataResponse{Frames: data.Frames{frameFromSeries(q.RefID, series, labels)}}, nil
	})
	if err != nil {
		return backend.ErrDataResponse(dataStatusFor(err), err.Error())
	}
	return resp
}

// seriesForKind runs only the range call the kind needs: ForecastRange for the
// point forecast, ForecastIntervalRange for a bound.
func seriesForKind(fitted forecast.Fitted, kind string, from, to time.Time, level float64) (timeseries.Series[float64], error) {
	switch kind {
	case queryKindForecast:
		return fitted.ForecastRange(from, to)
	case queryKindLower, queryKindUpper:
		lower, upper, err := fitted.ForecastIntervalRange(from, to, level)
		if err != nil {
			return timeseries.Series[float64]{}, err
		}
		if kind == queryKindLower {
			return lower, nil
		}
		return upper, nil
	default:
		return timeseries.Series[float64]{}, fmt.Errorf("%w: %s", errUnknownKind, kind)
	}
}

func frameFromSeries(refID string, s timeseries.Series[float64], labels data.Labels) *data.Frame {
	ts := s.Times()
	src := s.Values()
	times := make([]time.Time, len(ts))
	vs := make([]*float64, len(src))
	for i := range ts {
		times[i] = ts[i].UTC()
		if math.IsNaN(src[i]) {
			continue
		}
		x := src[i]
		vs[i] = &x
	}
	frame := data.NewFrame(refID,
		data.NewField("Time", nil, times),
		data.NewField("Value", labels, vs),
	)
	frame.Meta = &data.FrameMeta{Type: data.FrameTypeTimeSeriesWide}
	return frame
}
