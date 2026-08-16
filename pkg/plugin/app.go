package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	forecast "github.com/eduard-kolotushin/timeseries-forecast"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
)

var (
	_ backend.CallResourceHandler   = (*App)(nil)
	_ instancemgmt.InstanceDisposer = (*App)(nil)
	_ backend.CheckHealthHandler    = (*App)(nil)
)

// App is the Grafana app backend: overlay /forecast plus optional baseline publisher.
type App struct {
	backend.CallResourceHandler
	mu     sync.Mutex
	cfg    PublisherConfig
	cancel context.CancelFunc
	done   chan struct{}
	closer io.Closer
}

// NewApp creates a new *App instance.
func NewApp(_ context.Context, settings backend.AppInstanceSettings) (instancemgmt.Instance, error) {
	var app App

	mux := http.NewServeMux()
	app.registerRoutes(mux)
	app.CallResourceHandler = httpadapter.New(mux)

	if err := app.applyJSONData(settings.JSONData); err != nil {
		return nil, err
	}
	return &app, nil
}

func (a *App) applyJSONData(raw json.RawMessage) error {
	cfg, err := parseConfig(raw)
	if err != nil {
		return err
	}
	return a.applyConfig(cfg)
}

func (a *App) applyConfig(cfg PublisherConfig) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	running := a.cancel != nil
	if a.cfg.equal(cfg) && running == cfg.Enabled {
		return nil
	}
	a.stopLocked()
	a.cfg = cfg
	if !cfg.Enabled {
		return nil
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	cal, err := forecast.CalendarByName(cfg.Calendar)
	if err != nil {
		return err
	}
	src := newDruidStore(cfg.DruidBroker, cfg.DruidDatasource, nil)
	sink := newKafkaSink(cfg.KafkaBrokers, cfg.KafkaTopic)
	pub := newPublisher(cfg, src, sink, cal)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	a.cancel = cancel
	a.done = done
	a.closer = sink
	go func() {
		defer close(done)
		log.DefaultLogger.Info("baseline publisher started",
			"datasource", cfg.DruidDatasource,
			"topic", cfg.KafkaTopic,
			"interval", cfg.Interval.String(),
			"aheadMinutes", cfg.AheadMinutes,
		)
		pub.run(ctx)
	}()
	return nil
}

func (a *App) stopLocked() {
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	if a.done != nil {
		<-a.done
		a.done = nil
	}
	if a.closer != nil {
		_ = a.closer.Close()
		a.closer = nil
	}
}

// Dispose stops the baseline publisher when Grafana recreates the app instance.
func (a *App) Dispose() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopLocked()
}

// CheckHealth handles health checks sent from Grafana to the plugin.
// Grafana does not recreate the app instance when Configuration is saved unless a backend
// RPC follows; this applies the request's jsonData so aheadMinutes and the rest take effect.
func (a *App) CheckHealth(_ context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	if req != nil && req.PluginContext.AppInstanceSettings != nil {
		if err := a.applyJSONData(req.PluginContext.AppInstanceSettings.JSONData); err != nil {
			return &backend.CheckHealthResult{
				Status:  backend.HealthStatusError,
				Message: err.Error(),
			}, nil
		}
	}
	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: a.healthMessage(),
	}, nil
}

func (a *App) healthMessage() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.cfg.Enabled {
		return "publisher off"
	}
	return fmt.Sprintf("publisher on, aheadMinutes=%d, interval=%s", a.cfg.AheadMinutes, a.cfg.Interval)
}
