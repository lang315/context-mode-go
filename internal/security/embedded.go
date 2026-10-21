package security

import (
	"regexp"
	"strings"
	"sync"
)

// Quote alternations replace the JS backreference form (['"])(.*?)\1, which
// RE2 lacks. Leftmost-first submatching makes the lazy bodies equivalent.
const (
	q2 = `(?:'(.*?)'|"(.*?)")`
	q3 = `(?:'(.*?)'|"(.*?)"|` + "`(.*?)`" + `)`
)

// shellEscapePatterns ports SHELL_ESCAPE_PATTERNS (security.ts:795-825).
var shellEscapePatterns = sync.OnceValue(func() map[string][]*regexp.Regexp {
	jsShellEscapes := []*regexp.Regexp{
		regexp.MustCompile(`exec(?:Sync|File|FileSync)?\(\s*` + q3),
		regexp.MustCompile(`spawn(?:Sync)?\(\s*` + q3),
	}
	return map[string][]*regexp.Regexp{
		"python": {
			regexp.MustCompile(`os\.system\(\s*` + q2 + `\s*\)`),
			regexp.MustCompile(`subprocess\.(?:run|call|Popen|check_output|check_call)\(\s*` + q2),
		},
		"javascript": jsShellEscapes,
		"typescript": jsShellEscapes,
		"ruby": {
			regexp.MustCompile(`system\(\s*` + q2),
			regexp.MustCompile("`(.*?)`"),
		},
		"go": {
			regexp.MustCompile(`exec\.Command\(\s*` + q3),
		},
		"php": {
			regexp.MustCompile(`shell_exec\(\s*` + q3),
			regexp.MustCompile(`(?:^|[^.])exec\(\s*` + q3),
			regexp.MustCompile(`(?:^|[^.])system\(\s*` + q3),
			regexp.MustCompile(`passthru\(\s*` + q3),
			regexp.MustCompile(`proc_open\(\s*` + q3),
		},
		"rust": {
			regexp.MustCompile(`Command::new\(\s*` + q3),
		},
	}
})

var (
	pySubprocessList = sync.OnceValue(func() *regexp.Regexp {
		return regexp.MustCompile(`subprocess\.(?:run|call|Popen|check_output|check_call)\(\s*\[([^\]]+)\]`)
	})
	quotedString = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(q2) })
)

// firstGroup returns the first non-empty capture group (the command body).
func firstGroup(m []string) string {
	for _, g := range m[1:] {
		if g != "" {
			return g
		}
	}
	return ""
}

// extractShellCommands ports security.ts:858-889 (with the Python list form,
// security.ts:834-849): subprocess.run(["rm", "-rf", "/"]) gives "rm -rf /".
func extractShellCommands(code, language string) []string {
	var cmds []string
	for _, re := range shellEscapePatterns()[language] {
		for _, m := range re.FindAllStringSubmatch(code, -1) {
			if c := firstGroup(m); c != "" {
				cmds = append(cmds, c)
			}
		}
	}
	if language == "python" {
		for _, m := range pySubprocessList().FindAllStringSubmatch(code, -1) {
			var args []string
			for _, a := range quotedString().FindAllStringSubmatch(m[1], -1) {
				args = append(args, a[1]+a[2])
			}
			if len(args) > 0 {
				cmds = append(cmds, strings.Join(args, " "))
			}
		}
	}
	return cmds
}

// EmbeddedBlockedMessage is the TS deny text for a shell command found inside
// non-shell code (server.ts:1151).
func EmbeddedBlockedMessage(cmd, pattern string) string {
	return `Command blocked by security policy: embedded shell command "` + cmd + `" matches deny pattern ` + pattern
}

// CheckCode ports the language dispatch at server.ts:1745-1750: shell code is
// checked as a command; other languages are scanned for embedded commands.
// Use CheckCodeMatch when the message must name the embedded command.
func (p *Policy) CheckCode(language, code string) (blocked bool, pattern string) {
	blocked, _, pattern = p.CheckCodeMatch(language, code)
	return blocked, pattern
}

// CheckCodeMatch is CheckCode that also returns the embedded command that
// matched ("" for shell). Callers build the text with BlockedMessage when cmd
// is "" and EmbeddedBlockedMessage otherwise (checkNonShellDenyPolicy,
// server.ts:1137-1162).
func (p *Policy) CheckCodeMatch(language, code string) (blocked bool, cmd, pattern string) {
	if language == "shell" {
		blocked, pattern = p.CheckCommand(code)
		return blocked, "", pattern
	}
	cmds := extractShellCommands(code, language)
	if len(cmds) == 0 {
		return false, "", ""
	}
	rules := p.rules() // one stat per settings file per call
	for _, c := range cmds {
		if blocked, pattern = matchCommand(rules, c); blocked {
			return true, c, pattern
		}
	}
	return false, "", ""
}
