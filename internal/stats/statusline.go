package stats

import "strconv"

// Line holds the statusline numbers (spec §3). OK is false when the DB, the
// session or a query is missing; Pct is Pct(session kept, session raw).
type Line struct {
	OK                        bool
	SessionKept, LifetimeKept int64
	Pct                       int
}

// StatusLine renders the one statusline line, with no newline and no ANSI.
func StatusLine(l Line) string {
	switch {
	case !l.OK:
		return "cmgo  ●  ready"
	case l.SessionKept == 0:
		return "cmgo  ●  " + KB(l.LifetimeKept) + " kept out lifetime"
	}
	return "cmgo  ●  " + KB(l.SessionKept) + " this chat  ·  " + KB(l.LifetimeKept) + " lifetime  ·  " + strconv.Itoa(l.Pct) + "% kept out"
}
