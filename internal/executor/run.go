package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Request is one ctx_execute / ctx_execute_file run.
type Request struct {
	Language   Language
	Code       string
	Timeout    time.Duration // 0 = none
	Background bool
	Cwd        string // "" = project dir
	FilePath   string // non-empty for ctx_execute_file: inject FILE_CONTENT prefix (absolute path)
}

// Result is what the run produced. ExitCode is 1 on timeout and 0 when backgrounded.
type Result struct {
	Stdout, Stderr         string
	ExitCode               int
	TimedOut, Backgrounded bool
	SandboxBytes           int64 // bytes a ctx_execute JS/TS or Python run read through files or the network
}

// Run writes the script to a fresh temp dir, runs it, and removes the dir unless the
// run was backgrounded. The error is only for "cannot run" (missing runtime, temp dir).
// Port of executor.ts:282-330 (execute/executeFile) and 377-407 (rust compile + run).
func (e *Executor) Run(ctx context.Context, r Request) (Result, error) {
	file := r.FilePath
	if file != "" && !filepath.IsAbs(file) {
		file = filepath.Join(e.projectDir, file)
	}
	dir, err := os.MkdirTemp(osTempDir(), ".cmgo-")
	if err != nil {
		return Result{}, err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(dir)
		}
	}()
	code := prepareCode(r.Language, r.Code, file, e.projectDir, r.Background)
	script, err := writeScript(dir, r.Language, code, os.Getenv("PATH"))
	if err != nil {
		return Result{}, err
	}
	argv, err := e.command(r.Language, script)
	if err != nil {
		return Result{}, err
	}
	cwd := r.Cwd
	if cwd == "" {
		cwd = e.projectDir
	}
	env := buildEnv(os.Environ(), dir)

	countsFile := ""
	if r.Language == Python && file == "" && !r.Background && e.getenv("CMGO_PY_COUNTERS") != "0" {
		countsFile = filepath.Join(dir, ".counts")
		argv = pyArgv(argv[0], countsFile, script)
	}

	if r.Language == Rust {
		bin := strings.TrimSuffix(script, ".rs")
		limit := 60 * time.Second // a hung compile must not run forever
		if r.Timeout > 0 && r.Timeout < limit {
			limit = r.Timeout
		}
		c := e.spawn(ctx, []string{argv[0], script, "-o", bin}, dir, env, limit, false)
		if c.ExitCode != 0 {
			msg := c.Stderr
			if msg == "" && c.TimedOut {
				msg = "spawnSync rustc ETIMEDOUT"
			}
			return Result{Stderr: "Compilation failed:\n" + msg, ExitCode: 1}, nil
		}
		argv = []string{bin}
	}

	res := e.spawn(ctx, argv, cwd, env, r.Timeout, r.Background)
	keep = res.Backgrounded
	if countsFile != "" {
		res.SandboxBytes = readPyCounts(countsFile)
	}
	if strings.HasPrefix(code, jsCounters) { // only a wrapJS run reports counts
		res.Stderr, res.SandboxBytes = takeCounters(res.Stderr)
	}
	return res, nil
}

// spawn runs argv in its own process group. Port of executor.ts:409-571.
// Timeout or the output cap SIGKILLs the whole group; with background the group is
// left running, its pgid recorded for Shutdown, and the partial output returned.
func (e *Executor) spawn(ctx context.Context, argv []string, cwd string, env []string, timeout time.Duration, background bool) Result {
	// Not context.WithTimeout: a backgrounded run must outlive both the timeout and ctx.
	runCtx, kill := context.WithCancel(context.Background())
	detached := false
	defer func() {
		if !detached {
			kill()
		}
	}()
	stop := context.AfterFunc(ctx, kill)
	defer stop()

	out := &capture{max: e.hardCap, kill: kill}
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = cwd, env
	cmd.Stdout, cmd.Stderr = stream{out, &out.stdout}, stream{out, &out.stderr}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	if background {
		cmd.WaitDelay = 0 // never close a detached child's pipes; TS drains until they close
	}
	if err := cmd.Start(); err != nil {
		return Result{Stderr: err.Error(), ExitCode: 1}
	}
	pgid := cmd.Process.Pid
	e.mu.Lock()
	e.fg[pgid] = struct{}{}
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.fg, pgid)
		e.mu.Unlock()
	}()
	done := make(chan error, 1) // buffered: a backgrounded run's Wait finishes with no reader
	go func() { done <- cmd.Wait() }()

	var expired <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		expired = t.C
	}
	timedOut := false
	select {
	case <-done:
		if !background {
			syscall.Kill(-pgid, syscall.SIGKILL) // leftover grandchildren; ESRCH when none, ignored
		}
	case <-expired:
		timedOut = true
		if background {
			stop()
			detached = true
			e.mu.Lock()
			e.bg[cmd.Process.Pid] = struct{}{}
			e.mu.Unlock()
			stdout, stderr := out.detach()
			return Result{Stdout: stdout, Stderr: stderr, ExitCode: 0, TimedOut: true, Backgrounded: true}
		}
		kill()
		<-done
	}

	code := 1 // signaled (ExitCode -1), timed out, or no ProcessState
	if ps := cmd.ProcessState; ps != nil && ps.ExitCode() >= 0 && !timedOut {
		code = ps.ExitCode()
	}
	out.mu.Lock()
	defer out.mu.Unlock()
	stderr := out.stderr.String()
	if out.exceeded {
		stderr += fmt.Sprintf("\n[output capped at %.0fMB — process killed]", float64(e.hardCap)/1024/1024)
	}
	return Result{Stdout: out.stdout.String(), Stderr: stderr, ExitCode: code, TimedOut: timedOut}
}

// capture collects stdout and stderr under one combined byte cap.
type capture struct {
	mu             sync.Mutex
	stdout, stderr bytes.Buffer
	total, max     int64
	exceeded       bool
	discard        bool // set once backgrounded: drain, keep nothing
	kill           func()
}

// detach snapshots the output and switches both streams to discard.
func (c *capture) detach() (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.discard = true
	return c.stdout.String(), c.stderr.String()
}

type stream struct {
	c   *capture
	buf *bytes.Buffer
}

// Write never fails: an error would abort Go's pipe copy and leave the child blocked.
// Like TS, the chunk that crosses the cap is dropped whole and the group killed once.
func (s stream) Write(p []byte) (int, error) {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.discard {
		return len(p), nil
	}
	c.total += int64(len(p))
	if c.total <= c.max {
		s.buf.Write(p)
	} else if !c.exceeded {
		c.exceeded = true
		c.kill()
	}
	return len(p), nil
}
