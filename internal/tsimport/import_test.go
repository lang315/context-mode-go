package tsimport

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/store"
)

var testNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

// ago formats testNow-d as a TS datetime('now') value.
func ago(d time.Duration) string { return testNow.Add(-d).Format("2006-01-02 15:04:05") }

const day = 24 * time.Hour

// buildRoot writes a TS data root and returns it. Its writers stay open
// until the test ends, so the -wal and -shm files exist during the import.
//
//	sessions/aaaa….db          current schema: s1 (active, 3 events, the last one only in the WAL),
//	                           s-old (inactive), s-noproj (no project dir anywhere)
//	sessions/aaaa…__1234abcd.db old schema: s1 again, one duplicate event and one new
//	sessions/bbbb….db          old schema: s-b, inactive, project /Proj/B
//	content/aaaa….db           8-column FTS: "fresh" (1 day), "stale" (15 days)
//	content/bbbb….db           4-column FTS: "bdocs" (2 days): its project has only inactive sessions
//	content/cccc….db           "orphan": no session DB starts with cccc…
func buildRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	a, b, c := "aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb", "cccccccccccccccc"

	db := tsDB(t, filepath.Join(root, "sessions", a+".db"), sessionDDL)
	addMeta(t, db, "s1", "/Proj/A", ago(2*time.Hour), ago(90*time.Minute), 1)
	addEvent(t, db, "s1", "file_read", "/Proj/A/main.go", "/Proj/A", ago(2*time.Hour))
	addEvent(t, db, "s1", "decision", "use the quokkaline parser", "/Proj/A", ago(110*time.Minute))
	addMeta(t, db, "s-old", "/Proj/A", ago(9*day), ago(8*day), 0)
	addEvent(t, db, "s-old", "file_read", "/Proj/A/old.go", "/Proj/A", ago(8*day))
	addEvent(t, db, "s-noproj", "cwd", "/", "", ago(time.Hour))
	checkpoint(t, db)
	addEvent(t, db, "s1", "file_edit", "/Proj/A/wal.go", "/Proj/A", ago(time.Hour))

	old := tsDB(t, filepath.Join(root, "sessions", a+"__1234abcd.db"), sessionDDLOld)
	addMeta(t, old, "s1", "/Proj/A", ago(3*time.Hour), ago(30*time.Minute), 2)
	for _, e := range [][3]string{
		{"file_read", "/Proj/A/main.go", ago(2 * time.Hour)}, // same as in a.db
		{"git", "commit", ago(30 * time.Minute)},
	} {
		mustExec(t, old, "INSERT INTO session_events(session_id, type, category, data, source_hook, created_at) VALUES('s1', ?, 'file', ?, 'PostToolUse', ?)", e[0], e[1], e[2])
	}

	bs := tsDB(t, filepath.Join(root, "sessions", b+".db"), sessionDDLOld)
	addMeta(t, bs, "s-b", "/Proj/B", ago(10*day), ago(10*day), 0)
	mustExec(t, bs, "INSERT INTO session_events(session_id, type, category, data, source_hook, created_at) VALUES('s-b', 'cwd', 'cwd', '/Proj/B', 'PostToolUse', ?)", ago(10*day))

	ca := tsDB(t, filepath.Join(root, "content", a+".db"), contentDDL)
	addSource(t, ca, "fresh", ago(day), [3]string{"Zephyrine", "the zephyrine config loader", "prose"}, [3]string{"Code", "func Load() {}", "code"})
	addSource(t, ca, "stale", ago(15*day), [3]string{"Old", "old text", "prose"})
	checkpoint(t, ca)
	cb := tsDB(t, filepath.Join(root, "content", b+".db"), contentDDLOld)
	addSource(t, cb, "bdocs", ago(2*day), [3]string{"B", "bravo docs", "prose"})
	cc := tsDB(t, filepath.Join(root, "content", c+".db"), contentDDL)
	addSource(t, cc, "orphan", ago(day), [3]string{"O", "orphan text", "prose"})
	return root
}

// hashTree returns the sha256 of every file under root.
func hashTree(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		out[p] = sha256.Sum256(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func options(t *testing.T, root string) Options {
	data := t.TempDir()
	return Options{Root: root, DataDir: data, DBPath: filepath.Join(data, "cmgo.db"), Now: testNow, SourceRetention: 14 * day}
}

// wantReport is the report of the first import of buildRoot.
var wantReport = Report{
	Sessions: 1, InactiveSessions: 2, NoProjectSessions: 1,
	Events: 4, DupEvents: 1,
	Sources: 2, Chunks: 3, ExpiredSources: 1, NoProjectSources: 1,
}

func openStore(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestRunImportsFixture(t *testing.T) {
	root := buildRoot(t)
	if fi, err := os.Stat(filepath.Join(root, "sessions", "aaaaaaaaaaaaaaaa.db-wal")); err != nil || fi.Size() == 0 {
		t.Fatalf("fixture wants WAL frames: %v %v", fi, err)
	}
	before := hashTree(t, root)
	o := options(t, root)
	got, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	want := wantReport
	want.Root = root
	if got != want {
		t.Fatalf("report = %+v\nwant     %+v", got, want)
	}
	if after := hashTree(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("TS files changed")
	}
	if left, _ := filepath.Glob(filepath.Join(o.DataDir, "run", "import-*")); len(left) != 0 {
		t.Fatalf("temp dirs left: %v", left)
	}

	st := openStore(t, o.DBPath)
	projA, projB := config.NormalizeProject("/Proj/A"), config.NormalizeProject("/Proj/B")
	evs, err := st.SessionEvents("s1")
	if err != nil {
		t.Fatal(err)
	}
	var datas []string
	for _, e := range evs {
		if e.Project != projA {
			t.Fatalf("event project = %q, want %q", e.Project, projA)
		}
		datas = append(datas, e.Data)
	}
	if want := []string{"/Proj/A/main.go", "use the quokkaline parser", "/Proj/A/wal.go", "commit"}; !reflect.DeepEqual(datas, want) {
		t.Fatalf("s1 events = %q, want %q", datas, want)
	}
	for _, sid := range []string{"s-old", "s-noproj", "s-b"} {
		if evs, _ := st.SessionEvents(sid); len(evs) != 0 {
			t.Fatalf("%s imported: %+v", sid, evs)
		}
	}
	for _, c := range []struct{ project, label string }{{projA, "fresh"}, {projB, "bdocs"}} {
		if src, err := st.SourceByLabel(c.project, c.label); err != nil || src == nil {
			t.Fatalf("source %s/%s = %v, %v", c.project, c.label, src, err)
		}
	}
	if src, _ := st.SourceByLabel(projA, "stale"); src != nil {
		t.Fatal("expired source imported")
	}
	mark, ok, err := st.Meta("ts_import")
	if want := testNow.Format(time.RFC3339) + " 1 2"; err != nil || !ok || mark != want {
		t.Fatalf("ts_import = %q, %v, %v; want %q", mark, ok, err, want)
	}

	// ctx_search and the timeline search find the imported data.
	res, err := st.SearchWithFallback("zephyrine", store.SearchOpts{Project: projA, Limit: 5})
	if err != nil || len(res) != 1 || res[0].Source != "fresh" {
		t.Fatalf("search = %+v, %v", res, err)
	}
	hits, err := st.SessionEventsForSearch(projA, "quokkaline", "", 5)
	if err != nil || len(hits) != 1 || hits[0].SessionID != "s1" {
		t.Fatalf("timeline = %+v, %v", hits, err)
	}
}

func TestRunSecondRunNeedsForce(t *testing.T) {
	root := buildRoot(t)
	o := options(t, root)
	if _, err := Run(o); err != nil {
		t.Fatal(err)
	}
	st := openStore(t, o.DBPath)
	mark, _, _ := st.Meta("ts_import")
	st.Close()
	sum := func() [32]byte { // cmgo.db after its WAL is checkpointed by the last close
		b, err := os.ReadFile(o.DBPath)
		if err != nil {
			t.Fatal(err)
		}
		return sha256.Sum256(b)
	}
	before := sum()

	if _, err := Run(o); !errors.Is(err, ErrAlreadyImported) {
		t.Fatalf("second Run err = %v, want ErrAlreadyImported", err)
	}
	if sum() != before {
		t.Fatal("cmgo.db changed without --force")
	}

	o.Force = true
	o.Now = testNow.Add(time.Minute)
	got, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	want := Report{Root: root, Sessions: 1, InactiveSessions: 2, NoProjectSessions: 1, DupEvents: 5,
		ExpiredSources: 1, NoProjectSources: 1, ExistingSources: 2}
	if got != want {
		t.Fatalf("forced report = %+v\nwant           %+v", got, want)
	}
	st = openStore(t, o.DBPath)
	if m, _, _ := st.Meta("ts_import"); m == mark || m != o.Now.Format(time.RFC3339)+" 1 0" {
		t.Fatalf("ts_import after --force = %q (was %q)", m, mark)
	}
}

func TestRunDryRunWritesNothing(t *testing.T) {
	root := buildRoot(t)
	o := options(t, root)
	o.DryRun = true
	got, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	want := wantReport
	want.Root = root
	if got != want {
		t.Fatalf("dry-run report = %+v, want %+v", got, want)
	}
	if _, err := os.Stat(o.DBPath); !os.IsNotExist(err) {
		t.Fatalf("dry run created cmgo.db: %v", err)
	}

	// With an existing cmgo.db, the counts see its rows and the file is untouched.
	st := openStore(t, o.DBPath)
	if _, err := st.Index(config.NormalizeProject("/Proj/A"), "fresh", []store.Chunk{{Title: "Mine", Content: "cmgo text"}}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	before := hashTree(t, o.DataDir)
	got, err = Run(o)
	if err != nil {
		t.Fatal(err)
	}
	want.Sources, want.Chunks, want.ExistingSources = 1, 1, 1
	if got != want {
		t.Fatalf("dry-run report = %+v, want %+v", got, want)
	}
	if after := hashTree(t, o.DataDir); !reflect.DeepEqual(before, after) {
		t.Fatal("dry run changed CMGO_DIR")
	}
}

func TestRunMissingRoot(t *testing.T) {
	o := options(t, filepath.Join(t.TempDir(), "nope"))
	if _, err := Run(o); err == nil {
		t.Fatal("Run on a missing root succeeded")
	}
	if _, err := os.Stat(o.DBPath); !os.IsNotExist(err) {
		t.Fatalf("cmgo.db created for a missing root: %v", err)
	}
}

func TestDefaultRoot(t *testing.T) {
	home := t.TempDir()
	claude := filepath.Join(home, "cc")
	cm := filepath.Join(home, "cm")
	env := map[string]string{"CONTEXT_MODE_DIR": cm}
	getenv := func(k string) string { return env[k] }
	mk := func(dir string) {
		if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	steps := []struct {
		create, want string
	}{
		{"", ""},
		{filepath.Join(home, ".claude", "context-mode"), filepath.Join(home, ".claude", "context-mode")},
		{filepath.Join(claude, "context-mode"), filepath.Join(claude, "context-mode")},
		{cm, cm},
	}
	for i, s := range steps {
		if s.create != "" {
			mk(s.create)
		}
		if got := DefaultRoot(getenv, home, claude); got != s.want {
			t.Fatalf("step %d: DefaultRoot = %q, want %q", i, got, s.want)
		}
	}
}

// writeGarbage writes 8 KB of random bytes, which is not a database.
func writeGarbage(t *testing.T, path string) {
	t.Helper()
	b := make([]byte, 8192)
	rand.Read(b)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// An unreadable TS DB is skipped and counted; the rest imports and the mark
// is set (spec §3.2).
func TestRunSkipsUnreadableDBs(t *testing.T) {
	root := buildRoot(t)
	writeGarbage(t, filepath.Join(root, "sessions", "zzzz.db"))
	writeGarbage(t, filepath.Join(root, "content", "zzzz.db"))
	o := options(t, root)
	got, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	want := wantReport
	want.Root, want.UnreadableSessionDBs, want.UnreadableContentDBs = root, 1, 1
	if got != want {
		t.Fatalf("report = %+v\nwant     %+v", got, want)
	}
	st := openStore(t, o.DBPath)
	if _, ok, err := st.Meta("ts_import"); err != nil || !ok {
		t.Fatalf("ts_import mark: %v, %v", ok, err)
	}
}
