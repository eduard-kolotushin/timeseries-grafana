package plugin

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	forecast "github.com/eduard-kolotushin/timeseries-forecast"
	"github.com/eduard-kolotushin/timeseries-grafana/pkg/vault"
)

// TestLazyStoresRecoverAfterAFailedConnect pins the recovery the store needs when
// Vault has not issued the mTLS certificate at startup: the first operation
// reports the cause, and a later operation opens the pool once the source is
// available instead of returning the first error for the process lifetime.
func TestLazyStoresRecoverAfterAFailedConnect(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	inner := newMemoryStore()
	want := forecast.Snapshot{V: 1, Kind: "naive", Last: 1, Step: 1_000_000_000}
	var attempts atomic.Int32
	lazy := newLazyStores(func(context.Context) (SnapshotStore, ScheduleStore, func(), error) {
		if attempts.Add(1) == 1 {
			return nil, nil, nil, vault.ErrNoCertificate
		}
		return inner, nil, nil, nil
	})

	if err := lazy.Put(ctx, 1, "key", want); !errors.Is(err, vault.ErrNoCertificate) {
		t.Fatalf("first put: err = %v, want ErrNoCertificate", err)
	}
	if err := lazy.Put(ctx, 1, "key", want); err != nil {
		t.Fatalf("recovered put: %v", err)
	}
	got, ok, err := lazy.Get(ctx, 1, "key")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Kind != want.Kind || got.Last != want.Last {
		t.Fatalf("get = %+v, want %+v", got, want)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("connect attempts = %d, want 2", got)
	}
}

// TestLazyStoresCloseIsFinal: closing before the first connect releases nothing
// and makes later operations fail instead of opening a pool the disposer no
// longer owns; closing a connected pool runs its close exactly once.
func TestLazyStoresCloseIsFinal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var closes atomic.Int32
	lazy := newLazyStores(func(context.Context) (SnapshotStore, ScheduleStore, func(), error) {
		return newMemoryStore(), nil, func() { closes.Add(1) }, nil
	})
	lazy.Close()
	if got := closes.Load(); got != 0 {
		t.Fatalf("closes = %d before a connect, want 0", got)
	}
	if err := lazy.Ping(ctx); !errors.Is(err, errLazyClosed) {
		t.Fatalf("ping after close = %v, want errLazyClosed", err)
	}
	lazy.Close()
	if got := closes.Load(); got != 0 {
		t.Fatalf("closes = %d after a second close, want 0", got)
	}

	open := newLazyStores(func(context.Context) (SnapshotStore, ScheduleStore, func(), error) {
		return newMemoryStore(), nil, func() { closes.Add(1) }, nil
	})
	if err := open.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	open.Close()
	open.Close()
	if got := closes.Load(); got != 1 {
		t.Fatalf("closes = %d after closing a connected store twice, want 1", got)
	}
}

// TestLazyStoresWithoutSchedules: the datasource's lazy store has no scheduler;
// a schedule call must answer an error rather than a nil dereference.
func TestLazyStoresWithoutSchedules(t *testing.T) {
	t.Parallel()
	lazy := newLazyStores(func(context.Context) (SnapshotStore, ScheduleStore, func(), error) {
		return newMemoryStore(), nil, nil, nil
	})
	if _, err := lazy.List(context.Background(), 1); !errors.Is(err, errNoScheduleStore) {
		t.Fatalf("list = %v, want errNoScheduleStore", err)
	}
}
