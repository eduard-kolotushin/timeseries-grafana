package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	forecast "github.com/eduard-kolotushin/timeseries-forecast"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ensureSQL is the only creator of both tables. The plugin owns forecast.retrain
// outright: the baselines worker inserts, claims and finishes its own rows there
// but never provisions them, so a deployment that runs the worker without ever
// loading the plugin would not get the table.
//
// forecast.retrain carries no secondary index on purpose: it holds one row per
// trained panel (plus one per worker-owned baseline hash), every plugin read is
// the (scope, org_id, key) primary key, and the claim scan orders a table that
// stays in the hundreds of rows — an index would only add write cost to each
// retrain.
//
// org_id is part of the key because a panel's cache key is org-independent: the
// same dashboard and datasource uids hash the same in two orgs, so a (scope, key)
// key would put two orgs on one row, where either org's fit would rewrite the
// other's cron and stored query objects. See scheduleKeySQL.
const ensureSQL = `
CREATE SCHEMA IF NOT EXISTS forecast;
CREATE TABLE IF NOT EXISTS forecast.snapshots (
  org_id BIGINT NOT NULL,
  cache_key CHAR(64) NOT NULL,
  snapshot JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, cache_key)
);
CREATE TABLE IF NOT EXISTS forecast.retrain (
  scope TEXT NOT NULL,                -- 'panel' | 'baseline'
  key TEXT NOT NULL,                  -- panel: cache_key, baseline: metric_hash
  org_id BIGINT NOT NULL DEFAULT 0,
  cron TEXT NOT NULL,
  timezone TEXT NOT NULL DEFAULT 'UTC',
  enabled BOOLEAN NOT NULL DEFAULT true,
  spec JSONB,                         -- panel: opaque datasource query objects; baseline: model spec
  next_run_at TIMESTAMPTZ,
  last_run_at TIMESTAMPTZ,
  last_status TEXT,
  claimed_by TEXT,
  claimed_until TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (scope, org_id, key)
);
DO $$
DECLARE pk_name TEXT; pk_cols TEXT;
BEGIN
  SELECT c.conname, string_agg(a.attname, ',' ORDER BY a.attnum)
    INTO pk_name, pk_cols
  FROM pg_constraint c
  JOIN pg_class t ON t.oid = c.conrelid
  JOIN pg_namespace n ON n.oid = t.relnamespace
  JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY (c.conkey)
  WHERE n.nspname = 'forecast' AND t.relname = 'retrain' AND c.contype = 'p'
  GROUP BY c.conname;
  IF pk_cols = 'scope,key' THEN
    -- A table created before org_id joined the key. Existing rows keep their
    -- org: a panel row was written with the org that fitted it.
    EXECUTE format('ALTER TABLE forecast.retrain DROP CONSTRAINT %I', pk_name);
    ALTER TABLE forecast.retrain ADD PRIMARY KEY (scope, org_id, key);
  END IF;
END $$;
`

// scheduleKeySQL reports whether forecast.retrain's primary key is the per-org
// shape ensureSQL migrates to, so a runtime user that may not ALTER an upgraded
// table fails the schedule store loudly instead of sharing a row between orgs.
const scheduleKeySQL = `
SELECT EXISTS (
  SELECT 1
  FROM pg_constraint c
  JOIN pg_class t ON t.oid = c.conrelid
  JOIN pg_namespace n ON n.oid = t.relnamespace
  WHERE n.nspname = 'forecast' AND t.relname = 'retrain' AND c.contype = 'p'
    AND array_length(c.conkey, 1) = 3
    AND c.conkey @> ARRAY[(SELECT a.attnum FROM pg_attribute a WHERE a.attrelid = t.oid AND a.attname = 'org_id')]
)`

// errScheduleKey is the schedule table's upgrade error: the DDL ran, but the
// table still keys on (scope, key), so the process cannot store per-org rows.
var errScheduleKey = errors.New("forecast store: forecast.retrain has no per-org primary key; it needs PRIMARY KEY (scope, org_id, key), which the table owner must apply")

// ensureRetryAfter throttles reconnect attempts while Postgres is unreachable so
// a burst of overlay loads does not turn into a burst of failed dials.
const ensureRetryAfter = 5 * time.Second

// pgxPool is the slice of *pgxpool.Pool this store uses. It is an interface so
// the per-table readiness split below can be exercised without a live Postgres;
// *pgxpool.Pool is its only production implementation.
type pgxPool interface {
	Ping(ctx context.Context) error
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Close()
}

// storeProbe is one table's readiness and retry state. Every table has its own:
// ensureSQL provisions both, so a runtime user that may not CREATE can be missing
// forecast.retrain while forecast.snapshots is fine — and that schedule failure
// must not change the answer a snapshot call gets.
type storeProbe struct {
	ready   bool
	err     error
	attempt time.Time
}

type postgresStore struct {
	pool pgxPool

	mu    sync.Mutex
	snap  storeProbe
	sched storeProbe
	now   func() time.Time
}

func (s *postgresStore) Close() {
	if s == nil || s.pool == nil {
		return
	}
	s.pool.Close()
}

// ensure pings Postgres and creates the schema on first successful use. It is
// retried on later calls (after ensureRetryAfter) instead of failing the store
// permanently when the database is unavailable at plugin start.
func (s *postgresStore) ensure(ctx context.Context) error {
	return s.ensureTable(ctx, &s.snap, `SELECT 1 FROM forecast.snapshots LIMIT 1`, "")
}

// ensureSchedules is the same contract for the schedule table. Readiness is tracked per
// table: a deployment that cannot CREATE and upgrades with forecast.retrain missing must
// keep serving snapshots (and keep retrying the DDL) instead of latching ready and
// answering every schedule call with a permanent relation-does-not-exist. Unlike the
// snapshot table it also requires the per-org key shape, because a schedule row shared
// between two orgs is a correctness bug rather than a missing feature.
func (s *postgresStore) ensureSchedules(ctx context.Context) error {
	return s.ensureTable(ctx, &s.sched, `SELECT 1 FROM forecast.retrain LIMIT 1`, scheduleKeySQL)
}

// ensureTable provisions one table and records its readiness. shapeSQL, when set,
// must answer true or this store stays unusable for that table: ensureSQL may have
// been refused by a runtime user without ALTER, and an upgraded table with the old
// key is worse than no schedules at all.
func (s *postgresStore) ensureTable(ctx context.Context, probe *storeProbe, readySQL, shapeSQL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if probe.ready {
		return nil
	}
	now := s.now()
	if probe.err != nil && now.Sub(probe.attempt) < ensureRetryAfter {
		return probe.err
	}
	probe.attempt = now
	if err := s.pool.Ping(ctx); err != nil {
		probe.err = fmt.Errorf("forecast store: %w", err)
		return probe.err
	}
	if _, err := s.pool.Exec(ctx, ensureSQL); err != nil {
		// A locked-down runtime user may lack CREATE. Accept that when the table
		// this call needs is already provisioned.
		if _, probeErr := s.pool.Exec(ctx, readySQL); probeErr != nil {
			probe.err = fmt.Errorf("forecast store: %w", err)
			return probe.err
		}
	}
	if shapeSQL != "" {
		var ok bool
		if err := s.pool.QueryRow(ctx, shapeSQL).Scan(&ok); err != nil {
			probe.err = fmt.Errorf("forecast store: %w", err)
			return probe.err
		}
		if !ok {
			probe.err = errScheduleKey
			return probe.err
		}
	}
	probe.ready = true
	probe.err = nil
	return nil
}

// Row reads one schedule row by its full key. A panel fit reads its row this way
// rather than through List, which would transfer and decode every worker baseline
// row to find one of them.
func (s *postgresStore) Row(ctx context.Context, orgID int64, scope, key string) (ScheduleRow, bool, error) {
	if err := s.ensureSchedules(ctx); err != nil {
		return ScheduleRow{}, false, err
	}
	// Baseline rows are the worker's fleet-wide rows: they are stored at org 0
	// whatever org asks, which is what keeps them visible (and editable) everywhere.
	if scope == scopeBaseline {
		orgID = 0
	}
	row := ScheduleRow{}
	var next, last *time.Time
	var status *string
	err := s.pool.QueryRow(ctx, `
SELECT scope, key, org_id, cron, timezone, enabled, spec, next_run_at, last_run_at, last_status
FROM forecast.retrain WHERE scope = $1 AND org_id = $2 AND key = $3
`, scope, orgID, key).Scan(&row.Scope, &row.Key, &row.OrgID, &row.Cron, &row.Timezone, &row.Enabled, &row.Spec, &next, &last, &status)
	if err == pgx.ErrNoRows {
		return ScheduleRow{}, false, nil
	}
	if err != nil {
		return ScheduleRow{}, false, err
	}
	if next != nil {
		row.NextRunAt = next.UTC()
	}
	if last != nil {
		row.LastRunAt = last.UTC()
	}
	if status != nil {
		row.LastStatus = *status
	}
	return row, true, nil
}

func (s *postgresStore) Get(ctx context.Context, orgID int64, key string) (forecast.Snapshot, bool, error) {
	if err := s.ensure(ctx); err != nil {
		return forecast.Snapshot{}, false, err
	}
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
	if err := s.ensure(ctx); err != nil {
		return err
	}
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

// openPostgresStore parses the DSN and builds the pool. pgxpool connects
// lazily; the first Get/Put pings and provisions the schema (see ensure), so a
// database that is down at plugin start does not disable the store for the
// life of the process.
func openPostgresStore(ctx context.Context, dsn string) (*postgresStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &postgresStore{pool: pool, now: time.Now}, nil
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
