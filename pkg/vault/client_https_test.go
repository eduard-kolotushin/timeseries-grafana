package vault

import (
	"strings"
	"testing"
	"time"
)

// A client certificate on a plain http Vault address is dead material: net/http
// does not run TLS, so the token would travel in clear while the configuration
// claims mTLS. The client must refuse the combination; plain http without mTLS
// stays allowed because Vault's own tooling accepts it.
func TestVaultClientMTLSRequiresHTTPS(t *testing.T) {
	pki := newTestPKI(t, time.Now().Add(time.Hour))
	certFile, keyFile, _ := writePair(t, pki)

	if _, err := NewClient(Config{
		Addr:           "http://vault:8200",
		Auth:           AuthConfig{Token: "t"},
		ClientMTLS:     true,
		ClientCertFile: certFile,
		ClientKeyFile:  keyFile,
	}); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("http with client mTLS: err=%v, want the https refusal", err)
	}
	if _, err := NewClient(Config{
		Addr: "http://vault:8200",
		Auth: AuthConfig{Token: "t"},
	}); err != nil {
		t.Fatalf("plain http without mTLS must stay allowed: %v", err)
	}
}
