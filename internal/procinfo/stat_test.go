package procinfo

import "testing"

func TestParseStat(t *testing.T) {
	cases := []struct {
		in   string
		ppid int
		name string
		ok   bool
	}{
		{"4242 (claude) S 4100 4242 4100 34816 4242 4194304 0 0\n", 4100, "claude", true},
		{"77 (my prog) R 1 77 77 0 -1 0\n", 1, "my prog", true},
		// comm may hold ") " itself; the last ')' ends it.
		{"77 (a) S 9 (b)) S 55 77 77 0\n", 55, "a) S 9 (b)", true},
		{"1 (init) S 0 1 1 0 -1\n", 0, "init", true},
		{"", 0, "", false},
		{"77 (noclose S 1 2", 0, "", false},
		{"77 (x)", 0, "", false},
		{"77 (x) S", 0, "", false},
		{"77 (x) S abc 1", 0, "", false},
	}
	for _, c := range cases {
		ppid, name, err := parseStat(c.in)
		if (err == nil) != c.ok || ppid != c.ppid || name != c.name {
			t.Errorf("parseStat(%q) = %d, %q, %v; want %d, %q, ok=%v", c.in, ppid, name, err, c.ppid, c.name, c.ok)
		}
	}
}
