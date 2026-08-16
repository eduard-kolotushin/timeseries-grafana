# timeseries-grafana

Grafana app plugin that overlays forecasts from [`timeseries-forecast`](https://github.com/eduard-kolotushin/timeseries-forecast) on any dashboard query.

**Plugin ID:** `eduardkolotushin-forecast-app`  
**Nested panel:** `eduardkolotushin-forecast-panel`  
**Go:** 1.26+

This repo is plugin source only. The Grafana runtime lives in [`timeseries-grafana-sandbox`](../timeseries-grafana-sandbox). Open all siblings with [`../timeseries-workspace.code-workspace`](../timeseries-workspace.code-workspace).

See [docs/INTENTIONS.md](docs/INTENTIONS.md) and [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Local siblings

`go.mod` replaces the libraries while they live side by side:

```
replace github.com/eduard-kolotushin/timeseries => ../timeseries
replace github.com/eduard-kolotushin/timeseries-forecast => ../timeseries-forecast
```

## Build

```bash
make build      # webpack + Linux backend -> dist/
make frontend   # webpack only
make backend    # Linux amd64 binary only
```

Grafana start/restart lives in the sibling sandbox Makefile (`make up`, `make refresh`, `make ingest`).

Minute-of-week baselines are published from the Go backend when enabled on **Configuration** (Druid broker, datasource, Kafka brokers, baseline topic, lookback, ahead minutes N, interval). The overlay panel does not need those settings.

## Agents

Contributors and coding agents: start with [AGENTS.md](AGENTS.md).
