package install

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// mcpEntry is what ~/.claude.json holds for mcpServers.cmgo.
type mcpEntry struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// mcpState reads mcpServers.cmgo. A missing or unparseable file counts as not
// registered; cmgo never writes it, the claude CLI does.
func mcpState(path string) (e mcpEntry, registered bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return e, false
	}
	var v struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(b, &v) != nil || v.MCPServers["cmgo"] == nil {
		return e, false
	}
	json.Unmarshal(v.MCPServers["cmgo"], &e) // a malformed entry is stale: re-register
	return e, true
}

// registerMCP adds (add) or removes the user-scope cmgo MCP server when that
// is needed. An entry that points at another binary is replaced, so an
// install from a new path repoints Claude Code.
func registerMCP(o Options, add bool) error {
	e, registered := mcpState(o.ClaudeJSONPath)
	if !add {
		if !registered {
			return nil
		}
		return claude(o, "mcp", "remove", "--scope", "user", "cmgo")
	}
	if registered {
		if e.Command == o.Binary && len(e.Args) == 1 && e.Args[0] == "serve" {
			return nil
		}
		if err := claude(o, "mcp", "remove", "--scope", "user", "cmgo"); err != nil {
			return err
		}
	}
	return claude(o, "mcp", "add", "--scope", "user", "cmgo", "--", o.Binary, "serve")
}

// claude runs the claude CLI, or prints the command when it is not on PATH or
// in a dry run.
func claude(o Options, args ...string) error {
	if !o.DryRun {
		if _, err := o.LookPath("claude"); err == nil {
			if err := o.Run("claude", args...); err != nil {
				return fmt.Errorf("claude %s %s: %w", args[0], args[1], err)
			}
			return nil
		}
	}
	q := []string{"claude"}
	for _, a := range args {
		q = append(q, shellQuote(a))
	}
	_, err := fmt.Fprintf(o.Stdout, "run: %s\n", strings.Join(q, " "))
	return err
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote quotes s for a POSIX shell, leaving plain words as they are.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
