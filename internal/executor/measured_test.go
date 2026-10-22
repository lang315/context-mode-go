package executor

import (
	"os"
	"testing"
	"time"
)

// Measured says whether the run could report SandboxBytes: JS/TS and Python
// with counters on, not detached. Everything else (shell, other languages,
// Python with the switch off, a run still going when Run returns) has no
// counts, so its kept bytes are unknown.
func TestResultMeasured(t *testing.T) {
	cases := []struct {
		name string
		req  Request
		off  bool // CMGO_PY_COUNTERS=0
		want bool
	}{
		{"javascript", Request{Language: JavaScript, Code: "console.log(1)"}, false, true},
		{"typescript", Request{Language: TypeScript, Code: "console.log(1)"}, false, true},
		{"ruby", Request{Language: Ruby, Code: "puts 1"}, false, false},
		{"shell", Request{Language: Shell, Code: "echo 1"}, false, false},
		{"python", Request{Language: Python, Code: "print(1)"}, false, true},
		{"python counters off", Request{Language: Python, Code: "print(1)"}, true, false},
		{"python background", Request{Language: Python, Code: "print(1)", Background: true, Timeout: 300 * time.Millisecond}, false, false},
		// Detached: still running when Run returns, so no counter line was written.
		{"javascript background detached", Request{Language: JavaScript, Code: "console.log(1)", Background: true, Timeout: 300 * time.Millisecond}, false, false},
		// wrapJS keeps a background JS run alive with setInterval, so one that exits
		// before its timeout cannot be made from JS; shell can, but is not instrumented.
		{"shell background finished", Request{Language: Shell, Code: "echo 1", Background: true, Timeout: 5 * time.Second}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newTestExec(t)
			if e.runtime(c.req.Language) == "" {
				t.Skipf("%s runtime not installed", c.req.Language)
			}
			if c.off {
				e.getenv = func(k string) string {
					if k == "CMGO_PY_COUNTERS" {
						return "0"
					}
					return os.Getenv(k)
				}
			}
			if c.req.Timeout == 0 {
				c.req.Timeout = 60 * time.Second
			}
			res := run(t, e, c.req)
			t.Cleanup(func() { e.Shutdown() })
			if res.Measured != c.want {
				t.Errorf("Measured = %v, want %v", res.Measured, c.want)
			}
		})
	}
}
