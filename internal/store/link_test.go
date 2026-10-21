package store

import (
	"testing"
	"time"
)

func promptEvent(data string) []Event {
	return []Event{{Type: "user_prompt", Category: "user-prompt", Priority: 1, Data: data}}
}

// A session linked late adopts the rows its Claude Code process's servers
// wrote before the link, with an empty window. Rows of another process or
// project stay where they are.
func TestInsertEventsAndLinkAdoptsUnlinkedRows(t *testing.T) {
	s := openTest(t)
	for _, r := range []struct {
		server, project string
		cc              int
	}{{"srv", "/p", 4242}, {"other", "/p", 777}, {"elsewhere", "/q", 4242}} {
		if err := s.AddToolStat(r.server, r.cc, r.project, "2026-10-21 08:00:00", "ctx_execute", 100, 40); err != nil {
			t.Fatal(err)
		}
	}
	// A server of an earlier Claude Code process that had the same pid
	// started before this process did.
	if err := s.AddToolStat("reused", 4242, "/p", "2026-10-20 07:59:59", "ctx_execute", 100, 40); err != nil {
		t.Fatal(err)
	}
	claudeStart := time.Date(2026, 10, 21, 7, 59, 59, 500e6, time.UTC)
	if _, err := s.InsertEventsAndLink("s1", "/p", "UserPromptSubmit", promptEvent("hi"), func() (int, time.Time) { return 4242, claudeStart }); err != nil {
		t.Fatal(err)
	}
	for server, want := range map[string]string{"srv": "s1", "other": "", "elsewhere": "", "reused": ""} {
		var window string
		var calls, returned int
		if err := s.db.QueryRow("SELECT window, calls, bytes_returned FROM tool_stats WHERE server_id = ?", server).Scan(&window, &calls, &returned); err != nil {
			t.Fatal(err)
		}
		if window != want || calls != 1 || returned != 100 {
			t.Errorf("%s: window %q calls %d returned %d, want window %q calls 1 returned 100", server, window, calls, returned, want)
		}
	}
}

// The process walk runs once per session, also when it finds no Claude Code
// ancestor; cc_pid then stays 0 (unknown).
func TestInsertEventsAndLinkWalksOnce(t *testing.T) {
	s := openTest(t)
	walks := 0
	walk := func() (int, time.Time) { walks++; return 0, time.Time{} }
	for _, data := range []string{"one", "two"} {
		if _, err := s.InsertEventsAndLink("s1", "/p", "UserPromptSubmit", promptEvent(data), walk); err != nil {
			t.Fatal(err)
		}
	}
	if walks != 1 {
		t.Fatalf("walks = %d, want 1", walks)
	}
	if n := tableCount(t, s, "SELECT cc_pid FROM session_meta WHERE session_id = 's1'"); n != 0 {
		t.Fatalf("cc_pid = %d, want 0", n)
	}
}
