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

func sessionStartOut() string {
	return `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":` +
		string(hookio.QuoteString(routing.RoutingBlock(false, false))) + "}}\n"
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

	if out := callHook(t, env, "sessionstart", `{"session_id":"s1","source":"startup"}`); out != sessionStartOut() {
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
	for _, src := range []string{"resume", "compact", "clear"} {
		if out := callHook(t, env, "sessionstart", `{"session_id":"s1","source":"`+src+`"}`); out != sessionStartOut() {
			t.Fatalf("%s: got %q", src, out)
		}
	}
	noDB(t, env)
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
