package mcpserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerExecuteFile registers ctx_execute_file. Port of S:2042-2230.
func registerExecuteFile(s *mcp.Server, d *Deps) {
	s.AddTool(&mcp.Tool{
		Name:        "ctx_execute_file",
		Title:       "Run code over a file (executes code, reads the given path)",
		Description: executeFileDesc,
		Annotations: execAnnotations,
		InputSchema: json.RawMessage(executeFileSchema),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a, err := ParseArgs(req.Params.Arguments)
		if err != nil {
			return ArgError(err), nil
		}
		path, ok, err := a.String("path")
		if err == nil && !ok {
			err = errors.New("path: required")
		}
		if err != nil {
			return ArgError(err), nil
		}
		e, err := parseExecArgs(a)
		if err != nil {
			return ArgError(err), nil
		}
		// Project boundary, then Read() deny, then the code deny check (S:2118-2132).
		if err := d.Policy.CheckReadPath(path); err != nil {
			return TextResult(err.Error(), true), nil
		}
		if denied := denyCode(d, e); denied != nil {
			return denied, nil
		}

		r, err := d.Exec.Run(ctx, executor.Request{Language: e.lang, Code: e.code, Timeout: e.timeout, FilePath: path})
		if err != nil {
			return ErrorResult("Runtime error: %v", err), nil
		}
		echo := buildExecuteEcho(string(e.lang), e.code, path)
		if r.TimedOut {
			return TextResult(echo+"Timed out processing "+path+" after "+e.msText()+"ms", true), nil
		}
		return finishRun(d, r, e, echo, "file:"+path), nil
	})
}

// Description and schema below are generated from server.ts S:2056-2112.

const executeFileDesc = `Read a file into a sandboxed FILE_CONTENT variable and run code over it. Only what you console.log() enters your conversation — the file bytes stay in the sandbox.

Think-in-Code applied to file-level analysis: Reading the whole file means every byte enters your conversation memory and costs reasoning capacity for the rest of the session. Running code over it here lets you keep the raw bytes out and only the derived answer in. Same principle as ctx_execute, scoped to one named file via the FILE_CONTENT variable.

WHEN:
  - You want to KNOW SOMETHING ABOUT a file (line count, matches of a pattern, parsed structure, statistical aggregate) without needing to SEE all of it
  - The file is structured (CSV, JSON, log, code) and a code-level derivation is cheaper than reading verbatim
  - The file is large enough that reading the full content would burn meaningful conversation memory you need for the actual work
  - The derivation may itself produce a large output you want recall-by-topic on later — pass an ` + "`" + `intent` + "`" + ` string; outputs over ~5KB are auto-indexed and only matching sections come back, retrievable via ctx_search

WHEN NOT:
  - You intend to EDIT the file — use Read so the subsequent Edit can match the exact text
  - You only need one specific line and you know its offset — Read with offset/limit is the simplest path
  - The file is small AND you will consume all of it for understanding/editing — Read directly

RETURNS:
  Only what your code prints. The FILE_CONTENT variable holds the raw bytes inside the sandbox; nothing else leaves. When ` + "`" + `intent` + "`" + ` is set and output exceeds the auto-index threshold, the response carries searchable section titles + previews instead of the raw stdout.

EXAMPLE: ctx_execute_file(path: "huge.log", language: "javascript", code: "const errs = FILE_CONTENT.split('\\n').filter(l => /ERROR|FATAL/.test(l)); console.log(` + "`" + `${errs.length} error lines` + "`" + `); console.log(errs.slice(-5).join('\\n'))")
EXAMPLE: ctx_execute_file(path: "data.csv", language: "javascript", code: "const rows = FILE_CONTENT.split('\\n'); console.log(` + "`" + `rows: ${rows.length - 1}, header: ${rows[0]}` + "`" + `)")`

const executeFileSchema = `{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "Absolute file path or relative to project root"
		},
		"language": {
			"type": "string",
			"enum": ["javascript", "typescript", "python", "shell", "ruby", "go", "rust", "php", "perl", "r", "elixir", "csharp"],
			"description": "Runtime language"
		},
		"code": {
			"type": "string",
			"description": "Code to process FILE_CONTENT (file_content in Elixir). Print summary via console.log/print/echo/IO.puts/Console.WriteLine."
		},
		"timeout": {
			"type": ["number", "string"],
			"description": "Max execution time in ms. When omitted, no server-side timer fires — the MCP host's RPC timeout governs."
		},
		"intent": {
			"type": "string",
			"description": "What you're looking for in the output. When provided and output is large (>5KB), returns only matching sections via BM25 search instead of truncated output."
		}
	},
	"required": ["path", "language", "code"]
}`
