// Package routing ports the TS PreToolUse decision tree (hooks/core/routing.mjs),
// its Claude Code formatter (hooks/core/formatters.mjs) and the routing block
// text (hooks/routing-block.mjs), with cmgo tool names.
package routing

import (
	"math"
	"regexp"
	"strings"
)

// jsSpaceClass is the body of a character class equal to JS \s (WhiteSpace
// plus LineTerminator). Go's \s is only [\t\n\f\r ].
const jsSpaceClass = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// isJSSpace reports whether r is in JS \s, the set String.prototype.trim strips.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsTrim is JS s.trim().
func jsTrim(s string) string {
	return strings.TrimFunc(s, isJSSpace)
}

// jsRegexp compiles a TS regex source after rewriting \s and \S to the JS
// classes. The sources it is used on have no \s inside brackets and no
// escaped backslash before an s.
func jsRegexp(src string) *regexp.Regexp {
	src = strings.ReplaceAll(src, `\s`, "["+jsSpaceClass+"]")
	src = strings.ReplaceAll(src, `\S`, "[^"+jsSpaceClass+"]")
	return regexp.MustCompile(src)
}

// jsParseInt is JS parseInt(s, 10): leading JS whitespace, an optional sign,
// then the longest digit run; ok is false where JS gives NaN. Values past
// int64 saturate (JS would keep a float, which every caller rejects anyway).
func jsParseInt(s string) (n int64, ok bool) {
	s = strings.TrimLeftFunc(s, isJSSpace)
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	for i := 0; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		ok = true
		d := int64(s[i] - '0')
		if n > (math.MaxInt64-d)/10 {
			n = math.MaxInt64
		} else if n != math.MaxInt64 {
			n = n*10 + d
		}
	}
	if neg {
		n = -n
	}
	return n, ok
}
