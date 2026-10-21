package capture

import (
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

func TestFNV1a32(t *testing.T) {
	// From the TS bundle: plan_hash for these plans.
	for in, want := range map[string]string{"Refactor auth": "fd047da3", "计划😀": "c89e473f", "p": "f50c43ef", "": "811c9dc5"} {
		if got := fnv1a32(in); got != want {
			t.Errorf("fnv1a32(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestPlanEvents(t *testing.T) {
	cases := []struct {
		name, tool, input, resp string
		want                    []store.Event
	}{
		{"enter", "EnterPlanMode", `{}`, `"ok"`, []store.Event{ev("plan_enter", "plan", 2, "entered plan mode")}},
		{"exit approved", "ExitPlanMode", `{"plan":"Refactor auth","allowedPrompts":[{"prompt":"Edit src/auth.ts"},"run tests"]}`,
			`"User has approved your plan"`, []store.Event{
				ev("plan_exit", "plan", 2, "exited plan mode (allowed: Edit src/auth.ts, run tests) plan_bytes:13 plan_hash:fd047da3"),
				ev("plan_approved", "plan", 1, "plan approved by user")}},
		{"exit, plan from response, odd prompts", "ExitPlanMode",
			`{"allowedPrompts":[null,3,{"prompt":null},{"tool":"Bash"},["a",null]]}`,
			`{"plan":"计划😀","isAgent":false,"note":"User approved"}`, []store.Event{
				ev("plan_exit", "plan", 2, "exited plan mode (allowed: null, 3, null, [object Object], a,) plan_bytes:4 plan_hash:c89e473f"),
				ev("plan_approved", "plan", 1, "plan approved by user")}},
		{"exit, empty plans", "ExitPlanMode", `{"plan":"","allowedPrompts":[]}`, `"{\"plan\":\"\"}"`,
			[]store.Event{ev("plan_exit", "plan", 2, "exited plan mode")}},
		{"exit declined", "ExitPlanMode", `{"plan":"p"}`, `"User DECLINED"`, []store.Event{
			ev("plan_exit", "plan", 2, "exited plan mode plan_bytes:1 plan_hash:f50c43ef"),
			ev("plan_rejected", "plan", 2, "plan rejected: User DECLINED")}},
		{"exit rejected, no plan", "ExitPlanMode", `{}`, `"User rejected the plan"`, []store.Event{
			ev("plan_exit", "plan", 2, "exited plan mode"),
			ev("plan_rejected", "plan", 2, "plan rejected: User rejected the plan")}},
		{"write plan file", "Write", `{"file_path":"/home/u/.claude/plans/my-plan.md"}`, `"ok"`,
			[]store.Event{ev("plan_file_write", "plan", 2, "plan file: my-plan.md")}},
		{"edit plan file, backslashes", "Edit", `{"file_path":"C:\\u\\.claude\\plans\\p.md"}`, `"ok"`,
			[]store.Event{ev("plan_file_write", "plan", 2, "plan file: p.md")}},
		{"write other file", "Write", `{"file_path":"/p/.claude/x.md"}`, `"ok"`, nil},
		{"read plan file", "Read", `{"file_path":"/u/.claude/plans/p.md"}`, `"ok"`, nil},
	}
	for _, c := range cases {
		checkEvents(t, c.name, planEvents(newCall(t, c.tool, c.input, c.resp)), c.want)
	}
}
