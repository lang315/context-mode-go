package hookcmd

import (
	"io"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/stats"
	"github.com/lang315/context-mode-go/internal/store"
)

// statusLine prints Claude Code's statusLine line (stats spec §3), with no
// trailing newline. It is not a hook: it prints even on empty stdin, and it
// neither logs nor writes, because Claude Code runs it several times a second.
func statusLine(hio IO) {
	io.WriteString(hio.Stdout, stats.StatusLine(statusNumbers(hio)))
}

// statusNumbers reads the line's numbers through a read-only store. Every
// failure, a missing DB or session included, is the zero Line: the fallback.
func statusNumbers(hio IO) (l stats.Line) {
	defer func() {
		if recover() != nil {
			l = stats.Line{}
		}
	}()
	raw, err := readStdin(hio.Stdin)
	if err != nil {
		return l
	}
	sid := hio.Getenv("CLAUDE_SESSION_ID")
	if in, err := hookio.ParseObject(raw); err == nil {
		if s, ok := in.String("session_id"); ok && s != "" {
			sid = s
		}
	}
	if sid == "" {
		return l
	}
	// The project does not affect DBPath, so the cwd is not needed.
	cfg, err := config.Load(hio.Getenv, func() (string, error) { return "/", nil })
	if err != nil {
		return l
	}
	st, err := store.OpenReadOnly(cfg.DBPath)
	if err != nil {
		return l
	}
	defer st.Close()
	if v, err := st.UserVersion(); err != nil || v < 3 { // migration 3 adds tool_stats
		return l
	}
	kept, total, lifetime, err := st.SessionLine(sid)
	if err != nil {
		return l
	}
	return stats.Line{OK: true, SessionKept: kept, LifetimeKept: lifetime, Pct: stats.Pct(kept, total)}
}
