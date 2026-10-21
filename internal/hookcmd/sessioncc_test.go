package hookcmd

import (
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// fakeClaude makes this process a child of a "claude" process with pid 4242,
// so the test does not depend on the Claude Code (or not) it runs under.
func fakeClaude(t *testing.T) {
	t.Helper()
	old := procLookup
	t.Cleanup(func() { procLookup = old })
	procLookup = func(pid int) (int, string, error) {
		switch pid {
		case os.Getpid():
			return 4242, "cmgo-hook", nil
		case 4242:
			return 1, "claude", nil
		}
		return 0, "", errors.New("no such process")
	}
}

// sessionCC reads a session's session_meta.cc_pid, or -1 when it has no row.
func sessionCC(t *testing.T, env map[string]string, sid string) int {
	t.Helper()
	db, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: filepath.Join(env["CMGO_DIR"], "cmgo.db")}).String()+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cc := -1
	if err := db.QueryRow("SELECT cc_pid FROM session_meta WHERE session_id = ?", sid).Scan(&cc); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	return cc
}

// Stats spec §1.2: every SessionStart source records the Claude Code pid of
// its session, clear included (it starts a new session id in the same
// process), and still prints the routing block first.
func TestHookSessionStartRecordsCCPID(t *testing.T) {
	fakeClaude(t)
	env := hookEnv(t)
	for _, src := range []string{"startup", "clear", "resume", "compact"} {
		sid := "s-" + src
		if out := callHook(t, env, "sessionstart", `{"session_id":"`+sid+`","source":"`+src+`"}`); out != sessionStartOut("") {
			t.Fatalf("%s: got %q", src, out)
		}
		if cc := sessionCC(t, env, sid); cc != 4242 {
			t.Errorf("%s: cc_pid = %d, want 4242", src, cc)
		}
	}
	if log := hookErrors(env); log != "" {
		t.Fatalf("hook errors: %s", log)
	}
}

// With no claude ancestor the row still exists, with cc_pid 0 (never linked).
func TestHookSessionStartNoClaudeAncestor(t *testing.T) {
	old := procLookup
	t.Cleanup(func() { procLookup = old })
	procLookup = func(int) (int, string, error) { return 0, "", errors.New("no such process") }
	env := hookEnv(t)
	callHook(t, env, "sessionstart", `{"session_id":"s1","source":"clear"}`)
	if cc := sessionCC(t, env, "s1"); cc != 0 {
		t.Fatalf("cc_pid = %d, want 0", cc)
	}
}
