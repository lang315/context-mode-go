package mcpserver

import (
	"strings"
	"testing"
)

func TestExecuteRecordsUnmeasured(t *testing.T) {
	h := newHarness(t, nil)
	row := h.recordStats()
	h.call("ctx_execute", args("language", "shell", "code", "echo hi"))
	if got := row("ctx_execute"); got.Calls != 1 || got.Unmeasured != 1 {
		t.Fatalf("after shell: %+v", got)
	}
	h.call("ctx_execute", args("language", "javascript", "code", "console.log(1)"))
	if got := row("ctx_execute"); got.Calls != 2 || got.Unmeasured != 1 {
		t.Fatalf("after javascript: %+v", got)
	}
	// Never ran code: an argument error adds a call, not an unmeasured call.
	h.call("ctx_execute", `{"language":"shell"}`)
	if got := row("ctx_execute"); got.Calls != 3 || got.Unmeasured != 1 {
		t.Fatalf("after argument error: %+v", got)
	}
}

func TestStatsShowsNotMeasuredOnlyWhenCounted(t *testing.T) {
	h := newHarness(t, nil)
	h.recordStats()
	h.call("ctx_execute", args("language", "javascript", "code", "console.log(1)"))
	if got, _ := h.call("ctx_stats", `{}`); strings.Contains(got, "not measured") {
		t.Fatalf("JS-only report mentions not measured:\n%s", got)
	}
	h.call("ctx_execute", args("language", "shell", "code", "echo hi"))
	got, _ := h.call("ctx_stats", `{}`)
	if !strings.Contains(got, "returned  ·  1 not measured\n") || !strings.Contains(got, "kept out  ·  1 ctx_execute calls not measured  ·  ") {
		t.Fatalf("report:\n%s", got)
	}
}
