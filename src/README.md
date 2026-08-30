# Timeseries Forecast

Grafana app that fits univariate models from `timeseries-forecast` and overlays the forecast window on the query history.

Add the **Forecast overlay** visualization to a panel, pick a data source, and choose a model. After a snapshot is saved, the nested **Forecast** datasource can emit forecast / interval series for Grafana alerting by `refId`. The Docker Grafana + TestData environment is the sibling `timeseries-grafana-sandbox`.
