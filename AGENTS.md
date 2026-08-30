# AGENTS.md

Operating manual for agents working in this repository.

## Project

Grafana app plugin that overlays univariate forecasts on dashboard queries. The Go backend calls `timeseries-forecast`; the nested panel draws history plus forecast. Minute-of-week Druid→Kafka baselines are sibling `timeseries-baselines`, not this process.

- **Folder:** `timeseries-grafana`
- **Plugin ID:** `eduardkolotushin-forecast-app` (nested panel `eduardkolotushin-forecast-panel`)
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
- Do not host a Druid/Kafka ticker here (see `timeseries-baselines`)
- No Prometheus, OpenSearch, or Postgres **datasource HTTP** in `pkg/` (`gpx_forecast` stays datasource-agnostic). pgx may store fitted snapshots
- Stay within v1–v7 unless `docs/INTENTIONS.md` is updated first

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

Mergeable `conf/forecast.ini.template` for CI/CD `grafana.ini`. Backend reads `FORECAST_STORE_*`, then `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_*` / GrafanaCfg, then jsonData.

## v1/v2/v3/v4/v5/v6/v7 out of scope

Docker Compose sandbox (see `timeseries-grafana-sandbox`), Kubernetes Helm (see `timeseries-k8s`), Grafana.com signing/publish, Prom/OS/PG **datasource HTTP** in `pkg/`, Elasticsearch plugin type, alerting, extra app pages, baseline publisher process.

## Workflow

- Table-driven Go tests for the forecast resource
- Table-driven frontend tests for train rewrite, extract/match, and cache fingerprint
- Depend on tagged `timeseries` and `timeseries-forecast` modules; do not add a `replace` directive
- `make build` writes frontend + Linux backend to `dist/`
- Run Grafana from `timeseries-grafana-sandbox` after building `dist/`
- Cluster images and Helm live in `timeseries-k8s` (build from a git pin of this repo)
- GitHub Actions on `main`: `gofmt`, `go test`, frontend typecheck/jest/webpack
