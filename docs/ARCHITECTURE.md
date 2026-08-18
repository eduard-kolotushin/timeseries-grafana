# Architecture

## Layout

Grafana app plugin (frontend in `src/`, backend in `pkg/`):

| Path | Responsibility |
| --- | --- |
| `src/plugin.json` | App metadata, nested panel include |
| `src/forecast-panel/` | Overlay visualization |
| `src/components/AppConfig/` | Enable-app copy (no publisher settings) |
| `pkg/plugin/forecast.go` | Fit/forecast using sibling modules |
| `pkg/plugin/resources.go` | `POST /forecast`, `GET /ping` |

Grafana Compose, TestData, Kafka, and demo dashboards live in sibling `timeseries-grafana-sandbox`, which mounts `dist/`. The Druid→Kafka baseline ticker lives in sibling `timeseries-baselines`.

## Overlay data flow

1. Grafana queries the **visible** panel time range. The nested panel draws those frames as history (time + first numeric field per series).
2. Independently, the panel issues a second datasource query for `[timeRange.to − lookback, timeRange.to]` (same targets, with Druid/SQL time bounds rewritten to that window; `maxDataPoints` sized to the lookback). Lookback is Auto by model, or a typed duration (`15d`, `48h`, or a number of days).
3. It POSTs the **training** points `{ times, values, model, horizon, season, calendar, level, ... }` to `/api/plugins/eduardkolotushin-forecast-app/resources/forecast`. `level` is `0` when `showInterval` is off.
4. The backend builds `timeseries.Series[float64]`, fits, and returns future unix-ms points plus optional `lower` / `upper` when `level` is in `(0, 1)`.
5. The panel draws visible history and forecast with `@grafana/ui` `TimeSeries`. Interval bounds use `custom.fillBelowTo` on the forecast frame. Extra lookback points are not plotted.
6. If the lookback query cannot run or fails, the panel falls back to the visible series.

## Training lookback

Auto windows (override with panel `lookback`, e.g. `15d`):

| Model | Lookback |
| --- | --- |
| baseline minute-of-week or hour-of-week | 21d |
| baseline hour | 14d |
| baseline day | 56d |
| seasonal naive | 14d |
| naive, mean, drift, SES, Holt | 7d |

`maxDataPoints` for the training query is `min(100000, ceil(lookback / interval) + 1)`.

## Horizon clock

Same as `timeseries-forecast`: last timestamp + `k * step` for `k = 1..h`.

## Modules

`go.mod` requires tagged `github.com/eduard-kolotushin/timeseries` and `github.com/eduard-kolotushin/timeseries-forecast`. Do not add a `replace` directive.
