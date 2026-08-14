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
npm install
npm run build
```

On Windows, cross-compile the backend for the Linux Grafana container:

```powershell
$env:GOOS="linux"; $env:GOARCH="amd64"; go build -o dist/gpx_forecast_linux_amd64 ./pkg
```

On Linux/macOS:

```bash
GOOS=linux GOARCH=amd64 go build -o dist/gpx_forecast_linux_amd64 ./pkg
```

Then start Grafana from the sandbox:

```bash
cd ../timeseries-grafana-sandbox
docker compose up
```

## Agents

Contributors and coding agents: start with [AGENTS.md](AGENTS.md).
