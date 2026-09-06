package plugin

import (
	"context"
	"sync"
	"time"

	forecast "github.com/eduard-kolotushin/timeseries-forecast"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

const (
	// snapshotCacheTTL bounds how long a process trusts a cached snapshot. The
	// overlay (app) process and the alerting (datasource) process each cache
	// independently, and Grafana HA runs one plugin process per replica, so a
	// Retrain elsewhere becomes visible here after at most this long.
	snapshotCacheTTL = 30 * time.Second
	// snapshotCacheMax bounds resident snapshots per process. A minute-of-week
	// baseline is ~20k floats as JSON, so this keeps the cache under ~100 MiB.
	snapshotCacheMax = 256
)

func connectStore(ctx context.Context, dsn string) (SnapshotStore, func()) {
	if dsn == "" {
		return nil, nil
	}
	pg, err := openPostgresStore(ctx, dsn)
	if err != nil {
		log.DefaultLogger.Error("forecast store", "err", err.Error())
		return errStore{err: err}, nil
	}
	return withCache(pg), pg.Close
}

// SnapshotStore persists fitted snapshots. A nil store means persist is off.
type SnapshotStore interface {
	Get(ctx context.Context, orgID int64, key string) (forecast.Snapshot, bool, error)
	Put(ctx context.Context, orgID int64, key string, snap forecast.Snapshot) error
}

type memKey struct {
	org int64
	key string
}

type memoryStore struct {
	mu sync.Mutex
	m  map[memKey]forecast.Snapshot
}

func newMemoryStore() *memoryStore {
	return &memoryStore{m: make(map[memKey]forecast.Snapshot)}
}

func (s *memoryStore) Get(_ context.Context, orgID int64, key string) (forecast.Snapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.m[memKey{org: orgID, key: key}]
	return snap, ok, nil
}

func (s *memoryStore) Put(_ context.Context, orgID int64, key string, snap forecast.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[memKey{org: orgID, key: key}] = snap
	return nil
}

type errStore struct{ err error }

func (s errStore) Get(context.Context, int64, string) (forecast.Snapshot, bool, error) {
	return forecast.Snapshot{}, false, s.err
}

func (s errStore) Put(context.Context, int64, string, forecast.Snapshot) error {
	return s.err
}

type cacheEntry struct {
	snap     forecast.Snapshot
	loadedAt time.Time
}

// cachedStore is a bounded, TTL read-through cache over a SnapshotStore.
// Put writes through and refreshes the local entry; Get falls back to the
// inner store once an entry is older than ttl, so other processes' writes
// become visible without a restart.
type cachedStore struct {
	inner SnapshotStore
	ttl   time.Duration
	max   int
	now   func() time.Time

	mu  sync.Mutex
	mem map[memKey]cacheEntry
}

func withCache(inner SnapshotStore) SnapshotStore {
	if inner == nil {
		return nil
	}
	return newCachedStore(inner, snapshotCacheTTL, snapshotCacheMax)
}

func newCachedStore(inner SnapshotStore, ttl time.Duration, max int) *cachedStore {
	if max < 1 {
		max = 1
	}
	return &cachedStore{
		inner: inner,
		ttl:   ttl,
		max:   max,
		now:   time.Now,
		mem:   make(map[memKey]cacheEntry, max),
	}
}

func (s *cachedStore) lookup(k memKey) (forecast.Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.mem[k]
	if !ok {
		return forecast.Snapshot{}, false
	}
	if s.ttl > 0 && s.now().Sub(e.loadedAt) >= s.ttl {
		delete(s.mem, k)
		return forecast.Snapshot{}, false
	}
	return e.snap, true
}

func (s *cachedStore) remember(k memKey, snap forecast.Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if _, exists := s.mem[k]; !exists && len(s.mem) >= s.max {
		s.evictLocked(now)
	}
	s.mem[k] = cacheEntry{snap: snap, loadedAt: now}
}

// evictLocked drops expired entries, then the oldest entry if still full.
// Runs only when the cache is at capacity, so the O(n) scan is rare and n ≤ max.
func (s *cachedStore) evictLocked(now time.Time) {
	var oldestKey memKey
	var oldest time.Time
	first := true
	for k, e := range s.mem {
		if s.ttl > 0 && now.Sub(e.loadedAt) >= s.ttl {
			delete(s.mem, k)
			continue
		}
		if first || e.loadedAt.Before(oldest) {
			oldestKey, oldest, first = k, e.loadedAt, false
		}
	}
	if len(s.mem) >= s.max && !first {
		delete(s.mem, oldestKey)
	}
}

func (s *cachedStore) forget(k memKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.mem, k)
}

func (s *cachedStore) Get(ctx context.Context, orgID int64, key string) (forecast.Snapshot, bool, error) {
	k := memKey{org: orgID, key: key}
	if snap, ok := s.lookup(k); ok {
		return snap, true, nil
	}
	snap, ok, err := s.inner.Get(ctx, orgID, key)
	if err != nil || !ok {
		return forecast.Snapshot{}, ok, err
	}
	s.remember(k, snap)
	return snap, true, nil
}

func (s *cachedStore) Put(ctx context.Context, orgID int64, key string, snap forecast.Snapshot) error {
	k := memKey{org: orgID, key: key}
	if err := s.inner.Put(ctx, orgID, key, snap); err != nil {
		s.forget(k)
		return err
	}
	s.remember(k, snap)
	return nil
}
