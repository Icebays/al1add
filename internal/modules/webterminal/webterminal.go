// Package webterminal exposes an interactive shell in the browser over a
// WebSocket, backed by a PTY. It is gated behind the panel's admin auth and can
// be disabled entirely.
package webterminal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"

	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/logx"
)

// Config of the module.
type Config struct {
	Enable  bool   `json:"Enable"`
	Shell   string `json:"Shell"`   // default: sh / cmd
	WorkDir string `json:"WorkDir"` // default: data dir
}

// Module serves the web terminal.
type Module struct {
	app *core.App
	log *logx.Logger
	mu  sync.RWMutex
	cfg Config
	up  websocket.Upgrader
}

// New creates the module and registers its admin endpoint.
func New(app *core.App) *Module {
	m := &Module{app: app, log: app.Logs.L("webterminal")}
	m.up = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	app.HandleAdmin("webterminal", http.HandlerFunc(m.serveWS))
	return m
}

func (m *Module) Name() string  { return "webterminal" }
func (m *Module) Title() string { return "Web終端" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset()        { m.cfg = Config{} }
func (m *Module) Start() error  { return nil }
func (m *Module) Stop()         {}

// Status reports whether the terminal is enabled.
func (m *Module) Status() any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return map[string]any{"enable": m.cfg.Enable, "shell": m.shell(), "platform": runtime.GOOS}
}

func (m *Module) shell() string {
	if m.cfg.Shell != "" {
		return m.cfg.Shell
	}
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	if _, err := os.Stat("/bin/bash"); err == nil {
		return "/bin/bash"
	}
	return "/bin/sh"
}

// Action has no commands (the terminal is a WebSocket endpoint).
func (m *Module) Action(ctx context.Context, action string, body json.RawMessage) (any, error) {
	return nil, fmt.Errorf("unknown action %q", action)
}

type wsMsg struct {
	Type string `json:"type"` // input | resize
	Data string `json:"data"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func (m *Module) serveWS(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	enabled := m.cfg.Enable
	shell := m.shell()
	dir := m.cfg.WorkDir
	m.mu.RUnlock()
	if !enabled {
		http.Error(w, "web terminal is disabled", http.StatusForbidden)
		return
	}
	if runtime.GOOS == "windows" {
		http.Error(w, "web terminal is not supported on Windows", http.StatusNotImplemented)
		return
	}
	if dir == "" {
		dir = m.app.DataDir
	}
	conn, err := m.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	cmd := exec.Command(shell)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("cannot start shell: "+err.Error()))
		return
	}
	defer func() { _ = ptmx.Close(); _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	m.log.Infof("[%s] terminal session opened", core.RequestIP(r))
	defer m.log.Infof("[%s] terminal session closed", core.RequestIP(r))

	// pty -> ws
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				_ = conn.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shell exited"), time.Now().Add(time.Second))
				return
			}
		}
	}()

	// ws -> pty
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if mt == websocket.TextMessage && len(data) > 0 && data[0] == '{' {
			var msg wsMsg
			if json.Unmarshal(data, &msg) == nil {
				switch msg.Type {
				case "resize":
					_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(msg.Cols), Rows: uint16(msg.Rows)})
					continue
				case "input":
					_, _ = ptmx.Write([]byte(msg.Data))
					continue
				}
			}
		}
		_, _ = ptmx.Write(data)
	}
}

var _ = strings.TrimSpace
