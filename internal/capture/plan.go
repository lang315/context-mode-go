package capture

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

var planPath = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`(?:^|[/\\])\.claude[/\\]plans[/\\]`) })

// planEvents ports extractPlan (X:714-803) without apply_patch.
func planEvents(c call) []store.Event {
	switch c.tool {
	case "EnterPlanMode":
		return []store.Event{ev("plan_enter", "plan", 2, "entered plan mode")}
	case "ExitPlanMode":
		evs := []store.Event{ev("plan_exit", "plan", 2, exitDetail(c))}
		lower := asciiLower(c.resp) // the words are ASCII; no other letter lowercases into them
		switch {
		case strings.Contains(lower, "approve"): // also covers "approved"
			evs = append(evs, ev("plan_approved", "plan", 1, "plan approved by user"))
		case strings.Contains(lower, "rejected") || strings.Contains(lower, "decline") || strings.Contains(lower, "denied"):
			evs = append(evs, ev("plan_rejected", "plan", 2, "plan rejected: "+c.resp))
		}
		return evs
	case "Write", "Edit":
		path := inputString(c.input, "file_path", "")
		if planPath().MatchString(path) {
			return []store.Event{ev("plan_file_write", "plan", 2, "plan file: "+path[strings.LastIndexAny(path, `/\`)+1:])}
		}
	}
	return nil
}

// exitDetail is the plan_exit text: allowed prompts, then the plan's UTF-16
// length and hash (X:727-744).
func exitDetail(c call) string {
	detail := "exited plan mode"
	if raw, ok := c.input.Get("allowedPrompts"); ok {
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) == nil && len(items) > 0 {
			parts := make([]string, len(items))
			for i, it := range items {
				parts[i] = jsString(it)
				if o, err := hookio.ParseObject(it); err == nil && o.Has("prompt") {
					p, _ := o.Get("prompt")
					parts[i] = jsString(p)
				}
			}
			detail += " (allowed: " + strings.Join(parts, ", ") + ")"
		}
	}
	plan, _ := c.input.String("plan")
	if plan == "" { // extractExitPlanText: else the response's plan field
		if top, err := hookio.ParseObject([]byte(c.resp)); err == nil {
			plan, _ = top.String("plan")
		}
	}
	if plan != "" {
		detail += fmt.Sprintf(" plan_bytes:%d plan_hash:%s", hookio.JSLen(plan), fnv1a32(plan))
	}
	return detail
}

// fnv1a32 ports fnv1a32Hex (X:685-692): FNV-1a over UTF-16 code units.
func fnv1a32(s string) string {
	h := uint32(0x811c9dc5)
	for _, u := range utf16.Encode([]rune(s)) {
		h ^= uint32(u)
		h *= 0x01000193
	}
	return fmt.Sprintf("%08x", h)
}
