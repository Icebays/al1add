// Package docker provides basic Docker management: list containers and images,
// start/stop/restart/remove containers and read their logs, by talking to the
// Docker Engine API over its unix socket (or a TCP host).
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/logx"
)

// Config of the module.
type Config struct {
	Enable bool   `json:"Enable"`
	Host   string `json:"Host"` // unix:///var/run/docker.sock or tcp://host:2375
}

// Module manages Docker.
type Module struct {
	app *core.App
	log *logx.Logger
	cfg Config
	mu  sync.RWMutex
	cli *client
}

// New creates the module.
func New(app *core.App) *Module { return &Module{app: app, log: app.Logs.L("docker")} }

func (m *Module) Name() string  { return "docker" }
func (m *Module) Title() string { return "Docker 管理" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset()        { m.cfg = Config{Host: "unix:///var/run/docker.sock"} }

// Normalize sets defaults.
func (m *Module) Normalize() {
	if m.cfg.Host == "" {
		m.cfg.Host = "unix:///var/run/docker.sock"
	}
}

// Start builds the client and pings the daemon.
func (m *Module) Start() error {
	if !m.cfg.Enable {
		return nil
	}
	m.mu.Lock()
	m.cli = newClient(m.cfg.Host)
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.cli.ping(ctx); err != nil {
		m.log.Warnf("%v", err)
		return nil // keep module enabled; surface the error in status
	}
	return nil
}

// Stop releases the client.
func (m *Module) Stop() {
	m.mu.Lock()
	m.cli = nil
	m.mu.Unlock()
}

func (m *Module) client() (*client, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.cfg.Enable {
		return nil, fmt.Errorf("Docker module is disabled")
	}
	if m.cli == nil {
		return nil, fmt.Errorf("Docker client not initialised")
	}
	return m.cli, nil
}

// Status reports daemon reachability.
func (m *Module) Status() any {
	m.mu.RLock()
	cli, enable := m.cli, m.cfg.Enable
	m.mu.RUnlock()
	st := map[string]any{"enable": enable}
	if cli == nil {
		st["connected"] = false
		return st
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := cli.ping(ctx); err != nil {
		st["connected"] = false
		st["error"] = err.Error()
		return st
	}
	st["connected"] = true
	if cs, err := cli.containers(ctx); err == nil {
		running := 0
		for _, c := range cs {
			if c.State == "running" {
				running++
			}
		}
		st["containers"] = len(cs)
		st["running"] = running
	}
	return st
}

// Action: ps, images, start|stop|restart|remove {ID}, logs {ID, Tail}.
func (m *Module) Action(ctx context.Context, action string, body json.RawMessage) (any, error) {
	cli, err := m.client()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	switch action {
	case "ps":
		cs, err := cli.containers(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"containers": shape(cs)}, nil
	case "images":
		is, err := cli.images(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"images": is}, nil
	case "start", "stop", "restart", "remove":
		req, err := core.Decode[struct{ ID string }](body)
		if err != nil {
			return nil, err
		}
		if err := cli.containerAction(ctx, req.ID, action); err != nil {
			return nil, err
		}
		m.log.Infof("container %s: %s", short(req.ID), action)
		return map[string]any{"ok": true}, nil
	case "logs":
		req, err := core.Decode[struct {
			ID   string
			Tail int
		}](body)
		if err != nil {
			return nil, err
		}
		if req.Tail <= 0 || req.Tail > 2000 {
			req.Tail = 200
		}
		out, err := cli.logs(ctx, req.ID, req.Tail)
		if err != nil {
			return nil, err
		}
		return map[string]any{"logs": out}, nil
	}
	return nil, fmt.Errorf("unknown action %q", action)
}

type containerView struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Image   string   `json:"image"`
	State   string   `json:"state"`
	Status  string   `json:"status"`
	Ports   []string `json:"ports"`
	Created string   `json:"created"`
}

func shape(cs []container) []containerView {
	out := make([]containerView, 0, len(cs))
	for _, c := range cs {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		var ports []string
		for _, p := range c.Ports {
			if p.PublicPort != 0 {
				ports = append(ports, fmt.Sprintf("%d:%d/%s", p.PublicPort, p.PrivatePort, p.Type))
			} else {
				ports = append(ports, fmt.Sprintf("%d/%s", p.PrivatePort, p.Type))
			}
		}
		out = append(out, containerView{
			ID: short(c.ID), Name: name, Image: c.Image, State: c.State, Status: c.Status,
			Ports: ports, Created: time.Unix(c.Created, 0).Format("2006-01-02 15:04"),
		})
	}
	return out
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
