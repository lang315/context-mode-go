package routing

import "testing"

// Expected values in this file come from node running the TS regex literals
// (routing.mjs:228-241) and the exported isStructurallyBounded.

func TestStripHeredocs(t *testing.T) {
	cases := []struct{ in, want string }{
		{"cat <<EOF\ncurl x\nEOF\necho done", "cat \necho done"},
		{"cat <<'EOF'\nbody\nEOF", "cat "},
		{"cat <<-\"END\"\n\tbody\n\tEND rest", "cat  rest"},
		{"cat <<EOFX\nfoo\nEOF tail", "cat  tail"},
		{"a <<A\nx\nB", "a <<A\nx\nB"},
		{"x <<< word", "x <<< word"},
		{"a <<E1\nq\n  E1 b <<E2\nr\nE2 c", "a  b  c"},
		{"cat << EOF\nx\nEOFZ\nEOF", "cat Z\nEOF"},
		{"<<\nEOF", "<<\nEOF"},
		{"cat <<EOF\u00a0\nx\n\u3000EOF!", "cat !"},
	}
	for _, c := range cases {
		if got := stripHeredocs(c.in); got != c.want {
			t.Errorf("stripHeredocs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStripQuotedContent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"gh issue edit --body \"use curl here\"", "gh issue edit --body \"\""},
		{"echo 'a \"b' \"c 'd\" e", "echo '' \"\" e"},
		{"echo \"unterminated", "echo \"unterminated"},
		{"cat <<EOF\n'x'\nEOF 'y'", "cat  ''"},
	}
	for _, c := range cases {
		if got := stripQuotedContent(c.in); got != c.want {
			t.Errorf("stripQuotedContent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsStructurallyBounded(t *testing.T) {
	bounded := []string{"pwd", "whoami", "hostname", "hostname -f", "date", "date -Iseconds", "echo hello", "printf '%s' x", "which node", "type git", "command -v gh", "git status", "git status --short", "git rev-parse HEAD", "git remote -v", "git remote show origin", "git branch", "git branch -vv", "git config --get user.email", "git diff --stat", "git diff --name-only", "git stash list", "git tag", "git tag -l 'v1.*'", "git log -5", "git log -10 --oneline", "node --version", "npm --version", "git --version", "node -V", "ls", "ls -la /etc", "cp /a /b", "cp -r /a /b", "mv /a /b", "rm /tmp/foo", "rm -rf /tmp/foo", "uname", "uname -a", "uname -srm", "id", "id mksglu", "id -u", "realpath ./foo", "ln -s a b", "ln a b", "ln -sf /src /dst", "ln --verbosex a", "mv -f -v a b", "mv\u00a0a b", "mkdir -p /tmp/test-dir", "  pwd  ", "\u00a0pwd", "touch a", "cd", "readlink -f x", "x -V", "git  status", "git\u3000status"}
	unbounded := []string{"ls -R /", "ls --recursive", "ls --recursive-x", "ls -laR", "ln -v a b", "ln -sv a b", "ln --verbose a b", "ln -vs a b", "ln -vfs a b", "ln -sfvr /src /dst", "ln --verbose", "ln --verbose-x a", "mv -fv a b", "mv\u00a0-v a b", "find /", "cat /var/log/syslog", "git log", "git diff", "git log -100", "pwd | xargs cat", "pwd > /tmp/out", "echo $(find /)", "echo `find /`", "date & cat x", "whoami; find /", "git status\nfind /", "git status\r\nfind /", "git status\rfind /", "echo hi > f", "cat x", "cat <<EOF", "a<b", "pwd\u0085", "", "touch", "rm", "rm -v", "-V", "ls\u00a0-R"}
	for _, c := range bounded {
		if !isStructurallyBounded(c) {
			t.Errorf("isStructurallyBounded(%q) = false, want true", c)
		}
	}
	for _, c := range unbounded {
		if isStructurallyBounded(c) {
			t.Errorf("isStructurallyBounded(%q) = true, want false", c)
		}
	}
}

func TestHasShellControl(t *testing.T) {
	// <(?!<) and &(?!&) always match the last < or & of a string, so any
	// < or & counts, heredocs and && included.
	for _, c := range []string{"a | b", "a `b`", "a\nb", "a\rb", "$(x)", "a >> b", "a > b", "a < b", "cat <<EOF", "a & b", "a && b", "a || b", "a; b"} {
		if !hasShellControl(c) {
			t.Errorf("hasShellControl(%q) = false, want true", c)
		}
	}
	for _, c := range []string{"git status", "echo $HOME", "echo (x)"} {
		if hasShellControl(c) {
			t.Errorf("hasShellControl(%q) = true, want false", c)
		}
	}
}
