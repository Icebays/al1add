// Package ddns keeps DNS records pointed at the host's current public IP.
// It can obtain the IP from a network interface or an external "what is my IP"
// service, supports IPv4 and IPv6, regex filtering, and notifies a webhook on
// change.
package ddns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/dnsprov"
	"github.com/icebays/al1add/internal/logx"
	"github.com/icebays/al1add/internal/vars"
	"github.com/icebays/al1add/internal/webhook"
)

// Task is one DDNS task.
type Task struct {
	ID          string         `json:"ID"`
	Name        string         `json:"Name"`
	Enable      bool           `json:"Enable"`
	Stack       string         `json:"Stack"` // ipv4 | ipv6
	Creds       dnsprov.Creds  `json:"Creds"`
	Domains     []string       `json:"Domains"`
	TTL         int            `json:"TTL"`
	Proxied     bool           `json:"Proxied"` // Cloudflare orange cloud
	IntervalSec int            `json:"IntervalSec"`
	Source      string         `json:"Source"`    // interface | url
	Interface   string         `json:"Interface"` // when Source=interface
	URLs        []string       `json:"URLs"`      // when Source=url
	IPReg       string         `json:"IPReg"`     // optional regex to extract/select IP
	Webhook     webhook.Config `json:"Webhook"`
	Remark      string         `json:"Remark"`

	// runtime (persisted for display)
	LastIP     string `json:"LastIP"`
	LastUpdate string `json:"LastUpdate"`
	LastError  string `json:"LastError"`
}

// Config of the module.
type Config struct {
	Tasks []Task `json:"Tasks"`
}

// Module runs DDNS tasks.
type Module struct {
	app  *core.App
	log  *logx.Logger
	cfg  Config
	mu   sync.Mutex
	stop chan struct{}
	wg   sync.WaitGroup
}

// New creates the module.
func New(app *core.App) *Module {
	return &Module{app: app, log: app.Logs.L("ddns")}
}

func (m *Module) Name() string  { return "ddns" }
func (m *Module) Title() string { return "動態域名 DDNS" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset()        { m.cfg = Config{} }

// Normalize sets defaults.
func (m *Module) Normalize() {
	for i := range m.cfg.Tasks {
		t := &m.cfg.Tasks[i]
		if t.ID == "" {
			t.ID = core.RandID()
		}
		if t.Stack == "" {
			t.Stack = "ipv4"
		}
		if t.IntervalSec <= 0 {
			t.IntervalSec = 120
		}
		if t.Source == "" {
			t.Source = "url"
		}
		vars.Register(fmt.Sprintf("DDNS_%s_IP", t.Name), func(id string) func() string {
			return func() string { return m.taskIP(id) }
		}(t.ID))
	}
}

func (m *Module) taskIP(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.cfg.Tasks {
		if t.ID == id {
			return t.LastIP
		}
	}
	return ""
}

// Validate checks tasks.
func (m *Module) Validate() error {
	for _, t := range m.cfg.Tasks {
		if len(t.Domains) == 0 && t.Enable {
			return fmt.Errorf("task %q has no domains", t.Name)
		}
		if t.IPReg != "" {
			if _, err := regexp.Compile(t.IPReg); err != nil {
				return fmt.Errorf("task %q: bad IP regex: %w", t.Name, err)
			}
		}
		if t.Enable {
			if _, err := dnsprov.New(t.Creds); err != nil {
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
			go m.run(i, m.stop)
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

func (m *Module) run(idx int, stop chan struct{}) {
	defer m.wg.Done()
	m.mu.Lock()
	t := m.cfg.Tasks[idx]
	m.mu.Unlock()
	interval := time.Duration(t.IntervalSec) * time.Second
	tick := time.NewTimer(2 * time.Second) // first run shortly after start
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			m.once(idx)
			tick.Reset(interval)
		}
	}
}

func (m *Module) once(idx int) {
	m.mu.Lock()
	if idx >= len(m.cfg.Tasks) {
		m.mu.Unlock()
		return
	}
	t := m.cfg.Tasks[idx]
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ip, err := m.detectIP(ctx, t)
	if err != nil {
		m.record(idx, t.LastIP, fmt.Sprintf("detect IP: %v", err))
		return
	}
	prov, err := dnsprov.New(t.Creds)
	if err != nil {
		m.record(idx, t.LastIP, err.Error())
		return
	}
	rtype := "A"
	if t.Stack == "ipv6" {
		rtype = "AAAA"
	}
	changedAny := false
	for _, d := range t.Domains {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		changed, err := prov.Upsert(ctx, d, rtype, ip, t.TTL, t.Proxied)
		if err != nil {
			m.record(idx, t.LastIP, fmt.Sprintf("%s: %v", d, err))
			return
		}
		if changed {
			changedAny = true
			m.log.Infof("task %q: %s %s -> %s", t.Name, rtype, d, ip)
		}
	}
	m.record(idx, ip, "")
	if changedAny && t.Webhook.Enable {
		go func(wc webhook.Config, name, ip string) {
			_, werr := wc.Send(context.Background(), map[string]string{"DDNS_IP": ip, "DDNS_NAME": name})
			if werr != nil {
				m.log.Warnf("task %q webhook: %v", name, werr)
			}
		}(t.Webhook, t.Name, ip)
	}
}

func (m *Module) record(idx int, ip, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if idx >= len(m.cfg.Tasks) {
		return
	}
	t := &m.cfg.Tasks[idx]
	t.LastIP = ip
	t.LastError = errMsg
	if errMsg == "" {
		t.LastUpdate = time.Now().Format("2006-01-02 15:04:05")
	}
	_ = m.app.Store.Save(m.Name(), &m.cfg)
}

func (m *Module) detectIP(ctx context.Context, t Task) (string, error) {
	wantV6 := t.Stack == "ipv6"
	var reg *regexp.Regexp
	if t.IPReg != "" {
		reg = regexp.MustCompile(t.IPReg)
	}
	switch t.Source {
	case "interface":
		return ipFromInterface(t.Interface, wantV6, reg)
	default:
		urls := t.URLs
		if len(urls) == 0 {
			if wantV6 {
				urls = []string{"https://6.ipw.cn", "https://api6.ipify.org"}
			} else {
				urls = []string{"https://4.ipw.cn", "https://api.ipify.org"}
			}
		}
		var last error
		for _, u := range urls {
			ip, err := ipFromURL(ctx, u, wantV6, reg)
			if err == nil {
				return ip, nil
			}
			last = err
		}
		return "", last
	}
}

func ipFromInterface(name string, wantV6 bool, reg *regexp.Regexp) (string, error) {
	var ifaces []net.Interface
	if name != "" {
		ifc, err := net.InterfaceByName(name)
		if err != nil {
			return "", err
		}
		ifaces = []net.Interface{*ifc}
	} else {
		all, err := net.Interfaces()
		if err != nil {
			return "", err
		}
		ifaces = all
	}
	for _, ifc := range ifaces {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			is6 := ip.To4() == nil
			if is6 != wantV6 {
				continue
			}
			if is6 && ip.IsPrivate() {
				continue // skip ULA, prefer global
			}
			s := ip.String()
			if reg != nil && !reg.MatchString(s) {
				continue
			}
			return s, nil
		}
	}
	return "", errors.New("no matching address on interface")
}

var ipPattern = regexp.MustCompile(`[0-9a-fA-F:.]+`)

func ipFromURL(ctx context.Context, url string, wantV6 bool, reg *regexp.Regexp) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	text := strings.TrimSpace(string(data))
	candidates := ipPattern.FindAllString(text, -1)
	for _, c := range candidates {
		ip := net.ParseIP(c)
		if ip == nil {
			continue
		}
		if (ip.To4() == nil) != wantV6 {
			continue
		}
		if reg != nil && !reg.MatchString(c) {
			continue
		}
		return ip.String(), nil
	}
	return "", fmt.Errorf("no %s address in response from %s", stack(wantV6), url)
}

func stack(v6 bool) string {
	if v6 {
		return "IPv6"
	}
	return "IPv4"
}

// Status lists tasks.
func (m *Module) Status() any {
	m.mu.Lock()
	defer m.mu.Unlock()
	type ti struct {
		ID, Name, Stack, LastIP, LastUpdate, LastError string
		Enable                                         bool
		Domains                                        []string
	}
	out := []ti{}
	for _, t := range m.cfg.Tasks {
		out = append(out, ti{t.ID, t.Name, t.Stack, t.LastIP, t.LastUpdate, t.LastError, t.Enable, t.Domains})
	}
	return map[string]any{"tasks": out}
}

// Action: update {ID} forces one run.
func (m *Module) Action(ctx context.Context, action string, body json.RawMessage) (any, error) {
	req, err := core.Decode[struct{ ID string }](body)
	if err != nil {
		return nil, err
	}
	switch action {
	case "update":
		m.mu.Lock()
		idx := -1
		for i := range m.cfg.Tasks {
			if m.cfg.Tasks[i].ID == req.ID {
				idx = i
				break
			}
		}
		m.mu.Unlock()
		if idx < 0 {
			return nil, errors.New("task not found")
		}
		m.once(idx)
		m.mu.Lock()
		t := m.cfg.Tasks[idx]
		m.mu.Unlock()
		if t.LastError != "" {
			return nil, errors.New(t.LastError)
		}
		return map[string]any{"ip": t.LastIP}, nil
	}
	return nil, fmt.Errorf("unknown action %q", action)
}
