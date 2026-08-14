# AGENTS.md

Operating manual for agents working in this repository.

## Project

Grafana app plugin that overlays univariate forecasts on dashboard queries. The Go backend calls `timeseries-forecast`; the nested panel draws history plus forecast.

- **Folder:** `timeseries-grafana`
- **Plugin ID:** `eduardkolotushin-forecast-app` (nested panel `eduardkolotushin-forecast-panel`)
- **Go module:** `github.com/eduard-kolotushin/timeseries-grafana`
- **Go:** 1.26+
- **Local siblings:** `../timeseries`, `../timeseries-forecast` via `go.mod` replace
- **Sandbox:** sibling `timeseries-grafana-sandbox`

## Read first

1. [docs/INTENTIONS.md](docs/INTENTIONS.md)
2. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)

## Hard constraints

- Do not reimplement Series or forecast models; use the public sibling APIs only
- Visualization plugin source stays in this repo; the Grafana runtime does not
- Nested panel calls `POST /api/plugins/eduardkolotushin-forecast-app/resources/forecast`
- Stay within v1 unless INTENTIONS is updated first

## v1 in scope

App + Go backend, nested overlay panel.

## v1 out of scope

Docker Compose sandbox (see `timeseries-grafana-sandbox`), Grafana.com signing/publish, Prometheus, prediction intervals, alerting, extra app pages.

## Workflow

- Table-driven Go tests for the forecast resource
- `replace` directives for local sibling modules
- Run Grafana from `timeseries-grafana-sandbox` after building `dist/`
