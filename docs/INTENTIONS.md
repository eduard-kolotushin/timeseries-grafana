# Project intentions

## Goal

Grafana plugin that overlays univariate forecasts on dashboard queries. This plugin visualizes; it does not own Series or model math. The Docker Grafana runtime is a separate sibling. Minute-of-week baselines from Druid to Kafka live in sibling `timeseries-baselines`, not in this plugin process.

## Locked choices

| Decision | Choice |
| --- | --- |
| Repo | sibling `timeseries-grafana` |
| Plugin | Grafana app `eduardkolotushin-forecast-app` with Go backend |
| Panel | nested `eduardkolotushin-forecast-panel` |
| Alert queries | nested datasource `eduardkolotushin-forecast-datasource` (Restore + `ForecastRange`; overlay trains) |
| Models | public `timeseries-forecast` Fit functions (tagged module; no `replace`) |
| Sandbox | sibling `timeseries-grafana-sandbox` (Compose) |
| Kubernetes | sibling `timeseries-k8s` (Helm + images) |
| Baseline publisher | sibling `timeseries-baselines` (standalone process, not Grafana-hosted) |
| Train adapters | frontend only (Prometheus, OpenSearch, Postgres, existing Druid/SQL rewrite). No Prom/OS/PG HTTP clients in `pkg/`. The v12 scheduler replays the frontend's query objects through Grafana's own `/api/ds/query`, which is not a datasource client |

## v1 must-have

- `POST /forecast` resource using `timeseries.New` + `forecast.Fit*` + `Forecast(h)`
- Nested panel overlaying history and forecast
- Options: model, horizon, alpha, beta, period, season, calendar

## v2 must-have

- `POST /forecast` returns optional `lower` / `upper` from `ForecastInterval`
- Nested panel option `interval` (coverage in `(0, 1)`, default 0.95; `0` hides bands)
- Overlay draws a `fillBelowTo` band on the forecast series

## v3 must-have

- Nested panel option `showInterval` (default on); coverage applies only when the switch is on (`level` `0` when off)
- Training window independent of the panel time range: a second datasource query for `[trainFrom, trainTo]`
- Training period is a Grafana from/to time picker (relative or absolute), same as the dashboard time picker. Clear / Auto uses a model-based window ending at the panel `to`. Dashboards that still store a duration `lookback` (`15d`, `48h`) keep that meaning until a picker range is saved. Display still follows the panel query range
- Overlay does not plot the extra training points

## v4 must-have

- Nested panel option `forecastRange` `{from,to}` (Grafana raw strings). Empty / Auto is Grafana dashboard `now` through `now` + a model-based duration (independent of the dashboard range)
- Replace the numeric horizon with that from/to picker (same UX as the training picker)
- `POST /forecast` sends `from` / `to` unix ms; backend calls `ForecastRange` (and `ForecastIntervalRange` when bands are on)
- Plot `to` extends to include the forecast window so Auto `now→now+duration` is visible
- If the forecast window has no drawable points, the panel shows a specific reason (no silent empty overlay; no silent fit of the visible series when the train query is empty)

Auto forecast duration (start = dashboard `now`):

| Model | Duration |
| --- | --- |
| baseline minute-of-week or hour-of-week | 6h |
| baseline hour | 24h |
| baseline day | 7d |
| seasonal naive | 24h |
| naive, mean, drift, SES, Holt | 6h |

## v5 must-have

- Type-keyed training-query adapters so the second datasource query uses the training window (and a model-aware step) on Prometheus, OpenSearch, and Postgres, while keeping the existing Druid/SQL rewrite for other types (including TestData)
- Extraction and matching for labeled and wide frames: every numeric field, Grafana display names (labels), match train to visible by that name
- Valid train queries: Prometheus **range** PromQL; OpenSearch **Lucene metric + date histogram** or **PPL time series**; Postgres **time series** SQL (`postgres` and `grafana-postgresql-datasource`) with Grafana time macros
- Invalid train queries (reason, history only, no `POST /forecast`): Prometheus instant; OpenSearch logs/raw/traces; Postgres table/EXPLAIN; frames that are not time+number
- Do not silently fit the visible series when several train series exist and a name misses
- No Prometheus, OpenSearch, or Postgres HTTP clients in `pkg/` (`gpx_forecast` stays datasource-agnostic)

Train step follows the model, not the dashboard interval: minute-of-week `1m`, hour / hour-of-week `1h`, day `1d`, otherwise the request `intervalMs` (floor 1m). Still clamp with `MAX_TRAIN_POINTS` (100k).

## v6 must-have

- Persist fitted snapshots in Postgres (`forecast.snapshots`) via **pgx** in `gpx_forecast`. Org-scoped, survives Grafana restart, shared across users. Not Grafana Postgres datasource HTTP and not Druid metadata Postgres
- `POST /forecast` `cacheKey` / `needTrain` / `retrain`. Skip the training datasource query until Retrain or a change to query / model / train-range strings
- Deployment configuration for the DSN: env `FORECAST_STORE_*` → `GF_PLUGIN_*` → grafana.ini → provisioned jsonData (`storeUrl`, or `storeHost`/… field-wise). No UI fields for it. No DSN: persist off (always `needTrain`)
- Overlay Retrain control; status when a saved model is used

## v7 must-have

- Backend DSN resolution: `FORECAST_STORE_*` env (Grafana 12.4+ does not forward host env into plugin processes by default), then Grafana `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_*` / `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_DATASOURCE_*` / GrafanaCfg, then jsonData / `secureJsonData`. Empty host (and no URL): persist off
- Mergeable Grafana `.ini` snippet (`conf/forecast.ini.template`) for proprietary CI/CD that already ships this plugin with Grafana: `[plugin.eduardkolotushin-forecast-app]` and `[plugin.eduardkolotushin-forecast-datasource]` snapshot-store keys with `${FORECAST_STORE_*}` placeholders
- Panel options stay on the dashboard. Do not put unsigned-plugin allowlist or datasource provisioning in this template

## v8 must-have

- Nested Grafana datasource `eduardkolotushin-forecast-datasource` (`backend`, `metrics`, `alerting`) so unified alerting and expressions can use forecast / lower / upper by Grafana `refId`
- Query editor: output kind, model and train-range **strings** (fingerprint only), series name, copy of query A’s datasource uid and inner query; frontend writes the same `cacheKey` as the overlay (canonical SQL/expr, Grafana time macros equivalent to interpolated panel timestamps). No train rewrite and no live train query on this path
- `QueryData` `Restore`s the snapshot and `ForecastRange`s / `ForecastIntervalRange`s for the request time range. Miss (`needTrain`) is an error frame. Overlay remains the only train/retrain path
- Snapshot DSN for this process: Forecast datasource provisioned jsonData (same keys as the app process's), `[plugin.eduardkolotushin-forecast-datasource]`, or parent `AppInstanceSettings` when Grafana sends them. Overlay train still uses the app process DSN
- Do not execute the source datasource from `pkg/` on alert eval. Live metric comparison stays Grafana query A
- This plugin does not ship Grafana alert rules or notification channels

## v9 must-have

- Mixed panel (metric query A + Forecast datasource query B) is a supported overlay scenario. Overlay history, cacheKey, and train rewrite use only metric targets; Forecast datasource frames are not fitted and are not plotted (overlay POST is the overlay forecast). No Overlay/Query/Compare draw mode
- Forecast query editor stays manual (kind, model, train-range strings, series name, source query). Do not auto-fill from siblings or overlay options. An optional **Copy source from query A** button may copy `sourceTargets` for the fingerprint only
- Grafana shows the panel **Alert** tab only for Time series and Graph, and only if that visualization was current when the editor data pane was created. Switching from this overlay to Time series in the same edit session does not add the tab. Working paths: Time series panel opened as Time series (or leave edit and re-open after switching); panel menu More → New alert rule; Alerting → New alert rule. Do not ship alert rules

## v10 must-have

- Overlay options **Alerting** with **New alert rule**: navigate to Grafana `/alerting/new` with live panel queries (including Mixed Forecast rows), default reduce + threshold expressions, and `__dashboardUid__` / `__panelId__` annotations so the rule stays linked to this overlay. Dashboard must be saved first. Rows whose datasource `meta.alerting` is off are dropped (Grafana’s “no alerting capable query” reason). Grafana 13 TestData and the nested Forecast datasource are both alerting-capable.
- This is not Grafana’s data-pane Alert tab (still Time series / Graph only). Do not rename the overlay plugin id. Do not ship alert rules or contact points

## v11 must-have

High load must not crash `gpx_forecast` or Grafana. Prefer a reason (or HTTP 413/429) over unbounded work.

- **Backend (`POST /forecast` and Forecast `QueryData`)**: cap the request body and `len(times)` / `len(values)` at the existing `MAX_TRAIN_POINTS` (100k). Reject oversize with 400/413. A body too large to describe a legal training series (two arrays of `MAX_TRAIN_POINTS` elements at 32 bytes each) is refused **before** it is decoded, and one emitted window is capped at `maxForecastPoints` (1e6, the twin of the library's `MaxForecastPoints`, checked before the fit on the fit, restore and datasource paths), so a `to` far enough to need more points is a 413 reason instead of an allocation. Limit concurrent Fit / ForecastRange work (default a small fixed inflight cap, env-overridable). When the cap is full, return 429; do not queue unbounded goroutines. Honor request context cancel. Recover panics in resource and `QueryData` handlers so one bad request cannot kill the plugin process
- **Overlay frontend**: do not POST a train body longer than that cap. Do not fan-out one POST per series in parallel. Max in-flight overlay loads per panel instance is a panel option (default 1, minimum 1). When the cap is full, drop or cancel a stale refresh. On 413/429/5xx show a reason; no tight retry loop
- **Grafana process**: training queries stay in Grafana datasource plugins and stay clamped by `maxDataPoints` ≤ 100k. This plugin does not add a second train query per series. Snapshot Restore stays the cheap path; load limits apply there too so an alert-eval burst cannot grow without bound
- Do not add a job queue, extra `gpx_forecast` replicas, Grafana core changes, SIMD, or a parallel public API

## v12 must-have

Retrain stored snapshots on a cron with **no browser open**, and expose the schedule so an operator can see and edit it. The backend fetches its own training data through Grafana's own query API.

- **Backend retrain scheduler** in the app process: one ticker (`FORECAST_RETRAIN_TICK`, default 30s) that claims due rows from Postgres and retrains them. Started only when a store, a claimable row, and the Grafana query API are all available; `Dispose` cancels it, and three consecutive `401`/`403` refusals from Grafana end it too (a configuration error it cannot retry its way out of)
- **`forecast.retrain` table** (schema `forecast`, created by the migration engine in `gpx_forecast` — see v13; the plugin is its only DDL owner). Columns: `scope`, `key`, `org_id`, `cron`, `timezone`, `enabled`, `spec JSONB`, `next_run_at`, `last_run_at`, `last_status`, `claimed_by`, `claimed_until`, `updated_at`, `superseded_at`; keyed `(scope, org_id, key)` by a `PRIMARY KEY` in v12 and a `UNIQUE` constraint beside the uuid primary key from v13 on. `org_id` is part of the key because a panel's `cacheKey` is org-independent (the same dashboard and datasource uids hash the same in every org): keyed `(scope, key)`, two orgs would share one row, and either org's fit would rewrite the other's cron and stored query objects while the other could not even list it. `0002_retrain.sql` migrates a table created with the old key in place (rows keep the org that wrote them), and `ensureSchedules` refuses to serve a table whose key is still `(scope, key)` instead of quietly sharing rows between orgs. `scope` is `panel` (overlay `cacheKey`, this org's row) or `baseline` (`metric_hash`, written by sibling `timeseries-baselines`, which never creates the table, and stored at the fleet-wide `org_id = 0`)
- **Claim queue**: `Claim` is `UPDATE … FROM (SELECT … FOR UPDATE SKIP LOCKED)`, so Grafana HA (one plugin process per replica) retrains each due row exactly once without a leader. Lease `FORECAST_RETRAIN_LEASE` (derived from the claim batch and the fetch timeout — 6m today, which stays above the batch's worst case) releases a row whose worker died. The plugin claims only `scope='panel'` rows **of its own org** that have a `spec` with a non-empty `queries` array and no `superseded_at` stamp; the worker claims only `scope='baseline'` and likewise skips a superseded row. Neither can claim a row it cannot retrain — and for the plugin that includes the org, because Grafana resolves a stored query's `datasourceUid` inside the requesting org and the scheduler holds one credential, so another org's row would be fetched as the wrong org's series and stored under its key. A row of another org stays due and is refreshed by that org's own overlay load
- **The plugin fetches its own training data** with `POST <Grafana>/api/ds/query` and the query objects the overlay stored verbatim, so no datasource-specific field is ever interpreted in `pkg/`. This introduces **no per-datasource logic and no datasource HTTP client**: the request body is opaque JSON that came from Grafana's own frontend, and the response is decoded with the SDK's `data.Frame` JSON unmarshaller. The type-keyed train rewrite stays in the overlay frontend
- **`trainSource` in the fit request**: `POST /forecast` gains an optional `trainSource {datasourceUid, queries, from, to, seriesName, relative, lookbackMs, panelId, panelTitle, dashboardUid, querySummary}` carrying exactly what the browser sent to the datasource, plus how its window was resolved: a lookback window — Auto **or** a relative picker value (`now-7d`/`now`, i.e. anything the picker's Quick ranges produce) — is `relative: true` with `lookbackMs`, so a cron retrain re-resolves `[now − lookbackMs, now]` at claim time instead of refetching one frozen range forever; only a calendar/absolute pick is `relative: false` and replayed verbatim. `panelId` / `panelTitle` / `dashboardUid` are the panel's own identity (`PanelProps.id` / `.title`, `data.request.dashboardUID` or `/d/<uid>` from the URL) and `querySummary` is a one-line, type-keyed label of the training query (`PromQL: …`, `PPL: …`, `Lucene: …`, `SQL: …`, `Druid SQL: …`, capped at 120 chars) built by the same frontend module that rewrites the targets. A successful fit upserts `forecast.retrain` for that `cacheKey` (never resetting an existing row's cron) and, when the spec carries the panel's identity, stamps that dashboard panel's other keys `superseded_at` so a key the panel no longer uses stops being retrained; the stamp is cleared again if that key is selected once more. `trainSource` and its window **and identification** fields are **not** part of the `cacheKey` fingerprint: storing provenance must never orphan the snapshot the panel is already serving
- **Series identity**: the browser names a training series with `@grafana/data`'s `getFieldDisplayName` (`config.displayName`, then `displayNameFromDS`, then frame/field/label parts, then the duplicate-name index). The backend mirrors that preference in Go and matches a named spec against the raw field name **or** the display name, so a labeled Prometheus/OpenSearch panel retrains at all. When the name matches nothing, a reply that offers exactly one candidate (one numeric field in a frame with a time field) is accepted rather than refusing the row forever; a reply with more than one candidate still fails with `no series named …`, because fitting the wrong series would overwrite the snapshot the panel's cache key points at
- **Owner-guarded finish**: only the claim holder may release a claim. `Finish`/`Done` are `UPDATE … WHERE scope = $1 AND org_id = $2 AND key = $3 AND claimed_by = $owner`, so a retrain that outlived its lease can neither clear a newer owner's claim nor overwrite its `next_run_at`/`last_status` — not even after that owner has finished and released the claim itself. Zero rows updated is a Debug log, not an error
- **Auth auto-disable**: a `401`/`403` from `/api/ds/query` is its own error. Three consecutive ticks ending in that refusal stop the scheduler with one Error log naming `FORECAST_GRAFANA_URL` / `FORECAST_GRAFANA_TOKEN` (or `FORECAST_RETRAIN_ENABLED=false`); a tick that got past the fetch resets the count, so a broken datasource never disables it
- **Per-table store readiness**: the snapshot and schedule probes keep separate readiness and retry state, so a failing `forecast.retrain` DDL can never turn a snapshot `Get` into an error
- **Resource routes**: `GET|PUT /schedules` and `DELETE /schedules?scope=&key=` on the app resource mux, Admin-gated through `backend.PluginConfigFromContext(ctx).User.Role` (`403` otherwise). `GET` lists this org's `panel` rows plus every fleet-wide `baseline` row. `PUT` retimes either scope, but a `scope='baseline'` key must already exist (`404`): those rows are the worker's, derived from the metrics it sees, and a row invented through the API would be claimed by the fleet forever for a hash with no series. An absent `enabled` keeps the stored value, so a `PUT` that only retimes a row never switches it off. `DELETE` removes either scope — a `panel` row of this org, or the fleet-wide `baseline` row (whose worker re-creates it on its next tick while the hash still reports, so deleting a retired hash's row is how its forever-retry ends)
- **Schedule UI**: a table of rows (source, scope, key, cron, timezone, next/last run, status) with cron + timezone editing, an enable toggle, and delete for either scope, on the **Retrain schedules** app page (a tab beside Overview and Configuration), plus a default retrain schedule (`jsonData.retrainCron` / `retrainTimezone`) on the Configuration page. A `panel` row is identified by what trained it — the panel title linked to `/d/<dashboardUid>?viewPanel=<panelId>`, plus the matched series, the stored lookback and the query summary — and `GET /schedules` derives that `source` object from the stored spec; the spec's query objects still never leave the backend. Every overlay request carries `provenance {panelId, panelTitle, dashboardUid, querySummary}` outside `trainSource`, and the backend merges it into an existing panel row (`Identify`; never creates a row, never writes an unchanged merge) on the read paths — a request that carries training points writes the whole spec anyway, so the merge runs only where the panel is served from a stored snapshot, which is where it is needed. A `baseline` row has no spec: its key is the upstream `metric_hash`, so the table offers a copy button for it and the note above the table explains that worker rows are fleet-wide (`org_id = 0`, visible in and shared by every org, created by the worker) while panel rows belong to this org alone. A row a newer key superseded is marked in its Status column (from `supersededAt`), so an Admin can see and delete a key no panel uses any more
- **`needTrain` also fires when the schedule is due** (`next_run_at <= now()`), so a row the scheduler cannot retrain — no stored spec, scheduler disabled — is still refreshed by the next overlay load. `POST /forecast` is unchanged for every other caller
- Config: `FORECAST_RETRAIN_ENABLED`, `FORECAST_RETRAIN_TICK`, `FORECAST_RETRAIN_LEASE`, `FORECAST_RETRAIN_CRON`, `FORECAST_GRAFANA_URL`, `FORECAST_GRAFANA_TOKEN`, following the existing `FORECAST_*` → `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_*` / ini / jsonData / `secureJsonData` precedence
- A scheduler failure never fails a query: if the scheduler is disabled or `/api/ds/query` is unreachable, `needTrain` on the next overlay load remains the retrain path
- **Retrain schedules app page**: a second app config page (id `schedules`, title `Retrain schedules`) shows the `forecast.retrain` row table as a peer tab of Overview / Configuration at `/plugins/%PLUGIN_ID%?page=schedules`. The Default retrain schedule (`jsonData.retrainCron` / `retrainTimezone`) stays on the Configuration page. Grafana gates every app config page on `plugins:write`; the schedule resource API keeps its own Admin gate.

## v13 must-have

The schema is versioned, a pipeline can prepare it before Grafana starts, and every primary key in it is a uuid.

- **Versioned migrations** are SQL files in `pkg/store/migrations` (`NNNN_name.sql`), embedded with `go:embed` and applied by the engine in `pkg/store`. They are the schema's only authority; the old `ensureSQL` constant is gone
- **A standalone migrator** (`cmd/migrate`, executable `gpx_forecast_migrate`, `make migrate`) applies them without starting the plugin: DSN from `--dsn` or the plugin's own `FORECAST_STORE_*` / `GF_PLUGIN_*` env chain (no ini file, no jsonData), `--dry-run` to list pending versions and create nothing, `--timeout` (default 60s), non-zero exit naming the failing file. A CI/CD pipeline runs it before Grafana starts — a one-shot compose service or a Kubernetes Job in `timeseries-grafana-sandbox` / `timeseries-k8s`
- **The plugin still applies the same set at its first store use**, so an installation that never runs the CLI converges one request late, and a migration failure keeps the existing per-table fallback: a readable table the call needs means the store is ready, the schedule path fails loudly while its key shape is wrong, and the attempt is retried after the backoff
- **Ledger and locking**: `forecast.schema_migrations (id uuid PK, version TEXT UNIQUE, name, applied_at)`, created by the engine (not by a migration file) inside `pg_advisory_xact_lock`; one transaction per file, so a failing migration leaves behind neither its DDL nor its ledger row, and a CI job, every Grafana replica's lazy apply and two replicas serialise against each other. No checksum column — an applied file is never edited, a new one is added
- **uuid primary keys**: every table this plugin owns carries `id uuid PRIMARY KEY DEFAULT gen_random_uuid()` (generated by the database, so no new Go dependency and no writer change), with the natural key kept beside it as a `UNIQUE` constraint — `forecast.snapshots` `UNIQUE (org_id, cache_key)`, `forecast.retrain` `UNIQUE (scope, org_id, key)`. Every read, `ON CONFLICT` target and `FOR UPDATE SKIP LOCKED` claim keeps resolving through those indexes
- **An existing table is adopted in place**: `0002_retrain.sql` still widens the legacy `(scope, key)` key to the per-org shape while the catalog reports it, then the uuid blocks add the surrogate key, and every row keeps the org that wrote it
- **PostgreSQL >= 13** (`gen_random_uuid()` is core from 13; both environments in this repo are 17); a ledger version newer than the binary is a warning, never an error
- **Config**: no new setting. The migrator reads `FORECAST_STORE_*` / `GF_PLUGIN_*` exactly as the plugin does, and `--dsn` is a CLI flag only

## v14 must-have

A schedule row does not own a snapshot, and nothing may keep a model whose metric stopped being published.

- **A snapshot is not owned by its row**: deleting a `panel` row leaves the model in place, and the next tick gives it a row back with the deployment default cron (a re-created row carries no `spec`, so the scheduler cannot claim it until a panel load merges its identity and a fit writes the spec). "Delete the schedule" therefore never silently costs a retrain's work
- **Retention runs on the retrain ticker** (`FORECAST_SNAPSHOT_TTL`, default `72h` — three daily cycles —, `0` disables it, a value below `1h` is refused and the default kept), so `FORECAST_RETRAIN_ENABLED=false` turns retention off with the scheduler and no second process or cron is introduced. One transaction per tick, three statements in `pkg/plugin/retention.go`:
  - a snapshot untouched for the window is deleted: neither a scheduled retrain nor a browser fit refreshed it, so the metric stopped being published
  - a row idle for the whole window (`next_run_at` and `last_run_at` both older, or no `last_run_at` at all) with no snapshot behind it is deleted. That is also the rule for a `baseline` row, whose snapshot lives in the worker's own schema. A *failed* retrain keeps a recent `last_run_at` (the owner-guarded finish writes it), so a transient Druid or Grafana outage is never mistaken for a dead metric — and an admin's cron on a live panel whose snapshot is only ever refreshed by browser fits survives, because that snapshot is fresh
  - a snapshot with no row (an admin deleted it, or an earlier release never wrote one) gets a `panel` row back with the deployment default cron, which is why the reconcile can never resurrect a claimable-but-unfetchable row
- **Explicit removal**: `DELETE /schedules?scope=&key=&drop=row|model`. The default `row` keeps v12's behaviour (the row only, the model stays); `model` also deletes the snapshot and every row for that key, superseded siblings included, so the tick's reconcile cannot resurrect what a user removed. A `baseline` key's snapshot is not this process's to delete: the response says so, and the worker's own sweep collects it
- **The worker collects its own**: `baselines.snapshots` is written only by `timeseries-baselines`, so that repo sweeps it (`SNAPSHOT_TTL`, the same default and the same "nothing refreshed it and its row is idle" rule). This process never reads or writes that table

## v1/v2/v3/v4/v5/v6/v7/v8/v9/v10/v11/v12/v13/v14 non-goals

Do not add these without first updating this document:

- Docker Compose / TestData sandbox (those live in `timeseries-grafana-sandbox`)
- Kubernetes Helm / container images (those live in `timeseries-k8s`)
- Publishing or signing on grafana.com
- Prometheus, OpenSearch, or Postgres **datasource HTTP** in `pkg/` (pgx snapshot store is v6)
- Elasticsearch plugin type
- Shipping Grafana alert rules or contact points
- Extra app pages beyond the landing page, the existing Configuration page, and the Retrain schedules page
- Duplicating Series or forecast algorithms
- A Druid/Kafka ticker in this plugin (see `timeseries-baselines`); the v12 scheduler retrains **this plugin's** `forecast.snapshots`, it does not compute minute-of-week baselines
- Consuming the metrics Kafka topic
- Interpreting datasource-specific query content in `pkg/` (v12 replays opaque query objects through `/api/ds/query`; it never rewrites them)
- Shipping Grafana alert rules, contact points, or a job queue
- A second migration tool (Flyway, goose, golang-migrate) or any schema-diff ORM in front of these files
- An integer surrogate key on any table this plugin owns, and a Go-side uuid generator (the database's `gen_random_uuid()` is the only one)
- Retention outside the retrain ticker: a second scheduler, a Grafana-side cron, a job queue, a separate collector process, or a table the worker owns. Deleting a snapshot is not a reason to stop a query either — the overlay's `needTrain` path already answers a missing snapshot

## Quality bar

- Backend does not mutate caller series (libraries already return new series)
- Invalid model/series/level/forecast range map to HTTP 400
- Table-driven tests cover golden paths for the resource and datasource `QueryData`, plus oversize / busy (413/429) load limits
- Table-driven tests cover the schedule resource (method/status matrix, Admin gate, upsert→list round-trip, invalid cron, the baseline-row rules — an existing worker key can be retimed, an invented one is `404`, either scope can be deleted — per-org key isolation where two orgs share one cache key and keep separate rows, the derived `source` summary and its cap, and that a listing never echoes the stored query objects) and `needTrain`-when-due
- Table-driven frontend tests cover train rewrite, extract/match, cache fingerprint, mixed metric vs Forecast frames, forecast-query `cacheKey`, overlay New alert rule defaults, overlay load limits, `trainSource` capture, the schedule API, and the schedule UI
- `trainSource` must not enter the `cacheKey` fingerprint: adding or editing a scheduled retrain must not invalidate a stored snapshot
- The retrain scheduler must never fail a query: an unreachable `/api/ds/query`, a disabled scheduler, or a claim error leaves `needTrain` as the retrain path
- A schedule row belongs to one org: `forecast.retrain` keeps `(scope, org_id, key)` unique, `0002_retrain.sql` rewrites an older table to it in place, and a table left on the old key fails the schedule store instead of sharing rows between orgs
- The scheduler claims and retrains only the org its Grafana credential belongs to; another org's row stays due for that org's own overlay load
- Every primary key this plugin owns is a uuid (`gen_random_uuid()`), the natural key beside it is `UNIQUE`, and an applied migration file is never edited
- Retention never loses a live model: a snapshot is collected only when nothing refreshed it within `FORECAST_SNAPSHOT_TTL`, and a row only when it is idle by its own clocks and has no snapshot behind it. `0` disables the sweep and a window below `1h` is refused rather than obeyed
- A deleted schedule row does not delete the model: the sweep re-creates the row with the deployment default, and `drop=model` is the only way a caller removes the snapshot (a `baseline` key's snapshot belongs to the worker and is reported as such)
- GitHub Actions on `main` runs `gofmt` over `./pkg ./cmd` and `Magefile.go`, `go test -race ./pkg/... ./cmd/...`, the linux backend and migrator builds, the migration CLI against the service Postgres, and frontend lint/typecheck/jest/webpack
