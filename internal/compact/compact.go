// Package compact builds the text cmgo-hook puts in place of a large Bash
// result in the main session (bash-compact spec §1, §3). No I/O.
package compact

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	DefaultMinBytes = 5120
	MaxBytes        = 30000 // Claude Code spills a larger Bash result to a file itself
	FullMarker      = "# cmgo:full"

	edgeBytes    = 1024
	maxMatches   = 20
	maxMatchLine = 200
)

var matchRE = regexp.MustCompile(`(?i)error|fail|panic|warn`)

// MinBytes reads CMGO_BASH_COMPACT_MIN_BYTES: an integer 1-30000 is the
// threshold, 0 turns compaction off (returned as 0), anything else, unset
// included, is DefaultMinBytes.
func MinBytes(getenv func(string) string) int {
	n, err := strconv.Atoi(strings.TrimSpace(getenv("CMGO_BASH_COMPACT_MIN_BYTES")))
	if err != nil || n < 0 || n > MaxBytes {
		return DefaultMinBytes
	}
	return n
}

// Output is what the model sees of a Bash result: stdout, then stderr after
// a newline when both are non-empty.
func Output(stdout, stderr string) string {
	if stdout == "" || stderr == "" {
		return stdout + stderr
	}
	return stdout + "\n" + stderr
}

// Text returns what the model sees instead of output: the first and last
// edgeBytes cut at line boundaries, up to maxMatches lines of the middle that
// match error|fail|panic|warn, and how to get the rest from label.
func Text(output, label string) string {
	head := cutHead(output, edgeBytes)
	tail := cutTail(output[len(head):], edgeBytes)
	middle := output[len(head) : len(output)-len(tail)]
	var matches []string
	for _, line := range strings.Split(middle, "\n") {
		if matchRE.MatchString(line) {
			matches = append(matches, line)
		}
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(head, "\n"))
	fmt.Fprintf(&b, "\n\n[cmgo: %.1f KB, %d lines; the middle is kept out of context.", float64(len(output))/1024, lineCount(output))
	switch n := len(matches); {
	case n == 0:
		b.WriteString("]\n")
	case n == 1:
		b.WriteString(" 1 line matches error|fail|panic|warn:]\n")
	case n <= maxMatches:
		fmt.Fprintf(&b, " %d lines match error|fail|panic|warn:]\n", n)
	default:
		fmt.Fprintf(&b, " %d lines match error|fail|panic|warn (first %d shown):]\n", n, maxMatches)
	}
	for _, m := range matches[:min(len(matches), maxMatches)] {
		b.WriteString(cutHead(m, maxMatchLine))
		b.WriteString("\n")
	}
	b.WriteString("...\n")
	b.WriteString(strings.TrimRight(tail, "\n"))
	fmt.Fprintf(&b, "\n\n[cmgo: the full output is indexed as %q. Search it: ctx_search(queries: [\"...\"], source: %q). Need it verbatim? Re-run the command with %q at the end.]",
		label, label, FullMarker)
	return b.String()
}

// cutHead returns at most the first n bytes of s, cut back to the last
// newline when there is one, else to a UTF-8 boundary.
func cutHead(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if i := strings.LastIndexByte(s[:n], '\n'); i >= 0 {
		return s[:i+1]
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// cutTail returns at most the last n bytes of s, cut forward to just after
// the first newline when there is one, else to a UTF-8 boundary.
func cutTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	start := len(s) - n
	if i := strings.IndexByte(s[start:], '\n'); i >= 0 && start+i+1 < len(s) {
		return s[start+i+1:]
	}
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}

// lineCount counts the lines of s; a trailing newline does not start one.
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimRight(s, "\n"), "\n") + 1
}
