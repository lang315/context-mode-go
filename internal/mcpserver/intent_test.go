package mcpserver

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestBuildExecuteEcho(t *testing.T) {
	if got := buildExecuteEcho("shell", "echo hi", ""); got != "```shell\necho hi\n```\n\n" {
		t.Errorf("plain echo = %q", got)
	}
	if got := buildExecuteEcho("python", "print(1)", "data.txt"); got != "path=data.txt\n```python\nprint(1)\n```\n\n" {
		t.Errorf("path echo = %q", got)
	}
	long := strings.Repeat("a", 2001)
	if got, want := buildExecuteEcho("shell", long, ""), "```shell\n"+strings.Repeat("a", 2000)+"\n… (truncated)\n```\n\n"; got != want {
		t.Errorf("truncated echo has len %d, want %d", len(got), len(want))
	}
	if got := buildExecuteEcho("shell", strings.Repeat("a", 2000), ""); strings.Contains(got, "truncated") {
		t.Error("2000-char code must not be truncated")
	}
}

func TestUTF16Prefix(t *testing.T) {
	// "😀" is 2 UTF-16 units: JS "a😀b".slice(0, 2) keeps only "a" here.
	if got, cut := utf16Prefix("a😀b", 2); got != "a" || !cut {
		t.Errorf("got %q %v", got, cut)
	}
	if got, cut := utf16Prefix("a😀b", 3); got != "a😀" || !cut {
		t.Errorf("got %q %v", got, cut)
	}
	if got, cut := utf16Prefix("a😀b", 4); got != "a😀b" || cut {
		t.Errorf("got %q %v", got, cut)
	}
}

// intentFixture is ~200 lines of log output with a few ERROR lines, sized so
// plain-text chunking yields enough chunks for distinctive terms.
func intentFixture() string {
	var b strings.Builder
	for i := 1; i <= 200; i++ {
		switch {
		case i%30 == 0:
			fmt.Fprintf(&b, "line %d ERROR disk quota exceeded on volume\n", i)
		case i%45 == 0:
			fmt.Fprintf(&b, "line %d retrying upload of artifact bundle\n", i)
		default:
			fmt.Fprintf(&b, "line %d step completed without problems\n", i)
		}
	}
	return b.String()
}

func TestIntentSearchMatch(t *testing.T) {
	h := newHarness(t, nil)
	out := intentFixture()
	if len(out) <= intentSearchThreshold {
		t.Fatalf("fixture is %d bytes, want > %d", len(out), intentSearchThreshold)
	}
	got, err := intentSearch(h.Deps, out, "disk quota", "execute:shell")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(got, "\n")
	if !regexp.MustCompile(`^Indexed \d+ sections from "execute:shell" into knowledge base\.$`).MatchString(lines[0]) {
		t.Errorf("line 0 = %q", lines[0])
	}
	want1 := regexp.MustCompile(`^[1-5] sections matched "disk quota" \(201 lines, ` + regexp.QuoteMeta(KB(len(out))) + `\):$`)
	if !want1.MatchString(lines[1]) {
		t.Errorf("line 1 = %q", lines[1])
	}
	if lines[2] != "" || !strings.HasPrefix(lines[3], "  - ") {
		t.Errorf("result list = %q", lines[2:4])
	}
	if !strings.Contains(got, "\n\nSearchable terms: ") {
		t.Errorf("missing Searchable terms:\n%s", got)
	}
	if !strings.HasSuffix(got, "\n\nUse ctx_search(queries: [...]) to retrieve full content of any section.") {
		t.Errorf("bad tail:\n%s", got)
	}
}

func TestIntentSearchNoMatch(t *testing.T) {
	h := newHarness(t, nil)
	out := intentFixture()
	got, err := intentSearch(h.Deps, out, "zzqx", "execute:shell")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(got, "\n")
	if want := fmt.Sprintf(`No sections matched intent "zzqx" in 201-line output (%s).`, KB(len(out))); lines[1] != want {
		t.Errorf("line 1 = %q, want %q", lines[1], want)
	}
	if !strings.HasSuffix(got, "\n\nUse ctx_search(queries: [...]) to explore the indexed content.") {
		t.Errorf("bad tail:\n%s", got)
	}
}

func TestIndexStdoutPointer(t *testing.T) {
	h := newHarness(t, nil)
	got, err := indexStdout(h.Deps, "# Title\n\nsome prose here\n", "execute:shell")
	if err != nil {
		t.Fatal(err)
	}
	want := "Indexed 1 sections (0 with code) from: execute:shell\nUse ctx_search(queries: [\"...\"]) to query this content. Use source: \"execute:shell\" to scope results."
	if got != want {
		t.Errorf("got %q", got)
	}
}
