package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestParseName(t *testing.T) {
	tests := []struct {
		file    string
		version string
		name    string
		wantErr bool
	}{
		{file: "0001_snapshots.sql", version: "0001", name: "snapshots"},
		{file: "0002_retrain.sql", version: "0002", name: "retrain"},
		{file: "0010_a_b.sql", version: "0010", name: "a_b"},
		{file: "nope.sql", wantErr: true},
		{file: "0001.sql", wantErr: true},
		{file: "1_a.sql", wantErr: true},
		{file: "000a_a.sql", wantErr: true},
		{file: "0001_.sql", wantErr: true},
		{file: "0001_a.SQL", wantErr: true},
		{file: "0001_a", wantErr: true},
	}
	for _, tt := range tests {
		version, name, err := parseName(tt.file)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("%s: want an error, got %q/%q", tt.file, version, name)
			}
			continue
		}
		if err != nil || version != tt.version || name != tt.name {
			t.Fatalf("%s: got %q/%q err=%v want %q/%q", tt.file, version, name, err, tt.version, tt.name)
		}
	}
}

func TestAll(t *testing.T) {
	ms := All()
	if len(ms) < 2 {
		t.Fatalf("want at least the snapshots and retrain migrations, got %d", len(ms))
	}
	seen := map[string]bool{}
	for i, m := range ms {
		if m.Version == "" || m.Name == "" || strings.TrimSpace(m.SQL) == "" {
			t.Fatalf("migration %d is incomplete: %+v", i, m)
		}
		if seen[m.Version] {
			t.Fatalf("two migrations share version %s", m.Version)
		}
		seen[m.Version] = true
		if i > 0 && ms[i-1].Version >= m.Version {
			t.Fatalf("versions are not ascending: %s then %s", ms[i-1].Version, m.Version)
		}
	}
	for _, name := range []string{"snapshots", "retrain"} {
		if migration(t, name).SQL == "" {
			t.Fatalf("no %s migration", name)
		}
	}
}

func migration(t *testing.T, name string) Migration {
	t.Helper()
	for _, m := range All() {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("no %s migration in the embedded set", name)
	return Migration{}
}

// TestRetrainMigrationMarkers: the plugin's own test slices the legacy-key block
// out of this file by these two markers, so they are part of the file's contract.
// The uuid blocks after them are what the requirement is about.
func TestRetrainMigrationMarkers(t *testing.T) {
	sql := migration(t, "retrain").SQL
	start := strings.Index(sql, "-- legacy-key:")
	end := strings.Index(sql, "-- uuid-key:")
	if start < 0 || end < 0 {
		t.Fatal("0002_retrain.sql must mark its legacy-key and uuid-key blocks")
	}
	if start > end {
		t.Fatalf("the legacy-key block must come first: %d, %d", start, end)
	}
	legacy := sql[start:end]
	if !strings.Contains(legacy, "t.relname = 'retrain'") || !strings.Contains(legacy, "ADD PRIMARY KEY (scope, org_id, key)") {
		t.Fatalf("the legacy-key block does not widen the key in place: %s", legacy)
	}
	for _, want := range []string{"id UUID PRIMARY KEY DEFAULT gen_random_uuid()", "UNIQUE (scope, org_id, key)"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("0002_retrain.sql lacks %q", want)
		}
	}
	for _, want := range []string{"id UUID PRIMARY KEY DEFAULT gen_random_uuid()", "UNIQUE (org_id, cache_key)"} {
		if !strings.Contains(migration(t, "snapshots").SQL, want) {
			t.Fatalf("0001_snapshots.sql lacks %q", want)
		}
	}
}

// stubPool stands in for Postgres so the apply loop's decisions are pinned without
// a database: what a dry run writes, what a real run writes, and what happens when
// another migrator committed the version while this one waited for the lock.
type stubPool struct {
	// ledger is what the aggregate read answers: one version per line.
	ledger string
	// appliedInTx is what the per-version probe inside the transaction answers.
	appliedInTx bool
	statements  []string
}

func (p *stubPool) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	p.statements = append(p.statements, sql)
	return pgconn.NewCommandTag("SELECT 1"), nil
}

func (p *stubPool) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	p.statements = append(p.statements, sql)
	return stubRow{p: p}
}

func (p *stubPool) Begin(context.Context) (pgx.Tx, error) { return stubTx{p: p}, nil }

type stubTx struct {
	pgx.Tx
	p *stubPool
}

func (t stubTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.p.Exec(ctx, sql, args...)
}

func (t stubTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return t.p.QueryRow(ctx, sql, args...)
}

func (stubTx) Commit(context.Context) error   { return nil }
func (stubTx) Rollback(context.Context) error { return nil }

type stubRow struct{ p *stubPool }

func (r stubRow) Scan(dest ...any) error {
	switch d := dest[0].(type) {
	case *string:
		*d = r.p.ledger
		return nil
	case *int:
		if !r.p.appliedInTx {
			return pgx.ErrNoRows
		}
		*d = 1
		return nil
	}
	return errors.New("stubRow does not answer this statement")
}

// TestApplyDryRunReadsOnly: a dry run reports the pending versions and writes
// nothing at all — not the migrations, not the ledger, not even the schema.
func TestApplyDryRunReadsOnly(t *testing.T) {
	pool := &stubPool{}
	res, err := Apply(context.Background(), pool, All(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 0 {
		t.Fatalf("a dry run applied %v", res.Applied)
	}
	if len(res.Pending) != len(All()) {
		t.Fatalf("pending %v, want every version", res.Pending)
	}
	for _, sql := range pool.statements {
		if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(sql)), "SELECT") {
			t.Fatalf("a dry run wrote: %s", sql)
		}
	}
}

// TestApplyWritesLedgerAfterEachFile: one transaction per version, and the ledger
// row only after that version's SQL.
func TestApplyWritesLedgerAfterEachFile(t *testing.T) {
	pool := &stubPool{}
	res, err := Apply(context.Background(), pool, All(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != len(All()) {
		t.Fatalf("applied %v, want every version", res.Applied)
	}
	var order []string
	for _, sql := range pool.statements {
		switch {
		case sql == ledgerDDL:
			// The idempotent ledger bootstrap runs first in every transaction.
		case strings.HasPrefix(sql, "INSERT INTO forecast.schema_migrations"):
			order = append(order, "ledger-row")
		default:
			for _, m := range All() {
				if sql == m.SQL {
					order = append(order, m.Version)
				}
			}
		}
	}
	var want []string
	for _, m := range All() {
		want = append(want, m.Version, "ledger-row")
	}
	if !equalColumns(order, want) {
		t.Fatalf("statement order %v, want %v", order, want)
	}
}

// TestApplySkipsAVersionAnotherMigratorCommitted: the re-read inside the
// transaction is what makes concurrent migrators safe, so a version committed
// while this one waited for the lock must not be applied twice.
func TestApplySkipsAVersionAnotherMigratorCommitted(t *testing.T) {
	pool := &stubPool{appliedInTx: true}
	res, err := Apply(context.Background(), pool, All(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 0 {
		t.Fatalf("applied %v, want nothing", res.Applied)
	}
	for _, sql := range pool.statements {
		if strings.Contains(sql, "CREATE TABLE IF NOT EXISTS forecast.snapshots") {
			t.Fatal("a migration was applied although another migrator had committed it")
		}
	}
}

func TestMigrateFromScratch(t *testing.T) {
	ctx := context.Background()
	pool := scratchDatabase(t, "migrate_from_scratch")

	res, err := Apply(ctx, pool, All(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != len(All()) || len(res.Pending) != 0 || len(res.Unknown) != 0 {
		t.Fatalf("first run: applied %v pending %v unknown %v", res.Applied, res.Pending, res.Unknown)
	}
	again, err := Apply(ctx, pool, All(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Applied) != 0 || len(again.Pending) != 0 {
		t.Fatalf("a second run applied %v pending %v, want nothing", again.Applied, again.Pending)
	}

	var versions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM forecast.schema_migrations`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != len(All()) {
		t.Fatalf("ledger has %d rows, want %d", versions, len(All()))
	}
	assertPrimaryKey(t, ctx, pool, "forecast.schema_migrations", "id")
	assertPrimaryKey(t, ctx, pool, "forecast.snapshots", "id")
	assertUnique(t, ctx, pool, "forecast.snapshots", "org_id", "cache_key")
	assertPrimaryKey(t, ctx, pool, "forecast.retrain", "id")
	assertUnique(t, ctx, pool, "forecast.retrain", "scope", "org_id", "key")
}

// TestMigrateAdoptsLegacySchema starts from the shape the previous release
// created: the natural key as the primary key, no uuid column, no superseded_at.
// Everything the plugin and the worker do to those tables has to keep working.
func TestMigrateAdoptsLegacySchema(t *testing.T) {
	ctx := context.Background()
	pool := scratchDatabase(t, "migrate_legacy")
	if _, err := pool.Exec(ctx, `
CREATE SCHEMA forecast;
CREATE TABLE forecast.snapshots (
  org_id BIGINT NOT NULL,
  cache_key CHAR(64) NOT NULL,
  snapshot JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, cache_key)
);
CREATE TABLE forecast.retrain (
  scope TEXT NOT NULL,
  key TEXT NOT NULL,
  org_id BIGINT NOT NULL DEFAULT 0,
  cron TEXT NOT NULL,
  timezone TEXT NOT NULL DEFAULT 'UTC',
  enabled BOOLEAN NOT NULL DEFAULT true,
  spec JSONB,
  next_run_at TIMESTAMPTZ,
  last_run_at TIMESTAMPTZ,
  last_status TEXT,
  claimed_by TEXT,
  claimed_until TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (scope, key)
);
INSERT INTO forecast.snapshots (org_id, cache_key, snapshot) VALUES (7, repeat('a', 64), '{"last":4}');
INSERT INTO forecast.retrain (scope, key, org_id, cron) VALUES ('panel', 'keep-me', 7, '*/5 * * * *');
`); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, pool, All(), false); err != nil {
		t.Fatal(err)
	}

	var snapID, retrainID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM forecast.snapshots`).Scan(&snapID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM forecast.retrain`).Scan(&retrainID); err != nil {
		t.Fatal(err)
	}
	if snapID == "" || retrainID == "" || snapID == retrainID {
		t.Fatalf("backfilled ids: snapshots %q retrain %q", snapID, retrainID)
	}
	var org int64
	var cron string
	if err := pool.QueryRow(ctx, `SELECT org_id, cron FROM forecast.retrain WHERE key = 'keep-me'`).Scan(&org, &cron); err != nil {
		t.Fatal(err)
	}
	if org != 7 || cron != "*/5 * * * *" {
		t.Fatalf("the row changed: org=%d cron=%s", org, cron)
	}

	assertPrimaryKey(t, ctx, pool, "forecast.retrain", "id")
	assertUnique(t, ctx, pool, "forecast.retrain", "scope", "org_id", "key")
	assertPrimaryKey(t, ctx, pool, "forecast.snapshots", "id")
	assertUnique(t, ctx, pool, "forecast.snapshots", "org_id", "cache_key")
	assertColumn(t, ctx, pool, "forecast.retrain", "superseded_at")

	// The conflict targets every caller uses still resolve, and the natural key is
	// still unique (so ON CONFLICT has something to lock).
	if _, err := pool.Exec(ctx, `
INSERT INTO forecast.retrain (scope, key, org_id, cron) VALUES ('panel', 'keep-me', 7, '*/2 * * * *')
ON CONFLICT (scope, org_id, key) DO UPDATE SET cron = EXCLUDED.cron`); err != nil {
		t.Fatalf("the retrain upsert no longer resolves: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO forecast.snapshots (org_id, cache_key, snapshot) VALUES (7, repeat('a', 64), '{}')
ON CONFLICT (org_id, cache_key) DO UPDATE SET snapshot = EXCLUDED.snapshot`); err != nil {
		t.Fatalf("the snapshot upsert no longer resolves: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO forecast.retrain (scope, key, org_id, cron) VALUES ('panel', 'keep-me', 7, 'x')`); err == nil {
		t.Fatal("the natural key is not unique any more")
	}
	// The worker's insert names its columns and never mentions id.
	if _, err := pool.Exec(ctx, `
INSERT INTO forecast.retrain (scope, key, cron, timezone, enabled, next_run_at)
VALUES ('baseline', 'a-hash', '* * * * *', 'UTC', true, now())`); err != nil {
		t.Fatalf("an insert without id no longer works: %v", err)
	}
}

// TestMigrateAdoptsAPreOrgRetrainTable: a table old enough to be keyed (scope, key)
// may predate the org_id column as well. The migration has to add it, not abort with
// "column org_id of relation retrain does not exist" — that error would fail the
// whole file, so the migrator would exit non-zero in a pipeline and the plugin's
// schedule path would report a missing per-org key forever.
func TestMigrateAdoptsAPreOrgRetrainTable(t *testing.T) {
	ctx := context.Background()
	pool := scratchDatabase(t, "migrate_pre_org")
	if _, err := pool.Exec(ctx, `
CREATE SCHEMA forecast;
CREATE TABLE forecast.retrain (
  scope TEXT NOT NULL,
  key TEXT NOT NULL,
  cron TEXT NOT NULL,
  timezone TEXT NOT NULL DEFAULT 'UTC',
  enabled BOOLEAN NOT NULL DEFAULT true,
  spec JSONB,
  next_run_at TIMESTAMPTZ,
  last_run_at TIMESTAMPTZ,
  last_status TEXT,
  claimed_by TEXT,
  claimed_until TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (scope, key)
);
INSERT INTO forecast.retrain (scope, key, cron) VALUES ('panel', 'keep-me', '*/5 * * * *');
`); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, pool, All(), false); err != nil {
		t.Fatalf("a table without org_id must migrate, not abort: %v", err)
	}
	var org int64
	var cron string
	if err := pool.QueryRow(ctx, `SELECT org_id, cron FROM forecast.retrain WHERE key = 'keep-me'`).Scan(&org, &cron); err != nil {
		t.Fatal(err)
	}
	if org != 0 || cron != "*/5 * * * *" {
		t.Fatalf("the row changed: org=%d cron=%s", org, cron)
	}
	assertPrimaryKey(t, ctx, pool, "forecast.retrain", "id")
	assertUnique(t, ctx, pool, "forecast.retrain", "scope", "org_id", "key")
	assertColumn(t, ctx, pool, "forecast.retrain", "superseded_at")
}

func TestMigrateConcurrent(t *testing.T) {
	ctx := context.Background()
	pool := scratchDatabase(t, "migrate_concurrent")
	second, err := pgxpool.New(ctx, pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)

	var wg sync.WaitGroup
	results := make([]Result, 2)
	errs := make([]error, 2)
	for i, p := range []*pgxpool.Pool{pool, second} {
		wg.Add(1)
		go func(i int, p *pgxpool.Pool) {
			defer wg.Done()
			results[i], errs[i] = Apply(ctx, p, All(), false)
		}(i, p)
	}
	wg.Wait()
	applied := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("migrator %d: %v", i, err)
		}
		applied += len(results[i].Applied)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM forecast.schema_migrations`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != len(All()) {
		t.Fatalf("ledger has %d rows, want %d", rows, len(All()))
	}
	if applied != len(All()) {
		t.Fatalf("the two migrators applied %d versions in total, want %d", applied, len(All()))
	}
}

func TestMigrateUnknownVersion(t *testing.T) {
	ctx := context.Background()
	pool := scratchDatabase(t, "migrate_unknown")
	if _, err := Apply(ctx, pool, All(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO forecast.schema_migrations (version, name) VALUES ('9999', 'future')`); err != nil {
		t.Fatal(err)
	}
	res, err := Apply(ctx, pool, All(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Unknown) != 1 || res.Unknown[0] != "9999" {
		t.Fatalf("unknown %v, want [9999]", res.Unknown)
	}
	if len(res.Applied) != 0 {
		t.Fatalf("applied %v, want nothing", res.Applied)
	}
}

// TestApplyRollsBackAFailingMigration: one transaction per file means a failure
// leaves neither the DDL nor a ledger row behind.
func TestApplyRollsBackAFailingMigration(t *testing.T) {
	ctx := context.Background()
	pool := scratchDatabase(t, "migrate_failing")
	ms := []Migration{
		{Version: "0001", Name: "ok", SQL: `CREATE TABLE forecast.rollback_probe (id UUID PRIMARY KEY DEFAULT gen_random_uuid())`},
		{Version: "0002", Name: "boom", SQL: `CREATE TABLE forecast.rollback_probe_2 (id UUID PRIMARY KEY); SELECT 1/0`},
	}
	_, err := Apply(ctx, pool, ms, false)
	if err == nil {
		t.Fatal("want the failing migration to fail the run")
	}
	if !strings.Contains(err.Error(), "0002_boom") {
		t.Fatalf("the error does not name the file: %v", err)
	}
	var versions string
	if err := pool.QueryRow(ctx, `SELECT coalesce(string_agg(version, ','), '') FROM forecast.schema_migrations`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != "0001" {
		t.Fatalf("ledger %q, want 0001 only", versions)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('forecast.rollback_probe_2') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("the failing migration's DDL was committed")
	}
}

// scratchDatabase drops and recreates a database for one test and returns a pool
// on it, so the migration tests cannot race pkg/plugin's pg-gated tests on the
// shared service database.
func scratchDatabase(t *testing.T, name string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("FORECAST_TEST_PG")
	if dsn == "" {
		t.Skip("FORECAST_TEST_PG not set")
	}
	ctx := context.Background()
	adminDSN, err := ScratchDSN(dsn, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	testDSN, err := ScratchDSN(dsn, name)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
		t.Fatalf("drop %s: %v", name, err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if admin, err := pgxpool.New(context.Background(), adminDSN); err == nil {
			defer admin.Close()
			if _, err := admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
				t.Errorf("drop %s: %v", name, err)
			}
		}
	})
	return pool
}

// constraintColumns returns the column names of every constraint of one type on a
// table, keyed by constraint name.
func constraintColumns(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table, contype string) map[string][]string {
	t.Helper()
	rows, err := pool.Query(ctx, `
SELECT c.conname, array_agg(a.attname ORDER BY x.ord)
FROM pg_constraint c
JOIN pg_class t ON t.oid = c.conrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
JOIN unnest(c.conkey) WITH ORDINALITY AS x(attnum, ord) ON true
JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = x.attnum
WHERE n.nspname = split_part($1, '.', 1) AND t.relname = split_part($1, '.', 2) AND c.contype::text = $2
GROUP BY c.conname`, table, contype)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var name string
		var columns []string
		if err := rows.Scan(&name, &columns); err != nil {
			t.Fatal(err)
		}
		out[name] = columns
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func assertPrimaryKey(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, columns ...string) {
	t.Helper()
	got := constraintColumns(t, ctx, pool, table, "p")
	if len(got) != 1 {
		t.Fatalf("%s: want exactly one primary key, got %v", table, got)
	}
	for _, cols := range got {
		if !equalColumns(cols, columns) {
			t.Fatalf("%s: primary key is %v, want %v", table, cols, columns)
		}
	}
}

func assertUnique(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, columns ...string) {
	t.Helper()
	got := constraintColumns(t, ctx, pool, table, "u")
	for _, cols := range got {
		if equalColumns(cols, columns) {
			return
		}
	}
	t.Fatalf("%s: no UNIQUE (%s); unique constraints: %v", table, strings.Join(columns, ", "), got)
}

func assertColumn(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table, column string) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM information_schema.columns
  WHERE table_schema = split_part($1, '.', 1) AND table_name = split_part($1, '.', 2) AND column_name = $2
)`, table, column).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatalf("%s has no %s column", table, column)
	}
}

func equalColumns(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
