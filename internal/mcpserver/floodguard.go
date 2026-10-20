package mcpserver

import (
	"sync"
	"time"
)

// ctx_search throttle defaults (S:2457-2459); the env overrides are dropped.
const (
	searchWindow     = 60 * time.Second
	searchSoftCapAt  = 3 // after this many calls: 1 result per query
	searchBlockAfter = 8 // after this many calls: refuse
)

// floodGuard is the ctx_search rolling-window call counter. TS keys it per
// agent context; one cmgo process serves one session, so one bucket suffices.
type floodGuard struct {
	mu          sync.Mutex
	count       int
	windowStart time.Time
}

type floodDecision struct {
	count       int
	windowStart time.Time
	blocked     bool
	softCapped  bool
}

// record counts one call at now. Port of FloodGuard.record (flood-guard.ts:75-90).
func (g *floodGuard) record(now time.Time) floodDecision {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.count == 0 || now.Sub(g.windowStart) > searchWindow {
		g.count, g.windowStart = 0, now
	}
	g.count++
	return floodDecision{
		count:       g.count,
		windowStart: g.windowStart,
		blocked:     g.count > searchBlockAfter,
		softCapped:  g.count > searchSoftCapAt,
	}
}
