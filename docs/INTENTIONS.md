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
| Sandbox | sibling `timeseries-grafana-sandbox` (Compose) |
| Kubernetes | sibling `timeseries-k8s` (Helm + images) |
| Baseline publisher | sibling `timeseries-baselines` (standalone process, not Grafana-hosted) |
| Train adapters | frontend only (Prometheus, OpenSearch, Postgres, existing Druid/SQL rewrite). No Prom/OS/PG HTTP clients in `pkg/` |

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

- Mergeable Grafana `.ini` snippet (`conf/forecast.ini.template`) for proprietary CI/CD that already ships this plugin with Grafana: `[plugin.eduardkolotushin-forecast-app]` snapshot-store keys with `${FORECAST_STORE_*}` placeholders
- Backend DSN resolution: `FORECAST_STORE_*` env, then Grafana `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_*` / GrafanaCfg, then Configuration `jsonData` / `secureJsonData`. Empty host (and no URL): persist off
- Panel options stay on the dashboard. Do not put unsigned-plugin allowlist or datasource provisioning in this template

## v1/v2/v3/v4/v5/v6/v7 non-goals

Do not add these without first updating this document:

- Docker Compose / TestData sandbox (those live in `timeseries-grafana-sandbox`)
- Kubernetes Helm / container images (those live in `timeseries-k8s`)
- Publishing or signing on grafana.com
- Prometheus, OpenSearch, or Postgres **datasource HTTP** in `pkg/` (pgx snapshot store is v6)
- Elasticsearch plugin type
- Alerting
- Extra app pages beyond the landing page and existing Configuration page
- Duplicating Series or forecast algorithms
- A Druid/Kafka ticker in this plugin (see `timeseries-baselines`)
- Consuming the metrics Kafka topic

## Quality bar

- Backend does not mutate caller series (libraries already return new series)
- Invalid model/series/level/forecast range map to HTTP 400
- Table-driven tests cover golden paths for the resource
- Table-driven frontend tests cover train rewrite, extract/match, and cache fingerprint
- GitHub Actions on `main` runs `gofmt`, `go test`, and frontend typecheck/jest/webpack
