// Package hookcmd runs one Claude Code hook fire (hooks spec §1-§4). It is
// what cmgo-hook executes; it links no MCP server, executor or fetcher, so a
// hook process starts fast.
package hookcmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/lang315/context-mode-go/internal/capture"
	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
)

// stdinTimeout bounds the stdin read (spec §1.2). It is a fallback: Claude
// Code closes stdin. A var so the test need not wait 2 s.
var stdinTimeout = 2 * time.Second

// dbBusyTimeout bounds every wait for the shared DB's lock. Claude Code waits
// for the hook process to exit, so on BUSY the hook drops its events and
// logs one line (spec §1.2) instead of stalling the session.
const dbBusyTimeout = 2 * time.Second

// guidanceMaxAge is how long a guidance marker dir is kept (spec §4).
const guidanceMaxAge = 7 * 24 * time.Hour

// IO is what a hook fire talks over; tests inject env, cwd, stdin and clock.
type IO struct {
	Getenv func(string) string
	Getwd  func() (string, error)
	Stdin  io.Reader
	Stdout io.Writer
	Now    func() time.Time
}

// hookCtx is one parsed hook fire.
type hookCtx struct {
	in     *hookio.Object
	cfg    config.Config
	sid    string
	runDir string // $CMGO_DIR/run: guidance markers and MCP sentinels
	getenv func(string) string
	out    io.Writer
	now    time.Time
}

// hookHandlers maps a hook event name to its handler.
var hookHandlers = map[string]func(*hookCtx) error{
	"pretooluse":         preToolUse,
	"posttooluse":        postToolUse,
	"posttoolusefailure": postToolUseFailure,
	"userpromptsubmit":   userPromptSubmit,
	"stop":               stop,
	"sessionstart":       sessionStart,
	"precompact":         preCompact,
}

// Run runs one hook fire and always exits 0 (spec §1.2): an error or panic
// is appended to $CMGO_DIR/hook-errors.log and nothing more is printed.
func Run(event string, hio IO) int {
	h, ok := hookHandlers[event]
	if !ok {
		return 0
	}
	defer func() {
		if r := recover(); r != nil {
			logHookError(hio.Getenv, event, fmt.Errorf("panic: %v", r))
		}
	}()
	if err := runHook(h, hio); err != nil {
		logHookError(hio.Getenv, event, err)
	}
	return 0
}

func runHook(h func(*hookCtx) error, hio IO) error {
	raw, err := readStdin(hio.Stdin)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil // spec §1.2: empty stdin, no output
	}
	in, err := hookio.ParseObject(raw)
	if err != nil {
		return err
	}
	getwd := hio.Getwd
	if cwd, ok := in.String("cwd"); ok && cwd != "" {
		getwd = func() (string, error) { return cwd, nil }
	}
	cfg, err := config.Load(hio.Getenv, getwd)
	if err != nil {
		return err
	}
	return h(&hookCtx{
		in:     in,
		cfg:    cfg,
		sid:    hookio.SessionID(in, hio.Getenv, os.Getppid()),
		runDir: filepath.Join(cfg.DataDir, "run"),
		getenv: hio.Getenv,
		out:    hio.Stdout,
		now:    hio.Now(),
	})
}

// readStdin reads the whole payload, giving up after stdinTimeout. The
// reader goroutine is left blocked on timeout; the process exits anyway.
func readStdin(r io.Reader) ([]byte, error) {
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := hookio.ReadInput(r)
		ch <- result{b, err}
	}()
	select {
	case res := <-ch:
		return res.b, res.err
	case <-time.After(stdinTimeout):
		return nil, errors.New("stdin: no end of input within " + stdinTimeout.String())
	}
}

// logHookError appends one line to $CMGO_DIR/hook-errors.log, best effort.
func logHookError(getenv func(string) string, event string, err error) {
	// The project dir does not affect DataDir, so a failed parse can still log.
	cfg, cerr := config.Load(getenv, func() (string, error) { return "/", nil })
	if cerr != nil {
		return
	}
	if os.MkdirAll(cfg.DataDir, 0o755) != nil {
		return
	}
	f, ferr := os.OpenFile(filepath.Join(cfg.DataDir, "hook-errors.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s pid=%d %s: %v\n", time.Now().Format(time.RFC3339), os.Getpid(), event, err)
}

// insertEvents writes evs in one transaction; with none it does not open the
// store at all (spec §1.1).
func (h *hookCtx) insertEvents(sourceHook string, evs []store.Event) error {
	if len(evs) == 0 {
		return nil
	}
	st, err := store.OpenWith(h.cfg.DBPath, store.Options{BusyTimeout: dbBusyTimeout})
	if err != nil {
		return err
	}
	defer st.Close()
	_, err = st.InsertEvents(h.sid, h.cfg.ProjectKey, sourceHook, evs)
	return err
}

// notNull is JS `input[key] != null`: present and not JSON null.
func notNull(in *hookio.Object, key string) bool {
	raw, ok := in.Get(key)
	return ok && string(bytes.TrimSpace(raw)) != "null"
}

// preToolUse routes the call (spec §2) and writes the decision before it
// opens the store, so the DB never delays Claude Code.
func preToolUse(h *hookCtx) error {
	tool, _ := h.in.String("tool_name")
	input, ok := h.in.Object("tool_input")
	if !ok {
		input = &hookio.Object{}
	}
	subagent := notNull(h.in, "agent_id") || notNull(h.in, "agent_type")
	dec := routing.Route(tool, input, subagent, routing.Deps{
		ProjectDir: h.cfg.ProjectDir,
		SessionID:  h.sid,
		RunDir:     h.runDir,
		Policy:     security.NewPolicyDir(h.cfg.ProjectDir, h.cfg.ClaudeDir),
		Getenv:     h.getenv,
		MCPReady:   func() bool { return routing.MCPReady(h.runDir) },
	})
	if out := routing.Format(dec); out != nil {
		if _, err := h.out.Write(out); err != nil {
			return err
		}
	}
	return h.insertEvents("PreToolUse", routing.Events(tool, dec))
}

// postToolUse records the kept extractors' events (spec §3.3).
func postToolUse(h *hookCtx) error {
	tool, _ := h.in.String("tool_name")
	input, _ := h.in.Object("tool_input")
	resp, _ := h.in.Get("tool_response") // nil when absent
	return h.insertEvents("PostToolUse", capture.PostToolUse(tool, input, resp, h.cfg.Home))
}

func postToolUseFailure(h *hookCtx) error {
	tool, _ := h.in.String("tool_name")
	return h.insertEvents("PostToolUseFailure", capture.PostToolUseFailure(tool, h.in))
}

func userPromptSubmit(h *hookCtx) error {
	prompt, ok := h.in.String("prompt")
	if !ok {
		prompt, _ = h.in.String("message") // TS: input.prompt ?? input.message ?? ""
	}
	return h.insertEvents("UserPromptSubmit", capture.UserPrompt(prompt))
}

// stop always records turn_end, then prints {} as TS stop.mjs does.
func stop(h *hookCtx) error {
	active, _ := h.in.Bool("stop_hook_active")
	var msg *string
	if s, ok := h.in.String("last_assistant_message"); ok {
		msg = &s
	}
	// TS stop.mjs prints after its try/catch: a failed write must not swallow
	// the output, so print first and still return the write error to be logged.
	_, werr := io.WriteString(h.out, "{}\n")
	if err := h.insertEvents("Stop", []store.Event{capture.Stop(active, msg)}); err != nil {
		return err
	}
	return werr
}

// preCompact records the compaction (spec §2.1) and prints {} as TS
// precompact.mjs does, first, so a DB error cannot swallow it.
func preCompact(h *hookCtx) error {
	_, werr := io.WriteString(h.out, "{}\n")
	st, err := store.OpenWith(h.cfg.DBPath, store.Options{BusyTimeout: dbBusyTimeout})
	if err != nil {
		return err
	}
	defer st.Close()
	if _, _, err := st.CompactSession(h.sid, h.cfg.ProjectKey); err != nil {
		return err
	}
	return werr
}

// sessionStart prints the routing block for every source (spec §4). Only
// startup also prunes old guidance markers and records the rule and
// session_start events; resume and compact context is sub-project 3.
func sessionStart(h *hookCtx) error {
	out := `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":` +
		string(hookio.QuoteString(routing.RoutingBlock(false, false))) + "}}\n"
	if _, err := io.WriteString(h.out, out); err != nil {
		return err
	}
	src, _ := h.in.String("source")
	if raw, ok := h.in.Get("source"); !ok || string(raw) == "null" {
		src = "startup" // TS: input.source ?? "startup"
	}
	if src != "startup" {
		return nil
	}
	routing.PruneGuidance(h.runDir, h.now, guidanceMaxAge)
	evs := append(capture.SessionStartRules(h.cfg.Home, h.cfg.ProjectDir), capture.SessionStartEvent(src, h.cfg.ProjectDir, h.now))
	return h.insertEvents("SessionStart", evs)
}
