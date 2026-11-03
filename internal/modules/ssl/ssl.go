// Package ssl manages TLS certificates: upload your own, or obtain and
// auto-renew them from an ACME CA (Let's Encrypt / ZeroSSL / Buypass) using
// HTTP-01 or Cloudflare DNS-01 challenges. It implements core.CertService so
// the panel, web service and other modules can serve HTTPS.
package ssl

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"

	"github.com/icebays/al1add/internal/cfapi"
	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/logx"
)

// CADirectories keyed by short name.
var CADirectories = map[string]string{
	"letsencrypt":         "https://acme-v02.api.letsencrypt.org/directory",
	"letsencrypt_staging": "https://acme-staging-v02.api.letsencrypt.org/directory",
	"buypass":             "https://api.buypass.com/acme/directory",
	"zerossl":             "https://acme.zerossl.com/v2/DV90",
	"google":              "https://dv.acme-v02.api.pki.goog/directory",
}

// Cert is one certificate entry.
type Cert struct {
	ID          string   `json:"ID"`
	Name        string   `json:"Name"`
	Domains     []string `json:"Domains"`   // SANs; first is CN
	Source      string   `json:"Source"`    // "acme" | "upload"
	Challenge   string   `json:"Challenge"` // "http-01" | "dns-01"
	CA          string   `json:"CA"`        // key in CADirectories
	Email       string   `json:"Email"`     // ACME account email
	KeyType     string   `json:"KeyType"`   // "ec256" | "ec384" | "rsa2048"
	CFToken     string   `json:"CFToken"`   // Cloudflare token for dns-01
	AutoRenew   bool     `json:"AutoRenew"`
	CertPEM     string   `json:"CertPEM"`  // full chain (upload or issued)
	KeyPEM      string   `json:"KeyPEM"`   // private key
	NotAfter    string   `json:"NotAfter"` // informational
	NotBefore   string   `json:"NotBefore"`
	Issuer      string   `json:"Issuer"`
	LastError   string   `json:"LastError"`
	LastRenewal string   `json:"LastRenewal"`
	Remark      string   `json:"Remark"`
}

// Config of the module.
type Config struct {
	Certs      []Cert `json:"Certs"`
	ACMEKeyPEM string `json:"ACMEKeyPEM"` // reused ACME account key
}

// Module manages certificates.
type Module struct {
	app *core.App
	log *logx.Logger
	cfg Config

	mu        sync.RWMutex
	parsed    map[string]*tls.Certificate // ID -> cert
	byDomain  map[string]*tls.Certificate // domain -> cert (for SNI)
	httpToks  map[string]string           // token -> keyAuth (http-01)
	stopRenew chan struct{}
}

// New creates the module.
func New(app *core.App) *Module {
	m := &Module{app: app, log: app.Logs.L("ssl"), parsed: map[string]*tls.Certificate{}, byDomain: map[string]*tls.Certificate{}, httpToks: map[string]string{}}
	app.Certs = m
	return m
}

func (m *Module) Name() string  { return "ssl" }
func (m *Module) Title() string { return "SSL證書" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset()        { m.cfg = Config{} }

// Normalize assigns IDs and defaults.
func (m *Module) Normalize() {
	for i := range m.cfg.Certs {
		c := &m.cfg.Certs[i]
		if c.ID == "" {
			c.ID = core.RandID()
		}
		if c.KeyType == "" {
			c.KeyType = "ec256"
		}
		if c.CA == "" {
			c.CA = "letsencrypt"
		}
		if c.Challenge == "" {
			c.Challenge = "http-01"
		}
	}
}

// Validate checks certs.
func (m *Module) Validate() error {
	for _, c := range m.cfg.Certs {
		if len(c.Domains) == 0 {
			return fmt.Errorf("certificate %q has no domains", c.Name)
		}
		if c.Source == "upload" && (c.CertPEM == "" || c.KeyPEM == "") {
			return fmt.Errorf("certificate %q: upload source needs cert and key PEM", c.Name)
		}
		if c.Source == "acme" && c.Challenge == "dns-01" && c.CFToken == "" {
			return fmt.Errorf("certificate %q: dns-01 needs a Cloudflare token", c.Name)
		}
	}
	return nil
}

// Start parses stored certs and launches the renewal loop.
func (m *Module) Start() error {
	m.rebuild()
	m.stopRenew = make(chan struct{})
	go m.renewLoop(m.stopRenew)
	return nil
}

// Stop halts the renewal loop.
func (m *Module) Stop() {
	if m.stopRenew != nil {
		close(m.stopRenew)
		m.stopRenew = nil
	}
}

func (m *Module) rebuild() {
	parsed := map[string]*tls.Certificate{}
	byDomain := map[string]*tls.Certificate{}
	for i := range m.cfg.Certs {
		c := &m.cfg.Certs[i]
		if c.CertPEM == "" || c.KeyPEM == "" {
			continue
		}
		tc, err := tls.X509KeyPair([]byte(c.CertPEM), []byte(c.KeyPEM))
		if err != nil {
			m.log.Errorf("certificate %q: %v", c.Name, err)
			continue
		}
		if leaf, err := x509.ParseCertificate(tc.Certificate[0]); err == nil {
			tc.Leaf = leaf
			c.NotAfter = leaf.NotAfter.Format("2006-01-02 15:04:05")
			c.NotBefore = leaf.NotBefore.Format("2006-01-02 15:04:05")
			c.Issuer = leaf.Issuer.CommonName
		}
		parsed[c.ID] = &tc
		for _, d := range c.Domains {
			byDomain[strings.ToLower(d)] = &tc
		}
	}
	m.mu.Lock()
	m.parsed, m.byDomain = parsed, byDomain
	m.mu.Unlock()
}

// GetCertificate implements core.CertService (SNI).
func (m *Module) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	name := strings.ToLower(hello.ServerName)
	if c, ok := m.byDomain[name]; ok {
		return c, nil
	}
	// wildcard match
	if i := strings.IndexByte(name, '.'); i >= 0 {
		if c, ok := m.byDomain["*"+name[i:]]; ok {
			return c, nil
		}
	}
	for _, c := range m.byDomain {
		return c, nil // any cert, better than handshake failure
	}
	return m.selfSigned(name)
}

// HTTP01Handler returns (keyAuth, true) for an ACME challenge path.
func (m *Module) HTTP01(token string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.httpToks[token]
	return v, ok
}

// Status lists certs with expiry.
func (m *Module) Status() any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	type ci struct {
		ID, Name, NotAfter, Issuer, LastError string
		Domains                               []string
		DaysLeft                              int
		Valid                                 bool
	}
	out := []ci{}
	for i := range m.cfg.Certs {
		c := m.cfg.Certs[i]
		info := ci{ID: c.ID, Name: c.Name, NotAfter: c.NotAfter, Issuer: c.Issuer, LastError: c.LastError, Domains: c.Domains}
		if tc := m.parsed[c.ID]; tc != nil && tc.Leaf != nil {
			info.Valid = true
			info.DaysLeft = int(time.Until(tc.Leaf.NotAfter).Hours() / 24)
		}
		out = append(out, info)
	}
	return map[string]any{"certs": out}
}

// Action: issue {ID}, download {ID}.
func (m *Module) Action(ctx context.Context, action string, body json.RawMessage) (any, error) {
	req, err := core.Decode[struct{ ID string }](body)
	if err != nil {
		return nil, err
	}
	switch action {
	case "issue":
		c := m.findCert(req.ID)
		if c == nil {
			return nil, errors.New("certificate not found")
		}
		if err := m.issue(ctx, c); err != nil {
			m.setError(req.ID, err.Error())
			return nil, err
		}
		if err := m.app.SaveCurrent(m.Name()); err != nil {
			return nil, err
		}
		m.rebuild()
		return map[string]any{"ok": true, "notAfter": m.findCert(req.ID).NotAfter}, nil
	}
	return nil, fmt.Errorf("unknown action %q", action)
}

func (m *Module) findCert(id string) *Cert {
	for i := range m.cfg.Certs {
		if m.cfg.Certs[i].ID == id {
			return &m.cfg.Certs[i]
		}
	}
	return nil
}

func (m *Module) setError(id, msg string) {
	if c := m.findCert(id); c != nil {
		c.LastError = msg
		_ = m.app.SaveCurrent(m.Name())
	}
}

// renewLoop renews certificates within 30 days of expiry.
func (m *Module) renewLoop(stop chan struct{}) {
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	m.checkRenewals()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			m.checkRenewals()
		}
	}
}

func (m *Module) checkRenewals() {
	m.mu.RLock()
	var due []string
	for i := range m.cfg.Certs {
		c := m.cfg.Certs[i]
		if c.Source != "acme" || !c.AutoRenew {
			continue
		}
		tc := m.parsed[c.ID]
		if tc == nil || tc.Leaf == nil || time.Until(tc.Leaf.NotAfter) < 30*24*time.Hour {
			due = append(due, c.ID)
		}
	}
	m.mu.RUnlock()
	for _, id := range due {
		c := m.findCert(id)
		if c == nil {
			continue
		}
		m.log.Infof("auto-renewing certificate %q", c.Name)
		if err := m.issue(context.Background(), c); err != nil {
			m.log.Errorf("renew %q: %v", c.Name, err)
			m.setError(id, err.Error())
			continue
		}
		_ = m.app.SaveCurrent(m.Name())
		m.rebuild()
	}
}

func (m *Module) selfSigned(name string) (*tls.Certificate, error) {
	if name == "" {
		name = "al1add.local"
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{name},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// newCFSolver builds a Cloudflare DNS-01 solver for a cert.
func (m *Module) newCFSolver(token string) *cfSolver {
	return &cfSolver{client: cfapi.New(token), created: map[string]string{}}
}

func genKey(keyType string) (crypto.Signer, error) {
	switch keyType {
	case "ec384":
		return ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case "rsa2048":
		return rsaKey(2048)
	default:
		return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	}
}

func encodeKey(k crypto.Signer) (string, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}

var _ = acme.Client{}
