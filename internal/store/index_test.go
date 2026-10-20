package store

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestVocabWords(t *testing.T) {
	got := vocabWords("The update_fn and my-var: Ünïcode ab, abc ABC fix 日本語")
	want := []string{"update_fn", "my-var", "ünïcode", "abc", "日本語"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("vocabWords = %q, want %q", got, want)
	}
}

func TestIndexReplaceAndScope(t *testing.T) {
	s := openTest(t)
	chunks := []Chunk{
		{Title: "Intro", Content: "connection pooling basics"},
		{Title: "Code", Content: "```go\nfunc Dial() {}\n```", HasCode: true},
	}
	res, err := s.Index("p1", "docs", chunks)
	if err != nil {
		t.Fatal(err)
	}
	want := IndexResult{SourceID: res.SourceID, Label: "docs", TotalChunks: 2, CodeChunks: 1,
		Sections: []Section{{"Intro", 25}, {"Code", 24}}}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("Index = %+v, want %+v", res, want)
	}
	if _, err := s.Index("p2", "docs", chunks[:1]); err != nil {
		t.Fatal(err)
	}

	// Re-index same (project, label): old chunks are gone, new id.
	res2, err := s.Index("p1", "docs", []Chunk{{Title: "New", Content: "replacement text"}})
	if err != nil {
		t.Fatal(err)
	}
	if res2.SourceID == res.SourceID {
		t.Fatal("re-index kept old source id")
	}
	var n, tri int
	s.db.QueryRow("SELECT count(*) FROM chunks WHERE source_id = ?", res.SourceID).Scan(&n)
	s.db.QueryRow("SELECT count(*) FROM chunks_trigram WHERE source_id = ?", res.SourceID).Scan(&tri)
	if n != 0 || tri != 0 {
		t.Fatalf("stale chunks left: %d %d", n, tri)
	}

	src, err := s.SourceByLabel("p1", "docs")
	if err != nil || src == nil {
		t.Fatalf("SourceByLabel: %v %v", src, err)
	}
	if src.ID != res2.SourceID || src.ChunkCount != 1 || src.CodeChunkCount != 0 || time.Since(src.IndexedAt) > time.Minute {
		t.Fatalf("source = %+v", src)
	}
	if src, err := s.SourceByLabel("p1", "missing"); src != nil || err != nil {
		t.Fatalf("missing source = %v %v", src, err)
	}
	if c, _ := s.ChunkCount("p1"); c != 1 {
		t.Fatalf("ChunkCount(p1) = %d", c)
	}
	if c, _ := s.ChunkCount("p2"); c != 1 {
		t.Fatalf("ChunkCount(p2) = %d", c)
	}

	// Empty chunk list still records the source (0/0).
	if r, err := s.Index("p1", "empty", nil); err != nil || r.TotalChunks != 0 {
		t.Fatalf("empty index: %+v %v", r, err)
	}
	if src, _ := s.SourceByLabel("p1", "empty"); src == nil || src.ChunkCount != 0 {
		t.Fatalf("empty source = %+v", src)
	}
}

func TestIndexVocabularyAndFuzzyCacheReset(t *testing.T) {
	s := openTest(t)
	s.fuzzyKeys, s.fuzzyVals = []string{"x"}, map[string]string{"x": ""}
	if _, err := s.Index("p", "a", []Chunk{{Title: "t", Content: "Kubernetes the deployment"}}); err != nil {
		t.Fatal(err)
	}
	var words []string
	rows, _ := s.db.Query("SELECT word FROM vocabulary ORDER BY word")
	for rows.Next() {
		var w string
		rows.Scan(&w)
		words = append(words, w)
	}
	rows.Close()
	if !reflect.DeepEqual(words, []string{"deployment", "kubernetes"}) {
		t.Fatalf("vocabulary = %q", words)
	}
	if len(s.fuzzyVals) != 0 {
		t.Fatal("fuzzy cache not cleared after new words")
	}
	// No new words: cache kept.
	s.fuzzyKeys, s.fuzzyVals = []string{"x"}, map[string]string{"x": ""}
	s.Index("p", "b", []Chunk{{Title: "t", Content: "deployment"}})
	if len(s.fuzzyVals) != 1 {
		t.Fatal("fuzzy cache cleared without new words")
	}
}

// Two Stores (two processes in production) writing the same file at once.
func TestConcurrentStoresSameFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 202)
	for w, st := range []*Store{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 50 {
				label := fmt.Sprintf("w%d-%d", w, i)
				if _, err := st.Index("p", label, ChunkMarkdown("# "+label+"\nbody text "+label, ChunkMaxBytes)); err != nil {
					errs <- err
				}
				if _, err := st.Index("p", "shared", []Chunk{{Title: "s", Content: fmt.Sprintf("shared %d %d", w, i)}}); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if c, err := a.ChunkCount("p"); err != nil || c != 101 {
		t.Fatalf("ChunkCount = %d, %v", c, err)
	}
	var n int
	b.db.QueryRow("SELECT count(*) FROM sources WHERE label = 'shared'").Scan(&n)
	if n != 1 {
		t.Fatalf("shared sources = %d", n)
	}
	b.db.QueryRow("SELECT count(*) FROM chunks").Scan(&n)
	if n != 101 {
		t.Fatalf("chunks rows = %d", n)
	}
}
