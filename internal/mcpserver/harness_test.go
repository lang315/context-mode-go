package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/lang315/context-mode-go/internal/fetch"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// harness is a connected server+client pair over in-memory transports,
// backed by a temp project dir, temp home and temp DB.
type harness struct {
	t       *testing.T
	Deps    *Deps
	Session *mcp.ClientSession
	Project string
	Home    string
}

// newHarness builds Deps rooted in t.TempDir(). now may be nil (time.Now).
func newHarness(t *testing.T, now func() time.Time) *harness {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	home := filepath.Join(root, "home")
	for _, d := range []string{project, home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{
		DataDir:    filepath.Join(root, "data"),
		DBPath:     filepath.Join(root, "data", "cmgo.db"),
		ProjectDir: project,
		ProjectKey: config.NormalizeProject(project),
		Home:       home,
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	ex := executor.New(project)
	t.Cleanup(func() { ex.Shutdown(); st.Close() })
	d := &Deps{Cfg: cfg, Store: st, Exec: ex, Policy: security.NewPolicy(project, home), Fetch: fetch.New(false), Now: now}

	srv := New(d, "test")
	ct, stt := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.Connect(ctx, stt, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close(); ss.Close() })
	return &harness{t: t, Deps: d, Session: cs, Project: project, Home: home}
}

// call invokes a tool with arguments given as a JSON object literal and
// returns the concatenated text content and the IsError flag.
func (h *harness) call(name, argsJSON string) (string, bool) {
	h.t.Helper()
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		h.t.Fatalf("bad test args %s: %v", argsJSON, err)
	}
	res, err := h.Session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		h.t.Fatalf("CallTool %s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

// writeSettings writes <project>/.claude/settings.json with the given deny rules.
func (h *harness) writeSettings(deny ...string) {
	h.t.Helper()
	dir := filepath.Join(h.Project, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"permissions": map[string]any{"deny": deny}})
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), body, 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func TestHarnessConnects(t *testing.T) {
	h := newHarness(t, nil)
	if _, err := h.Session.ListTools(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}
