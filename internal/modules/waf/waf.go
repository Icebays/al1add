// Package waf provides a Coraza-based Web Application Firewall. Named profiles
// hold SecLang directives; web service sub-rules reference a profile by ID and
// their traffic is inspected. It implements core.WAFService.
package waf

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	coraza "github.com/corazawaf/coraza/v3"
	corazahttp "github.com/corazawaf/coraza/v3/http"

	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/logx"
)

// DefaultDirectives is a minimal baseline ruleset blocking a few common attacks.
const DefaultDirectives = `SecRuleEngine On
SecRequestBodyAccess On
SecRule REQUEST_URI "@rx (?i)(?:\.\./|\.\.\\)" "id:1001,phase:1,deny,status:403,msg:'Path traversal'"
SecRule ARGS "@rx (?i)(?:union(?:\s+all)?\s+select|select\s+.+\s+from|insert\s+into|drop\s+table)" "id:1002,phase:2,deny,status:403,msg:'SQL injection'"
SecRule ARGS "@rx (?i)<script[^>]*>" "id:1003,phase:2,deny,status:403,msg:'XSS attempt'"
`

// Profile is a named WAF ruleset.
type Profile struct {
	ID         string `json:"ID"`
	Name       string `json:"Name"`
	Directives string `json:"Directives"`
	Remark     string `json:"Remark"`
}

// Config of the module.
type Config struct {
	Profiles []Profile `json:"Profiles"`
}

// Module compiles profiles and wraps handlers.
type Module struct {
	app      *core.App
	log      *logx.Logger
	cfg      Config
	mu       sync.RWMutex
	compiled map[string]coraza.WAF
}

// New creates the module.
func New(app *core.App) *Module {
	m := &Module{app: app, log: app.Logs.L("waf"), compiled: map[string]coraza.WAF{}}
	app.WAF = m
	return m
}

func (m *Module) Name() string  { return "waf" }
func (m *Module) Title() string { return "Web防火牆 (Coraza)" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset()        { m.cfg = Config{} }

// Normalize assigns IDs and seeds a default profile.
func (m *Module) Normalize() {
	for i := range m.cfg.Profiles {
		if m.cfg.Profiles[i].ID == "" {
			m.cfg.Profiles[i].ID = core.RandID()
		}
	}
}

func buildWAF(directives string) (coraza.WAF, error) {
	if directives == "" {
		directives = DefaultDirectives
	}
	return coraza.NewWAF(coraza.NewWAFConfig().WithDirectives(directives))
}

// Validate compiles each profile.
func (m *Module) Validate() error {
	for _, p := range m.cfg.Profiles {
		if _, err := buildWAF(p.Directives); err != nil {
			return fmt.Errorf("profile %q: %w", p.Name, err)
		}
	}
	return nil
}

// Start compiles all profiles.
func (m *Module) Start() error {
	compiled := map[string]coraza.WAF{}
	for _, p := range m.cfg.Profiles {
		w, err := buildWAF(p.Directives)
		if err != nil {
			m.log.Errorf("profile %q: %v", p.Name, err)
			continue
		}
		compiled[p.ID] = w
	}
	m.mu.Lock()
	m.compiled = compiled
	m.mu.Unlock()
	return nil
}

// Stop clears compiled WAFs.
func (m *Module) Stop() {
	m.mu.Lock()
	m.compiled = map[string]coraza.WAF{}
	m.mu.Unlock()
}

// Wrap implements core.WAFService: wrap h with profile profileID. Unknown
// profiles pass through unchanged.
func (m *Module) Wrap(profileID string, h http.Handler) http.Handler {
	m.mu.RLock()
	w := m.compiled[profileID]
	m.mu.RUnlock()
	if w == nil {
		return h
	}
	return corazahttp.WrapHandler(w, h)
}

// Status lists profiles.
func (m *Module) Status() any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	type pi struct {
		ID, Name string
		Active   bool
	}
	out := []pi{}
	for _, p := range m.cfg.Profiles {
		_, ok := m.compiled[p.ID]
		out = append(out, pi{p.ID, p.Name, ok})
	}
	return map[string]any{"profiles": out}
}

// Action has no commands.
func (m *Module) Action(ctx context.Context, action string, body json.RawMessage) (any, error) {
	return nil, fmt.Errorf("unknown action %q", action)
}
