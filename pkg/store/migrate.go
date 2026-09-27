// Package store owns the PostgreSQL schema this plugin creates: the versioned
// migration files under migrations/, the engine that applies them, and the store
// DSN resolution both the plugin and cmd/migrate read.
//
// The migration files are the only schema authority. cmd/migrate applies them
// out-of-process so a CI/CD pipeline can prepare a database before Grafana
// starts; a deployment that never runs that step still converges, because the
// plugin applies the same set at its first store use.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// advisoryLockKey serialises concurrent migrators — a CI/CD job racing a Grafana
// replica that also auto-applies. 0x666f726563617374 is "forecast" in ASCII.
const advisoryLockKey int64 = 0x666f726563617374

// ledgerDDL is the one table the engine creates outside the migration files,
// because it records them. It is idempotent and runs inside the advisory lock, so
// two migrators starting against an empty database cannot race each other into a
// duplicate-table error.
const ledgerDDL = `
CREATE SCHEMA IF NOT EXISTS forecast;
CREATE TABLE IF NOT EXISTS forecast.schema_migrations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  version TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// ledgerVersionsSQL answers with one aggregated row: an empty ledger still has a
// row to scan, and a missing table is the only way this read comes back empty.
const ledgerVersionsSQL = `SELECT coalesce(string_agg(version, E'\n'), '') FROM forecast.schema_migrations`

const (
	advisoryLockSQL    = `SELECT pg_advisory_xact_lock($1)`
	alreadyAppliedSQL  = `SELECT 1 FROM forecast.schema_migrations WHERE version = $1`
	insertLedgerSQL    = `INSERT INTO forecast.schema_migrations (version, name) VALUES ($1, $2)`
	migrationNameHint  = "want NNNN_name.sql"
	migrationNameInfix = "_"
	migrationFileExt   = ".sql"
	migrationDir       = "migrations"
)

// Migration is one versioned, idempotent SQL file: 0001_snapshots.sql is Version
// "0001", Name "snapshots".
type Migration struct {
	Version string
	Name    string
	SQL     string
}

// Result is what one Apply call did.
type Result struct {
	Applied []string // versions applied by this call, in order
	Pending []string // versions still to apply; a dry run fills this in and writes nothing
	Unknown []string // versions in the ledger that this binary does not embed
	// Already is how many of ms the ledger already listed when this call read it.
	Already int
}

// Pool is the slice of *pgxpool.Pool the engine needs.
type Pool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// All returns the embedded migrations, sorted by version. A malformed file name
// panics: the names are build inputs, not user input, and a skipped migration is
// worse than a process that will not start.
func All() []Migration {
	entries, err := fs.ReadDir(migrationFS, migrationDir)
	if err != nil {
		panic("store: embedded migrations unreadable: " + err.Error())
	}
	ms := make([]Migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		version, name, err := parseName(entry.Name())
		if err != nil {
			panic("store: " + err.Error())
		}
		raw, err := migrationFS.ReadFile(migrationDir + "/" + entry.Name())
		if err != nil {
			panic("store: " + err.Error())
		}
		ms = append(ms, Migration{Version: version, Name: name, SQL: strings.TrimSpace(string(raw))})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })
	return ms
}

// parseName splits 0001_snapshots.sql into its version and name.
func parseName(file string) (string, string, error) {
	base, ok := strings.CutSuffix(file, migrationFileExt)
	if !ok {
		return "", "", fmt.Errorf("bad migration filename %q (%s)", file, migrationNameHint)
	}
	version, name, ok := strings.Cut(base, migrationNameInfix)
	if !ok || len(version) != 4 || name == "" || !allDigits(version) {
		return "", "", fmt.Errorf("bad migration filename %q (%s)", file, migrationNameHint)
	}
	return version, name, nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Apply applies every migration of ms that forecast.schema_migrations does not
// list, in version order. Each migration runs in its own transaction that begins
// by taking pg_advisory_xact_lock(advisoryLockKey) and re-reading the ledger, so
// the migrator a pipeline runs and a Grafana process that auto-applies serialise
// instead of racing; a failing migration leaves behind neither its DDL nor its
// ledger row.
//
// A dry run reads the ledger and reports Pending without writing anything,
// including the ledger itself.
func Apply(ctx context.Context, pool Pool, ms []Migration, dryRun bool) (Result, error) {
	applied, err := ledgerVersions(ctx, pool)
	if err != nil {
		return Result{}, err
	}
	sorted := append([]Migration(nil), ms...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Version < sorted[j].Version })
	known := make(map[string]bool, len(sorted))
	for _, m := range sorted {
		known[m.Version] = true
	}
	res := Result{}
	for version := range applied {
		if !known[version] {
			res.Unknown = append(res.Unknown, version)
		}
	}
	sort.Strings(res.Unknown)
	var pending []Migration
	for _, m := range sorted {
		if applied[m.Version] {
			res.Already++
			continue
		}
		pending = append(pending, m)
	}
	if dryRun {
		for _, m := range pending {
			res.Pending = append(res.Pending, m.Version)
		}
		return res, nil
	}
	for _, m := range pending {
		wrote, err := applyOne(ctx, pool, m)
		if err != nil {
			return res, err
		}
		if wrote {
			res.Applied = append(res.Applied, m.Version)
		}
	}
	return res, nil
}

// ledgerVersions reads the applied set. A missing table is not an error: it means
// nothing has been applied yet.
func ledgerVersions(ctx context.Context, pool Pool) (map[string]bool, error) {
	var raw string
	err := pool.QueryRow(ctx, ledgerVersionsSQL).Scan(&raw)
	if err != nil {
		if isUndefinedTable(err) || errors.Is(err, pgx.ErrNoRows) {
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("forecast store: %w", err)
	}
	applied := map[string]bool{}
	for _, version := range strings.Split(raw, "\n") {
		if version = strings.TrimSpace(version); version != "" {
			applied[version] = true
		}
	}
	return applied, nil
}

// applyOne applies one migration inside one transaction, or reports that another
// migrator committed it while this call waited for the advisory lock.
func applyOne(ctx context.Context, pool Pool, m Migration) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("forecast store: %w", err)
	}
	// Rollback after a commit is a no-op, so every error path can share it.
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, advisoryLockSQL, advisoryLockKey); err != nil {
		return false, fmt.Errorf("forecast store: %w", err)
	}
	if _, err := tx.Exec(ctx, ledgerDDL); err != nil {
		return false, fmt.Errorf("forecast store: %w", err)
	}
	var seen int
	switch err := tx.QueryRow(ctx, alreadyAppliedSQL, m.Version).Scan(&seen); {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return false, fmt.Errorf("forecast store: %w", err)
	default:
		return false, nil
	}
	// The migration SQL and the ledger DDL are passed without arguments, which
	// keeps pgx on the simple protocol: a file holds several statements.
	if _, err := tx.Exec(ctx, m.SQL); err != nil {
		return false, migrationError(m, err)
	}
	if _, err := tx.Exec(ctx, insertLedgerSQL, m.Version, m.Name); err != nil {
		return false, migrationError(m, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, migrationError(m, err)
	}
	return true, nil
}

func migrationError(m Migration, err error) error {
	return fmt.Errorf("migration %s_%s: %w", m.Version, m.Name, err)
}

func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}
