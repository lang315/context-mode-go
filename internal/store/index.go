package store

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
)

// IndexResult reports one Index call.
type IndexResult struct {
	SourceID                int64
	Label                   string
	TotalChunks, CodeChunks int
	Sections                []Section
}

// Section is one chunk in the "## Indexed Sections" list.
type Section struct {
	Title string
	Bytes int
}

// Source is one row of sources.
type Source struct {
	ID                         int64
	Label                      string
	ChunkCount, CodeChunkCount int
	IndexedAt                  time.Time
}

// optimizeEvery: ContentStore.OPTIMIZE_EVERY (ST:463).
const optimizeEvery = 50

var vocabSplit = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`[^\p{L}\p{N}_-]+`) })

// vocabWords: lowercase, split on [^\p{L}\p{N}_-]+, keep words of at least 3
// chars that are not stopwords; unique, in first-seen order (ST:1675-1681).
func vocabWords(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range vocabSplit().Split(strings.ToLower(text), -1) {
		if u16len(w) >= 3 && !stopwords[w] && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// Index replaces the (project, label) source with chunks in one transaction
// and adds their words to the vocabulary. Port of #insertChunks
// (ST:1085-1143). Vocabulary comes from the chunk contents because the raw
// text is not passed in.
func (s *Store) Index(project, label string, chunks []Chunk) (IndexResult, error) {
	res := IndexResult{Label: label, TotalChunks: len(chunks)}
	for _, c := range chunks {
		if c.HasCode {
			res.CodeChunks++
		}
		res.Sections = append(res.Sections, Section{Title: c.Title, Bytes: len(c.Content)})
	}

	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	for _, q := range []string{
		"DELETE FROM chunks WHERE source_id IN (SELECT id FROM sources WHERE project = ? AND label = ?)",
		"DELETE FROM chunks_trigram WHERE source_id IN (SELECT id FROM sources WHERE project = ? AND label = ?)",
		"DELETE FROM sources WHERE project = ? AND label = ?",
	} {
		if _, err := tx.Exec(q, project, label); err != nil {
			return res, err
		}
	}
	id, added, err := insertSource(tx, project, label, "", chunks, true)
	if err != nil {
		return res, err
	}
	res.SourceID = id
	if err := tx.Commit(); err != nil {
		return res, err
	}

	s.mu.Lock()
	if added > 0 { // new words can change fuzzy answers (ST:1694)
		s.fuzzyKeys, s.fuzzyVals = nil, map[string]string{}
	}
	s.indexCalls++
	optimize := s.indexCalls%optimizeEvery == 0
	s.mu.Unlock()
	if optimize { // best effort, like #optimizeFTS (ST:1661)
		s.db.Exec("INSERT INTO chunks(chunks) VALUES('optimize')")
		s.db.Exec("INSERT INTO chunks_trigram(chunks_trigram) VALUES('optimize')")
	}
	return res, nil
}

// insertSource writes one sources row, its chunks in both FTS tables and,
// when vocab is true, their words, inside tx. indexedAt "" means
// datetime('now'). It returns the source id and the number of new
// vocabulary words.
func insertSource(tx *sql.Tx, project, label, indexedAt string, chunks []Chunk, vocab bool) (int64, int64, error) {
	code := 0
	contents := make([]string, len(chunks))
	for i, c := range chunks {
		if c.HasCode {
			code++
		}
		contents[i] = c.Content
	}
	r, err := tx.Exec("INSERT INTO sources (project, label, chunk_count, code_chunk_count, indexed_at) VALUES (?, ?, ?, ?, COALESCE(NULLIF(?, ''), datetime('now')))",
		project, label, len(chunks), code, indexedAt)
	if err != nil {
		return 0, 0, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return 0, 0, err
	}
	for _, table := range []string{"chunks", "chunks_trigram"} {
		stmt, err := tx.Prepare("INSERT INTO " + table + " (title, content, source_id, content_type) VALUES (?, ?, ?, ?)")
		if err != nil {
			return 0, 0, err
		}
		for _, c := range chunks {
			ct := Prose
			if c.HasCode {
				ct = Code
			}
			if _, err := stmt.Exec(c.Title, c.Content, id, string(ct)); err != nil {
				stmt.Close()
				return 0, 0, err
			}
		}
		stmt.Close()
	}
	var added int64
	if !vocab {
		return id, 0, nil
	}
	if words := vocabWords(strings.Join(contents, "\n")); len(words) > 0 {
		stmt, err := tx.Prepare("INSERT OR IGNORE INTO vocabulary (word) VALUES (?)")
		if err != nil {
			return 0, 0, err
		}
		for _, w := range words {
			r, err := stmt.Exec(w)
			if err != nil {
				stmt.Close()
				return 0, 0, err
			}
			n, _ := r.RowsAffected()
			added += n
		}
		stmt.Close()
	}
	return id, added, nil
}

// SourceByLabel returns the (project, label) source, or nil if missing.
func (s *Store) SourceByLabel(project, label string) (*Source, error) {
	var src Source
	var at string
	err := s.db.QueryRow("SELECT id, label, chunk_count, code_chunk_count, indexed_at FROM sources WHERE project = ? AND label = ?",
		project, label).Scan(&src.ID, &src.Label, &src.ChunkCount, &src.CodeChunkCount, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	src.IndexedAt, err = time.Parse(sqliteTime, at)
	return &src, err
}

// ChunkCount is the number of chunks indexed for project.
func (s *Store) ChunkCount(project string) (int, error) {
	var n int
	err := s.db.QueryRow("SELECT COALESCE(SUM(chunk_count), 0) FROM sources WHERE project = ?", project).Scan(&n)
	return n, err
}
