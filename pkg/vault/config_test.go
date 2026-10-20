package vault

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func envGetter(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolveEnv(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantNil   bool
		wantErr   string
		compute   bool
		pvv       bool
		store     bool
		wantStore string
		wantCN    string
	}{
		{name: "no address", env: map[string]string{}, wantNil: true},
		{
			name:    "bare address and token",
			env:     map[string]string{"VAULT_ADDR": "http://v:8200", "VAULT_TOKEN": "t", "FORECAST_VAULT_PKI_ROLE": "compute"},
			compute: true,
		},
		{
			name: "all roles select all components",
			env: map[string]string{
				"FORECAST_VAULT_ADDR":           "http://v:8200",
				"FORECAST_VAULT_TOKEN":          "t",
				"FORECAST_VAULT_PKI_ROLE":       "compute",
				"FORECAST_VAULT_PKI_PVV_ROLE":   "pvv",
				"FORECAST_VAULT_PKI_STORE_ROLE": "pg",
				"FORECAST_VAULT_STORE_CN":       "forecast",
			},
			compute: true, pvv: true, store: true, wantStore: "pg", wantCN: "forecast",
		},
		{
			name: "store role alone keeps the others off",
			env: map[string]string{
				"FORECAST_VAULT_ADDR":           "http://v:8200",
				"FORECAST_VAULT_TOKEN":          "t",
				"FORECAST_VAULT_PKI_STORE_ROLE": "pg",
			},
			store: true, wantStore: "pg",
		},
		{
			name: "explicit false wins",
			env: map[string]string{
				"FORECAST_VAULT_ADDR":           "http://v:8200",
				"FORECAST_VAULT_TOKEN":          "t",
				"FORECAST_VAULT_PKI_ROLE":       "compute",
				"FORECAST_VAULT_PKI_STORE_ROLE": "pg",
				"FORECAST_COMPUTE_MTLS":         "false",
			},
			store: true, wantStore: "pg",
		},
		{
			name: "address with no role is an error",
			env: map[string]string{
				"FORECAST_VAULT_ADDR":  "http://v:8200",
				"FORECAST_VAULT_TOKEN": "t",
			},
			wantErr: "no component role is configured (compute, pvv, store)",
		},
		{
			name:    "role without an address is an error",
			env:     map[string]string{"FORECAST_VAULT_PKI_ROLE": "compute"},
			wantErr: "address is required when a component role is configured",
		},
		{
			name: "compute local pair needs no Vault address",
			env: map[string]string{
				"FORECAST_COMPUTE_CLIENT_CERT": "/c",
				"FORECAST_COMPUTE_CLIENT_KEY":  "/k",
				"FORECAST_COMPUTE_CA_CERT":     "/ca",
			},
			compute: true,
		},
		{
			name: "compute server pair alone selects the listener",
			env: map[string]string{
				"FORECAST_COMPUTE_SERVER_CERT": "/sc",
				"FORECAST_COMPUTE_SERVER_KEY":  "/sk",
			},
			compute: true,
		},
		{
			name: "pvv local pair needs no Vault address",
			env: map[string]string{
				"FORECAST_PVV_CLIENT_CERT": "/c",
				"FORECAST_PVV_CLIENT_KEY":  "/k",
			},
			pvv: true,
		},
		{
			name: "store local pair needs no Vault address",
			env: map[string]string{
				"FORECAST_STORE_CLIENT_CERT": "/c",
				"FORECAST_STORE_CLIENT_KEY":  "/k",
			},
			store: true,
		},
		{
			name: "explicit store off keeps local files ignored",
			env: map[string]string{
				"FORECAST_STORE_CLIENT_CERT": "/c",
				"FORECAST_STORE_CLIENT_KEY":  "/k",
				"FORECAST_STORE_MTLS":        "false",
			},
		},
		{
			name: "address with a local source is not a role error",
			env: map[string]string{
				"FORECAST_VAULT_ADDR":          "http://v:8200",
				"FORECAST_VAULT_TOKEN":         "t",
				"FORECAST_COMPUTE_CLIENT_CERT": "/c",
				"FORECAST_COMPUTE_CLIENT_KEY":  "/k",
			},
			compute: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveEnv(envGetter(tc.env))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if tc.wantNil {
				if got != nil || err != nil {
					t.Fatalf("got %+v err %v, want nil", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.ComputeMTLS != tc.compute || got.PVVMTLS != tc.pvv || got.StoreMTLS != tc.store {
				t.Fatalf("switches = compute:%v pvv:%v store:%v", got.ComputeMTLS, got.PVVMTLS, got.StoreMTLS)
			}
			if tc.wantStore != "" && got.StoreRole != tc.wantStore {
				t.Fatalf("store role = %q", got.StoreRole)
			}
			if tc.wantCN != "" && got.StoreCN != tc.wantCN {
				t.Fatalf("store CN = %q", got.StoreCN)
			}
		})
	}
}

func TestNewFileTLSConfigs(t *testing.T) {
	pki := newTestPKI(t, time.Now().Add(time.Hour))
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	caPath := filepath.Join(dir, "ca.pem")
	for path, data := range map[string]string{certPath: pki.certPEM, keyPath: pki.keyPEM, caPath: pki.caPEM} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files := FileTLS{ClientCertFile: certPath, ClientKeyFile: keyPath, CAFile: caPath}
	client, err := NewFileClientTLSConfig(files, "forecast.local")
	if err != nil {
		t.Fatal(err)
	}
	if client.ServerName != "forecast.local" || len(client.Certificates) != 1 || client.RootCAs == nil {
		t.Fatalf("client config = %+v", client)
	}
	server, err := NewFileServerTLSConfig(FileTLS{ServerCertFile: certPath, ServerKeyFile: keyPath, CAFile: caPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(server.Certificates) != 1 || server.ClientAuth != tls.RequireAndVerifyClientCert || server.ClientCAs == nil {
		t.Fatalf("server config = %+v", server)
	}

	if _, err := NewFileClientTLSConfig(FileTLS{ClientCertFile: certPath}, ""); err == nil {
		t.Fatal("a lone client certificate must fail")
	}
	if _, err := NewFileServerTLSConfig(FileTLS{ServerCertFile: certPath, ServerKeyFile: keyPath}); err == nil {
		t.Fatal("a listener without a client CA must fail")
	}
	if _, err := NewFileClientTLSConfig(FileTLS{ClientCertFile: "-----BEGIN CERTIFICATE-----", ClientKeyFile: keyPath}, ""); err == nil || !strings.Contains(err.Error(), "file path") {
		t.Fatalf("PEM content: err=%v", err)
	}
}

func TestNewClientTLSConfig(t *testing.T) {
	if _, err := NewClientTLSConfig(nil, ""); err == nil {
		t.Fatal("nil manager must fail")
	}
	f, srv := newFakeVault(t, time.Now().Add(time.Hour))
	c, err := NewClient(Config{Addr: srv.URL, Auth: AuthConfig{Token: "t"}})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(c, "pki", "forecast", "gpx", time.Hour)
	if _, err := NewClientTLSConfig(m, "host"); err == nil {
		t.Fatal("manager without an issue must fail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cfg, err := NewClientTLSConfig(m, "host")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerName != "host" || cfg.RootCAs == nil || len(cfg.RootCAs.Subjects()) == 0 {
		t.Fatalf("config = %+v", cfg)
	}
	first, err := cfg.GetClientCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Leaf == nil {
		first.Leaf, _ = x509.ParseCertificate(first.Certificate[0])
	}

	rotated := newTestPKI(t, time.Now().Add(time.Hour))
	f.mu.Lock()
	f.nextPKI = &rotated
	f.mu.Unlock()
	if err := m.issue(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := cfg.GetClientCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Leaf == nil {
		second.Leaf, _ = x509.ParseCertificate(second.Certificate[0])
	}
	if bytes.Equal(first.Certificate[0], second.Certificate[0]) {
		t.Fatal("GetClientCertificate did not pick up the rotated leaf")
	}
}
