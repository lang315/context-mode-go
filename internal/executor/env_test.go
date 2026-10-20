package executor

import (
	"os"
	"strings"
	"testing"
)

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

func TestBuildEnvDenylist(t *testing.T) {
	parent := []string{
		"NODE_OPTIONS=--require /evil.js",
		"BASH_FUNC_x%%=() { echo pwned; }",
		"LD_PRELOAD=/evil.so",
		"DYLD_INSERT_LIBRARIES=/evil.dylib",
		"BASH_ENV=/evil.sh",
		"GIT_SSH_COMMAND=evil",
		"RUSTC_WRAPPER=evil",
		"COMPlus_EnableDiagnostics=1",
		"complus_lower=1",
		"DOTNET_DiagnosticPorts=x",
		"PYTHONPATH=/keep",
		"KEEP_ME=yes",
		"HOME=/home/u",
		"PATH=/opt/bin:/usr/bin",
		"LANG=C",
		"TMPDIR=/somewhere/else",
	}
	m := envMap(buildEnv(parent, "/tmp/.cmgo-x"))
	for _, k := range []string{"NODE_OPTIONS", "BASH_FUNC_x%%", "LD_PRELOAD", "DYLD_INSERT_LIBRARIES",
		"BASH_ENV", "GIT_SSH_COMMAND", "RUSTC_WRAPPER", "COMPlus_EnableDiagnostics", "complus_lower", "DOTNET_DiagnosticPorts"} {
		if _, ok := m[k]; ok {
			t.Errorf("%s not removed", k)
		}
	}
	want := map[string]string{
		"PYTHONPATH": "/keep", "KEEP_ME": "yes", "HOME": "/home/u", "PATH": "/opt/bin:/usr/bin",
		"TMPDIR": "/tmp/.cmgo-x", "LANG": "en_US.UTF-8", "PYTHONDONTWRITEBYTECODE": "1",
		"PYTHONUNBUFFERED": "1", "PYTHONUTF8": "1", "NO_COLOR": "1",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %q; want %q", k, m[k], v)
		}
	}
}

func TestBuildEnvDefaults(t *testing.T) {
	m := envMap(buildEnv(nil, "/tmp/.cmgo-y"))
	if m["PATH"] != "/usr/local/bin:/usr/bin:/bin" {
		t.Errorf("PATH default = %q", m["PATH"])
	}
	if m["HOME"] != "/tmp/.cmgo-y" {
		t.Errorf("HOME fallback = %q", m["HOME"])
	}
	wantCert := ""
	for _, p := range certPaths {
		if _, err := os.Stat(p); err == nil {
			wantCert = p
			break
		}
	}
	if m["SSL_CERT_FILE"] != wantCert {
		t.Errorf("SSL_CERT_FILE = %q; want %q", m["SSL_CERT_FILE"], wantCert)
	}
	if got := envMap(buildEnv([]string{"SSL_CERT_FILE=/mine.pem"}, "/t"))["SSL_CERT_FILE"]; got != "/mine.pem" {
		t.Errorf("SSL_CERT_FILE override lost: %q", got)
	}
}
