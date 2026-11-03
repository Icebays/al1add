// Package ipdb provides IP → country lookups from a MaxMind-format (.mmdb)
// database, used by IP filter "geo:XX" entries.
package ipdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang"

	"github.com/icebays/al1add/internal/core"
)

// Config of the module.
type Config struct {
	Enable      bool   `json:"Enable"`
	DBPath      string `json:"DBPath"`
	DownloadURL string `json:"DownloadURL"`
}

// Module implements core.GeoService.
type Module struct {
	app *core.App
	cfg Config
	mu  sync.RWMutex
	db  *maxminddb.Reader
	err string
}

// New creates the module.
func New(app *core.App) *Module {
	m := &Module{app: app}
	app.Geo = m
	return m
}

func (m *Module) Name() string  { return "ipdb" }
func (m *Module) Title() string { return "IP地址庫" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset() {
	m.cfg = Config{DownloadURL: "https://cdn.jsdelivr.net/npm/geolite2-country/GeoLite2-Country.mmdb.gz"}
}

func (m *Module) defaultPath() string { return m.app.Store.Path("ipdb", "country.mmdb") }

// Start opens the database.
func (m *Module) Start() error {
	m.Stop()
	if !m.cfg.Enable {
		return nil
	}
	path := m.cfg.DBPath
	if path == "" {
		path = m.defaultPath()
	}
	db, err := maxminddb.Open(path)
	if err != nil {
		m.mu.Lock()
		m.err = err.Error()
		m.mu.Unlock()
		return fmt.Errorf("open %s: %w (use the download action first)", path, err)
	}
	m.mu.Lock()
	m.db, m.err = db, ""
	m.mu.Unlock()
	return nil
}

// Stop closes the database.
func (m *Module) Stop() {
	m.mu.Lock()
	if m.db != nil {
		m.db.Close()
		m.db = nil
	}
	m.mu.Unlock()
}

// Country returns the ISO country code of ip, or "".
func (m *Module) Country(ip net.IP) string {
	m.mu.RLock()
	db := m.db
	m.mu.RUnlock()
	if db == nil || ip == nil {
		return ""
	}
	var rec struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
		RegisteredCountry struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"registered_country"`
	}
	if err := db.Lookup(ip, &rec); err != nil {
		return ""
	}
	if rec.Country.ISOCode != "" {
		return rec.Country.ISOCode
	}
	return rec.RegisteredCountry.ISOCode
}

// Status reports database state.
func (m *Module) Status() any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st := map[string]any{"loaded": m.db != nil, "error": m.err}
	if m.db != nil {
		st["type"] = m.db.Metadata.DatabaseType
		st["built"] = time.Unix(int64(m.db.Metadata.BuildEpoch), 0).Format("2006-01-02")
		st["nodes"] = m.db.Metadata.NodeCount
	}
	return st
}

// Action: download, lookup {IP}.
func (m *Module) Action(ctx context.Context, action string, body json.RawMessage) (any, error) {
	switch action {
	case "lookup":
		req, err := core.Decode[struct{ IP string }](body)
		if err != nil {
			return nil, err
		}
		ip := net.ParseIP(strings.TrimSpace(req.IP))
		if ip == nil {
			return nil, errors.New("invalid IP")
		}
		return map[string]any{"country": m.Country(ip)}, nil
	case "download":
		if m.cfg.DownloadURL == "" {
			return nil, errors.New("download URL is empty")
		}
		path := m.cfg.DBPath
		if path == "" {
			path = m.defaultPath()
		}
		n, err := download(ctx, m.cfg.DownloadURL, path)
		if err != nil {
			return nil, err
		}
		m.app.Logs.L(m.Name()).Infof("downloaded IP database (%d bytes) to %s", n, path)
		if m.cfg.Enable {
			if err := m.Start(); err != nil {
				return nil, err
			}
		}
		return map[string]any{"bytes": n, "path": path}, nil
	}
	return nil, fmt.Errorf("unknown action %q", action)
}

func download(ctx context.Context, url, path string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var r io.Reader = resp.Body
	if strings.HasSuffix(url, ".gz") {
		gz, err := gzipReader(resp.Body)
		if err != nil {
			return 0, err
		}
		r = gz
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, io.LimitReader(r, 512<<20))
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return 0, err
	}
	if db, err := maxminddb.Open(tmp); err != nil {
		os.Remove(tmp)
		return 0, fmt.Errorf("downloaded file is not a valid mmdb: %w", err)
	} else {
		db.Close()
	}
	return n, os.Rename(tmp, path)
}
