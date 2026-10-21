package memory

import (
	"strings"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// AutoInjection ports buildAutoInjection (AI:30-102): the last role, the
// last 5 decisions, the last 10 unique skills and the last intent, in that
// priority, under a 500-token budget (a token is 4 UTF-16 units, rounded
// up). It returns "" when there is nothing to inject.
func AutoInjection(events []store.StoredEvent) string {
	var role, intent *store.StoredEvent
	var decisions, skills []string
	for i, ev := range events {
		switch ev.Category {
		case "role":
			role = &events[i]
		case "decision":
			decisions = append(decisions, ev.Data)
		case "skill":
			skills = append(skills, ev.Data)
		case "intent":
			intent = &events[i]
		}
	}
	skills = unique(skills)

	var parts []string
	budget := 500
	add := func(s string) {
		parts = append(parts, s)
		budget -= (hookio.JSLen(s) + 3) / 4
	}
	if role != nil {
		add("<behavioral_directive>\n" + hookio.JSPrefix(role.Data, 400) + "\n</behavioral_directive>")
	}
	if len(decisions) > 0 {
		// TS falls back to the last 3 when this block exceeds the budget. It
		// cannot: the role costs at most 112 tokens and 5 decisions 139.
		lines := make([]string, 0, 5)
		for _, d := range decisions[max(0, len(decisions)-5):] {
			lines = append(lines, "- "+hookio.JSPrefix(d, 100))
		}
		add("<rules>\nFollow these decisions:\n" + strings.Join(lines, "\n") + "\n</rules>")
	}
	if len(skills) > 0 && budget > 50 {
		add("<active_skills>\nRe-invoke if relevant: " + strings.Join(skills[max(0, len(skills)-10):], ", ") +
			"\nTo reload: call the Skill tool with the skill name.\n</active_skills>")
	}
	if intent != nil && budget > 20 {
		parts = append(parts, "<session_mode>"+intent.Data+"</session_mode>")
	}
	if len(parts) == 0 {
		return ""
	}
	return "<session_state source=\"compaction\">\n\n" + strings.Join(parts, "\n\n") + "\n\n</session_state>"
}
