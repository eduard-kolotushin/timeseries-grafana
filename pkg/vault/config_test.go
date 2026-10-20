package vault

import (
	"bytes"
	"context"
	"crypto/x509"
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
