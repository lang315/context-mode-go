package security

// EvaluateCommand ports evaluateCommand (security.ts:512-563). decision is
// "deny", "ask" or "allow"; pattern is the full rule ("Bash(sudo *)"), or ""
// for the default ask. A deny in any file wins first; then, file by file, an
// ask on any element returns ask, and allow needs every element allowed.
func (p *Policy) EvaluateCommand(cmd string) (decision, pattern string) {
	rules := p.rules()
	if blocked, pat := matchCommand(rules, cmd); blocked {
		return "deny", pat
	}
	els := collectCommandElements(cmd)
	for _, r := range rules {
		allAllowed, allowPat := len(els) > 0, ""
		for _, el := range els {
			if pat := firstMatch(r.bashAsk, el); pat != "" {
				return "ask", pat
			}
			if pat := firstMatch(r.bashAllow, el); pat != "" {
				allowPat = pat // TS reports the last element's allow rule
			} else {
				allAllowed = false
			}
		}
		if allAllowed {
			return "allow", allowPat
		}
	}
	return "ask", ""
}

// firstMatch ports matchesAnyPattern (security.ts:141-152).
func firstMatch(rules []bashRule, el string) string {
	for _, r := range rules {
		if r.re.MatchString(el) {
			return r.pattern
		}
	}
	return ""
}
