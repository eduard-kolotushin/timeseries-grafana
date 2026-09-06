package plugin

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	forecast "github.com/eduard-kolotushin/timeseries-forecast"
)

type countingStore struct {
	SnapshotStore
	gets int
}

func (c *countingStore) Get(ctx context.Context, orgID int64, key string) (forecast.Snapshot, bool, error) {
	c.gets++
	return c.SnapshotStore.Get(ctx, orgID, key)
}

type failPutStore struct {
	SnapshotStore
	err error
}

func (f failPutStore) Put(context.Context, int64, string, forecast.Snapshot) error {
	return f.err
}

func snapAt(last int64) forecast.Snapshot {
	return forecast.Snapshot{V: 1, Kind: "naive", Last: last, Step: 1_000_000_000, Data: []byte(`{"last":1,"sigma":1}`)}
}

func TestCachedStoreSeesOtherProcessWritesAfterTTL(t *testing.T) {
	ctx := context.Background()
	shared := newMemoryStore()
	clock := time.Unix(1_000_000, 0)
	now := func() time.Time { return clock }

	overlay := newCachedStore(shared, 30*time.Second, 16)
	overlay.now = now
	alerting := newCachedStore(shared, 30*time.Second, 16)
	alerting.now = now
	key := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	if err := overlay.Put(ctx, 1, key, snapAt(1000)); err != nil {
		t.Fatal(err)
	}
	got, ok, err := alerting.Get(ctx, 1, key)
	if err != nil || !ok || got.Last != 1000 {
		t.Fatalf("first read: ok=%v err=%v last=%d", ok, err, got.Last)
	}

	// Retrain in the overlay process. The alerting process still holds the old entry.
	if err := overlay.Put(ctx, 1, key, snapAt(2000)); err != nil {
		t.Fatal(err)
	}
	got, _, _ = alerting.Get(ctx, 1, key)
	if got.Last != 1000 {
		t.Fatalf("within TTL want stale 1000, got %d", got.Last)
	}
	got, _, _ = overlay.Get(ctx, 1, key)
	if got.Last != 2000 {
		t.Fatalf("writer process must see its own Put, got %d", got.Last)
	}

	clock = clock.Add(30 * time.Second)
	got, ok, err = alerting.Get(ctx, 1, key)
	if err != nil || !ok || got.Last != 2000 {
		t.Fatalf("after TTL want 2000, got ok=%v err=%v last=%d", ok, err, got.Last)
	}
}

func TestCachedStoreHitsAvoidInner(t *testing.T) {
	ctx := context.Background()
	inner := &countingStore{SnapshotStore: newMemoryStore()}
	clock := time.Unix(1_000_000, 0)
	c := newCachedStore(inner, time.Minute, 16)
	c.now = func() time.Time { return clock }
	key := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	if _, ok, _ := c.Get(ctx, 1, key); ok {
		t.Fatal("miss expected")
	}
	if inner.gets != 1 {
		t.Fatalf("gets=%d", inner.gets)
	}
	if err := c.Put(ctx, 1, key, snapAt(1)); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, ok, _ := c.Get(ctx, 1, key); !ok {
			t.Fatal("hit expected")
		}
	}
	if inner.gets != 1 {
		t.Fatalf("cached hits must not touch inner, gets=%d", inner.gets)
	}
	clock = clock.Add(time.Minute)
	if _, ok, _ := c.Get(ctx, 1, key); !ok {
		t.Fatal("hit expected after refresh")
	}
	if inner.gets != 2 {
		t.Fatalf("expired entry must re-read inner once, gets=%d", inner.gets)
	}
}

func TestCachedStoreBounded(t *testing.T) {
	ctx := context.Background()
	inner := newMemoryStore()
	clock := time.Unix(1_000_000, 0)
	c := newCachedStore(inner, time.Hour, 3)
	c.now = func() time.Time { return clock }

	for i := range 5 {
		clock = clock.Add(time.Second)
		if err := c.Put(ctx, 1, fmt.Sprintf("%064d", i), snapAt(int64(i))); err != nil {
			t.Fatal(err)
		}
		if n := len(c.mem); n > 3 {
			t.Fatalf("after put %d resident=%d > max", i, n)
		}
	}
	// Oldest entries were evicted from memory but remain readable through inner.
	if _, ok := c.lookup(memKey{org: 1, key: fmt.Sprintf("%064d", 0)}); ok {
		t.Fatal("oldest entry should have been evicted")
	}
	if _, ok := c.lookup(memKey{org: 1, key: fmt.Sprintf("%064d", 4)}); !ok {
		t.Fatal("newest entry should be resident")
	}
	got, ok, err := c.Get(ctx, 1, fmt.Sprintf("%064d", 0))
	if err != nil || !ok || got.Last != 0 {
		t.Fatalf("evicted key must still read through: ok=%v err=%v", ok, err)
	}
}

func TestCachedStoreOrgIsolationAndPutFailure(t *testing.T) {
	ctx := context.Background()
	key := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	c := newCachedStore(newMemoryStore(), time.Hour, 16)
	if err := c.Put(ctx, 1, key, snapAt(1)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := c.Get(ctx, 2, key); ok {
		t.Fatal("org 2 must not see org 1 snapshot")
	}

	boom := errors.New("pg down")
	failing := newCachedStore(failPutStore{SnapshotStore: newMemoryStore(), err: boom}, time.Hour, 16)
	failing.remember(memKey{org: 1, key: key}, snapAt(1))
	if err := failing.Put(ctx, 1, key, snapAt(2)); !errors.Is(err, boom) {
		t.Fatalf("err=%v", err)
	}
	if _, ok := failing.lookup(memKey{org: 1, key: key}); ok {
		t.Fatal("failed Put must not leave a stale local entry")
	}
}
