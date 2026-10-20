package executor

import (
	"os"
	"strings"
)

// deniedEnv are vars that corrupt sandbox stdout, inject code, or break runtimes.
// Exact port of the DENIED set in executor.ts:578-678.
var deniedEnv = map[string]bool{
	// Shell
	"BASH_ENV": true, "ENV": true, "PROMPT_COMMAND": true, "PS4": true, "SHELLOPTS": true,
	"BASHOPTS": true, "CDPATH": true, "INPUTRC": true, "BASH_XTRACEFD": true,
	// Node.js
	"NODE_OPTIONS": true, "NODE_PATH": true,
	// Python
	"PYTHONSTARTUP": true, "PYTHONHOME": true, "PYTHONWARNINGS": true, "PYTHONBREAKPOINT": true, "PYTHONINSPECT": true,
	// Ruby
	"RUBYOPT": true, "RUBYLIB": true,
	// Perl
	"PERL5OPT": true, "PERL5LIB": true, "PERLLIB": true, "PERL5DB": true,
	// Elixir/Erlang
	"ERL_AFLAGS": true, "ERL_FLAGS": true, "ELIXIR_ERL_OPTIONS": true, "ERL_LIBS": true,
	// Go
	"GOFLAGS": true, "CGO_CFLAGS": true, "CGO_LDFLAGS": true,
	// Rust
	"RUSTC": true, "RUSTC_WRAPPER": true, "RUSTC_WORKSPACE_WRAPPER": true,
	"CARGO_BUILD_RUSTC": true, "CARGO_BUILD_RUSTC_WRAPPER": true, "RUSTFLAGS": true,
	// PHP
	"PHPRC": true, "PHP_INI_SCAN_DIR": true,
	// R
	"R_PROFILE": true, "R_PROFILE_USER": true, "R_HOME": true,
	// .NET startup hooks / deps
	"DOTNET_STARTUP_HOOKS": true, "DOTNET_ADDITIONAL_DEPS": true, "DOTNET_SHARED_STORE": true,
	"DOTNET_ROOT": true, "DOTNET_ROOT(x86)": true, "DOTNET_HOST_PATH": true,
	// .NET profiler attach / diagnostics
	"CORECLR_PROFILER": true, "CORECLR_PROFILER_PATH": true, "CORECLR_PROFILER_PATH_32": true,
	"CORECLR_PROFILER_PATH_64": true, "CORECLR_PROFILER_PATH_ARM32": true, "CORECLR_PROFILER_PATH_ARM64": true,
	"CORECLR_ENABLE_PROFILING": true, "DOTNET_PROFILER_PATH": true, "DOTNET_PROFILER_PATH_32": true,
	"DOTNET_PROFILER_PATH_64": true, "DOTNET_PROFILER_PATH_ARM32": true, "DOTNET_PROFILER_PATH_ARM64": true,
	"DOTNET_DiagnosticPorts": true, "DOTNET_BUNDLE_EXTRACT_BASE_DIR": true,
	// Dynamic linker
	"LD_PRELOAD": true, "DYLD_INSERT_LIBRARIES": true,
	// OpenSSL
	"OPENSSL_CONF": true, "OPENSSL_ENGINES": true,
	// Compiler
	"CC": true, "CXX": true, "AR": true,
	// Git
	"GIT_TEMPLATE_DIR": true, "GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_SYSTEM": true, "GIT_EXEC_PATH": true,
	"GIT_SSH": true, "GIT_SSH_COMMAND": true, "GIT_ASKPASS": true,
}

// certPaths are probed in order for SSL_CERT_FILE (executor.ts:723-728).
var certPaths = []string{
	"/etc/ssl/cert.pem",
	"/etc/ssl/certs/ca-certificates.crt",
	"/etc/pki/tls/certs/ca-bundle.crt",
	"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
}

// buildEnv filters parent (KEY=VALUE entries) and applies sandbox overrides.
// Port of executor.ts:573-745 (#buildSafeEnv), POSIX branches only.
func buildEnv(parent []string, tmpDir string) []string {
	env := map[string]string{}
	for _, kv := range parent {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || deniedEnv[k] || strings.HasPrefix(k, "BASH_FUNC_") ||
			strings.HasPrefix(strings.ToUpper(k), "COMPLUS_") {
			continue
		}
		env[k] = v
	}
	if env["HOME"] == "" {
		env["HOME"] = tmpDir
	}
	env["TMPDIR"] = tmpDir
	env["LANG"] = "en_US.UTF-8"
	env["PYTHONDONTWRITEBYTECODE"] = "1"
	env["PYTHONUNBUFFERED"] = "1"
	env["PYTHONUTF8"] = "1"
	env["NO_COLOR"] = "1"
	if env["PATH"] == "" {
		env["PATH"] = "/usr/local/bin:/usr/bin:/bin"
	}
	if env["SSL_CERT_FILE"] == "" {
		for _, p := range certPaths {
			if _, err := os.Stat(p); err == nil {
				env["SSL_CERT_FILE"] = p
				break
			}
		}
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
