package hookio

import "testing"

// U+2028, U+2029 and U+FFFD as UTF-8 bytes, spelled out so no tool rewrites them.
const (
	ls   = "\xe2\x80\xa8"
	ps   = "\xe2\x80\xa9"
	repl = "\xef\xbf\xbd"
)

// Expected values come from node: JSON.stringify(<input>).
func TestQuoteStringMatchesJSONStringify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"<a>&b", `"<a>&b"`},
		{ls + ps, `"` + ls + ps + `"`},
		{"\x00\x01\x1f\x7f", `"\` + `u0000\` + `u0001\` + `u001f` + "\x7f\""},
		{"\b\f\n\r\t\"\\", `"\b\f\n\r\t\"\\"`},
		{"é😀", `"é😀"`},
		{"a\xff", `"a` + repl + `"`},      // node decodes the stray byte to U+FFFD
		{`\` + `u2028`, `"\\` + `u2028"`}, // literal backslash-u text stays escaped
		{`\` + ls, `"\\` + ls + `"`},      // backslash then a real U+2028
		{`\` + `ufffd`, `"\\` + `ufffd"`},
		{"", `""`},
	}
	for _, c := range cases {
		if got := string(QuoteString(c.in)); got != c.want {
			t.Errorf("QuoteString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMarshal(t *testing.T) {
	v := struct {
		B string  `json:"b"`
		A []any   `json:"a"`
		N float64 `json:"n"`
		F float64 `json:"f"`
	}{"<x>", []any{1, ls}, 1e21, 0.1}
	got, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"b":"<x>","a":[1,"` + ls + `"],"n":1e+21,"f":0.1}`
	if string(got) != want {
		t.Errorf("Marshal = %q, want %q", got, want)
	}
	if _, err := Marshal(func() {}); err == nil {
		t.Error("Marshal(func) succeeded, want error")
	}
}

func TestJSLenPrefixRuneLen(t *testing.T) {
	if n := JSLen("a😀é"); n != 4 {
		t.Errorf("JSLen = %d, want 4", n)
	}
	if n := RuneLen("a😀é"); n != 3 {
		t.Errorf("RuneLen = %d, want 3", n)
	}
	prefixes := []struct {
		s    string
		n    int
		want string
	}{
		{"a😀é", 0, ""},
		{"a😀é", -1, ""},
		{"a😀é", 1, "a"},
		{"a😀é", 2, "a"}, // never splits the surrogate pair
		{"a😀é", 3, "a😀"},
		{"a😀é", 4, "a😀é"},
		{"a😀é", 99, "a😀é"},
	}
	for _, c := range prefixes {
		if got := JSPrefix(c.s, c.n); got != c.want {
			t.Errorf("JSPrefix(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
}
