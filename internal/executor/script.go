package executor

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// scriptExt maps a language to its temp script extension (executor.ts:23-36).
var scriptExt = map[Language]string{
	JavaScript: "js", TypeScript: "ts", Python: "py", Shell: "sh", Ruby: "rb", Go: "go",
	Rust: "rs", PHP: "php", Perl: "pl", R: "R", Elixir: "exs", CSharp: "csx",
}

// jsonQuote mirrors JSON.stringify for a string (no HTML escaping).
func jsonQuote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// shQuote single-quotes s for POSIX sh (executor.ts:63-65).
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// wrapFileContent prefixes code with FILE_CONTENT_PATH / file_path / FILE_CONTENT. Port of executor.ts:747-784.
func wrapFileContent(abs string, lang Language, code string) string {
	q := jsonQuote(abs)
	lq := literalQuote(abs)
	switch lang {
	case JavaScript, TypeScript:
		return "const FILE_CONTENT_PATH = " + q + ";\nconst file_path = FILE_CONTENT_PATH;\nconst FILE_CONTENT = require(\"fs\").readFileSync(FILE_CONTENT_PATH, \"utf-8\");\n" + code
	case Python:
		return "FILE_CONTENT_PATH = " + q + "\nfile_path = FILE_CONTENT_PATH\nwith open(FILE_CONTENT_PATH, \"r\", encoding=\"utf-8\") as _f:\n    FILE_CONTENT = _f.read()\n" + code
	case Shell:
		sq := shQuote(abs)
		return "FILE_CONTENT_PATH=" + sq + "\nfile_path=" + sq + "\nFILE_CONTENT=$(cat " + sq + ")\n" + code
	case Ruby:
		return "FILE_CONTENT_PATH = " + lq + "\nfile_path = FILE_CONTENT_PATH\nFILE_CONTENT = File.read(FILE_CONTENT_PATH, encoding: \"utf-8\")\n" + code
	case Go:
		return "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nvar FILE_CONTENT_PATH = " + q + "\nvar file_path = FILE_CONTENT_PATH\n\nfunc main() {\n\tb, _ := os.ReadFile(FILE_CONTENT_PATH)\n\tFILE_CONTENT := string(b)\n\t_ = FILE_CONTENT\n\t_ = fmt.Sprint()\n" + code + "\n}\n"
	case Rust:
		return "#![allow(unused_variables)]\nuse std::fs;\n\nfn main() {\n    let file_content_path = " + q + ";\n    let file_path = file_content_path;\n    let file_content = fs::read_to_string(file_content_path).unwrap();\n" + code + "\n}\n"
	case PHP:
		return "<?php\n$FILE_CONTENT_PATH = " + lq + ";\n$file_path = $FILE_CONTENT_PATH;\n$FILE_CONTENT = file_get_contents($FILE_CONTENT_PATH);\n" + code
	case Perl:
		return "my $FILE_CONTENT_PATH = " + lq + ";\nmy $file_path = $FILE_CONTENT_PATH;\nopen(my $fh, '<:encoding(UTF-8)', $FILE_CONTENT_PATH) or die \"Cannot open: $!\";\nmy $FILE_CONTENT = do { local $/; <$fh> };\nclose($fh);\n" + code
	case R:
		return "FILE_CONTENT_PATH <- " + q + "\nfile_path <- FILE_CONTENT_PATH\nFILE_CONTENT <- readLines(FILE_CONTENT_PATH, warn=FALSE, encoding=\"UTF-8\")\nFILE_CONTENT <- paste(FILE_CONTENT, collapse=\"\\n\")\n" + code
	case Elixir:
		return "file_content_path = " + binaryLiteral(abs) + "\nfile_path = file_content_path\nfile_content = File.read!(file_content_path)\n" + code
	case CSharp:
		return "var FILE_CONTENT_PATH = " + q + ";\nvar file_path = FILE_CONTENT_PATH;\nvar FILE_CONTENT = System.IO.File.ReadAllText(FILE_CONTENT_PATH);\n" + code
	}
	return code
}

// literalQuote is a single-quoted literal with no interpolation (Ruby, PHP, Perl):
// only \ and ' are special inside it, unlike double quotes (#{}, @{[]}, {${}}).
func literalQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// binaryLiteral is an Elixir binary of the bytes of s, so no interpolation is possible.
func binaryLiteral(s string) string {
	n := make([]string, len(s))
	for i := 0; i < len(s); i++ {
		n[i] = strconv.Itoa(int(s[i]))
	}
	return "<<" + strings.Join(n, ", ") + ">>"
}

// wrapJS wraps JS/TS in an async main so top-level await works.
// Port of server.ts:1756-1820 without the __CM_FS__/__CM_NET__ counters and the
// require-shadowing IIFE that only fed them (spec §2.2).
func wrapJS(code string, background bool) string {
	s := "\nasync function __cm_main(){\n" + code + "\n}\n__cm_main().catch(e=>{console.error(e);process.exitCode=1});"
	if background {
		s += "\nsetInterval(()=>{},2147483647);"
	}
	return s
}

// prepareCode applies every source transform before the script is written:
// FILE_CONTENT prefix (execute_file) or JS wrapper (execute), then executor.ts:333-347 fixups.
func prepareCode(lang Language, code, filePath, projectDir string, background bool) string {
	if filePath != "" {
		code = wrapFileContent(filePath, lang, code)
	} else if lang == JavaScript || lang == TypeScript {
		code = wrapJS(code, background)
	}
	switch lang {
	case Go:
		if !strings.Contains(code, "package ") {
			code = "package main\n\nimport \"fmt\"\n\nfunc main() {\n" + code + "\n}\n"
		}
	case PHP:
		if !strings.HasPrefix(strings.TrimLeftFunc(code, unicode.IsSpace), "<?") {
			code = "<?php\n" + code
		}
	case Elixir:
		if _, err := os.Stat(filepath.Join(projectDir, "mix.exs")); err == nil {
			q := jsonQuote(filepath.Join(projectDir, "_build/dev/lib"))
			code = "Path.wildcard(Path.join(" + q + ", \"*/ebin\"))\n|> Enum.each(&Code.prepend_path/1)\n\n" + code
		}
	}
	return code
}

// writeScript writes code to dir/script.<ext> and returns its path. Shell scripts are
// 0700 and restore the parent PATH on line 1 (executor.ts:67-75, 349-367).
func writeScript(dir string, lang Language, code, parentPATH string) (string, error) {
	fp := filepath.Join(dir, "script."+scriptExt[lang])
	mode := os.FileMode(0o644)
	if lang == Shell {
		mode = 0o700
		if parentPATH != "" {
			code = "export PATH=" + shQuote(parentPATH) + "\n" + code
		}
	}
	return fp, os.WriteFile(fp, []byte(code), mode)
}

// osTempDir is the real per-user temp dir, ignoring a TMPDIR override that may point
// into the project. Port of executor.ts:99-111 (POSIX).
var osTempDir = sync.OnceValue(detectOSTempDir)

func detectOSTempDir() string {
	if runtime.GOOS == "darwin" {
		cmd := exec.Command("getconf", "DARWIN_USER_TEMP_DIR")
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "TMPDIR=") {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		out, err := cmd.Output()
		dir := strings.TrimSpace(string(out))
		if cwd, _ := os.Getwd(); err == nil && dir != "" && dir != cwd {
			return dir
		}
	}
	// linux: TS takes dirname(mktemp -u -d) with TMPDIR unset, which is /tmp.
	return "/tmp"
}
