package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/eduard-kolotushin/timeseries-grafana/pkg/plugin"
	"github.com/grafana/grafana-plugin-sdk-go/backend/app"
	"github.com/grafana/grafana-plugin-sdk-go/backend/datasource"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

const (
	appPluginID        = "eduardkolotushin-forecast-app"
	datasourcePluginID = "eduardkolotushin-forecast-datasource"
)

func main() {
	if datasourceMode() {
		if err := datasource.Manage(datasourcePluginID, plugin.NewDatasource, datasource.ManageOpts{}); err != nil {
			log.DefaultLogger.Error(err.Error())
			os.Exit(1)
		}
		return
	}
	if err := app.Manage(appPluginID, plugin.NewApp, app.ManageOpts{}); err != nil {
		log.DefaultLogger.Error(err.Error())
		os.Exit(1)
	}
}

func datasourceMode() bool {
	if os.Getenv("GF_PLUGIN_ID") == datasourcePluginID {
		return true
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.Contains(filepath.ToSlash(exe), "/forecast-datasource/")
}
