package routing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readFixture makes a project dir with known file sizes and a separate home
// holding h.txt. It returns both.
func readFixture(t *testing.T) (proj, home string) {
	t.Helper()
	proj, home = t.TempDir(), t.TempDir()
	write := func(path string, n int) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Repeat("x", n)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(proj, "a.txt"), 20000)
	write(filepath.Join(proj, "b.txt"), 3000)
	write(filepath.Join(proj, "my file.txt"), 20000)
	write(filepath.Join(proj, "sub", "c.txt"), 30000)
	write(filepath.Join(home, "h.txt"), 18000)
	if err := os.Symlink(filepath.Join(proj, "a.txt"), filepath.Join(proj, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(proj, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	return proj, home
}

func TestEstimateRead(t *testing.T) {
	proj, home := readFixture(t)
	cases := []struct {
		cmd  string
		want int64
	}{
		{`cat a.txt`, 20000},
		{`cat a.txt b.txt`, 23000},
		{`cat -n a.txt`, 20000},
		{`nl -ba a.txt`, 20000},
		{`cat "my file.txt"`, 20000},
		{`cat my\ file.txt`, 20000},
		{`cat link`, 20000},
		{`cat dir`, 0},
		{`cat missing.txt`, 0},
		{`cat`, 0},
		{`cd sub && cat c.txt`, 30000},
		{`cd sub; cat c.txt`, 30000},
		{`cd .. && cat a.txt`, 0},
		{`cd "$X" && cat a.txt`, 0},
		{`cd - && cat a.txt`, 0},
		{`cat ~/h.txt`, 18000},
		{`cat $F`, 0},
		{`cat *.txt`, 0},
		{`cat a.txt | head -5`, 320},
		{`cat a.txt | head -n 1000`, 20000},
		{`cat a.txt | wc -l`, 0},
		{`cat a.txt | grep -c x`, 0},
		{`cat a.txt | grep x`, 20000},
		{`cat a.txt | grep -m 5 x`, 320},
		{`cat a.txt | grep -ci x`, 0},
		{`cat 'a.txt'`, 20000},
		{`cat "a.txt`, 0},
		{`cat < a.txt`, 0},
		{`(cat a.txt)`, 0},
		{`cat a.txt &`, 20000},
		{`cat a.txt || cat b.txt`, 23000},
		{`cat a.txt > out.txt`, 0},
		{`cat a.txt >/dev/null`, 0},
		{`cat a.txt 2>/dev/null`, 20000},
		{`cat a.txt 2>&1`, 20000},
		{"cat > a.txt <<EOF\nx\nEOF", 0},
		{`cat $(echo a.txt)`, 0},
		{"cat `echo a.txt`", 0},
		{`echo hi; cat a.txt`, 20000},
		{`cat a.txt; cat a.txt`, 40000},
		{`head -n 100 a.txt`, 6400},
		{`head -100 a.txt`, 6400},
		{`head -c 500 a.txt`, 500},
		{`head a.txt`, 640},
		{`tail -n 1000 a.txt`, 20000},
		{`tail -n +5 a.txt`, 20000},
		{`tail -f a.txt`, 0},
		{`sed -n '1,500p' a.txt`, 0},
		{`git diff`, 0},
		{`cat a.txt b.txt a.txt b.txt a.txt b.txt a.txt b.txt a.txt`, 92000},
		{`cat a.txt` + strings.Repeat(" ", 5000), 0},
	}
	for _, c := range cases {
		if got, _ := estimateRead(c.cmd, proj, home); got != c.want {
			t.Errorf("estimateRead(%q) = %d, want %d", c.cmd, got, c.want)
		}
	}
}

func TestEstimateReadLargest(t *testing.T) {
	proj, home := readFixture(t)
	if _, name := estimateRead("cat a.txt sub/c.txt", proj, home); name != "sub/c.txt" {
		t.Fatalf("largest = %q, want sub/c.txt", name)
	}
	if _, name := estimateRead("echo hi", proj, home); name != "" {
		t.Fatalf("largest = %q, want empty", name)
	}
}

// readDeps is testDeps with a project dir holding big.txt (20000 bytes) and
// small.txt (3000 bytes), Cwd set to it, and an env map.
func readDeps(t *testing.T, env map[string]string) (Deps, string) {
	t.Helper()
	proj, home := readFixture(t)
	d := testDeps(t)
	d.Cwd, d.Home = proj, home
	d.Getenv = func(k string) string { return env[k] }
	return d, proj
}

func TestRouteBashReadGuidance(t *testing.T) {
	d, _ := readDeps(t, nil)
	dec := Route("Bash", bash("cat a.txt"), false, d)
	if dec.Action != "context" || dec.Redirect != nil {
		t.Fatalf("first: %+v", dec)
	}
	for _, want := range []string{"about 20 KB (a.txt)", "Reading to Edit the file? Use Read", "Edit needs a prior Read", "mcp__cmgo__ctx_execute_file", "mcp__cmgo__ctx_batch_execute", "head or tail"} {
		if !strings.Contains(dec.AdditionalContext, want) {
			t.Errorf("long text lacks %q:\n%s", want, dec.AdditionalContext)
		}
	}
	if strings.Contains(dec.AdditionalContext, "sed") {
		t.Errorf("long text mentions sed:\n%s", dec.AdditionalContext)
	}
	if evs := Events("Bash", dec); len(evs) != 0 {
		t.Errorf("events for a hint: %+v", evs)
	}

	dec = Route("Bash", bash("cat a.txt"), false, d)
	if dec.Action != "context" || !strings.Contains(dec.AdditionalContext, "About 20 KB of output (a.txt)") || strings.Contains(dec.AdditionalContext, "Reading to Edit") {
		t.Errorf("second: %+v", dec)
	}
}

func TestRouteBashReadGuidanceThreshold(t *testing.T) {
	cases := []struct {
		env  string
		cmd  string
		hint bool
	}{
		{"", "cat b.txt", false}, // 3000 < 16384
		{"", "cat a.txt", true},
		{"", "cat a.txt b.txt", true},
		{"0", "cat a.txt", false}, // off
		{"junk", "cat a.txt", true},
		{"-5", "cat a.txt", true},
		{"200000000", "cat a.txt", true}, // out of range: default
		{"30000", "cat a.txt", false},
		{"1000", "cat b.txt", true},
	}
	for _, c := range cases {
		d, _ := readDeps(t, map[string]string{"CMGO_BASH_READ_MIN_BYTES": c.env})
		dec := Route("Bash", bash(c.cmd), false, d)
		got := strings.Contains(dec.AdditionalContext, " KB (") || strings.Contains(dec.AdditionalContext, "KB of output")
		if got != c.hint {
			t.Errorf("env %q, %q: hint %v, want %v (%+v)", c.env, c.cmd, got, c.hint, dec)
		}
	}
}

func TestRouteBashReadGuidanceFallThrough(t *testing.T) {
	// MCP down: only today's generic guidance.
	d, _ := readDeps(t, nil)
	d.MCPReady = func() bool { return false }
	if dec := Route("Bash", bash("cat a.txt"), false, d); dec.AdditionalContext != bashGuidance {
		t.Errorf("MCP down: %q", dec.AdditionalContext)
	}
	// Commands the guard ignores get exactly the decision they got before.
	for _, cmd := range []string{"echo hi", "git status", "cat b.txt", "sed -n '1,500p' a.txt", "git diff", "npm test", "cat > a.txt <<EOF\nx\nEOF"} {
		d1, _ := readDeps(t, nil)
		d2, _ := readDeps(t, map[string]string{"CMGO_BASH_READ_MIN_BYTES": "0"})
		got := Route("Bash", bash(cmd), false, d1)
		want := Route("Bash", bash(cmd), false, d2)
		if got.Action != want.Action || got.AdditionalContext != want.AdditionalContext || got.Reason != want.Reason {
			t.Errorf("%q: %+v, want %+v", cmd, got, want)
		}
	}
	// Policy and the redirects run first.
	d, _ = readDeps(t, nil)
	if dec := Route("Bash", bash("cat a.txt && sudo ls"), false, d); dec.Action != "deny" {
		t.Errorf("deny rule: %+v", dec)
	}
	if dec := Route("Bash", bash("curl https://example.com | cat a.txt"), false, d); dec.Action != "modify" {
		t.Errorf("curl redirect: %+v", dec)
	}
	// The guard runs before the length nudge.
	d, _ = readDeps(t, map[string]string{"CMGO_BASH_NUDGE_MIN_COMMAND_BYTES": "100"})
	if dec := Route("Bash", bash("cat a.txt"), false, d); !strings.Contains(dec.AdditionalContext, "about 20 KB") {
		t.Errorf("nudge threshold hid the hint: %+v", dec)
	}
}
