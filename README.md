# timeseries-grafana

Grafana app plugin that overlays forecasts from [`timeseries-forecast`](https://github.com/eduard-kolotushin/timeseries-forecast) on any dashboard query.

**Plugin ID:** `eduardkolotushin-forecast-app`  
**Nested panel:** `eduardkolotushin-forecast-panel`  
**Go:** 1.26+

This repo is plugin source only. Local Grafana is [`timeseries-grafana-sandbox`](../timeseries-grafana-sandbox). Cluster Helm and images are [`timeseries-k8s`](../timeseries-k8s). Open all siblings with [`../timeseries-workspace.code-workspace`](../timeseries-workspace.code-workspace).

CI/CD that already ships Grafana plus plugins: merge [`conf/forecast.ini.template`](conf/forecast.ini.template) into `grafana.ini`. See [`conf/README.md`](conf/README.md).

See [docs/INTENTIONS.md](docs/INTENTIONS.md) and [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

The Go backend depends on tagged `timeseries` and `timeseries-forecast` modules.

## Build

```bash
make build      # webpack + Linux backend -> dist/
make frontend   # webpack only
make backend    # Linux amd64 binary only
```

Grafana start/restart lives in the sibling sandbox Makefile (`make up`, `make refresh`, `make ingest`). A cluster install is `helm install` from [`timeseries-k8s`](../timeseries-k8s). Minute-of-week Druid→Kafka baselines run in sibling [`timeseries-baselines`](../timeseries-baselines), not in this plugin process.

## Agents

Contributors and coding agents: start with [AGENTS.md](AGENTS.md).
