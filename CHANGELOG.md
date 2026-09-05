# Changelog

## 1.0.0

- App plugin with Go `/forecast` resource
- Nested Forecast overlay panel
- Nested Forecast datasource for Grafana alerting (`QueryData` Restore of overlay snapshots)
- Mixed overlay: ignore Forecast datasource frames for fit and plot; optional Copy source from query A (no auto-fill)
- Overlay options New alert rule: Grafana alerting form with live panel queries (not the Time series Alert tab)
- Load limits: cap train body/length and concurrent Fit / ForecastRange; overlay `maxInflightLoads` (default 1); 413/429 or a panel reason instead of unbounded work
