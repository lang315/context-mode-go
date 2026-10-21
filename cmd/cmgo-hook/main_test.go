package main

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// cmgo-hook exists to start fast (hooks spec §1.1): it must not link the
// MCP server or anything only the server needs.
func TestNoServerDeps(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	forbidden := []string{
		"github.com/modelcontextprotocol/go-sdk",
		"net/http",
		"github.com/JohannesKaufmann/html-to-markdown",
		"github.com/lang315/context-mode-go/internal/executor",
		"github.com/lang315/context-mode-go/internal/fetch",
		"github.com/lang315/context-mode-go/internal/mcpserver",
	}
	deps := strings.Fields(string(out))
	if !slices.Contains(deps, "github.com/lang315/context-mode-go/internal/hookcmd") {
		t.Fatalf("cmgo-hook does not link internal/hookcmd; deps: %v", deps)
	}
	for _, dep := range deps {
		for _, f := range forbidden {
			if dep == f || strings.HasPrefix(dep, f+"/") {
				t.Errorf("cmgo-hook links %s", dep)
			}
		}
	}
}
