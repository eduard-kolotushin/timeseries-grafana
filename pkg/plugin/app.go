package plugin

import (
	"context"
	"net/http"

	"github.com/eduard-kolotushin/timeseries-grafana/pkg/vault"
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

// App is the Grafana app backend: overlay /forecast and the retrain schedules.
type App struct {
	backend.CallResourceHandler
	// compute is nil in inline mode (this process fits) and set in remote mode
	// (this process forwards /forecast to the compute service and runs no ticker).
	compute *computeMode
	store   SnapshotStore
	sched   ScheduleStore
	poster  framePoster
	retrain retrainConfig
	close   func()
	cancel  func()
	// vaultCancels stop the per-component certificate managers (PVV, store).
	vaultCancels []func()
	limit        *workLimiter
	maxBody      int64
}

// NewApp creates a new *App instance.
func NewApp(ctx context.Context, settings backend.AppInstanceSettings) (instancemgmt.Instance, error) {
	return newApp(ctx, settings, nil, nil, nil)
}

func newApp(ctx context.Context, settings backend.AppInstanceSettings, store SnapshotStore, sched ScheduleStore, poster framePoster) (*App, error) {
	return newAppWith(ctx, settings, store, sched, poster, computeFrom(ctx, settings))
}

// newAppWith is newApp with the compute mode given: the standalone compute service
// builds the same App with a nil mode, so it can never forward to another compute
// service, while every Grafana-managed caller lets newApp resolve the mode from
// settings.
func newAppWith(ctx context.Context, settings backend.AppInstanceSettings, store SnapshotStore, sched ScheduleStore, poster framePoster, compute *computeMode) (*App, error) {
	// A forwarding slot is a socket and an upstream request, not a fit, so remote
	// mode gets a wider default than the inline process whose slots bound fitting
	// CPU. FORECAST_MAX_INFLIGHT overrides either one.
	fallback := defaultMaxInflight
	mode := "inline"
	url := ""
	if compute != nil {
		fallback = defaultMaxProxyInflight
		mode = "remote"
		url = compute.url
	}
	app := &App{
		compute: compute,
		store:   store,
		sched:   sched,
		poster:  poster,
		retrain: computeRetrain(ctx, settings),
		limit:   newWorkLimiter(maxInflightOr(ctx, settings.JSONData, fallback)),
		maxBody: maxForecastBodyBytes,
	}
	log.DefaultLogger.Info("forecast compute mode", "mode", mode, "url", url)
	// Vault is resolved before the store is opened: the store's pgx TLS config
	// needs a signed client certificate, and each component's manager is
	// independent so one switch can be off while another is on.
	vc, verr := vaultFrom(ctx, settings)
	if verr != nil {
		log.DefaultLogger.Error("forecast: vault configuration refused", "err", verr.Error())
	}
	var storeMgr *vault.Manager
	if vc != nil && vc.StoreMTLS {
		if vc.StoreRole == "" {
			log.DefaultLogger.Error("forecast: vault store mTLS needs a role", "err", "FORECAST_VAULT_PKI_STORE_ROLE is required with FORECAST_STORE_MTLS=true")
		} else {
			mgrCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
			storeMgr = vault.NewManager(vc.Client, vc.PKIMount, vc.StoreRole, vc.StoreCN, vc.CertTTL)
			if err := storeMgr.Start(mgrCtx); err != nil {
				// The manager retries in the background; until it succeeds the pool
				// reports ErrNoCertificate and health stays red.
				log.DefaultLogger.Error("forecast: vault store issue failed", "err", err.Error())
			}
			app.vaultCancels = append(app.vaultCancels, cancel)
		}
	}
	if store == nil {
		app.store, app.sched, app.close = connectStores(ctx, storeDSN(ctx, settings), storeMgr)
	}
	if vc != nil && vc.PVVMTLS && app.compute == nil {
		if vc.PVVRole == "" {
			log.DefaultLogger.Error("forecast: vault pvv mTLS needs a role", "err", "FORECAST_VAULT_PKI_PVV_ROLE is required with FORECAST_PVV_MTLS=true")
		} else {
			// The ticker (and therefore PVV) runs in this process only in inline
			// mode; in remote mode the compute service issues its own certificate.
			mgrCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
			mgr := vault.NewManager(vc.Client, vc.PKIMount, vc.PVVRole, vc.PVVCN, vc.CertTTL)
			if err := mgr.Start(mgrCtx); err != nil {
				// The manager keeps retrying; until it succeeds PVV fetches fail
				// with ErrNoCertificate rather than falling back to Grafana mode.
				log.DefaultLogger.Error("forecast: vault pvv issue failed", "err", err.Error())
			}
			app.retrain.PVVManager = mgr
			app.vaultCancels = append(app.vaultCancels, cancel)
		}
	}
	if app.poster == nil {
		app.poster = newGrafanaPoster(app.retrain)
	}
	// Only an app with a schedule table, a poster and retraining enabled runs the
	// unattended ticker; the overlay's own Retrain remains the path without it. In
	// remote mode the ticker is the compute service's: the claim queue would make
	// two tickers correct but not scaled, and scaling fitting is the point of the
	// mode, so this process claims nothing.
	//
	// The ticker must outlive the request that happens to create this instance —
	// instancemgmt builds an instance from the first RPC's context, which is
	// cancelled the moment that request finishes — so it gets a context detached
	// from that one and stopped by Dispose instead.
	if app.compute == nil && app.sched != nil && app.poster != nil && app.retrain.Enabled {
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
	if a.compute != nil && a.compute.cancel != nil {
		a.compute.cancel()
	}
	for _, cancel := range a.vaultCancels {
		cancel()
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

// CheckHealth handles health checks sent from Grafana to the plugin. Every path
// through this app either reads a snapshot or writes one, so the probe answers for
// the store instead of unconditionally saying ok: a deployment with no DSN is
// healthy by design (the overlay still trains), but one whose store is
// unreachable — or whose schema the migrations could not provision — is not, and
// used to be indistinguishable from a working plugin.
func (a *App) CheckHealth(ctx context.Context, _ *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	if a.store != nil {
		if err := a.store.Ping(ctx); err != nil {
			return &backend.CheckHealthResult{
				Status:  backend.HealthStatusError,
				Message: err.Error(),
			}, nil
		}
	}
	// In remote mode this process is a proxy, so the probe also answers for the
	// upstream: an operator must be able to see from Grafana whether the service
	// the panel depends on answers at all.
	if a.compute != nil {
		if reason := a.computeProbe(ctx); reason != "" {
			return &backend.CheckHealthResult{
				Status:  backend.HealthStatusError,
				Message: "compute " + a.compute.url + ": " + reason,
			}, nil
		}
	}
	if a.store == nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusOk,
			Message: "ok: forecast store is not configured",
		}, nil
	}
	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: "ok",
	}, nil
}
