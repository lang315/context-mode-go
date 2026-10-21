package install

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// mcpRegistered reports whether ~/.claude.json has mcpServers.cmgo. A missing
// or unparseable file counts as not registered; cmgo never writes it, the
// claude CLI does.
func mcpRegistered(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var v struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	return json.Unmarshal(b, &v) == nil && v.MCPServers["cmgo"] != nil
}

// registerMCP adds (add) or removes the user-scope cmgo MCP server when that
// is needed.
func registerMCP(o Options, add bool) error {
	if mcpRegistered(o.ClaudeJSONPath) == add {
		return nil
	}
	args := []string{"mcp", "remove", "--scope", "user", "cmgo"}
	if add {
		args = []string{"mcp", "add", "--scope", "user", "cmgo", "--", o.Binary, "serve"}
	}
	return claude(o, args...)
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
