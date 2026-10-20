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
	defaultVaultCertTTL  = vault.DefaultCertTTL
	defaultVaultClientCN = vault.DefaultClientCN
	defaultVaultPVVCN    = vault.DefaultPVVCN
)

// vaultFrom resolves Vault settings from the app instance settings.
func vaultFrom(ctx context.Context, settings backend.AppInstanceSettings) (*vault.Resolved, error) {
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
// only for the two secrets, like computeToken and grafanaToken. The three
// component switches default to "that component's role is configured"; an
// explicit false disables a component even when its role is set.
func vaultFromLookup(look storeLookup, settings backend.AppInstanceSettings) (*vault.Resolved, error) {
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
	vaultCert := look.Get("FORECAST_VAULT_CLIENT_CERT", "VAULT_CLIENT_CERT", "vault_client_cert", "vaultClientCert")
	vaultKey := look.Get("FORECAST_VAULT_CLIENT_KEY", "VAULT_CLIENT_KEY", "vault_client_key", "vaultClientKey")
	vaultMTLS := parseBool(look.Get("FORECAST_VAULT_MTLS", "VAULT_MTLS", "vault_mtls", "vaultMtls"), vaultCert != "" || vaultKey != "")
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
		CACertFile:     look.Get("FORECAST_VAULT_CA_CERT", "VAULT_CA_CERT", "vault_ca_cert", "vaultCaCert"),
		TLSSkipVerify:  parseBool(look.Get("FORECAST_VAULT_TLS_SKIP_VERIFY", "VAULT_TLS_SKIP_VERIFY", "vault_tls_skip_verify", "vaultTlsSkipVerify"), false),
		ClientMTLS:     vaultMTLS,
		ClientCertFile: vaultCert,
		ClientKeyFile:  vaultKey,
	}
	client, err := vault.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	computeRole := look.Get("FORECAST_VAULT_PKI_ROLE", "VAULT_PKI_ROLE", "vault_pki_role", "vaultPkiRole")
	pvvRole := look.Get("FORECAST_VAULT_PKI_PVV_ROLE", "VAULT_PKI_PVV_ROLE", "vault_pki_pvv_role", "vaultPvvRole")
	storeRole := look.Get("FORECAST_VAULT_PKI_STORE_ROLE", "VAULT_PKI_STORE_ROLE", "vault_pki_store_role", "vaultPkiStoreRole")
	computeMTLS := parseBool(look.Get("FORECAST_COMPUTE_MTLS", "COMPUTE_MTLS", "compute_mtls", "computeMtls"), computeRole != "")
	pvvMTLS := parseBool(look.Get("FORECAST_PVV_MTLS", "PVV_MTLS", "pvv_mtls", "pvvMtls"), pvvRole != "")
	storeMTLS := parseBool(look.Get("FORECAST_STORE_MTLS", "STORE_MTLS", "store_mtls", "storeMtls"), storeRole != "")
	if computeRole == "" && pvvRole == "" && storeRole == "" && !computeMTLS && !pvvMTLS && !storeMTLS {
		return nil, errors.New("forecast: vault: no component role is configured (compute, pvv, store)")
	}
	ttl := parseDuration(look.Get("FORECAST_VAULT_CERT_TTL", "VAULT_CERT_TTL", "vault_cert_ttl", "vaultCertTtl"), defaultVaultCertTTL)
	if ttl < time.Minute {
		log.DefaultLogger.Warn("forecast: ignoring the Vault certificate TTL", "ttl", ttl, "default", defaultVaultCertTTL)
		ttl = defaultVaultCertTTL
	}
	storeCN := look.Get("FORECAST_VAULT_STORE_CN", "VAULT_STORE_CN", "vault_store_cn", "vaultStoreCn")
	if storeCN == "" {
		storeCN = look.Get("FORECAST_STORE_USER", "STORE_USER", "store_user", "storeUser")
	}
	if storeCN == "" {
		storeCN = "overlay"
	}
	return &vault.Resolved{
		Client:      client,
		PKIMount:    defaultString(look.Get("FORECAST_VAULT_PKI_MOUNT", "VAULT_PKI_MOUNT", "vault_pki_mount", "vaultPkiMount"), defaultVaultPKIMount),
		ComputeRole: computeRole,
		PVVRole:     pvvRole,
		StoreRole:   storeRole,
		ClientCN:    defaultString(look.Get("FORECAST_VAULT_CLIENT_CN", "VAULT_CLIENT_CN", "vault_client_cn", "vaultClientCn"), defaultVaultClientCN),
		PVVCN:       defaultString(look.Get("FORECAST_VAULT_PVV_CN", "VAULT_PVV_CN", "vault_pvv_cn", "vaultPvvCn"), defaultVaultPVVCN),
		StoreCN:     storeCN,
		ServerName:  look.Get("FORECAST_COMPUTE_TLS_SERVER_NAME", "COMPUTE_TLS_SERVER_NAME", "compute_tls_server_name", "computeTlsServerName"),
		CertTTL:     ttl,
		ComputeMTLS: computeMTLS,
		PVVMTLS:     pvvMTLS,
		StoreMTLS:   storeMTLS,
		VaultMTLS:   vaultMTLS,
	}, nil
}

func defaultString(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
