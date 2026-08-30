package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

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

func (d *Datasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	resp := backend.NewQueryDataResponse()
	orgID := req.PluginContext.OrgID
	store := d.ensureStore(ctx, req.PluginContext)
	for _, q := range req.Queries {
		resp.Responses[q.RefID] = d.queryOne(ctx, store, orgID, q)
	}
	return resp, nil
}

func (d *Datasource) queryOne(ctx context.Context, store SnapshotStore, orgID int64, q backend.DataQuery) backend.DataResponse {
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
	snap, ok, err := store.Get(ctx, orgID, in.CacheKey)
	if err != nil {
		return backend.ErrDataResponse(backend.StatusInternal, err.Error())
	}
	if !ok {
		return backend.ErrDataResponseWithSource(backend.StatusBadRequest, backend.ErrorSourcePlugin, msgNeedTrain)
	}
	fitted, err := forecast.Restore(snap)
	if err != nil {
		return backend.ErrDataResponse(backend.StatusBadRequest, err.Error())
	}
	level := in.Level
	if kind != queryKindForecast && level == 0 {
		level = 0.95
	}
	out, err := emitForecast(fitted, ForecastRequest{
		From:  q.TimeRange.From.UTC().UnixMilli(),
		To:    q.TimeRange.To.UTC().UnixMilli(),
		Level: level,
	})
	if err != nil {
		st := backend.StatusInternal
		if httpStatusFor(err) == 400 {
			st = backend.StatusBadRequest
		}
		return backend.ErrDataResponse(st, err.Error())
	}
	values, err := seriesForKind(out, kind)
	if err != nil {
		return backend.ErrDataResponse(backend.StatusBadRequest, err.Error())
	}
	frame, err := frameFromSeries(q.RefID, out.Times, values)
	if err != nil {
		return backend.ErrDataResponse(backend.StatusInternal, err.Error())
	}
	return backend.DataResponse{Frames: data.Frames{frame}}
}

func seriesForKind(out ForecastResponse, kind string) ([]nullableFloat, error) {
	switch kind {
	case queryKindForecast:
		return out.Values, nil
	case queryKindLower:
		if len(out.Lower) == 0 {
			return nil, fmt.Errorf("forecast: no lower series")
		}
		return out.Lower, nil
	case queryKindUpper:
		if len(out.Upper) == 0 {
			return nil, fmt.Errorf("forecast: no upper series")
		}
		return out.Upper, nil
	default:
		return nil, fmt.Errorf("%w: %s", errUnknownKind, kind)
	}
}

func frameFromSeries(refID string, times []int64, values []nullableFloat) (*data.Frame, error) {
	if len(times) != len(values) {
		return nil, fmt.Errorf("forecast: times and values length mismatch")
	}
	ts := make([]time.Time, len(times))
	vs := make([]*float64, len(values))
	for i := range times {
		ts[i] = time.UnixMilli(times[i]).UTC()
		v := float64(values[i])
		if math.IsNaN(v) {
			continue
		}
		x := v
		vs[i] = &x
	}
	frame := data.NewFrame(refID,
		data.NewField("Time", nil, ts),
		data.NewField("Value", nil, vs),
	)
	frame.Meta = &data.FrameMeta{Type: data.FrameTypeTimeSeriesWide}
	return frame, nil
}
