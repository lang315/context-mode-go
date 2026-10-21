package routing

import (
	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
	"regexp"
	"sync"
)

// echoReason pulls the message out of a Bash redirect (formatters.mjs:53).
var echoReason = sync.OnceValue(func() *regexp.Regexp { return jsRegexp(`(?s)^echo\s+"(.+)"$`) })

// fallbackReason is the deny text for a modify whose command is not an echo
// (formatters.mjs:56); routing never builds one.
const fallbackReason = "Redirected to ctx_execute / ctx_fetch_and_index. Call ctx_execute(language, code) to fetch and derive your answer in one round trip, or call ctx_fetch_and_index(url, source) when you want to query the response later via ctx_search. Both have full network access. Retry the same call on a transient DNS error (EAI_AGAIN, ETIMEDOUT, ENETUNREACH)."

// hookOutput is hookSpecificOutput with the keys in TS order.
type hookOutput struct {
	HookEventName            string         `json:"hookEventName"`
	PermissionDecision       string         `json:"permissionDecision,omitempty"`
	PermissionDecisionReason *string        `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             *hookio.Object `json:"updatedInput,omitempty"`
	AdditionalContext        *string        `json:"additionalContext,omitempty"`
}

// Format ports the Claude Code formatter (formatters.mjs:7-71): nil for a
// passthrough, else one compact JSON line ending in "\n". A modify that
// carries a command is a deny, because Claude Code ignores a replaced Bash
// command; any other modify is updatedInput alone, with no
// permissionDecision (R1 P23).
func Format(dec Decision) []byte {
	out := hookOutput{HookEventName: "PreToolUse"}
	if reason, ok := denyReason(dec); ok {
		out.PermissionDecision, out.PermissionDecisionReason = "deny", &reason
	} else {
		switch dec.Action {
		case "ask":
			out.PermissionDecision = "ask"
		case "modify":
			out.UpdatedInput = dec.UpdatedInput
		case "context":
			out.AdditionalContext = &dec.AdditionalContext
		default:
			return nil
		}
	}
	b, err := hookio.Marshal(struct {
		HookSpecificOutput hookOutput `json:"hookSpecificOutput"`
	}{out})
	if err != nil {
		return nil // updatedInput holds JSON that already parsed, so this cannot happen
	}
	return append(b, '\n')
}

// denyReason returns the reason Claude Code is shown when dec is emitted as
// a deny: a deny, or a modify carrying a command.
func denyReason(dec Decision) (string, bool) {
	switch {
	case dec.Action == "deny":
		return dec.Reason, true
	case dec.Action == "modify" && dec.UpdatedInput != nil && dec.UpdatedInput.Has("command"):
		cmd, _ := dec.UpdatedInput.String("command")
		if m := echoReason().FindStringSubmatch(cmd); m != nil {
			return m[1], true
		}
		return fallbackReason, true
	}
	return "", false
}

// Events returns the store events PreToolUse records for dec (spec §2.4):
// the redirect event first, then a rejected event for anything Claude Code
// sees as a deny. TS wrote these as two separate marker files, so it had no
// order; redirect first keeps the cause before the refusal in the timeline.
func Events(toolName string, dec Decision) []store.Event {
	var evs []store.Event
	if r := dec.Redirect; r != nil {
		evs = append(evs, store.Event{Type: r.Type, Category: "redirect", Priority: 2, Data: r.Tool + ": " + r.Summary, BytesAvoided: r.BytesAvoided})
	}
	if reason, ok := denyReason(dec); ok {
		evs = append(evs, store.Event{Type: "rejected", Category: "rejected-approach", Priority: 2, Data: toolName + ": " + reason})
	}
	return evs
}
