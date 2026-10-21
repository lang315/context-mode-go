package capture

import (
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/lang315/context-mode-go/internal/store"
)

func gitRe(rest string) *regexp.Regexp {
	return regexp.MustCompile(`\bgit` + jsWS + `+` + rest)
}

// gitOps is GIT_PATTERNS (X:352-371) in priority order, plus the subcommand
// each entry stands for. TS looked the parsed subcommand up by op label, so
// `checkout` (label "branch") was never found (spec §3.4).
var gitOps = sync.OnceValue(func() []struct {
	sub, op string
	re      *regexp.Regexp
} {
	return []struct {
		sub, op string
		re      *regexp.Regexp
	}{
		{"checkout", "branch", gitRe(`checkout\b`)},
		{"commit", "commit", gitRe(`commit\b`)},
		{"merge", "merge", gitRe(`merge` + jsWS + `+` + jsNotWS + `+`)},
		{"rebase", "rebase", gitRe(`rebase\b`)},
		{"stash", "stash", gitRe(`stash\b`)},
		{"push", "push", gitRe(`push\b`)},
		{"pull", "pull", gitRe(`pull\b`)},
		{"log", "log", gitRe(`log\b`)},
		{"diff", "diff", gitRe(`diff\b`)},
		{"status", "status", gitRe(`status\b`)},
		{"branch", "branch", gitRe(`branch\b`)},
		{"reset", "reset", gitRe(`reset\b`)},
		{"add", "add", gitRe(`add\b`)},
		{"cherry-pick", "cherry-pick", gitRe(`cherry-pick\b`)},
		{"tag", "tag", gitRe(`tag\b`)},
		{"fetch", "fetch", gitRe(`fetch\b`)},
		{"clone", "clone", gitRe(`clone\b`)},
		{"worktree", "worktree", gitRe(`worktree\b`)},
	}
})

// gitEvents ports extractGit (X:373-440): the parsed subcommand first, then
// the first table regex that matches anywhere (so `cd /r && git status`
// still counts). A -C dir becomes a leading cwd event.
func gitEvents(c call) []store.Event {
	if c.tool != "Bash" {
		return nil
	}
	cmd := inputString(c.input, "command", "")
	dir, sub := parseGit(cmd)
	op := ""
	for _, g := range gitOps() {
		if sub != "" && g.sub == sub {
			op = g.op
			break
		}
	}
	if op == "" {
		for _, g := range gitOps() {
			if g.re.MatchString(cmd) {
				op = g.op
				break
			}
		}
	}
	if op == "" {
		return nil
	}
	var evs []store.Event
	if dir != "" {
		evs = append(evs, ev("cwd", "cwd", 2, expandHome(dir, c.home)))
	}
	if op == "commit" {
		if msg := commitMessage(cmd); msg != "" {
			return append(evs, ev("git_commit", "git", 2, msg))
		}
	}
	return append(evs, ev("git", "git", 2, op))
}

// parseGit ports parseGitInvocation (X:483-527). It returns the last -C /
// --directory value and the first bare token after git; "" means none
// (TS null, and an empty dir is falsy there too).
func parseGit(cmd string) (dir, sub string) {
	toks := tokenize(cmd)
	i := 0
	for i < len(toks) && isEnvAssignment(toks[i]) {
		i++
	}
	isGit := func(t string) bool { return t == "git" || strings.HasSuffix(t, "/git") }
	for i < len(toks) && !isGit(toks[i]) && isRunner(toks[i]) {
		i++
	}
	if i >= len(toks) || !isGit(toks[i]) {
		return "", ""
	}
	for i++; i < len(toks); i++ {
		t := toks[i]
		switch {
		case t == "-C" || t == "--directory":
			dir = ""
			if i+1 < len(toks) {
				dir = toks[i+1]
			}
			i++
		case strings.HasPrefix(t, "--directory="):
			dir = strings.TrimPrefix(t, "--directory=")
		case strings.HasPrefix(t, "-"):
		default:
			return dir, t
		}
	}
	return dir, ""
}

// expandHome ports expandHomeTilde (X:458-463): "~" and "~/x" only. With no
// home TS expanded to "~", which leaves the path unchanged.
func expandHome(p, home string) string {
	if home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return home + p[1:]
	}
	return p
}

// isEnvAssignment ports X:529-547: [A-Z_][A-Z0-9_]*= at the start.
func isEnvAssignment(t string) bool {
	for j := 0; j < len(t); j++ {
		c := t[j]
		switch {
		case j == 0:
			if !(c >= 'A' && c <= 'Z' || c == '_') {
				return false
			}
		case c == '=':
			return true
		case !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'):
			return false
		}
	}
	return false
}

func isRunner(t string) bool {
	switch t {
	case "sudo", "doas", "env", "exec", "time":
		return true
	}
	return false
}

// tokenize ports tokenizeCommand (X:567-601): space and tab separate,
// quotes group, a backslash takes the next character literally (inside
// either quote too), and adjacent pieces merge.
func tokenize(cmd string) []string {
	var toks []string
	n := len(cmd)
	// next returns the full character at i, so an escaped multi-byte
	// character stays whole (JS takes it one UTF-16 unit at a time).
	next := func(i int) string {
		_, size := utf8.DecodeRuneInString(cmd[i:])
		return cmd[i : i+size]
	}
	i := 0
	for i < n {
		for i < n && (cmd[i] == ' ' || cmd[i] == '\t') {
			i++
		}
		if i >= n {
			break
		}
		var buf strings.Builder
		for i < n && cmd[i] != ' ' && cmd[i] != '\t' {
			ch := cmd[i]
			switch {
			case ch == '"' || ch == '\'':
				i++
				for i < n && cmd[i] != ch {
					if cmd[i] == '\\' && i+1 < n {
						i++
					}
					s := next(i)
					buf.WriteString(s)
					i += len(s)
				}
				if i < n {
					i++ // closing quote
				}
			case ch == '\\' && i+1 < n:
				s := next(i + 1)
				buf.WriteString(s)
				i += 1 + len(s)
			default:
				buf.WriteByte(ch)
				i++
			}
		}
		toks = append(toks, buf.String())
	}
	return toks
}

// commitMessage ports extractCommitMessageFromCommand (X:609-638): the
// value of the first --message=V, --message V or -[a-z]*m V; "" for none.
func commitMessage(cmd string) string {
	argv := tokenize(cmd)
	arg := func(i int) string {
		if i < len(argv) {
			return argv[i]
		}
		return ""
	}
	for i, a := range argv {
		switch {
		case len(a) > len("--message=") && strings.HasPrefix(a, "--message="):
			return strings.TrimPrefix(a, "--message=")
		case a == "--message":
			return arg(i + 1)
		case len(a) >= 2 && a[0] == '-' && a[1] != '-' && a[len(a)-1] == 'm' && lowerRun(a[1:]):
			return arg(i + 1)
		}
	}
	return ""
}

func lowerRun(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'a' || s[i] > 'z' {
			return false
		}
	}
	return true
}
