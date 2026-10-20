// Package vault issues short-lived TLS certificates from a HashiCorp Vault PKI
// secrets engine.
//
// It is deliberately a small stdlib client: login (static token, AppRole, or
// Kubernetes), pki issue, and token renewal are the only calls this plugin
// makes, so pulling in hashicorp/vault/api would add a dependency tree for two
// REST endpoints. The package knows nothing about the compute service or PVV;
// callers decide which role and common name a certificate is for.
package vault

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultTimeout bounds one Vault HTTP call.
	DefaultTimeout = 10 * time.Second

	// DefaultAppRoleMount and DefaultK8sMount are the conventional auth mounts.
	DefaultAppRoleMount = "approle"
	DefaultK8sMount     = "kubernetes"
	DefaultK8sJWTPath   = "/var/run/secrets/kubernetes.io/serviceaccount/token"

	// DefaultPKIMount is where a Vault PKI secrets engine normally lives.
	DefaultPKIMount = "pki"

	maxResponseBytes = 1 << 20
)

var (
	// ErrNoCertificate is returned by a transport or listener before the first
	// certificate has been issued.
	ErrNoCertificate = errors.New("forecast: vault: no certificate issued yet")

	// ErrNoAuth means the configuration named a Vault address but no auth method.
	ErrNoAuth = errors.New("forecast: vault: no auth method (token, approle, or kubernetes)")
)

// AuthConfig is the login material. Token wins over AppRole, which wins over
// Kubernetes; only one method is used.
type AuthConfig struct {
	Token        string
	RoleID       string
	SecretID     string
	AppRoleMount string
	K8sRole      string
	K8sMount     string
	K8sJWTPath   string
}

// Config is one Vault endpoint and how to authenticate to it.
type Config struct {
	Addr          string
	Namespace     string
	Auth          AuthConfig
	CACertFile    string
	TLSSkipVerify bool
	// ClientMTLS presents a client certificate on the Vault connection itself.
	// The certificate cannot be issued by this Vault (it is needed to reach it),
	// so the pair is file-based and loaded once when the client is built.
	ClientMTLS     bool
	ClientCertFile string
	ClientKeyFile  string
	Timeout        time.Duration
}

// Client talks to one Vault server. It is safe for concurrent use.
type Client struct {
	cfg        Config
	http       *http.Client
	loginBased bool

	mu        sync.Mutex
	token     string
	expiresAt time.Time
	lease     time.Duration
	renewable bool
	lookedUp  bool
}

// IssuedCert is one Vault PKI issue response.
type IssuedCert struct {
	Certificate *tls.Certificate
	CAPool      *x509.CertPool
	// CAPEM is the same chain as CAPool, kept as bytes so a client transport can
	// union it with another CA file.
	CAPEM    []byte
	NotAfter time.Time
}

// NewClient validates the configuration and returns a client. It performs no
// network call: login and lookup-self happen on first use, so a service can
// report a configuration error without depending on Vault being up.
func NewClient(cfg Config) (*Client, error) {
	cfg.Addr = strings.TrimRight(strings.TrimSpace(cfg.Addr), "/")
	if cfg.Addr == "" {
		return nil, errors.New("forecast: vault: address is required")
	}
	for _, p := range []struct{ name, value string }{
		{"FORECAST_VAULT_CA_CERT", cfg.CACertFile},
		{"FORECAST_VAULT_CLIENT_CERT", cfg.ClientCertFile},
		{"FORECAST_VAULT_CLIENT_KEY", cfg.ClientKeyFile},
	} {
		if err := rejectPEMContent(p.name, p.value); err != nil {
			return nil, err
		}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	cfg.Auth.AppRoleMount = defaultString(cfg.Auth.AppRoleMount, DefaultAppRoleMount)
	cfg.Auth.K8sMount = defaultString(cfg.Auth.K8sMount, DefaultK8sMount)
	cfg.Auth.K8sJWTPath = defaultString(cfg.Auth.K8sJWTPath, DefaultK8sJWTPath)
	cfg.Auth.Token = strings.TrimSpace(cfg.Auth.Token)
	cfg.Auth.RoleID = strings.TrimSpace(cfg.Auth.RoleID)
	cfg.Auth.SecretID = strings.TrimSpace(cfg.Auth.SecretID)
	cfg.Auth.K8sRole = strings.TrimSpace(cfg.Auth.K8sRole)

	loginBased := cfg.Auth.Token == ""
	if loginBased {
		switch {
		case cfg.Auth.RoleID != "" || cfg.Auth.SecretID != "":
			if cfg.Auth.RoleID == "" || cfg.Auth.SecretID == "" {
				return nil, errors.New("forecast: vault: both FORECAST_VAULT_ROLE_ID and FORECAST_VAULT_SECRET_ID are required")
			}
		case cfg.Auth.K8sRole != "":
		default:
			return nil, ErrNoAuth
		}
	}

	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.TLSSkipVerify}
	if cfg.CACertFile != "" {
		pemBytes, err := os.ReadFile(cfg.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("forecast: vault: read ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, errors.New("forecast: vault: ca contains no certificates")
		}
		tlsCfg.RootCAs = pool
	}
	if cfg.ClientMTLS {
		if cfg.ClientCertFile == "" || cfg.ClientKeyFile == "" {
			return nil, errors.New("forecast: vault: both FORECAST_VAULT_CLIENT_CERT and FORECAST_VAULT_CLIENT_KEY are required with FORECAST_VAULT_MTLS=true")
		}
		pair, err := tls.LoadX509KeyPair(cfg.ClientCertFile, cfg.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("forecast: vault: load client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{pair}
	}
	return &Client{
		cfg:        cfg,
		http:       &http.Client{Timeout: cfg.Timeout, Transport: &http.Transport{TLSClientConfig: tlsCfg}},
		loginBased: loginBased,
		token:      cfg.Auth.Token,
	}, nil
}

func rejectPEMContent(name, value string) error {
	if strings.Contains(value, "-----BEGIN") {
		return fmt.Errorf("forecast: vault: %s must be a file path, not PEM content", name)
	}
	return nil
}

// IssueCertificate asks the PKI engine for a certificate. The returned leaf is
// populated so callers can schedule renewal from NotAfter.
func (c *Client) IssueCertificate(ctx context.Context, mount, role, commonName string, ttl time.Duration) (*IssuedCert, error) {
	mount = defaultString(strings.TrimSpace(mount), DefaultPKIMount)
	role = strings.TrimSpace(role)
	commonName = strings.TrimSpace(commonName)
	if role == "" {
		return nil, errors.New("forecast: vault: pki role is required")
	}
	if commonName == "" {
		return nil, errors.New("forecast: vault: common name is required")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	path := "/v1/" + mount + "/issue/" + role
	body := map[string]string{"common_name": commonName, "ttl": ttl.String(), "alt_names": commonName}
	for attempt := range 2 {
		token, err := c.ensureToken(ctx)
		if err != nil {
			return nil, err
		}
		status, payload, err := c.do(ctx, http.MethodPost, path, token, body)
		if err != nil {
			return nil, fmt.Errorf("forecast: vault: issue %s/%s: %w", mount, role, err)
		}
		if status == http.StatusForbidden && c.loginBased && attempt == 0 {
			if err := c.relogin(ctx); err != nil {
				return nil, err
			}
			continue
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("forecast: vault: issue %s/%s: %s %s", mount, role, http.StatusText(status), truncate(payload, 200))
		}
		return parseIssue(payload)
	}
	return nil, errors.New("forecast: vault: issue failed after a token refresh")
}

func parseIssue(payload []byte) (*IssuedCert, error) {
	var res struct {
		Data struct {
			Certificate string   `json:"certificate"`
			PrivateKey  string   `json:"private_key"`
			IssuingCA   string   `json:"issuing_ca"`
			CAChain     []string `json:"ca_chain"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &res); err != nil {
		return nil, fmt.Errorf("forecast: vault: issue response: %w", err)
	}
	if strings.TrimSpace(res.Data.Certificate) == "" || strings.TrimSpace(res.Data.PrivateKey) == "" {
		return nil, errors.New("forecast: vault: issue response has no certificate/private_key")
	}
	pair, err := tls.X509KeyPair([]byte(res.Data.Certificate), []byte(res.Data.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("forecast: vault: parse issued certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("forecast: vault: parse issued leaf: %w", err)
	}
	pair.Leaf = leaf

	chain := res.Data.CAChain
	if len(chain) == 0 && res.Data.IssuingCA != "" {
		chain = []string{res.Data.IssuingCA}
	}
	pool := x509.NewCertPool()
	var caPEM []byte
	for _, certPEM := range chain {
		if certPEM == "" {
			continue
		}
		if pool.AppendCertsFromPEM([]byte(certPEM)) {
			caPEM = append(caPEM, []byte(certPEM)...)
		}
	}
	if len(caPEM) == 0 {
		return nil, errors.New("forecast: vault: issue response has no CA chain")
	}
	return &IssuedCert{Certificate: &pair, CAPool: pool, CAPEM: caPEM, NotAfter: leaf.NotAfter}, nil
}

func (c *Client) ensureToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loginBased {
		if c.token == "" || c.lease > 0 && time.Until(c.expiresAt) < c.lease/4 {
			if err := c.loginLocked(ctx); err != nil {
				return "", err
			}
		}
		return c.token, nil
	}
	if c.token == "" {
		return "", ErrNoAuth
	}
	if !c.lookedUp {
		c.lookedUp = true
		if err := c.lookupSelfLocked(ctx); err != nil {
			// A token without lookup-self permission still works for pki issue.
			// Leave the lease unknown rather than refusing to start.
			c.lease = 0
		}
	}
	if c.renewable && c.lease > 0 && time.Until(c.expiresAt) < c.lease/4 {
		if err := c.renewSelfLocked(ctx); err != nil {
			// Keep the token: the issue call reports the real failure if the
			// token really is unusable.
			c.lease = 0
		}
	}
	return c.token, nil
}

func (c *Client) relogin(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loginLocked(ctx)
}

func (c *Client) loginLocked(ctx context.Context) error {
	var path string
	body := map[string]string{}
	switch {
	case c.cfg.Auth.RoleID != "":
		path = "/v1/auth/" + c.cfg.Auth.AppRoleMount + "/login"
		body["role_id"] = c.cfg.Auth.RoleID
		body["secret_id"] = c.cfg.Auth.SecretID
	case c.cfg.Auth.K8sRole != "":
		jwt, err := os.ReadFile(c.cfg.Auth.K8sJWTPath)
		if err != nil {
			return fmt.Errorf("forecast: vault: read kubernetes jwt: %w", err)
		}
		jwtText := strings.TrimSpace(string(jwt))
		if jwtText == "" {
			return errors.New("forecast: vault: kubernetes jwt is empty")
		}
		path = "/v1/auth/" + c.cfg.Auth.K8sMount + "/login"
		body["role"] = c.cfg.Auth.K8sRole
		body["jwt"] = jwtText
	default:
		return ErrNoAuth
	}
	status, payload, err := c.do(ctx, http.MethodPost, path, "", body)
	if err != nil {
		return fmt.Errorf("forecast: vault: login: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("forecast: vault: login: %s %s", http.StatusText(status), truncate(payload, 200))
	}
	var res struct {
		Auth struct {
			ClientToken   string  `json:"client_token"`
			LeaseDuration float64 `json:"lease_duration"`
			Renewable     bool    `json:"renewable"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(payload, &res); err != nil {
		return fmt.Errorf("forecast: vault: login response: %w", err)
	}
	if res.Auth.ClientToken == "" {
		return errors.New("forecast: vault: login response has no client_token")
	}
	c.token = res.Auth.ClientToken
	c.lease = time.Duration(res.Auth.LeaseDuration * float64(time.Second))
	c.renewable = res.Auth.Renewable
	c.expiresAt = time.Now().Add(c.lease)
	return nil
}

func (c *Client) lookupSelfLocked(ctx context.Context) error {
	status, payload, err := c.do(ctx, http.MethodGet, "/v1/auth/token/lookup-self", c.token, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("forecast: vault: lookup-self: %s", http.StatusText(status))
	}
	var res struct {
		Data struct {
			TTL       float64 `json:"ttl"`
			Renewable bool    `json:"renewable"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &res); err != nil {
		return err
	}
	c.lease = time.Duration(res.Data.TTL * float64(time.Second))
	c.renewable = res.Data.Renewable
	c.expiresAt = time.Now().Add(c.lease)
	return nil
}

func (c *Client) renewSelfLocked(ctx context.Context) error {
	status, payload, err := c.do(ctx, http.MethodPost, "/v1/auth/token/renew-self", c.token, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("forecast: vault: renew-self: %s", http.StatusText(status))
	}
	var res struct {
		Auth struct {
			LeaseDuration float64 `json:"lease_duration"`
			Renewable     bool    `json:"renewable"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(payload, &res); err != nil {
		return err
	}
	c.lease = time.Duration(res.Auth.LeaseDuration * float64(time.Second))
	c.renewable = res.Auth.Renewable
	c.expiresAt = time.Now().Add(c.lease)
	return nil
}

func (c *Client) do(ctx context.Context, method, path, token string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.Addr+path, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("X-Vault-Token", token)
	}
	if c.cfg.Namespace != "" {
		req.Header.Set("X-Vault-Namespace", c.cfg.Namespace)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, payload, nil
}

func defaultString(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func truncate(payload []byte, n int) string {
	text := strings.TrimSpace(string(payload))
	if len(text) <= n {
		return text
	}
	return text[:n] + "…"
}
