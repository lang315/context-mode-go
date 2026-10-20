package store

import (
	"cmp"
	"database/sql"
	"errors"
	"math"
	"slices"
	"strings"
)

// DistinctiveTerms returns up to maxTerms words that appear in 2..max(3, 40%)
// chunks of the source, ranked by IDF + length + identifier bonus; nil for
// sources with fewer than 3 chunks. Port of getDistinctiveTerms (ST:1573-1617).
func (s *Store) DistinctiveTerms(sourceID int64, maxTerms int) ([]string, error) {
	var total int
	err := s.db.QueryRow("SELECT chunk_count FROM sources WHERE id = ?", sourceID).Scan(&total)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && total < 3) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	maxApp := int(math.Max(3, math.Ceil(float64(total)*0.4)))

	rows, err := s.db.Query("SELECT content FROM chunks WHERE source_id = ? ORDER BY rowid", sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var order []string // first-seen order, like a JS Map
	freq := map[string]int{}
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return nil, err
		}
		for _, w := range vocabWords(content) {
			if freq[w] == 0 {
				order = append(order, w)
			}
			freq[w]++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	type scored struct {
		word  string
		score float64
	}
	var list []scored
	for _, w := range order {
		n := freq[w]
		if n < 2 || n > maxApp {
			continue
		}
		bonus := 0.0
		if strings.Contains(w, "_") {
			bonus = 1.5
		} else if u16len(w) >= 12 {
			bonus = 0.8
		}
		list = append(list, scored{w, math.Log(float64(total)/float64(n)) + math.Min(float64(u16len(w))/20, 0.5) + bonus})
	}
	slices.SortStableFunc(list, func(a, b scored) int { return cmp.Compare(b.score, a.score) })
	out := []string{}
	for _, e := range list[:min(maxTerms, len(list))] {
		out = append(out, e.word)
	}
	return out, nil
}
