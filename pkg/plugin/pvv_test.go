package plugin

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

type pvvTestTLS struct {
	caPath         string
	clientCertPath string
	clientKeyPath  string
	serverCert     tls.Certificate
	caPool         *x509.CertPool
}

func newPVVTestTLS(t *testing.T) pvvTestTLS {
	t.Helper()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "pvv-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serverTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTmpl, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	clientTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "forecast-retrain"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTmpl, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	clientCertPath := filepath.Join(dir, "client.pem")
	clientKeyPath := filepath.Join(dir, "client-key.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clientCertPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clientKeyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(clientKey)}), 0o600); err != nil {
		t.Fatal(err)
	}
	return pvvTestTLS{
		caPath:         caPath,
		clientCertPath: clientCertPath,
		clientKeyPath:  clientKeyPath,
		serverCert:     tls.Certificate{Certificate: [][]byte{serverDER}, PrivateKey: serverKey},
		caPool:         caPool,
	}
}

func startPVV(t *testing.T, tlsMaterial pvvTestTLS, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{tlsMaterial.serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    tlsMaterial.caPool,
		MinVersion:   tls.VersionTLS12,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func pvvPoster(t *testing.T, tlsMaterial pvvTestTLS, url string, chunk time.Duration) *grafanaPoster {
	t.Helper()
	return newGrafanaPoster(retrainConfig{
		GrafanaURL:         "http://127.0.0.1:1",
		PVVURL:             url,
		PVVClientCert:      tlsMaterial.clientCertPath,
		PVVClientKey:       tlsMaterial.clientKeyPath,
		PVVCA:              tlsMaterial.caPath,
		RetrainWindowChunk: chunk,
	})
}

func pvvFramesJSON(from int64) []byte {
	return []byte(fmt.Sprintf(
		`{"frames":[{"schema":{"fields":[{"name":"time","type":"time"},{"name":"value","type":"number"}]},"data":{"values":[[%d,%d],[1,2]]}}],"status":200}`,
		from, from+60_000,
	))
}

func TestPVVDataByRefIDChunksAndMerges(t *testing.T) {
	tlsMaterial := newPVVTestTLS(t)
	var requests []pvvPanelRequest
	var authHeaders, contentTypes []string
	var rawKeys []map[string]json.RawMessage
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != pvvDataByRefIDPath {
			http.Error(w, "bad route", http.StatusNotFound)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req pvvPanelRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(raw, &keys); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests = append(requests, req)
		rawKeys = append(rawKeys, keys)
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		contentTypes = append(contentTypes, r.Header.Get("Content-Type"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(pvvFramesJSON(req.From))
	})
	srv := startPVV(t, tlsMaterial, handler)
	poster := pvvPoster(t, tlsMaterial, srv.URL, 24*time.Hour)
	if !poster.pvvMode() {
		t.Fatal("a client certificate and key must select PVV mode")
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	to := base + int64(50*time.Hour/time.Millisecond)
	spec := retrainSpec{
		TrainSource: TrainSource{
			Queries:      json.RawMessage(`[{"refId":"A"}]`),
			From:         base,
			To:           to,
			RefID:        "A",
			DashboardUID: "dash-1",
			PanelID:      7,
		},
	}
	row := ScheduleRow{OrgID: 2, Scope: scopePanel, Key: strings.Repeat("a", 64)}
	series, err := poster.fetchSeries(context.Background(), row, spec)
	if err != nil {
		t.Fatal(err)
	}
	if series.Len() != 6 {
		t.Fatalf("len=%d want 6 (two points per 24h/24h/2h chunk)", series.Len())
	}
	times := series.Times()
	for i := 1; i < len(times); i++ {
		if !times[i].After(times[i-1]) {
			t.Fatalf("times[%d]=%s is not after times[%d]=%s", i, times[i], i-1, times[i-1])
		}
	}
	if len(requests) != 3 {
		t.Fatalf("requests=%d want 3", len(requests))
	}
	want := [][2]int64{
		{base, base + int64(24*time.Hour/time.Millisecond)},
		{base + int64(24*time.Hour/time.Millisecond), base + int64(48*time.Hour/time.Millisecond)},
		{base + int64(48*time.Hour/time.Millisecond), to},
	}
	for i, req := range requests {
		if req.OrgID != 2 || req.DashboardUID != "dash-1" || req.PanelID != 7 || req.RefID != "A" {
			t.Fatalf("request[%d]=%+v", i, req)
		}
		if req.From != want[i][0] || req.To != want[i][1] {
			t.Fatalf("request[%d] window=(%d,%d) want %v", i, req.From, req.To, want[i])
		}
		if authHeaders[i] != "" {
			t.Fatalf("request[%d] carried Authorization %q: PVV authenticates by mTLS", i, authHeaders[i])
		}
		if contentTypes[i] != "application/json" {
			t.Fatalf("request[%d] content type %q", i, contentTypes[i])
		}
		if _, ok := rawKeys[i]["queryHash"]; ok {
			t.Fatalf("request[%d] sent queryHash", i)
		}
		for _, key := range []string{"org_id", "dashboardUID", "panelId", "refId", "from", "to"} {
			if _, ok := rawKeys[i][key]; !ok {
				t.Fatalf("request[%d] is missing %q: %s", i, key, rawKeys[i])
			}
		}
		if len(rawKeys[i]) != 6 {
			t.Fatalf("request[%d] has keys %v, want exactly the six DataByRefId fields", i, rawKeys[i])
		}
	}
}

func TestPVVAcceptsHTTP200(t *testing.T) {
	tlsMaterial := newPVVTestTLS(t)
	srv := startPVV(t, tlsMaterial, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req pvvPanelRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(pvvFramesJSON(req.From))
	}))
	poster := pvvPoster(t, tlsMaterial, srv.URL, 24*time.Hour)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	spec := retrainSpec{TrainSource: TrainSource{
		Queries: json.RawMessage(`[{"refId":"A"}]`), RefID: "A", DashboardUID: "dash-1", PanelID: 7,
		From: base, To: base + int64(2*time.Minute/time.Millisecond),
	}}
	series, err := poster.fetchSeries(context.Background(), ScheduleRow{OrgID: 1, Key: "k"}, spec)
	if err != nil {
		t.Fatal(err)
	}
	if series.Len() != 2 {
		t.Fatalf("len=%d want 2", series.Len())
	}
}

func TestPVVNoFallbackAndErrors(t *testing.T) {
	tlsMaterial := newPVVTestTLS(t)
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "400", status: http.StatusBadRequest, body: `{"message":"missing field"}`, want: "status 400"},
		{name: "401", status: http.StatusUnauthorized, body: `{"message":"bad cert"}`, want: "status 401"},
		{name: "503", status: http.StatusServiceUnavailable, body: `{"message":"down"}`, want: "status 503"},
		{name: "data response error", status: http.StatusCreated, body: `{"frames":[],"status":200,"error":"datasource failed"}`, want: "datasource failed"},
		{name: "malformed", status: http.StatusCreated, body: `{`, want: "pvv DataByRefId"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var grafanaCalls int
			grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				grafanaCalls++
				http.Error(w, "fallback", http.StatusInternalServerError)
			}))
			t.Cleanup(grafana.Close)
			srv := startPVV(t, tlsMaterial, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			poster := pvvPoster(t, tlsMaterial, srv.URL, 24*time.Hour)
			poster.url = grafana.URL // any fallback would hit this server and be counted
			spec := retrainSpec{TrainSource: TrainSource{
				Queries: json.RawMessage(`[{"refId":"A"}]`), RefID: "A", DashboardUID: "dash-1", PanelID: 7,
				From: 1_700_000_000_000, To: 1_700_000_060_000,
			}}
			_, err := poster.fetchSeries(context.Background(), ScheduleRow{OrgID: 1, Key: "k"}, spec)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want %q", err, tc.want)
			}
			if grafanaCalls != 0 {
				t.Fatalf("PVV failure fell back to /api/ds/query %d times", grafanaCalls)
			}
		})
	}
}

func TestPVVFrameDecodeDropsNullPoints(t *testing.T) {
	raw := json.RawMessage(`[{"schema":{"fields":[{"name":"time","type":"time"},{"name":"v","type":"number"}]},"data":{"values":[[1000,null,3000],[1.5,null,3]]}}]`)
	frames, err := decodePVVFrames(raw)
	if err != nil {
		t.Fatal(err)
	}
	series, err := seriesFromFrames(frames, retrainSpec{TrainSource: TrainSource{SeriesName: "v"}})
	if err != nil {
		t.Fatal(err)
	}
	if series.Len() != 2 {
		t.Fatalf("len=%d, want the two non-null points", series.Len())
	}
	if got := series.Values(); got[0] != 1.5 || got[1] != 3 {
		t.Fatalf("values=%v", got)
	}
}

func TestPVVRefIDAndIdentity(t *testing.T) {
	one := retrainSpec{TrainSource: TrainSource{Queries: json.RawMessage(`[{"refId":"A"},{"refId":"B"}]`)}}
	if _, err := pvvRefID(one); err == nil || !strings.Contains(err.Error(), "needs a refId") {
		t.Fatalf("two queries without a stored refId: err=%v", err)
	}
	one.Queries = json.RawMessage(`[{"refId":"A"}]`)
	if got, err := pvvRefID(one); err != nil || got != "A" {
		t.Fatalf("single query: got=%q err=%v", got, err)
	}
	one.RefID = "B"
	if got, err := pvvRefID(one); err != nil || got != "B" {
		t.Fatalf("stored refId: got=%q err=%v", got, err)
	}

	if _, _, _, _, err := pvvIdentity(retrainSpec{}, ScheduleRow{}); err == nil || !strings.Contains(err.Error(), "no org") {
		t.Fatalf("missing org: err=%v", err)
	}
	if _, _, _, _, err := pvvIdentity(retrainSpec{}, ScheduleRow{OrgID: 1}); err == nil || !strings.Contains(err.Error(), "dashboardUid") {
		t.Fatalf("missing dashboard: err=%v", err)
	}
	if _, _, _, _, err := pvvIdentity(retrainSpec{TrainSource: TrainSource{DashboardUID: "d"}}, ScheduleRow{OrgID: 1}); err == nil || !strings.Contains(err.Error(), "panelId") {
		t.Fatalf("missing panel: err=%v", err)
	}
}

func TestPVVRetrainDueUsesEachRowsOrg(t *testing.T) {
	tlsMaterial := newPVVTestTLS(t)
	var orgs []int64
	srv := startPVV(t, tlsMaterial, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req pvvPanelRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		orgs = append(orgs, req.OrgID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(pvvFramesJSON(req.From))
	}))
	poster := pvvPoster(t, tlsMaterial, srv.URL, 24*time.Hour)
	store, sched := newMemoryStore(), newMemSchedules()
	app := &App{
		store: store, sched: sched, poster: poster,
		retrain: retrainConfig{Enabled: true, Cron: "*/5 * * * *", Timezone: "UTC", Lease: time.Minute, RetryMax: time.Hour, Tick: time.Second},
		limit:   newWorkLimiter(1),
	}
	base := time.Now().Add(-time.Hour).UTC().Truncate(time.Minute)
	spec := func() []byte {
		raw, err := json.Marshal(retrainSpec{
			TrainSource: TrainSource{
				Queries:      json.RawMessage(`[{"refId":"A"}]`),
				From:         base.UnixMilli(),
				To:           base.Add(2 * time.Minute).UnixMilli(),
				RefID:        "A",
				DashboardUID: "dash-1",
				PanelID:      7,
			},
			Model: "naive",
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}()
	keys := []string{strings.Repeat("b", 64), strings.Repeat("c", 64)}
	for i, org := range []int64{1, 2} {
		sched.seed(ScheduleRow{
			OrgID: org, Scope: scopePanel, Key: keys[i], Cron: "*/5 * * * *", Timezone: "UTC",
			Enabled: true, Spec: spec, NextRunAt: base,
		})
	}
	out := app.retrainDue(context.Background())
	if out.claimed != 2 || out.ok != 2 || out.failed != 0 {
		t.Fatalf("out=%+v", out)
	}
	seen := map[int64]bool{}
	for _, org := range orgs {
		seen[org] = true
	}
	if len(orgs) != 2 || !seen[1] || !seen[2] {
		t.Fatalf("DataByRefId org_ids=%v, want one request per org", orgs)
	}
	for i, org := range []int64{1, 2} {
		if _, ok, err := store.Get(context.Background(), org, keys[i]); err != nil || !ok {
			t.Fatalf("org %d snapshot ok=%v err=%v", org, ok, err)
		}
	}
}

func TestPVVModeSelection(t *testing.T) {
	tlsMaterial := newPVVTestTLS(t)
	if p := newGrafanaPoster(retrainConfig{}); p.pvvMode() {
		t.Fatal("no client certificate/key must keep Grafana mode")
	}
	if p := pvvPoster(t, tlsMaterial, "https://127.0.0.1:1", 24*time.Hour); !p.pvvMode() {
		t.Fatal("client certificate/key must select PVV mode")
	}
	onlyCert := newGrafanaPoster(retrainConfig{
		PVVURL:        "https://127.0.0.1:1",
		PVVClientCert: tlsMaterial.clientCertPath,
	})
	if !onlyCert.pvvMode() {
		t.Fatal("a configured certificate alone must still select PVV mode, not fall back")
	}
	spec := retrainSpec{TrainSource: TrainSource{RefID: "A", DashboardUID: "d", PanelID: 1}}
	if _, err := onlyCert.fetchSeries(context.Background(), ScheduleRow{OrgID: 1}, spec); err == nil || !strings.Contains(err.Error(), "requires both") {
		t.Fatalf("cert without key: err=%v", err)
	}
}

func TestPVVConfigResolution(t *testing.T) {
	clearRetrainEnv(t)
	t.Setenv("FORECAST_GRAFANA_URL", "http://grafana:3000")
	t.Setenv("FORECAST_RETRAIN_WINDOW_CHUNK", "48h")
	cfg := computeRetrain(context.Background(), backend.AppInstanceSettings{})
	if cfg.PVVURL != "http://grafana:3000" {
		t.Fatalf("PVVURL=%q, want the Grafana URL fallback", cfg.PVVURL)
	}
	if cfg.RetrainWindowChunk != defaultRetrainWindowChunk {
		t.Fatalf("chunk=%s, want the 24h fallback", cfg.RetrainWindowChunk)
	}
	t.Setenv("FORECAST_PVV_URL", "https://pvv:8443")
	t.Setenv("FORECAST_RETRAIN_WINDOW_CHUNK", "12h")
	cfg = computeRetrain(context.Background(), backend.AppInstanceSettings{})
	if cfg.PVVURL != "https://pvv:8443" || cfg.RetrainWindowChunk != 12*time.Hour {
		t.Fatalf("cfg=%+v", cfg)
	}
}
