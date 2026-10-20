package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// rpcPeer drives serve() over in-process pipes with newline-delimited JSON-RPC.
type rpcPeer struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *bufio.Reader
	done chan int
	errb *bytes.Buffer
}

func startServe(t *testing.T, env map[string]string) *rpcPeer {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	errb := &bytes.Buffer{}
	p := &rpcPeer{t: t, in: inW, out: bufio.NewReader(outR), done: make(chan int, 1), errb: errb}
	go func() {
		p.done <- serve(context.Background(), serveIO{
			getenv: func(k string) string { return env[k] },
			getwd:  os.Getwd,
			in:     inR,
			out:    outW,
			stderr: errb,
		})
		outW.Close()
	}()
	return p
}

func (p *rpcPeer) send(msg map[string]any) {
	p.t.Helper()
	b, _ := json.Marshal(msg)
	written := make(chan error, 1)
	go func() { _, err := p.in.Write(append(b, '\n')); written <- err }()
	select {
	case err := <-written:
		if err != nil {
			p.t.Fatal(err)
		}
	case code := <-p.done:
		p.t.Fatalf("server exited with %d before reading the request (stderr: %s)", code, p.errb.String())
	case <-time.After(10 * time.Second):
		p.t.Fatalf("server did not read the request within 10s (stderr: %s)", p.errb.String())
	}
}

func (p *rpcPeer) recv(id int) map[string]any {
	p.t.Helper()
	for {
		line, err := p.out.ReadBytes('\n')
		if err != nil {
			p.t.Fatalf("read: %v (stderr: %s)", err, p.errb.String())
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			p.t.Fatalf("bad frame %q: %v", line, err)
		}
		if got, ok := m["id"].(float64); ok && int(got) == id {
			return m
		}
	}
}

func (p *rpcPeer) initialize() {
	p.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "0"},
	}})
	if r := p.recv(1); r["error"] != nil {
		p.t.Fatalf("initialize: %v", r["error"])
	}
	p.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

func testEnv(t *testing.T) map[string]string {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"HOME": filepath.Join(root, "home"), "CMGO_DIR": filepath.Join(root, "data"), "CMGO_PROJECT_DIR": project}
}

func TestServeListsFiveToolsAndExitsOnEOF(t *testing.T) {
	env := testEnv(t)
	p := startServe(t, env)
	p.initialize()
	p.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	r := p.recv(2)
	var names []string
	for _, tool := range r["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	sort.Strings(names)
	want := "ctx_batch_execute,ctx_execute,ctx_execute_file,ctx_fetch_and_index,ctx_search"
	if strings.Join(names, ",") != want {
		t.Fatalf("tools = %v", names)
	}
	p.in.Close()
	select {
	case code := <-p.done:
		if code != 0 {
			t.Fatalf("exit %d: %s", code, p.errb.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not exit after stdin EOF")
	}
	if _, err := os.Stat(filepath.Join(env["CMGO_DIR"], "cmgo.db")); err != nil {
		t.Fatalf("db not created: %v", err)
	}
}

func TestServeFailsOnUnusableDataDir(t *testing.T) {
	env := testEnv(t)
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	env["CMGO_DIR"] = filepath.Join(blocker, "sub") // parent is a file
	p := startServe(t, env)
	select {
	case code := <-p.done:
		if code != 1 || !strings.Contains(p.errb.String(), "cmgo: open") {
			t.Fatalf("exit %d stderr %q", code, p.errb.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve should fail fast")
	}
}

// pgrepCount counts processes whose command line contains pattern.
func pgrepCount(t *testing.T, pattern string) int {
	t.Helper()
	out, _ := exec.Command("pgrep", "-f", pattern).Output()
	return len(strings.Fields(string(out)))
}

// Review focus: Claude Code closing a session while a foreground command is
// still running must not leave that command (or its children) behind.
func TestServeShutdownKillsForegroundRun(t *testing.T) {
	env := testEnv(t)
	p := startServe(t, env)
	p.initialize()
	// Built at runtime so no other command line (e.g. the shell running go test) contains it.
	marker := fmt.Sprintf("sleep %d", 4000+os.Getpid()%5000)
	p.send(map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/call", "params": map[string]any{
		"name": "ctx_execute", "arguments": map[string]any{"language": "shell", "code": marker + " & " + marker + "; wait"},
	}})
	deadline := time.Now().Add(5 * time.Second)
	for pgrepCount(t, marker) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if pgrepCount(t, marker) < 2 {
		t.Fatal("command never started")
	}
	p.in.Close()
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not exit while a foreground command was running")
	}
	deadline = time.Now().Add(3 * time.Second)
	for pgrepCount(t, marker) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := pgrepCount(t, marker); n > 0 {
		exec.Command("pkill", "-f", marker).Run()
		t.Fatalf("%d process(es) left running after shutdown", n)
	}
}

// Review focus: two sessions on the same project share one DB; concurrent
// indexing from both must not surface SQLITE_BUSY to the model.
func TestServeConcurrentSessionsSameDB(t *testing.T) {
	env := testEnv(t)
	a, b := startServe(t, env), startServe(t, env)
	a.initialize()
	b.initialize()
	var wg sync.WaitGroup
	errs := make(chan string, 20)
	for _, p := range []*rpcPeer{a, b} {
		wg.Add(1)
		go func(p *rpcPeer) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				id := 100 + i
				p.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{
					"name": "ctx_batch_execute", "arguments": map[string]any{
						"commands": []any{map[string]any{"label": fmt.Sprintf("seq%d", i), "command": "seq 1 2000"}},
						"queries":  []any{"1999"},
					},
				}})
				r := p.recv(id)
				res, _ := r["result"].(map[string]any)
				if r["error"] != nil || res == nil || res["isError"] == true {
					errs <- fmt.Sprintf("call %d: %v %v", i, r["error"], res)
				}
			}
		}(p)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	a.in.Close()
	b.in.Close()
	<-a.done
	<-b.done
}
