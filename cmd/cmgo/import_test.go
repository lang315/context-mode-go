package main

import (
	"bytes"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

// tsRoot writes a minimal TS data root: one active session with two events,
// one session without a project, and a content DB with a fresh and an
// expired source. Only the columns the importer reads are created; the
// full TS schemas are covered in internal/tsimport.
func tsRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	at := func(d time.Duration) string { return time.Now().UTC().Add(-d).Format("2006-01-02 15:04:05") }
	open := func(path, ddl string) *sql.DB {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		db, err := driver.Open((&url.URL{Scheme: "file", Path: path}).String(), fts5.Register)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
		return db
	}
	exec := func(db *sql.DB, q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	s := open(filepath.Join(root, "sessions", "0123456789abcdef.db"), `
CREATE TABLE session_events (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, type TEXT NOT NULL,
  category TEXT NOT NULL, data TEXT NOT NULL, project_dir TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);`)
	exec(s, "INSERT INTO session_events(session_id, type, category, data, project_dir, created_at) VALUES('s1', 'cwd', 'cwd', '/w', '/w', ?), ('s1', 'git', 'git', 'push', '', ?), ('s2', 'cwd', 'cwd', '/', '', ?)",
		at(time.Hour), at(time.Minute), at(time.Minute))
	c := open(filepath.Join(root, "content", "0123456789abcdef.db"), `
CREATE TABLE sources (id INTEGER PRIMARY KEY AUTOINCREMENT, label TEXT NOT NULL, indexed_at TEXT NOT NULL);
CREATE VIRTUAL TABLE chunks USING fts5(title, content, source_id UNINDEXED, content_type UNINDEXED);`)
	exec(c, "INSERT INTO sources(label, indexed_at) VALUES('fresh', ?), ('stale', ?)", at(time.Hour), at(20*24*time.Hour))
	exec(c, "INSERT INTO chunks(title, content, source_id, content_type) VALUES('T', 'text', 1, 'prose'), ('U', 'more', 1, 'prose'), ('V', 'old', 2, 'prose')")
	return root
}

func importEnv(t *testing.T, extra map[string]string) (getenv func(string) string, dataDir string) {
	home := t.TempDir()
	dataDir = filepath.Join(home, "cmgo")
	env := map[string]string{"HOME": home, "CMGO_DIR": dataDir, "CMGO_PROJECT_DIR": home}
	for k, v := range extra {
		env[k] = v
	}
	return func(k string) string { return env[k] }, dataDir
}

func runImportCmd(getenv func(string) string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := runImport(args, &out, &errb, getenv)
	return code, out.String(), errb.String()
}

var doneLine = regexp.MustCompile(`\ndone in \d+ ms\n$`)

func TestImportCommandOutputAndGuard(t *testing.T) {
	root := tsRoot(t)
	getenv, dataDir := importEnv(t, nil)
	code, out, errOut := runImportCmd(getenv, "--from", root)
	want := "cmgo import from " + root + "\n" +
		"  sessions: 1 imported, 1 skipped (1 no project)\n" +
		"  events:   2 imported, 0 duplicates\n" +
		"  sources:  1 imported (2 chunks), 1 skipped (1 expired)\n"
	if code != 0 || !strings.HasPrefix(out, want) || !doneLine.MatchString(out) || errOut != "" {
		t.Fatalf("first run = %d\n%s\nstderr %q\nwant prefix\n%s", code, out, errOut, want)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "cmgo.db")); err != nil {
		t.Fatal(err)
	}

	code, out, errOut = runImportCmd(getenv, "--from", root)
	if code != 1 || out != "" || !strings.Contains(errOut, "already imported") || !strings.Contains(errOut, "--force") {
		t.Fatalf("second run = %d %q %q", code, out, errOut)
	}

	code, out, _ = runImportCmd(getenv, "--from", root, "--force")
	want = "cmgo import from " + root + "\n" +
		"  sessions: 1 imported, 1 skipped (1 no project)\n" +
		"  events:   0 imported, 2 duplicates\n" +
		"  sources:  0 imported (0 chunks), 2 skipped (1 expired, 1 exists)\n"
	if code != 0 || !strings.HasPrefix(out, want) {
		t.Fatalf("forced run = %d\n%s\nwant prefix\n%s", code, out, want)
	}
}

func TestImportCommandDryRunAndDefaultRoot(t *testing.T) {
	root := tsRoot(t)
	getenv, dataDir := importEnv(t, map[string]string{"CONTEXT_MODE_DIR": root})
	code, out, errOut := runImportCmd(getenv, "--dry-run")
	if code != 0 || !strings.HasPrefix(out, "cmgo import from "+root+" (dry run)\n  sessions: 1 imported,") || errOut != "" {
		t.Fatalf("dry run = %d\n%s\nstderr %q", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "cmgo.db")); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote cmgo.db: %v", err)
	}
}

func TestImportCommandErrors(t *testing.T) {
	getenv, _ := importEnv(t, nil)
	if code, _, errOut := runImportCmd(getenv); code != 1 || !strings.Contains(errOut, "no TS context-mode data found") {
		t.Fatalf("no root = %d %q", code, errOut)
	}
	if code, _, _ := runImportCmd(getenv, "--bogus"); code != 2 {
		t.Fatalf("bad flag exit = %d, want 2", code)
	}
	if code, _, _ := runImportCmd(getenv, "extra"); code != 2 {
		t.Fatalf("extra argument exit = %d, want 2", code)
	}
}

func TestImportCommandCountsUnreadableDBs(t *testing.T) {
	root := tsRoot(t)
	for _, dir := range []string{"sessions", "content"} {
		if err := os.WriteFile(filepath.Join(root, dir, "zzzz.db"), bytes.Repeat([]byte("not a database "), 600), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	getenv, _ := importEnv(t, nil)
	code, out, errOut := runImportCmd(getenv, "--from", root)
	want := "cmgo import from " + root + "\n" +
		"  sessions: 1 imported, 2 skipped (1 no project, 1 unreadable)\n" +
		"  events:   2 imported, 0 duplicates\n" +
		"  sources:  1 imported (2 chunks), 2 skipped (1 expired, 1 unreadable)\n"
	if code != 0 || !strings.HasPrefix(out, want) || errOut != "" {
		t.Fatalf("run = %d\n%s\nstderr %q\nwant prefix\n%s", code, out, errOut, want)
	}
}
