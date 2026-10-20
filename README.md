# timeseries-grafana

Grafana app plugin that overlays forecasts from [`timeseries-forecast`](https://github.com/eduard-kolotushin/timeseries-forecast) on any dashboard query.

**Plugin ID:** `eduardkolotushin-forecast-app`  
**Nested panel:** `eduardkolotushin-forecast-panel`  
**Nested datasource:** `eduardkolotushin-forecast-datasource`  
**Go:** 1.26+

This repo is plugin source only. Local Grafana is [`timeseries-grafana-sandbox`](../timeseries-grafana-sandbox). Cluster Helm and images are [`timeseries-k8s`](../timeseries-k8s). Open all siblings with [`../timeseries-workspace.code-workspace`](../timeseries-workspace.code-workspace).

CI/CD that already ships Grafana plus plugins: merge [`conf/forecast.ini.template`](conf/forecast.ini.template) into `grafana.ini`. See [`conf/README.md`](conf/README.md).

Putting a built `dist/` into a Grafana this repo does not build — a host install, your own image, or a cluster — is in [`docs/INSTALL.md`](docs/INSTALL.md): unsigned loading, app and datasource provisioning, the Postgres store and its migrations, the retrainer, Vault PKI mTLS (per-component switches, including the v21 client certificate to Vault), and the checks that prove it works.

See [docs/INTENTIONS.md](docs/INTENTIONS.md) and [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). What each screen, option and cron actually does — and how it was verified on the Compose and Kubernetes sandboxes — is in [docs/FUNCTIONAL_OVERVIEW.md](docs/FUNCTIONAL_OVERVIEW.md).

The Go backend depends on tagged `timeseries` and `timeseries-forecast` modules.

## Build

```bash
make build      # webpack + Linux backend -> dist/
make frontend   # webpack only
make backend    # Linux amd64 binary only
```

Grafana start/restart lives in the sibling sandbox Makefile (`make up`, `make refresh`, `make ingest`). A cluster install is `helm install` from [`timeseries-k8s`](../timeseries-k8s). Minute-of-week Druid→Kafka baselines run in sibling [`timeseries-baselines`](../timeseries-baselines), not in this plugin process. Overlay options **New alert rule** builds the whole rule from the panel — its metric rows plus a synthesized Forecast row that restores the trained baseline, both over `now - lookback … now`, with reduce/math/threshold, or with a `sql` expression as the condition where the series can only be named inside SQL — so no Mixed datasource or hand-filled Forecast row is needed; alert rules themselves are not shipped here.

## Agents

Contributors and coding agents: start with [AGENTS.md](AGENTS.md).
