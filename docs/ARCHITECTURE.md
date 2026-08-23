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

Grafana Compose, TestData, Kafka, and demo dashboards live in sibling `timeseries-grafana-sandbox`, which mounts `dist/`. Cluster install (Grafana image with this plugin baked in, plus the worker image) lives in sibling `timeseries-k8s`. The Druid→Kafka baseline ticker lives in sibling `timeseries-baselines`.

## Overlay data flow

1. Grafana queries the **visible** panel time range. The nested panel draws those frames as history (time + first numeric field per series).
2. Independently, the panel issues a second datasource query for the training window (same targets, with Druid/SQL time bounds rewritten to that window; `maxDataPoints` sized to the window). The window is a Grafana from/to range (`trainRange`). Auto (cleared picker) is `[timeRange.to − autoLookback, timeRange.to]`.
3. It POSTs the **training** points `{ times, values, model, from, to, season, calendar, level, ... }` to `/api/plugins/eduardkolotushin-forecast-app/resources/forecast`. `from`/`to` are unix ms for the forecast window. `level` is `0` when `showInterval` is off.
4. The backend builds `timeseries.Series[float64]`, fits, and returns grid points in `[from, to]` (`ForecastRange`) plus optional `lower` / `upper` when `level` is in `(0, 1)`.
5. The panel draws visible history and forecast with `@grafana/ui` `TimeSeries`. Interval bounds use `custom.fillBelowTo` on the forecast frame. Extra training points are not plotted. The plot `to` includes the forecast window.
6. If the training query returns no points, or the forecast window has no grid points (or all NaN), the panel shows a reason. It does not silently fit the visible series.

## Training window

`trainRange` is Grafana raw from/to (`now-7d`/`now`, or absolute ISO). Empty / Auto windows (legacy `lookback` duration still applies if `trainRange` was never saved):

| Model | Lookback |
| --- | --- |
| baseline minute-of-week or hour-of-week | 21d |
| baseline hour | 14d |
| baseline day | 56d |
| seasonal naive | 14d |
| naive, mean, drift, SES, Holt | 7d |

`maxDataPoints` for the training query is `min(100000, ceil((trainTo − trainFrom) / interval) + 1)`.

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
