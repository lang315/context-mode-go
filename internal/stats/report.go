package stats

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ToolRow is one tool's summed tool_stats counters.
type ToolRow struct {
	Tool                  string
	Calls, Returned, Kept int64
	Unmeasured            int64 // calls whose kept bytes could not be counted
}

// Category is a session-memory event category and its event count.
type Category struct {
	Name  string
	Count int
}

// Report holds everything the ctx_stats text shows (spec §2).
type Report struct {
	Version       string
	Now           time.Time
	SessionLinked bool
	SessionStart  time.Time // duration = Now - SessionStart
	Tools         []ToolRow // this session's server rows
	// Raw is the bytes processed this session: the sum of Kept + Returned
	// over the rows of tools that have a raw definition (spec §1.3:
	// ctx_execute, ctx_execute_file, ctx_batch_execute, ctx_fetch_and_index).
	// The caller computes it; the headline percentage is Pct(sum Kept, Raw).
	Raw                 int64
	Redirects           int64 // PreToolUse redirects this session (Read tips excluded)
	RedirectBytes       int64 // their bytes_avoided, shown apart from Kept
	Events              int
	Compactions         int
	Categories          []Category // any order; Format shows the top 8
	ProjectSources      int
	ProjectContentBytes int64
	ProjectSessions     int
	ProjectEvents       int
	LifetimeKept        int64 // all tool_stats kept: measured bytes only
	LifetimeUnmeasured  int64 // all ctx_execute calls recorded as not measured
	LifetimeSessions    int
	LifetimeSince       time.Time // earliest started_at; zero hides "since"
}

// Format renders the ctx_stats text, with no trailing newline.
func Format(r Report) string {
	var calls, returned, kept int64
	for _, t := range r.Tools {
		calls, returned, kept = calls+t.Calls, returned+t.Returned, kept+t.Kept
	}
	var b strings.Builder
	b.WriteString("cmgo stats\n\n")
	if r.SessionLinked {
		fmt.Fprintf(&b, "This session  ·  %s  ·  %d calls\n", Duration(r.Now.Sub(r.SessionStart)), calls)
	} else {
		b.WriteString("This session  ·  no hook data yet (is cmgo-hook installed?)\n")
	}
	fmt.Fprintf(&b, "  Kept out of context    %s   (%d%% of %s processed)\n", KB(kept), Pct(kept, r.Raw), KB(r.Raw))
	fmt.Fprintf(&b, "  Returned to context    %s\n", KB(returned))
	if r.SessionLinked {
		fmt.Fprintf(&b, "  Redirected by hooks    %d calls  ·  ~%s (estimated)\n", r.Redirects, KB(r.RedirectBytes))
	}
	tools := slices.Clone(r.Tools)
	slices.SortFunc(tools, func(a, b ToolRow) int {
		return cmp.Or(cmp.Compare(b.Calls, a.Calls), strings.Compare(a.Tool, b.Tool))
	})
	for _, t := range tools {
		fmt.Fprintf(&b, "  %-22s %4d calls  %9s kept  %9s returned", t.Tool, t.Calls, KB(t.Kept), KB(t.Returned))
		if t.Unmeasured > 0 {
			fmt.Fprintf(&b, "  ·  %d not measured", t.Unmeasured)
		}
		b.WriteString("\n")
	}
	if r.SessionLinked {
		fmt.Fprintf(&b, "\nSession memory  ·  %d events  ·  %d compactions\n", r.Events, r.Compactions)
		cats := slices.Clone(r.Categories)
		slices.SortFunc(cats, func(a, b Category) int {
			return cmp.Or(cmp.Compare(b.Count, a.Count), strings.Compare(a.Name, b.Name))
		})
		for _, c := range cats[:min(len(cats), 8)] {
			fmt.Fprintf(&b, "  %-22s %5d\n", c.Name, c.Count)
		}
	}
	fmt.Fprintf(&b, "\nThis project  ·  %d sources indexed (%s)  ·  %d sessions  ·  %d events\n",
		r.ProjectSources, KB(r.ProjectContentBytes), r.ProjectSessions, r.ProjectEvents)
	fmt.Fprintf(&b, "\nAll projects  ·  %s kept out", KB(r.LifetimeKept))
	if r.LifetimeUnmeasured > 0 {
		calls := "calls"
		if r.LifetimeUnmeasured == 1 {
			calls = "call"
		}
		fmt.Fprintf(&b, "  ·  %d ctx_execute %s not measured", r.LifetimeUnmeasured, calls)
	}
	fmt.Fprintf(&b, "  ·  %d sessions", r.LifetimeSessions)
	if !r.LifetimeSince.IsZero() {
		b.WriteString("  ·  since " + r.LifetimeSince.UTC().Format("2006-01-02"))
	}
	b.WriteString("\n\ncmgo " + r.Version)
	return b.String()
}
