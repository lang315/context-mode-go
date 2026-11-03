package memory

import (
	"fmt"
	"strings"
	"testing"
)

func TestDirectiveNoEvents(t *testing.T) {
	if got := Directive("compact", nil, 10); got != "" {
		t.Fatalf("Directive(nil) = %q", got)
	}
}

func TestDirectiveSourceAttribute(t *testing.T) {
	rows := evs([3]string{"user-prompt", "user_prompt", "go on"})
	compact, resume := Directive("compact", rows, 10), Directive("resume", rows, 10)
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

// Every list section keeps its last n items; git and env lines are cut to
// 150 units like the other sections' lines.
func TestDirectiveCapsListSections(t *testing.T) {
	long := strings.Repeat("x", 400)
	var rows [][3]string
	for i := range 4 {
		rows = append(rows,
			[3]string{"error", "error_tool", fmt.Sprintf("err%d", i)},
			[3]string{"decision", "decision", fmt.Sprintf("dec%d", i)},
			[3]string{"skill", "skill", fmt.Sprintf("skill%d", i)},
			[3]string{"git", "git_commit", fmt.Sprintf("commit%d %s", i, long)},
			[3]string{"env", "env", fmt.Sprintf("export A%d=*** %s", i, long)})
	}
	d := Directive("compact", evs(rows...), 2)
	for _, gone := range []string{"err1", "dec1", "skill1", "commit1", "A1="} {
		if strings.Contains(d, gone) {
			t.Errorf("%q kept, want only the last 2 items", gone)
		}
	}
	for _, kept := range []string{"err2", "err3", "dec3", "skill3", cut("commit3 "+long, 150), cut("export A3=*** "+long, 150)} {
		if !strings.Contains(d, kept) {
			t.Errorf("%q missing", kept)
		}
	}
	if strings.Contains(d, long) {
		t.Error("a git or env line is not cut")
	}
}
