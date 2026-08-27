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

func storeDSN(settings backend.AppInstanceSettings) string {
	if u := strings.TrimSpace(os.Getenv("FORECAST_STORE_URL")); u != "" {
		return u
	}
	jd := map[string]any{}
	if len(settings.JSONData) > 0 {
		_ = json.Unmarshal(settings.JSONData, &jd)
	}
	host := envOrJSON("FORECAST_STORE_HOST", jd, "storeHost")
	if host == "" {
		return ""
	}
	port := envOrJSON("FORECAST_STORE_PORT", jd, "storePort")
	if port == "" {
		port = "5432"
	}
	db := envOrJSON("FORECAST_STORE_DATABASE", jd, "storeDatabase")
	if db == "" {
		db = "overlay"
	}
	user := envOrJSON("FORECAST_STORE_USER", jd, "storeUser")
	if user == "" {
		user = "overlay"
	}
	ssl := envOrJSON("FORECAST_STORE_SSLMODE", jd, "storeSslMode")
	if ssl == "" {
		ssl = "disable"
	}
	pass := strings.TrimSpace(os.Getenv("FORECAST_STORE_PASSWORD"))
	if pass == "" && settings.DecryptedSecureJSONData != nil {
		pass = settings.DecryptedSecureJSONData["storePassword"]
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

func envOrJSON(env string, jd map[string]any, key string) string {
	if v := strings.TrimSpace(os.Getenv(env)); v != "" {
		return v
	}
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
