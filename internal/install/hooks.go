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

// hookCommand quotes the binary so a path with spaces still runs.
func hookCommand(binary, arg string) string { return `"` + binary + `" hook ` + arg }

// isOwnCommand reports whether cmd is a cmgo hook command from any install
// location: first token (quoted or bare) with basename cmgo, then "hook <arg>".
func isOwnCommand(cmd string) bool {
	var exe, rest string
	if s, ok := strings.CutPrefix(cmd, `"`); ok {
		i := strings.IndexByte(s, '"')
		if i < 0 {
			return false
		}
		exe, rest = s[:i], s[i+1:]
	} else {
		exe, rest, _ = strings.Cut(cmd, " ")
	}
	f := strings.Fields(rest)
	return filepath.Base(exe) == "cmgo" && len(f) == 2 && f[0] == "hook" &&
		slices.ContainsFunc(HookEvents, func(h struct{ Event, Arg, Matcher string }) bool { return h.Arg == f[1] })
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
func addHooks(root *object, binary string) error {
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
		cmd.set("command", hookCommand(binary, he.Arg))
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
