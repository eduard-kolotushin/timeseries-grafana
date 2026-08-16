# Project intentions

## Goal

Grafana plugin that overlays univariate forecasts on dashboard queries, and (v2) publishes minute-of-week seasonal baselines from a Druid table to Kafka. This plugin does not own Series or model math. The Docker Grafana runtime is a separate sibling.

## Locked choices

| Decision | Choice |
| --- | --- |
| Repo | sibling `timeseries-grafana` |
| Plugin | Grafana app `eduardkolotushin-forecast-app` with Go backend |
| Panel | nested `eduardkolotushin-forecast-panel` |
| Models | public `timeseries-forecast` Fit functions |
| Sandbox | sibling `timeseries-grafana-sandbox` |
| Baseline source | Druid SQL (not the metrics Kafka topic) |
| Baseline model | `FitSeasonalBaseline` minute-of-week |
| Baseline output | one Kafka message per ready metric per tick, at last timestamp + N minutes |

## v1 must-have

- `POST /forecast` resource using `timeseries.New` + `forecast.Fit*` + `Forecast(h)`
- Nested panel overlaying history and forecast
- Options: model, horizon, alpha, beta, period, season, calendar

## v2 must-have

- App Configuration jsonData: enabled, Druid broker/datasource, Kafka brokers/baseline topic, lookback, aheadMinutes N, interval, calendar
- Backend ticker: distinct `metric_hash` from Druid; skip unless `max(__time)-min(__time) >= lookback`
- Fit last lookback window with minute-of-week seasonal baseline; skip non-1-minute series
- Publish `{"metric_hash","metric_ts","baseline_value"}` to the baseline Kafka topic (`metric_ts` Unix ms = last + N minutes)
- Default off until Druid and Kafka are configured

## v1/v2 non-goals

Do not add these without first updating this document:

- Docker Compose / TestData sandbox (those live in `timeseries-grafana-sandbox`)
- Publishing or signing on grafana.com
- Prometheus
- Prediction intervals
- Alerting
- Extra app pages beyond the landing page and existing Configuration page
- Duplicating Series or forecast algorithms
- Consuming the metrics Kafka topic (Druid is the source of truth)

## Quality bar

- Backend does not mutate caller series (libraries already return new series)
- Invalid model/horizon/series map to HTTP 400
- Table-driven tests cover golden paths for the resource and the publisher
- Publisher: one O(n) fit per hash per tick; O(1) per horizon step; pre-size series slices
