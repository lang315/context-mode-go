package routing

import (
	"path/filepath"
	"strings"
)

// readCommands start a command whose output the model usually needs
// verbatim, to Edit or review it (bash-compact spec §1 item 5).
var readCommands = map[string]bool{"sed": true, "cat": true, "head": true, "tail": true, "nl": true, "tac": true, "bat": true, "less": true}

// ReadsFiles reports whether any chain of command starts with a reading
// command: sed, cat, head, tail, nl, tac, bat, less, git diff or git show.
// Leading VAR=value words are skipped, and so are git's -C <dir> and
// --no-pager. A form the tokenizer does not read (heredoc, $(, backtick,
// subshell) is not a reading command.
func ReadsFiles(command string) bool {
	chains, ok := tokenize(command)
	if !ok {
		return false
	}
	for _, ch := range chains {
		if len(ch) == 0 {
			continue
		}
		w := ch[0].words
		for len(w) > 0 && strings.Index(w[0].s, "=") > 0 {
			w = w[1:]
		}
		if len(w) == 0 {
			continue
		}
		name := filepath.Base(w[0].s)
		if readCommands[name] {
			return true
		}
		if name == "git" && gitReads(w[1:]) {
			return true
		}
	}
	return false
}

// gitReads reports whether git's arguments name the diff or show subcommand.
func gitReads(args []rgWord) bool {
	for len(args) > 0 {
		switch {
		case args[0].s == "--no-pager":
			args = args[1:]
		case args[0].s == "-C" && len(args) > 1:
			args = args[2:]
		default:
			return args[0].s == "diff" || args[0].s == "show"
		}
	}
	return false
}
