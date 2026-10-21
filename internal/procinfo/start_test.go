package procinfo

import (
	"errors"
	"os"
	"testing"
	"time"
)

// Claude returns the matched ancestor's start time with its pid.
func TestClaudeReturnsStart(t *testing.T) {
	start := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	look := func(pid int) (Info, error) {
		switch pid {
		case 10:
			return Info{PPID: 4242, Exe: "/x/cmgo-hook"}, nil
		case 4242:
			return Info{PPID: 1, Exe: "/h/.local/share/claude/versions/2.1.304", Start: start}, nil
		}
		return Info{}, errors.New("no such process")
	}
	if pid, got := Claude(10, look); pid != 4242 || !got.Equal(start) {
		t.Fatalf("Claude = %d, %v; want 4242, %v", pid, got, start)
	}
	if pid, got := Claude(4242, look); pid != 0 || !got.IsZero() {
		t.Fatalf("Claude(no ancestor) = %d, %v; want 0, zero time", pid, got)
	}
}

// The system lookup reports when a process started: this test binary started
// moments ago.
func TestSystemOwnProcessStart(t *testing.T) {
	self, err := System()(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if now := time.Now(); self.Start.After(now) || now.Sub(self.Start) > time.Hour {
		t.Fatalf("Start = %v, now %v", self.Start, now)
	}
}

// parseStatStart reads field 22 (starttime, clock ticks after boot) of a
// Linux /proc/<pid>/stat line; comm may hold spaces and ')'.
func TestParseStatStart(t *testing.T) {
	line := "77 (a) b) S 1 77 77 0 -1 4194304 0 0 0 0 5 3 0 0 20 0 1 0 123456 1000 10\n"
	if ticks, err := parseStatStart(line); err != nil || ticks != 123456 {
		t.Fatalf("parseStatStart = %d, %v; want 123456", ticks, err)
	}
	if _, err := parseStatStart("77 (x) S 1 2"); err == nil {
		t.Fatal("short stat: want an error")
	}
}

// parseBtime reads the boot time from /proc/stat.
func TestParseBtime(t *testing.T) {
	if bt, err := parseBtime("cpu  1 2 3\nbtime 1792560000\nprocesses 9\n"); err != nil || bt != 1792560000 {
		t.Fatalf("parseBtime = %d, %v", bt, err)
	}
	if _, err := parseBtime("cpu 1\n"); err == nil {
		t.Fatal("no btime: want an error")
	}
}
