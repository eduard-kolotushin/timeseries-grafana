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

1. The nested panel reads Grafana frames (time + first numeric field) per series.
2. It POSTs `{ times, values, model, horizon, season, calendar, ... }` to `/api/plugins/eduardkolotushin-forecast-app/resources/forecast`.
3. The backend builds `timeseries.Series[float64]`, fits, and returns future unix-ms points.
4. The panel draws history and forecast with `@grafana/ui` `TimeSeries`.

## Horizon clock

Same as `timeseries-forecast`: last timestamp + `k * step` for `k = 1..h`.

## Local modules

```
replace github.com/eduard-kolotushin/timeseries => ../timeseries
replace github.com/eduard-kolotushin/timeseries-forecast => ../timeseries-forecast
```
