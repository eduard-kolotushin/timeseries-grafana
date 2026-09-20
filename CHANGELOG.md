# Changelog

## Unreleased

- The scheduler retrains on **moving** data: `trainSource` carries `relative`/`lookbackMs` when the overlay resolved the window from a lookback, and the backend re-resolves `[now − lookbackMs, now]` at claim time (Debug-logs the resolved window and its kind) instead of refetching the same frozen range every tick. An explicit `trainRange` picker is still replayed verbatim. Neither field is part of the `cacheKey` fingerprint
- A cron retrain now finds the series a labeled panel trained on: the backend mirrors `@grafana/data`'s `getFieldDisplayName` (`config.displayName`, `displayNameFromDS`, frame/field/label parts, the `value 1`/`value 2` duplicate index) and matches `seriesName` against the field name or that display name. When a name matches nothing, a reply with exactly one candidate is accepted instead of failing that row forever; two candidates are still refused
- Only the claim holder can finish a claim: `Finish` (and the worker's `Done`) is `UPDATE … WHERE scope = $1 AND key = $2 AND (claimed_by IS NULL OR claimed_by = $owner)`, so a retrain that outlived its lease can no longer clear a newer owner's claim or overwrite its `next_run_at`/`last_status`
- The scheduler disables itself after three consecutive `401`/`403` refusals from Grafana with one Error log naming `FORECAST_GRAFANA_URL` / `FORECAST_GRAFANA_TOKEN` (or `FORECAST_RETRAIN_ENABLED=false`), instead of retrying a configuration error every tick forever; a tick that got past the fetch resets the count, so a broken datasource never disables it and `needTrain` remains the fallback
- The train-length cap now runs before the training slices are allocated, so an oversized `/api/ds/query` reply is refused with 413-class `errTrainTooLong` instead of allocating first, and the snapshot and schedule probes keep separate readiness/retry state, so a failing `forecast.retrain` DDL no longer turns a snapshot read into an error
- Snapshots can retrain on a cron with no browser open. The app backend (`gpx_forecast`) runs a ticker that claims due rows from `forecast.retrain` (`FOR UPDATE SKIP LOCKED`, so Grafana HA retrains each row exactly once) and re-fits them itself by replaying the stored `trainSource` through Grafana's own `/api/ds/query`. `POST /forecast` now records `trainSource` (datasource uid, the verbatim query objects, the training window, the series name) from a successful fit; it is deliberately **not** part of the `cacheKey` fingerprint, so adding or editing a schedule never invalidates a stored snapshot. The ticker is off with `FORECAST_RETRAIN_ENABLED=false`, and `needTrain` remains the retrain path whenever the scheduler cannot run
- New resource routes `GET|PUT|DELETE /schedules` and `POST /schedules/default` (Admin-only via `backend.PluginContext.User.Role`) plus a **Retrain schedules** app page (a tab beside Overview and Configuration): every row's cron, timezone, next/last run and status, an enable toggle, and delete for `panel` rows. `baseline` rows belong to sibling `timeseries-baselines`, are listed and editable here, and cannot be deleted (the worker re-inserts them)
- `forecast.retrain` (`gpx_forecast` is its only DDL owner) carries `scope` `panel`/`baseline`, `cron`, `timezone`, `enabled`, `spec`, `next_run_at`, `last_run_at`, `last_status` and the `claimed_by`/`claimed_until` lease. The plugin claims only `panel` rows with a spec; the worker claims only `baseline` rows, so neither can claim work it cannot run
- A probe now also returns `needTrain` when the row's `next_run_at` has passed, so a row the scheduler cannot retrain is still refreshed by the next overlay load
- Scheduled retrains reproduce the panel's own model: the stored `spec` carries `alpha` / `beta` / `period` (a cron retrain used to refit with the backend defaults and overwrite the snapshot with them), and a browser fit no longer re-enables a row an admin turned off or moves its cron. A `seriesName` the replayed frames do not carry now records an `error` instead of fitting a different series under that cache key, the DDL fallback checks `forecast.snapshots` and `forecast.retrain` separately before latching ready, and the Configuration page validates the default cron/timezone through `POST /schedules/default` (a rejected default blocks the save) while preserving the jsonData keys it does not render
- Snapshot cache is bounded (256 entries) with a 30 s TTL, so an overlay Retrain reaches the alerting datasource process (and other Grafana replicas) without a restart and resident memory no longer grows with distinct cache keys
- Snapshot store connects lazily and retries (5 s backoff) instead of failing permanently when Postgres is down at plugin start; DDL permission errors are tolerated when the table the call needs already exists
- Inflight limiter bounds Fit / Restore / ForecastRange only; Postgres reads and writes no longer hold compute slots
- `cacheKey` falls back to a pure-JS SHA-256 when `crypto.subtle` is unavailable (plain-HTTP Grafana), producing identical keys
- Aborted overlay loads cancel the in-flight `POST /forecast` and training query instead of only skipping the next request
- Button-only panel editors (Retrain, New alert rule) use their own option paths instead of `trainRange`

- Train and forecast pickers use the dashboard timezone (not a hard-coded browser zone), so calendar days match `resolveTrainWindow` / `resolveForecastWindow`

## 1.0.0

- App plugin with Go `/forecast` resource
- Nested Forecast overlay panel
- Nested Forecast datasource for Grafana alerting (`QueryData` Restore of overlay snapshots)
- Mixed overlay: ignore Forecast datasource frames for fit and plot; optional Copy source from query A (no auto-fill)
- Overlay options New alert rule: Grafana alerting form with live panel queries (not the Time series Alert tab)
- Load limits: cap train body/length and concurrent Fit / ForecastRange; overlay `maxInflightLoads` (default 1); 413/429 or a panel reason instead of unbounded work
