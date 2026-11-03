// Package secgroup implements security groups: shared user credentials and
// runtime IP authorizations granted after a successful web login, which web
// rules and port forwarding can rely on.
package secgroup

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/icebays/al1add/internal/core"
)

// User of a group. Password holds a bcrypt hash; plain text entered in the UI
// is hashed on save.
type User struct {
	Username string `json:"Username"`
	Password string `json:"Password"`
	Remark   string `json:"Remark"`
}

// Group is one security group.
type Group struct {
	ID             string `json:"ID"`
	Name           string `json:"Name"`
	Users          []User `json:"Users"`
	AuthTTLMinutes int    `json:"AuthTTLMinutes"`
	CrossRuleSSO   bool   `json:"CrossRuleSSO"`
	Remark         string `json:"Remark"`
}

// Config of the module.
type Config struct {
	Groups []Group `json:"Groups"`
}

type grant struct {
	User    string
	Expires time.Time
}

// Module implements core.GroupService.
type Module struct {
	app    *core.App
	cfg    Config
	mu     sync.RWMutex
	groups map[string]Group
	grants map[string]map[string]grant // group -> ip -> grant
	stop   chan struct{}
}

// New creates the module.
func New(app *core.App) *Module {
	m := &Module{app: app, grants: map[string]map[string]grant{}}
	app.Groups = m
	return m
}

func (m *Module) Name() string  { return "secgroup" }
func (m *Module) Title() string { return "安全組" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset()        { m.cfg = Config{} }

// Normalize assigns IDs and hashes plain passwords.
func (m *Module) Normalize() {
	for i := range m.cfg.Groups {
		g := &m.cfg.Groups[i]
		if g.ID == "" {
			g.ID = core.RandID()
		}
		if g.AuthTTLMinutes <= 0 {
			g.AuthTTLMinutes = 720
		}
		for j := range g.Users {
			u := &g.Users[j]
			u.Username = strings.TrimSpace(u.Username)
			if u.Password != "" && !strings.HasPrefix(u.Password, "$2") {
				if h, err := bcrypt.GenerateFromPassword([]byte(u.Password), bcrypt.DefaultCost); err == nil {
					u.Password = string(h)
				}
			}
		}
	}
}

// Start indexes groups and starts the expiry sweeper.
func (m *Module) Start() error {
	idx := map[string]Group{}
	for _, g := range m.cfg.Groups {
		idx[g.ID] = g
	}
	m.mu.Lock()
	m.groups = idx
	for gid := range m.grants {
		if _, ok := idx[gid]; !ok {
			delete(m.grants, gid)
		}
	}
	m.mu.Unlock()
	m.stop = make(chan struct{})
	go m.sweep(m.stop)
	return nil
}

// Stop halts the sweeper (grants are kept across restarts).
func (m *Module) Stop() {
	if m.stop != nil {
		close(m.stop)
		m.stop = nil
	}
}

func (m *Module) sweep(stop chan struct{}) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-t.C:
			m.mu.Lock()
			for _, ips := range m.grants {
				for ip, g := range ips {
					if now.After(g.Expires) {
						delete(ips, ip)
					}
				}
			}
			m.mu.Unlock()
		}
	}
}

// Authenticate checks credentials against a group.
func (m *Module) Authenticate(groupID, user, pass string) bool {
	m.mu.RLock()
	g, ok := m.groups[groupID]
	m.mu.RUnlock()
	if !ok {
		return false
	}
	for _, u := range g.Users {
		if u.Username == user && u.Password != "" && bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(pass)) == nil {
			return true
		}
	}
	return false
}

// Grant authorizes ip for the group's TTL.
func (m *Module) Grant(groupID, ip, user string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[groupID]
	if !ok {
		return
	}
	if m.grants[groupID] == nil {
		m.grants[groupID] = map[string]grant{}
	}
	m.grants[groupID][ip] = grant{User: user, Expires: time.Now().Add(time.Duration(g.AuthTTLMinutes) * time.Minute)}
	m.app.Logs.L(m.Name()).Infof("[%s] authorized for group %q by user %q", ip, g.Name, user)
}

// IsAuthorized reports whether ip holds a valid grant.
func (m *Module) IsAuthorized(groupID, ip string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	g, ok := m.grants[groupID][ip]
	return ok && time.Now().Before(g.Expires)
}

// CrossRuleSSO reports the group's SSO flag.
func (m *Module) CrossRuleSSO(groupID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.groups[groupID].CrossRuleSSO
}

type grantInfo struct {
	Group   string `json:"group"`
	GroupID string `json:"groupId"`
	IP      string `json:"ip"`
	User    string `json:"user"`
	Expires string `json:"expires"`
}

// Status lists active grants.
func (m *Module) Status() any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []grantInfo{}
	for gid, ips := range m.grants {
		for ip, g := range ips {
			out = append(out, grantInfo{m.groups[gid].Name, gid, ip, g.User, core.FmtTime(g.Expires)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Expires > out[j].Expires })
	return map[string]any{"grants": out}
}

// Action: revoke {GroupID, IP}, grant {GroupID, IP}.
func (m *Module) Action(ctx context.Context, action string, body json.RawMessage) (any, error) {
	req, err := core.Decode[struct{ GroupID, IP string }](body)
	if err != nil {
		return nil, err
	}
	switch action {
	case "revoke":
		m.mu.Lock()
		delete(m.grants[req.GroupID], req.IP)
		m.mu.Unlock()
		return map[string]any{"ok": true}, nil
	case "grant":
		m.Grant(req.GroupID, strings.TrimSpace(req.IP), "admin")
		return map[string]any{"ok": true}, nil
	}
	return nil, fmt.Errorf("unknown action %q", action)
}
