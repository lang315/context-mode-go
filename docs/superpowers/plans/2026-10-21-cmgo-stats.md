# cmgo stats and statusline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give cmgo measured statistics:
- what each tool kept out of context and what it returned, attributed to the right Claude Code session;
- a `ctx_stats` report;
- a read-only `cmgo-hook statusline` that runs in a few milliseconds.

**Architecture:**
- **`internal/procinfo` (new):** finds the Claude Code ancestor pid (`cc_pid`) of any process. `cmgo serve` and SessionStart both record it, so server stats link to the hook's session id.
- **`internal/store`:** migration 3 adds `tool_stats` (keyed by server, tool and session window) and `session_meta.cc_pid`.
- **`internal/mcpserver`:** records each call. `ctx_stats` renders through `internal/stats` (new, pure text).
- **`internal/hookcmd`:** serves `statusline` through the small binary.

**Tech Stack:**
- Go 1.27.2. `golang.org/x/sys` (already in the module graph) becomes a direct dependency for the darwin process lookup.
- Python 3 for the bench.

**Spec:** `docs/superpowers/specs/2026-10-21-cmgo-stats-design.md` (sub-project 4 of 5). It builds on the core, hooks and session-memory specs.

**How the code in this plan was produced:**
- Every code block was built and tested in a draft module on top of the finished sub-project 3 branch, then copied in here verbatim.
- The assembled plan was replayed task by task, Task 1 through 19, into a fresh worktree of `sp3-session-memory`, with `go vet` and the task's package tests run after each task. Appendix B has the result.
- Each code block is the complete file named on the line above it. When a later task shows a file again, replace the whole file.

## Global Constraints

- **Repo and branch:** repo `~/GolandProjects/github.com/lang315/context-mode-go`. Work on branch `sp4-stats`, stacked on `sp3-session-memory`.
- **Dependencies:**
  - The only `go.mod` change is `golang.org/x/sys` moving from indirect to direct (`go mod tidy`).
  - `cmd/cmgo-hook` must still not link the MCP SDK, `net/http`, HTML conversion, executor, fetch or mcpserver.
- **Platforms:** macOS and Linux only. Verified on macOS arm64; the Linux `/proc` parser is unit-tested only.
- **Tests:** use `t.TempDir()`. Never write to the real `~/.claude`, `~/.claude.json` or the TS repo, and never run `cmgo install` against the real home.
- **Stats never fail a tool:** a failed stats write is ignored.
- **The statusline never writes:** it opens the DB with `mode=ro`, runs no migrations, prints with no trailing newline, and always exits 0.
- **Definitions:** "kept" is `max(0, raw - returned)` per call, with raw as in spec §1.3. Redirect `bytes_avoided` values are reported separately.
- **Hook latency:** p95 ≤ 10 ms per case in `bench/hook-latency.py`, including the statusline on 100 sessions and 50,000 events.
- **Process markers:** never put a literal process marker in a shell command that also runs `pkill` or `pgrep`.
- **Commits:** Conventional Commits, in English, one per task.

## Review Focus

These are the five conditions most likely to hurt someone using the stats that the spec implies but does not list. Each has a test in the task that owns the code.

1. **Two Claude Code sessions in one project, and `/clear` inside one of them.** Each session must see only its own calls. `TestStatsLinksSessionByCCPID` and `TestStatsClearWindow` (Task 8).
2. **Claude Code redraws the statusline while another session writes the DB.** The statusline must not change the DB or its WAL. `TestStatusLine` asserts that the files are unchanged around the call (Task 17), and `TestOpenReadOnlyUnderWriteLock` covers the open under a held write lock (Task 9).
3. **The statusline runs before cmgo has ever run, or against an old schema.** It must print `cmgo  ●  ready` and exit 0. `TestStatusLineNoDB` and `TestStatusLineOldSchema` (Task 17).
4. **`cmgo serve` or a hook runs without a `claude` ancestor** (a terminal, another client). `cc_pid` must be 0 and nothing may link by accident. `TestHookSessionStartNoClaudeAncestor` (Task 16) and `TestStatsUnlinked` (Task 8).
5. **A project purge in a shared DB.** It removes that project's stats only. `TestPurgeProjectLeavesOtherProjects` (Task 10).

## File Structure

| Path | Responsibility |
|---|---|
| `internal/procinfo` | Parent-chain walk to the `claude` ancestor; darwin sysctl and Linux `/proc` lookups |
| `internal/stats` | `KB`, `Duration`, `Pct` (TS-exact), the `ctx_stats` report and the statusline text |
| `internal/store/stats.go`, `readonly.go` (+ migration 3) | `tool_stats`, `SetSessionCC`, `Stats`, `SessionLine`, `OpenReadOnly`, `UserVersion` |
| `internal/mcpserver/stats.go`, `ctxstats.go` | `StatsRecorder` with raw bytes per tool, the `ctx_stats` tool |
| `internal/hookcmd/statusline.go` | `cmgo-hook statusline` |
| `internal/install` | `statusLine` registration |
| `bench/hook-latency.py` | Statusline case |

## Task order

| Task | Content |
|---|---|
| 1 | Parent-chain walk with an injected lookup |
| 2 | Linux /proc/<pid>/stat parser |
| 3 | System process table lookup (darwin, Linux) |
| 4 | Byte sizes, durations and percentages |
| 5 | ctx_stats report text |
| 6 | Statusline line |
| 7 | Migration 3, AddToolStat and SetSessionCC |
| 8 | Stats snapshot with session linking by cc_pid |
| 9 | Read-only open and the statusline numbers |
| 10 | Project purge deletes tool_stats |
| 11 | StatsRecorder and the execute tools' raw bytes |
| 12 | Batch, fetch, search and purge stats; purge message |
| 13 | ctx_stats tool |
| 14 | Routing block ctx stats entry |
| 15 | serve sets the stats recorder |
| 16 | SessionStart records the Claude Code pid |
| 17 | The statusline event |
| 18 | Install the statusLine |
| 19 | Statusline latency bench |

Suggested review checkpoints, after:
- Task 6 (`procinfo` and `stats`);
- Task 15 (store, mcpserver, routing, serve);
- Task 19 (hooks, install, bench).

---

### Task 1: Parent-chain walk with an injected lookup

**Files:**
- Create: `internal/procinfo/procinfo.go`
- Test: `internal/procinfo/procinfo_test.go`

**Interfaces:**
- Consumes: none (stdlib only)
- Produces: `type Lookup func(pid int) (ppid int, name string, err error)`; `func ClaudePID(pid int, look Lookup) int`

- [ ] **Step 1: Write the failing test**

`internal/procinfo/procinfo_test.go`:
```go
package procinfo

import (
	"errors"
	"testing"
)

type proc struct {
	ppid int
	name string
}

// table turns a fake process table into a Lookup that counts its calls.
func table(procs map[int]proc, calls *int) Lookup {
	return func(pid int) (int, string, error) {
		*calls++
		p, ok := procs[pid]
		if !ok {
			return 0, "", errors.New("no such process")
		}
		return p.ppid, p.name, nil
	}
}

func TestClaudePID(t *testing.T) {
	cases := []struct {
		name  string
		procs map[int]proc
		pid   int
		want  int
	}{
		{"parent is claude", map[int]proc{100: {50, "cmgo"}, 50: {1, "claude"}, 1: {0, "launchd"}}, 100, 50},
		{"grandparent, mixed case", map[int]proc{100: {60, "cmgo-hook"}, 60: {50, "sh"}, 50: {1, "Claude Helper"}, 1: {0, "init"}}, 100, 50},
		{"own name is not checked", map[int]proc{100: {1, "claude"}, 1: {0, "launchd"}}, 100, 0},
		{"no claude ancestor stops at pid 0", map[int]proc{100: {60, "cmgo"}, 60: {1, "zsh"}, 1: {0, "launchd"}}, 100, 0},
		{"pid 1 may be claude (container init)", map[int]proc{100: {1, "cmgo"}, 1: {0, "claude"}}, 100, 1},
		{"own lookup fails", map[int]proc{}, 100, 0},
		{"ancestor lookup fails", map[int]proc{100: {60, "cmgo"}, 60: {40, "sh"}}, 100, 0},
		{"two-process cycle", map[int]proc{100: {60, "cmgo"}, 60: {70, "a"}, 70: {60, "b"}}, 100, 0},
		{"self-parent cycle", map[int]proc{100: {60, "cmgo"}, 60: {60, "a"}}, 100, 0},
	}
	for _, c := range cases {
		calls := 0
		if got := ClaudePID(c.pid, table(c.procs, &calls)); got != c.want {
			t.Errorf("%s: ClaudePID = %d, want %d", c.name, got, c.want)
		}
		if calls > 9 {
			t.Errorf("%s: %d lookups, want at most 9", c.name, calls)
		}
	}
}

// chain builds 100 -> 101 -> ... with the ancestor at hop n named claude.
func chain(n int) map[int]proc {
	procs := map[int]proc{}
	for i := 0; i < 20; i++ {
		name := "sh"
		if i == n {
			name = "claude"
		}
		procs[100+i] = proc{101 + i, name}
	}
	return procs
}

func TestClaudePIDDepthCap(t *testing.T) {
	calls := 0
	if got := ClaudePID(100, table(chain(8), &calls)); got != 108 {
		t.Errorf("hop 8: ClaudePID = %d, want 108", got)
	}
	calls = 0
	if got := ClaudePID(100, table(chain(9), &calls)); got != 0 {
		t.Errorf("hop 9: ClaudePID = %d, want 0", got)
	}
	if calls != 9 {
		t.Errorf("hop 9: %d lookups, want 9 (own pid + 8 ancestors)", calls)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/procinfo/ -run 'TestClaudePID' -v`
Expected: FAIL with "internal/procinfo/procinfo_test.go:14:44: undefined: Lookup"

- [ ] **Step 3: Write minimal implementation**

`internal/procinfo/procinfo.go`:
```go
// Package procinfo finds the Claude Code process that owns this one, so the
// MCP server and the hooks can agree on a session without being told its id.
package procinfo

import "strings"

// Lookup returns a process's parent pid and executable name.
type Lookup func(pid int) (ppid int, name string, err error)

// maxHops bounds the walk; it also ends parent cycles.
const maxHops = 8

// ClaudePID walks from pid's parent up to 8 hops and returns the first
// ancestor whose name contains "claude" (case-insensitive), else 0.
// pid's own name is not checked.
func ClaudePID(pid int, look Lookup) int {
	next, _, err := look(pid)
	for hop := 0; hop < maxHops && err == nil && next > 0; hop++ {
		cur := next
		var name string
		if next, name, err = look(cur); err == nil && strings.Contains(strings.ToLower(name), "claude") {
			return cur
		}
	}
	return 0
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/procinfo/ -run 'TestClaudePID' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/procinfo/procinfo.go internal/procinfo/procinfo_test.go
git commit -m "feat(procinfo): walk the parent chain to the Claude Code process"
```

### Task 2: Linux /proc/<pid>/stat parser

**Files:**
- Create: `internal/procinfo/stat.go`
- Test: `internal/procinfo/stat_test.go`

**Interfaces:**
- Consumes: none (stdlib only)
- Produces: unexported `func parseStat(s string) (ppid int, name string, err error)`

- [ ] **Step 1: Write the failing test**

`internal/procinfo/stat_test.go`:
```go
package procinfo

import "testing"

func TestParseStat(t *testing.T) {
	cases := []struct {
		in   string
		ppid int
		name string
		ok   bool
	}{
		{"4242 (claude) S 4100 4242 4100 34816 4242 4194304 0 0\n", 4100, "claude", true},
		{"77 (my prog) R 1 77 77 0 -1 0\n", 1, "my prog", true},
		// comm may hold ") " itself; the last ')' ends it.
		{"77 (a) S 9 (b)) S 55 77 77 0\n", 55, "a) S 9 (b)", true},
		{"1 (init) S 0 1 1 0 -1\n", 0, "init", true},
		{"", 0, "", false},
		{"77 (noclose S 1 2", 0, "", false},
		{"77 (x)", 0, "", false},
		{"77 (x) S", 0, "", false},
		{"77 (x) S abc 1", 0, "", false},
	}
	for _, c := range cases {
		ppid, name, err := parseStat(c.in)
		if (err == nil) != c.ok || ppid != c.ppid || name != c.name {
			t.Errorf("parseStat(%q) = %d, %q, %v; want %d, %q, ok=%v", c.in, ppid, name, err, c.ppid, c.name, c.ok)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/procinfo/ -run 'TestParseStat' -v`
Expected: FAIL with "internal/procinfo/stat_test.go:24:22: undefined: parseStat"

- [ ] **Step 3: Write minimal implementation**

`internal/procinfo/stat.go`:
```go
package procinfo

import (
	"errors"
	"strconv"
	"strings"
)

// parseStat reads the parent pid and name from a Linux /proc/<pid>/stat line:
// "pid (comm) state ppid ...". comm may contain spaces and parentheses, so it
// ends at the last ')'. It lives in an untagged file so darwin runs its tests.
func parseStat(s string) (ppid int, name string, err error) {
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return 0, "", errors.New("procinfo: malformed stat")
	}
	f := strings.Fields(s[end+1:]) // state, ppid, ...
	if len(f) < 2 {
		return 0, "", errors.New("procinfo: short stat")
	}
	if ppid, err = strconv.Atoi(f[1]); err != nil {
		return 0, "", err
	}
	return ppid, s[open+1 : end], nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/procinfo/ -run 'TestParseStat' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/procinfo/stat.go internal/procinfo/stat_test.go
git commit -m "feat(procinfo): parse /proc/<pid>/stat from the last paren"
```

### Task 3: System process table lookup (darwin, Linux)

**Files:**
- Create: `internal/procinfo/procinfo_darwin.go`
- Create: `internal/procinfo/procinfo_linux.go`
- Create: `internal/procinfo/procinfo_other.go`
- Modify: `go.mod`
- Test: `internal/procinfo/system_test.go`

**Interfaces:**
- Consumes: `golang.org/x/sys/unix.SysctlKinfoProc(name string, args ...int) (*unix.KinfoProc, error)` (darwin); `unix.ByteSliceToString(s []byte) string`; `parseStat` (Task 2, Linux)
- Produces: `func System() Lookup`

- [ ] **Step 1: Write the failing test**

The smoke test reads the real process table: the own pid must resolve to a name and to `os.Getppid()`, the parent must resolve too, and pid -1 must fail.

`internal/procinfo/system_test.go`:
```go
package procinfo

import (
	"os"
	"testing"
)

func TestSystemOwnProcess(t *testing.T) {
	look := System()
	ppid, name, err := look(os.Getpid())
	if err != nil || name == "" || ppid != os.Getppid() {
		t.Fatalf("System()(self) = %d, %q, %v; want ppid %d and a name", ppid, name, err, os.Getppid())
	}
	if gp, pname, err := look(ppid); err != nil || pname == "" || gp < 0 {
		t.Errorf("System()(parent %d) = %d, %q, %v", ppid, gp, pname, err)
	}
	if _, _, err := look(-1); err == nil {
		t.Error("System()(-1): want an error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/procinfo/ -run 'TestSystemOwnProcess' -v`
Expected: FAIL with "internal/procinfo/system_test.go:9:10: undefined: System"

- [ ] **Step 3: Write minimal implementation**

`internal/procinfo/procinfo_darwin.go`:
```go
package procinfo

import "golang.org/x/sys/unix"

// System returns the darwin process table lookup (sysctl kern.proc.pid).
// The name is p_comm, cut to 16 bytes by the kernel.
func System() Lookup {
	return func(pid int) (int, string, error) {
		kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil {
			return 0, "", err
		}
		return int(kp.Eproc.Ppid), unix.ByteSliceToString(kp.Proc.P_comm[:]), nil
	}
}
```

`internal/procinfo/procinfo_linux.go`:
```go
package procinfo

import (
	"os"
	"strconv"
)

// System returns the Linux process table lookup (/proc/<pid>/stat).
// The name is comm, cut to 15 bytes by the kernel.
func System() Lookup {
	return func(pid int) (int, string, error) {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return 0, "", err
		}
		return parseStat(string(b))
	}
}
```

`internal/procinfo/procinfo_other.go`:
```go
//go:build !darwin && !linux

package procinfo

import "errors"

// System has no process table on other systems; ClaudePID then returns 0.
func System() Lookup {
	return func(int) (int, string, error) { return 0, "", errors.New("procinfo: unsupported OS") }
}
```

Then run `go mod tidy`. It moves `golang.org/x/sys v0.48.0` from the `// indirect` block to the direct `require` block and leaves `go.sum` unchanged. `go.mod` afterwards:

`go.mod`:
```go
module github.com/lang315/context-mode-go

go 1.27.2

require (
	github.com/JohannesKaufmann/html-to-markdown/v2 v2.5.2
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/ncruces/go-sqlite3 v0.35.6
	golang.org/x/net v0.55.0
	golang.org/x/sys v0.48.0
)

require (
	github.com/JohannesKaufmann/dom v0.3.1 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/ncruces/go-sqlite3-wasm/v6 v6.3.35304 // indirect
	github.com/ncruces/julianday v1.0.0 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/time v0.15.0 // indirect
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/procinfo/ -count=1 -race -v && GOOS=linux go vet ./internal/procinfo/ && GOOS=freebsd go vet ./internal/procinfo/`
Expected: PASS (the two `go vet` runs compile the Linux file and the fallback file on darwin and print nothing)

- [ ] **Step 5: Commit**

```bash
git add internal/procinfo/procinfo_darwin.go internal/procinfo/procinfo_linux.go internal/procinfo/procinfo_other.go internal/procinfo/system_test.go go.mod
git commit -m "feat(procinfo): read the darwin and Linux process tables"
```

---

### Task 4: Byte sizes, durations and percentages

**Files:**
- Create: `internal/stats/units.go`
- Test: `internal/stats/units_test.go`

**Interfaces:**
- Consumes: none (stdlib only)
- Produces: `func KB(n int64) string`; `func Duration(d time.Duration) string`; `func Pct(kept, raw int64) int`; unexported `func toFixed(x float64, digits int) string`

- [ ] **Step 1: Write the failing test**

`internal/stats/units_test.go`:
```go
package stats

import (
	"testing"
	"time"
)

func TestKB(t *testing.T) {
	// Expected values from node: kb(b) imported from build/session/analytics.js.
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"}, {-5, "0 B"}, {1, "1 B"}, {512, "512 B"}, {1023, "1023 B"},
		{1024, "1.0 KB"}, {1075, "1.0 KB"}, {1126, "1.1 KB"},
		// Exact ties: JS toFixed rounds up, Go's FormatFloat rounds to even.
		{1280, "1.3 KB"}, {2816, "2.8 KB"}, {3328, "3.3 KB"},
		{102399, "100.0 KB"}, {102400, "100 KB"}, {102912, "101 KB"}, {1048575, "1024 KB"},
		{1048576, "1.0 MB"}, {1310720, "1.3 MB"}, {104857599, "100.0 MB"}, {104857600, "100 MB"}, {1073741823, "1024 MB"},
		{1073741824, "1.00 GB"}, {1207959552, "1.13 GB"}, {1476395008, "1.38 GB"},
		{107374182399, "100.00 GB"}, {107374182400, "100.0 GB"}, {1099511627776, "1024.0 GB"},
	}
	for _, c := range cases {
		if got := KB(c.in); got != c.want {
			t.Errorf("KB(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDuration(t *testing.T) {
	// Expected values from node: formatDuration((ms / 60000).toFixed(1)),
	// the function text taken from build/session/analytics.js.
	cases := []struct {
		ms   int64
		want string
	}{
		{-5000, "< 1 min"}, {0, "< 1 min"}, {1000, "< 1 min"},
		{57000, "< 1 min"}, // 0.95 is stored just below .95, so toFixed(1) gives "0.9"
		{57001, "1 min"}, {59999, "1 min"}, {60000, "1 min"}, {89999, "2 min"}, {90000, "2 min"},
		{3539999, "59 min"}, {3597000, "1h"}, {3597600, "1h"}, {3600000, "1h"}, {3630000, "1h 1m"},
		{7173000, "1h 60m"}, {7176000, "1h 60m"}, // TS quirk: Math.round(min % 60) can be 60
		{7200000, "2h"}, {86400000, "24h"},
	}
	for _, c := range cases {
		if got := Duration(time.Duration(c.ms) * time.Millisecond); got != c.want {
			t.Errorf("Duration(%dms) = %q, want %q", c.ms, got, c.want)
		}
	}
}

func TestPct(t *testing.T) {
	cases := []struct {
		kept, raw int64
		want      int
	}{
		{0, 0, 0}, {5, 0, 0}, {0, 10, 0}, {1, 3, 33}, {2, 3, 67}, {1, 200, 1}, {1, 201, 0}, {10, 10, 100},
	}
	for _, c := range cases {
		if got := Pct(c.kept, c.raw); got != c.want {
			t.Errorf("Pct(%d, %d) = %d, want %d", c.kept, c.raw, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/stats/ -run 'TestKB|TestDuration|TestPct' -v`
Expected: FAIL with "internal/stats/units_test.go:24:13: undefined: KB"

- [ ] **Step 3: Write minimal implementation**

`internal/stats/units.go`:
```go
// Package stats renders the ctx_stats report and the statusline line from
// numbers the caller has already queried. It does no I/O.
package stats

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// KB formats a byte count like TS kb() (src/session/analytics.ts:1961-1977).
func KB(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	k := float64(n) / 1024
	if k < 1024 {
		if k < 100 {
			return toFixed(k, 1) + " KB"
		}
		return strconv.FormatFloat(math.Round(k), 'f', 0, 64) + " KB"
	}
	m := k / 1024
	if m < 1024 {
		if m < 100 {
			return toFixed(m, 1) + " MB"
		}
		return strconv.FormatFloat(math.Round(m), 'f', 0, 64) + " MB"
	}
	g := m / 1024
	if g < 100 {
		return toFixed(g, 2) + " GB"
	}
	return toFixed(g, 1) + " GB"
}

// Duration formats an uptime like TS formatDuration (analytics.ts:1980-1987),
// including the caller's toFixed(1) on the minutes (analytics.ts:464).
func Duration(d time.Duration) string {
	if d < 0 {
		d = 0 // TS gives "< 1 min" for any negative uptime too
	}
	min, _ := strconv.ParseFloat(toFixed(float64(d.Milliseconds())/60000, 1), 64)
	if min < 1 {
		return "< 1 min"
	}
	if min < 60 {
		return strconv.FormatFloat(math.Round(min), 'f', 0, 64) + " min"
	}
	h := math.Floor(min / 60)
	m := math.Round(math.Mod(min, 60)) // can be 60, as in TS
	if m > 0 {
		return strconv.FormatFloat(h, 'f', 0, 64) + "h " + strconv.FormatFloat(m, 'f', 0, 64) + "m"
	}
	return strconv.FormatFloat(h, 'f', 0, 64) + "h"
}

// Pct is round(kept / raw * 100), or 0 when raw is 0.
func Pct(kept, raw int64) int {
	if raw == 0 {
		return 0
	}
	return int(math.Round(float64(kept) / float64(raw) * 100))
}

// toFixed is JS Number.prototype.toFixed for x >= 0: it rounds the exact
// binary value and takes the larger candidate on a tie, where Go's
// FormatFloat rounds to even (1.25 gives "1.3", not "1.2").
func toFixed(x float64, digits int) string {
	r := new(big.Rat).SetFloat64(x)
	r.Mul(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)))
	r.Add(r, big.NewRat(1, 2))
	s := new(big.Int).Quo(r.Num(), r.Denom()).String() // floor, as r > 0
	if len(s) <= digits {
		s = strings.Repeat("0", digits-len(s)+1) + s
	}
	return s[:len(s)-digits] + "." + s[len(s)-digits:]
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/stats/ -run 'TestKB|TestDuration|TestPct' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/stats/units.go internal/stats/units_test.go
git commit -m "feat(stats): port TS kb and formatDuration"
```

### Task 5: ctx_stats report text

**Files:**
- Create: `internal/stats/report.go`
- Test: `internal/stats/report_test.go`

**Interfaces:**
- Consumes: `KB`, `Duration`, `Pct` (Task 4)
- Produces: `type ToolRow struct{ Tool string; Calls, Returned, Kept int64 }`; `type Category struct{ Name string; Count int }`; `type Report struct{ Version string; Now time.Time; SessionLinked bool; SessionStart time.Time; Tools []ToolRow; Raw, Redirects, RedirectBytes int64; Events, Compactions int; Categories []Category; ProjectSources int; ProjectContentBytes int64; ProjectSessions, ProjectEvents int; LifetimeKept int64; LifetimeSessions int; LifetimeSince time.Time }`; `func Format(r Report) string`

- [ ] **Step 1: Write the failing test**

The padded tool and category lines in `TestFormatFull` were checked against node (`padEnd`/`padStart` with TS `kb()`).

`internal/stats/report_test.go`:
```go
package stats

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

// lines joins the expected report; Format adds no trailing newline.
func lines(l ...string) string { return strings.Join(l, "\n") }

func TestFormatFull(t *testing.T) {
	tools := []ToolRow{
		{"ctx_search", 3, 1200, 0},
		{"ctx_execute", 5, 2048, 50000},
		{"ctx_fetch_and_index", 1, 3000, 120000},
		{"ctx_batch_execute", 5, 1000, 200000},
	}
	cats := []Category{
		{"rule", 1}, {"prompt", 2}, {"env", 1}, {"cwd", 2}, {"decision", 3},
		{"error", 4}, {"task", 6}, {"git", 6}, {"file", 12},
	}
	r := Report{
		Version:       "0.4.0",
		Now:           now,
		SessionLinked: true,
		SessionStart:  now.Add(-75 * time.Minute),
		Tools:         tools,
		Raw:           (2048 + 50000) + (3000 + 120000) + (1000 + 200000),
		Redirects:     4, RedirectBytes: 40960,
		Events: 37, Compactions: 2, Categories: cats,
		ProjectSources: 12, ProjectContentBytes: 2621440, ProjectSessions: 9, ProjectEvents: 412,
		LifetimeKept: 5000000, LifetimeSessions: 31, LifetimeSince: time.Date(2026, 9, 1, 23, 30, 0, 0, time.UTC),
	}
	// Padded lines checked against node: padEnd/padStart with TS kb().
	want := lines(
		"cmgo stats",
		"",
		"This session  ·  1h 15m  ·  14 calls",
		"  Kept out of context    361 KB   (98% of 367 KB processed)",
		"  Returned to context    7.1 KB",
		"  Redirected by hooks    4 calls  ·  ~40.0 KB",
		"  ctx_batch_execute         5 calls     195 KB kept     1000 B returned",
		"  ctx_execute               5 calls    48.8 KB kept     2.0 KB returned",
		"  ctx_search                3 calls        0 B kept     1.2 KB returned",
		"  ctx_fetch_and_index       1 calls     117 KB kept     2.9 KB returned",
		"",
		"Session memory  ·  37 events  ·  2 compactions",
		"  file                      12",
		"  git                        6",
		"  task                       6",
		"  error                      4",
		"  decision                   3",
		"  cwd                        2",
		"  prompt                     2",
		"  env                        1",
		"",
		"This project  ·  12 sources indexed (2.5 MB)  ·  9 sessions  ·  412 events",
		"",
		"All projects  ·  4.8 MB kept out  ·  31 sessions  ·  since 2026-09-01",
		"",
		"cmgo 0.4.0",
	)
	if got := Format(r); got != want {
		t.Errorf("Format =\n%s\nwant\n%s", got, want)
	}
	if tools[0].Tool != "ctx_search" || cats[0].Name != "rule" || len(cats) != 9 {
		t.Error("Format reordered or cut the caller's slices")
	}
}

func TestFormatFreshSession(t *testing.T) {
	r := Report{Version: "0.4.0", Now: now, SessionLinked: true, SessionStart: now}
	want := lines(
		"cmgo stats",
		"",
		"This session  ·  < 1 min  ·  0 calls",
		"  Kept out of context    0 B   (0% of 0 B processed)",
		"  Returned to context    0 B",
		"  Redirected by hooks    0 calls  ·  ~0 B",
		"",
		"Session memory  ·  0 events  ·  0 compactions",
		"",
		"This project  ·  0 sources indexed (0 B)  ·  0 sessions  ·  0 events",
		"",
		"All projects  ·  0 B kept out  ·  0 sessions",
		"",
		"cmgo 0.4.0",
	)
	if got := Format(r); got != want {
		t.Errorf("Format =\n%s\nwant\n%s", got, want)
	}
}

func TestFormatUnlinked(t *testing.T) {
	// No session: hook-based lines (redirects, session memory) are dropped,
	// the server's own rows are still shown.
	r := Report{
		Version: "0.4.0", Now: now,
		Tools: []ToolRow{{"ctx_execute", 1, 300, 4096}}, Raw: 4396,
		Redirects: 9, Events: 5, Categories: []Category{{"file", 5}},
		ProjectSources: 1, ProjectContentBytes: 2048, ProjectSessions: 1, ProjectEvents: 5,
		LifetimeKept: 4096, LifetimeSessions: 1, LifetimeSince: now,
	}
	want := lines(
		"cmgo stats",
		"",
		"This session  ·  no hook data yet (is cmgo-hook installed?)",
		"  Kept out of context    4.0 KB   (93% of 4.3 KB processed)",
		"  Returned to context    300 B",
		"  ctx_execute               1 calls     4.0 KB kept      300 B returned",
		"",
		"This project  ·  1 sources indexed (2.0 KB)  ·  1 sessions  ·  5 events",
		"",
		"All projects  ·  4.0 KB kept out  ·  1 sessions  ·  since 2026-10-21",
		"",
		"cmgo 0.4.0",
	)
	if got := Format(r); got != want {
		t.Errorf("Format =\n%s\nwant\n%s", got, want)
	}
}

func TestFormatZeroRaw(t *testing.T) {
	r := Report{
		Version: "0.4.0", Now: now, SessionLinked: true, SessionStart: now.Add(-30 * time.Minute),
		Tools: []ToolRow{{"ctx_search", 2, 900, 0}},
	}
	want := lines(
		"cmgo stats",
		"",
		"This session  ·  30 min  ·  2 calls",
		"  Kept out of context    0 B   (0% of 0 B processed)",
		"  Returned to context    900 B",
		"  Redirected by hooks    0 calls  ·  ~0 B",
		"  ctx_search                2 calls        0 B kept      900 B returned",
		"",
		"Session memory  ·  0 events  ·  0 compactions",
		"",
		"This project  ·  0 sources indexed (0 B)  ·  0 sessions  ·  0 events",
		"",
		"All projects  ·  0 B kept out  ·  0 sessions",
		"",
		"cmgo 0.4.0",
	)
	if got := Format(r); got != want {
		t.Errorf("Format =\n%s\nwant\n%s", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/stats/ -run 'TestFormat' -v`
Expected: FAIL with "internal/stats/report_test.go:15:13: undefined: ToolRow"

- [ ] **Step 3: Write minimal implementation**

`internal/stats/report.go`:
```go
package stats

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ToolRow is one tool's summed tool_stats counters.
type ToolRow struct {
	Tool                  string
	Calls, Returned, Kept int64
}

// Category is a session-memory event category and its event count.
type Category struct {
	Name  string
	Count int
}

// Report holds everything the ctx_stats text shows (spec §2).
type Report struct {
	Version       string
	Now           time.Time
	SessionLinked bool
	SessionStart  time.Time // duration = Now - SessionStart
	Tools         []ToolRow // this session's server rows
	// Raw is the bytes processed this session: the sum of Kept + Returned
	// over the rows of tools that have a raw definition (spec §1.3:
	// ctx_execute, ctx_execute_file, ctx_batch_execute, ctx_fetch_and_index).
	// The caller computes it; the headline percentage is Pct(sum Kept, Raw).
	Raw                 int64
	Redirects           int64 // PreToolUse redirects this session
	RedirectBytes       int64 // their bytes_avoided, shown apart from Kept
	Events              int
	Compactions         int
	Categories          []Category // any order; Format shows the top 8
	ProjectSources      int
	ProjectContentBytes int64
	ProjectSessions     int
	ProjectEvents       int
	LifetimeKept        int64 // all tool_stats kept plus all redirect bytes_avoided
	LifetimeSessions    int
	LifetimeSince       time.Time // earliest started_at; zero hides "since"
}

// Format renders the ctx_stats text, with no trailing newline.
func Format(r Report) string {
	var calls, returned, kept int64
	for _, t := range r.Tools {
		calls, returned, kept = calls+t.Calls, returned+t.Returned, kept+t.Kept
	}
	var b strings.Builder
	b.WriteString("cmgo stats\n\n")
	if r.SessionLinked {
		fmt.Fprintf(&b, "This session  ·  %s  ·  %d calls\n", Duration(r.Now.Sub(r.SessionStart)), calls)
	} else {
		b.WriteString("This session  ·  no hook data yet (is cmgo-hook installed?)\n")
	}
	fmt.Fprintf(&b, "  Kept out of context    %s   (%d%% of %s processed)\n", KB(kept), Pct(kept, r.Raw), KB(r.Raw))
	fmt.Fprintf(&b, "  Returned to context    %s\n", KB(returned))
	if r.SessionLinked {
		fmt.Fprintf(&b, "  Redirected by hooks    %d calls  ·  ~%s\n", r.Redirects, KB(r.RedirectBytes))
	}
	tools := slices.Clone(r.Tools)
	slices.SortFunc(tools, func(a, b ToolRow) int {
		return cmp.Or(cmp.Compare(b.Calls, a.Calls), strings.Compare(a.Tool, b.Tool))
	})
	for _, t := range tools {
		fmt.Fprintf(&b, "  %-22s %4d calls  %9s kept  %9s returned\n", t.Tool, t.Calls, KB(t.Kept), KB(t.Returned))
	}
	if r.SessionLinked {
		fmt.Fprintf(&b, "\nSession memory  ·  %d events  ·  %d compactions\n", r.Events, r.Compactions)
		cats := slices.Clone(r.Categories)
		slices.SortFunc(cats, func(a, b Category) int {
			return cmp.Or(cmp.Compare(b.Count, a.Count), strings.Compare(a.Name, b.Name))
		})
		for _, c := range cats[:min(len(cats), 8)] {
			fmt.Fprintf(&b, "  %-22s %5d\n", c.Name, c.Count)
		}
	}
	fmt.Fprintf(&b, "\nThis project  ·  %d sources indexed (%s)  ·  %d sessions  ·  %d events\n",
		r.ProjectSources, KB(r.ProjectContentBytes), r.ProjectSessions, r.ProjectEvents)
	fmt.Fprintf(&b, "\nAll projects  ·  %s kept out  ·  %d sessions", KB(r.LifetimeKept), r.LifetimeSessions)
	if !r.LifetimeSince.IsZero() {
		b.WriteString("  ·  since " + r.LifetimeSince.UTC().Format("2006-01-02"))
	}
	b.WriteString("\n\ncmgo " + r.Version)
	return b.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/stats/ -run 'TestFormat' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/stats/report.go internal/stats/report_test.go
git commit -m "feat(stats): render the ctx_stats report"
```

### Task 6: Statusline line

**Files:**
- Create: `internal/stats/statusline.go`
- Test: `internal/stats/statusline_test.go`

**Interfaces:**
- Consumes: `KB` (Task 4)
- Produces: `type Line struct{ OK bool; SessionKept, LifetimeKept int64; Pct int }`; `func StatusLine(l Line) string`

- [ ] **Step 1: Write the failing test**

`internal/stats/statusline_test.go`:
```go
package stats

import "testing"

func TestStatusLine(t *testing.T) {
	cases := []struct {
		in   Line
		want string
	}{
		{Line{}, "cmgo  ●  ready"},
		{Line{OK: false, SessionKept: 5000, LifetimeKept: 9000, Pct: 50}, "cmgo  ●  ready"},
		{Line{OK: true, LifetimeKept: 5000000}, "cmgo  ●  4.8 MB kept out lifetime"},
		{Line{OK: true}, "cmgo  ●  0 B kept out lifetime"},
		{Line{OK: true, SessionKept: 370000, LifetimeKept: 5000000, Pct: 98}, "cmgo  ●  361 KB this chat  ·  4.8 MB lifetime  ·  98% kept out"},
	}
	for _, c := range cases {
		if got := StatusLine(c.in); got != c.want {
			t.Errorf("StatusLine(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/stats/ -run 'TestStatusLine' -v`
Expected: FAIL with "internal/stats/statusline_test.go:7:8: undefined: Line"

- [ ] **Step 3: Write minimal implementation**

`internal/stats/statusline.go`:
```go
package stats

import "strconv"

// Line holds the statusline numbers (spec §3). OK is false when the DB, the
// session or a query is missing; Pct is Pct(session kept, session raw).
type Line struct {
	OK                        bool
	SessionKept, LifetimeKept int64
	Pct                       int
}

// StatusLine renders the one statusline line, with no newline and no ANSI.
func StatusLine(l Line) string {
	switch {
	case !l.OK:
		return "cmgo  ●  ready"
	case l.SessionKept == 0:
		return "cmgo  ●  " + KB(l.LifetimeKept) + " kept out lifetime"
	}
	return "cmgo  ●  " + KB(l.SessionKept) + " this chat  ·  " + KB(l.LifetimeKept) + " lifetime  ·  " + strconv.Itoa(l.Pct) + "% kept out"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/stats/ -count=1 -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/stats/statusline.go internal/stats/statusline_test.go
git commit -m "feat(stats): render the statusline line"
```

---

### Task 7: Migration 3, AddToolStat and SetSessionCC

**Files:**
- Modify: `internal/store/store.go`
- Create: `internal/store/stats.go`
- Test: `internal/store/stats_test.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: `store.Open`, `store.InsertEvents` (existing)
- Produces: migration 3 in `var migrations` (`tool_stats` with `window`, key `(server_id, tool, window)`); unexported `const newestSession`; `type ToolStat struct{ Tool string; Calls, BytesReturned, BytesKept int64 }`; `func (s *Store) AddToolStat(serverID string, ccPID int, project, startedAt, tool string, returned, kept int64) error`; `func (s *Store) SetSessionCC(sessionID, project string, ccPID int) error`

- [ ] **Step 1: Write the failing test**

`internal/store/stats_test.go`:
```go
package store

import (
	"testing"
)

func TestAddToolStatUpserts(t *testing.T) {
	s := openTest(t)
	add := func(server, tool string, returned, kept int64) {
		t.Helper()
		if err := s.AddToolStat(server, 42, "/p", "2026-10-21 10:00:00", tool, returned, kept); err != nil {
			t.Fatal(err)
		}
	}
	add("srv1", "ctx_execute", 100, 900)
	add("srv1", "ctx_execute", 50, 0)
	add("srv1", "ctx_search", 30, 0)
	add("srv2", "ctx_execute", 7, 3)

	var calls, returned, kept, ccPID int64
	var project, started, window, updated string
	row := func(window string) {
		t.Helper()
		if err := s.db.QueryRow(`SELECT calls, bytes_returned, bytes_kept, cc_pid, project, started_at, updated_at
FROM tool_stats WHERE server_id = 'srv1' AND tool = 'ctx_execute' AND window = ?`, window).Scan(&calls, &returned, &kept, &ccPID, &project, &started, &updated); err != nil {
			t.Fatal(err)
		}
	}
	// No session of cc 42 yet: the window is ''.
	row("")
	if calls != 2 || returned != 150 || kept != 900 || ccPID != 42 || project != "/p" || started != "2026-10-21 10:00:00" || len(updated) != len(sqliteTime) {
		t.Fatalf("row = %d %d %d %d %q %q %q", calls, returned, kept, ccPID, project, started, updated)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM tool_stats"); n != 3 {
		t.Fatalf("rows = %d, want 3", n)
	}

	// Sessions of cc 42: the newest one in /p is the window; /q's is ignored.
	for _, sess := range []struct{ id, project, started string }{
		{"s1", "/p", "2026-10-21 09:00:00"}, {"s2", "/p", "2026-10-21 09:30:00"}, {"s3", "/q", "2026-10-21 11:00:00"},
	} {
		if err := s.SetSessionCC(sess.id, sess.project, 42); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec("UPDATE session_meta SET started_at = ? WHERE session_id = ?", sess.started, sess.id); err != nil {
			t.Fatal(err)
		}
	}
	add("srv1", "ctx_execute", 1, 2)
	row("2026-10-21 09:30:00")
	if calls != 1 || returned != 1 || kept != 2 {
		t.Fatalf("windowed row = %d %d %d", calls, returned, kept)
	}
	// cc_pid 0 never takes a window, even with sessions registered under 0.
	if err := s.SetSessionCC("s0", "/p", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.AddToolStat("srv0", 0, "/p", "2026-10-21 10:00:00", "ctx_execute", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT window FROM tool_stats WHERE server_id = 'srv0'").Scan(&window); err != nil || window != "" {
		t.Fatalf("srv0 window = %q, %v", window, err)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM tool_stats"); n != 5 {
		t.Fatalf("rows = %d, want 5", n)
	}
}

func TestSetSessionCC(t *testing.T) {
	s := openTest(t)
	// A session SessionStart has not registered yet gets its meta row.
	if err := s.SetSessionCC("s1", "/p", 4242); err != nil {
		t.Fatal(err)
	}
	insertTestEvents(t, s, "s1", "/p", Event{Type: "a", Category: "file", Priority: 1, Data: "x"})
	if err := s.SetSessionCC("s1", "/p", 777); err != nil {
		t.Fatal(err)
	}
	var cc, events int
	var project string
	if err := s.db.QueryRow("SELECT cc_pid, event_count, project FROM session_meta WHERE session_id = 's1'").Scan(&cc, &events, &project); err != nil {
		t.Fatal(err)
	}
	if cc != 777 || events != 1 || project != "/p" {
		t.Fatalf("meta = cc %d, events %d, project %q", cc, events, project)
	}
	// Sessions registered by InsertEvents alone keep cc_pid 0.
	insertTestEvents(t, s, "s2", "/p", Event{Type: "a", Category: "file", Priority: 1, Data: "y"})
	if n := tableCount(t, s, "SELECT cc_pid FROM session_meta WHERE session_id = 's2'"); n != 0 {
		t.Fatalf("s2 cc_pid = %d", n)
	}
}
```

`internal/store/store_test.go`:
```go
package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "sub", "cmgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenSchemaAndPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "cmgo.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	var jm string
	var busy, fk, syncMode int
	s.db.QueryRow("PRAGMA user_version").Scan(&v)
	s.db.QueryRow("PRAGMA journal_mode").Scan(&jm)
	s.db.QueryRow("PRAGMA busy_timeout").Scan(&busy)
	s.db.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	s.db.QueryRow("PRAGMA synchronous").Scan(&syncMode)
	if v != 3 || jm != "wal" || busy != 30000 || fk != 1 || syncMode != 1 {
		t.Fatalf("user_version=%d journal=%s busy=%d fk=%d sync=%d", v, jm, busy, fk, syncMode)
	}
	for _, tbl := range []string{"meta", "sources", "chunks", "chunks_trigram", "vocabulary", "tool_stats"} {
		var n int
		if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = ?", tbl).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s: n=%d err=%v", tbl, n, err)
		}
	}
	if _, err := s.db.Exec("INSERT INTO sources(project, label, chunk_count, code_chunk_count) VALUES('p','x',0,0)"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Reopen: migrations are not re-run and data survives.
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	s.db.QueryRow("SELECT count(*) FROM sources").Scan(&n)
	if n != 1 {
		t.Fatalf("sources after reopen = %d", n)
	}
}

// A DB written by the core (user_version 1) gains the session and stats
// tables on Open and keeps its sources.
func TestMigrateFromV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	all := migrations
	migrations = all[:1] // the core code path: only migration 1 exists
	old, err := Open(path)
	migrations = all
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec("INSERT INTO sources(project, label, chunk_count, code_chunk_count) VALUES('p','x',0,0)"); err != nil {
		t.Fatal(err)
	}
	old.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v, n int
	s.db.QueryRow("PRAGMA user_version").Scan(&v)
	s.db.QueryRow("SELECT count(*) FROM sources").Scan(&n)
	if v != 3 || n != 1 {
		t.Fatalf("user_version=%d sources=%d, want 3 and 1", v, n)
	}
	for _, name := range []string{"session_meta", "session_events", "session_events_session", "session_events_project", "session_events_evict",
		"tool_stats", "tool_stats_cc", "tool_stats_project", "session_events_redirect"} {
		if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = ?", name).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s: n=%d err=%v", name, n, err)
		}
	}
}

func TestCleanupIfDue(t *testing.T) {
	s := openTest(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO sources(id, project, label, chunk_count, code_chunk_count, indexed_at) VALUES(1,'p','old',1,0,'2020-01-01 00:00:00')")
	exec("INSERT INTO sources(id, project, label, chunk_count, code_chunk_count) VALUES(2,'q','fresh',1,0)")
	for _, tbl := range []string{"chunks", "chunks_trigram"} {
		exec("INSERT INTO " + tbl + "(title, content, source_id, content_type) VALUES('t','old body',1,'prose')")
		exec("INSERT INTO " + tbl + "(title, content, source_id, content_type) VALUES('t','fresh body',2,'prose')")
	}

	now := time.Now()
	const day = 24 * time.Hour
	n, err := s.CleanupIfDue(now, 14*day, day)
	if err != nil || n != 1 {
		t.Fatalf("first cleanup n=%d err=%v", n, err)
	}
	var left, chunks, tri int
	s.db.QueryRow("SELECT count(*) FROM sources").Scan(&left)
	s.db.QueryRow("SELECT count(*) FROM chunks").Scan(&chunks)
	s.db.QueryRow("SELECT count(*) FROM chunks_trigram").Scan(&tri)
	if left != 1 || chunks != 1 || tri != 1 {
		t.Fatalf("after cleanup sources=%d chunks=%d trigram=%d", left, chunks, tri)
	}

	// Not due again within 24h, even if an old source appears.
	exec("UPDATE sources SET indexed_at = '2020-01-01 00:00:00' WHERE id = 2")
	if n, err := s.CleanupIfDue(now.Add(23*time.Hour), 14*day, day); err != nil || n != 0 {
		t.Fatalf("second cleanup n=%d err=%v", n, err)
	}
	// Due after 24h.
	if n, err := s.CleanupIfDue(now.Add(25*time.Hour), 14*day, day); err != nil || n != 1 {
		t.Fatalf("third cleanup n=%d err=%v", n, err)
	}
	var last string
	s.db.QueryRow("SELECT value FROM meta WHERE key='last_cleanup'").Scan(&last)
	if last != now.Add(25*time.Hour).UTC().Format(time.RFC3339) {
		t.Fatalf("last_cleanup = %q", last)
	}
}

// Sessions are swept by last activity, not start time: an old session that is
// still active stays.
func TestCleanupIfDueSessions(t *testing.T) {
	s := openTest(t)
	ev := []Event{{Type: "t", Category: "c", Priority: 1, Data: "x"}}
	for _, sid := range []string{"stale", "active", "fresh"} {
		if _, err := s.InsertEvents(sid, "/p", "PostToolUse", ev); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	ago := func(d time.Duration) string { return now.Add(-d).UTC().Format(sqliteTime) }
	const day = 24 * time.Hour
	s.db.Exec("UPDATE session_meta SET started_at = ?, last_event_at = ? WHERE session_id = 'stale'", ago(9*day), ago(8*day))
	s.db.Exec("UPDATE session_meta SET started_at = ?, last_event_at = ? WHERE session_id = 'active'", ago(30*day), ago(6*day))

	if _, err := s.CleanupIfDue(now, 14*day, day); err != nil {
		t.Fatal(err)
	}
	var metas, events int
	s.db.QueryRow("SELECT count(*) FROM session_meta WHERE session_id = 'stale'").Scan(&metas)
	s.db.QueryRow("SELECT count(*) FROM session_events WHERE session_id = 'stale'").Scan(&events)
	if metas != 0 || events != 0 {
		t.Fatalf("stale session left: meta=%d events=%d", metas, events)
	}
	s.db.QueryRow("SELECT count(*) FROM session_meta").Scan(&metas)
	s.db.QueryRow("SELECT count(*) FROM session_events").Scan(&events)
	if metas != 2 || events != 2 {
		t.Fatalf("kept meta=%d events=%d, want 2 and 2", metas, events)
	}
}

// A not-due CleanupIfDue must only read. Another Store holds the write lock;
// the not-due call must return at once, while a due call (control) waits.
func TestCleanupNotDueTakesNoWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	now := time.Now()
	const day = 24 * time.Hour
	if _, err := s.CleanupIfDue(now, 14*day, day); err != nil {
		t.Fatal(err)
	}
	lock, err := other.db.Begin() // BEGIN IMMEDIATE: holds the write lock
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { _, err := s.CleanupIfDue(now.Add(time.Hour), 14*day, day); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		lock.Rollback()
		t.Fatal("not-due CleanupIfDue waited for the write lock")
	}

	// Control: a due call needs the lock, so it waits until the lock is released.
	go func() { _, err := s.CleanupIfDue(now.Add(25*time.Hour), 14*day, day); done <- err }()
	select {
	case <-done:
		lock.Rollback()
		t.Fatal("due CleanupIfDue did not wait for the write lock")
	case <-time.After(300 * time.Millisecond):
	}
	lock.Rollback()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestAddToolStat|TestSetSessionCC|TestOpenSchemaAndPragmas|TestMigrateFromV1' -v`
Expected: FAIL with "internal/store/stats_test.go:11:15: s.AddToolStat undefined (type *Store has no field or method AddToolStat)"

- [ ] **Step 3: Write minimal implementation**

`internal/store/store.go`:
```go
// Package store is the SQLite FTS5 knowledge base: schema, chunkers, search
// and snippets. Port of context-mode src/store.ts (ST) with the differences
// listed in the cmgo core spec §2.2 and §3.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

// ContentType is the chunk kind stored in the content_type column.
type ContentType string

const (
	Code  ContentType = "code"
	Prose ContentType = "prose"
)

// sqliteTime is the layout of datetime('now') values in sources.indexed_at.
const sqliteTime = "2006-01-02 15:04:05"

// Store owns one *sql.DB with a single connection kept open for the life of
// the process.
type Store struct {
	db *sql.DB

	mu         sync.Mutex
	indexCalls int               // Index calls in this process; FTS optimize every 50 (ST:1132)
	fuzzyKeys  []string          // LRU order of fuzzyVals, oldest first (ST:470)
	fuzzyVals  map[string]string // word -> correction; "" means no correction
}

// migrations[i] moves the schema from user_version i to i+1.
var migrations = []string{
	`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE sources (
  id INTEGER PRIMARY KEY,
  project TEXT NOT NULL,
  label TEXT NOT NULL,
  chunk_count INTEGER NOT NULL,
  code_chunk_count INTEGER NOT NULL,
  indexed_at TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE (project, label)
);
CREATE VIRTUAL TABLE chunks USING fts5(
  title, content, source_id UNINDEXED, content_type UNINDEXED,
  tokenize = 'porter unicode61');
CREATE VIRTUAL TABLE chunks_trigram USING fts5(
  title, content, source_id UNINDEXED, content_type UNINDEXED,
  tokenize = 'trigram');
CREATE TABLE vocabulary (word TEXT PRIMARY KEY);`,
	// Session event capture (hooks spec §3.1).
	`CREATE TABLE session_meta (
  session_id TEXT PRIMARY KEY,
  project TEXT NOT NULL,
  started_at TEXT NOT NULL,         -- UTC 'YYYY-MM-DD HH:MM:SS', like sources.indexed_at
  last_event_at TEXT NOT NULL,
  event_count INTEGER NOT NULL DEFAULT 0,
  compact_count INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE session_events (
  id INTEGER PRIMARY KEY,
  session_id TEXT NOT NULL,
  project TEXT NOT NULL,
  type TEXT NOT NULL,
  category TEXT NOT NULL,
  priority INTEGER NOT NULL,        -- 1 = most important ... 4 = least
  data TEXT NOT NULL,
  data_hash TEXT NOT NULL,          -- sha256(data) hex, first 16, upper case (DB:1145)
  source_hook TEXT NOT NULL,
  bytes_avoided INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL          -- UTC 'YYYY-MM-DD HH:MM:SS'
);
CREATE INDEX session_events_session ON session_events(session_id, id);
CREATE INDEX session_events_project ON session_events(project, id);
CREATE INDEX session_events_evict ON session_events(session_id, priority, id);`,
	// Server tool counters and the session link (stats spec §1.1).
	`CREATE TABLE tool_stats (
  server_id TEXT NOT NULL,          -- "<pid>-<start unix nanos>" of the cmgo serve process
  cc_pid INTEGER NOT NULL,          -- Claude Code process that owns the server (§1.2), 0 if unknown
  project TEXT NOT NULL,
  tool TEXT NOT NULL,
  calls INTEGER NOT NULL DEFAULT 0,
  bytes_returned INTEGER NOT NULL DEFAULT 0,
  bytes_kept INTEGER NOT NULL DEFAULT 0,
  started_at TEXT NOT NULL,         -- server start, UTC 'YYYY-MM-DD HH:MM:SS'
  window TEXT NOT NULL DEFAULT '',   -- started_at of the linked session at write time ('' when none)
  updated_at TEXT NOT NULL,
  PRIMARY KEY (server_id, tool, window)
);
CREATE INDEX tool_stats_cc ON tool_stats(cc_pid, updated_at);
CREATE INDEX tool_stats_project ON tool_stats(project);
ALTER TABLE session_meta ADD COLUMN cc_pid INTEGER NOT NULL DEFAULT 0;
-- Not in the spec DDL: the statusline sums every redirect's bytes_avoided on
-- each run, and a scan of 50,000 events alone takes longer than its 10 ms budget.
CREATE INDEX session_events_redirect ON session_events(bytes_avoided) WHERE category = 'redirect';`,
}

// defaultBusyTimeout is how long a statement or the WAL switch waits for
// another connection's lock. The server keeps it; hooks use a shorter one.
const defaultBusyTimeout = 30 * time.Second

// Options tunes OpenWith. The zero value is what Open uses.
type Options struct {
	// BusyTimeout bounds every lock wait, the WAL switch included.
	// 0 means 30 s.
	BusyTimeout time.Duration
}

// Open creates the parent directory, opens the DB with WAL pragmas and
// applies pending migrations.
func Open(path string) (*Store, error) { return OpenWith(path, Options{}) }

// OpenWith is Open with options. Claude Code waits for a hook process to
// exit, so hooks pass a short BusyTimeout and drop their events on BUSY.
func OpenWith(path string, opts Options) (*Store, error) {
	busy := opts.BusyTimeout
	if busy <= 0 {
		busy = defaultBusyTimeout
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// journal_mode is not in the DSN: WAL persists in the file, and switching
	// to it needs an exclusive lock that SQLite refuses with SQLITE_BUSY
	// without consulting busy_timeout. Re-applying it on every connection made
	// a session fail to start while another session was writing.
	dsn := (&url.URL{Scheme: "file", Path: abs}).String() +
		"?_txlock=immediate" +
		"&_pragma=busy_timeout(" + strconv.FormatInt(busy.Milliseconds(), 10) + ")" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)"
	db, err := driver.Open(dsn, fts5.Register)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	s := &Store{db: db, fuzzyVals: map[string]string{}}
	if err := s.ensureWAL(busy); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// ensureWAL switches the file to WAL once. Reading the mode takes no lock;
// the switch is retried with backoff for up to busy while another connection
// holds a lock.
func (s *Store) ensureWAL(busy time.Duration) error {
	deadline := time.Now().Add(busy)
	wait := 10 * time.Millisecond
	for {
		var mode string
		err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
		if err == nil && mode == "wal" {
			return nil
		}
		if err == nil {
			err = s.db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode)
			if err == nil && mode == "wal" {
				return nil
			}
			if err == nil {
				err = fmt.Errorf("journal_mode is %q, want wal", mode)
			}
		}
		if !isBusy(err) || time.Now().After(deadline) {
			return fmt.Errorf("enable WAL: %w", err)
		}
		time.Sleep(wait)
		if wait < 250*time.Millisecond {
			wait *= 2
		}
	}
}

// isBusy reports SQLITE_BUSY / SQLITE_LOCKED, which the driver may wrap.
func isBusy(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sqlite3.BUSY) || errors.Is(err, sqlite3.LOCKED) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "database table is locked")
}

// migrate re-reads user_version inside an IMMEDIATE transaction so two
// processes opening a fresh file at once do not both run step 1.
func (s *Store) migrate() error {
	// A plain read takes no write lock: an up-to-date DB (every hook fire)
	// must not queue behind another session's write transaction.
	var cur int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&cur); err != nil {
		return err
	}
	if cur >= len(migrations) {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var v int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v >= len(migrations) {
		return nil
	}
	for i := v; i < len(migrations); i++ {
		if _, err := tx.Exec(migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", len(migrations))); err != nil {
		return err
	}
	return tx.Commit()
}

// Close closes the DB (SQLite checkpoints the WAL on last close).
func (s *Store) Close() error { return s.db.Close() }

// cleanupDue reports whether meta.last_cleanup is missing or older than every.
func cleanupDue(q interface {
	QueryRow(string, ...any) *sql.Row
}, now time.Time, every time.Duration) (bool, error) {
	var last string
	err := q.QueryRow("SELECT value FROM meta WHERE key = 'last_cleanup'").Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	t, err := time.Parse(time.RFC3339, last)
	return err != nil || now.Sub(t) >= every, nil
}

// CleanupIfDue deletes sources older than retention (all projects) when
// meta.last_cleanup is missing or older than every, and in the same
// transaction the sessions inactive for SessionRetention. Returns sources deleted.
// When not due it only reads: no write lock is taken.
// Port of cleanupStaleSources (ST:1641) gated per spec §3.1.
func (s *Store) CleanupIfDue(now time.Time, retention, every time.Duration) (int, error) {
	if due, err := cleanupDue(s.db, now, every); err != nil || !due {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Re-check under the write lock: another process may have just cleaned.
	if due, err := cleanupDue(tx, now, every); err != nil || !due {
		return 0, err
	}
	cutoff := now.Add(-retention).UTC().Format(sqliteTime)
	old := "SELECT id FROM sources WHERE datetime(indexed_at) < datetime(?)"
	if _, err := tx.Exec("DELETE FROM chunks WHERE source_id IN ("+old+")", cutoff); err != nil {
		return 0, err
	}
	if _, err := tx.Exec("DELETE FROM chunks_trigram WHERE source_id IN ("+old+")", cutoff); err != nil {
		return 0, err
	}
	res, err := tx.Exec("DELETE FROM sources WHERE datetime(indexed_at) < datetime(?)", cutoff)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	// Sessions inactive for SessionRetention (DB:1696-1705, keyed on activity).
	stale := now.Add(-SessionRetention).UTC().Format(sqliteTime)
	if _, err := tx.Exec("DELETE FROM session_events WHERE session_id IN (SELECT session_id FROM session_meta WHERE last_event_at < ?)", stale); err != nil {
		return 0, err
	}
	if _, err := tx.Exec("DELETE FROM session_meta WHERE last_event_at < ?", stale); err != nil {
		return 0, err
	}
	if _, err := tx.Exec("INSERT INTO meta(key, value) VALUES('last_cleanup', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		now.UTC().Format(time.RFC3339)); err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}
```

`internal/store/stats.go`:
```go
package store

// ToolStat is one tool's summed tool_stats counters.
type ToolStat struct {
	Tool                            string
	Calls, BytesReturned, BytesKept int64
}

// AddToolStat records one finished tool call of a cmgo serve process: calls+1
// and the call's bytes are added, updated_at becomes now. The row's window is
// the started_at of the newest session of ccPID in project (” when there is
// none, or ccPID is 0), so after a /clear the calls go to new rows (stats
// spec §1.2). The first call of a (serverID, tool, window) inserts the row
// with startedAt, the server start. One statement: the window is read in the
// same transaction as the write, and a call costs one write.
func (s *Store) AddToolStat(serverID string, ccPID int, project, startedAt, tool string, returned, kept int64) error {
	_, err := s.db.Exec(`INSERT INTO tool_stats(server_id, cc_pid, project, tool, calls, bytes_returned, bytes_kept, started_at, window, updated_at)
VALUES(?1, ?2, ?3, ?4, 1, ?5, ?6, ?7, CASE WHEN ?2 = 0 THEN '' ELSE COALESCE((`+newestSession+`), '') END, datetime('now'))
ON CONFLICT(server_id, tool, window) DO UPDATE SET calls = calls + 1,
  bytes_returned = bytes_returned + excluded.bytes_returned,
  bytes_kept = bytes_kept + excluded.bytes_kept,
  updated_at = excluded.updated_at`,
		serverID, ccPID, project, tool, returned, kept, startedAt)
	return err
}

// newestSession selects the started_at of the newest session of Claude Code
// pid ?2 in project ?3: the window a server write belongs to.
const newestSession = `SELECT started_at FROM session_meta WHERE cc_pid = ?2 AND project = ?3
ORDER BY started_at DESC, rowid DESC LIMIT 1`

// SetSessionCC records the Claude Code pid that owns a session (stats spec
// §1.2). A session not seen yet gets its session_meta row, as InsertEvents'
// upsert would create it.
func (s *Store) SetSessionCC(sessionID, project string, ccPID int) error {
	_, err := s.db.Exec(`INSERT INTO session_meta(session_id, project, started_at, last_event_at, cc_pid)
VALUES(?, ?, datetime('now'), datetime('now'), ?)
ON CONFLICT(session_id) DO UPDATE SET cc_pid = excluded.cc_pid`, sessionID, project, ccPID)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run 'TestAddToolStat|TestSetSessionCC|TestOpenSchemaAndPragmas|TestMigrateFromV1' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/stats.go internal/store/stats_test.go internal/store/store_test.go
git commit -m "feat(store): add tool_stats table and session cc_pid (migration 3)"
```

### Task 8: Stats snapshot with session linking by cc_pid

**Files:**
- Modify: `internal/store/stats.go`
- Test: `internal/store/stats_test.go`

**Interfaces:**
- Consumes: `AddToolStat`, `SetSessionCC`, `ToolStat` from Task 7; `store.Index`, `store.InsertEvents` (existing)
- Produces: `type StatsQuery struct{ ServerID string; CCPID int; Project string }`; `type StatsSnapshot struct{ SessionID string; SessionLinked bool; SessionStart string; Tools []ToolStat; Raw int64; Redirects, RedirectBytes int64; Events, Compactions int; Categories map[string]int; ProjectSources int; ProjectContentBytes int64; ProjectSessions, ProjectEvents int; LifetimeKept int64; LifetimeSessions int; LifetimeSince string }`; `func (s *Store) Stats(q StatsQuery) (StatsSnapshot, error)`; `func (s *Store) SourceBytes(sourceID int64) (int64, error)`; unexported `rawTools`, `rawBytes`, `lifetimeKept`, `toolStats`, `categories`

- [ ] **Step 1: Write the failing test**

`internal/store/stats_test.go`:
```go
package store

import (
	"reflect"
	"testing"
)

func TestAddToolStatUpserts(t *testing.T) {
	s := openTest(t)
	add := func(server, tool string, returned, kept int64) {
		t.Helper()
		if err := s.AddToolStat(server, 42, "/p", "2026-10-21 10:00:00", tool, returned, kept); err != nil {
			t.Fatal(err)
		}
	}
	add("srv1", "ctx_execute", 100, 900)
	add("srv1", "ctx_execute", 50, 0)
	add("srv1", "ctx_search", 30, 0)
	add("srv2", "ctx_execute", 7, 3)

	var calls, returned, kept, ccPID int64
	var project, started, window, updated string
	row := func(window string) {
		t.Helper()
		if err := s.db.QueryRow(`SELECT calls, bytes_returned, bytes_kept, cc_pid, project, started_at, updated_at
FROM tool_stats WHERE server_id = 'srv1' AND tool = 'ctx_execute' AND window = ?`, window).Scan(&calls, &returned, &kept, &ccPID, &project, &started, &updated); err != nil {
			t.Fatal(err)
		}
	}
	// No session of cc 42 yet: the window is ''.
	row("")
	if calls != 2 || returned != 150 || kept != 900 || ccPID != 42 || project != "/p" || started != "2026-10-21 10:00:00" || len(updated) != len(sqliteTime) {
		t.Fatalf("row = %d %d %d %d %q %q %q", calls, returned, kept, ccPID, project, started, updated)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM tool_stats"); n != 3 {
		t.Fatalf("rows = %d, want 3", n)
	}

	// Sessions of cc 42: the newest one in /p is the window; /q's is ignored.
	for _, sess := range []struct{ id, project, started string }{
		{"s1", "/p", "2026-10-21 09:00:00"}, {"s2", "/p", "2026-10-21 09:30:00"}, {"s3", "/q", "2026-10-21 11:00:00"},
	} {
		if err := s.SetSessionCC(sess.id, sess.project, 42); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec("UPDATE session_meta SET started_at = ? WHERE session_id = ?", sess.started, sess.id); err != nil {
			t.Fatal(err)
		}
	}
	add("srv1", "ctx_execute", 1, 2)
	row("2026-10-21 09:30:00")
	if calls != 1 || returned != 1 || kept != 2 {
		t.Fatalf("windowed row = %d %d %d", calls, returned, kept)
	}
	// cc_pid 0 never takes a window, even with sessions registered under 0.
	if err := s.SetSessionCC("s0", "/p", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.AddToolStat("srv0", 0, "/p", "2026-10-21 10:00:00", "ctx_execute", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT window FROM tool_stats WHERE server_id = 'srv0'").Scan(&window); err != nil || window != "" {
		t.Fatalf("srv0 window = %q, %v", window, err)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM tool_stats"); n != 5 {
		t.Fatalf("rows = %d, want 5", n)
	}
}

func TestSetSessionCC(t *testing.T) {
	s := openTest(t)
	// A session SessionStart has not registered yet gets its meta row.
	if err := s.SetSessionCC("s1", "/p", 4242); err != nil {
		t.Fatal(err)
	}
	insertTestEvents(t, s, "s1", "/p", Event{Type: "a", Category: "file", Priority: 1, Data: "x"})
	if err := s.SetSessionCC("s1", "/p", 777); err != nil {
		t.Fatal(err)
	}
	var cc, events int
	var project string
	if err := s.db.QueryRow("SELECT cc_pid, event_count, project FROM session_meta WHERE session_id = 's1'").Scan(&cc, &events, &project); err != nil {
		t.Fatal(err)
	}
	if cc != 777 || events != 1 || project != "/p" {
		t.Fatalf("meta = cc %d, events %d, project %q", cc, events, project)
	}
	// Sessions registered by InsertEvents alone keep cc_pid 0.
	insertTestEvents(t, s, "s2", "/p", Event{Type: "a", Category: "file", Priority: 1, Data: "y"})
	if n := tableCount(t, s, "SELECT cc_pid FROM session_meta WHERE session_id = 's2'"); n != 0 {
		t.Fatalf("s2 cc_pid = %d", n)
	}
}

// statsFixture is two Claude Code processes (cc 100 and 200) with one
// session each in /p, their servers' rows, and a foreign project /q.
// Session starts are set directly: the columns have one-second resolution.
// Each row's window is the session started_at at write time.
func statsFixture(t *testing.T) *Store {
	t.Helper()
	s := openTest(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	add := func(server string, cc int, project, tool string, returned, kept int64) {
		t.Helper()
		if err := s.AddToolStat(server, cc, project, "2026-10-21 08:00:00", tool, returned, kept); err != nil {
			t.Fatal(err)
		}
	}
	for _, sess := range []struct {
		id      string
		cc      int
		project string
	}{{"sA", 100, "/p"}, {"sB", 200, "/p"}, {"sQ", 300, "/q"}} {
		if err := s.SetSessionCC(sess.id, sess.project, sess.cc); err != nil {
			t.Fatal(err)
		}
		exec("UPDATE session_meta SET started_at = '2026-10-21 09:00:00' WHERE session_id = ?", sess.id)
	}
	add("srvA", 100, "/p", "ctx_execute", 100, 900)
	add("srvA", 100, "/p", "ctx_execute", 100, 0)
	add("srvB", 200, "/p", "ctx_execute", 10, 90)
	add("srvB", 200, "/p", "ctx_search", 5, 0)
	add("srvQ", 300, "/q", "ctx_fetch_and_index", 1, 4000)
	add("srvX", 0, "/p", "ctx_execute", 2, 8)
	insertTestEvents(t, s, "sA", "/p",
		Event{Type: "bash-redirected", Category: "redirect", Priority: 2, Data: "a", BytesAvoided: 8192},
		Event{Type: "webfetch-redirected", Category: "redirect", Priority: 2, Data: "b", BytesAvoided: 16384},
		Event{Type: "file_read", Category: "file", Priority: 1, Data: "c"})
	insertTestEvents(t, s, "sQ", "/q", Event{Type: "read-redirected", Category: "redirect", Priority: 2, Data: "d", BytesAvoided: 1000})
	exec("UPDATE session_meta SET compact_count = 2 WHERE session_id = 'sA'")
	for _, src := range []struct{ project, label, content string }{
		{"/p", "one", "héllo"}, {"/p", "two", "abc"}, {"/q", "three", "zzzzzzzzzz"},
	} {
		if _, err := s.Index(src.project, src.label, []Chunk{{Title: "T", Content: src.content}}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestStatsLinksSessionByCCPID(t *testing.T) {
	s := statsFixture(t)
	got, err := s.Stats(StatsQuery{ServerID: "srvA", CCPID: 100, Project: "/p"})
	if err != nil {
		t.Fatal(err)
	}
	want := StatsSnapshot{
		SessionID: "sA", SessionLinked: true, SessionStart: "2026-10-21 09:00:00",
		Tools: []ToolStat{{Tool: "ctx_execute", Calls: 2, BytesReturned: 200, BytesKept: 900}}, Raw: 1100,
		Redirects: 2, RedirectBytes: 24576,
		Events: 3, Compactions: 2, Categories: map[string]int{"redirect": 2, "file": 1},
		ProjectSources: 2, ProjectContentBytes: 9, // "héllo" is 6 bytes, not 5 characters
		ProjectSessions: 2, ProjectEvents: 3,
		LifetimeKept: 900 + 90 + 4000 + 8 + 24576 + 1000, LifetimeSessions: 3, LifetimeSince: "2026-10-21 08:00:00",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Stats =\n%+v\nwant\n%+v", got, want)
	}

	// The other Claude Code process in the same project sees only its own server.
	got, err = s.Stats(StatsQuery{ServerID: "srvB", CCPID: 200, Project: "/p"})
	if err != nil {
		t.Fatal(err)
	}
	wantTools := []ToolStat{{Tool: "ctx_execute", Calls: 1, BytesReturned: 10, BytesKept: 90}, {Tool: "ctx_search", Calls: 1, BytesReturned: 5}}
	if got.SessionID != "sB" || !got.SessionLinked || !reflect.DeepEqual(got.Tools, wantTools) || got.Raw != 100 || got.Events != 0 || got.Redirects != 0 || got.Categories == nil {
		t.Fatalf("srvB: %+v", got)
	}
}

// /clear keeps the server process: its later calls go to the new session's
// window, and each session sees only its own calls.
func TestStatsClearWindow(t *testing.T) {
	s := statsFixture(t)
	if err := s.SetSessionCC("sA2", "/p", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE session_meta SET started_at = '2026-10-21 11:00:00' WHERE session_id = 'sA2'"); err != nil {
		t.Fatal(err)
	}
	q := StatsQuery{ServerID: "srvA", CCPID: 100, Project: "/p"}
	got, err := s.Stats(q)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "sA2" || got.SessionStart != "2026-10-21 11:00:00" || len(got.Tools) != 0 || got.Raw != 0 || got.Events != 0 {
		t.Fatalf("after /clear: %+v", got)
	}
	// The same server writes again, after the /clear.
	if err := s.AddToolStat("srvA", 100, "/p", "2026-10-21 08:00:00", "ctx_execute", 1, 2); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Stats(q)
	if want := []ToolStat{{Tool: "ctx_execute", Calls: 1, BytesReturned: 1, BytesKept: 2}}; !reflect.DeepEqual(got.Tools, want) || got.Raw != 3 {
		t.Fatalf("second session: %+v", got)
	}
	// The first session keeps its own calls, in its own window.
	first, err := s.toolStats("cc_pid = 100 AND project = '/p' AND window = '2026-10-21 09:00:00'")
	if want := []ToolStat{{Tool: "ctx_execute", Calls: 2, BytesReturned: 200, BytesKept: 900}}; err != nil || !reflect.DeepEqual(first, want) {
		t.Fatalf("first session: %+v %v", first, err)
	}
	// Unlinked, the server's own rows add up across windows.
	if got, _ := s.Stats(StatsQuery{ServerID: "srvA", CCPID: 0, Project: "/p"}); !reflect.DeepEqual(got.Tools, []ToolStat{{Tool: "ctx_execute", Calls: 3, BytesReturned: 201, BytesKept: 902}}) {
		t.Fatalf("unlinked: %+v", got.Tools)
	}
}

func TestStatsUnlinked(t *testing.T) {
	s := statsFixture(t)
	for _, q := range []StatsQuery{
		{ServerID: "srvX", CCPID: 0, Project: "/p"},   // pid unknown: never linked, even to old cc_pid 0 rows
		{ServerID: "srvX", CCPID: 100, Project: "/q"}, // cc_pid of a session in another project
		{ServerID: "srvX", CCPID: 999, Project: "/p"}, // no SessionStart for this pid
	} {
		if err := s.SetSessionCC("old", "/p", 0); err != nil {
			t.Fatal(err)
		}
		got, err := s.Stats(q)
		if err != nil {
			t.Fatal(err)
		}
		if got.SessionLinked || got.SessionID != "" || got.SessionStart != "" || got.Events != 0 ||
			!reflect.DeepEqual(got.Tools, []ToolStat{{Tool: "ctx_execute", Calls: 1, BytesReturned: 2, BytesKept: 8}}) {
			t.Errorf("%+v: %+v", q, got)
		}
	}
}

func TestStatsEmptyDB(t *testing.T) {
	s := openTest(t)
	got, err := s.Stats(StatsQuery{ServerID: "srv", CCPID: 1, Project: "/p"})
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionLinked || len(got.Tools) != 0 || got.LifetimeKept != 0 || got.LifetimeSince != "" || got.LifetimeSessions != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestSourceBytes(t *testing.T) {
	s := openTest(t)
	r, err := s.Index("/p", "x", []Chunk{{Title: "A", Content: "héllo"}, {Title: "B", Content: "abc"}})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.SourceBytes(r.SourceID); err != nil || n != 9 {
		t.Fatalf("SourceBytes = %d, %v", n, err)
	}
	if n, err := s.SourceBytes(r.SourceID + 1); err != nil || n != 0 {
		t.Fatalf("missing source = %d, %v", n, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestStats|TestSourceBytes' -v`
Expected: FAIL with "internal/store/stats_test.go:148:16: s.Stats undefined (type *Store has no field or method Stats)"

- [ ] **Step 3: Write minimal implementation**

`internal/store/stats.go`:
```go
package store

import (
	"database/sql"
	"errors"
)

// ToolStat is one tool's summed tool_stats counters.
type ToolStat struct {
	Tool                            string
	Calls, BytesReturned, BytesKept int64
}

// AddToolStat records one finished tool call of a cmgo serve process: calls+1
// and the call's bytes are added, updated_at becomes now. The row's window is
// the started_at of the newest session of ccPID in project (” when there is
// none, or ccPID is 0), so after a /clear the calls go to new rows (stats
// spec §1.2). The first call of a (serverID, tool, window) inserts the row
// with startedAt, the server start. One statement: the window is read in the
// same transaction as the write, and a call costs one write.
func (s *Store) AddToolStat(serverID string, ccPID int, project, startedAt, tool string, returned, kept int64) error {
	_, err := s.db.Exec(`INSERT INTO tool_stats(server_id, cc_pid, project, tool, calls, bytes_returned, bytes_kept, started_at, window, updated_at)
VALUES(?1, ?2, ?3, ?4, 1, ?5, ?6, ?7, CASE WHEN ?2 = 0 THEN '' ELSE COALESCE((`+newestSession+`), '') END, datetime('now'))
ON CONFLICT(server_id, tool, window) DO UPDATE SET calls = calls + 1,
  bytes_returned = bytes_returned + excluded.bytes_returned,
  bytes_kept = bytes_kept + excluded.bytes_kept,
  updated_at = excluded.updated_at`,
		serverID, ccPID, project, tool, returned, kept, startedAt)
	return err
}

// newestSession selects the started_at of the newest session of Claude Code
// pid ?2 in project ?3: the window a server write belongs to.
const newestSession = `SELECT started_at FROM session_meta WHERE cc_pid = ?2 AND project = ?3
ORDER BY started_at DESC, rowid DESC LIMIT 1`

// SetSessionCC records the Claude Code pid that owns a session (stats spec
// §1.2). A session not seen yet gets its session_meta row, as InsertEvents'
// upsert would create it.
func (s *Store) SetSessionCC(sessionID, project string, ccPID int) error {
	_, err := s.db.Exec(`INSERT INTO session_meta(session_id, project, started_at, last_event_at, cc_pid)
VALUES(?, ?, datetime('now'), datetime('now'), ?)
ON CONFLICT(session_id) DO UPDATE SET cc_pid = excluded.cc_pid`, sessionID, project, ccPID)
	return err
}

// StatsQuery identifies the cmgo serve process asking for ctx_stats.
type StatsQuery struct {
	ServerID string
	CCPID    int // 0 when serve found no Claude Code ancestor
	Project  string
}

// StatsSnapshot is everything the ctx_stats report shows except the version
// and the clock (stats spec §1.2, §2).
type StatsSnapshot struct {
	SessionID     string
	SessionLinked bool
	SessionStart  string     // 'YYYY-MM-DD HH:MM:SS' UTC; "" when not linked
	Tools         []ToolStat // linked session's rows, else this server's rows; by tool name
	Raw           int64      // bytes processed: Kept + Returned over the Tools that have a raw size
	// Redirects and RedirectBytes are the linked session's "redirect" events:
	// their count and SUM(bytes_avoided).
	Redirects, RedirectBytes int64
	Events, Compactions      int
	Categories               map[string]int // event count per category; nil when not linked
	ProjectSources           int
	ProjectContentBytes      int64 // UTF-8 bytes of the project's chunk contents
	ProjectSessions          int
	ProjectEvents            int
	LifetimeKept             int64 // every tool_stats bytes_kept plus every redirect bytes_avoided
	LifetimeSessions         int
	LifetimeSince            string // earliest tool_stats started_at; "" when there is none
}

// rawTools are the tools whose calls have a raw size (stats spec §1.3); the
// others (ctx_search, ctx_purge, ctx_stats) only return bytes.
var rawTools = map[string]bool{"ctx_execute": true, "ctx_execute_file": true, "ctx_batch_execute": true, "ctx_fetch_and_index": true}

// rawBytes is the bytes processed by tools: what they kept plus what they returned.
func rawBytes(tools []ToolStat) int64 {
	var n int64
	for _, t := range tools {
		if rawTools[t.Tool] {
			n += t.BytesKept + t.BytesReturned
		}
	}
	return n
}

// lifetimeKept is the all-projects kept-out total (stats spec §2 "Lifetime").
const lifetimeKept = `(SELECT COALESCE(SUM(bytes_kept), 0) FROM tool_stats) +
  (SELECT COALESCE(SUM(bytes_avoided), 0) FROM session_events WHERE category = 'redirect')`

// Stats gathers the ctx_stats numbers. The caller's session is the newest
// one its Claude Code process started in this project; its server rows are
// those of that pid and project whose window is the session's started_at, so
// a /clear starts from zero (stats spec §1.2). With no linked session the
// server's own rows, all windows, are shown.
func (s *Store) Stats(q StatsQuery) (StatsSnapshot, error) {
	var st StatsSnapshot
	// cc_pid 0 means unknown, and every session_meta row older than
	// migration 3 has it: never link on it.
	if q.CCPID != 0 {
		err := s.db.QueryRow(`SELECT session_id, started_at FROM session_meta WHERE cc_pid = ? AND project = ?
ORDER BY started_at DESC, rowid DESC LIMIT 1`, q.CCPID, q.Project).Scan(&st.SessionID, &st.SessionStart)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return st, err
		}
		st.SessionLinked = err == nil
	}
	var err error
	if st.SessionLinked {
		st.Tools, err = s.toolStats("cc_pid = ? AND project = ? AND window = ?", q.CCPID, q.Project, st.SessionStart)
	} else {
		st.Tools, err = s.toolStats("server_id = ?", q.ServerID)
	}
	if err != nil {
		return st, err
	}
	st.Raw = rawBytes(st.Tools)
	if st.SessionLinked {
		if err := s.db.QueryRow(`SELECT count(*), COALESCE(SUM(bytes_avoided), 0) FROM session_events
WHERE session_id = ? AND category = 'redirect'`, st.SessionID).Scan(&st.Redirects, &st.RedirectBytes); err != nil {
			return st, err
		}
		if err := s.db.QueryRow("SELECT compact_count FROM session_meta WHERE session_id = ?", st.SessionID).Scan(&st.Compactions); err != nil {
			return st, err
		}
		if st.Categories, st.Events, err = s.categories(st.SessionID); err != nil {
			return st, err
		}
	}
	err = s.db.QueryRow(`SELECT (SELECT count(*) FROM sources WHERE project = ?),
  (SELECT COALESCE(SUM(length(CAST(content AS BLOB))), 0) FROM chunks WHERE source_id IN (SELECT id FROM sources WHERE project = ?)),
  (SELECT count(*) FROM session_meta WHERE project = ?),
  (SELECT count(*) FROM session_events WHERE project = ?),
  `+lifetimeKept+`,
  (SELECT count(*) FROM session_meta),
  (SELECT COALESCE(MIN(started_at), '') FROM tool_stats)`, q.Project, q.Project, q.Project, q.Project).Scan(
		&st.ProjectSources, &st.ProjectContentBytes, &st.ProjectSessions, &st.ProjectEvents,
		&st.LifetimeKept, &st.LifetimeSessions, &st.LifetimeSince)
	return st, err
}

// toolStats sums tool_stats rows matching where, one ToolStat per tool.
func (s *Store) toolStats(where string, args ...any) ([]ToolStat, error) {
	rows, err := s.db.Query("SELECT tool, SUM(calls), SUM(bytes_returned), SUM(bytes_kept) FROM tool_stats WHERE "+where+
		" GROUP BY tool ORDER BY tool", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ToolStat
	for rows.Next() {
		var t ToolStat
		if err := rows.Scan(&t.Tool, &t.Calls, &t.BytesReturned, &t.BytesKept); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// categories counts a session's events per category, and in total.
func (s *Store) categories(sessionID string) (map[string]int, int, error) {
	rows, err := s.db.Query("SELECT category, count(*) FROM session_events WHERE session_id = ? GROUP BY category", sessionID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	cats, total := map[string]int{}, 0
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			return nil, 0, err
		}
		cats[name] = n
		total += n
	}
	return cats, total, rows.Err()
}

// SourceBytes is the UTF-8 size of a source's chunk contents: the raw bytes a
// ctx_fetch_and_index cache hit keeps out (stats spec §1.3).
// shortcut: source_id is UNINDEXED in FTS5, so this scans every chunk; add a
// bytes column to sources if cache hits on a large knowledge base get slow.
func (s *Store) SourceBytes(sourceID int64) (int64, error) {
	var n int64
	err := s.db.QueryRow("SELECT COALESCE(SUM(length(CAST(content AS BLOB))), 0) FROM chunks WHERE source_id = ?", sourceID).Scan(&n)
	return n, err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run 'TestStats|TestSourceBytes' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/stats.go internal/store/stats_test.go
git commit -m "feat(store): link server stats to the Claude Code session window in Stats"
```

### Task 9: Read-only open and the statusline numbers

**Files:**
- Create: `internal/store/readonly.go`
- Test: `internal/store/readonly_test.go`

**Interfaces:**
- Consumes: `toolStats`, `rawBytes`, `lifetimeKept` from Task 8; `statsFixture` (test) from Task 8; the `session_events_redirect` index from Task 7
- Produces: `func OpenReadOnly(path string) (*Store, error)`; `func (s *Store) UserVersion() (int, error)`; `func (s *Store) SessionLine(sessionID string) (sessionKept, sessionRaw, lifetime int64, err error)`

- [ ] **Step 1: Write the failing test**

`internal/store/readonly_test.go`:
```go
package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenReadOnlyMissingFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	if s, err := OpenReadOnly(filepath.Join(dir, "cmgo.db")); err == nil {
		s.Close()
		t.Fatal("OpenReadOnly of a missing file succeeded")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("OpenReadOnly created %s: %v", dir, err)
	}
}

// fileState is a file's size and mtime, or "absent".
func fileState(t *testing.T, path string) string {
	t.Helper()
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "absent"
	}
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d bytes, %v", fi.Size(), fi.ModTime())
}

// The statusline reads while a hook or the server holds the write lock: a
// WAL reader must not wait, must see only committed rows, and must leave the
// DB and its -wal file as they were.
func TestOpenReadOnlyUnderWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.SetSessionCC("s1", "/p", 100); err != nil {
		t.Fatal(err)
	}
	if err := w.AddToolStat("srv", 100, "/p", "2000-01-01 00:00:00", "ctx_execute", 10, 90); err != nil {
		t.Fatal(err)
	}
	tx, err := w.db.Begin() // BEGIN IMMEDIATE: holds the write lock
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE tool_stats SET bytes_kept = 5000"); err != nil {
		t.Fatal(err)
	}
	before := fileState(t, path) + " | " + fileState(t, path+"-wal")

	r, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := r.UserVersion(); err != nil || v != len(migrations) {
		t.Fatalf("UserVersion = %d, %v", v, err)
	}
	kept, raw, life, err := r.SessionLine("s1")
	if err != nil || kept != 90 || raw != 100 || life != 90 {
		t.Fatalf("SessionLine = %d %d %d %v", kept, raw, life, err)
	}
	if err := r.AddToolStat("srv", 100, "/p", "2000-01-01 00:00:00", "ctx_execute", 1, 1); err == nil {
		t.Fatal("write through a read-only store succeeded")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if after := fileState(t, path) + " | " + fileState(t, path+"-wal"); after != before {
		t.Fatalf("files changed:\nbefore %s\nafter  %s", before, after)
	}
}

// With no other connection, SQLite has removed -wal and -shm; a read-only
// open must still read the WAL-mode file.
func TestOpenReadOnlyClosedDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddToolStat("srv", 100, "/p", "2000-01-01 00:00:00", "ctx_execute", 10, 90); err != nil {
		t.Fatal(err)
	}
	w.Close()
	before := fileState(t, path)
	r, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, _, life, err := r.SessionLine("unknown"); err != nil || life != 90 {
		t.Fatalf("SessionLine = %d, %v", life, err)
	}
	if after := fileState(t, path); after != before {
		t.Fatalf("db changed: %s -> %s", before, after)
	}
}

func TestSessionLine(t *testing.T) {
	s := statsFixture(t)
	cases := []struct {
		session         string
		kept, raw, life int64
	}{
		// sA: srvA's ctx_execute row; lifetime is every kept byte plus every redirect.
		{"sA", 900, 1100, 900 + 90 + 4000 + 8 + 24576 + 1000},
		// sB: ctx_search returns bytes but has no raw size.
		{"sB", 90, 100, 900 + 90 + 4000 + 8 + 24576 + 1000},
		{"missing", 0, 0, 900 + 90 + 4000 + 8 + 24576 + 1000},
	}
	for _, c := range cases {
		kept, raw, life, err := s.SessionLine(c.session)
		if err != nil || kept != c.kept || raw != c.raw || life != c.life {
			t.Errorf("%s: %d %d %d %v, want %d %d %d", c.session, kept, raw, life, err, c.kept, c.raw, c.life)
		}
	}
	// A session whose pid is unknown never takes the cc_pid 0 rows (srvX).
	if err := s.SetSessionCC("s0", "/p", 0); err != nil {
		t.Fatal(err)
	}
	if kept, raw, _, err := s.SessionLine("s0"); err != nil || kept != 0 || raw != 0 {
		t.Fatalf("s0: %d %d %v", kept, raw, err)
	}
	// /clear: the new session under cc 100 starts after srvA's last write.
	if err := s.SetSessionCC("sA2", "/p", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE session_meta SET started_at = '2026-10-21 11:00:00' WHERE session_id = 'sA2'"); err != nil {
		t.Fatal(err)
	}
	if kept, raw, _, err := s.SessionLine("sA2"); err != nil || kept != 0 || raw != 0 {
		t.Fatalf("sA2: %d %d %v", kept, raw, err)
	}
	// srvA, kept across the /clear, calls again: only sA2 sees the call.
	if err := s.AddToolStat("srvA", 100, "/p", "2026-10-21 08:00:00", "ctx_execute", 1, 2); err != nil {
		t.Fatal(err)
	}
	if kept, raw, _, err := s.SessionLine("sA2"); err != nil || kept != 2 || raw != 3 {
		t.Fatalf("sA2 after a call: %d %d %v", kept, raw, err)
	}
	if kept, raw, _, err := s.SessionLine("sA"); err != nil || kept != 900 || raw != 1100 {
		t.Fatalf("sA after /clear: %d %d %v", kept, raw, err)
	}
}

// The lifetime total runs on every statusline call; it must read the partial
// redirect index, not scan session_events.
func TestLifetimeKeptUsesRedirectIndex(t *testing.T) {
	s := openTest(t)
	rows, err := s.db.Query("EXPLAIN QUERY PLAN SELECT " + lifetimeKept)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	got := strings.Join(plan, "\n")
	if !strings.Contains(got, "session_events USING COVERING INDEX session_events_redirect") {
		t.Fatalf("plan:\n%s", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestOpenReadOnly|TestSessionLine|TestLifetimeKept' -v`
Expected: FAIL with "internal/store/readonly_test.go:13:15: undefined: OpenReadOnly"

- [ ] **Step 3: Write minimal implementation**

`internal/store/readonly.go`:
```go
package store

import (
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"

	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

// readOnlyBusy bounds a read-only wait. A WAL reader does not wait for
// writers; it only waits while another connection recovers the WAL index.
const readOnlyBusy = "250"

// OpenReadOnly opens an existing DB for the statusline (stats spec §3): the
// file URI has mode=ro, and unlike Open it creates no directory, sets no
// journal mode and runs no migration, so it never writes. It fails when the
// file is missing; callers check UserVersion before relying on a table.
func OpenReadOnly(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: abs}).String() + "?mode=ro&_pragma=busy_timeout(" + readOnlyBusy + ")"
	db, err := driver.Open(dsn, fts5.Register)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, fuzzyVals: map[string]string{}}, nil
}

// UserVersion is the schema version: the number of migrations applied.
func (s *Store) UserVersion() (int, error) {
	var v int
	err := s.db.QueryRow("PRAGMA user_version").Scan(&v)
	return v, err
}

// SessionLine returns the statusline numbers (stats spec §3): the kept and
// raw bytes of the server rows linked to sessionID (its cc_pid and project,
// window = its started_at, as Stats links them), and the all-projects kept
// total. An unknown session, or one whose cc_pid is 0, has no server rows.
func (s *Store) SessionLine(sessionID string) (sessionKept, sessionRaw, lifetime int64, err error) {
	var cc int
	var project, started string
	err = s.db.QueryRow("SELECT cc_pid, project, started_at FROM session_meta WHERE session_id = ?", sessionID).Scan(&cc, &project, &started)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, err
	}
	if err == nil && cc != 0 {
		tools, err := s.toolStats("cc_pid = ? AND project = ? AND window = ?", cc, project, started)
		if err != nil {
			return 0, 0, 0, err
		}
		for _, t := range tools {
			sessionKept += t.BytesKept
		}
		sessionRaw = rawBytes(tools)
	}
	err = s.db.QueryRow("SELECT " + lifetimeKept).Scan(&lifetime)
	return sessionKept, sessionRaw, lifetime, err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run 'TestOpenReadOnly|TestSessionLine|TestLifetimeKept' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/readonly.go internal/store/readonly_test.go
git commit -m "feat(store): add OpenReadOnly, UserVersion and SessionLine"
```

### Task 10: Project purge deletes tool_stats

**Files:**
- Modify: `internal/store/purge.go`
- Test: `internal/store/purge_test.go`

**Interfaces:**
- Consumes: `AddToolStat` from Task 7
- Produces: `func (s *Store) PurgeProject(project string) (sources, sessions int64, err error)` (signature unchanged) now also deletes the project's `tool_stats` rows

- [ ] **Step 1: Write the failing test**

`internal/store/purge_test.go`:
```go
package store

import "testing"

func tableCount(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPurgeSession(t *testing.T) {
	s := openTest(t)
	insertTestEvents(t, s, "s1", "/p", Event{Type: "a", Category: "file", Priority: 1, Data: "x"}, Event{Type: "b", Category: "git", Priority: 2, Data: "y"})
	insertTestEvents(t, s, "s2", "/p", Event{Type: "a", Category: "file", Priority: 1, Data: "z"})
	if _, err := s.Index("/p", "notes", []Chunk{{Title: "T", Content: "kept content"}}); err != nil {
		t.Fatal(err)
	}

	// 2 events + 1 meta row.
	if rows, err := s.PurgeSession("s1", "/p"); err != nil || rows != 3 {
		t.Fatalf("PurgeSession = %d, %v", rows, err)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM session_events WHERE session_id = 's1'") +
		tableCount(t, s, "SELECT count(*) FROM session_meta WHERE session_id = 's1'"); n != 0 {
		t.Fatalf("s1 rows left: %d", n)
	}
	if evs, _ := s.SessionEvents("s2"); len(evs) != 1 {
		t.Fatalf("s2 events = %d", len(evs))
	}
	if n, _ := s.ChunkCount("/p"); n != 1 {
		t.Fatalf("chunks = %d", n)
	}
	if rows, err := s.PurgeSession("s1", "/p"); err != nil || rows != 0 {
		t.Fatalf("second PurgeSession = %d, %v", rows, err)
	}
}

func TestPurgeProjectLeavesOtherProjects(t *testing.T) {
	s := openTest(t)
	for _, p := range []string{"/p", "/q"} {
		for _, label := range []string{"notes", "docs"} {
			if _, err := s.Index(p, label, []Chunk{{Title: "T", Content: "gizmo widget " + p}, {Title: "U", Content: "second chunk"}}); err != nil {
				t.Fatal(err)
			}
		}
		insertTestEvents(t, s, p+"-s1", p, Event{Type: "a", Category: "file", Priority: 1, Data: "x"})
		insertTestEvents(t, s, p+"-s2", p, Event{Type: "a", Category: "file", Priority: 1, Data: "y"})
		for _, tool := range []string{"ctx_execute", "ctx_search"} {
			if err := s.AddToolStat("srv"+p, 7, p, "2026-10-21 08:00:00", tool, 1, 2); err != nil {
				t.Fatal(err)
			}
		}
	}
	vocab := tableCount(t, s, "SELECT count(*) FROM vocabulary")

	sources, sessions, err := s.PurgeProject("/p")
	if err != nil || sources != 2 || sessions != 2 {
		t.Fatalf("PurgeProject = %d %d %v", sources, sessions, err)
	}
	if n, _ := s.ChunkCount("/p"); n != 0 {
		t.Fatalf("/p chunks = %d", n)
	}
	if n, _ := s.ChunkCount("/q"); n != 4 {
		t.Fatalf("/q chunks = %d", n)
	}
	// Trigram rows go with their sources; /q keeps its 4.
	if n := tableCount(t, s, "SELECT count(*) FROM chunks_trigram"); n != 4 {
		t.Fatalf("trigram rows = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM sources WHERE project = '/p'"); n != 0 {
		t.Fatalf("/p sources = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM session_events WHERE project = '/p'") +
		tableCount(t, s, "SELECT count(*) FROM session_meta WHERE project = '/p'"); n != 0 {
		t.Fatalf("/p session rows = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM session_events WHERE project = '/q'"); n != 2 {
		t.Fatalf("/q events = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM session_meta WHERE project = '/q'"); n != 2 {
		t.Fatalf("/q meta = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM tool_stats WHERE project = '/p'"); n != 0 {
		t.Fatalf("/p tool_stats = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM tool_stats WHERE project = '/q'"); n != 2 {
		t.Fatalf("/q tool_stats = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM vocabulary"); n != vocab {
		t.Fatalf("vocabulary %d -> %d", vocab, n)
	}
	if sources, sessions, err := s.PurgeProject("/p"); err != nil || sources != 0 || sessions != 0 {
		t.Fatalf("second PurgeProject = %d %d %v", sources, sessions, err)
	}
}

// One DB serves every project, so a session id from another project must not
// be purged by a call made in this one (project /x/ab vs /x/a).
func TestPurgeSessionIgnoresOtherProjects(t *testing.T) {
	s := openTest(t)
	insertTestEvents(t, s, "shared", "/x/ab", Event{Type: "a", Category: "file", Priority: 1, Data: "x"})
	insertTestEvents(t, s, "mine", "/x/a", Event{Type: "a", Category: "file", Priority: 1, Data: "y"})

	if rows, err := s.PurgeSession("shared", "/x/a"); err != nil || rows != 0 {
		t.Fatalf("purge from /x/a of a /x/ab session = %d, %v, want 0", rows, err)
	}
	if evs, _ := s.SessionEvents("shared"); len(evs) != 1 {
		t.Fatalf("/x/ab events = %d, want 1", len(evs))
	}
	if n := tableCount(t, s, "SELECT count(*) FROM session_meta WHERE session_id = 'shared'"); n != 1 {
		t.Fatalf("/x/ab meta rows = %d, want 1", n)
	}
	if rows, err := s.PurgeSession("mine", "/x/a"); err != nil || rows != 2 {
		t.Fatalf("own session = %d, %v, want 2", rows, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestPurgeProjectLeavesOtherProjects -v`
Expected: FAIL with "purge_test.go:87: /p tool_stats = 2"

- [ ] **Step 3: Write minimal implementation**

`internal/store/purge.go`:
```go
package store

import "database/sql"

// PurgeSession deletes one session's session_events and session_meta rows
// and returns how many rows went. Port of the session scope of purgeSession
// (src/session/purge.ts:196-275); chunks carry no session id here. TS had one
// DB per project; here the DB is shared, so only project's rows can go.
func (s *Store) PurgeSession(sessionID, project string) (rows int64, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, q := range []string{
		"DELETE FROM session_events WHERE session_id = ? AND project = ?",
		"DELETE FROM session_meta WHERE session_id = ? AND project = ?",
	} {
		n, err := execCount(tx, q, sessionID, project)
		if err != nil {
			return 0, err
		}
		rows += n
	}
	return rows, tx.Commit()
}

// PurgeProject deletes every source of project with its porter and trigram
// chunks, the events and meta rows of the project's sessions, and the
// project's tool_stats rows (stats spec §4). The shared vocabulary is kept. Returns the sources and sessions (meta rows) deleted.
// This replaces the TS project scope, which unlinked the per-project DB files
// (purge.ts:277-338); cmgo's DB is shared, so only this project's rows go.
func (s *Store) PurgeProject(project string) (sources, sessions int64, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	ids := "SELECT id FROM sources WHERE project = ?"
	if _, err := tx.Exec("DELETE FROM chunks WHERE source_id IN ("+ids+")", project); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec("DELETE FROM chunks_trigram WHERE source_id IN ("+ids+")", project); err != nil {
		return 0, 0, err
	}
	if sources, err = execCount(tx, "DELETE FROM sources WHERE project = ?", project); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec("DELETE FROM session_events WHERE project = ? OR session_id IN (SELECT session_id FROM session_meta WHERE project = ?)",
		project, project); err != nil {
		return 0, 0, err
	}
	if sessions, err = execCount(tx, "DELETE FROM session_meta WHERE project = ?", project); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec("DELETE FROM tool_stats WHERE project = ?", project); err != nil {
		return 0, 0, err
	}
	return sources, sessions, tx.Commit()
}

// execCount runs a statement and returns its RowsAffected.
func execCount(tx *sql.Tx, query string, args ...any) (int64, error) {
	res, err := tx.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestPurgeProjectLeavesOtherProjects -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/purge.go internal/store/purge_test.go
git commit -m "feat(store): purge the project's tool_stats rows"
```

### Task 11: StatsRecorder and the execute tools' raw bytes

**Files:**
- Modify: `internal/mcpserver/server.go`
- Create: `internal/mcpserver/stats.go`
- Modify: `internal/mcpserver/execute.go`
- Modify: `internal/mcpserver/executefile.go`
- Test: `internal/mcpserver/stats_test.go`

**Interfaces:**
- Consumes: `store.AddToolStat`, `store.Stats`, `store.StatsQuery`, `store.ToolStat` from Task 7/Task 8
- Produces: `type StatsRecorder struct{ ServerID string; CCPID int; StartedAt string }`; `func (r *StatsRecorder) Record(d *Deps, tool string, raw int64, res *mcp.CallToolResult)`; `Deps.Stats *StatsRecorder`; unexported `func fileSize(projectDir, path string) int64`; test helpers `(*harness).recordStats`, `args(kv ...any) string`

- [ ] **Step 1: Write the failing test**

`internal/mcpserver/stats_test.go`:
```go
package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

// recordStats turns stats on for h, as serve does, and returns a reader of
// one tool's row for this server.
func (h *harness) recordStats() func(tool string) store.ToolStat {
	h.t.Helper()
	h.Deps.Stats = &StatsRecorder{ServerID: "srv-test", CCPID: 0, StartedAt: "2026-10-21 08:00:00"}
	return func(tool string) store.ToolStat {
		h.t.Helper()
		snap, err := h.Deps.Store.Stats(store.StatsQuery{ServerID: "srv-test", Project: h.Deps.Cfg.ProjectKey})
		if err != nil {
			h.t.Fatal(err)
		}
		for _, ts := range snap.Tools {
			if ts.Tool == tool {
				return ts
			}
		}
		return store.ToolStat{Tool: tool}
	}
}

// args is a JSON object literal for h.call.
func args(kv ...any) string {
	m := map[string]any{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func TestStatsOffRecordsNothing(t *testing.T) {
	h := newHarness(t, nil)
	h.call("ctx_execute", args("language", "shell", "code", "echo hi"))
	snap, err := h.Deps.Store.Stats(store.StatsQuery{Project: h.Deps.Cfg.ProjectKey})
	if err != nil || len(snap.Tools) != 0 || snap.LifetimeSince != "" {
		t.Fatalf("rows recorded with Stats nil: %+v %v", snap, err)
	}
}

// ctx_execute: raw is the run's stdout + stderr bytes (stats spec §1.3).
func TestStatsExecuteKept(t *testing.T) {
	h := newHarness(t, nil)
	row := h.recordStats()

	small, _ := h.call("ctx_execute", args("language", "shell", "code", "printf abc; printf de >&2"))
	// 5 raw bytes, more returned (the code echo): nothing kept.
	if got, want := row("ctx_execute"), (store.ToolStat{Tool: "ctx_execute", Calls: 1, BytesReturned: int64(len(small))}); got != want {
		t.Fatalf("small run: %+v, want %+v", got, want)
	}

	// Over 5 KB with an intent: indexed, and only matching sections come back.
	big, _ := h.call("ctx_execute", args("language", "shell", "code", `head -c 100000 /dev/zero | tr '\0' a`, "intent", "letters"))
	got := row("ctx_execute")
	want := store.ToolStat{Tool: "ctx_execute", Calls: 2, BytesReturned: int64(len(small) + len(big)), BytesKept: int64(100000 - len(big))}
	if got != want || len(big) > 10000 {
		t.Fatalf("indexed run: %+v, want %+v (returned %d bytes)", got, want, len(big))
	}

	// An argument error is a call that processed nothing.
	bad, isErr := h.call("ctx_execute", args("language", "cobol", "code", "x"))
	want.Calls, want.BytesReturned = 3, want.BytesReturned+int64(len(bad))
	if got := row("ctx_execute"); !isErr || got != want {
		t.Fatalf("arg error: %+v, want %+v", got, want)
	}
}

// ctx_execute_file: raw is the file size + stdout + stderr bytes.
func TestStatsExecuteFileKept(t *testing.T) {
	h := newHarness(t, nil)
	row := h.recordStats()
	h.writeProjectFile("data.txt", strings.Repeat("x", 3000))
	code := `printf %s "${#FILE_CONTENT}"`
	text, isErr := h.call("ctx_execute_file", args("path", "data.txt", "language", "shell", "code", code))
	stdout, ok := strings.CutPrefix(text, buildExecuteEcho("shell", code, "data.txt"))
	if isErr || !ok || stdout != "3000" {
		t.Fatalf("run: %v %q", isErr, text)
	}
	want := store.ToolStat{Tool: "ctx_execute_file", Calls: 1, BytesReturned: int64(len(text)), BytesKept: int64(3000 + len(stdout) - len(text))}
	if got := row("ctx_execute_file"); got != want {
		t.Fatalf("%+v, want %+v", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcpserver/ -run 'TestStats' -v`
Expected: FAIL with "internal/mcpserver/stats_test.go:15:9: h.Deps.Stats undefined (type *Deps has no field or method Stats)"

- [ ] **Step 3: Write minimal implementation**

`internal/mcpserver/server.go`:
```go
// Package mcpserver registers cmgo's MCP tools and formats their output.
package mcpserver

import (
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/lang315/context-mode-go/internal/fetch"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Deps are the long-lived services every tool handler uses.
type Deps struct {
	Cfg    config.Config
	Store  *store.Store
	Exec   *executor.Executor
	Policy *security.Policy
	Fetch  *fetch.Fetcher
	Now    func() time.Time // injectable clock; defaults to time.Now
	Stats  *StatsRecorder   // nil records no tool_stats
}

// New builds the MCP server with all six tools registered.
func New(d *Deps, version string) *mcp.Server {
	if d.Now == nil {
		d.Now = time.Now
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "cmgo", Version: version}, nil)
	registerExecute(s, d)
	registerExecuteFile(s, d)
	registerBatch(s, d)
	registerSearch(s, d)
	registerFetchAndIndex(s, d)
	registerPurge(s, d)
	return s
}
```

`internal/mcpserver/stats.go`:
```go
package mcpserver

import "github.com/modelcontextprotocol/go-sdk/mcp"

// StatsRecorder writes this server's tool_stats rows (stats spec §1.3).
type StatsRecorder struct {
	ServerID  string // "<pid>-<start unix nanos>" of this cmgo serve process
	CCPID     int    // owning Claude Code process, 0 if unknown
	StartedAt string // server start, UTC 'YYYY-MM-DD HH:MM:SS'
}

// Record adds one finished call of tool: bytes_returned is the UTF-8 size of
// the result text, bytes_kept is max(0, raw - returned), where raw is what
// the tool produced or read. A nil recorder records nothing, and a failed
// write is dropped: stats never fail a tool.
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
	_ = d.Store.AddToolStat(r.ServerID, r.CCPID, d.Cfg.ProjectKey, r.StartedAt, tool, returned, max(0, raw-returned))
}
```

`internal/mcpserver/execute.go`:
```go
package mcpserver

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// execArgs are the arguments ctx_execute and ctx_execute_file share.
type execArgs struct {
	lang         executor.Language
	code, intent string
	timeoutMS    float64 // as given, for "after Nms" texts
	timeout      time.Duration
}

// parseExecArgs validates language/code/timeout/intent like the TS zod schemas
// (S:1692-1741, S:2076-2112).
func parseExecArgs(a Args) (execArgs, error) {
	var e execArgs
	lang, ok, err := a.String("language")
	if err != nil {
		return e, err
	}
	e.lang = executor.Language(lang)
	if !ok || !slices.Contains(executor.Languages, e.lang) {
		return e, fmt.Errorf("language: invalid enum value %q, expected one of %v", lang, executor.Languages)
	}
	if e.code, ok, err = a.String("code"); err != nil || !ok {
		return e, cmp.Or(err, fmt.Errorf("code: required"))
	}
	if e.intent, _, err = a.String("intent"); err != nil {
		return e, err
	}
	ms, ok, err := a.Number("timeout")
	if err != nil {
		return e, err
	}
	// shortcut: timeout <= 0 means no timer (Go executor), where TS setTimeout(0) fires at once.
	if ok && ms > 0 {
		e.timeoutMS, e.timeout = ms, time.Duration(ms*float64(time.Millisecond))
	}
	return e, nil
}

// msText formats the timeout like JS `${n}` (2000, 1.5).
func (e execArgs) msText() string { return strconv.FormatFloat(e.timeoutMS, 'f', -1, 64) }

// denyCode ports the deny-only firewall dispatch (S:1745-1751, S:2126-2132).
func denyCode(d *Deps, e execArgs) *mcp.CallToolResult {
	blocked, cmd, pattern := d.Policy.CheckCodeMatch(string(e.lang), e.code)
	switch {
	case !blocked:
		return nil
	case cmd == "":
		return TextResult(security.BlockedMessage(pattern), true)
	default:
		return TextResult(security.EmbeddedBlockedMessage(cmd, pattern), true)
	}
}

// finishRun is the shared tail of both handlers after the timeout branch:
// exit classification, then intent search, auto-index or inline output.
// Port of S:1879-1941 and S:2159-2219.
func finishRun(d *Deps, r executor.Result, e execArgs, echo, label string) *mcp.CallToolResult {
	out, isError := r.Stdout, false
	if r.ExitCode != 0 {
		if !executor.IsSoftFail(e.lang, r.ExitCode, r.Stdout) {
			isError, label = true, label+":error"
			out = fmt.Sprintf("Exit code: %d\n\nstdout:\n%s\n\nstderr:\n%s", r.ExitCode, r.Stdout, r.Stderr)
		}
	} else if out == "" {
		out = "(no output)"
	}

	var text string
	var err error
	switch {
	case strings.TrimSpace(e.intent) != "" && len(out) > intentSearchThreshold:
		text, err = intentSearch(d, out, e.intent, label)
	case len(out) > largeOutputThreshold && r.ExitCode != 0:
		text, err = intentSearch(d, out, "errors failures exceptions", label)
	case len(out) > largeOutputThreshold:
		text, err = indexStdout(d, out, label)
	default:
		text = out
	}
	if err != nil {
		return ErrorResult("Runtime error: %v", err)
	}
	return TextResult(echo+text, isError)
}

// execAnnotations are the TS #846 hints shared by both tools.
var execAnnotations = &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: false, OpenWorldHint: new(true)}

// registerExecute registers ctx_execute. Port of S:1647-1952.
func registerExecute(s *mcp.Server, d *Deps) {
	s.AddTool(&mcp.Tool{
		Name:        "ctx_execute",
		Title:       "Run code in a sandbox (executes the supplied code)",
		Description: executeDesc,
		Annotations: execAnnotations,
		InputSchema: json.RawMessage(executeSchema),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (res *mcp.CallToolResult, _ error) {
		var raw int64
		defer func() { d.Stats.Record(d, "ctx_execute", raw, res) }()
		a, err := ParseArgs(req.Params.Arguments)
		if err != nil {
			return ArgError(err), nil
		}
		e, err := parseExecArgs(a)
		if err != nil {
			return ArgError(err), nil
		}
		background, _, err := a.Bool("background")
		if err != nil {
			return ArgError(err), nil
		}
		cwd, _, err := a.String("cwd")
		if err != nil {
			return ArgError(err), nil
		}
		if denied := denyCode(d, e); denied != nil {
			return denied, nil
		}

		r, err := d.Exec.Run(ctx, executor.Request{Language: e.lang, Code: e.code, Timeout: e.timeout, Background: background, Cwd: cwd})
		if err != nil {
			return ErrorResult("Runtime error: %v", err), nil
		}
		raw = int64(len(r.Stdout) + len(r.Stderr))
		echo := buildExecuteEcho(string(e.lang), e.code, "")
		if r.TimedOut {
			partial := strings.TrimSpace(r.Stdout)
			switch {
			case r.Backgrounded && partial != "":
				return TextResult(echo+partial+"\n\n_(process backgrounded after "+e.msText()+"ms — still running)_", false), nil
			case partial != "":
				return TextResult(echo+partial+"\n\n_(timed out after "+e.msText()+"ms — partial output shown above)_", false), nil
			}
			return TextResult(echo+"Execution timed out after "+e.msText()+"ms\n\nstderr:\n"+r.Stderr, true), nil
		}
		return finishRun(d, r, e, echo, "execute:"+string(e.lang)), nil
	})
}

// Description and schema below are generated from server.ts S:1660-1741
// (bunNote dropped; langList is all 12 languages).

const executeDesc = `Run code in a sandboxed subprocess. Languages: javascript, typescript, python, shell, ruby, go, rust, php, perl, r, elixir, csharp.

Think-in-Code — the core philosophy: the bytes your code processes never enter your conversation memory; only what you console.log() does. Reading a 700 KB log directly means 700 KB of your remaining reasoning capacity gets spent on raw bytes. Running code over that same log in this sandbox and printing a 3 KB summary leaves you with 697 KB of capacity for the actual work.

Concrete shape — analyze 47 source files without reading any of them:
  ctx_execute(language: "javascript", code: ` + "`" + `
    const fs = require('fs');
    const files = fs.readdirSync('src').filter(f => f.endsWith('.ts'));
    files.forEach(f => {
      const lines = fs.readFileSync('src/'+f,'utf8').split('\\n').length;
      console.log(f + ': ' + lines + ' lines');
    });
  ` + "`" + `)
  // 47 files analyzed, 15,314 LoC summarized — output ~3.6 KB instead of 47 Read() calls = ~700 KB.

WHEN:
  - You intend to derive an answer FROM data (filter, count, aggregate, parse, compare, transform) — do the derivation in code and print only the answer
  - Output shape or size cannot be predicted before execution (recursive finds, repo-wide greps, list endpoints, query results, log scans)
  - You would otherwise read raw output and then mentally compute — that compute belongs here, in code, where its inputs stay out of your conversation
  - You need to keep a long-running process alive (dev server, watcher, daemon) — pass ` + "`" + `background: true` + "`" + ` to detach on timeout instead of killing the process
  - The output may legitimately be large but you only want recall-by-topic later — pass an ` + "`" + `intent` + "`" + ` string; outputs over ~5KB are auto-indexed into the knowledge base and only the section titles + previews come back, retrievable via ctx_search

WHEN NOT:
  - Single observational command whose entire short output you intend to consume verbatim (whoami, pwd, git status on a clean tree) — Bash is simpler
  - File mutations (Edit/Write) or navigation (cd/ls) — Bash is the right surface
  - You already know the output is one short fixed line and you want to read it as-is

RETURNS:
  Only what your code prints. Wrap risky calls in try/catch — uncaught errors go to stderr and may leak more than intended. When ` + "`" + `intent` + "`" + ` is set and output exceeds the auto-index threshold, the response carries searchable section titles + previews instead of the raw stdout; use ctx_search(queries: [...]) to drill into specific sections.

EXAMPLE: ctx_execute(language: "javascript", code: "const out = require('child_process').execSync('npm test', {encoding:'utf8', stdio:['ignore','pipe','pipe']}); console.log(out.split('\\n').filter(l => /(FAIL|✗|×|Error:|Tests +.*(failed|passed))/i.test(l)).slice(0, 60).join('\\n'))")
EXAMPLE: ctx_execute(language: "javascript", code: "const out = require('child_process').execSync('gh issue list --json number,title --limit 100', {encoding:'utf8'}); const hooks = JSON.parse(out).filter(i => /hook|routing/i.test(i.title)); console.log(` + "`" + `${hooks.length} hook-related issues` + "`" + `)")`

const executeSchema = `{
	"type": "object",
	"properties": {
		"language": {
			"type": "string",
			"enum": ["javascript", "typescript", "python", "shell", "ruby", "go", "rust", "php", "perl", "r", "elixir", "csharp"],
			"description": "Runtime language"
		},
		"code": {
			"type": "string",
			"description": "Source code to execute. Use console.log (JS/TS), print (Python/Ruby/Perl/R), echo (Shell), echo (PHP), fmt.Println (Go), IO.puts (Elixir), or Console.WriteLine (C#) to output a summary to context."
		},
		"timeout": {
			"type": ["number", "string"],
			"description": "Max execution time in ms. When omitted, no server-side timer fires — the MCP host's RPC timeout governs (which is the right layer for this policy). Pass an explicit value for long-running builds (Gradle/Maven/SBT)."
		},
		"background": {
			"type": ["boolean", "string"],
			"default": false,
			"description": "Keep process running after timeout (for servers/daemons). Returns partial output without killing the process. IMPORTANT: Do NOT add setTimeout/self-close timers in background scripts — the process must stay alive until the timeout detaches it. For server+fetch patterns, prefer putting both server and fetch in ONE ctx_execute call instead of using background."
		},
		"cwd": {
			"type": "string",
			"description": "Optional working directory for shell commands. Non-shell languages still execute from their sandbox temp directory."
		},
		"intent": {
			"type": "string",
			"description": "What you're looking for in the output. When provided and output is large (>5KB), indexes output into knowledge base and returns section titles + previews — not full content. Use ctx_search(queries: [...]) to retrieve specific sections. Example: 'failing tests', 'HTTP 500 errors'.\n\nTIP: Use specific technical terms, not just concepts. Check 'Searchable terms' in the response for available vocabulary."
		}
	},
	"required": ["language", "code"]
}`
```

`internal/mcpserver/executefile.go`:
```go
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerExecuteFile registers ctx_execute_file. Port of S:2042-2230.
func registerExecuteFile(s *mcp.Server, d *Deps) {
	s.AddTool(&mcp.Tool{
		Name:        "ctx_execute_file",
		Title:       "Run code over a file (executes code, reads the given path)",
		Description: executeFileDesc,
		Annotations: execAnnotations,
		InputSchema: json.RawMessage(executeFileSchema),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (res *mcp.CallToolResult, _ error) {
		var raw int64
		defer func() { d.Stats.Record(d, "ctx_execute_file", raw, res) }()
		a, err := ParseArgs(req.Params.Arguments)
		if err != nil {
			return ArgError(err), nil
		}
		path, ok, err := a.String("path")
		if err == nil && !ok {
			err = errors.New("path: required")
		} else if err == nil && path == "" {
			err = errors.New("path: must not be empty")
		}
		if err != nil {
			return ArgError(err), nil
		}
		e, err := parseExecArgs(a)
		if err != nil {
			return ArgError(err), nil
		}
		// Project boundary, then Read() deny, then the code deny check (S:2118-2132).
		if err := d.Policy.CheckReadPath(path); err != nil {
			return TextResult(err.Error(), true), nil
		}
		if denied := denyCode(d, e); denied != nil {
			return denied, nil
		}

		r, err := d.Exec.Run(ctx, executor.Request{Language: e.lang, Code: e.code, Timeout: e.timeout, FilePath: path})
		if err != nil {
			return ErrorResult("Runtime error: %v", err), nil
		}
		raw = fileSize(d.Cfg.ProjectDir, path) + int64(len(r.Stdout)+len(r.Stderr))
		echo := buildExecuteEcho(string(e.lang), e.code, path)
		if r.TimedOut {
			return TextResult(echo+"Timed out processing "+path+" after "+e.msText()+"ms", true), nil
		}
		return finishRun(d, r, e, echo, "file:"+path), nil
	})
}

// fileSize is the size of the file ctx_execute_file read, resolved against
// the project as executor.Run resolves it; 0 when it cannot be read.
func fileSize(projectDir, path string) int64 {
	if !filepath.IsAbs(path) {
		path = filepath.Join(projectDir, path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// Description and schema below are generated from server.ts S:2056-2112.

const executeFileDesc = `Read a file into a sandboxed FILE_CONTENT variable and run code over it. Only what you console.log() enters your conversation — the file bytes stay in the sandbox.

Think-in-Code applied to file-level analysis: Reading the whole file means every byte enters your conversation memory and costs reasoning capacity for the rest of the session. Running code over it here lets you keep the raw bytes out and only the derived answer in. Same principle as ctx_execute, scoped to one named file via the FILE_CONTENT variable.

WHEN:
  - You want to KNOW SOMETHING ABOUT a file (line count, matches of a pattern, parsed structure, statistical aggregate) without needing to SEE all of it
  - The file is structured (CSV, JSON, log, code) and a code-level derivation is cheaper than reading verbatim
  - The file is large enough that reading the full content would burn meaningful conversation memory you need for the actual work
  - The derivation may itself produce a large output you want recall-by-topic on later — pass an ` + "`" + `intent` + "`" + ` string; outputs over ~5KB are auto-indexed and only matching sections come back, retrievable via ctx_search

WHEN NOT:
  - You intend to EDIT the file — use Read so the subsequent Edit can match the exact text
  - You only need one specific line and you know its offset — Read with offset/limit is the simplest path
  - The file is small AND you will consume all of it for understanding/editing — Read directly

RETURNS:
  Only what your code prints. The FILE_CONTENT variable holds the raw bytes inside the sandbox; nothing else leaves. When ` + "`" + `intent` + "`" + ` is set and output exceeds the auto-index threshold, the response carries searchable section titles + previews instead of the raw stdout.

EXAMPLE: ctx_execute_file(path: "huge.log", language: "javascript", code: "const errs = FILE_CONTENT.split('\\n').filter(l => /ERROR|FATAL/.test(l)); console.log(` + "`" + `${errs.length} error lines` + "`" + `); console.log(errs.slice(-5).join('\\n'))")
EXAMPLE: ctx_execute_file(path: "data.csv", language: "javascript", code: "const rows = FILE_CONTENT.split('\\n'); console.log(` + "`" + `rows: ${rows.length - 1}, header: ${rows[0]}` + "`" + `)")`

const executeFileSchema = `{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "Absolute file path or relative to project root"
		},
		"language": {
			"type": "string",
			"enum": ["javascript", "typescript", "python", "shell", "ruby", "go", "rust", "php", "perl", "r", "elixir", "csharp"],
			"description": "Runtime language"
		},
		"code": {
			"type": "string",
			"description": "Code to process FILE_CONTENT (file_content in Elixir). Print summary via console.log/print/echo/IO.puts/Console.WriteLine."
		},
		"timeout": {
			"type": ["number", "string"],
			"description": "Max execution time in ms. When omitted, no server-side timer fires — the MCP host's RPC timeout governs."
		},
		"intent": {
			"type": "string",
			"description": "What you're looking for in the output. When provided and output is large (>5KB), returns only matching sections via BM25 search instead of truncated output."
		}
	},
	"required": ["path", "language", "code"]
}`
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcpserver/ -run 'TestStats' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver/server.go internal/mcpserver/stats.go internal/mcpserver/execute.go internal/mcpserver/executefile.go internal/mcpserver/stats_test.go
git commit -m "feat(mcpserver): record ctx_execute and ctx_execute_file stats"
```

### Task 12: Batch, fetch, search and purge stats; purge message

**Files:**
- Modify: `internal/mcpserver/batch.go`
- Modify: `internal/mcpserver/fetchindex.go`
- Modify: `internal/mcpserver/search.go`
- Modify: `internal/mcpserver/purge.go`
- Test: `internal/mcpserver/stats_test.go`
- Test: `internal/mcpserver/purge_test.go`

**Interfaces:**
- Consumes: `StatsRecorder.Record` from Task 11; `store.SourceBytes` from Task 8
- Produces: unexported `func batchExecute(ctx context.Context, d *Deps, args json.RawMessage) (*mcp.CallToolResult, int64)`; `func runBatchCommands(ctx context.Context, ex *executor.Executor, b batchArgs) (outputs []string, raw int64, err error)`; `func handleFetchAndIndex(ctx context.Context, d *Deps, args json.RawMessage) (*mcp.CallToolResult, int64)`; `fetchJob.raw int64`; ctx_purge project message `Purged: knowledge base (FTS5), session events, session stats. All session data for this project has been permanently deleted.`

- [ ] **Step 1: Write the failing test**

`internal/mcpserver/stats_test.go`:
```go
package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

// recordStats turns stats on for h, as serve does, and returns a reader of
// one tool's row for this server.
func (h *harness) recordStats() func(tool string) store.ToolStat {
	h.t.Helper()
	h.Deps.Stats = &StatsRecorder{ServerID: "srv-test", CCPID: 0, StartedAt: "2026-10-21 08:00:00"}
	return func(tool string) store.ToolStat {
		h.t.Helper()
		snap, err := h.Deps.Store.Stats(store.StatsQuery{ServerID: "srv-test", Project: h.Deps.Cfg.ProjectKey})
		if err != nil {
			h.t.Fatal(err)
		}
		for _, ts := range snap.Tools {
			if ts.Tool == tool {
				return ts
			}
		}
		return store.ToolStat{Tool: tool}
	}
}

// args is a JSON object literal for h.call.
func args(kv ...any) string {
	m := map[string]any{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func TestStatsOffRecordsNothing(t *testing.T) {
	h := newHarness(t, nil)
	h.call("ctx_execute", args("language", "shell", "code", "echo hi"))
	snap, err := h.Deps.Store.Stats(store.StatsQuery{Project: h.Deps.Cfg.ProjectKey})
	if err != nil || len(snap.Tools) != 0 || snap.LifetimeSince != "" {
		t.Fatalf("rows recorded with Stats nil: %+v %v", snap, err)
	}
}

// ctx_execute: raw is the run's stdout + stderr bytes (stats spec §1.3).
func TestStatsExecuteKept(t *testing.T) {
	h := newHarness(t, nil)
	row := h.recordStats()

	small, _ := h.call("ctx_execute", args("language", "shell", "code", "printf abc; printf de >&2"))
	// 5 raw bytes, more returned (the code echo): nothing kept.
	if got, want := row("ctx_execute"), (store.ToolStat{Tool: "ctx_execute", Calls: 1, BytesReturned: int64(len(small))}); got != want {
		t.Fatalf("small run: %+v, want %+v", got, want)
	}

	// Over 5 KB with an intent: indexed, and only matching sections come back.
	big, _ := h.call("ctx_execute", args("language", "shell", "code", `head -c 100000 /dev/zero | tr '\0' a`, "intent", "letters"))
	got := row("ctx_execute")
	want := store.ToolStat{Tool: "ctx_execute", Calls: 2, BytesReturned: int64(len(small) + len(big)), BytesKept: int64(100000 - len(big))}
	if got != want || len(big) > 10000 {
		t.Fatalf("indexed run: %+v, want %+v (returned %d bytes)", got, want, len(big))
	}

	// An argument error is a call that processed nothing.
	bad, isErr := h.call("ctx_execute", args("language", "cobol", "code", "x"))
	want.Calls, want.BytesReturned = 3, want.BytesReturned+int64(len(bad))
	if got := row("ctx_execute"); !isErr || got != want {
		t.Fatalf("arg error: %+v, want %+v", got, want)
	}
}

// ctx_execute_file: raw is the file size + stdout + stderr bytes.
func TestStatsExecuteFileKept(t *testing.T) {
	h := newHarness(t, nil)
	row := h.recordStats()
	h.writeProjectFile("data.txt", strings.Repeat("x", 3000))
	code := `printf %s "${#FILE_CONTENT}"`
	text, isErr := h.call("ctx_execute_file", args("path", "data.txt", "language", "shell", "code", code))
	stdout, ok := strings.CutPrefix(text, buildExecuteEcho("shell", code, "data.txt"))
	if isErr || !ok || stdout != "3000" {
		t.Fatalf("run: %v %q", isErr, text)
	}
	want := store.ToolStat{Tool: "ctx_execute_file", Calls: 1, BytesReturned: int64(len(text)), BytesKept: int64(3000 + len(stdout) - len(text))}
	if got := row("ctx_execute_file"); got != want {
		t.Fatalf("%+v, want %+v", got, want)
	}
}

// ctx_batch_execute: raw is the sum of every command's stdout + stderr
// bytes, in serial and in parallel mode.
func TestStatsBatchKept(t *testing.T) {
	for _, concurrency := range []int{1, 2} {
		h := newHarness(t, nil)
		row := h.recordStats()
		text, isErr := h.call("ctx_batch_execute", args(
			"commands", []map[string]string{
				{"label": "letters", "command": `head -c 50000 /dev/zero | tr '\0' b`},
				{"label": "errs", "command": "printf xy >&2"},
			},
			"queries", []string{"letters"}, "concurrency", concurrency))
		want := store.ToolStat{Tool: "ctx_batch_execute", Calls: 1, BytesReturned: int64(len(text)), BytesKept: int64(50000 + 2 - len(text))}
		if got := row("ctx_batch_execute"); isErr || got != want || len(text) > 20000 {
			t.Fatalf("concurrency %d: %+v, want %+v (isErr %v, returned %d)", concurrency, got, want, isErr, len(text))
		}
	}
}

// ctx_fetch_and_index: raw is the fetched body bytes, or on a cache hit the
// cached source's content bytes.
func TestStatsFetchKept(t *testing.T) {
	body := strings.Repeat("zebrafish line of text\n", 2000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	h := newHarness(t, nil)
	row := h.recordStats()

	first, isErr := h.call("ctx_fetch_and_index", args("url", srv.URL+"/a", "source", "big"))
	raw := int64(len(strings.TrimSpace(body)))
	want := store.ToolStat{Tool: "ctx_fetch_and_index", Calls: 1, BytesReturned: int64(len(first)), BytesKept: raw - int64(len(first))}
	if got := row("ctx_fetch_and_index"); isErr || got != want {
		t.Fatalf("fetch: %+v, want %+v (isErr %v)", got, want, isErr)
	}

	cached, isErr := h.call("ctx_fetch_and_index", args("url", srv.URL+"/a", "source", "big"))
	src, err := h.Deps.Store.SourceByLabel(h.Deps.Cfg.ProjectKey, fetchCacheKey("big", true, srv.URL+"/a"))
	if err != nil || src == nil {
		t.Fatal(src, err)
	}
	content, err := h.Deps.Store.SourceBytes(src.ID)
	if err != nil || content < 40000 {
		t.Fatalf("SourceBytes = %d, %v", content, err)
	}
	want.Calls, want.BytesReturned = 2, want.BytesReturned+int64(len(cached))
	want.BytesKept += content - int64(len(cached))
	if got := row("ctx_fetch_and_index"); isErr || !strings.Contains(cached, "cached") || got != want {
		t.Fatalf("cache hit: %+v, want %+v\n%s", got, want, cached)
	}
}

// ctx_search and ctx_purge have no raw size: they only return bytes.
func TestStatsSearchAndPurgeReturnOnly(t *testing.T) {
	h := newHarness(t, nil)
	row := h.recordStats()
	h.index("notes", store.Chunk{Title: "Alpha", Content: strings.Repeat("gizmo text ", 500)})
	found, _ := h.call("ctx_search", args("queries", []string{"gizmo"}))
	if got, want := row("ctx_search"), (store.ToolStat{Tool: "ctx_search", Calls: 1, BytesReturned: int64(len(found))}); got != want {
		t.Fatalf("search: %+v, want %+v", got, want)
	}
	cancelled, _ := h.call("ctx_purge", `{}`)
	if got, want := row("ctx_purge"), (store.ToolStat{Tool: "ctx_purge", Calls: 1, BytesReturned: int64(len(cancelled))}); got != want {
		t.Fatalf("purge: %+v, want %+v", got, want)
	}
}
```

`internal/mcpserver/purge_test.go`:
```go
package mcpserver

import (
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

const (
	purgeCancelled = "Purge cancelled. Pass confirm: true to proceed."
	purgeAmbiguous = "Ambiguous purge: sessionId implies scope:'session', cannot combine with scope:'project'. " +
		"Use scope:'project' WITHOUT sessionId for the legacy whole-project wipe."
	purgeProjectMsg = "Purged: knowledge base (FTS5), session events, session stats. All session data for this project has been permanently deleted."
)

func TestPurgeArgumentChecks(t *testing.T) {
	h := newHarness(t, nil)
	h.events("s1", "", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "kept"})
	cases := []struct {
		args, want string
		isErr      bool
	}{
		{`{}`, purgeCancelled, false},
		{`{"confirm": false}`, purgeCancelled, false},
		{`{"confirm": "false", "sessionId": "s1"}`, purgeCancelled, false},
		// The ambiguity check runs before the confirm check (S:4510).
		{`{"confirm": false, "sessionId": "s1", "scope": "project"}`, purgeAmbiguous, true},
		{`{"confirm": true, "scope": "session"}`, "purgeSession: scope:'session' requires sessionId. Pass scope:'project' for the legacy whole-project wipe.", true},
		{`{"confirm": "yes"}`, "Invalid arguments: confirm: expected boolean", true},
		{`{"confirm": true, "scope": "all"}`, `Invalid arguments: scope: expected "session" or "project"`, true},
	}
	for _, c := range cases {
		if out, isErr := h.call("ctx_purge", c.args); out != c.want || isErr != c.isErr {
			t.Errorf("%s: isErr=%v %q", c.args, isErr, out)
		}
	}
	if evs, _ := h.Deps.Store.SessionEvents("s1"); len(evs) != 1 {
		t.Fatalf("events were deleted: %d left", len(evs))
	}
}

func TestPurgeSessionRoundTrip(t *testing.T) {
	h := newHarness(t, nil)
	h.events("s1", "", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "gizmo one"})
	h.events("s2", "", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "gizmo two"})
	h.index("notes", store.Chunk{Title: "Alpha", Content: "gizmo text"})

	if out, isErr := h.call("ctx_purge", `{"confirm": true, "sessionId": "s1"}`); isErr ||
		out != "Purged session s1: session events. Other sessions and project-wide stats preserved." {
		t.Fatalf("isErr=%v %q", isErr, out)
	}
	if out, _ := h.call("ctx_purge", `{"confirm": "true", "scope": "session", "sessionId": "s1"}`); out != "Purged session s1: no matching rows. Other sessions and project-wide stats preserved." {
		t.Fatalf("second purge: %q", out)
	}
	out, _ := h.call("ctx_search", `{"queries": ["gizmo"], "sort": "timeline"}`)
	if strings.Contains(out, "gizmo one") || !strings.Contains(out, "gizmo two") || !strings.Contains(out, "gizmo text") {
		t.Fatalf("after session purge:\n%s", out)
	}
}

func TestPurgeProjectRoundTrip(t *testing.T) {
	for _, args := range []string{`{"confirm": true}`, `{"confirm": true, "scope": "project"}`} {
		h := newHarness(t, nil)
		other := "/other/project"
		h.index("notes", store.Chunk{Title: "Alpha", Content: "gizmo text"})
		h.events("s1", "", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "gizmo one"})
		if _, err := h.Deps.Store.Index(other, "notes", []store.Chunk{{Title: "B", Content: "gizmo elsewhere"}}); err != nil {
			t.Fatal(err)
		}
		h.events("s9", other, store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "gizmo foreign"})

		if out, isErr := h.call("ctx_purge", args); isErr || out != purgeProjectMsg {
			t.Fatalf("%s: isErr=%v %q", args, isErr, out)
		}
		if out, isErr := h.call("ctx_search", `{"queries": ["gizmo"]}`); !isErr || out != emptyKBMessage {
			t.Fatalf("%s: KB not empty: %q", args, out)
		}
		if out, _ := h.call("ctx_search", `{"queries": ["gizmo"], "sort": "timeline"}`); !strings.HasPrefix(out, "## gizmo\nNo results found.") {
			t.Fatalf("%s: timeline after purge:\n%s", args, out)
		}
		if n, _ := h.Deps.Store.ChunkCount(other); n != 1 {
			t.Fatalf("%s: other project chunks = %d", args, n)
		}
		if evs, _ := h.Deps.Store.SessionEvents("s9"); len(evs) != 1 {
			t.Fatalf("%s: other project events = %d", args, len(evs))
		}
	}
}

// A purge that matched nothing must not claim a permanent deletion.
func TestPurgeProjectNothingToPurge(t *testing.T) {
	h := newHarness(t, nil)
	want := "Nothing to purge for this project (" + h.Deps.Cfg.ProjectKey + ")."
	if out, isErr := h.call("ctx_purge", `{"confirm": true}`); isErr || out != want {
		t.Fatalf("isErr=%v %q, want %q", isErr, out, want)
	}
	// Only another project has data: still nothing for this one.
	if _, err := h.Deps.Store.Index("/other/project", "notes", []store.Chunk{{Title: "B", Content: "elsewhere"}}); err != nil {
		t.Fatal(err)
	}
	if out, _ := h.call("ctx_purge", `{"confirm": true, "scope": "project"}`); out != want {
		t.Fatalf("other project only: %q", out)
	}
	// Data present: the old message.
	h.index("notes", store.Chunk{Title: "Alpha", Content: "gizmo text"})
	if out, _ := h.call("ctx_purge", `{"confirm": true}`); out != purgeProjectMsg {
		t.Fatalf("with data: %q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcpserver/ -run 'TestStats|TestPurge' -v`
Expected: FAIL with "purge_test.go:74: {"confirm": true}: isErr=false "Purged: knowledge base (FTS5), session events. All session data for this project has been permanently deleted.""

- [ ] **Step 3: Write minimal implementation**

`internal/mcpserver/batch.go`:
```go
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	commandEchoMax   = 500       // S:1477
	batchSnippetMax  = 3000      // S:1398
	batchOutputCap   = 80 * 1024 // S:1375
	batchSearchLimit = 3         // S:1393
	batchTermsMax    = 40        // ST:1573 getDistinctiveTerms default
)

const batchDescription = "Run multiple commands in ONE call. Every command's output is auto-indexed into the knowledge base; if you also pass `queries`, the matching sections come back in the same round trip so a follow-up search call is not needed.\n" +
	"\n" +
	"Concurrency parallelizes the FETCH phase (run-the-commands). The DERIVATION phase — turning raw output into an answer — still belongs in code: add a processing command that consumes the indexed output and prints only the answer, so the raw bytes never enter your conversation (Think-in-Code, same principle as the sandbox tool).\n" +
	"\n" +
	"WHEN:\n" +
	"  - You have 3+ related commands you would otherwise run sequentially (multi-issue lookups, git log + git diff + git blame, multi-file reads, multi-region cloud queries)\n" +
	"  - You want to gather AND query in one round trip — pass `queries` so the matching sections come back inline\n" +
	"  - You want to parallelize I/O-bound work — pass `concurrency` 2-8 (network calls, gh CLI, cloud APIs, multi-repo git reads)\n" +
	"  - The combined output is large enough that piping it through ctx_search later would itself be expensive — let auto-index + inline queries do both in one shot\n" +
	"\n" +
	"WHEN NOT:\n" +
	"  - Single command with no follow-up query — run it in the sandbox tool directly\n" +
	"  - CPU-bound or stateful commands — keep concurrency at 1 (npm test, build, lint, port-binding servers, lock-file holders, anything that races on the same resource)\n" +
	"\n" +
	"RETURNS:\n" +
	"  Auto-indexed section list per command label, plus top matches per query (when `queries` is passed). Raw output is NOT echoed in full — only the matched windows. Concurrency>1 switches each command to its own per-command timeout (no shared budget); concurrency=1 preserves the legacy shared-budget cascading-skip-on-timeout path. Use 4-8 for I/O-bound batches; keep at 1 for CPU work or shared-state commands; lower the value when target hosts enforce per-IP rate limits.\n" +
	"\n" +
	"EXAMPLE: ctx_batch_execute(\n" +
	"  commands: [\n" +
	"    {label: \"issue 1\", command: \"gh issue view 1\"},\n" +
	"    {label: \"issue 2\", command: \"gh issue view 2\"},\n" +
	"    {label: \"summarize\", command: \"echo done\"}\n" +
	"  ],\n" +
	"  queries: [\"root cause\", \"proposed fix\"],\n" +
	"  concurrency: 2\n" +
	")"

// batchSchema mirrors the zod schema at S:3715-3776; coerced fields also accept strings.
const batchSchema = `{
  "type": "object",
  "properties": {
    "commands": {
      "type": ["array", "string"],
      "minItems": 1,
      "items": {
        "type": "object",
        "properties": {
          "label": {"type": "string", "description": "Section header for this command's output (e.g., 'README', 'Package.json', 'Source Tree')"},
          "command": {"type": "string", "description": "Shell command to execute"}
        },
        "required": ["label", "command"]
      },
      "description": "Commands to execute as a batch. Output is labeled with the section header. Default order is sequential; pass concurrency>1 to run in parallel (output stays in input order)."
    },
    "queries": {
      "type": ["array", "string"],
      "minItems": 1,
      "items": {"type": "string"},
      "description": "Search queries to extract information from indexed output. Use 5-8 comprehensive queries. Each returns top 5 matching sections with full content. This is your ONLY chance — put ALL your questions here. No follow-up calls needed."
    },
    "timeout": {
      "type": ["number", "string"],
      "description": "Max execution time in ms. When omitted, no server-side timer fires — the MCP host's RPC timeout governs. With concurrency=1, the value (when set) is a shared budget across commands; with concurrency>1, it is applied per-command."
    },
    "concurrency": {
      "type": ["integer", "string"],
      "minimum": 1,
      "maximum": 8,
      "default": 1,
      "description": "Max commands to run in parallel (1-8, default: 1). Use 4-8 for I/O-bound batches (network, gh, curl, multi-repo git reads). Keep at 1 for CPU-bound (npm test, build, lint) or stateful commands (ports, locks). >1 switches to per-command timeouts (no shared budget) and individual ` + "`(timed out)`" + ` blocks instead of cascading skip."
    },
    "cwd": {"type": "string", "description": "Optional working directory for all shell commands in this batch."},
    "query_scope": {
      "type": "string",
      "enum": ["batch", "global"],
      "default": "batch",
      "description": "Scope for ` + "`queries`" + ` (default: ` + "`batch`" + `). ` + "`batch`" + ` searches ONLY the chunks produced by this batch's commands — useful when you want answers about the just-fetched output. ` + "`global`" + ` searches the entire persistent index (same scope as ctx_search) — useful when you want the batch commands to enrich context and the queries to also surface related prior knowledge in one round trip."
    }
  },
  "required": ["commands", "queries"]
}`

func registerBatch(s *mcp.Server, d *Deps) {
	t := true
	s.AddTool(&mcp.Tool{
		Name:        "ctx_batch_execute",
		Title:       "Batch Execute & Search",
		Description: batchDescription,
		InputSchema: json.RawMessage(batchSchema),
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &t, OpenWorldHint: &t},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, raw := batchExecute(ctx, d, req.Params.Arguments)
		d.Stats.Record(d, "ctx_batch_execute", raw, res)
		return res, nil
	})
}

// batchArgs is the validated ctx_batch_execute input.
type batchArgs struct {
	commands    []Command
	queries     []string
	timeoutMs   float64
	hasTimeout  bool
	concurrency int
	cwd         string
	scope       string
}

func parseBatchArgs(raw json.RawMessage) (batchArgs, error) {
	var b batchArgs
	a, err := ParseArgs(raw)
	if err != nil {
		return b, err
	}
	var ok bool
	if b.commands, ok, err = a.Commands("commands"); err != nil {
		return b, err
	} else if !ok || len(b.commands) == 0 {
		return b, errors.New("commands: expected a non-empty array")
	}
	if b.queries, ok, err = a.Strings("queries"); err != nil {
		return b, err
	} else if !ok || len(b.queries) == 0 {
		return b, errors.New("queries: expected a non-empty array")
	}
	if b.timeoutMs, b.hasTimeout, err = a.Number("timeout"); err != nil {
		return b, err
	}
	b.concurrency = 1
	if c, ok, err := a.Number("concurrency"); err != nil {
		return b, err
	} else if ok {
		if c != math.Trunc(c) || c < 1 || c > 8 {
			return b, errors.New("concurrency: expected an integer from 1 to 8")
		}
		b.concurrency = int(c)
	}
	if b.cwd, _, err = a.String("cwd"); err != nil {
		return b, err
	}
	b.scope = "batch"
	if sc, ok, err := a.String("query_scope"); err != nil {
		return b, err
	} else if ok {
		if sc != "batch" && sc != "global" {
			return b, errors.New(`query_scope: expected "batch" or "global"`)
		}
		b.scope = sc
	}
	return b, nil
}

// batchExecute is the ctx_batch_execute handler (S:3778-3892). It also
// returns the commands' output bytes, the call's raw size (stats spec §1.3).
func batchExecute(ctx context.Context, d *Deps, args json.RawMessage) (*mcp.CallToolResult, int64) {
	b, err := parseBatchArgs(args)
	if err != nil {
		return ArgError(err), 0
	}
	for _, c := range b.commands {
		if blocked, pattern := d.Policy.CheckCommand(c.Command); blocked {
			return TextResult(security.BlockedMessage(pattern), true), 0
		}
	}

	outputs, raw, err := runBatchCommands(ctx, d.Exec, b)
	if err != nil {
		return ErrorResult("Batch execution error: %v", err), raw
	}
	stdout := strings.Join(outputs, "\n")
	totalLines := strings.Count(stdout, "\n") + 1

	labels := make([]string, len(b.commands))
	for i, c := range b.commands {
		labels[i] = c.Label
	}
	source := "batch:" + jsPrefix(strings.Join(labels, ","), 80)
	indexed, err := d.Store.Index(d.Cfg.ProjectKey, source, store.ChunkMarkdown(stdout, store.ChunkMaxBytes))
	if err != nil {
		return ErrorResult("Batch execution error: %v", err), raw
	}

	lines := []string{
		fmt.Sprintf("Executed %d commands (%d lines, %s). Indexed %d sections. Searched %d queries.",
			len(b.commands), totalLines, KB(len(stdout)), indexed.TotalChunks, len(b.queries)),
		"",
		"## Commands",
		"",
	}
	for _, c := range b.commands {
		lines = append(lines, fmt.Sprintf("- %s: `%s`", c.Label, truncateCommandForEcho(c.Command)))
	}
	lines = append(lines, "", "## Indexed Sections", "")
	for _, s := range indexed.Sections {
		lines = append(lines, fmt.Sprintf("- %s (%s)", s.Title, KB(s.Bytes)))
	}
	lines = append(lines, "")
	qr, err := formatBatchQueryResults(d.Store, d.Cfg.ProjectKey, b.queries, source, batchOutputCap, b.scope)
	if err != nil {
		return ErrorResult("Batch execution error: %v", err), raw
	}
	lines = append(lines, qr...)
	terms, err := d.Store.DistinctiveTerms(indexed.SourceID, batchTermsMax)
	if err != nil {
		return ErrorResult("Batch execution error: %v", err), raw
	}
	if len(terms) > 0 {
		lines = append(lines, "\nSearchable terms for follow-up: "+strings.Join(terms, ", "))
	} else {
		lines = append(lines, "")
	}
	// TS S:3810 also has a "Batch timed out after Nms. No output captured."
	// branch; it needs zero outputs, which commands.min(1) makes unreachable.
	return TextResult(strings.Join(lines, "\n"), false), raw
}

// runBatchCommands runs every command as shell (S:1560-1641). Serial mode
// shares one timeout budget and skips what is left once it runs out; an
// executor error aborts the batch. Parallel mode gives each command the full
// timeout and turns an executor error into that command's section. raw is
// the sum of the commands' stdout + stderr bytes.
func runBatchCommands(ctx context.Context, ex *executor.Executor, b batchArgs) (outputs []string, raw int64, err error) {
	run := func(c Command, timeout time.Duration) (executor.Result, error) {
		return ex.Run(ctx, executor.Request{Language: executor.Shell, Code: c.Command, Timeout: timeout, Cwd: b.cwd})
	}
	budget := time.Duration(b.timeoutMs * float64(time.Millisecond))

	if b.concurrency <= 1 {
		outputs = make([]string, 0, len(b.commands))
		start := time.Now()
		for i, c := range b.commands {
			var perCmd time.Duration
			if b.hasTimeout {
				remaining := budget - time.Since(start)
				if remaining <= 0 {
					outputs = append(outputs, skippedSection(c.Label))
					continue
				}
				perCmd = remaining
			}
			res, err := run(c, perCmd)
			if err != nil {
				return nil, raw, err
			}
			raw += int64(len(res.Stdout) + len(res.Stderr))
			outputs = append(outputs, formatCommandOutput(c.Label, c.Command, combineExecOutput(res)))
			if res.TimedOut {
				for _, rest := range b.commands[i+1:] {
					outputs = append(outputs, skippedSection(rest.Label))
				}
				break
			}
		}
		return outputs, raw, nil
	}

	var perCmd time.Duration
	timeoutText := "?"
	if b.hasTimeout {
		perCmd = budget
		timeoutText = strconv.FormatFloat(b.timeoutMs, 'f', -1, 64)
	}
	outputs = make([]string, len(b.commands))
	sizes := make([]int, len(b.commands))
	sem := make(chan struct{}, b.concurrency)
	var wg sync.WaitGroup
	for i, c := range b.commands {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			res, err := run(c, perCmd)
			if err != nil {
				outputs[i] = fmt.Sprintf("# %s\n\n(executor error: %v)\n", c.Label, err)
				return
			}
			sizes[i] = len(res.Stdout) + len(res.Stderr)
			out := formatCommandOutput(c.Label, c.Command, combineExecOutput(res))
			if res.TimedOut {
				out = strings.TrimSuffix(out, "\n") + "\n(timed out after " + timeoutText + "ms)\n"
			}
			outputs[i] = out
		}()
	}
	wg.Wait()
	for _, n := range sizes {
		raw += int64(n)
	}
	return outputs, raw, nil
}

func skippedSection(label string) string {
	return "# " + label + "\n\n(skipped — batch timeout exceeded)\n"
}

// formatCommandOutput is S:1529-1543 without the __CM_FS__ byte counter.
func formatCommandOutput(label, command, raw string) string {
	if raw == "" {
		raw = "(no output)"
	}
	return "# " + label + "\n\n$ " + truncateCommandForEcho(command) + "\n\n" + raw + "\n"
}

// combineExecOutput is S:1545-1551.
func combineExecOutput(r executor.Result) string {
	if r.Stderr == "" {
		return r.Stdout
	}
	if r.Stdout == "" {
		return r.Stderr
	}
	sep := "\n"
	if strings.HasSuffix(r.Stdout, "\n") {
		sep = ""
	}
	return r.Stdout + sep + r.Stderr
}

// truncateCommandForEcho is S:1479-1483: collapse whitespace, cap at 500.
func truncateCommandForEcho(command string) string {
	cleaned := strings.Join(strings.Fields(command), " ")
	if jsLen(cleaned) <= commandEchoMax {
		return cleaned
	}
	return jsPrefix(cleaned, commandEchoMax) + "…"
}

// formatBatchQueryResults is S:1371-1418. scope "batch" matches the batch
// source label exactly; "global" searches the whole project.
func formatBatchQueryResults(st *store.Store, project string, queries []string, source string, maxOutput int, scope string) ([]string, error) {
	var sections []string
	outputSize := 0
	opts := store.SearchOpts{Project: project, Limit: batchSearchLimit, Mode: "relevance", SourceExact: true}
	if scope != "global" {
		opts.Source = source
	}
	for _, q := range queries {
		if outputSize > maxOutput {
			sections = append(sections, fmt.Sprintf("## %s\n(output cap reached — use ctx_search(queries: [\"%s\"]) for details)\n", q, q))
			continue
		}
		results, err := st.SearchWithFallback(q, opts)
		if err != nil {
			return nil, err
		}
		sections = append(sections, "## "+q, "")
		if len(results) == 0 {
			sections = append(sections, "No matching sections found.", "")
			continue
		}
		for _, r := range results {
			snippet := store.ExtractSnippet(r.Content, q, batchSnippetMax, r.Highlighted)
			sections = append(sections, "### "+r.Title, snippet, "")
			outputSize += jsLen(snippet) + jsLen(r.Title)
		}
	}
	if scope == "global" {
		sections = append(sections, "\n> **Scope:** Queries searched the entire persistent index (query_scope: \"global\").")
	} else {
		sections = append(sections, "\n> **Tip:** Results are scoped to this batch only. To search across all indexed sources, use `ctx_search(queries: [...])` or call ctx_batch_execute with `query_scope: \"global\"`.")
	}
	return sections, nil
}

// jsLen is JS String.length (UTF-16 code units), used where TS sizes text.
func jsLen(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// jsPrefix is JS s.slice(0, n) in UTF-16 code units; a split surrogate pair
// is dropped rather than emitted as invalid UTF-8.
func jsPrefix(s string, n int) string {
	units := 0
	for i, r := range s {
		units += utf16.RuneLen(r)
		if units > n {
			return s[:i]
		}
	}
	return s
}
```

`internal/mcpserver/fetchindex.go`:
```go
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/lang315/context-mode-go/internal/fetch"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// S:3418-3445, minus the "inline index tool" WHEN NOT bullet (cmgo has no ctx_index).
const fetchDescription = "Fetches URL content, converts HTML to markdown (JSON is chunked by key paths, plain text indexed directly), persists it in a searchable knowledge base, and returns a small preview window per source. The raw page bytes never enter your conversation — they live in storage and you retrieve any section on-demand via ctx_search.\n\n" +
	"Caching: every fetch is cached on disk and reused for repeat calls within the TTL window. The default TTL is 24 hours; override per-call with the `ttl` parameter (milliseconds, `ttl: 0` bypasses cache like `force: true`). Stored content older than 14 days is cleaned up on startup.\n\n" +
	"WHEN:\n" +
	"  - You need web content (docs, changelogs, API references, spec pages) and the raw page bytes should NOT enter your conversation\n" +
	"  - Multi-URL research (library evaluation, migration scans, doc comparisons): pass the `requests` array and a `concurrency` value 2-8 for parallel I/O\n" +
	"  - You want repeat lookups against the same URL to be cheap (TTL cache hits return only a hint, no re-fetch)\n" +
	"  - You want a long-lived cache window (override `ttl` upward for stable specs) or a guaranteed-fresh fetch (`ttl: 0` or `force: true`)\n\n" +
	"WHEN NOT:\n" +
	"  - The page is SPA-rendered (JavaScript-required to materialize content) — this is a plain HTTP fetch, no headless browser\n\n" +
	"RETURNS:\n" +
	"  Per-source preview windows extracted around indexable headings plus indexing metadata (chunk counts, source labels, cache state). Raw content is NOT echoed back — retrieve any section on-demand via ctx_search(source: \"<label>\"). Concurrency parallelizes the fetch phase up to your chosen value (capped by the host's logical CPU count); the FTS5 write phase always runs serially because SQLite is a single-writer store. Net latency = max(fetch latency across the pool) + sum(per-source index write time). Cache hits skip both phases and return a small freshness hint instead of re-fetching. Use 4-8 for stable I/O-bound batches; lower the value when the target host enforces a per-IP rate limit you cannot raise.\n\n" +
	"EXAMPLE: ctx_fetch_and_index(\n" +
	"  requests: [{url: \"https://react.dev/...\", source: \"react\"}, {url: \"https://vuejs.org/...\", source: \"vue\"}],\n" +
	"  concurrency: 5\n" +
	")"

// S:3446-3498 (zod schema), with coerced fields widened to accept strings.
const fetchSchema = `{
  "type": "object",
  "properties": {
    "url": {"type": "string", "description": "Single URL to fetch and index (legacy single-shape)"},
    "source": {"type": "string", "description": "Label for the indexed content when using single ` + "`url`" + ` (e.g., 'React useEffect docs', 'Supabase Auth API'). For batch, put source in each requests entry."},
    "requests": {
      "type": ["array", "string"],
      "items": {
        "type": "object",
        "properties": {
          "url": {"type": "string", "description": "URL to fetch"},
          "source": {"type": "string", "description": "Label for this URL's indexed content"}
        },
        "required": ["url"]
      },
      "minItems": 1,
      "description": "Batch shape: array of {url, source?} entries. Use with concurrency>1 for parallel fetch. Each request indexed under its own source label. Output preserves input order."
    },
    "concurrency": {"type": ["integer", "string"], "minimum": 1, "maximum": 8, "default": 1, "description": "Max URLs to fetch in parallel (1-8, default: 1). Use 4-8 for I/O-bound multi-URL batches (library docs, changelogs, pricing pages). Capped by os.cpus().length on small machines (response notes when capped). Indexing is always serial regardless — only fetches race."},
    "force": {"type": ["boolean", "string"], "description": "Skip cache and re-fetch even if content was recently indexed"},
    "ttl": {"type": ["integer", "string"], "minimum": 0, "description": "Override the cache freshness window for this call, in milliseconds. ` + "`ttl: 0`" + ` bypasses the cache like ` + "`force: true`" + `; omit to use the default 24h TTL."}
  }
}`

type fetchRequest struct {
	url, source string
	hasSource   bool
}

// fetchJob is the parallel-safe half of one URL (fetchOneUrl, S:3288-3370):
// either a final outcome (cached, error) or a body still to index.
type fetchJob struct {
	out  fetchOutcome
	body fetch.Result
	raw  int64 // bytes kept out before formatting: the body, or a cache hit's content (stats spec §1.3)
}

func registerFetchAndIndex(s *mcp.Server, d *Deps) {
	no := false
	yes := true
	s.AddTool(&mcp.Tool{
		Name:        "ctx_fetch_and_index",
		Title:       "Fetch & Index URL(s)",
		Description: fetchDescription,
		InputSchema: json.RawMessage(fetchSchema),
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &no, OpenWorldHint: &yes},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, raw := handleFetchAndIndex(ctx, d, req.Params.Arguments)
		d.Stats.Record(d, "ctx_fetch_and_index", raw, res)
		return res, nil
	})
}

// handleFetchAndIndex is the tool handler, S:3499-3671. It also returns the
// call's raw size, the sum of every job's raw bytes.
func handleFetchAndIndex(ctx context.Context, d *Deps, args json.RawMessage) (*mcp.CallToolResult, int64) {
	a, err := ParseArgs(args)
	if err != nil {
		return ArgError(err), 0
	}
	batch, hasRequests, err := fetchRequests(a)
	if err != nil {
		return ArgError(err), 0
	}
	concurrency, err := fetchInt(a, "concurrency", 1, 1, 8)
	if err != nil {
		return ArgError(err), 0
	}
	ttl, err := fetchInt(a, "ttl", -1, 0, math.MaxInt64)
	if err != nil {
		return ArgError(err), 0
	}
	force, _, err := a.Bool("force")
	if err != nil {
		return ArgError(err), 0
	}
	if len(batch) == 0 {
		return TextResult("ctx_fetch_and_index requires either `url` (single) or `requests: [{url, source?}, ...]` (batch).", true), 0
	}

	single := !hasRequests && len(batch) == 1
	eff, capped := fetchPoolSize(int(concurrency), len(batch), !single && concurrency > 1)

	// Parallel fetch with eff workers; results land by index.
	jobs := make([]fetchJob, len(batch))
	sem := make(chan struct{}, eff)
	var wg sync.WaitGroup
	for i, r := range batch {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			jobs[i] = fetchOne(ctx, d, r, force, ttl)
		}()
	}
	wg.Wait()

	// Serial index drain in request order, S:3542-3570.
	outs := make([]fetchOutcome, len(jobs))
	var raw int64
	for i, j := range jobs {
		raw += j.raw
		if j.out.kind != "" {
			outs[i] = j.out
			continue
		}
		outs[i] = fetchIndex(d, batch[i], j.body)
	}

	var text string
	var isErr bool
	if single {
		text, isErr = fetchSingleText(outs[0])
	} else {
		text, isErr = fetchBatchText(outs, eff, capped)
	}
	return TextResult(text, isErr), raw
}

// fetchRequests normalizes {url, source} and requests[] (requests wins), S:3502-3506.
func fetchRequests(a Args) ([]fetchRequest, bool, error) {
	arr, hasRequests, err := a.Array("requests")
	if err != nil {
		return nil, false, err
	}
	if hasRequests {
		if len(arr) == 0 {
			return nil, true, errors.New("requests: expected at least 1 entry")
		}
		out := make([]fetchRequest, 0, len(arr))
		for i, e := range arr {
			m, ok := e.(map[string]any)
			if !ok {
				return nil, true, fmt.Errorf("requests[%d]: expected object", i)
			}
			ra := Args(m)
			u, hasURL, err := ra.String("url")
			if err != nil || !hasURL {
				return nil, true, fmt.Errorf("requests[%d].url: expected string", i)
			}
			src, hasSrc, err := ra.String("source")
			if err != nil {
				return nil, true, fmt.Errorf("requests[%d].%v", i, err)
			}
			out = append(out, fetchRequest{url: u, source: src, hasSource: hasSrc})
		}
		return out, true, nil
	}
	u, _, err := a.String("url")
	if err != nil {
		return nil, false, err
	}
	src, hasSrc, err := a.String("source")
	if err != nil {
		return nil, false, err
	}
	if u == "" {
		return nil, false, nil
	}
	return []fetchRequest{{url: u, source: src, hasSource: hasSrc}}, false, nil
}

// fetchInt mirrors z.coerce.number().int().min(lo).max(hi).optional(); def when absent.
func fetchInt(a Args, key string, def, lo, hi int64) (int64, error) {
	f, ok, err := a.Number(key)
	if err != nil || !ok {
		return def, err
	}
	if f != math.Trunc(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("%s: expected integer", key)
	}
	if f < float64(lo) || f > float64(hi) {
		return 0, fmt.Errorf("%s: must be between %d and %d", key, lo, hi)
	}
	return int64(f), nil
}

// fetchOne is the cache check plus fetch, S:3288-3370. It writes nothing.
// ttl < 0 means "not given".
func fetchOne(ctx context.Context, d *Deps, r fetchRequest, force bool, ttl int64) fetchJob {
	label := fetchCacheKey(r.source, r.hasSource, r.url)
	if !force && ttl != 0 {
		src, err := d.Store.SourceByLabel(d.Cfg.ProjectKey, label)
		if err == nil && src != nil {
			ttlMs := ttl
			if ttlMs < 0 {
				ttlMs = fetchTTLDefaultMs
			}
			if age := d.Now().Sub(src.IndexedAt).Milliseconds(); age < ttlMs {
				var raw int64
				if d.Stats != nil { // the size scans the chunks; only stats need it
					raw, _ = d.Store.SourceBytes(src.ID)
				}
				return fetchJob{out: fetchOutcome{kind: "cached", url: r.url, label: src.Label, chunks: src.ChunkCount, age: fetchAge(age), ttl: fetchTTL(ttlMs)}, raw: raw}
			}
		}
	}
	res, err := d.Fetch.Fetch(ctx, r.url)
	if err != nil {
		reason := "exit"
		if errors.Is(err, fetch.ErrEmpty) {
			reason = "empty"
		}
		return fetchJob{out: fetchOutcome{kind: "error", url: r.url, err: err.Error(), reason: reason}}
	}
	return fetchJob{body: res, raw: int64(len(res.Body))}
}

// fetchIndex is indexFetched, S:3378-3409: route by content kind, one store write.
func fetchIndex(d *Deps, r fetchRequest, res fetch.Result) fetchOutcome {
	var chunks []store.Chunk
	switch res.Kind {
	case fetch.KindJSON:
		var err error
		if chunks, err = store.ChunkJSON(res.Body, store.ChunkMaxBytes); err != nil || len(chunks) == 0 {
			chunks = store.ChunkPlainText(res.Body, 20)
		}
	case fetch.KindHTML:
		chunks = store.ChunkMarkdown(res.Body, store.ChunkMaxBytes)
	default:
		chunks = store.ChunkPlainText(res.Body, 20)
	}
	ir, err := d.Store.Index(d.Cfg.ProjectKey, fetchCacheKey(r.source, r.hasSource, r.url), chunks)
	if err != nil {
		return fetchOutcome{kind: "error", url: r.url, err: err.Error(), reason: "throw"}
	}
	return fetchOutcome{kind: "fetched", url: r.url, label: ir.Label, chunks: ir.TotalChunks, bytes: len(res.Body), preview: fetchPreview(res.Body)}
}
```

`internal/mcpserver/search.go`:
```go
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/memory"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	searchSnippetMax = 1500      // S:2750
	searchOutputCap  = 40 * 1024 // S:2672
)

// searchDescription is S:2564-2585 minus the ctx_stats, CONTEXT_MODE_* env
// and file-staleness sentences, the ctx_index tool (not in cmgo) and the
// "26 event categories" count (cmgo captures fewer).
const searchDescription = "Search a unified knowledge base with a multi-strategy ranking pipeline. Two parallel matchers run on every query: a Porter-stemming matcher (\"caching\" finds \"cached\", \"caches\", \"cach\") and a trigram-substring matcher (\"useEff\" finds \"useEffect\"). Their ranked lists are merged via Reciprocal Rank Fusion, so a document that ranks well in both surfaces above one that wins only on a single strategy. Multi-term queries get an additional proximity-rerank pass that boosts passages where the query terms appear close together. Typos are corrected via Levenshtein distance and re-searched. Result snippets are window-extracted around the matched terms, not blindly truncated.\n" +
	"\n" +
	"The knowledge base is unified: queries reach indexed content you stored (ctx_fetch_and_index, ctx_batch_execute output, and large ctx_execute / ctx_execute_file output) AND auto-captured session memory written by hooks (decisions, errors, blockers, plans, user prompts, rejected approaches, tool failures, compaction guides).\n" +
	"\n" +
	"WHEN:\n" +
	"  - You want to recall something that exists in storage (recently indexed content, prior session events, auto-memory) instead of re-reading raw sources\n" +
	"  - You have multiple related questions about the same body of knowledge — batch every question into one call (the ranking pipeline runs per-query but the round-trip cost is paid once)\n" +
	"  - You want to scope the query to one labelled source (pass `source` — partial match is fine)\n" +
	"  - You want a chronological view across current session + prior sessions + persistent auto-memory (pass `sort: \"timeline\"` — the default `relevance` mode only ranks within the current session)\n" +
	"  - You want to filter ranked results by content shape (pass `contentType: \"code\"` to surface implementation snippets or `contentType: \"prose\"` to surface explanations)\n" +
	"\n" +
	"WHEN NOT:\n" +
	"  - The data you want to query has never been stored in the knowledge base AND no session memory has accumulated around it — capture first (run a gather-and-index call), then come back here to query\n" +
	"  - You have one ad-hoc question against data that is not in the knowledge base — answer it inline by running code in the sandbox tool; one round-trip instead of capture-then-query\n" +
	"\n" +
	"RETURNS:\n" +
	"  Per-query ranked sections with window-extracted snippets. Use 2-4 specific technical terms per query. Common session-memory source labels: `decision` (user corrections / preferences), `error` (past failures), `blocker`, `plan`, `user-prompt`, `rejected-approach`, `compaction` (post-compact session guide). Each response carries a throttle counter (call #N/M in the rolling time window); results taper toward the soft cap and calls block after the hard cap.\n" +
	"\n" +
	"EXAMPLE: ctx_search(queries: [\"root cause\", \"proposed fix\", \"test coverage\"], source: \"issue-#683\")\n" +
	"EXAMPLE: ctx_search(queries: [\"what did we decide about caching\"], source: \"decision\", sort: \"timeline\")\n" +
	"EXAMPLE: ctx_search(queries: [\"useEffect cleanup pattern\"], source: \"react-docs\", contentType: \"code\", limit: 5)\n" +
	"EXAMPLE: ctx_search(queries: [\"last user prompt\", \"active skills\", \"open blockers\"], sort: \"timeline\")"

// searchSchema mirrors buildCtxSearchInputSchema (ctx-search-schema.ts:62-105)
// without the shared-DB `project` field.
const searchSchema = `{
  "type": "object",
  "properties": {
    "queries": {"type": ["array", "string"], "items": {"type": "string"}, "description": "Array of search queries. Batch ALL questions in one call."},
    "limit": {"type": ["number", "string"], "default": 3, "description": "Results per query (default: 3)"},
    "source": {"type": "string", "description": "Filter to a specific indexed source (partial match)."},
    "contentType": {"type": "string", "enum": ["code", "prose"], "description": "Filter results by content type: 'code' or 'prose'."},
    "sort": {"type": "string", "enum": ["relevance", "timeline"], "default": "relevance", "description": "Sort mode. 'relevance' (default): BM25 ranked, current session only. 'timeline': chronological across current session, prior sessions, and auto-memory."}
  }
}`

// sessionEventsSource is the source the directive's <session_search> block
// names. cmgo answers it from the events table, not an indexed dump (spec §3.1).
const sessionEventsSource = "session-events"

const emptyKBMessage = "Knowledge base is empty — no content has been indexed yet.\n\n" +
	"ctx_search is a follow-up tool that queries previously indexed content. " +
	"To gather and index content first, use:\n" +
	"  • ctx_batch_execute(commands, queries) — run commands, auto-index output, and search in one call\n" +
	"  • ctx_fetch_and_index(url) — fetch a URL, index it, then search with ctx_search\n" +
	"  • ctx_index(content, source) — manually index text content\n\n" +
	"After indexing, ctx_search becomes available for follow-up queries."

func registerSearch(s *mcp.Server, d *Deps) {
	guard := &floodGuard{} // one server per process, so this is process-wide
	s.AddTool(&mcp.Tool{
		Name:        "ctx_search",
		Title:       "Search Indexed Content",
		Description: searchDescription,
		InputSchema: json.RawMessage(searchSchema),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: new(bool), OpenWorldHint: new(bool)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := searchTool(d, guard, req.Params.Arguments)
		d.Stats.Record(d, "ctx_search", 0, res)
		return res, nil
	})
}

type searchArgs struct {
	queries     []string
	limit       float64
	source      string
	contentType store.ContentType
	sort        string
}

func parseSearchArgs(raw json.RawMessage) (searchArgs, error) {
	sa := searchArgs{limit: 3, sort: "relevance"}
	a, err := ParseArgs(raw)
	if err != nil {
		return sa, err
	}
	if sa.queries, _, err = a.Strings("queries"); err != nil {
		return sa, err
	}
	// No legacy `query`: TS reads it (S:2621) but its zod schema has no such
	// field, so the SDK strips it and TS answers "provide query or queries".
	if n, ok, err := a.Number("limit"); err != nil {
		return sa, err
	} else if ok {
		sa.limit = n
	}
	if sa.source, _, err = a.String("source"); err != nil {
		return sa, err
	}
	if ct, ok, err := a.String("contentType"); err != nil {
		return sa, err
	} else if ok {
		if ct != string(store.Code) && ct != string(store.Prose) {
			return sa, errors.New(`contentType: expected "code" or "prose"`)
		}
		sa.contentType = store.ContentType(ct)
	}
	if so, ok, err := a.String("sort"); err != nil {
		return sa, err
	} else if ok {
		if so != "relevance" && so != "timeline" {
			return sa, errors.New(`sort: expected "relevance" or "timeline"`)
		}
		sa.sort = so
	}
	return sa, nil
}

// searchTool is the ctx_search handler (S:2592-2805). Relevance mode reads
// the content store; timeline mode merges it with session events and
// auto-memory. The session-events source reads only session events.
func searchTool(d *Deps, guard *floodGuard, raw json.RawMessage) *mcp.CallToolResult {
	sa, err := parseSearchArgs(raw)
	if err != nil {
		return ArgError(err)
	}
	project := d.Cfg.ProjectKey
	if sa.sort != "timeline" && sa.source != sessionEventsSource {
		n, err := d.Store.ChunkCount(project)
		if err != nil {
			return ErrorResult("Search error: %v", err)
		}
		if n == 0 {
			return TextResult(emptyKBMessage, true)
		}
	}
	if len(sa.queries) == 0 {
		return TextResult("Error: provide query or queries.", true)
	}

	now := d.Now()
	flood := guard.record(now)
	if flood.blocked {
		return ErrorResult("BLOCKED: %d search calls in %ds. "+
			"You're flooding context. STOP making individual search calls. "+
			"Use ctx_batch_execute(commands, queries) for your next research step.",
			flood.count, int(math.Round(now.Sub(flood.windowStart).Seconds())))
	}
	effectiveLimit := 1
	if !flood.softCapped {
		effectiveLimit = int(math.Min(sa.limit, 2)) // JS slice(0, 1.5) keeps 1
	}

	opts := store.SearchOpts{Project: project, Source: sa.source, ContentType: sa.contentType, Limit: effectiveLimit, Mode: sa.sort}
	totalSize := 0
	var sections []string
	for _, q := range sa.queries {
		if totalSize > searchOutputCap {
			sections = append(sections, "## "+q+"\n(output cap reached)\n")
			continue
		}
		var hits []searchHit
		if effectiveLimit > 0 { // TS slice(0, <=0) is empty; the store would default to 3
			if hits, err = searchHits(d, q, sa, opts); err != nil {
				return ErrorResult("Search error: %v", err)
			}
		}
		if len(hits) == 0 {
			sections = append(sections, "## "+q+"\nNo results found.")
			continue
		}
		parts := make([]string, len(hits))
		for i, r := range hits {
			ts := ""
			if !r.at.IsZero() {
				ts = " | " + r.at.UTC().Format("2006-01-02 15:04")
			}
			parts[i] = fmt.Sprintf("--- [%s%s | %s] ---\n### %s\n\n%s",
				r.origin, ts, r.source, r.title, store.ExtractSnippet(r.content, q, searchSnippetMax, r.highlighted))
		}
		formatted := strings.Join(parts, "\n\n")
		sections = append(sections, "## "+q+"\n\n"+formatted)
		totalSize += jsLen(formatted)
	}
	output := strings.Join(sections, "\n\n---\n\n")

	if flood.count >= searchSoftCapAt {
		output += fmt.Sprintf("\n\n⚠ search call #%d/%d in this window. "+
			"Results limited to %d/query. %d call(s) remaining before block. "+
			`Batch queries: ctx_search(queries: ["q1","q2","q3"]) or use ctx_batch_execute.`,
			flood.count, searchBlockAfter, effectiveLimit, max(0, searchBlockAfter-flood.count))
	} else {
		output += fmt.Sprintf("\n\n> Throttle: call #%d/%d in this window. "+
			"%d call(s) before soft cap. "+
			"Prefer ctx_search(queries: [...]) array form for multi-query workloads — it counts as a single call.",
			flood.count, searchBlockAfter, max(0, searchSoftCapAt-flood.count))
	}
	// TS S:2785 falls back to "No results found. Indexed sources: …" when the
	// output is blank; the throttle line above makes that unreachable.
	return TextResult(output, false)
}

// searchHit is one formatted ctx_search result from any source (S:2739-2756).
type searchHit struct {
	origin, source, title, content, highlighted string
	at                                          time.Time
}

// searchHits runs one query. Source session-events returns the project's
// newest opts.Limit matching session events, newest first, in either mode.
// Relevance mode returns the content store's ranked results. Timeline mode (spec §3.2) takes the newest opts.Limit
// matches from the content store, the project's session events (all
// sessions) and, without a source filter, auto-memory; keeps the newest
// opts.Limit overall and returns them oldest first. TS kept the oldest.
func searchHits(d *Deps, q string, sa searchArgs, opts store.SearchOpts) ([]searchHit, error) {
	if sa.source == sessionEventsSource {
		evs, err := d.Store.SessionEventsForSearch(d.Cfg.ProjectKey, q, "", opts.Limit)
		if err != nil {
			return nil, err
		}
		hits := make([]searchHit, len(evs))
		for i, e := range evs {
			at, _ := time.Parse("2006-01-02 15:04:05", e.CreatedAt)
			hits[i] = searchHit{"prior-session", e.Category, "[" + e.Category + "] " + e.Type, e.Data, "", at}
		}
		return hits, nil
	}
	res, err := d.Store.SearchWithFallback(q, opts)
	if err != nil {
		return nil, err
	}
	var hits []searchHit
	for _, r := range res { // timeline mode returns these oldest first
		hits = append(hits, searchHit{"current-session", r.Source, r.Title, r.Content, r.Highlighted, r.IndexedAt})
	}
	if sa.sort != "timeline" {
		return hits, nil
	}
	evs, err := d.Store.SessionEventsForSearch(d.Cfg.ProjectKey, q, sa.source, opts.Limit)
	if err != nil {
		return nil, err
	}
	for _, e := range slices.Backward(evs) { // newest first from the store
		at, _ := time.Parse("2006-01-02 15:04:05", e.CreatedAt)
		// TS labels every session event prior-session, the current session's too (unified.ts:130).
		hits = append(hits, searchHit{"prior-session", "prior-session", "[" + e.Category + "] " + e.Type, e.Data, "", at})
	}
	if sa.source == "" {
		// Claude Code names its memory dir after the project dir as given, so ProjectDir, not ProjectKey.
		for _, m := range memory.SearchAutoMemory(q, opts.Limit, d.Cfg.ProjectDir, d.Cfg.ClaudeDir) {
			hits = append(hits, searchHit{"auto-memory", m.Source, m.Title, m.Content, "", m.Timestamp})
		}
	}
	// Stable: equal times (second resolution) keep source order, then id order.
	slices.SortStableFunc(hits, func(a, b searchHit) int { return a.at.Compare(b.at) })
	return hits[max(0, len(hits)-opts.Limit):], nil
}
```

`internal/mcpserver/purge.go`:
```go
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// purgeDescription is S:4457-4481 minus the ctx_stats, stats-file, events
// markdown and per-session FTS5 chunk wording (cmgo has one DB and chunks
// carry no session) and the stderr deprecation warning.
const purgeDescription = `DESTRUCTIVE: permanently delete indexed content. Cannot be undone. Requires confirm:true and exactly one scope.

WHEN:
  - User explicitly asks to clear a specific session ('purge this session', 'wipe this conversation')
  - User explicitly asks to reset the whole project ('reset everything', 'wipe the knowledge base')

WHEN NOT:
  - User says 'reset', 'clear', or 'wipe' without naming a scope -> ask which scope before calling
  - User wants to free memory or improve performance -> do not purge

SCOPES (pass exactly one):
  - Per-session: ctx_purge(confirm: true, sessionId: "<uuid>") deletes that session's events (auto-captured decisions, errors, plans, user prompts, rejected approaches, etc.); sibling sessions are preserved.
  - Per-project: ctx_purge(confirm: true, scope: "project") wipes the FTS5 knowledge base and every session event of this project.

CONTRACT:
  - confirm:true is required; confirm:false returns 'purge cancelled'.
  - sessionId and scope:'project' together return 'ambiguous - pick one'.
  - scope:'session' without sessionId throws (sessionId required).
  - Bare {confirm:true} is deprecated: maps to scope:'project'.

RETURNS:
  A summary of removed rows + the resolved scope.

EXAMPLE: ctx_purge(confirm: true, sessionId: "7c8a-1234-5678-9abc-def012345678")
EXAMPLE: ctx_purge(confirm: true, scope: "project")`

// purgeSchema mirrors the TS zod object (S:4485-4503) minus the stats wording.
// confirm is not required: spec §4 answers a missing confirm with the
// cancel message instead of a schema error.
const purgeSchema = `{
  "type": "object",
  "properties": {
    "confirm": {"type": ["boolean", "string"], "description": "MUST be true. Destructive operation; false returns 'purge cancelled'."},
    "sessionId": {"type": "string", "description": "UUID of a single session. Pairs with confirm:true to wipe only that session's events. Sibling sessions are preserved. MUST NOT be combined with scope:'project'."},
    "scope": {"type": "string", "enum": ["session", "project"], "description": "Explicit scope selector. 'session' REQUIRES sessionId. 'project' wipes the entire project (FTS5 + every session). Omit only for the deprecated bare-{confirm:true} back-compat path."}
  }
}`

// registerPurge registers ctx_purge. Port of S:4445-4611 over cmgo's single DB.
func registerPurge(s *mcp.Server, d *Deps) {
	s.AddTool(&mcp.Tool{
		Name:        "ctx_purge",
		Title:       "Purge Knowledge Base",
		Description: purgeDescription,
		InputSchema: json.RawMessage(purgeSchema),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: new(bool)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := purgeTool(d, req.Params.Arguments)
		d.Stats.Record(d, "ctx_purge", 0, res)
		return res, nil
	})
}

func purgeTool(d *Deps, raw json.RawMessage) *mcp.CallToolResult {
	a, err := ParseArgs(raw)
	if err != nil {
		return ArgError(err)
	}
	confirm, _, err := a.Bool("confirm")
	if err != nil {
		return ArgError(err)
	}
	sessionID, _, err := a.String("sessionId")
	if err != nil {
		return ArgError(err)
	}
	scope, _, err := a.String("scope")
	if err != nil {
		return ArgError(err)
	}
	if scope != "" && scope != "session" && scope != "project" {
		return ArgError(errors.New(`scope: expected "session" or "project"`))
	}
	if sessionID != "" && scope == "project" {
		return TextResult("Ambiguous purge: sessionId implies scope:'session', cannot combine with scope:'project'. "+
			"Use scope:'project' WITHOUT sessionId for the legacy whole-project wipe.", true)
	}
	if !confirm {
		return TextResult("Purge cancelled. Pass confirm: true to proceed.", false)
	}
	if scope == "" && sessionID != "" {
		scope = "session"
	}
	switch scope {
	case "session":
		if sessionID == "" { // purge.ts:189 throws
			return TextResult("purgeSession: scope:'session' requires sessionId. Pass scope:'project' for the legacy whole-project wipe.", true)
		}
		rows, err := d.Store.PurgeSession(sessionID, d.Cfg.ProjectKey)
		if err != nil {
			return ErrorResult("Purge error: %v", err)
		}
		what := "no matching rows"
		if rows > 0 {
			what = "session events"
		}
		return TextResult("Purged session "+sessionID+": "+what+". Other sessions and project-wide stats preserved.", false)
	default: // "project", or bare {confirm:true}
		sources, sessions, err := d.Store.PurgeProject(d.Cfg.ProjectKey)
		if err != nil {
			return ErrorResult("Purge error: %v", err)
		}
		if sources == 0 && sessions == 0 {
			return TextResult("Nothing to purge for this project ("+d.Cfg.ProjectKey+").", false)
		}
		return TextResult("Purged: knowledge base (FTS5), session events, session stats. All session data for this project has been permanently deleted.", false)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcpserver/ -run 'TestStats|TestPurge' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver/batch.go internal/mcpserver/fetchindex.go internal/mcpserver/search.go internal/mcpserver/purge.go internal/mcpserver/stats_test.go internal/mcpserver/purge_test.go
git commit -m "feat(mcpserver): record batch, fetch, search and purge stats"
```

### Task 13: ctx_stats tool

**Files:**
- Create: `internal/mcpserver/ctxstats.go`
- Modify: `internal/mcpserver/server.go`
- Test: `internal/mcpserver/ctxstats_test.go`
- Test: `internal/mcpserver/tools_list_test.go`
- Test: `cmd/cmgo/serve_test.go`

**Interfaces:**
- Consumes: `store.Stats` from Task 8; `StatsRecorder` from Task 11; `stats.Format`, `stats.Report`, `stats.ToolRow`, `stats.Category` from STR
- Produces: tool `ctx_stats`; unexported `func registerStats(s *mcp.Server, d *Deps, version string)`, `func statsTool(d *Deps, version string) *mcp.CallToolResult`, `func statsReport(s store.StatsSnapshot, now time.Time, version string) stats.Report`, `func sqliteUTC(s string) time.Time`

- [ ] **Step 1: Write the failing test**

`internal/mcpserver/ctxstats_test.go`:
```go
package mcpserver

import (
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/store"
)

// ctx_stats end to end: a session linked by cc_pid, a server of another
// Claude Code process in the same project, hook events, an indexed source.
func TestCtxStatsReport(t *testing.T) {
	var now time.Time
	h := newHarness(t, func() time.Time { return now })
	project := h.Deps.Cfg.ProjectKey
	h.Deps.Stats = &StatsRecorder{ServerID: "srv-e2e", CCPID: 4242, StartedAt: "2026-10-21 08:00:00"}
	if err := h.Deps.Store.SetSessionCC("sess-1", project, 4242); err != nil {
		t.Fatal(err)
	}
	// started_at is datetime('now'), cut to the second: the report is 2h
	// later give or take that second, which Duration prints as "2h".
	now = time.Now().Add(2 * time.Hour)
	h.events("sess-1", "",
		store.Event{Type: "bash-redirected", Category: "redirect", Priority: 2, Data: "Bash: curl x", BytesAvoided: 8192},
		store.Event{Type: "file_read", Category: "file", Priority: 1, Data: "a.go"})
	if err := h.Deps.Store.AddToolStat("srv-other", 999, project, "2026-10-20 07:00:00", "ctx_execute", 5, 5000); err != nil {
		t.Fatal(err)
	}
	h.index("notes", store.Chunk{Title: "N", Content: "héllo wörld"})
	h.writeProjectFile("data.txt", strings.Repeat("x", 1000))
	// raw 1000 + 2 stdout; returned is the 38-byte code echo plus "ok".
	if out, isErr := h.call("ctx_execute_file", args("path", "data.txt", "language", "shell", "code", "printf ok")); isErr || len(out) != 40 {
		t.Fatalf("execute_file: %v %d %q", isErr, len(out), out)
	}

	got, isErr := h.call("ctx_stats", `{}`)
	want := `cmgo stats

This session  ·  2h  ·  1 calls
  Kept out of context    962 B   (96% of 1002 B processed)
  Returned to context    40 B
  Redirected by hooks    1 calls  ·  ~8.0 KB
  ctx_execute_file          1 calls      962 B kept       40 B returned

Session memory  ·  2 events  ·  0 compactions
  file                       1
  redirect                   1

This project  ·  1 sources indexed (13 B)  ·  1 sessions  ·  2 events

All projects  ·  13.8 KB kept out  ·  1 sessions  ·  since 2026-10-20

cmgo test`
	if isErr || got != want {
		t.Fatalf("isErr=%v\ngot:\n%s\nwant:\n%s", isErr, got, want)
	}
	// The ctx_stats call itself is recorded, with no raw size.
	if again, _ := h.call("ctx_stats", `{}`); !strings.Contains(again, "  ctx_stats                 1 calls        0 B kept") ||
		!strings.Contains(again, "(96% of 1002 B processed)") {
		t.Fatalf("second report:\n%s", again)
	}
}

func TestCtxStatsUnlinked(t *testing.T) {
	h := newHarness(t, nil)
	h.Deps.Stats = &StatsRecorder{ServerID: "srv-solo", StartedAt: "2026-10-21 08:00:00"} // no Claude Code ancestor
	h.call("ctx_execute", args("language", "shell", "code", "printf abc"))
	got, isErr := h.call("ctx_stats", `{}`)
	if isErr || !strings.HasPrefix(got, "cmgo stats\n\nThis session  ·  no hook data yet (is cmgo-hook installed?)\n") ||
		!strings.Contains(got, "\n  ctx_execute               1 calls        0 B kept") || strings.Contains(got, "Session memory") {
		t.Fatalf("isErr=%v\n%s", isErr, got)
	}
}
```

`internal/mcpserver/tools_list_test.go`:
```go
package mcpserver

import (
	"context"
	"sort"
	"strings"
	"testing"
)

func TestServerRegistersSevenTools(t *testing.T) {
	h := newHarness(t, nil)
	res, err := h.Session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := []string{"ctx_batch_execute", "ctx_execute", "ctx_execute_file", "ctx_fetch_and_index", "ctx_purge", "ctx_search", "ctx_stats"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v, want %v", names, want)
	}
}
```

`cmd/cmgo/serve_test.go`:
```go
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// rpcPeer drives serve() over in-process pipes with newline-delimited JSON-RPC.
type rpcPeer struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *bufio.Reader
	done chan int
	errb *bytes.Buffer
}

func startServe(t *testing.T, env map[string]string) *rpcPeer {
	t.Helper()
	return startServeCtx(t, context.Background(), env)
}

func startServeCtx(t *testing.T, ctx context.Context, env map[string]string) *rpcPeer {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	errb := &bytes.Buffer{}
	p := &rpcPeer{t: t, in: inW, out: bufio.NewReader(outR), done: make(chan int, 1), errb: errb}
	go func() {
		p.done <- serve(ctx, serveIO{
			getenv: func(k string) string { return env[k] },
			getwd:  os.Getwd,
			in:     inR,
			out:    outW,
			stderr: errb,
		})
		outW.Close()
	}()
	return p
}

func (p *rpcPeer) send(msg map[string]any) {
	p.t.Helper()
	b, _ := json.Marshal(msg)
	written := make(chan error, 1)
	go func() { _, err := p.in.Write(append(b, '\n')); written <- err }()
	select {
	case err := <-written:
		if err != nil {
			p.t.Fatal(err)
		}
	case code := <-p.done:
		p.t.Fatalf("server exited with %d before reading the request (stderr: %s)", code, p.errb.String())
	case <-time.After(10 * time.Second):
		p.t.Fatalf("server did not read the request within 10s (stderr: %s)", p.errb.String())
	}
}

func (p *rpcPeer) recv(id int) map[string]any {
	p.t.Helper()
	for {
		line, err := p.out.ReadBytes('\n')
		if err != nil {
			p.t.Fatalf("read: %v (stderr: %s)", err, p.errb.String())
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			p.t.Fatalf("bad frame %q: %v", line, err)
		}
		if got, ok := m["id"].(float64); ok && int(got) == id {
			return m
		}
	}
}

func (p *rpcPeer) initialize() {
	p.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "0"},
	}})
	if r := p.recv(1); r["error"] != nil {
		p.t.Fatalf("initialize: %v", r["error"])
	}
	p.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

func testEnv(t *testing.T) map[string]string {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"HOME": filepath.Join(root, "home"), "CMGO_DIR": filepath.Join(root, "data"), "CMGO_PROJECT_DIR": project}
}

func TestServeListsSevenToolsAndExitsOnEOF(t *testing.T) {
	env := testEnv(t)
	p := startServe(t, env)
	p.initialize()
	p.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	r := p.recv(2)
	var names []string
	for _, tool := range r["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	sort.Strings(names)
	want := "ctx_batch_execute,ctx_execute,ctx_execute_file,ctx_fetch_and_index,ctx_purge,ctx_search,ctx_stats"
	if strings.Join(names, ",") != want {
		t.Fatalf("tools = %v", names)
	}
	p.in.Close()
	select {
	case code := <-p.done:
		if code != 0 {
			t.Fatalf("exit %d: %s", code, p.errb.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not exit after stdin EOF")
	}
	if _, err := os.Stat(filepath.Join(env["CMGO_DIR"], "cmgo.db")); err != nil {
		t.Fatalf("db not created: %v", err)
	}
}

// Spec §2.5: the MCP-ready sentinel lives exactly as long as serve.
func TestServeWritesSentinel(t *testing.T) {
	env := testEnv(t)
	p := startServe(t, env)
	p.initialize() // the store is open, so the sentinel is written
	sentinel := filepath.Join(env["CMGO_DIR"], "run", fmt.Sprintf("mcp-ready-%d", os.Getpid()))
	if b, err := os.ReadFile(sentinel); err != nil || string(b) != fmt.Sprint(os.Getpid()) {
		t.Fatalf("sentinel %q: %v", b, err)
	}
	p.in.Close()
	if code := <-p.done; code != 0 {
		t.Fatalf("exit %d: %s", code, p.errb.String())
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("sentinel left after serve returned")
	}
}

// A sentinel that cannot be written costs the redirects, not the server.
func TestServeSentinelErrorIsAWarning(t *testing.T) {
	env := testEnv(t)
	os.MkdirAll(env["CMGO_DIR"], 0o755)
	os.WriteFile(filepath.Join(env["CMGO_DIR"], "run"), []byte("x"), 0o644) // run/ cannot be a dir
	p := startServe(t, env)
	p.initialize()
	p.in.Close()
	if code := <-p.done; code != 0 || !strings.Contains(p.errb.String(), "cmgo: mcp-ready sentinel skipped:") {
		t.Fatalf("exit %d, stderr %q", code, p.errb.String())
	}
}

func TestServeFailsOnUnusableDataDir(t *testing.T) {
	env := testEnv(t)
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	env["CMGO_DIR"] = filepath.Join(blocker, "sub") // parent is a file
	p := startServe(t, env)
	select {
	case code := <-p.done:
		if code != 1 || !strings.Contains(p.errb.String(), "cmgo: open") {
			t.Fatalf("exit %d stderr %q", code, p.errb.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve should fail fast")
	}
}

// pgrepCount counts processes whose command line contains pattern.
func pgrepCount(t *testing.T, pattern string) int {
	t.Helper()
	out, _ := exec.Command("pgrep", "-f", pattern).Output()
	return len(strings.Fields(string(out)))
}

// Review focus: Claude Code closing a session while a foreground command is
// still running must not leave that command (or its children) behind.
func TestServeShutdownKillsForegroundRun(t *testing.T) {
	env := testEnv(t)
	p := startServe(t, env)
	p.initialize()
	// Built at runtime so no other command line (e.g. the shell running go test) contains it.
	marker := fmt.Sprintf("sleep %d", 4000+os.Getpid()%5000)
	p.send(map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/call", "params": map[string]any{
		"name": "ctx_execute", "arguments": map[string]any{"language": "shell", "code": marker + " & " + marker + "; wait"},
	}})
	deadline := time.Now().Add(5 * time.Second)
	for pgrepCount(t, marker) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if pgrepCount(t, marker) < 2 {
		t.Fatal("command never started")
	}
	p.in.Close()
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not exit while a foreground command was running")
	}
	deadline = time.Now().Add(3 * time.Second)
	for pgrepCount(t, marker) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := pgrepCount(t, marker); n > 0 {
		exec.Command("pkill", "-f", marker).Run()
		t.Fatalf("%d process(es) left running after shutdown", n)
	}
}

// SIGINT/SIGTERM reach serve as a cancelled ctx: a running foreground command
// with no timeout must be killed and serve must return promptly.
func TestServeCancelKillsForegroundRun(t *testing.T) {
	env := testEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := startServeCtx(t, ctx, env)
	p.initialize()
	marker := fmt.Sprintf("sleep %d", 4001+os.Getpid()%5000)
	p.send(map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/call", "params": map[string]any{
		"name": "ctx_execute", "arguments": map[string]any{"language": "shell", "code": marker},
	}})
	deadline := time.Now().Add(5 * time.Second)
	for pgrepCount(t, marker) < 1 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if pgrepCount(t, marker) < 1 {
		t.Fatal("command never started")
	}
	cancel()
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		exec.Command("pkill", "-f", marker).Run()
		t.Fatal("serve did not return within 3s of cancel")
	}
	deadline = time.Now().Add(2 * time.Second)
	for pgrepCount(t, marker) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := pgrepCount(t, marker); n > 0 {
		exec.Command("pkill", "-f", marker).Run()
		t.Fatalf("%d process(es) left running after cancel", n)
	}
}

// Review focus: two sessions on the same project share one DB; concurrent
// indexing from both must not surface SQLITE_BUSY to the model.
func TestServeConcurrentSessionsSameDB(t *testing.T) {
	env := testEnv(t)
	a, b := startServe(t, env), startServe(t, env)
	a.initialize()
	b.initialize()
	var wg sync.WaitGroup
	errs := make(chan string, 20)
	for _, p := range []*rpcPeer{a, b} {
		wg.Add(1)
		go func(p *rpcPeer) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				id := 100 + i
				p.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{
					"name": "ctx_batch_execute", "arguments": map[string]any{
						"commands": []any{map[string]any{"label": fmt.Sprintf("seq%d", i), "command": "seq 1 2000"}},
						"queries":  []any{"1999"},
					},
				}})
				r := p.recv(id)
				res, _ := r["result"].(map[string]any)
				if r["error"] != nil || res == nil || res["isError"] == true {
					errs <- fmt.Sprintf("call %d: %v %v", i, r["error"], res)
				}
			}
		}(p)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	a.in.Close()
	b.in.Close()
	<-a.done
	<-b.done
}

// ctx_execute checks deny rules itself; the global ones live in
// $CLAUDE_CONFIG_DIR/settings.json when that is set.
func TestServeDeniesFromClaudeConfigDir(t *testing.T) {
	env := testEnv(t)
	cfg := filepath.Join(t.TempDir(), "claudecfg")
	os.MkdirAll(cfg, 0o755)
	os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(`{"permissions":{"deny":["Bash(echo cmgodenied*)"]}}`), 0o644)
	env["CLAUDE_CONFIG_DIR"] = cfg
	p := startServe(t, env)
	p.initialize()
	p.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "ctx_execute", "arguments": map[string]any{"language": "shell", "code": "echo cmgodenied"},
	}})
	res := p.recv(2)["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if res["isError"] != true || !strings.Contains(text, "deny") {
		t.Fatalf("not denied: %v", res)
	}
	p.in.Close()
	<-p.done
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcpserver/ ./cmd/cmgo/ -run 'TestCtxStats|TestServerRegisters|TestServeLists' -v`
Expected: FAIL with "ctxstats_test.go:37: CallTool ctx_stats: calling "tools/call": unknown tool "ctx_stats""

- [ ] **Step 3: Write minimal implementation**

`internal/mcpserver/ctxstats.go`:
```go
package mcpserver

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lang315/context-mode-go/internal/stats"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// statsDescription is S:3947-3950 minus "estimated token usage": cmgo keeps
// no token counts (stats spec §0).
const statsDescription = "Returns context consumption statistics for the current session. " +
	"Shows bytes kept out of and returned to context, breakdown by tool, call counts, " +
	"session memory, and context savings ratio."

// registerStats registers ctx_stats (stats spec §2). version is cmgo's, for
// the report's last line.
func registerStats(s *mcp.Server, d *Deps, version string) {
	s.AddTool(&mcp.Tool{
		Name:        "ctx_stats",
		Title:       "Session Statistics",
		Description: statsDescription,
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: new(bool), IdempotentHint: true, OpenWorldHint: new(bool)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := statsTool(d, version)
		d.Stats.Record(d, "ctx_stats", 0, res)
		return res, nil
	})
}

func statsTool(d *Deps, version string) *mcp.CallToolResult {
	q := store.StatsQuery{Project: d.Cfg.ProjectKey}
	if d.Stats != nil {
		q.ServerID, q.CCPID = d.Stats.ServerID, d.Stats.CCPID
	}
	snap, err := d.Store.Stats(q)
	if err != nil {
		return ErrorResult("Stats error: %v", err)
	}
	return TextResult(stats.Format(statsReport(snap, d.Now(), version)), false)
}

// statsReport maps the store snapshot onto the renderer's input.
func statsReport(s store.StatsSnapshot, now time.Time, version string) stats.Report {
	r := stats.Report{
		Version:             version,
		Now:                 now,
		SessionLinked:       s.SessionLinked,
		SessionStart:        sqliteUTC(s.SessionStart),
		Raw:                 s.Raw,
		Redirects:           s.Redirects,
		RedirectBytes:       s.RedirectBytes,
		Events:              s.Events,
		Compactions:         s.Compactions,
		ProjectSources:      s.ProjectSources,
		ProjectContentBytes: s.ProjectContentBytes,
		ProjectSessions:     s.ProjectSessions,
		ProjectEvents:       s.ProjectEvents,
		LifetimeKept:        s.LifetimeKept,
		LifetimeSessions:    s.LifetimeSessions,
		LifetimeSince:       sqliteUTC(s.LifetimeSince),
	}
	for _, t := range s.Tools {
		r.Tools = append(r.Tools, stats.ToolRow{Tool: t.Tool, Calls: t.Calls, Returned: t.BytesReturned, Kept: t.BytesKept})
	}
	for name, n := range s.Categories {
		r.Categories = append(r.Categories, stats.Category{Name: name, Count: n})
	}
	return r
}

// sqliteUTC parses a datetime('now') value; "" or a bad value gives the zero time.
func sqliteUTC(s string) time.Time {
	t, _ := time.Parse("2006-01-02 15:04:05", s)
	return t
}
```

`internal/mcpserver/server.go`:
```go
// Package mcpserver registers cmgo's MCP tools and formats their output.
package mcpserver

import (
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/lang315/context-mode-go/internal/fetch"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Deps are the long-lived services every tool handler uses.
type Deps struct {
	Cfg    config.Config
	Store  *store.Store
	Exec   *executor.Executor
	Policy *security.Policy
	Fetch  *fetch.Fetcher
	Now    func() time.Time // injectable clock; defaults to time.Now
	Stats  *StatsRecorder   // nil records no tool_stats
}

// New builds the MCP server with all seven tools registered.
func New(d *Deps, version string) *mcp.Server {
	if d.Now == nil {
		d.Now = time.Now
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "cmgo", Version: version}, nil)
	registerExecute(s, d)
	registerExecuteFile(s, d)
	registerBatch(s, d)
	registerSearch(s, d)
	registerFetchAndIndex(s, d)
	registerPurge(s, d)
	registerStats(s, d, version)
	return s
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcpserver/ ./cmd/cmgo/ -run 'TestCtxStats|TestServerRegisters|TestServeLists' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver/ctxstats.go internal/mcpserver/server.go internal/mcpserver/ctxstats_test.go internal/mcpserver/tools_list_test.go cmd/cmgo/serve_test.go
git commit -m "feat(mcpserver): add the ctx_stats tool"
```

### Task 14: Routing block ctx stats entry

**Files:**
- Modify: `internal/routing/text.go`
- Test: `internal/routing/golden_test.go`
- Test: `internal/routing/text_test.go`

**Interfaces:**
- Consumes: none (routing text only)
- Produces: `func RoutingBlock(includeCommands, toolSearchBootstrap bool) string` (signature unchanged): with includeCommands the `<ctx_commands>` section starts with the TS `ctx stats` entry

- [ ] **Step 1: Write the failing test**

`internal/routing/golden_test.go`:
```go
package routing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/security"
)

// goldenCase is one case of ../../testdata/golden/hooks-pretooluse.json,
// written by tools/golden-gen/gen-hooks.mjs from TS routePreToolUse +
// formatDecision, each case in a fresh node process. output is the exact
// stdout of the TS hook, or null when it writes nothing. Tool names are
// already cmgo's; <BASE> stands for the generator's temp dir.
type goldenCase struct {
	I          int             `json:"i"`
	Tool       string          `json:"tool"`
	Input      json.RawMessage `json:"input"`
	ProjectDir string          `json:"projectDir"` // "<proj>" or ""
	SessionID  string          `json:"sessionId"`
	Subagent   bool            `json:"subagent"`
	Output     *string         `json:"output"`
}

// knownDiff is a case where cmgo differs from TS on purpose, one row of
// testdata/golden/HOOKS-KNOWN-DIFFS.md. want is the cmgo output ("" for none).
type knownDiff struct{ reason, want string }

func knownDiffs() map[int]knownDiff {
	const ctxMatcher = "spec §2.3: ctx_execute, ctx_execute_file and ctx_batch_execute are not PreToolUse matchers; cmgo serve checks deny rules itself"
	return map[int]knownDiff{
		25: {ctxMatcher, ""},
		26: {ctxMatcher, ""},
		28: {ctxMatcher, ""},
		29: {ctxMatcher, ""},
		30: {ctxMatcher, ""},
		31: {"spec §2.3: external-MCP guidance dropped (the TS mcp__ matcher never fires, R1 P11)", ""},
	}
}

func TestGoldenPreToolUse(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", "hooks-pretooluse.json"))
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	raw = []byte(strings.ReplaceAll(string(raw), "<BASE>", base))
	var g struct {
		TSCommit string       `json:"tsCommit"`
		Cases    []goldenCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if g.TSCommit != "a5e6dd31" {
		t.Fatalf("hooks-pretooluse.json: commit %q", g.TSCommit)
	}
	cases := g.Cases

	// The state gen-hooks.mjs sets up: two files around the 50,000-byte
	// Read threshold, project rules, an empty home, a live MCP sentinel.
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	proj := filepath.Join(base, "proj")
	write(filepath.Join(proj, ".claude", "settings.json"),
		`{"permissions":{"deny":["Bash(sudo *)","Read(**/.env)"],"ask":["Bash(git push:*)"],"allow":["Bash(curl:*)"]}}`)
	write(filepath.Join(base, "big.txt"), strings.Repeat("x", 50001))
	write(filepath.Join(base, "small.txt"), strings.Repeat("x", 50000))
	home := filepath.Join(base, "home")
	run := filepath.Join(base, "run")
	remove, err := WriteSentinel(run, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer remove()

	diffs := knownDiffs()
	seen := map[int]bool{}
	for _, c := range cases { // in order: session s1's guidance markers carry over
		in, err := hookio.ParseObject(c.Input)
		if err != nil {
			t.Fatal(err)
		}
		pd := ""
		if c.ProjectDir == "<proj>" {
			pd = proj
		}
		d := Deps{
			ProjectDir: pd,
			SessionID:  c.SessionID,
			RunDir:     run,
			Policy:     security.NewPolicy(pd, home),
			Getenv:     func(string) string { return "" },
			MCPReady:   func() bool { return MCPReady(run) },
		}
		got := string(Format(Route(c.Tool, in, c.Subagent, d)))
		ts := ""
		if c.Output != nil {
			ts = *c.Output
		}
		if kd, ok := diffs[c.I]; ok {
			seen[c.I] = true
			if got == ts {
				t.Errorf("case %d (%s): listed difference is gone (%s)", c.I, c.Tool, kd.reason)
			} else if got != kd.want {
				t.Errorf("case %d (%s):\n got %q\nwant %q", c.I, c.Tool, got, kd.want)
			}
			continue
		}
		if got != ts {
			t.Errorf("case %d (%s %s):\n got %q\nwant %q", c.I, c.Tool, c.Input, got, ts)
		}
	}
	if len(cases) != 37 || len(seen) != len(diffs) {
		t.Errorf("%d cases, %d of %d known diffs seen", len(cases), len(seen), len(diffs))
	}
}

// missingCommands are the TS <ctx_commands> entries cmgo has no tool for
// yet (spec §2.5): doctor and upgrade arrive with sub-project 5, which
// deletes them here. stats came with sub-project 4.
var missingCommands = []string{"doctor", "upgrade"}

// dropCommands derives cmgo's routing block from the TS text: it cuts the
// entry of each missing command, its two lines and the blank line after them.
func dropCommands(t *testing.T, ts string) string {
	t.Helper()
	for _, name := range missingCommands {
		i := strings.Index(ts, "    \"ctx "+name+"\" | ")
		if i < 0 {
			t.Fatalf("no ctx %s entry in the TS routing block", name)
		}
		j := strings.Index(ts[i:], "\n\n")
		ts = ts[:i] + ts[i+j+2:]
	}
	return ts
}

// TestGoldenStrings checks the texts in ../../testdata/golden/hooks-strings.json,
// rendered by gen-hooks.mjs from hooks/routing-block.mjs with the mcp__cmgo__
// namer, and the redirect texts taken from TS routePreToolUse decisions.
func TestGoldenStrings(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", "hooks-strings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		TSCommit      string `json:"tsCommit"`
		RoutingBlocks []struct {
			Name                string `json:"name"`
			IncludeCommands     bool   `json:"includeCommands"`
			ToolSearchBootstrap bool   `json:"toolSearchBootstrap"`
			Text                string `json:"text"`
		} `json:"routingBlocks"`
		Texts map[string]string `json:"texts"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if g.TSCommit != "a5e6dd31" || len(g.RoutingBlocks) != 3 {
		t.Fatalf("hooks-strings.json: commit %q, %d routing blocks", g.TSCommit, len(g.RoutingBlocks))
	}
	for _, b := range g.RoutingBlocks {
		want := b.Text
		if b.IncludeCommands {
			want = dropCommands(t, want)
		}
		if got := RoutingBlock(b.IncludeCommands, b.ToolSearchBootstrap); got != want {
			t.Errorf("%s: RoutingBlock(%v, %v) differs\ngot:\n%s", b.Name, b.IncludeCommands, b.ToolSearchBootstrap, got)
		}
	}
	got := map[string]string{
		"guidance-bash":              bashGuidance,
		"guidance-read":              readGuidance,
		"guidance-grep":              grepGuidance,
		"deny-curl-wget":             curlMessage,
		"deny-inline-http":           inlineMessage,
		"deny-build-gradlew-example": buildMessage("./gradlew build"),
		"deny-webfetch-example":      webFetchMessage("https://example.com/a"),
	}
	if len(g.Texts) != len(got) {
		t.Errorf("hooks-strings.json has %d texts, want %d", len(g.Texts), len(got))
	}
	for name, want := range g.Texts {
		if got[name] != want {
			t.Errorf("%s:\ngot  %q\nwant %q", name, got[name], want)
		}
	}
}
```

`internal/routing/text_test.go`:
```go
package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// The expected texts are the TS output (hooks/routing-block.mjs and
// hooks/core/routing.mjs at a5e6dd31) with mcp__plugin_context-mode_context-mode__
// replaced by mcp__cmgo__, pinned by byte length and sha256. testdata/golden/
// hooks-strings.json carries the full texts (TestGoldenStrings).
type pinned struct {
	name string
	size int
	hash string
}

func checkPinned(t *testing.T, p pinned, got string) {
	t.Helper()
	sum := sha256.Sum256([]byte(got))
	if len(got) != p.size || hex.EncodeToString(sum[:]) != p.hash {
		t.Errorf("%s: %d bytes, sha256 %x; want %d bytes, %s\ngot:\n%s", p.name, len(got), sum, p.size, p.hash, got)
	}
}

func TestRoutingBlock(t *testing.T) {
	cases := []struct {
		commands, bootstrap bool
		want                pinned
	}{
		// The TS text minus the doctor and upgrade entries (spec §2.5).
		{true, false, pinned{"routing block with commands", 3931, "33c0cd36f55398f029ef06711282a7c1977b0a084536860ef2a500840dc2bc85"}},
		{false, true, pinned{"agent routing suffix", 4105, "8c756c25026a96477fac96011f6869f83d1a4974640c5bff5373d76ba5de762c"}},
		{false, false, pinned{"SessionStart routing block", 3481, "6b7c33490c32133a9bfe5a11b240236f6732a0f700c581ad90f46d6868edd023"}},
	}
	for _, c := range cases {
		checkPinned(t, c.want, RoutingBlock(c.commands, c.bootstrap))
	}
	if strings.Contains(RoutingBlock(false, false), "mcp__plugin_") {
		t.Error("routing block still names the TS plugin tools")
	}
}

func TestGuidanceAndRedirectTexts(t *testing.T) {
	cases := []struct {
		got  string
		want pinned
	}{
		{bashGuidance, pinned{"guidance-bash", 597, "8e5b6e0792263eb68968bdf22bf1ad96db5ab44c58b3d0b686ef59144d885100"}},
		{readGuidance, pinned{"guidance-read", 377, "480cdbe6726c56c5e9d30043d59b710fd69760afa3bcd18c4bcc1b84d1f5f11c"}},
		{grepGuidance, pinned{"guidance-grep", 477, "3759bf1be31f94b6c5f49281c80144dc629201c1410ff6a1850ce98729713c88"}},
		{curlMessage, pinned{"deny-curl-wget", 467, "8445533370d36a76361daaa46de4c9046d37a4c49980a04d71bca1374a7207b9"}},
		{inlineMessage, pinned{"deny-inline-http", 342, "da11642dff800f1658d28902eddc419ada0b3f3e0912f3e96b4dfb02f4bdc356"}},
		{webFetchMessage("https://example.com/a"), pinned{"deny-webfetch-example", 563, "4a134a67a5f425ddb67b4c38b81e67d9a6939d3f16a1976f734756a8025c9ed9"}},
		{buildMessage("./gradlew build"), pinned{"deny-build-gradlew-example", 402, "f97ec9b71a06e587b3529da87527eb15345ea76f4c53cdaf03dfd48bb2412ef6"}},
	}
	for _, c := range cases {
		checkPinned(t, c.want, c.got)
	}
}

func TestBuildMessageEscapes(t *testing.T) {
	// routing.mjs:811: backslashes doubled, then quotes escaped.
	got := buildMessage(`mvn "test" C:\x`)
	if want := `code: \"mvn \"test\" C:\\x 2>&1 | tail -30\")`; !strings.Contains(got, want) {
		t.Errorf("buildMessage = %q, want it to contain %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/routing/ -run 'TestRoutingBlock|TestGoldenStrings' -v`
Expected: FAIL with "golden_test.go:179: routing_block_claude: RoutingBlock(true, false) differs"

- [ ] **Step 3: Write minimal implementation**

`internal/routing/text.go`:
```go
package routing

import "strings"

// ToolPrefix is the name prefix of cmgo's MCP tools under
// `claude mcp add --scope user cmgo` (spec §1.3).
const ToolPrefix = "mcp__cmgo__"

// named stands in for the TS tool namer t(): every $T in a TS template
// becomes ToolPrefix. It is applied to constant text only, never to input.
func named(s string) string {
	return strings.ReplaceAll(s, "$T", ToolPrefix)
}

// The routing block of createRoutingBlock (routing-block.mjs:16-80), cut at
// its two ${…} slots. Each optional section carries the newlines its slot
// adds, so an empty slot still leaves a blank line, as in TS.
var (
	blockHead = named(`
<context_window_protection>
  <priority_instructions>
    Every byte a tool returns enters your conversation memory and costs reasoning capacity for the rest of the session. The context-mode tools let you do the work in a sandbox and surface only the derived answer — the raw bytes stay out. Think-in-Code: program the analysis, do not compute it by reading raw data into your conversation.
  </priority_instructions>
`)
	blockBootstrap = named(`
  <deferred_tool_bootstrap>
    The context-mode tools below may be DEFERRED in your harness — their schemas are not loaded yet, so calling them directly fails (e.g. "tool not found" / InputValidationError). Load them ONCE before your first ctx_* call:
    ToolSearch(query: "select:$Tctx_batch_execute,$Tctx_search,$Tctx_execute,$Tctx_execute_file,$Tctx_fetch_and_index")
    After that they are callable. If any ctx_* call fails as not-found, ToolSearch it and retry — do NOT fall back to Bash/Read just because the schema was not loaded yet.
  </deferred_tool_bootstrap>
`)
	blockMiddle = named(`
  <tool_selection_hierarchy>
    0. MEMORY: $Tctx_search(sort: "timeline")
       - On resume or compaction, query prior decisions, errors, plans, user prompts before asking the user — auto-captured session memory is searchable.
    1. GATHER: $Tctx_batch_execute(commands, queries)
       - Primary research tool. Runs commands in parallel, auto-indexes each output, and (when queries are passed) returns matching sections in the same round trip — no follow-up search call.
       - Each command: {label: "section header", command: "shell command"}; the label becomes the FTS5 chunk title — descriptive labels improve search.
    2. FOLLOW-UP: $Tctx_search(queries: ["q1", "q2", ...])
       - Multiple related questions about anything already indexed (your captures + session memory). Batch every question in one array; the ranking pipeline runs per-query and the round-trip cost is paid once.
    3. PROCESSING: $Tctx_execute(language, code) | $Tctx_execute_file(path, language, code)
       - Derive answers FROM data: filter, count, aggregate, parse, transform. Only what you console.log() enters your conversation; the raw bytes stay in the sandbox.
  </tool_selection_hierarchy>

  <when_not_to_use>
    - You intend to PROCESS the output (filter, count, parse, aggregate) → use $Tctx_batch_execute or $Tctx_execute. Bash stays correct when you intend to OBSERVE a short fixed output (git status on a clean tree, whoami, pwd) or when you are mutating state (git, mkdir, rm, mv, navigation).
    - You want to analyze, summarize, or extract from a file → use $Tctx_execute_file. Read stays correct when you intend to Edit the file (Edit needs the exact bytes in your conversation to match against).
    - WebFetch → use $Tctx_fetch_and_index; full network access, results indexed for $Tctx_search, raw page bytes never enter your conversation.
    - $Tctx_execute and $Tctx_execute_file for file writes → these run code in a subprocess and discard the sandbox FS; they are for analysis, processing, and computation only.
  </when_not_to_use>

  <file_writing_policy>
    File writes use the native Write or Edit tool — $Tctx_execute, $Tctx_execute_file, and Bash subprocesses do not persist edits to the host filesystem.
    Applies to all file types: code, configs, plans, specs, YAML, JSON, markdown.
  </file_writing_policy>

  <output_constraints>
    <artifact_policy>
      Write artifacts (code, configs, PRDs) to files. Return only: file path + 1-line description.
    </artifact_policy>
  </output_constraints>
  <session_continuity>
    Skills, roles, and decisions captured earlier in this session are a memory aid, not a standing order. Treat them as context that may help — the user's most recent message always takes precedence. If a captured directive conflicts with what the user now asks, follow the user; a past phrase does not bind you.
  </session_continuity>
`)
	// Only the commands cmgo has a tool for (spec §2.5): the doctor and
	// upgrade entries arrive with sub-project 5. The stats entry keeps the TS
	// trigger list verbatim (stats spec §4).
	blockCommands = named(`
  <ctx_commands>
    "ctx stats" | "ctx-stats" | "/ctx-stats" | context savings question
    → Call stats MCP tool, display full output verbatim.

    "ctx purge" | "ctx-purge" | "/ctx-purge" | wipe/reset knowledge base
    → Call purge MCP tool with confirm: true. Warn: irreversible.

    After /clear or /compact: knowledge base preserved. Tell user: "context-mode knowledge base preserved. Use ` + "`" + `ctx purge` + "`" + ` to start fresh."
  </ctx_commands>
`)
	blockTail = named(`
</context_window_protection>`)
)

// RoutingBlock ports createRoutingBlock with the cmgo tool namer.
// includeCommands adds the <ctx_commands> section and toolSearchBootstrap
// the <deferred_tool_bootstrap> section.
func RoutingBlock(includeCommands, toolSearchBootstrap bool) string {
	s := blockHead
	if toolSearchBootstrap {
		s += blockBootstrap
	}
	s += blockMiddle
	if includeCommands {
		s += blockCommands
	}
	return s + blockTail
}

// The guidance texts (routing-block.mjs:82-92) and the redirect messages
// (routing.mjs:771, 802, 815, 879).
var (
	readGuidance  = named("<context_guidance>\n  <tip>\n    Reading to Edit the file? Read is correct — Edit needs the exact bytes in your conversation to match against.\n    Reading to analyze, summarize, or extract from the file? Use $Tctx_execute_file(path, language, code) — the bytes stay in the sandbox and only what your code prints enters your conversation.\n  </tip>\n</context_guidance>")
	grepGuidance  = named("<context_guidance>\n  <tip>\n    Grep results may be larger than you expect. When you intend to count, filter, or aggregate matches (not just spot-check one), run the search through $Tctx_execute(language: \"javascript\", code: \"...\") — the raw match list stays in the sandbox and only your derived answer enters your conversation. Use language: \"shell\" only when the code matches the host shell (PowerShell on Windows, POSIX shell on Unix).\n  </tip>\n</context_guidance>")
	bashGuidance  = named("<context_guidance>\n  <tip>\n    When you intend to PROCESS the output (filter, count, parse, aggregate), use $Tctx_batch_execute(commands, queries) for multiple commands or $Tctx_execute(language: \"javascript\", code: \"...\") for one — the raw output stays in the sandbox and only what you print enters your conversation. Shell stays the right surface when you intend to OBSERVE a short fixed output or when you are mutating state (git, mkdir, rm, mv, navigation); if you use $Tctx_execute(language: \"shell\"), write syntax for the host shell.\n  </tip>\n</context_guidance>")
	curlMessage   = named(`context-mode: curl/wget redirected. Call $Tctx_execute(language, code) to fetch the URL, derive your answer in code, and print only the result — the raw HTTP body stays in the sandbox instead of entering your conversation. Or call $Tctx_fetch_and_index(url, source) when you want to query the response later via $Tctx_search. Both have full network access. Retry the same call on a transient DNS error (EAI_AGAIN, ETIMEDOUT, ENETUNREACH).`)
	inlineMessage = named(`context-mode: Inline HTTP redirected. Call $Tctx_execute(language, code) to fetch, derive your answer in code, and console.log() only the result — the raw response body stays in the sandbox instead of entering your conversation. Full network access. Retry the same call on a transient DNS error (EAI_AGAIN, ETIMEDOUT, ENETUNREACH).`)
	buildHead     = named(`context-mode: Build tool redirected. Call $Tctx_execute(language: \"shell\", code: \"`)
	buildTail     = ` 2>&1 | tail -30\") to run the build and print only the tail — the verbose build log stays in the sandbox instead of entering your conversation. For more targeted output, replace \"tail -30\" with \"grep -E '(error|warning|FAIL|✗|×)'\" or similar, so only the lines that matter come back.`
	webFetchHead  = named(`context-mode: WebFetch redirected. Call $Tctx_fetch_and_index(url: "`)
	webFetchTail  = named(`", source: "...") to fetch + index the page, then $Tctx_search(queries: [...]) to query the indexed content — the raw page bytes stay in storage instead of entering your conversation. Or call $Tctx_execute(language, code) when you want to derive your answer in one round trip (parse, extract, count) without persisting the response. Both have full network access. Retry the same call on a transient DNS error (EAI_AGAIN, ETIMEDOUT, ENETUNREACH).`)
)

// buildMessage is the build-tool redirect text for command. The command is
// escaped as routing.mjs:811 does, and the template's \" stay literal: the
// Claude Code formatter never unescapes them.
func buildMessage(command string) string {
	safe := strings.ReplaceAll(command, `\`, `\\`)
	safe = strings.ReplaceAll(safe, `"`, `\"`)
	return buildHead + safe + buildTail
}

// webFetchMessage is the WebFetch deny text; url goes in raw (R1 P16).
func webFetchMessage(url string) string {
	return webFetchHead + url + webFetchTail
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/routing/ -run 'TestRoutingBlock|TestGoldenStrings' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/routing/text.go internal/routing/golden_test.go internal/routing/text_test.go
git commit -m "feat(routing): add the ctx stats command to the routing block"
```

### Task 15: serve sets the stats recorder

**Files:**
- Modify: `cmd/cmgo/serve.go`
- Test: `cmd/cmgo/serve_test.go`

**Interfaces:**
- Consumes: `procinfo.ClaudePID`, `procinfo.System`, `procinfo.Lookup` from PROC; `mcpserver.StatsRecorder` from Task 11
- Produces: unexported `func newStatsRecorder(pid int, start time.Time, look procinfo.Lookup) *mcpserver.StatsRecorder`; `serve` sets `Deps.Stats`

- [ ] **Step 1: Write the failing test**

`cmd/cmgo/serve_test.go`:
```go
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// rpcPeer drives serve() over in-process pipes with newline-delimited JSON-RPC.
type rpcPeer struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *bufio.Reader
	done chan int
	errb *bytes.Buffer
}

func startServe(t *testing.T, env map[string]string) *rpcPeer {
	t.Helper()
	return startServeCtx(t, context.Background(), env)
}

func startServeCtx(t *testing.T, ctx context.Context, env map[string]string) *rpcPeer {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	errb := &bytes.Buffer{}
	p := &rpcPeer{t: t, in: inW, out: bufio.NewReader(outR), done: make(chan int, 1), errb: errb}
	go func() {
		p.done <- serve(ctx, serveIO{
			getenv: func(k string) string { return env[k] },
			getwd:  os.Getwd,
			in:     inR,
			out:    outW,
			stderr: errb,
		})
		outW.Close()
	}()
	return p
}

func (p *rpcPeer) send(msg map[string]any) {
	p.t.Helper()
	b, _ := json.Marshal(msg)
	written := make(chan error, 1)
	go func() { _, err := p.in.Write(append(b, '\n')); written <- err }()
	select {
	case err := <-written:
		if err != nil {
			p.t.Fatal(err)
		}
	case code := <-p.done:
		p.t.Fatalf("server exited with %d before reading the request (stderr: %s)", code, p.errb.String())
	case <-time.After(10 * time.Second):
		p.t.Fatalf("server did not read the request within 10s (stderr: %s)", p.errb.String())
	}
}

func (p *rpcPeer) recv(id int) map[string]any {
	p.t.Helper()
	for {
		line, err := p.out.ReadBytes('\n')
		if err != nil {
			p.t.Fatalf("read: %v (stderr: %s)", err, p.errb.String())
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			p.t.Fatalf("bad frame %q: %v", line, err)
		}
		if got, ok := m["id"].(float64); ok && int(got) == id {
			return m
		}
	}
}

func (p *rpcPeer) initialize() {
	p.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "0"},
	}})
	if r := p.recv(1); r["error"] != nil {
		p.t.Fatalf("initialize: %v", r["error"])
	}
	p.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

func testEnv(t *testing.T) map[string]string {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"HOME": filepath.Join(root, "home"), "CMGO_DIR": filepath.Join(root, "data"), "CMGO_PROJECT_DIR": project}
}

func TestServeListsSevenToolsAndExitsOnEOF(t *testing.T) {
	env := testEnv(t)
	p := startServe(t, env)
	p.initialize()
	p.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	r := p.recv(2)
	var names []string
	for _, tool := range r["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	sort.Strings(names)
	want := "ctx_batch_execute,ctx_execute,ctx_execute_file,ctx_fetch_and_index,ctx_purge,ctx_search,ctx_stats"
	if strings.Join(names, ",") != want {
		t.Fatalf("tools = %v", names)
	}
	p.in.Close()
	select {
	case code := <-p.done:
		if code != 0 {
			t.Fatalf("exit %d: %s", code, p.errb.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not exit after stdin EOF")
	}
	if _, err := os.Stat(filepath.Join(env["CMGO_DIR"], "cmgo.db")); err != nil {
		t.Fatalf("db not created: %v", err)
	}
}

// Spec §2.5: the MCP-ready sentinel lives exactly as long as serve.
func TestServeWritesSentinel(t *testing.T) {
	env := testEnv(t)
	p := startServe(t, env)
	p.initialize() // the store is open, so the sentinel is written
	sentinel := filepath.Join(env["CMGO_DIR"], "run", fmt.Sprintf("mcp-ready-%d", os.Getpid()))
	if b, err := os.ReadFile(sentinel); err != nil || string(b) != fmt.Sprint(os.Getpid()) {
		t.Fatalf("sentinel %q: %v", b, err)
	}
	p.in.Close()
	if code := <-p.done; code != 0 {
		t.Fatalf("exit %d: %s", code, p.errb.String())
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("sentinel left after serve returned")
	}
}

// A sentinel that cannot be written costs the redirects, not the server.
func TestServeSentinelErrorIsAWarning(t *testing.T) {
	env := testEnv(t)
	os.MkdirAll(env["CMGO_DIR"], 0o755)
	os.WriteFile(filepath.Join(env["CMGO_DIR"], "run"), []byte("x"), 0o644) // run/ cannot be a dir
	p := startServe(t, env)
	p.initialize()
	p.in.Close()
	if code := <-p.done; code != 0 || !strings.Contains(p.errb.String(), "cmgo: mcp-ready sentinel skipped:") {
		t.Fatalf("exit %d, stderr %q", code, p.errb.String())
	}
}

func TestServeFailsOnUnusableDataDir(t *testing.T) {
	env := testEnv(t)
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	env["CMGO_DIR"] = filepath.Join(blocker, "sub") // parent is a file
	p := startServe(t, env)
	select {
	case code := <-p.done:
		if code != 1 || !strings.Contains(p.errb.String(), "cmgo: open") {
			t.Fatalf("exit %d stderr %q", code, p.errb.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve should fail fast")
	}
}

// pgrepCount counts processes whose command line contains pattern.
func pgrepCount(t *testing.T, pattern string) int {
	t.Helper()
	out, _ := exec.Command("pgrep", "-f", pattern).Output()
	return len(strings.Fields(string(out)))
}

// Review focus: Claude Code closing a session while a foreground command is
// still running must not leave that command (or its children) behind.
func TestServeShutdownKillsForegroundRun(t *testing.T) {
	env := testEnv(t)
	p := startServe(t, env)
	p.initialize()
	// Built at runtime so no other command line (e.g. the shell running go test) contains it.
	marker := fmt.Sprintf("sleep %d", 4000+os.Getpid()%5000)
	p.send(map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/call", "params": map[string]any{
		"name": "ctx_execute", "arguments": map[string]any{"language": "shell", "code": marker + " & " + marker + "; wait"},
	}})
	deadline := time.Now().Add(5 * time.Second)
	for pgrepCount(t, marker) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if pgrepCount(t, marker) < 2 {
		t.Fatal("command never started")
	}
	p.in.Close()
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not exit while a foreground command was running")
	}
	deadline = time.Now().Add(3 * time.Second)
	for pgrepCount(t, marker) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := pgrepCount(t, marker); n > 0 {
		exec.Command("pkill", "-f", marker).Run()
		t.Fatalf("%d process(es) left running after shutdown", n)
	}
}

// SIGINT/SIGTERM reach serve as a cancelled ctx: a running foreground command
// with no timeout must be killed and serve must return promptly.
func TestServeCancelKillsForegroundRun(t *testing.T) {
	env := testEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := startServeCtx(t, ctx, env)
	p.initialize()
	marker := fmt.Sprintf("sleep %d", 4001+os.Getpid()%5000)
	p.send(map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/call", "params": map[string]any{
		"name": "ctx_execute", "arguments": map[string]any{"language": "shell", "code": marker},
	}})
	deadline := time.Now().Add(5 * time.Second)
	for pgrepCount(t, marker) < 1 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if pgrepCount(t, marker) < 1 {
		t.Fatal("command never started")
	}
	cancel()
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		exec.Command("pkill", "-f", marker).Run()
		t.Fatal("serve did not return within 3s of cancel")
	}
	deadline = time.Now().Add(2 * time.Second)
	for pgrepCount(t, marker) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := pgrepCount(t, marker); n > 0 {
		exec.Command("pkill", "-f", marker).Run()
		t.Fatalf("%d process(es) left running after cancel", n)
	}
}

// Review focus: two sessions on the same project share one DB; concurrent
// indexing from both must not surface SQLITE_BUSY to the model.
func TestServeConcurrentSessionsSameDB(t *testing.T) {
	env := testEnv(t)
	a, b := startServe(t, env), startServe(t, env)
	a.initialize()
	b.initialize()
	var wg sync.WaitGroup
	errs := make(chan string, 20)
	for _, p := range []*rpcPeer{a, b} {
		wg.Add(1)
		go func(p *rpcPeer) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				id := 100 + i
				p.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{
					"name": "ctx_batch_execute", "arguments": map[string]any{
						"commands": []any{map[string]any{"label": fmt.Sprintf("seq%d", i), "command": "seq 1 2000"}},
						"queries":  []any{"1999"},
					},
				}})
				r := p.recv(id)
				res, _ := r["result"].(map[string]any)
				if r["error"] != nil || res == nil || res["isError"] == true {
					errs <- fmt.Sprintf("call %d: %v %v", i, r["error"], res)
				}
			}
		}(p)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	a.in.Close()
	b.in.Close()
	<-a.done
	<-b.done
}

// ctx_execute checks deny rules itself; the global ones live in
// $CLAUDE_CONFIG_DIR/settings.json when that is set.
func TestServeDeniesFromClaudeConfigDir(t *testing.T) {
	env := testEnv(t)
	cfg := filepath.Join(t.TempDir(), "claudecfg")
	os.MkdirAll(cfg, 0o755)
	os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(`{"permissions":{"deny":["Bash(echo cmgodenied*)"]}}`), 0o644)
	env["CLAUDE_CONFIG_DIR"] = cfg
	p := startServe(t, env)
	p.initialize()
	p.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "ctx_execute", "arguments": map[string]any{"language": "shell", "code": "echo cmgodenied"},
	}})
	res := p.recv(2)["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if res["isError"] != true || !strings.Contains(text, "deny") {
		t.Fatalf("not denied: %v", res)
	}
	p.in.Close()
	<-p.done
}

func TestNewStatsRecorder(t *testing.T) {
	procs := map[int]struct {
		ppid int
		name string
	}{
		500: {400, "cmgo"},
		400: {300, "node"},
		300: {1, "Claude"},
	}
	look := func(pid int) (int, string, error) {
		p, ok := procs[pid]
		if !ok {
			return 0, "", fmt.Errorf("no pid %d", pid)
		}
		return p.ppid, p.name, nil
	}
	start := time.Date(2026, 10, 21, 9, 30, 5, 123456789, time.FixedZone("x", 7200))
	r := newStatsRecorder(500, start, look)
	want := fmt.Sprintf("500-%d", start.UnixNano())
	if r.ServerID != want || r.CCPID != 300 || r.StartedAt != "2026-10-21 07:30:05" {
		t.Fatalf("recorder = %+v, want server %s", r, want)
	}
	if r := newStatsRecorder(400, start, look); r.CCPID != 300 {
		t.Fatalf("from node: cc_pid %d", r.CCPID)
	}
	if r := newStatsRecorder(300, start, look); r.CCPID != 0 { // no claude ancestor above it
		t.Fatalf("from claude itself: cc_pid %d", r.CCPID)
	}
}

// serve records every tool call; ctx_stats reads the rows back.
func TestServeRecordsToolStats(t *testing.T) {
	env := testEnv(t)
	p := startServe(t, env)
	p.initialize()
	call := func(id int, name string, args map[string]any) string {
		p.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
		res := p.recv(id)["result"].(map[string]any)
		return res["content"].([]any)[0].(map[string]any)["text"].(string)
	}
	call(2, "ctx_execute", map[string]any{"language": "shell", "code": "echo hi"})
	call(3, "ctx_execute", map[string]any{"language": "shell", "code": "echo again"})
	if text := call(4, "ctx_stats", map[string]any{}); !strings.Contains(text, "\n  ctx_execute               2 calls") || !strings.HasSuffix(text, "\n\ncmgo "+version) {
		t.Fatalf("ctx_stats:\n%s", text)
	}
	p.in.Close()
	<-p.done
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/cmgo/ -run 'TestNewStatsRecorder|TestServeRecordsToolStats' -v`
Expected: FAIL with "cmd/cmgo/serve_test.go:338:7: undefined: newStatsRecorder"

- [ ] **Step 3: Write minimal implementation**

`cmd/cmgo/serve.go`:
```go
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/lang315/context-mode-go/internal/fetch"
	"github.com/lang315/context-mode-go/internal/mcpserver"
	"github.com/lang315/context-mode-go/internal/procinfo"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	sourceRetention = 14 * 24 * time.Hour // spec §2.1 SOURCE_RETENTION
	cleanupEvery    = 24 * time.Hour      // spec §2.2: at most once per 24 h
)

// serveIO is what `cmgo serve` talks over; tests pass pipes.
type serveIO struct {
	getenv func(string) string
	getwd  func() (string, error)
	in     io.ReadCloser
	out    io.WriteCloser
	stderr io.Writer
}

// newStatsRecorder identifies this server process in tool_stats (stats spec
// §1.1-§1.2): pid plus start time, so a reused pid is a new server, and the
// Claude Code ancestor that links it to the session hooks record.
func newStatsRecorder(pid int, start time.Time, look procinfo.Lookup) *mcpserver.StatsRecorder {
	return &mcpserver.StatsRecorder{
		ServerID:  fmt.Sprintf("%d-%d", pid, start.UnixNano()),
		CCPID:     procinfo.ClaudePID(pid, look),
		StartedAt: start.UTC().Format("2006-01-02 15:04:05"),
	}
}

// serve runs the MCP server until stdin closes or SIGINT/SIGTERM arrives,
// then terminates background process groups and closes the DB.
func serve(ctx context.Context, sio serveIO) int {
	cfg, err := config.Load(sio.getenv, sio.getwd)
	if err != nil {
		fmt.Fprintf(sio.stderr, "cmgo: config: %v\n", err)
		return 1
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(sio.stderr, "cmgo: open %s: %v\n", cfg.DBPath, err)
		return 1
	}
	defer st.Close()
	// Hooks redirect curl and WebFetch only while a server is live (spec §2.5).
	if remove, err := routing.WriteSentinel(filepath.Join(cfg.DataDir, "run"), os.Getpid()); err != nil {
		fmt.Fprintf(sio.stderr, "cmgo: mcp-ready sentinel skipped: %v\n", err)
	} else {
		defer remove()
	}
	if _, err := st.CleanupIfDue(time.Now(), sourceRetention, cleanupEvery); err != nil {
		fmt.Fprintf(sio.stderr, "cmgo: cleanup skipped: %v\n", err) // never fatal
	}
	ex := executor.New(cfg.ProjectDir)
	defer ex.Shutdown()

	d := &mcpserver.Deps{
		Cfg:    cfg,
		Store:  st,
		Exec:   ex,
		Policy: security.NewPolicyDir(cfg.ProjectDir, cfg.ClaudeDir),
		Fetch:  fetch.New(cfg.FetchStrict),
		Stats:  newStatsRecorder(os.Getpid(), time.Now(), procinfo.System()),
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// A signal cancels ctx; kill running commands now instead of waiting for Run to drain.
	defer context.AfterFunc(ctx, ex.Shutdown)()
	// Lift the 16 MB frame cap: TS accepted any size of tool argument.
	t := &mcp.IOTransport{Reader: sio.in, Writer: sio.out, MaxLineLength: -1}
	if err := mcpserver.New(d, version).Run(ctx, t); err != nil && ctx.Err() == nil && err != io.EOF {
		fmt.Fprintf(sio.stderr, "cmgo: serve: %v\n", err)
		return 1
	}
	return 0
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/cmgo/ -run 'TestNewStatsRecorder|TestServeRecordsToolStats' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/cmgo/serve.go cmd/cmgo/serve_test.go
git commit -m "feat(cmgo): record tool stats from serve with the Claude Code pid"
```

---

### Task 16: SessionStart records the Claude Code pid

**Files:**
- Modify: `internal/hookcmd/hookcmd.go`
- Create: `internal/hookcmd/sessioncc_test.go`
- Test: `internal/hookcmd/hookcmd_test.go`
- Test: `internal/hookcmd/lock_test.go`

**Interfaces:**
- Consumes: `procinfo.Lookup`, `procinfo.System() Lookup`, `procinfo.ClaudePID(pid int, look Lookup) int` (fragment PROC); `func (s *Store) SetSessionCC(sessionID, project string, ccPID int) error` (fragment S4A); `store.OpenWith`, `(*Store).InsertEvents` (existing)
- Produces: unexported `var procLookup procinfo.Lookup`; `func (h *hookCtx) recordSession(st *store.Store, evs []store.Event) error`; SessionStart opens the DB for every source and writes `cc_pid` before its events

- [ ] **Step 1: Write the failing test**

The new test fakes a `claude` parent with pid 4242 and checks the `session_meta.cc_pid` of each source. `TestHookSessionStartOtherSources` drops its `noDB` check after `clear`, and the lock test adds a `clear` fire, which must fail as fast as the other writes.

`internal/hookcmd/sessioncc_test.go`:
```go
package hookcmd

import (
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// fakeClaude makes this process a child of a "claude" process with pid 4242,
// so the test does not depend on the Claude Code (or not) it runs under.
func fakeClaude(t *testing.T) {
	t.Helper()
	old := procLookup
	t.Cleanup(func() { procLookup = old })
	procLookup = func(pid int) (int, string, error) {
		switch pid {
		case os.Getpid():
			return 4242, "cmgo-hook", nil
		case 4242:
			return 1, "claude", nil
		}
		return 0, "", errors.New("no such process")
	}
}

// sessionCC reads a session's session_meta.cc_pid, or -1 when it has no row.
func sessionCC(t *testing.T, env map[string]string, sid string) int {
	t.Helper()
	db, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: filepath.Join(env["CMGO_DIR"], "cmgo.db")}).String()+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cc := -1
	if err := db.QueryRow("SELECT cc_pid FROM session_meta WHERE session_id = ?", sid).Scan(&cc); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	return cc
}

// Stats spec §1.2: every SessionStart source records the Claude Code pid of
// its session, clear included (it starts a new session id in the same
// process), and still prints the routing block first.
func TestHookSessionStartRecordsCCPID(t *testing.T) {
	fakeClaude(t)
	env := hookEnv(t)
	for _, src := range []string{"startup", "clear", "resume", "compact"} {
		sid := "s-" + src
		if out := callHook(t, env, "sessionstart", `{"session_id":"`+sid+`","source":"`+src+`"}`); out != sessionStartOut("") {
			t.Fatalf("%s: got %q", src, out)
		}
		if cc := sessionCC(t, env, sid); cc != 4242 {
			t.Errorf("%s: cc_pid = %d, want 4242", src, cc)
		}
	}
	if log := hookErrors(env); log != "" {
		t.Fatalf("hook errors: %s", log)
	}
}

// With no claude ancestor the row still exists, with cc_pid 0 (never linked).
func TestHookSessionStartNoClaudeAncestor(t *testing.T) {
	old := procLookup
	t.Cleanup(func() { procLookup = old })
	procLookup = func(int) (int, string, error) { return 0, "", errors.New("no such process") }
	env := hookEnv(t)
	callHook(t, env, "sessionstart", `{"session_id":"s1","source":"clear"}`)
	if cc := sessionCC(t, env, "s1"); cc != 0 {
		t.Fatalf("cc_pid = %d, want 0", cc)
	}
}
```

`internal/hookcmd/hookcmd_test.go`:
```go
package hookcmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/memory"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/store"
)

// hookEnv is a temp HOME, CMGO_DIR and project dir, never the real ones.
func hookEnv(t *testing.T) map[string]string {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"HOME": filepath.Join(root, "home"), "CMGO_DIR": filepath.Join(root, "data"), "CLAUDE_PROJECT_DIR": project}
}

var hookNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

// callHook runs one hook fire and returns its stdout.
func callHook(t *testing.T, env map[string]string, event, stdin string) string {
	t.Helper()
	var out bytes.Buffer
	code := Run(event, IO{
		Getenv: func(k string) string { return env[k] },
		Getwd:  os.Getwd,
		Stdin:  strings.NewReader(stdin),
		Stdout: &out,
		Now:    func() time.Time { return hookNow },
	})
	if code != 0 {
		t.Fatalf("%s: exit %d", event, code)
	}
	return out.String()
}

// eventRows returns a session's rows as "type|category|priority|data|hook|bytes".
func eventRows(t *testing.T, env map[string]string, sid string) []string {
	t.Helper()
	st, err := store.Open(filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	evs, err := st.SessionEvents(sid)
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, e := range evs {
		rows = append(rows, fmt.Sprintf("%s|%s|%d|%s|%s|%d", e.Type, e.Category, e.Priority, e.Data, e.SourceHook, e.BytesAvoided))
	}
	return rows
}

func assertRows(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func noDB(t *testing.T, env map[string]string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(env["CMGO_DIR"], "cmgo.db")); err == nil {
		t.Fatal("the store was opened with no event to write")
	}
}

func hookErrors(env map[string]string) string {
	b, _ := os.ReadFile(filepath.Join(env["CMGO_DIR"], "hook-errors.log"))
	return string(b)
}

const curlInput = `{"tool_name":"Bash","tool_input":{"command":"curl https://x.com"},"session_id":"s1"}`

func TestHookPreToolUsePassthrough(t *testing.T) {
	env := hookEnv(t)
	for _, in := range []string{
		`{"tool_name":"Bash","tool_input":{"command":"git status"},"session_id":"s1"}`,
		curlInput, // no live cmgo serve: redirects are gated off
	} {
		if out := callHook(t, env, "pretooluse", in); out != "" {
			t.Fatalf("%s: got %q", in, out)
		}
	}
	// A missing or non-object tool_input is an empty command: Bash guidance, once.
	if out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","session_id":"s2"}`); !strings.Contains(out, `"additionalContext":"<context_guidance>`) {
		t.Fatalf("missing tool_input: got %q", out)
	}
	if out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","tool_input":"x","session_id":"s2"}`); out != "" {
		t.Fatalf("second guidance: got %q", out)
	}
	noDB(t, env)
	if s := hookErrors(env); s != "" {
		t.Fatalf("errors logged: %s", s)
	}
}

func TestHookPreToolUseRedirect(t *testing.T) {
	env := hookEnv(t)
	remove, err := routing.WriteSentinel(filepath.Join(env["CMGO_DIR"], "run"), os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	out := callHook(t, env, "pretooluse", curlInput)
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"context-mode: curl/wget redirected. Call mcp__cmgo__ctx_execute(language, code)`
	if !strings.HasPrefix(out, want) || !strings.HasSuffix(out, "\"}}\n") {
		t.Fatalf("got %q", out)
	}
	rows := eventRows(t, env, "s1")
	if len(rows) != 2 || rows[0] != "bash-redirected|redirect|2|Bash: curl https://x.com|PreToolUse|8192" ||
		!strings.HasPrefix(rows[1], "rejected|rejected-approach|2|Bash: context-mode: curl/wget redirected.") {
		t.Fatalf("rows: %q", rows)
	}
	// A subagent call is gated off even with a live server.
	if out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","tool_input":{"command":"curl https://x.com"},"session_id":"s1","agent_id":"a"}`); out != "" {
		t.Fatalf("subagent: got %q", out)
	}
}

func TestHookPreToolUseSecurityDeny(t *testing.T) {
	env := hookEnv(t)
	dir := filepath.Join(env["CLAUDE_PROJECT_DIR"], ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"permissions":{"deny":["Bash(rm -rf *)"]}}`), 0o644)
	out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","tool_input":{"command":"rm -rf /x"},"session_id":"s1"}`)
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Blocked by security policy: matches deny pattern Bash(rm -rf *)"}}` + "\n"
	if out != want {
		t.Fatalf("got %q", out)
	}
	assertRows(t, eventRows(t, env, "s1"),
		"rejected|rejected-approach|2|Bash: Blocked by security policy: matches deny pattern Bash(rm -rf *)|PreToolUse|0")
}

// The project dir comes from input.cwd when no env var names it.
func TestHookProjectFromInputCwd(t *testing.T) {
	env := hookEnv(t)
	project := env["CLAUDE_PROJECT_DIR"]
	delete(env, "CLAUDE_PROJECT_DIR")
	os.MkdirAll(filepath.Join(project, ".claude"), 0o755)
	os.WriteFile(filepath.Join(project, ".claude", "settings.json"), []byte(`{"permissions":{"deny":["Bash(rm -rf *)"]}}`), 0o644)
	in := fmt.Sprintf(`{"tool_name":"Bash","tool_input":{"command":"rm -rf /x"},"session_id":"s1","cwd":%q}`, project)
	if out := callHook(t, env, "pretooluse", in); !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatalf("got %q", out)
	}
}

var errLine = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\S* pid=\d+ pretooluse: \S`)

func TestHookFailOpen(t *testing.T) {
	env := hookEnv(t)
	if out := callHook(t, env, "pretooluse", `{"tool_name":`); out != "" {
		t.Fatalf("malformed: got %q", out)
	}
	if s := hookErrors(env); !errLine.MatchString(s) || strings.Count(s, "\n") != 1 {
		t.Fatalf("log: %q", s)
	}
	if out := callHook(t, env, "nope", curlInput); out != "" {
		t.Fatalf("unknown event: got %q", out)
	}
}

// Spec §1.2: empty stdin produces no output and is not an error.
func TestHookEmptyStdin(t *testing.T) {
	env := hookEnv(t)
	if out := callHook(t, env, "pretooluse", " \n"); out != "" {
		t.Fatalf("got %q", out)
	}
	if s := hookErrors(env); s != "" {
		t.Fatalf("log: %q", s)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { panic("boom") }

func TestHookRecoversPanic(t *testing.T) {
	env := hookEnv(t)
	dir := filepath.Join(env["CLAUDE_PROJECT_DIR"], ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"permissions":{"deny":["Bash(rm -rf *)"]}}`), 0o644)
	code := Run("pretooluse", IO{
		Getenv: func(k string) string { return env[k] },
		Getwd:  os.Getwd,
		Stdin:  strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"rm -rf /x"}}`),
		Stdout: failWriter{},
		Now:    time.Now,
	})
	if code != 0 || !strings.Contains(hookErrors(env), "pretooluse: panic: boom") {
		t.Fatalf("exit %d, log %q", code, hookErrors(env))
	}
}

func TestHookStdinTimeout(t *testing.T) {
	defer func(d time.Duration) { stdinTimeout = d }(stdinTimeout)
	stdinTimeout = 50 * time.Millisecond
	env := hookEnv(t)
	r, w := io.Pipe() // never closed: stdin that does not end
	defer w.Close()
	var out bytes.Buffer
	code := Run("pretooluse", IO{
		Getenv: func(k string) string { return env[k] }, Getwd: os.Getwd, Stdin: r, Stdout: &out, Now: time.Now,
	})
	if code != 0 || out.Len() != 0 || !strings.Contains(hookErrors(env), "pretooluse: stdin") {
		t.Fatalf("exit %d, out %q, log %q", code, out.String(), hookErrors(env))
	}
}

// sessionStartOut is the SessionStart output: the routing block, then extra.
func sessionStartOut(extra string) string {
	return `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":` +
		string(hookio.QuoteString(routing.RoutingBlock(true, false)+extra)) + "}}\n"
}

func TestHookSessionStartStartup(t *testing.T) {
	env := hookEnv(t)
	project := env["CLAUDE_PROJECT_DIR"]
	homeRules := filepath.Join(env["HOME"], ".claude", "CLAUDE.md")
	os.MkdirAll(filepath.Dir(homeRules), 0o755)
	os.WriteFile(homeRules, []byte("home rules"), 0o644)
	os.WriteFile(filepath.Join(project, "CLAUDE.md"), []byte("project rules"), 0o644)
	runDir := filepath.Join(env["CMGO_DIR"], "run")
	for _, d := range []string{"guidance-old", "guidance-new"} {
		os.MkdirAll(filepath.Join(runDir, d), 0o700)
	}
	old := hookNow.Add(-8 * 24 * time.Hour)
	os.Chtimes(filepath.Join(runDir, "guidance-old"), old, old)

	if out := callHook(t, env, "sessionstart", `{"session_id":"s1","source":"startup"}`); out != sessionStartOut("") {
		t.Fatalf("got %q", out)
	}
	assertRows(t, eventRows(t, env, "s1"),
		"rule|rule|1|"+homeRules+"|SessionStart|0",
		"rule_content|rule|1|home rules|SessionStart|0",
		"rule|rule|1|"+filepath.Join(project, "CLAUDE.md")+"|SessionStart|0",
		"rule_content|rule|1|project rules|SessionStart|0",
		fmt.Sprintf(`session_start|session_start|1|{"source":"startup","project_dir":%q,"started_at":%d}|SessionStart|0`, project, hookNow.Unix()))
	if _, err := os.Stat(filepath.Join(runDir, "guidance-old")); err == nil {
		t.Fatal("old guidance dir not pruned")
	}
	if _, err := os.Stat(filepath.Join(runDir, "guidance-new")); err != nil {
		t.Fatal("fresh guidance dir removed")
	}
	// A missing or null source is startup, as TS input.source ?? "startup".
	callHook(t, env, "sessionstart", `{"session_id":"s2"}`)
	callHook(t, env, "sessionstart", `{"session_id":"s3","source":null}`)
	for _, sid := range []string{"s2", "s3"} {
		if rows := eventRows(t, env, sid); len(rows) != 5 {
			t.Fatalf("%s rows: %q", sid, rows)
		}
	}
}

func TestHookSessionStartOtherSources(t *testing.T) {
	env := hookEnv(t)
	if out := callHook(t, env, "sessionstart", `{"session_id":"s1","source":"clear"}`); out != sessionStartOut("") {
		t.Fatalf("clear: got %q", out)
	}
	// With no events, resume and compact add no context and record only session_start.
	for _, src := range []string{"resume", "compact"} {
		if out := callHook(t, env, "sessionstart", `{"session_id":"s-`+src+`","source":"`+src+`"}`); out != sessionStartOut("") {
			t.Fatalf("%s: got %q", src, out)
		}
		assertRows(t, eventRows(t, env, "s-"+src), sessionStartRow(env, src))
	}
}

func sessionStartRow(env map[string]string, src string) string {
	return fmt.Sprintf(`session_start|session_start|1|{"source":%q,"project_dir":%q,"started_at":%d}|SessionStart|0`,
		src, env["CLAUDE_PROJECT_DIR"], hookNow.Unix())
}

// seedSession records three events for s1 and returns them as stored.
func seedSession(t *testing.T, env map[string]string) []store.StoredEvent {
	t.Helper()
	callHook(t, env, "posttooluse", `{"tool_name":"Read","tool_input":{"file_path":"/p/a.go"},"tool_response":"x","session_id":"s1"}`)
	callHook(t, env, "userpromptsubmit", `{"prompt":"fix the login bug","session_id":"s1"}`)
	st, err := store.Open(filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	evs, err := st.SessionEvents("s1")
	if err != nil || len(evs) != 3 {
		t.Fatalf("seed: %d events, %v", len(evs), err)
	}
	return evs
}

// Spec §2.2: compact adds the directive and auto-injection and records
// resume_completed and session_start.
func TestHookSessionStartCompact(t *testing.T) {
	env := hookEnv(t)
	evs := seedSession(t, env)
	ai := memory.AutoInjection(evs)
	if ai == "" {
		t.Fatal("seed events give no auto-injection")
	}
	want := sessionStartOut(memory.Directive("compact", evs) + "\n\n" + ai)
	if out := callHook(t, env, "sessionstart", `{"session_id":"s1","source":"compact"}`); out != want {
		t.Fatalf("got %q\nwant %q", out, want)
	}
	rows := eventRows(t, env, "s1")
	assertRows(t, rows[3:],
		"resume_completed|session-resume|1|Session resumed from compact. Prior events loaded.|SessionStart|0",
		sessionStartRow(env, "compact"))
	if s := hookErrors(env); s != "" {
		t.Fatalf("errors logged: %s", s)
	}
}

func TestHookSessionStartResume(t *testing.T) {
	env := hookEnv(t)
	evs := seedSession(t, env)
	want := sessionStartOut(memory.Directive("resume", evs))
	if out := callHook(t, env, "sessionstart", `{"session_id":"s1","source":"resume"}`); out != want {
		t.Fatalf("got %q\nwant %q", out, want)
	}
	assertRows(t, eventRows(t, env, "s1")[3:], sessionStartRow(env, "resume"))
}

// The directive needs the events, so the DB is read before printing; a
// failed read prints the routing block alone. Writes come after the output,
// so a locked DB still gets the directive out and logs the write error.
func TestHookSessionStartCompactFailOpen(t *testing.T) {
	env := hookEnv(t)
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	env["CMGO_DIR"] = filepath.Join(blocker, "data") // parent is a regular file
	if out := callHook(t, env, "sessionstart", `{"session_id":"s1","source":"compact"}`); out != sessionStartOut("") {
		t.Fatalf("no DB: got %q", out)
	}

	env = hookEnv(t)
	evs := seedSession(t, env)
	holdDBLock(t, filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if out := callHook(t, env, "sessionstart", `{"session_id":"s1","source":"resume"}`); out != sessionStartOut(memory.Directive("resume", evs)) {
		t.Fatalf("locked: got %q", out)
	}
	if !strings.Contains(hookErrors(env), "sessionstart:") {
		t.Fatalf("no logged error: %q", hookErrors(env))
	}
}

func TestHookPostToolUse(t *testing.T) {
	env := hookEnv(t)
	if out := callHook(t, env, "posttooluse", `{"tool_name":"Read","tool_input":{"file_path":"/p/a.go"},"tool_response":{"type":"text","file":{"filePath":"/p/a.go","content":"x"}},"session_id":"s1"}`); out != "" {
		t.Fatalf("got %q", out)
	}
	assertRows(t, eventRows(t, env, "s1"), "file_read|file|1|/p/a.go|PostToolUse|0")

	env = hookEnv(t)
	callHook(t, env, "posttooluse", `{"tool_name":"WebSearch","tool_input":{"query":"x"},"session_id":"s1"}`)
	noDB(t, env)
}

func TestHookPostToolUseFailure(t *testing.T) {
	env := hookEnv(t)
	if out := callHook(t, env, "posttoolusefailure", `{"tool_name":"Bash","tool_input":{"command":"false"},"error":"exit 1","session_id":"s1"}`); out != "" {
		t.Fatalf("got %q", out)
	}
	assertRows(t, eventRows(t, env, "s1"), "error_tool|error|2|exit 1|PostToolUseFailure|0")
}

func TestHookUserPromptSubmit(t *testing.T) {
	env := hookEnv(t)
	if out := callHook(t, env, "userpromptsubmit", `{"prompt":"hello","session_id":"s1"}`); out != "" {
		t.Fatalf("got %q", out)
	}
	callHook(t, env, "userpromptsubmit", `{"message":"from message","session_id":"s1"}`) // TS: prompt ?? message
	assertRows(t, eventRows(t, env, "s1"),
		"user_prompt|user-prompt|1|hello|UserPromptSubmit|0",
		"intent|intent|4|implement|UserPromptSubmit|0", // short prompt; the second one is deduped
		"user_prompt|user-prompt|1|from message|UserPromptSubmit|0")

	env = hookEnv(t)
	callHook(t, env, "userpromptsubmit", `{"prompt":"<system-reminder>x","session_id":"s1"}`)
	noDB(t, env)
}

func TestHookStop(t *testing.T) {
	env := hookEnv(t)
	if out := callHook(t, env, "stop", `{"session_id":"s1","stop_hook_active":false,"last_assistant_message":"done"}`); out != "{}\n" {
		t.Fatalf("got %q", out)
	}
	if out := callHook(t, env, "stop", `{"session_id":"s1","stop_hook_active":true}`); out != "{}\n" {
		t.Fatalf("got %q", out)
	}
	assertRows(t, eventRows(t, env, "s1"),
		`turn_end|session|1|{"stop_hook_active":false,"last_assistant_message":"done"}|Stop|0`,
		`turn_end|session|1|{"stop_hook_active":true,"last_assistant_message":null}|Stop|0`)
}

func TestHookPreCompact(t *testing.T) {
	env := hookEnv(t)
	// A session with no events is left untouched (TS precompact.mjs:47).
	if out := callHook(t, env, "precompact", `{"session_id":"s1","trigger":"auto"}`); out != "{}\n" {
		t.Fatalf("got %q", out)
	}
	if rows := eventRows(t, env, "s1"); len(rows) != 0 {
		t.Fatalf("rows: %q", rows)
	}
	callHook(t, env, "posttooluse", `{"tool_name":"Read","tool_input":{"file_path":"/p/a.go"},"tool_response":"x","session_id":"s1"}`)
	callHook(t, env, "userpromptsubmit", `{"prompt":"hello","session_id":"s1"}`)
	if out := callHook(t, env, "precompact", `{"session_id":"s1","trigger":"manual"}`); out != "{}\n" {
		t.Fatalf("got %q", out)
	}
	assertRows(t, eventRows(t, env, "s1"),
		"file_read|file|1|/p/a.go|PostToolUse|0",
		"user_prompt|user-prompt|1|hello|UserPromptSubmit|0",
		"intent|intent|4|implement|UserPromptSubmit|0",
		"compaction_summary|compaction|1|Session compacted. 3 events, 1 files touched.|PreCompact|0")
	if s := hookErrors(env); s != "" {
		t.Fatalf("errors logged: %s", s)
	}

	// Like stop, a failed DB open still prints {}.
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	env["CMGO_DIR"] = filepath.Join(blocker, "data") // parent is a regular file
	if out := callHook(t, env, "precompact", `{"session_id":"s1"}`); out != "{}\n" {
		t.Fatalf("no DB: got %q", out)
	}
}

// install writes $CLAUDE_CONFIG_DIR/settings.json, so a deny rule there must
// be enforced even though HOME/.claude has none.
func TestHookPreToolUseDenyFromClaudeConfigDir(t *testing.T) {
	env := hookEnv(t)
	cfg := filepath.Join(t.TempDir(), "claudecfg")
	os.MkdirAll(cfg, 0o755)
	os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(`{"permissions":{"deny":["Bash(rm -rf *)"]}}`), 0o644)
	env["CLAUDE_CONFIG_DIR"] = cfg
	out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","tool_input":{"command":"rm -rf /x"},"session_id":"s1"}`)
	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatalf("got %q", out)
	}
}

// TS stop.mjs prints {} after its try/catch, so a failed DB write must not
// swallow the output. The error is still logged where it can be.
func TestHookStopPrintsWhenDBWriteFails(t *testing.T) {
	env := hookEnv(t)
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	env["CMGO_DIR"] = filepath.Join(blocker, "data") // parent is a regular file
	if out := callHook(t, env, "stop", `{"session_id":"s1","stop_hook_active":false}`); out != "{}\n" {
		t.Fatalf("got %q", out)
	}

	// With a usable data dir but a locked DB the line is logged.
	env = hookEnv(t)
	callHook(t, env, "stop", `{"session_id":"s1"}`)
	holdDBLock(t, filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if out := callHook(t, env, "stop", `{"session_id":"s1"}`); out != "{}\n" {
		t.Fatalf("locked: got %q", out)
	}
	if log, _ := os.ReadFile(filepath.Join(env["CMGO_DIR"], "hook-errors.log")); !strings.Contains(string(log), "stop:") {
		t.Fatalf("no logged error: %q", log)
	}
}

// TS decides "subagent" with `agent_id != null`, so a null field is a main
// session call; anything else, an empty string included, is a subagent.
func TestHookNullAgentFieldsAreNotSubagent(t *testing.T) {
	env := hookEnv(t)
	remove, err := routing.WriteSentinel(filepath.Join(env["CMGO_DIR"], "run"), os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	for _, c := range []struct {
		fields   string
		redirect bool
	}{
		{`"agent_id":null`, true},
		{`"agent_type":null`, true},
		{`"agent_id":null,"agent_type":null`, true},
		{`"agent_id":null,"agent_type":"general-purpose"`, false},
		{`"agent_id":""`, false},
		{`"agent_id":"a1"`, false},
	} {
		out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","tool_input":{"command":"curl https://x.com"},"session_id":"s1",`+c.fields+`}`)
		if got := strings.Contains(out, "curl/wget redirected"); got != c.redirect {
			t.Errorf("%s: redirected=%v, want %v (out %q)", c.fields, got, c.redirect, out)
		}
	}
}
```

`internal/hookcmd/lock_test.go`:
```go
package hookcmd

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
)

// holdDBLock keeps BEGIN IMMEDIATE on the hook DB from a second connection.
func holdDBLock(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: path}).String()+"?_pragma=busy_timeout(100)")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.ExecContext(context.Background(), "ROLLBACK")
		conn.Close()
		db.Close()
	})
}

// Claude Code waits for the hook process to exit, so a DB that another
// session holds locked must cost a hook seconds, not the 30 s server timeout.
func TestHookLockedDBFailsOpenFast(t *testing.T) {
	env := hookEnv(t)
	callHook(t, env, "posttooluse", `{"session_id":"s1","tool_name":"Read","tool_input":{"file_path":"/p/a.go"}}`)
	holdDBLock(t, filepath.Join(env["CMGO_DIR"], "cmgo.db"))

	for _, c := range []struct{ event, stdin string }{
		{"posttooluse", `{"session_id":"s1","tool_name":"Read","tool_input":{"file_path":"/p/b.go"}}`},
		{"stop", `{"session_id":"s1","stop_hook_active":false}`},
		{"sessionstart", `{"session_id":"s2","source":"clear"}`},
	} {
		done := make(chan struct{})
		start := time.Now()
		go func() { defer close(done); callHook(t, env, c.event, c.stdin) }()
		select {
		case <-done:
			if d := time.Since(start); d > 3*time.Second {
				t.Errorf("%s took %v with the DB write-locked", c.event, d)
			}
		case <-time.After(6 * time.Second):
			t.Fatalf("%s still running after 6s with the DB write-locked", c.event)
		}
	}
	log, err := os.ReadFile(filepath.Join(env["CMGO_DIR"], "hook-errors.log"))
	if err != nil || strings.Count(string(log), "\n") != 3 {
		t.Fatalf("want 3 logged errors, got %q (%v)", log, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hookcmd/ -count=1`
Expected: FAIL with `internal/hookcmd/sessioncc_test.go:16:9: undefined: procLookup`

- [ ] **Step 3: Write minimal implementation**

`sessionStart` prints first, then opens the store for every source; `recordSession` writes `cc_pid` before the events, and `sessionResume` ends with it too.

`internal/hookcmd/hookcmd.go`:
```go
// Package hookcmd runs one Claude Code hook fire (hooks spec §1-§4). It is
// what cmgo-hook executes; it links no MCP server, executor or fetcher, so a
// hook process starts fast.
package hookcmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/lang315/context-mode-go/internal/capture"
	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/memory"
	"github.com/lang315/context-mode-go/internal/procinfo"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
)

// stdinTimeout bounds the stdin read (spec §1.2). It is a fallback: Claude
// Code closes stdin. A var so the test need not wait 2 s.
var stdinTimeout = 2 * time.Second

// dbBusyTimeout bounds every wait for the shared DB's lock. Claude Code waits
// for the hook process to exit, so on BUSY the hook drops its events and
// logs one line (spec §1.2) instead of stalling the session.
const dbBusyTimeout = 2 * time.Second

// procLookup reads the process table for the Claude Code pid that
// SessionStart records; a var so tests need not run under Claude Code.
var procLookup = procinfo.System()

// guidanceMaxAge is how long a guidance marker dir is kept (spec §4).
const guidanceMaxAge = 7 * 24 * time.Hour

// IO is what a hook fire talks over; tests inject env, cwd, stdin and clock.
type IO struct {
	Getenv func(string) string
	Getwd  func() (string, error)
	Stdin  io.Reader
	Stdout io.Writer
	Now    func() time.Time
}

// hookCtx is one parsed hook fire.
type hookCtx struct {
	in     *hookio.Object
	cfg    config.Config
	sid    string
	runDir string // $CMGO_DIR/run: guidance markers and MCP sentinels
	getenv func(string) string
	out    io.Writer
	now    time.Time
}

// hookHandlers maps a hook event name to its handler.
var hookHandlers = map[string]func(*hookCtx) error{
	"pretooluse":         preToolUse,
	"posttooluse":        postToolUse,
	"posttoolusefailure": postToolUseFailure,
	"userpromptsubmit":   userPromptSubmit,
	"stop":               stop,
	"sessionstart":       sessionStart,
	"precompact":         preCompact,
}

// Run runs one hook fire and always exits 0 (spec §1.2): an error or panic
// is appended to $CMGO_DIR/hook-errors.log and nothing more is printed.
func Run(event string, hio IO) int {
	h, ok := hookHandlers[event]
	if !ok {
		return 0
	}
	defer func() {
		if r := recover(); r != nil {
			logHookError(hio.Getenv, event, fmt.Errorf("panic: %v", r))
		}
	}()
	if err := runHook(h, hio); err != nil {
		logHookError(hio.Getenv, event, err)
	}
	return 0
}

func runHook(h func(*hookCtx) error, hio IO) error {
	raw, err := readStdin(hio.Stdin)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil // spec §1.2: empty stdin, no output
	}
	in, err := hookio.ParseObject(raw)
	if err != nil {
		return err
	}
	getwd := hio.Getwd
	if cwd, ok := in.String("cwd"); ok && cwd != "" {
		getwd = func() (string, error) { return cwd, nil }
	}
	cfg, err := config.Load(hio.Getenv, getwd)
	if err != nil {
		return err
	}
	return h(&hookCtx{
		in:     in,
		cfg:    cfg,
		sid:    hookio.SessionID(in, hio.Getenv, os.Getppid()),
		runDir: filepath.Join(cfg.DataDir, "run"),
		getenv: hio.Getenv,
		out:    hio.Stdout,
		now:    hio.Now(),
	})
}

// readStdin reads the whole payload, giving up after stdinTimeout. The
// reader goroutine is left blocked on timeout; the process exits anyway.
func readStdin(r io.Reader) ([]byte, error) {
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := hookio.ReadInput(r)
		ch <- result{b, err}
	}()
	select {
	case res := <-ch:
		return res.b, res.err
	case <-time.After(stdinTimeout):
		return nil, errors.New("stdin: no end of input within " + stdinTimeout.String())
	}
}

// logHookError appends one line to $CMGO_DIR/hook-errors.log, best effort.
func logHookError(getenv func(string) string, event string, err error) {
	// The project dir does not affect DataDir, so a failed parse can still log.
	cfg, cerr := config.Load(getenv, func() (string, error) { return "/", nil })
	if cerr != nil {
		return
	}
	if os.MkdirAll(cfg.DataDir, 0o755) != nil {
		return
	}
	f, ferr := os.OpenFile(filepath.Join(cfg.DataDir, "hook-errors.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s pid=%d %s: %v\n", time.Now().Format(time.RFC3339), os.Getpid(), event, err)
}

// insertEvents writes evs in one transaction; with none it does not open the
// store at all (spec §1.1).
func (h *hookCtx) insertEvents(sourceHook string, evs []store.Event) error {
	if len(evs) == 0 {
		return nil
	}
	st, err := store.OpenWith(h.cfg.DBPath, store.Options{BusyTimeout: dbBusyTimeout})
	if err != nil {
		return err
	}
	defer st.Close()
	_, err = st.InsertEvents(h.sid, h.cfg.ProjectKey, sourceHook, evs)
	return err
}

// notNull is JS `input[key] != null`: present and not JSON null.
func notNull(in *hookio.Object, key string) bool {
	raw, ok := in.Get(key)
	return ok && string(bytes.TrimSpace(raw)) != "null"
}

// preToolUse routes the call (spec §2) and writes the decision before it
// opens the store, so the DB never delays Claude Code.
func preToolUse(h *hookCtx) error {
	tool, _ := h.in.String("tool_name")
	input, ok := h.in.Object("tool_input")
	if !ok {
		input = &hookio.Object{}
	}
	subagent := notNull(h.in, "agent_id") || notNull(h.in, "agent_type")
	dec := routing.Route(tool, input, subagent, routing.Deps{
		ProjectDir: h.cfg.ProjectDir,
		SessionID:  h.sid,
		RunDir:     h.runDir,
		Policy:     security.NewPolicyDir(h.cfg.ProjectDir, h.cfg.ClaudeDir),
		Getenv:     h.getenv,
		MCPReady:   func() bool { return routing.MCPReady(h.runDir) },
	})
	if out := routing.Format(dec); out != nil {
		if _, err := h.out.Write(out); err != nil {
			return err
		}
	}
	return h.insertEvents("PreToolUse", routing.Events(tool, dec))
}

// postToolUse records the kept extractors' events (spec §3.3).
func postToolUse(h *hookCtx) error {
	tool, _ := h.in.String("tool_name")
	input, _ := h.in.Object("tool_input")
	resp, _ := h.in.Get("tool_response") // nil when absent
	return h.insertEvents("PostToolUse", capture.PostToolUse(tool, input, resp, h.cfg.Home))
}

func postToolUseFailure(h *hookCtx) error {
	tool, _ := h.in.String("tool_name")
	return h.insertEvents("PostToolUseFailure", capture.PostToolUseFailure(tool, h.in))
}

func userPromptSubmit(h *hookCtx) error {
	prompt, ok := h.in.String("prompt")
	if !ok {
		prompt, _ = h.in.String("message") // TS: input.prompt ?? input.message ?? ""
	}
	return h.insertEvents("UserPromptSubmit", capture.UserPrompt(prompt))
}

// stop always records turn_end, then prints {} as TS stop.mjs does.
func stop(h *hookCtx) error {
	active, _ := h.in.Bool("stop_hook_active")
	var msg *string
	if s, ok := h.in.String("last_assistant_message"); ok {
		msg = &s
	}
	// TS stop.mjs prints after its try/catch: a failed write must not swallow
	// the output, so print first and still return the write error to be logged.
	_, werr := io.WriteString(h.out, "{}\n")
	if err := h.insertEvents("Stop", []store.Event{capture.Stop(active, msg)}); err != nil {
		return err
	}
	return werr
}

// preCompact records the compaction (spec §2.1) and prints {} as TS
// precompact.mjs does, first, so a DB error cannot swallow it.
func preCompact(h *hookCtx) error {
	_, werr := io.WriteString(h.out, "{}\n")
	st, err := store.OpenWith(h.cfg.DBPath, store.Options{BusyTimeout: dbBusyTimeout})
	if err != nil {
		return err
	}
	defer st.Close()
	if _, _, err := st.CompactSession(h.sid, h.cfg.ProjectKey); err != nil {
		return err
	}
	return werr
}

// sessionStart prints the routing block for every source (spec §4), then
// records the session's Claude Code pid (stats spec §1.2). Startup also
// prunes old guidance markers and records the rule and session_start events;
// compact and resume add the session's memory (spec §2.2).
func sessionStart(h *hookCtx) error {
	block := routing.RoutingBlock(true, false)
	src, _ := h.in.String("source")
	if raw, ok := h.in.Get("source"); !ok || string(raw) == "null" {
		src = "startup" // TS: input.source ?? "startup"
	}
	if src == "compact" || src == "resume" {
		return h.sessionResume(src, block)
	}
	if err := h.printSessionStart(block); err != nil {
		return err
	}
	var evs []store.Event
	if src == "startup" {
		routing.PruneGuidance(h.runDir, h.now, guidanceMaxAge)
		evs = append(capture.SessionStartRules(h.cfg.Home, h.cfg.ProjectDir), capture.SessionStartEvent(src, h.cfg.ProjectDir, h.now))
	}
	st, err := store.OpenWith(h.cfg.DBPath, store.Options{BusyTimeout: dbBusyTimeout})
	if err != nil {
		return err
	}
	defer st.Close()
	return h.recordSession(st, evs)
}

// recordSession writes the session's Claude Code pid first: a server call
// written before it is tagged with the previous session's window (stats spec
// §1.2). Then it records evs.
func (h *hookCtx) recordSession(st *store.Store, evs []store.Event) error {
	if err := st.SetSessionCC(h.sid, h.cfg.ProjectKey, procinfo.ClaudePID(os.Getpid(), procLookup)); err != nil {
		return err
	}
	_, err := st.InsertEvents(h.sid, h.cfg.ProjectKey, "SessionStart", evs)
	return err
}

func (h *hookCtx) printSessionStart(context string) error {
	_, err := io.WriteString(h.out, `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":`+
		string(hookio.QuoteString(context))+"}}\n")
	return err
}

// sessionResume appends the directive (and, on compact, the auto-injection)
// built from the session's events to block, then records the resume events,
// as TS sessionstart.mjs:191-280 does. TS also indexed an events markdown;
// cmgo does not (spec §2.3). The directive needs the events, so the read
// comes before the output; a failed read prints block alone. Writes come
// after the output.
func (h *hookCtx) sessionResume(src, block string) error {
	st, err := store.OpenWith(h.cfg.DBPath, store.Options{BusyTimeout: dbBusyTimeout})
	if err != nil {
		return errors.Join(err, h.printSessionStart(block))
	}
	defer st.Close()
	evs, err := st.SessionEvents(h.sid)
	if err != nil {
		return errors.Join(err, h.printSessionStart(block))
	}
	context := block + memory.Directive(src, evs) // "" with no events
	var add []store.Event
	if len(evs) > 0 && src == "compact" {
		if ai := memory.AutoInjection(evs); ai != "" {
			context += "\n\n" + ai
		}
		add = append(add, store.Event{Type: "resume_completed", Category: "session-resume", Priority: 1,
			Data: "Session resumed from compact. Prior events loaded."})
	}
	if err := h.printSessionStart(context); err != nil {
		return err
	}
	return h.recordSession(st, append(add, capture.SessionStartEvent(src, h.cfg.ProjectDir, h.now)))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/hookcmd/ -count=1 && go test -race ./internal/hookcmd/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/hookcmd/hookcmd.go internal/hookcmd/sessioncc_test.go internal/hookcmd/hookcmd_test.go internal/hookcmd/lock_test.go
git commit -m "feat(hookcmd): record the Claude Code pid on every SessionStart"
```

### Task 17: The statusline event

**Files:**
- Create: `internal/hookcmd/statusline.go`
- Modify: `internal/hookcmd/hookcmd.go`
- Test: `internal/hookcmd/statusline_test.go`
- Test: `cmd/cmgo/statusline_exec_test.go`

**Interfaces:**
- Consumes: `store.OpenReadOnly(path string) (*Store, error)`, `(*Store).UserVersion() (int, error)`, `(*Store).SessionLine(sessionID string) (sessionKept, sessionRaw, lifetime int64, err error)` (fragment S4A); `stats.Line`, `stats.StatusLine(l Line) string`, `stats.Pct(kept, raw int64) int` (fragment STR); `readStdin`, `config.Load`, `hookio.ParseObject` (existing)
- Produces: `hookcmd.Run("statusline", hio)` prints one line with no newline and returns 0, so `cmgo-hook statusline` and `cmgo hook statusline` work; unexported `func statusLine(hio IO)`, `func statusNumbers(hio IO) (l stats.Line)`

- [ ] **Step 1: Write the failing test**

The unit test keeps a writer open (so the `-wal` file exists) and checks every case, then that the DB and `-wal` sizes and mtimes did not change. The built-binary test checks the fallback on an empty data dir (which must stay absent), then the session line after a real SessionStart and one recorded server call.

`internal/hookcmd/statusline_test.go`:
```go
package hookcmd

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

const ready = "cmgo  ●  ready"

// statusDB opens a writer on the hook DB with session s1 (cc_pid 100) whose
// server kept 90 of 100 bytes, and another session's 1000 kept bytes. The
// writer stays open until the test ends, so the -wal file exists.
func statusDB(t *testing.T, env map[string]string) string {
	t.Helper()
	path := filepath.Join(env["CMGO_DIR"], "cmgo.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, err := range []error{
		st.SetSessionCC("s1", "/p", 100),
		st.AddToolStat("srv", 100, "/p", "2000-01-01 00:00:00", "ctx_execute", 10, 90),
		st.AddToolStat("other", 200, "/q", "2000-01-01 00:00:00", "ctx_execute", 0, 1000),
		st.SetSessionCC("s2", "/p", 300),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// fileState is a file's size and mtime, or "absent".
func fileState(t *testing.T, path string) string {
	t.Helper()
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "absent"
	}
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d bytes, %v", fi.Size(), fi.ModTime())
}

func TestStatusLine(t *testing.T) {
	env := hookEnv(t)
	path := statusDB(t, env)
	cases := []struct{ name, sessionEnv, stdin, want string }{
		{"session", "", `{"session_id":"s1","cwd":"/x"}`, "cmgo  ●  90 B this chat  ·  1.1 KB lifetime  ·  90% kept out"},
		{"env session", "s1", `{"cwd":"/x"}`, "cmgo  ●  90 B this chat  ·  1.1 KB lifetime  ·  90% kept out"},
		{"stdin wins over env", "s2", `{"session_id":"s1"}`, "cmgo  ●  90 B this chat  ·  1.1 KB lifetime  ·  90% kept out"},
		{"empty stdin, env session", "s1", "", "cmgo  ●  90 B this chat  ·  1.1 KB lifetime  ·  90% kept out"},
		{"malformed stdin, env session", "s1", "{nope", "cmgo  ●  90 B this chat  ·  1.1 KB lifetime  ·  90% kept out"},
		{"session without server rows", "", `{"session_id":"s2"}`, "cmgo  ●  1.1 KB kept out lifetime"},
		{"unknown session", "", `{"session_id":"nope"}`, "cmgo  ●  1.1 KB kept out lifetime"},
		{"no session", "", `{"cwd":"/x"}`, ready},
		{"empty stdin, no session", "", "", ready},
	}
	if fileState(t, path+"-wal") == "absent" {
		t.Fatal("no -wal file: the no-write check would prove nothing")
	}
	before := fileState(t, path) + " | " + fileState(t, path+"-wal")
	for _, c := range cases {
		e := map[string]string{"HOME": env["HOME"], "CMGO_DIR": env["CMGO_DIR"], "CLAUDE_SESSION_ID": c.sessionEnv}
		if got := callHook(t, e, "statusline", c.stdin); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	// Spec criterion 3: the statusline never writes, even while a writer
	// holds the DB open.
	if after := fileState(t, path) + " | " + fileState(t, path+"-wal"); after != before {
		t.Fatalf("DB files changed:\nbefore %s\nafter  %s", before, after)
	}
	if log := hookErrors(env); log != "" {
		t.Fatalf("hook errors: %s", log)
	}
}

// A missing DB is the fallback and creates nothing: no data dir, no log.
func TestStatusLineNoDB(t *testing.T) {
	env := hookEnv(t)
	if got := callHook(t, env, "statusline", `{"session_id":"s1"}`); got != ready {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(env["CMGO_DIR"]); !os.IsNotExist(err) {
		t.Fatalf("data dir created: %v", err)
	}
}

// A DB older than migration 3 has no tool_stats: fallback, not an error.
func TestStatusLineOldSchema(t *testing.T) {
	env := hookEnv(t)
	path := filepath.Join(env["CMGO_DIR"], "cmgo.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	db, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: path}).String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA user_version = 2"); err != nil {
		t.Fatal(err)
	}
	if got := callHook(t, env, "statusline", `{"session_id":"s1"}`); got != ready {
		t.Fatalf("got %q", got)
	}
	if log := hookErrors(env); log != "" {
		t.Fatalf("hook errors: %s", log)
	}
}
```

`cmd/cmgo/statusline_exec_test.go`:
```go
package main

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/procinfo"
	"github.com/lang315/context-mode-go/internal/store"
)

// Stats spec §3 through the built binary: the fallback on an empty data dir
// (which it must not create), then the session line once SessionStart has
// linked the session and its server has recorded a call.
func TestBinaryStatusLine(t *testing.T) {
	env := hookEnv(t)
	if out, code := runHookBinary(t, env, `{"session_id":"s1"}`, "statusline"); code != 0 || out != "cmgo  ●  ready" {
		t.Fatalf("empty data dir: exit %d, stdout %q", code, out)
	}
	if _, err := os.Stat(env["CMGO_DIR"]); !os.IsNotExist(err) {
		t.Fatalf("statusline created the data dir: %v", err)
	}
	if _, code := runHookBinary(t, env, `{"session_id":"s1","source":"startup"}`, "sessionstart"); code != 0 {
		t.Fatalf("sessionstart: exit %d", code)
	}

	// The hook is this test's child, so it finds the same Claude Code
	// ancestor as this process: a real pid under Claude Code, else 0.
	path := filepath.Join(env["CMGO_DIR"], "cmgo.db")
	db, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: path}).String()+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var cc int
	if err := db.QueryRow("SELECT cc_pid FROM session_meta WHERE session_id = 's1'").Scan(&cc); err != nil {
		t.Fatal(err)
	}
	if want := procinfo.ClaudePID(os.Getpid(), procinfo.System()); cc != want {
		t.Fatalf("recorded cc_pid %d, want %d", cc, want)
	}
	cfg, err := config.Load(func(k string) string { return env[k] }, os.Getwd)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if cc == 0 { // cc_pid 0 never links; stand in for Claude Code
		cc = 4242
		if err := st.SetSessionCC("s1", cfg.ProjectKey, cc); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AddToolStat("srv", cc, cfg.ProjectKey, "2000-01-01 00:00:00", "ctx_execute", 1000, 9000); err != nil {
		t.Fatal(err)
	}
	want := "cmgo  ●  8.8 KB this chat  ·  8.8 KB lifetime  ·  90% kept out"
	if out, code := runHookBinary(t, env, `{"session_id":"s1"}`, "statusline"); code != 0 || out != want {
		t.Fatalf("exit %d, stdout %q, want %q", code, out, want)
	}
	if log := hookErrors(env); log != "" {
		t.Fatalf("hook errors: %s", log)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hookcmd/ ./cmd/cmgo/ -run 'StatusLine' -count=1`
Expected: FAIL with `statusline_test.go:74: session: got "", want "cmgo  ●  90 B this chat  ·  1.1 KB lifetime  ·  90% kept out"` and `statusline_exec_test.go:23: empty data dir: exit 0, stdout ""` (an unknown event prints nothing)

- [ ] **Step 3: Write minimal implementation**

`internal/hookcmd/statusline.go`:
```go
package hookcmd

import (
	"io"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/stats"
	"github.com/lang315/context-mode-go/internal/store"
)

// statusLine prints Claude Code's statusLine line (stats spec §3), with no
// trailing newline. It is not a hook: it prints even on empty stdin, and it
// neither logs nor writes, because Claude Code runs it several times a second.
func statusLine(hio IO) {
	io.WriteString(hio.Stdout, stats.StatusLine(statusNumbers(hio)))
}

// statusNumbers reads the line's numbers through a read-only store. Every
// failure, a missing DB or session included, is the zero Line: the fallback.
func statusNumbers(hio IO) (l stats.Line) {
	defer func() {
		if recover() != nil {
			l = stats.Line{}
		}
	}()
	raw, err := readStdin(hio.Stdin)
	if err != nil {
		return l
	}
	sid := hio.Getenv("CLAUDE_SESSION_ID")
	if in, err := hookio.ParseObject(raw); err == nil {
		if s, ok := in.String("session_id"); ok && s != "" {
			sid = s
		}
	}
	if sid == "" {
		return l
	}
	// The project does not affect DBPath, so the cwd is not needed.
	cfg, err := config.Load(hio.Getenv, func() (string, error) { return "/", nil })
	if err != nil {
		return l
	}
	st, err := store.OpenReadOnly(cfg.DBPath)
	if err != nil {
		return l
	}
	defer st.Close()
	if v, err := st.UserVersion(); err != nil || v < 3 { // migration 3 adds tool_stats
		return l
	}
	kept, total, lifetime, err := st.SessionLine(sid)
	if err != nil {
		return l
	}
	return stats.Line{OK: true, SessionKept: kept, LifetimeKept: lifetime, Pct: stats.Pct(kept, total)}
}
```

`internal/hookcmd/hookcmd.go`:
```go
// Package hookcmd runs one Claude Code hook fire (hooks spec §1-§4). It is
// what cmgo-hook executes; it links no MCP server, executor or fetcher, so a
// hook process starts fast.
package hookcmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/lang315/context-mode-go/internal/capture"
	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/memory"
	"github.com/lang315/context-mode-go/internal/procinfo"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
)

// stdinTimeout bounds the stdin read (spec §1.2). It is a fallback: Claude
// Code closes stdin. A var so the test need not wait 2 s.
var stdinTimeout = 2 * time.Second

// dbBusyTimeout bounds every wait for the shared DB's lock. Claude Code waits
// for the hook process to exit, so on BUSY the hook drops its events and
// logs one line (spec §1.2) instead of stalling the session.
const dbBusyTimeout = 2 * time.Second

// procLookup reads the process table for the Claude Code pid that
// SessionStart records; a var so tests need not run under Claude Code.
var procLookup = procinfo.System()

// guidanceMaxAge is how long a guidance marker dir is kept (spec §4).
const guidanceMaxAge = 7 * 24 * time.Hour

// IO is what a hook fire talks over; tests inject env, cwd, stdin and clock.
type IO struct {
	Getenv func(string) string
	Getwd  func() (string, error)
	Stdin  io.Reader
	Stdout io.Writer
	Now    func() time.Time
}

// hookCtx is one parsed hook fire.
type hookCtx struct {
	in     *hookio.Object
	cfg    config.Config
	sid    string
	runDir string // $CMGO_DIR/run: guidance markers and MCP sentinels
	getenv func(string) string
	out    io.Writer
	now    time.Time
}

// hookHandlers maps a hook event name to its handler.
var hookHandlers = map[string]func(*hookCtx) error{
	"pretooluse":         preToolUse,
	"posttooluse":        postToolUse,
	"posttoolusefailure": postToolUseFailure,
	"userpromptsubmit":   userPromptSubmit,
	"stop":               stop,
	"sessionstart":       sessionStart,
	"precompact":         preCompact,
}

// Run runs one hook fire and always exits 0 (spec §1.2): an error or panic
// is appended to $CMGO_DIR/hook-errors.log and nothing more is printed. The
// statusline event is not a hook fire and has its own rules (statusLine).
func Run(event string, hio IO) int {
	if event == "statusline" {
		statusLine(hio)
		return 0
	}
	h, ok := hookHandlers[event]
	if !ok {
		return 0
	}
	defer func() {
		if r := recover(); r != nil {
			logHookError(hio.Getenv, event, fmt.Errorf("panic: %v", r))
		}
	}()
	if err := runHook(h, hio); err != nil {
		logHookError(hio.Getenv, event, err)
	}
	return 0
}

func runHook(h func(*hookCtx) error, hio IO) error {
	raw, err := readStdin(hio.Stdin)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil // spec §1.2: empty stdin, no output
	}
	in, err := hookio.ParseObject(raw)
	if err != nil {
		return err
	}
	getwd := hio.Getwd
	if cwd, ok := in.String("cwd"); ok && cwd != "" {
		getwd = func() (string, error) { return cwd, nil }
	}
	cfg, err := config.Load(hio.Getenv, getwd)
	if err != nil {
		return err
	}
	return h(&hookCtx{
		in:     in,
		cfg:    cfg,
		sid:    hookio.SessionID(in, hio.Getenv, os.Getppid()),
		runDir: filepath.Join(cfg.DataDir, "run"),
		getenv: hio.Getenv,
		out:    hio.Stdout,
		now:    hio.Now(),
	})
}

// readStdin reads the whole payload, giving up after stdinTimeout. The
// reader goroutine is left blocked on timeout; the process exits anyway.
func readStdin(r io.Reader) ([]byte, error) {
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := hookio.ReadInput(r)
		ch <- result{b, err}
	}()
	select {
	case res := <-ch:
		return res.b, res.err
	case <-time.After(stdinTimeout):
		return nil, errors.New("stdin: no end of input within " + stdinTimeout.String())
	}
}

// logHookError appends one line to $CMGO_DIR/hook-errors.log, best effort.
func logHookError(getenv func(string) string, event string, err error) {
	// The project dir does not affect DataDir, so a failed parse can still log.
	cfg, cerr := config.Load(getenv, func() (string, error) { return "/", nil })
	if cerr != nil {
		return
	}
	if os.MkdirAll(cfg.DataDir, 0o755) != nil {
		return
	}
	f, ferr := os.OpenFile(filepath.Join(cfg.DataDir, "hook-errors.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s pid=%d %s: %v\n", time.Now().Format(time.RFC3339), os.Getpid(), event, err)
}

// insertEvents writes evs in one transaction; with none it does not open the
// store at all (spec §1.1).
func (h *hookCtx) insertEvents(sourceHook string, evs []store.Event) error {
	if len(evs) == 0 {
		return nil
	}
	st, err := store.OpenWith(h.cfg.DBPath, store.Options{BusyTimeout: dbBusyTimeout})
	if err != nil {
		return err
	}
	defer st.Close()
	_, err = st.InsertEvents(h.sid, h.cfg.ProjectKey, sourceHook, evs)
	return err
}

// notNull is JS `input[key] != null`: present and not JSON null.
func notNull(in *hookio.Object, key string) bool {
	raw, ok := in.Get(key)
	return ok && string(bytes.TrimSpace(raw)) != "null"
}

// preToolUse routes the call (spec §2) and writes the decision before it
// opens the store, so the DB never delays Claude Code.
func preToolUse(h *hookCtx) error {
	tool, _ := h.in.String("tool_name")
	input, ok := h.in.Object("tool_input")
	if !ok {
		input = &hookio.Object{}
	}
	subagent := notNull(h.in, "agent_id") || notNull(h.in, "agent_type")
	dec := routing.Route(tool, input, subagent, routing.Deps{
		ProjectDir: h.cfg.ProjectDir,
		SessionID:  h.sid,
		RunDir:     h.runDir,
		Policy:     security.NewPolicyDir(h.cfg.ProjectDir, h.cfg.ClaudeDir),
		Getenv:     h.getenv,
		MCPReady:   func() bool { return routing.MCPReady(h.runDir) },
	})
	if out := routing.Format(dec); out != nil {
		if _, err := h.out.Write(out); err != nil {
			return err
		}
	}
	return h.insertEvents("PreToolUse", routing.Events(tool, dec))
}

// postToolUse records the kept extractors' events (spec §3.3).
func postToolUse(h *hookCtx) error {
	tool, _ := h.in.String("tool_name")
	input, _ := h.in.Object("tool_input")
	resp, _ := h.in.Get("tool_response") // nil when absent
	return h.insertEvents("PostToolUse", capture.PostToolUse(tool, input, resp, h.cfg.Home))
}

func postToolUseFailure(h *hookCtx) error {
	tool, _ := h.in.String("tool_name")
	return h.insertEvents("PostToolUseFailure", capture.PostToolUseFailure(tool, h.in))
}

func userPromptSubmit(h *hookCtx) error {
	prompt, ok := h.in.String("prompt")
	if !ok {
		prompt, _ = h.in.String("message") // TS: input.prompt ?? input.message ?? ""
	}
	return h.insertEvents("UserPromptSubmit", capture.UserPrompt(prompt))
}

// stop always records turn_end, then prints {} as TS stop.mjs does.
func stop(h *hookCtx) error {
	active, _ := h.in.Bool("stop_hook_active")
	var msg *string
	if s, ok := h.in.String("last_assistant_message"); ok {
		msg = &s
	}
	// TS stop.mjs prints after its try/catch: a failed write must not swallow
	// the output, so print first and still return the write error to be logged.
	_, werr := io.WriteString(h.out, "{}\n")
	if err := h.insertEvents("Stop", []store.Event{capture.Stop(active, msg)}); err != nil {
		return err
	}
	return werr
}

// preCompact records the compaction (spec §2.1) and prints {} as TS
// precompact.mjs does, first, so a DB error cannot swallow it.
func preCompact(h *hookCtx) error {
	_, werr := io.WriteString(h.out, "{}\n")
	st, err := store.OpenWith(h.cfg.DBPath, store.Options{BusyTimeout: dbBusyTimeout})
	if err != nil {
		return err
	}
	defer st.Close()
	if _, _, err := st.CompactSession(h.sid, h.cfg.ProjectKey); err != nil {
		return err
	}
	return werr
}

// sessionStart prints the routing block for every source (spec §4), then
// records the session's Claude Code pid (stats spec §1.2). Startup also
// prunes old guidance markers and records the rule and session_start events;
// compact and resume add the session's memory (spec §2.2).
func sessionStart(h *hookCtx) error {
	block := routing.RoutingBlock(true, false)
	src, _ := h.in.String("source")
	if raw, ok := h.in.Get("source"); !ok || string(raw) == "null" {
		src = "startup" // TS: input.source ?? "startup"
	}
	if src == "compact" || src == "resume" {
		return h.sessionResume(src, block)
	}
	if err := h.printSessionStart(block); err != nil {
		return err
	}
	var evs []store.Event
	if src == "startup" {
		routing.PruneGuidance(h.runDir, h.now, guidanceMaxAge)
		evs = append(capture.SessionStartRules(h.cfg.Home, h.cfg.ProjectDir), capture.SessionStartEvent(src, h.cfg.ProjectDir, h.now))
	}
	st, err := store.OpenWith(h.cfg.DBPath, store.Options{BusyTimeout: dbBusyTimeout})
	if err != nil {
		return err
	}
	defer st.Close()
	return h.recordSession(st, evs)
}

// recordSession writes the session's Claude Code pid first: a server call
// written before it is tagged with the previous session's window (stats spec
// §1.2). Then it records evs.
func (h *hookCtx) recordSession(st *store.Store, evs []store.Event) error {
	if err := st.SetSessionCC(h.sid, h.cfg.ProjectKey, procinfo.ClaudePID(os.Getpid(), procLookup)); err != nil {
		return err
	}
	_, err := st.InsertEvents(h.sid, h.cfg.ProjectKey, "SessionStart", evs)
	return err
}

func (h *hookCtx) printSessionStart(context string) error {
	_, err := io.WriteString(h.out, `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":`+
		string(hookio.QuoteString(context))+"}}\n")
	return err
}

// sessionResume appends the directive (and, on compact, the auto-injection)
// built from the session's events to block, then records the resume events,
// as TS sessionstart.mjs:191-280 does. TS also indexed an events markdown;
// cmgo does not (spec §2.3). The directive needs the events, so the read
// comes before the output; a failed read prints block alone. Writes come
// after the output.
func (h *hookCtx) sessionResume(src, block string) error {
	st, err := store.OpenWith(h.cfg.DBPath, store.Options{BusyTimeout: dbBusyTimeout})
	if err != nil {
		return errors.Join(err, h.printSessionStart(block))
	}
	defer st.Close()
	evs, err := st.SessionEvents(h.sid)
	if err != nil {
		return errors.Join(err, h.printSessionStart(block))
	}
	context := block + memory.Directive(src, evs) // "" with no events
	var add []store.Event
	if len(evs) > 0 && src == "compact" {
		if ai := memory.AutoInjection(evs); ai != "" {
			context += "\n\n" + ai
		}
		add = append(add, store.Event{Type: "resume_completed", Category: "session-resume", Priority: 1,
			Data: "Session resumed from compact. Prior events loaded."})
	}
	if err := h.printSessionStart(context); err != nil {
		return err
	}
	return h.recordSession(st, append(add, capture.SessionStartEvent(src, h.cfg.ProjectDir, h.now)))
}
```

- [ ] **Step 4: Run test to verify it passes**

`cmd/cmgo-hook`'s `TestNoServerDeps` must stay green: `procinfo` adds only `golang.org/x/sys/unix` and `stats` only the standard library.

Run: `go test ./internal/hookcmd/ ./cmd/cmgo/ ./cmd/cmgo-hook/ -count=1 && go test -race ./internal/hookcmd/ -count=1 && ! go list -deps ./cmd/cmgo-hook | grep -q modelcontextprotocol`
Expected: PASS, and exit status 0

- [ ] **Step 5: Commit**

```bash
git add internal/hookcmd/statusline.go internal/hookcmd/hookcmd.go internal/hookcmd/statusline_test.go cmd/cmgo/statusline_exec_test.go
git commit -m "feat(hookcmd): add the read-only statusline event"
```

### Task 18: Install the statusLine

**Files:**
- Modify: `internal/install/hooks.go`
- Modify: `internal/install/install.go`
- Create: `internal/install/statusline_test.go`
- Test: `internal/install/install_test.go`

**Interfaces:**
- Consumes: `shellWord`, `hookCommand`, `newObject`, `(*object).get/set/del`, `settings` (existing)
- Produces: unexported `func ownArg(cmd string) string` (the parser `isOwnCommand` now wraps); `func addStatusLine(s *settings, o Options)`; `func isOwnStatusLine(sl *object) bool`; `func stripStatusLine(root *object) int`; `Install` and `Uninstall` handle `statusLine`

- [ ] **Step 1: Write the failing test**

The new tests cover the three cases: absent (set), foreign (kept, notice, kept by uninstall), cmgo's from an old path (replaced, removed by uninstall). In `install_test.go`, `freshHooks` now adds the statusLine, and `TestInstallKeepsUserHooks` expects it as the last key.

`internal/install/statusline_test.go`:
```go
package install

import (
	"strings"
	"testing"
)

// statusLineOf returns the settings file's statusLine value as JSON, or "".
func statusLineOf(t *testing.T, content string) string {
	t.Helper()
	v, ok := mustParse(t, content).get("statusLine")
	if !ok {
		return ""
	}
	return strings.TrimSuffix(string(encodeJSON(v)), "\n")
}

func TestIsOwnStatusLine(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{`'/opt/cmgo/bin/cmgo-hook' statusline`, true},
		{`"/old place/cmgo-hook" statusline`, true},
		{`cmgo-hook statusline`, true},
		{`/x/cmgo hook statusline`, true},
		{`'/x/cmgo-hook' statusline --x`, false},
		{`'/x/cmgo-hook' stop`, false},
		{`/x/mine.sh statusline`, false},
		{`npx ccstatusline`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := ownArg(c.cmd) == "statusline"; got != c.want {
			t.Errorf("%q: %v, want %v", c.cmd, got, c.want)
		}
	}
}

// Stats spec §4: an absent statusLine becomes cmgo's.
func TestInstallSetsStatusLine(t *testing.T) {
	o, f := newOpts(t)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"type\": \"command\",\n  \"command\": \"/opt/cmgo/bin/cmgo-hook statusline\"\n}"
	if got := statusLineOf(t, readFile(t, o.SettingsPath)); got != want {
		t.Fatalf("statusLine\n%s\nwant\n%s", got, want)
	}
	if f.stderr.Len() != 0 {
		t.Fatalf("stderr %q", f.stderr.String())
	}
}

// Another statusline is kept, with a notice; uninstall keeps it too.
func TestInstallKeepsForeignStatusLine(t *testing.T) {
	o, f := newOpts(t)
	orig := "{\n  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": \"~/bin/mine.sh\",\n    \"padding\": 0\n  }\n}\n"
	writeSettings(t, o.SettingsPath, orig, 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if got, want := statusLineOf(t, readFile(t, o.SettingsPath)), statusLineOf(t, orig); got != want {
		t.Fatalf("statusLine changed:\n%s", got)
	}
	if !strings.Contains(f.stderr.String(), "statusLine") || !strings.Contains(f.stderr.String(), "/opt/cmgo/bin/cmgo-hook statusline") {
		t.Fatalf("stderr %q", f.stderr.String())
	}
	if err := Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, o.SettingsPath); got != orig {
		t.Fatalf("got\n%s\nwant\n%s", got, orig)
	}
}

// cmgo's own statusLine from an older path is replaced, as its hooks are,
// and uninstall removes it.
func TestInstallReplacesOwnStatusLine(t *testing.T) {
	o, f := newOpts(t)
	writeSettings(t, o.SettingsPath, "{\n  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": \"'/old/cmgo-hook' statusline\",\n    \"padding\": 1\n  },\n  \"model\": \"opus\"\n}\n", 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"type\": \"command\",\n  \"command\": \"/opt/cmgo/bin/cmgo-hook statusline\"\n}"
	if got := statusLineOf(t, readFile(t, o.SettingsPath)); got != want {
		t.Fatalf("statusLine\n%s\nwant\n%s", got, want)
	}
	if f.stderr.Len() != 0 {
		t.Fatalf("stderr %q", f.stderr.String())
	}
	if err := Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, o.SettingsPath); got != "{\n  \"model\": \"opus\"\n}\n" {
		t.Fatalf("after uninstall:\n%s", got)
	}
}
```

`internal/install/install_test.go`:
```go
package install

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fake records what Install and Uninstall print and run.
type fake struct {
	stdout, stderr bytes.Buffer
	claude         bool     // LookPath("claude") succeeds
	runs           []string // each Run call, name and args joined by spaces
}

// newOpts returns options over a fresh temp dir whose settings dir does not exist yet.
func newOpts(t *testing.T) (Options, *fake) {
	t.Helper()
	dir := t.TempDir()
	f := &fake{}
	return Options{
		SettingsPath:   filepath.Join(dir, "claude", "settings.json"),
		ClaudeJSONPath: filepath.Join(dir, ".claude.json"),
		Binary:         bin,
		Stdout:         &f.stdout,
		Stderr:         &f.stderr,
		LookPath: func(name string) (string, error) {
			if f.claude && name == "claude" {
				return "/usr/local/bin/claude", nil
			}
			return "", exec.ErrNotFound
		},
		Run: func(name string, args ...string) error {
			f.runs = append(f.runs, strings.Join(append([]string{name}, args...), " "))
			return nil
		},
	}, f
}

func writeSettings(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// freshHooks is the file Install writes when it starts from {}.
func freshHooks(t *testing.T, hookBinary string) string {
	t.Helper()
	root := newObject()
	if err := addHooks(root, hookBinary); err != nil {
		t.Fatal(err)
	}
	addStatusLine(&settings{root: root}, Options{HookBinary: hookBinary})
	return string(encodeJSON(root))
}

// commands returns, per matcher entry of event, the inner command strings.
func commands(t *testing.T, content, event string) [][]string {
	t.Helper()
	hooks, _ := mustParse(t, content).get("hooks")
	ev, _ := hooks.(*object).get(event)
	var out [][]string
	for _, e := range ev.([]any) {
		inner, _ := e.(*object).get("hooks")
		var cmds []string
		for _, h := range inner.([]any) {
			c, _ := h.(*object).get("command")
			cmds = append(cmds, c.(string))
		}
		out = append(out, cmds)
	}
	return out
}

const userSettings = `{
  "model": "opus",
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "user.sh"
          },
          {
            "type": "command",
            "command": "\"/old/cmgo\" hook pretooluse"
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "say done",
            "timeout": 5
          }
        ]
      }
    ]
  },
  "theme": "dark"
}
`

func TestInstallMissingFile(t *testing.T) {
	o, f := newOpts(t)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, o.SettingsPath), freshHooks(t, hookBin); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	fi, _ := os.Stat(o.SettingsPath)
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if exists(o.SettingsPath + ".cmgo-bak") {
		t.Fatal("backup of a missing file")
	}
	if !strings.Contains(f.stdout.String(), "wrote "+o.SettingsPath) {
		t.Fatalf("stdout %q", f.stdout.String())
	}
}

func TestInstallKeepsUserHooks(t *testing.T) {
	o, _ := newOpts(t)
	writeSettings(t, o.SettingsPath, userSettings, 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, o.SettingsPath)
	own := func(arg string) []string { return []string{hookCommand(hookBin, arg)} }
	if c := commands(t, got, "PreToolUse"); !slices.EqualFunc(c, [][]string{{"user.sh"}, own("pretooluse")}, slices.Equal) {
		t.Fatalf("PreToolUse %q", c)
	}
	if c := commands(t, got, "Stop"); !slices.EqualFunc(c, [][]string{{"say done"}, own("stop")}, slices.Equal) {
		t.Fatalf("Stop %q", c)
	}
	if c := commands(t, got, "PostToolUse"); !slices.EqualFunc(c, [][]string{own("posttooluse")}, slices.Equal) {
		t.Fatalf("PostToolUse %q", c)
	}
	root := mustParse(t, got)
	if strings.Join(root.keys, ",") != "model,hooks,theme,statusLine" {
		t.Fatalf("keys %v", root.keys)
	}
	if !strings.Contains(got, `"timeout": 5`) || strings.Contains(got, "/old/") {
		t.Fatalf("got\n%s", got)
	}
	if readFile(t, o.SettingsPath+".cmgo-bak") != userSettings {
		t.Fatal("backup is not the original")
	}
}

func TestInstallReplacesOldPath(t *testing.T) {
	o, _ := newOpts(t)
	writeSettings(t, o.SettingsPath, freshHooks(t, "/old place/cmgo-hook"), 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, o.SettingsPath), freshHooks(t, hookBin); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestInstallIdempotent(t *testing.T) {
	for name, orig := range map[string]string{"missing": "", "user hooks": userSettings} {
		t.Run(name, func(t *testing.T) {
			o, f := newOpts(t)
			if orig != "" {
				writeSettings(t, o.SettingsPath, orig, 0o644)
			}
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			first := readFile(t, o.SettingsPath)
			f.stdout.Reset()
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			if second := readFile(t, o.SettingsPath); second != first {
				t.Fatalf("second install changed the file:\n%s\nfirst:\n%s", second, first)
			}
			if !strings.Contains(f.stdout.String(), "is up to date") {
				t.Fatalf("stdout %q", f.stdout.String())
			}
			bak := o.SettingsPath + ".cmgo-bak"
			if orig == "" && exists(bak) {
				t.Fatal("backup created")
			}
			if orig != "" && readFile(t, bak) != orig {
				t.Fatal("backup overwritten")
			}
		})
	}
}

func TestInstallUnparseable(t *testing.T) {
	for _, bad := range []string{`{"a": 1,`, `[]`, `{"hooks": []}`, `{"hooks": {"Stop": {}}}`} {
		o, f := newOpts(t)
		writeSettings(t, o.SettingsPath, bad, 0o644)
		err := Install(o)
		if err == nil || !strings.Contains(err.Error(), o.SettingsPath) {
			t.Fatalf("%s: err %v", bad, err)
		}
		if readFile(t, o.SettingsPath) != bad || exists(o.SettingsPath+".cmgo-bak") || len(f.runs) != 0 {
			t.Fatalf("%s: wrote or ran something", bad)
		}
		if err := Uninstall(o); bad == `{"a": 1,` && err == nil {
			t.Fatal("uninstall parsed a broken file")
		}
	}
}

func TestInstallBlankFile(t *testing.T) {
	o, _ := newOpts(t)
	writeSettings(t, o.SettingsPath, " \n", 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if readFile(t, o.SettingsPath) != freshHooks(t, hookBin) {
		t.Fatal("blank file not treated as {}")
	}
}

func TestInstallBadBinary(t *testing.T) {
	for _, b := range []string{"cmgo", `/a"b/cmgo`} {
		o, _ := newOpts(t)
		o.Binary = b
		if err := Install(o); err == nil || exists(o.SettingsPath) {
			t.Fatalf("%s: err %v", b, err)
		}
		o.Binary, o.HookBinary = bin, b+"-hook"
		if err := Install(o); err == nil || exists(o.SettingsPath) {
			t.Fatalf("hook binary %s: err %v", o.HookBinary, err)
		}
	}
}

func TestInstallHookBinary(t *testing.T) {
	o, f := newOpts(t)
	o.HookBinary = "/elsewhere/cmgo-hook"
	f.claude = true
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, o.SettingsPath), freshHooks(t, o.HookBinary); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	// The MCP server is still the main binary.
	if len(f.runs) != 1 || f.runs[0] != "claude mcp add --scope user cmgo -- "+bin+" serve" {
		t.Fatalf("runs %q", f.runs)
	}
}

func TestUninstallRoundTrip(t *testing.T) {
	plainUser := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "user.sh"
          }
        ]
      }
    ],
    "Notification": []
  },
  "model": "opus"
}
`
	origs := map[string]string{
		"no hooks key": "{\n  \"model\": \"opus\",\n  \"n\": 1.50,\n  \"s\": \"<a> & b\"\n}\n",
		"user hooks":   plainUser,
	}
	for name, orig := range origs {
		t.Run(name, func(t *testing.T) {
			o, _ := newOpts(t)
			writeSettings(t, o.SettingsPath, orig, 0o644)
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			if err := Uninstall(o); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, o.SettingsPath); got != orig {
				t.Fatalf("got\n%s\nwant\n%s", got, orig)
			}
		})
	}
}

func TestUninstallNothingToRemove(t *testing.T) {
	o, f := newOpts(t)
	if err := Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if exists(o.SettingsPath) {
		t.Fatal("uninstall created a settings file")
	}
	// Four-space indent: nothing of ours, so no reformatting either.
	orig := "{\n    \"hooks\": {}\n}\n"
	writeSettings(t, o.SettingsPath, orig, 0o644)
	if err := Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if readFile(t, o.SettingsPath) != orig || exists(o.SettingsPath+".cmgo-bak") {
		t.Fatal("file rewritten")
	}
	if !strings.Contains(f.stdout.String(), "no cmgo hooks in") {
		t.Fatalf("stdout %q", f.stdout.String())
	}
}

func TestTSPlugin(t *testing.T) {
	orig := `{"enabledPlugins": {"other@x": true, "context-mode@context-mode": true, "context-mode@old": false}}`
	o, f := newOpts(t)
	writeSettings(t, o.SettingsPath, orig, 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.stderr.String(), "warning: plugin context-mode@context-mode is enabled") || strings.Contains(f.stderr.String(), "@old") {
		t.Fatalf("stderr %q", f.stderr.String())
	}
	if !strings.Contains(readFile(t, o.SettingsPath), `"context-mode@context-mode": true`) {
		t.Fatal("plugin changed without DisableTSPlugin")
	}

	o, f = newOpts(t)
	o.DisableTSPlugin = true
	writeSettings(t, o.SettingsPath, orig, 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, o.SettingsPath)
	if !strings.Contains(got, `"context-mode@context-mode": false`) || !strings.Contains(got, `"other@x": true`) {
		t.Fatalf("got\n%s", got)
	}
	if !strings.Contains(f.stderr.String(), "disabled plugin context-mode@context-mode") {
		t.Fatalf("stderr %q", f.stderr.String())
	}
}

func TestDryRun(t *testing.T) {
	for name, orig := range map[string]string{"missing": "", "user hooks": userSettings} {
		t.Run(name, func(t *testing.T) {
			o, f := newOpts(t)
			if orig != "" {
				writeSettings(t, o.SettingsPath, orig, 0o644)
			}
			o.DryRun = true
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			if orig == "" && exists(filepath.Dir(o.SettingsPath)) {
				t.Fatal("dry run created the settings dir")
			}
			if orig != "" {
				if readFile(t, o.SettingsPath) != orig {
					t.Fatal("dry run wrote the file")
				}
				if entries, _ := os.ReadDir(filepath.Dir(o.SettingsPath)); len(entries) != 1 {
					t.Fatalf("dry run left files: %v", entries)
				}
			}
			if len(f.runs) != 0 {
				t.Fatalf("ran %v", f.runs)
			}
			printed := f.stdout.String()
			o.DryRun, o.Stdout = false, &bytes.Buffer{}
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(printed, readFile(t, o.SettingsPath)) {
				t.Fatalf("dry run printed\n%s", printed)
			}
		})
	}
}

func TestModePreserved(t *testing.T) {
	o, _ := newOpts(t)
	writeSettings(t, o.SettingsPath, "{}", 0o600)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{o.SettingsPath, o.SettingsPath + ".cmgo-bak"} {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", p, fi.Mode())
		}
	}
}

func TestSymlinkedSettings(t *testing.T) {
	// Dotfile managers link settings.json; the link must survive.
	o, _ := newOpts(t)
	real := filepath.Join(t.TempDir(), "settings.json")
	writeSettings(t, real, "{}", 0o644)
	if err := os.MkdirAll(filepath.Dir(o.SettingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, o.SettingsPath); err != nil {
		t.Fatal(err)
	}
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(o.SettingsPath); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced by a file")
	}
	if readFile(t, real) != freshHooks(t, hookBin) || readFile(t, real+".cmgo-bak") != "{}" {
		t.Fatal("target not updated")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/install/ -count=1`
Expected: FAIL with `internal/install/statusline_test.go:34:13: undefined: ownArg`

- [ ] **Step 3: Write minimal implementation**

`internal/install/hooks.go`:
```go
package install

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

const postMatcher = "Bash|Read|Write|Edit|NotebookEdit|Glob|Grep|TodoWrite|TaskCreate|TaskUpdate|EnterPlanMode|ExitPlanMode|Skill|Agent|AskUserQuestion|EnterWorktree"

// HookEvents lists (settings event name, cmgo hook arg, matcher) in hooks
// spec §3.5 order, then PreCompact (session memory spec §1).
var HookEvents = []struct{ Event, Arg, Matcher string }{
	{"PreToolUse", "pretooluse", "Bash|WebFetch|Read|Grep|Agent"},
	{"PostToolUse", "posttooluse", postMatcher},
	{"PostToolUseFailure", "posttoolusefailure", postMatcher},
	{"UserPromptSubmit", "userpromptsubmit", ""},
	{"SessionStart", "sessionstart", ""},
	{"Stop", "stop", ""},
	{"PreCompact", "precompact", ""},
}

// hookCommand quotes the hook binary for sh -c, which Claude Code runs it
// through: $, backtick, backslash and quotes in the install path stay literal.
func hookCommand(hookBinary, arg string) string { return shellQuote(hookBinary) + " " + arg }

// shellWord reads the first word of a shell command line: single-quoted,
// double-quoted (older installs wrote that form) or bare, joined as sh does
// ('it'\”s' is it's). It returns the word and the rest of the line.
func shellWord(cmd string) (word, rest string, ok bool) {
	var b strings.Builder
	for i := 0; i < len(cmd); i++ {
		switch c := cmd[i]; c {
		case '\'':
			j := strings.IndexByte(cmd[i+1:], '\'')
			if j < 0 {
				return "", "", false
			}
			b.WriteString(cmd[i+1 : i+1+j])
			i += j + 1
		case '"':
			j := strings.IndexByte(cmd[i+1:], '"')
			if j < 0 {
				return "", "", false
			}
			b.WriteString(cmd[i+1 : i+1+j])
			i += j + 1
		case '\\':
			if i+1 < len(cmd) {
				i++
				b.WriteByte(cmd[i])
			}
		case ' ', '\t':
			return b.String(), cmd[i:], true
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), "", true
}

// ownArg returns the event arg of a cmgo-hook command from any install
// location, else "": first word (single-quoted, double-quoted or bare) with
// basename cmgo-hook, then "<arg>"; or the form from before cmgo-hook
// existed, basename cmgo, then "hook <arg>". Both match so a reinstall
// replaces either.
func ownArg(cmd string) string {
	exe, rest, ok := shellWord(cmd)
	if !ok {
		return ""
	}
	f := strings.Fields(rest)
	switch filepath.Base(exe) {
	case "cmgo-hook":
		if len(f) == 1 {
			return f[0]
		}
	case "cmgo":
		if len(f) == 2 && f[0] == "hook" {
			return f[1]
		}
	}
	return ""
}

// isOwnCommand reports whether cmd is a cmgo hook command of a HookEvents arg.
func isOwnCommand(cmd string) bool {
	arg := ownArg(cmd)
	return arg != "" && slices.ContainsFunc(HookEvents, func(h struct{ Event, Arg, Matcher string }) bool { return h.Arg == arg })
}

// stripHooks removes cmgo's inner commands under every event and returns how
// many it removed. Entries, event arrays and the hooks key are dropped only
// when this call emptied them, so user-made empty values survive. Values of an
// unexpected shape are left alone.
func stripHooks(root *object) int {
	hv, _ := root.get("hooks")
	hooks, ok := hv.(*object)
	if !ok {
		return 0
	}
	total := 0
	for _, ev := range slices.Clone(hooks.keys) {
		entries, ok := hooks.vals[ev].([]any)
		if !ok {
			continue
		}
		kept := []any{}
		for _, e := range entries {
			if n, empty := stripEntry(e); n > 0 {
				total += n
				if empty {
					continue
				}
			}
			kept = append(kept, e)
		}
		switch {
		case len(kept) == len(entries):
		case len(kept) == 0:
			hooks.del(ev)
		default:
			hooks.set(ev, kept)
		}
	}
	if total > 0 && len(hooks.keys) == 0 {
		root.del("hooks")
	}
	return total
}

// stripEntry removes cmgo's commands from one matcher entry and reports how
// many it removed and whether none are left.
func stripEntry(e any) (removed int, empty bool) {
	eo, ok := e.(*object)
	if !ok {
		return 0, false
	}
	inner, ok := eo.vals["hooks"].([]any)
	if !ok {
		return 0, false
	}
	kept := []any{}
	for _, h := range inner {
		if ho, ok := h.(*object); ok {
			if c, ok := ho.vals["command"].(string); ok && isOwnCommand(c) {
				continue
			}
		}
		kept = append(kept, h)
	}
	removed = len(inner) - len(kept)
	if removed > 0 {
		eo.set("hooks", kept)
	}
	return removed, len(kept) == 0
}

// addHooks appends one matcher entry per HookEvents row, creating the hooks
// object and event arrays as needed.
func addHooks(root *object, hookBinary string) error {
	hv, ok := root.get("hooks")
	if !ok {
		hv = newObject()
		root.set("hooks", hv)
	}
	hooks, ok := hv.(*object)
	if !ok {
		return fmt.Errorf(`"hooks" is not an object`)
	}
	for _, he := range HookEvents {
		cmd := newObject()
		cmd.set("type", "command")
		cmd.set("command", hookCommand(hookBinary, he.Arg))
		entry := newObject()
		entry.set("matcher", he.Matcher)
		entry.set("hooks", []any{cmd})
		ev, ok := hooks.get(he.Event)
		if !ok {
			hooks.set(he.Event, []any{entry})
			continue
		}
		arr, ok := ev.([]any)
		if !ok {
			return fmt.Errorf(`"hooks.%s" is not an array`, he.Event)
		}
		hooks.set(he.Event, append(arr, entry))
	}
	return nil
}
```

`internal/install/install.go`:
```go
// Package install registers cmgo's hooks in Claude Code's settings.json and
// its MCP server through the claude CLI (spec §5).
package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Options are the inputs of Install and Uninstall. The caller resolves every
// path and injects LookPath and Run so tests never run the real claude.
type Options struct {
	SettingsPath    string // resolved by the caller (CLAUDE_CONFIG_DIR or ~/.claude)
	ClaudeJSONPath  string // ~/.claude.json
	Binary          string // absolute path to cmgo
	HookBinary      string // absolute path to cmgo-hook; "" means cmgo-hook next to Binary
	DisableTSPlugin bool
	DryRun          bool
	Stdout, Stderr  io.Writer
	LookPath        func(string) (string, error) // exec.LookPath in production
	Run             func(name string, args ...string) error
}

// Install replaces cmgo's hook commands in the settings file with the
// HookEvents entries for o.HookBinary, keeping every other command, points
// statusLine at it unless another statusline is set, then registers o.Binary
// as the MCP server.
func Install(o Options) error {
	if o.HookBinary == "" {
		o.HookBinary = filepath.Join(filepath.Dir(o.Binary), "cmgo-hook")
	}
	for _, b := range []string{o.Binary, o.HookBinary} {
		if !filepath.IsAbs(b) || strings.Contains(b, `"`) {
			return fmt.Errorf("binary path must be absolute and have no double quote: %q", b)
		}
	}
	s, err := loadSettings(o.SettingsPath)
	if err != nil {
		return err
	}
	stripHooks(s.root)
	stripStatusLine(s.root)
	if err := addHooks(s.root, o.HookBinary); err != nil {
		return fmt.Errorf("%s: %w", o.SettingsPath, err)
	}
	addStatusLine(s, o)
	checkPlugins(s.root, o)
	if err := s.save(o); err != nil {
		return err
	}
	return registerMCP(o, true)
}

// Uninstall removes cmgo's hook commands, its statusLine and the MCP server.
// It leaves enabledPlugins and another statusline alone.
func Uninstall(o Options) error {
	s, err := loadSettings(o.SettingsPath)
	if err != nil {
		return err
	}
	// Nothing removed: do not create or reformat the user's file.
	if stripHooks(s.root)+stripStatusLine(s.root) == 0 && !o.DryRun {
		fmt.Fprintf(o.Stdout, "cmgo: no cmgo hooks in %s\n", s.path)
	} else if err := s.save(o); err != nil {
		return err
	}
	return registerMCP(o, false)
}

// addStatusLine sets statusLine to cmgo-hook statusline when it is absent;
// Install has already stripped cmgo's own, like its hooks. Another statusline
// is left as it is, with a notice (stats spec §4).
func addStatusLine(s *settings, o Options) {
	cmd := hookCommand(o.HookBinary, "statusline")
	if _, ok := s.root.get("statusLine"); !ok {
		sl := newObject()
		sl.set("type", "command")
		sl.set("command", cmd)
		s.root.set("statusLine", sl)
		return
	}
	fmt.Fprintf(o.Stderr, "cmgo: notice: statusLine in %s is not cmgo's; left as is (to use cmgo's, set its command to %s)\n", s.path, cmd)
}

// isOwnStatusLine reports whether a statusLine object runs cmgo-hook statusline.
func isOwnStatusLine(sl *object) bool {
	c, ok := sl.vals["command"].(string)
	return ok && ownArg(c) == "statusline"
}

// stripStatusLine removes cmgo's statusLine and returns 1, else 0.
func stripStatusLine(root *object) int {
	if v, ok := root.get("statusLine"); ok {
		if sl, ok := v.(*object); ok && isOwnStatusLine(sl) {
			root.del("statusLine")
			return 1
		}
	}
	return 0
}

// checkPlugins warns about an enabled TS context-mode plugin, which would
// inject its routing text next to cmgo's, and turns it off when asked.
func checkPlugins(root *object, o Options) {
	pv, _ := root.get("enabledPlugins")
	p, ok := pv.(*object)
	if !ok {
		return
	}
	for _, k := range p.keys {
		if !strings.HasPrefix(k, "context-mode@") || p.vals[k] != true {
			continue
		}
		if o.DisableTSPlugin {
			p.set(k, false)
			fmt.Fprintf(o.Stderr, "cmgo: disabled plugin %s\n", k)
			continue
		}
		fmt.Fprintf(o.Stderr, "cmgo: warning: plugin %s is enabled; it and cmgo will both inject routing text (rerun with --disable-ts-plugin to turn it off)\n", k)
	}
}

// settings is a parsed settings file.
type settings struct {
	path    string // the real file, symlinks resolved
	existed bool
	orig    []byte
	mode    fs.FileMode
	root    *object
}

// loadSettings reads path; a missing or blank file is {}. A file that does
// not parse is an error, so nothing is ever written over it.
func loadSettings(path string) (*settings, error) {
	s := &settings{path: path, mode: 0o644, root: newObject()}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		s.path = real // write through a dotfile manager's link, not over it
	}
	fi, err := os.Stat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	s.existed, s.orig, s.mode = true, b, fi.Mode().Perm()
	if len(bytes.TrimSpace(b)) == 0 {
		return s, nil
	}
	v, err := parseJSON(b)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	root, ok := v.(*object)
	if !ok {
		return nil, fmt.Errorf("parse %s: not a JSON object", path)
	}
	s.root = root
	return s, nil
}

// save writes the edited file when its bytes changed, after keeping the
// original once as <file>.cmgo-bak. DryRun prints it instead.
func (s *settings) save(o Options) error {
	out := encodeJSON(s.root)
	if o.DryRun {
		_, err := o.Stdout.Write(out)
		return err
	}
	if s.existed && bytes.Equal(out, s.orig) {
		fmt.Fprintf(o.Stdout, "cmgo: %s is up to date\n", s.path)
		return nil
	}
	if s.existed {
		bak := s.path + ".cmgo-bak"
		if _, err := os.Lstat(bak); errors.Is(err, fs.ErrNotExist) {
			if err := writeAtomic(bak, s.orig, s.mode); err != nil {
				return err
			}
			fmt.Fprintf(o.Stdout, "cmgo: saved the original as %s\n", bak)
		} else if err != nil {
			return err
		}
	} else if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	if err := writeAtomic(s.path, out, s.mode); err != nil {
		return err
	}
	fmt.Fprintf(o.Stdout, "cmgo: wrote %s\n", s.path)
	return nil
}

// writeAtomic writes a temp file in the same dir and renames it over path, so
// a crash never leaves a half-written settings file.
func writeAtomic(path string, b []byte, mode fs.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) // fails harmlessly after the rename
	_, err = f.Write(b)
	if err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/install/ ./cmd/cmgo/ -count=1 && go test -race ./internal/install/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/install/hooks.go internal/install/install.go internal/install/statusline_test.go internal/install/install_test.go
git commit -m "feat(install): point statusLine at cmgo-hook unless another is set"
```

### Task 19: Statusline latency bench

**Files:**
- Modify: `bench/hook-latency.py`

**Interfaces:**
- Consumes: the built `cmgo-hook` (`statusline` from Task 17), migration 3's tables (fragment S4A)
- Produces: `fill_stats(binary, env, db)`; a `statusline (100 sessions, 50000 events)` case with p95 <= 10 ms and a no-write check of the DB file

- [ ] **Step 1: Write the failing test**

`bench/hook-latency.py`:
```python
#!/usr/bin/env python3 -I
"""Criterion 1: `cmgo-hook <event>` wall time p95 <= 10 ms per case (spec §6.4,
session memory spec §0 criterion 4, stats spec §0 criterion 3).

Run: go build -o bin/ ./cmd/cmgo-hook && python3 -I bench/hook-latency.py bin/cmgo-hook [runs]

Every case runs `runs` times against a temp CMGO_DIR, HOME and project dir
(never the real ones), after one warm-up call that creates the DB. The
precompact and sessionstart compact cases run against a session pre-filled
to 1000 events (the per-session cap) through the hook itself. The time
is subprocess.run wall clock, so it includes the fork/exec cost. Then
posttooluse runs from 4 threads at once, `runs` fires each, into the same DB.
Last, `cmgo-hook statusline` runs against a second DB that the binary creates
and migrates and that this script fills with sqlite3 to 100 sessions x 500
events plus linked tool_stats rows; the DB file must not change.
For context it also times /usr/bin/true and cmgo-hook with no arguments
(usage, exit 2) the same way, and prints the 1-minute load average.
"""
import json
import math
import os
import shutil
import sqlite3
import statistics
import subprocess
import sys
import tempfile
import threading
import time

LIMIT_MS, WORKERS, MEM_EVENTS, MEM_SID = 10.0, 4, 1000, "bench-mem"
STAT_SESSIONS, STAT_EVENTS = 100, 500


def timed(argv, env, data=b""):
    t0 = time.perf_counter()
    p = subprocess.run(argv, input=data, env=env, capture_output=True)
    return (time.perf_counter() - t0) * 1000, p


def fire(binary, env, event, payload):
    """Runs one hook; returns (ms, stdout). A non-zero exit is a bench error."""
    ms, p = timed([binary, event], env, json.dumps(payload).encode())
    if p.returncode != 0:
        raise SystemExit(f"{event}: exit {p.returncode}: {p.stderr.decode()}")
    return ms, p.stdout


def p95(xs):
    return sorted(xs)[math.ceil(0.95 * len(xs)) - 1]


def stats(times):
    return f"p50 {statistics.median(times):.2f} ms p95 {p95(times):.2f} ms"


def report(name, times):
    ok = p95(times) <= LIMIT_MS
    print(f"{name}: {stats(times)} {'PASS' if ok else 'FAIL'}")
    return ok


def read_payload(i, sid):
    return {"tool_name": "Read", "tool_input": {"file_path": f"/src/file{i}.go"},
            "tool_response": {"type": "text", "file": {"filePath": f"/src/file{i}.go", "content": "package x\n"}},
            "session_id": sid}


def fill_session(binary, env, db):
    """Records MEM_EVENTS events for MEM_SID: Read captures with a prompt every
    fourth fire, more than the cap, so the cap's eviction leaves exactly 1000."""
    for i in range(MEM_EVENTS + MEM_EVENTS // 10):
        if i % 4 == 0:
            fire(binary, env, "userpromptsubmit", {"prompt": f"step {i}: fix the parser and add tests", "session_id": MEM_SID})
        else:
            fire(binary, env, "posttooluse", read_payload(i, MEM_SID))
    con = sqlite3.connect(db)
    try:
        n = con.execute("SELECT count(*) FROM session_events WHERE session_id = ?", (MEM_SID,)).fetchone()[0]
    finally:
        con.close()
    if n != MEM_EVENTS:
        raise SystemExit(f"fill: {n} events, want {MEM_EVENTS}")


def fill_stats(binary, env, db):
    """Lets the binary create and migrate db, then replaces its rows with
    STAT_SESSIONS sessions of STAT_EVENTS ~400-byte events each (1 in 20 a
    redirect with bytes_avoided) and per session six tool_stats rows linked
    to it (its cc_pid, project and window) plus six of an older window."""
    fire(binary, env, "posttooluse", read_payload(-1, "warm"))
    con = sqlite3.connect(db)
    try:
        if con.execute("PRAGMA user_version").fetchone()[0] < 3:
            raise SystemExit("stats fill: DB is older than migration 3")
        con.execute("DELETE FROM session_events")
        con.execute("DELETE FROM session_meta")
        data = "x" * 400
        for s in range(STAT_SESSIONS):
            sid, project, started = f"stat-{s}", f"/bench/p{s % 5}", f"2026-10-{1 + s % 20:02d} 10:{s % 60:02d}:00"
            con.execute("INSERT INTO session_meta(session_id, project, started_at, last_event_at, event_count, cc_pid) "
                        "VALUES (?, ?, ?, ?, ?, ?)", (sid, project, started, started, STAT_EVENTS, 1000 + s))
            con.executemany(
                "INSERT INTO session_events(session_id, project, type, category, priority, data, data_hash, source_hook, "
                "bytes_avoided, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
                [(sid, project, "file_read", "redirect" if e % 20 == 0 else "file", 2, data, f"{s:08X}{e:08X}",
                  "PostToolUse", 8192 if e % 20 == 0 else 0, started) for e in range(STAT_EVENTS)])
            for window in (started, "2026-09-01 00:00:00"):
                con.executemany(
                    "INSERT INTO tool_stats(server_id, cc_pid, project, tool, calls, bytes_returned, bytes_kept, "
                    "started_at, window, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
                    [(f"srv-{s}", 1000 + s, project, f"ctx_tool{t}", 10, 4000, 90000, started, window, started)
                     for t in range(6)])
        con.commit()
        n = con.execute("SELECT count(*), count(DISTINCT session_id) FROM session_events").fetchone()
    finally:
        con.close()
    if n != (STAT_SESSIONS * STAT_EVENTS, STAT_SESSIONS):
        raise SystemExit(f"stats fill: {n[0]} events in {n[1]} sessions")


def main(binary, runs):
    binary = os.path.abspath(binary)
    root = tempfile.mkdtemp(prefix="cmgo-hook-bench-")
    try:
        data, home, project = (os.path.join(root, d) for d in ("data", "home", "project"))
        for d in (home, project):
            os.makedirs(d)
        env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "HOME": home,
               "CMGO_DIR": data, "CMGO_PROJECT_DIR": project}
        print(f"load average (1 min) at start: {os.getloadavg()[0]:.2f}")
        for name, argv in (("/usr/bin/true", ["/usr/bin/true"]), ("cmgo-hook, no args", [binary])):
            print(f"context {name}: {stats([timed(argv, env)[0] for _ in range(runs)])}")
        fire(binary, env, "posttooluse", read_payload(-1, "warm"))  # creates and migrates the DB
        fill_session(binary, env, os.path.join(data, "cmgo.db"))

        # A fake live server so the curl redirect fires: this process's pid.
        run_dir = os.path.join(data, "run")
        os.makedirs(run_dir, exist_ok=True)
        with open(os.path.join(run_dir, f"mcp-ready-{os.getpid()}"), "w") as f:
            f.write(str(os.getpid()))

        cases = [
            ("pretooluse passthrough", "pretooluse",
             lambda i: {"tool_name": "Bash", "tool_input": {"command": "git status"}, "session_id": "bench"},
             lambda out: out == b""),
            ("pretooluse curl redirect", "pretooluse",
             lambda i: {"tool_name": "Bash", "tool_input": {"command": "curl https://example.com"}, "session_id": "bench"},
             lambda out: b'"permissionDecision":"deny"' in out),
            ("posttooluse Read capture", "posttooluse", lambda i: read_payload(i, "bench"), lambda out: out == b""),
            ("userpromptsubmit", "userpromptsubmit",
             lambda i: {"prompt": f"please look at item {i} of the list and explain it in detail to me", "session_id": "bench"},
             lambda out: out == b""),
            ("sessionstart startup", "sessionstart",
             lambda i: {"source": "startup", "session_id": f"bench-start-{i}"},
             lambda out: out.startswith(b'{"hookSpecificOutput":{"hookEventName":"SessionStart"')),
            (f"precompact ({MEM_EVENTS} events)", "precompact",
             lambda i: {"session_id": MEM_SID, "trigger": "auto"}, lambda out: out == b"{}\n"),
            (f"sessionstart compact ({MEM_EVENTS} events)", "sessionstart",
             lambda i: {"source": "compact", "session_id": MEM_SID},
             lambda out: b'<session_knowledge source=\\"compact\\">' in out),
        ]
        ok = True
        for name, event, payload, check in cases:
            times = []
            for i in range(runs):
                ms, out = fire(binary, env, event, payload(i))
                if i == 0 and not check(out):
                    raise SystemExit(f"{name}: unexpected stdout {out[:200]!r}")
                times.append(ms)
            ok &= report(name, times)

        times, lock, errors = [], threading.Lock(), []

        def worker(w):
            try:
                for i in range(runs):
                    ms, _ = fire(binary, env, "posttooluse", read_payload(w * runs + i, f"bench-w{w}"))
                    with lock:
                        times.append(ms)
            except BaseException as e:  # SystemExit from fire included
                errors.append(e)

        threads = [threading.Thread(target=worker, args=(w,)) for w in range(WORKERS)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        if errors:
            raise SystemExit(f"concurrent posttooluse: {errors[0]}")
        ok &= report(f"posttooluse x{WORKERS} concurrent", times)

        stat_env = dict(env, CMGO_DIR=os.path.join(root, "stat-data"))
        stat_db = os.path.join(stat_env["CMGO_DIR"], "cmgo.db")
        fill_stats(binary, stat_env, stat_db)
        db_before = (os.stat(stat_db).st_size, os.stat(stat_db).st_mtime_ns)
        times = []
        for i in range(runs):
            ms, out = fire(binary, stat_env, "statusline", {"session_id": f"stat-{i % STAT_SESSIONS}"})
            if i == 0 and not (out.startswith("cmgo  ●  ".encode()) and b" this chat " in out):
                raise SystemExit(f"statusline: unexpected stdout {out[:200]!r}")
            times.append(ms)
        ok &= report(f"statusline ({STAT_SESSIONS} sessions, {STAT_SESSIONS * STAT_EVENTS} events)", times)
        if (os.stat(stat_db).st_size, os.stat(stat_db).st_mtime_ns) != db_before:
            print("FAIL: statusline changed the DB file")
            ok = False

        for log in (os.path.join(d, "hook-errors.log") for d in (data, stat_env["CMGO_DIR"])):
            if not os.path.exists(log):
                continue
            with open(log) as f:
                lines = f.read().splitlines()
            print(f"FAIL: {len(lines)} hook error(s), first: {lines[0]}")
            ok = False
        print(f"load average (1 min) at end: {os.getloadavg()[0]:.2f}")
        print("PASS" if ok else "FAIL")
        return 0 if ok else 1
    finally:
        shutil.rmtree(root, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1], int(sys.argv[2]) if len(sys.argv) > 2 else 200))
```

- [ ] **Step 2: Run test to verify it fails**

The case fails on a `cmgo-hook` without the statusline event, such as one built before Task 17 (`git stash` the Task 17 change or check out its parent into a temp worktree).

Run: `B=$(mktemp -d /private/tmp/claude-501/s4b-bin.XXXX) && go build -o "$B/" ./cmd/cmgo-hook && python3 -I bench/hook-latency.py "$B/cmgo-hook" 3`
Expected: FAIL with `statusline: unexpected stdout b''`

- [ ] **Step 3: Write minimal implementation**

No code change: the statusline event exists from Task 17. Rebuild `cmgo-hook` from the current tree.

- [ ] **Step 4: Run test to verify it passes**

Run: `B=$(mktemp -d /private/tmp/claude-501/s4b-bin.XXXX) && go build -o "$B/" ./cmd/cmgo-hook ./cmd/cmgo && python3 -I bench/hook-latency.py "$B/cmgo-hook" 200`
Expected: PASS. A run on darwin/arm64 (Go 1.27.3):
```
load average (1 min) at start: 3.49
context /usr/bin/true: p50 1.98 ms p95 2.18 ms
context cmgo-hook, no args: p50 4.70 ms p95 5.38 ms
pretooluse passthrough: p50 4.74 ms p95 5.09 ms PASS
pretooluse curl redirect: p50 6.10 ms p95 6.79 ms PASS
posttooluse Read capture: p50 6.25 ms p95 6.88 ms PASS
userpromptsubmit: p50 6.28 ms p95 7.08 ms PASS
sessionstart startup: p50 6.53 ms p95 7.04 ms PASS
precompact (1000 events): p50 6.50 ms p95 6.98 ms PASS
sessionstart compact (1000 events): p50 7.61 ms p95 8.42 ms PASS
posttooluse x4 concurrent: p50 7.47 ms p95 9.59 ms PASS
statusline (100 sessions, 50000 events): p50 6.05 ms p95 6.56 ms PASS
load average (1 min) at end: 3.72
PASS
```

- [ ] **Step 5: Commit**

```bash
git add bench/hook-latency.py
git commit -m "test(bench): time the statusline against 100 sessions and 50000 events"
```

---

## Appendix A: Porting notes by package

These notes are the deliberate choices made while drafting. If a review disagrees with one, change the spec first, then the code.

### internal/procinfo (Tasks 1-3)

Ports the parent-chain walk of the TS statusline (`bin/statusline.mjs:140-193`: `findClaudePid`, `findClaudePidLinux` `:154-169`, `findClaudePidDarwin` `:171-193`) as spec §1.2 of `docs/superpowers/specs/2026-10-21-cmgo-stats-design.md` defines it: the hooks, the statusline and `cmgo serve` all use it to find the Claude Code process (`cc_pid`) that owns them. TS walks with `ps -o ppid=,comm=` on darwin and `/proc/<pid>/status` on Linux; cmgo reads `sysctl kern.proc.pid` on darwin (`golang.org/x/sys/unix.SysctlKinfoProc`) and `/proc/<pid>/stat` on Linux.

**Parity notes (decided, flag in review if wrong):**
- A "hop" is one parent step: hop 1 is pid's parent. As in TS (`i < 8`, starting at `process.ppid`), at most 8 ancestors are examined, so `ClaudePID` makes at most 9 lookups (pid itself plus 8). pid's own name is never checked.
- Deviation from TS: when no ancestor matches or a lookup fails, `ClaudePID` returns 0 (spec §1.2 "unknown"); TS returns `process.ppid`.
- Deviation from TS: the walk stops at ppid <= 0, not at `pid > 1`, so pid 1 is examined. Spec §1.2 says "first ancestor", and pid 1 is `claude` when Claude Code is a container's init process. The 8-hop cap alone ends parent cycles; no visited set.
- Names are the kernel's short names: darwin `p_comm` (the basename, 16 bytes; TS takes the basename of `ps` comm), Linux `comm` (15 bytes, the same value as the `Name:` line TS reads). "claude" fits in both. Claude Code started as `node .../cli.js` has comm `node` and yields 0, in TS too.
- No subprocess: `sysctl` replaces the `ps` call per hop, because the statusline runs several times per second.
- `/proc/<pid>/stat` comm may contain spaces and parentheses, so it ends at the last `)`. The parser is in an untagged file so its tests run on darwin too.
- Other OSes get a `System()` whose lookup always fails (no Windows code; the build stays green elsewhere).
- `golang.org/x/sys` was already in the module graph as indirect; `go mod tidy` moves it to the direct `require` block. That is the only `go.mod` change, and `go.sum` does not change.

### internal/stats (Tasks 4-6)

Pure rendering for spec §2 (`ctx_stats` text) and §3 (statusline line) of `docs/superpowers/specs/2026-10-21-cmgo-stats-design.md`. It ports the TS size and duration helpers byte for byte: `kb()` (`src/session/analytics.ts:1961-1977`) and `formatDuration()` (`analytics.ts:1980-1987`) together with the `(uptimeMs / 60_000).toFixed(1)` step its caller applies first (`analytics.ts:464`). The report and statusline layouts are cmgo's own (spec §2, §3; the TS narrative `formatReport`, `analytics.ts:2980-3238`, is not ported). The caller (mcpserver for `ctx_stats`, cmgo-hook for the statusline) queries the numbers; this package does no I/O.

**Parity notes (decided, flag in review if wrong):**
- `KB` and `Duration` expected values come from node: `kb` imported from `build/session/analytics.js` (HOME, TMPDIR and CONTEXT_MODE_DIR at a fresh `mktemp -d` dir), and `formatDuration`, which the module does not export, taken as text from the same file with a regex and evaluated, fed `(ms / 60000).toFixed(1)`.
- JS `toFixed` rounds the exact binary value and takes the larger candidate on a tie; Go's `FormatFloat` rounds ties to even (`kb(1280)` is `"1.3 KB"` in JS, `"1.2 KB"` with `FormatFloat`). `toFixed` is ported with `math/big.Rat`, exact for every float64, because `Duration` divides by 60000 and `math.Round(x*10)` would round 0.95 (stored as 0.9499...) up.
- TS quirks kept: `kb` prints `"1024 KB"` for 1048575 bytes and `"1024 MB"` for 1073741823; `formatDuration` prints `"1h 60m"` for 119.6 minutes (`Math.round(min % 60)`) and `"1h"` for 59.95 minutes (`toFixed(1)` gives `"60.0"`).
- `Report.Raw` is supplied by the caller: the sum of `Kept + Returned` over the rows of tools that have a raw definition in spec §1.3 (`ctx_execute`, `ctx_execute_file`, `ctx_batch_execute`, `ctx_fetch_and_index`). The headline `Kept` and `Returned` sum every row in `Tools`, and the percentage is `Pct(sum Kept, Raw)`. Redirect bytes are shown on their own line and are not part of the session's kept bytes; `LifetimeKept` arrives already including them (spec §2 "Lifetime").
- Contract additions: `type Category struct{Name string; Count int}` replaces the anonymous struct in `Report.Categories` (so callers can name it), and `func Pct(kept, raw int64) int` is exported so the statusline caller computes `Line.Pct` with the same rounding.
- Unlinked session (spec §2 "no hook data yet"): the header line becomes the notice; the `Kept`, `Returned` and per-tool lines still render from the server's rows; the `Redirected by hooks` line and the `Session memory` section are dropped, because both come from hook data that a missing session does not have (showing 0 would claim a measurement).
- `LifetimeSince` zero (no `tool_stats` rows yet) hides `  ·  since <date>`; otherwise its UTC date is printed (`started_at` is stored in UTC).
- No plural forms (`1 calls`, `1 sessions`), as the spec template shows. `Format` returns no trailing newline; `StatusLine` none either.
- Ordering: tools by calls desc, then name; categories by count desc, then name, cut to 8. The caller's slices are not reordered.
- Padding uses `fmt` widths, which count runes; JS `padEnd`/`padStart` count UTF-16 units. They agree for the ASCII tool and category names cmgo produces.

### store, mcpserver, routing, serve (Tasks 7-15)

Persists cmgo's own tool counters and wires them into `ctx_stats`, as specified in `docs/superpowers/specs/2026-10-21-cmgo-stats-design.md` §1, §2 and §4. It replaces the TS in-memory counters (`trackResponse`, `src/server.ts:885`; `trackIndexed`, `:951`; the `stats-pid-<ppid>.json` sidecar written by `persistStats`, `:1018`) with one `tool_stats` UPSERT per call, linked to the Claude Code session through the `cc_pid` of `internal/procinfo` (fragment PROC). `ctx_stats` (TS registration `src/server.ts:3937-3952`) renders the store snapshot with `internal/stats` (fragment STR) instead of the TS narrative report. The routing block regains the TS `ctx stats` entry of `<ctx_commands>` (`hooks/routing-block.mjs:63-77`). The store also gains the read-only open and `SessionLine` that the `cmgo-hook statusline` (fragment S4B) calls.

**Parity notes (decided, flag in review if wrong):**
- Migration 3 is the spec §1.1 DDL plus one index the spec does not list: `session_events_redirect ON session_events(bytes_avoided) WHERE category = 'redirect'`. `SessionLine` sums every redirect's `bytes_avoided` on each statusline run; without the index, 50,000 events with 400-byte data took p95 11.8 ms in-process for that query alone (spec criterion 3 allows 10 ms for the whole run), and with it 0.8 ms. `TestLifetimeKeptUsesRedirectIndex` pins the query plan.
- Windows (spec §1.1-§1.2): `AddToolStat` tags each write with `window` = the `started_at` of the newest `session_meta` row (by `started_at`, then rowid) with the server's `cc_pid` and project, read inside the same single UPSERT statement; `''` when there is none or `cc_pid` is 0 (every row older than migration 3 has 0, so 0 never links). The key is `(server_id, tool, window)`, so after a `/clear` (which keeps the MCP server) the same server's calls go to new rows. `TestStatsClearWindow` pins that the second session sees only its own calls and the first keeps its own.
- Session linking: `Stats` takes the newest session with the server's `cc_pid` and project; its rows are those with that `cc_pid`, project and `window` = its `started_at`. With no linked session, `Tools` are this `server_id`'s rows summed over all windows. `SessionLine(sessionID)` applies the same rule from the session side. Two sessions of one Claude Code process that start in the same second share a window (one-second resolution of `started_at`).
- Raw bytes per call (spec §1.3): `ctx_execute` is stdout + stderr of the run, timeouts included; `ctx_execute_file` adds the file size from `os.Stat`, resolved against the project like `executor.Run` (0 when the stat fails); `ctx_batch_execute` is the sum of each command's stdout + stderr (not the formatted sections); `ctx_fetch_and_index` is `len(fetch.Result.Body)` per fetched URL, which is the body after HTML-to-markdown conversion and trimming, because `internal/fetch` does not expose the wire size; a cache hit is `store.SourceBytes` of the cached source (UTF-8 bytes of its chunk contents). `ctx_search`, `ctx_purge` and `ctx_stats` record 0. Argument and policy errors are recorded as calls with raw 0.
- `SourceBytes` scans the FTS5 chunks table (`source_id` is UNINDEXED), so it runs only on a cache hit and only when stats are on. Byte sums use `length(CAST(content AS BLOB))`; `length(TEXT)` counts characters.
- `StatsSnapshot.Raw` (contract addition) is `Kept + Returned` over the tools that have a raw size, computed in the store so `ctx_stats` (`stats.Report.Raw`) and `SessionLine` (`sessionRaw`) use the same definition.
- `StatsSnapshot.Events` is the count of stored `session_events` rows of the session (after eviction), not `session_meta.event_count`. `Categories` is nil when no session is linked.
- `OpenReadOnly` sets `busy_timeout(250)`: a WAL reader does not wait for writers, only for WAL recovery. Opening a closed WAL-mode DB read-only makes SQLite create empty `-wal` and `-shm` files next to it (the DB file itself is unchanged); a statusline test that asserts the `-wal` file is unchanged must start from a DB with a live writer or an existing `-wal`.
- `ctx_stats` (spec §2): input schema `{"type": "object", "properties": {}}`; title and annotations from TS (`Session Statistics`, read-only, idempotent, closed world); description from TS minus "estimated token usage". Its own call is recorded after the report is built, so a report never counts itself. The version is the one `mcpserver.New` already takes; `Deps` gains only `Stats`.
- Routing block (spec §4): the `ctx stats` entry keeps the TS trigger list verbatim (`"ctx stats" | "ctx-stats" | "/ctx-stats" | context savings question`), so `TestGoldenStrings` derives the cmgo block from the unchanged TS golden text by dropping only doctor and upgrade.
- `ServerID` is `"<pid>-<start unix nanos>"` and `StartedAt` the start in UTC `YYYY-MM-DD HH:MM:SS`; `cc_pid` is computed once at serve start.

### hookcmd statusline, install, bench (Tasks 16-19)

Adds the cmgo statusline and the hook side of session linking, as specified in `docs/superpowers/specs/2026-10-21-cmgo-stats-design.md` §1.2, §3, §4 and criterion 3. It replaces TS `bin/statusline.mjs`, which resolves the session from the statusLine stdin `session_id` and then `CLAUDE_SESSION_ID` (`bin/statusline.mjs:196-210`, stdin read at `:231`) but opens every session DB writable, with `cmgo-hook statusline`: it opens `cmgo.db` read-only through `store.OpenReadOnly`, reads `store.SessionLine` (fragment S4A) and renders `stats.StatusLine` (fragment STR). SessionStart, whose output is kept from TS `hooks/sessionstart.mjs`, now also records the session's Claude Code pid from `procinfo.ClaudePID` (fragment PROC), the link `cmgo serve` uses to tag its `tool_stats` rows with the session's window. `cmgo install` points `statusLine` at `cmgo-hook statusline` (TS has no such install step), and `bench/hook-latency.py` gains the criterion 3 case.

**Parity notes (decided, flag in review if wrong):**
- SessionStart records `cc_pid` for every source, `clear` included, through `store.SetSessionCC`, as its first DB write: a server call written before it is tagged with the previous session's window (S4A finding). `clear` used to print the routing block and open no DB (hooks spec: no event, no open); it now opens the DB for this one write, because `/clear` is the case that starts a new window in the same Claude Code process. Output still comes first in every source, and the 2 s busy timeout of the other hook writes applies (`TestHookLockedDBFailsOpenFast` gains a `clear` fire).
- With no `claude` ancestor, SessionStart still writes `cc_pid` 0. Store treats 0 as never linked.
- The process table is read through the package var `procLookup` (default `procinfo.System()`), as `stdinTimeout` is a var: the tests run under Claude Code, so a real walk would find a real `claude` ancestor.
- `statusline` is not a hook fire: `Run` handles it before the hook table. It prints even on empty stdin and never appends to `hook-errors.log` or creates `$CMGO_DIR`, because Claude Code runs it several times a second and a missing DB is the normal state of a fresh install. A panic is recovered into the fallback line. It always exits 0.
- Session id: stdin `session_id`, else `CLAUDE_SESSION_ID`, as spec §3 and TS `bin/statusline.mjs:206-210`; empty stdin or malformed JSON falls through to the env. It does not use `hookio.SessionID`, whose `transcript_path` UUID comes first: Claude Code names the transcript after `session_id`, so both give the same key, and the pid fallback (`pid-<ppid>`) would show the lifetime line where the spec wants the fallback line.
- Fallback line `cmgo  ●  ready` when there is no session id, no DB, `user_version` < 3, or any error. A session id the DB does not know is not an error: `SessionLine` gives it no session rows, so the line is `<lifetime> kept out lifetime`.
- `pct` is `stats.Pct(sessionKept, sessionRaw)`, with `sessionRaw` from `SessionLine` (kept + returned of the linked rows).
- Install mirrors the hooks: it first strips cmgo's own `statusLine` (any install path, detected by the shared command parser `ownArg` with arg `statusline`, in the `cmgo-hook statusline` and old `cmgo hook statusline` forms), then sets `{"type":"command","command":"<shellQuote(hook binary)> statusline"}` when the key is absent. So a reinstall from a new path updates it, and a second install is byte-identical. A field the user added to cmgo's own `statusLine` (such as `padding`) is dropped on reinstall, as it would be for a hook entry. Any other `statusLine` value is left as is, with one Stderr notice naming cmgo's command. `uninstall` removes `statusLine` only when it is cmgo's.
- No separate `cmgo statusline` subcommand: the existing `cmgo hook <event>` alias already runs `cmgo hook statusline`.
- Bench: the statusline case runs against its own data dir, created and migrated by one hook call, whose rows the script replaces with 100 sessions x 500 events (about 400-byte data, 1 in 20 a redirect with `bytes_avoided` 8192) and, per session, six `tool_stats` rows linked to it plus six of an older window. It runs `runs` times over the 100 session ids, checks that the first line has ` this chat `, and fails if the DB file's size or mtime changes. Opening a closed WAL DB read-only creates empty `-wal`/`-shm` files (S4A note), so the bench checks the main file only; the unit test checks the `-wal` too, with a live writer.
## Appendix B: Verification (2026-10-21, macOS arm64)

**Replay of this plan** (`replay_plan2.py`): each task's files were written into a fresh worktree of `sp3-session-memory`, then `go vet ./...` and that task's package tests were run. All 19 tasks were `ok`. Final step: `go test ./... && go test -race ./...`.

```
FINAL ok
ontext-mode-go/cmd/cmgo	5.555s
ok  	github.com/lang315/context-mode-go/cmd/cmgo-hook	0.586s
ok  	github.com/lang315/context-mode-go/internal/capture	0.258s
ok  	github.com/lang315/context-mode-go/internal/config	1.848s
ok  	github.com/lang315/context-mode-go/internal/executor	15.357s
ok  	github.com/lang315/context-mode-go/internal/fetch	1.722s
ok  	github.com/lang315/context-mode-go/internal/hookcmd	11.162s
ok  	github.com/lang315/context-mode-go/internal/hookio	1.226s
ok  	github.com/lang315/context-mode-go/internal/install	5.388s
ok  	github.com/lang315/context-mode-go/internal/mcpserver	6.312s
ok  	github.com/lang315/context-mode-go/internal/memory	2.330s
ok  	github.com/lang315/context-mode-go/internal/procinfo	2.126s
ok  	github.com/lang315/context-mode-go/internal/routing	1.664s
ok  	github.com/lang315/context-mode-go/internal/security	1.739s
ok  	github.com/lang315/context-mode-go/internal/stats	1.724s
ok  	github.com/lang315/context-mode-go/internal/store	3.719s
ok  	github.com/lang315/context-mode-go/cmd/cmgo	11.761s
ok  	github.com/lang315/context-mode-go/cmd/cmgo-hook	1.376s
ok  	github.com/lang315/context-mode-go/internal/capture	2.595s
ok  	github.com/lang315/context-mode-go/internal/config	2.060s
ok  	github.com/lang315/context-mode-go/internal/executor	16.231s
ok  	github.com/lang315/context-mode-go/internal/fetch	1.943s
ok  	github.com/lang315/context-mode-go/internal/hookcmd	14.267s
ok  	github.com/lang315/context-mode-go/internal/hookio	3.380s
ok  	github.com/lang315/context-mode-go/internal/install	7.013s
ok  	github.com/lang315/context-mode-go/internal/mcpserver	17.430s
ok  	github.com/lang315/context-mode-go/internal/memory	2.815s
ok  	github.com/lang315/context-mode-go/internal/procinfo	2.470s
ok  	github.com/lang315/context-mode-go/internal/routing	2.670s
ok  	github.com/lang315/context-mode-go/internal/security	2.341s
ok  	github.com/lang315/context-mode-go/internal/stats	2.164s
ok  	github.com/lang315/context-mode-go/internal/store	18.305s
```

`go test ./... -count=1 -v`: 428 `--- PASS`, 0 `--- FAIL`. The php, r, elixir and csharp subtests of `TestRunHelloEachLanguage` were skipped because those runtimes are not installed. After the replay, the tree matched the draft except `go.mod`: the replay script does not run Task 3's `go mod tidy` step, which moves `golang.org/x/sys` to the direct block.

**Bench, run on the draft binaries:**

```
load average (1 min) at start: 4.04
context /usr/bin/true: p50 1.99 ms p95 2.38 ms
context cmgo-hook, no args: p50 3.82 ms p95 4.48 ms
pretooluse passthrough: p50 4.24 ms p95 4.84 ms PASS
pretooluse curl redirect: p50 5.41 ms p95 5.94 ms PASS
posttooluse Read capture: p50 5.56 ms p95 6.23 ms PASS
userpromptsubmit: p50 5.63 ms p95 6.33 ms PASS
sessionstart startup: p50 5.77 ms p95 6.52 ms PASS
precompact (1000 events): p50 5.77 ms p95 6.42 ms PASS
sessionstart compact (1000 events): p50 6.90 ms p95 7.60 ms PASS
posttooluse x4 concurrent: p50 6.76 ms p95 8.94 ms PASS
statusline (100 sessions, 50000 events): p50 5.37 ms p95 5.99 ms PASS
load average (1 min) at end: 5.71
PASS
8 idle servers over 60s: 0 bytes written = 0.000 MB/min
PASS
```

**What the replay did not exercise:**
- **The fail-first steps:** the replay ran none of the Step 2 runs. The drafting agents replayed their own tasks one by one and recorded each "Expected: FAIL" line from a real run. A few line numbers in those lines may be off by a line or two: some intermediate files were patched to carry the sub-project 3 review fixes.
- **The Linux `/proc` lookup:** only the parser is tested. The lookup itself was never run on Linux.
- **Live Claude Code:** real `cc_pid` linking under Claude Code, and the statusLine end to end, were not checked.

## Appendix C: Execution notes (2026-10-21)

The plan was executed on branch `sp4-stats` with reviews at three checkpoints (Tasks 6, 15, 19) and a whole-branch review. Every file of Tasks 1-19 matched this plan byte for byte, and the finished tree equalled the draft, including the real `go mod tidy` of Task 3.

**Errata in this plan:** none found during execution.

**Fixed after the whole-branch review** (one commit each, test first). The spec was updated first (§1.1-1.3, §3, §4). Migration 3 was edited in place, because it had never been released.

| Finding | Fix |
|---|---|
| **Critical:** `cc_pid` was always 0 on darwin with Claude Code's native installer. The kernel process name is the version (`2.1.304`), because `~/.local/bin/claude` is a symlink into `versions/` | `procinfo` matches `claude` in a path element of the executable path, `argv[0]` or `argv[1]` (darwin `kern.procargs2`, Linux `/proc/<pid>/exe` and `cmdline`). A smoke test checks the real ancestor when run under Claude Code |
| `/resume` of an older session, and two `/clear`s in one second, attributed calls to the wrong session | `session_meta.linked_at` (ms) set by `SetSessionCC`; `tool_stats.window` is the session id with the newest `linked_at` for the server's `cc_pid` and project |
| Every tool call did an inline stats write with a 30 s busy timeout | One background worker with a 256-slot queue; drop when full; `serve` drains for 1 s; `ctx_stats` and `ctx_purge` flush first; `ctx_purge` is not recorded |
| The lifetime sum scanned all `tool_stats` rows on every statusline render | `stats_totals` running sums per project; the statusline takes 6 ms instead of 12 ms at 50,000 rows |
| A purge with only stats rows said "Nothing to purge" | `PurgeProject` counts stats rows and drops the totals |
| `"statusLine": null` blocked cmgo's statusline; reinstall dropped user fields | Null counts as absent; reinstall updates only `command` |

**Final state:**
- **Tests:** 442 pass, 0 fail; `go test -race ./...` clean.
- **Real check under Claude Code:** `cmgo-hook sessionstart` stored the real Claude Code pid in `session_meta.cc_pid`.
- **Bench:** at load average about 5.5 every case passes. The 4-worker concurrent case p95 is 9.3-10.0 ms; the statusline p95 is about 6 ms.
- **Idle writes:** 0 bytes in 60 s.

**Open follow-ups:**
- The 4-worker hook case runs at the limit on a loaded machine.
- The statusline may create the `-wal` and `-shm` files when they are missing, as any SQLite WAL reader does. The main DB file is never written.
