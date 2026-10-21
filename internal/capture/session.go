package capture

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// PostToolUseFailure records a failed tool call (spec §3.3, new in cmgo):
// the payload's error string, else the compact JSON of tool_response, else
// the compact JSON of the whole payload. The field name comes from the
// Claude Code docs; the fallbacks keep a row when the shape differs.
// toolName is not part of the data: spec §3.3 stores the error text alone.
func PostToolUseFailure(toolName string, payload *hookio.Object) []store.Event {
	if payload == nil {
		payload = &hookio.Object{}
	}
	data, ok := payload.String("error")
	if !ok {
		if raw, has := payload.Get("tool_response"); has {
			data = compactJS(raw)
		} else if b, err := payload.MarshalJSON(); err == nil {
			data = string(b)
		}
	}
	return []store.Event{ev("error_tool", "error", 2, data)}
}

// Stop ports stop.mjs:32-43: turn_end with the first 2000 UTF-16 units of
// the last assistant message, or null when there is none.
func Stop(stopHookActive bool, lastAssistantMessage *string) store.Event {
	msg := "null"
	if lastAssistantMessage != nil {
		msg = string(hookio.QuoteString(hookio.JSPrefix(*lastAssistantMessage, 2000)))
	}
	return ev("turn_end", "session", 1, fmt.Sprintf(`{"stop_hook_active":%t,"last_assistant_message":%s}`, stopHookActive, msg))
}

// SessionStartRules ports sessionstart.mjs:341-360: rule and rule_content
// for each readable CLAUDE.md that is not blank, in TS order. TS read the
// first file from resolveConfigDir(); the contract passes home, so it is
// always ~/.claude. An empty home skips that file instead of reading a
// path relative to the cwd.
func SessionStartRules(home, projectDir string) []store.Event {
	paths := []string{filepath.Join(projectDir, "CLAUDE.md"), filepath.Join(projectDir, ".claude", "CLAUDE.md")}
	if home != "" {
		paths = append([]string{filepath.Join(home, ".claude", "CLAUDE.md")}, paths...)
	}
	var evs []store.Event
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil || jsTrim(string(b)) == "" {
			continue
		}
		evs = append(evs, ev("rule", "rule", 1, p), ev("rule_content", "rule", 1, string(b)))
	}
	return evs
}

// SessionStartEvent ports emitSessionStartLifecycle (sessionstart.mjs:61-73).
func SessionStartEvent(source, projectDir string, now time.Time) store.Event {
	data := fmt.Sprintf(`{"source":%s,"project_dir":%s,"started_at":%d}`,
		hookio.QuoteString(source), hookio.QuoteString(projectDir), now.Unix())
	return ev("session_start", "session_start", 1, data)
}
