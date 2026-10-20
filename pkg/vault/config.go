package vault

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// FileTLS is one component's local certificate material. A Vault PKI role wins
// over these paths; they are the fallback for a deployment that cannot use
// Vault. Server fields exist for the compute listener, whose certificate is
// independent of the client certificate its plugin presents.
type FileTLS struct {
	ClientCertFile string
	ClientKeyFile  string
	CAFile         string
	ServerCertFile string
	ServerKeyFile  string
	SkipVerify     bool
}

// ClientConfigured reports whether either half of the client pair is set, so a
// lone half reaches the loader and fails loudly instead of silently disabling
// mTLS.
func (f FileTLS) ClientConfigured() bool {
	return f.ClientCertFile != "" || f.ClientKeyFile != ""
}

// ServerConfigured reports whether either half of the listener pair is set.
func (f FileTLS) ServerConfigured() bool {
	return f.ServerCertFile != "" || f.ServerKeyFile != ""
}

// Resolved is one component-selection view of the Vault configuration. The
// component MTLS booleans are already defaulted: a flag that is unset means
// "on iff that component's PKI role or local certificate material is
// configured", and an explicit false turns the component off even when both
// are present. Client is nil when no Vault address is configured and the
// components run from files.
type Resolved struct {
	Client *Client

	PKIMount    string
	ComputeRole string
	PVVRole     string
	StoreRole   string

	ClientCN string
	PVVCN    string
	StoreCN  string

	// ServerName is the compute server name / certificate CN. StoreServerName
	// overrides the pgx host for local store certificates.
	ServerName      string
	StoreServerName string
	CertTTL         time.Duration

	ComputeFiles FileTLS
	PVVFiles     FileTLS
	StoreFiles   FileTLS

	ComputeMTLS bool
	PVVMTLS     bool
	StoreMTLS   bool
	VaultMTLS   bool
	// ComputeServerMTLS is the compute listener's view: the same on/off switch,
	// defaulted from the compute role or either compute pair, because a client
	// pair in the process env still means the hop is expected to be TLS.
	ComputeServerMTLS bool
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
	computeRole := get("FORECAST_VAULT_PKI_ROLE")
	pvvRole := get("FORECAST_VAULT_PKI_PVV_ROLE")
	storeRole := get("FORECAST_VAULT_PKI_STORE_ROLE")
	if addr == "" && (computeRole != "" || pvvRole != "" || storeRole != "") {
		return nil, errors.New("forecast: vault: address is required when a component role is configured")
	}

	computeFiles := FileTLS{
		ClientCertFile: get("FORECAST_COMPUTE_CLIENT_CERT"),
		ClientKeyFile:  get("FORECAST_COMPUTE_CLIENT_KEY"),
		CAFile:         get("FORECAST_COMPUTE_CA_CERT"),
		ServerCertFile: get("FORECAST_COMPUTE_SERVER_CERT"),
		ServerKeyFile:  get("FORECAST_COMPUTE_SERVER_KEY"),
	}
	pvvFiles := FileTLS{
		ClientCertFile: get("FORECAST_PVV_CLIENT_CERT"),
		ClientKeyFile:  get("FORECAST_PVV_CLIENT_KEY"),
		CAFile:         get("FORECAST_PVV_CA_CERT"),
		SkipVerify:     parseBool(get("FORECAST_PVV_TLS_SKIP_VERIFY"), false),
	}
	storeFiles := FileTLS{
		ClientCertFile: get("FORECAST_STORE_CLIENT_CERT"),
		ClientKeyFile:  get("FORECAST_STORE_CLIENT_KEY"),
		CAFile:         get("FORECAST_STORE_CA_CERT"),
	}

	// With no Vault address the components may still run from local files; only
	// a completely unconfigured process is nil.
	if addr == "" && !computeFiles.ClientConfigured() && !computeFiles.ServerConfigured() &&
		!pvvFiles.ClientConfigured() && !storeFiles.ClientConfigured() &&
		get("FORECAST_COMPUTE_MTLS") == "" && get("FORECAST_PVV_MTLS") == "" && get("FORECAST_STORE_MTLS") == "" {
		return nil, nil
	}

	computeMTLS := parseBool(get("FORECAST_COMPUTE_MTLS"), computeRole != "" || computeFiles.ClientConfigured() || computeFiles.ServerConfigured())
	pvvMTLS := parseBool(get("FORECAST_PVV_MTLS"), pvvRole != "" || pvvFiles.ClientConfigured())
	storeMTLS := parseBool(get("FORECAST_STORE_MTLS"), storeRole != "" || storeFiles.ClientConfigured())
	// The listener uses the same switch and the same default (a role or either
	// pair in this process's env means the hop is expected to be TLS).
	computeServerMTLS := computeMTLS
	if addr != "" && computeRole == "" && pvvRole == "" && storeRole == "" &&
		!computeMTLS && !pvvMTLS && !storeMTLS && !computeFiles.ClientConfigured() &&
		!pvvFiles.ClientConfigured() && !storeFiles.ClientConfigured() {
		return nil, errNoComponentRole
	}

	ttl := parseDuration(get("FORECAST_VAULT_CERT_TTL"), DefaultCertTTL)
	if ttl < time.Minute {
		ttl = DefaultCertTTL
	}

	var client *Client
	vaultMTLS := false
	if addr != "" {
		token := get("FORECAST_VAULT_TOKEN")
		if token == "" {
			token = get("VAULT_TOKEN")
		}
		certFile := get("FORECAST_VAULT_CLIENT_CERT")
		keyFile := get("FORECAST_VAULT_CLIENT_KEY")
		vaultMTLS = parseBool(get("FORECAST_VAULT_MTLS"), certFile != "" || keyFile != "")
		var err error
		client, err = NewClient(Config{
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

		ServerName:      get("FORECAST_COMPUTE_TLS_SERVER_NAME"),
		StoreServerName: get("FORECAST_STORE_TLS_SERVER_NAME"),
		CertTTL:         ttl,

		ComputeFiles: computeFiles,
		PVVFiles:     pvvFiles,
		StoreFiles:   storeFiles,

		ComputeMTLS:       computeMTLS,
		PVVMTLS:           pvvMTLS,
		StoreMTLS:         storeMTLS,
		VaultMTLS:         vaultMTLS,
		ComputeServerMTLS: computeServerMTLS,
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
