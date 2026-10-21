package routing

import (
	"os"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/security"
)

// Deps is what Route needs from the hook process.
type Deps struct {
	ProjectDir string // absolute project dir
	SessionID  string
	RunDir     string // $CMGO_DIR/run (guidance markers, MCP sentinels)
	Policy     *security.Policy
	Getenv     func(string) string // CMGO_BASH_NUDGE_MIN_COMMAND_BYTES
	MCPReady   func() bool         // usually func() bool { return MCPReady(RunDir) }
}

// Redirect is the TS redirectMeta: what a redirect kept out of the context.
type Redirect struct {
	Tool, Type   string // "Bash","bash-redirected" | "Read","read-redirected" | "WebFetch","webfetch-redirected"
	BytesAvoided int64
	Summary      string // first 200 UTF-16 units
}

// Decision is the normalized TS routing decision.
type Decision struct {
	Action            string         // "" (passthrough), "deny", "ask", "modify", "context"
	Reason            string         // deny
	UpdatedInput      *hookio.Object // modify
	AdditionalContext string         // context
	Redirect          *Redirect
}

// readRedirectMin is the size above which Read always gets guidance
// (routing.mjs:853).
const readRedirectMin = 50_000

// agentFields are the Agent prompt fields, first present wins (routing.mjs:898).
var agentFields = []string{"prompt", "request", "objective", "question", "query", "task"}

// agentBlock is appended to Agent prompts (spec §2.1.6).
var agentBlock = RoutingBlock(false, true)

// Route ports routePreToolUse (routing.mjs:670-1017) for Claude Code with
// the spec §2.3 differences: no ctx_* or external-MCP branches, no
// fail-closed gate, and only the Claude Code input field names. subagent is
// true when the hook input has agent_id or agent_type.
func Route(toolName string, toolInput *hookio.Object, subagent bool, d Deps) Decision {
	if toolInput == nil {
		toolInput = &hookio.Object{}
	}
	switch toolName {
	case "Bash":
		return routeBash(toolInput, subagent, d)
	case "Read":
		return routeRead(toolInput, d)
	case "Grep":
		return guidance(d, "grep", grepGuidance)
	case "WebFetch":
		if !canRedirect(subagent, d) {
			return Decision{}
		}
		url, _ := toolInput.String("url")
		return Decision{Action: "deny", Reason: webFetchMessage(url), Redirect: &Redirect{
			Tool: "WebFetch", Type: "webfetch-redirected", BytesAvoided: 16384, Summary: hookio.JSPrefix(url, 200),
		}}
	case "Agent":
		return routeAgent(toolInput)
	}
	return Decision{}
}

// canRedirect ports mcpRedirect (routing.mjs:28-32): the redirects that
// point at ctx_* tools pass through when the caller cannot call them.
func canRedirect(subagent bool, d Deps) bool {
	return !subagent && d.MCPReady != nil && d.MCPReady()
}

func routeBash(in *hookio.Object, subagent bool, d Deps) Decision {
	command, _ := in.String("command")
	if d.Policy != nil {
		// Only an explicit rule acts; allow and the default ask fall through.
		switch decision, pattern := d.Policy.EvaluateCommand(command); {
		case decision == "deny":
			return Decision{Action: "deny", Reason: "Blocked by security policy: matches deny pattern " + pattern}
		case decision == "ask" && pattern != "":
			return Decision{Action: "ask"}
		}
	}
	stripped := stripQuotedContent(command)
	if found, dangerous := curlCheck(stripped); found {
		if !dangerous || !canRedirect(subagent, d) {
			return Decision{}
		}
		dec := redirectTo(curlMessage)
		dec.Redirect = &Redirect{Tool: "Bash", Type: "bash-redirected", BytesAvoided: 8192, Summary: hookio.JSPrefix(command, 200)}
		return dec
	}
	if inlineHTTP(stripHeredocs(command)) {
		if !canRedirect(subagent, d) {
			return Decision{}
		}
		return redirectTo(inlineMessage)
	}
	if buildTool(stripped) {
		if !canRedirect(subagent, d) {
			return Decision{}
		}
		return redirectTo(buildMessage(command))
	}
	if isStructurallyBounded(command) {
		return Decision{}
	}
	if n := nudgeMinBytes(d.Getenv); n > 0 && len(command) < n {
		return Decision{}
	}
	return guidance(d, "bash", bashGuidance)
}

// redirectTo is the TS Bash redirect: a modify whose command echoes msg.
// Format turns it into a deny with msg as the reason.
func redirectTo(msg string) Decision {
	in := &hookio.Object{}
	in.SetString("command", `echo "`+msg+`"`)
	return Decision{Action: "modify", UpdatedInput: in}
}

// nudgeMinBytes ports getBashNudgeMinCommandBytes (routing.mjs:97-108):
// an int in [1, 100000], else 0 (off).
func nudgeMinBytes(getenv func(string) string) int {
	if getenv == nil {
		return 0
	}
	n, ok := jsParseInt(getenv("CMGO_BASH_NUDGE_MIN_COMMAND_BYTES"))
	if !ok || n <= 0 || n > 100_000 {
		return 0
	}
	return int(n)
}

// routeRead ports routing.mjs:848-867. A regular file over 50,000 bytes gets
// guidance on every call (and still uses up the once-marker); a relative
// path is resolved against the hook's cwd, as TS statSync does.
func routeRead(in *hookio.Object, d Deps) Decision {
	path, _ := in.String("file_path")
	if path != "" {
		if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() && fi.Size() > readRedirectMin {
			guidanceOnce(d.RunDir, d.SessionID, "read")
			return Decision{Action: "context", AdditionalContext: readGuidance, Redirect: &Redirect{
				Tool: "Read", Type: "read-redirected", BytesAvoided: fi.Size(), Summary: hookio.JSPrefix(path, 200),
			}}
		}
	}
	return guidance(d, "read", readGuidance)
}

// guidance returns text as context the first time typ is due this session.
func guidance(d Deps, typ, text string) Decision {
	if !guidanceOnce(d.RunDir, d.SessionID, typ) {
		return Decision{}
	}
	return Decision{Action: "context", AdditionalContext: text}
}

// routeAgent ports routing.mjs:894-916: the routing block is appended to the
// first present prompt field, and subagent_type Bash becomes
// general-purpose. Keys keep their order; a new prompt key goes last. A
// non-string prompt counts as "" (TS would stringify it; Claude Code always
// sends a string).
func routeAgent(in *hookio.Object) Decision {
	field := "prompt"
	for _, f := range agentFields {
		if in.Has(f) {
			field = f
			break
		}
	}
	prompt, _ := in.String(field)
	out := in.Clone()
	out.SetString(field, prompt+agentBlock)
	if st, _ := in.String("subagent_type"); st == "Bash" {
		out.SetString("subagent_type", "general-purpose")
	}
	return Decision{Action: "modify", UpdatedInput: out}
}
