package routing

import (
	"reflect"
	"testing"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

func TestFormat(t *testing.T) {
	agent := &hookio.Object{}
	agent.SetString("prompt", "a<b>&c")
	agent.Set("n", []byte(`1`))
	notEcho := &hookio.Object{}
	notEcho.SetString("command", "ls")
	cases := []struct {
		name string
		dec  Decision
		want string
	}{
		{"passthrough", Decision{}, ""},
		{"deny", Decision{Action: "deny", Reason: "no <x> & \u2028"},
			"{\"hookSpecificOutput\":{\"hookEventName\":\"PreToolUse\",\"permissionDecision\":\"deny\",\"permissionDecisionReason\":\"no <x> & \u2028\"}}\n"},
		{"ask", Decision{Action: "ask", Reason: "ignored"},
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask"}}` + "\n"},
		{"modify agent", Decision{Action: "modify", UpdatedInput: agent},
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":{"prompt":"a<b>&c","n":1}}}` + "\n"},
		{"modify command", redirectTo("line1\n\"q\" \\x"),
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"line1\n\"q\" \\x"}}` + "\n"},
		{"modify command not echo", Decision{Action: "modify", UpdatedInput: notEcho},
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"` + fallbackReason + `"}}` + "\n"},
		{"context", Decision{Action: "context", AdditionalContext: "<context_guidance>"},
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"<context_guidance>"}}` + "\n"},
		{"empty context", Decision{Action: "context"},
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":""}}` + "\n"},
	}
	for _, c := range cases {
		if got := string(Format(c.dec)); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	if Format(Decision{}) != nil {
		t.Error("passthrough is not nil")
	}
}

func TestEvents(t *testing.T) {
	d := testDeps(t)
	curl := Route("Bash", bash("curl https://x.com"), false, d)
	web := Route("WebFetch", input(t, `{"url":"https://e.com/a"}`), false, d)
	cases := []struct {
		name, tool string
		dec        Decision
		want       []store.Event
	}{
		{"security deny", "Bash", Route("Bash", bash("sudo ls"), false, d), []store.Event{
			{Type: "rejected", Category: "rejected-approach", Priority: 2, Data: "Bash: Blocked by security policy: matches deny pattern Bash(sudo *)"},
		}},
		{"curl", "Bash", curl, []store.Event{
			{Type: "bash-redirected", Category: "redirect", Priority: 2, Data: "Bash: curl https://x.com", BytesAvoided: 8192},
			{Type: "rejected", Category: "rejected-approach", Priority: 2, Data: "Bash: " + curlMessage},
		}},
		{"inline", "Bash", Route("Bash", bash(`node -e "fetch('https://a.b')"`), false, d), []store.Event{
			{Type: "rejected", Category: "rejected-approach", Priority: 2, Data: "Bash: " + inlineMessage},
		}},
		{"webfetch", "WebFetch", web, []store.Event{
			{Type: "webfetch-redirected", Category: "redirect", Priority: 2, Data: "WebFetch: https://e.com/a", BytesAvoided: 16384},
			{Type: "rejected", Category: "rejected-approach", Priority: 2, Data: "WebFetch: " + webFetchMessage("https://e.com/a")},
		}},
		{"large read", "Read", Decision{Action: "context", AdditionalContext: readGuidance, Redirect: &Redirect{Tool: "Read", Type: "read-redirected", BytesAvoided: 50001, Summary: "/a/big.txt"}}, []store.Event{
			{Type: "read-redirected", Category: "redirect", Priority: 2, Data: "Read: /a/big.txt", BytesAvoided: 50001},
		}},
		{"ask", "Bash", Decision{Action: "ask"}, nil},
		{"agent", "Agent", Route("Agent", input(t, `{"prompt":"x"}`), false, d), nil},
		{"guidance", "Grep", Decision{Action: "context", AdditionalContext: grepGuidance}, nil},
		{"passthrough", "Bash", Decision{}, nil},
	}
	for _, c := range cases {
		if got := Events(c.tool, c.dec); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}
