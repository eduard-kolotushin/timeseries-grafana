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

Leave `store_host` empty (and do not set `store_url`) to disable the snapshot store. Panel model options are dashboard JSON, not this file. Do not put `allow_loading_unsigned_plugins` or datasource YAML here.

`gpx_forecast` also reads `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_*` (Grafana’s env form of `[plugin.eduardkolotushin-forecast-app]`). Process env `FORECAST_STORE_*` wins over ini.
