// Command gpx_forecast_migrate applies this plugin's PostgreSQL migrations
// without starting the plugin, so a CI/CD pipeline can prepare the database
// before Grafana starts.
//
// It reads the same store settings as the plugin — process env FORECAST_STORE_*,
// then the GF_PLUGIN_* variables Grafana exports for the plugin's ini sections —
// and the same embedded migration files (pkg/store), which the plugin also
// applies at its first store use. Running it is therefore optional: an
// installation that never runs it still converges, one request late.
//
//	gpx_forecast_migrate [--dsn postgres://…] [--dry-run] [--timeout 60s]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/eduard-kolotushin/timeseries-grafana/pkg/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// run resolves the DSN, applies the embedded migrations and returns the process
// exit code: 0 when the schema is up to date (including a ledger written by a
// newer deployment), 1 on any error. It takes its environment and streams as
// arguments so a test can drive it without a subprocess.
func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gpx_forecast_migrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var (
		dsn     = flags.String("dsn", "", "store DSN; overrides FORECAST_STORE_URL / FORECAST_STORE_HOST")
		dryRun  = flags.Bool("dry-run", false, "report the pending migrations without applying them")
		timeout = flags.Duration("timeout", 60*time.Second, "deadline for the whole run")
	)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	resolved := strings.TrimSpace(*dsn)
	if resolved == "" {
		resolved = store.DSNFrom(store.EnvLookup{Getenv: getenv}, "")
	}
	if resolved == "" {
		fmt.Fprintln(stderr, "gpx_forecast_migrate: no store DSN: pass --dsn or set FORECAST_STORE_URL / FORECAST_STORE_HOST")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	pool, err := pgxpool.New(ctx, resolved)
	if err != nil {
		fmt.Fprintf(stderr, "gpx_forecast_migrate: %v\n", err)
		return 1
	}
	defer pool.Close()

	ms := store.All()
	res, err := store.Apply(ctx, pool, ms, *dryRun)
	if err != nil {
		fmt.Fprintf(stderr, "gpx_forecast_migrate: %v\n", err)
		return 1
	}
	names := make(map[string]string, len(ms))
	for _, m := range ms {
		names[m.Version] = m.Version + "_" + m.Name
	}
	if len(res.Unknown) > 0 {
		fmt.Fprintf(stderr, "warning: forecast.schema_migrations has versions this binary does not embed: %s (applied by a newer deployment)\n", strings.Join(res.Unknown, " "))
	}
	switch {
	case len(res.Applied) > 0:
		for _, version := range res.Applied {
			fmt.Fprintf(stdout, "applied %s\n", names[version])
		}
	case len(res.Pending) > 0:
		for _, version := range res.Pending {
			fmt.Fprintf(stdout, "pending %s\n", names[version])
		}
	default:
		fmt.Fprintf(stdout, "nothing to apply (%d known, %d applied)\n", len(ms), res.Already)
	}
	return 0
}
