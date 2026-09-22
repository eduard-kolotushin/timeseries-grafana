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
	// finishes them itself. They are fleet-wide (the worker has no org and leaves
	// org_id at 0), so the plugin lists and edits them from any org.
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
	// SupersededAt is when this row stopped being the panel's current one: the
	// panel trained a different cache key, so this row is left in the list for its
	// history but is never claimed again. Zero means it is still current.
	SupersededAt time.Time
}

// ScheduleStore is the forecast.retrain table as the plugin uses it. Claim and
// Finish are scope-agnostic because the worker shares the table, but the plugin's
// own claim is restricted to scope='panel' rows of one org (see panelClaimSQL).
//
// Finish is owner-guarded: only the owner that took the claim may release it, so a
// retrain that outlives its lease can neither clear a newer owner's claim nor
// overwrite its next run.
type ScheduleStore interface {
	List(ctx context.Context, orgID int64) ([]ScheduleRow, error)
	Row(ctx context.Context, orgID int64, scope, key string) (ScheduleRow, bool, error)
	Upsert(ctx context.Context, orgID int64, row ScheduleRow) error
	Delete(ctx context.Context, orgID int64, scope, key string) error
	Due(ctx context.Context, orgID int64, key string, now time.Time) (bool, error)
	Claim(ctx context.Context, orgID int64, owner string, lease time.Duration, limit int) ([]ScheduleRow, error)
	Finish(ctx context.Context, owner string, orgID int64, scope, key string, next time.Time, status string) error
	// Identify merges a panel's identity into an existing row's spec. It never
	// creates a row: only a fit knows the query objects a claimable row needs.
	Identify(ctx context.Context, orgID int64, key string, prov PanelProvenance) error
	// Supersede retires the org's other panel rows that carry the same
	// dashboardUid+panelId provenance, and clears the flag on keepKey. It is how a
	// panel that changed its query stops the scheduler refitting the series its old
	// cache key trained. A row without that provenance (a cacheKey-only fit stores
	// none) is never touched, and neither is another panel's.
	Supersede(ctx context.Context, orgID int64, dashboardUID string, panelID int, keepKey string) error
}

// identifySQL merges the identity keys into whatever the spec already holds. Two
// guards matter: the row must exist with a spec (a provenance-only spec would be
// claimed by the scheduler and fail to fetch), and the merged result must differ
// from the stored one (the overlay identifies its row on every load, and a
// dashboard view is not a reason to write).
const identifySQL = `
UPDATE forecast.retrain
SET spec = spec || $3::jsonb, updated_at = now()
WHERE scope = 'panel' AND key = $1 AND org_id = $2 AND spec IS NOT NULL
  AND spec || $3::jsonb IS DISTINCT FROM spec
`

// Identify implements ScheduleStore.identifySQL: a JSONB merge of panelId,
// panelTitle and dashboardUid, leaving the panel's queries and model fields alone.
func (s *postgresStore) Identify(ctx context.Context, orgID int64, key string, prov PanelProvenance) error {
	if err := s.ensureSchedules(ctx); err != nil {
		return err
	}
	patch, err := provenanceJSON(prov)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, identifySQL, key, orgID, patch)
	return err
}

// provenanceJSON renders only the fields the overlay resolved, so a merge never
// blanks an identity a previous load stored.
func provenanceJSON(prov PanelProvenance) ([]byte, error) {
	patch := map[string]any{}
	if prov.PanelID != 0 {
		patch["panelId"] = prov.PanelID
	}
	if prov.PanelTitle != "" {
		patch["panelTitle"] = prov.PanelTitle
	}
	if prov.DashboardUID != "" {
		patch["dashboardUid"] = prov.DashboardUID
	}
	if prov.QuerySummary != "" {
		patch["querySummary"] = prov.QuerySummary
	}
	return json.Marshal(patch)
}

// panelClaimSQL is the plugin half of the claim protocol. The predicate is
// deliberately narrower than the worker's (scope='panel' AND a spec this process
// can actually fetch AND this org AND not superseded): the plugin must never claim
// a row it cannot fetch training data for, and the worker must never claim a panel
// row. `spec IS NOT NULL` alone is not enough — a spec whose queries is null or []
// is claimed and then posted to /api/ds/query, which answers 400 query.noQueries
// on every cron slot forever — so the claim requires a non-empty JSON array (spec
// is jsonb in this schema). The emptiness test is `<> '[]'`, not jsonb_array_length:
// WHERE clauses are not evaluated left to right, so a scalar `"queries":null` can
// reach jsonb_array_length and abort the whole claim with SQLSTATE 22023.
// A superseded row is the panel's old cache key: it is kept for its history but
// must never be refit. The org restriction is not cosmetic — Grafana resolves a
// datasourceUid inside the requesting org, and the scheduler holds one credential,
// so a row of another org would be fetched as the wrong org's series (or fail) and
// stored as this org's snapshot. FOR UPDATE SKIP LOCKED makes concurrent Grafana
// replicas safe without a coordinator.
const panelClaimSQL = `
WITH due AS (
  SELECT scope, org_id, key FROM forecast.retrain
  WHERE scope = 'panel' AND org_id = $4 AND enabled AND spec IS NOT NULL
    AND jsonb_typeof(spec->'queries') = 'array'
    AND spec->'queries' <> '[]'::jsonb
    AND superseded_at IS NULL
    AND next_run_at IS NOT NULL AND next_run_at <= now()
    AND (claimed_until IS NULL OR claimed_until < now())
  ORDER BY next_run_at
  LIMIT $1
  FOR UPDATE SKIP LOCKED
)
UPDATE forecast.retrain r
SET claimed_by = $2, claimed_until = now() + $3::interval, updated_at = now()
FROM due
WHERE r.scope = due.scope AND r.org_id = due.org_id AND r.key = due.key
RETURNING r.scope, r.key, r.org_id, r.cron, r.timezone, r.spec
`

// supersedeSQL retires a panel's older rows. The identity is read out of the
// stored spec with jsonb ->>, so only rows a browser wrote for this exact panel
// are touched: a cacheKey-only fit stores no provenance and can therefore never
// supersede a row it knows nothing about, and neither can another panel's fit.
// keepKey's own flag is cleared (the panel is showing that key again) and the
// others are stamped once — COALESCE, not now(), so re-running this on every fit
// does not keep rewriting rows that are already superseded. The CASE is what makes
// the statement idempotent: it updates only the rows whose value would change.
const supersedeSQL = `
UPDATE forecast.retrain
SET superseded_at = CASE WHEN key = $4 THEN NULL ELSE COALESCE(superseded_at, now()) END,
    updated_at = now()
WHERE scope = 'panel' AND org_id = $1
  AND spec->>'dashboardUid' = $2 AND spec->>'panelId' = $3
  AND CASE WHEN key = $4 THEN superseded_at IS NOT NULL ELSE superseded_at IS NULL END
`

// Supersede implements supersedeSQL.
func (s *postgresStore) Supersede(ctx context.Context, orgID int64, dashboardUID string, panelID int, keepKey string) error {
	if err := s.ensureSchedules(ctx); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, supersedeSQL, orgID, dashboardUID, strconv.Itoa(panelID), keepKey)
	return err
}

// intervalSeconds renders a duration as a Postgres interval literal.
// time.Duration.String() is not one ("5m0s" is a syntax error), so the lease
// travels as a plain second count.
func intervalSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64) + " seconds"
}

// List returns this org's panel rows plus every baseline row. A panel row is
// per-org (org_id is part of the primary key), so one org's schedules are never
// visible in another. Baseline rows are written by the worker (sibling
// timeseries-baselines), which has no notion of a Grafana org and leaves org_id at
// the column default, so they are fleet-wide by construction: filtering them by
// org would hide every worker schedule from the Retrain schedules page and from
// the PUT echo.
func (s *postgresStore) List(ctx context.Context, orgID int64) ([]ScheduleRow, error) {
	if err := s.ensureSchedules(ctx); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
SELECT scope, key, cron, timezone, enabled, spec, next_run_at, last_run_at, last_status, superseded_at
FROM forecast.retrain WHERE scope = 'baseline' OR org_id = $1 ORDER BY scope, key
`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ScheduleRow, 0, 16)
	for rows.Next() {
		row := ScheduleRow{OrgID: orgID}
		var next, last, superseded *time.Time
		var status *string
		if err := rows.Scan(&row.Scope, &row.Key, &row.Cron, &row.Timezone, &row.Enabled, &row.Spec, &next, &last, &status, &superseded); err != nil {
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
		if superseded != nil {
			row.SupersededAt = superseded.UTC()
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Upsert writes the row given. A nil Spec keeps the stored one, which is what
// lets the Configuration page edit a cron without erasing the panel's query
// objects. The conflict target is the full primary key, so two orgs whose panels
// hash to the same cache key own two rows and neither can rewrite the other's.
//
// Only an existing row is updated: org_id is part of the key and is never
// re-homed, so this cannot move a row between orgs either.
//
// superseded_at is never written here: a fresh insert leaves it NULL (the row is
// the panel's current one) and an update keeps whatever Supersede set, so a
// browser retrain of a row cannot silently resurrect one the panel retired.
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
ON CONFLICT (scope, org_id, key) DO UPDATE SET
  cron = EXCLUDED.cron,
  timezone = EXCLUDED.timezone,
  enabled = EXCLUDED.enabled,
  spec = COALESCE(EXCLUDED.spec, forecast.retrain.spec),
  next_run_at = EXCLUDED.next_run_at,
  updated_at = now()
`, row.Scope, row.Key, orgID, row.Cron, row.Timezone, row.Enabled, spec, next)
	return err
}

// Delete removes one row. A baseline row lives at org 0, so an admin deleting it
// from any org deletes the fleet-wide row; the worker re-creates it on its next
// tick if the hash is still reporting, which is what makes deleting a retired
// hash's row stick and deleting a live one harmless.
func (s *postgresStore) Delete(ctx context.Context, orgID int64, scope, key string) error {
	if err := s.ensureSchedules(ctx); err != nil {
		return err
	}
	if scope == scopeBaseline {
		orgID = 0
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

func (s *postgresStore) Claim(ctx context.Context, orgID int64, owner string, lease time.Duration, limit int) ([]ScheduleRow, error) {
	if err := s.ensureSchedules(ctx); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, panelClaimSQL, limit, owner, intervalSeconds(lease), orgID)
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
// that safe: a retrain that outlived its lease has no claim left to release, so it
// may neither clear the claim a newer owner took nor overwrite that owner's next
// run and status. Zero rows updated is that case, not an error.
//
// Nothing else may write a row's run history either: every caller of Finish holds
// the claim it took (retrainOne), so requiring the owner costs nothing and the
// "nobody holds it" case cannot arise from a live retrain.
//
// The worker's Done is the mirror of this statement for scope='baseline'.
func (s *postgresStore) Finish(ctx context.Context, owner string, orgID int64, scope, key string, next time.Time, status string) error {
	if err := s.ensureSchedules(ctx); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
UPDATE forecast.retrain
SET next_run_at = $4, last_run_at = now(), last_status = $5,
    claimed_by = NULL, claimed_until = NULL, updated_at = now()
WHERE scope = $1 AND org_id = $2 AND key = $3 AND claimed_by = $6
`, scope, orgID, key, next.UTC(), status, owner)
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

func (s errScheduleStore) Row(context.Context, int64, string, string) (ScheduleRow, bool, error) {
	return ScheduleRow{}, false, s.err
}

func (s errScheduleStore) Upsert(context.Context, int64, ScheduleRow) error { return s.err }

func (s errScheduleStore) Delete(context.Context, int64, string, string) error { return s.err }

func (s errScheduleStore) Due(context.Context, int64, string, time.Time) (bool, error) {
	return false, s.err
}

func (s errScheduleStore) Claim(context.Context, int64, string, time.Duration, int) ([]ScheduleRow, error) {
	return nil, s.err
}

func (s errScheduleStore) Finish(context.Context, string, int64, string, string, time.Time, string) error {
	return s.err
}

func (s errScheduleStore) Identify(context.Context, int64, string, PanelProvenance) error {
	return s.err
}

func (s errScheduleStore) Supersede(context.Context, int64, string, int, string) error {
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
