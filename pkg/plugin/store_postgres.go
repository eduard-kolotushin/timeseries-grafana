package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	forecast "github.com/eduard-kolotushin/timeseries-forecast"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const ensureSQL = `
CREATE SCHEMA IF NOT EXISTS forecast;
CREATE TABLE IF NOT EXISTS forecast.snapshots (
  org_id BIGINT NOT NULL,
  cache_key CHAR(64) NOT NULL,
  snapshot JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, cache_key)
);
`

type postgresStore struct {
	pool *pgxpool.Pool
}

func (s *postgresStore) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *postgresStore) Get(ctx context.Context, orgID int64, key string) (forecast.Snapshot, bool, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT snapshot FROM forecast.snapshots WHERE org_id = $1 AND cache_key = $2`, orgID, key).Scan(&raw)
	if err == pgx.ErrNoRows {
		return forecast.Snapshot{}, false, nil
	}
	if err != nil {
		return forecast.Snapshot{}, false, err
	}
	var snap forecast.Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return forecast.Snapshot{}, false, err
	}
	return snap, true, nil
}

func (s *postgresStore) Put(ctx context.Context, orgID int64, key string, snap forecast.Snapshot) error {
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO forecast.snapshots (org_id, cache_key, snapshot, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (org_id, cache_key) DO UPDATE SET snapshot = EXCLUDED.snapshot, updated_at = now()
`, orgID, key, raw)
	return err
}

func openPostgresStore(ctx context.Context, dsn string) (*postgresStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if _, err := pool.Exec(ctx, ensureSQL); err != nil {
		pool.Close()
		return nil, err
	}
	return &postgresStore{pool: pool}, nil
}

const (
	gfPluginPrefix   = "GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_"
	gfPluginDSPrefix = "GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_DATASOURCE_"
)

func storeDSN(ctx context.Context, settings backend.AppInstanceSettings) string {
	return storeDSNFrom(ctx, settings.JSONData, settings.DecryptedSecureJSONData)
}

func jsonHasStore(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	jd := map[string]any{}
	if err := json.Unmarshal(raw, &jd); err != nil {
		return false
	}
	return jsonField(jd, "storeHost") != "" || jsonField(jd, "storeUrl") != ""
}

// storeJSONFromContext prefers datasource jsonData when it has a host/URL, else the parent app settings.
// Grafana QueryData for a datasource does not currently attach AppInstanceSettings; this is a fallback.
func storeJSONFromContext(dsJSON []byte, dsSecure map[string]string, app *backend.AppInstanceSettings) ([]byte, map[string]string) {
	if jsonHasStore(dsJSON) {
		return dsJSON, dsSecure
	}
	if app != nil && jsonHasStore(app.JSONData) {
		secure := dsSecure
		if app.DecryptedSecureJSONData != nil {
			secure = app.DecryptedSecureJSONData
		}
		return app.JSONData, secure
	}
	return dsJSON, dsSecure
}

func storeDSNFrom(ctx context.Context, jsonData []byte, secure map[string]string) string {
	jd := map[string]any{}
	if len(jsonData) > 0 {
		_ = json.Unmarshal(jsonData, &jd)
	}
	look := storeLookup{
		getenv: os.Getenv,
		cfg:    backend.GrafanaConfigFromContext(ctx),
		json:   jd,
	}
	if u := look.get("FORECAST_STORE_URL", "STORE_URL", "store_url", ""); u != "" {
		return u
	}
	host := look.get("FORECAST_STORE_HOST", "STORE_HOST", "store_host", "storeHost")
	if host == "" {
		return ""
	}
	port := look.get("FORECAST_STORE_PORT", "STORE_PORT", "store_port", "storePort")
	if port == "" {
		port = "5432"
	}
	db := look.get("FORECAST_STORE_DATABASE", "STORE_DATABASE", "store_database", "storeDatabase")
	if db == "" {
		db = "overlay"
	}
	user := look.get("FORECAST_STORE_USER", "STORE_USER", "store_user", "storeUser")
	if user == "" {
		user = "overlay"
	}
	ssl := look.get("FORECAST_STORE_SSLMODE", "STORE_SSL_MODE", "store_ssl_mode", "storeSslMode")
	if ssl == "" {
		ssl = "disable"
	}
	pass := look.get("FORECAST_STORE_PASSWORD", "STORE_PASSWORD", "store_password", "")
	if pass == "" && secure != nil {
		pass = strings.TrimSpace(secure["storePassword"])
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

type storeLookup struct {
	getenv func(string) string
	cfg    *backend.GrafanaCfg
	json   map[string]any
}

func (s storeLookup) get(forecastEnv, gfSuffix, iniKey, jsonKey string) string {
	if v := strings.TrimSpace(s.getenv(forecastEnv)); v != "" {
		return v
	}
	for _, prefix := range []string{gfPluginPrefix, gfPluginDSPrefix} {
		if v := strings.TrimSpace(s.getenv(prefix + gfSuffix)); v != "" {
			return v
		}
	}
	if s.cfg != nil {
		keys := []string{
			iniKey,
			gfPluginPrefix + gfSuffix,
			gfPluginDSPrefix + gfSuffix,
			"plugin.eduardkolotushin-forecast-app." + iniKey,
			"plugin.eduardkolotushin-forecast-datasource." + iniKey,
		}
		for _, k := range keys {
			if v := strings.TrimSpace(s.cfg.Get(k)); v != "" {
				return v
			}
		}
	}
	if jsonKey == "" {
		return ""
	}
	return jsonField(s.json, jsonKey)
}

func jsonField(jd map[string]any, key string) string {
	raw, ok := jd[key]
	if !ok || raw == nil {
		return ""
	}
	switch t := raw.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatInt(int64(t), 10)
	case json.Number:
		return t.String()
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}
