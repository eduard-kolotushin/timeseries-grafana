package plugin

import (
	"context"
	"sync"

	forecast "github.com/eduard-kolotushin/timeseries-forecast"
)

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

type cachedStore struct {
	inner SnapshotStore
	mem   sync.Map
}

type errStore struct{ err error }

func (s errStore) Get(context.Context, int64, string) (forecast.Snapshot, bool, error) {
	return forecast.Snapshot{}, false, s.err
}

func (s errStore) Put(context.Context, int64, string, forecast.Snapshot) error {
	return s.err
}

func withCache(inner SnapshotStore) SnapshotStore {
	if inner == nil {
		return nil
	}
	return &cachedStore{inner: inner}
}

func cacheMapKey(orgID int64, key string) memKey {
	return memKey{org: orgID, key: key}
}

func (s *cachedStore) Get(ctx context.Context, orgID int64, key string) (forecast.Snapshot, bool, error) {
	k := cacheMapKey(orgID, key)
	if v, ok := s.mem.Load(k); ok {
		return v.(forecast.Snapshot), true, nil
	}
	snap, ok, err := s.inner.Get(ctx, orgID, key)
	if err != nil || !ok {
		return forecast.Snapshot{}, ok, err
	}
	s.mem.Store(k, snap)
	return snap, true, nil
}

func (s *cachedStore) Put(ctx context.Context, orgID int64, key string, snap forecast.Snapshot) error {
	if err := s.inner.Put(ctx, orgID, key, snap); err != nil {
		return err
	}
	s.mem.Store(cacheMapKey(orgID, key), snap)
	return nil
}
