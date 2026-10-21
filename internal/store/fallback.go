package store

import (
	"cmp"
	"slices"
	"strings"
	"unicode/utf16"
)

// fuzzyCacheSize: ContentStore.FUZZY_CACHE_SIZE (ST:471).
const fuzzyCacheSize = 256

// defaultSearchLimit: searchWithFallback's limit default (ST:1395).
const defaultSearchLimit = 3

// SearchWithFallback is the ctx_search entry point: RRF over porter +
// trigram, proximity rerank, then one fuzzy-corrected retry when nothing
// matched (ST:1393-1442). Timeline mode (spec §3.2) ranks up to
// timelineCandidates matches, keeps the newest o.Limit by indexed_at (ties:
// the later chunk), and returns them oldest first. TS kept the best ranked
// ones (src/search/unified.ts:104-187).
func (s *Store) SearchWithFallback(query string, o SearchOpts) ([]SearchResult, error) {
	if o.Limit <= 0 {
		o.Limit = defaultSearchLimit
	}
	if o.Mode != "timeline" {
		res, err := s.relevance(query, o)
		if err != nil {
			return nil, err
		}
		return res[:min(len(res), o.Limit)], nil
	}
	pool := o
	pool.Limit = max(o.Limit, timelineCandidates)
	res, err := s.relevance(query, pool)
	if err != nil {
		return nil, err
	}
	newest := slices.Clone(res)
	slices.SortStableFunc(newest, func(a, b SearchResult) int {
		if c := b.IndexedAt.Compare(a.IndexedAt); c != 0 {
			return c
		}
		return cmp.Compare(b.id, a.id)
	})
	keep := map[int64]bool{}
	for _, r := range newest[:min(len(newest), o.Limit)] {
		keep[r.id] = true
	}
	res = slices.DeleteFunc(res, func(r SearchResult) bool { return !keep[r.id] })
	// Stable, so equal times keep relevance order.
	slices.SortStableFunc(res, func(a, b SearchResult) int { return a.IndexedAt.Compare(b.IndexedAt) })
	return res, nil
}

// timelineCandidates is how many ranked matches timeline mode considers
// before it keeps the newest ones.
const timelineCandidates = 200

func (s *Store) relevance(query string, o SearchOpts) ([]SearchResult, error) {
	res, err := s.rrfSearch(query, o.Limit, o)
	if err != nil {
		return nil, err
	}
	if len(res) > 0 {
		return rerank(res, query), nil
	}

	var words, corrected []string
	for _, w := range jsFields(strings.ToLower(jsTrim(query))) {
		if u16len(w) >= 3 && !stopwords[w] {
			words = append(words, w)
		}
	}
	for _, w := range words {
		c, err := s.fuzzyCorrect(w)
		if err != nil {
			return nil, err
		}
		if c == "" {
			c = w
		}
		corrected = append(corrected, c)
	}
	cq := strings.Join(corrected, " ")
	if cq == strings.Join(words, " ") {
		return nil, nil
	}
	res, err = s.rrfSearch(cq, o.Limit, o)
	if err != nil || len(res) == 0 {
		return nil, err
	}
	return rerank(res, cq), nil
}

// fuzzyCorrect returns the closest vocabulary word within the edit-distance
// budget, or "" for none / exact match. LRU-cached (ST:1248-1293).
func (s *Store) fuzzyCorrect(query string) (string, error) {
	word := strings.ToLower(jsTrim(query))
	n := u16len(word)
	if n < 3 {
		return "", nil
	}
	s.mu.Lock()
	if v, ok := s.fuzzyVals[word]; ok {
		i := slices.Index(s.fuzzyKeys, word)
		s.fuzzyKeys = append(slices.Delete(s.fuzzyKeys, i, i+1), word)
		s.mu.Unlock()
		return v, nil
	}
	s.mu.Unlock()

	maxDist := maxEditDistance(n)
	rows, err := s.db.Query("SELECT word FROM vocabulary WHERE length(word) BETWEEN ? AND ?", n-maxDist, n+maxDist)
	if err != nil {
		return "", err
	}
	var candidates []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return "", err
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}

	best, bestDist, exact := "", maxDist+1, false
	w16 := utf16.Encode([]rune(word))
	for _, c := range candidates {
		if c == word {
			exact = true
			break
		}
		if d := levenshtein(w16, utf16.Encode([]rune(c))); d < bestDist {
			best, bestDist = c, d
		}
	}
	result := ""
	if !exact && bestDist <= maxDist {
		result = best
	}

	s.mu.Lock()
	if _, ok := s.fuzzyVals[word]; !ok {
		if len(s.fuzzyKeys) >= fuzzyCacheSize {
			delete(s.fuzzyVals, s.fuzzyKeys[0])
			s.fuzzyKeys = s.fuzzyKeys[1:]
		}
		s.fuzzyKeys = append(s.fuzzyKeys, word)
	}
	s.fuzzyVals[word] = result
	s.mu.Unlock()
	return result, nil
}

// maxEditDistance: ST:144-148.
func maxEditDistance(n int) int {
	switch {
	case n <= 4:
		return 1
	case n <= 12:
		return 2
	}
	return 3
}

// levenshtein over UTF-16 code units (ST:127-142).
func levenshtein(a, b []uint16) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	for i := range prev {
		prev[i] = i
	}
	for i := 1; i <= len(a); i++ {
		curr := make([]int, len(b)+1)
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				curr[j] = prev[j-1]
			} else {
				curr[j] = 1 + min(prev[j], curr[j-1], prev[j-1])
			}
		}
		prev = curr
	}
	return prev[len(b)]
}
