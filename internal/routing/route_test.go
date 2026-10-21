package routing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/security"
)

// testDeps returns Deps over temp dirs with the MCP server ready. The project
// settings hold the rules tools/golden-gen/gen-hooks.mjs writes.
func testDeps(t *testing.T) Deps {
	t.Helper()
	proj := t.TempDir()
	settings := `{"permissions":{"deny":["Bash(sudo *)","Read(**/.env)"],"ask":["Bash(git push:*)"],"allow":["Bash(curl:*)"]}}`
	if err := os.MkdirAll(filepath.Join(proj, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".claude", "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	return Deps{
		ProjectDir: proj,
		SessionID:  "s1",
		RunDir:     filepath.Join(t.TempDir(), "run"),
		Policy:     security.NewPolicy(proj, t.TempDir()),
		Getenv:     func(string) string { return "" },
		MCPReady:   func() bool { return true },
	}
}

func input(t *testing.T, js string) *hookio.Object {
	t.Helper()
	o, err := hookio.ParseObject([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func bash(cmd string) *hookio.Object {
	o := &hookio.Object{}
	o.SetString("command", cmd)
	return o
}

// echo is the updatedInput JSON of a Bash redirect carrying msg.
func echo(msg string) string {
	return `{"command":` + string(hookio.QuoteString(`echo "`+msg+`"`)) + `}`
}

// check compares dec with the wanted action and payload. payload is the
// reason (deny), the updatedInput JSON (modify) or the context (context).
func check(t *testing.T, name string, dec Decision, action, payload string) {
	t.Helper()
	if dec.Action != action {
		t.Errorf("%s: action %q, want %q", name, dec.Action, action)
		return
	}
	var got string
	switch action {
	case "deny":
		got = dec.Reason
	case "modify":
		b, err := dec.UpdatedInput.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		got = string(b)
	case "context":
		got = dec.AdditionalContext
	}
	if got != payload {
		t.Errorf("%s: payload\n got %s\nwant %s", name, got, payload)
	}
}

func TestRouteBashRedirects(t *testing.T) {
	d := testDeps(t)
	dec := Route("Bash", bash("curl https://x.com"), false, d)
	check(t, "curl", dec, "modify", echo(curlMessage))
	if r := dec.Redirect; r == nil || *r != (Redirect{Tool: "Bash", Type: "bash-redirected", BytesAvoided: 8192, Summary: "curl https://x.com"}) {
		t.Errorf("curl redirect = %+v", r)
	}
	check(t, "inline", Route("Bash", bash(`node -e "fetch('https://a.b')"`), false, d), "modify", echo(inlineMessage))
	check(t, "build", Route("Bash", bash("./gradlew build"), false, d), "modify", echo(buildMessage("./gradlew build")))
	if r := Route("Bash", bash("./gradlew build"), false, d).Redirect; r != nil {
		t.Errorf("build redirect = %+v, want none", r)
	}
	// R1 P6: all curl segments safe ends routing, the build tool included.
	check(t, "safe curl", Route("Bash", bash("curl -s -o f u && mvn test"), false, d), "", "")
	// An allow rule does not stop the redirect (golden #16).
	check(t, "allowed curl", Route("Bash", bash("curl https://allowed.example"), false, d), "modify", echo(curlMessage))
}

func TestRouteBashSummary(t *testing.T) {
	cmd := "curl " + strings.Repeat("\U0001F600", 120) // 245 UTF-16 units
	dec := Route("Bash", bash(cmd), false, testDeps(t))
	if dec.Redirect == nil || hookio.JSLen(dec.Redirect.Summary) != 199 || !strings.HasPrefix(cmd, dec.Redirect.Summary) {
		t.Errorf("summary = %+v; want the first 199 units (no split pair)", dec.Redirect)
	}
}

func TestRouteGating(t *testing.T) {
	d := testDeps(t)
	notReady := d
	notReady.MCPReady = func() bool { return false }
	for _, c := range []struct{ tool, in string }{
		{"Bash", `{"command":"curl https://x.com"}`},
		{"Bash", `{"command":"node -e \"fetch('https://a.b')\""}`},
		{"Bash", `{"command":"./gradlew build"}`},
		{"WebFetch", `{"url":"https://e.com"}`},
	} {
		check(t, c.in+" subagent", Route(c.tool, input(t, c.in), true, d), "", "")
		check(t, c.in+" not ready", Route(c.tool, input(t, c.in), false, notReady), "", "")
	}
	// Guidance, security and Agent injection still apply in a subagent.
	check(t, "subagent bash guidance", Route("Bash", bash("find ."), true, d), "context", bashGuidance)
	check(t, "subagent deny", Route("Bash", bash("sudo ls"), true, d), "deny", "Blocked by security policy: matches deny pattern Bash(sudo *)")
	if dec := Route("Agent", input(t, `{"prompt":"x"}`), true, notReady); dec.Action != "modify" {
		t.Errorf("subagent Agent action %q", dec.Action)
	}
}

func TestRouteBashSecurity(t *testing.T) {
	d := testDeps(t)
	check(t, "deny", Route("Bash", bash("sudo rm -rf /"), false, d), "deny", "Blocked by security policy: matches deny pattern Bash(sudo *)")
	check(t, "chained deny", Route("Bash", bash("echo ok && sudo ls"), false, d), "deny", "Blocked by security policy: matches deny pattern Bash(sudo *)")
	check(t, "ask", Route("Bash", bash("git push origin main"), false, d), "ask", "")
	d.Policy = nil
	check(t, "no policy", Route("Bash", bash("sudo ls"), false, d), "context", bashGuidance)
}

func TestRouteBashGuidance(t *testing.T) {
	d := testDeps(t)
	check(t, "bounded", Route("Bash", bash("git status"), false, d), "", "")
	check(t, "first", Route("Bash", bash("npm install"), false, d), "context", bashGuidance)
	check(t, "second", Route("Bash", bash("find /"), false, d), "", "")
	check(t, "empty command", Route("Bash", bash(""), false, testDeps(t)), "context", bashGuidance)
	check(t, "no command", Route("Bash", nil, false, testDeps(t)), "context", bashGuidance)
}

func TestRouteBashNudgeThreshold(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "CMGO_BASH_NUDGE_MIN_COMMAND_BYTES" {
				return v
			}
			return ""
		}
	}
	for _, c := range []struct{ env, cmd, want string }{
		{"64", "ps aux", ""},
		{"16", "find / -type f -name '*.log' -exec cat {} +", "context"},
		{"7", "ps aux", ""},        // 6 bytes, below the threshold
		{"6", "ps aux", "context"}, // at the threshold
		{"64abc", "ps aux", ""},    // parseInt
		{"0", "ps", "context"},
		{"-5", "ps", "context"},
		{"abc", "ps", "context"},
		{"100001", "ps", "context"},
		{"4096", "curl https://x.io", "modify"}, // redirects are never relaxed
	} {
		d := testDeps(t)
		d.Getenv = env(c.env)
		if got := Route("Bash", bash(c.cmd), false, d).Action; got != c.want {
			t.Errorf("env %q cmd %q: action %q, want %q", c.env, c.cmd, got, c.want)
		}
	}
}

func TestRouteRead(t *testing.T) {
	d := testDeps(t)
	dir := t.TempDir()
	small, big := filepath.Join(dir, "small.txt"), filepath.Join(dir, "big.txt")
	if err := os.WriteFile(small, []byte(strings.Repeat("x", 50000)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(big, []byte(strings.Repeat("x", 50001)), 0o600); err != nil {
		t.Fatal(err)
	}
	rd := func(p string) *hookio.Object { o := &hookio.Object{}; o.SetString("file_path", p); return o }
	check(t, "small first", Route("Read", rd(small), false, d), "context", readGuidance)
	check(t, "small second", Route("Read", rd(small), false, d), "", "")
	check(t, "missing", Route("Read", rd(filepath.Join(dir, "nope")), false, d), "", "")
	for i := 0; i < 2; i++ { // large files get guidance on every call
		dec := Route("Read", rd(big), false, d)
		check(t, "big", dec, "context", readGuidance)
		if r := dec.Redirect; r == nil || *r != (Redirect{Tool: "Read", Type: "read-redirected", BytesAvoided: 50001, Summary: big}) {
			t.Errorf("big redirect = %+v", r)
		}
	}
	check(t, "dir", Route("Read", rd(dir), false, testDeps(t)), "context", readGuidance)
	if dec := Route("Read", rd(dir), false, testDeps(t)); dec.Redirect != nil {
		t.Error("a directory got a redirect")
	}
}

func TestRouteGrepAndWebFetch(t *testing.T) {
	d := testDeps(t)
	check(t, "grep first", Route("Grep", input(t, `{"pattern":"x"}`), false, d), "context", grepGuidance)
	check(t, "grep second", Route("Grep", input(t, `{"pattern":"y"}`), false, d), "", "")
	for i := 0; i < 2; i++ { // never throttled
		dec := Route("WebFetch", input(t, `{"url":"https://example.com/a","prompt":"p"}`), false, d)
		check(t, "webfetch", dec, "deny", webFetchMessage("https://example.com/a"))
		if r := dec.Redirect; r == nil || *r != (Redirect{Tool: "WebFetch", Type: "webfetch-redirected", BytesAvoided: 16384, Summary: "https://example.com/a"}) {
			t.Errorf("webfetch redirect = %+v", r)
		}
	}
	check(t, "webfetch no url", Route("WebFetch", input(t, `{}`), false, d), "deny", webFetchMessage(""))
}

func TestRouteAgent(t *testing.T) {
	d := testDeps(t)
	q := func(s string) string { return string(hookio.QuoteString(s)) }
	cases := []struct{ in, want string }{
		{`{"prompt":"do X","subagent_type":"Explore","description":"d"}`,
			`{"prompt":` + q("do X"+RoutingBlock(false, true)) + `,"subagent_type":"Explore","description":"d"}`},
		{`{"description":"d","subagent_type":"Bash","prompt":"do Y"}`,
			`{"description":"d","subagent_type":"general-purpose","prompt":` + q("do Y"+RoutingBlock(false, true)) + `}`},
		{`{"other_param":123,"request":"analyze","nested":{"a":1}}`,
			`{"other_param":123,"request":` + q("analyze"+RoutingBlock(false, true)) + `,"nested":{"a":1}}`},
		{`{"task":"t","query":"q"}`,
			`{"task":"t","query":` + q("q"+RoutingBlock(false, true)) + `}`},
		{`{"unknown_field":"content"}`,
			`{"unknown_field":"content","prompt":` + q(RoutingBlock(false, true)) + `}`},
		{`{"prompt":null,"subagent_type":"bash"}`,
			`{"prompt":` + q(RoutingBlock(false, true)) + `,"subagent_type":"bash"}`},
	}
	for _, c := range cases {
		in := input(t, c.in)
		check(t, c.in, Route("Agent", in, false, d), "modify", c.want)
		if b, _ := in.MarshalJSON(); string(b) != string(mustCompact(t, c.in)) {
			t.Errorf("%s: input changed to %s", c.in, b)
		}
	}
}

func mustCompact(t *testing.T, js string) []byte {
	t.Helper()
	b, err := input(t, js).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRoutePassthrough(t *testing.T) {
	d := testDeps(t)
	for _, tool := range []string{"Task", "TaskCreate", "Write", "Edit", "Glob", "WebSearch", "", "mcp__cmgo__ctx_execute", "mcp__slack__list", "bash"} {
		check(t, tool, Route(tool, input(t, `{"command":"curl x","prompt":"p","language":"shell","code":"sudo ls"}`), false, d), "", "")
	}
}
