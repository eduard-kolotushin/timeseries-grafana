# Architecture

## Layout

Grafana app plugin (frontend in `src/`, backend in `pkg/`):

| Path | Responsibility |
| --- | --- |
| `src/plugin.json` | App metadata, nested panel include |
| `src/forecast-panel/` | Overlay visualization |
| `src/forecast-panel/trainRewrite.ts` | Type-keyed training-query rewrite (Prom / OpenSearch / Postgres / Druid) |
| `src/forecast-panel/extract.ts` | Time+numeric series from frames; train ↔ visible match |
| `src/components/AppConfig/` | Enable-app copy (no publisher settings) |
| `pkg/plugin/forecast.go` | Fit/forecast using sibling modules |
| `pkg/plugin/resources.go` | `POST /forecast`, `GET /ping` |

Grafana Compose, TestData, Kafka, and demo dashboards live in sibling `timeseries-grafana-sandbox`, which mounts `dist/`. Cluster install (Grafana image with this plugin baked in, plus the worker image) lives in sibling `timeseries-k8s`. The Druid→Kafka baseline ticker lives in sibling `timeseries-baselines`.

`gpx_forecast` never talks to Prometheus, OpenSearch, or Postgres. Training queries run through Grafana datasource plugins; the backend only sees `{times, values}`.

## Overlay data flow

1. Grafana queries the **visible** panel time range. The nested panel draws those frames as history (time + every numeric field per frame).
2. Independently, the panel issues a second datasource query for the training window (same targets, rewritten for that window and a model-aware step; `maxDataPoints` sized to the window). The window is a Grafana from/to range (`trainRange`). Auto (cleared picker) is `[timeRange.to − autoLookback, timeRange.to]`.
3. It POSTs the **training** points `{ times, values, model, from, to, season, calendar, level, ... }` to `/api/plugins/eduardkolotushin-forecast-app/resources/forecast`. `from`/`to` are unix ms for the forecast window. `level` is `0` when `showInterval` is off. One POST per extracted visible series.
4. The backend builds `timeseries.Series[float64]`, fits, and returns grid points in `[from, to]` (`ForecastRange`) plus optional `lower` / `upper` when `level` is in `(0, 1)`.
5. The panel draws visible history and forecast with `@grafana/ui` `TimeSeries`. Interval bounds use `custom.fillBelowTo` on the forecast frame. Extra training points are not plotted. The plot `to` includes the forecast window.
6. If the training query returns no points, or the forecast window has no grid points (or all NaN), the panel shows a reason. It does not silently fit the visible series.

## Training query adapters

Cloned panel targets are rewritten by `datasource.type` before `ds.query`. Window math stays in `lookback.ts`. The dispatcher lives in `trainRewrite.ts` and is used by `queryTrainingFrames`.

| `datasource.type` | Train rewrite |
| --- | --- |
| `prometheus` | Force range (`range: true`, `instant: false`, `exemplar: false`); set target `interval` to the train step. Re-query with new `range` / `intervalMs` / `maxDataPoints` / `__from`/`__to`. Do not globally replace numbers in `expr`. If `$__range` / `$__rate_interval` / `$__interval` are still macros, leave them for the Prometheus plugin. |
| `grafana-opensearch-datasource` | Skip non-metric query types (reason). Pin date_histogram `settings.interval` to the train step; `request.range` drives extended bounds. |
| `postgres`, `grafana-postgresql-datasource` | Keep `format` as time series. If `rawSql` still has `$__timeFilter` / `$__timeFrom` / `$__timeTo` / `$__unixEpochFilter`, leave them. If Grafana already expanded ISO/`BETWEEN`, replace those visible-range literals (same idea as Druid SQL), not arbitrary integers. |
| other (Druid, TestData) | Existing `applyLookbackRange`. TestData already honors `DataQueryRequest.range`. |

Set `request.intervalMs` to the train step before `ds.query`.

Valid train queries: Prometheus **range** PromQL (Mimir/AMP same type); OpenSearch **Lucene metric + date histogram** or **PPL time series**; Postgres **time series** SQL with Grafana time macros.

Invalid (reason, history only, no POST): Prometheus instant; OpenSearch logs/raw/traces; Postgres table/EXPLAIN; frames that are not time+number. Unsupported types are detected on the target **before** query when possible.

## Train step

Train step follows the model, not the dashboard interval:

| Model | Step |
| --- | --- |
| baseline minute-of-week | `1m` |
| baseline hour or hour-of-week | `1h` |
| baseline day | `1d` |
| otherwise | request `intervalMs`, floor 1m |

`maxDataPoints` for the training query is `min(100000, ceil((trainTo − trainFrom) / step) + 1)`.

## Extract and match

- Skip non-timeseries frames (logs/trace meta).
- Emit every numeric field (wide Postgres/Prom frames), not only the first.
- Series name is Grafana `getFieldDisplayName` (labels), not raw `"Value"`.
- Match train ↔ visible by that name. A single train series still maps to all visible series. When several train series exist and a name misses, skip that visible series; `REASON_TRAIN_EMPTY` only when **no** train points exist.

## Training window

`trainRange` is Grafana raw from/to (`now-7d`/`now`, or absolute ISO). Empty / Auto windows (legacy `lookback` duration still applies if `trainRange` was never saved):

| Model | Lookback |
| --- | --- |
| baseline minute-of-week or hour-of-week | 21d |
| baseline hour | 14d |
| baseline day | 56d |
| seasonal naive | 14d |
| naive, mean, drift, SES, Holt | 7d |

## Forecast window

`forecastRange` is Grafana raw from/to. Empty / Auto is `[dashboard now, now + autoForecastHorizon]`:

| Model | Duration |
| --- | --- |
| baseline minute-of-week or hour-of-week | 6h |
| baseline hour | 24h |
| baseline day | 7d |
| seasonal naive | 24h |
| naive, mean, drift, SES, Holt | 6h |

The backend emits `last + k×step` points inside that window (skip-ahead; no backcast before `last+step`).

## Horizon clock

Same as `timeseries-forecast`: grid `last + k * step` for `k ≥ 1`, clipped to the request `[from, to]`.

## Modules

`go.mod` requires tagged `github.com/eduard-kolotushin/timeseries` and `github.com/eduard-kolotushin/timeseries-forecast`. Do not add a `replace` directive.
