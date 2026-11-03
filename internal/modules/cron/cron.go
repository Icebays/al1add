// Package cron runs scheduled jobs: shell commands or webhook calls, on a
// standard 5-field cron schedule.
package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/logx"
	"github.com/icebays/al1add/internal/vars"
	"github.com/icebays/al1add/internal/webhook"
)

// Job is one scheduled task.
type Job struct {
	ID       string         `json:"ID"`
	Name     string         `json:"Name"`
	Enable   bool           `json:"Enable"`
	Cron     string         `json:"Cron"`    // 5-field expression
	Type     string         `json:"Type"`    // command | webhook
	Command  string         `json:"Command"` // when Type=command
	Webhook  webhook.Config `json:"Webhook"` // when Type=webhook
	Remark   string         `json:"Remark"`

	LastRun    string `json:"LastRun"`
	LastStatus string `json:"LastStatus"`
}

// Config of the module.
type Config struct {
	Jobs []Job `json:"Jobs"`
}

// Module runs cron jobs.
type Module struct {
	app  *core.App
	log  *logx.Logger
	cfg  Config
	mu   sync.Mutex
	stop chan struct{}
}

// New creates the module.
func New(app *core.App) *Module { return &Module{app: app, log: app.Logs.L("cron")} }

func (m *Module) Name() string  { return "cron" }
func (m *Module) Title() string { return "計劃任務" }
func (m *Module) Config() any   { return &m.cfg }
func (m *Module) Reset()        { m.cfg = Config{} }

// Normalize sets IDs/defaults.
func (m *Module) Normalize() {
	for i := range m.cfg.Jobs {
		j := &m.cfg.Jobs[i]
		if j.ID == "" {
			j.ID = core.RandID()
		}
		if j.Type == "" {
			j.Type = "command"
		}
	}
}

// Validate checks cron expressions.
func (m *Module) Validate() error {
	for _, j := range m.cfg.Jobs {
		if _, err := parseSchedule(j.Cron); err != nil {
			return fmt.Errorf("job %q: %w", j.Name, err)
		}
	}
	return nil
}

// Start launches the scheduler loop (fires on each minute boundary).
func (m *Module) Start() error {
	m.stop = make(chan struct{})
	go m.loop(m.stop)
	return nil
}

// Stop halts the scheduler.
func (m *Module) Stop() {
	if m.stop != nil {
		close(m.stop)
		m.stop = nil
	}
}

func (m *Module) loop(stop chan struct{}) {
	// align to the next minute
	now := time.Now()
	next := now.Truncate(time.Minute).Add(time.Minute)
	t := time.NewTimer(time.Until(next))
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case tick := <-t.C:
			m.fire(tick.Truncate(time.Minute))
			next = next.Add(time.Minute)
			t.Reset(time.Until(next))
		}
	}
}

func (m *Module) fire(t time.Time) {
	m.mu.Lock()
	jobs := make([]Job, len(m.cfg.Jobs))
	copy(jobs, m.cfg.Jobs)
	m.mu.Unlock()
	for _, j := range jobs {
		if !j.Enable {
			continue
		}
		sch, err := parseSchedule(j.Cron)
		if err != nil || !sch.matches(t) {
			continue
		}
		go m.runJob(j.ID)
	}
}

func (m *Module) runJob(id string) {
	m.mu.Lock()
	var job *Job
	for i := range m.cfg.Jobs {
		if m.cfg.Jobs[i].ID == id {
			job = &m.cfg.Jobs[i]
			break
		}
	}
	if job == nil {
		m.mu.Unlock()
		return
	}
	j := *job
	m.mu.Unlock()

	status := "ok"
	switch j.Type {
	case "webhook":
		if _, err := j.Webhook.Send(context.Background(), nil); err != nil {
			status = "error: " + err.Error()
		}
	default:
		out, err := runCommand(vars.Expand(j.Command, nil))
		if err != nil {
			status = "error: " + err.Error()
		}
		if out != "" {
			m.log.Infof("job %q output: %s", j.Name, trim(out))
		}
	}
	m.log.Infof("job %q ran: %s", j.Name, status)
	m.mu.Lock()
	for i := range m.cfg.Jobs {
		if m.cfg.Jobs[i].ID == id {
			m.cfg.Jobs[i].LastRun = time.Now().Format("2006-01-02 15:04:05")
			m.cfg.Jobs[i].LastStatus = status
		}
	}
	m.mu.Unlock()
	_ = m.app.SaveCurrent(m.Name())
}

func runCommand(cmdline string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", cmdline)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", cmdline)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func trim(s string) string {
	if len(s) > 500 {
		return s[:500] + "…"
	}
	return s
}

// Status lists jobs.
func (m *Module) Status() any {
	m.mu.Lock()
	defer m.mu.Unlock()
	type ji struct {
		ID, Name, Cron, Type, LastRun, LastStatus string
		Enable                                    bool
	}
	out := []ji{}
	for _, j := range m.cfg.Jobs {
		out = append(out, ji{j.ID, j.Name, j.Cron, j.Type, j.LastRun, j.LastStatus, j.Enable})
	}
	return map[string]any{"jobs": out}
}

// Action: run {ID} fires a job now.
func (m *Module) Action(ctx context.Context, action string, body json.RawMessage) (any, error) {
	req, err := core.Decode[struct{ ID string }](body)
	if err != nil {
		return nil, err
	}
	switch action {
	case "run":
		found := false
		m.mu.Lock()
		for _, j := range m.cfg.Jobs {
			if j.ID == req.ID {
				found = true
			}
		}
		m.mu.Unlock()
		if !found {
			return nil, errors.New("job not found")
		}
		m.runJob(req.ID)
		return map[string]any{"ok": true}, nil
	}
	return nil, fmt.Errorf("unknown action %q", action)
}
