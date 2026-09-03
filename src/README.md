# Timeseries Forecast

Grafana app that fits univariate models from `timeseries-forecast` and overlays the forecast window on the query history.

Add the **Forecast overlay** visualization to a panel, pick a data source, and choose a model. Mixed metric + Forecast datasource queries are supported on the overlay (Forecast frames are not fitted or plotted there). After a snapshot is saved, the nested **Forecast** datasource can emit forecast / interval series for Grafana alerting by `refId`. Overlay options **New alert rule** opens Grafana’s alerting editor with the panel queries. Grafana’s panel Alert tab is on Time series, not this overlay. The Docker Grafana + TestData environment is the sibling `timeseries-grafana-sandbox`.
