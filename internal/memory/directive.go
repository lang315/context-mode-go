package memory

import (
	"strings"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// searchTool is the name TS built with toolNamer("ctx_search").
const searchTool = "mcp__cmgo__ctx_search"

// Directive ports buildSessionDirective (SD:275-500) with the spec §2.4
// fixes: Last Request and <continue_from> read the last user-prompt event,
// prior-leg subagents are split by id, and the MCP Tools Used and Data
// References sections are gone (capture writes no mcp or data events).
// source is "compact" or "resume"; events are one session's rows in id
// order. It returns "" for no events, the guard TS kept in its caller.
func Directive(source string, events []store.StoredEvent) string {
	if len(events) == 0 {
		return ""
	}
	g := group(events)
	isCompact := source == "compact"
	attr := "continue"
	if isCompact {
		attr = "compact"
	}
	var b strings.Builder
	b.WriteString("\n<session_knowledge source=\"" + attr + "\">")
	b.WriteString("\n<session_guide>")

	if g.lastPrompt != "" {
		text(&b, "Last Request", cut(g.lastPrompt, 300))
	}
	pending, _ := g.tasks()
	list(&b, "Pending Tasks", pending)
	list(&b, "Key Decisions", mapData(g.cat["decision"], 150))
	if len(g.files) > 0 {
		text(&b, "Files Modified", strings.Join(g.files, ", "))
	}
	list(&b, "Unresolved Errors", mapData(g.cat["error"], 150))
	if git := uniqueData(g.cat["git"]); len(git) > 0 {
		text(&b, "Git", strings.Join(git, ", "))
	}
	if rules := ruleParents(g.cat["rule"]); len(rules) > 0 {
		text(&b, "Project Rules", strings.Join(rules, ", "))
	}
	current, prior := g.subagents()
	list(&b, "Subagent Tasks", mapData(current, 120))
	list(&b, "Subagent Tasks (earlier session — not current)", mapData(prior, 120))
	if skills := uniqueData(g.cat["skill"]); len(skills) > 0 {
		text(&b, "Skills Used", strings.Join(skills, ", "))
	}
	if env := environment(g); len(env) > 0 {
		text(&b, "Environment", strings.Join(env, "\n"))
	}
	if last, ok := lastData(g.cat["intent"]); ok {
		text(&b, "Session Intent", last)
	}
	if last, ok := lastData(g.cat["role"]); ok {
		text(&b, "User Role", last)
	}
	if plans := g.cat["plan"]; len(plans) > 0 {
		var lines []string
		switch {
		case hasType(plans, "plan_approved"):
			lines = []string{"Status: APPROVED AND EXECUTED", "The plan was approved and executed. Do NOT re-enter plan mode or re-propose the same plan."}
		case hasType(plans, "plan_rejected"):
			lines = []string{"Status: REJECTED BY USER", "The user rejected the previous plan. Ask what they want changed before re-planning."}
		case planActive(plans):
			lines = []string{"Status: ACTIVE (in planning phase)"}
			if hasType(plans, "plan_file_write") {
				lines = append(lines, "Plan file has been written. Awaiting user approval via ExitPlanMode.")
			}
		default:
			lines = []string{"Status: COMPLETED", "The plan has been executed. Do NOT re-enter plan mode or re-propose the same plan."}
		}
		list(&b, "Plan Mode", lines)
	}
	b.WriteString("\n</session_guide>")

	b.WriteString("\n<session_search>")
	b.WriteString("\nDetailed session data is indexed in context-mode FTS5 (source: \"session-events\").")
	b.WriteString("\nUse " + searchTool + "(queries: [...], source: \"session-events\") when you need specifics.")
	b.WriteString("\nDo NOT call ctx_index() — data is already indexed.")
	b.WriteString("\n</session_search>")
	if g.lastPrompt != "" && isCompact {
		b.WriteString("\n<continue_from>Continue working on the last request. Do NOT ask the user to repeat themselves.</continue_from>")
	}
	b.WriteString("\n</session_knowledge>")
	return b.String()
}

// text writes a directive section whose body is one block of text.
func text(b *strings.Builder, title, body string) {
	b.WriteString("\n## " + title + "\n" + body + "\n")
}

// list writes a directive section of "- " lines; nothing when items is empty.
func list(b *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	b.WriteString("\n## " + title)
	for _, it := range items {
		b.WriteString("\n- " + it)
	}
	b.WriteString("\n")
}

// cut keeps s when it is at most n UTF-16 units, else its first n-3 units
// and "...", as TS s.length > n ? s.substring(0, n-3) + "..." : s.
func cut(s string, n int) string {
	if hookio.JSLen(s) <= n {
		return s
	}
	return hookio.JSPrefix(s, n-3) + "..."
}

// mapData returns each event's data, cut to n units.
func mapData(evs []store.StoredEvent, n int) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = cut(ev.Data, n)
	}
	return out
}

// dataOf returns each event's data.
func dataOf(evs []store.StoredEvent) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.Data
	}
	return out
}

// uniqueData returns the distinct data values, first seen first.
func uniqueData(evs []store.StoredEvent) []string {
	return unique(dataOf(evs))
}

// unique drops repeats, first seen first (a JS Set).
func unique(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

func lastData(evs []store.StoredEvent) (string, bool) {
	if len(evs) == 0 {
		return "", false
	}
	return evs[len(evs)-1].Data, true
}

// ruleParents returns the last two path segments of each rule path (not
// rule_content), unique (SD:372-385).
func ruleParents(rules []store.StoredEvent) []string {
	var paths []string
	for _, ev := range rules {
		if ev.Type == "rule_content" {
			continue
		}
		segs := strings.Split(strings.ReplaceAll(ev.Data, `\`, "/"), "/") // JS split(/[/\\]/)
		if len(segs) > 2 {
			segs = segs[len(segs)-2:]
		}
		paths = append(paths, strings.Join(segs, "/"))
	}
	return unique(paths)
}

// environment returns "cwd: <last cwd>" and then every env event's data,
// the lines of both Environment sections.
func environment(g groups) []string {
	var lines []string
	if cwd, ok := lastData(g.cat["cwd"]); ok {
		lines = append(lines, "cwd: "+cwd)
	}
	return append(lines, dataOf(g.cat["env"])...)
}

func hasType(evs []store.StoredEvent, typ string) bool {
	for _, ev := range evs {
		if ev.Type == typ {
			return true
		}
	}
	return false
}

// planActive: the last plan event entered plan mode or wrote the plan file.
func planActive(plans []store.StoredEvent) bool {
	last := plans[len(plans)-1].Type
	return last == "plan_enter" || last == "plan_file_write"
}
