package plugin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/eduard-kolotushin/timeseries-grafana/pkg/store"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

func clearStoreEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"FORECAST_STORE_URL",
		"FORECAST_STORE_HOST",
		"FORECAST_STORE_PORT",
		"FORECAST_STORE_DATABASE",
		"FORECAST_STORE_USER",
		"FORECAST_STORE_SSLMODE",
		"FORECAST_STORE_PASSWORD",
		store.PluginEnvPrefixApp + "STORE_URL",
		store.PluginEnvPrefixApp + "STORE_HOST",
		store.PluginEnvPrefixApp + "STORE_PORT",
		store.PluginEnvPrefixApp + "STORE_DATABASE",
		store.PluginEnvPrefixApp + "STORE_USER",
		store.PluginEnvPrefixApp + "STORE_SSL_MODE",
		store.PluginEnvPrefixApp + "STORE_PASSWORD",
		store.PluginEnvPrefixDatasource + "STORE_URL",
		store.PluginEnvPrefixDatasource + "STORE_HOST",
		store.PluginEnvPrefixDatasource + "STORE_PORT",
		store.PluginEnvPrefixDatasource + "STORE_DATABASE",
		store.PluginEnvPrefixDatasource + "STORE_USER",
		store.PluginEnvPrefixDatasource + "STORE_SSL_MODE",
		store.PluginEnvPrefixDatasource + "STORE_PASSWORD",
	}
	for _, k := range keys {
		t.Setenv(k, "")
	}
}

func TestStoreDSN(t *testing.T) {
	jsonHost, _ := json.Marshal(map[string]any{"storeHost": "from-json"})
	jsonHostPort, _ := json.Marshal(map[string]any{"storeHost": "from-json", "storePort": 5555})
	jsonURL, _ := json.Marshal(map[string]any{"storeUrl": "postgres://u:p@json-url:2222/db?sslmode=require"})
	jsonURLAndHost, _ := json.Marshal(map[string]any{
		"storeUrl":  "postgres://u:p@json-url:2222/db?sslmode=require",
		"storeHost": "ignored-json-host",
	})
	tests := []struct {
		name     string
		env      map[string]string
		cfg      map[string]string
		settings backend.AppInstanceSettings
		want     string
		wantHost string
	}{
		{name: "empty is persist off"},
		{
			name: "FORECAST_STORE_URL wins",
			env:  map[string]string{"FORECAST_STORE_URL": "postgres://u:p@url-host:1111/db?sslmode=require"},
			cfg:  map[string]string{"store_host": "from-cfg"},
			settings: backend.AppInstanceSettings{
				JSONData: jsonHost,
			},
			want: "postgres://u:p@url-host:1111/db?sslmode=require",
		},
		{
			name:     "FORECAST_STORE_HOST wins over GF_PLUGIN and json",
			env:      map[string]string{"FORECAST_STORE_HOST": "from-env", store.PluginEnvPrefixApp + "STORE_HOST": "from-gf"},
			settings: backend.AppInstanceSettings{JSONData: jsonHost},
			wantHost: "from-env:5432",
		},
		{
			name:     "GF_PLUGIN after FORECAST_STORE empty",
			env:      map[string]string{store.PluginEnvPrefixApp + "STORE_HOST": "from-gf"},
			settings: backend.AppInstanceSettings{JSONData: jsonHost},
			wantHost: "from-gf:5432",
		},
		{
			name:     "GF_PLUGIN datasource prefix after app prefix empty",
			env:      map[string]string{store.PluginEnvPrefixDatasource + "STORE_HOST": "from-ds-gf"},
			settings: backend.AppInstanceSettings{JSONData: jsonHost},
			wantHost: "from-ds-gf:5432",
		},
		{
			name:     "GrafanaCfg store_host",
			cfg:      map[string]string{"store_host": "from-cfg"},
			settings: backend.AppInstanceSettings{JSONData: jsonHost},
			wantHost: "from-cfg:5432",
		},
		{
			name:     "GrafanaCfg datasource plugin section",
			cfg:      map[string]string{"plugin.eduardkolotushin-forecast-datasource.store_host": "from-ds-cfg"},
			settings: backend.AppInstanceSettings{JSONData: jsonHost},
			wantHost: "from-ds-cfg:5432",
		},
		{
			name:     "jsonData last",
			settings: backend.AppInstanceSettings{JSONData: jsonHostPort},
			wantHost: "from-json:5555",
		},
		{
			name:     "jsonData storeUrl",
			settings: backend.AppInstanceSettings{JSONData: jsonURL},
			want:     "postgres://u:p@json-url:2222/db?sslmode=require",
		},
		{
			name:     "jsonData storeUrl short-circuits a jsonData host",
			settings: backend.AppInstanceSettings{JSONData: jsonURLAndHost},
			want:     "postgres://u:p@json-url:2222/db?sslmode=require",
		},
		{
			name: "secureJsonData password",
			env:  map[string]string{"FORECAST_STORE_HOST": "pg"},
			settings: backend.AppInstanceSettings{
				DecryptedSecureJSONData: map[string]string{"storePassword": "s3cret"},
			},
			want: "postgres://overlay:s3cret@pg:5432/overlay?sslmode=disable",
		},
		{
			name: "GF_PLUGIN password",
			env: map[string]string{
				"FORECAST_STORE_HOST":                       "pg",
				store.PluginEnvPrefixApp + "STORE_PASSWORD": "ini-secret",
			},
			want: "postgres://overlay:ini-secret@pg:5432/overlay?sslmode=disable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearStoreEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			ctx := context.Background()
			if tt.cfg != nil {
				ctx = backend.WithGrafanaConfig(ctx, backend.NewGrafanaCfg(tt.cfg))
			}
			got := storeDSN(ctx, tt.settings)
			if tt.want != "" {
				if got != tt.want {
					t.Fatalf("got %q want %q", got, tt.want)
				}
				return
			}
			if tt.wantHost == "" {
				if got != "" {
					t.Fatalf("got %q want empty", got)
				}
				return
			}
			if !strings.Contains(got, "@"+tt.wantHost+"/") && !strings.Contains(got, "//"+tt.wantHost+"/") {
				t.Fatalf("got %q want host %q", got, tt.wantHost)
			}
		})
	}
}

func TestStoreJSONFromContext(t *testing.T) {
	dsHost, _ := json.Marshal(map[string]any{"storeHost": "from-ds"})
	dsURL, _ := json.Marshal(map[string]any{"storeUrl": "postgres://u:p@ds-url:3333/db?sslmode=require"})
	appHost, _ := json.Marshal(map[string]any{"storeHost": "from-app"})
	emptyObj, _ := json.Marshal(map[string]any{})
	tests := []struct {
		name     string
		dsJSON   []byte
		dsSecure map[string]string
		app      *backend.AppInstanceSettings
		wantHost string
	}{
		{name: "empty is persist off"},
		{
			name:     "empty object falls through to app",
			dsJSON:   emptyObj,
			app:      &backend.AppInstanceSettings{JSONData: appHost},
			wantHost: "from-app:5432",
		},
		{
			name:     "app jsonData when ds empty",
			app:      &backend.AppInstanceSettings{JSONData: appHost},
			wantHost: "from-app:5432",
		},
		{
			name:     "ds jsonData wins over app",
			dsJSON:   dsHost,
			app:      &backend.AppInstanceSettings{JSONData: appHost},
			wantHost: "from-ds:5432",
		},
		{
			// A datasource that declares only a URL must beat the parent app's host: jsonHasStore
			// counts the URL, and since the DSN builder reads the same key the pair is resolvable.
			name:     "ds jsonData storeUrl wins over app and resolves",
			dsJSON:   dsURL,
			app:      &backend.AppInstanceSettings{JSONData: appHost},
			wantHost: "ds-url:3333",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearStoreEnv(t)
			jsonData, secure := storeJSONFromContext(tt.dsJSON, tt.dsSecure, tt.app)
			got := storeDSNFrom(context.Background(), jsonData, secure)
			if tt.wantHost == "" {
				if got != "" {
					t.Fatalf("got %q want empty", got)
				}
				return
			}
			if !strings.Contains(got, "@"+tt.wantHost+"/") && !strings.Contains(got, "//"+tt.wantHost+"/") {
				t.Fatalf("got %q want host %q", got, tt.wantHost)
			}
		})
	}
}
