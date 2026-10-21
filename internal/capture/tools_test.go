package capture

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// newCall builds the extractor input the way PostToolUse does.
func newCall(t *testing.T, tool, input, resp string) call {
	t.Helper()
	in, err := hookio.ParseObject([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(resp)
	return call{tool: tool, input: in, raw: raw, resp: responseText(raw), home: "/home/test"}
}

func checkEvents(t *testing.T, name string, got, want []store.Event) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %+v\nwant %+v", name, got, want)
	}
}

func TestFileEvents(t *testing.T) {
	cases := []struct {
		name, tool, input, resp string
		want                    []store.Event
	}{
		{"read plain", "Read", `{"file_path":"/p/a.ts"}`, `"x"`,
			[]store.Event{{Type: "file_read", Category: "file", Priority: 1, Data: "/p/a.ts"}}},
		{"read rule, string response", "Read", `{"file_path":"/p/CLAUDE.md"}`, `"# rules"`, []store.Event{
			{Type: "rule", Category: "rule", Priority: 1, Data: "/p/CLAUDE.md"},
			{Type: "rule_content", Category: "rule", Priority: 1, Data: "# rules"},
			{Type: "file_read", Category: "file", Priority: 1, Data: "/p/CLAUDE.md"}}},
		// spec §3.4: the file text, not the stringified response object.
		{"read rule, CC object response", "Read", `{"file_path":"/p/.claude/x.md"}`,
			`{"type":"text","file":{"filePath":"/p/.claude/x.md","content":"body\n"}}`, []store.Event{
				{Type: "rule", Category: "rule", Priority: 1, Data: "/p/.claude/x.md"},
				{Type: "rule_content", Category: "rule", Priority: 1, Data: "body\n"},
				{Type: "file_read", Category: "file", Priority: 1, Data: "/p/.claude/x.md"}}},
		{"read rule, object without file.content", "Read", `{"file_path":"/p/GEMINI.md"}`, `{"type":"image"}`, []store.Event{
			{Type: "rule", Category: "rule", Priority: 1, Data: "/p/GEMINI.md"},
			{Type: "rule_content", Category: "rule", Priority: 1, Data: `{"type":"image"}`},
			{Type: "file_read", Category: "file", Priority: 1, Data: "/p/GEMINI.md"}}},
		{"read rule, empty file content", "Read", `{"file_path":"/p/KIRO.md"}`, `{"file":{"content":""}}`, []store.Event{
			{Type: "rule", Category: "rule", Priority: 1, Data: "/p/KIRO.md"},
			{Type: "file_read", Category: "file", Priority: 1, Data: "/p/KIRO.md"}}},
		// JS /i without /u folds ASCII only: U+212A KELVIN SIGN is not "k".
		{"no unicode case folding", "Read", `{"file_path":"/p/\u212aIRO.md"}`, `"x"`,
			[]store.Event{{Type: "file_read", Category: "file", Priority: 1, Data: "/p/\u212aIRO.md"}}},
		{"edit", "Edit", `{"file_path":"/p/a.ts"}`, `{}`,
			[]store.Event{{Type: "file_edit", Category: "file", Priority: 1, Data: "/p/a.ts"}}},
		{"notebook", "NotebookEdit", `{"notebook_path":"/p/n.ipynb"}`, `"ok"`,
			[]store.Event{{Type: "file_edit", Category: "file", Priority: 1, Data: "/p/n.ipynb"}}},
		{"write missing path", "Write", `{}`, `"ok"`,
			[]store.Event{{Type: "file_write", Category: "file", Priority: 1, Data: ""}}},
		{"glob", "Glob", `{"pattern":"**/*.ts"}`, `"a"`,
			[]store.Event{{Type: "file_glob", Category: "file", Priority: 3, Data: "**/*.ts"}}},
		{"grep no path", "Grep", `{"pattern":"foo"}`, `"a"`,
			[]store.Event{{Type: "file_search", Category: "file", Priority: 3, Data: "foo in "}}},
		{"other tool", "Bash", `{"command":"ls"}`, `"a"`, nil},
	}
	for _, c := range cases {
		checkEvents(t, c.name, fileEvents(newCall(t, c.tool, c.input, c.resp)), c.want)
	}
}

func TestBashSimpleEvents(t *testing.T) {
	cwd := func(d string) []store.Event {
		return []store.Event{{Type: "cwd", Category: "cwd", Priority: 2, Data: d}}
	}
	env := func(d string) []store.Event {
		return []store.Event{{Type: "env", Category: "env", Priority: 2, Data: d}}
	}
	cases := []struct {
		name, cmd string
		cwd, env  []store.Event
	}{
		{"cd plain", "cd /p/sub && git pull", cwd("/p/sub"), nil},
		{"cd double quotes", `cd "a b" && ls`, cwd("a b"), nil},
		{"cd after NBSP", "cd\u00a0/x", cwd("/x"), nil},
		{"abcd is not cd", "abcd /x", nil, nil},
		{"export masked", "export API_KEY=sk-123 && npm install", nil, env("export API_KEY=*** && npm install")},
		{"export stops at U+3000", "export T=abc\u3000x", nil, env("export T=***\u3000x")},
		{"activate", "source .venv/bin/activate", nil, env("source .venv/bin/activate")},
		{"pyenv", "pyenv local 3.12", nil, env("pyenv local 3.12")},
		{"go install", "go install ./...", nil, env("go install ./...")},
		{"not env", "npm test", nil, nil},
	}
	for _, c := range cases {
		in := newCall(t, "Bash", `{"command":`+string(hookio.QuoteString(c.cmd))+`}`, `{}`)
		checkEvents(t, c.name+" cwd", cwdEvents(in), c.cwd)
		checkEvents(t, c.name+" env", envEvents(in), c.env)
	}
	checkEvents(t, "cd on Read", cwdEvents(newCall(t, "Read", `{"command":"cd /x"}`, `""`)), nil)
}

func TestToolSpecificEvents(t *testing.T) {
	checkEvents(t, "task", taskEvents(newCall(t, "TaskCreate", `{"subject":"a <b> & c", "n":1.5,"z":null}`, `"ok"`)),
		[]store.Event{{Type: "task_create", Category: "task", Priority: 1, Data: `{"subject":"a <b> & c","n":1.5,"z":null}`}})
	checkEvents(t, "todo", taskEvents(newCall(t, "TodoWrite", `{"todos":[]}`, `"ok"`)),
		[]store.Event{{Type: "task", Category: "task", Priority: 1, Data: `{"todos":[]}`}})
	checkEvents(t, "skill", skillEvents(newCall(t, "Skill", `{"skill":"caveman"}`, `"ok"`)),
		[]store.Event{{Type: "skill", Category: "skill", Priority: 2, Data: "caveman"}})
	checkEvents(t, "agent object", subagentEvents(newCall(t, "Agent", `{"description":"d","prompt":"find X"}`, `{"isAsync":true}`)),
		[]store.Event{{Type: "subagent_completed", Category: "subagent", Priority: 2, Data: `[completed] find X → {"isAsync":true}`}})
	// A missing response is the text `""`, so TS never saw a launch.
	checkEvents(t, "agent no response", subagentEvents(newCall(t, "Agent", `{"description":"d"}`, ``)),
		[]store.Event{{Type: "subagent_completed", Category: "subagent", Priority: 2, Data: `[completed] d → ""`}})
	checkEvents(t, "agent empty string", subagentEvents(newCall(t, "Agent", `{"prompt":null,"description":"d"}`, `""`)),
		[]store.Event{{Type: "subagent_launched", Category: "subagent", Priority: 3, Data: "[launched] d"}})
	checkEvents(t, "worktree", worktreeEvents(newCall(t, "EnterWorktree", `{}`, `"ok"`)),
		[]store.Event{{Type: "worktree", Category: "env", Priority: 2, Data: "entered worktree: unnamed"}})
	checkEvents(t, "exit worktree dropped", worktreeEvents(newCall(t, "ExitWorktree", `{}`, `"ok"`)), nil)
}
