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
		{"ctx_search", 3, 1200, 0, 0},
		{"ctx_execute", 5, 2048, 50000, 0},
		{"ctx_fetch_and_index", 1, 3000, 120000, 0},
		{"ctx_batch_execute", 5, 1000, 200000, 0},
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
		Tools: []ToolRow{{"ctx_execute", 1, 300, 4096, 0}}, Raw: 4396,
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
		Tools: []ToolRow{{"ctx_search", 2, 900, 0, 0}},
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

func TestFormatNotMeasuredSuffixes(t *testing.T) {
	r := Report{Version: "x", Now: now, SessionLinked: true, SessionStart: now.Add(-time.Minute),
		Tools: []ToolRow{{Tool: "ctx_execute", Calls: 20, Returned: 3000, Kept: 1200, Unmeasured: 14}}}
	if got := Format(r); !strings.Contains(got, "returned  ·  14 not measured\n") {
		t.Errorf("row suffix missing:\n%s", got)
	}
	r.Tools[0].Unmeasured = 0
	if got := Format(r); strings.Contains(got, "not measured") {
		t.Errorf("suffix printed with 0:\n%s", got)
	}
	r.LifetimeUnmeasured = 31
	if got := Format(r); !strings.Contains(got, "0 B kept out  ·  31 ctx_execute calls not measured  ·  0 sessions") {
		t.Errorf("lifetime suffix missing:\n%s", got)
	}
}
