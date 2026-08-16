package main

import (
	"context"
	"os"

	"github.com/eduard-kolotushin/timeseries-grafana/pkg/plugin"
	"github.com/grafana/grafana-plugin-sdk-go/backend/app"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

func main() {
	plugin.StartWake(context.Background())
	if err := app.Manage("eduardkolotushin-forecast-app", plugin.NewApp, app.ManageOpts{}); err != nil {
		log.DefaultLogger.Error(err.Error())
		os.Exit(1)
	}
}
