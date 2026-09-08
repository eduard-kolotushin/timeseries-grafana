# Changelog

## Unreleased

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
