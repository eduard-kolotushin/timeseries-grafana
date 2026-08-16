# Architecture

## Layout

Grafana app plugin (frontend in `src/`, backend in `pkg/`):

| Path | Responsibility |
| --- | --- |
| `src/plugin.json` | App metadata, nested panel include |
| `src/forecast-panel/` | Overlay visualization |
| `src/components/AppConfig/` | App jsonData for the baseline publisher |
| `pkg/plugin/forecast.go` | Fit/forecast using sibling modules |
| `pkg/plugin/resources.go` | `POST /forecast`, `GET /ping` |
| `pkg/plugin/publisher.go` | Druid scan + minute-of-week baseline + Kafka |

Grafana Compose, TestData, Kafka, and demo dashboards live in sibling `timeseries-grafana-sandbox`, which mounts `dist/`.

## Overlay data flow

1. The nested panel reads Grafana frames (time + first numeric field) per series.
2. It POSTs `{ times, values, model, horizon, season, calendar, ... }` to `/api/plugins/eduardkolotushin-forecast-app/resources/forecast`.
3. The backend builds `timeseries.Series[float64]`, fits, and returns future unix-ms points.
4. The panel draws history and forecast with `@grafana/ui` `TimeSeries`.

## Baseline publisher (v2)

Enabled from the existing Configuration page (`jsonData`). Grafana starts the plugin process at boot but does not create an app instance until a backend RPC. The process therefore calls Grafana's plugin `/health` API on localhost (`127.0.0.1:$GF_SERVER_HTTP_PORT`) so CheckHealth runs with jsonData and the ticker starts on any Grafana install. If that API requires login, the preloaded frontend (and Configuration Save) hit `/health` instead. CheckHealth applies the request jsonData so a settings change takes effect on the next health RPC.

Each tick:

1. Druid SQL `GROUP BY metric_hash` for `min(__time)`, `max(__time)`.
2. Skip hashes whose span is shorter than `lookback` (default 336h).
3. Load the last lookback window; skip unless inferred step is 1 minute.
4. `FitSeasonalBaseline(..., SeasonMinuteOfWeek, calendar)` then `Forecast(N)`.
5. Publish only the last point to the **baseline** Kafka topic:

```json
{"metric_hash":"...","metric_ts":<unix_ms>,"baseline_value":<float>}
```

`metric_ts` is last observed timestamp + N minutes. The metrics Kafka topic is not read; Druid is the source of truth. Duplicate `(hash, metric_ts)` pairs are skipped in memory (process restart may republish; consumers treat as upsert).

## Horizon clock

Same as `timeseries-forecast`: last timestamp + `k * step` for `k = 1..h`. The publisher uses `k = N`.

## Local modules

```
replace github.com/eduard-kolotushin/timeseries => ../timeseries
replace github.com/eduard-kolotushin/timeseries-forecast => ../timeseries-forecast
```
