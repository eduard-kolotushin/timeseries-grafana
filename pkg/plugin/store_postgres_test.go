package plugin

import (
	"context"
	"os"
	"testing"

	forecast "github.com/eduard-kolotushin/timeseries-forecast"
)

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
