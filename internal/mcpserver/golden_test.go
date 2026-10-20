package mcpserver

// Golden parity test for ctx_batch_execute and ctx_search output (spec
// §5.2). tools.json is written by tools/golden-gen/gen.mjs from the TS
// server; this test replays the same calls, in order, against one harness
// per session and compares the masked text. Differences are listed by step
// ID in testdata/golden/KNOWN-DIFFS.md and in goldenToolKnown below.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type goldenToolStep struct {
	ID      string
	Session int
	Tool    string
	Args    json.RawMessage
	IsError bool
	Text    string
}

// goldenPin pins the current Go output of a step listed in KNOWN-DIFFS.md:
// goText maps the masked TS text to the masked Go text.
type goldenPin struct {
	reason  string
	isError bool
	goText  func(ts string) string
}

// goldenToolKnown: step ID → pinned Go output for KNOWN-DIFFS.md bug entries.
// Empty: the only entry (legacy-query) was fixed by matching TS.
var goldenToolKnown = map[string]goldenPin{}

func TestGoldenTools(t *testing.T) {
	b, err := os.ReadFile("../../testdata/golden/tools.json")
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		TSCommit string
		Masks    []struct{ Re, To string }
		Steps    []goldenToolStep
	}
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	if g.TSCommit != "a5e6dd31" || len(g.Steps) == 0 {
		t.Fatalf("tools.json: commit %q, %d steps", g.TSCommit, len(g.Steps))
	}
	mask := func(s string) string {
		for _, m := range g.Masks {
			s = regexp.MustCompile(m.Re).ReplaceAllString(s, m.To)
		}
		return s
	}

	var h *harness
	session := 0
	for _, st := range g.Steps {
		if st.Session != session { // fresh server, store and flood guard
			session = st.Session
			h = newHarness(t, nil)
			if err := os.CopyFS(filepath.Join(h.Project, "corpus"), os.DirFS("../../testdata/corpus")); err != nil {
				t.Fatal(err)
			}
		}
		text, isErr := h.call(st.Tool, string(st.Args))
		text = mask(text)
		wantText, wantErr := st.Text, st.IsError
		if pin, ok := goldenToolKnown[st.ID]; ok {
			t.Logf("%s: known diff, Go output pinned (%s)", st.ID, pin.reason)
			if text == st.Text && isErr == st.IsError {
				t.Errorf("%s: now matches TS; drop it from KNOWN-DIFFS.md and goldenToolKnown", st.ID)
			}
			wantText, wantErr = pin.goText(st.Text), pin.isError
		}
		if text != wantText || isErr != wantErr {
			t.Errorf("%s %s %s: isError=%v want %v\n%s", st.ID, st.Tool, st.Args, isErr, wantErr, lineDiff(text, wantText))
		}
	}
}

// lineDiff shows the first differing line with a little context.
func lineDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < max(len(g), len(w)); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl || i >= len(g) || i >= len(w) {
			return "first diff at line " + itoa(i+1) + ":\n got: " + quoteClip(gl) + "\nwant: " + quoteClip(wl)
		}
	}
	return "(texts equal)"
}

func quoteClip(s string) string {
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	b, _ := json.Marshal(s)
	return string(b)
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
