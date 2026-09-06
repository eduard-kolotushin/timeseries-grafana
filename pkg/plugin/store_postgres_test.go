package plugin

import (
	"context"
	"os"
	"testing"
	"time"

	forecast "github.com/eduard-kolotushin/timeseries-forecast"
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
	first := s.lastAttempt
	// Within the backoff window the cached error is returned without redialing.
	if _, _, err2 := s.Get(ctx, 1, "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"); err2 == nil || s.lastAttempt != first {
		t.Fatalf("expected throttled retry, err=%v attempt moved=%v", err2, s.lastAttempt != first)
	}
	clock = clock.Add(ensureRetryAfter)
	if _, _, err3 := s.Get(ctx, 1, "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"); err3 == nil || s.lastAttempt == first {
		t.Fatalf("expected a fresh attempt after backoff, err=%v", err3)
	}
	if s.ready {
		t.Fatal("store must not be marked ready")
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
