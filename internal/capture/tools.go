package capture

import (
	"encoding/json"
	"regexp"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// call is one PostToolUse input as the extractors see it.
type call struct {
	tool  string
	input *hookio.Object
	raw   json.RawMessage // tool_response as received; nil when absent
	resp  string          // the text TS extracted from (responseText)
	home  string
}

func ev(typ, cat string, prio int, data string) store.Event {
	return store.Event{Type: typ, Category: cat, Priority: prio, Data: data}
}

// ruleFile is the rule-file test of X:195-200, lowercased: it runs on
// asciiLower(path) because the TS regexps use `i` without `u`.
var ruleFile = []*regexp.Regexp{
	regexp.MustCompile(`(?:claude|agents(?:\.override)?|gemini|qwen|kiro)\.md$`),
	regexp.MustCompile(`/copilot-instructions\.md$`),
	regexp.MustCompile(`/context-mode\.mdc$`),
	regexp.MustCompile(`\.claude[\\/]`),
	regexp.MustCompile(`[\\/]memor(?:y|ies)[\\/][^\\/]+\.md$`),
}

func isRuleFile(path string) bool {
	lower := asciiLower(path)
	for _, re := range ruleFile {
		if re.MatchString(lower) {
			return true
		}
	}
	return false
}

// fileEvents ports extractFileAndRule (X:176-306) without apply_patch.
func fileEvents(c call) []store.Event {
	switch c.tool {
	case "Read":
		path := inputString(c.input, "file_path", "")
		var evs []store.Event
		if isRuleFile(path) {
			evs = append(evs, ev("rule", "rule", 1, path))
			if text := ruleContent(c); text != "" {
				evs = append(evs, ev("rule_content", "rule", 1, text))
			}
		}
		return append(evs, ev("file_read", "file", 1, path))
	case "Edit":
		return []store.Event{ev("file_edit", "file", 1, inputString(c.input, "file_path", ""))}
	case "NotebookEdit":
		return []store.Event{ev("file_edit", "file", 1, inputString(c.input, "notebook_path", ""))}
	case "Write":
		return []store.Event{ev("file_write", "file", 1, inputString(c.input, "file_path", ""))}
	case "Glob":
		return []store.Event{ev("file_glob", "file", 3, inputString(c.input, "pattern", ""))}
	case "Grep":
		data := inputString(c.input, "pattern", "") + " in " + inputString(c.input, "path", "")
		return []store.Event{ev("file_search", "file", 3, data)}
	}
	return nil
}

// ruleContent is the file text when Claude Code sent a FileReadOutput
// object, else the TS text (spec §3.4).
func ruleContent(c call) string {
	if top, err := hookio.ParseObject(c.raw); err == nil {
		if f, ok := top.Object("file"); ok {
			if s, ok := f.String("content"); ok {
				return s
			}
		}
	}
	return c.resp
}

var cdRe = regexp.MustCompile(`\bcd` + jsWS + `+("([^"]+)"|'([^']+)'|(` + jsNotWS + `+))`)

// cwdEvents ports extractCwd (X:312-327): the first cd in the command.
func cwdEvents(c call) []store.Event {
	if c.tool != "Bash" {
		return nil
	}
	m := cdRe.FindStringSubmatch(inputString(c.input, "command", ""))
	if m == nil {
		return nil
	}
	dir := m[2]
	if dir == "" {
		dir = m[3]
	}
	if dir == "" {
		dir = m[4]
	}
	return []store.Event{ev("cwd", "cwd", 2, dir)}
}

// envPatterns is ENV_PATTERNS (X:810-829).
var envPatterns = func() []*regexp.Regexp {
	srcs := []string{
		`\bsource` + jsWS + `+` + jsNotWS + `*activate\b`,
		`\bexport` + jsWS + `+\w+=`,
		`\bnvm` + jsWS + `+use\b`,
		`\bpyenv` + jsWS + `+(shell|local|global)\b`,
		`\bconda` + jsWS + `+activate\b`,
		`\brbenv` + jsWS + `+(shell|local|global)\b`,
		`\bnpm` + jsWS + `+install\b`,
		`\bnpm` + jsWS + `+ci\b`,
		`\bpip` + jsWS + `+install\b`,
		`\bbun` + jsWS + `+install\b`,
		`\byarn` + jsWS + `+(add|install)\b`,
		`\bpnpm` + jsWS + `+(add|install)\b`,
		`\bcargo` + jsWS + `+(install|add)\b`,
		`\bgo` + jsWS + `+(install|get)\b`,
		`\brustup\b`,
		`\basdf\b`,
		`\bvolta\b`,
		`\bdeno` + jsWS + `+install\b`,
	}
	res := make([]*regexp.Regexp, len(srcs))
	for i, s := range srcs {
		res[i] = regexp.MustCompile(s)
	}
	return res
}()

var exportRe = regexp.MustCompile(`\bexport` + jsWS + `+(\w+)=` + jsNotWS + `*`)

// envEvents ports extractEnv (X:831-847), masking export values.
func envEvents(c call) []store.Event {
	if c.tool != "Bash" {
		return nil
	}
	cmd := inputString(c.input, "command", "")
	for _, re := range envPatterns {
		if re.MatchString(cmd) {
			return []store.Event{ev("env", "env", 2, exportRe.ReplaceAllString(cmd, "export ${1}=***"))}
		}
	}
	return nil
}

// taskEvents ports extractTask (X:653-668): data is JSON.stringify(tool_input).
func taskEvents(c call) []store.Event {
	types := map[string]string{"TodoWrite": "task", "TaskCreate": "task_create", "TaskUpdate": "task_update"}
	typ, ok := types[c.tool]
	if !ok {
		return nil
	}
	b, err := c.input.MarshalJSON()
	if err != nil {
		return nil
	}
	return []store.Event{ev(typ, "task", 1, string(b))}
}

// skillEvents ports extractSkill (X:853-863).
func skillEvents(c call) []store.Event {
	if c.tool != "Skill" {
		return nil
	}
	return []store.Event{ev("skill", "skill", 2, inputString(c.input, "skill", ""))}
}

// subagentEvents ports extractSubagent (X:900-915).
func subagentEvents(c call) []store.Event {
	if c.tool != "Agent" {
		return nil
	}
	prompt := inputString(c.input, "prompt", inputString(c.input, "description", ""))
	if c.resp != "" {
		return []store.Event{ev("subagent_completed", "subagent", 2, "[completed] "+prompt+" → "+c.resp)}
	}
	return []store.Event{ev("subagent_launched", "subagent", 3, "[launched] "+prompt)}
}

// worktreeEvents ports the EnterWorktree half of extractWorktree (X:1243-1252).
func worktreeEvents(c call) []store.Event {
	if c.tool != "EnterWorktree" {
		return nil
	}
	return []store.Event{ev("worktree", "env", 2, "entered worktree: "+inputString(c.input, "name", "unnamed"))}
}
