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
	"strconv"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/capture"
	"github.com/lang315/context-mode-go/internal/compact"
	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/memory"
	"github.com/lang315/context-mode-go/internal/procinfo"
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

// procLookup reads the process table for the Claude Code pid that
// SessionStart records; a var so tests need not run under Claude Code.
var procLookup = procinfo.System()

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
// is appended to $CMGO_DIR/hook-errors.log and nothing more is printed. The
// statusline event is not a hook fire and has its own rules (statusLine).
func Run(event string, hio IO) int {
	if event == "statusline" {
		statusLine(hio)
		return 0
	}
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

// openStore opens cmgo.db with the hooks' short busy timeout.
func (h *hookCtx) openStore() (*store.Store, error) {
	return store.OpenWith(h.cfg.DBPath, store.Options{BusyTimeout: dbBusyTimeout})
}

// insertEvents writes evs in one transaction; with none it does not open the
// store at all (spec §1.1).
func (h *hookCtx) insertEvents(sourceHook string, evs []store.Event) error {
	if len(evs) == 0 {
		return nil
	}
	st, err := h.openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	// A session started before cmgo was installed is linked here, so the
	// server's stats reach it (stats spec §1.2).
	_, err = st.InsertEventsAndLink(h.sid, h.cfg.ProjectKey, sourceHook, evs, func() (int, time.Time) {
		return procinfo.Claude(os.Getpid(), procLookup)
	})
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
	cwd, _ := h.in.String("cwd") // relative paths in a Bash command resolve against it
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	dec := routing.Route(tool, input, subagent, routing.Deps{
		Cwd:        cwd,
		Home:       h.cfg.Home,
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

// postToolUse compacts a large main-session Bash result (bash-compact spec
// §1), then records the kept extractors' events (spec §3.3) from the original
// response. A failed compaction prints nothing, so the model keeps the
// original; its error is returned with the events' error, to be logged.
func postToolUse(h *hookCtx) error {
	tool, _ := h.in.String("tool_name")
	input, _ := h.in.Object("tool_input")
	resp, _ := h.in.Get("tool_response") // nil when absent
	cerr := compactBash(h, tool, input)
	return errors.Join(h.insertEvents("PostToolUse", capture.PostToolUse(tool, input, resp, h.cfg.Home)), cerr)
}

// compactBash replaces a large main-session Bash result with compact.Text
// after indexing the original as "bash:<tool_use_id>" (bash-compact spec
// §1-§2). It prints nothing whenever a condition fails or the store write
// fails.
func compactBash(h *hookCtx, tool string, input *hookio.Object) error {
	if tool != "Bash" || input == nil || notNull(h.in, "agent_id") || notNull(h.in, "agent_type") {
		return nil
	}
	threshold := compact.MinBytes(h.getenv)
	resp, ok := h.in.Object("tool_response")
	if threshold == 0 || !ok {
		return nil
	}
	stdout, _ := resp.String("stdout")
	stderr, _ := resp.String("stderr")
	output := compact.Output(stdout, stderr)
	if len(output) <= threshold || len(output) > compact.MaxBytes {
		return nil
	}
	command, _ := input.String("command")
	id, _ := h.in.String("tool_use_id")
	if id == "" || strings.Contains(command, compact.FullMarker) || routing.ReadsFiles(command) || !routing.MCPReady(h.runDir) {
		return nil
	}
	label := "bash:" + id
	text := compact.Text(output, label)
	if len(text) >= len(output) {
		return nil
	}
	st, err := h.openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	kept := min(int64(len(output)-len(text)), compact.MaxBytes)
	if err := st.IndexBashOutput(h.cfg.ProjectKey, label, h.sid, store.ChunkPlainText(output, 20), int64(len(text)), kept); err != nil {
		return err
	}
	interrupted, _ := resp.Bool("interrupted")
	_, err = io.WriteString(h.out, `{"hookSpecificOutput":{"hookEventName":"PostToolUse","updatedToolOutput":{"stdout":`+
		string(hookio.QuoteString(text))+`,"stderr":"","interrupted":`+strconv.FormatBool(interrupted)+`,"isImage":false}}}`+"\n")
	return err
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
	st, err := h.openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	if _, _, err := st.CompactSession(h.sid, h.cfg.ProjectKey); err != nil {
		return err
	}
	return werr
}

// sessionStart prints the routing block for every source (spec §4), then
// records the session's Claude Code pid (stats spec §1.2). Startup also
// prunes old guidance markers and records the rule and session_start events;
// compact and resume add the session's memory (spec §2.2).
func sessionStart(h *hookCtx) error {
	block := routing.RoutingBlock(true, false)
	src, _ := h.in.String("source")
	if raw, ok := h.in.Get("source"); !ok || string(raw) == "null" {
		src = "startup" // TS: input.source ?? "startup"
	}
	if src == "compact" || src == "resume" {
		return h.sessionResume(src, block)
	}
	if err := h.printSessionStart(block); err != nil {
		return err
	}
	var evs []store.Event
	if src == "startup" {
		routing.PruneGuidance(h.runDir, h.now, guidanceMaxAge)
		evs = append(capture.SessionStartRules(h.cfg.Home, h.cfg.ProjectDir), capture.SessionStartEvent(src, h.cfg.ProjectDir, h.now))
	}
	st, err := h.openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	return h.recordSession(st, evs)
}

// recordSession writes the session's Claude Code pid first: a server call
// written before it is tagged with the previous session's window (stats spec
// §1.2). Then it records evs.
func (h *hookCtx) recordSession(st *store.Store, evs []store.Event) error {
	if err := st.SetSessionCC(h.sid, h.cfg.ProjectKey, procinfo.ClaudePID(os.Getpid(), procLookup)); err != nil {
		return err
	}
	_, err := st.InsertEvents(h.sid, h.cfg.ProjectKey, "SessionStart", evs)
	return err
}

func (h *hookCtx) printSessionStart(context string) error {
	_, err := io.WriteString(h.out, `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":`+
		string(hookio.QuoteString(context))+"}}\n")
	return err
}

// sessionResume appends the directive (and, on compact, the auto-injection)
// built from the session's events to block, then records the resume events,
// as TS sessionstart.mjs:191-280 does. TS also indexed an events markdown;
// cmgo does not (spec §2.3). The directive needs the events, so the read
// comes before the output; a failed read prints block alone. Writes come
// after the output.
func (h *hookCtx) sessionResume(src, block string) error {
	st, err := h.openStore()
	if err != nil {
		return errors.Join(err, h.printSessionStart(block))
	}
	defer st.Close()
	evs, err := st.SessionEvents(h.sid)
	if err != nil {
		return errors.Join(err, h.printSessionStart(block))
	}
	context := block + memory.Directive(src, evs) // "" with no events
	var add []store.Event
	if len(evs) > 0 && src == "compact" {
		if ai := memory.AutoInjection(evs); ai != "" {
			context += "\n\n" + ai
		}
		add = append(add, store.Event{Type: "resume_completed", Category: "session-resume", Priority: 1,
			Data: "Session resumed from compact. Prior events loaded."})
	}
	if err := h.printSessionStart(context); err != nil {
		return err
	}
	return h.recordSession(st, append(add, capture.SessionStartEvent(src, h.cfg.ProjectDir, h.now)))
}
