package hookcmd

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

const ready = "cmgo  ●  ready"

// statusDB opens a writer on the hook DB with session s1 (cc_pid 100) whose
// server kept 90 of 100 bytes, and another session's 1000 kept bytes. The
// writer stays open until the test ends, so the -wal file exists.
func statusDB(t *testing.T, env map[string]string) string {
	t.Helper()
	path := filepath.Join(env["CMGO_DIR"], "cmgo.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, err := range []error{
		st.SetSessionCC("s1", "/p", 100),
		st.AddToolStat("srv", 100, "/p", "2000-01-01 00:00:00", "ctx_execute", 10, 90),
		st.AddToolStat("other", 200, "/q", "2000-01-01 00:00:00", "ctx_execute", 0, 1000),
		st.SetSessionCC("s2", "/p", 300),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// fileState is a file's size and mtime, or "absent".
func fileState(t *testing.T, path string) string {
	t.Helper()
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "absent"
	}
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d bytes, %v", fi.Size(), fi.ModTime())
}

func TestStatusLine(t *testing.T) {
	env := hookEnv(t)
	path := statusDB(t, env)
	cases := []struct{ name, sessionEnv, stdin, want string }{
		{"session", "", `{"session_id":"s1","cwd":"/x"}`, "cmgo  ●  90 B this chat  ·  1.1 KB lifetime  ·  90% kept out"},
		{"env session", "s1", `{"cwd":"/x"}`, "cmgo  ●  90 B this chat  ·  1.1 KB lifetime  ·  90% kept out"},
		{"stdin wins over env", "s2", `{"session_id":"s1"}`, "cmgo  ●  90 B this chat  ·  1.1 KB lifetime  ·  90% kept out"},
		{"empty stdin, env session", "s1", "", "cmgo  ●  90 B this chat  ·  1.1 KB lifetime  ·  90% kept out"},
		{"malformed stdin, env session", "s1", "{nope", "cmgo  ●  90 B this chat  ·  1.1 KB lifetime  ·  90% kept out"},
		{"session without server rows", "", `{"session_id":"s2"}`, "cmgo  ●  1.1 KB kept out lifetime"},
		{"unknown session", "", `{"session_id":"nope"}`, "cmgo  ●  1.1 KB kept out lifetime"},
		{"no session", "", `{"cwd":"/x"}`, ready},
		{"empty stdin, no session", "", "", ready},
	}
	if fileState(t, path+"-wal") == "absent" {
		t.Fatal("no -wal file: the no-write check would prove nothing")
	}
	before := fileState(t, path) + " | " + fileState(t, path+"-wal")
	for _, c := range cases {
		e := map[string]string{"HOME": env["HOME"], "CMGO_DIR": env["CMGO_DIR"], "CLAUDE_SESSION_ID": c.sessionEnv}
		if got := callHook(t, e, "statusline", c.stdin); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	// Spec criterion 3: the statusline never writes, even while a writer
	// holds the DB open.
	if after := fileState(t, path) + " | " + fileState(t, path+"-wal"); after != before {
		t.Fatalf("DB files changed:\nbefore %s\nafter  %s", before, after)
	}
	if log := hookErrors(env); log != "" {
		t.Fatalf("hook errors: %s", log)
	}
}

// A missing DB is the fallback and creates nothing: no data dir, no log.
func TestStatusLineNoDB(t *testing.T) {
	env := hookEnv(t)
	if got := callHook(t, env, "statusline", `{"session_id":"s1"}`); got != ready {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(env["CMGO_DIR"]); !os.IsNotExist(err) {
		t.Fatalf("data dir created: %v", err)
	}
}

// A DB older than migration 3 has no tool_stats: fallback, not an error.
func TestStatusLineOldSchema(t *testing.T) {
	env := hookEnv(t)
	path := filepath.Join(env["CMGO_DIR"], "cmgo.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	db, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: path}).String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA user_version = 2"); err != nil {
		t.Fatal(err)
	}
	if got := callHook(t, env, "statusline", `{"session_id":"s1"}`); got != ready {
		t.Fatalf("got %q", got)
	}
	if log := hookErrors(env); log != "" {
		t.Fatalf("hook errors: %s", log)
	}
}
