package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONQuoteMatchesJSONStringify(t *testing.T) {
	cases := map[string]string{
		`/a/b.txt`:      `"/a/b.txt"`,
		`/a "q" \ b`:    `"/a \"q\" \\ b"`,
		`/x<y>&z`:       `"/x<y>&z"`,
		"/caf\u00e9/日本": "\"/caf\u00e9/日本\"",
	}
	for in, want := range cases {
		if got := jsonQuote(in); got != want {
			t.Errorf("jsonQuote(%q) = %s; want %s", in, got, want)
		}
	}
}

func TestWrapFileContentPrefixes(t *testing.T) {
	p := "/p/it's \"f\".txt"
	q := `"/p/it's \"f\".txt"`
	sq := `'/p/it'\''s "f".txt'`
	want := map[Language]string{
		JavaScript: "const FILE_CONTENT_PATH = " + q + ";\nconst file_path = FILE_CONTENT_PATH;\nconst FILE_CONTENT = require(\"fs\").readFileSync(FILE_CONTENT_PATH, \"utf-8\");\nCODE",
		Python:     "FILE_CONTENT_PATH = " + q + "\nfile_path = FILE_CONTENT_PATH\nwith open(FILE_CONTENT_PATH, \"r\", encoding=\"utf-8\") as _f:\n    FILE_CONTENT = _f.read()\nCODE",
		Shell:      "FILE_CONTENT_PATH=" + sq + "\nfile_path=" + sq + "\nFILE_CONTENT=$(cat " + sq + ")\nCODE",
		Ruby:       "FILE_CONTENT_PATH = " + q + "\nfile_path = FILE_CONTENT_PATH\nFILE_CONTENT = File.read(FILE_CONTENT_PATH, encoding: \"utf-8\")\nCODE",
		Go:         "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nvar FILE_CONTENT_PATH = " + q + "\nvar file_path = FILE_CONTENT_PATH\n\nfunc main() {\n\tb, _ := os.ReadFile(FILE_CONTENT_PATH)\n\tFILE_CONTENT := string(b)\n\t_ = FILE_CONTENT\n\t_ = fmt.Sprint()\nCODE\n}\n",
		Rust:       "#![allow(unused_variables)]\nuse std::fs;\n\nfn main() {\n    let file_content_path = " + q + ";\n    let file_path = file_content_path;\n    let file_content = fs::read_to_string(file_content_path).unwrap();\nCODE\n}\n",
		PHP:        "<?php\n$FILE_CONTENT_PATH = " + q + ";\n$file_path = $FILE_CONTENT_PATH;\n$FILE_CONTENT = file_get_contents($FILE_CONTENT_PATH);\nCODE",
		Perl:       "my $FILE_CONTENT_PATH = " + q + ";\nmy $file_path = $FILE_CONTENT_PATH;\nopen(my $fh, '<:encoding(UTF-8)', $FILE_CONTENT_PATH) or die \"Cannot open: $!\";\nmy $FILE_CONTENT = do { local $/; <$fh> };\nclose($fh);\nCODE",
		R:          "FILE_CONTENT_PATH <- " + q + "\nfile_path <- FILE_CONTENT_PATH\nFILE_CONTENT <- readLines(FILE_CONTENT_PATH, warn=FALSE, encoding=\"UTF-8\")\nFILE_CONTENT <- paste(FILE_CONTENT, collapse=\"\\n\")\nCODE",
		Elixir:     "file_content_path = " + q + "\nfile_path = file_content_path\nfile_content = File.read!(file_content_path)\nCODE",
		CSharp:     "var FILE_CONTENT_PATH = " + q + ";\nvar file_path = FILE_CONTENT_PATH;\nvar FILE_CONTENT = System.IO.File.ReadAllText(FILE_CONTENT_PATH);\nCODE",
	}
	want[TypeScript] = want[JavaScript]
	for _, lang := range Languages {
		if got := wrapFileContent(p, lang, "CODE"); got != want[lang] {
			t.Errorf("%s prefix:\n got %q\nwant %q", lang, got, want[lang])
		}
	}
}

func TestWrapJS(t *testing.T) {
	want := "\nasync function __cm_main(){\nawait 1\n}\n__cm_main().catch(e=>{console.error(e);process.exitCode=1});"
	if got := wrapJS("await 1", false); got != want {
		t.Errorf("wrapJS = %q", got)
	}
	if got := wrapJS("await 1", true); got != want+"\nsetInterval(()=>{},2147483647);" {
		t.Errorf("wrapJS background = %q", got)
	}
}

func TestPrepareCode(t *testing.T) {
	// JS gets the wrapper only for ctx_execute (no FilePath).
	if got := prepareCode(JavaScript, "x", "", "/p", false); !strings.Contains(got, "__cm_main") {
		t.Errorf("js execute not wrapped: %q", got)
	}
	if got := prepareCode(TypeScript, "x", "/f", "/p", false); strings.Contains(got, "__cm_main") || !strings.HasPrefix(got, "const FILE_CONTENT_PATH") {
		t.Errorf("ts execute_file wrong: %q", got)
	}
	// Go without package is wrapped; with package left alone; FILE_CONTENT go not double-wrapped.
	if got := prepareCode(Go, `fmt.Println("hi")`, "", "/p", false); got != "package main\n\nimport \"fmt\"\n\nfunc main() {\nfmt.Println(\"hi\")\n}\n" {
		t.Errorf("go wrap = %q", got)
	}
	full := "package main\nfunc main(){}\n"
	if got := prepareCode(Go, full, "", "/p", false); got != full {
		t.Errorf("go with package changed: %q", got)
	}
	if got := prepareCode(Go, "x", "/f", "/p", false); strings.Count(got, "package main") != 1 {
		t.Errorf("go file_content double-wrapped: %q", got)
	}
	// PHP open tag.
	if got := prepareCode(PHP, "echo 1;", "", "/p", false); got != "<?php\necho 1;" {
		t.Errorf("php = %q", got)
	}
	if got := prepareCode(PHP, "  <?php echo 1;", "", "/p", false); got != "  <?php echo 1;" {
		t.Errorf("php with tag = %q", got)
	}
	// Elixir mix project prefix.
	proj := t.TempDir()
	if got := prepareCode(Elixir, "IO.puts 1", "", proj, false); got != "IO.puts 1" {
		t.Errorf("elixir without mix.exs = %q", got)
	}
	os.WriteFile(filepath.Join(proj, "mix.exs"), nil, 0o644)
	want := "Path.wildcard(Path.join(\"" + proj + "/_build/dev/lib\", \"*/ebin\"))\n|> Enum.each(&Code.prepend_path/1)\n\nIO.puts 1"
	if got := prepareCode(Elixir, "IO.puts 1", "", proj, false); got != want {
		t.Errorf("elixir mix = %q", got)
	}
}

func TestWriteScript(t *testing.T) {
	dir := t.TempDir()
	fp, err := writeScript(dir, Shell, "echo hi", "/a b/bin:/it's")
	if err != nil {
		t.Fatal(err)
	}
	if fp != filepath.Join(dir, "script.sh") {
		t.Errorf("path = %s", fp)
	}
	b, _ := os.ReadFile(fp)
	if string(b) != "export PATH='/a b/bin:/it'\\''s'\necho hi" {
		t.Errorf("shell content = %q", b)
	}
	if st, _ := os.Stat(fp); st.Mode().Perm() != 0o700 {
		t.Errorf("shell mode = %v", st.Mode().Perm())
	}
	fp, _ = writeScript(dir, Shell, "echo hi", "")
	if b, _ := os.ReadFile(fp); string(b) != "echo hi" {
		t.Errorf("shell without PATH = %q", b)
	}
	for lang, ext := range map[Language]string{Python: "py", R: "R", Elixir: "exs", CSharp: "csx", Perl: "pl"} {
		fp, _ := writeScript(dir, lang, "x", "/bin")
		if filepath.Base(fp) != "script."+ext {
			t.Errorf("%s file = %s", lang, fp)
		}
		if b, _ := os.ReadFile(fp); string(b) != "x" {
			t.Errorf("%s content = %q", lang, b)
		}
	}
}

func TestOSTempDir(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	d := detectOSTempDir() // osTempDir itself may already be cached by earlier tests
	if st, err := os.Stat(d); err != nil || !st.IsDir() {
		t.Fatalf("osTempDir %q: %v", d, err)
	}
	if strings.HasPrefix(d, os.Getenv("TMPDIR")) {
		t.Errorf("osTempDir followed TMPDIR override: %s", d)
	}
}
