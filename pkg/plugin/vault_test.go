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
	storeSettings, _ := json.Marshal(map[string]any{
		"vaultAddr":         "http://store:8200",
		"vaultPkiStoreRole": "forecast-pg",
		"storeUser":         "forecast",
	})
	tests := []struct {
		name        string
		env         map[string]string
		cfg         map[string]string
		settings    backend.AppInstanceSettings
		wantNil     bool
		wantErr     bool
		wantMount   string
		wantRole    string
		wantPVVRole string
		wantStore   string
		wantStoreCN string
		wantClient  string
		wantTTL     time.Duration
		wantCompute bool
		wantPVV     bool
		wantStoreOn bool
	}{
		{name: "unset", wantNil: true},
		{
			name: "compute role selects compute only",
			env: map[string]string{
				"FORECAST_VAULT_ADDR":     "http://vault:8200",
				"FORECAST_VAULT_TOKEN":    "t",
				"FORECAST_VAULT_PKI_ROLE": "forecast",
			},
			wantMount: "pki", wantRole: "forecast", wantClient: defaultVaultClientCN,
			wantTTL: defaultVaultCertTTL, wantCompute: true,
			wantStoreCN: "overlay",
		},
		{
			name:      "bare VAULT_ADDR and VAULT_TOKEN fallback",
			env:       map[string]string{"VAULT_ADDR": "http://bare:8200", "VAULT_TOKEN": "bare-token", "FORECAST_VAULT_PKI_ROLE": "forecast"},
			wantMount: "pki", wantRole: "forecast", wantClient: defaultVaultClientCN,
			wantTTL: defaultVaultCertTTL, wantCompute: true, wantStoreCN: "overlay",
		},
		{
			name: "store role alone keeps compute and pvv off",
			env: map[string]string{
				"FORECAST_VAULT_ADDR":  "http://vault:8200",
				"FORECAST_VAULT_TOKEN": "t",
			},
			settings:    backend.AppInstanceSettings{JSONData: storeSettings},
			wantMount:   "pki",
			wantStore:   "forecast-pg",
			wantStoreCN: "forecast",
			wantClient:  defaultVaultClientCN,
			wantTTL:     defaultVaultCertTTL,
			wantStoreOn: true,
		},
		{
			name: "explicit false wins over a configured role",
			env: map[string]string{
				"FORECAST_VAULT_ADDR":         "http://vault:8200",
				"FORECAST_VAULT_TOKEN":        "t",
				"FORECAST_VAULT_PKI_ROLE":     "forecast",
				"FORECAST_COMPUTE_MTLS":       "false",
				"FORECAST_VAULT_PKI_PVV_ROLE": "pvv",
				"FORECAST_PVV_MTLS":           "false",
			},
			wantMount: "pki", wantRole: "forecast", wantPVVRole: "pvv", wantClient: defaultVaultClientCN,
			wantTTL: defaultVaultCertTTL, wantStoreCN: "overlay",
		},
		{
			name: "flag true without its role still resolves when another component has one",
			env: map[string]string{
				"FORECAST_VAULT_ADDR":           "http://vault:8200",
				"FORECAST_VAULT_TOKEN":          "t",
				"FORECAST_COMPUTE_MTLS":         "true",
				"FORECAST_VAULT_PKI_STORE_ROLE": "forecast-pg",
			},
			wantMount: "pki", wantClient: defaultVaultClientCN, wantTTL: defaultVaultCertTTL,
			wantStore: "forecast-pg", wantStoreCN: "overlay",
			wantCompute: true, wantStoreOn: true,
		},
		{
			name: "jsonData plus secure token plus ini mount",
			env:  map[string]string{"FORECAST_VAULT_TOKEN": ""},
			cfg:  map[string]string{"vault_pki_mount": "ini-pki"},
			settings: backend.AppInstanceSettings{
				JSONData:                jsonSettings,
				DecryptedSecureJSONData: map[string]string{"vaultToken": "secure-token"},
			},
			wantMount: "ini-pki", wantRole: "json-role", wantPVVRole: "json-pvv",
			wantClient: "json-client", wantTTL: 2 * time.Hour,
			wantCompute: true, wantPVV: true, wantStoreCN: "overlay",
		},
		{
			name:    "addr without any role",
			env:     map[string]string{"FORECAST_VAULT_ADDR": "http://vault:8200", "FORECAST_VAULT_TOKEN": "t"},
			wantErr: true,
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
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected a vault configuration error")
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
			if got.Client == nil || got.PKIMount != tc.wantMount || got.ComputeRole != tc.wantRole ||
				got.PVVRole != tc.wantPVVRole || got.StoreRole != tc.wantStore ||
				got.StoreCN != tc.wantStoreCN || got.ClientCN != tc.wantClient || got.CertTTL != tc.wantTTL ||
				got.ComputeMTLS != tc.wantCompute || got.PVVMTLS != tc.wantPVV || got.StoreMTLS != tc.wantStoreOn {
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
