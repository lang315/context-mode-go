package mcpserver

import (
	"testing"
	"time"
)

func TestFloodGuardThresholdsAndWindow(t *testing.T) {
	var g floodGuard
	t0 := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 9; i++ {
		d := g.record(t0.Add(time.Duration(i) * time.Second))
		if d.count != i {
			t.Fatalf("call %d: count %d", i, d.count)
		}
		if d.softCapped != (i > 3) || d.blocked != (i > 8) {
			t.Fatalf("call %d: softCapped=%v blocked=%v", i, d.softCapped, d.blocked)
		}
		if !d.windowStart.Equal(t0.Add(time.Second)) {
			t.Fatalf("call %d: windowStart %v", i, d.windowStart)
		}
	}
	// Exactly 60s after the window start is still inside the window (TS uses >).
	if d := g.record(t0.Add(61 * time.Second)); d.count != 10 {
		t.Fatalf("at +60s: count %d, want 10", d.count)
	}
	d := g.record(t0.Add(62 * time.Second))
	if d.count != 1 || d.blocked || d.softCapped || !d.windowStart.Equal(t0.Add(62*time.Second)) {
		t.Fatalf("after window: %+v", d)
	}
}
