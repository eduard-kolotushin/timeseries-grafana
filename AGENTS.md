# AGENTS.md

Operating manual for agents working in this repository.

## Project

Grafana app plugin that overlays univariate forecasts on dashboard queries. The Go backend calls `timeseries-forecast`; the nested panel draws history plus forecast. Minute-of-week Druid→Kafka baselines are sibling `timeseries-baselines`, not this process.

- **Folder:** `timeseries-grafana`
- **Plugin ID:** `eduardkolotushin-forecast-app` (nested panel `eduardkolotushin-forecast-panel`, nested datasource `eduardkolotushin-forecast-datasource`)
- **Go module:** `github.com/eduard-kolotushin/timeseries-grafana`
- **Go:** 1.26+
- **Libraries:** tagged `timeseries` and `timeseries-forecast` modules (no `replace`)
- **Sandbox:** sibling `timeseries-grafana-sandbox`
- **Kubernetes:** sibling `timeseries-k8s`
- **Compute service:** `cmd/compute` → `gpx_forecast_compute`, the optional standalone fit/tick process (v16, `FORECAST_COMPUTE_URL`)

## Read first

1. [docs/INTENTIONS.md](docs/INTENTIONS.md)
2. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
3. [docs/FUNCTIONAL_OVERVIEW.md](docs/FUNCTIONAL_OVERVIEW.md) — what each screen and knob does, and how it was verified live

## Hard constraints

- Do not reimplement Series or forecast models; use the public sibling APIs only
- Visualization plugin source stays in this repo; the Grafana runtime does not (Compose sandbox or `timeseries-k8s`)
- Nested panel calls `POST /api/plugins/eduardkolotushin-forecast-app/resources/forecast`
- Nested datasource `QueryData` Restores snapshots; alerting uses Grafana `refId`s (metric vs forecast / interval)
- Do not host a Druid/Kafka ticker here (see `timeseries-baselines`)
- No Prometheus, OpenSearch, or Postgres **datasource HTTP** in `pkg/` (`gpx_forecast` stays datasource-agnostic). pgx may store fitted snapshots and schedules; `POST /api/ds/query` on Grafana's own API is not a datasource client
- Stay within v1–v16 unless `docs/INTENTIONS.md` is updated first
- The compute service (`cmd/compute`, `gpx_forecast_compute`) runs the same `pkg/plugin` `/forecast` handler and the same retrain ticker over plain HTTP: no second fit or dispatch implementation, no datasource client, and it reads process env only (never grafana.ini or jsonData)

## v1 in scope

App + Go backend, nested overlay panel.

## v2 in scope

Prediction interval bands on the overlay (`POST /forecast` `lower`/`upper`, panel `interval` option).

## v3 in scope

`showInterval` (default on). Training window via a second datasource query; Grafana from/to time picker, or Auto by model. Display still follows the panel time range.

## v4 in scope

Forecast from/to picker (Auto: Grafana `now` → `now` + model duration). Overlay is robust against that window: points or a reason. No silent train fallback.

## v5 in scope

Type-keyed train-query adapters (Prometheus range, OpenSearch metric+histogram or PPL time series, Postgres time-series SQL) plus labeled/wide-frame extract and name matching. Invalid query types get a reason and history only. Druid/TestData keep the existing rewrite.

## v6 in scope

Postgres snapshot store (`forecast.snapshots` via pgx). Skip the train query until Retrain or a query/model/train-range-string change. The DSN is deployment configuration — env / `GF_PLUGIN_*` / grafana.ini / provisioned jsonData — and the plugin's config pages render no store fields.

## v7 in scope

Mergeable `conf/forecast.ini.template` for CI/CD `grafana.ini` (`[plugin.eduardkolotushin-forecast-app]` and `[plugin.eduardkolotushin-forecast-datasource]`). Backend reads `FORECAST_STORE_*` (not forwarded into plugin processes on Grafana 12.4+ by default), then `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_*` / `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_DATASOURCE_*` / GrafanaCfg, then jsonData. Alerting QueryData uses Forecast datasource jsonData, not the app Configuration page.

## v8 in scope

Nested forecast datasource for Grafana alerting queries (`QueryData` Restore + `ForecastRange`). Overlay stays the train/retrain path. Do not ship alert rules.

## v9 in scope

Mixed metric + Forecast datasource on the overlay: ignore Forecast frames for fit/plot/cacheKey; no draw-mode option; Forecast query editor stays manual (optional Copy source from query A). Grafana Alert tab only on Time series / Graph when that viz opened the editor; use re-open, panel menu New alert rule, or the Alerting page. Do not ship alert rules.

## v10 in scope

Overlay options New alert rule: Grafana `/alerting/new` with live panel queries (including Mixed Forecast), dashboard/panel annotations, default reduce + threshold. Not Grafana’s data-pane Alert tab. Do not ship alert rules.

## v11 in scope

Bound `POST /forecast` / `QueryData` body and train length, and concurrent Fit / ForecastRange work, so high load returns 413/429 or a panel reason instead of crashing `gpx_forecast` or Grafana. Overlay: max in-flight loads per panel is a panel option (default 1), sequential series POSTs, no tight retry. No job queue; scaling fitting means more compute-service replicas (v16), never more Grafana-managed plugin processes.

## v12 in scope

Backend retrain scheduler: `forecast.retrain` (`scope` `panel` / `baseline`, keyed `(scope, org_id, key)`, a uuid `id` primary key from v13), `FOR UPDATE SKIP LOCKED` claims, `/api/ds/query` frame fetch from the stored `trainSource`, `GET|PUT|DELETE /schedules` (Admin) and `/schedules/default`, schedule UI. `needTrain` also fires when a schedule is due. `trainSource` stays out of the `cacheKey` fingerprint, provenance included. `org_id` is part of the row key because a `cacheKey` is org-independent, so a panel row is this org's alone and the scheduler claims and retrains only the org its Grafana credential belongs to (its own row of another org stays due for that org's overlay); the migrations migrate an older two-column key in place and `ensureSchedules` refuses a table still keyed `(scope, key)`. The schedules table identifies a `panel` row by its dashboard/panel/series/query (`GET /schedules` derives a `source` object; the spec's query objects never leave the backend), and every overlay request carries `provenance` (probe included) so the backend merges that identity into an existing row — on the read paths, since a request carrying training points writes the whole spec anyway — and a row written before it existed heals on the next dashboard load. A `baseline` row is fleet-wide (`org_id = 0`), created by the worker (an API `PUT` of an unknown baseline key is `404`), identifiable only by its `metric_hash`, and deletable like a panel row. No per-datasource logic in `pkg/`; a scheduler failure never fails a query. The forecast.retrain row table is a second app config page (`?page=schedules`), a peer tab of Overview and Configuration.

## v13 in scope

Versioned schema migrations, and a uuid primary key on every table this plugin owns. The schema is SQL files in `pkg/store/migrations` (`NNNN_name.sql`, embedded with `go:embed`) applied by the engine in `pkg/store`, whose ledger is `forecast.schema_migrations (id uuid PK, version TEXT UNIQUE, name, applied_at)`: one transaction per file, opened with `pg_advisory_xact_lock`, so a CI/CD migrator, every Grafana replica's lazy apply and two replicas serialise instead of racing, and a failing file leaves behind neither its DDL nor its ledger row. `cmd/migrate` (`gpx_forecast_migrate`, `make migrate`, `--dsn` / `--dry-run` / `--timeout`) is the pipeline entry point that applies them before Grafana starts, reading the same `FORECAST_STORE_*` / `GF_PLUGIN_*` env the plugin reads (no grafana.ini, no jsonData); the plugin applies the same set at its first store use, so a deployment that skips the CLI converges one request late and a migration failure keeps the existing per-table fallback. Every table carries `id uuid PRIMARY KEY DEFAULT gen_random_uuid()` with its natural key beside it as a `UNIQUE` constraint (`forecast.snapshots` `UNIQUE (org_id, cache_key)`, `forecast.retrain` `UNIQUE (scope, org_id, key)`), so every read, `ON CONFLICT` target and `FOR UPDATE SKIP LOCKED` claim is unchanged, no Go uuid generator is added, and a table an older release created is still adopted in place. Needs PostgreSQL 13+ (`gen_random_uuid()` is core from 13).

## v14 in scope

Snapshot retention and schedule reconciliation, swept in one transaction on the retrain ticker (`pkg/plugin/retention.go`, so `FORECAST_RETRAIN_ENABLED=false` turns it off too). A schedule row does not own a snapshot: deleting a row leaves the model and the next tick re-creates the row with the deployment default cron. A snapshot nothing refreshed within `FORECAST_SNAPSHOT_TTL` (default `72h`, `0` disables, a value below `1h` is refused and the default kept) is deleted, and so is a row idle for the whole window with no snapshot behind it (which is also the `baseline` rule; `baselines.snapshots` belongs to `timeseries-baselines`, which sweeps it under `SNAPSHOT_TTL`). `DELETE /schedules?scope=&key=&drop=row|model` removes the row only (default) or the model with every row for that key, superseded siblings included.

## v15 in scope

Retrain reliability on `forecast.retrain`: a failed or interrupted `panel` retrain is never lost, never retrained twice at once, and never retried in a storm. The claim is extended to `FORECAST_RETRAIN_LEASE` immediately before a row's work and the work is bounded by that lease, so a fit that would outlive its claim is dropped instead of duplicated; an extension matching zero rows means the claim was handed over, so that row is skipped without fitting or finishing it. A failed retrain increments a persisted `attempts` column (migration `0003_retrain_attempts.sql`) and is due again after `min(FORECAST_RETRAIN_LEASE × 2^(attempts-1), FORECAST_RETRAIN_RETRY_MAX)` — the new cap defaults to `1h` and a value not longer than the lease is refused and the default kept; a success resets the count and schedules the next cron slot, and `errBusy` burns no attempt. The row's attempt count rides `last_status` (`error: … (attempt N)`) — no new `GET /schedules` field, no dead-letter queue, no disable-after-N — and each tick logs claimed/retrained/failed counts. A `forecast.retrain` without `attempts` fails the schedule store loudly, naming `gpx_forecast_migrate`, rather than silently claiming nothing.

## v16 in scope

Fitting capacity scales on its own, selected by one deployment variable. `FORECAST_COMPUTE_URL` (env `FORECAST_*` → `GF_PLUGIN_*` → ini `compute_url` → jsonData `computeUrl`) unset keeps every fit and the retrain ticker in the Grafana-managed process; set, that process forwards `POST /forecast` verbatim to the standalone compute service and runs no ticker, so one implementation of the dispatch semantics serves both modes and the panel's resource URL, body and answer shape are untouched. The service is `cmd/compute` → `gpx_forecast_compute` (`make compute`, `FORECAST_COMPUTE_LISTEN` default `:8080`, `--listen` overrides), the same `pkg/plugin` handlers over plain HTTP, with the SDK's request identity replaced by `FORECAST_COMPUTE_TOKEN` (constant-time compared) and the `X-Forecast-Org` header the plugin sets from the org Grafana authenticated — overwriting any caller-supplied copy — which the service injects as `backend.PluginContext` so store keys stay org-scoped exactly as inline. It reads the process env only (like `gpx_forecast_migrate`), refuses to start without the token, ignores `FORECAST_COMPUTE_URL` (so a shared env block cannot make one compute service forward to another), and mounts only `/forecast`, `/ping` and an unauthenticated coarse `/healthz`. Replicas share one Postgres, so the v12 claim queue with the v15 lease, extension and owner-guarded finish makes N tickers safe. Training frames still come from Grafana's own `/api/ds/query` (`FORECAST_GRAFANA_URL` / `FORECAST_GRAFANA_TOKEN`, which a split deployment points at Grafana's service), so no datasource client enters `pkg/`. `QueryData`, `GET|PUT|DELETE /schedules`, `/schedules/default` and every store write stay in the plugin, so alerting and the schedules page keep working while the compute service is down; the forwarded path keeps the plugin's 413 caps, an unreachable service is a 502 naming the failure (never a silent local fit), `CheckHealth` also probes the service's `/healthz`, and the remote limiter default is `defaultMaxProxyInflight` (32) where inline is unchanged, with `FORECAST_MAX_INFLIGHT` overriding either.

## v1/v2/v3/v4/v5/v6/v7/v8/v9/v10/v11/v12/v13/v14/v15/v16 out of scope

Docker Compose sandbox (see `timeseries-grafana-sandbox`), Kubernetes Helm (see `timeseries-k8s`), Grafana.com signing/publish, Prom/OS/PG **datasource HTTP** in `pkg/`, Elasticsearch plugin type, shipping Grafana alert rules or contact points, extra app pages beyond the landing, Configuration, and Retrain schedules pages, baseline publisher process, a job queue, a second migration tool (Flyway, goose, golang-migrate) or a schema-diff ORM, an integer surrogate key on any table this plugin owns, retention outside the retrain ticker (a second scheduler, a Grafana-side cron, a separate collector process, or a table the worker owns). `gpx_forecast_compute` is the named exception to "a separate process": it is the same ticker and the same `/forecast` handler, so it is not a second scheduler, a second dispatch implementation, a second compute transport, or a service that reads grafana.ini / jsonData (process env only, like `gpx_forecast_migrate`). A panel that talks to the compute service directly, and a silent local fit when it is unreachable, are out of scope too.

## Workflow

- Table-driven Go tests for the forecast resource, datasource `QueryData`, and the schedule resource
- Table-driven Go tests for the compute boundary: mode resolution across every spelling, the token/org middleware (401/400 and the injected org), the verbatim forward (status, content type, body; 502 when unreachable; 413 before the upstream call), the `/forecast`+`/ping`+`/healthz` route subset, and that remote mode starts no ticker
- Table-driven frontend tests for train rewrite, extract/match, cache fingerprint, `trainSource` capture, mixed frames, forecast-query `cacheKey`, overlay New alert rule defaults, overlay load limits, and the schedule API/UI
- Depend on tagged `timeseries` and `timeseries-forecast` modules; do not add a `replace` directive
- `make build` writes frontend + Linux backend + migration CLI + compute service to `dist/`
- Run Grafana from `timeseries-grafana-sandbox` after building `dist/`
- Cluster images and Helm live in `timeseries-k8s` (build from a git pin of this repo)
- GitHub Actions on `main`: `gofmt` over `./pkg ./cmd` and `Magefile.go`, `go test -race ./pkg/... ./cmd/...`, the linux backend, migrator and compute-service builds, the migration CLI against the service Postgres, frontend lint/typecheck/jest/webpack
