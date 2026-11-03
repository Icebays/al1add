// Package procmgr supervises an external process: start, stop and auto-restart
// with backoff, streaming its output to a module logger.
package procmgr

import (
	"context"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/icebays/al1add/internal/logx"
)

// Spec describes a process to run.
type Spec struct {
	Path string
	Args []string
	Env  []string
	Dir  string
}

// Proc supervises one process.
type Proc struct {
	log   *logx.Logger
	mu    sync.Mutex
	spec  Spec
	cmd   *exec.Cmd
	stop  chan struct{}
	run   bool
	err   string
	since time.Time
}

// New creates a supervisor.
func New(log *logx.Logger) *Proc { return &Proc{log: log} }

// Start (re)launches the process with the given spec and keeps it alive.
func (p *Proc) Start(spec Spec) {
	p.Stop()
	p.mu.Lock()
	p.spec = spec
	p.stop = make(chan struct{})
	stop := p.stop
	p.run = true
	p.err = ""
	p.mu.Unlock()
	go p.supervise(stop)
}

func (p *Proc) supervise(stop chan struct{}) {
	backoff := time.Second
	for {
		select {
		case <-stop:
			return
		default:
		}
		cmd := exec.Command(p.spec.Path, p.spec.Args...)
		cmd.Env = p.spec.Env
		cmd.Dir = p.spec.Dir
		cmd.Stdout = &logx.Writer{L: p.log, Level: "INFO"}
		cmd.Stderr = &logx.Writer{L: p.log, Level: "INFO"}
		if err := cmd.Start(); err != nil {
			p.setErr(err.Error())
			p.log.Errorf("start %s: %v", p.spec.Path, err)
			if !p.wait(stop, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}
		p.mu.Lock()
		p.cmd = cmd
		p.since = time.Now()
		p.err = ""
		p.mu.Unlock()
		p.log.Infof("process started: %s", p.spec.Path)

		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-stop:
			p.kill(cmd)
			<-done
			return
		case err := <-done:
			ran := time.Since(p.since)
			if err != nil {
				p.setErr(err.Error())
				p.log.Warnf("process exited: %v", err)
			} else {
				p.log.Infof("process exited")
			}
			if ran > 30*time.Second {
				backoff = time.Second // ran long enough, reset backoff
			}
			if !p.wait(stop, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
		}
	}
}

func (p *Proc) wait(stop chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-stop:
		return false
	case <-t.C:
		return true
	}
}

func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > 30*time.Second {
		return 30 * time.Second
	}
	return d
}

func (p *Proc) kill(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
	}
}

// Stop terminates the process and stops supervision.
func (p *Proc) Stop() {
	p.mu.Lock()
	if !p.run {
		p.mu.Unlock()
		return
	}
	p.run = false
	stop := p.stop
	cmd := p.cmd
	p.mu.Unlock()
	if stop != nil {
		close(stop)
	}
	if cmd != nil {
		p.kill(cmd)
	}
}

func (p *Proc) setErr(msg string) {
	p.mu.Lock()
	p.err = msg
	p.mu.Unlock()
}

// Running reports whether the process is currently up.
func (p *Proc) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.run && p.cmd != nil && p.cmd.Process != nil && p.err == ""
}

// Info returns running state, last error and uptime seconds.
func (p *Proc) Info() (bool, string, int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var up int64
	if p.run && !p.since.IsZero() {
		up = int64(time.Since(p.since).Seconds())
	}
	return p.run, p.err, up
}

var _ = context.Background
