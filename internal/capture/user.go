package capture

import (
	"regexp"
	"strings"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// systemPrefixes mark prompts Claude Code generated (userpromptsubmit.mjs:40-43).
var systemPrefixes = []string{"<task-notification>", "<system-reminder>", "<context_guidance>", "<tool-result>"}

// UserPrompt ports userpromptsubmit.mjs:36-71: nil for a blank or system
// prompt, else user_prompt with the raw prompt, then extractUserEvents
// (X:2758-2774) on the trimmed prompt, without extractData.
func UserPrompt(prompt string) []store.Event {
	msg := jsTrim(prompt)
	if msg == "" {
		return nil
	}
	for _, p := range systemPrefixes {
		if strings.HasPrefix(msg, p) {
			return nil
		}
	}
	evs := []store.Event{ev("user_prompt", "user-prompt", 1, prompt)}
	for _, x := range []func(string) []store.Event{userPlan, userDecision, userRole, userIntent, userGoal, userBlocker} {
		evs = append(evs, x(msg)...)
	}
	return evs
}

// userPlan ports extractUserPlan (X:2929-2960): "/plan" after spaces or
// tabs, then the end or a space, tab, CR or LF. The checks are ASCII, so
// bytes give the same answer as UTF-16 units.
func userPlan(msg string) []store.Event {
	rest := strings.TrimLeft(msg, " \t")
	if !strings.HasPrefix(rest, "/plan") {
		return nil
	}
	rest = rest[len("/plan"):]
	if rest != "" && !strings.ContainsRune(" \t\n\r", rune(rest[0])) {
		return nil
	}
	detail := "plan via /plan slash"
	if arg := jsTrim(rest); arg != "" {
		detail += ": " + hookio.JSPrefix(arg, 120)
	}
	return []store.Event{ev("plan_enter", "plan", 2, detail)}
}

var (
	questionMark   = regexp.MustCompile(`[?？؟¿]`)
	letter         = regexp.MustCompile(`\pL`)
	clauseSep      = regexp.MustCompile(`[,;，；、،]`)
	twoWords       = regexp.MustCompile(`\pL+` + jsWS + `+\pL+`)
	letterRun      = regexp.MustCompile(`\pL{6,}`)
	goalDirective  = regexp.MustCompile(`(?is)^(?:/goal` + jsWS + `+|(?:goal|objective)` + jsWS + `*:` + jsWS + `*)(.+)$`)
	resolvedMark   = regexp.MustCompile(`[✓✔✅☑🎉]`)
	resolvedPrefix = regexp.MustCompile(`(?i)^` + jsWS + `*(?:fixed|resolved)` + jsWS + `*:`)
	blockerMarkers = regexp.MustCompile(`(?:\bError` + jsWS + `*:|\bException` + jsWS + `*:|\bTraceback\b|\bat` +
		jsWS + `+` + jsNotWS + `+` + jsWS + `*\([^)]*:\d+:\d+\))`)
)

// userDecision ports extractUserDecision (X:2218-2240): a non-question with
// a letter, a clause separator and 15..500 code points.
func userDecision(msg string) []store.Event {
	n := hookio.RuneLen(msg)
	if questionMark.MatchString(msg) || !letter.MatchString(msg) || !clauseSep.MatchString(msg) || n < 15 || n > 500 {
		return nil
	}
	return []store.Event{ev("decision", "decision", 2, msg)}
}

// Role cue lists, verbatim from X:2294-2320; the trailing spaces matter.
var (
	roleFillers  = map[string]bool{"ok": true, "okay": true, "sure": true, "yeah": true, "yep": true, "yup": true, "alright": true, "fine": true, "well": true, "so": true, "hmm": true, "right": true, "please": true}
	rolePrefixes = []string{
		"you are", "you're", "your role", "you will be", "you act", "you will act",
		"act as", "act like", "behave as", "behave like", "imagine you", "pretend you",
		"assume the role", "take the role", "play the role", "respond as",
		"tu es", "tu est", "vous etes", "vous êtes",
		"sen ", "siz ",
		"eres ", "tú eres", "usted es",
		"ты ", "вы ",
		"あなたは", "君は", "お前は", "あなたが",
		"你是", "您是",
		"तुम ", "आप ", "तू ",
		"أنت ", "انت ", "أنتَ ",
		// ROLE_DIRECTIVE_PREFIXES
		"always ", "never ", "respond ", "reply ", "answer ", "speak ",
		"write ", "prefer ", "format ", "output ", "communicate ", "use only ",
	}
)

// userRole ports extractRole (X:2341-2374), tested on the first clause.
func userRole(msg string) []store.Event {
	first := msg
	if i := strings.IndexAny(msg, ".!\n。！"); i >= 0 {
		first = msg[:i]
	}
	first = jsTrim(first)
	n := hookio.RuneLen(first)
	if questionMark.MatchString(first) || clauseSep.MatchString(first) || !letter.MatchString(first) || n < 8 || n > 120 {
		return nil
	}
	if !hasRoleCue(first) || !(twoWords.MatchString(first) || letterRun.MatchString(first)) {
		return nil
	}
	return []store.Event{ev("role", "role", 3, msg)}
}

// hasRoleCue ports X:2322-2339: drop leading filler words, then match a
// persona or directive prefix.
// shortcut: strings.ToLower maps per rune, JS toLowerCase is full (U+0130 becomes "i̇"); no listed prefix depends on it.
func hasRoleCue(first string) bool {
	var toks []string
	for _, t := range strings.Split(jsTrim(strings.ToLower(first)), " ") {
		if t != "" {
			toks = append(toks, t)
		}
	}
	for len(toks) > 0 && roleFillers[toks[0]] {
		toks = toks[1:]
	}
	norm := strings.Join(toks, " ")
	for _, p := range rolePrefixes {
		if norm != "" && strings.HasPrefix(norm, p) {
			return true
		}
	}
	return false
}

// userIntent ports extractIntent (X:2413-2433).
func userIntent(msg string) []store.Event {
	mode := ""
	switch {
	case questionMark.MatchString(msg):
		mode = "investigate"
	case letter.MatchString(msg) && hookio.RuneLen(msg) < 60:
		mode = "implement"
	}
	if mode == "" {
		return nil
	}
	return []store.Event{ev("intent", "intent", 4, mode)}
}

// userGoal ports extractGoal (X:2446-2462).
func userGoal(msg string) []store.Event {
	m := goalDirective.FindStringSubmatch(msg)
	if m == nil {
		return nil
	}
	goal := jsTrim(m[1])
	if goal == "" {
		return nil
	}
	return []store.Event{ev("goal", "goal", 4, goal)}
}

// userBlocker ports extractBlocker (X:2492-2520); resolved wins.
func userBlocker(msg string) []store.Event {
	if resolvedMark.MatchString(msg) || resolvedPrefix.MatchString(msg) {
		return []store.Event{ev("blocker_resolved", "blocked-on", 2, msg)}
	}
	if blockerMarkers.MatchString(msg) {
		return []store.Event{ev("blocker", "blocked-on", 2, msg)}
	}
	return nil
}
