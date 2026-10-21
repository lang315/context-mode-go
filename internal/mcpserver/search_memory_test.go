package mcpserver

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/store"
)

// events inserts session events for project (the harness project when "").
func (h *harness) events(sessionID, project string, evs ...store.Event) {
	h.t.Helper()
	if project == "" {
		project = h.Deps.Cfg.ProjectKey
	}
	if _, err := h.Deps.Store.InsertEvents(sessionID, project, "PostToolUse", evs); err != nil {
		h.t.Fatal(err)
	}
}

// stamp matches the "YYYY-MM-DD HH:MM" header field.
const stamp = `\d{4}-\d\d-\d\d \d\d:\d\d`

func TestSearchTimelineSessionEvents(t *testing.T) {
	h := newHarness(t, nil)
	h.events("s1", "",
		store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "first cache decision"},
		store.Event{Type: "file_read", Category: "file", Priority: 1, Data: "cache.go"},
	)
	h.events("s2", "", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "latest cache decision"})
	h.events("s9", "/other/project", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "foreign cache decision"})

	// Newest 2 matches of the project (all sessions), shown oldest to newest.
	out, isErr := h.call("ctx_search", `{"queries": ["CACHE"], "sort": "timeline"}`)
	re := regexp.MustCompile(`\A## CACHE\n\n` +
		`--- \[prior-session \| ` + stamp + ` \| prior-session\] ---\n### \[file\] file_read\n\ncache\.go\n\n` +
		`--- \[prior-session \| ` + stamp + ` \| prior-session\] ---\n### \[decision\] decision\n\nlatest cache decision` +
		regexp.QuoteMeta(throttle1) + `\z`)
	if isErr || !re.MatchString(out) {
		t.Fatalf("isErr=%v\n%s", isErr, out)
	}
	at, err := time.Parse("2006-01-02 15:04", regexp.MustCompile(stamp).FindString(out))
	if err != nil || time.Since(at) > 5*time.Minute || time.Since(at) < -time.Minute {
		t.Fatalf("timestamp %v is not the UTC insert time (%v)", at, err)
	}

	// source must equal the category; every term must match.
	out, _ = h.call("ctx_search", `{"queries": ["cache decision", "nothing here"], "sort": "timeline", "source": "decision", "limit": 1}`)
	if !strings.HasPrefix(out, "## cache decision\n\n--- [prior-session | ") ||
		!strings.Contains(out, "### [decision] decision\n\nlatest cache decision\n\n---\n\n## nothing here\nNo results found.") ||
		strings.Contains(out, "first cache") || strings.Contains(out, "foreign") {
		t.Fatalf("source filter:\n%s", out)
	}

	// Relevance mode does not read session events.
	h.index("notes", store.Chunk{Title: "Other", Content: "unrelated text"})
	if out, _ := h.call("ctx_search", `{"queries": ["latest cache decision"]}`); strings.Contains(out, "prior-session") {
		t.Fatalf("relevance reached session events:\n%s", out)
	}
}

func TestSearchTimelineMergesSources(t *testing.T) {
	h := newHarness(t, nil)
	md := filepath.Join(h.Project, "CLAUDE.md")
	if err := os.WriteFile(md, []byte("Widget rules live here."), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 5, 6, 7, 8, 9, 0, time.UTC)
	if err := os.Chtimes(md, old, old); err != nil {
		t.Fatal(err)
	}
	// Auto-memory alone: origin auto-memory, mtime in UTC, label as source.
	out, _ := h.call("ctx_search", `{"queries": ["widget"], "sort": "timeline"}`)
	if want := "## widget\n\n--- [auto-memory | 2020-05-06 07:08 | project/CLAUDE.md] ---\n### [auto-memory] project/CLAUDE.md\n\nWidget rules live here." + throttle1; out != want {
		t.Fatalf("auto-memory:\n%s", out)
	}

	h.index("notes", store.Chunk{Title: "Widget doc", Content: "widget content"})
	h.events("s1", "", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "widget decision"})
	// Three sources match; the old memory file is dropped, content then event remain.
	out, _ = h.call("ctx_search", `{"queries": ["widget"], "sort": "timeline"}`)
	re := regexp.MustCompile(`\A## widget\n\n` +
		`--- \[current-session \| ` + stamp + ` \| notes\] ---\n### Widget doc\n\nwidget content\n\n` +
		`--- \[prior-session \| ` + stamp + ` \| prior-session\] ---\n### \[decision\] decision\n\nwidget decision\n\n> Throttle`)
	if !re.MatchString(out) {
		t.Fatalf("merge:\n%s", out)
	}

	// A newer memory file outranks the content chunk and is shown last.
	future := time.Now().Add(24 * time.Hour)
	if err := os.Chtimes(md, future, future); err != nil {
		t.Fatal(err)
	}
	out, _ = h.call("ctx_search", `{"queries": ["widget"], "sort": "timeline"}`)
	re = regexp.MustCompile(`\A## widget\n\n--- \[prior-session \| [^\n]*\n### \[decision\] decision\n\nwidget decision\n\n` +
		`--- \[auto-memory \| ` + regexp.QuoteMeta(future.UTC().Format("2006-01-02 15:04")) + ` \| project/CLAUDE.md\] ---\n`)
	if !re.MatchString(out) {
		t.Fatalf("future memory:\n%s", out)
	}

	// A source filter skips auto-memory and filters the content label.
	h2 := newHarness(t, nil)
	if err := os.WriteFile(filepath.Join(h2.Project, "CLAUDE.md"), []byte("widget"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _ := h2.call("ctx_search", `{"queries": ["widget"], "sort": "timeline", "source": "notes"}`); !strings.HasPrefix(out, "## widget\nNo results found.") {
		t.Fatalf("source filter reached auto-memory:\n%s", out)
	}
}

func TestSearchDescriptionMentionsSessionMemory(t *testing.T) {
	for _, s := range []string{
		"AND auto-captured session memory written by hooks",
		"(recently indexed content, prior session events, auto-memory)",
		"You want a chronological view across current session + prior sessions + persistent auto-memory (pass `sort: \"timeline\"`",
		"AND no session memory has accumulated around it",
		"Common session-memory source labels: `decision`",
		`EXAMPLE: ctx_search(queries: ["what did we decide about caching"], source: "decision", sort: "timeline")`,
		`EXAMPLE: ctx_search(queries: ["last user prompt", "active skills", "open blockers"], sort: "timeline")`,
	} {
		if !strings.Contains(searchDescription, s) {
			t.Errorf("description lacks %q", s)
		}
	}
	for _, s := range []string{"ctx_stats", "CONTEXT_MODE_", "ctx_index", "content hash", "oldest first"} {
		if strings.Contains(searchDescription, s) {
			t.Errorf("description still has %q", s)
		}
	}
	if !strings.Contains(searchSchema, "'timeline': chronological across current session, prior sessions, and auto-memory.") {
		t.Error("sort schema description not restored")
	}
}
