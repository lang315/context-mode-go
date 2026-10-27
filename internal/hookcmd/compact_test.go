package hookcmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/store"
)

// compactInput is a posttooluse payload for a Bash call; extra is spliced
// into the top-level object.
func compactInput(cmd, stdout string, interrupted bool, extra string) string {
	return `{"session_id":"s1","tool_use_id":"tu1","tool_name":"Bash","tool_input":{"command":` + string(hookio.QuoteString(cmd)) +
		`},"tool_response":{"stdout":` + string(hookio.QuoteString(stdout)) + `,"stderr":"","interrupted":` + fmt.Sprint(interrupted) +
		`,"isImage":false}` + extra + `}`
}

// bigStdout is about 19 KB of test output with a marker line and an error
// line in the middle.
func bigStdout() string {
	var b strings.Builder
	for i := 0; i < 500; i++ {
		switch i {
		case 250:
			b.WriteString("zebra-marker-77 in the middle\n")
		case 260:
			b.WriteString("error: boom\n")
		default:
			fmt.Fprintf(&b, "line %04d ok github.com/x/pkg%d 0.01s\n", i, i)
		}
	}
	return b.String()
}

// compactEnv is a hook env with a live MCP sentinel and no DB yet.
func compactEnv(t *testing.T) map[string]string {
	t.Helper()
	env := hookEnv(t)
	remove, err := routing.WriteSentinel(filepath.Join(env["CMGO_DIR"], "run"), os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { remove() })
	return env
}

type compactOut struct {
	HookSpecificOutput struct {
		HookEventName     string
		UpdatedToolOutput struct {
			Stdout, Stderr       string
			Interrupted, IsImage bool
		}
	}
}

func TestHookPostToolUseBashCompact(t *testing.T) {
	env := compactEnv(t)
	stdout := bigStdout()
	out := callHook(t, env, "posttooluse", compactInput("go test ./...", stdout, true, ""))
	var got compactOut
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	u := got.HookSpecificOutput.UpdatedToolOutput
	if got.HookSpecificOutput.HookEventName != "PostToolUse" || u.Stderr != "" || !u.Interrupted || u.IsImage {
		t.Fatalf("shape: %+v", got)
	}
	for _, want := range []string{"line 0000 ", "line 0499 ", "error: boom", `indexed as "bash:tu1"`} {
		if !strings.Contains(u.Stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, u.Stdout)
		}
	}
	if strings.Contains(u.Stdout, "zebra-marker-77") {
		t.Errorf("middle line shown:\n%s", u.Stdout)
	}

	st, err := store.Open(filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hits, err := st.SearchWithFallback("zebra", store.SearchOpts{Project: config.NormalizeProject(env["CLAUDE_PROJECT_DIR"]), Source: "bash:tu1", SourceExact: true})
	if err != nil || len(hits) == 0 || !strings.Contains(hits[0].Content, "zebra-marker-77") {
		t.Fatalf("search = %+v, %v", hits, err)
	}
	var calls, ret, kept int64
	if err := metaDB(t, env).QueryRow("SELECT calls, bytes_returned, bytes_kept FROM tool_stats WHERE server_id = 'hook' AND tool = 'bash-compact' AND window = 's1'").
		Scan(&calls, &ret, &kept); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || ret != int64(len(u.Stdout)) || kept != min(int64(len(stdout)-len(u.Stdout)), 30000) {
		t.Errorf("tool_stats = %d %d %d", calls, ret, kept)
	}
	if s := hookErrors(env); s != "" {
		t.Errorf("errors logged: %s", s)
	}
}

func TestHookPostToolUseBashCompactSkips(t *testing.T) {
	big := bigStdout()
	readInput := `{"session_id":"s1","tool_use_id":"tu1","tool_name":"Read","tool_input":{"file_path":"/p/a.go"},"tool_response":{"type":"text","file":{"filePath":"/p/a.go","content":` +
		string(hookio.QuoteString(big)) + `}}}`
	cases := []struct {
		name, stdin string
		env         map[string]string
		mcpDown     bool
	}{
		{name: "subagent", stdin: compactInput("go test ./...", big, false, `,"agent_id":"a1","agent_type":"general-purpose"`)},
		{name: "at threshold", stdin: compactInput("go test ./...", strings.Repeat("x\n", 2560), false, "")},
		{name: "over 30000", stdin: compactInput("go test ./...", strings.Repeat("y", 30001), false, "")},
		{name: "reading command", stdin: compactInput("cat big.log", big, false, "")},
		{name: "full marker", stdin: compactInput("go test ./... # cmgo:full", big, false, "")},
		{name: "switched off", stdin: compactInput("go test ./...", big, false, ""), env: map[string]string{"CMGO_BASH_COMPACT_MIN_BYTES": "0"}},
		{name: "text longer than output", stdin: compactInput("go test ./...", strings.Repeat("z", 100), false, ""), env: map[string]string{"CMGO_BASH_COMPACT_MIN_BYTES": "1"}},
		{name: "no stdout", stdin: `{"session_id":"s1","tool_use_id":"tu1","tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":{"stderr":""}}`},
		{name: "not Bash", stdin: readInput},
		{name: "MCP down", stdin: compactInput("go test ./...", big, false, ""), mcpDown: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := hookEnv(t)
			if !c.mcpDown {
				env = compactEnv(t)
			}
			for k, v := range c.env {
				env[k] = v
			}
			if out := callHook(t, env, "posttooluse", c.stdin); strings.Contains(out, "updatedToolOutput") {
				t.Errorf("compacted: %q", out)
			}
			if s := hookErrors(env); s != "" {
				t.Errorf("errors logged: %s", s)
			}
		})
	}
}

// With the DB write-locked the hook prints nothing, so the model keeps the
// original output, and logs the error.
func TestHookPostToolUseBashCompactLockedDB(t *testing.T) {
	env := compactEnv(t)
	callHook(t, env, "posttooluse", `{"session_id":"s1","tool_name":"Read","tool_input":{"file_path":"/p/a.go"}}`) // creates the DB
	holdDBLock(t, filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if out := callHook(t, env, "posttooluse", compactInput("go test ./...", bigStdout(), false, "")); out != "" {
		t.Errorf("printed %q with the DB locked", out)
	}
	if hookErrors(env) == "" {
		t.Error("no error logged")
	}
}

func TestKnownDiffsListBashCompact(t *testing.T) {
	for file, id := range map[string]string{"HOOKS-KNOWN-DIFFS.md": "posttooluse:bash-compact", "KNOWN-DIFFS.md": "stats:bash-compact"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", file))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), id) {
			t.Errorf("%s has no %s entry", file, id)
		}
	}
}
