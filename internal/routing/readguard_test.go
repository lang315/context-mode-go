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
