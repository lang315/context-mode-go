package routing

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lang315/context-mode-go/internal/hookio"
)

// Bash read guidance (spec: bash-read-guidance §1): estimate how many bytes a
// Bash command prints from files, using a bounded tokenizer and at most
// readGuardMaxStats stats.
const (
	readGuardMaxCommand   = 4096 // longer commands are scripts, not reads
	readGuardMaxStats     = 8
	readGuardBytesPerLine = 64 // upper-side average for source and Markdown
)

// readGuardMin ports nothing from TS: CMGO_BASH_READ_MIN_BYTES is an int in
// [1, 100000000]; anything else, unset included, turns the guidance off
// (returned as 0). It is off by default since the 2026-10-27 audit: 0 of 11
// hints were followed (spec §5.3 override). 16384 restores the old default.
func readGuardMin(getenv func(string) string) int64 {
	if getenv == nil {
		return 0
	}
	if n, ok := jsParseInt(getenv("CMGO_BASH_READ_MIN_BYTES")); ok && n >= 0 && n <= 100_000_000 {
		return n
	}
	return 0
}

// bashReadGuidance returns the hint for a Bash command that prints at least
// the threshold from files, once the MCP server is up (spec §1). The first
// hint of a session is the long text, later ones the short text.
func bashReadGuidance(command string, d Deps) (string, bool) {
	min := readGuardMin(d.Getenv)
	if min == 0 || d.MCPReady == nil {
		return "", false
	}
	total, name := estimateRead(command, d.Cwd, d.Home)
	if total < min || !d.MCPReady() {
		return "", false
	}
	tmpl := bashReadShort
	if guidanceOnce(d.RunDir, d.SessionID, "bashread") {
		tmpl = bashReadLong
	}
	return fmt.Sprintf(tmpl, (total+1023)/1024, hookio.JSPrefix(name, 120)), true
}

// rgWord is one shell word with its quotes removed.
type rgWord struct {
	s       string
	unknown bool // has an expansion ($, or an unquoted glob character): never statted
	tilde   bool // starts with an unquoted ~
}

// rgStage is one command of a pipeline.
type rgStage struct {
	words  []rgWord
	toFile bool // stdout goes somewhere other than the context
}

// rgChain is a pipeline: stages joined by |.
type rgChain []rgStage

// tokenize splits cmd into pipelines at &&, ||, ;, & and newline. It reports
// false for forms it does not read: long commands, heredocs, command
// substitution, subshells, input redirects and unbalanced quotes.
func tokenize(cmd string) ([]rgChain, bool) {
	if len(cmd) > readGuardMaxCommand || strings.Contains(cmd, "<<") ||
		strings.Contains(cmd, "$(") || strings.Contains(cmd, "`") {
		return nil, false
	}
	var (
		chains []rgChain
		chain  rgChain
		stage  rgStage
		cur    []byte
		w      rgWord
		have   bool // a word is in progress (an empty "" counts)
		redir  int  // 1: the next word is a stdout redirect target; 2: a stderr one
	)
	flush := func() {
		if !have {
			return
		}
		w.s = string(cur)
		switch redir {
		case 0:
			stage.words = append(stage.words, w)
		case 1:
			stage.toFile = true
		}
		redir, cur, w, have = 0, cur[:0], rgWord{}, false
	}
	endStage := func() { flush(); chain = append(chain, stage); stage = rgStage{} }
	endChain := func() { endStage(); chains = append(chains, chain); chain = nil }
	next := func(i int) byte {
		if i+1 < len(cmd) {
			return cmd[i+1]
		}
		return 0
	}
	for i := 0; i < len(cmd); i++ {
		switch c := cmd[i]; {
		case c == '\\':
			if i++; i < len(cmd) {
				cur, have = append(cur, cmd[i]), true
			}
		case c == '\'':
			j := strings.IndexByte(cmd[i+1:], '\'')
			if j < 0 {
				return nil, false
			}
			cur, have = append(cur, cmd[i+1:i+1+j]...), true
			i += j + 1
		case c == '"':
			have = true
			for i++; i < len(cmd) && cmd[i] != '"'; i++ {
				if cmd[i] == '\\' && i+1 < len(cmd) {
					i++
				} else if cmd[i] == '$' {
					w.unknown = true
				}
				cur = append(cur, cmd[i])
			}
			if i >= len(cmd) {
				return nil, false
			}
		case c == ' ' || c == '\t':
			flush()
		case c == '\n' || c == ';':
			endChain()
		case c == '|':
			if next(i) == '|' {
				i++
				endChain()
			} else {
				endStage()
			}
		case c == '&':
			switch next(i) {
			case '&':
				i++
				endChain()
			case '>': // &>file: both streams to a file
				i++
				if next(i) == '>' {
					i++
				}
				flush()
				redir = 1
			default:
				endChain()
			}
		case c == '>':
			fd := 1
			if have && !w.unknown && len(cur) == 1 && cur[0] >= '0' && cur[0] <= '9' {
				fd = int(cur[0] - '0')
				cur, have = cur[:0], false
			} else {
				flush()
			}
			if next(i) == '>' {
				i++
			}
			if next(i) == '&' { // >&2, 2>&1: no file involved
				i++
				for next(i) >= '0' && next(i) <= '9' || next(i) == '-' {
					i++
				}
				continue
			}
			redir = 1
			if fd != 1 {
				redir = 2
			}
		case c == '<' || c == '(' || c == ')':
			return nil, false
		case strings.IndexByte("$*?[{!", c) >= 0:
			cur, have, w.unknown = append(cur, c), true, true
		case c == '~' && !have:
			cur, have, w.tilde = append(cur, c), true, true
		default:
			cur, have = append(cur, c), true
		}
	}
	endChain()
	return chains, true
}

// estimator walks the chains of one command, tracking the working dir.
type estimator struct {
	base, home  string // base is "" when unknown
	stats       int
	largest     int64
	largestName string
}

// estimateRead returns the summed estimate of the bytes command prints from
// files, and the typed name of the largest file it stats ("" when none).
// cwd is the dir relative paths resolve against. Forms it does not read
// (spec §1.1) estimate 0.
func estimateRead(command, cwd, home string) (int64, string) {
	chains, ok := tokenize(command)
	if !ok {
		return 0, ""
	}
	e := &estimator{base: cwd, home: home}
	var total int64
	for _, ch := range chains {
		total += e.chain(ch)
	}
	return total, e.largestName
}

func (e *estimator) chain(ch rgChain) int64 {
	if len(ch) == 0 || len(ch[0].words) == 0 || ch[0].words[0].unknown {
		return 0
	}
	first := ch[0].words
	switch first[0].s {
	case "cd":
		e.cd(first)
		return 0
	case "pushd", "popd":
		e.base = ""
		return 0
	}
	for _, st := range ch {
		if st.toFile {
			return 0
		}
	}
	est := e.stage(first)
	for _, st := range ch[1:] {
		est = min(est, limitOf(st.words))
	}
	return est
}

// cd moves the base; anything it cannot resolve makes the base unknown.
func (e *estimator) cd(words []rgWord) {
	switch {
	case len(words) == 1 && e.home != "":
		e.base = e.home
	case len(words) == 2 && words[1].s != "-":
		if p, ok := e.resolve(words[1]); ok {
			e.base = p
			return
		}
		e.base = ""
	default:
		e.base = ""
	}
}

func (e *estimator) stage(words []rgWord) int64 {
	switch words[0].s {
	case "cat", "nl", "tac", "bat":
		var operands []rgWord
		for i, w := range words[1:] {
			if w.s == "--" && !w.unknown {
				operands = append(operands, words[i+2:]...)
				break
			}
			if w.unknown || !strings.HasPrefix(w.s, "-") {
				operands = append(operands, w)
			}
		}
		return e.files(operands, math.MaxInt64)
	case "head", "tail":
		o := parseHeadTail(words[0].s == "tail", words[1:])
		if o.follow {
			return 0
		}
		return e.files(o.operands, o.bound())
	}
	return 0
}

// files sums the sizes of the regular files named by operands, each capped
// at limit. At most readGuardMaxStats operands are statted per command.
func (e *estimator) files(operands []rgWord, limit int64) int64 {
	var sum int64
	for _, w := range operands {
		if e.stats >= readGuardMaxStats {
			break
		}
		p, ok := e.resolve(w)
		if !ok {
			continue
		}
		e.stats++
		fi, err := os.Stat(p)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		n := min(fi.Size(), limit)
		sum += n
		if n > e.largest {
			e.largest, e.largestName = n, w.s
		}
	}
	return sum
}

// resolve turns a word into an absolute path, or reports false when it
// cannot be known.
func (e *estimator) resolve(w rgWord) (string, bool) {
	p := w.s
	switch {
	case w.unknown || p == "":
		return "", false
	case w.tilde:
		if e.home == "" || (p != "~" && !strings.HasPrefix(p, "~/")) {
			return "", false
		}
		return filepath.Join(e.home, p[1:]), true
	case filepath.IsAbs(p):
		return p, true
	case e.base == "":
		return "", false
	}
	return filepath.Join(e.base, p), true
}

// htOpts are the parsed arguments of head or tail.
type htOpts struct {
	lines, bytes int64 // bytes < 0: count lines
	full         bool  // tail -n +K, or a count that cannot be read: no bound
	follow       bool  // tail -f
	operands     []rgWord
}

// bound is the most bytes head or tail prints per file.
func (o htOpts) bound() int64 {
	switch {
	case o.bytes >= 0:
		return o.bytes
	case o.full:
		return math.MaxInt64
	}
	return o.lines * readGuardBytesPerLine
}

func parseHeadTail(tail bool, words []rgWord) htOpts {
	o := htOpts{lines: 10, bytes: -1}
	count := func(s string) (int64, bool) {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n >= 0 && n < 1<<40 {
			return n, true
		}
		return 0, false
	}
	setLines := func(s string) {
		if tail && strings.HasPrefix(s, "+") {
			o.full = true
		} else if n, ok := count(s); ok {
			o.lines, o.full = n, false
		} else {
			o.full = true
		}
	}
	setBytes := func(s string) {
		if n, ok := count(s); ok {
			o.bytes = n
		} else {
			o.full = true
		}
	}
	for i := 0; i < len(words); i++ {
		w := words[i]
		s := w.s
		arg := func() string { // the option's value: the next word
			if i+1 < len(words) {
				i++
				return words[i].s
			}
			return ""
		}
		switch {
		case w.unknown:
			o.operands = append(o.operands, w)
		case s == "--":
			o.operands = append(o.operands, words[i+1:]...)
			return o
		case s == "-n" || s == "--lines":
			setLines(arg())
		case strings.HasPrefix(s, "--lines="):
			setLines(s[len("--lines="):])
		case s == "-c" || s == "--bytes":
			setBytes(arg())
		case strings.HasPrefix(s, "--bytes="):
			setBytes(s[len("--bytes="):])
		case strings.HasPrefix(s, "-n"):
			setLines(s[2:])
		case strings.HasPrefix(s, "-c"):
			setBytes(s[2:])
		case tail && (s == "-f" || s == "-F" || strings.HasPrefix(s, "--follow")):
			o.follow = true
		case len(s) > 1 && s[0] == '-' && strings.Trim(s[1:], "0123456789") == "":
			setLines(s[1:])
		case strings.HasPrefix(s, "-") && s != "-":
			// -q, -v, -z...: no effect on the size
		default:
			o.operands = append(o.operands, w)
		}
	}
	return o
}

// limitOf is how many bytes a later pipeline stage lets through, or
// math.MaxInt64 when it is not a limiter (spec §1.1).
func limitOf(words []rgWord) int64 {
	if len(words) == 0 || words[0].unknown {
		return math.MaxInt64
	}
	switch words[0].s {
	case "head", "tail":
		o := parseHeadTail(words[0].s == "tail", words[1:])
		if o.follow {
			return math.MaxInt64
		}
		return o.bound()
	case "wc":
		return 0
	case "grep":
		for i, w := range words[1:] {
			s := w.s
			switch {
			case w.unknown:
			case s == "--count" || s == "--quiet" || s == "--files-with-matches" || s == "--files-without-match":
				return 0
			case strings.HasPrefix(s, "--max-count="):
				if n, err := strconv.ParseInt(s[len("--max-count="):], 10, 32); err == nil && n >= 0 {
					return n * readGuardBytesPerLine
				}
			case s == "-m" && i+2 < len(words):
				if n, err := strconv.ParseInt(words[i+2].s, 10, 32); err == nil && n >= 0 {
					return n * readGuardBytesPerLine
				}
			case len(s) > 1 && s[0] == '-' && s[1] != '-' && strings.ContainsAny(s[1:], "cqlL"):
				return 0
			}
		}
	}
	return math.MaxInt64
}
