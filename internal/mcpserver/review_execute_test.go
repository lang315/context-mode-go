package mcpserver

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lang315/context-mode-go/internal/store"
)

// Review focus: binary output through the whole ctx_execute path (index +
// intent search) must reach the client as valid UTF-8 and stay searchable.
func TestExecuteBinaryOutputIsValidUTF8(t *testing.T) {
	h := newHarness(t, nil)
	out, isErr := h.call("ctx_execute", `{"language": "shell", "code": "head -c 200000 /dev/urandom; echo; echo marker-zebra-42"}`)
	if isErr {
		t.Fatalf("isError:\n%.300s", out)
	}
	if !utf8.ValidString(out) {
		t.Fatal("result text is not valid UTF-8")
	}
	if !strings.Contains(out, "Indexed") {
		t.Fatalf("output over 100KB should be indexed:\n%.300s", out)
	}
	res, err := h.Deps.Store.SearchWithFallback("zebra", store.SearchOpts{Project: h.Deps.Cfg.ProjectKey, Limit: 3})
	if err != nil || len(res) == 0 {
		t.Fatalf("marker not searchable after binary output: %v %v", res, err)
	}
}
