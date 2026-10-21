// Package memory builds the texts SessionStart injects after a compaction
// or a resume: the session directive and the auto-injection block. They are
// pure functions over one session's events, ported from
// hooks/session-directive.mjs and hooks/auto-injection.mjs.
package memory

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/lang315/context-mode-go/internal/store"
)

// groups is one pass over a session's events (SD:54-73) for the directive.
type groups struct {
	cat        map[string][]store.StoredEvent // by category, in id order
	lastPrompt string                         // data of the last user-prompt event
	files      []string                       // unique basenames, first seen first (a JS Set)
	leg        int64                          // id of the last session_start event, 0 when none
}

// group sorts events by category. TS read category "prompt", which capture
// never writes; cmgo reads "user-prompt" (spec §2.4).
func group(events []store.StoredEvent) groups {
	g := groups{cat: map[string][]store.StoredEvent{}}
	for _, ev := range events {
		if ev.Category == "session_start" {
			g.leg = ev.ID
		}
		if ev.Category == "user-prompt" {
			g.lastPrompt = ev.Data
			continue
		}
		g.cat[ev.Category] = append(g.cat[ev.Category], ev)
	}
	seen := map[string]bool{}
	for _, ev := range g.cat["file"] {
		path := ev.Data
		if i := strings.LastIndex(path, " in "); i >= 0 {
			path = path[i+len(" in "):]
		}
		base := jsTrim(path[strings.LastIndexAny(path, `/\`)+1:])
		if base != "" && !strings.Contains(base, "*") && !seen[base] {
			seen[base] = true
			g.files = append(g.files, base)
		}
	}
	return g
}

// subagents splits the subagent events at the leg boundary (#780): prior
// ones were written before the last session_start, in an earlier leg. TS
// compared created_at strings of 1 s resolution; ids are exact.
func (g groups) subagents() (current, prior []store.StoredEvent) {
	for _, ev := range g.cat["subagent"] {
		if g.leg > 0 && ev.ID < g.leg {
			prior = append(prior, ev)
		} else {
			current = append(current, ev)
		}
	}
	return current, prior
}

// tasks splits task subjects into pending and done (SD:300-334).
// A create has a subject, an update a taskId and a status; creates match
// updates in ascending numeric taskId order.
func (g groups) tasks() (pending, done []string) {
	var creates, ids []string
	updates := map[string]json.RawMessage{}
	for _, ev := range g.cat["task"] {
		var f map[string]json.RawMessage
		_ = json.Unmarshal([]byte(ev.Data), &f) // bad JSON, null and non-objects leave f nil, so the row is skipped (TS catch)
		if truthy(f["subject"]) {
			creates = append(creates, jsString(f["subject"]))
		} else if truthy(f["taskId"]) && truthy(f["status"]) {
			id := jsString(f["taskId"])
			if _, dup := updates[id]; !dup {
				ids = append(ids, id)
			}
			updates[id] = f["status"]
		}
	}
	// shortcut: Object.keys lists integer keys first; numeric taskIds (all Claude Code writes) sort the same. Upgrade if ids like "01" or "abc" appear.
	sort.SliceStable(ids, func(i, j int) bool { return jsNumber(ids[i]) < jsNumber(ids[j]) })
	for i, subject := range creates {
		var status string
		if i < len(ids) {
			_ = json.Unmarshal(updates[ids[i]], &status) // a non-string status is never done
		}
		switch status {
		case "completed", "deleted", "failed":
			done = append(done, subject)
		default:
			pending = append(pending, subject)
		}
	}
	return pending, done
}

// truthy is JS truthiness for a parsed JSON value; a missing key is nil.
func truthy(raw json.RawMessage) bool {
	switch s := string(raw); s {
	case "", "null", "false", `""`:
		return false
	default:
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			return n != 0
		}
		return true
	}
}

// jsNumber is JS Number(s) for a taskId key, NaN when it is not a number.
func jsNumber(s string) float64 {
	s = jsTrim(s)
	if s == "" {
		return 0
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return n
}

// jsString is JS String(v) for a parsed JSON value.
// shortcut: numbers keep their JSON text (JS would print 1e2 as 100); upgrade if a tool sends such numbers.
func jsString(raw json.RawMessage) string {
	switch raw[0] {
	case '"':
		var s string
		_ = json.Unmarshal(raw, &s) // raw came from a parsed document
		return s
	case '{':
		return "[object Object]"
	case '[':
		var items []json.RawMessage
		_ = json.Unmarshal(raw, &items)
		parts := make([]string, len(items))
		for i, it := range items {
			if string(it) != "null" { // Array.join writes null as ""
				parts[i] = jsString(it)
			}
		}
		return strings.Join(parts, ",")
	}
	return string(raw) // true, numbers
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }
