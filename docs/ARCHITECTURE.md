# Architecture

## Layout

Grafana app plugin (frontend in `src/`, backend in `pkg/`):

| Path | Responsibility |
| --- | --- |
| `src/plugin.json` | App metadata, nested panel and datasource includes |
| `src/forecast-panel/` | Overlay visualization |
| `src/forecast-panel/trainRewrite.ts` | Type-keyed training-query rewrite (Prom / OpenSearch / Postgres / Druid) |
| `src/forecast-panel/extract.ts` | Time+numeric series from frames; train ↔ visible match |
| `src/forecast-panel/cacheKey.ts` | Train-cache fingerprint (SHA-256); overlay and forecast datasource share this. SQL/expr identity; time macros match interpolated panel timestamps |
| `src/forecast-panel/mixed.ts` | Metric vs Forecast datasource targets/frames on Mixed overlay |
| `src/forecast-panel/alertFromPanel.ts` | Overlay options New alert rule: Grafana `/alerting/new` defaults from live panel queries |
| `src/forecast-datasource/` | Nested queryable datasource for alerting (`kind` forecast / lower / upper) |
| `src/components/AppConfig/` | Overlay Postgres DSN for the snapshot store |
| `conf/forecast.ini.template` | CI/CD merge snippet for `grafana.ini` (`[plugin.eduardkolotushin-forecast-app]` and `[plugin.eduardkolotushin-forecast-datasource]`) |
| `pkg/plugin/forecast.go` | Fit/forecast using sibling modules |
| `pkg/plugin/store.go` | SnapshotStore; pgx `forecast.snapshots` |
| `pkg/plugin/resources.go` | `POST /forecast`, `GET /ping` |
| `pkg/plugin/datasource.go` | `QueryData`: Restore snapshot, one frame per query `refId` |
| `pkg/main.go` | `app.Manage` or `datasource.Manage` from the executable path / `GF_PLUGIN_ID` |

Grafana Compose, TestData, Kafka, and demo dashboards live in sibling `timeseries-grafana-sandbox`, which mounts `dist/`. Cluster install (Grafana image with this plugin baked in, plus the worker image) lives in sibling `timeseries-k8s`. The Druid→Kafka baseline ticker lives in sibling `timeseries-baselines`.

`gpx_forecast` never talks to Prometheus, OpenSearch, or the Grafana Postgres **datasource**. Training queries run through Grafana datasource plugins; the backend sees `{times, values}` on a miss. Fitted snapshots are stored with **pgx** in schema `forecast` on a configured Postgres (sandbox overlay-postgres, not Druid metadata). Grafana starts a second process of the same binary for the nested datasource (binary copied under `forecast-datasource/`); that process only `Restore`s. Grafana 12.4+ does not forward host `FORECAST_STORE_*` into plugin processes, and datasource `QueryData` does not receive app Configuration jsonData, so the Forecast datasource instance (or `[plugin.eduardkolotushin-forecast-datasource]`) must carry the DSN.

## Overlay data flow

1. Grafana queries the **visible** panel time range. The nested panel draws **metric** frames as history (time + every numeric field per frame). Mixed Forecast datasource frames are not history, are not fitted, and are not plotted.
2. Per visible metric series it POSTs `{ cacheKey, from, to, model, ... }` without training points. `cacheKey` is SHA-256 of **metric** datasource uid, canonical query (SQL / PromQL `expr` / redacted native body), model options, raw train-range strings, and series name. Forecast datasource queries are omitted from the fingerprint so adding query B does not invalidate the overlay snapshot. Grafana time macros and interpolated visible timestamps are `__TIME__`. Volatile query-row fields (`key`, interval, maxDataPoints) are omitted. Dashboard range and forecast window are not in the key.
3. On a hit the backend `Restore`s the snapshot and `ForecastRange`s. The panel skips the training datasource query.
4. On `needTrain` or Retrain, the panel issues one training query (rewritten for the train window and a model-aware step), then POSTs `{ times, values, cacheKey, ... }`. The backend fits, upserts JSONB, and returns the window.
5. Auto/relative train strings do not re-query until Retrain; the saved model can lag `now`.
6. The panel draws visible history and forecast with `@grafana/ui` `TimeSeries`. Interval bounds use `custom.fillBelowTo` on the forecast frame. Extra training points are not plotted. The plot `to` includes the forecast window.
7. If the training query returns no points, or the forecast window has no grid points (or all NaN), the panel shows a reason. It does not silently fit the visible series.

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

## Snapshot store

DSN resolution (first non-empty wins per field; URL short-circuits the rest):

1. Process env `FORECAST_STORE_URL` / `FORECAST_STORE_*` (only if Grafana forwards host env; Grafana 12.4+ does not by default)
2. Grafana ini-to-env `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_*` or `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_DATASOURCE_*`, and `GrafanaCfg` keys (`store_host`, `store_port`, …) from `[plugin.eduardkolotushin-forecast-app]` or `[plugin.eduardkolotushin-forecast-datasource]`
3. jsonData / secureJsonData (`storeHost`, `storePort`, `storeDatabase`, `storeUser`, `storeSslMode`, `storePassword`): app Configuration page for the overlay process; Forecast datasource jsonData for alerting `QueryData`. If datasource jsonData has no host/URL, `QueryData` also tries parent `AppInstanceSettings` when Grafana sends them

CI/CD merges [`conf/forecast.ini.template`](../conf/forecast.ini.template) into `grafana.ini` (Grafana expands `${FORECAST_STORE_*}`). On connect: `CREATE SCHEMA IF NOT EXISTS forecast` and table `forecast.snapshots (org_id, cache_key, snapshot JSONB, updated_at)` PK `(org_id, cache_key)`. `org_id` comes from plugin context. No DSN: persist off.

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

## Forecast datasource (alerting)

Grafana unified alerting evaluates backend datasource queries, not overlay panel JavaScript. Typical alert:

1. Query A: live metric (Prometheus / OS / PG / Druid).
2. Query B: this forecast datasource, `kind: forecast`, same `cacheKey` fingerprint as the overlay (datasource uid, canonical SQL/expr, model options, **raw** train-range strings, series name). Overlay Auto is empty from/to strings, not `now-21d`.
3. Query C (optional): `kind: upper` or `lower` at coverage `level` (default 0.95).
4. Expression: A vs B, or A vs C.

`QueryData` looks up the snapshot and emits one time series frame. On miss it returns an error (`needTrain`); it does not fit and does not query other datasources. Train on the overlay first. If the store DSN is missing in this process, the error is `snapshot store not configured (needTrain)` — that is not a cache miss.

Mixed overlay + Forecast query: the overlay plots POST forecast only. The Forecast query editor does not auto-fill; **Copy source from query A** is opt-in for the fingerprint. Grafana’s panel Alert tab exists only for Time series / Graph. Overlay options **New alert rule** navigates to `/alerting/new` with live queries (including Mixed Forecast rows), default reduce + threshold, and dashboard/panel annotations. On Grafana 13 scenes, live targets come from the editor scene query runner (`__grafanaSceneContext`), not `getDashboardSaveModel` (that helper still calls `getSaveModelCloneOld`). Dashboard must be saved. Panel menu More → New alert rule and Alerting → New alert rule remain. Do not ship alert rules.

## Horizon clock

Same as `timeseries-forecast`: grid `last + k * step` for `k ≥ 1`, clipped to the request `[from, to]`.

## Modules

`go.mod` requires tagged `github.com/eduard-kolotushin/timeseries` and `github.com/eduard-kolotushin/timeseries-forecast`. Do not add a `replace` directive.
