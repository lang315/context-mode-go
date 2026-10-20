package mcpserver

// codeEchoMax is CODE_ECHO_MAX (S:1511), counted in UTF-16 code units like JS.
const codeEchoMax = 2000

// buildExecuteEcho is the source-code preamble before tool output; path is
// "" for ctx_execute. Port of truncateCodeForEcho + buildExecuteEcho (S:1513-1527).
func buildExecuteEcho(language, code, path string) string {
	header := ""
	if path != "" {
		header = "path=" + path + "\n"
	}
	if cut, truncated := utf16Prefix(code, codeEchoMax); truncated {
		code = cut + "\n… (truncated)"
	}
	return header + "```" + language + "\n" + code + "\n```\n\n"
}

// utf16Prefix is JS s.slice(0, n) for the first n UTF-16 code units, reporting
// whether anything was cut. A surrogate pair straddling n is dropped whole.
func utf16Prefix(s string, n int) (string, bool) {
	units := 0
	for i, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if units+w > n {
			return s[:i], true
		}
		units += w
	}
	return s, false
}
