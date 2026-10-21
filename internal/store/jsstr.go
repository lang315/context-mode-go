package store

import (
	"unicode/utf16"
)

// JavaScript string semantics the TS code relies on. JS `\s` and trim() use
// this whitespace set (Go's regexp `\s` is ASCII only), and `.length` /
// `.slice()` count UTF-16 code units, not bytes.

// jsWS is the JS `\s` class, for building regexps.
const jsWS = `[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

// jsDot is the JS `.` class without the s flag (excludes line terminators).
const jsDot = `[^\n\r\x{2028}\x{2029}]`

// u16len is String.prototype.length.
func u16len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// u16prefix is s.slice(0, n). A surrogate pair cut in half becomes U+FFFD,
// as V8 does when the lone surrogate is later written out as UTF-8.
func u16prefix(s string, n int) string {
	u := utf16.Encode([]rune(s))
	if n >= len(u) {
		return s
	}
	return string(utf16.Decode(u[:n]))
}
