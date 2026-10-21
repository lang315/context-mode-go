package install

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

const postMatcher = "Bash|Read|Write|Edit|NotebookEdit|Glob|Grep|TodoWrite|TaskCreate|TaskUpdate|EnterPlanMode|ExitPlanMode|Skill|Agent|AskUserQuestion|EnterWorktree"

// HookEvents lists (settings event name, cmgo hook arg, matcher) in spec §3.5 order.
var HookEvents = []struct{ Event, Arg, Matcher string }{
	{"PreToolUse", "pretooluse", "Bash|WebFetch|Read|Grep|Agent"},
	{"PostToolUse", "posttooluse", postMatcher},
	{"PostToolUseFailure", "posttoolusefailure", postMatcher},
	{"UserPromptSubmit", "userpromptsubmit", ""},
	{"SessionStart", "sessionstart", ""},
	{"Stop", "stop", ""},
}

// hookCommand quotes the hook binary for sh -c, which Claude Code runs it
// through: $, backtick, backslash and quotes in the install path stay literal.
func hookCommand(hookBinary, arg string) string { return shellQuote(hookBinary) + " " + arg }

// shellWord reads the first word of a shell command line: single-quoted,
// double-quoted (older installs wrote that form) or bare, joined as sh does
// ('it'\”s' is it's). It returns the word and the rest of the line.
func shellWord(cmd string) (word, rest string, ok bool) {
	var b strings.Builder
	for i := 0; i < len(cmd); i++ {
		switch c := cmd[i]; c {
		case '\'':
			j := strings.IndexByte(cmd[i+1:], '\'')
			if j < 0 {
				return "", "", false
			}
			b.WriteString(cmd[i+1 : i+1+j])
			i += j + 1
		case '"':
			j := strings.IndexByte(cmd[i+1:], '"')
			if j < 0 {
				return "", "", false
			}
			b.WriteString(cmd[i+1 : i+1+j])
			i += j + 1
		case '\\':
			if i+1 < len(cmd) {
				i++
				b.WriteByte(cmd[i])
			}
		case ' ', '\t':
			return b.String(), cmd[i:], true
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), "", true
}

// isOwnCommand reports whether cmd is a cmgo hook command from any install
// location: first word (single-quoted, double-quoted or bare) with basename
// cmgo-hook, then "<arg>"; or the form from before cmgo-hook existed,
// basename cmgo, then "hook <arg>". Both match so a reinstall replaces either.
func isOwnCommand(cmd string) bool {
	exe, rest, ok := shellWord(cmd)
	if !ok {
		return false
	}
	f := strings.Fields(rest)
	var arg string
	switch filepath.Base(exe) {
	case "cmgo-hook":
		if len(f) == 1 {
			arg = f[0]
		}
	case "cmgo":
		if len(f) == 2 && f[0] == "hook" {
			arg = f[1]
		}
	}
	return arg != "" && slices.ContainsFunc(HookEvents, func(h struct{ Event, Arg, Matcher string }) bool { return h.Arg == arg })
}

// stripHooks removes cmgo's inner commands under every event and returns how
// many it removed. Entries, event arrays and the hooks key are dropped only
// when this call emptied them, so user-made empty values survive. Values of an
// unexpected shape are left alone.
func stripHooks(root *object) int {
	hv, _ := root.get("hooks")
	hooks, ok := hv.(*object)
	if !ok {
		return 0
	}
	total := 0
	for _, ev := range slices.Clone(hooks.keys) {
		entries, ok := hooks.vals[ev].([]any)
		if !ok {
			continue
		}
		kept := []any{}
		for _, e := range entries {
			if n, empty := stripEntry(e); n > 0 {
				total += n
				if empty {
					continue
				}
			}
			kept = append(kept, e)
		}
		switch {
		case len(kept) == len(entries):
		case len(kept) == 0:
			hooks.del(ev)
		default:
			hooks.set(ev, kept)
		}
	}
	if total > 0 && len(hooks.keys) == 0 {
		root.del("hooks")
	}
	return total
}

// stripEntry removes cmgo's commands from one matcher entry and reports how
// many it removed and whether none are left.
func stripEntry(e any) (removed int, empty bool) {
	eo, ok := e.(*object)
	if !ok {
		return 0, false
	}
	inner, ok := eo.vals["hooks"].([]any)
	if !ok {
		return 0, false
	}
	kept := []any{}
	for _, h := range inner {
		if ho, ok := h.(*object); ok {
			if c, ok := ho.vals["command"].(string); ok && isOwnCommand(c) {
				continue
			}
		}
		kept = append(kept, h)
	}
	removed = len(inner) - len(kept)
	if removed > 0 {
		eo.set("hooks", kept)
	}
	return removed, len(kept) == 0
}

// addHooks appends one matcher entry per HookEvents row, creating the hooks
// object and event arrays as needed.
func addHooks(root *object, hookBinary string) error {
	hv, ok := root.get("hooks")
	if !ok {
		hv = newObject()
		root.set("hooks", hv)
	}
	hooks, ok := hv.(*object)
	if !ok {
		return fmt.Errorf(`"hooks" is not an object`)
	}
	for _, he := range HookEvents {
		cmd := newObject()
		cmd.set("type", "command")
		cmd.set("command", hookCommand(hookBinary, he.Arg))
		entry := newObject()
		entry.set("matcher", he.Matcher)
		entry.set("hooks", []any{cmd})
		ev, ok := hooks.get(he.Event)
		if !ok {
			hooks.set(he.Event, []any{entry})
			continue
		}
		arr, ok := ev.([]any)
		if !ok {
			return fmt.Errorf(`"hooks.%s" is not an array`, he.Event)
		}
		hooks.set(he.Event, append(arr, entry))
	}
	return nil
}
