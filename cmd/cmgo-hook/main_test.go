package main

import (
	"os/exec"
	"path/filepath"
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
		"github.com/lang315/context-mode-go/internal/install", // embeds the skills
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

// `cmgo-hook version` prints the -X main.version value; doctor compares it
// with cmgo's own version.
func TestVersionArg(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "cmgo-hook")
	if out, err := exec.Command("go", "build", "-ldflags", "-X main.version=v9.9.9", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "version").Output()
	if err != nil || string(out) != "cmgo-hook v9.9.9\n" {
		t.Fatalf("version: %q, %v", out, err)
	}
}
