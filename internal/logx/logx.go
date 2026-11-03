// Package logx keeps a ring buffer of recent log lines per module.
package logx

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// Entry is one log line.
type Entry struct {
	Time   time.Time `json:"time"`
	Module string    `json:"module"`
	Level  string    `json:"level"`
	Msg    string    `json:"msg"`
}

type ring struct {
	buf  []Entry
	next int
	full bool
}

func (r *ring) add(e Entry) {
	r.buf[r.next] = e
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

func (r *ring) last(n int) []Entry {
	var all []Entry
	if r.full {
		all = append(all, r.buf[r.next:]...)
	}
	all = append(all, r.buf[:r.next]...)
	if n > 0 && len(all) > n {
		all = all[len(all)-n:]
	}
	return all
}

// Hub collects logs from all modules.
type Hub struct {
	mu   sync.Mutex
	size int
	mods map[string]*ring
	all  *ring
}

// NewHub creates a hub keeping size entries per module.
func NewHub(size int) *Hub {
	return &Hub{size: size, mods: map[string]*ring{}, all: &ring{buf: make([]Entry, size*4)}}
}

// Logger writes into the hub for one module.
type Logger struct {
	h      *Hub
	module string
}

// L returns the logger of a module.
func (h *Hub) L(module string) *Logger { return &Logger{h: h, module: module} }

func (h *Hub) add(e Entry) {
	h.mu.Lock()
	r := h.mods[e.Module]
	if r == nil {
		r = &ring{buf: make([]Entry, h.size)}
		h.mods[e.Module] = r
	}
	r.add(e)
	h.all.add(e)
	h.mu.Unlock()
	log.Printf("[%s] %s %s", e.Module, e.Level, e.Msg)
}

// Get returns the last n entries of a module ("" for all modules).
func (h *Hub) Get(module string, n int) []Entry {
	h.mu.Lock()
	defer h.mu.Unlock()
	if module == "" {
		return h.all.last(n)
	}
	if r := h.mods[module]; r != nil {
		return r.last(n)
	}
	return []Entry{}
}

func (l *Logger) write(level, format string, a ...any) {
	l.h.add(Entry{Time: time.Now(), Module: l.module, Level: level, Msg: fmt.Sprintf(format, a...)})
}

// Infof logs at info level.
func (l *Logger) Infof(format string, a ...any) { l.write("INFO", format, a...) }

// Warnf logs at warn level.
func (l *Logger) Warnf(format string, a ...any) { l.write("WARN", format, a...) }

// Errorf logs at error level.
func (l *Logger) Errorf(format string, a ...any) { l.write("ERROR", format, a...) }

// Writer adapts the logger to io.Writer (one entry per Write call, trimmed).
type Writer struct {
	L     *Logger
	Level string
	buf   []byte
	mu    sync.Mutex
}

func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := indexByte(w.buf, '\n')
		if i < 0 {
			if len(w.buf) > 4096 {
				w.flush(w.buf)
				w.buf = w.buf[:0]
			}
			return len(p), nil
		}
		w.flush(w.buf[:i])
		w.buf = w.buf[i+1:]
	}
}

func (w *Writer) flush(line []byte) {
	s := string(line)
	if len(s) > 0 && s[len(s)-1] == '\r' {
		s = s[:len(s)-1]
	}
	if s == "" {
		return
	}
	lvl := w.Level
	if lvl == "" {
		lvl = "INFO"
	}
	w.L.write(lvl, "%s", s)
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
