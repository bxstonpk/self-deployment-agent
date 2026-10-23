package hostinfo

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const statA = `cpu  100 0 100 700 100 0 0 0 0 0
cpu0 50 0 50 350 50 0 0 0 0 0
intr 1 2 3
`

// 400 more jiffies: 100 user, 100 system, 150 idle, 50 iowait -> 50% busy.
const statB = `cpu  200 0 200 850 150 0 0 0 0 0
cpu0 100 0 100 425 75 0 0 0 0 0
`

const meminfo = `MemTotal:        3456064 kB
MemFree:          700000 kB
MemAvailable:    2000000 kB
Buffers:           10000 kB
`

func TestParseCPUStat(t *testing.T) {
	idle, total, err := ParseCPUStat(statA)
	if err != nil || idle != 800 || total != 1000 {
		t.Fatalf("got idle=%d total=%d err=%v, want 800/1000", idle, total, err)
	}
	// guest columns are already inside user/nice and must not be counted twice.
	if _, total, _ := ParseCPUStat("cpu  10 0 10 80 0 0 0 0 999 999\n"); total != 100 {
		t.Fatalf("guest jiffies counted twice: total=%d", total)
	}
	if _, _, err := ParseCPUStat("intr 1 2\n"); err == nil {
		t.Fatal("expected an error without the aggregate cpu line")
	}
}

func TestParseMeminfo(t *testing.T) {
	total, available, err := ParseMeminfo(meminfo)
	if err != nil || total != 3456064*1024 || available != 2000000*1024 {
		t.Fatalf("got total=%d available=%d err=%v", total, available, err)
	}
	if _, _, err := ParseMeminfo("MemTotal: 10 kB\n"); err == nil {
		t.Fatal("expected an error without MemAvailable")
	}
}

func TestParseLoadavg(t *testing.T) {
	l1, l5, l15, err := ParseLoadavg("2.31 2.15 1.45 3/412 12345\n")
	if err != nil || l1 != 2.31 || l5 != 2.15 || l15 != 1.45 {
		t.Fatalf("got %v %v %v %v", l1, l5, l15, err)
	}
}

func writeProc(t *testing.T, dir, stat string) {
	t.Helper()
	for name, content := range map[string]string{
		"stat": stat, "meminfo": meminfo, "loadavg": "0.50 0.40 0.30 1/100 42\n", "uptime": "5000.25 9000.00\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSampler_CPUIsTheBusyShareBetweenReadings(t *testing.T) {
	dir := t.TempDir()
	s := NewSampler(dir, 3)
	if s.Status().Available {
		t.Fatal("nothing read yet, so nothing is available")
	}

	writeProc(t, dir, statA)
	t0 := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	if err := s.Sample(t0); err != nil {
		t.Fatal(err)
	}
	if len(s.History()) != 0 {
		t.Fatal("the first reading has no CPU figure and must not be charted")
	}
	st := s.Status()
	if !st.Available || st.MemoryTotal != 3456064*1024 || st.MemoryUsed != (3456064-2000000)*1024 || st.Load1 != 0.5 || st.UptimeSeconds != 5000.25 {
		t.Fatalf("unexpected status %+v", st)
	}

	writeProc(t, dir, statB)
	if err := s.Sample(t0.Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := s.Status().CPUPercent; math.Abs(got-50) > 0.001 {
		t.Fatalf("cpu = %v, want 50", got)
	}
	h := s.History()
	if len(h) != 1 || math.Abs(h[0].CPUPercent-50) > 0.001 {
		t.Fatalf("history %+v", h)
	}
}

func TestSampler_KeepsOnlyCapacity(t *testing.T) {
	dir := t.TempDir()
	s := NewSampler(dir, 2)
	writeProc(t, dir, statA)
	t0 := time.Now()
	for i := 0; i < 5; i++ {
		if err := s.Sample(t0.Add(time.Duration(i) * time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if h := s.History(); len(h) != 2 || !h[1].At.Equal(t0.Add(4*time.Second)) {
		t.Fatalf("expected the last 2 readings, got %+v", h)
	}
}

func TestSampler_NoProcIsUnavailableNotAnError(t *testing.T) {
	s := NewSampler(filepath.Join(t.TempDir(), "missing"), 2)
	if err := s.Sample(time.Now()); err == nil {
		t.Fatal("expected an error reading a missing /proc")
	}
	if s.Status().Available {
		t.Fatal("must stay unavailable")
	}
}
