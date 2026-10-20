package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/eduard-kolotushin/timeseries-grafana/pkg/vault"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

const (
	defaultVaultPKIMount = vault.DefaultPKIMount
	defaultVaultCertTTL  = 24 * time.Hour
	defaultVaultClientCN = "gpx-forecast"
	defaultVaultPVVCN    = "gpx-forecast-pvv"
)

// vaultConfig is the resolved Vault PKI configuration shared by the compute
// client, the compute listener and the PVV retrain transport. A nil *vaultConfig
// means Vault is not configured, so every caller keeps its pre-v19 shape.
type vaultConfig struct {
	client     *vault.Client
	pkiMount   string
	pkiRole    string
	pvvRole    string
	clientCN   string
	pvvCN      string
	certTTL    time.Duration
	serverName string
}

// vaultFrom resolves Vault settings from the app instance settings.
func vaultFrom(ctx context.Context, settings backend.AppInstanceSettings) (*vaultConfig, error) {
	jd := map[string]any{}
	if len(settings.JSONData) > 0 {
		_ = json.Unmarshal(settings.JSONData, &jd)
	}
	look := storeLookup{
		getenv: os.Getenv,
		cfg:    backend.GrafanaConfigFromContext(ctx),
		json:   jd,
	}
	return vaultFromLookup(look, settings)
}

// vaultFromLookup resolves Vault settings through storeLookup's ordinary chain,
// with the bare VAULT_ADDR / VAULT_TOKEN variables as a last env fallback (they
// are the names Vault's own tooling exports). Secure JSON values are consulted
// only for the two secrets, like computeToken and grafanaToken.
func vaultFromLookup(look storeLookup, settings backend.AppInstanceSettings) (*vaultConfig, error) {
	getenv := look.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	addr := look.Get("FORECAST_VAULT_ADDR", "VAULT_ADDR", "vault_addr", "vaultAddr")
	if addr == "" {
		addr = strings.TrimSpace(getenv("VAULT_ADDR"))
	}
	if addr == "" {
		return nil, nil
	}
	token := look.Get("FORECAST_VAULT_TOKEN", "VAULT_TOKEN", "vault_token", "vaultToken")
	if token == "" {
		token = strings.TrimSpace(getenv("VAULT_TOKEN"))
	}
	secretID := look.Get("FORECAST_VAULT_SECRET_ID", "VAULT_SECRET_ID", "vault_secret_id", "vaultSecretId")
	if settings.DecryptedSecureJSONData != nil {
		if token == "" {
			token = strings.TrimSpace(settings.DecryptedSecureJSONData["vaultToken"])
		}
		if secretID == "" {
			secretID = strings.TrimSpace(settings.DecryptedSecureJSONData["vaultSecretId"])
		}
	}
	cfg := vault.Config{
		Addr:      addr,
		Namespace: look.Get("FORECAST_VAULT_NAMESPACE", "VAULT_NAMESPACE", "vault_namespace", "vaultNamespace"),
		Auth: vault.AuthConfig{
			Token:        token,
			RoleID:       look.Get("FORECAST_VAULT_ROLE_ID", "VAULT_ROLE_ID", "vault_role_id", "vaultRoleId"),
			SecretID:     secretID,
			AppRoleMount: look.Get("FORECAST_VAULT_APPROLE_MOUNT", "VAULT_APPROLE_MOUNT", "vault_approle_mount", "vaultApproleMount"),
			K8sRole:      look.Get("FORECAST_VAULT_K8S_ROLE", "VAULT_K8S_ROLE", "vault_k8s_role", "vaultK8sRole"),
			K8sMount:     look.Get("FORECAST_VAULT_K8S_MOUNT", "VAULT_K8S_MOUNT", "vault_k8s_mount", "vaultK8sMount"),
			K8sJWTPath:   look.Get("FORECAST_VAULT_K8S_JWT_PATH", "VAULT_K8S_JWT_PATH", "vault_k8s_jwt_path", "vaultK8sJwtPath"),
		},
		CACertFile:    look.Get("FORECAST_VAULT_CA_CERT", "VAULT_CA_CERT", "vault_ca_cert", "vaultCaCert"),
		TLSSkipVerify: parseBool(look.Get("FORECAST_VAULT_TLS_SKIP_VERIFY", "VAULT_TLS_SKIP_VERIFY", "vault_tls_skip_verify", "vaultTlsSkipVerify"), false),
	}
	client, err := vault.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	pkiRole := look.Get("FORECAST_VAULT_PKI_ROLE", "VAULT_PKI_ROLE", "vault_pki_role", "vaultPkiRole")
	pvvRole := look.Get("FORECAST_VAULT_PKI_PVV_ROLE", "VAULT_PKI_PVV_ROLE", "vault_pki_pvv_role", "vaultPvvRole")
	if pkiRole == "" && pvvRole == "" {
		return nil, errors.New("forecast: vault: FORECAST_VAULT_PKI_ROLE is required when FORECAST_VAULT_ADDR is set")
	}
	ttl := parseDuration(look.Get("FORECAST_VAULT_CERT_TTL", "VAULT_CERT_TTL", "vault_cert_ttl", "vaultCertTtl"), defaultVaultCertTTL)
	if ttl < time.Minute {
		log.DefaultLogger.Warn("forecast: ignoring the Vault certificate TTL", "ttl", ttl, "default", defaultVaultCertTTL)
		ttl = defaultVaultCertTTL
	}
	return &vaultConfig{
		client:     client,
		pkiMount:   defaultString(look.Get("FORECAST_VAULT_PKI_MOUNT", "VAULT_PKI_MOUNT", "vault_pki_mount", "vaultPkiMount"), defaultVaultPKIMount),
		pkiRole:    pkiRole,
		pvvRole:    pvvRole,
		clientCN:   defaultString(look.Get("FORECAST_VAULT_CLIENT_CN", "VAULT_CLIENT_CN", "vault_client_cn", "vaultClientCn"), defaultVaultClientCN),
		pvvCN:      defaultString(look.Get("FORECAST_VAULT_PVV_CN", "VAULT_PVV_CN", "vault_pvv_cn", "vaultPvvCn"), defaultVaultPVVCN),
		certTTL:    ttl,
		serverName: look.Get("FORECAST_COMPUTE_TLS_SERVER_NAME", "COMPUTE_TLS_SERVER_NAME", "compute_tls_server_name", "computeTlsServerName"),
	}, nil
}

func defaultString(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
