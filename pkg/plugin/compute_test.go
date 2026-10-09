package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/eduard-kolotushin/timeseries-grafana/pkg/store"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// clearComputeEnv isolates the compute settings the way clearStoreEnv isolates the store
// ones: every spelling computeFrom reads, plus the knobs the mode-aware limiter and the
// ticker gate consult, so a developer's shell cannot decide what these tests assert.
func clearComputeEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"FORECAST_COMPUTE_URL",
		"FORECAST_COMPUTE_TOKEN",
		"FORECAST_MAX_INFLIGHT",
		"FORECAST_RETRAIN_ENABLED",
		store.PluginEnvPrefixApp + "COMPUTE_URL",
		store.PluginEnvPrefixApp + "COMPUTE_TOKEN",
		store.PluginEnvPrefixDatasource + "COMPUTE_URL",
		store.PluginEnvPrefixDatasource + "COMPUTE_TOKEN",
	}
	for _, k := range keys {
		t.Setenv(k, "")
	}
}

// newComputeApp builds the App the standalone service builds: a nil compute mode (so it
// can never forward), the given store and no schedule store.
func newComputeApp(t *testing.T, store SnapshotStore) *App {
	t.Helper()
	clearStoreEnv(t)
	app, err := newAppWith(context.Background(), backend.AppInstanceSettings{}, store, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Dispose)
	return app
}

func computeMux(app *App, token string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", app.handlePing)
	mux.HandleFunc("/healthz", app.handleComputeHealth)
	mux.Handle("/forecast", computeAuth(token, http.HandlerFunc(app.handleForecast)))
	return mux
}

// TestComputeFromResolvesTheMode: the URL's presence is the mode, and every spelling of
// the setting resolves through storeLookup's chain. A URL that resolves but is whitespace
// is not a mode either.
func TestComputeFromResolvesTheMode(t *testing.T) {
	jsonURL, _ := json.Marshal(map[string]any{"computeUrl": "http://from-json:8080"})
	jsonURLToken, _ := json.Marshal(map[string]any{"computeUrl": "http://from-json:8080", "computeToken": "json-token"})
	tests := []struct {
		name      string
		env       map[string]string
		cfg       map[string]string
		settings  backend.AppInstanceSettings
		wantNil   bool
		wantURL   string
		wantToken string
	}{
		{name: "nothing set is inline", wantNil: true},
		{
			name:    "every spelling empty is inline",
			env:     map[string]string{"FORECAST_COMPUTE_URL": "  "},
			cfg:     nil,
			wantNil: true,
		},
		{
			name:    "FORECAST_COMPUTE_URL selects remote",
			env:     map[string]string{"FORECAST_COMPUTE_URL": "http://env:8080"},
			wantURL: "http://env:8080",
		},
		{
			name:    "a trailing slash is trimmed",
			env:     map[string]string{"FORECAST_COMPUTE_URL": "http://env:8080/"},
			wantURL: "http://env:8080",
		},
		{
			name:    "GF_PLUGIN app spelling",
			env:     map[string]string{store.PluginEnvPrefixApp + "COMPUTE_URL": "http://gf-app:8080"},
			wantURL: "http://gf-app:8080",
		},
		{
			name:    "GF_PLUGIN datasource spelling",
			env:     map[string]string{store.PluginEnvPrefixDatasource + "COMPUTE_URL": "http://gf-ds:8080"},
			wantURL: "http://gf-ds:8080",
		},
		{
			name:    "grafana.ini spelling",
			cfg:     map[string]string{"compute_url": "http://ini:8080"},
			wantURL: "http://ini:8080",
		},
		{
			name:     "jsonData spelling",
			settings: backend.AppInstanceSettings{JSONData: jsonURL},
			wantURL:  "http://from-json:8080",
		},
		{
			name:     "env wins over jsonData",
			env:      map[string]string{"FORECAST_COMPUTE_URL": "http://env:8080"},
			settings: backend.AppInstanceSettings{JSONData: jsonURL},
			wantURL:  "http://env:8080",
		},
		{
			name:      "token from jsonData",
			settings:  backend.AppInstanceSettings{JSONData: jsonURLToken},
			wantURL:   "http://from-json:8080",
			wantToken: "json-token",
		},
		{
			name: "token from secureJsonData",
			env:  map[string]string{"FORECAST_COMPUTE_URL": "http://env:8080"},
			settings: backend.AppInstanceSettings{
				DecryptedSecureJSONData: map[string]string{"computeToken": "secure-token"},
			},
			wantURL:   "http://env:8080",
			wantToken: "secure-token",
		},
		{
			name:      "env token wins over secureJsonData",
			env:       map[string]string{"FORECAST_COMPUTE_URL": "http://env:8080", "FORECAST_COMPUTE_TOKEN": "env-token"},
			settings:  backend.AppInstanceSettings{DecryptedSecureJSONData: map[string]string{"computeToken": "secure-token"}},
			wantURL:   "http://env:8080",
			wantToken: "env-token",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearComputeEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			ctx := context.Background()
			if tc.cfg != nil {
				ctx = backend.WithGrafanaConfig(ctx, backend.NewGrafanaCfg(tc.cfg))
			}
			got := computeFrom(ctx, tc.settings)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("mode = %+v, want inline", got)
				}
				return
			}
			if got == nil {
				t.Fatal("mode is inline, want remote")
			}
			if got.url != tc.wantURL {
				t.Fatalf("url = %q want %q", got.url, tc.wantURL)
			}
			if got.token != tc.wantToken {
				t.Fatalf("token = %q want %q", got.token, tc.wantToken)
			}
			if got.client == nil || got.client.Timeout != computeTimeout {
				t.Fatalf("client = %+v, want the computeTimeout budget", got.client)
			}
		})
	}
}

// TestComputeAuthBoundary: the service's trust boundary. A missing or wrong token is
// refused before anything else, the org is parsed as int64 and non-negative, and a valid
// pair is what makes the reused handlers see the plugin context httpadapter would have
// attached under Grafana.
func TestComputeAuthBoundary(t *testing.T) {
	const token = "shared-secret"
	tests := []struct {
		name       string
		auth       string
		org        string
		wantStatus int
		wantOrg    int64
		wantPhrase string
	}{
		{name: "no authorization", org: "7", wantStatus: 401, wantPhrase: "unauthorized"},
		{name: "wrong token", auth: "Bearer nope", org: "7", wantStatus: 401, wantPhrase: "unauthorized"},
		{name: "token without the bearer scheme", auth: token, org: "7", wantStatus: 401, wantPhrase: "unauthorized"},
		{name: "token of the right shape but wrong", auth: "Bearer shared-secre", org: "7", wantStatus: 401, wantPhrase: "unauthorized"},
		{name: "missing org", auth: "Bearer " + token, wantStatus: 400, wantPhrase: "missing X-Forecast-Org"},
		{name: "non-numeric org", auth: "Bearer " + token, org: "abc", wantStatus: 400, wantPhrase: "bad X-Forecast-Org"},
		{name: "negative org", auth: "Bearer " + token, org: "-1", wantStatus: 400, wantPhrase: "bad X-Forecast-Org"},
		{name: "org zero is legal", auth: "Bearer " + token, org: "0", wantStatus: 200, wantOrg: 0},
		{name: "org is injected as the plugin context", auth: "Bearer " + token, org: "7", wantStatus: 200, wantOrg: 7},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var seen int64 = -1
			reached := false
			h := computeAuth(token, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				reached = true
				seen = backend.PluginConfigFromContext(req.Context()).OrgID
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodPost, "/forecast", strings.NewReader("{}"))
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			if tc.org != "" {
				req.Header.Set(computeOrgHeader, tc.org)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%s want %d", rec.Code, rec.Body, tc.wantStatus)
			}
			if tc.wantPhrase != "" && !strings.Contains(rec.Body.String(), tc.wantPhrase) {
				t.Fatalf("body=%q want %q", rec.Body.String(), tc.wantPhrase)
			}
			if tc.wantStatus != http.StatusOK {
				if reached {
					t.Fatal("the wrapped handler ran for a refused request")
				}
				return
			}
			if !reached || seen != tc.wantOrg {
				t.Fatalf("handler org=%d reached=%v want %d", seen, reached, tc.wantOrg)
			}
		})
	}
}

// TestServeComputeRefusesWithoutToken: an unauthenticated compute endpoint is a hole, so
// the process refuses to start rather than serving one.
func TestServeComputeRefusesWithoutToken(t *testing.T) {
	clearComputeEnv(t)
	t.Setenv("FORECAST_COMPUTE_TOKEN", "")
	clearStoreEnv(t)
	err := ServeCompute(context.Background(), "127.0.0.1:0")
	if !errors.Is(err, errComputeNoToken) {
		t.Fatalf("err=%v want errComputeNoToken", err)
	}
}

// TestServeComputeStartsAndStopsIgnoringAComputeURL: with a token the service serves until
// its context is cancelled, and a FORECAST_COMPUTE_URL in the same env block does not make
// it dial itself — the mode is fixed by construction, not read.
func TestServeComputeStartsAndStopsIgnoringAComputeURL(t *testing.T) {
	clearComputeEnv(t)
	t.Setenv("FORECAST_COMPUTE_TOKEN", "sandbox-token")
	t.Setenv("FORECAST_COMPUTE_URL", "http://127.0.0.1:1")
	clearStoreEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeCompute(ctx, "127.0.0.1:0") }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ServeCompute: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ServeCompute did not stop on context cancellation")
	}
}

// TestComputeMuxServesOnlyForecastAndHealth: the service mounts the fit path, the ping
// probe and the coarse health probe — no schedules API, no datasource routes — and a fit
// reaches the same inline dispatch with the org from the header, so the snapshot lands
// under the org the plugin asserted.
func TestComputeMuxServesOnlyForecastAndHealth(t *testing.T) {
	t.Setenv("FORECAST_COMPUTE_TOKEN", "sandbox-token")
	const org = 9
	mem := newMemoryStore()
	app := newComputeApp(t, mem)
	mux := computeMux(app, "sandbox-token")
	srv := httptest.NewServer(mux)
	defer srv.Close()

	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}

	if status, raw := get("/healthz"); status != http.StatusOK || !strings.Contains(raw, `"ok"`) {
		t.Fatalf("healthz=%d %s", status, raw)
	}
	if status, raw := get("/ping"); status != http.StatusOK || !strings.Contains(raw, `"message":"ok"`) {
		t.Fatalf("ping=%d %s", status, raw)
	}
	if status, _ := get("/schedules"); status != http.StatusNotFound {
		t.Fatalf("schedules=%d want 404 (it stays in the Grafana-managed plugin)", status)
	}

	key := strings.Repeat("c", 64)
	body, _ := json.Marshal(ForecastRequest{
		Times:    []int64{0, 60_000, 120_000, 180_000},
		Values:   []nullableFloat{1, 2, 3, 4},
		Model:    "naive",
		CacheKey: key,
		From:     240_000,
		To:       300_000,
	})
	post := func(auth, orgHeader string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/forecast", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		if orgHeader != "" {
			req.Header.Set(computeOrgHeader, orgHeader)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}

	if status, raw := post("", "9"); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated fit=%d %s want 401", status, raw)
	}
	if status, raw := post("Bearer sandbox-token", ""); status != http.StatusBadRequest {
		t.Fatalf("fit without an org=%d %s want 400", status, raw)
	}
	status, raw := post("Bearer sandbox-token", "9")
	if status != http.StatusOK {
		t.Fatalf("fit=%d %s", status, raw)
	}
	var out ForecastResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	if len(out.Values) != 2 || float64(out.Values[0]) != 4 {
		t.Fatalf("forecast=%v, want the naive continuation", out.Values)
	}
	if _, ok, err := mem.Get(context.Background(), org, key); err != nil || !ok {
		t.Fatalf("snapshot ok=%v err=%v, want it stored under the asserted org %d", ok, err, org)
	}
	// A different org's request must not see that row.
	if _, ok, _ := mem.Get(context.Background(), org+1, key); ok {
		t.Fatal("the snapshot was visible to another org")
	}
}

// upstreamRecorder is a compute service stand-in that answers with a fixed status, body
// and content type and records what it received.
type upstreamRecorder struct {
	*httptest.Server
	status  int
	body    string
	ctype   string
	calls   int
	org     string
	auth    string
	rawBody []byte
	ctypeIn string
}

func newUpstream(t *testing.T, status int, ctype, body string) *upstreamRecorder {
	t.Helper()
	u := &upstreamRecorder{status: status, body: body, ctype: ctype}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		u.calls++
		u.org = req.Header.Get(computeOrgHeader)
		u.auth = req.Header.Get("Authorization")
		u.ctypeIn = req.Header.Get("Content-Type")
		u.rawBody, _ = io.ReadAll(req.Body)
		w.Header().Set("Content-Type", u.ctype)
		w.WriteHeader(u.status)
		_, _ = io.WriteString(w, u.body)
	}))
	t.Cleanup(u.Close)
	return u
}

func remoteApp(t *testing.T, u *upstreamRecorder) *App {
	t.Helper()
	app, err := newAppWith(context.Background(), backend.AppInstanceSettings{}, newMemoryStore(), nil, nil,
		&computeMode{url: u.URL, token: "upstream-token", client: u.Client()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Dispose)
	return app
}

// TestForecastForwardsVerbatim: a remote-mode fit is re-sent with the plugin's identity and
// the upstream answer reaches the caller unchanged — status, content type and body — for a
// success and for the two refusals the overlay knows how to render.
func TestForecastForwardsVerbatim(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		ctype  string
		body   string
	}{
		{name: "ok", status: http.StatusOK, ctype: "application/json", body: `{"times":[4000],"values":[5]}`},
		{name: "busy", status: http.StatusTooManyRequests, ctype: "text/plain; charset=utf-8", body: errBusy.Error()},
		{name: "too long", status: http.StatusRequestEntityTooLarge, ctype: "text/plain; charset=utf-8", body: errTrainTooLong.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := newUpstream(t, tc.status, tc.ctype, tc.body)
			app := remoteApp(t, u)
			body, _ := json.Marshal(ForecastRequest{
				Times:  []int64{0, 1000, 2000, 3000},
				Values: []nullableFloat{1, 2, 3, 4},
				Model:  "naive",
				From:   4000,
				To:     5000,
			})
			status, raw := callRoute(t, app, adminCtx(7), http.MethodPost, "forecast", body)
			if status != tc.status {
				t.Fatalf("status=%d body=%s want %d", status, raw, tc.status)
			}
			if string(raw) != tc.body {
				t.Fatalf("body=%q want %q verbatim", raw, tc.body)
			}
			if u.calls != 1 {
				t.Fatalf("upstream calls=%d want 1", u.calls)
			}
			if string(u.rawBody) != string(body) {
				t.Fatalf("upstream body=%s want the panel's body unchanged", u.rawBody)
			}
			if u.auth != "Bearer upstream-token" {
				t.Fatalf("upstream authorization=%q", u.auth)
			}
			if u.org != "7" {
				t.Fatalf("upstream org=%q want the authenticated org", u.org)
			}
			if u.ctypeIn != "application/json" {
				t.Fatalf("upstream content type=%q", u.ctypeIn)
			}
		})
	}
}

// TestForecastForwardsVerbatimThroughGrafanaRoute checks that the identity the plugin
// asserts is the org Grafana authenticated and not anything the caller sent: a forwarded
// request carries no attacker-controlled header, because the outgoing request is built
// from scratch.
func TestForecastForwardOverwritesTheCallerOrg(t *testing.T) {
	u := newUpstream(t, http.StatusOK, "application/json", `{}`)
	app := remoteApp(t, u)
	req := httptest.NewRequest(http.MethodPost, "/forecast", strings.NewReader(`{}`))
	req.Header.Set(computeOrgHeader, "999")
	req.Header.Set("Authorization", "Bearer client-token")
	req = req.WithContext(backend.WithPluginContext(req.Context(), adminCtx(3)))
	rec := httptest.NewRecorder()
	app.handleForecast(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if u.org != "3" {
		t.Fatalf("upstream org=%q, want Grafana's org 3", u.org)
	}
	if u.auth != "Bearer upstream-token" {
		t.Fatalf("upstream authorization=%q, want the plugin's own token", u.auth)
	}
}

// TestForecastForwardUnreachableIsBadGateway: a compute service that cannot be reached is a
// 502 naming the failure — never a silent local fit, which would hide a split deployment
// whose compute half is down while the plugin keeps burning Grafana CPU.
func TestForecastForwardUnreachableIsBadGateway(t *testing.T) {
	app, err := newAppWith(context.Background(), backend.AppInstanceSettings{}, newMemoryStore(), nil, nil,
		&computeMode{url: "http://127.0.0.1:1", token: "t", client: &http.Client{Timeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Dispose)
	body, _ := json.Marshal(ForecastRequest{
		Times: []int64{0, 1000}, Values: []nullableFloat{1, 2}, Model: "naive", From: 2000, To: 3000,
	})
	status, raw := callRoute(t, app, adminCtx(7), http.MethodPost, "forecast", body)
	if status != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", status, raw)
	}
	if !strings.HasPrefix(string(raw), errComputeUnreachable.Error()) {
		t.Fatalf("body=%q want it to name %q", raw, errComputeUnreachable)
	}
}

// TestForecastForwardRefusesOversizeBodyBeforeUpstream: the two ContentLength checks stay
// in the plugin before the forward, so an impossible body is refused without a decode here
// and without a call there.
func TestForecastForwardRefusesOversizeBodyBeforeUpstream(t *testing.T) {
	u := newUpstream(t, http.StatusOK, "application/json", `{}`)
	app := remoteApp(t, u)
	req := httptest.NewRequest(http.MethodPost, "/forecast", strings.NewReader("{}"))
	req.ContentLength = maxTrainBodyBytes() + 1
	req = req.WithContext(backend.WithPluginContext(req.Context(), adminCtx(7)))
	rec := httptest.NewRecorder()
	app.handleForecast(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s want 413", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), errTrainBodyTooLarge.Error()) {
		t.Fatalf("body=%q want %q", rec.Body.String(), errTrainBodyTooLarge)
	}
	if u.calls != 0 {
		t.Fatalf("upstream calls=%d, want the refusal to happen before the forward", u.calls)
	}
}

// TestRemoteModeRunsNoTickerAndUsesTheProxyLimit: remote mode hands the ticker to the
// compute service and swaps the limiter default, because a forwarding slot is a socket
// rather than a fit. FORECAST_MAX_INFLIGHT still overrides either default.
func TestRemoteModeRunsNoTickerAndUsesTheProxyLimit(t *testing.T) {
	newPoster := func() framePoster { return &fakePoster{points: 6, step: time.Minute} }
	settings := backend.AppInstanceSettings{}

	t.Run("inline starts the ticker with the fit default", func(t *testing.T) {
		clearComputeEnv(t)
		app, err := newApp(context.Background(), settings, newMemoryStore(), newMemSchedules(), newPoster())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(app.Dispose)
		if app.cancel == nil {
			t.Fatal("inline mode did not start the scheduler")
		}
		if got := cap(app.limit.ch); got != defaultMaxInflight {
			t.Fatalf("limiter=%d want %d", got, defaultMaxInflight)
		}
	})

	t.Run("remote starts no ticker and gets the proxy default", func(t *testing.T) {
		clearComputeEnv(t)
		app, err := newAppWith(context.Background(), settings, newMemoryStore(), newMemSchedules(), newPoster(),
			&computeMode{url: "http://compute:8080", token: "t", client: http.DefaultClient})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(app.Dispose)
		if app.cancel != nil {
			t.Fatal("remote mode started a scheduler: the compute service owns the ticker")
		}
		if got := cap(app.limit.ch); got != defaultMaxProxyInflight {
			t.Fatalf("limiter=%d want %d", got, defaultMaxProxyInflight)
		}
	})

	t.Run("FORECAST_MAX_INFLIGHT overrides both", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			compute *computeMode
		}{
			{name: "inline"},
			{name: "remote", compute: &computeMode{url: "http://compute:8080", token: "t", client: http.DefaultClient}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				clearComputeEnv(t)
				t.Setenv("FORECAST_MAX_INFLIGHT", "5")
				app, err := newAppWith(context.Background(), settings, newMemoryStore(), newMemSchedules(), newPoster(), tc.compute)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(app.Dispose)
				if got := cap(app.limit.ch); got != 5 {
					t.Fatalf("limiter=%d want the configured 5", got)
				}
			})
		}
	})
}

// TestForecastRemoteModeMatchesInline: the model of the whole change — the same request
// through the inline handler and through a plugin proxying to that handler answers the
// same bytes and writes the same row. If the two ever diverge, the mode is not a switch.
func TestForecastRemoteModeMatchesInline(t *testing.T) {
	clearComputeEnv(t)
	body, _ := json.Marshal(ForecastRequest{
		Times:    []int64{0, 60_000, 120_000, 180_000},
		Values:   []nullableFloat{1, 2, 3, 4},
		Model:    "naive",
		CacheKey: strings.Repeat("b", 64),
		From:     240_000,
		To:       300_000,
	})

	inlineStore := newMemoryStore()
	inline, err := newAppWith(context.Background(), backend.AppInstanceSettings{}, inlineStore, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(inline.Dispose)

	// The remote plugin proxies to a service built exactly as ServeCompute builds it: an
	// App with a nil mode and its own store.
	upstream := httptest.NewServer(computeMux(newComputeApp(t, newMemoryStore()), "shared"))
	defer upstream.Close()
	remote, err := newAppWith(context.Background(), backend.AppInstanceSettings{}, newMemoryStore(), nil, nil,
		&computeMode{url: upstream.URL, token: "shared", client: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remote.Dispose)

	inlineStatus, inlineBody := callRoute(t, inline, adminCtx(7), http.MethodPost, "forecast", body)
	remoteStatus, remoteBody := callRoute(t, remote, adminCtx(7), http.MethodPost, "forecast", body)
	if inlineStatus != http.StatusOK || remoteStatus != http.StatusOK {
		t.Fatalf("inline=%d %s remote=%d %s", inlineStatus, inlineBody, remoteStatus, remoteBody)
	}
	if string(inlineBody) != string(remoteBody) {
		t.Fatalf("inline=%s remote=%s", inlineBody, remoteBody)
	}
	if _, ok, err := inlineStore.Get(context.Background(), 7, strings.Repeat("b", 64)); err != nil || !ok {
		t.Fatalf("inline snapshot ok=%v err=%v", ok, err)
	}
}

// TestHealthReportsAnUnreachableComputeService: in remote mode the health probe answers for
// the upstream too, so a split deployment whose compute half is down is visible from
// Grafana instead of looking healthy while every panel fails.
func TestHealthReportsAnUnreachableComputeService(t *testing.T) {
	clearComputeEnv(t)
	clearStoreEnv(t)
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := dead.URL
	dead.Close()

	app, err := newAppWith(context.Background(), backend.AppInstanceSettings{}, nil, nil, nil,
		&computeMode{url: url, token: "t", client: &http.Client{Timeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Dispose)
	res, err := app.CheckHealth(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != backend.HealthStatusError || !strings.Contains(res.Message, url) {
		t.Fatalf("health=%+v, want an error naming %s", res, url)
	}

	upstream := newUpstream(t, http.StatusOK, "application/json", `{"status":"ok"}`)
	app2, err := newAppWith(context.Background(), backend.AppInstanceSettings{}, nil, nil, nil,
		&computeMode{url: upstream.URL, token: "t", client: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app2.Dispose)
	res2, err := app2.CheckHealth(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Status != backend.HealthStatusOk {
		t.Fatalf("health=%+v, want ok", res2)
	}
	if upstream.calls != 1 {
		t.Fatalf("healthz calls=%d want 1", upstream.calls)
	}
}
