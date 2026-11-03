// Package cloudflared runs Cloudflare Tunnel (the cloudflared binary) so a
// local service is reachable over Cloudflare without a public IP. It supervises
// one tunnel per rule, started either from a tunnel token or a config file.
package cloudflared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sync"

	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/logx"
	"github.com/icebays/al1add/internal/procmgr"
)

// Tunnel is one cloudflared instance.
type Tunnel struct {
	ID         string   `json:"ID"`
	Name       string   `json:"Name"`
	Enable     bool     `json:"Enable"`
	Mode       string   `json:"Mode"`       // token | config
	Token      string   `json:"Token"`      // when Mode=token
	ConfigFile string   `json:"ConfigFile"` // when Mode=config
	ExtraArgs  []string `json:"ExtraArgs"`
	Remark     string   `json:"Remark"`
}

// Config of the module.
type Config struct {
	BinPath string   `json:"BinPath"` // path to cloudflared
	Tunnels []Tunnel `json:"Tunnels"`
}

// Module supervises tunnels.
type Module struct {
	app   *core.App
	log   *logx.Logger
	cfg   Config
	mu    sync.Mutex
	procs map[string]*procmgr.Proc
}

// New creates the module.
func New(app *core.App) *Module {
	return &Module{app: app, log: app.Logs.L("cloudflared"), procs: map[string]*procmgr.Proc{}}
}

func (m *Module) Name() string  { return "cloudflared" }
func (m *Module) Title() string { return "Cloudflare 隧道" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset()        { m.cfg = Config{BinPath: "cloudflared"} }

// Normalize sets IDs and defaults.
func (m *Module) Normalize() {
	if m.cfg.BinPath == "" {
		m.cfg.BinPath = "cloudflared"
	}
	for i := range m.cfg.Tunnels {
		t := &m.cfg.Tunnels[i]
		if t.ID == "" {
			t.ID = core.RandID()
		}
		if t.Mode == "" {
			t.Mode = "token"
		}
	}
}

func (m *Module) binPath() (string, error) {
	return exec.LookPath(m.cfg.BinPath)
}

// Start launches enabled tunnels.
func (m *Module) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	bin, err := m.binPath()
	if err != nil {
		if hasEnabled(m.cfg.Tunnels) {
			m.log.Warnf("cloudflared binary %q not found: %v — install it and set the path in settings", m.cfg.BinPath, err)
		}
		return nil
	}
	for _, t := range m.cfg.Tunnels {
		if !t.Enable {
			continue
		}
		p := procmgr.New(m.app.Logs.L("cloudflared:" + t.Name))
		p.Start(procmgr.Spec{Path: bin, Args: m.args(t)})
		m.procs[t.ID] = p
		m.log.Infof("tunnel %q started", t.Name)
	}
	return nil
}

func (m *Module) args(t Tunnel) []string {
	args := []string{"tunnel", "--no-autoupdate"}
	args = append(args, t.ExtraArgs...)
	switch t.Mode {
	case "config":
		args = append(args, "--config", t.ConfigFile, "run")
	default:
		args = append(args, "run", "--token", t.Token)
	}
	return args
}

func hasEnabled(ts []Tunnel) bool {
	for _, t := range ts {
		if t.Enable {
			return true
		}
	}
	return false
}

// Stop halts tunnels.
func (m *Module) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.procs {
		p.Stop()
		delete(m.procs, id)
	}
}

// Status reports tunnel states.
func (m *Module) Status() any {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, binErr := m.binPath()
	type ti struct {
		ID, Name, Mode, Error string
		Running               bool
		Uptime                int64
	}
	out := []ti{}
	for _, t := range m.cfg.Tunnels {
		info := ti{ID: t.ID, Name: t.Name, Mode: t.Mode}
		if p, ok := m.procs[t.ID]; ok {
			info.Running, info.Error, info.Uptime = p.Info()
		}
		out = append(out, info)
	}
	binStatus := "ok"
	if binErr != nil {
		binStatus = "not found: " + m.cfg.BinPath
	}
	return map[string]any{"tunnels": out, "bin": binStatus}
}

// Action has no commands.
func (m *Module) Action(ctx context.Context, action string, body json.RawMessage) (any, error) {
	return nil, errors.New("unknown action " + action)
}

var _ = fmt.Sprint
