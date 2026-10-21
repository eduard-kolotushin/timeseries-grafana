package plugin

import (
	"context"
	"strings"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// A role without an address is a configuration error, but the store still asked
// for mTLS: the partial view must carry that switch (and the role) so the store
// layer can fail closed instead of connecting in plaintext.
func TestVaultConfigErrorKeepsTheComponentView(t *testing.T) {
	clearComputeEnv(t)
	t.Setenv("FORECAST_VAULT_PKI_STORE_ROLE", "forecast-pg")

	got, err := vaultFrom(context.Background(), backend.AppInstanceSettings{})
	if err == nil || !strings.Contains(err.Error(), "address is required") {
		t.Fatalf("err=%v, want the address-required error", err)
	}
	if got == nil {
		t.Fatal("a configuration error must still return the partial view")
	}
	if !got.StoreMTLS || got.StoreRole != "forecast-pg" || got.Client != nil {
		t.Fatalf("partial view = %+v", got)
	}
}
