package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/eduard-kolotushin/timeseries"
	forecast "github.com/eduard-kolotushin/timeseries-forecast"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
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
	mu       sync.Mutex
	store    SnapshotStore
	close    func()
	jsonData []byte
	secure   map[string]string
	connect  func(context.Context, string) (SnapshotStore, func())
	limit    *workLimiter
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
		ds.store, ds.close = ds.connectFn()(ctx, storeDSNFrom(ctx, settings.JSONData, settings.DecryptedSecureJSONData))
	}
	return ds, nil
}

func (d *Datasource) connectFn() func(context.Context, string) (SnapshotStore, func()) {
	if d.connect != nil {
		return d.connect
	}
	return connectStore
}

func (d *Datasource) ensureStore(ctx context.Context, pCtx backend.PluginContext) SnapshotStore {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.store != nil {
		return d.store
	}
	jsonData, secure := storeJSONFromContext(d.jsonData, d.secure, pCtx.AppInstanceSettings)
	d.store, d.close = d.connectFn()(ctx, storeDSNFrom(ctx, jsonData, secure))
	return d.store
}

func (d *Datasource) Dispose() {
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
	if in.CacheKey == "" || !cacheKeyPattern.MatchString(in.CacheKey) {
		return backend.ErrDataResponse(backend.StatusBadRequest, errInvalidCacheKey.Error())
	}
	if store == nil {
		return backend.ErrDataResponseWithSource(backend.StatusBadRequest, backend.ErrorSourcePlugin, msgStoreOff)
	}
	level := in.Level
	if kind != queryKindForecast && level == 0 {
		level = 0.95
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
	resp, err := runLimited(ctx, d.computeLimit(), func() (backend.DataResponse, error) {
		fitted, err := forecast.Restore(snap)
		if err != nil {
			return backend.ErrDataResponse(backend.StatusBadRequest, err.Error()), nil
		}
		series, err := seriesForKind(fitted, kind, from, to, level)
		if err != nil {
			return backend.ErrDataResponse(dataStatusFor(err), err.Error()), nil
		}
		return backend.DataResponse{Frames: data.Frames{frameFromSeries(q.RefID, series)}}, nil
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

func frameFromSeries(refID string, s timeseries.Series[float64]) *data.Frame {
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
		data.NewField("Value", nil, vs),
	)
	frame.Meta = &data.FrameMeta{Type: data.FrameTypeTimeSeriesWide}
	return frame
}
