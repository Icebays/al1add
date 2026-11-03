// Package stun discovers the public IP:port mapping of a local UDP port using
// STUN, keeps the mapping alive, and reports changes through a webhook and the
// {STUN_<name>_*} global variables — useful behind full-cone NAT.
package stun

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/logx"
	"github.com/icebays/al1add/internal/vars"
	"github.com/icebays/al1add/internal/webhook"
)

// Task is one STUN keep-alive task.
type Task struct {
	ID          string         `json:"ID"`
	Name        string         `json:"Name"`
	Enable      bool           `json:"Enable"`
	LocalPort   int            `json:"LocalPort"`   // local UDP port to map (0 = ephemeral)
	STUNServer  string         `json:"STUNServer"`  // host:port
	IntervalSec int            `json:"IntervalSec"` // keep-alive interval
	Webhook     webhook.Config `json:"Webhook"`
	Remark      string         `json:"Remark"`

	LastAddr  string `json:"LastAddr"`
	LastOK    string `json:"LastOK"`
	LastError string `json:"LastError"`
}

// Config of the module.
type Config struct {
	Tasks []Task `json:"Tasks"`
}

// Module runs STUN tasks.
type Module struct {
	app  *core.App
	log  *logx.Logger
	cfg  Config
	mu   sync.Mutex
	stop chan struct{}
	wg   sync.WaitGroup
}

// New creates the module.
func New(app *core.App) *Module { return &Module{app: app, log: app.Logs.L("stun")} }

func (m *Module) Name() string  { return "stun" }
func (m *Module) Title() string { return "STUN內網穿透" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset()        { m.cfg = Config{} }

// Normalize sets defaults and registers variables.
func (m *Module) Normalize() {
	for i := range m.cfg.Tasks {
		t := &m.cfg.Tasks[i]
		if t.ID == "" {
			t.ID = core.RandID()
		}
		if t.IntervalSec <= 0 {
			t.IntervalSec = 30
		}
		if t.STUNServer == "" {
			t.STUNServer = "stun.miwifi.com:3478"
		}
		id := t.ID
		name := t.Name
		vars.Register("STUN_"+name+"_ADDR", func() string { return m.field(id, "addr") })
		vars.Register("STUN_"+name+"_IP", func() string { return m.field(id, "ip") })
		vars.Register("STUN_"+name+"_PORT", func() string { return m.field(id, "port") })
	}
}

func (m *Module) field(id, which string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.cfg.Tasks {
		if t.ID == id {
			host, port, err := net.SplitHostPort(t.LastAddr)
			if err != nil {
				return t.LastAddr
			}
			switch which {
			case "ip":
				return host
			case "port":
				return port
			default:
				return t.LastAddr
			}
		}
	}
	return ""
}

// Validate checks tasks.
func (m *Module) Validate() error {
	for _, t := range m.cfg.Tasks {
		if t.Enable {
			if _, err := resolveServer(t.STUNServer); err != nil {
				return fmt.Errorf("task %q: %w", t.Name, err)
			}
		}
	}
	return nil
}

// Start launches enabled tasks.
func (m *Module) Start() error {
	m.stop = make(chan struct{})
	for i := range m.cfg.Tasks {
		if m.cfg.Tasks[i].Enable {
			m.wg.Add(1)
			go m.run(m.cfg.Tasks[i].ID, m.stop)
		}
	}
	return nil
}

// Stop halts tasks.
func (m *Module) Stop() {
	if m.stop != nil {
		close(m.stop)
		m.stop = nil
		m.wg.Wait()
	}
}

func (m *Module) run(id string, stop chan struct{}) {
	defer m.wg.Done()
	m.mu.Lock()
	t := m.taskByID(id)
	if t == nil {
		m.mu.Unlock()
		return
	}
	task := *t
	m.mu.Unlock()

	laddr := &net.UDPAddr{Port: task.LocalPort}
	conn, err := net.ListenUDP("udp4", laddr)
	if err != nil {
		m.setErr(id, fmt.Sprintf("listen udp: %v", err))
		m.log.Errorf("task %q: %v", task.Name, err)
		return
	}
	defer conn.Close()
	server, err := resolveServer(task.STUNServer)
	if err != nil {
		m.setErr(id, err.Error())
		return
	}
	interval := time.Duration(task.IntervalSec) * time.Second
	tk := time.NewTimer(time.Second)
	defer tk.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tk.C:
			mp, err := bindingRequest(conn, server, 5*time.Second)
			if err != nil {
				m.setErr(id, err.Error())
			} else {
				addr := net.JoinHostPort(mp.IP.String(), strconv.Itoa(mp.Port))
				m.setAddr(id, addr, task)
			}
			tk.Reset(interval)
		}
	}
}

func (m *Module) taskByID(id string) *Task {
	for i := range m.cfg.Tasks {
		if m.cfg.Tasks[i].ID == id {
			return &m.cfg.Tasks[i]
		}
	}
	return nil
}

func (m *Module) setErr(id, msg string) {
	m.mu.Lock()
	if t := m.taskByID(id); t != nil {
		t.LastError = msg
	}
	m.mu.Unlock()
}

func (m *Module) setAddr(id, addr string, task Task) {
	m.mu.Lock()
	t := m.taskByID(id)
	if t == nil {
		m.mu.Unlock()
		return
	}
	changed := t.LastAddr != addr
	t.LastAddr = addr
	t.LastError = ""
	t.LastOK = time.Now().Format("2006-01-02 15:04:05")
	m.mu.Unlock()
	if changed {
		m.log.Infof("task %q public mapping: %s", task.Name, addr)
		_ = m.app.SaveCurrent(m.Name())
		if task.Webhook.Enable {
			host, port, _ := net.SplitHostPort(addr)
			go task.Webhook.Send(context.Background(), map[string]string{
				"STUN_ADDR": addr, "STUN_IP": host, "STUN_PORT": port, "STUN_NAME": task.Name,
			})
		}
	}
}

// Status lists tasks.
func (m *Module) Status() any {
	m.mu.Lock()
	defer m.mu.Unlock()
	type ti struct {
		ID, Name, LastAddr, LastOK, LastError string
		Enable                                bool
	}
	out := []ti{}
	for _, t := range m.cfg.Tasks {
		out = append(out, ti{t.ID, t.Name, t.LastAddr, t.LastOK, t.LastError, t.Enable})
	}
	return map[string]any{"tasks": out}
}

// Action has no commands.
func (m *Module) Action(ctx context.Context, action string, body json.RawMessage) (any, error) {
	return nil, fmt.Errorf("unknown action %q", action)
}
