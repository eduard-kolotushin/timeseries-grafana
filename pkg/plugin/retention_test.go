package plugin

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	forecast "github.com/eduard-kolotushin/timeseries-forecast"
	"github.com/eduard-kolotushin/timeseries-grafana/pkg/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// scratchPostgresStore gives one retention test its own migrated, empty database.
// The sweep is deliberately global — it collects every stale row in the schema —
// so unlike the other pg-gated tests it cannot be pointed at the shared service
// database without collecting another test's rows (or the live environment's).
func scratchPostgresStore(t *testing.T, name string) *postgresStore {
	t.Helper()
	dsn := os.Getenv("FORECAST_TEST_PG")
	if dsn == "" {
		t.Skip("FORECAST_TEST_PG not set")
	}
	ctx := context.Background()
	adminDSN, err := store.ScratchDSN(dsn, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	testDSN, err := store.ScratchDSN(dsn, name)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
		admin.Close()
		t.Fatalf("drop %s: %v", name, err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatalf("create %s: %v", name, err)
	}
	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if _, err := store.Apply(ctx, pool, store.All(), false); err != nil {
		pool.Close()
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		defer admin.Close()
		if _, err := admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
	})
	return &postgresStore{pool: pool, now: time.Now}
}

const sweepTTL = 72 * time.Hour

// putSnapshot stores a snapshot the store can round-trip.
func putSnapshot(t *testing.T, ctx context.Context, s *postgresStore, orgID int64, key string) {
	t.Helper()
	snap := forecast.Snapshot{V: 1, Kind: "naive", Last: 1, Step: int64(time.Minute)}
	if err := s.Put(ctx, orgID, key, snap); err != nil {
		t.Fatal(err)
	}
}

// backdateSnapshot/backdateRow move a row's own clocks, which is how a test
// expresses "nothing refreshed this for d" without waiting for d to pass.
func backdateSnapshot(t *testing.T, ctx context.Context, s *postgresStore, orgID int64, key string, d time.Duration) {
	t.Helper()
	if _, err := s.pool.Exec(ctx, `
UPDATE forecast.snapshots SET updated_at = now() - $1::interval WHERE org_id = $2 AND cache_key = $3
`, intervalSeconds(d), orgID, key); err != nil {
		t.Fatal(err)
	}
}

func backdateRow(t *testing.T, ctx context.Context, s *postgresStore, orgID int64, scope, key string, d time.Duration) {
	t.Helper()
	if _, err := s.pool.Exec(ctx, `
UPDATE forecast.retrain SET next_run_at = now() - $1::interval, last_run_at = now() - $1::interval
WHERE scope = $2 AND org_id = $3 AND key = $4
`, intervalSeconds(d), scope, orgID, key); err != nil {
		t.Fatal(err)
	}
}

func sweep(t *testing.T, ctx context.Context, s *postgresStore) sweepResult {
	t.Helper()
	res, err := s.Sweep(ctx, sweepTTL, "0 3 * * *", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestSweepCollectsAStaleSnapshotAndItsRow: a model nothing refreshed for the whole
// window, with a row idle by both clocks and no snapshot behind it, is what a metric
// that stopped being published looks like — both go together.
func TestSweepCollectsAStaleSnapshotAndItsRow(t *testing.T) {
	ctx := context.Background()
	s := scratchPostgresStore(t, "forecast_retention_stale")
	key := strings.Repeat("a", 64)
	putSnapshot(t, ctx, s, 11, key)
	if err := s.Upsert(ctx, 11, ScheduleRow{
		Scope: scopePanel, Key: key, Cron: "0 3 * * *", Timezone: "UTC", Enabled: true,
		Spec: json.RawMessage(`{"queries":[{"refId":"A"}]}`), NextRunAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	backdateSnapshot(t, ctx, s, 11, key, 30*24*time.Hour)
	backdateRow(t, ctx, s, 11, scopePanel, key, 30*24*time.Hour)

	res := sweep(t, ctx, s)
	if res.Snapshots != 1 || res.Rows != 1 {
		t.Fatalf("sweep=%+v want one snapshot and one row", res)
	}
	if _, ok, err := s.Get(ctx, 11, key); err != nil || ok {
		t.Fatalf("a stale snapshot survived: ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.Row(ctx, 11, scopePanel, key); err != nil || ok {
		t.Fatalf("a stale row survived: ok=%v err=%v", ok, err)
	}
}

// TestSweepKeepsABrowserRefreshedRowAndItsCron: the panel's own load refreshes the
// snapshot without touching the row's clocks, so a row the scheduler never runs (or
// keeps failing) must survive on the strength of a fresh snapshot — and keep the
// cron an admin set.
func TestSweepKeepsABrowserRefreshedRowAndItsCron(t *testing.T) {
	ctx := context.Background()
	s := scratchPostgresStore(t, "forecast_retention_live")
	key := strings.Repeat("b", 64)
	putSnapshot(t, ctx, s, 12, key)
	if err := s.Upsert(ctx, 12, ScheduleRow{
		Scope: scopePanel, Key: key, Cron: "*/2 * * * *", Timezone: "Europe/Moscow", Enabled: false,
		Spec: json.RawMessage(`{"queries":[{"refId":"A"}]}`), NextRunAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	// The row has been idle for a month; only the snapshot is fresh.
	backdateRow(t, ctx, s, 12, scopePanel, key, 30*24*time.Hour)

	res := sweep(t, ctx, s)
	if res.Snapshots != 0 || res.Rows != 0 || res.Recreated != 0 {
		t.Fatalf("sweep collected a model that is still being refreshed: %+v", res)
	}
	row, ok, err := s.Row(ctx, 12, scopePanel, key)
	if err != nil || !ok {
		t.Fatalf("row: ok=%v err=%v", ok, err)
	}
	if row.Cron != "*/2 * * * *" || row.Timezone != "Europe/Moscow" || row.Enabled {
		t.Fatalf("the admin's row changed: %+v", row)
	}
	if _, ok, err := s.Get(ctx, 12, key); err != nil || !ok {
		t.Fatalf("a fresh snapshot was collected: ok=%v err=%v", ok, err)
	}
}

// TestSweepCollectsAnIdleBaselineRow: a baseline row's snapshot lives in the
// worker's schema, so the same statement reduces to the two clocks the worker
// itself updates — an idle one goes, a freshly retrained one stays.
func TestSweepCollectsAnIdleBaselineRow(t *testing.T) {
	ctx := context.Background()
	s := scratchPostgresStore(t, "forecast_retention_baseline")
	idle, live := strings.Repeat("c", 64), strings.Repeat("d", 64)
	for _, key := range []string{idle, live} {
		if err := s.Upsert(ctx, 0, ScheduleRow{
			Scope: scopeBaseline, Key: key, Cron: "0 3 * * *", Timezone: "UTC", Enabled: true,
			NextRunAt: time.Now().Add(-time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	backdateRow(t, ctx, s, 0, scopeBaseline, idle, 30*24*time.Hour)

	res := sweep(t, ctx, s)
	if res.Rows != 1 || res.Snapshots != 0 {
		t.Fatalf("sweep=%+v want exactly the idle baseline row", res)
	}
	if _, ok, err := s.Row(ctx, 0, scopeBaseline, idle); err != nil || ok {
		t.Fatalf("the idle baseline row survived: ok=%v err=%v", ok, err)
	}
	row, ok, err := s.Row(ctx, 0, scopeBaseline, live)
	if err != nil || !ok {
		t.Fatalf("a freshly retrained baseline row was collected: ok=%v err=%v", ok, err)
	}
	if row.Key != live {
		t.Fatalf("wrong row survived: %+v", row)
	}
}

// TestSweepRecreatesARowForAnOrphanSnapshot: a snapshot whose row an admin deleted
// (or an earlier release never wrote) gets the deployment default back, once, and
// with no spec — so the scheduler cannot claim a row it could not fetch training
// data for, and the panel's next load is what gives it one.
func TestSweepRecreatesARowForAnOrphanSnapshot(t *testing.T) {
	ctx := context.Background()
	s := scratchPostgresStore(t, "forecast_retention_orphan")
	orphan, custom := strings.Repeat("e", 64), strings.Repeat("f", 64)
	putSnapshot(t, ctx, s, 13, orphan)
	putSnapshot(t, ctx, s, 13, custom)
	if err := s.Upsert(ctx, 13, ScheduleRow{
		Scope: scopePanel, Key: custom, Cron: "*/2 * * * *", Timezone: "UTC", Enabled: true,
		Spec: json.RawMessage(`{"queries":[{"refId":"A"}]}`), NextRunAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	res := sweep(t, ctx, s)
	if res.Recreated != 1 {
		t.Fatalf("sweep=%+v want exactly one re-created row", res)
	}
	row, ok, err := s.Row(ctx, 13, scopePanel, orphan)
	if err != nil || !ok {
		t.Fatalf("orphan row: ok=%v err=%v", ok, err)
	}
	if row.Cron != "0 3 * * *" || row.Timezone != "UTC" || !row.Enabled {
		t.Fatalf("the re-created row did not get the deployment default: %+v", row)
	}
	if len(row.Spec) != 0 {
		t.Fatalf("the re-created row carries a spec it cannot fetch: %s", row.Spec)
	}
	// A row that already exists keeps the cron it has, and a second sweep is a
	// no-op rather than a rewrite on every tick.
	existing, ok, err := s.Row(ctx, 13, scopePanel, custom)
	if err != nil || !ok || existing.Cron != "*/2 * * * *" {
		t.Fatalf("an existing row was rewritten: ok=%v err=%v row=%+v", ok, err, existing)
	}
	if again := sweep(t, ctx, s); again != (sweepResult{}) {
		t.Fatalf("a second sweep collected something: %+v", again)
	}
}
