package plugin

import (
	"context"
	"encoding/json"
	"strings"
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
		wantVaultOn bool
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
			name: "vault client mTLS explicitly off ignores the file pair",
			env: map[string]string{
				"FORECAST_VAULT_ADDR":        "http://vault:8200",
				"FORECAST_VAULT_TOKEN":       "t",
				"FORECAST_VAULT_PKI_ROLE":    "forecast",
				"FORECAST_VAULT_CLIENT_CERT": "/does/not/exist.crt",
				"FORECAST_VAULT_CLIENT_KEY":  "/does/not/exist.key",
				"FORECAST_VAULT_MTLS":        "false",
			},
			wantMount: "pki", wantRole: "forecast", wantClient: defaultVaultClientCN,
			wantTTL: defaultVaultCertTTL, wantStoreCN: "overlay", wantCompute: true,
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
				got.ComputeMTLS != tc.wantCompute || got.PVVMTLS != tc.wantPVV || got.StoreMTLS != tc.wantStoreOn ||
				got.VaultMTLS != tc.wantVaultOn {
				t.Fatalf("config = %+v", got)
			}
		})
	}
}

func TestVaultLocalFileResolution(t *testing.T) {
	clearComputeEnv(t)
	t.Setenv("FORECAST_COMPUTE_CLIENT_CERT", "/c/compute.crt")
	t.Setenv("FORECAST_COMPUTE_CLIENT_KEY", "/c/compute.key")
	t.Setenv("FORECAST_COMPUTE_CA_CERT", "/c/ca.pem")
	t.Setenv("FORECAST_COMPUTE_SERVER_CERT", "/c/server.crt")
	t.Setenv("FORECAST_COMPUTE_SERVER_KEY", "/c/server.key")
	t.Setenv("FORECAST_STORE_CLIENT_CERT", "/c/store.crt")
	t.Setenv("FORECAST_STORE_CLIENT_KEY", "/c/store.key")
	t.Setenv("FORECAST_STORE_TLS_SERVER_NAME", "overlay-postgres")
	t.Setenv("FORECAST_PVV_CLIENT_CERT", "/c/pvv.crt")
	t.Setenv("FORECAST_PVV_CLIENT_KEY", "/c/pvv.key")
	t.Setenv("FORECAST_STORE_MTLS", "false")

	got, err := vaultFrom(context.Background(), backend.AppInstanceSettings{})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("local files without a Vault address must resolve")
	}
	if got.Client != nil {
		t.Fatal("no Vault address must mean no Vault client")
	}
	if !got.ComputeMTLS || !got.ComputeServerMTLS || !got.PVVMTLS || got.StoreMTLS {
		t.Fatalf("switches = compute:%v server:%v pvv:%v store:%v", got.ComputeMTLS, got.ComputeServerMTLS, got.PVVMTLS, got.StoreMTLS)
	}
	if got.ComputeFiles.ClientCertFile != "/c/compute.crt" || got.ComputeFiles.ServerKeyFile != "/c/server.key" ||
		got.PVVFiles.CAFile != "" || got.StoreFiles.ClientKeyFile != "/c/store.key" ||
		got.StoreServerName != "overlay-postgres" {
		t.Fatalf("files = compute:%+v pvv:%+v store:%+v server=%q", got.ComputeFiles, got.PVVFiles, got.StoreFiles, got.StoreServerName)
	}
	if got.StoreMTLS {
		t.Fatal("explicit store false must ignore the local pair")
	}

	t.Setenv("FORECAST_VAULT_PKI_ROLE", "compute")
	if _, err := vaultFrom(context.Background(), backend.AppInstanceSettings{}); err == nil || !strings.Contains(err.Error(), "address is required") {
		t.Fatalf("role without address: err=%v", err)
	}
}

func TestPVVModeFromVault(t *testing.T) {
	clearComputeEnv(t)
	mgr := &vault.Manager{}
	p := newGrafanaPoster(retrainConfig{PVVMTLS: true, PVVURL: "https://127.0.0.1:1", PVVManager: mgr})
	if !p.pvvMode() {
		t.Fatal("a vault manager must select PVV mode")
	}
	if p.pvvClient == nil || p.pvvInitErr != nil {
		t.Fatalf("pvv client = %v err = %v", p.pvvClient, p.pvvInitErr)
	}
	both := newGrafanaPoster(retrainConfig{
		PVVMTLS:       true,
		PVVURL:        "https://127.0.0.1:1",
		PVVManager:    mgr,
		PVVClientCert: "/does/not/exist",
		PVVClientKey:  "/does/not/exist",
	})
	if both.pvvInitErr != nil {
		t.Fatalf("vault must win over file paths: %v", both.pvvInitErr)
	}
}
