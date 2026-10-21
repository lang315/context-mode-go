package capture

import (
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

func TestUserPromptSkips(t *testing.T) {
	for _, p := range []string{"", " \n\u3000", "<task-notification>x", "  <system-reminder>x", "<context_guidance>", "<tool-result>y"} {
		if evs := UserPrompt(p); evs != nil {
			t.Errorf("UserPrompt(%q) = %v, want nil", p, evs)
		}
	}
}

func TestUserPrompt(t *testing.T) {
	prompt := func(p string) store.Event { return ev("user_prompt", "user-prompt", 1, p) }
	intent := func(m string) store.Event { return ev("intent", "intent", 4, m) }
	cases := []struct {
		in   string
		want []store.Event
	}{
		// The stored prompt is raw; the extractors see it trimmed.
		{"  fix the login bug \n", []store.Event{prompt("  fix the login bug \n"), intent("implement")}},
		{"why does the build fail?", []store.Event{prompt("why does the build fail?"), intent("investigate")}},
		{"\t/plan\targ  ", []store.Event{prompt("\t/plan\targ  "), ev("plan_enter", "plan", 2, "plan via /plan slash: arg"), intent("implement")}},
		{"/plans x", []store.Event{prompt("/plans x"), intent("implement")}},
		{"/plan " + strings.Repeat("é", 130), []store.Event{prompt("/plan " + strings.Repeat("é", 130)),
			ev("plan_enter", "plan", 2, "plan via /plan slash: "+strings.Repeat("é", 120))}},
		{"不要用redis，用sqlite做缓存层就好了吧好吗", []store.Event{prompt("不要用redis，用sqlite做缓存层就好了吧好吗"),
			ev("decision", "decision", 2, "不要用redis，用sqlite做缓存层就好了吧好吗"), intent("implement")}},
		{"please always respond in English", []store.Event{prompt("please always respond in English"),
			ev("role", "role", 3, "please always respond in English"), intent("implement")}},
		{"あなたは経験豊富なエンジニアです。", []store.Event{prompt("あなたは経験豊富なエンジニアです。"),
			ev("role", "role", 3, "あなたは経験豊富なエンジニアです。"), intent("implement")}},
		{"That's fine for now", []store.Event{prompt("That's fine for now"), intent("implement")}},
		{"ok, you are a cat", []store.Event{prompt("ok, you are a cat"), ev("decision", "decision", 2, "ok, you are a cat"), intent("implement")}},
		{"/goal\nmulti\nline", []store.Event{prompt("/goal\nmulti\nline"), intent("implement"), ev("goal", "goal", 4, "multi\nline")}},
		{"GOAL:   x ", []store.Event{prompt("GOAL:   x "), intent("implement"), ev("goal", "goal", 4, "x")}},
		{"Objective :  ", []store.Event{prompt("Objective :  "), intent("implement")}},
		{"at Object.<anonymous> (/a/b.js:10:5)", []store.Event{prompt("at Object.<anonymous> (/a/b.js:10:5)"),
			intent("implement"), ev("blocker", "blocked-on", 2, "at Object.<anonymous> (/a/b.js:10:5)")}},
		{"TypeError: boom", []store.Event{prompt("TypeError: boom"), intent("implement")}}, // \b needs a word start
		{"✅ Error: gone", []store.Event{prompt("✅ Error: gone"), intent("implement"), ev("blocker_resolved", "blocked-on", 2, "✅ Error: gone")}},
		{strings.Repeat("a", 59), []store.Event{prompt(strings.Repeat("a", 59)), intent("implement")}},
		{strings.Repeat("a", 60), []store.Event{prompt(strings.Repeat("a", 60))}},
		{strings.Repeat("x", 1030), []store.Event{prompt(strings.Repeat("x", 1030))}}, // the data event is dropped
		{"1234 5678", []store.Event{prompt("1234 5678")}},
	}
	for _, c := range cases {
		checkEvents(t, c.in, UserPrompt(c.in), c.want)
	}
}
