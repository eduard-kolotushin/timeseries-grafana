package plugin

import (
	"context"
	"encoding/json"
	"errors"
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
	pingErr    error
	execErr    func(sql string) error
	rowErr     error
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

func (p *fakePool) QueryRow(context.Context, string, ...any) pgx.Row { return fakeRow{err: p.rowErr} }

func (p *fakePool) Close() {}

type fakeRow struct{ err error }

func (r fakeRow) Scan(...any) error { return r.err }

// TestScheduleDDLFailureLeavesSnapshotsServing: ensureSQL provisions both
// tables, so a runtime user that may not CREATE can be missing forecast.retrain
// while forecast.snapshots is fine. A schedule failure must not change what a
// snapshot call answers — sharing one cached error made every overlay probe 500
// until the retry window expired.
func TestScheduleDDLFailureLeavesSnapshotsServing(t *testing.T) {
	ctx := context.Background()
	db := &fakePool{
		execErr: func(sql string) error {
			if strings.Contains(sql, "forecast.retrain") {
				return errors.New("ERROR: permission denied for schema forecast (SQLSTATE 42501)")
			}
			return nil
		},
		rowErr: pgx.ErrNoRows,
	}
	clock := time.Unix(1_000_000, 0)
	s := &postgresStore{pool: db, now: func() time.Time { return clock }}

	if err := s.ensureSchedules(ctx); err == nil {
		t.Fatal("the schedule path must report the DDL failure")
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
		t.Fatal("the schedule path must not latch ready while the table is missing")
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

	claimed, err := s.Claim(ctx, "test-owner", time.Minute, 100)
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
	again, err := s.Claim(ctx, "test-owner-2", time.Minute, 100)
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
	if err := s.Finish(ctx, "test-owner-2", scopePanel, key, stale, "error: stale"); err != nil {
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
	if claimed, err := s.Claim(ctx, "test-owner-4", time.Minute, 100); err != nil {
		t.Fatal(err)
	} else {
		for _, row := range claimed {
			if row.Key == key {
				t.Fatalf("a stale owner released the claim: %+v", row)
			}
		}
	}

	if err := s.Finish(ctx, "test-owner", scopePanel, key, next, "ok"); err != nil {
		t.Fatal(err)
	}
	if due, err := s.Due(ctx, orgID, key, time.Now()); err != nil || due {
		t.Fatalf("due after finish=%v err=%v", due, err)
	}
	// Finish must have released the claim outright, not merely parked it.
	if claimed, err := s.Claim(ctx, "test-owner-3", time.Minute, 100); err != nil {
		t.Fatal(err)
	} else {
		for _, row := range claimed {
			if row.Key == key {
				t.Fatalf("still claimed after finish: %+v", row)
			}
		}
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
