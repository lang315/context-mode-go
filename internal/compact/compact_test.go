package compact

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// numbered is n lines of test-runner output; extra replaces line i.
func numbered(n int, extra map[int]string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if s, ok := extra[i]; ok {
			b.WriteString(s + "\n")
			continue
		}
		fmt.Fprintf(&b, "line %04d ok github.com/x/pkg%d 0.01s\n", i, i)
	}
	return b.String()
}

func TestTextHeadTailAndFooter(t *testing.T) {
	out := numbered(500, nil)
	got := Text(out, "bash:t1")
	head, _, ok := strings.Cut(got, "\n\n[cmgo:")
	if !ok || !strings.HasPrefix(got, "line 0000 ") || len(head) > 1024 {
		t.Fatalf("head (%d bytes):\n%s", len(head), got)
	}
	for _, want := range []string{
		fmt.Sprintf("[cmgo: %.1f KB, 500 lines; the middle is kept out of context.]\n...\n", float64(len(out))/1024),
		"line 0499 ok",
		`[cmgo: the full output is indexed as "bash:t1". Search it: ctx_search(queries: ["..."], source: "bash:t1"). Need it verbatim? Re-run the command with "# cmgo:full" at the end.]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "line 0250 ") {
		t.Errorf("middle line shown:\n%s", got)
	}
	if !strings.HasSuffix(got, "at the end.]") {
		t.Errorf("does not end with the footer:\n%s", got)
	}
}

func TestTextMatches(t *testing.T) {
	out := numbered(500, map[int]string{3: "error in head", 250: "--- FAIL: TestX (0.00s)", 260: "panic: boom"})
	got := Text(out, "bash:t1")
	if !strings.Contains(got, " 2 lines match error|fail|panic|warn:]\n--- FAIL: TestX (0.00s)\npanic: boom\n...\n") {
		t.Errorf("matches:\n%s", got)
	}
	if n := strings.Count(got, "error in head"); n != 1 {
		t.Errorf("head match shown %d times, want 1 (head only)", n)
	}
	one := Text(numbered(500, map[int]string{250: "WARNING: x"}), "bash:t1")
	if !strings.Contains(one, " 1 line matches error|fail|panic|warn:]\nWARNING: x\n") {
		t.Errorf("one match:\n%s", one)
	}
}

func TestTextManyMatchesAndLongLine(t *testing.T) {
	extra := map[int]string{}
	for i := 200; i <= 220; i++ {
		extra[i] = fmt.Sprintf("warn %d", i)
	}
	got := Text(numbered(500, extra), "bash:t1")
	if !strings.Contains(got, " 21 lines match error|fail|panic|warn (first 20 shown):]\n") ||
		!strings.Contains(got, "warn 219\n") || strings.Contains(got, "warn 220") {
		t.Errorf("21 matches:\n%s", got)
	}
	long := Text(numbered(500, map[int]string{250: "error " + strings.Repeat("x", 500)}), "bash:t1")
	if !strings.Contains(long, "error "+strings.Repeat("x", 194)+"\n") || strings.Contains(long, strings.Repeat("x", 195)) {
		t.Errorf("long match not cut to 200 bytes:\n%s", long)
	}
}

func TestTextNoNewlineStaysUTF8(t *testing.T) {
	got := Text(strings.Repeat("é", 10000), "bash:t1") // 20,000 bytes, no newline
	if !utf8.ValidString(got) || len(got) > 4096 {
		t.Fatalf("valid %v, %d bytes", utf8.ValidString(got), len(got))
	}
}

func TestMinBytes(t *testing.T) {
	for v, want := range map[string]int{"": 5120, "0": 0, "junk": 5120, "-1": 5120, "1": 1, "30000": 30000, "30001": 5120, " 8192 ": 8192} {
		if got := MinBytes(func(string) string { return v }); got != want {
			t.Errorf("MinBytes(%q) = %d, want %d", v, got, want)
		}
	}
}

func TestOutput(t *testing.T) {
	for _, c := range [][3]string{{"a", "", "a"}, {"", "b", "b"}, {"a", "b", "a\nb"}, {"", "", ""}} {
		if got := Output(c[0], c[1]); got != c[2] {
			t.Errorf("Output(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
