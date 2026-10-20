package vault

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writePair(t *testing.T, pki testPKI) (certFile, keyFile, caFile string) {
	t.Helper()
	dir := t.TempDir()
	certFile = filepath.Join(dir, "client.crt")
	keyFile = filepath.Join(dir, "client.key")
	caFile = filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(certFile, []byte(pki.certPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte(pki.keyPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caFile, []byte(pki.caPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile, caFile
}

func TestResolveEnvVaultMTLS(t *testing.T) {
	pki := newTestPKI(t, time.Now().Add(time.Hour))
	certFile, keyFile, _ := writePair(t, pki)
	base := map[string]string{
		"FORECAST_VAULT_ADDR":        "https://vault.example:8200",
		"FORECAST_VAULT_TOKEN":       "t",
		"FORECAST_VAULT_PKI_ROLE":    "compute",
		"FORECAST_VAULT_CLIENT_CERT": certFile,
		"FORECAST_VAULT_CLIENT_KEY":  keyFile,
	}
	tests := []struct {
		name      string
		env       map[string]string
		wantMTLS  bool
		wantError string
	}{
		{name: "pair defaults on", env: base, wantMTLS: true},
		{
			name: "explicit false ignores the pair",
			env:  map[string]string{"FORECAST_VAULT_MTLS": "false"},
			// merged below
		},
		{
			name:      "half a pair is an error",
			env:       map[string]string{"FORECAST_VAULT_CLIENT_KEY": ""},
			wantError: "both FORECAST_VAULT_CLIENT_CERT and FORECAST_VAULT_CLIENT_KEY are required",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			for k, v := range base {
				env[k] = v
			}
			for k, v := range tc.env {
				env[k] = v
			}
			got, err := ResolveEnv(envGetter(env))
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("err = %v, want %q", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.VaultMTLS != tc.wantMTLS {
				t.Fatalf("VaultMTLS = %v, want %v", got.VaultMTLS, tc.wantMTLS)
			}
		})
	}
}

func TestVaultEndpointRequiresClientCertificate(t *testing.T) {
	pki := newTestPKI(t, time.Now().Add(time.Hour))
	certFile, keyFile, caFile := writePair(t, pki)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{pki.cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pki.pool,
	}
	srv.StartTLS()
	defer srv.Close()

	withCert, err := NewClient(Config{
		Addr:           srv.URL,
		Auth:           AuthConfig{Token: "t"},
		CACertFile:     caFile,
		ClientMTLS:     true,
		ClientCertFile: certFile,
		ClientKeyFile:  keyFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if status, _, err := withCert.do(context.Background(), http.MethodGet, "/health", "", nil); err != nil || status != http.StatusOK {
		t.Fatalf("mTLS request status=%d err=%v", status, err)
	}

	withoutCert, err := NewClient(Config{
		Addr:           srv.URL,
		Auth:           AuthConfig{Token: "t"},
		CACertFile:     caFile,
		ClientMTLS:     false,
		ClientCertFile: certFile,
		ClientKeyFile:  keyFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := withoutCert.do(context.Background(), http.MethodGet, "/health", "", nil); err == nil {
		t.Fatal("explicitly disabled client mTLS must be refused by a server requiring a certificate")
	}
}

func TestNewClientVaultMTLSErrors(t *testing.T) {
	pki := newTestPKI(t, time.Now().Add(time.Hour))
	certFile, _, caFile := writePair(t, pki)
	_, err := NewClient(Config{
		Addr:           "https://vault.example:8200",
		Auth:           AuthConfig{Token: "t"},
		CACertFile:     caFile,
		ClientMTLS:     true,
		ClientCertFile: certFile,
	})
	if err == nil || !strings.Contains(err.Error(), "both FORECAST_VAULT_CLIENT_CERT and FORECAST_VAULT_CLIENT_KEY are required") {
		t.Fatalf("err = %v", err)
	}
}
