package mcpserver

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// execArgs are the arguments ctx_execute and ctx_execute_file share.
type execArgs struct {
	lang         executor.Language
	code, intent string
	timeoutMS    float64 // as given, for "after Nms" texts
	timeout      time.Duration
}

// parseExecArgs validates language/code/timeout/intent like the TS zod schemas
// (S:1692-1741, S:2076-2112).
func parseExecArgs(a Args) (execArgs, error) {
	var e execArgs
	lang, ok, err := a.String("language")
	if err != nil {
		return e, err
	}
	e.lang = executor.Language(lang)
	if !ok || !slices.Contains(executor.Languages, e.lang) {
		return e, fmt.Errorf("language: invalid enum value %q, expected one of %v", lang, executor.Languages)
	}
	if e.code, ok, err = a.String("code"); err != nil || !ok {
		return e, cmp.Or(err, fmt.Errorf("code: required"))
	}
	if e.intent, _, err = a.String("intent"); err != nil {
		return e, err
	}
	ms, ok, err := a.Number("timeout")
	if err != nil {
		return e, err
	}
	// shortcut: timeout <= 0 means no timer (Go executor), where TS setTimeout(0) fires at once.
	if ok && ms > 0 {
		e.timeoutMS, e.timeout = ms, time.Duration(ms*float64(time.Millisecond))
	}
	return e, nil
}

// msText formats the timeout like JS `${n}` (2000, 1.5).
func (e execArgs) msText() string { return strconv.FormatFloat(e.timeoutMS, 'f', -1, 64) }

// denyCode ports the deny-only firewall dispatch (S:1745-1751, S:2126-2132).
func denyCode(d *Deps, e execArgs) *mcp.CallToolResult {
	blocked, cmd, pattern := d.Policy.CheckCodeMatch(string(e.lang), e.code)
	switch {
	case !blocked:
		return nil
	case cmd == "":
		return TextResult(security.BlockedMessage(pattern), true)
	default:
		return TextResult(security.EmbeddedBlockedMessage(cmd, pattern), true)
	}
}

// finishRun is the shared tail of both handlers after the timeout branch:
// exit classification, then intent search, auto-index or inline output.
// Port of S:1879-1941 and S:2159-2219.
func finishRun(d *Deps, r executor.Result, e execArgs, echo, label string) *mcp.CallToolResult {
	out, isError := r.Stdout, false
	if r.ExitCode != 0 {
		if !executor.IsSoftFail(e.lang, r.ExitCode, r.Stdout) {
			isError, label = true, label+":error"
			out = fmt.Sprintf("Exit code: %d\n\nstdout:\n%s\n\nstderr:\n%s", r.ExitCode, r.Stdout, r.Stderr)
		}
	} else if out == "" {
		out = "(no output)"
	}

	var text string
	var err error
	switch {
	case strings.TrimSpace(e.intent) != "" && len(out) > intentSearchThreshold:
		text, err = intentSearch(d, out, e.intent, label)
	case len(out) > largeOutputThreshold && r.ExitCode != 0:
		text, err = intentSearch(d, out, "errors failures exceptions", label)
	case len(out) > largeOutputThreshold:
		text, err = indexStdout(d, out, label)
	default:
		text = out
	}
	if err != nil {
		return ErrorResult("Runtime error: %v", err)
	}
	return TextResult(echo+text, isError)
}

// execAnnotations are the TS #846 hints shared by both tools.
var execAnnotations = &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: false, OpenWorldHint: new(true)}

// registerExecute registers ctx_execute. Port of S:1647-1952.
func registerExecute(s *mcp.Server, d *Deps) {
	s.AddTool(&mcp.Tool{
		Name:        "ctx_execute",
		Title:       "Run code in a sandbox (executes the supplied code)",
		Description: executeDesc,
		Annotations: execAnnotations,
		InputSchema: json.RawMessage(executeSchema),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (res *mcp.CallToolResult, _ error) {
		var raw int64
		defer func() { d.Stats.Record(d, "ctx_execute", raw, res) }()
		a, err := ParseArgs(req.Params.Arguments)
		if err != nil {
			return ArgError(err), nil
		}
		e, err := parseExecArgs(a)
		if err != nil {
			return ArgError(err), nil
		}
		background, _, err := a.Bool("background")
		if err != nil {
			return ArgError(err), nil
		}
		cwd, _, err := a.String("cwd")
		if err != nil {
			return ArgError(err), nil
		}
		if denied := denyCode(d, e); denied != nil {
			return denied, nil
		}

		r, err := d.Exec.Run(ctx, executor.Request{Language: e.lang, Code: e.code, Timeout: e.timeout, Background: background, Cwd: cwd})
		if err != nil {
			return ErrorResult("Runtime error: %v", err), nil
		}
		raw = int64(len(r.Stdout)+len(r.Stderr)) + r.SandboxBytes
		echo := buildExecuteEcho(string(e.lang), e.code, "")
		if r.TimedOut {
			partial := strings.TrimSpace(r.Stdout)
			switch {
			case r.Backgrounded && partial != "":
				return TextResult(echo+partial+"\n\n_(process backgrounded after "+e.msText()+"ms — still running)_", false), nil
			case partial != "":
				return TextResult(echo+partial+"\n\n_(timed out after "+e.msText()+"ms — partial output shown above)_", false), nil
			}
			return TextResult(echo+"Execution timed out after "+e.msText()+"ms\n\nstderr:\n"+r.Stderr, true), nil
		}
		return finishRun(d, r, e, echo, "execute:"+string(e.lang)), nil
	})
}

// Description and schema below are generated from server.ts S:1660-1741
// (bunNote dropped; langList is all 12 languages).

const executeDesc = `Run code in a sandboxed subprocess. Languages: javascript, typescript, python, shell, ruby, go, rust, php, perl, r, elixir, csharp.

Think-in-Code — the core philosophy: the bytes your code processes never enter your conversation memory; only what you console.log() does. Reading a 700 KB log directly means 700 KB of your remaining reasoning capacity gets spent on raw bytes. Running code over that same log in this sandbox and printing a 3 KB summary leaves you with 697 KB of capacity for the actual work.

Concrete shape — analyze 47 source files without reading any of them:
  ctx_execute(language: "javascript", code: ` + "`" + `
    const fs = require('fs');
    const files = fs.readdirSync('src').filter(f => f.endsWith('.ts'));
    files.forEach(f => {
      const lines = fs.readFileSync('src/'+f,'utf8').split('\\n').length;
      console.log(f + ': ' + lines + ' lines');
    });
  ` + "`" + `)
  // 47 files analyzed, 15,314 LoC summarized — output ~3.6 KB instead of 47 Read() calls = ~700 KB.

WHEN:
  - You intend to derive an answer FROM data (filter, count, aggregate, parse, compare, transform) — do the derivation in code and print only the answer
  - Output shape or size cannot be predicted before execution (recursive finds, repo-wide greps, list endpoints, query results, log scans)
  - You would otherwise read raw output and then mentally compute — that compute belongs here, in code, where its inputs stay out of your conversation
  - You need to keep a long-running process alive (dev server, watcher, daemon) — pass ` + "`" + `background: true` + "`" + ` to detach on timeout instead of killing the process
  - The output may legitimately be large but you only want recall-by-topic later — pass an ` + "`" + `intent` + "`" + ` string; outputs over ~5KB are auto-indexed into the knowledge base and only the section titles + previews come back, retrievable via ctx_search

WHEN NOT:
  - Single observational command whose entire short output you intend to consume verbatim (whoami, pwd, git status on a clean tree) — Bash is simpler
  - File mutations (Edit/Write) or navigation (cd/ls) — Bash is the right surface
  - You already know the output is one short fixed line and you want to read it as-is

RETURNS:
  Only what your code prints. Wrap risky calls in try/catch — uncaught errors go to stderr and may leak more than intended. When ` + "`" + `intent` + "`" + ` is set and output exceeds the auto-index threshold, the response carries searchable section titles + previews instead of the raw stdout; use ctx_search(queries: [...]) to drill into specific sections.

EXAMPLE: ctx_execute(language: "javascript", code: "const out = require('child_process').execSync('npm test', {encoding:'utf8', stdio:['ignore','pipe','pipe']}); console.log(out.split('\\n').filter(l => /(FAIL|✗|×|Error:|Tests +.*(failed|passed))/i.test(l)).slice(0, 60).join('\\n'))")
EXAMPLE: ctx_execute(language: "javascript", code: "const out = require('child_process').execSync('gh issue list --json number,title --limit 100', {encoding:'utf8'}); const hooks = JSON.parse(out).filter(i => /hook|routing/i.test(i.title)); console.log(` + "`" + `${hooks.length} hook-related issues` + "`" + `)")`

const executeSchema = `{
	"type": "object",
	"properties": {
		"language": {
			"type": "string",
			"enum": ["javascript", "typescript", "python", "shell", "ruby", "go", "rust", "php", "perl", "r", "elixir", "csharp"],
			"description": "Runtime language"
		},
		"code": {
			"type": "string",
			"description": "Source code to execute. Use console.log (JS/TS), print (Python/Ruby/Perl/R), echo (Shell), echo (PHP), fmt.Println (Go), IO.puts (Elixir), or Console.WriteLine (C#) to output a summary to context."
		},
		"timeout": {
			"type": ["number", "string"],
			"description": "Max execution time in ms. When omitted, no server-side timer fires — the MCP host's RPC timeout governs (which is the right layer for this policy). Pass an explicit value for long-running builds (Gradle/Maven/SBT)."
		},
		"background": {
			"type": ["boolean", "string"],
			"default": false,
			"description": "Keep process running after timeout (for servers/daemons). Returns partial output without killing the process. IMPORTANT: Do NOT add setTimeout/self-close timers in background scripts — the process must stay alive until the timeout detaches it. For server+fetch patterns, prefer putting both server and fetch in ONE ctx_execute call instead of using background."
		},
		"cwd": {
			"type": "string",
			"description": "Optional working directory for shell commands. Non-shell languages still execute from their sandbox temp directory."
		},
		"intent": {
			"type": "string",
			"description": "What you're looking for in the output. When provided and output is large (>5KB), indexes output into knowledge base and returns section titles + previews — not full content. Use ctx_search(queries: [...]) to retrieve specific sections. Example: 'failing tests', 'HTTP 500 errors'.\n\nTIP: Use specific technical terms, not just concepts. Check 'Searchable terms' in the response for available vocabulary."
		}
	},
	"required": ["language", "code"]
}`
