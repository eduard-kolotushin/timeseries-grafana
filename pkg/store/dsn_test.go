package store

import (
	"strings"
	"testing"
)

// TestEnvLookup pins the migrator's lookup: process env only, in the same order
// the plugin's Lookup uses, with no ini-file or jsonData spelling.
func TestEnvLookup(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "FORECAST_* wins over both GF_PLUGIN prefixes",
			env: map[string]string{
				"FORECAST_STORE_HOST":                    "from-forecast",
				PluginEnvPrefixApp + "STORE_HOST":        "from-app",
				PluginEnvPrefixDatasource + "STORE_HOST": "from-ds",
			},
			want: "from-forecast",
		},
		{
			name: "app prefix before the datasource prefix",
			env: map[string]string{
				PluginEnvPrefixApp + "STORE_HOST":        "from-app",
				PluginEnvPrefixDatasource + "STORE_HOST": "from-ds",
			},
			want: "from-app",
		},
		{
			name: "datasource prefix is the fallback",
			env:  map[string]string{PluginEnvPrefixDatasource + "STORE_HOST": "from-ds"},
			want: "from-ds",
		},
		{
			name: "a blank value is not a value",
			env:  map[string]string{"FORECAST_STORE_HOST": "   "},
			want: "",
		},
		{
			name: "the ini and jsonData spellings have no env equivalent",
			env:  map[string]string{"store_host": "from-ini", "storeHost": "from-json"},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := EnvLookup{Getenv: func(key string) string { return tt.env[key] }}
			if got := l.Get("FORECAST_STORE_HOST", "STORE_HOST", "store_host", "storeHost"); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
	if got := (EnvLookup{}).Get("FORECAST_STORE_HOST", "STORE_HOST", "store_host", "storeHost"); got != "" {
		t.Fatalf("a nil Getenv must resolve nothing, got %q", got)
	}
}

// TestDSNFromEnvOnly covers the CLI's chain: --dsn bypasses it, and what it
// resolves has to carry the same field defaults the plugin's chain applies.
func TestDSNFromEnvOnly(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "nothing set means persist off", want: ""},
		{
			name: "a URL short-circuits the field-wise form",
			env: map[string]string{
				"FORECAST_STORE_URL":                         "postgres://u:p@url-host:1111/db?sslmode=require",
				PluginEnvPrefixApp + "STORE_HOST":            "ignored",
				PluginEnvPrefixDatasource + "STORE_DATABASE": "ignored-db",
			},
			want: "postgres://u:p@url-host:1111/db?sslmode=require",
		},
		{
			name: "a GF_PLUGIN host gets every default",
			env:  map[string]string{PluginEnvPrefixApp + "STORE_HOST": "pg"},
			want: "postgres://overlay@pg:5432/overlay?sslmode=disable",
		},
		{
			name: "the datasource prefix and a password",
			env: map[string]string{
				PluginEnvPrefixDatasource + "STORE_HOST": "pg2",
				PluginEnvPrefixDatasource + "STORE_PORT": "6543",
				"FORECAST_STORE_PASSWORD":                "s3cret",
			},
			want: "postgres://overlay:s3cret@pg2:6543/overlay?sslmode=disable",
		},
		{
			name: "an explicit sslmode",
			env: map[string]string{
				"FORECAST_STORE_HOST":    "pg",
				"FORECAST_STORE_SSLMODE": "require",
			},
			want: "postgres://overlay@pg:5432/overlay?sslmode=require",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := EnvLookup{Getenv: func(key string) string { return tt.env[key] }}
			if got := DSNFrom(l, ""); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

// TestScratchDSN: the migration tests derive their throwaway database's DSN from
// the configured one, so the credentials and sslmode have to survive the rewrite.
func TestScratchDSN(t *testing.T) {
	got, err := ScratchDSN("postgres://overlay:overlay@localhost:5432/overlay?sslmode=disable", "migrate_test")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/migrate_test", "overlay:overlay@", "sslmode=disable"} {
		if !strings.Contains(got, want) {
			t.Fatalf("ScratchDSN lost %q: %s", want, got)
		}
	}
	if _, err := ScratchDSN("://not-a-dsn", "x"); err == nil {
		t.Fatal("a malformed DSN must be an error")
	}
}
