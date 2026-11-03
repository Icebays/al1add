// Package webservice provides HTTP(S) front-ends: reverse proxy, redirect,
// URL jump and static file serving, matched by domain and path, with BasicAuth,
// a login page backed by security groups, IP filtering, CORS and optional WAF.
package webservice

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/logx"
)

// SubRule matches a domain (+optional path prefix) and serves it.
type SubRule struct {
	ID          string   `json:"ID"`
	Enable      bool     `json:"Enable"`
	FrontDomain string   `json:"FrontDomain"` // host or host/path; empty = match any
	Type        string   `json:"Type"`        // proxy | redirect | redirect301 | static
	Backend     string   `json:"Backend"`     // url for proxy/redirect; dir path or storage:ID for static
	// proxy options
	KeepClientIP     bool `json:"KeepClientIP"`     // set X-Forwarded-For
	AppendClientIP   bool `json:"AppendClientIP"`   // append rather than replace
	IgnoreBackendTLS bool `json:"IgnoreBackendTLS"` // skip backend cert verify
	CORS             bool `json:"CORS"`
	HostHeaderOrigin bool `json:"HostHeaderOrigin"` // keep the original Host header
	// auth
	BasicUser string `json:"BasicUser"`
	BasicPass string `json:"BasicPass"`
	WebLogin  bool   `json:"WebLogin"`  // login page instead of BasicAuth
	GroupID   string `json:"GroupID"`   // security group for login / authorization
	GroupMode string `json:"GroupMode"` // when WebLogin is off: strict|supplement|""
	// filtering / waf
	IPFilter   string `json:"IPFilter"`
	WAFProfile string `json:"WAFProfile"`
	Remark     string `json:"Remark"`
}

// Rule is one listening front-end.
type Rule struct {
	ID       string    `json:"ID"`
	Name     string    `json:"Name"`
	Enable   bool      `json:"Enable"`
	ListenIP string    `json:"ListenIP"`
	Port     int       `json:"Port"`
	TLS      bool      `json:"TLS"`
	SubRules []SubRule `json:"SubRules"`
	Remark   string    `json:"Remark"`
}

// Config of the module.
type Config struct {
	Rules []Rule `json:"Rules"`
}

type http01Lookup interface {
	HTTP01(token string) (string, bool)
}

type listener struct {
	rule   Rule
	server *http.Server
	ln     net.Listener
	hits   map[string]*int64
}

// Module runs web service rules.
type Module struct {
	app       *core.App
	log       *logx.Logger
	cfg       Config
	mu        sync.Mutex
	listeners map[string]*listener
}

// New creates the module.
func New(app *core.App) *Module {
	return &Module{app: app, log: app.Logs.L("webservice"), listeners: map[string]*listener{}}
}

func (m *Module) Name() string  { return "webservice" }
func (m *Module) Title() string { return "Web服務" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset()        { m.cfg = Config{} }

// Normalize sets IDs and defaults.
func (m *Module) Normalize() {
	for i := range m.cfg.Rules {
		r := &m.cfg.Rules[i]
		if r.ID == "" {
			r.ID = core.RandID()
		}
		for j := range r.SubRules {
			s := &r.SubRules[j]
			if s.ID == "" {
				s.ID = core.RandID()
			}
			if s.Type == "" {
				s.Type = "proxy"
			}
		}
	}
}

// Validate checks rules.
func (m *Module) Validate() error {
	seen := map[string]string{}
	for _, r := range m.cfg.Rules {
		if !r.Enable {
			continue
		}
		if r.Port <= 0 || r.Port > 65535 {
			return fmt.Errorf("rule %q: invalid port", r.Name)
		}
		key := net.JoinHostPort(r.ListenIP, fmt.Sprint(r.Port))
		if other, ok := seen[key]; ok {
			return fmt.Errorf("rule %q: %s already used by %q", r.Name, key, other)
		}
		seen[key] = r.Name
		if r.TLS && m.app.Certs == nil {
			return fmt.Errorf("rule %q: TLS requires the SSL module", r.Name)
		}
	}
	return nil
}

// Start launches enabled rules.
func (m *Module) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.cfg.Rules {
		r := m.cfg.Rules[i]
		if !r.Enable {
			continue
		}
		l := &listener{rule: r, hits: map[string]*int64{}}
		if err := m.startListener(l); err != nil {
			m.log.Errorf("rule %q: %v", r.Name, err)
			continue
		}
		m.listeners[r.ID] = l
		scheme := "http"
		if r.TLS {
			scheme = "https"
		}
		m.log.Infof("rule %q listening on %s://%s:%d", r.Name, scheme, r.ListenIP, r.Port)
	}
	return nil
}

// Stop halts every rule.
func (m *Module) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, l := range m.listeners {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = l.server.Shutdown(ctx)
		cancel()
		delete(m.listeners, id)
	}
}

func (m *Module) startListener(l *listener) error {
	addr := net.JoinHostPort(l.rule.ListenIP, fmt.Sprint(l.rule.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	l.ln = ln
	l.server = &http.Server{Handler: m.makeHandler(l), ReadHeaderTimeout: 20 * time.Second}
	if l.rule.TLS {
		l.server.TLSConfig = &tls.Config{GetCertificate: m.app.Certs.GetCertificate, MinVersion: tls.VersionTLS12}
		go func() { _ = l.server.ServeTLS(ln, "", "") }()
	} else {
		go func() { _ = l.server.Serve(ln) }()
	}
	return nil
}

func (m *Module) makeHandler(l *listener) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ACME HTTP-01 pass-through.
		if strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
			if h, ok := m.app.Certs.(http01Lookup); ok {
				tok := strings.TrimPrefix(r.URL.Path, "/.well-known/acme-challenge/")
				if ka, ok := h.HTTP01(tok); ok {
					w.Header().Set("Content-Type", "text/plain")
					_, _ = w.Write([]byte(ka))
					return
				}
			}
		}
		sub := m.matchSub(l.rule, r)
		if sub == nil {
			http.Error(w, "no matching web rule", http.StatusNotFound)
			return
		}
		m.incr(l, sub.ID)
		ip := core.RequestIP(r)
		if sub.IPFilter != "" && m.app.IPFilter != nil && !accessAllowed(m, ip, sub) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !m.authorize(w, r, sub) {
			return
		}
		h := m.buildSubHandler(l.rule, sub)
		if sub.WAFProfile != "" && m.app.WAF != nil {
			h = m.app.WAF.Wrap(sub.WAFProfile, h)
		}
		h.ServeHTTP(w, r)
	})
}

func accessAllowed(m *Module, ip net.IP, sub *SubRule) bool {
	// When web login/basic auth is active, the IP filter alone gates pre-auth.
	mode := sub.GroupMode
	if sub.WebLogin || sub.BasicUser != "" {
		mode = core.GroupModeNone
	}
	return m.app.CheckAccess(ip, sub.IPFilter, sub.GroupID, mode)
}

func (m *Module) matchSub(rule Rule, r *http.Request) *SubRule {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)
	type cand struct {
		s     *SubRule
		score int
	}
	var best *cand
	for i := range rule.SubRules {
		s := &rule.SubRules[i]
		if !s.Enable {
			continue
		}
		fd := strings.ToLower(strings.TrimSpace(s.FrontDomain))
		if fd == "" { // match-all, lowest priority
			if best == nil {
				best = &cand{s, 0}
			}
			continue
		}
		dom, path, _ := strings.Cut(fd, "/")
		if !hostMatch(dom, host) {
			continue
		}
		if path != "" && !strings.HasPrefix(strings.TrimPrefix(r.URL.Path, "/"), path) {
			continue
		}
		score := 1 + len(path)
		if best == nil || score > best.score {
			best = &cand{s, score}
		}
	}
	if best == nil {
		return nil
	}
	return best.s
}

func hostMatch(pattern, host string) bool {
	if pattern == host {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		return strings.HasSuffix(host, pattern[1:]) && strings.Count(host, ".") >= strings.Count(pattern, ".")
	}
	return false
}

func (m *Module) incr(l *listener, subID string) {
	m.mu.Lock()
	c := l.hits[subID]
	if c == nil {
		var n int64
		c = &n
		l.hits[subID] = c
	}
	*c++
	m.mu.Unlock()
}

// Status lists rules and sub-rule hit counts.
func (m *Module) Status() any {
	m.mu.Lock()
	defer m.mu.Unlock()
	type si struct {
		ID, Front, Type, Backend string
		Hits                     int64
		Running                  bool
	}
	type ri struct {
		ID, Name, Listen string
		TLS, Running     bool
		Subs             []si
	}
	out := []ri{}
	for _, r := range m.cfg.Rules {
		info := ri{ID: r.ID, Name: r.Name, Listen: net.JoinHostPort(r.ListenIP, fmt.Sprint(r.Port)), TLS: r.TLS}
		l, ok := m.listeners[r.ID]
		info.Running = ok
		for _, s := range r.SubRules {
			var hits int64
			if ok {
				if c := l.hits[s.ID]; c != nil {
					hits = *c
				}
			}
			info.Subs = append(info.Subs, si{s.ID, s.FrontDomain, s.Type, s.Backend, hits, ok && s.Enable})
		}
		sort.Slice(info.Subs, func(i, j int) bool { return info.Subs[i].Hits > info.Subs[j].Hits })
		out = append(out, info)
	}
	return map[string]any{"rules": out}
}

// Action has no commands.
func (m *Module) Action(ctx context.Context, action string, body json.RawMessage) (any, error) {
	return nil, fmt.Errorf("unknown action %q", action)
}
