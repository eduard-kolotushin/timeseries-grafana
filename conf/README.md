# CI/CD grafana.ini snippet

Proprietary Grafana images that already copy `dist/` can merge [forecast.ini.template](forecast.ini.template)
into `grafana.ini` (or `custom.ini`). Grafana expands `${FORECAST_*}` in `.ini` files; a pipeline can also
replace the placeholders before shipping. The template is laid out as **process → component → function**;
this file is the same map with the env spelling.

## App process

| component | function | ini key → env |
| --- | --- | --- |
| Vault | connection | `vault_addr` → `FORECAST_VAULT_ADDR`; `vault_namespace`; `vault_ca_cert`; `vault_tls_skip_verify` |
| Vault | authentication | `vault_token`; `vault_role_id`; `vault_secret_id`; `vault_approle_mount`; `vault_k8s_role`; `vault_k8s_mount`; `vault_k8s_jwt_path` |
| Vault | PKI issuance defaults | `vault_pki_mount`; `vault_cert_ttl` |
| Vault | client certificate to Vault (v21) | `vault_mtls`; `vault_client_cert`; `vault_client_key` |
| compute | forwarding | `compute_url` → `FORECAST_COMPUTE_URL`; `compute_token` |
| compute | mTLS | `compute_mtls`; `vault_pki_role`; `vault_client_cn`; `compute_tls_server_name`; `compute_client_cert`; `compute_client_key`; `compute_ca_cert`; `compute_server_cert`; `compute_server_key` |
| store | connection | `store_url`, or `store_host` / `store_port` / `store_database` / `store_user` / `store_ssl_mode` / `store_password` |
| store | retention | `snapshot_ttl` |
| store | mTLS | `store_mtls`; `vault_pki_store_role`; `vault_store_cn`; `store_client_cert`; `store_client_key`; `store_ca_cert`; `store_tls_server_name` |
| PVV | transport | `pvv_url`; `pvv_ca_cert`; `pvv_tls_skip_verify`; `retrain_window_chunk` |
| PVV | mTLS | `pvv_mtls`; `pvv_client_cert`; `pvv_client_key`; `vault_pki_pvv_role`; `vault_pvv_cn` |
| retrain | schedule and reliability | `retrain_enabled`; `retrain_tick`; `retrain_lease`; `retrain_retry_max`; `retrain_cron` |
| retrain | Grafana frame source | `grafana_url`; `grafana_token` |
| process | in-flight work | `max_inflight` → `FORECAST_MAX_INFLIGHT` |

Every env name is `FORECAST_` plus the upper-case key (`vault_mtls` → `FORECAST_VAULT_MTLS`), with
`store_ssl_mode` → `FORECAST_STORE_SSLMODE` as the one spelling exception. The shipped
template leaves the store connection fields, compute, retrain, Grafana and `max_inflight` keys active; Vault
and PVV blocks are commented until configured. `store_host` empty (and no `store_url`) means persist is off.

## Datasource process

| component | function | ini key → env |
| --- | --- | --- |
| store | connection | `store_url` / `store_host` / `store_port` / `store_database` / `store_user` / `store_ssl_mode` / `store_password` |
| store | mTLS | `store_mtls`; `vault_pki_store_role`; `vault_store_cn`; `store_client_cert`; `store_client_key`; `store_ca_cert`; `store_tls_server_name` |
| Vault | connection | `vault_addr`; `vault_namespace`; `vault_ca_cert`; `vault_tls_skip_verify` |
| Vault | authentication | `vault_token`; `vault_role_id`; `vault_secret_id`; `vault_approle_mount`; `vault_k8s_role`; `vault_k8s_mount`; `vault_k8s_jwt_path` |
| Vault | PKI issuance defaults | `vault_pki_mount`; `vault_cert_ttl` |
| Vault | client certificate to Vault (v21) | `vault_mtls`; `vault_client_cert`; `vault_client_key` |
| process | in-flight work | `max_inflight` |

The app and datasource sections are not identical. The app carries `retrain_*`, `grafana_*`, `compute_*`
and the PVV keys; the datasource serves alerting `QueryData`, which only Restores, so it has no compute,
PVV or retrain keys. It does need its own Vault block when `store_mtls` uses a Vault role; a local pair
needs none.

## Remote compute service

The service reads process env only (no ini, no jsonData):

| component | function | env |
| --- | --- | --- |
| process | listener | `FORECAST_COMPUTE_LISTEN` (default `:8080`, `--listen` overrides) |
| process | authentication | `FORECAST_COMPUTE_TOKEN` (required) |
| compute | mTLS listener | `FORECAST_COMPUTE_MTLS`; `FORECAST_COMPUTE_TLS_SERVER_NAME`; `FORECAST_COMPUTE_SERVER_CERT`; `FORECAST_COMPUTE_SERVER_KEY`; `FORECAST_COMPUTE_CA_CERT` |
| store | connection | `FORECAST_STORE_*` |
| store | mTLS | `FORECAST_STORE_MTLS`; `FORECAST_VAULT_PKI_STORE_ROLE`; `FORECAST_VAULT_STORE_CN`; `FORECAST_STORE_CLIENT_CERT`; `FORECAST_STORE_CLIENT_KEY`; `FORECAST_STORE_CA_CERT`; `FORECAST_STORE_TLS_SERVER_NAME` |
| Vault | connection/auth/PKI | `FORECAST_VAULT_*`, including `FORECAST_VAULT_MTLS` / `_CLIENT_CERT` / `_CLIENT_KEY` (v21) |
| PVV | transport/mTLS | `FORECAST_PVV_*`, `FORECAST_VAULT_PKI_PVV_ROLE`, `FORECAST_RETRAIN_WINDOW_CHUNK` |
| retrain | ticker | `FORECAST_RETRAIN_*`, `FORECAST_SNAPSHOT_TTL` |
| Grafana | frame source | `FORECAST_GRAFANA_URL`, `FORECAST_GRAFANA_TOKEN` |
| process | in-flight work | `FORECAST_MAX_INFLIGHT` |

## Rules

- **Certificates are paths, not content.** `*_ca_cert`, `*_client_cert`, `*_client_key`,
  `*_server_cert` and `*_server_key` are file paths; a value containing a PEM block is rejected at
  construction.
- **Vault PKI or local files per component.** `compute_mtls`, `pvv_mtls` and `store_mtls` select the
  TLS mode; a configured Vault role issues the material, otherwise the local pair is loaded. A switch
  on with neither is a loud per-component error, and an explicit `false` wins over both. Local files are
  loaded once, so rotation needs a restart; Vault-issued certificates rotate at half-life.
- **Vault is optional.** With `vault_addr` empty, each component can still run mTLS from its files. A
  Vault address with no component role and no local pair is a loud error.
- Grafana 12.4+ does not forward host `FORECAST_STORE_*` into plugin processes by default; each process
  reads its own `[plugin.<id>]` via `GF_PLUGIN_*` / GrafanaCfg. Overlay jsonData comes from provisioning
  (the app Configuration page renders no store fields); alerting needs the same store keys on the Forecast
  datasource instance unless this ini section is merged.
- `gpx_forecast` also reads `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_*` and
  `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_DATASOURCE_*`. Process env `FORECAST_*` wins over ini when present.
  Grafana also exports the section id-less — `[plugin.eduardkolotushin-forecast-app] store_host` reaches
  the plugin process as `GF_PLUGIN_STORE_HOST` (Grafana 13.1.0) — and the id-qualified spelling wins when
  both are set.
- Empty `max_inflight` uses the default: 4 Fit / ForecastRange slots inline, 32 forwards in remote mode.
- Do not put `allow_loading_unsigned_plugins` or datasource YAML here. Panel model options are dashboard
  JSON, not this file.
