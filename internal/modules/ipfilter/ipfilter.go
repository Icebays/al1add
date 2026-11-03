// Package ipfilter implements named whitelists / blacklists that other
// modules reference by ID.
package ipfilter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/icebays/al1add/internal/core"
)

// List is one named filter.
type List struct {
	ID      string   `json:"ID"`
	Name    string   `json:"Name"`
	Type    string   `json:"Type"` // white | black
	Entries []string `json:"Entries"`
	Remark  string   `json:"Remark"`
}

// Config of the module.
type Config struct {
	Lists []List `json:"Lists"`
}

type ipRange struct{ from, to net.IP }

type compiled struct {
	white    bool
	nets     []*net.IPNet
	ranges   []ipRange
	geo      map[string]bool
	matchAll bool
}

// Module implements core.IPFilterService.
type Module struct {
	app *core.App
	cfg Config
	mu  sync.RWMutex
	c   map[string]*compiled
}

// New creates the module.
func New(app *core.App) *Module {
	m := &Module{app: app}
	app.IPFilter = m
	return m
}

func (m *Module) Name() string  { return "ipfilter" }
func (m *Module) Title() string { return "IP過濾" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset()        { m.cfg = Config{} }
func (m *Module) Stop()         {}

// Normalize assigns IDs.
func (m *Module) Normalize() {
	for i := range m.cfg.Lists {
		l := &m.cfg.Lists[i]
		if l.ID == "" {
			l.ID = core.RandID()
		}
		if l.Type != "black" {
			l.Type = "white"
		}
	}
}

// Validate checks entries.
func (m *Module) Validate() error {
	for _, l := range m.cfg.Lists {
		if _, err := compile(l); err != nil {
			return fmt.Errorf("list %q: %w", l.Name, err)
		}
	}
	return nil
}

// Start compiles the lists.
func (m *Module) Start() error {
	c := map[string]*compiled{}
	for _, l := range m.cfg.Lists {
		cl, err := compile(l)
		if err != nil {
			return err
		}
		c[l.ID] = cl
	}
	m.mu.Lock()
	m.c = c
	m.mu.Unlock()
	return nil
}

func compile(l List) (*compiled, error) {
	c := &compiled{white: l.Type != "black", geo: map[string]bool{}}
	for _, raw := range l.Entries {
		e := strings.TrimSpace(raw)
		if i := strings.IndexByte(e, '#'); i >= 0 {
			e = strings.TrimSpace(e[:i])
		}
		if e == "" {
			continue
		}
		switch {
		case e == "*" || e == "all":
			c.matchAll = true
		case strings.HasPrefix(strings.ToLower(e), "geo:"):
			c.geo[strings.ToUpper(strings.TrimSpace(e[4:]))] = true
		case strings.Contains(e, "/"):
			_, n, err := net.ParseCIDR(e)
			if err != nil {
				return nil, fmt.Errorf("bad CIDR %q", e)
			}
			c.nets = append(c.nets, n)
		case strings.Contains(e, "-"):
			a, b, _ := strings.Cut(e, "-")
			ia, ib := norm(net.ParseIP(strings.TrimSpace(a))), norm(net.ParseIP(strings.TrimSpace(b)))
			if ia == nil || ib == nil || len(ia) != len(ib) {
				return nil, fmt.Errorf("bad range %q", e)
			}
			c.ranges = append(c.ranges, ipRange{ia, ib})
		case strings.EqualFold(e, "lan"):
			for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "169.254.0.0/16", "::1/128", "fc00::/7", "fe80::/10"} {
				_, n, _ := net.ParseCIDR(cidr)
				c.nets = append(c.nets, n)
			}
		default:
			ip := norm(net.ParseIP(e))
			if ip == nil {
				return nil, fmt.Errorf("bad IP %q", e)
			}
			bits := len(ip) * 8
			c.nets = append(c.nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
	return c, nil
}

func norm(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip.To16()
}

func (c *compiled) match(ip net.IP, geo core.GeoService) bool {
	if c.matchAll {
		return true
	}
	ip = norm(ip)
	if ip == nil {
		return false
	}
	for _, n := range c.nets {
		if n.Contains(ip) {
			return true
		}
	}
	for _, r := range c.ranges {
		if len(r.from) == len(ip) && bytes.Compare(ip, r.from) >= 0 && bytes.Compare(ip, r.to) <= 0 {
			return true
		}
	}
	if len(c.geo) > 0 && geo != nil {
		if cc := geo.Country(ip); cc != "" && c.geo[cc] {
			return true
		}
	}
	return false
}

// Allow reports whether ip passes list listID. Unknown lists deny.
func (m *Module) Allow(listID string, ip net.IP) bool {
	if listID == "" {
		return true
	}
	m.mu.RLock()
	c := m.c[listID]
	m.mu.RUnlock()
	if c == nil {
		return false
	}
	hit := c.match(ip, m.app.Geo)
	if c.white {
		return hit
	}
	return !hit
}

// Action: test {List, IP}.
func (m *Module) Action(ctx context.Context, action string, body json.RawMessage) (any, error) {
	switch action {
	case "test":
		req, err := core.Decode[struct{ List, IP string }](body)
		if err != nil {
			return nil, err
		}
		ip := net.ParseIP(strings.TrimSpace(req.IP))
		if ip == nil {
			return nil, errors.New("invalid IP")
		}
		country := ""
		if m.app.Geo != nil {
			country = m.app.Geo.Country(ip)
		}
		return map[string]any{"allowed": m.Allow(req.List, ip), "country": country}, nil
	}
	return nil, fmt.Errorf("unknown action %q", action)
}
