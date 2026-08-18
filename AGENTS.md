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

## Read first

1. [docs/INTENTIONS.md](docs/INTENTIONS.md)
2. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)

## Hard constraints

- Do not reimplement Series or forecast models; use the public sibling APIs only
- Visualization plugin source stays in this repo; the Grafana runtime does not
- Nested panel calls `POST /api/plugins/eduardkolotushin-forecast-app/resources/forecast`
- Do not host a Druid/Kafka ticker here (see `timeseries-baselines`)
- Stay within v1/v2/v3 unless `docs/INTENTIONS.md` is updated first

## v1 in scope

App + Go backend, nested overlay panel.

## v2 in scope

Prediction interval bands on the overlay (`POST /forecast` `lower`/`upper`, panel `interval` option).

## v3 in scope

`showInterval` (default on). Training lookback via a second datasource query; Auto by model, or a typed duration. Display still follows the panel time range.

## v1/v2/v3 out of scope

Docker Compose sandbox (see `timeseries-grafana-sandbox`), Grafana.com signing/publish, Prometheus, alerting, extra app pages, baseline publisher process.

## Workflow

- Table-driven Go tests for the forecast resource
- Depend on tagged `timeseries` and `timeseries-forecast` modules; do not add a `replace` directive
- `make build` writes frontend + Linux backend to `dist/`
- Run Grafana from `timeseries-grafana-sandbox` after building `dist/`
- GitHub Actions on `main`: `gofmt`, `go test`, frontend typecheck/jest/webpack
