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

// vaultFromLookup resolves Vault settings and local certificate paths through
// storeLookup's ordinary chain, with the bare VAULT_ADDR / VAULT_TOKEN variables
// as a last env fallback (they are the names Vault's own tooling exports). Secure
// JSON values are consulted only for the two secrets, like computeToken and
// grafanaToken. The three component switches default to "that component's Vault
// role or local certificate path is configured"; an explicit false disables a
// component even when either source is set. A nil Client with local files means
// the process runs mTLS without Vault. A configuration error returns a partial
// Resolved — switches, roles, files and names set, Client nil — so a caller can
// fail closed instead of falling back to plaintext.
func vaultFromLookup(look storeLookup, settings backend.AppInstanceSettings) (*vault.Resolved, error) {
	getenv := look.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	addr := look.Get("FORECAST_VAULT_ADDR", "VAULT_ADDR", "vault_addr", "vaultAddr")
	if addr == "" {
		addr = strings.TrimSpace(getenv("VAULT_ADDR"))
	}
	computeRole := look.Get("FORECAST_VAULT_PKI_ROLE", "VAULT_PKI_ROLE", "vault_pki_role", "vaultPkiRole")
	pvvRole := look.Get("FORECAST_VAULT_PKI_PVV_ROLE", "VAULT_PKI_PVV_ROLE", "vault_pki_pvv_role", "vaultPvvRole")
	storeRole := look.Get("FORECAST_VAULT_PKI_STORE_ROLE", "VAULT_PKI_STORE_ROLE", "vault_pki_store_role", "vaultPkiStoreRole")

	computeFiles := vault.FileTLS{
		ClientCertFile: look.Get("FORECAST_COMPUTE_CLIENT_CERT", "COMPUTE_CLIENT_CERT", "compute_client_cert", "computeClientCert"),
		ClientKeyFile:  look.Get("FORECAST_COMPUTE_CLIENT_KEY", "COMPUTE_CLIENT_KEY", "compute_client_key", "computeClientKey"),
		CAFile:         look.Get("FORECAST_COMPUTE_CA_CERT", "COMPUTE_CA_CERT", "compute_ca_cert", "computeCaCert"),
		ServerCertFile: look.Get("FORECAST_COMPUTE_SERVER_CERT", "COMPUTE_SERVER_CERT", "compute_server_cert", "computeServerCert"),
		ServerKeyFile:  look.Get("FORECAST_COMPUTE_SERVER_KEY", "COMPUTE_SERVER_KEY", "compute_server_key", "computeServerKey"),
	}
	storeFiles := vault.FileTLS{
		ClientCertFile: look.Get("FORECAST_STORE_CLIENT_CERT", "STORE_CLIENT_CERT", "store_client_cert", "storeClientCert"),
		ClientKeyFile:  look.Get("FORECAST_STORE_CLIENT_KEY", "STORE_CLIENT_KEY", "store_client_key", "storeClientKey"),
		CAFile:         look.Get("FORECAST_STORE_CA_CERT", "STORE_CA_CERT", "store_ca_cert", "storeCaCert"),
	}
	pvvFiles := vault.FileTLS{
		ClientCertFile: look.Get("FORECAST_PVV_CLIENT_CERT", "PVV_CLIENT_CERT", "pvv_client_cert", "pvvClientCert"),
		ClientKeyFile:  look.Get("FORECAST_PVV_CLIENT_KEY", "PVV_CLIENT_KEY", "pvv_client_key", "pvvClientKey"),
		CAFile:         look.Get("FORECAST_PVV_CA_CERT", "PVV_CA_CERT", "pvv_ca_cert", "pvvCaCert"),
		SkipVerify:     parseBool(look.Get("FORECAST_PVV_TLS_SKIP_VERIFY", "PVV_TLS_SKIP_VERIFY", "pvv_tls_skip_verify", "pvvTlsSkipVerify"), false),
	}

	computeFlag := look.Get("FORECAST_COMPUTE_MTLS", "COMPUTE_MTLS", "compute_mtls", "computeMtls")
	pvvFlag := look.Get("FORECAST_PVV_MTLS", "PVV_MTLS", "pvv_mtls", "pvvMtls")
	storeFlag := look.Get("FORECAST_STORE_MTLS", "STORE_MTLS", "store_mtls", "storeMtls")
	computeMTLS := parseBool(computeFlag, computeRole != "" || computeFiles.ClientConfigured() || computeFiles.ServerConfigured())
	pvvMTLS := parseBool(pvvFlag, pvvRole != "" || pvvFiles.ClientConfigured())
	storeMTLS := parseBool(storeFlag, storeRole != "" || storeFiles.ClientConfigured())

	if addr == "" && computeRole == "" && pvvRole == "" && storeRole == "" &&
		!computeFiles.ClientConfigured() && !computeFiles.ServerConfigured() &&
		!storeFiles.ClientConfigured() && !pvvFiles.ClientConfigured() && computeFlag == "" && pvvFlag == "" && storeFlag == "" {
		return nil, nil
	}
	// The component view exists before the last configuration checks: a caller
	// that receives an error still knows whether its store asked for mTLS, so the
	// pool can refuse a plaintext connection instead of downgrading.
	resolved := &vault.Resolved{
		PKIMount:    defaultString(look.Get("FORECAST_VAULT_PKI_MOUNT", "VAULT_PKI_MOUNT", "vault_pki_mount", "vaultPkiMount"), defaultVaultPKIMount),
		ComputeRole: computeRole,
		PVVRole:     pvvRole,
		StoreRole:   storeRole,
		ClientCN:    defaultString(look.Get("FORECAST_VAULT_CLIENT_CN", "VAULT_CLIENT_CN", "vault_client_cn", "vaultClientCn"), defaultVaultClientCN),
		PVVCN:       defaultString(look.Get("FORECAST_VAULT_PVV_CN", "VAULT_PVV_CN", "vault_pvv_cn", "vaultPvvCn"), defaultVaultPVVCN),
		ServerName:  look.Get("FORECAST_COMPUTE_TLS_SERVER_NAME", "COMPUTE_TLS_SERVER_NAME", "compute_tls_server_name", "computeTlsServerName"),
		// A local store certificate is verified against the pgx host unless this
		// override names the certificate's SAN.
		StoreServerName:   look.Get("FORECAST_STORE_TLS_SERVER_NAME", "STORE_TLS_SERVER_NAME", "store_tls_server_name", "storeTlsServerName"),
		ComputeFiles:      computeFiles,
		PVVFiles:          pvvFiles,
		StoreFiles:        storeFiles,
		ComputeMTLS:       computeMTLS,
		PVVMTLS:           pvvMTLS,
		StoreMTLS:         storeMTLS,
		ComputeServerMTLS: computeMTLS,
	}
	if addr == "" && (computeRole != "" || pvvRole != "" || storeRole != "") {
		return resolved, errors.New("forecast: vault: address is required when a component role is configured")
	}
	if addr != "" && computeRole == "" && pvvRole == "" && storeRole == "" &&
		!computeMTLS && !pvvMTLS && !storeMTLS && !computeFiles.ClientConfigured() && !storeFiles.ClientConfigured() && !pvvFiles.ClientConfigured() {
		return resolved, errors.New("forecast: vault: no component role is configured (compute, pvv, store)")
	}

	vaultMTLS := false
	var client *vault.Client
	if addr != "" {
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
		vaultMTLS = parseBool(look.Get("FORECAST_VAULT_MTLS", "VAULT_MTLS", "vault_mtls", "vaultMtls"), vaultCert != "" || vaultKey != "")
		var err error
		client, err = vault.NewClient(vault.Config{
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
		})
		if err != nil {
			return resolved, err
		}
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
	resolved.Client = client
	resolved.StoreCN = storeCN
	resolved.CertTTL = ttl
	resolved.VaultMTLS = vaultMTLS
	return resolved, nil
}

func defaultString(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
