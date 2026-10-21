package store

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf16"
)

// SearchOpts scopes a search. Project is always applied.
type SearchOpts struct {
	Project     string
	Source      string      // "" = all
	SourceExact bool        // true: label = ?, false: label LIKE %x% ESCAPE '\'
	ContentType ContentType // "" = any
	Limit       int
	Mode        string // "relevance" | "timeline"
}

// SearchResult is one hit.
type SearchResult struct {
	Title, Content, Source string
	ContentType            ContentType
	Highlighted            string // content with \x02/\x03 markers, "" if none
	Rank                   float64
	IndexedAt              time.Time
	id                     int64 // chunk rowid; breaks indexed_at ties in timeline mode
}

// rrfK: standard RRF constant (ST:1304).
const rrfK = 60

var (
	porterStrip  = regexp.MustCompile(`['"(){}\[\]*:^~]`)
	trigramStrip = regexp.MustCompile(`["'(){}\[\]*:^~]`)
)

func jsFields(s string) []string { return strings.FieldsFunc(s, isJSSpace) }

// dedupeTokens keeps the first spelling of case-insensitive duplicates (ST:77).
func dedupeTokens(tokens []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tokens {
		if k := strings.ToLower(t); !seen[k] {
			seen[k] = true
			out = append(out, t)
		}
	}
	return out
}

// quoteOr drops stopwords (unless all are stopwords) and joins quoted terms
// with " OR ".
func quoteOr(words []string) string {
	var meaningful []string
	for _, w := range words {
		if !stopwords[strings.ToLower(w)] {
			meaningful = append(meaningful, w)
		}
	}
	if len(meaningful) == 0 {
		meaningful = words
	}
	for i, w := range meaningful {
		meaningful[i] = `"` + w + `"`
	}
	return strings.Join(meaningful, " OR ")
}

// SanitizeQuery builds the porter MATCH expression, OR mode (ST:90-111).
func SanitizeQuery(q string) string {
	q = strings.ReplaceAll(q, "\x00", "") // a NUL ends the MATCH string in SQLite
	var words []string
	for _, w := range jsFields(porterStrip.ReplaceAllString(q, " ")) {
		switch strings.ToUpper(w) {
		case "AND", "OR", "NOT", "NEAR":
			continue
		}
		words = append(words, w)
	}
	words = dedupeTokens(words)
	if len(words) == 0 {
		return `""`
	}
	return quoteOr(words)
}

// SanitizeTrigramQuery builds the trigram MATCH expression, OR mode; "" means
// skip the trigram search (ST:113-125).
func SanitizeTrigramQuery(q string) string {
	q = strings.ReplaceAll(q, "\x00", "")
	cleaned := jsTrim(trigramStrip.ReplaceAllString(q, ""))
	if u16len(cleaned) < 3 {
		return ""
	}
	var words []string
	for _, w := range jsFields(cleaned) {
		if u16len(w) >= 3 {
			words = append(words, w)
		}
	}
	words = dedupeTokens(words)
	if len(words) == 0 {
		return ""
	}
	return quoteOr(words)
}

// likeParam escapes LIKE metacharacters for ESCAPE '\' (ST:1160-1173).
func likeParam(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

// scopeSQL is the shared WHERE tail and its args (after the MATCH arg).
func scopeSQL(table string, o SearchOpts) (string, []any) {
	where := " AND sources.project = ?"
	args := []any{o.Project}
	if o.Source != "" {
		if o.SourceExact {
			where += " AND sources.label = ?"
			args = append(args, o.Source)
		} else {
			where += ` AND sources.label LIKE ? ESCAPE '\'`
			args = append(args, likeParam(o.Source))
		}
	}
	if o.ContentType != "" {
		where += " AND " + table + ".content_type = ?"
		args = append(args, string(o.ContentType))
	}
	return where, args
}

// ftsSearch runs one FTS table query (ST:638-831 plus the project filter).
func (s *Store) ftsSearch(table, match string, limit int, o SearchOpts) ([]SearchResult, error) {
	where, args := scopeSQL(table, o)
	q := "SELECT " + table + ".title, " + table + ".content, " + table + ".content_type, sources.label, " +
		"bm25(" + table + ", 5.0, 1.0) AS rank, highlight(" + table + ", 1, char(2), char(3)), sources.indexed_at, " + table + ".rowid " +
		"FROM " + table + " JOIN sources ON sources.id = " + table + ".source_id " +
		"WHERE " + table + " MATCH ?" + where + " ORDER BY rank LIMIT ?"
	args = append(append([]any{match}, args...), limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchResult
	for rows.Next() {
		var r SearchResult
		var ct, at string
		if err := rows.Scan(&r.Title, &r.Content, &ct, &r.Source, &r.Rank, &r.Highlighted, &at, &r.id); err != nil {
			return nil, err
		}
		r.ContentType = ContentType(ct)
		if !strings.Contains(r.Highlighted, "\x02") {
			r.Highlighted = ""
		}
		r.IndexedAt, _ = time.Parse(sqliteTime, at)
		out = append(out, r)
	}
	return out, rows.Err()
}

// rrfSearch fuses porter and trigram hits by reciprocal rank, keyed by
// source::title; Rank = -score (ST:1297-1337).
func (s *Store) rrfSearch(query string, limit int, o SearchOpts) ([]SearchResult, error) {
	fetch := max(limit*2, 10)
	porter, err := s.ftsSearch("chunks", SanitizeQuery(query), fetch, o)
	if err != nil {
		return nil, err
	}
	var trigram []SearchResult
	if tq := SanitizeTrigramQuery(query); tq != "" {
		if trigram, err = s.ftsSearch("chunks_trigram", tq, fetch, o); err != nil {
			return nil, err
		}
	}
	type scored struct {
		r     SearchResult
		score float64
	}
	var order []*scored // insertion order: porter first, then trigram
	byKey := map[string]*scored{}
	for _, list := range [][]SearchResult{porter, trigram} {
		for i, r := range list {
			k := r.Source + "::" + r.Title
			if e := byKey[k]; e != nil {
				e.score += 1 / float64(rrfK+i+1)
				continue
			}
			e := &scored{r, 1 / float64(rrfK+i+1)}
			byKey[k] = e
			order = append(order, e)
		}
	}
	slices.SortStableFunc(order, func(a, b *scored) int { return cmp.Compare(b.score, a.score) })
	out := make([]SearchResult, 0, min(limit, len(order)))
	for _, e := range order[:min(limit, len(order))] {
		e.r.Rank = -e.score
		out = append(out, e.r)
	}
	return out, nil
}

// rerank applies title, proximity and phrase boosts; ties keep RRF order
// (ST:1341-1389). Offsets are UTF-16 code units, as in JS.
func rerank(results []SearchResult, query string) []SearchResult {
	var all, filtered []string
	for _, w := range jsFields(strings.ToLower(query)) {
		if u16len(w) >= 2 {
			all = append(all, w)
			if !stopwords[w] {
				filtered = append(filtered, w)
			}
		}
	}
	terms := filtered
	if len(terms) == 0 {
		terms = all
	}
	type boosted struct {
		r     SearchResult
		boost float64
	}
	bs := make([]boosted, len(results))
	for i, r := range results {
		titleLower := strings.ToLower(r.Title)
		hits := 0
		for _, t := range terms {
			if strings.Contains(titleLower, t) {
				hits++
			}
		}
		weight := 0.3
		if r.ContentType == Code {
			weight = 0.6
		}
		titleBoost := 0.0
		if hits > 0 {
			titleBoost = weight * (float64(hits) / float64(len(terms)))
		}
		proximity, phrase := 0.0, 0.0
		if len(terms) >= 2 {
			content := utf16.Encode([]rune(strings.ToLower(r.Content)))
			positions := make([][]int, len(terms))
			missing := false
			for j, t := range terms {
				positions[j] = findAllPositions(content, utf16.Encode([]rune(t)))
				missing = missing || len(positions[j]) == 0
			}
			if !missing {
				minSpan := findMinSpan(positions)
				proximity = 1 / (1 + float64(minSpan)/float64(max(len(content), 1)))
				phrase = 0.5 * min(1, float64(countAdjacentPairs(positions, terms, 30))/4)
			}
		}
		bs[i] = boosted{r, titleBoost + proximity + phrase}
	}
	slices.SortStableFunc(bs, func(a, b boosted) int {
		if c := cmp.Compare(b.boost, a.boost); c != 0 {
			return c
		}
		return cmp.Compare(a.r.Rank, b.r.Rank)
	})
	out := make([]SearchResult, len(bs))
	for i, b := range bs {
		out[i] = b.r
	}
	return out
}

// findAllPositions: overlapping indexOf matches (ST:319-327).
func findAllPositions(text, term []uint16) []int {
	var out []int
	for i := 0; i+len(term) <= len(text); i++ {
		if slices.Equal(text[i:i+len(term)], term) {
			out = append(out, i)
		}
	}
	return out
}

// countAdjacentPairs: ST:340-364.
func countAdjacentPairs(lists [][]int, terms []string, gap int) int {
	if len(lists) < 2 || len(terms) < 2 {
		return 0
	}
	total := 0
	for i := 0; i < min(len(lists), len(terms))-1; i++ {
		left, right := lists[i], lists[i+1]
		leftLen := u16len(terms[i])
		j := 0
		for _, p := range left {
			minStart := p + leftLen
			for j < len(right) && right[j] < minStart {
				j++
			}
			if j < len(right) && right[j] <= minStart+gap {
				total++
				j++
			}
		}
	}
	return total
}

// findMinSpan: smallest window holding one position from each list
// (ST:370-402). Lists are non-empty and sorted.
func findMinSpan(lists [][]int) int {
	if len(lists) == 1 {
		return 0
	}
	ptrs := make([]int, len(lists))
	best := -1
	for {
		curMin, curMax, minIdx := lists[0][ptrs[0]], lists[0][ptrs[0]], 0
		for i := 1; i < len(lists); i++ {
			v := lists[i][ptrs[i]]
			if v < curMin {
				curMin, minIdx = v, i
			}
			if v > curMax {
				curMax = v
			}
		}
		if span := curMax - curMin; best < 0 || span < best {
			best = span
		}
		ptrs[minIdx]++
		if ptrs[minIdx] >= len(lists[minIdx]) {
			return best
		}
	}
}
