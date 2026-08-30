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

// App is the Grafana app backend: overlay /forecast.
type App struct {
	backend.CallResourceHandler
	store SnapshotStore
	close func()
}

// NewApp creates a new *App instance.
func NewApp(ctx context.Context, settings backend.AppInstanceSettings) (instancemgmt.Instance, error) {
	return newApp(ctx, settings, nil)
}

func newApp(ctx context.Context, settings backend.AppInstanceSettings, store SnapshotStore) (*App, error) {
	app := &App{store: store}
	if store == nil {
		app.store, app.close = connectStore(ctx, storeDSN(ctx, settings))
	}
	mux := http.NewServeMux()
	app.registerRoutes(mux)
	app.CallResourceHandler = httpadapter.New(mux)
	return app, nil
}

// Dispose closes the snapshot store pool when present.
func (a *App) Dispose() {
	if a.close != nil {
		a.close()
	}
}

// CheckHealth handles health checks sent from Grafana to the plugin.
func (a *App) CheckHealth(_ context.Context, _ *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: "ok",
	}, nil
}
