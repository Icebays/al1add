// Package frp runs FRP (fast reverse proxy) clients/servers by supervising the
// frpc / frps binaries with a config file, for intranet penetration.
package frp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/logx"
	"github.com/icebays/al1add/internal/procmgr"
)

// Instance is one frp client or server.
type Instance struct {
	ID        string `json:"ID"`
	Name      string `json:"Name"`
	Enable    bool   `json:"Enable"`
	Role      string `json:"Role"`      // frpc | frps
	ConfigTOML string `json:"ConfigTOML"` // inline config written to a temp file
	Remark    string `json:"Remark"`
}

// Config of the module.
type Config struct {
	FrpcPath  string     `json:"FrpcPath"`
	FrpsPath  string     `json:"FrpsPath"`
	Instances []Instance `json:"Instances"`
}

// Module supervises frp instances.
type Module struct {
	app   *core.App
	log   *logx.Logger
	cfg   Config
	mu    sync.Mutex
	procs map[string]*procmgr.Proc
}

// New creates the module.
func New(app *core.App) *Module {
	return &Module{app: app, log: app.Logs.L("frp"), procs: map[string]*procmgr.Proc{}}
}

func (m *Module) Name() string  { return "frp" }
func (m *Module) Title() string { return "FRP內網穿透" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset()        { m.cfg = Config{FrpcPath: "frpc", FrpsPath: "frps"} }

// Normalize sets IDs and defaults.
func (m *Module) Normalize() {
	if m.cfg.FrpcPath == "" {
		m.cfg.FrpcPath = "frpc"
	}
	if m.cfg.FrpsPath == "" {
		m.cfg.FrpsPath = "frps"
	}
	for i := range m.cfg.Instances {
		in := &m.cfg.Instances[i]
		if in.ID == "" {
			in.ID = core.RandID()
		}
		if in.Role == "" {
			in.Role = "frpc"
		}
	}
}

func (m *Module) dir() string { return m.app.Store.Path("frp") }

// Start launches enabled instances.
func (m *Module) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_ = os.MkdirAll(m.dir(), 0o700)
	for _, in := range m.cfg.Instances {
		if !in.Enable {
			continue
		}
		binName := m.cfg.FrpcPath
		if in.Role == "frps" {
			binName = m.cfg.FrpsPath
		}
		bin, err := exec.LookPath(binName)
		if err != nil {
			m.log.Warnf("instance %q: binary %q not found — install frp and set the path", in.Name, binName)
			continue
		}
		cfgPath := filepath.Join(m.dir(), in.ID+".toml")
		if err := os.WriteFile(cfgPath, []byte(in.ConfigTOML), 0o600); err != nil {
			m.log.Errorf("instance %q: write config: %v", in.Name, err)
			continue
		}
		p := procmgr.New(m.app.Logs.L("frp:" + in.Name))
		p.Start(procmgr.Spec{Path: bin, Args: []string{"-c", cfgPath}})
		m.procs[in.ID] = p
		m.log.Infof("instance %q (%s) started", in.Name, in.Role)
	}
	return nil
}

// Stop halts instances.
func (m *Module) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.procs {
		p.Stop()
		delete(m.procs, id)
	}
}

// Status reports instance states.
func (m *Module) Status() any {
	m.mu.Lock()
	defer m.mu.Unlock()
	type ii struct {
		ID, Name, Role, Error string
		Running               bool
		Uptime                int64
	}
	out := []ii{}
	for _, in := range m.cfg.Instances {
		info := ii{ID: in.ID, Name: in.Name, Role: in.Role}
		if p, ok := m.procs[in.ID]; ok {
			info.Running, info.Error, info.Uptime = p.Info()
		}
		out = append(out, info)
	}
	return map[string]any{"instances": out}
}

// Action has no commands.
func (m *Module) Action(ctx context.Context, action string, body json.RawMessage) (any, error) {
	return nil, errors.New("unknown action " + action)
}
