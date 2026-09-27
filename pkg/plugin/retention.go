package plugin

import (
	"context"
	"fmt"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

// Retention is what keeps the snapshot store from growing forever, and it is
// deliberately three small statements on the retrain ticker rather than a second
// process: the scheduler that refreshes a model is the thing that knows best when
// nothing is refreshing it any more, and FORECAST_RETRAIN_ENABLED=false is then
// the single switch that turns both off.
//
// A schedule row does not own a snapshot. A row is a *when*, not a *what*: an admin
// may delete a panel's row to stop refitting it, and the model behind it is still
// what the panel's cacheKey resolves to. So the sweep removes a model only when
// nothing refreshed it, and gives a model without a row the deployment default
// back instead of orphaning it.

// deleteStaleSnapshotsSQL collects a model nothing refreshed within the window:
// neither a scheduled retrain nor a browser fit touched it, which means the metric
// stopped being published.
const deleteStaleSnapshotsSQL = `
DELETE FROM forecast.snapshots WHERE updated_at < now() - $1::interval
`

// deleteIdleRowsSQL collects a row that has been idle for the whole window and has
// no snapshot behind it. Two independent clocks are required because either one
// alone would collect a row that is still alive: next_run_at is what the scheduler
// moves on success, last_run_at is what a failed attempt moves, so a Druid or
// Grafana outage (retried every lease, each failure writing last_run_at) never
// looks like a dead metric. Neither clock moves while a claim is in flight, so a
// live claim is excluded too: without that, a sweep could delete a row another
// replica is fitting and the reconcile would give the row back with this process's
// deployment default cron instead of the cron an admin set. The NOT EXISTS is what
// preserves an admin's cron on a live panel whose snapshot is only ever refreshed
// by browser fits — and it makes the same statement correct for scope='baseline':
// the plugin's snapshots table never holds a baseline key, so for those rows the
// condition reduces to the two clocks the worker itself updates.
const deleteIdleRowsSQL = `
DELETE FROM forecast.retrain r
WHERE r.next_run_at IS NOT NULL
  AND r.next_run_at < now() - $1::interval
  AND (r.last_run_at IS NULL OR r.last_run_at < now() - $1::interval)
  AND (r.claimed_until IS NULL OR r.claimed_until < now())
  AND NOT EXISTS (
    SELECT 1 FROM forecast.snapshots s
    WHERE s.org_id = r.org_id AND s.cache_key = r.key)
`

// reconcileRowsSQL gives a snapshot without a row the deployment default back. A
// deleted row's cron is gone with it, so the default is the only cron a re-created
// row can carry; it has no spec either, which is why panelClaimSQL ignores it until
// a panel load merges its identity and a fit writes the spec. That is also why this
// cannot resurrect a claimable-but-unfetchable row.
const reconcileRowsSQL = `
INSERT INTO forecast.retrain (scope, org_id, key, cron, timezone, enabled, next_run_at)
SELECT 'panel', s.org_id, s.cache_key, $1, $2, true, now()
FROM forecast.snapshots s
WHERE NOT EXISTS (
  SELECT 1 FROM forecast.retrain r
  WHERE r.scope = 'panel' AND r.org_id = s.org_id AND r.key = s.cache_key)
ON CONFLICT (scope, org_id, key) DO NOTHING
`

// sweepResult is what one sweep collected, for the tick's log line.
type sweepResult struct {
	Snapshots int64
	Rows      int64
	Recreated int64
}

// Sweep applies the retention rule above. The three statements run in one
// transaction in the order declared, because each depends on the one before it: a
// snapshot collected first is then absent, which is what makes its row collectable,
// and only a snapshot that survived is given a row back.
func (s *postgresStore) Sweep(ctx context.Context, ttl time.Duration, cron, timezone string) (sweepResult, error) {
	if err := s.ensure(ctx); err != nil {
		return sweepResult{}, err
	}
	if err := s.ensureSchedules(ctx); err != nil {
		return sweepResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sweepResult{}, fmt.Errorf("forecast store: %w", err)
	}
	// Rollback after a commit is a no-op, so every error path can share it.
	defer func() { _ = tx.Rollback(ctx) }()
	window := intervalSeconds(ttl)
	out := sweepResult{}
	for _, stmt := range []struct {
		name string
		sql  string
		args []any
		into *int64
	}{
		{"collect snapshots", deleteStaleSnapshotsSQL, []any{window}, &out.Snapshots},
		{"collect rows", deleteIdleRowsSQL, []any{window}, &out.Rows},
		{"reconcile rows", reconcileRowsSQL, []any{cron, timezone}, &out.Recreated},
	} {
		tag, err := tx.Exec(ctx, stmt.sql, stmt.args...)
		if err != nil {
			return sweepResult{}, fmt.Errorf("forecast store: %s: %w", stmt.name, err)
		}
		*stmt.into = tag.RowsAffected()
	}
	if err := tx.Commit(ctx); err != nil {
		return sweepResult{}, fmt.Errorf("forecast store: %w", err)
	}
	return out, nil
}

// retentionSweeper is the store half of retention. It is an optional interface
// rather than a ScheduleStore method because only the Postgres store can collect:
// the ticker must keep ticking (and a test double must stay a double) when it
// cannot.
type retentionSweeper interface {
	Sweep(ctx context.Context, ttl time.Duration, cron, timezone string) (sweepResult, error)
}

// sweepRetention collects once per tick. Every failure mode is a log line and
// nothing else: a scheduler (or a store) that cannot collect must still retrain,
// and a query must never fail because retention did.
func (a *App) sweepRetention(ctx context.Context) {
	if a.store == nil || a.retrain.SnapshotTTL <= 0 {
		return
	}
	sweeper, ok := a.sched.(retentionSweeper)
	if !ok {
		return
	}
	res, err := sweeper.Sweep(ctx, a.retrain.SnapshotTTL, a.retrain.Cron, normalizedTimezone(a.retrain.Timezone))
	if err != nil {
		log.DefaultLogger.Warn("forecast retention", "err", err.Error())
		return
	}
	if res.Snapshots > 0 || res.Rows > 0 || res.Recreated > 0 {
		log.DefaultLogger.Debug(
			"forecast retention",
			"snapshots", res.Snapshots, "rows", res.Rows, "recreated", res.Recreated,
		)
	}
}
