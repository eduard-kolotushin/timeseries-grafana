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
| Train adapters | frontend only (Prometheus, OpenSearch, Postgres, existing Druid/SQL rewrite). No Prom/OS/PG HTTP clients in `pkg/`. The v12 scheduler replays the frontend's query objects through Grafana's own `/api/ds/query`, or through PVV's `/gse/data/v1/DataByRefId` in v18 (a b2b retrain transport, not a datasource client) |
| Compute placement | inline in the Grafana-managed `gpx_forecast` process by default; the standalone `gpx_forecast_compute` service when `FORECAST_COMPUTE_URL` is set (v16). Either way frames come from Grafana's own `/api/ds/query`, or PVV's `/gse/data/v1/DataByRefId` when the v18 PVV transport is configured |

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
- The window may reach into or before the training range: every `last+k×step` grid point in `[from, to]` is emitted for any integer `k`, so `k ≤ 0` backcasts the final fitted state. Bands there come only from the baseline's stored per-key SEs; every other model answers `null` bounds. A window with no grid point still shows its reason
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

- Backend DSN resolution: `FORECAST_STORE_*` env (Grafana 12.4+ does not forward host env into plugin processes by default), then Grafana `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_*` / `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_DATASOURCE_*`, then the id-less `GF_PLUGIN_<KEY>` spelling Grafana 13 exports, then GrafanaCfg, then jsonData / `secureJsonData`. Empty host (and no URL): persist off
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
- Do not add a job queue, extra `gpx_forecast` replicas, Grafana core changes, SIMD, or a parallel public API. Scaling fitting means more **compute-service** replicas (v16), never more Grafana-managed plugin processes

## v12 must-have

Retrain stored snapshots on a cron with **no browser open**, and expose the schedule so an operator can see and edit it. The backend fetches its own training data through Grafana's own query API.

- **Backend retrain scheduler** in the app process (or, under v16, in the standalone compute service when `compute_url` moves fitting there): one ticker (`FORECAST_RETRAIN_TICK`, default 30s) that claims due rows from Postgres and retrains them. Started only when a store, a claimable row, and the Grafana query API are all available; `Dispose` cancels it, and three consecutive `401`/`403` refusals from Grafana end it too (a configuration error it cannot retry its way out of)
- **`forecast.retrain` table** (schema `forecast`, created by the migration engine in `gpx_forecast` — see v13; the plugin is its only DDL owner). Columns: `scope`, `key`, `org_id`, `cron`, `timezone`, `enabled`, `spec JSONB`, `next_run_at`, `last_run_at`, `last_status`, `claimed_by`, `claimed_until`, `updated_at`, `superseded_at`; keyed `(scope, org_id, key)` by a `PRIMARY KEY` in v12 and a `UNIQUE` constraint beside the uuid primary key from v13 on. `org_id` is part of the key because a panel's `cacheKey` is org-independent (the same dashboard and datasource uids hash the same in every org): keyed `(scope, key)`, two orgs would share one row, and either org's fit would rewrite the other's cron and stored query objects while the other could not even list it. `0002_retrain.sql` migrates a table created with the old key in place (rows keep the org that wrote them), and `ensureSchedules` refuses to serve a table whose key is still `(scope, key)` instead of quietly sharing rows between orgs. `scope` is `panel` (overlay `cacheKey`, this org's row) or `baseline` (`metric_hash`, written by sibling `timeseries-baselines`, which never creates the table, and stored at the fleet-wide `org_id = 0`; the worker retires a row whose metric has been stale for `STALE_AFTER`, baselines v7).
- **Claim queue**: `Claim` is `UPDATE … FROM (SELECT … FOR UPDATE SKIP LOCKED)`, so Grafana HA (one plugin process per replica) retrains each due row exactly once without a leader. Lease `FORECAST_RETRAIN_LEASE` (derived from the claim batch and the fetch timeout — 6m today, which stays above the batch's worst case) releases a row whose worker died. The plugin claims only `scope='panel'` rows **of its own org** that have a `spec` with a non-empty `queries` array and no `superseded_at` stamp; the worker claims only `scope='baseline'` and likewise skips a superseded row. Neither can claim a row it cannot retrain — and for the plugin that includes the org, because Grafana resolves a stored query's `datasourceUid` inside the requesting org and the scheduler holds one credential, so another org's row would be fetched as the wrong org's series and stored under its key. A row of another org stays due and is refreshed by that org's own overlay load
- **The plugin fetches its own training data** with `POST <Grafana>/api/ds/query` and the query objects the overlay stored verbatim, so no datasource-specific field is ever interpreted in `pkg/`. This introduces **no per-datasource logic and no datasource HTTP client**: the request body is opaque JSON that came from Grafana's own frontend, and the response is decoded with the SDK's `data.Frame` JSON unmarshaller. The type-keyed train rewrite stays in the overlay frontend
- **`trainSource` in the fit request**: `POST /forecast` gains an optional `trainSource {datasourceUid, queries, from, to, seriesName, relative, lookbackMs, panelId, panelTitle, dashboardUid, querySummary}` carrying exactly what the browser sent to the datasource, plus how its window was resolved: a lookback window — Auto **or** a relative picker value (`now-7d`/`now`, i.e. anything the picker's Quick ranges produce) — is `relative: true` with `lookbackMs`, so a cron retrain re-resolves `[now − lookbackMs, now]` at claim time instead of refetching one frozen range forever; only a calendar/absolute pick is `relative: false` and replayed verbatim. `panelId` / `panelTitle` / `dashboardUid` are the panel's own identity (`PanelProps.id` / `.title`, `data.request.dashboardUID` or `/d/<uid>` from the URL) and `querySummary` is a one-line, type-keyed label of the training query (`PromQL: …`, `PPL: …`, `Lucene: …`, `SQL: …`, `Druid SQL: …`, capped at 120 chars) built by the same frontend module that rewrites the targets. A successful fit upserts `forecast.retrain` for that `cacheKey` (never resetting an existing row's cron) and, when the spec carries the panel's identity, stamps that dashboard panel's other keys `superseded_at` so a key the panel no longer uses stops being retrained; the stamp is cleared again if that key is selected once more. `trainSource` and its window **and identification** fields are **not** part of the `cacheKey` fingerprint: storing provenance must never orphan the snapshot the panel is already serving
- **Series identity**: the browser names a training series with `@grafana/data`'s `getFieldDisplayName` (`config.displayName`, then `displayNameFromDS`, then frame/field/label parts, then the duplicate-name index). The backend mirrors that preference in Go and matches a named spec against the raw field name **or** the display name, so a labeled Prometheus/OpenSearch panel retrains at all. When the name matches nothing, a reply that offers exactly one candidate (one numeric field in a frame with a time field) is accepted rather than refusing the row forever; a reply with more than one candidate still fails with `no series named …`, because fitting the wrong series would overwrite the snapshot the panel's cache key points at
- **Owner-guarded finish**: only the claim holder may release a claim. `Finish`/`Done` are `UPDATE … WHERE scope = $1 AND org_id = $2 AND key = $3 AND claimed_by = $owner`, so a retrain that outlived its lease can neither clear a newer owner's claim nor overwrite its `next_run_at`/`last_status` — not even after that owner has finished and released the claim itself. Zero rows updated is a Debug log, not an error
- **Auth auto-disable**: a `401`/`403` from `/api/ds/query` is its own error. Three consecutive ticks ending in that refusal stop the scheduler with one Error log naming `FORECAST_GRAFANA_URL` / `FORECAST_GRAFANA_TOKEN` (or `FORECAST_RETRAIN_ENABLED=false`); a tick that got past the fetch resets the count, so a broken datasource never disables it
- **Per-table store readiness**: the snapshot and schedule probes keep separate readiness and retry state, so a failing `forecast.retrain` DDL can never turn a snapshot `Get` into an error
- **Resource routes**: `GET|PUT /schedules` and `DELETE /schedules?scope=&key=` on the app resource mux, Admin-gated through `backend.PluginConfigFromContext(ctx).User.Role` (`403` otherwise). `GET` lists this org's `panel` rows plus every fleet-wide `baseline` row. `PUT` retimes either scope, but a `scope='baseline'` key must already exist (`404`): those rows are the worker's, derived from the metrics it sees, and a row invented through the API would be claimed by the fleet forever for a hash with no series. An absent `enabled` keeps the stored value, so a `PUT` that only retimes a row never switches it off. `DELETE` removes either scope — a `panel` row of this org, or the fleet-wide `baseline` row (the worker retires a stale metric's row and model itself; deleting a row while the hash still reports is the manual way, and its worker re-creates it on its next tick)
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

## v15 must-have

Retrain reliability: a row that fails, or that is interrupted by a restart, is never lost, is never retrained twice at once, and never retries in a storm.

- **At-least-once, never abandoned.** A due row is retrained by exactly one claim holder, and bookkeeping never advances a claim past its lease: an unfinished claim (crash, restart, network partition) is re-admitted to any instance once `claimed_until < now()` and the row is still due, and the claim predicate never requires a particular owner. No due row is lost while one instance lives
- **The lease covers the work it protects.** Immediately before a row's work starts, its claim is extended to `now() + FORECAST_RETRAIN_LEASE` and the work runs under a deadline equal to that lease, so a fit cannot outlive its claim (and be retrained concurrently by another replica, duplicating the same heavy queries) nor be cut short. An extension that matches zero rows means the claim was handed to a newer owner: that row is dropped without fitting it and without finishing it, so the new owner's `next_run_at`/`last_status` are never overwritten
- **Retries are bounded in rate, never given up.** A failed retrain increments a persisted `attempts` count on the row and is due again after `min(base × 2^(attempts-1), cap)`, base `FORECAST_RETRAIN_LEASE` and cap `FORECAST_RETRAIN_RETRY_MAX` (default `1h`; refused and the default kept when it is not longer than the lease). A success resets the count to `0` and schedules the next cron slot, so a permanently broken row settles at one retry per hour instead of one per lease forever, and its attempt count is carried in `last_status` (`error: … (attempt N)`) and the logs. A busy compute slot (`errBusy`) burns no attempt: the row never ran. There is no dead-letter queue and no disable-after-N-failures — an Admin disables or deletes the row through `/schedules`, and `needTrain` still refreshes a panel whose row the scheduler cannot retrain
- **The backlog is observable.** Every tick that claimed something logs the rows claimed, retrained and failed, so an operator can watch the queue drain — or fail to
- **A row's lease is the only thing that expires**: `GET /schedules` and the Retrain schedules page are unchanged (the attempt count already rides `lastStatus`), and a migration failure keeps the existing per-table fallback, so a `forecast.retrain` without the `attempts` column fails the schedule store loudly (the message names `gpx_forecast_migrate`) instead of silently claiming nothing

## v16 must-have

Fitting capacity scales on its own. One deployment variable moves the fit and the retrain ticker out of the Grafana-managed process, and the split deployment keeps getting its training frames through Grafana's own datasource proxy.

- **Compute mode**: `FORECAST_COMPUTE_URL` (env `FORECAST_*` → `GF_PLUGIN_*` → ini `compute_url` → jsonData `computeUrl`) selects where fitting runs. Unset — the default — keeps every fit and the retrain ticker in the Grafana-managed `gpx_forecast` process, so an existing installation is unchanged. Set, that process forwards `POST /forecast` verbatim to the standalone compute service and runs **no** ticker, so one implementation of the dispatch semantics (`due`, `needTrain`, provenance merges, the store writes) serves both modes and the panel's resource URL, body and answer shape are untouched
- **Compute service**: `cmd/compute` → `gpx_forecast_compute` (`make compute`), the same `pkg/plugin` handlers over plain HTTP, with the Grafana SDK's request identity replaced by two things the trusted plugin sends: `Authorization: Bearer FORECAST_COMPUTE_TOKEN` (constant-time compared) and `X-Forecast-Org`, which the plugin takes from the org Grafana authenticated and the service injects as the SDK plugin context, so a browser user cannot name another org and the store keys stay org-scoped exactly as inline. It reads its configuration from the process env only (like `gpx_forecast_migrate`), refuses to start without the token, **ignores** `FORECAST_COMPUTE_URL` (its mode is fixed at "no forwarding", so a shared env block cannot make one compute service forward to another), and mounts only `/forecast`, `/ping` and an unauthenticated, coarse `/healthz`. Replicas share one Postgres, so the v12 claim queue makes N tickers safe: each due row is claimed once, and the v15 lease/extension/owner-guarded finish keep it that way
- **Training data comes from Grafana's own `/api/ds/query`, or from PVV's `/gse/data/v1/DataByRefId` when the v18 PVV transport is configured** (`FORECAST_GRAFANA_URL` / `FORECAST_GRAFANA_TOKEN`, which a split deployment must point at Grafana's service rather than the plugin's default loopback), so no datasource client enters `pkg/` and a templated or datasource-specific query stays opaque
- **Unchanged in remote mode**: the panel and its resource URL, alerting `QueryData` (a local store Restore, so alerting and the schedules page keep working from the same Postgres while the compute service is down), `GET|PUT|DELETE /schedules` and `POST /schedules/default`, and the plugin's body cap on the forwarded path (413 before any decode or upstream call). A forwarding slot is a socket rather than a fit, so the remote limiter default is wider (`defaultMaxProxyInflight`, 32) where the inline default is unchanged; `FORECAST_MAX_INFLIGHT` overrides either
- **Failure is explicit**: an unreachable compute service is a 502 naming the failure — never a silent local fit, which would hide a split deployment whose compute half is down while the plugin quietly burns Grafana CPU. `CheckHealth` in remote mode additionally probes the compute service's `/healthz`, so the split is observable from Grafana
- **Config**: `FORECAST_COMPUTE_URL` / `FORECAST_COMPUTE_TOKEN` (`COMPUTE_URL` / `COMPUTE_TOKEN`, ini `compute_url` / `compute_token`, jsonData `computeUrl` / `computeToken` + `secureJsonData.computeToken`), plus `FORECAST_COMPUTE_LISTEN` (default `:8080`, `--listen` overrides) and `FORECAST_COMPUTE_TOKEN` on the service. Sharing one env block with the plugin is safe: the service ignores the URL

## v17 must-have

A complete alert rule is one click away on a trained overlay panel, and it compares the right instants.

- Overlay options **Alerting** builds a complete Grafana alert rule from the panel's own state, so no Mixed
  datasource, hand-filled Forecast row, or **Copy source from query A** press is needed: **New alert rule** copies
  the panel's metric queries and synthesizes one Forecast-datasource query from the panel's fit options (model and
  its parameters, training range, legacy lookback), with the visible metric targets as `sourceTargets`, the chosen
  series name, that series' labels, and the **same cacheKey** the overlay fit wrote, so the rule restores the
  trained snapshot instead of answering `needTrain`
- **Alert series** (a select shown only when the panel offers more than one series), **Alert lookback** (default
  `10m`) and **Alert tolerance** (percent, default 20) are panel options; the series is the only input the operator
  supplies, and an unloaded panel, a panel with no metric query to an alerting-capable datasource, or a deployment
  with no Forecast datasource instance answers a reason instead of producing a broken rule. The select shows a
  `Select a series` placeholder until one is chosen — never an unchosen series as if it had been picked
- Both rule queries carry `relativeTimeRange { from: <lookback>, to: 0 }` — never a dashboard-derived range — so
  the metric and the baseline reduce their last value at the same instant
- The expression chain is Reduce(`last`) over the metric, Reduce(`last`) over the baseline, Math
  `abs((metric - baseline) / baseline) * 100`, and a Threshold `gt <tolerance>` on that Math as the **alert
  condition**; the panel's own expression rows are no longer reused or forwarded
- A Forecast-datasource restore labels its frame from the query model's `seriesLabels`, so a metric query that
  returns several series pairs only the chosen one (Grafana's Math node matches by labels and drops the rest) and
  the alert instance carries that series' labels — the measured behaviour of every labelled datasource
  (Prometheus, Druid SQL wide with a `GROUP BY` dimension, Postgres `time_series` dimension columns, OpenSearch
  group-by keys). When the chosen row's series carry **no** labels, the rule instead names the series the way the
  evaluation does: for the datasources where Grafana synthesizes a name
  (graphite / testdata / dynatrace / influxdb, legacy conversion path) the baseline is labelled
  `{__name__: <DisplayNameFromDS | DisplayName | field name, the first that is non-empty and unique in that query>}`,
  and otherwise the rule is built with a **`sql` expression as the alert condition** that selects the series by
  `__metric_name__` / `__display_name__`, computes `abs((metric - baseline) / baseline) * 100` against
  `Alert tolerance` and returns 1 / 0 (Grafana maps `0` to Normal, non-zero to Alerting, NULL to NoData). That
  path needs Grafana's `sqlExpressions` feature (GA and default-on in 13.1, admin-enabled before it); with the
  feature off, and for a row whose series no column or label can name, the button answers a reason instead of
  writing a rule
- A rule is only ever built for one series: the two label-free shapes above keep that promise without sending a
  label the datasource never set (which would make Grafana broadcast the single baseline across every series and
  alert on series the operator did not choose)
- Do not ship alert rules or contact points

## v18 must-have

Unattended retraining fetches a panel's training frames through either Grafana's
`/api/ds/query` or PVV's `/gse/data/v1/DataByRefId`, selected by deployment
configuration, with no datasource type or datasource field name in `pkg/`.

- Every fit stores the per-series `refId` in `trainSource`. `refId` is a generic Grafana
  query field; it stays out of `cacheKey`.
- PVV mode is selected by either `FORECAST_PVV_CLIENT_CERT` or `FORECAST_PVV_CLIENT_KEY`;
  both are required to make requests. The pair uses mTLS, with optional
  `FORECAST_PVV_CA_CERT` / `FORECAST_PVV_TLS_SKIP_VERIFY`. `FORECAST_PVV_URL` defaults to
  `FORECAST_GRAFANA_URL`. In PVV mode the ticker does not call `/api/org` or
  `/orgs`; it claims every due `scope='panel'` row regardless of org and
  fetches each through DataByRefId with that row's `org_id`. A PVV failure is the row's
  failure; there is no `/api/ds/query` fallback inside PVV mode.
- Grafana mode (no PVV client certificate/key) is the existing behavior: `/api/org`, claim
  that org, `/api/ds/query`.
- Windows are split into `FORECAST_RETRAIN_WINDOW_CHUNK` slices (default and maximum
  `24h`, sequential) for PVV's range cap; chunk series are merged, deduplicated and bounded
  by `MAX_TRAIN_POINTS`.
- `queryHash` is not sent; a changed panel query is fetched as the live query.
- The PVV client is a b2b retrain transport, not a datasource client. It never interprets
  datasource-specific query content and is not a second scheduler or fit implementation.

## v19 must-have

The plugin→compute hop can be mTLS, with every certificate issued dynamically by
HashiCorp Vault's PKI secrets engine and re-issued before it expires. Unset Vault
configuration keeps v16's plain HTTP plus shared token exactly as shipped.

- **Certificates come from Vault PKI only**: `pkg/vault` is a small stdlib client
  (`net/http`, no `hashicorp/vault/api` dependency) that logs in, calls
  `POST /v1/<pki_mount>/issue/<role>` with a common name and TTL, and parses
  `certificate` / `private_key` / `ca_chain`. The compute hop has no file-based
  certificate source.
- **Auth**: a static token (`FORECAST_VAULT_TOKEN`, then bare `VAULT_TOKEN`),
  AppRole (`FORECAST_VAULT_ROLE_ID` + `FORECAST_VAULT_SECRET_ID`), or Kubernetes
  (`FORECAST_VAULT_K8S_ROLE`, JWT re-read from the service-account file at each
  login), in that precedence. Login-based tokens are re-acquired before expiry and
  once on a `403`; a renewable static token calls `renew-self`.
- **Rotation**: a cert manager re-issues at half the issued leaf's remaining
  lifetime (default TTL `FORECAST_VAULT_CERT_TTL=24h`), keeps the last good cert
  when Vault is down, and retries every 30 s. The compute listener serves the
  current cert per handshake and requires a client cert; the plugin's forwarding
  client swaps its transport on renew. No process restart is needed to rotate.
- **Compute service**: with Vault configured the service refuses to start unless
  `FORECAST_VAULT_PKI_ROLE`, `FORECAST_COMPUTE_TLS_SERVER_NAME` and a working
  initial issue are all present; it listens TLS-only (TLS 1.2+,
  `RequireAndVerifyClientCert`). Without Vault it is the v16 plain listener.
- **Plugin client**: remote mode plus Vault requires an `https` compute URL (an
  `http` URL answers 502 with that reason, never a silent downgrade); the expected
  server name is `FORECAST_COMPUTE_TLS_SERVER_NAME` or the URL hostname.
  `/forecast` and the `/healthz` probe use the same mTLS client, and `Dispose`
  stops the re-issue goroutine.
- **PVV too**: when Vault is configured and `FORECAST_VAULT_PKI_PVV_ROLE` is set,
  the retrain transport's client certificate is issued from Vault (CN
  `FORECAST_VAULT_PVV_CN`) instead of `FORECAST_PVV_CLIENT_CERT`/`_KEY`; PVV's
  server CA stays the existing `FORECAST_PVV_CA_CERT` file. PVV mode is selected
  by either source.
- **Config chain**: every key follows `FORECAST_*` env → `GF_PLUGIN_*` → grafana.ini
  `vault_*` → jsonData `vault*`, with `vaultToken` / `vaultSecretId` accepted from
  `secureJsonData`. The compute service remains process-env only.
- **Deployment**: `timeseries-k8s` v5 renders the `vault.*` values and switches
  `computeUrl` to `https` when enabled, with no credential in a ConfigMap; the
  sandbox v9 runs a dev Vault and proves the hop live.
- Without Vault, bytes, status, limiter default and limits are exactly v16/v18.

## v20 must-have

mTLS is selectable per component, not one Vault-wide switch: the compute hop,
PVV's retrain transport, and the Postgres snapshot store each turn on
independently, because a VM install enforces mTLS in application configuration
while a Kubernetes install leaves it to Istio and runs plain traffic.

- **Three booleans**: `FORECAST_COMPUTE_MTLS`, `FORECAST_PVV_MTLS`,
  `FORECAST_STORE_MTLS` (GF_PLUGIN suffixes, ini `compute_mtls` / `pvv_mtls` /
  `store_mtls`, jsonData `computeMtls` / `pvvMtls` / `storeMtls`). Unset defaults
  to "that component's Vault PKI role is configured"; an explicit `false` turns
  that component off even when its role is set.
- **Compute hop**: on requires `FORECAST_VAULT_PKI_ROLE` and an `https` URL; off
  forwards over plain HTTP exactly as v16, even when Vault is configured for PVV
  or the store. `ServeCompute` follows the same flag: on requires the role and
  `FORECAST_COMPUTE_TLS_SERVER_NAME` and listens TLS-only; off keeps the v16 plain
  listener.
- **PVV**: on requires `FORECAST_VAULT_PKI_PVV_ROLE` and issues the client
  certificate from Vault; off leaves the v18 file pair as the only source (or
  disables PVV when no files exist), with no certificate issued.
- **Postgres**: on requires `FORECAST_VAULT_PKI_STORE_ROLE`; the client
  certificate is issued from Vault with CN `FORECAST_VAULT_STORE_CN` (default the
  resolved store user), and pgx presents it through a `tls.Config`
  `GetClientCertificate` callback, so new pool connections pick up a rotated
  certificate without recreating the pool. The root pool is the CA chain of the
  first issue; rotating the Vault CA needs a rolling restart.
- The same store TLS configuration serves the app process, the nested datasource
  process, the standalone compute service, and `gpx_forecast_migrate` (the CLI
  resolves the Vault env itself and fails loudly when mTLS is on without a role).
- A configured Vault with **no** component role is still a loud error
  (`forecast: vault: no component role is configured (compute, pvv, store)`).
  A flag explicitly on without its role is a per-component error, never a silent
  plaintext fallback.
- Without a Vault address every switch is off; bytes, limits and wire shapes are
  exactly v19's non-Vault behavior.
- Live proof is the sandbox: overlay-postgres serves TLS and requires
  `clientcert=verify-full` for a dedicated `forecast` store user, while Grafana's
  core Postgres datasource and the seed jobs keep plain scram on the same server.

## v21 must-have

The Vault API is itself a TLS hop, and it is selectable like the others. A Vault
listener may require a client certificate, but that certificate cannot be issued
by the same Vault (a bootstrap loop), so this hop uses a file pair rather than
PKI.

- **One boolean**: `FORECAST_VAULT_MTLS` (GF_PLUGIN suffix, ini `vault_mtls`,
  jsonData `vaultMtls`). Unset defaults to "either client-certificate path
  is set" (so a lone half is a loud construction error); an explicit `false`
  ignores the pair even when the files exist.
- **File pair**: `FORECAST_VAULT_CLIENT_CERT` and `FORECAST_VAULT_CLIENT_KEY`
  (`vault_client_cert` / `vault_client_key`, jsonData `vaultClientCert` /
  `vaultClientKey`). Both are required when the switch is on; a missing half is a
  loud construction error. Every certificate setting (these two, the Vault CA,
  and PVV's client/CA values) is a **file path**, never PEM content; a value
  containing a PEM block is rejected at construction. The pair is loaded when
  the client is built, so rotating the Vault client certificate needs a process
  restart. Server verification remains `FORECAST_VAULT_CA_CERT` /
  `FORECAST_VAULT_TLS_SKIP_VERIFY`.
- The same env/lookup chain reaches the app, the nested datasource, the compute
  service and `gpx_forecast_migrate`; a configured Vault with no component role
  is still the v20 error, independent of this hop.
- Live proof: a client-certificate-requiring TLS proxy in front of the sandbox
  Vault accepts the compute service and migrator with the configured pair and
  refuses a client without it.

## v22 must-have

The compute hop, PVV's retrain transport and the Postgres snapshot store also
accept a **local file certificate pair**, so an installation that cannot use
Vault PKI still runs mTLS. The existing per-component switch chooses whether the
component uses TLS and the configured material chooses the source; no new source
flag is added.

- **Source precedence**: when a component's Vault PKI role is configured, Vault
  issues the certificate exactly as v19/v20 (and wins if a file pair is also
  present). With no role, a configured client certificate/key pair is loaded
  from disk. A switch that is on with neither source is a loud per-component
  error, never a plaintext fallback.
- **Compute client**: `FORECAST_COMPUTE_CLIENT_CERT` /
  `FORECAST_COMPUTE_CLIENT_KEY`, optional `FORECAST_COMPUTE_CA_CERT` (system
  roots when empty) and `FORECAST_COMPUTE_TLS_SERVER_NAME` (URL host when
  empty). The URL must still be `https`.
- **Compute listener**: `FORECAST_COMPUTE_SERVER_CERT` /
  `FORECAST_COMPUTE_SERVER_KEY` plus `FORECAST_COMPUTE_CA_CERT` to verify client
  certificates. The listener keeps `RequireAndVerifyClientCert`; server and
  client pairs are separate because they belong to different processes.
  `FORECAST_COMPUTE_MTLS` remains the on/off switch; unset defaults to "a compute
  role or either compute pair is configured".
- **PVV**: the v18 `FORECAST_PVV_CLIENT_CERT` / `FORECAST_PVV_CLIENT_KEY` /
  `FORECAST_PVV_CA_CERT` pair is now actually governed by `FORECAST_PVV_MTLS`,
  like every other component: unset defaults on when a pair exists, explicit
  `false` disables PVV even when files exist.
- **Store**: `FORECAST_STORE_CLIENT_CERT` / `FORECAST_STORE_CLIENT_KEY`, optional
  `FORECAST_STORE_CA_CERT` and optional `FORECAST_STORE_TLS_SERVER_NAME` (pgx
  host when empty).
- Every certificate setting remains a **file path**, never PEM content; the same
  guards as v21 reject a PEM block. Local material is loaded once when the
  client, pool or listener is built, so rotating a local certificate needs a
  process restart (Vault-issued material keeps its half-life rotation).
- A deployment with no Vault address may still run any component from files. A
  configured Vault with no component role and no local pair is still the v20
  loud error. The same env/lookup chain reaches the app, the nested datasource,
  the compute service and `gpx_forecast_migrate`.

## v1/v2/v3/v4/v5/v6/v7/v8/v9/v10/v11/v12/v13/v14/v15/v16/v17/v18/v19/v20/v21/v22 non-goals

Do not add these without first updating this document:

- Docker Compose / TestData sandbox (those live in `timeseries-grafana-sandbox`)
- Kubernetes Helm / container images (those live in `timeseries-k8s`)
- Publishing or signing on grafana.com
- Prometheus, OpenSearch, or Postgres **datasource HTTP** in `pkg/` (pgx snapshot store is v6). The v18 PVV `/gse/data/v1/DataByRefId` client is the named exception: it is a b2b retrain transport selected by configuration, not a datasource client
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
- Retention outside the retrain ticker: a second scheduler, a Grafana-side cron, a job queue, a separate collector process, or a table the worker owns. `gpx_forecast_compute` (v16) is the named exception to "a separate process": it is not a second scheduler, it is the same ticker running where the fit runs, and it claims the same rows through the same lease protocol. Deleting a snapshot is not a reason to stop a query either — the overlay's `needTrain` path already answers a missing snapshot
- A second implementation of the fit or the dispatch semantics, or a second compute transport: the service runs the same `pkg/plugin` handlers, and remote mode forwards the request rather than re-deriving `due` / `needTrain` / provenance locally
- A compute service that reads grafana.ini or jsonData, or that authenticates with anything but the shared token and the org the plugin asserts, **except** the v19 mTLS layer: the token and the asserted org stay, and Vault-issued certificates add transport authentication. Its configuration is process env only, exactly like `gpx_forecast_migrate`
- A certificate source beyond Vault PKI and the local file pair of v21/v22: no Vault KV certificate source, no `hashicorp/vault/api` dependency, and no third source (ACME, cert-manager API, an inline PEM setting). Vault wins when a component has both a role and files; PEM content is always refused.
- A remote **plugin backend** — Grafana dialing a `gpx_forecast` gRPC server on another host. **Settled: the compute transport is HTTP** (`gpx_forecast_compute`, one authenticated route), and this stays a non-goal. The SDK's standalone client mode does exist — `GF_PLUGIN_GRPC_ADDRESS_EDUARDKOLOTUSHIN_FORECAST_APP` (or `standalone.txt` / `pid.txt` beside the local plugin binary) makes the spawned process print the `1|2|tcp|<address>|grpc` locator for *that* address instead of serving itself, so Grafana need not dial its own child — but it is a debugging facility, not a deployment: the plugin directory and `plugin.json` must still exist locally, the SDK ships `FindAndKillCurrentPlugin`/`pgrep` helpers around it, and the hop carries no TLS and no token of its own (the SDK's plugin `ServeOpts` expose none), so a network-exposed plugin port would answer `forecast.snapshots`, `forecast.retrain` and every resource route as the plugin for anyone who can reach it. Reviving it needs a new entry here, not a config knob
- A panel that talks to the compute service directly (the shared token must never reach a browser) and a silent local fallback when the compute service is unreachable
- A dead-letter queue, a disable-after-N-failures path, or a retry backoff that is not the capped exponential one above. A permanently broken row is retried at the cap until an Admin disables or deletes it, and its attempt count stays in `last_status` rather than becoming a new `GET /schedules` field

## Quality bar

- Backend does not mutate caller series (libraries already return new series)
- Invalid model/series/level/forecast range map to HTTP 400
- Table-driven tests cover golden paths for the resource and datasource `QueryData`, plus oversize / busy (413/429) load limits
- Table-driven tests cover the schedule resource (method/status matrix, Admin gate, upsert→list round-trip, invalid cron, the baseline-row rules — an existing worker key can be retimed, an invented one is `404`, either scope can be deleted — per-org key isolation where two orgs share one cache key and keep separate rows, the derived `source` summary and its cap, and that a listing never echoes the stored query objects) and `needTrain`-when-due
- Table-driven frontend tests cover train rewrite, extract/match, cache fingerprint, mixed metric vs Forecast frames, forecast-query `cacheKey`, overlay New alert rule synthesis (the shared metric/baseline window, the two Reduce nodes, the Math deviation, the Threshold condition, the chosen series' `seriesName` / `seriesLabels` / `sourceTargets`, and a `cacheKey` equal to the overlay's — plus `seriesTarget`'s identity decision (labels → the synthesized `__name__` → the SQL column → a reason) and `deviationSQL`'s golden statement), overlay load limits, `trainSource` capture, the schedule API, and the schedule UI
- `trainSource` must not enter the `cacheKey` fingerprint: adding or editing a scheduled retrain must not invalidate a stored snapshot
- The retrain scheduler must never fail a query: an unreachable `/api/ds/query`, a disabled scheduler, or a claim error leaves `needTrain` as the retrain path
- A schedule row belongs to one org: `forecast.retrain` keeps `(scope, org_id, key)` unique, `0002_retrain.sql` rewrites an older table to it in place, and a table left on the old key fails the schedule store instead of sharing rows between orgs
- The scheduler claims and retrains only the org its Grafana credential belongs to; another org's row stays due for that org's own overlay load
- Every primary key this plugin owns is a uuid (`gen_random_uuid()`), the natural key beside it is `UNIQUE`, and an applied migration file is never edited
- Retention never loses a live model: a snapshot is collected only when nothing refreshed it within `FORECAST_SNAPSHOT_TTL`, and a row only when it is idle by its own clocks and has no snapshot behind it. `0` disables the sweep and a window below `1h` is refused rather than obeyed
- Compute mode is a switch, not a second code path: the same request answers the same bytes with the URL unset and with it set to a compute service built from the same handlers, and the compute service mounts `/forecast`, `/ping` and `/healthz` only
- The compute hop is authenticated and org-faithful: a missing or wrong token is refused, `X-Forecast-Org` must be a non-negative integer, the plugin overwrites any caller-supplied copy, and the store writes land under the asserted org. An unreachable compute service is a 502 with a reason, never a silent local fit. With compute mTLS on the hop additionally requires a client certificate, an `http` compute URL is refused, and a failed issue is a 502 or a startup refusal, never a plaintext downgrade. Each component's switch is independent: compute, PVV and the Postgres store can be on or off separately, and a flag turned on without a Vault role **or** a local certificate pair is an error rather than a silent downgrade. Local files are paths, PEM content is refused, and a source conflict resolves to Vault
- The ticker lives in exactly one place per deployment: remote mode starts none in the plugin, and N compute replicas retrain each due row once through the v12/v15 claim protocol
- Inline behaviour is unchanged: with `FORECAST_COMPUTE_URL` unset the limiter default, the ticker and the `/forecast` handler are exactly what v15 shipped
- A deleted schedule row does not delete the model: the sweep re-creates the row with the deployment default, and `drop=model` is the only way a caller removes the snapshot (a `baseline` key's snapshot belongs to the worker and is reported as such)
- GitHub Actions on `main` runs `gofmt` over `./pkg ./cmd` and `Magefile.go`, `go test -race ./pkg/... ./cmd/...`, the linux backend, migrator and compute-service builds, the migration CLI against the service Postgres, and frontend lint/typecheck/jest/webpack
