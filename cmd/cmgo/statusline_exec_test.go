package main

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/procinfo"
	"github.com/lang315/context-mode-go/internal/store"
)

// Stats spec §3 through the built binary: the fallback on an empty data dir
// (which it must not create), then the session line once SessionStart has
// linked the session and its server has recorded a call.
func TestBinaryStatusLine(t *testing.T) {
	env := hookEnv(t)
	if out, code := runHookBinary(t, env, `{"session_id":"s1"}`, "statusline"); code != 0 || out != "cmgo  ●  ready" {
		t.Fatalf("empty data dir: exit %d, stdout %q", code, out)
	}
	if _, err := os.Stat(env["CMGO_DIR"]); !os.IsNotExist(err) {
		t.Fatalf("statusline created the data dir: %v", err)
	}
	if _, code := runHookBinary(t, env, `{"session_id":"s1","source":"startup"}`, "sessionstart"); code != 0 {
		t.Fatalf("sessionstart: exit %d", code)
	}

	// The hook is this test's child, so it finds the same Claude Code
	// ancestor as this process: a real pid under Claude Code, else 0.
	path := filepath.Join(env["CMGO_DIR"], "cmgo.db")
	db, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: path}).String()+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var cc int
	if err := db.QueryRow("SELECT cc_pid FROM session_meta WHERE session_id = 's1'").Scan(&cc); err != nil {
		t.Fatal(err)
	}
	if want := procinfo.ClaudePID(os.Getpid(), procinfo.System()); cc != want {
		t.Fatalf("recorded cc_pid %d, want %d", cc, want)
	}
	cfg, err := config.Load(func(k string) string { return env[k] }, os.Getwd)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if cc == 0 { // cc_pid 0 never links; stand in for Claude Code
		cc = 4242
		if err := st.SetSessionCC("s1", cfg.ProjectKey, cc); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AddToolStat("srv", cc, cfg.ProjectKey, "2000-01-01 00:00:00", "ctx_execute", 1000, 9000); err != nil {
		t.Fatal(err)
	}
	want := "cmgo  ●  8.8 KB this chat  ·  8.8 KB lifetime  ·  90% kept out"
	if out, code := runHookBinary(t, env, `{"session_id":"s1"}`, "statusline"); code != 0 || out != want {
		t.Fatalf("exit %d, stdout %q, want %q", code, out, want)
	}
	if log := hookErrors(env); log != "" {
		t.Fatalf("hook errors: %s", log)
	}
}
