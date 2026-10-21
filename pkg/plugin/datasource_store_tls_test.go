package plugin

import (
	"context"
	"testing"
)

// A Vault configuration error must not turn an mTLS store into a plaintext one:
// the resolved view is partial but still says the store asked for TLS, so the
// pool fails loudly instead of connecting without a certificate.
func TestDatasourceStoreTLSFailsClosedOnVaultConfigError(t *testing.T) {
	clearComputeEnv(t)
	t.Setenv("FORECAST_VAULT_PKI_STORE_ROLE", "forecast-pg") // no Vault address

	ds := &Datasource{}
	cfg, cancel := ds.storeTLS(context.Background(), nil, nil)
	if cancel != nil {
		t.Fatal("a failed resolution cannot own a manager")
	}
	if !cfg.Enabled {
		t.Fatalf("store TLS disabled after a vault configuration error: %+v", cfg)
	}
	if cfg.Manager != nil || cfg.Files.ClientConfigured() {
		t.Fatalf("a failed resolution must not invent a certificate source: %+v", cfg)
	}
}
