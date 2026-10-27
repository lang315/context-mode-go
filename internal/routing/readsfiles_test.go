package routing

import "testing"

func TestReadsFiles(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"sed -n 1,9p f.go", true},
		{"cd d && cat f", true},
		{"FOO=1 head -50 f", true},
		{"git -C d diff", true},
		{"git --no-pager show HEAD", true},
		{"cat f | grep x", true},
		{"ls && cat f", true},
		{"/usr/bin/tail -n 5 log", true},
		{"go test ./...", false},
		{"git status", false},
		{"git log --oneline", false},
		{"grep -rn x .", false},
		{"go test ./... | tail -5", false}, // only the first stage of a chain counts
		{"cat <<EOF\nx\nEOF", false},       // the tokenizer skips heredocs
		{"echo $(cat f)", false},
		{"", false},
	}
	for _, c := range cases {
		if got := ReadsFiles(c.cmd); got != c.want {
			t.Errorf("ReadsFiles(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}
