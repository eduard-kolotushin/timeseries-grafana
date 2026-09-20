package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	forecast "github.com/eduard-kolotushin/timeseries-forecast"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestPostgresStoreLazyConnect: an unreachable database must not turn the store
// into a permanent error; Get/Put fail per call and retry after the backoff.
func TestPostgresStoreLazyConnect(t *testing.T) {
	ctx := context.Background()
	s, err := openPostgresStore(ctx, "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("open must only parse the DSN: %v", err)
	}
	t.Cleanup(s.Close)
	clock := time.Unix(1_000_000, 0)
	s.now = func() time.Time { return clock }

	_, _, err = s.Get(ctx, 1, "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	if err == nil {
		t.Fatal("expected connect error")
	}
	first := s.snap.attempt
	// Within the backoff window the cached error is returned without redialing.
	if _, _, err2 := s.Get(ctx, 1, "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"); err2 == nil || s.snap.attempt != first {
		t.Fatalf("expected throttled retry, err=%v attempt moved=%v", err2, s.snap.attempt != first)
	}
	clock = clock.Add(ensureRetryAfter)
	if _, _, err3 := s.Get(ctx, 1, "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"); err3 == nil || s.snap.attempt == first {
		t.Fatalf("expected a fresh attempt after backoff, err=%v", err3)
	}
	if s.snap.ready {
		t.Fatal("store must not be marked ready")
	}
}

// fakePool answers the statements ensureTable issues, without a Postgres.
type fakePool struct {
	pingErr error
	execErr func(sql string) error
	rowErr  error
	// shape is what the schedule primary-key probe answers.
	shape      bool
	statements []string
}

func (p *fakePool) Ping(context.Context) error { return p.pingErr }

func (p *fakePool) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	p.statements = append(p.statements, sql)
	if p.execErr != nil {
		if err := p.execErr(sql); err != nil {
			return pgconn.CommandTag{}, err
		}
	}
	return pgconn.NewCommandTag("CREATE TABLE"), nil
}

func (p *fakePool) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("fakePool has no rows")
}

func (p *fakePool) QueryRow(context.Context, string, ...any) pgx.Row {
	return fakeRow{err: p.rowErr, shape: p.shape}
}

func (p *fakePool) Close() {}

type fakeRow struct {
	err   error
	shape bool
}

// Scan answers the two QueryRow calls a store makes: the schedule primary-key probe
// (a bool) and a snapshot read (which has no row here).
func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) == 1 {
		switch d := dest[0].(type) {
		case *bool:
			*d = r.shape
			return nil
		case *[]byte:
			return pgx.ErrNoRows
		}
	}
	return errors.New("fakeRow does not answer this statement")
}

// TestScheduleDDLFailureLeavesSnapshotsServing: ensureSQL provisions both
// tables, so a runtime user that may not CREATE can be missing forecast.retrain
// while forecast.snapshots is fine. A schedule failure must not change what a
// snapshot call answers — sharing one cached error made every overlay probe 500
// until the retry window expired.
func TestScheduleDDLFailureLeavesSnapshotsServing(t *testing.T) {
	ctx := context.Background()
	db := &fakePool{
		execErr: func(sql string) error {
			// A runtime user without CREATE: the DDL is refused, the reads are not.
			if strings.HasPrefix(strings.TrimSpace(sql), "CREATE") {
				return errors.New("ERROR: permission denied for schema forecast (SQLSTATE 42501)")
			}
			return nil
		},
	}
	clock := time.Unix(1_000_000, 0)
	s := &postgresStore{pool: db, now: func() time.Time { return clock }}

	// The table exists and already carries the per-org key: a locked-down runtime user
	// that may not CREATE is fine, which is the upgrade path of a deployed plugin.
	db.shape = true
	if err := s.ensureSchedules(ctx); err != nil {
		t.Fatalf("a readable, migrated table must be usable without CREATE: %v", err)
	}
	db.shape = false
	s.sched = storeProbe{}
	if err := s.ensureSchedules(ctx); err == nil {
		t.Fatal("a table without the per-org key must fail loudly, not share rows between orgs")
	}
	if _, ok, err := s.Get(ctx, 1, "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"); err != nil || ok {
		t.Fatalf("a schedule DDL failure changed the snapshot path: ok=%v err=%v", ok, err)
	}

	// The per-table retry state still throttles the failing DDL, and retries it
	// after the window instead of latching the table as ready.
	attempts := len(db.statements)
	if err := s.ensureSchedules(ctx); err == nil {
		t.Fatal("the schedule path must keep reporting the failure")
	}
	if len(db.statements) != attempts {
		t.Fatalf("the failing DDL was retried inside the backoff window (%d statements)", len(db.statements))
	}
	clock = clock.Add(ensureRetryAfter)
	if err := s.ensureSchedules(ctx); err == nil {
		t.Fatal("the schedule path must not latch ready while the key is wrong")
	}
	if len(db.statements) == attempts {
		t.Fatal("the DDL was never retried after the backoff")
	}
}

func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("FORECAST_TEST_PG")
	if dsn == "" {
		t.Skip("FORECAST_TEST_PG not set")
	}
	ctx := context.Background()
	s, err := openPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	snap := forecast.Snapshot{V: 1, Kind: "naive", Last: 3000, Step: 1_000_000_000, Data: []byte(`{"last":4,"sigma":1}`)}
	key := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if err := s.Put(ctx, 1, key, snap); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get(ctx, 1, key)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Kind != "naive" || got.Last != 3000 {
		t.Fatalf("round-trip %+v", got)
	}
	_, ok, err = s.Get(ctx, 2, key)
	if err != nil || ok {
		t.Fatalf("org miss: ok=%v err=%v", ok, err)
	}
	snap.Last = 4000
	if err := s.Put(ctx, 1, key, snap); err != nil {
		t.Fatal(err)
	}
	got, ok, err = s.Get(ctx, 1, key)
	if err != nil || !ok || got.Last != 4000 {
		t.Fatalf("upsert %+v ok=%v err=%v", got, ok, err)
	}
}

// panelRows keeps only the org-scoped rows of a List result; baseline rows are
// fleet-wide and written by the worker, so a live test cannot own them.
func panelRows(rows []ScheduleRow) []ScheduleRow {
	out := make([]ScheduleRow, 0, len(rows))
	for _, row := range rows {
		if row.Scope == scopePanel {
			out = append(out, row)
		}
	}
	return out
}

func hasRow(rows []ScheduleRow, scope, key string) bool {
	for _, row := range rows {
		if row.Scope == scope && row.Key == key {
			return true
		}
	}
	return false
}

// specOfKey reads one row's stored spec, so a merge can be compared byte for byte
// (jsonb round-trips key order, which an equality on the merged value would miss).
func specOfKey(t *testing.T, ctx context.Context, s *postgresStore, orgID int64, key string) string {
	t.Helper()
	rows, err := s.List(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Scope == scopePanel && row.Key == key {
			return string(row.Spec)
		}
	}
	t.Fatalf("no panel row for %s", key)
	return ""
}

// TestPostgresScheduleOrgKey pins the per-org primary key against a real Postgres.
// A panel's cache key is org-independent — the same dashboard and datasource uids
// hash the same in every org — so (scope, key) as the whole key let the second
// org's fit rewrite the first org's cron and stored query objects, invisibly: the
// first org could not be told and the second could not list the row it wrote.
func TestPostgresScheduleOrgKey(t *testing.T) {
	dsn := os.Getenv("FORECAST_TEST_PG")
	if dsn == "" {
		t.Skip("FORECAST_TEST_PG not set")
	}
	ctx := context.Background()
	s, err := openPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	key := "test-org-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	orgs := []int64{987001, 987002}
	for _, org := range orgs {
		org := org
		t.Cleanup(func() { _ = s.Delete(context.Background(), org, scopePanel, key) })
	}
	past := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	for i, org := range orgs {
		if err := s.Upsert(ctx, org, ScheduleRow{
			Scope: scopePanel, Key: key, Cron: "*/5 * * * *", Timezone: "UTC", Enabled: true,
			Spec:      json.RawMessage(fmt.Sprintf(`{"queries":[{"refId":"%c"}],"model":"baseline"}`, 'A'+i)),
			NextRunAt: past,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Two rows, one per org, each with its own spec, and each claimable by its own
	// org alone: this is the whole point of org_id being in the key.
	for i, org := range orgs {
		row, ok, err := s.Row(ctx, org, scopePanel, key)
		if err != nil || !ok {
			t.Fatalf("org %d row ok=%v err=%v", org, ok, err)
		}
		// jsonb normalises spacing, so read the value rather than the document text.
		var spec struct {
			Queries []struct {
				RefID string `json:"refId"`
			} `json:"queries"`
		}
		if err := json.Unmarshal(row.Spec, &spec); err != nil {
			t.Fatal(err)
		}
		if row.OrgID != org || len(spec.Queries) != 1 || spec.Queries[0].RefID != string(rune('A'+i)) {
			t.Fatalf("org %d row=%+v spec=%s", org, row, row.Spec)
		}
		if mine := panelRows(mustList(t, ctx, s, org)); len(mine) != 1 || mine[0].OrgID != org {
			t.Fatalf("org %d lists %+v", org, mine)
		}
		claimed, err := s.Claim(ctx, org, "org-key-owner", time.Minute, 100)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range claimed {
			if row.Key == key {
				found = row.OrgID == org
			}
		}
		if !found {
			t.Fatalf("org %d did not claim its own row: %+v", org, claimed)
		}
	}
	// The other org's claim did not lease this org's row away.
	if err := s.Finish(ctx, "org-key-owner", orgs[1], scopePanel, key, time.Now().Add(time.Hour), "ok"); err != nil {
		t.Fatal(err)
	}
	row, ok, err := s.Row(ctx, orgs[0], scopePanel, key)
	if err != nil || !ok {
		t.Fatalf("org 1 row ok=%v err=%v", ok, err)
	}
	if !row.NextRunAt.Equal(past) || row.LastStatus != "" {
		t.Fatalf("finishing another org's row moved this one: %+v", row)
	}
}

func mustList(t *testing.T, ctx context.Context, s *postgresStore, orgID int64) []ScheduleRow {
	t.Helper()
	rows, err := s.List(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// TestPostgresRetrainKeyMigration pins the upgrade path inside ensureSQL: a
// forecast.retrain keyed on (scope, key) — every deployment that predates org_id
// joining the key — must come out keyed per org with its rows intact, and running
// the statement again must be a no-op, because ensureSQL runs on every connect.
//
// The block is the shipped text, retargeted at a scratch table so the test can start
// from the old shape. The batch shape check in ensureSchedules reports a table whose
// key never got migrated, so a broken statement here fails the scheduler loudly.
func TestPostgresRetrainKeyMigration(t *testing.T) {
	dsn := os.Getenv("FORECAST_TEST_PG")
	if dsn == "" {
		t.Skip("FORECAST_TEST_PG not set")
	}
	ctx := context.Background()
	s, err := openPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.ensureSchedules(ctx); err != nil {
		t.Fatal(err)
	}

	const probe = "forecast.retrain_migration_probe"
	if _, err := s.pool.Exec(ctx, `
DROP TABLE IF EXISTS `+probe+`;
CREATE TABLE `+probe+` (
  scope TEXT NOT NULL,
  key TEXT NOT NULL,
  org_id BIGINT NOT NULL DEFAULT 0,
  cron TEXT NOT NULL,
  PRIMARY KEY (scope, key)
)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+probe) })
	if _, err := s.pool.Exec(ctx, `INSERT INTO `+probe+` (scope, key, org_id, cron) VALUES ('panel', 'keep-me', 7, '*/5 * * * *')`); err != nil {
		t.Fatal(err)
	}

	block := migrationStatement(t, probe)
	if _, err := s.pool.Exec(ctx, block); err != nil {
		t.Fatalf("the migration statement failed: %v", err)
	}
	if def := primaryKeyOf(t, ctx, s, probe); !strings.Contains(def, "org_id") {
		t.Fatalf("key after migration: %q", def)
	}
	var cron string
	if err := s.pool.QueryRow(ctx, `SELECT cron FROM `+probe+` WHERE org_id = 7 AND key = 'keep-me'`).Scan(&cron); err != nil {
		t.Fatalf("the migration lost the row: %v", err)
	}
	if _, err := s.pool.Exec(ctx, block); err != nil {
		t.Fatalf("the migration is not idempotent: %v", err)
	}
}

// migrationStatement returns ensureSQL's retrain-key migration, retargeted at
// another table: both the table it alters and the relname it detects.
func migrationStatement(t *testing.T, table string) string {
	t.Helper()
	at := strings.Index(ensureSQL, "DO $$")
	if at < 0 {
		t.Fatal("ensureSQL no longer carries the retrain-key migration")
	}
	relname := table[strings.LastIndex(table, ".")+1:]
	block := strings.ReplaceAll(ensureSQL[at:], "forecast.retrain", table)
	return strings.ReplaceAll(block, "t.relname = 'retrain'", "t.relname = '"+relname+"'")
}

func primaryKeyOf(t *testing.T, ctx context.Context, s *postgresStore, table string) string {
	t.Helper()
	var def string
	if err := s.pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = $1::regclass AND contype = 'p'`, table).Scan(&def); err != nil {
		t.Fatal(err)
	}
	return def
}

// TestPostgresSchedule exercises the claim protocol against a real Postgres:
// the plugin's DDL, the panel-only claim predicate, the lease and the finish
// round-trip are all SQL behaviour that no in-memory fake can vouch for.
func TestPostgresSchedule(t *testing.T) {
	dsn := os.Getenv("FORECAST_TEST_PG")
	if dsn == "" {
		t.Skip("FORECAST_TEST_PG not set")
	}
	ctx := context.Background()
	s, err := openPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	key := "test-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	const orgID int64 = 987654
	// An hour in the past sorts this row ahead of any leftover due row, so the
	// bounded claim below is expected to include it.
	past := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	spec := json.RawMessage(`{"queries":[{"refId":"A"}],"model":"baseline","season":"minute-week"}`)
	if err := s.Upsert(ctx, orgID, ScheduleRow{
		Scope: scopePanel, Key: key, Cron: "*/5 * * * *", Timezone: "UTC",
		Enabled: true, Spec: spec, NextRunAt: past,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Delete(context.Background(), orgID, scopePanel, key) })

	rows, err := s.List(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	// Baseline rows are fleet-wide and belong to the worker, so this live test
	// can only speak for the panel rows of its own org.
	mine := panelRows(rows)
	if len(mine) != 1 {
		t.Fatalf("list=%+v", rows)
	}
	if row := mine[0]; row.Cron != "*/5 * * * *" || row.Timezone != "UTC" || !row.Enabled || len(row.Spec) == 0 || !row.NextRunAt.Equal(past) {
		t.Fatalf("round-trip %+v", row)
	}
	if other, err := s.List(ctx, orgID+1); err != nil || len(panelRows(other)) != 0 {
		t.Fatalf("org isolation: rows=%+v err=%v", other, err)
	}
	// A worker-written baseline row carries no org, so it is visible everywhere
	// and must survive an admin edit aimed at another org.
	if err := s.Upsert(ctx, orgID, ScheduleRow{
		Scope: scopeBaseline, Key: key, Cron: "*/4 * * * *", Timezone: "UTC", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Delete(context.Background(), 0, scopeBaseline, key) })
	if rows, err := s.List(ctx, orgID+1); err != nil {
		t.Fatal(err)
	} else if !hasRow(rows, scopeBaseline, key) {
		t.Fatalf("worker baseline row hidden from another org: %+v", rows)
	}

	if due, err := s.Due(ctx, orgID, key, time.Now()); err != nil || !due {
		t.Fatalf("due=%v err=%v", due, err)
	}

	claimed, err := s.Claim(ctx, orgID, "test-owner", time.Minute, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range claimed {
		if row.Key == key && row.OrgID == orgID {
			found = len(row.Spec) > 0 && row.Cron == "*/5 * * * *"
		}
	}
	if !found {
		t.Fatalf("claim did not return our row: %+v", claimed)
	}
	// The lease holds the row out of the next claim.
	again, err := s.Claim(ctx, orgID, "test-owner-2", time.Minute, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range again {
		if row.Key == key {
			t.Fatalf("leased row claimed twice: %+v", row)
		}
	}

	next := time.Now().Add(5 * time.Minute).UTC().Truncate(time.Millisecond)
	// A stale owner — a retrain that outlived its lease — must not release this
	// claim or move this row's next run: those belong to whoever holds the lease.
	stale := next.Add(time.Hour)
	if err := s.Finish(ctx, "test-owner-2", orgID, scopePanel, key, stale, "error: stale"); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.List(ctx, orgID); err != nil {
		t.Fatal(err)
	} else {
		mine := panelRows(rows)
		if len(mine) != 1 {
			t.Fatalf("list=%+v", rows)
		}
		if !mine[0].NextRunAt.Equal(past) || mine[0].LastStatus != "" {
			t.Fatalf("a stale owner moved the row: %+v", mine[0])
		}
	}
	if claimed, err := s.Claim(ctx, orgID, "test-owner-4", time.Minute, 100); err != nil {
		t.Fatal(err)
	} else {
		for _, row := range claimed {
			if row.Key == key {
				t.Fatalf("a stale owner released the claim: %+v", row)
			}
		}
	}

	if err := s.Finish(ctx, "test-owner", orgID, scopePanel, key, next, "ok"); err != nil {
		t.Fatal(err)
	}
	if due, err := s.Due(ctx, orgID, key, time.Now()); err != nil || due {
		t.Fatalf("due after finish=%v err=%v", due, err)
	}
	// Finish must have released the claim outright, not merely parked it.
	if claimed, err := s.Claim(ctx, orgID, "test-owner-3", time.Minute, 100); err != nil {
		t.Fatal(err)
	} else {
		for _, row := range claimed {
			if row.Key == key {
				t.Fatalf("still claimed after finish: %+v", row)
			}
		}
	}

	// A released claim is not an invitation: an owner that no longer holds one must
	// not write its outcome, or a retrain that outlived its lease would overwrite the
	// next run and status another process already recorded for this row.
	if err := s.Finish(ctx, "test-owner-2", orgID, scopePanel, key, stale, "error: stale"); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.List(ctx, orgID); err != nil {
		t.Fatal(err)
	} else if mine := panelRows(rows); len(mine) != 1 || !mine[0].NextRunAt.Equal(next) || mine[0].LastStatus != "ok" {
		t.Fatalf("a released claim was written by a stale owner: %+v", mine)
	}

	// The Configuration page edits a cron without a spec: the stored query
	// objects must survive.
	if err := s.Upsert(ctx, orgID, ScheduleRow{
		Scope: scopePanel, Key: key, Cron: "*/9 * * * *", Timezone: "UTC",
		Enabled: false, NextRunAt: next,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err = s.List(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	mine = panelRows(rows)
	if len(mine) != 1 || len(mine[0].Spec) == 0 || mine[0].Cron != "*/9 * * * *" || mine[0].Enabled {
		t.Fatalf("spec edit round-trip %+v", rows)
	}
	if mine[0].LastStatus != "ok" {
		t.Fatalf("run history lost: %+v", mine[0])
	}

	// The overlay identifies its row on every request, probe included. The merge adds
	// identity to the stored spec without touching the query objects the scheduler
	// replays, must not create a row, and must not write when nothing changed.
	prov := PanelProvenance{PanelID: 3, PanelTitle: "CPU", DashboardUID: "dash-1", QuerySummary: "Druid: minuteweek · minute"}
	if err := s.Identify(ctx, orgID, key, prov); err != nil {
		t.Fatal(err)
	}
	identified := specOfKey(t, ctx, s, orgID, key)
	// jsonb normalises spacing, so compare values rather than document text.
	var got map[string]any
	if err := json.Unmarshal([]byte(identified), &got); err != nil {
		t.Fatal(err)
	}
	if got["panelId"] != float64(3) || got["panelTitle"] != "CPU" || got["dashboardUid"] != "dash-1" || got["querySummary"] != "Druid: minuteweek · minute" {
		t.Fatalf("identify wrote %+v", got)
	}
	// The merge adds identity only: the query objects the scheduler replays and the
	// model fields the refit needs are untouched.
	if got["model"] != "baseline" || got["season"] != "minute-week" {
		t.Fatalf("identify damaged the spec: %+v", got)
	}
	if queries, ok := got["queries"].([]any); !ok || len(queries) != 1 {
		t.Fatalf("identify dropped the query objects: %+v", got)
	}
	if err := s.Identify(ctx, orgID, key, prov); err != nil {
		t.Fatal(err)
	}
	if again := specOfKey(t, ctx, s, orgID, key); again != identified {
		t.Fatalf("an unchanged identify rewrote the spec: %s -> %s", identified, again)
	}
	// No row, no spec to merge into: a probe must never create a claimable row.
	if err := s.Identify(ctx, orgID, key+"-absent", prov); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.List(ctx, orgID); err != nil {
		t.Fatal(err)
	} else if hasRow(panelRows(rows), scopePanel, key+"-absent") {
		t.Fatalf("identify created a row: %+v", rows)
	}
	// Another org's panel must not reach this row.
	if err := s.Identify(ctx, orgID+1, key, PanelProvenance{PanelTitle: "stolen"}); err != nil {
		t.Fatal(err)
	}
	if after := specOfKey(t, ctx, s, orgID, key); after != identified {
		t.Fatalf("another org identified the row: %s", after)
	}

	// A disabled row is not due even when its next run is in the past.
	if err := s.Upsert(ctx, orgID, ScheduleRow{
		Scope: scopePanel, Key: key, Cron: "*/9 * * * *", Timezone: "UTC",
		Enabled: false, NextRunAt: past,
	}); err != nil {
		t.Fatal(err)
	}
	if due, err := s.Due(ctx, orgID, key, time.Now()); err != nil || due {
		t.Fatalf("disabled due=%v err=%v", due, err)
	}

	if err := s.Delete(ctx, orgID, scopePanel, key); err != nil {
		t.Fatal(err)
	}
	if after, err := s.List(ctx, orgID); err != nil || len(panelRows(after)) != 0 {
		t.Fatalf("after delete rows=%+v err=%v", after, err)
	}
}
