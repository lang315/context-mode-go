package mcpserver

import (
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lang315/context-mode-go/internal/store"
)

// statsFlushWait bounds how long ctx_stats and ctx_purge wait for queued
// records to be written.
const statsFlushWait = time.Second

// statsQueue is how many records wait for the worker. When it is full a
// record is dropped: stats are best effort, a tool call is not.
const statsQueue = 256

// StatsRecorder writes this server's tool_stats rows (stats spec §1.3).
// Record only enqueues; one background worker writes, so a tool call never
// waits on the DB (another session may hold its write lock for a while).
// The zero value works; the worker starts with the first Record.
type StatsRecorder struct {
	ServerID  string // "<pid>-<start unix nanos>" of this cmgo serve process
	CCPID     int    // owning Claude Code process, 0 if unknown
	StartedAt string // server start, UTC 'YYYY-MM-DD HH:MM:SS'

	mu     sync.RWMutex
	ch     chan statJob
	closed bool
	done   chan struct{} // closed when the worker has stopped
}

// statJob is one finished call, or (flush set) a marker the worker answers
// once everything queued before it is written.
type statJob struct {
	st             *store.Store
	project, tool  string
	returned, kept int64
	flush          chan struct{}
}

// start creates the queue and the worker. The caller holds r.mu.
func (r *StatsRecorder) start() {
	r.ch = make(chan statJob, statsQueue)
	r.done = make(chan struct{})
	go func() {
		defer close(r.done)
		for j := range r.ch {
			if j.flush != nil {
				close(j.flush)
				continue
			}
			_ = j.st.AddToolStat(r.ServerID, r.CCPID, j.project, r.StartedAt, j.tool, j.returned, j.kept)
		}
	}()
}

// Record adds one finished call of tool: bytes_returned is the UTF-8 size of
// the result text, bytes_kept is max(0, raw - returned), where raw is what
// the tool produced or read. A nil recorder records nothing, a full queue or
// a closed recorder drops the call, and a failed write is dropped too: stats
// never fail or slow a tool.
func (r *StatsRecorder) Record(d *Deps, tool string, raw int64, res *mcp.CallToolResult) {
	if r == nil || res == nil {
		return
	}
	var returned int64
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			returned += int64(len(tc.Text))
		}
	}
	job := statJob{st: d.Store, project: d.Cfg.ProjectKey, tool: tool, returned: returned, kept: max(0, raw-returned)}
	r.mu.Lock() // not RLock: the first call starts the worker
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	if r.ch == nil {
		r.start()
	}
	select {
	case r.ch <- job:
	default: // full: drop
	}
}

// Flush waits until every record queued before the call is written, or
// timeout passes; it reports whether they were. Tests and ctx_stats use it so
// a report sees the calls before it.
func (r *StatsRecorder) Flush(timeout time.Duration) bool {
	if r == nil {
		return true
	}
	marker := make(chan struct{})
	r.mu.Lock()
	if r.closed || r.ch == nil {
		r.mu.Unlock()
		return true // nothing is queued
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case r.ch <- statJob{flush: marker}:
			r.mu.Unlock()
			select {
			case <-marker:
				return true
			case <-deadline.C:
				return false
			}
		case <-deadline.C:
			r.mu.Unlock()
			return false
		default:
			// Queue full: Record cannot enqueue while we hold the lock, so
			// release it briefly and let the worker make room.
			r.mu.Unlock()
			time.Sleep(time.Millisecond)
			r.mu.Lock()
			if r.closed {
				r.mu.Unlock()
				return true
			}
		}
	}
}

// Close stops accepting records and waits up to timeout for the worker to
// write what is queued. Serve calls it at shutdown with a short limit.
func (r *StatsRecorder) Close(timeout time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	ch, done := r.ch, r.done
	if ch != nil {
		close(ch)
	}
	r.mu.Unlock()
	if done == nil {
		return
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	}
}
