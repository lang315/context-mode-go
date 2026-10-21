// Package capture turns hook inputs into session events (hooks spec §3.3).
// The extractors port src/session/extract.ts (X:) at a5e6dd31: same order,
// regexps, data formats and JS length rules, minus the extractors spec §3.4
// drops and with its fixes. Nothing here touches the store.
package capture

import (
	"encoding/json"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// extractors run in the extractEvents order (X:2710-2750).
var extractors = []func(call) []store.Event{
	fileEvents, cwdEvents, gitEvents, envEvents, taskEvents,
	planEvents, skillEvents, subagentEvents, decisionEvents, worktreeEvents,
}

// PostToolUse runs the kept extractors (spec §3.3) in TS order. home expands
// "~" in git -C paths. toolResponse is the raw JSON value, nil when absent.
func PostToolUse(toolName string, toolInput *hookio.Object, toolResponse json.RawMessage, home string) []store.Event {
	if toolInput == nil {
		toolInput = &hookio.Object{} // tool_input ?? {}
	}
	c := call{tool: toolName, input: toolInput, raw: toolResponse, resp: responseText(toolResponse), home: home}
	var evs []store.Event
	for _, x := range extractors {
		evs = append(evs, x(c)...)
	}
	return evs
}
