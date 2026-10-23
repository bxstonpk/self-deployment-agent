// Package hostinfo reads the platform host's own CPU, memory and load for
// the administrators' dashboard.
//
// platform-api runs in a container, but /proc/stat, /proc/meminfo and
// /proc/loadavg are not namespaced by Docker: inside the container they
// describe the whole host, which is exactly what the dashboard shows. On a
// machine without them (a developer's Windows or macOS) every reading is
// unavailable rather than invented.
package hostinfo

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Point is one reading of the host.
type Point struct {
	At          time.Time `json:"at"`
	CPUPercent  float64   `json:"cpu_percent"`
	MemoryUsed  int64     `json:"memory_used_bytes"`
	MemoryTotal int64     `json:"memory_total_bytes"`
}

// Status is the host now: the latest Point plus what doesn't need history.
type Status struct {
	Available     bool      `json:"available"`
	CPUCount      int       `json:"cpu_count"`
	CPUPercent    float64   `json:"cpu_percent"`
	MemoryUsed    int64     `json:"memory_used_bytes"`
	MemoryTotal   int64     `json:"memory_total_bytes"`
	Load1         float64   `json:"load_1"`
	Load5         float64   `json:"load_5"`
	Load15        float64   `json:"load_15"`
	UptimeSeconds float64   `json:"uptime_seconds"`
	SampledAt     time.Time `json:"sampled_at"`
}

// Sampler keeps a rolling history of host readings. CPU use is the share
// of non-idle time between two readings, so the first reading has none.
type Sampler struct {
	root     string
	capacity int

	mu        sync.Mutex
	prevIdle  uint64
	prevTotal uint64
	havePrev  bool
	history   []Point
	latest    Status
}

// NewSampler reads from procRoot (normally "/proc") and keeps the last
// capacity readings.
func NewSampler(procRoot string, capacity int) *Sampler {
	return &Sampler{root: procRoot, capacity: capacity}
}

// Sample takes one reading and adds it to the history.
func (s *Sampler) Sample(now time.Time) error {
	stat, err := os.ReadFile(filepath.Join(s.root, "stat"))
	if err != nil {
		return fmt.Errorf("read stat: %w", err)
	}
	idle, total, err := ParseCPUStat(string(stat))
	if err != nil {
		return err
	}
	meminfo, err := os.ReadFile(filepath.Join(s.root, "meminfo"))
	if err != nil {
		return fmt.Errorf("read meminfo: %w", err)
	}
	memTotal, memAvailable, err := ParseMeminfo(string(meminfo))
	if err != nil {
		return err
	}
	var l1, l5, l15 float64
	if raw, err := os.ReadFile(filepath.Join(s.root, "loadavg")); err == nil {
		l1, l5, l15, _ = ParseLoadavg(string(raw))
	}
	var uptime float64
	if raw, err := os.ReadFile(filepath.Join(s.root, "uptime")); err == nil {
		if f := strings.Fields(string(raw)); len(f) > 0 {
			uptime, _ = strconv.ParseFloat(f[0], 64)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	cpu := 0.0
	if s.havePrev && total > s.prevTotal {
		busy := float64((total - s.prevTotal) - (idle - s.prevIdle))
		cpu = 100 * busy / float64(total-s.prevTotal)
	}
	first := !s.havePrev
	s.prevIdle, s.prevTotal, s.havePrev = idle, total, true

	used := memTotal - memAvailable
	s.latest = Status{
		Available: true, CPUCount: runtime.NumCPU(), CPUPercent: cpu,
		MemoryUsed: used, MemoryTotal: memTotal,
		Load1: l1, Load5: l5, Load15: l15, UptimeSeconds: uptime, SampledAt: now,
	}
	if first {
		return nil // no CPU figure yet: not worth a point on a chart
	}
	s.history = append(s.history, Point{At: now, CPUPercent: cpu, MemoryUsed: used, MemoryTotal: memTotal})
	if len(s.history) > s.capacity {
		s.history = s.history[len(s.history)-s.capacity:]
	}
	return nil
}

// Status returns the latest reading; Available is false until one succeeds.
func (s *Sampler) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latest
}

// History returns the readings kept, oldest first.
func (s *Sampler) History() []Point {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Point, len(s.history))
	copy(out, s.history)
	return out
}

// ParseCPUStat returns the idle and total jiffies from /proc/stat's
// aggregate "cpu" line. Idle includes iowait: time waiting on the disk is
// not the CPU being busy.
func ParseCPUStat(content string) (idle, total uint64, err error) {
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		for i, f := range fields[1:] {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				return 0, 0, fmt.Errorf("parse /proc/stat: %w", err)
			}
			// guest and guest_nice (9th, 10th) are already counted in user
			// and nice.
			if i >= 8 {
				break
			}
			total += v
			if i == 3 || i == 4 { // idle, iowait
				idle += v
			}
		}
		return idle, total, nil
	}
	return 0, 0, fmt.Errorf("parse /proc/stat: no aggregate cpu line")
}

// ParseMeminfo returns MemTotal and MemAvailable in bytes. MemAvailable is
// what the kernel estimates can be used without swapping — the same figure
// `free` shows as available — so used is the rest.
func ParseMeminfo(content string) (total, available int64, err error) {
	var haveTotal, haveAvailable bool
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total, haveTotal = kb*1024, true
		case "MemAvailable:":
			available, haveAvailable = kb*1024, true
		}
	}
	if !haveTotal || !haveAvailable {
		return 0, 0, fmt.Errorf("parse /proc/meminfo: MemTotal or MemAvailable missing")
	}
	return total, available, nil
}

// ParseLoadavg returns the 1, 5 and 15 minute load averages.
func ParseLoadavg(content string) (l1, l5, l15 float64, err error) {
	f := strings.Fields(content)
	if len(f) < 3 {
		return 0, 0, 0, fmt.Errorf("parse /proc/loadavg: %q", content)
	}
	if l1, err = strconv.ParseFloat(f[0], 64); err != nil {
		return
	}
	if l5, err = strconv.ParseFloat(f[1], 64); err != nil {
		return
	}
	l15, err = strconv.ParseFloat(f[2], 64)
	return
}
