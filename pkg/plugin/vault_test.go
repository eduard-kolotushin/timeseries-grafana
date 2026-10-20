package plugin

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/eduard-kolotushin/timeseries-grafana/pkg/vault"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

func TestVaultConfigResolution(t *testing.T) {
	jsonSettings, _ := json.Marshal(map[string]any{
		"vaultAddr":     "http://json:8200",
		"vaultPkiRole":  "json-role",
		"vaultPkiMount": "json-pki",
		"vaultClientCn": "json-client",
		"vaultCertTtl":  "2h",
		"vaultPvvRole":  "json-pvv",
	})
	tests := []struct {
		name        string
		env         map[string]string
		cfg         map[string]string
		settings    backend.AppInstanceSettings
		wantNil     bool
		wantAddr    string
		wantToken   string
		wantMount   string
		wantRole    string
		wantPVVRole string
		wantClient  string
		wantTTL     time.Duration
	}{
		{name: "unset", wantNil: true},
		{
			name:       "env defaults",
			env:        map[string]string{"FORECAST_VAULT_ADDR": "http://vault:8200", "FORECAST_VAULT_TOKEN": "t", "FORECAST_VAULT_PKI_ROLE": "forecast"},
			wantAddr:   "http://vault:8200",
			wantToken:  "t",
			wantMount:  "pki",
			wantRole:   "forecast",
			wantClient: defaultVaultClientCN,
			wantTTL:    defaultVaultCertTTL,
		},
		{
			name:     "bare VAULT_ADDR and VAULT_TOKEN fallback",
			env:      map[string]string{"VAULT_ADDR": "http://bare:8200", "VAULT_TOKEN": "bare-token", "FORECAST_VAULT_PKI_ROLE": "forecast"},
			wantAddr: "http://bare:8200", wantToken: "bare-token",
			wantMount: "pki", wantRole: "forecast", wantClient: defaultVaultClientCN, wantTTL: defaultVaultCertTTL,
		},
		{
			name: "jsonData plus secure token plus ini mount",
			env:  map[string]string{"FORECAST_VAULT_TOKEN": ""},
			cfg:  map[string]string{"vault_pki_mount": "ini-pki"},
			settings: backend.AppInstanceSettings{
				JSONData:                jsonSettings,
				DecryptedSecureJSONData: map[string]string{"vaultToken": "secure-token"},
			},
			wantAddr: "http://json:8200", wantToken: "secure-token",
			wantMount: "ini-pki", wantRole: "json-role", wantPVVRole: "json-pvv",
			wantClient: "json-client", wantTTL: 2 * time.Hour,
		},
		{
			name:    "addr without a role",
			env:     map[string]string{"FORECAST_VAULT_ADDR": "http://vault:8200", "FORECAST_VAULT_TOKEN": "t"},
			wantNil: false,
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
			got, err := vaultFrom(ctx, tc.settings)
			if tc.name == "addr without a role" {
				if err == nil {
					t.Fatal("expected a role error")
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
			if got.client == nil || got.pkiMount != tc.wantMount || got.pkiRole != tc.wantRole ||
				got.pvvRole != tc.wantPVVRole || got.clientCN != tc.wantClient || got.certTTL != tc.wantTTL {
				t.Fatalf("config = %+v", got)
			}
		})
	}
}

func TestPVVModeFromVault(t *testing.T) {
	clearComputeEnv(t)
	mgr := &vault.Manager{}
	p := newGrafanaPoster(retrainConfig{PVVURL: "https://127.0.0.1:1", PVVManager: mgr})
	if !p.pvvMode() {
		t.Fatal("a vault manager must select PVV mode")
	}
	if p.pvvClient == nil || p.pvvInitErr != nil {
		t.Fatalf("pvv client = %v err = %v", p.pvvClient, p.pvvInitErr)
	}
	both := newGrafanaPoster(retrainConfig{
		PVVURL:        "https://127.0.0.1:1",
		PVVManager:    mgr,
		PVVClientCert: "/does/not/exist",
		PVVClientKey:  "/does/not/exist",
	})
	if both.pvvInitErr != nil {
		t.Fatalf("vault must win over file paths: %v", both.pvvInitErr)
	}
}
