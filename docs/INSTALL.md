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
  unattended retrainer pulls training frames from Grafana's own `/api/ds/query` (section 9).

## 2. What `make build` produces

| in `dist/` | what it is |
| --- | --- |
| `plugin.json`, `module.js`, `*.js`, `img/`, `LICENSE`, `README.md`, `CHANGELOG.md` | the app plugin, id `eduardkolotushin-forecast-app` |
| `forecast-panel/` | nested **panel** plugin, id `eduardkolotushin-forecast-panel` |
| `forecast-datasource/` | nested **datasource** plugin, id `eduardkolotushin-forecast-datasource`, with its own copy of `gpx_forecast_linux_amd64` |
| `gpx_forecast_linux_amd64` | the app's backend process |
| `gpx_forecast_migrate_linux_amd64` | schema migration CLI (section 8) |
| `gpx_forecast_compute_linux_amd64` | optional standalone fit/tick service (section 10) |
| `forecast.ini.template` | the `grafana.ini` snippet used in section 6c |

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
    secureJsonData:
      storePassword: overlay
```

Alternatively put the same keys in the datasource's `[plugin.eduardkolotushin-forecast-datasource]`
ini section, or set `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_DATASOURCE_*`.

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
| `FORECAST_SNAPSHOT_TTL` / `snapshot_ttl` | `72h` | delete snapshots nothing refreshed; `0` disables, below `1h` is refused |
| `FORECAST_RETRAIN_TICK` / `retrain_tick` | `30s` | claim loop interval |
| `FORECAST_RETRAIN_LEASE` / `retrain_lease` | `6m` | claim lease; a fit that would outlive it is dropped, not duplicated |
| `FORECAST_RETRAIN_RETRY_MAX` / `retrain_retry_max` | `1h` | cap on the retry backoff; not longer than the lease is refused |

### 9a. Which URL, and which token

The scheduler calls two endpoints of that URL, each with `Authorization: Bearer <grafanaToken>`:
`GET <url>/api/org` once per process (the org its claims are scoped to) and `POST <url>/api/ds/query` once
per due row. Both must be **Grafana itself, reached directly** by the process that ticks:

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
restore a snapshot needs its own **Forecast** datasource (section 7).

The datasource's own credentials are not this key's business: Grafana applies them when it runs the stored
query, so the plugin never talks to Abyss or to IAM. That has one consequence worth knowing: a datasource
that authorizes *per user* (an IAM token forwarded with the request, as Abyss does in the browser) has no
user on an unattended fetch — it needs its own credential in its datasource settings for retrains to work.

How a misconfiguration shows up: `401`/`403` counts as a refusal and three consecutive ticks end the ticker
with one Error log naming `FORECAST_GRAFANA_URL` / `FORECAST_GRAFANA_TOKEN`; a URL that is unreachable, or
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
| `FORECAST_GRAFANA_URL`, `FORECAST_GRAFANA_TOKEN` | for the retrain ticker it owns |
| `FORECAST_RETRAIN_*`, `FORECAST_SNAPSHOT_TTL` | the ticker and retention, unchanged |
| `FORECAST_MAX_INFLIGHT` | concurrent fits in this service, default `32` |

Then set `computeUrl: http://<service>:8080` and `computeToken` (jsonData / `secureJsonData`) on the app,
or `compute_url` / `compute_token` in its ini section. The app then forwards `POST /forecast` verbatim,
runs no ticker (the service owns it), and answers `502` naming the failure when the service is
unreachable — it never silently fits locally. `QueryData`, the schedules API and the panel keep working
from the same store while the service is down. Replicas share one Postgres, and the claim queue with its
lease makes N tickers safe.

The service exposes `/forecast`, `/ping` and an unauthenticated coarse `/healthz`; it deliberately does
not serve the schedules API.

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

1. **Administration → Plugins → Timeseries Forecast** shows the app enabled, with the panel and the
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
| the plugin behaves like an older build, or the backend dies with `fatal error: invalid runtime symbol table` | the plugin directory is a bind mount whose backend binary did not refresh (measured on a Windows/Docker Desktop share after rebuilding the ~47 MB backend) | copy the tree into the image or the plugin directory instead of mounting the build output, or recreate the mount/container before restarting Grafana |

## 13. Limits worth knowing

`FORECAST_MAX_INFLIGHT` (ini `max_inflight`, default `4` inline / `32` in remote mode) bounds concurrent
Fit and ForecastRange work per process; beyond it the backend answers `429`, and above the body/train
caps it answers `413`, instead of queueing or dying. Scaling fitting means more compute-service replicas,
never more Grafana-managed plugin processes.
