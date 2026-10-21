package hookcmd

import (
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/lang315/context-mode-go/internal/procinfo"
)

// fakeClaude makes this process a child of a "claude" process with pid 4242,
// so the test does not depend on the Claude Code (or not) it runs under.
func fakeClaude(t *testing.T) {
	t.Helper()
	old := procLookup
	t.Cleanup(func() { procLookup = old })
	procLookup = func(pid int) (procinfo.Info, error) {
		switch pid {
		case os.Getpid():
			return procinfo.Info{PPID: 4242, Exe: "/x/cmgo-hook", Arg0: "cmgo-hook"}, nil
		case 4242:
			// The native installer: the kernel name is the version, the paths say claude.
			return procinfo.Info{PPID: 1, Exe: "/h/.local/share/claude/versions/2.1.304", Arg0: "claude"}, nil
		}
		return procinfo.Info{}, errors.New("no such process")
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

// A session no cmgo SessionStart has seen (it started before cmgo was
// installed) is linked by its next hook that writes events.
func TestHookLinksUnseenSession(t *testing.T) {
	fakeClaude(t)
	env := hookEnv(t)
	callHook(t, env, "userpromptsubmit", `{"session_id":"s1","prompt":"hello"}`)
	if cc := sessionCC(t, env, "s1"); cc != 4242 {
		t.Fatalf("cc_pid = %d, want 4242", cc)
	}
	if log := hookErrors(env); log != "" {
		t.Fatalf("hook errors: %s", log)
	}
}

// A linked session keeps its link: later hooks do not move linked_at, which
// picks the window of the server's stats.
func TestHookKeepsExistingLink(t *testing.T) {
	fakeClaude(t)
	env := hookEnv(t)
	callHook(t, env, "sessionstart", `{"session_id":"s1","source":"startup"}`)
	before := linkedAt(t, env, "s1")
	callHook(t, env, "userpromptsubmit", `{"session_id":"s1","prompt":"hello"}`)
	if after := linkedAt(t, env, "s1"); after != before {
		t.Fatalf("linked_at moved from %q to %q", before, after)
	}
}

func linkedAt(t *testing.T, env map[string]string, sid string) string {
	t.Helper()
	db, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: filepath.Join(env["CMGO_DIR"], "cmgo.db")}).String()+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var at string
	if err := db.QueryRow("SELECT linked_at FROM session_meta WHERE session_id = ?", sid).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

// With no claude ancestor the row still exists, with cc_pid 0 (never linked).
func TestHookSessionStartNoClaudeAncestor(t *testing.T) {
	old := procLookup
	t.Cleanup(func() { procLookup = old })
	procLookup = func(int) (procinfo.Info, error) { return procinfo.Info{}, errors.New("no such process") }
	env := hookEnv(t)
	callHook(t, env, "sessionstart", `{"session_id":"s1","source":"clear"}`)
	if cc := sessionCC(t, env, "s1"); cc != 0 {
		t.Fatalf("cc_pid = %d, want 0", cc)
	}
}
