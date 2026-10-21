package capture

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/lang315/context-mode-go/internal/hookio"
)

// Expected values come from node: String(JSON.parse(raw)) and
// JSON.stringify(JSON.parse(raw)).
func TestJSString(t *testing.T) {
	cases := []struct{ raw, want string }{
		{`"a b"`, "a b"},
		{`42`, "42"},
		{`true`, "true"},
		{`null`, "null"},
		{`{"prompt":"x"}`, "[object Object]"},
		{`["a",null,[1,2],{}]`, "a,,1,2,[object Object]"},
		{`[]`, ""},
	}
	for _, c := range cases {
		if got := jsString(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("jsString(%s) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestResponseText(t *testing.T) {
	cases := []struct{ raw, want string }{
		{``, `""`}, // missing: JSON.stringify(undefined ?? "")
		{`null`, `""`},
		{`"plain\ntext"`, "plain\ntext"},
		{`{ "stdout" : "<a>&b", "n" : [1, 2] }`, `{"stdout":"<a>&b","n":[1,2]}`},
		{`["\u2028"]`, "[\"\u2028\"]"}, // JSON.stringify writes U+2028 raw
		{`false`, `false`},
	}
	for _, c := range cases {
		if got := responseText(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("responseText(%s) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestJSTrimAndClasses(t *testing.T) {
	if got := hookio.JSTrim("\u00a0\ufeff x \u3000\n"); got != "x" {
		t.Errorf("jsTrim = %q, want %q", got, "x")
	}
	ws := regexp.MustCompile(`^a` + jsWS + `+` + jsNotWS + `+$`)
	if !ws.MatchString("a\u00a0\u2003bc") || ws.MatchString("a b\u3000c") {
		t.Error("jsWS / jsNotWS do not follow JS \\s / \\S")
	}
	if got := asciiLower("ÄBC.MD \u017f \u212a"); got != "Äbc.md \u017f \u212a" {
		t.Errorf("asciiLower = %q", got)
	}
}
