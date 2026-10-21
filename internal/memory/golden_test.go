package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

// goldenCase is one case of ../../testdata/golden/memory-*.json, written by
// tools/golden-gen/gen-memory.mjs. ts is the TS output and expected the
// cmgo output, computed by the same TS code with the spec §2.4 fixes
// applied to its input (see MEMORY-KNOWN-DIFFS.md).
type goldenCase struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Events []struct {
		ID        int64  `json:"id"`
		Type      string `json:"type"`
		Category  string `json:"category"`
		Data      string `json:"data"`
		CreatedAt string `json:"created_at"`
	} `json:"events"`
	TS       *string `json:"ts"`
	Expected string  `json:"expected"`
}

func (c goldenCase) stored() []store.StoredEvent {
	out := make([]store.StoredEvent, len(c.Events))
	for i, e := range c.Events {
		out[i] = store.StoredEvent{ID: e.ID, Type: e.Type, Category: e.Category, Data: e.Data, CreatedAt: e.CreatedAt}
	}
	return out
}

type goldenFile struct {
	TSCommit string       `json:"tsCommit"`
	Cases    []goldenCase `json:"cases"`
}

func loadGolden(t *testing.T, name string) goldenFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", name))
	if err != nil {
		t.Fatal(err)
	}
	var g goldenFile
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if g.TSCommit != "a5e6dd31" || len(g.Cases) == 0 {
		t.Fatalf("%s: commit %q, %d cases", name, g.TSCommit, len(g.Cases))
	}
	return g
}

// knownDiffs are the cases where TS output differs from cmgo's on purpose,
// the rows of testdata/golden/MEMORY-KNOWN-DIFFS.md, with their fixes.
func knownDiffs() map[string]string {
	return map[string]string{
		"directive:full/compact":         "user-prompt, no-mcp-data",
		"directive:full/resume":          "user-prompt, no-mcp-data",
		"directive:leg/resume":           "leg-boundary",
		"directive:leg-tie/compact":      "leg-boundary",
		"directive:trunc/compact":        "user-prompt, no-mcp-data",
		"directive:trunc-exact/resume":   "user-prompt",
		"directive:trunc-astral/compact": "user-prompt",
		"directive:prompts/compact":      "user-prompt",
		"directive:prompts/resume":       "user-prompt",
		"directive:mcp-data-only/resume": "no-mcp-data",
	}
}

// checkKnownDiffs fails when a case's TS text differs from its expected text
// without a knownDiffs row, or has a row but no longer differs.
func checkKnownDiffs(t *testing.T, kind string, cases []goldenCase) {
	t.Helper()
	diffs := knownDiffs()
	for _, c := range cases {
		id := kind + ":" + c.Name
		fix, listed := diffs[id]
		delete(diffs, id)
		if c.TS == nil {
			t.Errorf("%s: no ts text", id)
		} else if differs := *c.TS != c.Expected; differs != listed {
			t.Errorf("%s: TS differs from expected = %v, but listed = %v (%s)", id, differs, listed, fix)
		}
	}
	for id := range diffs {
		if strings.HasPrefix(id, kind+":") {
			t.Errorf("%s: listed but no such case", id)
		}
	}
}

func TestGoldenDirective(t *testing.T) {
	g := loadGolden(t, "memory-directive.json")
	checkKnownDiffs(t, "directive", g.Cases)
	for _, c := range g.Cases {
		if got := Directive(c.Source, c.stored()); got != c.Expected {
			t.Errorf("%s:\n--- got\n%s\n--- want\n%s", c.Name, got, c.Expected)
		}
	}
}

func TestGoldenAutoInjection(t *testing.T) {
	for _, c := range loadGolden(t, "memory-autoinjection.json").Cases {
		if got := AutoInjection(c.stored()); got != c.Expected {
			t.Errorf("%s:\n--- got\n%s\n--- want\n%s", c.Name, got, c.Expected)
		}
	}
}
