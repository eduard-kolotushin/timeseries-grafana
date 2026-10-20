package vault

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// Resolved is one component-selection view of the Vault configuration. The
// three MTLS booleans are already defaulted: a flag that is unset means "on iff
// that component's PKI role is configured", and an explicit false turns the
// component off even when its role is present.
type Resolved struct {
	Client *Client

	PKIMount    string
	ComputeRole string
	PVVRole     string
	StoreRole   string

	ClientCN string
	PVVCN    string
	StoreCN  string

	ServerName string
	CertTTL    time.Duration

	ComputeMTLS bool
	PVVMTLS     bool
	StoreMTLS   bool
	VaultMTLS   bool
}

// ResolveEnv resolves the Vault configuration from process env only. It is the
// env-only counterpart of the plugin's storeLookup adapter, for processes that
// never read grafana.ini or jsonData (the compute service and gpx_forecast_migrate).
func ResolveEnv(getenv func(string) string) (*Resolved, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	get := func(key string) string { return strings.TrimSpace(getenv(key)) }

	addr := get("FORECAST_VAULT_ADDR")
	if addr == "" {
		addr = get("VAULT_ADDR")
	}
	if addr == "" {
		return nil, nil
	}
	token := get("FORECAST_VAULT_TOKEN")
	if token == "" {
		token = get("VAULT_TOKEN")
	}
	certFile := get("FORECAST_VAULT_CLIENT_CERT")
	keyFile := get("FORECAST_VAULT_CLIENT_KEY")
	vaultMTLS := parseBool(get("FORECAST_VAULT_MTLS"), certFile != "" || keyFile != "")
	client, err := NewClient(Config{
		Addr:      addr,
		Namespace: get("FORECAST_VAULT_NAMESPACE"),
		Auth: AuthConfig{
			Token:        token,
			RoleID:       get("FORECAST_VAULT_ROLE_ID"),
			SecretID:     get("FORECAST_VAULT_SECRET_ID"),
			AppRoleMount: get("FORECAST_VAULT_APPROLE_MOUNT"),
			K8sRole:      get("FORECAST_VAULT_K8S_ROLE"),
			K8sMount:     get("FORECAST_VAULT_K8S_MOUNT"),
			K8sJWTPath:   get("FORECAST_VAULT_K8S_JWT_PATH"),
		},
		CACertFile:     get("FORECAST_VAULT_CA_CERT"),
		TLSSkipVerify:  parseBool(get("FORECAST_VAULT_TLS_SKIP_VERIFY"), false),
		ClientMTLS:     vaultMTLS,
		ClientCertFile: certFile,
		ClientKeyFile:  keyFile,
	})
	if err != nil {
		return nil, err
	}
	computeRole := get("FORECAST_VAULT_PKI_ROLE")
	pvvRole := get("FORECAST_VAULT_PKI_PVV_ROLE")
	storeRole := get("FORECAST_VAULT_PKI_STORE_ROLE")
	computeMTLS := parseBool(get("FORECAST_COMPUTE_MTLS"), computeRole != "")
	pvvMTLS := parseBool(get("FORECAST_PVV_MTLS"), pvvRole != "")
	storeMTLS := parseBool(get("FORECAST_STORE_MTLS"), storeRole != "")
	if computeRole == "" && pvvRole == "" && storeRole == "" && !computeMTLS && !pvvMTLS && !storeMTLS {
		return nil, errNoComponentRole
	}
	ttl := parseDuration(get("FORECAST_VAULT_CERT_TTL"), DefaultCertTTL)
	if ttl < time.Minute {
		ttl = DefaultCertTTL
	}
	return &Resolved{
		Client:      client,
		PKIMount:    defaultString(get("FORECAST_VAULT_PKI_MOUNT"), DefaultPKIMount),
		ComputeRole: computeRole,
		PVVRole:     pvvRole,
		StoreRole:   storeRole,
		ClientCN:    defaultString(get("FORECAST_VAULT_CLIENT_CN"), DefaultClientCN),
		PVVCN:       defaultString(get("FORECAST_VAULT_PVV_CN"), DefaultPVVCN),
		StoreCN:     get("FORECAST_VAULT_STORE_CN"),
		ServerName:  get("FORECAST_COMPUTE_TLS_SERVER_NAME"),
		CertTTL:     ttl,
		ComputeMTLS: computeMTLS,
		PVVMTLS:     pvvMTLS,
		StoreMTLS:   storeMTLS,
		VaultMTLS:   vaultMTLS,
	}, nil
}

const (
	// DefaultCertTTL is the requested lifetime when none is configured.
	DefaultCertTTL = 24 * time.Hour

	// DefaultClientCN and DefaultPVVCN are the common names used when the
	// operator does not choose one.
	DefaultClientCN = "gpx-forecast"
	DefaultPVVCN    = "gpx-forecast-pvv"
)

var errNoComponentRole = errors.New("forecast: vault: no component role is configured (compute, pvv, store)")

// parseBool keeps the default for empty or unreadable flags, so a typo cannot
// silently turn a component's mTLS off.
func parseBool(s string, def bool) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return def
	}
	return v
}

func parseDuration(s string, def time.Duration) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	v, err := time.ParseDuration(s)
	if err != nil || v <= 0 {
		return def
	}
	return v
}
