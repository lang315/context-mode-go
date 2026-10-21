package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/memory"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/store"
)

// Session memory spec §2.1-2.2 on the built binary: precompact records the
// compaction, then SessionStart(compact) returns the directive built from the
// session's events.
func TestBinaryPreCompactAndCompactDirective(t *testing.T) {
	env := hookEnv(t)
	for _, c := range []struct{ event, stdin string }{
		{"userpromptsubmit", `{"prompt":"fix the login bug","session_id":"s1"}`},
		{"posttooluse", `{"tool_name":"Read","tool_input":{"file_path":"/p/a.go"},"tool_response":"x","session_id":"s1"}`},
	} {
		if out, code := runHookBinary(t, env, c.stdin, c.event); code != 0 || out != "" {
			t.Fatalf("%s: exit %d, stdout %q", c.event, code, out)
		}
	}
	if out, code := runHookBinary(t, env, `{"session_id":"s1","trigger":"auto"}`, "precompact"); code != 0 || out != "{}\n" {
		t.Fatalf("precompact: exit %d, stdout %q", code, out)
	}
	rows := eventRows(t, env, "s1")
	if len(rows) != 4 || rows[3] != "compaction_summary|compaction|1|Session compacted. 3 events, 1 files touched.|PreCompact|0" {
		t.Fatalf("rows %q", rows)
	}

	st, err := store.Open(filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	evs, err := st.SessionEvents("s1")
	st.Close()
	if err != nil {
		t.Fatal(err)
	}
	ctx := routing.RoutingBlock(true, false) + memory.Directive("compact", evs) + "\n\n" + memory.AutoInjection(evs)
	want := `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":` + string(hookio.QuoteString(ctx)) + "}}\n"
	out, code := runHookBinary(t, env, `{"session_id":"s1","source":"compact"}`, "sessionstart")
	if code != 0 || out != want || !strings.Contains(out, `<session_knowledge source=\"compact\">`) {
		t.Fatalf("sessionstart compact: exit %d\ngot  %q\nwant %q", code, out, want)
	}
	if s := hookErrors(env); s != "" {
		t.Fatalf("errors logged: %s", s)
	}
}
