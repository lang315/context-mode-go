package procinfo

import (
	"encoding/binary"
	"testing"
)

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
		ppid, err := parseStat(c.in)
		if (err == nil) != c.ok || ppid != c.ppid {
			t.Errorf("parseStat(%q) = %d, %v; want %d, ok=%v", c.in, ppid, err, c.ppid, c.ok)
		}
	}
}

func TestParseCmdline(t *testing.T) {
	cases := []struct {
		in         string
		arg0, arg1 string
	}{
		{"claude\x00--resume\x00", "claude", "--resume"},
		{"node\x00/usr/lib/@anthropic-ai/claude-code/cli.js\x00-p\x00", "node", "/usr/lib/@anthropic-ai/claude-code/cli.js"},
		{"zsh\x00", "zsh", ""},
		{"zsh", "zsh", ""},
		{"", "", ""},
		{"\x00\x00", "", ""},
	}
	for _, c := range cases {
		if a0, a1 := parseCmdline([]byte(c.in)); a0 != c.arg0 || a1 != c.arg1 {
			t.Errorf("parseCmdline(%q) = %q, %q; want %q, %q", c.in, a0, a1, c.arg0, c.arg1)
		}
	}
}

// kern.procargs2: int32 argc, the exec path, NUL padding, argc argument
// strings, then the environment.
func TestParseProcArgs(t *testing.T) {
	buf := func(argc int32, rest string) []byte {
		b := make([]byte, 4, 4+len(rest))
		binary.NativeEndian.PutUint32(b, uint32(argc))
		return append(b, rest...)
	}
	cases := []struct {
		name            string
		in              []byte
		exe, arg0, arg1 string
	}{
		{"native claude", buf(2, "/Users/me/.local/share/claude/versions/2.1.304\x00\x00\x00claude\x00--resume\x00HOME=/Users/me\x00"),
			"/Users/me/.local/share/claude/versions/2.1.304", "claude", "--resume"},
		{"one argument, env follows", buf(1, "/bin/zsh\x00\x00zsh\x00HOME=/x\x00"), "/bin/zsh", "zsh", ""},
		{"argc counts: arg1 is not read from the env", buf(1, "/bin/zsh\x00zsh\x00A=b\x00"), "/bin/zsh", "zsh", ""},
		{"no padding", buf(2, "/usr/bin/node\x00node\x00/p/cli.js\x00"), "/usr/bin/node", "node", "/p/cli.js"},
		{"argc 0", buf(0, "/bin/x\x00\x00"), "/bin/x", "", ""},
		{"truncated", buf(2, "/bin/x"), "/bin/x", "", ""},
		{"too short", []byte{1, 0}, "", "", ""},
		{"empty", nil, "", "", ""},
	}
	for _, c := range cases {
		exe, a0, a1 := parseProcArgs(c.in)
		if exe != c.exe || a0 != c.arg0 || a1 != c.arg1 {
			t.Errorf("%s: parseProcArgs = %q, %q, %q; want %q, %q, %q", c.name, exe, a0, a1, c.exe, c.arg0, c.arg1)
		}
	}
}
