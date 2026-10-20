package vault

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type testPKI struct {
	caPEM   string
	certPEM string
	keyPEM  string
	cert    tls.Certificate
	pool    *x509.CertPool
}

// newTestPKI creates a CA and one dual-use leaf (server and client auth) for
// 127.0.0.1, which lets one issued certificate stand in for both ends of the
// compute hop.
func newTestPKI(t *testing.T, notAfter time.Time) testPKI {
	t.Helper()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "vault-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              notAfter.Add(time.Hour),
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
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "forecast-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pair.Leaf = leaf
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return testPKI{
		caPEM:   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})),
		certPEM: string(certPEM),
		keyPEM:  string(keyPEM),
		cert:    pair,
		pool:    pool,
	}
}

// fakeVault serves the three endpoints the package uses: login, issue, and
// token renewal. It records call counts so tests can assert precedence and the
// 403 re-login path.
type fakeVault struct {
	mu  sync.Mutex
	pki testPKI

	issues int
	logins int
	renews int

	forbidFirstIssue bool
	loginToken       string
}

func (f *fakeVault) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/approle/login", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.logins++
		token := f.loginToken
		if token == "" {
			token = "approle-token"
		}
		f.mu.Unlock()
		if body["role_id"] == "" || body["secret_id"] == "" {
			http.Error(w, "missing role_id/secret_id", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"auth": map[string]any{
			"client_token": token, "lease_duration": 3600, "renewable": true,
		}})
	})
	mux.HandleFunc("/v1/auth/kubernetes/login", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.logins++
		f.mu.Unlock()
		if body["role"] == "" || body["jwt"] == "" {
			http.Error(w, "missing role/jwt", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"auth": map[string]any{
			"client_token": "k8s-token", "lease_duration": 3600, "renewable": true,
		}})
	})
	mux.HandleFunc("/v1/auth/token/lookup-self", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"ttl": 3600, "renewable": true}})
	})
	mux.HandleFunc("/v1/auth/token/renew-self", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.renews++
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"auth": map[string]any{
			"lease_duration": 3600, "renewable": true,
		}})
	})
	mux.HandleFunc("/v1/pki/issue/forecast", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.issues++
		forbid := f.forbidFirstIssue && f.issues == 1
		f.mu.Unlock()
		if forbid {
			http.Error(w, `{"errors":["permission denied"]}`, http.StatusForbidden)
			return
		}
		if body["common_name"] == "" || body["ttl"] == "" {
			http.Error(w, "missing common_name/ttl", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"certificate": f.pki.certPEM,
			"private_key": f.pki.keyPEM,
			"ca_chain":    []string{f.pki.caPEM},
		}})
	})
	return mux
}

func (f *fakeVault) counts() (issues, logins, renews int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.issues, f.logins, f.renews
}

func newFakeVault(t *testing.T, notAfter time.Time) (*fakeVault, *httptest.Server) {
	t.Helper()
	f := &fakeVault{pki: newTestPKI(t, notAfter)}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	return f, srv
}

func TestNewClientValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{name: "no address", cfg: Config{Auth: AuthConfig{Token: "t"}}, wantErr: "forecast: vault: address is required"},
		{name: "no auth", cfg: Config{Addr: "http://vault:8200"}, wantErr: ErrNoAuth.Error()},
		{
			name:    "partial approle",
			cfg:     Config{Addr: "http://vault:8200", Auth: AuthConfig{RoleID: "r"}},
			wantErr: "forecast: vault: both FORECAST_VAULT_ROLE_ID and FORECAST_VAULT_SECRET_ID are required",
		},
		{name: "token", cfg: Config{Addr: "http://vault:8200", Auth: AuthConfig{Token: "t"}}},
		{name: "approle", cfg: Config{Addr: "http://vault:8200", Auth: AuthConfig{RoleID: "r", SecretID: "s"}}},
		{name: "kubernetes", cfg: Config{Addr: "http://vault:8200", Auth: AuthConfig{K8sRole: "r"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewClient(tc.cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("NewClient: %v", err)
				}
				return
			}
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestAuthMethodsAndPrecedence(t *testing.T) {
	jwtPath := filepath.Join(t.TempDir(), "jwt")
	if err := os.WriteFile(jwtPath, []byte("jwt-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, srv := newFakeVault(t, time.Now().Add(time.Hour))

	tests := []struct {
		name       string
		auth       AuthConfig
		wantLogins int
		wantToken  string
	}{
		{
			name:       "token wins over approle",
			auth:       AuthConfig{Token: "static", RoleID: "r", SecretID: "s"},
			wantLogins: 0,
			wantToken:  "static",
		},
		{
			name:       "approle",
			auth:       AuthConfig{RoleID: "r", SecretID: "s"},
			wantLogins: 1,
			wantToken:  "approle-token",
		},
		{
			name:       "kubernetes",
			auth:       AuthConfig{K8sRole: "r", K8sJWTPath: jwtPath},
			wantLogins: 1,
			wantToken:  "k8s-token",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, before, _ := f.counts()
			c, err := NewClient(Config{Addr: srv.URL, Auth: tc.auth})
			if err != nil {
				t.Fatal(err)
			}
			issued, err := c.IssueCertificate(context.Background(), "pki", "forecast", "gpx", time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			if issued.Certificate.Leaf == nil {
				t.Fatal("issued leaf not parsed")
			}
			token, err := c.ensureToken(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if token != tc.wantToken {
				t.Fatalf("token = %q want %q", token, tc.wantToken)
			}
			_, after, _ := f.counts()
			if after-before != tc.wantLogins {
				t.Fatalf("logins = %d want %d", after-before, tc.wantLogins)
			}
		})
	}
}

func TestIssueReloginsOnForbidden(t *testing.T) {
	f, srv := newFakeVault(t, time.Now().Add(time.Hour))
	f.forbidFirstIssue = true
	c, err := NewClient(Config{Addr: srv.URL, Auth: AuthConfig{RoleID: "r", SecretID: "s"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.IssueCertificate(context.Background(), "pki", "forecast", "gpx", time.Hour); err != nil {
		t.Fatal(err)
	}
	issues, logins, _ := f.counts()
	if issues != 2 || logins != 2 {
		t.Fatalf("issues=%d logins=%d, want 2 and 2 (initial login + relogin)", issues, logins)
	}
}

func TestManagerRotatesAndServesTLS(t *testing.T) {
	f, srv := newFakeVault(t, time.Now().Add(2*time.Second))
	c, err := NewClient(Config{Addr: srv.URL, Auth: AuthConfig{Token: "t"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := NewManager(c, "pki", "forecast", "gpx", time.Second)
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := m.Snapshot(); !ok {
		t.Fatal("no snapshot after Start")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		issues, _, _ := f.counts()
		if issues >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("issues = %d, want a re-issue", issues)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// A fresh manager (long-lived cert) serves mTLS: a client presenting the
	// issued certificate is accepted, a certless client is refused.
	f2, srv2 := newFakeVault(t, time.Now().Add(time.Hour))
	c2, err := NewClient(Config{Addr: srv2.URL, Auth: AuthConfig{Token: "t"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	serverMgr := NewManager(c2, "pki", "forecast", "gpx", time.Hour)
	if err := serverMgr.Start(ctx2); err != nil {
		t.Fatal(err)
	}

	httpSrv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	httpSrv.TLS = NewServerTLSConfig(serverMgr)
	httpSrv.StartTLS()
	t.Cleanup(httpSrv.Close)

	clientMgr := NewManager(c2, "pki", "forecast", "gpx", time.Hour)
	if err := clientMgr.Start(ctx2); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: NewClientTransport(clientMgr, "127.0.0.1", nil, false), Timeout: 5 * time.Second}
	resp, err := client.Get(httpSrv.URL)
	if err != nil {
		t.Fatalf("mTLS GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	certless := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f2.pki.pool, MinVersion: tls.VersionTLS12}}, Timeout: 5 * time.Second}
	if _, err := certless.Get(httpSrv.URL); err == nil {
		t.Fatal("certless client was accepted")
	}
}

func TestTransportBeforeIssue(t *testing.T) {
	tr := NewClientTransport(nil, "127.0.0.1", nil, false)
	req, _ := http.NewRequest(http.MethodGet, "https://127.0.0.1/", nil)
	if _, err := tr.RoundTrip(req); !errors.Is(err, ErrNoCertificate) {
		t.Fatalf("err = %v, want ErrNoCertificate", err)
	}
}
