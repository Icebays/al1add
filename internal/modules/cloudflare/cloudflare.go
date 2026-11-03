// Package cloudflare is the Cloudflare control-panel module: it manages one or
// more Cloudflare accounts (API tokens or Global API Key) and lets you browse
// zones, edit DNS records, purge the cache, toggle zone settings and maintain
// IP access rules — all from the al1add panel.
package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/icebays/al1add/internal/cfapi"
	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/logx"
)

// Account is a stored Cloudflare credential.
type Account struct {
	ID     string `json:"ID"`
	Name   string `json:"Name"`
	Type   string `json:"Type"`   // token | key
	Token  string `json:"Token"`  // when Type=token
	Email  string `json:"Email"`  // when Type=key
	APIKey string `json:"APIKey"` // when Type=key
	Remark string `json:"Remark"`

	LastCheck  string `json:"LastCheck"`
	LastStatus string `json:"LastStatus"`
}

// Config of the module.
type Config struct {
	Accounts []Account `json:"Accounts"`
}

// Module manages Cloudflare accounts.
type Module struct {
	app *core.App
	log *logx.Logger
	cfg Config
	mu  sync.Mutex
}

// New creates the module.
func New(app *core.App) *Module { return &Module{app: app, log: app.Logs.L("cloudflare")} }

func (m *Module) Name() string  { return "cloudflare" }
func (m *Module) Title() string { return "Cloudflare 管理" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset()        { m.cfg = Config{} }
func (m *Module) Start() error  { return nil }
func (m *Module) Stop()         {}

// Normalize assigns IDs.
func (m *Module) Normalize() {
	for i := range m.cfg.Accounts {
		a := &m.cfg.Accounts[i]
		if a.ID == "" {
			a.ID = core.RandID()
		}
		if a.Type == "" {
			if a.APIKey != "" {
				a.Type = "key"
			} else {
				a.Type = "token"
			}
		}
	}
}

// Validate checks credentials are present.
func (m *Module) Validate() error {
	for _, a := range m.cfg.Accounts {
		if a.Type == "token" && a.Token == "" {
			return fmt.Errorf("account %q: API token is required", a.Name)
		}
		if a.Type == "key" && (a.Email == "" || a.APIKey == "") {
			return fmt.Errorf("account %q: email and API key are required", a.Name)
		}
	}
	return nil
}

func (m *Module) client(accountID string) (*cfapi.Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.cfg.Accounts {
		if a.ID == accountID {
			if a.Type == "key" {
				return cfapi.NewKey(a.Email, a.APIKey), nil
			}
			return cfapi.New(a.Token), nil
		}
	}
	return nil, errors.New("account not found")
}

// Status lists accounts (no secrets).
func (m *Module) Status() any {
	m.mu.Lock()
	defer m.mu.Unlock()
	type ai struct {
		ID, Name, Type, LastCheck, LastStatus string
	}
	out := []ai{}
	for _, a := range m.cfg.Accounts {
		out = append(out, ai{a.ID, a.Name, a.Type, a.LastCheck, a.LastStatus})
	}
	return map[string]any{"accounts": out}
}

// Action dispatches Cloudflare operations.
func (m *Module) Action(ctx context.Context, action string, body json.RawMessage) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	switch action {
	case "verify":
		return m.verify(ctx, body)
	case "zones":
		return m.zones(ctx, body)
	case "records":
		return m.records(ctx, body)
	case "record_create":
		return m.recordWrite(ctx, body, "create")
	case "record_update":
		return m.recordWrite(ctx, body, "update")
	case "record_delete":
		return m.recordDelete(ctx, body)
	case "purge":
		return m.purge(ctx, body)
	case "settings":
		return m.settings(ctx, body)
	case "setting_set":
		return m.settingSet(ctx, body)
	case "access_rules":
		return m.accessRules(ctx, body)
	case "access_rule_create":
		return m.accessRuleCreate(ctx, body)
	case "access_rule_delete":
		return m.accessRuleDelete(ctx, body)
	case "devmode":
		return m.devmode(ctx, body)
	}
	return nil, fmt.Errorf("unknown action %q", action)
}

func (m *Module) verify(ctx context.Context, body json.RawMessage) (any, error) {
	req, err := core.Decode[struct{ AccountID string }](body)
	if err != nil {
		return nil, err
	}
	c, err := m.client(req.AccountID)
	if err != nil {
		return nil, err
	}
	status := "ok"
	var verr error
	if c.Token != "" {
		v, e := c.VerifyToken(ctx)
		if e != nil {
			verr = e
		} else {
			status = v.Status
		}
	} else {
		if _, e := c.Zones(ctx, ""); e != nil {
			verr = e
		}
	}
	m.mu.Lock()
	for i := range m.cfg.Accounts {
		if m.cfg.Accounts[i].ID == req.AccountID {
			m.cfg.Accounts[i].LastCheck = time.Now().Format("2006-01-02 15:04:05")
			if verr != nil {
				m.cfg.Accounts[i].LastStatus = "error: " + verr.Error()
			} else {
				m.cfg.Accounts[i].LastStatus = status
			}
		}
	}
	m.mu.Unlock()
	_ = m.app.SaveCurrent(m.Name())
	if verr != nil {
		return nil, verr
	}
	return map[string]any{"status": status}, nil
}

func (m *Module) zones(ctx context.Context, body json.RawMessage) (any, error) {
	req, err := core.Decode[struct{ AccountID, Name string }](body)
	if err != nil {
		return nil, err
	}
	c, err := m.client(req.AccountID)
	if err != nil {
		return nil, err
	}
	zs, err := c.Zones(ctx, strings.TrimSpace(req.Name))
	if err != nil {
		return nil, err
	}
	return map[string]any{"zones": zs}, nil
}

func (m *Module) records(ctx context.Context, body json.RawMessage) (any, error) {
	req, err := core.Decode[struct{ AccountID, ZoneID, Type, Name string }](body)
	if err != nil {
		return nil, err
	}
	c, err := m.client(req.AccountID)
	if err != nil {
		return nil, err
	}
	rs, err := c.Records(ctx, req.ZoneID, req.Type, req.Name)
	if err != nil {
		return nil, err
	}
	return map[string]any{"records": rs}, nil
}

func (m *Module) recordWrite(ctx context.Context, body json.RawMessage, op string) (any, error) {
	req, err := core.Decode[struct {
		AccountID string
		ZoneID    string
		RecordID  string
		Record    cfapi.DNSRecord
	}](body)
	if err != nil {
		return nil, err
	}
	c, err := m.client(req.AccountID)
	if err != nil {
		return nil, err
	}
	if req.Record.TTL == 0 {
		req.Record.TTL = 1
	}
	var out *cfapi.DNSRecord
	if op == "create" {
		out, err = c.CreateRecord(ctx, req.ZoneID, req.Record)
	} else {
		out, err = c.UpdateRecord(ctx, req.ZoneID, req.RecordID, req.Record)
	}
	if err != nil {
		return nil, err
	}
	m.log.Infof("%s record %s %s = %s", op, req.Record.Type, req.Record.Name, req.Record.Content)
	return map[string]any{"record": out}, nil
}

func (m *Module) recordDelete(ctx context.Context, body json.RawMessage) (any, error) {
	req, err := core.Decode[struct{ AccountID, ZoneID, RecordID string }](body)
	if err != nil {
		return nil, err
	}
	c, err := m.client(req.AccountID)
	if err != nil {
		return nil, err
	}
	if err := c.DeleteRecord(ctx, req.ZoneID, req.RecordID); err != nil {
		return nil, err
	}
	m.log.Infof("deleted record %s in zone %s", req.RecordID, req.ZoneID)
	return map[string]any{"ok": true}, nil
}

func (m *Module) purge(ctx context.Context, body json.RawMessage) (any, error) {
	req, err := core.Decode[struct {
		AccountID string
		ZoneID    string
		All       bool
		Files     []string
	}](body)
	if err != nil {
		return nil, err
	}
	c, err := m.client(req.AccountID)
	if err != nil {
		return nil, err
	}
	if err := c.PurgeCache(ctx, req.ZoneID, req.All, req.Files); err != nil {
		return nil, err
	}
	m.log.Infof("purged cache for zone %s (all=%v, %d files)", req.ZoneID, req.All, len(req.Files))
	return map[string]any{"ok": true}, nil
}

func (m *Module) settings(ctx context.Context, body json.RawMessage) (any, error) {
	req, err := core.Decode[struct{ AccountID, ZoneID string }](body)
	if err != nil {
		return nil, err
	}
	c, err := m.client(req.AccountID)
	if err != nil {
		return nil, err
	}
	s, err := c.Settings(ctx, req.ZoneID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"settings": s}, nil
}

func (m *Module) settingSet(ctx context.Context, body json.RawMessage) (any, error) {
	req, err := core.Decode[struct {
		AccountID string
		ZoneID    string
		Setting   string
		Value     any
	}](body)
	if err != nil {
		return nil, err
	}
	c, err := m.client(req.AccountID)
	if err != nil {
		return nil, err
	}
	out, err := c.SetSetting(ctx, req.ZoneID, req.Setting, req.Value)
	if err != nil {
		return nil, err
	}
	m.log.Infof("set zone %s setting %s = %v", req.ZoneID, req.Setting, req.Value)
	return map[string]any{"setting": out}, nil
}

func (m *Module) devmode(ctx context.Context, body json.RawMessage) (any, error) {
	req, err := core.Decode[struct {
		AccountID string
		ZoneID    string
		On        bool
	}](body)
	if err != nil {
		return nil, err
	}
	c, err := m.client(req.AccountID)
	if err != nil {
		return nil, err
	}
	if err := c.ZoneDevMode(ctx, req.ZoneID, req.On); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (m *Module) accessRules(ctx context.Context, body json.RawMessage) (any, error) {
	req, err := core.Decode[struct{ AccountID, ZoneID string }](body)
	if err != nil {
		return nil, err
	}
	c, err := m.client(req.AccountID)
	if err != nil {
		return nil, err
	}
	rs, err := c.AccessRules(ctx, req.ZoneID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"rules": rs}, nil
}

func (m *Module) accessRuleCreate(ctx context.Context, body json.RawMessage) (any, error) {
	req, err := core.Decode[struct {
		AccountID, ZoneID, Mode, Target, Value, Notes string
	}](body)
	if err != nil {
		return nil, err
	}
	c, err := m.client(req.AccountID)
	if err != nil {
		return nil, err
	}
	out, err := c.CreateAccessRule(ctx, req.ZoneID, req.Mode, req.Target, req.Value, req.Notes)
	if err != nil {
		return nil, err
	}
	m.log.Infof("created %s access rule %s=%s on zone %s", req.Mode, req.Target, req.Value, req.ZoneID)
	return map[string]any{"rule": out}, nil
}

func (m *Module) accessRuleDelete(ctx context.Context, body json.RawMessage) (any, error) {
	req, err := core.Decode[struct{ AccountID, ZoneID, RuleID string }](body)
	if err != nil {
		return nil, err
	}
	c, err := m.client(req.AccountID)
	if err != nil {
		return nil, err
	}
	if err := c.DeleteAccessRule(ctx, req.ZoneID, req.RuleID); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}
