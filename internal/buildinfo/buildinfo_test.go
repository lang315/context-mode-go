package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestResolve(t *testing.T) {
	info := func(v string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Path: "github.com/lang315/context-mode-go", Version: v}}, true
		}
	}
	none := func() (*debug.BuildInfo, bool) { return nil, false }
	cases := []struct {
		name, ldflag string
		info         func() (*debug.BuildInfo, bool)
		want         string
	}{
		{"ldflags win", "v1.2.3", info("v0.9.0"), "v1.2.3"},
		{"module version", "", info("v0.9.0"), "v0.9.0"},
		{"local build", "", info("(devel)"), "dev"},
		{"empty module version", "", info(""), "dev"},
		{"no build info", "", none, "dev"},
	}
	for _, c := range cases {
		if got := resolve(c.ldflag, c.info); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Under go test the main module has no version, so Version falls back to dev.
func TestVersionUnderTest(t *testing.T) {
	if got := Version(""); got != "dev" {
		t.Fatalf("got %q", got)
	}
	if got := Version("v2"); got != "v2" {
		t.Fatalf("got %q", got)
	}
}
