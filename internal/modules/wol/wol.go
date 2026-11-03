// Package wol wakes LAN hosts by sending Wake-on-LAN magic packets, and can
// optionally check whether a host came online.
package wol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/icebays/al1add/internal/core"
)

// Device is a saved WOL target.
type Device struct {
	ID        string `json:"ID"`
	Name      string `json:"Name"`
	MAC       string `json:"MAC"`
	Broadcast string `json:"Broadcast"` // e.g. 192.168.1.255:9 (default 255.255.255.255:9)
	CheckHost string `json:"CheckHost"` // optional host:port to probe after waking
	Remark    string `json:"Remark"`
}

// Config of the module.
type Config struct {
	Devices []Device `json:"Devices"`
}

// Module sends WOL packets.
type Module struct {
	app *core.App
	cfg Config
	mu  sync.RWMutex
}

// New creates the module.
func New(app *core.App) *Module { return &Module{app: app} }

func (m *Module) Name() string  { return "wol" }
func (m *Module) Title() string { return "網絡喚醒 WOL" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset()        { m.cfg = Config{} }
func (m *Module) Start() error  { return nil }
func (m *Module) Stop()         {}

// Normalize assigns IDs.
func (m *Module) Normalize() {
	for i := range m.cfg.Devices {
		if m.cfg.Devices[i].ID == "" {
			m.cfg.Devices[i].ID = core.RandID()
		}
	}
}

// Validate checks MAC addresses.
func (m *Module) Validate() error {
	for _, d := range m.cfg.Devices {
		if _, err := parseMAC(d.MAC); err != nil {
			return fmt.Errorf("device %q: %w", d.Name, err)
		}
	}
	return nil
}

func parseMAC(s string) (net.HardwareAddr, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), "-", ":")
	mac, err := net.ParseMAC(s)
	if err != nil {
		return nil, fmt.Errorf("invalid MAC %q", s)
	}
	if len(mac) != 6 {
		return nil, fmt.Errorf("MAC %q must be 6 bytes", s)
	}
	return mac, nil
}

func magicPacket(mac net.HardwareAddr) []byte {
	pkt := make([]byte, 0, 102)
	for i := 0; i < 6; i++ {
		pkt = append(pkt, 0xff)
	}
	for i := 0; i < 16; i++ {
		pkt = append(pkt, mac...)
	}
	return pkt
}

// Wake sends the magic packet for a device.
func (m *Module) Wake(d Device) error {
	mac, err := parseMAC(d.MAC)
	if err != nil {
		return err
	}
	bcast := d.Broadcast
	if bcast == "" {
		bcast = "255.255.255.255:9"
	}
	if !strings.Contains(bcast, ":") {
		bcast += ":9"
	}
	addr, err := net.ResolveUDPAddr("udp", bcast)
	if err != nil {
		return err
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	pkt := magicPacket(mac)
	for i := 0; i < 3; i++ { // send a few for reliability
		if _, err := conn.Write(pkt); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	m.app.Logs.L(m.Name()).Infof("sent magic packet to %s (%s) via %s", d.Name, d.MAC, bcast)
	return nil
}

// Status returns the device list.
func (m *Module) Status() any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return map[string]any{"devices": m.cfg.Devices}
}

// Action: wake {ID}, probe {ID}.
func (m *Module) Action(ctx context.Context, action string, body json.RawMessage) (any, error) {
	req, err := core.Decode[struct{ ID string }](body)
	if err != nil {
		return nil, err
	}
	var dev *Device
	m.mu.RLock()
	for i := range m.cfg.Devices {
		if m.cfg.Devices[i].ID == req.ID {
			d := m.cfg.Devices[i]
			dev = &d
			break
		}
	}
	m.mu.RUnlock()
	if dev == nil {
		return nil, errors.New("device not found")
	}
	switch action {
	case "wake":
		if err := m.Wake(*dev); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	case "probe":
		if dev.CheckHost == "" {
			return nil, errors.New("device has no check host")
		}
		host := dev.CheckHost
		if !strings.Contains(host, ":") {
			host += ":80"
		}
		c, err := net.DialTimeout("tcp", host, 3*time.Second)
		if err != nil {
			return map[string]any{"online": false, "error": err.Error()}, nil
		}
		c.Close()
		return map[string]any{"online": true}, nil
	}
	return nil, fmt.Errorf("unknown action %q", action)
}
