package mcpserver

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolsListIncludesExecuteFile(t *testing.T) {
	checkListedTool(t, "ctx_execute_file", "Run code over a file (executes code, reads the given path)", executeFileDesc, executeFileSchema)
}

func TestExecuteFileShellAndPython(t *testing.T) {
	h := newHarness(t, nil)
	h.writeProjectFile("data.txt", "alpha\nbeta\ngamma\n")

	code := `echo \"$FILE_CONTENT\" | wc -l | tr -d ' '`
	text, isErr := h.call("ctx_execute_file", `{"path":"data.txt","language":"shell","code":"`+code+`"}`)
	if want := "path=data.txt\n```shell\n" + strings.ReplaceAll(code, `\"`, `"`) + "\n```\n\n3\n"; isErr || text != want {
		t.Errorf("shell: got %v %q, want %q", isErr, text, want)
	}

	text, isErr = h.call("ctx_execute_file", `{"path":"data.txt","language":"python","code":"print(FILE_CONTENT.split()[1])","timeout":"2000"}`)
	if want := "path=data.txt\n```python\nprint(FILE_CONTENT.split()[1])\n```\n\nbeta\n"; isErr || text != want {
		t.Errorf("python: got %v %q", isErr, text)
	}
}

func TestExecuteFileTimeout(t *testing.T) {
	h := newHarness(t, nil)
	h.writeProjectFile("data.txt", "x\n")
	text, isErr := h.call("ctx_execute_file", `{"path":"data.txt","language":"shell","code":"echo hi; sleep 10","timeout":300}`)
	if want := "path=data.txt\n```shell\necho hi; sleep 10\n```\n\nTimed out processing data.txt after 300ms"; !isErr || text != want {
		t.Errorf("got %v %q", isErr, text)
	}
}

func TestExecuteFileOutsideProject(t *testing.T) {
	h := newHarness(t, nil)
	outside := filepath.Join(h.Home, "secret.txt")
	text, isErr := h.call("ctx_execute_file", fmt.Sprintf(`{"path":%q,"language":"shell","code":"echo hi"}`, outside))
	want := `File access blocked: "` + outside + `" resolves outside the project root (` + h.Project + `). ` +
		`context-mode confines ctx_execute_file to the workspace so it cannot be used to bypass the host's sandbox/permission controls (issue #852). ` +
		`To intentionally process a file outside the project, add a host allow rule, e.g. "permissions": { "allow": ["Read(` + outside + `)"] } in your settings.`
	if !isErr || text != want {
		t.Errorf("got %v %q", isErr, text)
	}
}

func TestExecuteFileReadDeny(t *testing.T) {
	h := newHarness(t, nil)
	h.writeSettings("Read(**/.env)", "Bash(sudo *)")
	h.writeProjectFile(".env", "TOKEN=x\n")
	h.writeProjectFile("data.txt", "x\n")
	text, isErr := h.call("ctx_execute_file", `{"path":".env","language":"shell","code":"echo hi"}`)
	if !isErr || text != "File access blocked by security policy: path matches Read deny pattern **/.env" {
		t.Errorf("read deny: got %v %q", isErr, text)
	}
	text, isErr = h.call("ctx_execute_file", `{"path":"data.txt","language":"shell","code":"sudo cat \"$FILE_CONTENT_PATH\""}`)
	if !isErr || text != "Command blocked by security policy: matches deny pattern Bash(sudo *)" {
		t.Errorf("code deny: got %v %q", isErr, text)
	}
}

func TestExecuteFileArgErrors(t *testing.T) {
	h := newHarness(t, nil)
	for _, args := range []string{
		`{"language":"shell","code":"echo hi"}`,
		`{"path":"data.txt","language":"cobol","code":"x"}`,
	} {
		if text, isErr := h.call("ctx_execute_file", args); !isErr || !strings.HasPrefix(text, "Invalid arguments: ") {
			t.Errorf("%s: got %v %q", args, isErr, text)
		}
	}
}
