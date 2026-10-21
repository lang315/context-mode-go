package routing

import (
	"math"
	"testing"
)

func TestIsJSSpace(t *testing.T) {
	// Expected values from node: /\s/.test(c) for each code point.
	for _, r := range []rune{'\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2000, 0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff} {
		if !isJSSpace(r) {
			t.Errorf("isJSSpace(%U) = false, want true", r)
		}
	}
	for _, r := range []rune{'a', '-', 0x85, 0x180e, 0x200b, 0} {
		if isJSSpace(r) {
			t.Errorf("isJSSpace(%U) = true, want false", r)
		}
	}
}

func TestJSTrim(t *testing.T) {
	cases := map[string]string{
		"  pwd  ":            "pwd",
		"\u00a0pwd\ufeff":    "pwd",
		"\u2028git status\v": "git status",
		"\u0085pwd":          "\u0085pwd", // NEL is not JS whitespace
		"":                   "",
		" \t ":               "",
	}
	for in, want := range cases {
		if got := jsTrim(in); got != want {
			t.Errorf("jsTrim(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJSRegexpSpace(t *testing.T) {
	re := jsRegexp(`^a\s+b\S$`)
	for _, s := range []string{"a bc", "a\u00a0b-", "a\u3000\tbx"} {
		if !re.MatchString(s) {
			t.Errorf("%q should match", s)
		}
	}
	for _, s := range []string{"a\u0085bc", "a b ", "abc"} {
		if re.MatchString(s) {
			t.Errorf("%q should not match", s)
		}
	}
}

func TestJSParseInt(t *testing.T) {
	// Expected values from node: parseInt(s, 10).
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"42", 42, true},
		{"  7", 7, true},
		{"\u00a07", 7, true},
		{"\ufeff8", 8, true},
		{"10abc", 10, true},
		{"-5", -5, true},
		{"+3", 3, true},
		{"0x10", 0, true},
		{"3.9", 3, true},
		{"abc", 0, false},
		{"", 0, false},
		{"  ", 0, false},
		{"-", 0, false},
		{"\u0085 9", 0, false},
		{"99999999999999999999", math.MaxInt64, true},
	}
	for _, c := range cases {
		got, ok := jsParseInt(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("jsParseInt(%q) = %d, %v; want %d, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
