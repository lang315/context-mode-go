package capture

import (
	"encoding/json"
	"strings"

	"github.com/lang315/context-mode-go/internal/hookio"
)

// JavaScript semantics the TS extractors rely on. JS `\s` and trim() cover
// Unicode spaces; Go's regexp `\s` is ASCII only. `\b`, `\w` and `\d` are
// ASCII in both, so they are used as is.
const wsBody = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

const (
	jsWS    = "[" + wsBody + "]"  // JS \s
	jsNotWS = "[^" + wsBody + "]" // JS \S
)

// asciiLower lowercases A-Z only. A JS regexp with `i` and no `u` folds
// ASCII letters only, where Go's (?i) would also fold U+017F and U+212A.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

// jsString is JS String(v) for a parsed JSON value. Callers handle
// `?? default` themselves, because String(null) is "null". raw is never
// empty: it is a value from a parsed document.
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
	return string(raw) // null, true, false, numbers
}

// inputString is String(o[key] ?? def).
func inputString(o *hookio.Object, key, def string) string {
	raw, ok := o.Get(key)
	if !ok || string(raw) == "null" {
		return def
	}
	return jsString(raw)
}

// responseText is the text TS extracted from: posttooluse.mjs:51-58 passed a
// string response as is and JSON.stringify(r ?? "") otherwise, so a missing
// or null response is the two characters `""`.
func responseText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return `""`
	}
	if raw[0] == '"' {
		return jsString(raw)
	}
	return compactJS(raw)
}

// compactJS is JSON.stringify(JSON.parse(raw)) for the key order and
// escaping: hookio.Object compacts values and writes U+2028, U+2029 raw.
// shortcut: other escapes (A) and number spellings stay as received; Claude Code writes them as JSON.stringify does.
func compactJS(raw json.RawMessage) string {
	o := &hookio.Object{}
	o.Set("v", raw)
	b, err := o.MarshalJSON()
	if err != nil {
		return string(raw)
	}
	return string(b[len(`{"v":`) : len(b)-1])
}
