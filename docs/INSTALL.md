# Installing the plugin in an existing Grafana

How to put a built `dist/` into a Grafana this repo does not build: a host install, your own image, or a
cluster. The sibling sandbox (`timeseries-grafana-sandbox` Compose) and chart (`timeseries-k8s` Helm)
already do everything below; this file is the manual path, with the exact values and the checks that were
run against a fresh Grafana 13.1.0 while writing it.

## 1. Before you start

- **Grafana 12.3 or newer** — all three manifests declare `grafanaDependency: ">=12.3.0"`.
- **A built `dist/`** from `make build` (section 2).
- **PostgreSQL 13 or newer** if you want the snapshot store (section 8). Without a store the overlay
  retrains on every panel load and the Forecast datasource cannot serve alerting queries.
- **The datasources your dashboards query** (Prometheus, Druid, OpenSearch, Postgres, TestData). The
  plugin overlays a query result; it never fetches metric series itself, with one exception: the
  unattended retrainer pulls training frames from Grafana's own `/api/ds/query`, or from PVV's
  `/gse/data/v1/DataByRefId` when configured (section 9).

## 2. What `make build` produces

| in `dist/` | what it is |
| --- | --- |
| `plugin.json`, `module.js`, `*.js`, `img/`, `LICENSE`, `README.md`, `CHANGELOG.md` | the app plugin, id `eduardkolotushin-forecast-app` |
| `forecast-panel/` | nested **panel** plugin, id `eduardkolotushin-forecast-panel` |
| `forecast-datasource/` | nested **datasource** plugin, id `eduardkolotushin-forecast-datasource`, with its own copy of `gpx_forecast_linux_amd64` |
| `gpx_forecast_linux_amd64` | the app's backend process |
| `gpx_forecast_migrate_linux_amd64` | schema migration CLI (section 8) |
| `gpx_forecast_compute_linux_amd64` | optional standalone fit/tick service (section 10) |
| `forecast.ini.template` | the `grafana.ini` snippet used in sections 6c, 6d and 10 |
| `docs/` | the guide you are reading and the other docs, copied by `make docs` |

The Go binaries are built for **linux/amd64**. Copy the tree whole: Grafana loads the panel and the
datasource from those subdirectories, so flattening the directory registers only the app.

## 3. Copy the plugin into place

The plugin lives in its own directory under Grafana's plugins directory — by default
`/var/lib/grafana/plugins`, overridable with `GF_PATHS_PLUGINS` or `[paths] plugins` in `grafana.ini`:

```sh
# host / VM install (Grafana's own user is usually `grafana`, package installs use UID 472)
sudo rm -rf /var/lib/grafana/plugins/eduardkolotushin-forecast-app
sudo cp -r dist /var/lib/grafana/plugins/eduardkolotushin-forecast-app
sudo chown -R grafana:grafana /var/lib/grafana/plugins/eduardkolotushin-forecast-app
# an unzip from Windows or a COPY from a Windows checkout can drop the executable bit
sudo chmod 0755 /var/lib/grafana/plugins/eduardkolotushin-forecast-app/gpx_forecast_linux_amd64 \
                 /var/lib/grafana/plugins/eduardkolotushin-forecast-app/forecast-datasource/gpx_forecast_linux_amd64
```

In your own image, do it in the Dockerfile — this is what `timeseries-k8s` ships:

```dockerfile
COPY dist /opt/grafana-plugins/eduardkolotushin-forecast-app
RUN chown -R 472:472 /opt/grafana-plugins
ENV GF_PATHS_PLUGINS=/opt/grafana-plugins
```

On Kubernetes, install the `timeseries` chart from `timeseries-k8s` (`grafana.enabled`, `postgres.*`
values); it copies the built plugin into the Grafana image and provisions the app and datasource for you.

## 4. Allow the three unsigned plugins

The plugin is not signed (signing/publishing is out of scope for this repo), so Grafana must be told to
load it. All **three** ids are needed: the app, the nested panel, and the nested datasource.

`grafana.ini`:

```ini
[plugins]
allow_loading_unsigned_plugins = eduardkolotushin-forecast-app,eduardkolotushin-forecast-panel,eduardkolotushin-forecast-datasource
```

or the equivalent environment variable:

```sh
GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS=eduardkolotushin-forecast-app,eduardkolotushin-forecast-panel,eduardkolotushin-forecast-datasource
```

Do not use `GF_DEFAULT_APP_MODE=development`; the allowlist is the supported switch (and the only one
that works on a cluster). **Restart Grafana** — the plugin scanner runs at startup.

## 5. Check that it loaded

```sh
curl -s localhost:3000/api/plugins/eduardkolotushin-forecast-app/settings | jq '{id, enabled, version: .info.version}'
curl -s 'localhost:3000/api/plugins?type=app' | jq '.[] | {id, enabled, signature}'
```

Expected on 13.1.0 (verified): the app reports `enabled: true`, `signature: "unsigned"`, and the version
your build stamped (`1.0.0+dev.<sha>`), and so do `eduardkolotushin-forecast-panel` and
`eduardkolotushin-forecast-datasource`.

The app's backend endpoints answer only once the app instance is **enabled** for the org (section 6).
Before that:

```sh
curl -s -w '\n%{http_code}\n' localhost:3000/api/plugins/eduardkolotushin-forecast-app/resources/ping
# {"message":"Plugin is not enabled"}
# 404
```

## 6. Enable and configure the app

### 6a. Provisioning (recommended)

`/etc/grafana/provisioning/plugins/apps.yaml`:

```yaml
apiVersion: 1

apps:
  - type: 'eduardkolotushin-forecast-app'
    org_id: 1
    disabled: false
    jsonData:
      storeHost: overlay-postgres
      storePort: 5432
      storeDatabase: overlay
      storeUser: overlay
      storeSslMode: disable
    secureJsonData:
      storePassword: overlay
      # The retrainer's own Grafana credential (section 9a), created in this org.
      grafanaToken: glsa_…
```

### 6b. Or the API / UI (Admin)

```sh
curl -s -X POST localhost:3000/api/plugins/eduardkolotushin-forecast-app/settings \
  -H 'Content-Type: application/json' \
  -d '{"enabled":true,"jsonData":{"storeHost":"overlay-postgres","storePort":5432,"storeDatabase":"overlay","storeUser":"overlay","storeSslMode":"disable"},"secureJsonData":{"storePassword":"overlay","grafanaToken":"glsa_…"}}'
```

In the UI: **Administration → Plugins and data → Plugins → Timeseries Forecast → Enable**, or the app's
own **Configuration** page. That page renders no store fields and no retrain endpoint/credential fields by
design — the DSN, `grafanaUrl` and `grafanaToken` are deployment configuration, written through provisioning
or this API, not a user setting.

Disabling the app again drops its backend: the resource endpoints answer
`404 {"message":"Plugin is not enabled"}` (verified), and re-enabling with the same jsonData brings them
back with the store intact (also verified).

### 6c. Where the store DSN may come from

`gpx_forecast` resolves each key in this order, first non-empty wins:

| order | source | spelling |
| --- | --- | --- |
| 1 | process env | `FORECAST_STORE_HOST`, `FORECAST_STORE_PORT`, `FORECAST_STORE_DATABASE`, `FORECAST_STORE_USER`, `FORECAST_STORE_SSLMODE`, `FORECAST_STORE_PASSWORD` (or a whole `FORECAST_STORE_URL`) |
| 2 | Grafana's plugin env | `GF_PLUGIN_STORE_HOST`, `GF_PLUGIN_STORE_PORT`, `GF_PLUGIN_STORE_DATABASE`, `GF_PLUGIN_STORE_USER`, `GF_PLUGIN_STORE_SSL_MODE`, `GF_PLUGIN_STORE_PASSWORD` — Grafana reads the process's own `[plugin.<id>]` section and exports each key **without** the plugin id. The id-qualified spelling is read as well and wins when both are set: `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_STORE_HOST`, or `…_DATASOURCE_…` for the Forecast datasource process |
| 3 | `grafana.ini` | `[plugin.eduardkolotushin-forecast-app]` → `store_host`, `store_port`, `store_database`, `store_user`, `store_ssl_mode`, `store_password` (see `conf/forecast.ini.template`) |
| 4 | instance jsonData | `storeHost`, `storePort`, `storeDatabase`, `storeUser`, `storeSslMode`, and `secureJsonData.storePassword` |

**Grafana 12.4+ does not forward the host's `FORECAST_STORE_*` environment into plugin processes**, so
row 1 usually does not apply on a modern Grafana. Rows 2–4 are how the settings really arrive, and row 2
is populated *from* row 3: Grafana reads the `[plugin.<id>]` section and hands each key to that plugin
process — which is why the ini section is the only environment-like source that works with no jsonData.

Verified on Grafana 13.1.0 with nothing but those two ini sections set and no jsonData anywhere
(`[plugin.eduardkolotushin-forecast-app]` and `[plugin.eduardkolotushin-forecast-datasource]`):
`/resources/schedules` returned the store's rows, `/api/datasources/uid/forecast/health` answered
`{"message":"ok","status":"OK"}`, and the log line `forecast compute mode mode=remote url=…` showed the
section's `compute_url` taking effect.

Defaults when a key is absent: port `5432`, database `overlay`, user `overlay`, sslmode `disable`. An
empty host (and no URL) means "no store": the overlay then retrains on every load.

**Postgres mTLS (v20/v22)** is a separate switch from the compute hop and PVV. Set `FORECAST_STORE_MTLS=true`
(the app, the datasource instance, the compute service and `gpx_forecast_migrate` all honor it), then
choose one source:

- **Vault PKI**: configure Vault and set `FORECAST_VAULT_PKI_STORE_ROLE` to the role that issues the
  store's client certificate. The CN defaults to the resolved `store_user` (override with
  `FORECAST_VAULT_STORE_CN`); the Vault CA from the issue response is the pgx root. `pgx` presents the
  certificate through a `GetClientCertificate` callback, so a renewed certificate is used by new pool
  connections without a restart; a rotation of the Vault CA itself needs a rolling restart.
- **Local files**: set `FORECAST_STORE_CLIENT_CERT` / `FORECAST_STORE_CLIENT_KEY` (paths), optionally
  `FORECAST_STORE_CA_CERT` (system roots when empty) and `FORECAST_STORE_TLS_SERVER_NAME` when the DSN host
  is not the certificate's SAN. No Vault is needed. The pair is loaded when the pool is built, so rotation
  needs a restart. A Vault role wins when both are configured.

`sslmode` in the DSN is superseded when the switch is on. With the switch off (the default when no store
role and no local pair are set), the connection is exactly the pre-v20 `sslmode`-only behavior.

A component's flag can be set to `false` even when its role or file pair exists — that is the
Kubernetes/Istio shape, where the sidecar terminates mTLS and the application talks plain. A flag set
`true` with neither a role nor a local pair is an error naming the missing keys.

### 6d. Configuration map (component → function)

`conf/forecast.ini.template` is laid out as **process → component → function**. Use this map to find a
key; the jsonData spelling is the same words in camelCase, the env spelling is `FORECAST_` plus the
upper-case key (section 6c shows the precedence).

**`[plugin.eduardkolotushin-forecast-app]`**

| component | function | keys |
| --- | --- | --- |
| Vault | connection | `vault_addr`, `vault_namespace`, `vault_ca_cert`, `vault_tls_skip_verify` |
| Vault | authentication | `vault_token`, `vault_role_id`, `vault_secret_id`, `vault_approle_mount`, `vault_k8s_role`, `vault_k8s_mount`, `vault_k8s_jwt_path` |
| Vault | PKI issuance defaults | `vault_pki_mount`, `vault_cert_ttl` |
| Vault | client certificate to Vault (v21) | `vault_mtls`, `vault_client_cert`, `vault_client_key` |
| compute | forwarding | `compute_url`, `compute_token` |
| compute | mTLS | `compute_mtls`, `vault_pki_role`, `vault_client_cn`, `compute_tls_server_name`, `compute_client_cert`, `compute_client_key`, `compute_ca_cert`, `compute_server_cert`, `compute_server_key` |
| store | connection | `store_url`, or `store_host` / `store_port` / `store_database` / `store_user` / `store_ssl_mode` / `store_password` |
| store | retention | `snapshot_ttl` |
| store | mTLS | `store_mtls`, `vault_pki_store_role`, `vault_store_cn`, `store_client_cert`, `store_client_key`, `store_ca_cert`, `store_tls_server_name` |
| PVV | transport | `pvv_url`, `pvv_ca_cert`, `pvv_tls_skip_verify`, `retrain_window_chunk` |
| PVV | mTLS | `pvv_mtls`, `pvv_client_cert`, `pvv_client_key`, `vault_pki_pvv_role`, `vault_pvv_cn` |
| retrain | schedule and reliability | `retrain_enabled`, `retrain_tick`, `retrain_lease`, `retrain_retry_max`, `retrain_cron` |
| retrain | Grafana frame source | `grafana_url`, `grafana_token` |
| process | in-flight work | `max_inflight` |

**`[plugin.eduardkolotushin-forecast-datasource]`**

| component | function | keys |
| --- | --- | --- |
| store | connection | `store_url` / `store_*` (same meanings as the app) |
| store | mTLS | `store_mtls`, `vault_pki_store_role`, `vault_store_cn`, `store_client_cert`, `store_client_key`, `store_ca_cert`, `store_tls_server_name` |
| Vault | connection, authentication, issuance, client mTLS | the same keys as the app's Vault block; only the store role is used |
| process | in-flight work | `max_inflight` |

The datasource never forwards to compute and never runs the ticker, so it has no compute, PVV or retrain
keys. Optional keys in the template are commented out; uncomment the block you configure. The shipped
file leaves the store fields, compute, retrain, Grafana and `max_inflight` keys active.

## 7. The Forecast datasource (for Grafana alerting)

Alert rules that plot forecast or interval series query the nested datasource, which **Restores** a
trained snapshot. It is a separate process and does not receive the app's jsonData, so it needs the same
store keys on its own instance:

`/etc/grafana/provisioning/datasources/forecast.yaml`:

```yaml
apiVersion: 1

datasources:
  - name: Forecast
    type: eduardkolotushin-forecast-datasource
    uid: forecast
    access: proxy
    jsonData:
      storeHost: overlay-postgres
      storePort: 5432
      storeDatabase: overlay
      storeUser: overlay
      storeSslMode: disable
      # v20 store mTLS: uncomment and point at Vault. The datasource does not
      # inherit the app's Vault settings.
      # storeMtls: true
      # vaultPkiStoreRole: forecast-pg
      # vaultAddr: https://vault.example:8200
      # vaultCaCert: /etc/vault/ca.pem       # path, not PEM content
      # vaultK8sRole: forecast-k8s          # or vaultToken / AppRole
      # v21: only if Vault itself requires a client certificate
      # vaultMtls: true
      # vaultClientCert: /etc/vault-client/tls.crt
      # vaultClientKey: /etc/vault-client/tls.key
    secureJsonData:
      storePassword: overlay
      # vaultToken: …
```

Alternatively put the same keys in the datasource's `[plugin.eduardkolotushin-forecast-datasource]`
ini section, or set `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_DATASOURCE_*`. The section 6d map shows the
datasource's smaller key set; it has no compute, PVV or retrain keys.

Health check (verified — this is the fastest proof that the alerting path can reach the store):

```sh
curl -s localhost:3000/api/datasources/uid/forecast/health
# {"message":"ok","status":"OK"}
```

## 8. Prepare the Postgres schema (optional but recommended)

```sh
export FORECAST_STORE_HOST=... FORECAST_STORE_USER=... FORECAST_STORE_PASSWORD=... FORECAST_STORE_DATABASE=...

./gpx_forecast_migrate_linux_amd64 --dry-run   # list pending migrations, writes nothing
./gpx_forecast_migrate_linux_amd64             # apply them
```

- Needs **PostgreSQL 13+** (`gen_random_uuid()`).
- It is optional: the plugin applies the same migrations at its first store use, converging one request
  late. Running the CLI in a pipeline/Job means Grafana never races it.
- Safe while Grafana is up: each file takes a `pg_advisory_xact_lock` and runs in one transaction, and
  `forecast.schema_migrations` records it, so a pipeline run and every plugin process serialise.
- It reads process env `FORECAST_STORE_*`, then the `GF_PLUGIN_*` variables Grafana exports — no
  `grafana.ini` of its own and no jsonData. `--dsn postgres://…` overrides everything, `--timeout`
  defaults to `60s`, and the exit code is non-zero on the first failure.
- With store mTLS it selects the same source as the plugin: `FORECAST_VAULT_*` when
  `FORECAST_VAULT_PKI_STORE_ROLE` is set, otherwise `FORECAST_STORE_CLIENT_CERT` / `_KEY` (and optional
  `FORECAST_STORE_CA_CERT` / `_TLS_SERVER_NAME`). A switch on with neither source fails before migrating.
  The certificate values are paths.

## 9. Unattended retraining

Only the **app** process — or the compute service, when `compute_url` moves the ticker to it — runs the
scheduler (the datasource never does; it only Restores). It claims due rows from `forecast.retrain` and
fetches its own training frames from Grafana, so it needs a Grafana URL it can reach itself and, when
anonymous auth is off, a Viewer service-account token:

| key (jsonData / ini / env) | default | meaning |
| --- | --- | --- |
| `retrainEnabled` / `retrain_enabled` / `FORECAST_RETRAIN_ENABLED` | on | `false` turns the ticker off (and the retention sweep with it) |
| `retrainCron` / `retrain_cron` / `FORECAST_RETRAIN_CRON` | `0 3 * * *` | next run per model; the model's own cron wins once a schedule row exists |
| `grafanaUrl` / `grafana_url` / `FORECAST_GRAFANA_URL` | `http://127.0.0.1:3000` | where training frames come from |
| `grafanaToken` / `grafana_token` / `FORECAST_GRAFANA_TOKEN` | — | Viewer service-account token for `/api/org` and `/api/ds/query` |
| `pvvUrl` / `pvv_url` / `FORECAST_PVV_URL` | `FORECAST_GRAFANA_URL` | PVV base URL; the route is `/gse/data/v1/DataByRefId` |
| `pvvClientCert` / `pvv_client_cert` / `FORECAST_PVV_CLIENT_CERT` | — | local client certificate **path**; used when no Vault PVV role is set |
| `pvvClientKey` / `pvv_client_key` / `FORECAST_PVV_CLIENT_KEY` | — | local client private key **path** |
| `pvvCaCert` / `pvv_ca_cert` / `FORECAST_PVV_CA_CERT` | system roots | CA bundle **path** for the PVV server |
| `pvvMtls` / `pvv_mtls` / `FORECAST_PVV_MTLS` | on iff the Vault PVV role or either local path is set | selects PVV over TLS; `false` disables PVV even when files exist |
| `vaultPkiPvvRole` / `vault_pki_pvv_role` / `FORECAST_VAULT_PKI_PVV_ROLE` | — | issue the PVV client certificate from Vault; wins over the file pair |
| `vaultPvvCn` / `vault_pvv_cn` / `FORECAST_VAULT_PVV_CN` | `gpx-forecast-pvv` | CN of the Vault-issued PVV certificate |
| `pvvTlsSkipVerify` / `pvv_tls_skip_verify` / `FORECAST_PVV_TLS_SKIP_VERIFY` | `false` | test only; skip server certificate verification |
| `retrainWindowChunk` / `retrain_window_chunk` / `FORECAST_RETRAIN_WINDOW_CHUNK` | `24h` | maximum PVV request range; values above `24h` fall back to `24h` |
| `FORECAST_SNAPSHOT_TTL` / `snapshot_ttl` | `72h` | delete snapshots nothing refreshed; `0` disables, below `1h` is refused |
| `FORECAST_RETRAIN_TICK` / `retrain_tick` | `30s` | claim loop interval |
| `FORECAST_RETRAIN_LEASE` / `retrain_lease` | `6m` | claim lease; a fit that would outlive it is dropped, not duplicated |
| `FORECAST_RETRAIN_RETRY_MAX` / `retrain_retry_max` | `1h` | cap on the retry backoff; not longer than the lease is refused |

**PVV mode** is selected when `FORECAST_PVV_MTLS` is on and `vaultPkiPvvRole` is set or the file pair is complete; the file pair requires both values. It claims every due panel row across all orgs, sends each row's `org_id` to `<pvvUrl>/gse/data/v1/DataByRefId` over mTLS, and calls neither `/api/org` nor `/orgs`; a PVV failure is the row's failure, with no `/api/ds/query` fallback. The client certificate's CN must be in PVV's `AllowedCNs` (a PVV-side deployment step). The compute service reads these keys from its process env only, like every other setting.

### 9a. Which URL, and which token

In **Grafana mode** (no PVV client certificate/key), the scheduler calls two endpoints of that URL, each
with `Authorization: Bearer <grafanaToken>`: `GET <url>/api/org` once per process (the org its claims are
scoped to) and `POST <url>/api/ds/query` once per due row. Both must be **Grafana itself, reached
directly** by the process that ticks. In **PVV mode** this section does not apply: no Grafana token is
sent to PVV and neither `/api/org` nor `/orgs` is called.

- plugin in Grafana's process (the normal case): `http://127.0.0.1:3000` — the default, and correct as long
  as Grafana listens there on the same host.
- compute service (section 10): Grafana as *that container* sees it, e.g. `http://grafana:3000` or
  `http://<release>-grafana:80`. `127.0.0.1` inside that container is the service itself, not Grafana.
- **not** an IAM / forward-auth / SSO URL in front of Grafana. Such a proxy replaces `Authorization` and
  requires a browser session cookie; the unattended call carries a service-account token and no session, so
  it is refused. Keep the proxy in front of browsers and point this key at Grafana directly.
- the scheme is required (`http://` or `https://`), trailing slashes are trimmed, and the path must match
  what Grafana serves: with `serve_from_sub_path = true` and `root_url = https://iam-host.sbt/grafana-vm-01`,
  an internal `grafana_url = http://127.0.0.1:3000/grafana-vm-01` is correct where `http://127.0.0.1:3000`
  is not.

Two checks from the host that ticks, before trusting a deployment (both are read-only):

```bash
# Does this process reach Grafana, and does Grafana serve at the root?
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3000/api/health   # 200 root, 404 → add the sub-path
# Is the credential accepted by the listener this process talks to?
curl -s -H "Authorization: Bearer $FORECAST_GRAFANA_TOKEN" http://127.0.0.1:3000/api/org | head -c 80
```

`200` plus a `{"id":…}` body for both is what a working retrainer looks like; a redirect or `401` on the
second one means the direct listener still expects the proxy's session, which the authentication team has to
allow for API tokens on that path. The token is a Grafana **service account** token in the org that owns the
panels — Viewer is enough for both endpoints — because the scheduler resolves its org from `/api/org` and
claims only that org's rows.

Where that token comes from: **Administration → Users and access → Service accounts**
(`/org/serviceaccounts`) → **Add service account** with role **Viewer** → **Add service account token** →
copy the `glsa_…` value, which Grafana shows once. A service account belongs to the org you are signed in
to (`sa-<org>-<name>`), which is the org `/api/org` then reports — create it in the org that owns the
panels. An Admin credential can do the same over the API:

```bash
curl -s -X POST http://127.0.0.1:3000/api/serviceaccounts -H 'Content-Type: application/json' \
  -d '{"name":"forecast-retrainer","role":"Viewer"}'                  # → {"id":7,"login":"sa-1-forecast-retrainer",…}
curl -s -X POST http://127.0.0.1:3000/api/serviceaccounts/7/tokens -H 'Content-Type: application/json' \
  -d '{"name":"forecast-retrainer"}'                                  # → {"key":"glsa_…"} — the value to configure, once
```

Leave `secondsToLive` out for a token that does not expire; with an expiry, the scheduler meets `401` after
it lapses and disables itself after three ticks (below), so renew it early — the value is read when the
plugin (or compute) process starts.

**Several orgs.** The plugin is installed once, but Grafana keeps a plugin's settings *per org* — its
`plugin_setting` row is keyed by `org_id` + `plugin_id`, `secureJsonData` included — so enable and configure
the app in each org that needs it: `org_id` in the provisioning file above, or the settings API while acting
in that org (section 6b). Each org then has its own app instance, its own ticker, and its own credential, and
that ticker claims only the rows of the org its token belongs to (`/api/org`), so give every org a
service-account token of its own in `secureJsonData.grafanaToken`. The `FORECAST_GRAFANA_TOKEN` and
`grafana_token` spellings are process-wide and *outrank* `secureJsonData`, so a single such value would serve
one org's credential to every org's instance — leave them unset in a multi-org deployment. An org with no
token of its own still has its overlay and its Retrain button; only its unattended retraining is missing, and
that never touches another org's models. Datasources are per org as well, so every org whose alerts should
restore a snapshot needs its own **Forecast** datasource (section 7). **PVV mode is the exception**: its
claim is not org-scoped, so one configured process retrains every org's due panel rows and sends each row's
`org_id`; no per-org Grafana token is involved.

The datasource's own credentials are not this key's business: Grafana applies them when it runs the stored
query, so the plugin never talks to Abyss or to IAM. That has one consequence worth knowing: a datasource
that authorizes *per user* (an IAM token forwarded with the request, as Abyss does in the browser) has no
user on an unattended fetch — it needs its own credential in its datasource settings for retrains to work,
or the deployment selects PVV mode, which supplies the per-org credential itself.

How a misconfiguration shows up: in Grafana mode, `401`/`403` counts as a refusal and three consecutive
ticks end the ticker with one Error log naming `FORECAST_GRAFANA_URL` / `FORECAST_GRAFANA_TOKEN`; a URL
that is unreachable, or
wrong but answering (a `404` path), leaves the ticker running — it logs `retrain tick … failed=N` and the
row's `lastStatus` carries the error, which is where to look first.

The panel's own Retrain button keeps working meanwhile: it trains interactively and writes the same row.

Per-model crons and the admin API live on the app's **Retrain schedules** page
(`/plugins/eduardkolotushin-forecast-app?page=schedules`, or Administration → Plugins and data → Plugins →
Timeseries Forecast → **Retrain schedules**) and at
`GET|PUT|DELETE /api/plugins/eduardkolotushin-forecast-app/resources/schedules` (Admin).

## 10. Optional: the standalone compute service

If fitting should scale independently of Grafana, run `gpx_forecast_compute_linux_amd64` next to the
store and point the app at it. The service reads **process env only** (no ini, no jsonData):

| env | meaning |
| --- | --- |
| `FORECAST_COMPUTE_LISTEN` | listen address, default `:8080` (`--listen` overrides) |
| `FORECAST_COMPUTE_TOKEN` | shared secret, required — the service refuses to start without it |
| `FORECAST_STORE_*` | the same snapshot store as the plugin |
| `FORECAST_GRAFANA_URL`, `FORECAST_GRAFANA_TOKEN` | for the retrain ticker it owns (Grafana mode) |
| `FORECAST_PVV_*`, `FORECAST_RETRAIN_WINDOW_CHUNK` | PVV mode, when the client certificate/key are set (or `FORECAST_VAULT_PKI_PVV_ROLE` issues one) |
| `FORECAST_VAULT_*` | Vault address, namespace, auth, PKI mount/role, CA path and (v21) client certificate paths |
| `FORECAST_COMPUTE_MTLS` | listen TLS-only when on; unset follows "the compute PKI role or a compute certificate path is set"; off keeps the plain listener |
| `FORECAST_COMPUTE_TLS_SERVER_NAME` | DNS name the service's certificate is issued for and the plugin verifies (Vault CN or a local certificate SAN) |
| `FORECAST_COMPUTE_SERVER_CERT`, `FORECAST_COMPUTE_SERVER_KEY` | local listener pair; used when no compute PKI role is set |
| `FORECAST_COMPUTE_CA_CERT` | CA the listener trusts for client certificates (required with the local listener pair) |
| `FORECAST_RETRAIN_*`, `FORECAST_SNAPSHOT_TTL` | the ticker and retention, unchanged |
| `FORECAST_MAX_INFLIGHT` | concurrent fits in this service, default `4` (the forwarding plugin process's own slots default to `32`) |

Then set `computeUrl: http://<service>:8080` (`https://` when compute mTLS is on) and `computeToken`
(jsonData / `secureJsonData`) on the app, or `compute_url` / `compute_token` in its ini section. The app
then forwards `POST /forecast` verbatim, runs no ticker (the service owns it), and answers `502` naming
the failure when the service is unreachable — it never silently fits locally. `QueryData`, the schedules
API and the panel keep working from the same store while the service is down. Replicas share one Postgres,
and the claim queue with its lease makes N tickers safe.

### mTLS from Vault PKI or local files

Every hop is independent. An explicit `false` disables a component even when its Vault role or local pair
exists (the Kubernetes/Istio shape); a switch set `true` with neither source is an error naming the missing
keys. A configured Vault role wins over a local pair.

| component | switch | Vault identity | local source | what the switch does |
| --- | --- | --- | --- | --- |
| compute | `FORECAST_COMPUTE_MTLS` | `FORECAST_VAULT_PKI_ROLE`, CN `FORECAST_VAULT_CLIENT_CN` (default `gpx-forecast`) | client pair `FORECAST_COMPUTE_CLIENT_CERT` / `_KEY`, optional `FORECAST_COMPUTE_CA_CERT` | forward over `https` with a client cert; the service listens TLS-only |
| PVV | `FORECAST_PVV_MTLS` | `FORECAST_VAULT_PKI_PVV_ROLE`, CN `FORECAST_VAULT_PVV_CN` (default `gpx-forecast-pvv`) | `FORECAST_PVV_CLIENT_CERT` / `_KEY`, optional `FORECAST_PVV_CA_CERT` | present a client cert to PVV |
| store | `FORECAST_STORE_MTLS` | `FORECAST_VAULT_PKI_STORE_ROLE`, CN `FORECAST_VAULT_STORE_CN` (default the store user) | `FORECAST_STORE_CLIENT_CERT` / `_KEY`, optional `FORECAST_STORE_CA_CERT` / `_TLS_SERVER_NAME` | pgx presents a client cert to Postgres |
| compute listener | `FORECAST_COMPUTE_MTLS` | same compute role, CN `FORECAST_COMPUTE_TLS_SERVER_NAME` | `FORECAST_COMPUTE_SERVER_CERT` / `_KEY` + `FORECAST_COMPUTE_CA_CERT` | serve TLS and require a client certificate |
| Vault API | `FORECAST_VAULT_MTLS` | — | file pair `FORECAST_VAULT_CLIENT_CERT` / `_KEY` | present a client cert to Vault itself |

Each switch defaults to "that component's Vault role or local certificate path is configured" (the Vault
API switch: either file path is set). The same env/lookup chain reaches the app, the datasource, the compute
service and the migrator; the app may also spell these keys as jsonData (`vaultAddr`, `vaultPkiRole`,
`storeMtls`, …).

**Vault connection — all components.** Set `FORECAST_VAULT_ADDR`; empty means no Vault. Auth is tried in
order: static token (`FORECAST_VAULT_TOKEN`, then bare `VAULT_TOKEN`), AppRole
(`FORECAST_VAULT_ROLE_ID` + `FORECAST_VAULT_SECRET_ID`), Kubernetes (`FORECAST_VAULT_K8S_ROLE`, JWT
re-read from the service-account file). `FORECAST_VAULT_PKI_MOUNT` defaults to `pki`;
`FORECAST_VAULT_CERT_TTL` (default `24h`) requests the leaf lifetime. `FORECAST_VAULT_CA_CERT` and
`FORECAST_VAULT_TLS_SKIP_VERIFY` verify the Vault server; the client certificate for this hop is the v21
pair below.

**Vault client certificate (v21).** A Vault listener may require a client certificate. Set
`FORECAST_VAULT_MTLS=true` and point `FORECAST_VAULT_CLIENT_CERT` / `FORECAST_VAULT_CLIENT_KEY` at a PEM
pair; unset, the switch is on when either path is set (a lone half is an error); explicit `false` ignores
the pair. This pair cannot come from the same Vault (it is needed to reach it), so it is file-based and
loaded when the client is built — rotate it with a process restart. The Helm chart renders the paths and
accepts `compute.extraVolumes` / `compute.extraVolumeMounts` and `grafana.extraVolumes` /
`grafana.extraVolumeMounts`, but does not create the Secret: mount the pair into the Grafana and compute
pods.

**Compute.** With Vault: set `FORECAST_VAULT_ADDR` and `FORECAST_VAULT_PKI_ROLE` on both the app and the
service. The service refuses to start without `FORECAST_COMPUTE_TLS_SERVER_NAME` and a working initial
issue, then listens TLS-only with `RequireAndVerifyClientCert`; the app forwards over `https` only.
Certificates are issued by `pki/issue/<role>` and re-issued at half their remaining lifetime, so leaf
rotation needs no restart.

Without Vault: set `FORECAST_COMPUTE_MTLS=true` and give the app
`FORECAST_COMPUTE_CLIENT_CERT` / `FORECAST_COMPUTE_CLIENT_KEY` (optional `FORECAST_COMPUTE_CA_CERT`,
`FORECAST_COMPUTE_TLS_SERVER_NAME`), and the service `FORECAST_COMPUTE_SERVER_CERT` /
`FORECAST_COMPUTE_SERVER_KEY` plus `FORECAST_COMPUTE_CA_CERT` to verify clients. The URL is still
`https://`. The two pairs are separate because the plugin presents the client certificate and the service
presents the server certificate; both are loaded once, so rotate them with a restart.

**PVV.** With Vault: set `FORECAST_VAULT_PKI_PVV_ROLE` (app, compute service, or both) and leave
`FORECAST_PVV_MTLS` true; the retrain transport presents a Vault-issued certificate and PVV mode is
selected by that role. Without Vault: set `FORECAST_PVV_MTLS=true` and
`FORECAST_PVV_CLIENT_CERT` / `_KEY` (paths) plus optional `FORECAST_PVV_CA_CERT`. An explicit
`FORECAST_PVV_MTLS=false` disables PVV even when the files exist, keeping the scheduler on Grafana's
`/api/ds/query`.

**Store.** With Vault: set `FORECAST_STORE_MTLS=true` and `FORECAST_VAULT_PKI_STORE_ROLE` on every process
that opens the store (app, datasource, compute service, migrator). The CN defaults to the resolved
`store_user` (override with `FORECAST_VAULT_STORE_CN`); pgx presents the certificate through a
`GetClientCertificate` callback, so new pool connections pick up a renewed leaf without a restart. The
Vault CA from the issue response is the pgx root; rotating the Vault CA itself needs a rolling restart.

Without Vault: set `FORECAST_STORE_MTLS=true` and `FORECAST_STORE_CLIENT_CERT` /
`FORECAST_STORE_CLIENT_KEY` on every such process, with optional `FORECAST_STORE_CA_CERT` (system roots
when empty) and `FORECAST_STORE_TLS_SERVER_NAME` (the DSN host when empty). `sslmode` is superseded when the
switch is on. Local material is loaded when the pool is built, so rotation needs a restart.

All certificate settings are **file paths**, never PEM content: `FORECAST_VAULT_CA_CERT`,
`FORECAST_VAULT_CLIENT_CERT`, `FORECAST_VAULT_CLIENT_KEY`, `FORECAST_COMPUTE_CLIENT_CERT` / `_KEY` /
`_CA_CERT` / `_SERVER_CERT` / `_SERVER_KEY`, `FORECAST_STORE_CLIENT_CERT` / `_KEY` / `_CA_CERT`, and PVV's
`FORECAST_PVV_CA_CERT` / `FORECAST_PVV_CLIENT_CERT` / `FORECAST_PVV_CLIENT_KEY`. A value containing a PEM
block is rejected at construction (`… must be a file path, not PEM content`).

The service exposes `/forecast`, `/ping` and an unauthenticated coarse `/healthz`; it deliberately does
not serve the schedules API.

> Without Vault the compute hop is plain HTTP unless a local pair is configured; with a local pair it is
> still TLS with the same shared token and `X-Forecast-Org`. Keep a plain hop on a trusted pod network, do
> not expose the Service outside the cluster, and terminate TLS in front of it if it crosses a host or
> network boundary.

## 11. End-to-end verification

All of these were run against a fresh Grafana 13.1.0 with only the mounts in sections 3, 4, 6a and 7:

```sh
curl -s localhost:3000/api/plugins/eduardkolotushin-forecast-app/resources/ping
# {"message":"ok"}

curl -s localhost:3000/api/plugins/eduardkolotushin-forecast-app/resources/schedules
# [{"scope":"baseline","key":"live","cron":"*/5 * * * *", … "lastStatus":"ok"}, …]   (Admin only)
# an empty array means the store is reachable and simply has no schedules yet

curl -s localhost:3000/api/datasources/uid/forecast/health
# {"message":"ok","status":"OK"}
```

Then in the UI:

1. **Administration → Plugins and data → Timeseries Forecast** shows the app enabled, with the panel and the
   datasource listed as included plugins.
2. Add a **Forecast overlay** panel to a dashboard, pick a metric query and a training query: the panel
   draws history plus forecast and, with `showInterval`, the band. Its status reads `Using saved model`
   when a snapshot was restored, or offers **Retrain** when the store has none.
3. **New alert rule** from the panel options builds a rule against the metric query plus a synthesized
   Forecast query — that is the path that needs section 7.
4. The app's pages: `/a/eduardkolotushin-forecast-app` is its Overview page (what the plugin does and the
   two wiring rules), and the config pages carry a tab bar — **Overview · Configuration · Retrain
   schedules**. `Retrain schedules` (`/plugins/eduardkolotushin-forecast-app?page=schedules`) lists the
   `forecast.retrain` rows with Source / Scope / Key / Cron / Timezone / Next run / Last run / Status /
   Enabled, plus search and the admin controls for the cron and the default. Grafana marks the unsigned
   plugin with an **Invalid plugin signature** banner in that header — expected, informational, and it
   does not block loading.

If any mTLS switch is on (Vault PKI or local files), also check:

```sh
# Vault PKI: the plugin and the compute service both logged an initial issue.
#   forecast: compute mTLS enabled pki_role=… server_name=…
# Local files: the same line says source=files; no Vault log appears.
#   {"tls":true,...}
# The migrator with a local store pair and no Vault variables prints the same ledger line:
#   FORECAST_STORE_MTLS=true FORECAST_STORE_CLIENT_CERT=… FORECAST_STORE_CLIENT_KEY=…
#   FORECAST_STORE_CA_CERT=… gpx_forecast_migrate --dry-run
# The compute listener refuses a peer without a client certificate:
curl -k https://<compute-host>:8080/healthz
# curl: (56) … tlsv13 alert certificate required

# Store mTLS: the dedicated user is refused without a cert, and accepted by the plugin.
psql "host=… user=forecast dbname=overlay sslmode=require" -c 'select 1'
# FATAL: connection requires a valid client certificate
```

If Vault itself requires a client certificate, a deliberate request without the pair must fail with
`remote error: tls: certificate required`; the plugin log shows the issue succeeding only after
`vault_mtls` and the two paths are set.

## 12. Troubleshooting

| symptom | cause | fix |
| --- | --- | --- |
| `404 {"message":"Plugin is not enabled"}` on `/resources/…` | the app instance is not enabled for the org | section 6 (provisioning `disabled: false`, or Enable in the UI/API) |
| plugin absent from the plugin list | wrong directory, flattened tree, missing `plugin.json`, or no restart | section 3, then restart Grafana |
| log says the unsigned plugin is not allowed to load | allowlist missing one of the three ids | section 4 — app **and** panel **and** datasource |
| the plugin page shows an **Invalid plugin signature** banner | the plugin is unsigned (by design; signing is out of scope) | expected and informational once section 4 is in place — it only disappears with a signed build |
| backend fails to start (no `/resources` at all) | binary not executable, wrong owner, or wrong architecture | `chmod 0755`, `chown` to Grafana's UID (472), rebuild for the host's `GOOS`/`GOARCH` |
| panel always trains / a "no store" reason on every load | the DSN is not visible to the **plugin process** — the host's `FORECAST_STORE_*` env is not forwarded on Grafana 12.4+ | section 6c: use `GF_PLUGIN_*`, the ini section, or jsonData |
| alert rule's forecast query fails while the panel works | the **Forecast datasource** instance has no store keys (it does not inherit the app's jsonData) | section 7 |
| `forecast.retrain has no attempts column` in the log | the schema was created by an older release | run `gpx_forecast_migrate_linux_amd64` (section 8) |
| retrainer goes quiet after startup | not reachable as configured, or 401 from a missing/invalid token | set `grafanaUrl`/`grafanaToken` (section 9), or `retrainEnabled: false` if a compute service owns ticking |
| fit returns `502` / `401` | compute service unreachable, or `computeToken` does not match `FORECAST_COMPUTE_TOKEN` | section 10; the plugin never falls back to a local fit |
| `forecast: vault: no component role is configured (compute, pvv, store)` | `vault_addr` is set but none of the three PKI roles is, and no local certificate pair is configured | set the role for the component you want, point it at a local pair, or leave `vault_addr` empty (section 10) |
| `forecast: vault: address is required when a component role is configured` | a `FORECAST_VAULT_PKI_*_ROLE` is set without `vault_addr` | set the address or remove the role |
| `forecast: compute: mTLS needs FORECAST_VAULT_PKI_ROLE or FORECAST_COMPUTE_CLIENT_CERT/KEY` | compute mTLS is on with neither source | configure the role, or the client pair, or set `FORECAST_COMPUTE_MTLS=false` |
| `forecast compute: mTLS needs … FORECAST_COMPUTE_SERVER_CERT/KEY and FORECAST_COMPUTE_CA_CERT` | the service has no listener pair and no compute role | configure the server pair and CA, or turn the listener switch off |
| `forecast: store: mTLS needs FORECAST_VAULT_PKI_STORE_ROLE or FORECAST_STORE_CLIENT_CERT/KEY` | store mTLS is on with neither source | configure the role or the client pair, or set `FORECAST_STORE_MTLS=false` |
| `forecast: vault: login: 403 …` | Vault token/AppRole/Kubernetes role is wrong or its policy lacks `pki/issue/<role>` | fix the auth method and policy; Kubernetes auth must bind the pod's service account (section 10) |
| `forecast: vault: … must be a file path, not PEM content` (or `compute tls` / `store tls` / `pvv` prefix) | a certificate value is PEM content, not a path | point it at a mounted file (section 6d) |
| `remote error: tls: certificate required` when the plugin talks to Vault | the Vault listener requires a client certificate but `vault_mtls` is off or the pair is missing | set `vault_mtls` and the client cert/key paths (section 10) |
| alerting datasource cannot restore after store mTLS was enabled | the datasource needs its own Vault/store-mTLS keys, not the app's | section 7 and the datasource part of section 6d |
| the plugin behaves like an older build, or the backend dies with `fatal error: invalid runtime symbol table` | the plugin directory is a bind mount whose backend binary did not refresh (measured on a Windows/Docker Desktop share after rebuilding the ~47 MB backend) | copy the tree into the image or the plugin directory instead of mounting the build output, or recreate the mount/container before restarting Grafana |

## 13. Limits worth knowing

`FORECAST_MAX_INFLIGHT` (ini `max_inflight`, default `4` inline / `32` in remote mode) bounds concurrent
Fit and ForecastRange work per process; beyond it the backend answers `429`, and above the body/train
caps it answers `413`, instead of queueing or dying. Scaling fitting means more compute-service replicas,
never more Grafana-managed plugin processes.
