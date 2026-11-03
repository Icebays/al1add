// Package core wires modules together: registry, config lifecycle and the
// services modules expose to each other.
package core

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/icebays/al1add/internal/logx"
	"github.com/icebays/al1add/internal/store"
)

// Version of the program.
const Version = "1.0.0"

// Module is a configurable, restartable feature.
type Module interface {
	Name() string  // config/file name, e.g. "ddns"
	Title() string // display name
	Config() any   // pointer to the live config struct
	Reset()        // zero the config and apply defaults
	Start() error  // apply config (called after Reset/Load)
	Stop()         // release listeners/goroutines
}

// Normalizer fixes up a config after it is decoded (IDs, hashes...).
type Normalizer interface{ Normalize() }

// Validator rejects bad configs before they are saved.
type Validator interface{ Validate() error }

// Statuser reports runtime status.
type Statuser interface{ Status() any }

// Actioner handles module specific commands from the admin UI / cron.
type Actioner interface {
	Action(ctx context.Context, action string, body json.RawMessage) (any, error)
}

// Services modules provide to each other. Nil-safe helpers below.
type (
	IPFilterService interface {
		Allow(listID string, ip net.IP) bool
	}
	GeoService interface {
		Country(ip net.IP) string
	}
	CertService interface {
		GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error)
	}
	GroupService interface {
		Authenticate(groupID, user, pass string) bool
		Grant(groupID, ip, user string)
		IsAuthorized(groupID, ip string) bool
		CrossRuleSSO(groupID string) bool
	}
	StorageService interface {
		Root(storageID string) (path string, readOnly bool, err error)
	}
	WAFService interface {
		Wrap(profileID string, h http.Handler) http.Handler
	}
)

// App holds every module and shared service.
type App struct {
	Store   *store.Store
	Logs    *logx.Hub
	DataDir string

	IPFilter IPFilterService
	Geo      GeoService
	Certs    CertService
	Groups   GroupService
	Storage  StorageService
	WAF      WAFService

	mu      sync.Mutex
	modules []Module
	byName  map[string]Module

	adminMu       sync.RWMutex
	adminHandlers map[string]http.Handler
}

// New creates an app.
func New(st *store.Store, logs *logx.Hub) *App {
	return &App{Store: st, Logs: logs, DataDir: st.Dir(), byName: map[string]Module{}, adminHandlers: map[string]http.Handler{}}
}

// Register adds a module (order = start order).
func (a *App) Register(m Module) {
	a.modules = append(a.modules, m)
	a.byName[m.Name()] = m
}

// Modules returns all modules in registration order.
func (a *App) Modules() []Module { return a.modules }

// Module finds a module by name.
func (a *App) Module(name string) Module { return a.byName[name] }

// HandleAdmin registers an authenticated admin endpoint under /api/x/<path>.
func (a *App) HandleAdmin(path string, h http.Handler) {
	a.adminMu.Lock()
	a.adminHandlers[path] = h
	a.adminMu.Unlock()
}

// AdminHandler returns the handler for /api/x/<path>.
func (a *App) AdminHandler(path string) http.Handler {
	a.adminMu.RLock()
	defer a.adminMu.RUnlock()
	return a.adminHandlers[path]
}

// LoadAll loads every module config from the store.
func (a *App) LoadAll() {
	for _, m := range a.modules {
		a.load(m)
	}
}

func (a *App) load(m Module) {
	m.Reset()
	found, err := a.Store.Load(m.Name(), m.Config())
	if err != nil {
		a.Logs.L(m.Name()).Errorf("load config: %v (using defaults)", err)
		m.Reset()
	}
	if n, ok := m.(Normalizer); ok {
		n.Normalize()
	}
	if !found {
		_ = a.Store.Save(m.Name(), m.Config())
	}
}

// StartAll starts every module.
func (a *App) StartAll() {
	for _, m := range a.modules {
		if err := m.Start(); err != nil {
			a.Logs.L(m.Name()).Errorf("start failed: %v", err)
		} else {
			a.Logs.L(m.Name()).Infof("module started")
		}
	}
}

// StopAll stops modules in reverse order.
func (a *App) StopAll() {
	for i := len(a.modules) - 1; i >= 0; i-- {
		a.modules[i].Stop()
	}
}

// ReloadAll re-reads configs from disk and restarts modules (after restore).
func (a *App) ReloadAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.StopAll()
	a.LoadAll()
	a.StartAll()
}

// ConfigJSON returns the module config as JSON.
func (a *App) ConfigJSON(name string) ([]byte, error) {
	m := a.byName[name]
	if m == nil {
		return nil, fmt.Errorf("unknown module %q", name)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return json.Marshal(m.Config())
}

// SetConfig replaces a module config, saves it and restarts the module.
// On validation failure the previous config is kept running.
func (a *App) SetConfig(name string, data []byte) error {
	m := a.byName[name]
	if m == nil {
		return fmt.Errorf("unknown module %q", name)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	old, _ := json.Marshal(m.Config())
	m.Stop()
	restore := func() {
		m.Reset()
		_ = json.Unmarshal(old, m.Config())
		_ = m.Start()
	}
	m.Reset()
	if err := json.Unmarshal(data, m.Config()); err != nil {
		restore()
		return fmt.Errorf("invalid config: %w", err)
	}
	if n, ok := m.(Normalizer); ok {
		n.Normalize()
	}
	if v, ok := m.(Validator); ok {
		if err := v.Validate(); err != nil {
			restore()
			return err
		}
	}
	if err := a.Store.Save(name, m.Config()); err != nil {
		restore()
		return err
	}
	if err := m.Start(); err != nil {
		a.Logs.L(name).Errorf("start failed: %v", err)
		return fmt.Errorf("saved, but start failed: %w", err)
	}
	a.Logs.L(name).Infof("configuration applied")
	return nil
}

// SaveCurrent persists a module config without restarting it (used when a
// module updates its own state, e.g. ACME certificates).
func (a *App) SaveCurrent(name string) error {
	m := a.byName[name]
	if m == nil {
		return fmt.Errorf("unknown module %q", name)
	}
	return a.Store.Save(name, m.Config())
}

// Status returns module status or nil.
func (a *App) Status(name string) any {
	if s, ok := a.byName[name].(Statuser); ok {
		return s.Status()
	}
	return nil
}

// Action dispatches a module action.
func (a *App) Action(ctx context.Context, name, action string, body json.RawMessage) (any, error) {
	m := a.byName[name]
	if m == nil {
		return nil, fmt.Errorf("unknown module %q", name)
	}
	ac, ok := m.(Actioner)
	if !ok {
		return nil, errors.New("module has no actions")
	}
	return ac.Action(ctx, action, body)
}

// ModuleInfo describes a module for the UI.
type ModuleInfo struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

// Infos lists modules.
func (a *App) Infos() []ModuleInfo {
	out := make([]ModuleInfo, 0, len(a.modules))
	for _, m := range a.modules {
		out = append(out, ModuleInfo{m.Name(), m.Title()})
	}
	return out
}

// RandID returns a short random hex identifier.
func RandID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// SortedKeys returns map keys sorted.
func SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Decode unmarshals an action body.
func Decode[T any](body json.RawMessage) (T, error) {
	var v T
	if len(body) == 0 {
		return v, nil
	}
	err := json.Unmarshal(body, &v)
	return v, err
}

// FmtTime formats a time for status output ("" when zero).
func FmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}
