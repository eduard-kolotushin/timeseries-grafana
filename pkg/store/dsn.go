package store

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
)

// PluginEnvPrefixApp and PluginEnvPrefixDatasource are the Grafana 12.4+ process
// env spellings of a plugin's own ini section: Grafana exports
// [plugin.<id>] <key> as GF_PLUGIN_<ID>_<KEY>.
const (
	PluginEnvPrefixApp        = "GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_"
	PluginEnvPrefixDatasource = "GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_DATASOURCE_"
)

// Lookup resolves one store setting in the plugin's precedence order:
// FORECAST_* env, then GF_PLUGIN_* env for either plugin id, then grafana.ini via
// GrafanaCfg, then jsonData. It is an interface so the same DSN rules serve the
// plugin (which has an ini file and jsonData) and the migration CLI (env only).
type Lookup interface {
	Get(forecastEnv, gfSuffix, iniKey, jsonKey string) string
}

// DSNFrom resolves the store DSN. A URL short-circuits the field-wise form; an
// empty host means "no store", which the plugin reads as persist-off. securePassword
// is the datasource/app secureJsonData password, used when no env or ini value exists.
//
// The json key is camel like every other jsonData key this plugin writes
// (storeHost, storePort, …), while env and the ini section spell it
// FORECAST_STORE_URL / store_url. jsonHasStore tests the same camel key, so an
// instance that declares only a URL is both detected as "has a store" and
// resolvable here.
func DSNFrom(l Lookup, securePassword string) string {
	if u := l.Get("FORECAST_STORE_URL", "STORE_URL", "store_url", "storeUrl"); u != "" {
		return u
	}
	host := l.Get("FORECAST_STORE_HOST", "STORE_HOST", "store_host", "storeHost")
	if host == "" {
		return ""
	}
	port := l.Get("FORECAST_STORE_PORT", "STORE_PORT", "store_port", "storePort")
	if port == "" {
		port = "5432"
	}
	db := l.Get("FORECAST_STORE_DATABASE", "STORE_DATABASE", "store_database", "storeDatabase")
	if db == "" {
		db = "overlay"
	}
	user := l.Get("FORECAST_STORE_USER", "STORE_USER", "store_user", "storeUser")
	if user == "" {
		user = "overlay"
	}
	ssl := l.Get("FORECAST_STORE_SSLMODE", "STORE_SSL_MODE", "store_ssl_mode", "storeSslMode")
	if ssl == "" {
		ssl = "disable"
	}
	pass := l.Get("FORECAST_STORE_PASSWORD", "STORE_PASSWORD", "store_password", "")
	if pass == "" {
		pass = strings.TrimSpace(securePassword)
	}
	u := &url.URL{
		Scheme: "postgres",
		Host:   host + ":" + port,
		Path:   "/" + db,
	}
	if user != "" {
		if pass != "" {
			u.User = url.UserPassword(user, pass)
		} else {
			u.User = url.User(user)
		}
	}
	q := u.Query()
	q.Set("sslmode", ssl)
	u.RawQuery = q.Encode()
	return u.String()
}

// EnvLookup is the migration CLI's Lookup: process env only, no ini file and no
// jsonData.
type EnvLookup struct{ Getenv func(string) string }

func (e EnvLookup) Get(forecastEnv, gfSuffix, iniKey, jsonKey string) string {
	if e.Getenv == nil {
		return ""
	}
	if v := strings.TrimSpace(e.Getenv(forecastEnv)); v != "" {
		return v
	}
	// iniKey and jsonKey have no env spelling, so the two GF_PLUGIN_* process
	// variables Grafana exports for this plugin's own sections are the last word.
	for _, prefix := range []string{PluginEnvPrefixApp, PluginEnvPrefixDatasource} {
		if v := strings.TrimSpace(e.Getenv(prefix + gfSuffix)); v != "" {
			return v
		}
	}
	return ""
}

// ScratchDSN returns dsn pointed at another database, for a test or a CI job that
// validates migrations against a throwaway database instead of the one the
// application uses. The DSN must be a URL, which is what FORECAST_STORE_URL and
// this plugin's own DSN builder produce; the rewrite is done on the URL text
// because pgx.ConnConfig.ConnString returns the string that was parsed rather than
// the current fields.
func ScratchDSN(dsn, database string) (string, error) {
	if _, err := pgx.ParseConfig(dsn); err != nil {
		return "", fmt.Errorf("forecast store: %w", err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("forecast store: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("forecast store: %q is not a URL DSN", dsn)
	}
	u.Path = "/" + database
	return u.String(), nil
}
