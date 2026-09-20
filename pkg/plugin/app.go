package plugin

import (
	"context"
	"net/http"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
)

var (
	_ backend.CallResourceHandler   = (*App)(nil)
	_ instancemgmt.InstanceDisposer = (*App)(nil)
	_ backend.CheckHealthHandler    = (*App)(nil)
)

// App is the Grafana app backend: overlay /forecast and the retrain schedules.
type App struct {
	backend.CallResourceHandler
	store   SnapshotStore
	sched   ScheduleStore
	poster  framePoster
	retrain retrainConfig
	close   func()
	cancel  func()
	limit   *workLimiter
	maxBody int64
}

// NewApp creates a new *App instance.
func NewApp(ctx context.Context, settings backend.AppInstanceSettings) (instancemgmt.Instance, error) {
	return newApp(ctx, settings, nil, nil, nil)
}

func newApp(ctx context.Context, settings backend.AppInstanceSettings, store SnapshotStore, sched ScheduleStore, poster framePoster) (*App, error) {
	app := &App{
		store:   store,
		sched:   sched,
		poster:  poster,
		retrain: computeRetrain(ctx, settings),
		limit:   newWorkLimiter(maxInflightFrom(ctx, settings.JSONData)),
		maxBody: maxForecastBodyBytes,
	}
	if store == nil {
		app.store, app.sched, app.close = connectStores(ctx, storeDSN(ctx, settings))
	}
	if app.poster == nil {
		app.poster = newGrafanaPoster(app.retrain)
	}
	// Only an app with a schedule table, a poster and retraining enabled runs the
	// unattended ticker; the overlay's own Retrain remains the path without it.
	//
	// The ticker must outlive the request that happens to create this instance —
	// instancemgmt builds an instance from the first RPC's context, which is
	// cancelled the moment that request finishes — so it gets a context detached
	// from that one and stopped by Dispose instead.
	if app.sched != nil && app.poster != nil && app.retrain.Enabled {
		schedCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		app.cancel = cancel
		go app.runScheduler(schedCtx)
	}
	mux := http.NewServeMux()
	app.registerRoutes(mux)
	app.CallResourceHandler = httpadapter.New(mux)
	return app, nil
}

// Dispose stops the retrain scheduler and closes the snapshot store pool.
func (a *App) Dispose() {
	if a.cancel != nil {
		a.cancel()
	}
	if a.close != nil {
		a.close()
	}
}

func (a *App) computeLimit() *workLimiter {
	return a.limit
}

func (a *App) bodyLimit() int64 {
	if a.maxBody > 0 {
		return a.maxBody
	}
	return maxForecastBodyBytes
}

// CheckHealth handles health checks sent from Grafana to the plugin.
func (a *App) CheckHealth(_ context.Context, _ *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: "ok",
	}, nil
}
