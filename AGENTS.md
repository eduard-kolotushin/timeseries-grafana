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

## Read first

1. [docs/INTENTIONS.md](docs/INTENTIONS.md)
2. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)

## Hard constraints

- Do not reimplement Series or forecast models; use the public sibling APIs only
- Visualization plugin source stays in this repo; the Grafana runtime does not (Compose sandbox or `timeseries-k8s`)
- Nested panel calls `POST /api/plugins/eduardkolotushin-forecast-app/resources/forecast`
- Nested datasource `QueryData` Restores snapshots; alerting uses Grafana `refId`s (metric vs forecast / interval)
- Do not host a Druid/Kafka ticker here (see `timeseries-baselines`)
- No Prometheus, OpenSearch, or Postgres **datasource HTTP** in `pkg/` (`gpx_forecast` stays datasource-agnostic). pgx may store fitted snapshots and schedules; `POST /api/ds/query` on Grafana's own API is not a datasource client
- Stay within v1–v12 unless `docs/INTENTIONS.md` is updated first

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

Postgres snapshot store (`forecast.snapshots` via pgx). Skip the train query until Retrain or a query/model/train-range-string change. Existing Configuration page holds the DSN.

## v7 in scope

Mergeable `conf/forecast.ini.template` for CI/CD `grafana.ini` (`[plugin.eduardkolotushin-forecast-app]` and `[plugin.eduardkolotushin-forecast-datasource]`). Backend reads `FORECAST_STORE_*` (not forwarded into plugin processes on Grafana 12.4+ by default), then `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_*` / `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_DATASOURCE_*` / GrafanaCfg, then jsonData. Alerting QueryData uses Forecast datasource jsonData, not the app Configuration page.

## v8 in scope

Nested forecast datasource for Grafana alerting queries (`QueryData` Restore + `ForecastRange`). Overlay stays the train/retrain path. Do not ship alert rules.

## v9 in scope

Mixed metric + Forecast datasource on the overlay: ignore Forecast frames for fit/plot/cacheKey; no draw-mode option; Forecast query editor stays manual (optional Copy source from query A). Grafana Alert tab only on Time series / Graph when that viz opened the editor; use re-open, panel menu New alert rule, or the Alerting page. Do not ship alert rules.

## v10 in scope

Overlay options New alert rule: Grafana `/alerting/new` with live panel queries (including Mixed Forecast), dashboard/panel annotations, default reduce + threshold. Not Grafana’s data-pane Alert tab. Do not ship alert rules.

## v11 in scope

Bound `POST /forecast` / `QueryData` body and train length, and concurrent Fit / ForecastRange work, so high load returns 413/429 or a panel reason instead of crashing `gpx_forecast` or Grafana. Overlay: max in-flight loads per panel is a panel option (default 1), sequential series POSTs, no tight retry. No job queue or extra plugin replicas.

## v12 in scope

Backend retrain scheduler: `forecast.retrain` (`scope` `panel` / `baseline`, `PRIMARY KEY (scope, org_id, key)`), `FOR UPDATE SKIP LOCKED` claims, `/api/ds/query` frame fetch from the stored `trainSource`, `GET|PUT|DELETE /schedules` (Admin) and `/schedules/default`, schedule UI. `needTrain` also fires when a schedule is due. `trainSource` stays out of the `cacheKey` fingerprint, provenance included. `org_id` is part of the row key because a `cacheKey` is org-independent, so a panel row is this org's alone and the scheduler claims and retrains only the org its Grafana credential belongs to (its own row of another org stays due for that org's overlay); `ensureSQL` migrates an older two-column key in place and `ensureSchedules` refuses a table still keyed `(scope, key)`. The schedules table identifies a `panel` row by its dashboard/panel/series/query (`GET /schedules` derives a `source` object; the spec's query objects never leave the backend), and every overlay request carries `provenance` (probe included) so the backend merges that identity into an existing row — on the read paths, since a request carrying training points writes the whole spec anyway — and a row written before it existed heals on the next dashboard load. A `baseline` row is fleet-wide (`org_id = 0`), created by the worker (an API `PUT` of an unknown baseline key is `404`), identifiable only by its `metric_hash`, and deletable like a panel row. No per-datasource logic in `pkg/`; a scheduler failure never fails a query. The forecast.retrain row table is a second app config page (`?page=schedules`), a peer tab of Overview and Configuration.

## v1/v2/v3/v4/v5/v6/v7/v8/v9/v10/v11/v12 out of scope

Docker Compose sandbox (see `timeseries-grafana-sandbox`), Kubernetes Helm (see `timeseries-k8s`), Grafana.com signing/publish, Prom/OS/PG **datasource HTTP** in `pkg/`, Elasticsearch plugin type, shipping Grafana alert rules or contact points, extra app pages beyond the landing, Configuration, and Retrain schedules pages, baseline publisher process, a job queue.

## Workflow

- Table-driven Go tests for the forecast resource, datasource `QueryData`, and the schedule resource
- Table-driven frontend tests for train rewrite, extract/match, cache fingerprint, `trainSource` capture, mixed frames, forecast-query `cacheKey`, overlay New alert rule defaults, overlay load limits, and the schedule API/UI
- Depend on tagged `timeseries` and `timeseries-forecast` modules; do not add a `replace` directive
- `make build` writes frontend + Linux backend to `dist/`
- Run Grafana from `timeseries-grafana-sandbox` after building `dist/`
- Cluster images and Helm live in `timeseries-k8s` (build from a git pin of this repo)
- GitHub Actions on `main`: `gofmt`, `go test`, frontend typecheck/jest/webpack
