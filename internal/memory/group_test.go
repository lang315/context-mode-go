package memory

import (
	"reflect"
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

// evs builds one session's rows from {category, type, data} triples, with
// ids 1, 2, 3, … in order, as store.SessionEvents returns them.
func evs(rows ...[3]string) []store.StoredEvent {
	out := make([]store.StoredEvent, len(rows))
	for i, r := range rows {
		out[i] = store.StoredEvent{ID: int64(i + 1), Category: r[0], Type: r[1], Data: r[2]}
	}
	return out
}

func TestGroupLastPromptIsLastUserPrompt(t *testing.T) {
	g := group(evs(
		[3]string{"user-prompt", "user_prompt", "first"},
		[3]string{"prompt", "prompt", "never written by capture"},
		[3]string{"user-prompt", "user_prompt", "second"},
		[3]string{"decision", "decision", "d"},
	))
	if g.lastPrompt != "second" {
		t.Fatalf("lastPrompt = %q", g.lastPrompt)
	}
	if len(g.cat["user-prompt"]) != 0 || len(g.cat["prompt"]) != 1 || len(g.cat["decision"]) != 1 {
		t.Fatalf("cat = %v", g.cat)
	}
}

func TestGroupFileNames(t *testing.T) {
	g := group(evs(
		[3]string{"file", "file_search", "TODO in /p/src"},
		[3]string{"file", "file_write", `C:\w\f.txt`},
		[3]string{"file", "file_edit", "/a/b/ "},
		[3]string{"file", "file_search", "x in y in /z/w.go"},
		[3]string{"file", "file_glob", "**/*.ts"},
		[3]string{"file", "file_read", "/z/w.go"},
		[3]string{"file", "file_read", "/k/\u00a0s.md\ufeff"},
	))
	want := []string{"src", "f.txt", "w.go", "s.md"}
	if !reflect.DeepEqual(g.files, want) {
		t.Fatalf("files = %q, want %q", g.files, want)
	}
}

func TestGroupLegBoundaryByID(t *testing.T) {
	rows := evs(
		[3]string{"session_start", "session_start", "{}"},
		[3]string{"subagent", "subagent_completed", "old"},
		[3]string{"session_start", "session_start", "{}"},
		[3]string{"subagent", "subagent_launched", "new"},
	)
	// Same created_at everywhere: only the ids order the rows.
	for i := range rows {
		rows[i].CreatedAt = "2026-01-01 00:00:00"
	}
	current, prior := group(rows).subagents()
	if len(current) != 1 || current[0].Data != "new" || len(prior) != 1 || prior[0].Data != "old" {
		t.Fatalf("current %v, prior %v", current, prior)
	}
	// With no session_start, nothing is prior-leg.
	if current, prior := group(rows[1:2]).subagents(); len(current) != 1 || len(prior) != 0 {
		t.Fatalf("first leg: current %v, prior %v", current, prior)
	}
}

func TestTasksMatchByAscendingID(t *testing.T) {
	rows := evs(
		[3]string{"task", "task_create", `{"subject":"A"}`},
		[3]string{"task", "task_create", `{"subject":"B"}`},
		[3]string{"task", "task_create", `{"subject":"C"}`},
		[3]string{"task", "task_create", `{"subject":"D"}`},
		[3]string{"task", "task_create", `{"subject":42}`},
		[3]string{"task", "task_update", `{"taskId":"3","status":"completed"}`},
		[3]string{"task", "task_update", `{"taskId":"1","status":"in_progress"}`},
		[3]string{"task", "task_update", `{"taskId":"10","status":"deleted"}`},
		[3]string{"task", "task_update", `{"taskId":"2"}`},
		[3]string{"task", "task", `{"todos":[]}`},
		[3]string{"task", "task_create", "not json"},
		[3]string{"task", "task_create", "null"},
		[3]string{"task", "task_create", `{"subject":""}`},
	)
	g := group(rows)
	// ids sort as 1, 3, 10: A in_progress, B completed, C deleted, D and 42 unmatched.
	// Rows JSON.parse rejects, and null, are skipped.
	pending, done := g.tasks()
	if !reflect.DeepEqual(pending, []string{"A", "D", "42"}) || !reflect.DeepEqual(done, []string{"B", "C"}) {
		t.Fatalf("pending %q done %q", pending, done)
	}
}
