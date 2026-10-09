package plugin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

const (
	// computeURLPath is the one route the plugin forwards and the only route the
	// compute service mounts besides its health probes.
	computeURLPath = "/forecast"

	// computeOrgHeader carries the Grafana org the plugin asserts for a forwarded
	// request. The plugin takes it from the org Grafana authenticated, never from
	// the caller, and the compute service injects it as the SDK's plugin context
	// so the reused handlers read the same org they read under httpadapter.
	computeOrgHeader = "X-Forecast-Org"

	// computeTimeout bounds one forwarded call. A fit is milliseconds of work, and the point of the
	// bound is a *silent* upstream — a connection that is accepted and then never answered — which
	// otherwise holds the caller's request (and a proxy slot) for as long as the bound allows.
	// Measured live against a deliberately silent upstream: 2 minutes (the first value here) held the
	// request until the client had long given up (~30 s), so the caller never saw the 502 the plugin
	// eventually wrote. 15 s is far above a cold compute pod's first call (pgxpool connect, a fit of
	// up to maxTrainPoints points, one Postgres write) and short enough that the reason still reaches
	// a waiting panel.
	computeTimeout = 15 * time.Second
)

var (
	errComputeUnreachable  = errors.New("forecast: compute service unreachable")
	errComputeUnauthorized = errors.New("forecast compute: unauthorized")
	errComputeNoOrg        = errors.New("forecast compute: missing X-Forecast-Org")
	errComputeBadOrg       = errors.New("forecast compute: bad X-Forecast-Org")
	errComputeNoToken      = errors.New("forecast compute: FORECAST_COMPUTE_TOKEN is required: the service refuses unauthenticated requests")
	errComputeHealthMethod = errors.New("forecast compute: health probe is GET-only")
)

// computeMode is how this process fits series: in-process (nil) or by forwarding
// to a separate compute service. A nil *computeMode is inline, so the inline path
// allocates nothing and every existing test keeps its shape.
type computeMode struct {
	url    string
	token  string
	client *http.Client
}

// computeFrom resolves the compute mode from the same settings chain the store
// uses (FORECAST_* env → GF_PLUGIN_* env → grafana.ini → jsonData). The URL's
// presence selects the mode: unset or empty means inline, so there is no state in
// which a mode and a URL can disagree.
func computeFrom(ctx context.Context, settings backend.AppInstanceSettings) *computeMode {
	jd := map[string]any{}
	if len(settings.JSONData) > 0 {
		_ = json.Unmarshal(settings.JSONData, &jd)
	}
	look := storeLookup{
		getenv: os.Getenv,
		cfg:    backend.GrafanaConfigFromContext(ctx),
		json:   jd,
	}
	url := strings.TrimRight(look.Get("FORECAST_COMPUTE_URL", "COMPUTE_URL", "compute_url", "computeUrl"), "/")
	if url == "" {
		return nil
	}
	token := look.Get("FORECAST_COMPUTE_TOKEN", "COMPUTE_TOKEN", "compute_token", "computeToken")
	if token == "" && settings.DecryptedSecureJSONData != nil {
		token = strings.TrimSpace(settings.DecryptedSecureJSONData["computeToken"])
	}
	if token == "" {
		// Keep serving: without a token every forwarded call is refused upstream,
		// and a misconfiguration that silently does nothing is worse than one that
		// logs here and answers 401 there.
		log.DefaultLogger.Warn("forecast: compute mode without a token; forwarded calls will be refused", "url", url)
	}
	return &computeMode{url: url, token: token, client: &http.Client{Timeout: computeTimeout}}
}

// computeAuth is the standalone service's trust boundary: the shared token and the
// org the trusted plugin asserted replace the identity the Grafana SDK would have
// attached. The compare is constant time so the token cannot be recovered byte by
// byte.
func computeAuth(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		got := strings.TrimSpace(req.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			http.Error(w, errComputeUnauthorized.Error(), http.StatusUnauthorized)
			return
		}
		raw := strings.TrimSpace(req.Header.Get(computeOrgHeader))
		if raw == "" {
			http.Error(w, errComputeNoOrg.Error(), http.StatusBadRequest)
			return
		}
		org, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || org < 0 {
			http.Error(w, errComputeBadOrg.Error(), http.StatusBadRequest)
			return
		}
		ctx := backend.WithPluginContext(req.Context(), backend.PluginContext{OrgID: org})
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

// handleComputeHealth is the service's own probe. It is unauthenticated so a
// kubelet probe can call it without the token, so its answer is coarse: the store
// error text names internal hosts and stays in CheckHealth's message (the plugin's
// health page) and in this process's log.
func (a *App) handleComputeHealth(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, errComputeHealthMethod.Error(), http.StatusMethodNotAllowed)
		return
	}
	res, err := a.CheckHealth(req.Context(), nil)
	if err != nil || res == nil || res.Status != backend.HealthStatusOk {
		if err != nil {
			log.DefaultLogger.Error("forecast compute health", "err", err.Error())
		} else if res != nil {
			log.DefaultLogger.Error("forecast compute health", "err", res.Message)
		}
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]string{"status": "error"})
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// writeJSONStatus is writeJSON with a status code: the header must be sent before
// the body, and the shared helper cannot do it after encoding.
func writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// forwardForecast is the remote-mode body of handleForecast: the plugin is a
// proxy, so the request is re-sent verbatim with the identity the trusted plugin
// asserts, and the answer is copied through unchanged — status, content type and
// body — so a panel cannot tell the two modes apart.
func (a *App) forwardForecast(w http.ResponseWriter, req *http.Request) {
	release, err := a.limit.try(req.Context())
	if err != nil {
		http.Error(w, err.Error(), httpStatusFor(err))
		return
	}
	defer release()
	orgID := backend.PluginConfigFromContext(req.Context()).OrgID
	out, err := http.NewRequestWithContext(req.Context(), http.MethodPost, a.compute.url+computeURLPath,
		http.MaxBytesReader(w, req.Body, a.bodyLimit()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out.ContentLength = req.ContentLength
	out.Header.Set("Content-Type", "application/json")
	out.Header.Set("Authorization", "Bearer "+a.compute.token)
	out.Header.Set(computeOrgHeader, strconv.FormatInt(orgID, 10))
	resp, err := a.compute.client.Do(out)
	if err != nil {
		http.Error(w, errComputeUnreachable.Error()+": "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// computeProbe returns "" when the compute service answers its health probe, else
// a short reason. The upstream body is never echoed: CheckHealth's message is
// rendered in Grafana's health page, and the service's own detail (which names
// internal hosts) belongs in the service's log.
func (a *App) computeProbe(ctx context.Context) string {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, a.compute.url+"/healthz", nil)
	if err != nil {
		return err.Error()
	}
	resp, err := a.compute.client.Do(req)
	if err != nil {
		return err.Error()
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "healthz answered " + resp.Status
	}
	return ""
}

// ServeCompute runs the standalone compute service: the app's own handlers over
// plain HTTP, with the SDK's request identity replaced by FORECAST_COMPUTE_TOKEN
// and the X-Forecast-Org header the plugin sets. It builds the App with a nil
// compute mode, so the service can never forward to another compute service, and
// it mounts only /forecast, /ping and the coarse /healthz probe — the schedules
// API and the alerting QueryData path stay in the Grafana-managed plugin, which
// keeps working from the same store while this service is down.
func ServeCompute(ctx context.Context, addr string) error {
	token := strings.TrimSpace(os.Getenv("FORECAST_COMPUTE_TOKEN"))
	if token == "" {
		return errComputeNoToken
	}
	app, err := newAppWith(ctx, backend.AppInstanceSettings{}, nil, nil, nil, nil)
	if err != nil {
		return err
	}
	defer app.Dispose()
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", app.handlePing)
	mux.HandleFunc("/healthz", app.handleComputeHealth)
	mux.Handle("/forecast", computeAuth(token, http.HandlerFunc(app.handleForecast)))
	log.DefaultLogger.Info("forecast compute listening", "addr", addr, "store", app.store != nil, "retrain", app.retrain.Enabled)
	return serveHTTP(ctx, addr, mux)
}

// serveHTTP serves until ctx is cancelled, then shuts the listener down so an
// in-flight fit finishes rather than being cut mid-write. A listener that cannot
// bind (port in use, permission) returns its error at once instead of waiting for
// a cancellation that will never come.
func serveHTTP(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	err := srv.ListenAndServe()
	if ctx.Err() != nil {
		// The shutdown path is running: wait for it, so the caller's cleanup does
		// not race a listener that is still draining.
		<-done
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
