package security

import "strings"

// BlockedMessage is the TS deny text for a shell command (server.ts:1122).
// pattern is the full settings entry, e.g. "Bash(sudo *)".
func BlockedMessage(pattern string) string {
	return "Command blocked by security policy: matches deny pattern " + pattern
}

// CheckCommand ports evaluateCommandDenyOnly (security.ts:573-588): every
// chained segment and every nested subshell is matched against the Bash deny
// rules of each settings file in precedence order; the first match wins.
func (p *Policy) CheckCommand(cmd string) (blocked bool, pattern string) {
	return matchCommand(p.rules(), cmd)
}

// matchCommand is CheckCommand over already-loaded rules.
func matchCommand(rules []*fileRules, cmd string) (blocked bool, pattern string) {
	for _, el := range collectCommandElements(cmd) {
		for _, r := range rules {
			for _, d := range r.bashDeny {
				if d.re.MatchString(el) {
					return true, d.pattern
				}
			}
		}
	}
	return false, ""
}

// isEscaped ports security.ts:158-164: an odd run of backslashes before i.
func isEscaped(s string, i int) bool {
	n := 0
	for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
		n++
	}
	return n%2 == 1
}

// splitChainedCommands ports security.ts:174-237. It splits on && || ; | & and
// newlines outside quotes, backticks and $(...). Branch order is kept verbatim:
// && and || ignore escaping, as in TS.
func splitChainedCommands(command string) []string {
	var parts []string
	var cur strings.Builder
	inSingle, inDouble, inBacktick := false, false, false
	depth := 0
	flush := func() {
		parts = append(parts, strings.TrimSpace(cur.String()))
		cur.Reset()
	}
	next := func(i int) byte {
		if i+1 < len(command) {
			return command[i+1]
		}
		return 0
	}
	for i := 0; i < len(command); i++ {
		ch := command[i]
		escaped := isEscaped(command, i)
		switch {
		case ch == '\'' && !inDouble && !inBacktick && !escaped:
			inSingle = !inSingle
			cur.WriteByte(ch)
		case ch == '"' && !inSingle && !inBacktick && !escaped:
			inDouble = !inDouble
			cur.WriteByte(ch)
		case ch == '`' && !inSingle && !inDouble && !escaped:
			inBacktick = !inBacktick
			cur.WriteByte(ch)
		case !inSingle && !inDouble && !inBacktick:
			switch {
			case ch == '$' && next(i) == '(' && !escaped:
				depth++
				cur.WriteString("$(")
				i++
			case depth > 0 && ch == '(' && !escaped:
				depth++
				cur.WriteByte(ch)
			case ch == ')' && depth > 0 && !escaped:
				depth--
				cur.WriteByte(ch)
			case depth == 0 && (ch == ';' || ch == '\n' || ch == '\r') && !escaped:
				flush()
			case depth == 0 && ch == '|' && next(i) == '|':
				flush()
				i++
			case depth == 0 && ch == '&' && next(i) == '&':
				flush()
				i++
			case depth == 0 && ch == '&' && !escaped:
				flush()
			case depth == 0 && ch == '|':
				flush()
			default:
				cur.WriteByte(ch)
			}
		default:
			cur.WriteByte(ch)
		}
	}
	flush()
	out := parts[:0]
	for _, s := range parts {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// extractSubshellCommands ports security.ts:243-302: the bodies of $(...) and
// backtick substitutions outside single quotes. Backtick bodies are expanded
// recursively here; $(...) bodies are recursed by collectCommandElements.
// Arithmetic $(( is not a substitution, but substitutions inside it are found.
func extractSubshellCommands(command string) []string {
	var subs []string
	inSingle, inDouble := false, false
	backtickStart := -1
	var starts, depths []int
	parenDepth := 0
	at := func(i int) byte {
		if i < len(command) {
			return command[i]
		}
		return 0
	}
	for i := 0; i < len(command); i++ {
		ch := command[i]
		escaped := isEscaped(command, i)
		switch {
		case ch == '\'' && !inDouble && backtickStart == -1 && !escaped:
			inSingle = !inSingle
		case ch == '"' && !inSingle && backtickStart == -1 && !escaped:
			inDouble = !inDouble
		case ch == '`' && !inSingle && !inDouble && !escaped:
			if backtickStart == -1 {
				backtickStart = i + 1
			} else {
				sub := command[backtickStart:i]
				subs = append(subs, sub)
				subs = append(subs, extractSubshellCommands(sub)...)
				backtickStart = -1
			}
		case !inSingle && backtickStart == -1:
			switch {
			case ch == '$' && at(i+1) == '(' && !escaped:
				if at(i+2) == '(' {
					parenDepth += 2
					i += 2
				} else {
					starts = append(starts, i+2)
					depths = append(depths, parenDepth)
					parenDepth++
					i++
				}
			case ch == '(' && !escaped:
				parenDepth++
			case ch == ')' && !escaped:
				if parenDepth > 0 {
					parenDepth--
				}
				if n := len(depths); n > 0 && parenDepth == depths[n-1] {
					start := starts[n-1]
					depths, starts = depths[:n-1], starts[:n-1]
					subs = append(subs, command[start:i])
				}
			}
		}
	}
	return subs
}

// collectCommandElements ports security.ts:304-314.
func collectCommandElements(command string) []string {
	var els []string
	for _, seg := range splitChainedCommands(command) {
		els = append(els, seg)
		for _, sub := range extractSubshellCommands(seg) {
			els = append(els, collectCommandElements(sub)...)
		}
	}
	return els
}
