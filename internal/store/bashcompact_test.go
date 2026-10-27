package store

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIndexBashOutputSkipsVocabularyAndCountsTheCall(t *testing.T) {
	s := openTest(t)
	vocab := func() (n int) {
		s.db.QueryRow("SELECT count(*) FROM vocabulary").Scan(&n)
		return
	}
	before := vocab()
	chunks := ChunkPlainText("alpha\nzebramarker 42\nomega\n", 20)
	if err := s.IndexBashOutput("/p", "bash:t1", "s1", chunks, 2000, 15000); err != nil {
		t.Fatal(err)
	}
	if err := s.IndexBashOutput("/p", "bash:t2", "s1", chunks, 1000, 25000); err != nil {
		t.Fatal(err)
	}
	if got := vocab(); got != before {
		t.Errorf("vocabulary %d -> %d, want unchanged", before, got)
	}
	hits, err := s.SearchWithFallback("zebramarker", SearchOpts{Project: "/p", Source: "bash:t1", SourceExact: true})
	if err != nil || len(hits) == 0 || !strings.Contains(hits[0].Content, "zebramarker 42") {
		t.Fatalf("search = %+v, %v", hits, err)
	}
	rows, err := s.toolStats("window = ?", "s1")
	want := []ToolStat{{Tool: BashCompactTool, Calls: 2, BytesReturned: 3000, BytesKept: 40000}}
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatalf("tool_stats = %+v, %v; want %+v", rows, err, want)
	}
	var calls, ret, kept int64
	if err := s.db.QueryRow("SELECT calls, bytes_returned, bytes_kept FROM stats_totals WHERE project = '/p'").Scan(&calls, &ret, &kept); err != nil ||
		calls != 2 || ret != 3000 || kept != 40000 {
		t.Fatalf("stats_totals = %d %d %d, %v", calls, ret, kept, err)
	}
}

func TestCleanupDropsBashSourcesAfter48h(t *testing.T) {
	s := openTest(t)
	now := time.Now()
	chunks := ChunkPlainText("x\n", 20)
	for _, l := range []string{"bash:old", "bash:new"} {
		if err := s.IndexBashOutput("/p", l, "s1", chunks, 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Index("/p", "doc:x", chunks); err != nil {
		t.Fatal(err)
	}
	for label, age := range map[string]time.Duration{"bash:old": 49 * time.Hour, "bash:new": 47 * time.Hour, "doc:x": 72 * time.Hour} {
		if _, err := s.db.Exec("UPDATE sources SET indexed_at = ? WHERE label = ?", now.Add(-age).UTC().Format(sqliteTime), label); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.CleanupIfDue(now, 14*24*time.Hour, 24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("CleanupIfDue = %d, %v; want 1", n, err)
	}
	var labels []string
	r, err := s.db.Query("SELECT label FROM sources ORDER BY label")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for r.Next() {
		var l string
		r.Scan(&l)
		labels = append(labels, l)
	}
	if want := []string{"bash:new", "doc:x"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("sources = %v, want %v", labels, want)
	}
	var orphans int
	s.db.QueryRow("SELECT count(*) FROM chunks WHERE source_id NOT IN (SELECT id FROM sources)").Scan(&orphans)
	if orphans != 0 {
		t.Errorf("%d chunks left without a source", orphans)
	}
}
