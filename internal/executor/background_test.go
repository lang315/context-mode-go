package executor

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// bgPgids returns the recorded background process groups.
func bgPgids(e *Executor) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for pgid := range e.bg {
		out = append(out, strconv.Itoa(pgid))
	}
	return out
}

func TestBackgroundSurvivesTimeoutAndShutdown(t *testing.T) {
	e := newTestExec(t)
	t.Cleanup(e.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	res, err := e.Run(ctx, Request{
		Language:   Shell,
		Code:       `echo "$TMPDIR"; sleep 7735`,
		Timeout:    300 * time.Millisecond,
		Background: true,
	})
	if err != nil || !res.Backgrounded || !res.TimedOut || res.ExitCode != 0 {
		t.Fatalf("got %+v, %v", res, err)
	}
	dir := strings.TrimSpace(res.Stdout)
	t.Cleanup(func() { os.RemoveAll(dir) })
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("backgrounded run's temp dir removed: %v", err)
	}
	cancel() // the request ending must not kill a backgrounded group
	time.Sleep(200 * time.Millisecond)
	pgids := bgPgids(e)
	if len(pgids) != 1 || !alive("-g", pgids[0]) || !alive("-f", "sleep 7735") {
		t.Fatalf("backgrounded group not running (pgids %v)", pgids)
	}
	e.Shutdown()
	time.Sleep(200 * time.Millisecond)
	if alive("-g", pgids[0]) || alive("-f", "sleep 7735") {
		t.Fatal("Shutdown left the group running")
	}
	if len(bgPgids(e)) != 0 {
		t.Fatal("Shutdown did not clear the registry")
	}
}

func TestBackgroundChildKeepsWritingAfterLeaderExits(t *testing.T) {
	e := newTestExec(t)
	t.Cleanup(e.Shutdown)
	marker := filepath.Join(t.TempDir(), "marker")
	// The leader exits at 0.6s; its subshell writes for ~4s more (past any 2s WaitDelay),
	// touches the marker, then idles in the same group until Shutdown.
	code := `echo start; ( sleep 1; i=0; while [ $i -lt 8 ]; do echo tick$i; echo err$i >&2; i=$((i+1)); sleep 0.5; done; touch ` +
		shQuote(marker) + `; exec sleep 7736 ) & sleep 0.6`
	res := run(t, e, Request{Language: Shell, Code: code, Timeout: 300 * time.Millisecond, Background: true})
	if !res.Backgrounded || res.Stdout != "start\n" {
		t.Fatalf("got %+v", res)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("marker never appeared: background child was cut off (closed pipe / SIGPIPE)")
		}
		time.Sleep(100 * time.Millisecond)
	}
	pgids := bgPgids(e)
	if len(pgids) != 1 || !alive("-g", pgids[0]) {
		t.Fatalf("long-running group %v not alive", pgids)
	}
	e.Shutdown()
	time.Sleep(200 * time.Millisecond)
	if alive("-g", pgids[0]) || alive("-f", "sleep 7736") {
		t.Fatal("Shutdown left the group running")
	}
}

func TestBackgroundWithoutTimeoutRunsToCompletion(t *testing.T) {
	e := newTestExec(t)
	res := run(t, e, Request{Language: Shell, Code: "echo done", Background: true})
	if res.Backgrounded || res.TimedOut || res.Stdout != "done\n" || res.ExitCode != 0 || len(bgPgids(e)) != 0 {
		t.Fatalf("got %+v", res)
	}
}

func TestBackgroundJSKeepAlive(t *testing.T) {
	e := newTestExec(t)
	if e.runtime(JavaScript) == "" {
		t.Skip("no JS runtime")
	}
	t.Cleanup(e.Shutdown)
	// Without the setInterval keep-alive this would exit at once and not background.
	res := run(t, e, Request{Language: JavaScript, Code: `console.log("up")`, Timeout: 2 * time.Second, Background: true})
	if !res.Backgrounded || strings.TrimSpace(res.Stdout) != "up" {
		t.Fatalf("got %+v", res)
	}
	pgids := bgPgids(e)
	e.Shutdown()
	time.Sleep(200 * time.Millisecond)
	if len(pgids) != 1 || alive("-g", pgids[0]) {
		t.Fatalf("JS group %v still running after Shutdown", pgids)
	}
}

func TestIsSoftFail(t *testing.T) {
	cases := []struct {
		lang   Language
		code   int
		stdout string
		want   bool
	}{
		{Shell, 1, "match\n", true},
		{Shell, 1, " \n\t", false},
		{Shell, 1, "", false},
		{Shell, 2, "match", false},
		{Shell, 0, "match", false},
		{Python, 1, "match", false},
		{JavaScript, 1, "x", false},
	}
	for _, c := range cases {
		if got := IsSoftFail(c.lang, c.code, c.stdout); got != c.want {
			t.Errorf("IsSoftFail(%s, %d, %q) = %v; want %v", c.lang, c.code, c.stdout, got, c.want)
		}
	}
}
