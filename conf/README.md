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
| `retrain_enabled` | `FORECAST_RETRAIN_ENABLED` |
| `retrain_tick` | `FORECAST_RETRAIN_TICK` |
| `retrain_lease` | `FORECAST_RETRAIN_LEASE` |
| `retrain_cron` | `FORECAST_RETRAIN_CRON` |
| `grafana_url` | `FORECAST_GRAFANA_URL` |
| `grafana_token` | `FORECAST_GRAFANA_TOKEN` |

Leave `store_host` empty (and do not set `store_url`) to disable the snapshot store. Panel model options are dashboard JSON, not this file. Do not put `allow_loading_unsigned_plugins` or datasource YAML here.

The template's two plugin sections are not identical: the overlay section (`eduardkolotushin-forecast-app`) carries the `retrain_*` and `grafana_*` keys as well, while the alerting QueryData section (`eduardkolotushin-forecast-datasource`) holds the store keys only. Grafana 12.4+ does not forward host `FORECAST_STORE_*` into plugin processes by default; each process reads its own `[plugin.<id>]` via `GF_PLUGIN_*` / GrafanaCfg. Overlay jsonData comes from provisioning (the app Configuration page renders no store fields); alerting needs the same keys on the Forecast datasource instance unless this ini section is merged.

`gpx_forecast` also reads `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_*` and `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_DATASOURCE_*`. Process env `FORECAST_STORE_*` and `FORECAST_MAX_INFLIGHT` win over ini when present in the plugin process. Empty `max_inflight` uses the default of 4 concurrent Fit / ForecastRange slots.

The `retrain_*` and `grafana_*` keys belong to the **app** section only. The app process runs the unattended retrain scheduler (claim a due `forecast.retrain` row, fetch training frames from Grafana's `/api/ds/query`, fit, store). The datasource process serves alerting `QueryData`, which only Restores, so it has no scheduler. Empty `retrain_enabled` means on; `retrain_tick` defaults to `30s`, `retrain_lease` to `6m` (derived, not chosen: `retrainClaimBatch × frameFetchTimeout + frameFetchTimeout + 1 min` = `4 × 60s + 60s + 1m`, `pkg/plugin/retrain.go:46`), `retrain_cron` to `0 3 * * *`. `grafana_token` is only needed when anonymous auth is off: use a Viewer service-account token, or merge `grafana_token` in this ini / set `FORECAST_GRAFANA_TOKEN` in the plugin process env instead of a plaintext value elsewhere — no plugin page has a token field (the Configuration page renders only Cron and Timezone).

## Preparing the schema (migration CLI)

`gpx_forecast_migrate` (`cmd/migrate`, `make migrate`) applies the plugin's versioned schema — `pkg/store/migrations/*.sql` — without starting the plugin, so a CI/CD pipeline can prepare the database before Grafana starts: a one-shot compose service or a Kubernetes Job in `timeseries-grafana-sandbox` / `timeseries-k8s`.

It reads the same store settings as the plugin and nothing else: process env `FORECAST_STORE_*`, then the `GF_PLUGIN_*` variables Grafana exports for the two plugin sections (no grafana.ini of its own, no jsonData). `--dsn` overrides them, `--dry-run` lists the pending versions and writes nothing (not even the ledger), `--timeout` defaults to `60s`, and the exit code is non-zero on the first failing migration.

Running it is optional. The plugin applies the same set at its first store use, so a deployment that skips this step converges one request late. Running it while Grafana is up is safe: each migration takes a `pg_advisory_xact_lock` and runs in one transaction, and a `forecast.schema_migrations` ledger row records it, so a pipeline run and every plugin process serialise instead of racing. That schema needs PostgreSQL 13 or newer (`gen_random_uuid()`); a ledger version this binary does not embed is a warning, never an error.
