package capture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

func TestStop(t *testing.T) {
	done := "done <&>"
	long := strings.Repeat("a", 1999) + "😀b" // the pair would straddle unit 2000
	cases := []struct {
		active bool
		msg    *string
		want   string
	}{
		{false, &done, `{"stop_hook_active":false,"last_assistant_message":"done <&>"}`},
		{true, nil, `{"stop_hook_active":true,"last_assistant_message":null}`},
		{false, &long, `{"stop_hook_active":false,"last_assistant_message":"` + strings.Repeat("a", 1999) + `"}`},
	}
	for _, c := range cases {
		got := Stop(c.active, c.msg)
		if want := ev("turn_end", "session", 1, c.want); got != want {
			t.Errorf("Stop = %+v, want %+v", got, want)
		}
	}
}

func TestPostToolUseFailure(t *testing.T) {
	cases := []struct{ payload, want string }{
		{`{"tool_name":"Bash","error":"exit code 1: boom","tool_response":{"x":1}}`, "exit code 1: boom"},
		{`{"tool_name":"Bash","error":{"code":1},"tool_response":{ "stderr" : "<e>" }}`, `{"stderr":"<e>"}`},
		{`{"tool_name":"Bash","tool_response":"plain"}`, `"plain"`},
		{`{"tool_name":"Bash", "is_interrupt":true}`, `{"tool_name":"Bash","is_interrupt":true}`},
	}
	for _, c := range cases {
		p, err := hookio.ParseObject([]byte(c.payload))
		if err != nil {
			t.Fatal(err)
		}
		checkEvents(t, c.payload, PostToolUseFailure("Bash", p), []store.Event{ev("error_tool", "error", 2, c.want)})
	}
}

func TestSessionStartRules(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	userMD := filepath.Join(home, ".claude", "CLAUDE.md")
	write(userMD, "# user\n")
	write(filepath.Join(proj, "CLAUDE.md"), " \n\u3000\ufeff") // blank after JS trim
	nested := filepath.Join(proj, ".claude", "CLAUDE.md")
	write(nested, "nested")
	want := []store.Event{
		ev("rule", "rule", 1, userMD), ev("rule_content", "rule", 1, "# user\n"),
		ev("rule", "rule", 1, nested), ev("rule_content", "rule", 1, "nested"),
	}
	checkEvents(t, "rules", SessionStartRules(home, proj), want)
	checkEvents(t, "no home, no files", SessionStartRules("", t.TempDir()), nil)
}

func TestSessionStartEvent(t *testing.T) {
	got := SessionStartEvent("startup", `/p "q"`, time.Unix(1761000000, 999e6))
	want := ev("session_start", "session_start", 1, `{"source":"startup","project_dir":"/p \"q\"","started_at":1761000000}`)
	if got != want {
		t.Errorf("SessionStartEvent = %+v, want %+v", got, want)
	}
}
