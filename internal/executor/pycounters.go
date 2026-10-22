package executor

import (
	_ "embed"
	"os"
	"strconv"
	"strings"
)

// pyBootstrap is the Python source run with -c. It counts the bytes the user's
// script reads through open() and sockets and writes the sum to a file at exit.
//
//go:embed pycounters.py
var pyBootstrap string

// pyArgv runs script under the bootstrap. The bootstrap gets the counts file
// path and the script path as sys.argv[1] and [2]; its own source is exec'd in
// a private namespace so __main__ gains no name (spec §1 Q1).
func pyArgv(python, countsFile, script string) []string {
	boot := `exec(compile(` + jsonQuote(pyBootstrap) + `, "<cm>", "exec"), {"__name__": "cm"})`
	return []string{python, "-c", boot, countsFile, script}
}

// readPyCounts returns the count the bootstrap wrote; a missing or malformed
// file (killed run, os._exit, setup fallback) is 0.
func readPyCounts(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
