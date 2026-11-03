// Package vars implements global variables such as {NowDatetime} that can be
// used in webhooks, backend addresses and commands.
package vars

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	mu        sync.RWMutex
	providers = map[string]func() string{}
	resolvers []func(name string) (string, bool)
	startTime = time.Now()
	pattern   = regexp.MustCompile(`\{([A-Za-z0-9_\-\.\p{Han}]+)\}`)
)

// Register adds a named variable provider.
func Register(name string, fn func() string) {
	mu.Lock()
	providers[name] = fn
	mu.Unlock()
}

// RegisterResolver adds a dynamic resolver (e.g. PF_<name>_STATUS).
func RegisterResolver(fn func(name string) (string, bool)) {
	mu.Lock()
	resolvers = append(resolvers, fn)
	mu.Unlock()
}

// Lookup resolves one variable name.
func Lookup(name string) (string, bool) {
	mu.RLock()
	fn := providers[name]
	rs := resolvers
	mu.RUnlock()
	if fn != nil {
		return fn(), true
	}
	for _, r := range rs {
		if v, ok := r(name); ok {
			return v, true
		}
	}
	return "", false
}

// Expand replaces {Name} placeholders. extra takes precedence over globals.
// Unknown placeholders are left untouched.
func Expand(s string, extra map[string]string) string {
	if !strings.Contains(s, "{") {
		return s
	}
	return pattern.ReplaceAllStringFunc(s, func(m string) string {
		name := m[1 : len(m)-1]
		if v, ok := extra[name]; ok {
			return v
		}
		if v, ok := Lookup(name); ok {
			return v
		}
		return m
	})
}

// All returns a snapshot of every registered static variable.
func All() map[string]string {
	mu.RLock()
	names := make([]string, 0, len(providers))
	for n := range providers {
		names = append(names, n)
	}
	mu.RUnlock()
	sort.Strings(names)
	out := map[string]string{}
	for _, n := range names {
		if v, ok := Lookup(n); ok {
			out[n] = v
		}
	}
	return out
}

// StartTime is the process start time.
func StartTime() time.Time { return startTime }

// HumanBytes formats a byte count, returning value and unit separately.
func HumanBytes(b uint64) (string, string) {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(b)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	return strconv.FormatFloat(f, 'f', 1, 64), units[i]
}

func memInfo() (total, avail uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			total = v * 1024
		case "MemAvailable:":
			avail = v * 1024
		}
	}
	return
}

var cpuMu sync.Mutex
var lastIdle, lastTotal uint64
var lastCPU string = "0"

func systemCPU() string {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return "0"
	}
	line := strings.SplitN(string(data), "\n", 2)[0]
	f := strings.Fields(line)
	if len(f) < 5 {
		return "0"
	}
	var total, idle uint64
	for i, s := range f[1:] {
		v, _ := strconv.ParseUint(s, 10, 64)
		total += v
		if i == 3 || i == 4 {
			idle += v
		}
	}
	cpuMu.Lock()
	defer cpuMu.Unlock()
	if lastTotal != 0 && total > lastTotal {
		dt := float64(total - lastTotal)
		di := float64(idle - lastIdle)
		lastCPU = strconv.FormatFloat((1-di/dt)*100, 'f', 1, 64)
	}
	lastIdle, lastTotal = idle, total
	return lastCPU
}

func init() {
	Register("NowTimestamp", func() string { return strconv.FormatInt(time.Now().Unix(), 10) })
	Register("NowDatetime", func() string { return time.Now().Format("2006-01-02 15:04:05") })
	Register("StartTime", func() string { return startTime.Format("2006-01-02 15:04:05") })
	Register("Uptime", func() string { return time.Since(startTime).Round(time.Second).String() })
	Register("TotalCoroutines", func() string { return strconv.Itoa(runtime.NumGoroutine()) })
	Register("Hostname", func() string { h, _ := os.Hostname(); return h })
	Register("OS", func() string { return runtime.GOOS + "/" + runtime.GOARCH })
	proc := func() (string, string) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return HumanBytes(m.Sys)
	}
	Register("MemoryUsage", func() string { v, u := proc(); return v + u })
	Register("MemoryUsageVal", func() string { v, _ := proc(); return v })
	Register("MemoryUsageUnit", func() string { _, u := proc(); return u })
	sys := func(kind string) (string, string) {
		t, a := memInfo()
		switch kind {
		case "used":
			return HumanBytes(t - a)
		case "free":
			return HumanBytes(a)
		}
		return HumanBytes(t)
	}
	for _, k := range []struct{ name, kind string }{
		{"SystemMemoryUsage", "used"}, {"SystemUnusedMemory", "free"}, {"SystemTotalMemory", "total"},
	} {
		k := k
		Register(k.name, func() string { v, u := sys(k.kind); return v + u })
		Register(k.name+"Val", func() string { v, _ := sys(k.kind); return v })
		Register(k.name+"Unit", func() string { _, u := sys(k.kind); return u })
	}
	Register("SystemCPUUsage", systemCPU)
	Register("NumCPU", func() string { return fmt.Sprint(runtime.NumCPU()) })
}
