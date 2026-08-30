package plugin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
		gfPluginPrefix + "STORE_URL",
		gfPluginPrefix + "STORE_HOST",
		gfPluginPrefix + "STORE_PORT",
		gfPluginPrefix + "STORE_DATABASE",
		gfPluginPrefix + "STORE_USER",
		gfPluginPrefix + "STORE_SSL_MODE",
		gfPluginPrefix + "STORE_PASSWORD",
	}
	for _, k := range keys {
		t.Setenv(k, "")
	}
}

func TestStoreDSN(t *testing.T) {
	jsonHost, _ := json.Marshal(map[string]any{"storeHost": "from-json"})
	jsonHostPort, _ := json.Marshal(map[string]any{"storeHost": "from-json", "storePort": 5555})
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
			env:      map[string]string{"FORECAST_STORE_HOST": "from-env", gfPluginPrefix + "STORE_HOST": "from-gf"},
			settings: backend.AppInstanceSettings{JSONData: jsonHost},
			wantHost: "from-env:5432",
		},
		{
			name:     "GF_PLUGIN after FORECAST_STORE empty",
			env:      map[string]string{gfPluginPrefix + "STORE_HOST": "from-gf"},
			settings: backend.AppInstanceSettings{JSONData: jsonHost},
			wantHost: "from-gf:5432",
		},
		{
			name:     "GrafanaCfg store_host",
			cfg:      map[string]string{"store_host": "from-cfg"},
			settings: backend.AppInstanceSettings{JSONData: jsonHost},
			wantHost: "from-cfg:5432",
		},
		{
			name:     "jsonData last",
			settings: backend.AppInstanceSettings{JSONData: jsonHostPort},
			wantHost: "from-json:5555",
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
				"FORECAST_STORE_HOST":             "pg",
				gfPluginPrefix + "STORE_PASSWORD": "ini-secret",
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
