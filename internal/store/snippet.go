package store

import (
	"slices"
	"strings"
	"unicode/utf16"
)

// snippetWindow: chars kept on each side of a match (S:1341).
const snippetWindow = 300

// ExtractSnippet returns content whole when it fits in maxLen, else ±300-char
// windows around matches joined by "\n\n" with "…" at cut edges. Matches come
// from the \x02/\x03 markers in highlighted, else from indexOf on query terms
// longer than 2 chars. Port of extractSnippet (S:1270-1367); offsets and
// lengths are UTF-16 code units, as in JS.
func ExtractSnippet(content, query string, maxLen int, highlighted string) string {
	c := utf16.Encode([]rune(content))
	if len(c) <= maxLen {
		return content
	}
	var positions []int
	if highlighted != "" {
		clean := 0
		h := utf16.Encode([]rune(highlighted))
		for i := 0; i < len(h); {
			if h[i] != 0x02 {
				clean++
				i++
				continue
			}
			positions = append(positions, clean)
			i++
			for i < len(h) && h[i] != 0x03 {
				clean++
				i++
			}
			if i < len(h) {
				i++
			}
		}
	}
	if len(positions) == 0 {
		lower := utf16.Encode([]rune(strings.ToLower(content)))
		for _, t := range jsFields(strings.ToLower(query)) {
			if u16len(t) > 2 {
				positions = append(positions, findAllPositions(lower, utf16.Encode([]rune(t)))...)
			}
		}
	}
	if len(positions) == 0 {
		return string(utf16.Decode(c[:maxLen])) + "\n…"
	}

	slices.Sort(positions)
	var windows [][2]int
	for _, p := range positions {
		start, end := max(0, p-snippetWindow), min(len(c), p+snippetWindow)
		if n := len(windows); n > 0 && start <= windows[n-1][1] {
			windows[n-1][1] = end
		} else {
			windows = append(windows, [2]int{start, end})
		}
	}
	var parts []string
	total := 0
	for _, w := range windows {
		if total >= maxLen {
			break
		}
		part := c[w[0]:min(w[1], w[0]+maxLen-total)]
		s := string(utf16.Decode(part))
		if w[0] > 0 {
			s = "…" + s
		}
		if w[1] < len(c) {
			s += "…"
		}
		parts = append(parts, s)
		total += len(part)
	}
	return strings.Join(parts, "\n\n")
}
