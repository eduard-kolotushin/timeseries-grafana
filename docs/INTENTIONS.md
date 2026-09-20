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
- Configuration page (existing) and env `FORECAST_STORE_*` for the DSN. No DSN: persist off (always `needTrain`)
- Overlay Retrain control; status when a saved model is used

## v7 must-have

- Backend DSN resolution: `FORECAST_STORE_*` env (Grafana 12.4+ does not forward host env into plugin processes by default), then Grafana `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_*` / `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_DATASOURCE_*` / GrafanaCfg, then jsonData / `secureJsonData`. Empty host (and no URL): persist off
- Mergeable Grafana `.ini` snippet (`conf/forecast.ini.template`) for proprietary CI/CD that already ships this plugin with Grafana: `[plugin.eduardkolotushin-forecast-app]` and `[plugin.eduardkolotushin-forecast-datasource]` snapshot-store keys with `${FORECAST_STORE_*}` placeholders
- Panel options stay on the dashboard. Do not put unsigned-plugin allowlist or datasource provisioning in this template

## v8 must-have

- Nested Grafana datasource `eduardkolotushin-forecast-datasource` (`backend`, `metrics`, `alerting`) so unified alerting and expressions can use forecast / lower / upper by Grafana `refId`
- Query editor: output kind, model and train-range **strings** (fingerprint only), series name, copy of query A’s datasource uid and inner query; frontend writes the same `cacheKey` as the overlay (canonical SQL/expr, Grafana time macros equivalent to interpolated panel timestamps). No train rewrite and no live train query on this path
- `QueryData` `Restore`s the snapshot and `ForecastRange`s / `ForecastIntervalRange`s for the request time range. Miss (`needTrain`) is an error frame. Overlay remains the only train/retrain path
- Snapshot DSN for this process: Forecast datasource jsonData (same keys as the app Configuration page), `[plugin.eduardkolotushin-forecast-datasource]`, or parent `AppInstanceSettings` when Grafana sends them. Overlay train still uses the app process DSN
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

- **Backend (`POST /forecast` and Forecast `QueryData`)**: cap the request body and `len(times)` / `len(values)` at the existing `MAX_TRAIN_POINTS` (100k). Reject oversize with 400/413. Limit concurrent Fit / ForecastRange work (default a small fixed inflight cap, env-overridable). When the cap is full, return 429; do not queue unbounded goroutines. Honor request context cancel. Recover panics in resource and `QueryData` handlers so one bad request cannot kill the plugin process
- **Overlay frontend**: do not POST a train body longer than that cap. Do not fan-out one POST per series in parallel. Max in-flight overlay loads per panel instance is a panel option (default 1, minimum 1). When the cap is full, drop or cancel a stale refresh. On 413/429/5xx show a reason; no tight retry loop
- **Grafana process**: training queries stay in Grafana datasource plugins and stay clamped by `maxDataPoints` ≤ 100k. This plugin does not add a second train query per series. Snapshot Restore stays the cheap path; load limits apply there too so an alert-eval burst cannot grow without bound
- Do not add a job queue, extra `gpx_forecast` replicas, Grafana core changes, SIMD, or a parallel public API

## v12 must-have

Retrain stored snapshots on a cron with **no browser open**, and expose the schedule so an operator can see and edit it. The backend fetches its own training data through Grafana's own query API.

- **Backend retrain scheduler** in the app process: one ticker (`FORECAST_RETRAIN_TICK`, default 30s) that claims due rows from Postgres and retrains them. Started only when a store, a claimable row, and the Grafana query API are all available; `Dispose` cancels it
- **`forecast.retrain` table** (schema `forecast`, created by `ensureSQL` in `gpx_forecast` — the plugin is its only DDL owner). Columns: `scope`, `key`, `org_id`, `cron`, `timezone`, `enabled`, `spec JSONB`, `next_run_at`, `last_run_at`, `last_status`, `claimed_by`, `claimed_until`, `updated_at`; `PRIMARY KEY (scope, key)`. `scope` is `panel` (overlay `cacheKey`) or `baseline` (`metric_hash`, written by sibling `timeseries-baselines`, which never creates the table)
- **Claim queue**: `Claim` is `UPDATE … FROM (SELECT … FOR UPDATE SKIP LOCKED)`, so Grafana HA (one plugin process per replica) retrains each due row exactly once without a leader. Lease `FORECAST_RETRAIN_LEASE` (default 5m) releases a row whose worker died. The plugin claims only `scope='panel'` rows that have a `spec`; the worker claims only `scope='baseline'`. Neither can claim a row it cannot retrain
- **The plugin fetches its own training data** with `POST <Grafana>/api/ds/query` and the query objects the overlay stored verbatim, so no datasource-specific field is ever interpreted in `pkg/`. This introduces **no per-datasource logic and no datasource HTTP client**: the request body is opaque JSON that came from Grafana's own frontend, and the response is decoded with the SDK's `data.Frame` JSON unmarshaller. The type-keyed train rewrite stays in the overlay frontend
- **`trainSource` in the fit request**: `POST /forecast` gains an optional `trainSource {datasourceUid, queries, from, to, seriesName}` carrying exactly what the browser sent to the datasource. A successful fit upserts `forecast.retrain` for that `cacheKey` (never resetting an existing row's cron). `trainSource` is **not** part of the `cacheKey` fingerprint
- **Resource routes**: `GET|PUT /schedules` and `DELETE /schedules?scope=&key=` on the app resource mux, Admin-gated through `backend.PluginConfigFromContext(ctx).User.Role` (`403` otherwise). `DELETE` on a `scope='baseline'` row is refused (`400`): the worker re-inserts it
- **Schedule UI**: a table of rows (scope, key, cron, timezone, next/last run, status) with cron + timezone editing, an enable toggle, and delete for panel rows, on the **Retrain schedules** app page (a tab beside Overview and Configuration), plus a default retrain schedule (`jsonData.retrainCron` / `retrainTimezone`) on the Configuration page
- **`needTrain` also fires when the schedule is due** (`next_run_at <= now()`), so a row the scheduler cannot retrain — no stored spec, scheduler disabled — is still refreshed by the next overlay load. `POST /forecast` is unchanged for every other caller
- Config: `FORECAST_RETRAIN_ENABLED`, `FORECAST_RETRAIN_TICK`, `FORECAST_RETRAIN_LEASE`, `FORECAST_RETRAIN_CRON`, `FORECAST_GRAFANA_URL`, `FORECAST_GRAFANA_TOKEN`, following the existing `FORECAST_*` → `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_*` / ini / jsonData / `secureJsonData` precedence
- A scheduler failure never fails a query: if the scheduler is disabled or `/api/ds/query` is unreachable, `needTrain` on the next overlay load remains the retrain path
- **Retrain schedules app page**: a second app config page (id `schedules`, title `Retrain schedules`) shows the `forecast.retrain` row table as a peer tab of Overview / Configuration at `/plugins/%PLUGIN_ID%?page=schedules`. The Default retrain schedule (`jsonData.retrainCron` / `retrainTimezone`) stays on the Configuration page. Grafana gates every app config page on `plugins:write`; the schedule resource API keeps its own Admin gate.

## v1/v2/v3/v4/v5/v6/v7/v8/v9/v10/v11/v12 non-goals

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

## Quality bar

- Backend does not mutate caller series (libraries already return new series)
- Invalid model/series/level/forecast range map to HTTP 400
- Table-driven tests cover golden paths for the resource and datasource `QueryData`, plus oversize / busy (413/429) load limits
- Table-driven tests cover the schedule resource (method/status matrix, Admin gate, upsert→list round-trip, invalid cron, baseline-delete refusal) and `needTrain`-when-due
- Table-driven frontend tests cover train rewrite, extract/match, cache fingerprint, mixed metric vs Forecast frames, forecast-query `cacheKey`, overlay New alert rule defaults, overlay load limits, `trainSource` capture, the schedule API, and the schedule UI
- `trainSource` must not enter the `cacheKey` fingerprint: adding or editing a scheduled retrain must not invalidate a stored snapshot
- The retrain scheduler must never fail a query: an unreachable `/api/ds/query`, a disabled scheduler, or a claim error leaves `needTrain` as the retrain path
- GitHub Actions on `main` runs `gofmt`, `go test`, and frontend typecheck/jest/webpack
