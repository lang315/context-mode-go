package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorTool(t *testing.T) {
	h := newHarness(t, nil)
	bin := filepath.Join(t.TempDir(), "cmgo")
	os.WriteFile(filepath.Join(filepath.Dir(bin), "cmgo-hook"), []byte("#!/bin/sh\necho cmgo-hook test\n"), 0o755)
	h.Deps.Binary = bin
	text, isErr := h.call("ctx_doctor", `{}`)
	// The Version line depends on the fake script starting within 2 s, so it
	// is only checked for its cmgo half.
	if isErr || !strings.HasPrefix(text, "cmgo doctor\n\n[") || !strings.Contains(text, "] Version: cmgo test (hook binary ") {
		t.Fatalf("isErr=%v\n%s", isErr, text)
	}
	if !strings.Contains(text, "\n[OK] Storage: "+h.Deps.Cfg.DataDir+" (writable)\n[OK] Database: "+h.Deps.Cfg.DBPath+" schema v") {
		t.Fatalf("isErr=%v\n%s", isErr, text)
	}
	// No settings.json in the temp home: hooks missing, statusline not set.
	for _, want := range []string{"\n[OK] Executor: PASS (shell)\n", "\n[WARN] Hooks: missing PreToolUse, ", "\n[OK] Hook binary: " + filepath.Join(filepath.Dir(bin), "cmgo-hook") + "\n", "\n[WARN] MCP server: not registered — fix: cmgo install\n", "\n[OK] Statusline: not set\n[OK] TS plugin: not enabled"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
}

func TestUpgradeTool(t *testing.T) {
	h := newHarness(t, nil)
	h.Deps.Binary = "/opt/cmgo/bin/cmgo"
	text, isErr := h.call("ctx_upgrade", `{}`)
	want := "## ctx-upgrade\n\nRun this command using your shell execution tool:\n\n```\n\"/opt/cmgo/bin/cmgo\" upgrade\n```\n\n" +
		"After the command completes, display results as a markdown checklist:\n- `[x]` for success, `[ ]` for failure\n- Example format:\n" +
		"  ```\n  ## cmgo upgrade\n  - [x] git -C ~/src/context-mode-go pull --ff-only\n  - [x] go install ./cmd/cmgo ./cmd/cmgo-hook (in ~/src/context-mode-go)\n  - [x] ~/go/bin/cmgo install\n  ```\n" +
		"- Tell the user to restart their session to pick up the new version."
	if isErr || text != want {
		t.Fatalf("isErr=%v\ngot:\n%s\nwant:\n%s", isErr, text, want)
	}
}
