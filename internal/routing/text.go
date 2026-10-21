package routing

import "strings"

// ToolPrefix is the name prefix of cmgo's MCP tools under
// `claude mcp add --scope user cmgo` (spec §1.3).
const ToolPrefix = "mcp__cmgo__"

// named stands in for the TS tool namer t(): every $T in a TS template
// becomes ToolPrefix. It is applied to constant text only, never to input.
func named(s string) string {
	return strings.ReplaceAll(s, "$T", ToolPrefix)
}

// The routing block of createRoutingBlock (routing-block.mjs:16-80), cut at
// its two ${…} slots. Each optional section carries the newlines its slot
// adds, so an empty slot still leaves a blank line, as in TS.
var (
	blockHead = named(`
<context_window_protection>
  <priority_instructions>
    Every byte a tool returns enters your conversation memory and costs reasoning capacity for the rest of the session. The context-mode tools let you do the work in a sandbox and surface only the derived answer — the raw bytes stay out. Think-in-Code: program the analysis, do not compute it by reading raw data into your conversation.
  </priority_instructions>
`)
	blockBootstrap = named(`
  <deferred_tool_bootstrap>
    The context-mode tools below may be DEFERRED in your harness — their schemas are not loaded yet, so calling them directly fails (e.g. "tool not found" / InputValidationError). Load them ONCE before your first ctx_* call:
    ToolSearch(query: "select:$Tctx_batch_execute,$Tctx_search,$Tctx_execute,$Tctx_execute_file,$Tctx_fetch_and_index")
    After that they are callable. If any ctx_* call fails as not-found, ToolSearch it and retry — do NOT fall back to Bash/Read just because the schema was not loaded yet.
  </deferred_tool_bootstrap>
`)
	blockMiddle = named(`
  <tool_selection_hierarchy>
    0. MEMORY: $Tctx_search(sort: "timeline")
       - On resume or compaction, query prior decisions, errors, plans, user prompts before asking the user — auto-captured session memory is searchable.
    1. GATHER: $Tctx_batch_execute(commands, queries)
       - Primary research tool. Runs commands in parallel, auto-indexes each output, and (when queries are passed) returns matching sections in the same round trip — no follow-up search call.
       - Each command: {label: "section header", command: "shell command"}; the label becomes the FTS5 chunk title — descriptive labels improve search.
    2. FOLLOW-UP: $Tctx_search(queries: ["q1", "q2", ...])
       - Multiple related questions about anything already indexed (your captures + session memory). Batch every question in one array; the ranking pipeline runs per-query and the round-trip cost is paid once.
    3. PROCESSING: $Tctx_execute(language, code) | $Tctx_execute_file(path, language, code)
       - Derive answers FROM data: filter, count, aggregate, parse, transform. Only what you console.log() enters your conversation; the raw bytes stay in the sandbox.
  </tool_selection_hierarchy>

  <when_not_to_use>
    - You intend to PROCESS the output (filter, count, parse, aggregate) → use $Tctx_batch_execute or $Tctx_execute. Bash stays correct when you intend to OBSERVE a short fixed output (git status on a clean tree, whoami, pwd) or when you are mutating state (git, mkdir, rm, mv, navigation).
    - You want to analyze, summarize, or extract from a file → use $Tctx_execute_file. Read stays correct when you intend to Edit the file (Edit needs the exact bytes in your conversation to match against).
    - WebFetch → use $Tctx_fetch_and_index; full network access, results indexed for $Tctx_search, raw page bytes never enter your conversation.
    - $Tctx_execute and $Tctx_execute_file for file writes → these run code in a subprocess and discard the sandbox FS; they are for analysis, processing, and computation only.
  </when_not_to_use>

  <file_writing_policy>
    File writes use the native Write or Edit tool — $Tctx_execute, $Tctx_execute_file, and Bash subprocesses do not persist edits to the host filesystem.
    Applies to all file types: code, configs, plans, specs, YAML, JSON, markdown.
  </file_writing_policy>

  <output_constraints>
    <artifact_policy>
      Write artifacts (code, configs, PRDs) to files. Return only: file path + 1-line description.
    </artifact_policy>
  </output_constraints>
  <session_continuity>
    Skills, roles, and decisions captured earlier in this session are a memory aid, not a standing order. Treat them as context that may help — the user's most recent message always takes precedence. If a captured directive conflicts with what the user now asks, follow the user; a past phrase does not bind you.
  </session_continuity>
`)
	// Every TS command, now that cmgo has a tool for each (sub-project 5
	// spec §4). The trigger lists and the context-mode wording are kept verbatim.
	blockCommands = named(`
  <ctx_commands>
    "ctx stats" | "ctx-stats" | "/ctx-stats" | context savings question
    → Call stats MCP tool, display full output verbatim.

    "ctx doctor" | "ctx-doctor" | "/ctx-doctor" | diagnose context-mode
    → Call doctor MCP tool, run returned shell command, display as checklist.

    "ctx upgrade" | "ctx-upgrade" | "/ctx-upgrade" | update context-mode
    → Call upgrade MCP tool, run returned shell command, display as checklist.

    "ctx purge" | "ctx-purge" | "/ctx-purge" | wipe/reset knowledge base
    → Call purge MCP tool with confirm: true. Warn: irreversible.

    After /clear or /compact: knowledge base preserved. Tell user: "context-mode knowledge base preserved. Use ` + "`" + `ctx purge` + "`" + ` to start fresh."
  </ctx_commands>
`)
	blockTail = named(`
</context_window_protection>`)
)

// RoutingBlock ports createRoutingBlock with the cmgo tool namer.
// includeCommands adds the <ctx_commands> section and toolSearchBootstrap
// the <deferred_tool_bootstrap> section.
func RoutingBlock(includeCommands, toolSearchBootstrap bool) string {
	s := blockHead
	if toolSearchBootstrap {
		s += blockBootstrap
	}
	s += blockMiddle
	if includeCommands {
		s += blockCommands
	}
	return s + blockTail
}

// The guidance texts (routing-block.mjs:82-92) and the redirect messages
// (routing.mjs:771, 802, 815, 879).
var (
	readGuidance  = named("<context_guidance>\n  <tip>\n    Reading to Edit the file? Read is correct — Edit needs the exact bytes in your conversation to match against.\n    Reading to analyze, summarize, or extract from the file? Use $Tctx_execute_file(path, language, code) — the bytes stay in the sandbox and only what your code prints enters your conversation.\n  </tip>\n</context_guidance>")
	grepGuidance  = named("<context_guidance>\n  <tip>\n    Grep results may be larger than you expect. When you intend to count, filter, or aggregate matches (not just spot-check one), run the search through $Tctx_execute(language: \"javascript\", code: \"...\") — the raw match list stays in the sandbox and only your derived answer enters your conversation. Use language: \"shell\" only when the code matches the host shell (PowerShell on Windows, POSIX shell on Unix).\n  </tip>\n</context_guidance>")
	bashGuidance  = named("<context_guidance>\n  <tip>\n    When you intend to PROCESS the output (filter, count, parse, aggregate), use $Tctx_batch_execute(commands, queries) for multiple commands or $Tctx_execute(language: \"javascript\", code: \"...\") for one — the raw output stays in the sandbox and only what you print enters your conversation. Shell stays the right surface when you intend to OBSERVE a short fixed output or when you are mutating state (git, mkdir, rm, mv, navigation); if you use $Tctx_execute(language: \"shell\"), write syntax for the host shell.\n  </tip>\n</context_guidance>")
	curlMessage   = named(`context-mode: curl/wget redirected. Call $Tctx_execute(language, code) to fetch the URL, derive your answer in code, and print only the result — the raw HTTP body stays in the sandbox instead of entering your conversation. Or call $Tctx_fetch_and_index(url, source) when you want to query the response later via $Tctx_search. Both have full network access. Retry the same call on a transient DNS error (EAI_AGAIN, ETIMEDOUT, ENETUNREACH).`)
	inlineMessage = named(`context-mode: Inline HTTP redirected. Call $Tctx_execute(language, code) to fetch, derive your answer in code, and console.log() only the result — the raw response body stays in the sandbox instead of entering your conversation. Full network access. Retry the same call on a transient DNS error (EAI_AGAIN, ETIMEDOUT, ENETUNREACH).`)
	buildHead     = named(`context-mode: Build tool redirected. Call $Tctx_execute(language: \"shell\", code: \"`)
	buildTail     = ` 2>&1 | tail -30\") to run the build and print only the tail — the verbose build log stays in the sandbox instead of entering your conversation. For more targeted output, replace \"tail -30\" with \"grep -E '(error|warning|FAIL|✗|×)'\" or similar, so only the lines that matter come back.`
	webFetchHead  = named(`context-mode: WebFetch redirected. Call $Tctx_fetch_and_index(url: "`)
	webFetchTail  = named(`", source: "...") to fetch + index the page, then $Tctx_search(queries: [...]) to query the indexed content — the raw page bytes stay in storage instead of entering your conversation. Or call $Tctx_execute(language, code) when you want to derive your answer in one round trip (parse, extract, count) without persisting the response. Both have full network access. Retry the same call on a transient DNS error (EAI_AGAIN, ETIMEDOUT, ENETUNREACH).`)
)

// buildMessage is the build-tool redirect text for command. The command is
// escaped as routing.mjs:811 does, and the template's \" stay literal: the
// Claude Code formatter never unescapes them.
func buildMessage(command string) string {
	safe := strings.ReplaceAll(command, `\`, `\\`)
	safe = strings.ReplaceAll(safe, `"`, `\"`)
	return buildHead + safe + buildTail
}

// webFetchMessage is the WebFetch deny text; url goes in raw (R1 P16).
func webFetchMessage(url string) string {
	return webFetchHead + url + webFetchTail
}
