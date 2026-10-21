// Package hookio holds the stdin, JSON and string helpers every cmgo hook
// shares. Output strings match JS JSON.stringify so hook output is byte-equal
// to the TS hooks.
package hookio

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// Marshal returns compact JSON with no trailing newline. Strings are escaped
// as JSON.stringify does: no HTML escaping, and U+2028, U+2029 and U+FFFD
// stay raw (Go's encoder always escapes the first two and writes invalid
// UTF-8 as an escaped U+FFFD).
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return unescapeJS(bytes.TrimSuffix(buf.Bytes(), []byte("\n"))), nil
}

// QuoteString returns s as a JSON string literal, equal to JSON.stringify(s).
func QuoteString(s string) json.RawMessage {
	b, _ := Marshal(s) // a string always encodes
	return b
}

// rawEscapes maps the escape bodies (after the backslash) that JS writes raw.
var rawEscapes = map[string]string{
	"u2028": "\xe2\x80\xa8",
	"u2029": "\xe2\x80\xa9",
	"ufffd": "\xef\xbf\xbd",
}

// unescapeJS turns the escapes in rawEscapes back into raw characters. It
// walks escape pairs, so an escaped backslash followed by "u2028" text is
// left alone. Valid JSON has backslashes only inside strings, so this never
// touches structure.
func unescapeJS(b []byte) []byte {
	if !bytes.Contains(b, []byte{'\\'}) {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' || i+1 >= len(b) {
			out = append(out, b[i])
			continue
		}
		if i+6 <= len(b) {
			if raw, ok := rawEscapes[string(b[i+1:i+6])]; ok {
				out = append(out, raw...)
				i += 5
				continue
			}
		}
		out = append(out, b[i], b[i+1])
		i++
	}
	return out
}

// JSLen returns the length of s in UTF-16 code units, like JS s.length.
func JSLen(s string) int {
	n := 0
	for _, r := range s {
		n += units(r)
	}
	return n
}

// JSPrefix returns the longest prefix of s that is at most n UTF-16 code
// units, like JS s.slice(0, n), except that it stops before a surrogate pair
// instead of splitting it.
func JSPrefix(s string, n int) string {
	used := 0
	for i, r := range s {
		if used+units(r) > n {
			return s[:i]
		}
		used += units(r)
	}
	return s
}

// RuneLen returns the number of code points in s, like JS [...s].length.
func RuneLen(s string) int {
	return utf8.RuneCountInString(s)
}

// units is the UTF-16 length of r. Invalid bytes decode to U+FFFD, one unit,
// as they would after JS decoded them.
func units(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

// IsJSSpace reports whether r is in JS \s, the set String.prototype.trim strips.
func IsJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// JSTrim is String.prototype.trim.
func JSTrim(s string) string { return strings.TrimFunc(s, IsJSSpace) }
