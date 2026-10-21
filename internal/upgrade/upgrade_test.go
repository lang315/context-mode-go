package upgrade

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// rec records every command and fails the one whose name and args start with failOn.
type rec struct {
	runs   []string
	failOn string
}

func (r *rec) run(dir string, env []string, name string, args ...string) error {
	line := strings.Join(append([]string{name}, args...), " ")
	r.runs = append(r.runs, "dir="+dir+" env="+strings.Join(env, ",")+" "+line)
	if r.failOn != "" && strings.HasPrefix(line, r.failOn) {
		return errors.New("exit status 1")
	}
	return nil
}

// opts returns Options with env vars from vars, go and git on PATH, and a
// recorder. `go env GOBIN GOPATH` answers from vars["GOBIN"] and vars["GOPATH"],
// as the real go would for variables set in the environment.
func opts(vars map[string]string) (Options, *rec, *bytes.Buffer) {
	r := &rec{}
	var out bytes.Buffer
	return Options{
		Getenv:   func(k string) string { return vars[k] },
		LookPath: func(n string) (string, error) { return "/usr/bin/" + n, nil },
		Run:      r.run,
		Output: func(name string, args ...string) (string, error) {
			return vars["GOBIN"] + "\n" + vars["GOPATH"] + "\n", nil
		},
		Stdout: &out,
	}, r, &out
}

// sourceDir makes a checkout whose go.mod names module.
func sourceDir(t *testing.T, module string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("// cmgo\nmodule "+module+"\n\ngo 1.27.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestUpgradeFromSource(t *testing.T) {
	dir := sourceDir(t, Module)
	for _, viaEnv := range []bool{false, true} {
		vars := map[string]string{"GOBIN": "/gobin", "GOPATH": "/gopath"}
		o, r, out := opts(vars)
		if viaEnv {
			vars["CMGO_SOURCE"] = dir
		} else {
			o.Source = dir
			vars["CMGO_SOURCE"] = "/ignored"
		}
		if err := Upgrade(o); err != nil {
			t.Fatalf("viaEnv=%v: %v\n%s", viaEnv, err, out)
		}
		wantRuns := []string{
			"dir= env= git -C " + dir + " pull --ff-only",
			"dir=" + dir + " env= go install ./cmd/cmgo ./cmd/cmgo-hook",
			"dir= env= /gobin/cmgo install",
		}
		if !slices.Equal(r.runs, wantRuns) {
			t.Errorf("viaEnv=%v: runs\n%s\nwant\n%s", viaEnv, strings.Join(r.runs, "\n"), strings.Join(wantRuns, "\n"))
		}
		wantOut := "[x] source " + dir + " is " + Module + "\n" +
			"[x] git -C " + dir + " pull --ff-only\n" +
			"[x] go install ./cmd/cmgo ./cmd/cmgo-hook (in " + dir + ")\n" +
			"[x] /gobin/cmgo install\n"
		if out.String() != wantOut {
			t.Errorf("viaEnv=%v: output\n%s\nwant\n%s", viaEnv, out, wantOut)
		}
	}
}

func TestUpgradeFromModule(t *testing.T) {
	cases := []struct {
		name      string
		vars      map[string]string
		goprivate string
		gobin     string
	}{
		{"GOPATH bin", map[string]string{"GOPATH": "/gp1:/gp2"}, "github.com/lang315/*", "/gp1/bin"},
		{"HOME go bin, GOPRIVATE kept", map[string]string{"HOME": "/home/u", "GOPRIVATE": "example.com/x"}, "example.com/x,github.com/lang315/*", "/home/u/go/bin"},
	}
	for _, c := range cases {
		o, r, out := opts(c.vars)
		if err := Upgrade(o); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		wantRuns := []string{
			"dir= env=GOPRIVATE=" + c.goprivate + " go install " + Module + "/cmd/cmgo@latest " + Module + "/cmd/cmgo-hook@latest",
			"dir= env= " + c.gobin + "/cmgo install",
		}
		if !slices.Equal(r.runs, wantRuns) {
			t.Errorf("%s: runs\n%s", c.name, strings.Join(r.runs, "\n"))
		}
		wantOut := "[x] GOPRIVATE=" + c.goprivate + " go install " + Module + "/cmd/cmgo@latest " + Module + "/cmd/cmgo-hook@latest\n" +
			"[x] " + c.gobin + "/cmgo install\n"
		if out.String() != wantOut {
			t.Errorf("%s: output\n%s", c.name, out)
		}
	}
}

func TestUpgradeDryRun(t *testing.T) {
	o, r, out := opts(map[string]string{"GOBIN": "/gobin"})
	o.DryRun = true
	if err := Upgrade(o); err != nil {
		t.Fatal(err)
	}
	if len(r.runs) != 0 {
		t.Errorf("dry run ran %v", r.runs)
	}
	want := "dry run: GOPRIVATE=github.com/lang315/* go install " + Module + "/cmd/cmgo@latest " + Module + "/cmd/cmgo-hook@latest\n" +
		"dry run: /gobin/cmgo install\n"
	if out.String() != want {
		t.Errorf("output\n%s", out)
	}
}

func TestUpgradeStopsAtFirstFailure(t *testing.T) {
	dir := sourceDir(t, Module)
	o, r, out := opts(map[string]string{"GOBIN": "/gobin"})
	o.Source = dir
	r.failOn = "go install"
	if err := Upgrade(o); err == nil {
		t.Fatal("no error")
	}
	if len(r.runs) != 2 {
		t.Errorf("runs after the failure: %v", r.runs)
	}
	if !strings.HasSuffix(out.String(), "[ ] go install ./cmd/cmgo ./cmd/cmgo-hook (in "+dir+") — exit status 1\n") {
		t.Errorf("output\n%s", out)
	}
}

func TestUpgradeBadSourceAndMissingTools(t *testing.T) {
	other := sourceDir(t, "example.com/other")
	o, r, out := opts(map[string]string{"GOBIN": "/gobin"})
	o.Source = other
	if err := Upgrade(o); err == nil || len(r.runs) != 0 {
		t.Fatalf("other module: err %v, runs %v", err, r.runs)
	}
	if want := "[ ] source " + other + " is " + Module + " — go.mod names example.com/other\n"; out.String() != want {
		t.Errorf("other module output %q", out)
	}

	o, r, out = opts(map[string]string{"GOBIN": "/gobin"})
	o.Source = sourceDir(t, Module)
	o.LookPath = func(n string) (string, error) {
		if n == "git" {
			return "", exec.ErrNotFound
		}
		return "/usr/bin/" + n, nil
	}
	if err := Upgrade(o); err == nil || len(r.runs) != 0 {
		t.Fatalf("no git: err %v, runs %v", err, r.runs)
	}
	if !strings.HasSuffix(out.String(), " pull --ff-only — git not found on PATH; install git\n") {
		t.Errorf("no git output %q", out)
	}

	o, r, out = opts(map[string]string{"GOBIN": "/gobin"})
	o.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	if err := Upgrade(o); err == nil || len(r.runs) != 0 {
		t.Fatalf("no go: err %v, runs %v", err, r.runs)
	}
	if !strings.HasSuffix(out.String(), "@latest — go not found on PATH; install Go from https://go.dev/dl\n") {
		t.Errorf("no go output %q", out)
	}
}

// The bin dir comes from `go env GOBIN GOPATH`, which sees values set with
// `go env -w`, not from $GOBIN and $GOPATH (spec §2).
func TestUpgradeBinDirFromGoEnv(t *testing.T) {
	cases := []struct {
		name, goenv string
		err         error
		want        string
	}{
		{"GOBIN wins", "/set/by/go/env/w\n/gp\n", nil, "/set/by/go/env/w"},
		{"first GOPATH entry", "\n/gp1:/gp2\n", nil, "/gp1/bin"},
		{"go env fails: ~/go/bin", "", errors.New("exec: go: not found"), "/home/u/go/bin"},
		{"both empty: ~/go/bin", "\n\n", nil, "/home/u/go/bin"},
	}
	for _, c := range cases {
		o, r, _ := opts(map[string]string{"HOME": "/home/u", "GOBIN": "/env-gobin", "GOPATH": "/env-gopath"}) // the env must not be read
		var asked []string
		o.Output = func(name string, args ...string) (string, error) {
			asked = append([]string{name}, args...)
			return c.goenv, c.err
		}
		if err := Upgrade(o); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := strings.Join(asked, " "); got != "go env GOBIN GOPATH" {
			t.Errorf("%s: asked %q", c.name, got)
		}
		if last := r.runs[len(r.runs)-1]; !strings.HasSuffix(last, " "+c.want+"/cmgo install") {
			t.Errorf("%s: last run %q, want %s/cmgo install", c.name, last, c.want)
		}
	}
}
