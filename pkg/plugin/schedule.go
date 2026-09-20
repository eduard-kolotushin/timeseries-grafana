package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/robfig/cron/v3"
)

var (
	errInvalidCron     = errors.New("forecast: invalid cron")
	errInvalidTimezone = errors.New("forecast: invalid timezone")
)

const (
	// scopePanel rows are owned and retrained by this plugin: the spec holds the
	// browser's own datasource query objects, which is what lets the scheduler
	// re-fetch training data with no browser attached.
	scopePanel = "panel"
	// scopeBaseline rows belong to the baselines worker, which inserts, claims and
	// finishes them itself. The plugin only ever lists them (and refuses to delete
	// them: the worker would re-insert them on its next tick).
	scopeBaseline = "baseline"
)

// ScheduleRow is one row of forecast.retrain. Spec is opaque to the store; JSONB
// columns scan into nil when the row has no spec (a panel trained by a frontend
// that predates trainSource).
type ScheduleRow struct {
	OrgID      int64
	Scope      string
	Key        string
	Cron       string
	Timezone   string
	Enabled    bool
	Spec       json.RawMessage
	NextRunAt  time.Time
	LastRunAt  time.Time
	LastStatus string
}

// ScheduleStore is the forecast.retrain table as the plugin uses it. Claim and
// Finish are scope-agnostic because the worker shares the table, but the plugin's
// own claim is restricted to scope='panel' rows (see panelClaimSQL).
//
// Finish is owner-guarded: a claim may only be released by the owner that took
// it (or when nobody holds it), so a retrain that outlives its lease cannot clear
// a newer owner's claim.
type ScheduleStore interface {
	List(ctx context.Context, orgID int64) ([]ScheduleRow, error)
	Upsert(ctx context.Context, orgID int64, row ScheduleRow) error
	Delete(ctx context.Context, orgID int64, scope, key string) error
	Due(ctx context.Context, orgID int64, key string, now time.Time) (bool, error)
	Claim(ctx context.Context, owner string, lease time.Duration, limit int) ([]ScheduleRow, error)
	Finish(ctx context.Context, owner, scope, key string, next time.Time, status string) error
}

// panelClaimSQL is the plugin half of the claim protocol. The predicate is
// deliberately narrower than the worker's (scope='panel' AND spec IS NOT NULL):
// the plugin must never claim a row it cannot fetch training data for, and the
// worker must never claim a panel row. FOR UPDATE SKIP LOCKED makes concurrent
// Grafana replicas safe without a coordinator.
const panelClaimSQL = `
WITH due AS (
  SELECT scope, key FROM forecast.retrain
  WHERE scope = 'panel' AND enabled AND spec IS NOT NULL
    AND next_run_at IS NOT NULL AND next_run_at <= now()
    AND (claimed_until IS NULL OR claimed_until < now())
  ORDER BY next_run_at
  LIMIT $1
  FOR UPDATE SKIP LOCKED
)
UPDATE forecast.retrain r
SET claimed_by = $2, claimed_until = now() + $3::interval, updated_at = now()
FROM due
WHERE r.scope = due.scope AND r.key = due.key
RETURNING r.scope, r.key, r.org_id, r.cron, r.timezone, r.spec
`

// intervalSeconds renders a duration as a Postgres interval literal.
// time.Duration.String() is not one ("5m0s" is a syntax error), so the lease
// travels as a plain second count.
func intervalSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64) + " seconds"
}

// List returns this org's panel rows plus every baseline row. Baseline rows are
// written by the worker (sibling timeseries-baselines), which has no notion of a
// Grafana org and leaves org_id at the column default, so they are fleet-wide by
// construction: filtering them by org would hide every worker schedule from the
// Configuration page and from the PUT echo.
func (s *postgresStore) List(ctx context.Context, orgID int64) ([]ScheduleRow, error) {
	if err := s.ensureSchedules(ctx); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
SELECT scope, key, cron, timezone, enabled, spec, next_run_at, last_run_at, last_status
FROM forecast.retrain WHERE scope = 'baseline' OR org_id = $1 ORDER BY scope, key
`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ScheduleRow, 0, 16)
	for rows.Next() {
		row := ScheduleRow{OrgID: orgID}
		var next, last *time.Time
		var status *string
		if err := rows.Scan(&row.Scope, &row.Key, &row.Cron, &row.Timezone, &row.Enabled, &row.Spec, &next, &last, &status); err != nil {
			return nil, err
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
		out = append(out, row)
	}
	return out, rows.Err()
}

// Upsert writes the row given. A nil Spec keeps the stored one, which is what
// lets the Configuration page edit a cron without erasing the panel's query
// objects.
func (s *postgresStore) Upsert(ctx context.Context, orgID int64, row ScheduleRow) error {
	if err := s.ensureSchedules(ctx); err != nil {
		return err
	}
	// Baseline rows are fleet-wide (the worker owns them and has no org), so an
	// admin edit of a baseline cron must not re-home the row into the editor's
	// org: the worker's next Schedule/Done would then disagree with the row it
	// reads back.
	if row.Scope == scopeBaseline {
		orgID = 0
	}
	var spec any
	if len(row.Spec) > 0 {
		spec = []byte(row.Spec)
	}
	var next any
	if !row.NextRunAt.IsZero() {
		next = row.NextRunAt.UTC()
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO forecast.retrain (scope, key, org_id, cron, timezone, enabled, spec, next_run_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
ON CONFLICT (scope, key) DO UPDATE SET
  cron = EXCLUDED.cron,
  timezone = EXCLUDED.timezone,
  enabled = EXCLUDED.enabled,
  spec = COALESCE(EXCLUDED.spec, forecast.retrain.spec),
  next_run_at = EXCLUDED.next_run_at,
  updated_at = now()
`, row.Scope, row.Key, orgID, row.Cron, row.Timezone, row.Enabled, spec, next)
	return err
}

func (s *postgresStore) Delete(ctx context.Context, orgID int64, scope, key string) error {
	if err := s.ensureSchedules(ctx); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM forecast.retrain WHERE org_id = $1 AND scope = $2 AND key = $3`, orgID, scope, key)
	return err
}

// Due answers the probe path only: "is this panel's schedule past its next run".
// It is a single primary-key lookup because it runs on every overlay probe.
func (s *postgresStore) Due(ctx context.Context, orgID int64, key string, now time.Time) (bool, error) {
	if err := s.ensureSchedules(ctx); err != nil {
		return false, err
	}
	var due bool
	err := s.pool.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM forecast.retrain
  WHERE scope = 'panel' AND org_id = $1 AND key = $2 AND enabled
    AND next_run_at IS NOT NULL AND next_run_at <= $3
)
`, orgID, key, now.UTC()).Scan(&due)
	return due, err
}

func (s *postgresStore) Claim(ctx context.Context, owner string, lease time.Duration, limit int) ([]ScheduleRow, error) {
	if err := s.ensureSchedules(ctx); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, panelClaimSQL, limit, owner, intervalSeconds(lease))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ScheduleRow, 0, limit)
	for rows.Next() {
		var row ScheduleRow
		if err := rows.Scan(&row.Scope, &row.Key, &row.OrgID, &row.Cron, &row.Timezone, &row.Spec); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Finish records the outcome and drops the claim, so the row is claimable again
// exactly at next rather than at lease expiry. The owner predicate is what makes
// that safe: a retrain that outlived its lease has no claim left to release, and
// must not clear the claim (or overwrite the next run) of the process that took
// the row over. Zero rows updated is that case, not an error.
//
// The worker's Done is the mirror of this statement for scope='baseline'.
func (s *postgresStore) Finish(ctx context.Context, owner, scope, key string, next time.Time, status string) error {
	if err := s.ensureSchedules(ctx); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
UPDATE forecast.retrain
SET next_run_at = $3, last_run_at = now(), last_status = $4,
    claimed_by = NULL, claimed_until = NULL, updated_at = now()
WHERE scope = $1 AND key = $2 AND (claimed_by IS NULL OR claimed_by = $5)
`, scope, key, next.UTC(), status, owner)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		log.DefaultLogger.Debug("retrain claim lost", "scope", scope, "key", key, "owner", owner)
	}
	return nil
}

// errScheduleStore stands in when the pool cannot be built, so the scheduler
// reports a real error per attempt instead of a nil dereference.
type errScheduleStore struct{ err error }

func (s errScheduleStore) List(context.Context, int64) ([]ScheduleRow, error) { return nil, s.err }

func (s errScheduleStore) Upsert(context.Context, int64, ScheduleRow) error { return s.err }

func (s errScheduleStore) Delete(context.Context, int64, string, string) error { return s.err }

func (s errScheduleStore) Due(context.Context, int64, string, time.Time) (bool, error) {
	return false, s.err
}

func (s errScheduleStore) Claim(context.Context, string, time.Duration, int) ([]ScheduleRow, error) {
	return nil, s.err
}

func (s errScheduleStore) Finish(context.Context, string, string, string, time.Time, string) error {
	return s.err
}

// nextRun is the cron arithmetic both the plugin and the worker follow: the
// expression is evaluated in the row's own timezone and stored as UTC, so a DST
// transition shifts the wall-clock schedule without shifting the stored instant.
func nextRun(cronSpec, timezone string, now time.Time) (time.Time, error) {
	schedule, err := cron.ParseStandard(cronSpec)
	if err != nil {
		return time.Time{}, errInvalidCron
	}
	loc, err := time.LoadLocation(normalizedTimezone(timezone))
	if err != nil {
		return time.Time{}, errInvalidTimezone
	}
	return schedule.Next(now.In(loc)).UTC(), nil
}
