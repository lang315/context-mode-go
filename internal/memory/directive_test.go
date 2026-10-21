package memory

import (
	"strings"
	"testing"
)

func TestDirectiveNoEvents(t *testing.T) {
	if got := Directive("compact", nil); got != "" {
		t.Fatalf("Directive(nil) = %q", got)
	}
}

func TestDirectiveSourceAttribute(t *testing.T) {
	rows := evs([3]string{"user-prompt", "user_prompt", "go on"})
	compact, resume := Directive("compact", rows), Directive("resume", rows)
	if !strings.HasPrefix(compact, "\n<session_knowledge source=\"compact\">") || !strings.Contains(compact, "<continue_from>") {
		t.Fatalf("compact:\n%s", compact)
	}
	if !strings.HasPrefix(resume, "\n<session_knowledge source=\"continue\">") || strings.Contains(resume, "<continue_from>") {
		t.Fatalf("resume:\n%s", resume)
	}
}

func TestCutCountsUTF16Units(t *testing.T) {
	e := "\U0001F600" // 2 units
	cases := []struct{ in, want string }{
		{strings.Repeat("d", 150), strings.Repeat("d", 150)},
		{strings.Repeat("d", 151), strings.Repeat("d", 147) + "..."},
		{"a" + strings.Repeat(e, 75), "a" + strings.Repeat(e, 73) + "..."}, // 151 units, 76 runes
		{strings.Repeat(e, 75), strings.Repeat(e, 75)},                     // 150 units
	}
	for _, c := range cases {
		if got := cut(c.in, 150); got != c.want {
			t.Errorf("cut(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
