# Project intentions

## Goal

Grafana plugin that overlays univariate forecasts on dashboard queries. This plugin visualizes; it does not own Series or model math. The Docker Grafana runtime is a separate sibling. Minute-of-week baselines from Druid to Kafka live in sibling `timeseries-baselines`, not in this plugin process.

## Locked choices

| Decision | Choice |
| --- | --- |
| Repo | sibling `timeseries-grafana` |
| Plugin | Grafana app `eduardkolotushin-forecast-app` with Go backend |
| Panel | nested `eduardkolotushin-forecast-panel` |
| Models | public `timeseries-forecast` Fit functions (tagged module; no `replace`) |
| Sandbox | sibling `timeseries-grafana-sandbox` |
| Baseline publisher | sibling `timeseries-baselines` (standalone process, not Grafana-hosted) |

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
- Training lookback independent of the panel time range: a second datasource query for `[timeRange.to − lookback, timeRange.to]`
- Lookback **Auto** by model, or a typed duration (`15d`, `48h`, or a number of days); display still follows the panel query range
- Overlay does not plot the extra training points

## v1/v2/v3 non-goals

Do not add these without first updating this document:

- Docker Compose / TestData sandbox (those live in `timeseries-grafana-sandbox`)
- Publishing or signing on grafana.com
- Prometheus
- Alerting
- Extra app pages beyond the landing page and existing Configuration page
- Duplicating Series or forecast algorithms
- A Druid/Kafka ticker in this plugin (see `timeseries-baselines`)
- Consuming the metrics Kafka topic

## Quality bar

- Backend does not mutate caller series (libraries already return new series)
- Invalid model/horizon/series/level map to HTTP 400
- Table-driven tests cover golden paths for the resource
- GitHub Actions on `main` runs `gofmt`, `go test`, and frontend typecheck/jest/webpack
