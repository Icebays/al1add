// Package ftp serves a storage mount over FTP using ftpserverlib, with simple
// user/password authentication and a configurable passive-port range.
package ftp

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"

	ftpserver "github.com/fclairamb/ftpserverlib"
	"github.com/fclairamb/go-log/noop"
	"github.com/spf13/afero"

	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/logx"
)

// Config of the module.
type Config struct {
	Enable      bool   `json:"Enable"`
	ListenIP    string `json:"ListenIP"`
	Port        int    `json:"Port"`
	StorageID   string `json:"StorageID"`
	User        string `json:"User"`
	Pass        string `json:"Pass"`
	PasvMinPort int    `json:"PasvMinPort"`
	PasvMaxPort int    `json:"PasvMaxPort"`
	PublicHost  string `json:"PublicHost"` // advertised passive IP
}

// Module serves FTP.
type Module struct {
	app *core.App
	log *logx.Logger
	cfg Config
	mu  sync.Mutex
	srv *ftpserver.FtpServer
	ln  net.Listener
}

// New creates the module.
func New(app *core.App) *Module { return &Module{app: app, log: app.Logs.L("ftpserver")} }

func (m *Module) Name() string  { return "ftpserver" }
func (m *Module) Title() string { return "FTP服務" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset()        { m.cfg = Config{Port: 16621, PasvMinPort: 50000, PasvMaxPort: 50100} }

// Normalize defaults.
func (m *Module) Normalize() {
	if m.cfg.Port == 0 {
		m.cfg.Port = 16621
	}
	if m.cfg.PasvMinPort == 0 {
		m.cfg.PasvMinPort = 50000
	}
	if m.cfg.PasvMaxPort == 0 {
		m.cfg.PasvMaxPort = 50100
	}
}

// Start opens the FTP listener.
func (m *Module) Start() error {
	if !m.cfg.Enable {
		return nil
	}
	if m.app.Storage == nil {
		return errors.New("storage module unavailable")
	}
	root, ro, err := m.app.Storage.Root(m.cfg.StorageID)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(m.cfg.ListenIP, fmt.Sprint(m.cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	drv := &driver{cfg: m.cfg, root: root, readOnly: ro, listener: ln, log: m.log}
	srv := ftpserver.NewFtpServer(drv)
	srv.Logger = noop.NewNoOpLogger()
	m.mu.Lock()
	m.srv, m.ln = srv, ln
	m.mu.Unlock()
	go func() {
		if err := srv.Serve(); err != nil {
			m.log.Warnf("ftp server stopped: %v", err)
		}
	}()
	m.log.Infof("FTP serving %s on %s", root, addr)
	return nil
}

// Stop closes the server.
func (m *Module) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.srv != nil {
		_ = m.srv.Stop()
		m.srv = nil
	}
	if m.ln != nil {
		_ = m.ln.Close()
		m.ln = nil
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

// driver implements ftpserver.MainDriver.
type driver struct {
	cfg      Config
	root     string
	readOnly bool
	listener net.Listener
	log      *logx.Logger
}

func (d *driver) GetSettings() (*ftpserver.Settings, error) {
	return &ftpserver.Settings{
		Listener:        d.listener,
		PublicHost:      d.cfg.PublicHost,
		PassiveTransferPortRange: &ftpserver.PortRange{Start: d.cfg.PasvMinPort, End: d.cfg.PasvMaxPort},
		DefaultTransferType:      ftpserver.TransferTypeBinary,
	}, nil
}

func (d *driver) ClientConnected(cc ftpserver.ClientContext) (string, error) {
	return "al1add FTP service", nil
}

func (d *driver) ClientDisconnected(cc ftpserver.ClientContext) {}

func (d *driver) AuthUser(cc ftpserver.ClientContext, user, pass string) (ftpserver.ClientDriver, error) {
	if d.cfg.User != "" {
		if subtle.ConstantTimeCompare([]byte(user), []byte(d.cfg.User)) != 1 ||
			subtle.ConstantTimeCompare([]byte(pass), []byte(d.cfg.Pass)) != 1 {
			return nil, errors.New("authentication failed")
		}
	}
	base := afero.NewBasePathFs(afero.NewOsFs(), d.root)
	if d.readOnly {
		return afero.NewReadOnlyFs(base), nil
	}
	return base, nil
}

func (d *driver) GetTLSConfig() (*tls.Config, error) {
	return nil, errors.New("TLS not configured")
}
