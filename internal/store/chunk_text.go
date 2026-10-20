package store

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ST:160-170.
const (
	minBlankLineSections         = 3
	maxBlankLineSections         = 200
	blankSectionStrategyMaxBytes = 5000
	chunkTitleMaxChars           = 80
	whitespaceBreakRatio         = 0.5
	defaultLinesPerChunk         = 20
	plainOverlap                 = 2
)

var blankLineSplit = regexp.MustCompile(`\n` + jsWS + `*\n`)

// ChunkPlainText splits logs and command output: blank-line sections when the
// output is naturally sectioned, else one "Output" chunk, else windows of
// linesPerChunk lines with a 2-line overlap. Chunks are always prose.
// Port of indexPlainText + #chunkPlainText (ST:1019-1040, ST:1911-1976).
func ChunkPlainText(text string, linesPerChunk int) []Chunk {
	if jsTrim(text) == "" {
		return nil
	}
	if linesPerChunk <= 0 {
		linesPerChunk = defaultLinesPerChunk
	}
	maxBytes := ChunkMaxBytes

	sections := blankLineSplit.Split(text, -1)
	if len(sections) >= minBlankLineSections && len(sections) <= maxBlankLineSections && allUnder(sections, blankSectionStrategyMaxBytes) {
		var out []Chunk
		for i, sec := range sections {
			trimmed := jsTrim(sec)
			if trimmed == "" {
				continue
			}
			first, _, _ := strings.Cut(trimmed, "\n")
			title := u16prefix(first, chunkTitleMaxChars)
			if title == "" {
				title = fmt.Sprintf("Section %d", i+1)
			}
			if len(trimmed) <= maxBytes {
				out = append(out, Chunk{Title: title, Content: trimmed})
				continue
			}
			out = append(out, splitOversizedPlain(strings.Split(trimmed, "\n"), title, maxBytes)...)
		}
		return out
	}

	lines := strings.Split(text, "\n")
	if len(lines) <= linesPerChunk {
		if len(text) <= maxBytes {
			return []Chunk{{Title: "Output", Content: text}}
		}
		return splitOversizedPlain(lines, "Output", maxBytes)
	}

	var out []Chunk
	step := max(linesPerChunk-plainOverlap, 1)
	for i := 0; i < len(lines); i += step {
		slice := lines[i:min(i+linesPerChunk, len(lines))]
		title := u16prefix(jsTrim(slice[0]), chunkTitleMaxChars)
		if title == "" {
			title = fmt.Sprintf("Lines %d-%d", i+1, min(i+len(slice), len(lines)))
		}
		joined := strings.Join(slice, "\n")
		if len(joined) <= maxBytes {
			out = append(out, Chunk{Title: title, Content: joined})
		} else {
			out = append(out, splitOversizedPlain(slice, title, maxBytes)...)
		}
	}
	return out
}

func allUnder(ss []string, n int) bool {
	for _, s := range ss {
		if len(s) >= n {
			return false
		}
	}
	return true
}

// byteCappedPrefix: largest prefix of s within maxBytes, never cutting a
// character; always at least one character (ST:1825-1839).
func byteCappedPrefix(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	n := 0
	for _, r := range s {
		w := utf8.RuneLen(r)
		if n+w > maxBytes {
			break
		}
		n += w
	}
	if n == 0 {
		_, w := utf8.DecodeRuneInString(s)
		return s[:w]
	}
	return s[:n]
}

// splitOversizedPlain: ST:1846-1909. Both part counters advance per piece,
// which gives titles like "Output (2.1)", "Output (3.2)".
func splitOversizedPlain(lines []string, titlePrefix string, maxBytes int) []Chunk {
	var out []Chunk
	var acc []string
	part := 1
	flush := func() {
		if len(acc) == 0 {
			return
		}
		title := titlePrefix
		if part != 1 {
			title = fmt.Sprintf("%s (%d)", titlePrefix, part)
		}
		out = append(out, Chunk{Title: title, Content: strings.Join(acc, "\n")})
		part++
		acc = nil
	}
	for _, line := range lines {
		if len(line) > maxBytes {
			flush()
			remaining := line
			linePart := 1
			for remaining != "" {
				slice := byteCappedPrefix(remaining, maxBytes)
				if len(slice) < len(remaining) {
					bp := max(strings.LastIndex(slice, " "), strings.LastIndex(slice, "\n"))
					if bp >= 0 && float64(u16len(slice[:bp])) > float64(u16len(slice))*whitespaceBreakRatio {
						slice = slice[:bp]
					}
				}
				title := titlePrefix
				if part != 1 || linePart != 1 {
					title = fmt.Sprintf("%s (%d.%d)", titlePrefix, part, linePart)
				}
				out = append(out, Chunk{Title: title, Content: slice})
				remaining = remaining[len(slice):]
				linePart++
				part++
			}
			continue
		}
		candidate := line
		if len(acc) > 0 {
			candidate = strings.Join(acc, "\n") + "\n" + line
		}
		if len(candidate) > maxBytes && len(acc) > 0 {
			flush()
		}
		acc = append(acc, line)
	}
	flush()
	return out
}
