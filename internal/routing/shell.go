package routing

import (
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

// stripHeredocs ports routing.mjs:228-230,
// cmd.replace(/<<-?\s*["']?(\w+)["']?[\s\S]*?\n\s*\1/g, ""). RE2 has no
// backreference, so this walks the same backtracking order by hand: at each
// "<<", the word is tried longest first and, for each length, the earliest
// newline whose following spaces lead to that word. The other choice points
// (-?, \s*, the quotes) cannot change the outcome: giving one back leaves a
// character the next step cannot match.
func stripHeredocs(cmd string) string {
	var out strings.Builder
	last := 0
	for i := 0; i+1 < len(cmd); {
		if cmd[i] != '<' || cmd[i+1] != '<' {
			i++
			continue
		}
		end, ok := heredocAt(cmd, i)
		if !ok {
			i++
			continue
		}
		out.WriteString(cmd[last:i])
		last, i = end, end
	}
	if last == 0 {
		return cmd
	}
	out.WriteString(cmd[last:])
	return out.String()
}

// heredocAt matches the heredoc regex at cmd[i:], which starts with "<<",
// and returns the end of the match.
func heredocAt(cmd string, i int) (int, bool) {
	p := i + 2
	if p < len(cmd) && cmd[p] == '-' {
		p++
	}
	p = skipJSSpace(cmd, p)
	if p < len(cmd) && (cmd[p] == '"' || cmd[p] == '\'') {
		p++
	}
	wordEnd := p
	for wordEnd < len(cmd) && isWordByte(cmd[wordEnd]) {
		wordEnd++
	}
	for we := wordEnd; we > p; we-- {
		word := cmd[p:we]
		for nl := we; nl < len(cmd); nl++ {
			if cmd[nl] != '\n' {
				continue
			}
			if q := skipJSSpace(cmd, nl+1); strings.HasPrefix(cmd[q:], word) {
				return q + len(word), true
			}
		}
	}
	return 0, false
}

// skipJSSpace returns the index after the run of JS whitespace at s[i:].
func skipJSSpace(s string, i int) int {
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		if !isJSSpace(r) {
			break
		}
		i += n
	}
	return i
}

// isWordByte is JS \w (ASCII only, no u flag).
func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

var (
	singleQuoted = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`'[^']*'`) })
	doubleQuoted = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`"[^"]*"`) })
)

// stripQuotedContent ports routing.mjs:237-241: heredocs removed, then quoted
// strings emptied, so the curl and build-tool tests see only command tokens.
func stripQuotedContent(cmd string) string {
	s := singleQuoted().ReplaceAllLiteralString(stripHeredocs(cmd), "''")
	return doubleQuoted().ReplaceAllLiteralString(s, `""`)
}

// hasShellControl ports SHELL_CONTROL_OPERATORS (routing.mjs:339),
// /[|`\n\r]|\$\(|>>|>|<(?!<)|&(?!&)|&&|\|\||;/. The last < or & of a string
// is never followed by another, so the lookaheads always find a match when
// the character occurs at all.
func hasShellControl(s string) bool {
	return strings.ContainsAny(s, "|`\n\r>;<&") || strings.Contains(s, "$(")
}

// safePattern is one SAFE_COMMAND_PATTERNS entry: re must match and none of
// not may. The TS negative lookaheads all sit right after the command name
// at ^, so each becomes a prefix regex in not.
type safePattern struct {
	re  *regexp.Regexp
	not []*regexp.Regexp
}

func safe(src string, not ...string) safePattern {
	p := safePattern{re: jsRegexp(src)}
	for _, n := range not {
		p.not = append(p.not, jsRegexp(n))
	}
	return p
}

// noVerbose builds the mv/cp/rm/ln entry,
// /^mv(?!\s+-[a-zA-Z]*v[a-zA-Z]*)(?!\s+--verbose\b)\s+[^\r\n]+$/. Only the
// first flag token is checked, as in TS (mv -f -v a b is bounded).
func noVerbose(cmd string) safePattern {
	return safe(`^`+cmd+`\s+[^\r\n]+$`, `^`+cmd+`\s+-[a-zA-Z]*v`, `^`+cmd+`\s+--verbose\b`)
}

// safeCommands ports SAFE_COMMAND_PATTERNS (routing.mjs:260-324) in order.
var safeCommands = sync.OnceValue(func() []safePattern {
	return []safePattern{
		safe(`^pwd$`),
		safe(`^whoami$`),
		safe(`^hostname(?:\s+-[a-zA-Z]+)?$`),
		safe(`^uname(?:\s+-[a-zA-Z]+)?$`),
		safe(`^id(?:\s+\S+)?$`),
		safe(`^date(?:\s+[^\r\n]+)?$`),
		safe(`^echo\s`),
		safe(`^printf\s`),
		safe(`^which\s+\S+(?:\s+\S+)*$`),
		safe(`^type\s+\S+(?:\s+\S+)*$`),
		safe(`^command\s+-v\s+\S+(?:\s+\S+)*$`),
		safe(`^readlink(?:\s+[^\r\n]+)?$`),
		safe(`^basename(?:\s+[^\r\n]+)?$`),
		safe(`^dirname(?:\s+[^\r\n]+)?$`),
		safe(`^realpath(?:\s+[^\r\n]+)?$`),
		safe(`^cd(?:\s+[^\r\n]+)?$`),
		safe(`^mkdir(?:\s+[^\r\n]+)?$`),
		safe(`^touch\s+[^\r\n]+$`),
		noVerbose("mv"),
		noVerbose("cp"),
		noVerbose("rm"),
		noVerbose("ln"),
		safe(`^ls(?:\s+[^\r\n]+)?$`, `^ls\s+-[a-zA-Z]*R`, `^ls\s+--recursive`),
		safe(`^git\s+status(?:\s+[^\r\n]+)?$`),
		safe(`^git\s+rev-parse(?:\s+[^\r\n]+)?$`),
		safe(`^git\s+remote(?:\s+-v|\s+show\s+\S+)?$`),
		safe(`^git\s+branch(?:\s+[^\r\n]+)?$`),
		safe(`^git\s+config\s+--get(?:\s+[^\r\n]+)?$`),
		safe(`^git\s+diff\s+--stat(?:\s+[^\r\n]+)?$`),
		safe(`^git\s+diff\s+--name-only(?:\s+[^\r\n]+)?$`),
		safe(`^git\s+stash\s+list$`),
		safe(`^git\s+tag(?:\s+-l(?:\s+[^\r\n]+)?)?$`),
		safe(`^git\s+log\s+-\d{1,2}(?:\s+[^\r\n]+)?$`),
		safe(`(?:^|\s)--version(?:\s|$)`),
		safe(`^\S+\s+-V(?:\s|$)`),
	}
})

// isStructurallyBounded ports routing.mjs:347-352: a command with no shell
// control operator that matches a safe pattern gets no Bash guidance.
func isStructurallyBounded(command string) bool {
	if command == "" {
		return false
	}
	s := jsTrim(command)
	if hasShellControl(s) {
		return false
	}
	for _, p := range safeCommands() {
		if p.matches(s) {
			return true
		}
	}
	return false
}

func (p safePattern) matches(s string) bool {
	if !p.re.MatchString(s) {
		return false
	}
	for _, n := range p.not {
		if n.MatchString(s) {
			return false
		}
	}
	return true
}
