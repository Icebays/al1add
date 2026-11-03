// Package storage defines named mounts (currently local directories) that
// other file services — WebDAV, FTP and the web static file server — reference
// by ID. It implements core.StorageService.
package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/icebays/al1add/internal/core"
)

// Mount is one storage definition.
type Mount struct {
	ID       string `json:"ID"`
	Name     string `json:"Name"`
	Type     string `json:"Type"` // local
	Path     string `json:"Path"`
	ReadOnly bool   `json:"ReadOnly"`
	Remark   string `json:"Remark"`
}

// Config of the module.
type Config struct {
	Mounts []Mount `json:"Mounts"`
}

// Module manages mounts.
type Module struct {
	app *core.App
	cfg Config
	mu  sync.RWMutex
}

// New creates the module.
func New(app *core.App) *Module {
	m := &Module{app: app}
	app.Storage = m
	return m
}

func (m *Module) Name() string  { return "storage" }
func (m *Module) Title() string { return "網絡存儲" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset()        { m.cfg = Config{} }
func (m *Module) Start() error  { return nil }
func (m *Module) Stop()         {}

// Normalize assigns IDs.
func (m *Module) Normalize() {
	for i := range m.cfg.Mounts {
		mo := &m.cfg.Mounts[i]
		if mo.ID == "" {
			mo.ID = core.RandID()
		}
		if mo.Type == "" {
			mo.Type = "local"
		}
	}
}

// Validate checks paths exist.
func (m *Module) Validate() error {
	for _, mo := range m.cfg.Mounts {
		if mo.Type == "local" && mo.Path != "" {
			if st, err := os.Stat(mo.Path); err != nil {
				return fmt.Errorf("mount %q: %v", mo.Name, err)
			} else if !st.IsDir() {
				return fmt.Errorf("mount %q: %s is not a directory", mo.Name, mo.Path)
			}
		}
	}
	return nil
}

// Root implements core.StorageService.
func (m *Module) Root(storageID string) (string, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, mo := range m.cfg.Mounts {
		if mo.ID == storageID {
			if mo.Type != "local" {
				return "", false, fmt.Errorf("storage type %q not supported", mo.Type)
			}
			return mo.Path, mo.ReadOnly, nil
		}
	}
	return "", false, fmt.Errorf("storage %q not found", storageID)
}

// List returns mounts for other modules' selectors.
func (m *Module) List() []Mount {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Mount(nil), m.cfg.Mounts...)
}

// Status lists mounts with free space.
func (m *Module) Status() any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	type mi struct {
		ID, Name, Type, Path string
		ReadOnly, OK         bool
	}
	out := []mi{}
	for _, mo := range m.cfg.Mounts {
		ok := true
		if mo.Type == "local" {
			if st, err := os.Stat(mo.Path); err != nil || !st.IsDir() {
				ok = false
			}
		}
		out = append(out, mi{mo.ID, mo.Name, mo.Type, mo.Path, mo.ReadOnly, ok})
	}
	return map[string]any{"mounts": out}
}

// Action has no commands.
func (m *Module) Action(ctx context.Context, action string, body json.RawMessage) (any, error) {
	return nil, fmt.Errorf("unknown action %q", action)
}
