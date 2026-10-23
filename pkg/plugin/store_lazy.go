package plugin

import (
	"context"
	"errors"
	"sync"
	"time"

	forecast "github.com/eduard-kolotushin/timeseries-forecast"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

// lazyStores opens the Postgres pool on the first operation whose connect
// succeeds. openPostgresStore needs the Vault store certificate at pool-build
// time, and a failed first issue used to be cached as a permanent error store:
// every fit and probe stayed broken until a restart even though the manager
// retries the issue every retry interval. Each operation now retries the open,
// so the process recovers on the first success without a restart.
type lazyStores struct {
	connect func(context.Context) (SnapshotStore, ScheduleStore, func(), error)

	mu      sync.Mutex
	store   SnapshotStore
	sched   ScheduleStore
	closeFn func()
	closed  bool
}

var (
	errLazyClosed      = errors.New("forecast: store closed")
	errNoScheduleStore = errors.New("forecast: schedule store not configured")
)

func newLazyStores(connect func(context.Context) (SnapshotStore, ScheduleStore, func(), error)) *lazyStores {
	return &lazyStores{connect: connect}
}

func (l *lazyStores) ensure(ctx context.Context) (SnapshotStore, ScheduleStore, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, nil, errLazyClosed
	}
	if l.store != nil {
		return l.store, l.sched, nil
	}
	store, sched, closeFn, err := l.connect(ctx)
	if err != nil {
		log.DefaultLogger.Warn("forecast store connect failed", "err", err.Error())
		return nil, nil, err
	}
	l.store, l.sched, l.closeFn = store, sched, closeFn
	return store, sched, nil
}

// Close releases the pool if one was opened and prevents a later operation from
// opening another. It is safe before the first successful connect.
func (l *lazyStores) Close() {
	l.mu.Lock()
	l.closed = true
	fn := l.closeFn
	l.closeFn = nil
	l.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (l *lazyStores) Get(ctx context.Context, orgID int64, key string) (forecast.Snapshot, bool, error) {
	store, _, err := l.ensure(ctx)
	if err != nil {
		return forecast.Snapshot{}, false, err
	}
	return store.Get(ctx, orgID, key)
}

func (l *lazyStores) Put(ctx context.Context, orgID int64, key string, snap forecast.Snapshot) error {
	store, _, err := l.ensure(ctx)
	if err != nil {
		return err
	}
	return store.Put(ctx, orgID, key, snap)
}

func (l *lazyStores) Ping(ctx context.Context) error {
	store, _, err := l.ensure(ctx)
	if err != nil {
		return err
	}
	return store.Ping(ctx)
}

func (l *lazyStores) schedule(ctx context.Context) (ScheduleStore, error) {
	_, sched, err := l.ensure(ctx)
	if err != nil {
		return nil, err
	}
	if sched == nil {
		return nil, errNoScheduleStore
	}
	return sched, nil
}

func (l *lazyStores) List(ctx context.Context, orgID int64) ([]ScheduleRow, error) {
	sched, err := l.schedule(ctx)
	if err != nil {
		return nil, err
	}
	return sched.List(ctx, orgID)
}

func (l *lazyStores) Row(ctx context.Context, orgID int64, scope, key string) (ScheduleRow, bool, error) {
	sched, err := l.schedule(ctx)
	if err != nil {
		return ScheduleRow{}, false, err
	}
	return sched.Row(ctx, orgID, scope, key)
}

func (l *lazyStores) Upsert(ctx context.Context, orgID int64, row ScheduleRow) error {
	sched, err := l.schedule(ctx)
	if err != nil {
		return err
	}
	return sched.Upsert(ctx, orgID, row)
}

func (l *lazyStores) Delete(ctx context.Context, orgID int64, scope, key string) error {
	sched, err := l.schedule(ctx)
	if err != nil {
		return err
	}
	return sched.Delete(ctx, orgID, scope, key)
}

func (l *lazyStores) Due(ctx context.Context, orgID int64, key string, now time.Time) (bool, error) {
	sched, err := l.schedule(ctx)
	if err != nil {
		return false, err
	}
	return sched.Due(ctx, orgID, key, now)
}

func (l *lazyStores) Claim(ctx context.Context, orgs []int64, owner string, lease time.Duration, limit int) ([]ScheduleRow, error) {
	sched, err := l.schedule(ctx)
	if err != nil {
		return nil, err
	}
	return sched.Claim(ctx, orgs, owner, lease, limit)
}

func (l *lazyStores) Finish(ctx context.Context, owner string, orgID int64, scope, key string, next time.Time, status string, attempts int) error {
	sched, err := l.schedule(ctx)
	if err != nil {
		return err
	}
	return sched.Finish(ctx, owner, orgID, scope, key, next, status, attempts)
}

func (l *lazyStores) Extend(ctx context.Context, owner string, orgID int64, scope, key string, lease time.Duration) (bool, error) {
	sched, err := l.schedule(ctx)
	if err != nil {
		return false, err
	}
	return sched.Extend(ctx, owner, orgID, scope, key, lease)
}

func (l *lazyStores) Identify(ctx context.Context, orgID int64, key string, prov PanelProvenance) error {
	sched, err := l.schedule(ctx)
	if err != nil {
		return err
	}
	return sched.Identify(ctx, orgID, key, prov)
}

func (l *lazyStores) Supersede(ctx context.Context, orgID int64, dashboardUID string, panelID int, keys []string) error {
	sched, err := l.schedule(ctx)
	if err != nil {
		return err
	}
	return sched.Supersede(ctx, orgID, dashboardUID, panelID, keys)
}
