package vault

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"sync/atomic"
)

// NewServerTLSConfig is the compute listener's TLS configuration: it serves the
// manager's current certificate and requires a client certificate signed by the
// same PKI chain. A fresh config is built per handshake, so a renewed
// certificate (or a rotated CA) is used without restarting the listener.
func NewServerTLSConfig(m *Manager) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			cert, pool, ok := m.Snapshot()
			if !ok {
				return nil, ErrNoCertificate
			}
			return &tls.Config{
				MinVersion:   tls.VersionTLS12,
				Certificates: []tls.Certificate{*cert},
				ClientAuth:   tls.RequireAndVerifyClientCert,
				ClientCAs:    pool,
			}, nil
		},
	}
}

// RotatingTransport is an http.RoundTripper that swaps its underlying transport
// whenever the manager issues a new certificate. The whole transport is replaced
// instead of mutating a tls.Config in place: a handshake in flight keeps a
// consistent certificate and CA pool, and the old idle connections are closed.
type RotatingTransport struct {
	manager    *Manager
	serverName string
	extraCAPEM []byte
	skipVerify bool

	cur atomic.Pointer[http.Transport]
}

// NewClientTransport returns a transport presenting the manager's client
// certificate and trusting its CA chain, unioned with extraCAPEM when set (the
// PVV server CA for the retrain transport). Before the first issue it fails the
// request with ErrNoCertificate.
func NewClientTransport(m *Manager, serverName string, extraCAPEM []byte, skipVerify bool) *RotatingTransport {
	t := &RotatingTransport{
		manager:    m,
		serverName: serverName,
		extraCAPEM: extraCAPEM,
		skipVerify: skipVerify,
	}
	t.Rebuild()
	if m != nil {
		m.SetOnRenew(t.Rebuild)
	}
	return t
}

// Rebuild installs a transport built from the manager's current snapshot.
func (t *RotatingTransport) Rebuild() {
	if t.manager == nil {
		return
	}
	cert, caPEM, ok := t.manager.snapshotPEM()
	if !ok {
		return
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return
	}
	if len(t.extraCAPEM) > 0 {
		pool.AppendCertsFromPEM(t.extraCAPEM)
	}
	next := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			Certificates:       []tls.Certificate{*cert},
			RootCAs:            pool,
			ServerName:         t.serverName,
			InsecureSkipVerify: t.skipVerify,
		},
	}
	old := t.cur.Swap(next)
	if old != nil {
		old.CloseIdleConnections()
	}
}

func (t *RotatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr := t.cur.Load()
	if tr == nil {
		return nil, ErrNoCertificate
	}
	return tr.RoundTrip(req)
}

// NewClientTLSConfig is for TLS clients that take a *tls.Config instead of an
// http.RoundTripper, such as pgx. The client certificate rotates with the
// manager (every new connection picks up the current leaf); the root pool is the
// CA chain of the current issue, so rotating the Vault CA itself needs a
// restart. Call after Manager.Start's first successful issue.
func NewClientTLSConfig(m *Manager, serverName string) (*tls.Config, error) {
	if m == nil {
		return nil, ErrNoCertificate
	}
	_, caPEM, ok := m.snapshotPEM()
	if !ok {
		return nil, ErrNoCertificate
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("forecast: vault: CA chain contains no certificates")
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: serverName,
		RootCAs:    pool,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			cert, _, ok := m.Snapshot()
			if !ok || cert == nil {
				return nil, ErrNoCertificate
			}
			return cert, nil
		},
	}, nil
}
