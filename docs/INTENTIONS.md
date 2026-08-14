# Project intentions

## Goal

Grafana plugin that overlays univariate forecasts on dashboard queries. This plugin visualizes; it does not own Series or model math. The Docker Grafana runtime is a separate sibling.

## Locked choices

| Decision | Choice |
| --- | --- |
| Repo | sibling `timeseries-grafana` |
| Plugin | Grafana app `eduardkolotushin-forecast-app` with Go backend |
| Panel | nested `eduardkolotushin-forecast-panel` |
| Models | public `timeseries-forecast` Fit functions |
| Sandbox | sibling `timeseries-grafana-sandbox` |

## v1 must-have

- `POST /forecast` resource using `timeseries.New` + `forecast.Fit*` + `Forecast(h)`
- Nested panel overlaying history and forecast
- Options: model, horizon, alpha, beta, period, season, calendar

## v1 non-goals

Do not add these without first updating this document:

- Docker Compose / TestData sandbox (those live in `timeseries-grafana-sandbox`)
- Publishing or signing on grafana.com
- Prometheus
- Prediction intervals
- Alerting
- Extra app pages beyond a short landing/config page
- Duplicating Series or forecast algorithms

## Quality bar

- Backend does not mutate caller series (libraries already return new series)
- Invalid model/horizon/series map to HTTP 400
- Table-driven tests cover golden paths for the resource
