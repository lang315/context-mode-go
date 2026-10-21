package hookcmd

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
)

// holdDBLock keeps BEGIN IMMEDIATE on the hook DB from a second connection.
func holdDBLock(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: path}).String()+"?_pragma=busy_timeout(100)")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.ExecContext(context.Background(), "ROLLBACK")
		conn.Close()
		db.Close()
	})
}

// Claude Code waits for the hook process to exit, so a DB that another
// session holds locked must cost a hook seconds, not the 30 s server timeout.
func TestHookLockedDBFailsOpenFast(t *testing.T) {
	env := hookEnv(t)
	callHook(t, env, "posttooluse", `{"session_id":"s1","tool_name":"Read","tool_input":{"file_path":"/p/a.go"}}`)
	holdDBLock(t, filepath.Join(env["CMGO_DIR"], "cmgo.db"))

	for _, c := range []struct{ event, stdin string }{
		{"posttooluse", `{"session_id":"s1","tool_name":"Read","tool_input":{"file_path":"/p/b.go"}}`},
		{"stop", `{"session_id":"s1","stop_hook_active":false}`},
	} {
		done := make(chan struct{})
		start := time.Now()
		go func() { defer close(done); callHook(t, env, c.event, c.stdin) }()
		select {
		case <-done:
			if d := time.Since(start); d > 3*time.Second {
				t.Errorf("%s took %v with the DB write-locked", c.event, d)
			}
		case <-time.After(6 * time.Second):
			t.Fatalf("%s still running after 6s with the DB write-locked", c.event)
		}
	}
	log, err := os.ReadFile(filepath.Join(env["CMGO_DIR"], "hook-errors.log"))
	if err != nil || strings.Count(string(log), "\n") != 2 {
		t.Fatalf("want 2 logged errors, got %q (%v)", log, err)
	}
}
