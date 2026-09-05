# CI/CD grafana.ini snippet

Proprietary Grafana images that already copy `dist/` can merge [forecast.ini.template](forecast.ini.template) into `grafana.ini` (or `custom.ini`).

Grafana expands `${FORECAST_STORE_*}` in `.ini` files. Your pipeline can also replace those placeholders before shipping the file.

| ini key | Environment variable |
| --- | --- |
| `store_url` | `FORECAST_STORE_URL` |
| `store_host` | `FORECAST_STORE_HOST` |
| `store_port` | `FORECAST_STORE_PORT` |
| `store_database` | `FORECAST_STORE_DATABASE` |
| `store_user` | `FORECAST_STORE_USER` |
| `store_ssl_mode` | `FORECAST_STORE_SSLMODE` |
| `store_password` | `FORECAST_STORE_PASSWORD` |
| `max_inflight` | `FORECAST_MAX_INFLIGHT` |

Leave `store_host` empty (and do not set `store_url`) to disable the snapshot store. Panel model options are dashboard JSON, not this file. Do not put `allow_loading_unsigned_plugins` or datasource YAML here.

The template has two identical plugin sections: overlay (`eduardkolotushin-forecast-app`) and alerting QueryData (`eduardkolotushin-forecast-datasource`). Grafana 12.4+ does not forward host `FORECAST_STORE_*` into plugin processes by default; each process reads its own `[plugin.<id>]` via `GF_PLUGIN_*` / GrafanaCfg. Overlay jsonData comes from the app Configuration page; alerting needs the same keys on the Forecast datasource instance unless this ini section is merged.

`gpx_forecast` also reads `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_*` and `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_DATASOURCE_*`. Process env `FORECAST_STORE_*` and `FORECAST_MAX_INFLIGHT` win over ini when present in the plugin process. Empty `max_inflight` uses the default of 4 concurrent Fit / ForecastRange slots.
