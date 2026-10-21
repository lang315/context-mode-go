package install

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

const (
	addCmd    = "claude mcp add --scope user cmgo -- " + bin + " serve"
	removeCmd = "claude mcp remove --scope user cmgo"
)

func writeClaudeJSON(t *testing.T, o Options, content string) {
	t.Helper()
	if err := os.WriteFile(o.ClaudeJSONPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInstallMCP(t *testing.T) {
	cases := []struct {
		name, claudeJSON string // "" = no ~/.claude.json
		claude, dryRun   bool
		runs             []string
		printed          string
	}{
		{name: "no claude.json, claude on PATH", claude: true, runs: []string{addCmd}},
		{name: "unparseable claude.json", claudeJSON: `{"mcpServers":`, claude: true, runs: []string{addCmd}},
		{name: "other servers only", claudeJSON: `{"mcpServers":{"x":{}}}`, claude: true, runs: []string{addCmd}},
		{name: "claude not on PATH", printed: "run: " + addCmd + "\n"},
		{name: "dry run", claude: true, dryRun: true, printed: "run: " + addCmd + "\n"},
		{name: "registered", claudeJSON: `{"mcpServers":{"cmgo":{"command":"cmgo"}}}`, claude: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, f := newOpts(t)
			f.claude, o.DryRun = c.claude, c.dryRun
			if c.claudeJSON != "" {
				writeClaudeJSON(t, o, c.claudeJSON)
			}
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(f.runs, c.runs) {
				t.Fatalf("runs %q, want %q", f.runs, c.runs)
			}
			if got := strings.Contains(f.stdout.String(), "run: claude"); got != (c.printed != "") ||
				(c.printed != "" && !strings.HasSuffix(f.stdout.String(), c.printed)) {
				t.Fatalf("stdout %q, want suffix %q", f.stdout.String(), c.printed)
			}
		})
	}
}

func TestInstallMCPQuotesBinary(t *testing.T) {
	o, f := newOpts(t)
	o.Binary = "/Users/me/my bin/it's/cmgo"
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	want := `run: claude mcp add --scope user cmgo -- '/Users/me/my bin/it'\''s/cmgo' serve` + "\n"
	if !strings.HasSuffix(f.stdout.String(), want) {
		t.Fatalf("stdout %q", f.stdout.String())
	}
}

func TestInstallMCPRunError(t *testing.T) {
	o, _ := newOpts(t)
	o.LookPath = func(string) (string, error) { return "/bin/claude", nil }
	o.Run = func(string, ...string) error { return errors.New("exit status 1") }
	if err := Install(o); err == nil || !strings.Contains(err.Error(), "claude mcp add") {
		t.Fatalf("err %v", err)
	}
}

func TestUninstallMCP(t *testing.T) {
	cases := []struct {
		name, claudeJSON string
		claude, dryRun   bool
		runs             []string
		printed          bool
	}{
		{name: "registered, claude on PATH", claudeJSON: `{"mcpServers":{"cmgo":{}}}`, claude: true, runs: []string{removeCmd}},
		{name: "registered, no claude", claudeJSON: `{"mcpServers":{"cmgo":{}}}`, printed: true},
		{name: "registered, dry run", claudeJSON: `{"mcpServers":{"cmgo":{}}}`, claude: true, dryRun: true, printed: true},
		{name: "not registered", claudeJSON: `{"mcpServers":{}}`, claude: true},
		{name: "no claude.json", claude: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, f := newOpts(t)
			f.claude, o.DryRun = c.claude, c.dryRun
			if c.claudeJSON != "" {
				writeClaudeJSON(t, o, c.claudeJSON)
			}
			if err := Uninstall(o); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(f.runs, c.runs) {
				t.Fatalf("runs %q, want %q", f.runs, c.runs)
			}
			if got := strings.HasSuffix(f.stdout.String(), "run: "+removeCmd+"\n"); got != c.printed {
				t.Fatalf("stdout %q", f.stdout.String())
			}
		})
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/local/bin/cmgo": "/usr/local/bin/cmgo",
		"a b":                 "'a b'",
		"it's":                `'it'\''s'`,
		"":                    "''",
		"$HOME":               "'$HOME'",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("%q: %s, want %s", in, got, want)
		}
	}
}
