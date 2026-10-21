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
