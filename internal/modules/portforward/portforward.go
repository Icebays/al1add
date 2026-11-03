// Package portforward relays TCP and UDP traffic from a listening port to a
// target address, with per-rule access control (IP filter + security group)
// and live connection/traffic statistics.
package portforward

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/logx"
	"github.com/icebays/al1add/internal/vars"
)

// Rule is one forwarding rule.
type Rule struct {
	ID         string `json:"ID"`
	Name       string `json:"Name"`
	Enable     bool   `json:"Enable"`
	Protocol   string `json:"Protocol"` // tcp | udp | tcp+udp
	ListenIP   string `json:"ListenIP"`
	ListenPort int    `json:"ListenPort"`
	TargetAddr string `json:"TargetAddr"` // host:port (variables allowed)
	// access control
	IPFilter  string `json:"IPFilter"`
	GroupID   string `json:"GroupID"`
	GroupMode string `json:"GroupMode"`
	Remark    string `json:"Remark"`
}

// Config of the module.
type Config struct {
	Rules []Rule `json:"Rules"`
}

type ruleStat struct {
	tcpConns atomic.Int64
	udpSess  atomic.Int64
	rx       atomic.Int64
	tx       atomic.Int64
}

type runner struct {
	rule     Rule
	tcpLn    net.Listener
	udpConn  *net.UDPConn
	stat     *ruleStat
	stop     chan struct{}
	wg       sync.WaitGroup
	udpMu    sync.Mutex
	udpPeers map[string]*net.UDPConn
}

// Module runs port-forward rules.
type Module struct {
	app     *core.App
	log     *logx.Logger
	cfg     Config
	mu      sync.Mutex
	runners map[string]*runner
}

// New creates the module.
func New(app *core.App) *Module {
	m := &Module{app: app, log: app.Logs.L("portforward"), runners: map[string]*runner{}}
	vars.RegisterResolver(func(name string) (string, bool) {
		if n, ok := strings.CutPrefix(name, "PF_"); ok {
			if rn, ok := strings.CutSuffix(n, "_STATUS"); ok {
				m.mu.Lock()
				defer m.mu.Unlock()
				for _, r := range m.cfg.Rules {
					if r.Name == rn {
						if _, running := m.runners[r.ID]; running {
							return "on", true
						}
						return "off", true
					}
				}
			}
		}
		return "", false
	})
	vars.Register("PortForwardingTCPTotalConnections", func() string { return fmt.Sprint(m.totalTCP()) })
	vars.Register("PortForwardingUDPTotalSessions", func() string { return fmt.Sprint(m.totalUDP()) })
	return m
}

func (m *Module) Name() string  { return "portforward" }
func (m *Module) Title() string { return "端口轉發" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset()        { m.cfg = Config{} }

// Normalize sets IDs and defaults.
func (m *Module) Normalize() {
	for i := range m.cfg.Rules {
		r := &m.cfg.Rules[i]
		if r.ID == "" {
			r.ID = core.RandID()
		}
		if r.Protocol == "" {
			r.Protocol = "tcp"
		}
	}
}

// Validate checks ports.
func (m *Module) Validate() error {
	for _, r := range m.cfg.Rules {
		if r.Enable {
			if r.ListenPort <= 0 || r.ListenPort > 65535 {
				return fmt.Errorf("rule %q: invalid listen port", r.Name)
			}
			if _, _, err := net.SplitHostPort(vars.Expand(r.TargetAddr, nil)); err != nil {
				return fmt.Errorf("rule %q: invalid target %q", r.Name, r.TargetAddr)
			}
		}
	}
	return nil
}

// Start launches enabled rules.
func (m *Module) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.cfg.Rules {
		r := m.cfg.Rules[i]
		if !r.Enable {
			continue
		}
		rn := &runner{rule: r, stat: &ruleStat{}, stop: make(chan struct{}), udpPeers: map[string]*net.UDPConn{}}
		if err := m.startRunner(rn); err != nil {
			m.log.Errorf("rule %q: %v", r.Name, err)
			continue
		}
		m.runners[r.ID] = rn
		m.log.Infof("rule %q listening on %s:%d (%s) -> %s", r.Name, r.ListenIP, r.ListenPort, r.Protocol, r.TargetAddr)
	}
	return nil
}

// Stop halts every rule.
func (m *Module) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, rn := range m.runners {
		rn.close()
		delete(m.runners, id)
	}
}

func (m *Module) startRunner(rn *runner) error {
	addr := net.JoinHostPort(rn.rule.ListenIP, fmt.Sprint(rn.rule.ListenPort))
	proto := rn.rule.Protocol
	if proto == "tcp" || proto == "tcp+udp" {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		rn.tcpLn = ln
		rn.wg.Add(1)
		go m.serveTCP(rn)
	}
	if proto == "udp" || proto == "tcp+udp" {
		ua, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			rn.close()
			return err
		}
		uc, err := net.ListenUDP("udp", ua)
		if err != nil {
			rn.close()
			return err
		}
		rn.udpConn = uc
		rn.wg.Add(1)
		go m.serveUDP(rn)
	}
	return nil
}

func (rn *runner) close() {
	select {
	case <-rn.stop:
	default:
		close(rn.stop)
	}
	if rn.tcpLn != nil {
		rn.tcpLn.Close()
	}
	if rn.udpConn != nil {
		rn.udpConn.Close()
	}
	rn.udpMu.Lock()
	for _, p := range rn.udpPeers {
		p.Close()
	}
	rn.udpMu.Unlock()
	rn.wg.Wait()
}

func (m *Module) allow(r Rule, ip net.IP) bool {
	return m.app.CheckAccess(ip, r.IPFilter, r.GroupID, r.GroupMode)
}

func (m *Module) serveTCP(rn *runner) {
	defer rn.wg.Done()
	for {
		conn, err := rn.tcpLn.Accept()
		if err != nil {
			select {
			case <-rn.stop:
				return
			default:
				return
			}
		}
		go m.handleTCP(rn, conn)
	}
}

func (m *Module) handleTCP(rn *runner, in net.Conn) {
	defer in.Close()
	ip := core.HostIP(in.RemoteAddr().String())
	if !m.allow(rn.rule, ip) {
		return
	}
	target := vars.Expand(rn.rule.TargetAddr, nil)
	out, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		m.log.Warnf("rule %q: dial %s: %v", rn.rule.Name, target, err)
		return
	}
	defer out.Close()
	rn.stat.tcpConns.Add(1)
	defer rn.stat.tcpConns.Add(-1)

	done := make(chan struct{}, 2)
	go func() { n, _ := copyData(out, in); rn.stat.rx.Add(n); done <- struct{}{} }()
	go func() { n, _ := copyData(in, out); rn.stat.tx.Add(n); done <- struct{}{} }()
	select {
	case <-done:
	case <-rn.stop:
	}
}

func (m *Module) serveUDP(rn *runner) {
	defer rn.wg.Done()
	buf := make([]byte, 64*1024)
	for {
		n, src, err := rn.udpConn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if !m.allow(rn.rule, src.IP) {
			continue
		}
		m.handleUDP(rn, src, append([]byte(nil), buf[:n]...))
	}
}

func (m *Module) handleUDP(rn *runner, src *net.UDPAddr, data []byte) {
	key := src.String()
	rn.udpMu.Lock()
	peer := rn.udpPeers[key]
	if peer == nil {
		target := vars.Expand(rn.rule.TargetAddr, nil)
		ta, err := net.ResolveUDPAddr("udp", target)
		if err != nil {
			rn.udpMu.Unlock()
			return
		}
		peer, err = net.DialUDP("udp", nil, ta)
		if err != nil {
			rn.udpMu.Unlock()
			return
		}
		rn.udpPeers[key] = peer
		rn.stat.udpSess.Add(1)
		go m.udpReturn(rn, src, peer, key)
	}
	rn.udpMu.Unlock()
	_ = peer.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := peer.Write(data); err == nil {
		rn.stat.tx.Add(int64(len(data)))
	}
}

func (m *Module) udpReturn(rn *runner, src *net.UDPAddr, peer *net.UDPConn, key string) {
	buf := make([]byte, 64*1024)
	for {
		_ = peer.SetReadDeadline(time.Now().Add(60 * time.Second))
		n, err := peer.Read(buf)
		if err != nil {
			rn.udpMu.Lock()
			delete(rn.udpPeers, key)
			rn.udpMu.Unlock()
			peer.Close()
			rn.stat.udpSess.Add(-1)
			return
		}
		if _, err := rn.udpConn.WriteToUDP(buf[:n], src); err == nil {
			rn.stat.rx.Add(int64(n))
		}
	}
}

func (m *Module) totalTCP() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var t int64
	for _, rn := range m.runners {
		t += rn.stat.tcpConns.Load()
	}
	return t
}

func (m *Module) totalUDP() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var t int64
	for _, rn := range m.runners {
		t += rn.stat.udpSess.Load()
	}
	return t
}

// Status reports per-rule live stats.
func (m *Module) Status() any {
	m.mu.Lock()
	defer m.mu.Unlock()
	type ri struct {
		ID, Name, Protocol string
		Listen             string
		Target             string
		Running            bool
		TCPConns, UDPSess  int64
		RxBytes, TxBytes   int64
	}
	out := []ri{}
	for _, r := range m.cfg.Rules {
		info := ri{ID: r.ID, Name: r.Name, Protocol: r.Protocol,
			Listen: net.JoinHostPort(r.ListenIP, fmt.Sprint(r.ListenPort)), Target: r.TargetAddr}
		if rn, ok := m.runners[r.ID]; ok {
			info.Running = true
			info.TCPConns = rn.stat.tcpConns.Load()
			info.UDPSess = rn.stat.udpSess.Load()
			info.RxBytes = rn.stat.rx.Load()
			info.TxBytes = rn.stat.tx.Load()
		}
		out = append(out, info)
	}
	return map[string]any{"rules": out}
}

// Action has no commands yet.
func (m *Module) Action(ctx context.Context, action string, body json.RawMessage) (any, error) {
	return nil, fmt.Errorf("unknown action %q", action)
}
