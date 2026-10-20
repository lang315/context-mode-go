package security

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func realpath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func boundaryMessage(path, project string) string {
	return `File access blocked: "` + path + `" resolves outside the project root (` + project +
		`). context-mode confines ctx_execute_file to the workspace so it cannot be used to bypass ` +
		`the host's sandbox/permission controls (issue #852). To intentionally process a file outside ` +
		`the project, add a host allow rule, e.g. "permissions": { "allow": ["Read(` + path + `)"] } in your settings.`
}

func denyMessage(glob string) string {
	return "File access blocked by security policy: path matches Read deny pattern " + glob
}

// checkErr asserts CheckReadPath(path) returns nil (want == "") or exactly want.
func checkErr(t *testing.T, p *Policy, path, want string) {
	t.Helper()
	err := p.CheckReadPath(path)
	switch {
	case want == "" && err != nil:
		t.Errorf("CheckReadPath(%q) = %q, want nil", path, err)
	case want != "" && (err == nil || err.Error() != want):
		t.Errorf("CheckReadPath(%q) = %v,\nwant %q", path, err, want)
	}
}

func TestCheckReadPathBoundary(t *testing.T) {
	p, project, _, _ := newTestPolicy(t)
	outside := filepath.Join(filepath.Dir(project), "outside-"+filepath.Base(project))
	writeFile(t, filepath.Join(project, "data", "in.txt"), "x")
	writeFile(t, filepath.Join(outside, "secret.txt"), "s")
	symlink(t, filepath.Join(outside, "secret.txt"), filepath.Join(project, "escape.txt"))
	symlink(t, filepath.Join(project, "data", "in.txt"), filepath.Join(project, "alias.txt"))

	checkErr(t, p, "data/in.txt", "")
	checkErr(t, p, filepath.Join(project, "data", "in.txt"), "")
	checkErr(t, p, ".", "")
	checkErr(t, p, "not/created/yet.txt", "")
	checkErr(t, p, "alias.txt", "")
	checkErr(t, p, "data/../data/in.txt", "")

	up := "../" + filepath.Base(outside) + "/secret.txt"
	checkErr(t, p, up, boundaryMessage(up, project))
	abs := filepath.Join(outside, "secret.txt")
	checkErr(t, p, abs, boundaryMessage(abs, project))
	checkErr(t, p, "/etc/passwd", boundaryMessage("/etc/passwd", project))
	checkErr(t, p, "escape.txt", boundaryMessage("escape.txt", project))
}

func TestCheckReadPathAllowRule(t *testing.T) {
	p, project, home, _ := newTestPolicy(t)
	outside := filepath.Join(filepath.Dir(project), "outside-"+filepath.Base(project))
	writeFile(t, filepath.Join(outside, "log.txt"), "l")
	writeFile(t, filepath.Join(outside, "other", "x.txt"), "x")
	symlink(t, filepath.Join(outside, "log.txt"), filepath.Join(project, "log-link.txt"))
	writeFile(t, homeSettings(home), `{"permissions":{"allow":["Read(`+outside+`/*.txt)"]}}`)

	checkErr(t, p, filepath.Join(outside, "log.txt"), "")
	checkErr(t, p, "../"+filepath.Base(outside)+"/log.txt", "")
	// The symlink's candidates are "log-link.txt", <project>/log-link.txt and the
	// target's realpath. The allow glob names the unresolved dir, so on macOS
	// (/var -> /private/var) only an allow rule on the realpath would admit it.
	if realpath(t, outside) == outside {
		checkErr(t, p, "log-link.txt", "")
	} else {
		checkErr(t, p, "log-link.txt", boundaryMessage("log-link.txt", project))
	}
	// * does not cross "/", so a deeper file is still outside and not allowed.
	deep := filepath.Join(outside, "other", "x.txt")
	checkErr(t, p, deep, boundaryMessage(deep, project))
}

func TestCheckReadPathDenyCandidates(t *testing.T) {
	p, project, home, _ := newTestPolicy(t)
	realProject := realpath(t, project)
	writeFile(t, filepath.Join(project, ".env"), "e")
	writeFile(t, filepath.Join(project, "secrets", "key"), "k")
	writeFile(t, filepath.Join(project, "secrets", "real-only"), "r")
	symlink(t, filepath.Join(project, "secrets", "real-only"), filepath.Join(project, "innocent.txt"))
	writeFile(t, sharedSettings(project), `{"permissions":{"deny":["Read(.env)","Read(`+project+`/secrets/key)"]}}`)
	writeFile(t, homeSettings(home), `{"permissions":{"deny":["Read(`+realProject+`/secrets/real-only)"]}}`)

	checkErr(t, p, ".env", denyMessage(".env"))                                   // raw
	checkErr(t, p, "secrets/../secrets/key", denyMessage(project+"/secrets/key")) // resolved
	checkErr(t, p, "innocent.txt", denyMessage(realProject+"/secrets/real-only")) // realpath
	checkErr(t, p, filepath.Join(project, ".env"), "")                            // ".env" glob is anchored
}

func TestCheckReadPathDenyDarwinCaseFolding(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("case folding applies on darwin only")
	}
	p, project, _, _ := newTestPolicy(t)
	writeFile(t, sharedSettings(project), `{"permissions":{"deny":["Read(.env)","Read(**/.env)"]}}`)

	// ".ENV" resolves to <project>/.ENV; the anchored ".env" glob matches its raw form.
	checkErr(t, p, ".ENV", denyMessage(".env"))
	// The absolute form needs a glob that spans directories.
	checkErr(t, p, filepath.Join(project, ".ENV"), denyMessage("**/.env"))
}

func TestCheckReadPathAllowedOutsideStillDenied(t *testing.T) {
	p, project, home, _ := newTestPolicy(t)
	outside := filepath.Join(filepath.Dir(project), "outside-"+filepath.Base(project))
	writeFile(t, filepath.Join(outside, "id_rsa"), "k")
	realOutside := realpath(t, outside)
	symlink(t, filepath.Join(outside, "id_rsa"), filepath.Join(project, "notes.txt"))
	writeFile(t, homeSettings(home), `{"permissions":{"allow":["Read(`+realOutside+`/**)"],"deny":["Read(`+realOutside+`/id_rsa)"]}}`)

	// Boundary passes through the allow rule; deny then matches the realpath.
	checkErr(t, p, "notes.txt", denyMessage(realOutside+"/id_rsa"))
}

func TestCheckReadPathNoProjectDir(t *testing.T) {
	home := t.TempDir()
	p := NewPolicy("", home)
	writeFile(t, homeSettings(home), `{"permissions":{"deny":["Read(/etc/passwd)"]}}`)
	checkErr(t, p, "/etc/hosts", "")
	checkErr(t, p, "/etc/passwd", denyMessage("/etc/passwd"))
}

func TestCheckReadPathDarwinCaseFolding(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("case folding applies on darwin only")
	}
	p, project, home, _ := newTestPolicy(t)
	writeFile(t, filepath.Join(project, "a.txt"), "a")
	outside := filepath.Join(filepath.Dir(project), "outside-"+filepath.Base(project))
	writeFile(t, filepath.Join(outside, "b.txt"), "b")
	writeFile(t, homeSettings(home), `{"permissions":{"allow":["Read(`+strings.ToUpper(outside)+`/*)"]}}`)

	checkErr(t, p, strings.ToUpper(project)+"/a.txt", "") // boundary folds case
	checkErr(t, p, filepath.Join(outside, "b.txt"), "")   // allow folds case
}
