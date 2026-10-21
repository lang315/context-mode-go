package tsimport

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

// copyAndOpen copies src (with -wal/-shm) into a fresh dir and opens the copy.
func copyAndOpen(t *testing.T, src string) *tsCopy {
	t.Helper()
	path, err := copyDB(src, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := openCopy(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestReadSessionsCurrentSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions", "0123456789abcdef.db")
	db := tsDB(t, path, sessionDDL)
	addMeta(t, db, "s1", "/Users/me/Proj", "2026-10-20 09:00:00", "2026-10-20 10:00:00", 2)
	addEvent(t, db, "s1", "file_read", "/a.go", "/Users/me/Proj", "2026-10-20 09:30:00")
	addEvent(t, db, "s2", "file_read", "/b.go", "/b", "2026-10-19 08:00:00")
	addEvent(t, db, "s2", "file_edit", "/b.go", "/c", "2026-10-19 08:00:01")
	addEvent(t, db, "s1", "file_edit", "/a.go", "", "2026-10-20 11:00:00") // newer than meta.last_event_at
	addEvent(t, db, "s2", "cwd", "/b", "/b", "2026-10-19 08:00:02")
	mustExec(t, db, "INSERT INTO session_meta(session_id, project_dir) VALUES('s3', '/no-events')")

	got, err := copyAndOpen(t, path).sessions()
	if err != nil {
		t.Fatal(err)
	}
	ev := func(typ, data, at string) store.ImportedEvent {
		return store.ImportedEvent{Type: typ, Category: "file", Priority: 1, Data: data, SourceHook: "PostToolUse", BytesAvoided: 7, CreatedAt: at}
	}
	want := []tsSession{
		{id: "s1", projectDir: "/Users/me/Proj", startedAt: "2026-10-20 09:00:00", lastEventAt: "2026-10-20 11:00:00", compact: 2,
			events: []store.ImportedEvent{ev("file_read", "/a.go", "2026-10-20 09:30:00"), ev("file_edit", "/a.go", "2026-10-20 11:00:00")}},
		{id: "s2", projectDir: "/b", startedAt: "2026-10-19 08:00:00", lastEventAt: "2026-10-19 08:00:02",
			events: []store.ImportedEvent{ev("file_read", "/b.go", "2026-10-19 08:00:00"), ev("file_edit", "/b.go", "2026-10-19 08:00:01"), ev("cwd", "/b", "2026-10-19 08:00:02")}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sessions =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadSessionsOldSchemaDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db := tsDB(t, path, sessionDDLOld)
	addMeta(t, db, "s1", "/p", "2026-10-20 09:00:00", "", 0)
	mustExec(t, db, "INSERT INTO session_events(session_id, type, category, data, source_hook, created_at) VALUES('s1', 'cwd', 'cwd', '/p', 'PostToolUse', '2026-10-20 09:01:00')")

	got, err := copyAndOpen(t, path).sessions()
	if err != nil {
		t.Fatal(err)
	}
	want := []tsSession{{id: "s1", projectDir: "/p", startedAt: "2026-10-20 09:00:00", lastEventAt: "2026-10-20 09:01:00",
		events: []store.ImportedEvent{{Type: "cwd", Category: "cwd", Priority: 2, Data: "/p", SourceHook: "PostToolUse", CreatedAt: "2026-10-20 09:01:00"}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sessions = %+v, want %+v", got, want)
	}
}

func TestReadSessionsIncludesWALFrames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.db")
	db := tsDB(t, path, sessionDDL)
	addEvent(t, db, "s1", "cwd", "/p", "/p", "2026-10-20 09:00:00")
	checkpoint(t, db)
	addEvent(t, db, "s1", "file_read", "/in-wal.go", "/p", "2026-10-20 09:00:01")
	if fi, err := os.Stat(path + "-wal"); err != nil || fi.Size() == 0 {
		t.Fatalf("want uncheckpointed frames in -wal: %v, %v", fi, err)
	}

	got, err := copyAndOpen(t, path).sessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].events) != 2 || got[0].events[1].Data != "/in-wal.go" {
		t.Fatalf("sessions = %+v, want the WAL row too", got)
	}
}

func TestReadSourcesBothFTSSchemas(t *testing.T) {
	for name, ddl := range map[string]string{"8-column": contentDDL, "4-column": contentDDLOld} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "content.db")
			db := tsDB(t, path, ddl)
			addSource(t, db, "docs::https://x", "2026-10-15 08:00:00",
				[3]string{"Intro", "pooling basics", "prose"}, [3]string{"Dial", "func Dial() {}", "code"})
			addSource(t, db, "empty", "2026-10-16 08:00:00")

			got, err := copyAndOpen(t, path).sources()
			if err != nil {
				t.Fatal(err)
			}
			want := []tsSource{
				{label: "docs::https://x", indexedAt: "2026-10-15 08:00:00",
					chunks: []store.Chunk{{Title: "Intro", Content: "pooling basics"}, {Title: "Dial", Content: "func Dial() {}", HasCode: true}}},
				{label: "empty", indexedAt: "2026-10-16 08:00:00"},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("sources = %+v, want %+v", got, want)
			}
		})
	}
}
