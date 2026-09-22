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
| `src/forecast-panel/sha256.ts` | WebCrypto digest with a pure-JS fallback; `crypto.subtle` is missing on plain-HTTP (non-localhost) Grafana |
| `src/forecast-panel/abortable.ts` | Observable → Promise that unsubscribes on `AbortSignal`, so an aborted overlay load cancels the HTTP request (`postResource`, train query) |
| `src/forecast-panel/mixed.ts` | Metric vs Forecast datasource targets/frames on Mixed overlay |
| `src/forecast-panel/alertFromPanel.ts` | Overlay options New alert rule: Grafana `/alerting/new` defaults from live panel queries |
| `src/forecast-datasource/` | Nested queryable datasource for alerting (`kind` forecast / lower / upper) |
| `src/components/AppConfig/` | Configuration tab: snapshot-store DSN + default retrain schedule |
| `src/pages/` | App config page bodies; the `schedules` tab renders the `forecast.retrain` table |
| `conf/forecast.ini.template` | CI/CD merge snippet for `grafana.ini` (`[plugin.eduardkolotushin-forecast-app]` and `[plugin.eduardkolotushin-forecast-datasource]`) |
| `pkg/plugin/forecast.go` | Fit/forecast using sibling modules; fit path records `trainSource` and upserts the `panel` schedule row |
| `pkg/plugin/limits.go` | Train-length / body caps, the pre-decode per-element budget, the one-window `maxForecastPoints` cap and the Fit / ForecastRange inflight semaphore |
| `pkg/plugin/store.go` | SnapshotStore interface; bounded TTL read-through cache over pgx `forecast.snapshots` |
| `pkg/plugin/schedule.go` | `ScheduleStore` over `forecast.retrain` (list / read one row / upsert / delete / due / claim / finish / identify) |
| `pkg/plugin/retrain.go` | Unattended retrain scheduler: ticker, claim, `/api/ds/query` frame fetch, fit, `Put`, `Finish` |
| `pkg/plugin/schedules.go` | `/schedules` resource handlers and the Admin gate |
| `pkg/plugin/resources.go` | `POST /forecast`, `GET /ping`, `/schedules` |
| `pkg/plugin/datasource.go` | `QueryData`: Restore snapshot, one frame per query `refId` |
| `pkg/main.go` | `app.Manage` or `datasource.Manage` from the executable path / `GF_PLUGIN_ID` |

Grafana Compose, TestData, Kafka, and demo dashboards live in sibling `timeseries-grafana-sandbox`, which mounts `dist/`. Cluster install (Grafana image with this plugin baked in, plus the worker image) lives in sibling `timeseries-k8s`. The Druid→Kafka baseline ticker lives in sibling `timeseries-baselines`.

`gpx_forecast` never talks to Prometheus, OpenSearch, or the Grafana Postgres **datasource**. Training queries run through Grafana datasource plugins; the backend sees `{times, values}` on a miss. Fitted snapshots are stored with **pgx** in schema `forecast` on a configured Postgres (sandbox overlay-postgres, not Druid metadata). Grafana starts a second process of the same binary for the nested datasource (binary copied under `forecast-datasource/`); that process only `Restore`s. Grafana 12.4+ does not forward host `FORECAST_STORE_*` into plugin processes, and datasource `QueryData` does not receive app Configuration jsonData, so the Forecast datasource instance (or `[plugin.eduardkolotushin-forecast-datasource]`) must carry the DSN.

## Overlay data flow

1. Grafana queries the **visible** panel time range. The nested panel draws **metric** frames as history (time + every numeric field per frame). Mixed Forecast datasource frames are not history, are not fitted, and are not plotted.
2. Per visible metric series it POSTs `{ cacheKey, from, to, model, ... }` without training points. `cacheKey` is SHA-256 of **metric** datasource uid, canonical query (SQL / PromQL `expr` / redacted native body), model options, raw train-range strings, and series name. Forecast datasource queries are omitted from the fingerprint so adding query B does not invalidate the overlay snapshot. Grafana time macros and interpolated visible timestamps are `__TIME__`. Volatile query-row fields (`key`, interval, maxDataPoints) are omitted. Dashboard range and forecast window are not in the key.
3. On a hit the backend `Restore`s the snapshot and `ForecastRange`s. The panel skips the training datasource query.
4. On `needTrain` or Retrain, the panel issues one training query (rewritten for the train window and a model-aware step), then POSTs `{ times, values, cacheKey, trainSource, ... }`. The backend fits, upserts JSONB, upserts the `panel` row in `forecast.retrain`, and returns the window. `trainSource` is `{datasourceUid, queries, from, to, seriesName, relative, lookbackMs, panelId, panelTitle, dashboardUid, querySummary}` where `queries` are the exact objects the panel sent to the datasource after the type-keyed train rewrite (that rewrite sets its own step: `intervalMs` / `maxDataPoints`, or the Prometheus `interval` string), so the scheduler can replay the request later without any datasource knowledge in `pkg/`. `relative`/`lookbackMs` say whether that window was a lookback the scheduler must re-resolve at claim time (see **Training window resolution**). `trainSource` is **not** in the fingerprint, so adding or editing a schedule never invalidates a stored snapshot.
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

CI/CD merges [`conf/forecast.ini.template`](../conf/forecast.ini.template) into `grafana.ini` (Grafana expands `${FORECAST_STORE_*}`). `org_id` comes from plugin context. No DSN: persist off.

Connection lifecycle: `openPostgresStore` only parses the DSN (pgxpool connects lazily). The first `Get`/`Put` pings and runs `CREATE SCHEMA IF NOT EXISTS forecast` plus table `forecast.snapshots (org_id, cache_key, snapshot JSONB, updated_at)` PK `(org_id, cache_key)`; if that DDL fails but the table is already readable (locked-down runtime user), the store is still ready. Readiness and retry state are kept **per table** (`forecast.snapshots`, `forecast.retrain`): `ensureSQL` provisions both, so a deployment that cannot `CREATE` can be missing the schedule table while snapshots are fine, and that schedule error must never change what a snapshot `Get` answers. While Postgres is unreachable each call fails and a redial is attempted at most every 5 s per table, so a database that is down at plugin start does not disable the store for the life of the process.

Per-process cache: each `gpx_forecast` process (overlay app, alerting datasource, one per Grafana replica) keeps a read-through cache of at most 256 snapshots with a 30 s TTL. `Put` writes through and refreshes the local entry; after the TTL a `Get` re-reads Postgres, so a Retrain on the overlay is visible to alert evaluation within 30 s without a restart, and resident memory stays bounded (a minute-of-week baseline is ~20k floats as JSON).

## Retrain schedule and the unattended scheduler

A snapshot is only useful while it is fresh, and a dashboard nobody opens trains nothing. v12 moves the retrain out of the browser: the **app backend** runs a ticker that claims due rows and retrains them itself.

### `forecast.retrain`

Created by `ensureSQL` alongside `forecast.snapshots`; `gpx_forecast` is its only DDL owner.

| Column | Meaning |
| --- | --- |
| `scope`, `org_id`, `key` | The primary key. `panel` + this org + `cacheKey`, or `baseline` + `0` + `metric_hash` (written by sibling `timeseries-baselines`, fleet-wide by construction since it has no org) |
| `cron`, `timezone` | Retrain schedule. 5-field `cron.ParseStandard` form plus `@daily` / `@hourly` / `@every 1h`; `timezone` is an IANA name |
| `enabled` | Claimed only when true |
| `spec JSONB` | `panel`: the whole `trainSource` (datasource uid, the verbatim query objects, the train window, the series name, `relative`/`lookbackMs`, and the identification keys `panelId`, `panelTitle`, `dashboardUid`, `querySummary`) plus the model fields the refit needs: `model`, `alpha`, `beta`, `period`, `season`, `calendar`, `lookback` (the operator-readable width). `baseline`: the worker's model spec. NULL means unclaimable, as does a `queries` that is not a non-empty array |
| `next_run_at` | Due when `<= now()`. NULL means never |
| `last_run_at`, `last_status` | `ok` or `error: <message>` |
| `claimed_by`, `claimed_until` | Lease; claimable while `claimed_until IS NULL OR claimed_until < now()` |
| `updated_at` | Autotouched by the upsert |
| `superseded_at` | Set when a newer key from the same dashboard panel replaced this row (see Claiming). A stamped row is never claimed; selecting its key again clears the stamp |

`org_id` is part of the key because a `cacheKey` is **org-independent**: the fingerprint is the targets, options, series name and train window, so the same dashboard provisioned into two orgs hashes the same. Keyed `(scope, key)` those two orgs shared one row, and whichever org fitted last would overwrite the other's `cron`, `enabled` and stored query objects — while the org it overwrote could not even list the row, because `List` filters by org. `ensureSQL` migrates such a table in place (`DROP CONSTRAINT` + `ADD PRIMARY KEY (scope, org_id, key)` inside a `DO` block that only acts when the catalog still reports the two-column key), keeping every row's org, and `scheduleKeySQL` makes `ensureSchedules` refuse a table left on the old key rather than serve it: a runtime user that may not `ALTER` gets `errScheduleKey` on every schedule call (logged, never propagated into a query) instead of a silent cross-org write.

`PRIMARY KEY (scope, org_id, key)` is the whole index story: the table has one row per overlay series per org and one per baseline hash, so a sequence scan at this cardinality beats an index that the planner would ignore anyway.

### Claiming

```sql
WITH due AS (
  SELECT scope, org_id, key FROM forecast.retrain
  WHERE scope = 'panel' AND org_id = $4 AND enabled AND spec IS NOT NULL
    AND jsonb_typeof(spec->'queries') = 'array' AND spec->'queries' <> '[]'::jsonb
    AND superseded_at IS NULL
    AND next_run_at IS NOT NULL AND next_run_at <= now()
    AND (claimed_until IS NULL OR claimed_until < now())
  ORDER BY next_run_at LIMIT $1 FOR UPDATE SKIP LOCKED
)
UPDATE forecast.retrain r SET claimed_by = $2, claimed_until = now() + $3::interval, updated_at = now()
FROM due WHERE r.scope = due.scope AND r.org_id = due.org_id AND r.key = due.key
RETURNING r.scope, r.key, r.org_id, r.cron, r.timezone, r.spec
```

`FOR UPDATE SKIP LOCKED` is what makes Grafana HA safe: one plugin process per Grafana replica, all ticking, and every due row is retrained by exactly one of them. No leader, no lock table, no coordination. The lease covers a process that dies mid-retrain.

The claim is also **org-bound**: the scheduler resolves its own org once per process from `GET /api/org` (the org its credential belongs to) and claims only that org's rows. A `datasourceUid` is resolved by Grafana inside the requesting org, so claiming another org's row would fetch that row's stored queries as this org's series — or fail forever — and then store the result under the other org's key. If `/api/org` itself is refused the tick claims nothing and returns `denied`, so a bad credential still disables the ticker through the auth guard instead of guessing an org. Rows of other orgs stay due and are refreshed by their own org's overlay load.

Releasing a claim is **owner-guarded**, on both sides of the table:

```sql
UPDATE forecast.retrain
SET next_run_at = $4, last_run_at = now(), last_status = $5,
    claimed_by = NULL, claimed_until = NULL, updated_at = now()
WHERE scope = $1 AND org_id = $2 AND key = $3 AND claimed_by = $6  -- $6 = owner
```

The plugin's owner is `host:pid`, resolved once per tick and used for both the claim and every finish; a worker's owner is its `SHARD_ID`/self id. The predicate is the whole guard — **not** `claimed_by IS NULL OR claimed_by = owner`, which would let a retrain that outlived its lease write its own outcome over a row the newer owner had already finished and released. Both `Finish` and `Done` therefore update zero rows for a stale owner, and zero rows is logged at Debug as a lost claim, not an error.

The `panel` predicate requires `spec IS NOT NULL`, a `queries` that is a non-empty array (a stored `null` or `[]` is a row `/api/ds/query` answers `400 query.noQueries` for, so the claim refuses it rather than erroring on every slot) and `superseded_at IS NULL`; the `baseline` predicate is the same shape with `scope='baseline'` and the same superseded check. **The plugin never claims a row it cannot fetch**, and the worker never claims a panel row it has no overlay frontend for. A `panel` row without a spec, or with an empty query list (an overlay trained by an older frontend), is still valid — it just retrains through `needTrain` on the next overlay load.

**Superseding** is how a panel stops paying for a key it no longer uses. The overlay's `cacheKey` covers the training window, so changing the picker mints a new key and writes a new row while the old key's row would otherwise keep its cron forever. `recordPanelSchedule` therefore clears its own row's stamp and, in the same statement, stamps `superseded_at = now()` on the same org's `panel` rows that carry the same `dashboardUid` + `panelId` but a different key. Only a spec with that identity may retire another row — a `cacheKey`-only fit knows no panel and supersedes nothing — and selecting the old key again clears its stamp, so the switch is reversible. The lease is derived from `retrainClaimBatch` and `frameFetchTimeout` (`batch × timeout + timeout + 1m`, 6m today) so a batch of slow fetches can never outlive the claim it took.

### Tick

`runScheduler` claims up to 4 rows per tick (once per `FORECAST_RETRAIN_TICK`, default 30s) and retrains each with the existing limits:

1. `fetchFrames` — resolves the training window, then `POST <FORECAST_GRAFANA_URL>/api/ds/query` with `{queries: spec.queries, from, to}` and an optional `Authorization: Bearer` header. The resolved window and its kind are logged at Debug, so an operator can watch a cron retrain move. Extract (time field, numeric field matching `spec.seriesName` by field name or Grafana display name) into `timeseries.Series[float64]`, dropping NaN, and reject more than `MAX_TRAIN_POINTS` points **before** allocating the slices. The body is the frontend's own request, so **no datasource-specific field is interpreted here**
2. fit through the same `fitRequest` model switch as `POST /forecast`, inside `runLimited` (`workLimiter`), so a scheduled fit competes for the same inflight slots and backs off (`errBusy` → skip, retry next tick) instead of piling up
3. `SnapshotOf` → `store.Put` → `Finish(owner, next = nextRun(cron, timezone, now), "ok")`; any error → `Finish(owner, now + FORECAST_RETRAIN_LEASE, "error: …")`

Every branch logs `retrain scope=… key=… status=… dur=…`. A scheduler error is logged, never propagated: `/forecast` and `QueryData` behave exactly as before when the scheduler is off, when `/api/ds/query` is unreachable, or when Postgres is down. `needTrain` remains the fallback retrain path — including for a `Finish` that lost its claim, where the row simply stays with its new owner.

**Training window resolution.** The stored `trainSource` carries both the absolute `from`/`to` the browser last resolved and, when that window came from a lookback (`Auto` or a legacy duration, not an explicit picker), `relative: true` with `lookbackMs`. A relative row is fetched as `[claim now − lookbackMs, claim now]`, so a cron retrain trains on data that has actually arrived since the last run; the absolute pair is kept for rows an older frontend wrote, and it is the only correct window for a panel whose picker held real dates.

**Series identity.** `spec.seriesName` is `@grafana/data`'s `getFieldDisplayName`, mirrored in Go: `config.displayName`, then `config.displayNameFromDS`, then a time field with no labels, then the frame-name / field-name / label parts (a single label key shared by every labeled field resolves to its value, otherwise `formatLabels`, `{k="v"}`), then the duplicate-name index (`value 1`, `value 2`). A named spec matches either the raw field name or that display name. `@grafana/data`'s `" (comparison)"` suffix is the one preference that cannot be mirrored — it comes from `frame.meta.timeCompare`, which the Go `data.FrameMeta` does not expose. When a name matches nothing, the reply's **only** candidate (exactly one numeric field in a frame with a time field) is accepted, so a display name that drifted between the browser and this process cannot block a panel forever; two candidates still end as `error: … no series named …`, because fitting the wrong one would publish another series under this cache key.

**Auth auto-disable.** A `401`/`403` from Grafana is `errGrafanaUnauthorized`, distinct from every other fetch failure. Three consecutive ticks that end in that refusal end the ticker with one Error log naming `FORECAST_GRAFANA_URL` / `FORECAST_GRAFANA_TOKEN` (or `FORECAST_RETRAIN_ENABLED=false`); a tick that got past the fetch resets the count, so a broken datasource never disables the scheduler and an expired token does not retry forever.

### Auth and liveness

Default `FORECAST_GRAFANA_URL` is `http://127.0.0.1:3000` and the default token is empty, which is what the sandbox's anonymous Admin uses. A production Grafana with anonymous auth off needs a Viewer service-account token, which the scheduler reads from the same levels as the DSN (`FORECAST_GRAFANA_TOKEN`, `[plugin.eduardkolotushin-forecast-app]` / `[plugin.eduardkolotushin-forecast-datasource]`, or `secureJsonData.grafanaToken` written through the settings API — the Configuration page has no field for it). Neither available: Grafana answers `401`/`403` and the scheduler disables itself after three consecutive refusals (above), while the overlay keeps working and `needTrain` keeps refreshing the rows.

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
- Series name is Grafana `getFieldDisplayName` (labels), not raw `"Value"`. That name is what rides in `trainSource.seriesName`, and the backend mirrors the same preference in Go (see **Series identity** above) so a cron retrain re-extracts the same series; the mirror falls back to the reply's only candidate when the name matches nothing.
- Match train ↔ visible by that name. A single train series still maps to all visible series. When several train series exist and a name misses, skip that visible series; `REASON_TRAIN_EMPTY` only when **no** train points exist.

## Training window

`trainRange` is Grafana raw from/to (`now-7d`/`now`, or absolute `YYYY-MM-DD HH:mm:ss` in the **dashboard** timezone, same as the dashboard time picker). The overlay panel parses those strings with `PanelProps.timeZone` and reports whether the result was a lookback (`relative` + `lookbackMs`, re-resolved at retrain time) or an explicit picker (`relative: false`, replayed verbatim). A picker pair is stored as a lookback when it is expressible as one — `from` is a pure relative bound (`now`, `now-7d`, `now+6h`) and `to` is exactly `now`, which is what every Quick range is — because the picker shows those as "the last N"; a calendar or hand-typed absolute pick, a window that does not end at `now` (`now-24h` → `now-1h`), and a rounded bound (`now/d`) stay absolute, since a stored width cannot replay them faithfully. Empty / Auto windows (legacy `lookback` duration still applies if `trainRange` was never saved):

| Model | Lookback |
| --- | --- |
| baseline minute-of-week or hour-of-week | 21d |
| baseline hour | 14d |
| baseline day | 56d |
| seasonal naive | 14d |
| naive, mean, drift, SES, Holt | 7d |

## Forecast window

`forecastRange` is Grafana raw from/to in the same dashboard timezone. Empty / Auto is `[dashboard now, now + autoForecastHorizon]`:

| Model | Duration |
| --- | --- |
| baseline minute-of-week or hour-of-week | 6h |
| baseline hour | 24h |
| baseline day | 7d |
| seasonal naive | 24h |
| naive, mean, drift, SES, Holt | 6h |

The backend emits `last + k×step` points inside that window (skip-ahead; no backcast before `last+step`). One window is capped at `maxForecastPoints` (1e6) points on every path: a `[from, to]` that needs more is a 413 reason, and the library enforces the same bound as `forecast.MaxForecastPoints`.

## Resource routes

The app resource mux is reachable at `/api/plugins/eduardkolotushin-forecast-app/resources/…`.

| Route | Handler | Notes |
| --- | --- | --- |
| `GET /ping` | `handlePing` | Liveness, no auth |
| `POST /forecast` | `handleForecast` | Fit / probe / Restore; body capped at 16 MiB, a body too large for a legal training series refused before decoding, `MAX_TRAIN_POINTS` and `maxForecastPoints` (one emitted window) both answered with 413 |
| `GET /schedules` | `handleSchedules` | This org's `panel` rows plus every fleet-wide `baseline` row, **Admin only** |
| `PUT /schedules` | `handleSchedules` | Retime `{scope, key, cron, timezone, enabled}`; validates the cron and timezone, and an absent `enabled` keeps the stored value. A `baseline` key must already exist (`404`) — those rows are the worker's, so an admin may retime one but never invent one |
| `DELETE /schedules?scope=&key=` | `handleSchedules` | This org's `panel` row, or the fleet-wide `baseline` row; the worker re-creates a live hash's row on its next tick, so deleting a retired hash's row is how its forever-retry ends |
| `POST /schedules/default` | `handleScheduleDefault` | Validates `{cron, timezone}`; the Configuration page calls it before its settings POST and refuses to save a rejected default |

The Admin gate is `backend.PluginConfigFromContext(req.Context()).User.Role != "Admin"` → `403 "forecast: admin required"`. Responses never echo a row's `spec`: the list carries `scope, key, cron, timezone, enabled, nextRunAt, lastRunAt, lastStatus, hasSpec, supersededAt` plus the derived `source` (`dashboardUid`, `panelId`, `panelTitle`, `datasourceUid`, `seriesName`, `querySummary`, `lookback`), which `scheduleSourceFromSpec` reads out of the stored spec with a lenient unmarshal — a malformed or absent spec costs that row its Source cell, never the listing, and `querySummary` is capped at 200 runes on the way out whatever a client stored. `source` is absent for a `baseline` row (the worker writes no spec), whose only identity is its `metric_hash` key.

Every overlay request — the `needTrain` probe included — carries a top-level `provenance {panelId, panelTitle, dashboardUid, querySummary}`, which is deliberately **not** part of `trainSource` (a probe never runs the training query). `Identify` merges those keys into an existing panel row's spec with `spec || $patch`, guarded so it can never create a row (only a fit knows the query objects a claim needs) and never writes when the merge would change nothing (`spec || $3::jsonb IS DISTINCT FROM spec`); errors are logged, never returned. It runs on the **read** paths only: a request that carries training points writes the whole spec — provenance included — through `recordPanelSchedule` anyway, so merging there would be a redundant round trip on the request the user is waiting for, while the cached-load path it does serve already reads this row (`Due`, `Get`). That is how a row written before this field existed becomes identifiable on the next dashboard load, since the cron path has no panel context to fill it in. `recordPanelSchedule` reads its one row by key (`Row`) rather than listing the table, which would transfer and decode every worker baseline row per fit. `querySummary` is summarised from the panel's **own** targets, not the rewritten ones, so the probe and the fit produce the same label — a Postgres rewrite substitutes literal timestamps into SQL text, which would otherwise flip the cell on every load.

## Forecast datasource (alerting)

Grafana unified alerting evaluates backend datasource queries, not overlay panel JavaScript. Typical alert:

1. Query A: live metric (Prometheus / OS / PG / Druid).
2. Query B: this forecast datasource, `kind: forecast`, same `cacheKey` fingerprint as the overlay (datasource uid, canonical SQL/expr, model options, **raw** train-range strings, series name). Overlay Auto is empty from/to strings, not `now-21d`.
3. Query C (optional): `kind: upper` or `lower` at coverage `level` (default 0.95).
4. Expression: A vs B, or A vs C.

`QueryData` looks up the snapshot and emits one time series frame. On miss it returns an error (`needTrain`); it does not fit and does not query other datasources. A query whose `cacheKey` is absent or empty answers that same `needTrain` error — a just-added editor row has no key yet — while a key that is present but malformed stays a `400`. Train on the overlay first. If the store DSN is missing in this process, the error is `snapshot store not configured (needTrain)` — that is not a cache miss.

Mixed overlay + Forecast query: the overlay plots POST forecast only. The Forecast query editor does not auto-fill; **Copy source from query A** is opt-in for the fingerprint. Grafana’s panel Alert tab exists only for Time series / Graph. Overlay options **New alert rule** navigates to `/alerting/new` with live queries (including Mixed Forecast rows), default reduce + threshold, and dashboard/panel annotations. On Grafana 13 scenes, live targets come from the editor scene query runner (`__grafanaSceneContext`), not `getDashboardSaveModel` (that helper still calls `getSaveModelCloneOld`). Dashboard must be saved. Panel menu More → New alert rule and Alerting → New alert rule remain. Do not ship alert rules.

## Horizon clock

Same as `timeseries-forecast`: grid `last + k * step` for `k ≥ 1`, clipped to the request `[from, to]`.

## Load

`gpx_forecast` is a separate process from Grafana, but a large train body or many concurrent Fit / `ForecastRange` calls can still OOM the plugin or stall Grafana’s plugin proxy. v11 bounds that:

- Decode `POST /forecast` with a max body (16 MiB), refuse a body larger than a legal training series (two arrays of `MAX_TRAIN_POINTS` elements at ~32 bytes each) with 413 **before** the decoder runs, and reject `len(times)` / `len(values)` above `MAX_TRAIN_POINTS` (100k) with 413
- Cap one emitted window at `maxForecastPoints` (1e6), checked before the fit on the fit, restore and datasource paths, and map the library's own `forecast.ErrTooManyPoints` to the same 413. Without it a request whose `to` is far enough made `ForecastRange` allocate billions of points and killed the process with no error surfaced
- Inflight semaphore around CPU work only: Fit / SnapshotOf / ForecastRange (overlay) and Restore + ForecastRange (`QueryData`). Snapshot store `Get`/`Put` run outside the semaphore so a slow Postgres does not hold compute slots and turn into 429s; pgxpool bounds the DB side. Default 4, from `FORECAST_MAX_INFLIGHT` / `GF_PLUGIN_*_MAX_INFLIGHT` / GrafanaCfg `max_inflight` / jsonData `maxInflight`. Excess is 429, not an unbounded queue. NeedTrain probes without a snapshot do not take a slot
- Overlay: `maxInflightLoads` panel option (default 1); series POSTs stay sequential; 413/429/5xx set a reason and stop further series POSTs; no automatic retry. Aborting a stale load unsubscribes the `fetch` / `ds.query` Observable, which cancels the HTTP request, so the backend sees `context.Canceled` and frees its slot instead of finishing work nobody will draw
- Train `maxDataPoints` stays `min(100000, …)` so Grafana datasource queries are not a second unbounded path
- Resource and `QueryData` handlers recover panics

## Modules

`go.mod` requires tagged `github.com/eduard-kolotushin/timeseries` and `github.com/eduard-kolotushin/timeseries-forecast`. Do not add a `replace` directive.
