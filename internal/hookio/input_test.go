package hookio

import (
	"bytes"
	"strings"
	"testing"
)

func TestReadInput(t *testing.T) {
	b, err := ReadInput(strings.NewReader(`{"a":1}`))
	if err != nil || string(b) != `{"a":1}` {
		t.Errorf("ReadInput = %q, %v", b, err)
	}
	exact := bytes.Repeat([]byte{' '}, maxInput)
	if b, err := ReadInput(bytes.NewReader(exact)); err != nil || len(b) != maxInput {
		t.Errorf("ReadInput(16 MiB) = %d bytes, %v; want all, nil", len(b), err)
	}
	over := bytes.Repeat([]byte{' '}, maxInput+1)
	if _, err := ReadInput(bytes.NewReader(over)); err == nil {
		t.Error("ReadInput(16 MiB + 1) succeeded, want error")
	}
}

func TestSessionID(t *testing.T) {
	const uuid = "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0"
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "CLAUDE_SESSION_ID" {
				return v
			}
			return ""
		}
	}
	cases := []struct {
		name, in, env, want string
	}{
		{"transcript wins", `{"transcript_path":"/h/.claude/projects/p/` + uuid + `.jsonl","session_id":"s1"}`, "e1", uuid},
		{"uppercase uuid falls through", `{"transcript_path":"/p/` + strings.ToUpper(uuid) + `.jsonl","session_id":"s1"}`, "", "s1"},
		{"no .jsonl suffix", `{"transcript_path":"/p/` + uuid + `.jsonl.bak","session_id":"s1"}`, "", "s1"},
		{"non-string transcript", `{"transcript_path":7,"session_id":"s1"}`, "", "s1"},
		{"session_id", `{"session_id":"s1"}`, "e1", "s1"},
		{"empty session_id is falsy", `{"session_id":""}`, "e1", "e1"},
		{"camelCase and conversation_id ignored", `{"sessionId":"x","conversation_id":"y"}`, "e1", "e1"},
		{"env", `{}`, "e1", "e1"},
		{"ppid", `{}`, "", "pid-4242"},
	}
	for _, c := range cases {
		got := SessionID(mustParse(t, c.in), env(c.env), 4242)
		if got != c.want {
			t.Errorf("%s: SessionID = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFileID(t *testing.T) {
	keep := []string{"abc", "pid-12", "A.b_c-9", strings.Repeat("x", 128)}
	for _, s := range keep {
		if got := FileID(s); got != s {
			t.Errorf("FileID(%q) = %q, want unchanged", s, got)
		}
	}
	// Expected values are the first 16 hex chars of `printf %s <in> | shasum -a 256`.
	hashed := map[string]string{
		"../x":                   "d6b96a97d147daaa",
		"":                       "e3b0c44298fc1c14",
		"a b":                    "c8687a08aa5d6ed2",
		strings.Repeat("x", 129): "0ec9eb33e74510bc",
		"x\n":                    "73cb3858a687a849",
		".":                      "cdb4ee2aea69cc6a", // matches the pattern but names a directory
		"..":                     "5ec1f7e700f37c3d",
	}
	for in, want := range hashed {
		got := FileID(in)
		if got != want || len(got) != 16 {
			t.Errorf("FileID(%q) = %q, want %q", in, got, want)
		}
	}
}
