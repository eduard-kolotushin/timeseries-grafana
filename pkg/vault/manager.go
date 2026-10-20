package vault

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"sync"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

// Manager keeps one certificate (and its issuing CA chain) fresh: it issues at
// start, then re-issues at half the leaf's remaining lifetime. A failed refresh
// keeps the last good certificate and retries every retry interval, so a short
// Vault outage does not take the listener or the client down before the current
// leaf expires; once it has expired, handshakes fail and the failure is visible
// rather than silently downgraded.
type Manager struct {
	client *Client
	mount  string
	role   string
	cn     string
	ttl    time.Duration

	mu      sync.RWMutex
	snap    *certSnapshot
	onRenew func()

	now   func() time.Time
	retry time.Duration
}

type certSnapshot struct {
	cert  *tls.Certificate
	pool  *x509.CertPool
	caPEM []byte
}

// NewManager binds a manager to one PKI role and common name.
func NewManager(c *Client, mount, role, cn string, ttl time.Duration) *Manager {
	if c == nil {
		return nil
	}
	return &Manager{
		client: c,
		mount:  defaultString(mount, DefaultPKIMount),
		role:   role,
		cn:     cn,
		ttl:    ttl,
		now:    time.Now,
		retry:  30 * time.Second,
	}
}

// Start issues the first certificate and launches the renewal loop. A failed
// first issue is returned to the caller (the compute service treats it as fatal)
// while the loop keeps retrying, so a plugin whose Vault was briefly unavailable
// can recover without a restart.
func (m *Manager) Start(ctx context.Context) error {
	firstErr := m.issue(ctx)
	failed := firstErr != nil
	go m.loop(ctx, failed)
	return firstErr
}

// Snapshot returns the current certificate and CA pool, or ok=false before the
// first successful issue.
func (m *Manager) Snapshot() (*tls.Certificate, *x509.CertPool, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.snap == nil {
		return nil, nil, false
	}
	return m.snap.cert, m.snap.pool, true
}

func (m *Manager) snapshotPEM() (*tls.Certificate, []byte, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.snap == nil {
		return nil, nil, false
	}
	return m.snap.cert, m.snap.caPEM, true
}

// SetOnRenew registers a callback invoked after every successful issue. A
// manager has one consumer, so one callback is enough.
func (m *Manager) SetOnRenew(fn func()) {
	m.mu.Lock()
	m.onRenew = fn
	m.mu.Unlock()
}

func (m *Manager) issue(ctx context.Context) error {
	issued, err := m.client.IssueCertificate(ctx, m.mount, m.role, m.cn, m.ttl)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.snap = &certSnapshot{cert: issued.Certificate, pool: issued.CAPool, caPEM: issued.CAPEM}
	cb := m.onRenew
	m.mu.Unlock()
	if cb != nil {
		cb()
	}
	return nil
}

func (m *Manager) loop(ctx context.Context, failed bool) {
	for {
		delay := m.retry
		if !failed {
			delay = m.halfLife()
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := m.issue(ctx); err != nil {
			log.DefaultLogger.Warn("forecast: vault: refresh failed", "mount", m.mount, "role", m.role, "err", err.Error())
			failed = true
			continue
		}
		failed = false
	}
}

// halfLife is the remaining leaf lifetime divided by two, never below one
// second (tests use very short-lived certificates).
func (m *Manager) halfLife() time.Duration {
	m.mu.RLock()
	snap := m.snap
	m.mu.RUnlock()
	if snap == nil || snap.cert == nil || snap.cert.Leaf == nil {
		return m.retry
	}
	remaining := snap.cert.Leaf.NotAfter.Sub(m.now())
	if remaining <= 0 {
		return m.retry
	}
	delay := remaining / 2
	if delay < time.Second {
		delay = time.Second
	}
	return delay
}
