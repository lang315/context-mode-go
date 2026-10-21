package store

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/lang315/context-mode-go/internal/hookio"
)

// Chunk is one indexed section. ContentType is Code if HasCode else Prose.
type Chunk struct {
	Title, Content string
	HasCode        bool
}

// ChunkMaxBytes is CHUNK_MAX_BYTES / MAX_CHUNK_BYTES (ST:153).
const ChunkMaxBytes = 4096

var (
	mdRule      = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^[-_*]{3,}` + jsWS + `*$`) })
	mdHeading   = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^(#{1,4})` + jsWS + `+(` + jsDot + `+)$`) })
	mdFence     = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile("^(`{3,})(" + jsDot + "*)?$") })
	mdParagraph = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`\n\n+`) })
)

type mdHead struct {
	level int
	text  string
}

// ChunkMarkdown splits Markdown on H1-H4 headings and horizontal rules,
// keeping fenced code blocks whole. Port of #chunkMarkdown (ST:1699-1816).
func ChunkMarkdown(text string, maxBytes int) []Chunk {
	var chunks []Chunk
	lines := strings.Split(text, "\n")
	var stack []mdHead
	var current []string
	currentHeading := ""

	flush := func() {
		joined := hookio.JSTrim(strings.Join(current, "\n"))
		if joined == "" {
			return
		}
		title := buildTitle(stack, currentHeading)
		hasCode := false
		for _, l := range current {
			if strings.HasPrefix(l, "```") {
				hasCode = true
				break
			}
		}
		current = nil
		if len(joined) <= maxBytes {
			chunks = append(chunks, Chunk{Title: title, Content: joined, HasCode: hasCode})
			return
		}
		// Oversized: split at paragraph boundaries.
		paragraphs := mdParagraph().Split(joined, -1)
		var acc []string
		part := 1
		flushAcc := func() {
			if len(acc) == 0 {
				return
			}
			p := hookio.JSTrim(strings.Join(acc, "\n\n"))
			if p == "" {
				return
			}
			pt := title
			if len(paragraphs) > 1 {
				pt = fmt.Sprintf("%s (%d)", title, part)
			}
			part++
			chunks = append(chunks, Chunk{Title: pt, Content: p, HasCode: strings.Contains(p, "```")})
			acc = nil
		}
		for _, para := range paragraphs {
			acc = append(acc, para)
			if len(strings.Join(acc, "\n\n")) > maxBytes && len(acc) > 1 {
				acc = acc[:len(acc)-1]
				flushAcc()
				acc = []string{para}
			}
		}
		flushAcc()
	}

	for i := 0; i < len(lines); {
		line := lines[i]
		if mdRule().MatchString(line) {
			flush()
			i++
			continue
		}
		if m := mdHeading().FindStringSubmatch(line); m != nil {
			flush()
			level := len(m[1])
			heading := hookio.JSTrim(m[2])
			for len(stack) > 0 && stack[len(stack)-1].level >= level {
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, mdHead{level, heading})
			currentHeading = heading
			current = append(current, line)
			i++
			continue
		}
		if m := mdFence().FindStringSubmatch(line); m != nil {
			fence := m[1]
			code := []string{line}
			i++
			for i < len(lines) {
				code = append(code, lines[i])
				if strings.HasPrefix(lines[i], fence) && hookio.JSTrim(lines[i]) == fence {
					i++
					break
				}
				i++
			}
			current = append(current, code...)
			continue
		}
		current = append(current, line)
		i++
	}
	flush()
	return chunks
}

// buildTitle: ST:2115-2123.
func buildTitle(stack []mdHead, currentHeading string) string {
	if len(stack) == 0 {
		if currentHeading != "" {
			return currentHeading
		}
		return "Untitled"
	}
	parts := make([]string, len(stack))
	for i, h := range stack {
		parts[i] = h.text
	}
	return strings.Join(parts, " > ")
}
