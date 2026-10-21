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
	purgeProjectMsg = "Purged: knowledge base (FTS5), session events. All session data for this project has been permanently deleted."
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
