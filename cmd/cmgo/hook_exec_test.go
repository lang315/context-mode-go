package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/store"
)

var (
	binOnce sync.Once
	binDir  string
	binErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if binDir != "" {
		os.RemoveAll(binDir)
	}
	os.Exit(code)
}

// builtBinary builds cmgo and cmgo-hook once per test binary, on first use,
// and returns the path of name.
func builtBinary(t *testing.T, name string) string {
	t.Helper()
	binOnce.Do(func() {
		if binDir, binErr = os.MkdirTemp("", "cmgo-bin-"); binErr != nil {
			return
		}
		if out, err := exec.Command("go", "build", "-o", binDir+"/", ".", "../cmgo-hook").CombinedOutput(); err != nil {
			binErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if binErr != nil {
		t.Fatal(binErr)
	}
	return filepath.Join(binDir, name)
}

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

// execEnv is env plus PATH and nothing else, so no CLAUDE_* or CMGO_* value
// from the developer's shell leaks into the child.
func execEnv(env map[string]string) []string {
	out := []string{"PATH=" + os.Getenv("PATH")}
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// runHookBinary runs the built cmgo-hook with stdin and returns stdout and
// the exit code.
func runHookBinary(t *testing.T, env map[string]string, stdin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(builtBinary(t, "cmgo-hook"), args...)
	cmd.Env = execEnv(env)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out.String(), exit.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	if errb.Len() > 0 {
		t.Errorf("%v: stderr %q", args, errb.String())
	}
	return out.String(), 0
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

func hookErrors(env map[string]string) string {
	b, _ := os.ReadFile(filepath.Join(env["CMGO_DIR"], "hook-errors.log"))
	return string(b)
}

const curlInput = `{"tool_name":"Bash","tool_input":{"command":"curl https://x.com"},"session_id":"s1"}`

func TestBinaryHookEvents(t *testing.T) {
	env := hookEnv(t)
	sessionStart := `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":` +
		string(hookio.QuoteString(routing.RoutingBlock(false, false))) + "}}\n"
	cases := []struct {
		event, stdin string
		check        func(string) bool
	}{
		{"pretooluse", `{"tool_name":"Agent","tool_input":{"prompt":"hi"},"session_id":"s1"}`, func(s string) bool {
			return strings.HasPrefix(s, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":{"prompt":"hi\n<context_window_protection>`) &&
				strings.HasSuffix(s, "\"}}}\n")
		}},
		{"posttooluse", `{"tool_name":"Read","tool_input":{"file_path":"/p/a.go"},"tool_response":"x","session_id":"s1"}`, func(s string) bool { return s == "" }},
		{"posttoolusefailure", `{"tool_name":"Bash","error":"exit 1","session_id":"s1"}`, func(s string) bool { return s == "" }},
		{"userpromptsubmit", `{"prompt":"hello","session_id":"s1"}`, func(s string) bool { return s == "" }},
		{"stop", `{"session_id":"s1"}`, func(s string) bool { return s == "{}\n" }},
		{"sessionstart", `{"session_id":"s1","source":"startup"}`, func(s string) bool { return s == sessionStart }},
	}
	for _, c := range cases {
		out, code := runHookBinary(t, env, c.stdin, c.event)
		if code != 0 || !c.check(out) {
			t.Errorf("%s: exit %d, stdout %q", c.event, code, out)
		}
	}
	seen := map[string]bool{}
	for _, r := range eventRows(t, env, "s1") {
		seen[strings.Split(r, "|")[4]] = true
	}
	var hooks []string
	for h := range seen {
		hooks = append(hooks, h)
	}
	sort.Strings(hooks)
	if got := strings.Join(hooks, ","); got != "PostToolUse,PostToolUseFailure,SessionStart,Stop,UserPromptSubmit" {
		t.Fatalf("source hooks in the DB: %s", got)
	}
	if s := hookErrors(env); s != "" {
		t.Fatalf("errors logged: %s", s)
	}
}

func TestBinaryHookMalformedStdin(t *testing.T) {
	env := hookEnv(t)
	out, code := runHookBinary(t, env, `{`, "pretooluse")
	if code != 0 || out != "" {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	if s := hookErrors(env); !regexp.MustCompile(`^\S+ pid=\d+ pretooluse: .+\n$`).MatchString(s) {
		t.Fatalf("log %q", s)
	}
	if _, code := runHookBinary(t, env, ""); code != 2 {
		t.Fatalf("no event: exit %d", code)
	}
}
