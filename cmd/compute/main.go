// Command gpx_forecast_compute runs this plugin's /forecast handler as a
// standalone service, so a deployment can scale forecasting capacity on its own
// instead of by adding Grafana replicas.
//
// It is the other half of the plugin's FORECAST_COMPUTE_URL mode: the plugin
// keeps serving the panel's resource URL and forwards POST /forecast here, with
// the shared token and the org Grafana authenticated. The service opens the same
// snapshot store as the plugin, runs the same retrain ticker against the same
// forecast.retrain claim queue, and fetches its training frames from Grafana's own
// /api/ds/query — no datasource client is involved.
//
// It reads its configuration from the process environment only (FORECAST_STORE_*
// for the store, FORECAST_RETRAIN_* and FORECAST_GRAFANA_* for the ticker,
// FORECAST_MAX_INFLIGHT for the work limit, FORECAST_COMPUTE_TOKEN for the shared
// secret), the same rule as gpx_forecast_migrate: no grafana.ini and no jsonData.
// The plugin's FORECAST_COMPUTE_URL is deliberately ignored here, so a shared env
// block cannot make one compute service forward to another.
//
//	gpx_forecast_compute [--listen :8080]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/eduard-kolotushin/timeseries-grafana/pkg/plugin"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stderr))
}

// run resolves the listen address, serves /forecast until the process is
// interrupted, and returns the process exit code. It takes its environment and
// error stream as arguments so a test can drive it without a subprocess.
func run(args []string, getenv func(string) string, stderr io.Writer) int {
	flags := flag.NewFlagSet("gpx_forecast_compute", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listen := flags.String("listen", "", "address to listen on; overrides FORECAST_COMPUTE_LISTEN (default :8080)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	addr := strings.TrimSpace(*listen)
	if addr == "" {
		addr = strings.TrimSpace(getenv("FORECAST_COMPUTE_LISTEN"))
	}
	if addr == "" {
		addr = ":8080"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := plugin.ServeCompute(ctx, addr); err != nil {
		fmt.Fprintf(stderr, "gpx_forecast_compute: %v\n", err)
		return 1
	}
	return 0
}
