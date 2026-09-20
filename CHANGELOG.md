# Changelog

## Unreleased

- Snapshots can retrain on a cron with no browser open. The app backend (`gpx_forecast`) runs a ticker that claims due rows from `forecast.retrain` (`FOR UPDATE SKIP LOCKED`, so Grafana HA retrains each row exactly once) and re-fits them itself by replaying the stored `trainSource` through Grafana's own `/api/ds/query`. `POST /forecast` now records `trainSource` (datasource uid, the verbatim query objects, the training window, the series name) from a successful fit; it is deliberately **not** part of the `cacheKey` fingerprint, so adding or editing a schedule never invalidates a stored snapshot. The ticker is off with `FORECAST_RETRAIN_ENABLED=false`, and `needTrain` remains the retrain path whenever the scheduler cannot run
- New resource routes `GET|PUT|DELETE /schedules` and `POST /schedules/default` (Admin-only via `backend.PluginContext.User.Role`) plus a **Retrain schedules** table on the Configuration page: every row's cron, timezone, next/last run and status, an enable toggle, and delete for `panel` rows. `baseline` rows belong to sibling `timeseries-baselines`, are listed and editable here, and cannot be deleted (the worker re-inserts them)
- `forecast.retrain` (`gpx_forecast` is its only DDL owner) carries `scope` `panel`/`baseline`, `cron`, `timezone`, `enabled`, `spec`, `next_run_at`, `last_run_at`, `last_status` and the `claimed_by`/`claimed_until` lease. The plugin claims only `panel` rows with a spec; the worker claims only `baseline` rows, so neither can claim work it cannot run
- A probe now also returns `needTrain` when the row's `next_run_at` has passed, so a row the scheduler cannot retrain is still refreshed by the next overlay load
- Snapshot cache is bounded (256 entries) with a 30 s TTL, so an overlay Retrain reaches the alerting datasource process (and other Grafana replicas) without a restart and resident memory no longer grows with distinct cache keys
- Snapshot store connects lazily and retries (5 s backoff) instead of failing permanently when Postgres is down at plugin start; DDL permission errors are tolerated when the table already exists
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
