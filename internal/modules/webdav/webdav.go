// Package webdav serves storage mounts over WebDAV on a dedicated port with
// optional BasicAuth, so files are reachable from WebDAV clients.
package webdav

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/logx"
)

// Config of the module.
type Config struct {
	Enable    bool   `json:"Enable"`
	ListenIP  string `json:"ListenIP"`
	Port      int    `json:"Port"`
	StorageID string `json:"StorageID"` // which mount to serve
	User      string `json:"User"`
	Pass      string `json:"Pass"`
	ReadOnly  bool   `json:"ReadOnly"`
}

// Module serves WebDAV.
type Module struct {
	app *core.App
	log *logx.Logger
	cfg Config
	mu  sync.Mutex
	srv *http.Server
}

// New creates the module.
func New(app *core.App) *Module { return &Module{app: app, log: app.Logs.L("webdav")} }

func (m *Module) Name() string  { return "webdav" }
func (m *Module) Title() string { return "WebDAV" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset()        { m.cfg = Config{Port: 16680} }

// Normalize default port.
func (m *Module) Normalize() {
	if m.cfg.Port == 0 {
		m.cfg.Port = 16680
	}
}

// Start opens the WebDAV listener.
func (m *Module) Start() error {
	if !m.cfg.Enable {
		return nil
	}
	if m.app.Storage == nil {
		return fmt.Errorf("storage module unavailable")
	}
	root, ro, err := m.app.Storage.Root(m.cfg.StorageID)
	if err != nil {
		return err
	}
	readOnly := ro || m.cfg.ReadOnly
	dav := &webdav.Handler{
		FileSystem: webdav.Dir(root),
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				m.log.Warnf("%s %s: %v", r.Method, r.URL.Path, err)
			}
		},
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.cfg.User != "" {
			u, p, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(m.cfg.User)) != 1 ||
				subtle.ConstantTimeCompare([]byte(p), []byte(m.cfg.Pass)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="webdav"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		if readOnly && isWrite(r.Method) {
			http.Error(w, "read-only", http.StatusForbidden)
			return
		}
		dav.ServeHTTP(w, r)
	})
	addr := net.JoinHostPort(m.cfg.ListenIP, fmt.Sprint(m.cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	m.srv = &http.Server{Handler: handler, ReadHeaderTimeout: 20 * time.Second}
	go func() { _ = m.srv.Serve(ln) }()
	m.log.Infof("WebDAV serving %s on %s (read-only=%v)", root, addr, readOnly)
	return nil
}

func isWrite(method string) bool {
	switch method {
	case "PUT", "DELETE", "MKCOL", "COPY", "MOVE", "PROPPATCH", "LOCK", "UNLOCK":
		return true
	}
	return false
}

// Stop closes the listener.
func (m *Module) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = m.srv.Shutdown(ctx)
		cancel()
		m.srv = nil
	}
}

// Status reports state.
func (m *Module) Status() any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{"enable": m.cfg.Enable, "running": m.srv != nil, "port": m.cfg.Port}
}

// Action has no commands.
func (m *Module) Action(ctx context.Context, action string, body json.RawMessage) (any, error) {
	return nil, fmt.Errorf("unknown action %q", action)
}
