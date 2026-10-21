# cmgo hooks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Claude Code hooks to cmgo:
- PreToolUse routing and blocking;
- session event capture into `cmgo.db`;
- the SessionStart routing block;
- `cmgo install` / `cmgo uninstall`.

Hooks run as a small `cmgo-hook` binary with p95 ≤ 10 ms per fire, matching the TypeScript plugin's decisions and texts.

**Architecture:**
- `cmgo-hook <event>` is a short-lived process per Claude Code hook event. It reads one JSON object from stdin, prints at most one JSON line, and opens the shared SQLite store only when it has events to write.
- New packages, in this order:
  - `hookio`: JS-compatible JSON and ordered objects;
  - `routing`: PreToolUse decisions and texts;
  - `capture`: event extractors;
  - `install`: settings.json editing;
  - `hookcmd`: the entry point.
- `store` gains the session tables (migration 2), and `security` gains `EvaluateCommand`.
- Parity with TS commit `a5e6dd31` is proven by golden files generated from the TS modules.

**Tech Stack:**
- Go 1.27.2 and the existing dependencies; this plan adds none.
- Python 3 for the latency bench.
- Node, only to regenerate golden files by hand.

**Spec:** `docs/superpowers/specs/2026-10-21-cmgo-hooks-design.md` (sub-project 2 of 5). It builds on the core spec `docs/superpowers/specs/2026-10-20-cmgo-core-design.md`. Read both alongside this plan. Hooks spec §2.3 and §3.4 list every intentional difference from TS.

**How the code in this plan was produced:**
- Every code block was built and tested in a draft module on top of sub-project 1 before it was copied in here, verbatim.
- The assembled plan was then replayed task by task, Task 1 through 39, into a fresh worktree of this branch, with `go vet` and that task's package tests run after each task.
- Appendix B has the result and lists what the replay did not exercise.

Each code block is the complete file named on the line above it. When a later task shows a file that an earlier task or sub-project 1 created, replace the whole file.

## Global Constraints

- **Repo and branch:** the repo is `~/GolandProjects/github.com/lang315/context-mode-go`, module `github.com/lang315/context-mode-go`. Work on branch `sp2-hooks`; `go.mod` is unchanged by this plan.
- **Platforms:** macOS and Linux only. Write no Windows code paths. Verified on macOS arm64 only.
- **Dependencies:** none added. `cmd/cmgo-hook` must not link the MCP SDK, `net/http`, HTML conversion, `internal/executor`, `internal/fetch` or `internal/mcpserver`; a test checks this.
- **Data:**
  - Events go to `$CMGO_DIR/cmgo.db`, where `CMGO_DIR` defaults to `~/.claude/cmgo`.
  - Markers and sentinels go to `$CMGO_DIR/run/`.
  - Never write to `~/.claude/context-mode`, to the real `~/.claude` from tests, or to the TS repo.
  - Tests use `t.TempDir()`.
- **Never run the installer on the real `~/.claude`** while implementing. Tests run it on temp files.
- **Parity with TS:** decisions, texts, event types, categories, priorities and data formats match TS `a5e6dd31`, after the tool-name change `mcp__plugin_context-mode_context-mode__` → `mcp__cmgo__`, except the rows in hooks spec §2.3 and §3.4.
- **JS semantics:**
  - Lengths TS takes with `.length` or `.slice` are UTF-16 units (`hookio.JSLen`, `hookio.JSPrefix`). Lengths TS takes with `[...s]` are code points.
  - JSON output is compact, keeps key order, and does not escape `<`, `>`, `&`, U+2028 or U+2029.
- **Fail open:** a hook never blocks Claude Code because of its own error. It exits 0 with no output and logs one line to `$CMGO_DIR/hook-errors.log`.
- **Constants:**

  | Constant | Value |
  |---|---|
  | Events per session | 1000 |
  | Dedup window | 5 |
  | Event data cap | 32 KiB |
  | Session retention | 7 days |
  | Large Read | over 50,000 bytes |
  | Redirect summary | 200 UTF-16 units |
  | Stop message | 2000 UTF-16 units |
  | stdin | 16 MiB max, 2 s deadline |
  | Hook p95 | ≤ 10 ms |

- **Process markers:** never put a literal process marker in a shell command that also runs `pkill` or `pgrep`; build it at runtime.
- **Commits:** Conventional Commits, in English, one per task (the task's last step).

## Review Focus

These are the five conditions most likely to hurt someone using the hooks that the spec implies but does not list. Each has a test in the task that owns the code.

1. **`cmgo serve` and `cmgo-hook` disagree on where the MCP-ready sentinel lives.** Every curl, inline-HTTP, build-tool and WebFetch redirect would then pass through silently, because hooks fail open. `TestBinaryServeSentinelGatesRedirect` starts a real `serve` and a separate `cmgo-hook`, checks that the redirect fires, then checks that it passes through once `serve` exits (Task 37).
2. **Several sessions fire hooks into the same DB at once.** No hook may fail with `database is locked`, and none may lose events. `TestInsertEventsConcurrentStores` (Task 6) covers this in-process, and the bench's 4-worker case measures it end to end (Task 39).
3. **The installer meets a real `settings.json`:**
   - user hooks sharing an event or a matcher entry with cmgo's;
   - an old install path;
   - a file that does not parse;
   - a symlinked file;
   - an uninstall afterwards.

   Nothing of the user's may be lost or reordered. The tests are `TestInstallKeepsUserHooks`, `TestInstallReplacesOldPath`, `TestInstallUnparseable`, `TestSymlinkedSettings` and `TestUninstallRoundTrip` (Task 12).
4. **Claude Code sends empty, malformed, slow or unexpected input, or a hook panics.** The hook must exit 0 with no output and a log line, and never hang. The tests are `TestHookFailOpen`, `TestHookEmptyStdin`, `TestHookRecoversPanic`, `TestHookStdinTimeout` (Task 33) and `TestBinaryHookMalformedStdin` (Task 36).
5. **A long session reaches the 1000-event cap.** User prompts, files, rules and tasks (priority 1) must survive. Lower-priority rows go first. TS did the opposite. The test is `TestInsertEventsEvictsLeastImportant` (Task 7).

## File Structure

| Path | Responsibility |
|---|---|
| `internal/hookio` | JSON strings exactly as JS `JSON.stringify` writes them, UTF-16 lengths, an order-preserving `Object`, stdin reading, session id and file-safe ids |
| `internal/store/events.go` (+ migration 2 in `store.go`) | `session_meta` and `session_events`, `InsertEvents` (dedup, cap, eviction, cut), `SessionEvents`, the inactive-session sweep in `CleanupIfDue` |
| `internal/security/evaluate.go` | `EvaluateCommand`: deny, ask and allow over chained commands, in settings precedence order |
| `internal/install` | Order-preserving JSON tree for settings files, cmgo hook entries, install and uninstall, MCP registration via the `claude` CLI |
| `internal/routing` | PreToolUse: shell matchers ported from JS regexes, routing and guidance texts, guidance throttle, MCP-ready sentinel, `Route`, Claude Code output format, PreToolUse events |
| `internal/capture` | Event extractors for PostToolUse, PostToolUseFailure, UserPromptSubmit, Stop and SessionStart |
| `internal/hookcmd` | Hook entry point: stdin with deadline, config, dispatch per event, store writes, fail-open logging |
| `cmd/cmgo-hook` | The small hook binary |
| `cmd/cmgo` | `hook` alias, `install`, `uninstall`; `serve` writes the MCP-ready sentinel |
| `tools/golden-gen/gen-hooks.mjs`, `testdata/golden/hooks-*` | TS-generated goldens and the known-differences list (Task 1, so later golden tests can read them) |
| `bench/hook-latency.py` | Acceptance script for spec criterion 1 |

## Task order

| Task | Content |
|---|---|
| 1 | Hooks golden generator and golden files |
| 2 | JS-compatible JSON strings and UTF-16 lengths |
| 3 | Order-preserving JSON Object |
| 4 | ReadInput, SessionID and FileID |
| 5 | Migration 2 adds the session tables |
| 6 | InsertEvents, SessionEvents and the 32 KiB cut |
| 7 | Dedup window and eviction at the cap |
| 8 | Sweep inactive sessions in CleanupIfDue |
| 9 | EvaluateCommand with Bash allow and ask rules |
| 10 | Order-preserving JSON tree |
| 11 | HookEvents, own-command detection, strip and add |
| 12 | Install and Uninstall settings file: read, backup, atomic write, dry run, TS plugin |
| 13 | MCP registration through the claude CLI |
| 14 | hook commands run cmgo-hook |
| 15 | JS whitespace, regex sources and parseInt |
| 16 | Heredoc and quote stripping, structurally bounded commands |
| 17 | curl/wget, inline-HTTP and build-tool matchers |
| 18 | Routing block, guidance and redirect texts |
| 19 | Guidance throttle and prune |
| 20 | MCP-ready sentinel |
| 21 | Route |
| 22 | Claude Code output format and PreToolUse events |
| 23 | Golden replay of the TS PreToolUse cases |
| 24 | JS string, regexp and JSON-text helpers |
| 25 | File, rule, cwd, env, task, skill, subagent and worktree extractors |
| 26 | Git extractor with the subcommand lookup fix |
| 27 | Plan extractor and FNV-1a plan hash |
| 28 | AskUserQuestion decision extractor |
| 29 | UserPrompt: skip rules, user_prompt and the user extractors |
| 30 | PostToolUseFailure, Stop and SessionStart events |
| 31 | PostToolUse dispatcher and the TS golden replay |
| 32 | run takes stdin |
| 33 | hookcmd: dispatcher, fail-open policy and PreToolUse |
| 34 | hookcmd: SessionStart |
| 35 | hookcmd: PostToolUse, PostToolUseFailure, UserPromptSubmit and Stop |
| 36 | cmgo-hook binary, cmgo hook alias and built-binary hook tests |
| 37 | serve writes the MCP-ready sentinel |
| 38 | install and uninstall commands |
| 39 | hook latency bench |

Suggested review checkpoints, after:
- Task 1 (golden generator);
- Task 4 (`hookio`);
- Task 9 (`store` and `security`);
- Task 14 (`install`);
- Task 23 (`routing`);
- Task 31 (`capture`);
- Task 39 (`cmd` and bench).

---

### Task 1: Hooks golden generator and golden files

**Files:**
- Create: `tools/golden-gen/gen-hooks.mjs`
- Create: `testdata/golden/HOOKS-KNOWN-DIFFS.md`
- Generate: `testdata/golden/hooks-pretooluse.json`, `testdata/golden/hooks-capture.json`, `testdata/golden/hooks-strings.json`

**Interfaces:**
- Consumes: TS fork at `$TS_REPO` (default `/Users/lang/GolandProjects/github.com/lang315/context-mode`) at commit `a5e6dd31`, with `node_modules` and `build/` present. It uses these exports: `routePreToolUse(toolName, toolInput, projectDir, platform, sessionId, options)` and `initSecurity(buildDir)` from `hooks/core/routing.mjs`; `formatDecision(platform, decision)` from `hooks/core/formatters.mjs`; `createRoutingBlock(t, { includeCommands, toolSearchBootstrap })`, `createBashGuidance(t)`, `createReadGuidance(t)` and `createGrepGuidance(t)` from `hooks/routing-block.mjs`; `extractEvents({ tool_name, tool_input, tool_response })`, `extractUserEvents(prompt)`, `resetErrorResolutionState()` and `resetIterationLoopState()` from `hooks/session-extract.bundle.mjs`.
- Produces: `hooks-pretooluse.json` = `{ tsCommit, cases: [{ i, tool, input, projectDir ("<proj>" | ""), sessionId, subagent, decision, output (TS hook stdout | null) }] }`; `hooks-capture.json` = `{ tsCommit, toolCases: [{ name, input, output: [event] }], userCases: [{ prompt, events: [event] }] }`; `hooks-strings.json` = `{ tsCommit, routingBlocks: [{ name, includeCommands, toolSearchBootstrap, text }], texts: { "guidance-bash", "guidance-read", "guidance-grep", "deny-curl-wget", "deny-inline-http", "deny-build-gradlew-example", "deny-webfetch-example" } }`. Task 23 and Task 31 read these files.

- [ ] **Step 1: Write the failing test**

The test runs the generator twice, each time with a fresh temp dir as `TMPDIR`. It passes only if both runs exit 0, both leave their temp dir empty (`rmdir` fails otherwise), and both write the same bytes:

```bash
gen() {
  T=$(mktemp -d) &&
  TMPDIR=$T TS_REPO=/Users/lang/GolandProjects/github.com/lang315/context-mode node tools/golden-gen/gen-hooks.mjs >/dev/null &&
  rmdir "$T" && md5sum testdata/golden/hooks-*.json
}
a=$(gen) && b=$(gen) && [ "$a" = "$b" ] && echo "$a" && echo "deterministic: OK"
```

- [ ] **Step 2: Run test to verify it fails**

Run: the Step 1 block from the repo root.
Expected: FAIL with `Error: Cannot find module '<repo>/tools/golden-gen/gen-hooks.mjs'`, and no `deterministic: OK` line.

- [ ] **Step 3: Write minimal implementation**

`tools/golden-gen/gen-hooks.mjs`:
```js
#!/usr/bin/env node
// Hooks golden generator for cmgo (spec §6.2). Run by hand, never by go test:
//
//   TS_REPO=/path/to/context-mode node tools/golden-gen/gen-hooks.mjs
//
// Writes testdata/golden/hooks-{pretooluse,capture,strings}.json from the TS
// fork at $TS_REPO (commit a5e6dd31). It imports hooks/core/routing.mjs,
// hooks/core/formatters.mjs, hooks/routing-block.mjs and
// hooks/session-extract.bundle.mjs directly; it never runs a hook script,
// start.mjs or postinstall. All TS code runs in child node processes whose
// HOME, TMPDIR, CONTEXT_MODE_DIR and CONTEXT_MODE_MCP_SENTINEL_DIR (the MCP
// ready sentinel dir, /tmp by default) point into a fresh temp dir, removed
// at the end. Nothing is written to ~/.claude or $TS_REPO.
//
// Each PreToolUse case runs in its own process, as a real hook does: TS keeps
// a process-wide in-memory guidance set (R1 P18), so only the on-disk markers
// under the shared TMPDIR carry over from one case to the next.
// Every mcp__plugin_context-mode_context-mode__ in the output becomes
// mcp__cmgo__ (spec §1.3).
import { execFileSync, spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const SELF = fileURLToPath(import.meta.url);
const ROOT = resolve(dirname(SELF), "../..");
const OUT = join(ROOT, "testdata/golden");
const TS_REPO = resolve(process.env.TS_REPO || "/Users/lang/GolandProjects/github.com/lang315/context-mode");
const TS_COMMIT = "a5e6dd31";
const TS_PREFIX = "mcp__plugin_context-mode_context-mode__";
const CMGO_PREFIX = "mcp__cmgo__";
const tsModule = (path) => import(pathToFileURL(join(TS_REPO, path)).href);

// ─────────────────────────────────────────────────────────
// PreToolUse cases (routePreToolUse + formatDecision, platform claude-code)
// ─────────────────────────────────────────────────────────

// The project rules written to <base>/proj/.claude/settings.json.
const PROJECT_RULES = { permissions: {
  deny: ["Bash(sudo *)", "Read(**/.env)"], ask: ["Bash(git push:*)"], allow: ["Bash(curl:*)"] } };

// [tool, input, inProject, sessionId]; subagent cases run with
// mcpToolsAvailable: false. <base> holds small.txt and big.txt, one byte
// each side of the 50,000-byte Read threshold. The order matters: session
// s1's guidance markers carry over.
function preToolUseCases(base) {
  const small = join(base, "small.txt");
  const big = join(base, "big.txt");
  const x = (tool) => TS_PREFIX + tool;
  const main = [
    ["Bash", { command: "curl https://x.com" }],
    ["Bash", { command: "curl -s -o out.json https://x.com" }],
    ["Bash", { command: "curl -s https://x.com 2>/dev/null" }],
    ["Bash", { command: "wget -q -O - https://x.com" }],
    ["Bash", { command: "gh issue edit --body \"use curl here\"" }],
    ["Bash", { command: "node -e \"fetch('https://a.b')\"" }],
    ["Bash", { command: "python -c 'import requests; requests.get(u)'" }],
    ["Bash", { command: "./gradlew build" }],
    ["Bash", { command: "mvn \"test\" C:\\x" }],
    ["Bash", { command: "git status" }],
    ["Bash", { command: "ls -la | head" }],
    ["Bash", { command: "find / -name x" }],
    ["Bash", { command: "find / -name y" }],
    ["Bash", { command: "sudo rm -rf /" }, true],
    ["Bash", { command: "echo ok && sudo ls" }, true],
    ["Bash", { command: "git push origin main" }, true],
    ["Bash", { command: "curl https://allowed.example" }, true],
    ["Read", { file_path: small }],
    ["Read", { file_path: big }],
    ["Read", { file_path: small }, false, "s2"],
    ["Grep", { pattern: "x" }],
    ["WebFetch", { url: "https://example.com/a", prompt: "p" }],
    ["Agent", { prompt: "do X", subagent_type: "Explore", description: "d" }],
    ["Agent", { prompt: "do Y", subagent_type: "Bash" }],
    ["Task", { prompt: "do Z" }],
    [x("ctx_execute"), { language: "shell", code: "ls" }, true],
    [x("ctx_execute"), { language: "shell", code: "sudo ls" }, true],
    [x("ctx_execute"), { language: "javascript", code: "1" }, true],
    [x("ctx_execute_file"), { path: "a/.env", language: "shell", code: "cat" }, true],
    [x("ctx_batch_execute"), { commands: [{ label: "L", command: "sudo id" }] }, true],
    [x("ctx_batch_execute"), { commands: [{ label: "L", command: "id" }] }, true],
    ["mcp__slack__list", {}],
    ["mcp__slack__list", {}],
    ["Write", { file_path: "x" }],
  ].map(([tool, input, inProject = false, sessionId = "s1"]) => ({ tool, input, inProject, sessionId, subagent: false }));
  const sub = [
    ["Bash", { command: "curl https://x.com" }],
    ["WebFetch", { url: "https://e.com" }],
    ["Bash", { command: "find . -type f" }],
  ].map(([tool, input]) => ({ tool, input, inProject: false, sessionId: "sub1", subagent: true }));
  return [...main, ...sub];
}

// ─────────────────────────────────────────────────────────
// Capture cases (extractEvents, extractUserEvents). The first 27 tool cases
// and 11 prompts are the original research cases; the rest
// were added for internal/capture.
// ─────────────────────────────────────────────────────────

let m; // hooks/session-extract.bundle.mjs, loaded by capturePhase

// Mirrors posttooluse.mjs:51-58: tool_response is stringified unless a string.
const post = (tool_name, tool_input, tool_response) => {
  m.resetErrorResolutionState?.(); m.resetIterationLoopState?.(); // fresh process per hook
  return m.extractEvents({
    tool_name, tool_input: tool_input ?? {},
    tool_response: typeof tool_response === "string" ? tool_response : JSON.stringify(tool_response ?? ""),
  });
};
const bash = (command, stdout = "") => ({ tool_name: "Bash", tool_input: { command }, tool_response: { stdout, stderr: "", interrupted: false, isImage: false } });

const seedTools = [
  ["Read plain source file (CC object response)", {"tool_name":"Read","tool_input":{"file_path":"/p/src/a.ts"},"tool_response":{"type":"text","file":{"filePath":"/p/src/a.ts","content":"x","numLines":1,"startLine":1,"totalLines":1}}}],
  ["Read CLAUDE.md (rule + rule_content + file_read)", {"tool_name":"Read","tool_input":{"file_path":"/p/CLAUDE.md"},"tool_response":"# rules"}],
  ["Read flat FileReadOutput string", {"tool_name":"Read","tool_input":{"file_path":"/p/a.ts"},"tool_response":"{\"type\":\"text\",\"numLines\":10,\"totalLines\":200,\"startLine\":5}"}],
  ["Edit", {"tool_name":"Edit","tool_input":{"file_path":"/p/a.ts","old_string":"a","new_string":"b"},"tool_response":{"filePath":"/p/a.ts"}}],
  ["Write to plan file", {"tool_name":"Write","tool_input":{"file_path":"/home/u/.claude/plans/my-plan.md","content":"x"},"tool_response":"ok"}],
  ["NotebookEdit", {"tool_name":"NotebookEdit","tool_input":{"notebook_path":"/p/n.ipynb"},"tool_response":"ok"}],
  ["Glob", {"tool_name":"Glob","tool_input":{"pattern":"**/*.ts"},"tool_response":"a.ts"}],
  ["Grep with path", {"tool_name":"Grep","tool_input":{"pattern":"foo","path":"/p/src"},"tool_response":"x"}],
  ["Grep no path", {"tool_name":"Grep","tool_input":{"pattern":"foo"},"tool_response":"x"}],
  ["Bash cd + git pull (CC object response)", {"tool_name":"Bash","tool_input":{"command":"cd /p/sub && git pull"},"tool_response":{"stdout":"Already up to date.","stderr":"","interrupted":false,"isImage":false}}],
  ["Bash git -C ~/repos commit -m", {"tool_name":"Bash","tool_input":{"command":"git -C /abs/repo commit -m \"feat: x\""},"tool_response":{"stdout":"[main abc] feat: x","stderr":"","interrupted":false,"isImage":false}}],
  ["Bash error_tool false positive (grep output containing 'Error:')", {"tool_name":"Bash","tool_input":{"command":"grep -rn Error: src"},"tool_response":{"stdout":"src/a.ts:1: throw new Error: bad","stderr":"","interrupted":false,"isImage":false}}],
  ["Bash plain string failure (legacy test shape)", {"tool_name":"Bash","tool_input":{"command":"npm test"},"tool_response":"FAIL src/store.test.ts\nError: expected 3 but got 5\nexit code 1"}],
  ["Bash context-mode guidance echo is never an error", {"tool_name":"Bash","tool_input":{"command":"echo \"context-mode: curl blocked, retry fails error\""},"tool_response":"context-mode: curl blocked, retry fails error"}],
  ["Bash export sanitization + npm install", {"tool_name":"Bash","tool_input":{"command":"export API_KEY=sk-123 && npm install"},"tool_response":{"stdout":"","stderr":"","interrupted":false,"isImage":false}}],
  ["TodoWrite", {"tool_name":"TodoWrite","tool_input":{"todos":[{"content":"a","status":"pending"}]},"tool_response":"ok"}],
  ["TaskUpdate", {"tool_name":"TaskUpdate","tool_input":{"taskId":"1","status":"completed"},"tool_response":"ok"}],
  ["EnterPlanMode", {"tool_name":"EnterPlanMode","tool_input":{},"tool_response":"ok"}],
  ["ExitPlanMode approved with plan+allowedPrompts", {"tool_name":"ExitPlanMode","tool_input":{"plan":"Refactor auth","allowedPrompts":[{"prompt":"Edit src/auth.ts"},"run tests"]},"tool_response":"User has approved your plan"}],
  ["ExitPlanMode rejected", {"tool_name":"ExitPlanMode","tool_input":{},"tool_response":"User rejected the plan"}],
  ["Skill", {"tool_name":"Skill","tool_input":{"skill":"caveman"},"tool_response":"ok"}],
  ["Agent async launch (CC object response)", {"tool_name":"Agent","tool_input":{"description":"d","prompt":"find X"},"tool_response":{"isAsync":true,"status":"async_launched","agentId":"abc"}}],
  ["AskUserQuestion", {"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which DB?","options":[]}]},"tool_response":{"questions":[],"answers":{"Which DB?":"SQLite"}}}],
  ["EnterWorktree", {"tool_name":"EnterWorktree","tool_input":{"name":"feat-x"},"tool_response":"ok"}],
  ["Bash with URL + #123 (external_ref)", {"tool_name":"Bash","tool_input":{"command":"gh issue view #123 https://github.com/a/b/issues/9"},"tool_response":{"stdout":"see `https://x.com/y`.","stderr":"","interrupted":false,"isImage":false}}],
  ["Edit failure string with Error + cannot (constraint)", {"tool_name":"Edit","tool_input":{"file_path":"/p/a.ts"},"tool_response":"Error: cannot edit file, permission denied"}],
  ["mcp tool (dead on CC: matcher exact) ", {"tool_name":"mcp__plugin_context-mode_context-mode__ctx_search","tool_input":{"queries":["a"],"token":"s3cr3t"},"tool_response":"result"}],
];

const capTools = [
  ["git -C checkout (TS bug)", bash("git -C /x checkout main")],
  ["git checkout", bash("git checkout main")],
  ["git -C merge", bash("git -C /x merge dev")],
  ["git -C ~ status", bash("git -C ~/repos/myrepo status")],
  ["git --directory= tilde other user", bash("git --directory=~someoneelse/foo status")],
  ["git --directory value", bash("git --directory /abs/x log")],
  ["git -C empty", bash("git -C \"\" status")],
  ["git env prefix and runner", bash("FOO=1 sudo git --no-pager log -n 3")],
  ["git commit -am", bash("git commit -am \"stage+commit\"")],
  ["git commit --message=", bash("git commit --message=longform")],
  ["git commit --message value", bash("git commit --message 'two words'")],
  ["git commit --amend", bash("git commit --amend --no-edit")],
  ["git commit -m empty", bash("git commit -m \"\"")],
  ["git commit escaped quote", bash("git commit -m \"say \\\"hi\\\"\" && git push")],
  ["git push", bash("git push origin main")],
  ["git unknown op falls back to regex", bash("git frob && git status")],
  ["git newline is not a separator", bash("git status\ngit log")],
  ["git /usr/bin/git", bash("/usr/bin/git stash pop")],
  ["git merge needs arg in regex fallback", bash("cd /r && git merge")],
  ["cd double quoted", bash("cd \"a b\" && ls")],
  ["cd single quoted", bash("cd 'a b'")],
  ["cd after nbsp", bash("cd\u00a0/x")],
  ["no cd", bash("ls -la")],
  ["export twice + source activate", bash("export A=1 B=2; export TOKEN=abc\u3000x && source .venv/bin/activate")],
  ["go get", bash("go get example.com/x")],
  ["Read CLAUDE.md CC object response", { tool_name: "Read", tool_input: { file_path: "/p/CLAUDE.md" }, tool_response: { type: "text", file: { filePath: "/p/CLAUDE.md", content: "# rules\n", numLines: 1, startLine: 1, totalLines: 1 } } }],
  ["Read rule file no response", { tool_name: "Read", tool_input: { file_path: "/p/AGENTS.md" } }],
  ["Read .claude dir file", { tool_name: "Read", tool_input: { file_path: "/p/.CLAUDE/settings.json" }, tool_response: "{}" }],
  ["Read memory file", { tool_name: "Read", tool_input: { file_path: "/u/.codex/memories/x.md" }, tool_response: "m" }],
  ["Read copilot instructions", { tool_name: "Read", tool_input: { file_path: "/p/.github/copilot-instructions.MD" }, tool_response: "c" }],
  ["Read numeric path", { tool_name: "Read", tool_input: { file_path: 42 }, tool_response: "x" }],
  ["Edit plan file", { tool_name: "Edit", tool_input: { file_path: "C:\\u\\.claude\\plans\\p.md" }, tool_response: "ok" }],
  ["Write normal file", { tool_name: "Write", tool_input: { file_path: "/p/x.go", content: "<a>&" }, tool_response: { type: "create" } }],
  ["TaskCreate", { tool_name: "TaskCreate", tool_input: { subject: "a <b> & c", n: 1.5, z: null }, tool_response: "ok" }],
  ["ExitPlanMode plan from response", { tool_name: "ExitPlanMode", tool_input: { allowedPrompts: [null, 3, { prompt: null }, { tool: "Bash" }, ["a", null]] }, tool_response: { plan: "计划😀", isAgent: false, note: "User approved" } }],
  ["ExitPlanMode empty plan in input falls to response", { tool_name: "ExitPlanMode", tool_input: { plan: "", allowedPrompts: [] }, tool_response: "{\"plan\":\"\"}" }],
  ["ExitPlanMode declined", { tool_name: "ExitPlanMode", tool_input: { plan: "p" }, tool_response: "User DECLINED" }],
  ["AskUserQuestion multi-select", { tool_name: "AskUserQuestion", tool_input: { questions: [{ question: "Pick?" }] }, tool_response: { answers: { "Pick?": ["a", 1, "b"] } } }],
  ["AskUserQuestion fallback join", { tool_name: "AskUserQuestion", tool_input: { questions: [{ question: "Q1" }] }, tool_response: { answers: { other: "x", empty: "", list: ["y"] } } }],
  ["AskUserQuestion non-JSON", { tool_name: "AskUserQuestion", tool_input: { questions: [{ question: "Q" }] }, tool_response: "not json" }],
  ["AskUserQuestion no questions", { tool_name: "AskUserQuestion", tool_input: {}, tool_response: { answers: { a: "b" } } }],
  ["Agent description only, no response", { tool_name: "Agent", tool_input: { description: "d" } }],
  ["Agent string response", { tool_name: "Agent", tool_input: { prompt: "p" }, tool_response: "done" }],
  ["EnterWorktree unnamed", { tool_name: "EnterWorktree", tool_input: {}, tool_response: "ok" }],
  ["Skill missing", { tool_name: "Skill", tool_input: {}, tool_response: "ok" }],
];

const seedPrompts = [
  "fix the login bug", "why does the build fail?", "don't use redis, use sqlite instead for the cache layer",
  "You are a senior Go engineer. Keep answers short.", "/plan migrate the db layer", "/goal ship the Go port by Friday",
  "goal: port hooks", "TypeError: x is undefined\n    at foo (a.js:1:2)", "fixed: the login bug", "ok that's fine for now",
  "x".repeat(1030),
];
const capPrompts = [
  "  /plan", "/plans x", "\t/plan\targ  ", "/plan " + "a".repeat(130), "/planx",
  "不要用redis，用sqlite做缓存层就好了吧好吗", "不要用redis，用sqlite做缓存层？",
  "Tu es un développeur senior. Réponds en français.", "あなたは経験豊富なエンジニアです。", "你是高级工程师", "ok, you are a cat",
  "please always respond in English", "That's fine for now", "Sen kıdemli bir mühendissin",
  "Objective : finish it", "/goal\nmulti\nline", "GOAL:   ",
  "Traceback (most recent call last):", "✅ done", "RESOLVED: x", "at Object.<anonymous> (/a/b.js:10:5)",
  "a".repeat(59), "a".repeat(60), "1234 5678", "  spaced prompt  ",
];

// ─────────────────────────────────────────────────────────
// Child phases: each prints its result on stdout
// ─────────────────────────────────────────────────────────

async function routePhase(base, i) {
  const { routePreToolUse, initSecurity } = await tsModule("hooks/core/routing.mjs");
  const { formatDecision } = await tsModule("hooks/core/formatters.mjs");
  await initSecurity(join(TS_REPO, "build"));
  const c = preToolUseCases(base)[i];
  const opts = c.subagent ? { mcpToolsAvailable: false } : {};
  const decision = routePreToolUse(c.tool, c.input, c.inProject ? join(base, "proj") : "", "claude-code", c.sessionId, opts);
  const response = formatDecision("claude-code", decision);
  // output is the exact stdout of the TS hook, or null when it writes nothing.
  const output = response == null ? null : JSON.stringify(response) + "\n";
  process.stdout.write(JSON.stringify({ decision: decision ?? null, output }));
}

async function stringsPhase() {
  const rb = await tsModule("hooks/routing-block.mjs");
  const { routePreToolUse, initSecurity } = await tsModule("hooks/core/routing.mjs");
  await initSecurity(join(TS_REPO, "build"));
  const t = (tool) => CMGO_PREFIX + tool;
  const block = (name, includeCommands, toolSearchBootstrap) =>
    ({ name, includeCommands, toolSearchBootstrap, text: rb.createRoutingBlock(t, { includeCommands, toolSearchBootstrap }) });
  // The redirect texts are not exported: take them from routePreToolUse.
  const route = (tool, input) => routePreToolUse(tool, input, "", "claude-code", "strings", {});
  const echoed = (command) => route("Bash", { command }).updatedInput.command.replace(/^echo "/, "").replace(/"$/, "");
  process.stdout.write(JSON.stringify({
    routingBlocks: [
      block("routing_block_claude", true, false), // SessionStart on claude-code (TS)
      block("agent-routing-suffix", false, true), // appended to Agent prompts
      block("routing_block_session", false, false), // cmgo SessionStart
    ],
    texts: {
      "guidance-bash": rb.createBashGuidance(t),
      "guidance-read": rb.createReadGuidance(t),
      "guidance-grep": rb.createGrepGuidance(t),
      "deny-curl-wget": echoed("curl https://x.com"),
      "deny-inline-http": echoed("node -e \"fetch('https://a.b')\""),
      "deny-build-gradlew-example": echoed("./gradlew build"),
      "deny-webfetch-example": route("WebFetch", { url: "https://example.com/a" }).reason,
    },
  }));
}

async function capturePhase() {
  m = await tsModule("hooks/session-extract.bundle.mjs");
  process.env.HOME = "/home/test"; // the Go test passes home "/home/test"
  const toolCases = [...seedTools, ...capTools].map(([name, input]) =>
    ({ name, input, output: post(input.tool_name, input.tool_input, input.tool_response) }));
  // UserPromptSubmit passes the trimmed prompt (userpromptsubmit.mjs:36,75).
  const userCases = [...seedPrompts, ...capPrompts].map((prompt) => ({ prompt, events: m.extractUserEvents(prompt.trim()) }));

  // Invisible characters are written as \u escapes so the file survives copy and paste.
  // Go keeps escapes it compacts, so such a character must not sit in an object
  // tool_response or a task tool_input, where TS saw it raw.
  const visible = (s) => s.replace(/[\u007f-\u00a0\u1680\u2000-\u200f\u2028-\u202f\u205f\u3000\ufeff]/g,
    (c) => "\\u" + c.charCodeAt(0).toString(16).padStart(4, "0"));
  const lines = (xs) => xs.map((x) => "  " + visible(JSON.stringify(x))).join(",\n");
  process.stdout.write(`{"tsCommit": "${TS_COMMIT}",\n"toolCases": [\n${lines(toolCases)}\n],\n"userCases": [\n${lines(userCases)}\n]}\n`);
}

// ─────────────────────────────────────────────────────────

// child runs this script in one phase with only the temp-dir environment.
function child(base, args) {
  const env = {
    PATH: process.env.PATH,
    LANG: "en_US.UTF-8",
    TS_REPO,
    HOME: base,
    TMPDIR: base,
    CONTEXT_MODE_DIR: join(base, "context-mode"),
    CONTEXT_MODE_MCP_SENTINEL_DIR: join(base, "sentinel"),
  };
  const r = spawnSync(process.execPath, [SELF, ...args], {
    cwd: base, env, encoding: "utf8", stdio: ["ignore", "pipe", "inherit"], maxBuffer: 64 << 20,
  });
  if (r.status !== 0) throw new Error(`${args.join(" ")} failed (status ${r.status})`);
  return r.stdout;
}

async function main() {
  const phase = process.argv.find((a) => a.startsWith("--phase="))?.slice(8);
  if (phase === "route") return routePhase(process.argv[3], Number(process.argv[4]));
  if (phase === "strings") return stringsPhase();
  if (phase === "capture") return capturePhase();

  const head = execFileSync("git", ["-C", TS_REPO, "rev-parse", "HEAD"], { encoding: "utf8" }).trim();
  if (!head.startsWith(TS_COMMIT)) throw new Error(`TS_REPO is at ${head}, want ${TS_COMMIT}`);
  mkdirSync(OUT, { recursive: true });
  const tmp = mkdtempSync(join(tmpdir(), "cmgo-hooks-golden-"));
  try {
    const base = realpathSync(tmp); // TS sees one spelling of the path, replaced by <BASE> below
    mkdirSync(join(base, "sentinel"));
    // A live MCP server: this process outlives every child that probes it.
    writeFileSync(join(base, "sentinel", `context-mode-mcp-ready-${process.pid}`), String(process.pid));
    mkdirSync(join(base, "proj", ".claude"), { recursive: true });
    writeFileSync(join(base, "proj", ".claude", "settings.json"), JSON.stringify(PROJECT_RULES));
    writeFileSync(join(base, "small.txt"), "x".repeat(50000));
    writeFileSync(join(base, "big.txt"), "x".repeat(50001));

    const cases = preToolUseCases(base).map((c, i) => ({
      i, tool: c.tool, input: c.input, projectDir: c.inProject ? "<proj>" : "", sessionId: c.sessionId, subagent: c.subagent,
      ...JSON.parse(child(base, ["--phase=route", base, String(i)])),
    }));
    const strings = JSON.parse(child(base, ["--phase=strings"]));
    const capture = child(base, ["--phase=capture"]);

    const fix = (s) => s.split(base).join("<BASE>").split(TS_PREFIX).join(CMGO_PREFIX);
    const write = (name, text) => writeFileSync(join(OUT, name), fix(text));
    write("hooks-pretooluse.json", JSON.stringify({ tsCommit: TS_COMMIT, cases }, null, 2) + "\n");
    write("hooks-strings.json", JSON.stringify({ tsCommit: TS_COMMIT, ...strings }, null, 2) + "\n");
    write("hooks-capture.json", capture);
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
  console.log(`hooks golden files written to ${OUT}`);
}

main().catch((e) => { console.error(e); process.exit(1); });
```

The known-differences list. Tasks Task 23 and Task 31 hold the same rows in their Go tables:

`testdata/golden/HOOKS-KNOWN-DIFFS.md`:
```markdown
# Known differences: cmgo hooks vs TS golden output

The `hooks-*.json` files in this directory are TS output
(`tools/golden-gen/gen-hooks.mjs`, TS commit `a5e6dd31`), with every
`mcp__plugin_context-mode_context-mode__` replaced by `mcp__cmgo__`
(spec §1.3). Spec §6.2 allows only the differences listed here. The Go tests
hold the same rows (`knownDiffs` in `internal/routing/golden_test.go`,
`fixed` and `dropped` in `internal/capture/golden_test.go`). They fail if a
listed difference goes away, and they fail if any unlisted case differs.

Case ID forms: `pretooluse:<i>` (the `i` field of a case in
`hooks-pretooluse.json`); `capture:<name>` (the `name` field of a tool case
in `hooks-capture.json`). `hooks-strings.json` has no differences.

## Cases

| Case ID | File | Reason |
|---|---|---|
| `pretooluse:25` | `hooks-pretooluse.json` | spec §2.3: `ctx_execute` is not a PreToolUse matcher, so cmgo writes nothing (TS pins `cwd`) |
| `pretooluse:26` | `hooks-pretooluse.json` | spec §2.3: `ctx_execute` is not a PreToolUse matcher; `cmgo serve` checks deny rules itself (TS denies `sudo ls` here) |
| `pretooluse:28` | `hooks-pretooluse.json` | spec §2.3: `ctx_execute_file` is not a PreToolUse matcher; `cmgo serve` checks deny rules itself (TS denies the `.env` path here) |
| `pretooluse:29` | `hooks-pretooluse.json` | spec §2.3: `ctx_batch_execute` is not a PreToolUse matcher; `cmgo serve` checks deny rules itself (TS denies `sudo id` here) |
| `pretooluse:30` | `hooks-pretooluse.json` | spec §2.3: `ctx_batch_execute` is not a PreToolUse matcher, so cmgo writes nothing (TS pins `cwd`) |
| `pretooluse:31` | `hooks-pretooluse.json` | spec §2.3: external-MCP guidance dropped; the TS `mcp__` matcher never fires in Claude Code (R1 P11) |
| `capture:git -C checkout (TS bug)` | `hooks-capture.json` | spec §3.4: the parsed git subcommand is matched, so cmgo records `cwd` and `git branch` (TS records neither) |
| `capture:Read CLAUDE.md CC object response` | `hooks-capture.json` | spec §3.4: `rule_content` stores `tool_response.file.content` (TS stores the stringified response object) |

## Dropped event types (applied to every capture case)

spec §3.4: cmgo does not port these extractors, so the capture test removes
these types from every TS output before comparing: `error_tool` (moved to
PostToolUseFailure), `constraint_discovered`, `bash_outcome`,
`file_read_metadata`, `agent_finding`, `external_ref`, `mcp`,
`mcp_tool_call`, `webfetch_metadata`, `worktree_exit`, `agent_usage`,
`error_resolved`, `retry_detected`, and the UserPromptSubmit `data` event.
No TS output in `hooks-capture.json` contains `webfetch_metadata`,
`worktree_exit`, `agent_usage`, `error_resolved` or `retry_detected`, so
for those five the list is not exercised.
```

- [ ] **Step 4: Run test to verify it passes**

Run: the Step 1 block from the repo root.
Expected: PASS. The generator exits 0 both times, and the two runs give the same md5s. The gate is that equality, not the values. The author's run printed:
```
77f03a2a54fb68097c8d1bfce8bff2df  testdata/golden/hooks-capture.json
0e3c7a7e692aad0c9394747322d601f7  testdata/golden/hooks-pretooluse.json
5cb64c788cac0f80f9017f51b7b98829  testdata/golden/hooks-strings.json
deterministic: OK
```
The files have 113, 601 and 32 lines. `git -C "$TS_REPO" status --short` shows no new changes.

- [ ] **Step 5: Commit**

```bash
git add tools/golden-gen/gen-hooks.mjs testdata/golden/HOOKS-KNOWN-DIFFS.md testdata/golden/hooks-pretooluse.json testdata/golden/hooks-capture.json testdata/golden/hooks-strings.json
git commit -m "test(golden): hooks golden generator and TS hook golden files"
```

---

### Task 2: JS-compatible JSON strings and UTF-16 lengths

**Files:**
- Create: `internal/hookio/jsstr.go`
- Test: `internal/hookio/jsstr_test.go`

**Interfaces:**
- Consumes: none (stdlib only)
- Produces: `func Marshal(v any) ([]byte, error)`; `func QuoteString(s string) json.RawMessage`; `func JSLen(s string) int`; `func JSPrefix(s string, n int) string`; `func RuneLen(s string) int`; unexported `unescapeJS(b []byte) []byte`, `units(r rune) int`; test constants `ls`, `ps`, `repl` (U+2028, U+2029, U+FFFD as UTF-8 bytes)

- [ ] **Step 1: Write the failing test**

`internal/hookio/jsstr_test.go`:
```go
package hookio

import "testing"

// U+2028, U+2029 and U+FFFD as UTF-8 bytes, spelled out so no tool rewrites them.
const (
	ls   = "\xe2\x80\xa8"
	ps   = "\xe2\x80\xa9"
	repl = "\xef\xbf\xbd"
)

// Expected values come from node: JSON.stringify(<input>).
func TestQuoteStringMatchesJSONStringify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"<a>&b", `"<a>&b"`},
		{ls + ps, `"` + ls + ps + `"`},
		{"\x00\x01\x1f\x7f", `"\` + `u0000\` + `u0001\` + `u001f` + "\x7f\""},
		{"\b\f\n\r\t\"\\", `"\b\f\n\r\t\"\\"`},
		{"é😀", `"é😀"`},
		{"a\xff", `"a` + repl + `"`},      // node decodes the stray byte to U+FFFD
		{`\` + `u2028`, `"\\` + `u2028"`}, // literal backslash-u text stays escaped
		{`\` + ls, `"\\` + ls + `"`},      // backslash then a real U+2028
		{`\` + `ufffd`, `"\\` + `ufffd"`},
		{"", `""`},
	}
	for _, c := range cases {
		if got := string(QuoteString(c.in)); got != c.want {
			t.Errorf("QuoteString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMarshal(t *testing.T) {
	v := struct {
		B string  `json:"b"`
		A []any   `json:"a"`
		N float64 `json:"n"`
		F float64 `json:"f"`
	}{"<x>", []any{1, ls}, 1e21, 0.1}
	got, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"b":"<x>","a":[1,"` + ls + `"],"n":1e+21,"f":0.1}`
	if string(got) != want {
		t.Errorf("Marshal = %q, want %q", got, want)
	}
	if _, err := Marshal(func() {}); err == nil {
		t.Error("Marshal(func) succeeded, want error")
	}
}

func TestJSLenPrefixRuneLen(t *testing.T) {
	if n := JSLen("a😀é"); n != 4 {
		t.Errorf("JSLen = %d, want 4", n)
	}
	if n := RuneLen("a😀é"); n != 3 {
		t.Errorf("RuneLen = %d, want 3", n)
	}
	prefixes := []struct {
		s    string
		n    int
		want string
	}{
		{"a😀é", 0, ""},
		{"a😀é", -1, ""},
		{"a😀é", 1, "a"},
		{"a😀é", 2, "a"}, // never splits the surrogate pair
		{"a😀é", 3, "a😀"},
		{"a😀é", 4, "a😀é"},
		{"a😀é", 99, "a😀é"},
	}
	for _, c := range prefixes {
		if got := JSPrefix(c.s, c.n); got != c.want {
			t.Errorf("JSPrefix(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hookio/ -run 'TestQuoteStringMatchesJSONStringify|TestMarshal|TestJSLenPrefixRuneLen' -v`
Expected: FAIL with "internal/hookio/jsstr_test.go:27:20: undefined: QuoteString"

- [ ] **Step 3: Write minimal implementation**

`internal/hookio/jsstr.go`:
```go
// Package hookio holds the stdin, JSON and string helpers every cmgo hook
// shares. Output strings match JS JSON.stringify so hook output is byte-equal
// to the TS hooks.
package hookio

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"
)

// Marshal returns compact JSON with no trailing newline. Strings are escaped
// as JSON.stringify does: no HTML escaping, and U+2028, U+2029 and U+FFFD
// stay raw (Go's encoder always escapes the first two and writes invalid
// UTF-8 as an escaped U+FFFD).
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return unescapeJS(bytes.TrimSuffix(buf.Bytes(), []byte("\n"))), nil
}

// QuoteString returns s as a JSON string literal, equal to JSON.stringify(s).
func QuoteString(s string) json.RawMessage {
	b, _ := Marshal(s) // a string always encodes
	return b
}

// rawEscapes maps the escape bodies (after the backslash) that JS writes raw.
var rawEscapes = map[string]string{
	"u2028": "\xe2\x80\xa8",
	"u2029": "\xe2\x80\xa9",
	"ufffd": "\xef\xbf\xbd",
}

// unescapeJS turns the escapes in rawEscapes back into raw characters. It
// walks escape pairs, so an escaped backslash followed by "u2028" text is
// left alone. Valid JSON has backslashes only inside strings, so this never
// touches structure.
func unescapeJS(b []byte) []byte {
	if !bytes.Contains(b, []byte{'\\'}) {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' || i+1 >= len(b) {
			out = append(out, b[i])
			continue
		}
		if i+6 <= len(b) {
			if raw, ok := rawEscapes[string(b[i+1:i+6])]; ok {
				out = append(out, raw...)
				i += 5
				continue
			}
		}
		out = append(out, b[i], b[i+1])
		i++
	}
	return out
}

// JSLen returns the length of s in UTF-16 code units, like JS s.length.
func JSLen(s string) int {
	n := 0
	for _, r := range s {
		n += units(r)
	}
	return n
}

// JSPrefix returns the longest prefix of s that is at most n UTF-16 code
// units, like JS s.slice(0, n), except that it stops before a surrogate pair
// instead of splitting it.
func JSPrefix(s string, n int) string {
	used := 0
	for i, r := range s {
		if used+units(r) > n {
			return s[:i]
		}
		used += units(r)
	}
	return s
}

// RuneLen returns the number of code points in s, like JS [...s].length.
func RuneLen(s string) int {
	return utf8.RuneCountInString(s)
}

// units is the UTF-16 length of r. Invalid bytes decode to U+FFFD, one unit,
// as they would after JS decoded them.
func units(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/hookio/ -run 'TestQuoteStringMatchesJSONStringify|TestMarshal|TestJSLenPrefixRuneLen' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/hookio/jsstr.go internal/hookio/jsstr_test.go
git commit -m "feat(hookio): JSON.stringify-compatible Marshal and UTF-16 length helpers"
```

### Task 3: Order-preserving JSON Object

**Files:**
- Create: `internal/hookio/object.go`
- Test: `internal/hookio/object_test.go`

**Interfaces:**
- Consumes: `QuoteString(s string) json.RawMessage`, `Marshal(v any) ([]byte, error)`, unexported `unescapeJS(b []byte) []byte` and test constant `ls` from Task 2
- Produces: `type Object struct{ keys []string; vals map[string]json.RawMessage }`; `func ParseObject(b []byte) (*Object, error)`; `func (o *Object) Keys() []string`; `func (o *Object) Get(key string) (json.RawMessage, bool)`; `func (o *Object) Has(key string) bool`; `func (o *Object) String(key string) (string, bool)`; `func (o *Object) Bool(key string) (v bool, ok bool)`; `func (o *Object) Object(key string) (*Object, bool)`; `func (o *Object) Set(key string, v json.RawMessage)`; `func (o *Object) SetString(key, v string)`; `func (o *Object) MarshalJSON() ([]byte, error)`; `func (o *Object) Clone() *Object`; test helper `mustParse(t *testing.T, s string) *Object`

- [ ] **Step 1: Write the failing test**

`internal/hookio/object_test.go`:
```go
package hookio

import (
	"encoding/json"
	"reflect"
	"testing"
)

func mustParse(t *testing.T, s string) *Object {
	t.Helper()
	o, err := ParseObject([]byte(s))
	if err != nil {
		t.Fatalf("ParseObject(%q): %v", s, err)
	}
	return o
}

func TestParseObjectKeepsOrderAndRoundTrips(t *testing.T) {
	o := mustParse(t, "\xef\xbb\xbf \n{ \"z\": 1, \"a\" : { \"y\": [1, 2], \"b\": \"<&>\" }, \"m\": null }\n ")
	if got, want := o.Keys(), []string{"z", "a", "m"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Keys = %v, want %v", got, want)
	}
	out, err := o.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"z":1,"a":{"y":[1,2],"b":"<&>"},"m":null}`; string(out) != want {
		t.Errorf("MarshalJSON = %s, want %s", out, want)
	}
	// Marshal of a struct holding the object gives the same bytes.
	wrapped, err := Marshal(struct {
		O *Object `json:"o"`
		N *Object `json:"n"`
	}{O: o})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"o":` + string(out) + `,"n":null}`; string(wrapped) != want {
		t.Errorf("Marshal(wrapped) = %s, want %s", wrapped, want)
	}
}

func TestParseObjectEmptyAndErrors(t *testing.T) {
	for _, in := range []string{"", "  \n", "\xef\xbb\xbf", "\xef\xbb\xbf  "} {
		o := mustParse(t, in)
		if len(o.Keys()) != 0 {
			t.Errorf("ParseObject(%q) keys = %v, want none", in, o.Keys())
		}
		if out, _ := o.MarshalJSON(); string(out) != "{}" {
			t.Errorf("empty MarshalJSON = %s", out)
		}
	}
	for _, in := range []string{"[]", "null", `"x"`, "1", "{", `{"a":}`, `{"a":1}x`, `{"a":1}{}`, `{1:2}`} {
		if _, err := ParseObject([]byte(in)); err == nil {
			t.Errorf("ParseObject(%q) succeeded, want error", in)
		}
	}
}

func TestDuplicateKeyKeepsFirstPositionLastValue(t *testing.T) {
	o := mustParse(t, `{"a":1,"b":2,"a":3}`)
	out, _ := o.MarshalJSON()
	if want := `{"a":3,"b":2}`; string(out) != want {
		t.Errorf("got %s, want %s", out, want)
	}
}

func TestTypedGetters(t *testing.T) {
	o := mustParse(t, `{"s":"h\`+`u00e9","n":1,"t":true,"f":false,"o":{"k":"v"},"arr":[],"nul":null}`)
	if v, ok := o.String("s"); !ok || v != "hé" {
		t.Errorf(`String("s") = %q, %v`, v, ok)
	}
	for _, k := range []string{"n", "t", "o", "nul", "missing"} {
		if _, ok := o.String(k); ok {
			t.Errorf("String(%q) ok, want not ok", k)
		}
	}
	if v, ok := o.Bool("t"); !ok || !v {
		t.Errorf(`Bool("t") = %v, %v`, v, ok)
	}
	if v, ok := o.Bool("f"); !ok || v {
		t.Errorf(`Bool("f") = %v, %v`, v, ok)
	}
	for _, k := range []string{"s", "n", "nul", "missing"} {
		if _, ok := o.Bool(k); ok {
			t.Errorf("Bool(%q) ok, want not ok", k)
		}
	}
	if n, ok := o.Object("o"); !ok {
		t.Error(`Object("o") not ok`)
	} else if v, _ := n.String("k"); v != "v" {
		t.Errorf(`nested k = %q`, v)
	}
	for _, k := range []string{"arr", "nul", "s", "missing"} {
		if _, ok := o.Object(k); ok {
			t.Errorf("Object(%q) ok, want not ok", k)
		}
	}
	if raw, ok := o.Get("n"); !ok || string(raw) != "1" {
		t.Errorf(`Get("n") = %s, %v`, raw, ok)
	}
	if !o.Has("nul") || o.Has("missing") {
		t.Error("Has wrong")
	}
}

func TestSetAndClone(t *testing.T) {
	o := mustParse(t, `{"a":1,"b":2}`)
	c := o.Clone()
	o.Set("a", json.RawMessage(`[true]`))
	o.SetString("c", "<x>")
	out, _ := o.MarshalJSON()
	if want := `{"a":[true],"b":2,"c":"<x>"}`; string(out) != want {
		t.Errorf("after Set = %s, want %s", out, want)
	}
	cout, _ := c.MarshalJSON()
	if want := `{"a":1,"b":2}`; string(cout) != want {
		t.Errorf("clone changed: %s", cout)
	}
	var z Object // the zero value is usable
	z.SetString("k", "v")
	if zout, _ := z.MarshalJSON(); string(zout) != `{"k":"v"}` {
		t.Errorf("zero Object = %s", zout)
	}
}

// Raw values written by JS output U+2028 and stray-byte U+FFFD raw, so a
// re-emitted object does too; other escapes stay as received.
func TestMarshalJSONUnescapesLikeJS(t *testing.T) {
	in := `{"a":"x\` + `u2028y","b":"\` + `u0041\\` + `u2028"}`
	o := mustParse(t, in)
	out, _ := o.MarshalJSON()
	want := `{"a":"x` + ls + `y","b":"\` + `u0041\\` + `u2028"}`
	if string(out) != want {
		t.Errorf("MarshalJSON = %q, want %q", out, want)
	}
	if v, _ := o.String("a"); v != "x"+ls+"y" {
		t.Errorf("String(a) = %q", v)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hookio/ -run 'TestParseObject|TestDuplicateKeyKeepsFirstPositionLastValue|TestTypedGetters|TestSetAndClone|TestMarshalJSONUnescapesLikeJS' -v`
Expected: FAIL with "internal/hookio/object_test.go:9:41: undefined: Object"

- [ ] **Step 3: Write minimal implementation**

`internal/hookio/object.go`:
```go
package hookio

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode"
)

// Object is a JSON object that keeps key order, as a JS object does, so a
// hook can echo tool_input back with its keys where the host put them.
// Values stay raw JSON. The zero value is an empty object.
type Object struct {
	keys []string
	vals map[string]json.RawMessage
}

// ParseObject parses b as one JSON object. Like TS parseStdin it first trims
// a BOM and surrounding space, and empty input gives an empty object.
// Anything else that is not exactly one JSON object is an error.
func ParseObject(b []byte) (*Object, error) {
	// JS trim() also strips U+FEFF, which unicode.IsSpace does not.
	b = bytes.TrimFunc(b, func(r rune) bool { return unicode.IsSpace(r) || r == 0xFEFF })
	o := &Object{}
	if len(b) == 0 {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if tok, err := dec.Token(); err != nil {
		return nil, err
	} else if tok != json.Delim('{') {
		return nil, errors.New("hookio: input is not a JSON object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string) // the decoder only yields strings in key position
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		o.Set(key, raw) // a repeated key keeps its first position, last value, as JSON.parse does
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("hookio: data after the JSON object")
	}
	return o, nil
}

// Keys returns the keys in order.
func (o *Object) Keys() []string {
	return append([]string(nil), o.keys...)
}

// Get returns the raw value for key.
func (o *Object) Get(key string) (json.RawMessage, bool) {
	v, ok := o.vals[key]
	return v, ok
}

// Has reports whether key is present, whatever its value.
func (o *Object) Has(key string) bool {
	_, ok := o.vals[key]
	return ok
}

// String returns the value for key when it is a JSON string.
func (o *Object) String(key string) (string, bool) {
	raw, ok := o.vals[key]
	if !ok || len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// Bool returns the value for key when it is JSON true or false.
func (o *Object) Bool(key string) (v bool, ok bool) {
	switch string(o.vals[key]) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// Object returns the value for key when it is a JSON object.
func (o *Object) Object(key string) (*Object, bool) {
	raw, ok := o.vals[key]
	if !ok || len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	n, err := ParseObject(raw)
	return n, err == nil
}

// Set replaces the value for key in place, or appends key at the end.
func (o *Object) Set(key string, v json.RawMessage) {
	if o.vals == nil {
		o.vals = map[string]json.RawMessage{}
	}
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

// SetString sets key to the JSON string v.
func (o *Object) SetString(key, v string) {
	o.Set(key, QuoteString(v))
}

// MarshalJSON writes the object compact with keys in order. Values keep the
// bytes they arrived with, compacted (json.Compact never adds escapes), except
// that the escapes JSON.stringify writes raw are unescaped, so the output is
// what a JS JSON.parse + JSON.stringify round trip gives for those characters.
func (o *Object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(QuoteString(k))
		buf.WriteByte(':')
		if err := json.Compact(&buf, o.vals[k]); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return unescapeJS(buf.Bytes()), nil
}

// Clone returns a copy whose keys and values can be set without touching o.
func (o *Object) Clone() *Object {
	c := &Object{keys: o.Keys(), vals: make(map[string]json.RawMessage, len(o.vals))}
	for k, v := range o.vals {
		c.vals[k] = v
	}
	return c
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/hookio/ -run 'TestParseObject|TestDuplicateKeyKeepsFirstPositionLastValue|TestTypedGetters|TestSetAndClone|TestMarshalJSONUnescapesLikeJS' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/hookio/object.go internal/hookio/object_test.go
git commit -m "feat(hookio): order-preserving JSON Object with BOM-tolerant parse"
```

### Task 4: ReadInput, SessionID and FileID

**Files:**
- Create: `internal/hookio/input.go`
- Test: `internal/hookio/input_test.go`

**Interfaces:**
- Consumes: `ParseObject`, `(*Object).String` from Task 3; test helper `mustParse` from Task 3
- Produces: `func ReadInput(r io.Reader) ([]byte, error)`; `func SessionID(in *Object, getenv func(string) string, ppid int) string`; `func FileID(sid string) string`; unexported `const maxInput = 16 << 20`

- [ ] **Step 1: Write the failing test**

`internal/hookio/input_test.go`:
```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hookio/ -run 'TestReadInput|TestSessionID|TestFileID' -v`
Expected: FAIL with "internal/hookio/input_test.go:10:12: undefined: ReadInput"

- [ ] **Step 3: Write minimal implementation**

`internal/hookio/input.go`:
```go
package hookio

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strconv"
)

// maxInput caps one hook payload (spec §1.1). A larger tool_response is not
// worth capturing, and the cap bounds memory.
const maxInput = 16 << 20

// ReadInput reads all of r. More than 16 MiB is an error rather than a
// silent truncation, which would hand the parser a broken object.
func ReadInput(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxInput+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxInput {
		return nil, fmt.Errorf("hookio: input exceeds %d bytes", maxInput)
	}
	return b, nil
}

var transcriptUUID = regexp.MustCompile(`([a-f0-9-]{36})\.jsonl$`)

// SessionID resolves the session id as TS getSessionId does for Claude Code
// (spec §1.4). Empty strings fall through, as falsy values do in JS.
func SessionID(in *Object, getenv func(string) string, ppid int) string {
	if p, ok := in.String("transcript_path"); ok {
		if m := transcriptUUID.FindStringSubmatch(p); m != nil {
			return m[1]
		}
	}
	if s, ok := in.String("session_id"); ok && s != "" {
		return s
	}
	if s := getenv("CLAUDE_SESSION_ID"); s != "" {
		return s
	}
	return "pid-" + strconv.Itoa(ppid)
}

var safeFileID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// FileID makes sid safe to use in a file name: sid itself when it is short
// and plain, else the first 16 hex chars of its SHA-256. "." and ".." match
// the pattern but would name a directory, so they are hashed too.
func FileID(sid string) string {
	if safeFileID.MatchString(sid) && sid != "." && sid != ".." {
		return sid
	}
	sum := sha256.Sum256([]byte(sid))
	return hex.EncodeToString(sum[:])[:16]
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/hookio/ -run 'TestReadInput|TestSessionID|TestFileID' -v`
Expected: PASS

Then run the package gates: `go vet ./... && go test ./internal/hookio/ -count=1 -race`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/hookio/input.go internal/hookio/input_test.go
git commit -m "feat(hookio): capped stdin read, session id resolution and safe file ids"
```

---

### Task 5: Migration 2 adds the session tables

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: core `internal/store` (`Open`, `migrations`, `migrate`)
- Produces: migration 2 in `var migrations []string` (tables `session_meta`, `session_events`; indexes `session_events_session`, `session_events_project`, `session_events_evict`); `PRAGMA user_version` = 2

- [ ] **Step 1: Write the failing test**

Change `TestOpenSchemaAndPragmas` to expect user_version 2 and add `TestMigrateFromV1`, which builds a v1 DB with the core code path (only `migrations[0]`) and reopens it.

`internal/store/store_test.go`:

```go
package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "sub", "cmgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenSchemaAndPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "cmgo.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	var jm string
	var busy, fk, syncMode int
	s.db.QueryRow("PRAGMA user_version").Scan(&v)
	s.db.QueryRow("PRAGMA journal_mode").Scan(&jm)
	s.db.QueryRow("PRAGMA busy_timeout").Scan(&busy)
	s.db.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	s.db.QueryRow("PRAGMA synchronous").Scan(&syncMode)
	if v != 2 || jm != "wal" || busy != 30000 || fk != 1 || syncMode != 1 {
		t.Fatalf("user_version=%d journal=%s busy=%d fk=%d sync=%d", v, jm, busy, fk, syncMode)
	}
	for _, tbl := range []string{"meta", "sources", "chunks", "chunks_trigram", "vocabulary"} {
		var n int
		if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = ?", tbl).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s: n=%d err=%v", tbl, n, err)
		}
	}
	if _, err := s.db.Exec("INSERT INTO sources(project, label, chunk_count, code_chunk_count) VALUES('p','x',0,0)"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Reopen: migrations are not re-run and data survives.
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	s.db.QueryRow("SELECT count(*) FROM sources").Scan(&n)
	if n != 1 {
		t.Fatalf("sources after reopen = %d", n)
	}
}

// A DB written by the core (user_version 1) gains the session tables on Open
// and keeps its sources.
func TestMigrateFromV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	all := migrations
	migrations = all[:1] // the core code path: only migration 1 exists
	old, err := Open(path)
	migrations = all
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec("INSERT INTO sources(project, label, chunk_count, code_chunk_count) VALUES('p','x',0,0)"); err != nil {
		t.Fatal(err)
	}
	old.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v, n int
	s.db.QueryRow("PRAGMA user_version").Scan(&v)
	s.db.QueryRow("SELECT count(*) FROM sources").Scan(&n)
	if v != 2 || n != 1 {
		t.Fatalf("user_version=%d sources=%d, want 2 and 1", v, n)
	}
	for _, name := range []string{"session_meta", "session_events", "session_events_session", "session_events_project", "session_events_evict"} {
		if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = ?", name).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s: n=%d err=%v", name, n, err)
		}
	}
}

func TestCleanupIfDue(t *testing.T) {
	s := openTest(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO sources(id, project, label, chunk_count, code_chunk_count, indexed_at) VALUES(1,'p','old',1,0,'2020-01-01 00:00:00')")
	exec("INSERT INTO sources(id, project, label, chunk_count, code_chunk_count) VALUES(2,'q','fresh',1,0)")
	for _, tbl := range []string{"chunks", "chunks_trigram"} {
		exec("INSERT INTO " + tbl + "(title, content, source_id, content_type) VALUES('t','old body',1,'prose')")
		exec("INSERT INTO " + tbl + "(title, content, source_id, content_type) VALUES('t','fresh body',2,'prose')")
	}

	now := time.Now()
	const day = 24 * time.Hour
	n, err := s.CleanupIfDue(now, 14*day, day)
	if err != nil || n != 1 {
		t.Fatalf("first cleanup n=%d err=%v", n, err)
	}
	var left, chunks, tri int
	s.db.QueryRow("SELECT count(*) FROM sources").Scan(&left)
	s.db.QueryRow("SELECT count(*) FROM chunks").Scan(&chunks)
	s.db.QueryRow("SELECT count(*) FROM chunks_trigram").Scan(&tri)
	if left != 1 || chunks != 1 || tri != 1 {
		t.Fatalf("after cleanup sources=%d chunks=%d trigram=%d", left, chunks, tri)
	}

	// Not due again within 24h, even if an old source appears.
	exec("UPDATE sources SET indexed_at = '2020-01-01 00:00:00' WHERE id = 2")
	if n, err := s.CleanupIfDue(now.Add(23*time.Hour), 14*day, day); err != nil || n != 0 {
		t.Fatalf("second cleanup n=%d err=%v", n, err)
	}
	// Due after 24h.
	if n, err := s.CleanupIfDue(now.Add(25*time.Hour), 14*day, day); err != nil || n != 1 {
		t.Fatalf("third cleanup n=%d err=%v", n, err)
	}
	var last string
	s.db.QueryRow("SELECT value FROM meta WHERE key='last_cleanup'").Scan(&last)
	if last != now.Add(25*time.Hour).UTC().Format(time.RFC3339) {
		t.Fatalf("last_cleanup = %q", last)
	}
}

// A not-due CleanupIfDue must only read. Another Store holds the write lock;
// the not-due call must return at once, while a due call (control) waits.
func TestCleanupNotDueTakesNoWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	now := time.Now()
	const day = 24 * time.Hour
	if _, err := s.CleanupIfDue(now, 14*day, day); err != nil {
		t.Fatal(err)
	}
	lock, err := other.db.Begin() // BEGIN IMMEDIATE: holds the write lock
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { _, err := s.CleanupIfDue(now.Add(time.Hour), 14*day, day); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		lock.Rollback()
		t.Fatal("not-due CleanupIfDue waited for the write lock")
	}

	// Control: a due call needs the lock, so it waits until the lock is released.
	go func() { _, err := s.CleanupIfDue(now.Add(25*time.Hour), 14*day, day); done <- err }()
	select {
	case <-done:
		lock.Rollback()
		t.Fatal("due CleanupIfDue did not wait for the write lock")
	case <-time.After(300 * time.Millisecond):
	}
	lock.Rollback()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestOpenSchemaAndPragmas|TestMigrateFromV1' -v`
Expected: FAIL with "store_test.go:34: user_version=1 journal=wal busy=30000 fk=1 sync=1"

- [ ] **Step 3: Write minimal implementation**

Append the spec §3.1 DDL as `migrations[1]`.

`internal/store/store.go`:

```go
// Package store is the SQLite FTS5 knowledge base: schema, chunkers, search
// and snippets. Port of context-mode src/store.ts (ST) with the differences
// listed in the cmgo core spec §2.2 and §3.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

// ContentType is the chunk kind stored in the content_type column.
type ContentType string

const (
	Code  ContentType = "code"
	Prose ContentType = "prose"
)

// sqliteTime is the layout of datetime('now') values in sources.indexed_at.
const sqliteTime = "2006-01-02 15:04:05"

// Store owns one *sql.DB with a single connection kept open for the life of
// the process.
type Store struct {
	db *sql.DB

	mu         sync.Mutex
	indexCalls int               // Index calls in this process; FTS optimize every 50 (ST:1132)
	fuzzyKeys  []string          // LRU order of fuzzyVals, oldest first (ST:470)
	fuzzyVals  map[string]string // word -> correction; "" means no correction
}

// migrations[i] moves the schema from user_version i to i+1.
var migrations = []string{
	`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE sources (
  id INTEGER PRIMARY KEY,
  project TEXT NOT NULL,
  label TEXT NOT NULL,
  chunk_count INTEGER NOT NULL,
  code_chunk_count INTEGER NOT NULL,
  indexed_at TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE (project, label)
);
CREATE VIRTUAL TABLE chunks USING fts5(
  title, content, source_id UNINDEXED, content_type UNINDEXED,
  tokenize = 'porter unicode61');
CREATE VIRTUAL TABLE chunks_trigram USING fts5(
  title, content, source_id UNINDEXED, content_type UNINDEXED,
  tokenize = 'trigram');
CREATE TABLE vocabulary (word TEXT PRIMARY KEY);`,
	// Session event capture (hooks spec §3.1).
	`CREATE TABLE session_meta (
  session_id TEXT PRIMARY KEY,
  project TEXT NOT NULL,
  started_at TEXT NOT NULL,         -- UTC 'YYYY-MM-DD HH:MM:SS', like sources.indexed_at
  last_event_at TEXT NOT NULL,
  event_count INTEGER NOT NULL DEFAULT 0,
  compact_count INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE session_events (
  id INTEGER PRIMARY KEY,
  session_id TEXT NOT NULL,
  project TEXT NOT NULL,
  type TEXT NOT NULL,
  category TEXT NOT NULL,
  priority INTEGER NOT NULL,        -- 1 = most important ... 4 = least
  data TEXT NOT NULL,
  data_hash TEXT NOT NULL,          -- sha256(data) hex, first 16, upper case (DB:1145)
  source_hook TEXT NOT NULL,
  bytes_avoided INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL          -- UTC 'YYYY-MM-DD HH:MM:SS'
);
CREATE INDEX session_events_session ON session_events(session_id, id);
CREATE INDEX session_events_project ON session_events(project, id);
CREATE INDEX session_events_evict ON session_events(session_id, priority, id);`,
}

// Open creates the parent directory, opens the DB with WAL pragmas and
// applies pending migrations.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// journal_mode is not in the DSN: WAL persists in the file, and switching
	// to it needs an exclusive lock that SQLite refuses with SQLITE_BUSY
	// without consulting busy_timeout. Re-applying it on every connection made
	// a session fail to start while another session was writing.
	dsn := (&url.URL{Scheme: "file", Path: abs}).String() +
		"?_txlock=immediate" +
		"&_pragma=busy_timeout(30000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)"
	db, err := driver.Open(dsn, fts5.Register)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	s := &Store{db: db, fuzzyVals: map[string]string{}}
	if err := s.ensureWAL(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// walSwitchTimeout bounds retries of the WAL switch; it matches busy_timeout.
const walSwitchTimeout = 30 * time.Second

// ensureWAL switches the file to WAL once. Reading the mode takes no lock;
// the switch is retried with backoff while another connection holds a lock.
func (s *Store) ensureWAL() error {
	deadline := time.Now().Add(walSwitchTimeout)
	wait := 10 * time.Millisecond
	for {
		var mode string
		err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
		if err == nil && mode == "wal" {
			return nil
		}
		if err == nil {
			err = s.db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode)
			if err == nil && mode == "wal" {
				return nil
			}
			if err == nil {
				err = fmt.Errorf("journal_mode is %q, want wal", mode)
			}
		}
		if !isBusy(err) || time.Now().After(deadline) {
			return fmt.Errorf("enable WAL: %w", err)
		}
		time.Sleep(wait)
		if wait < 250*time.Millisecond {
			wait *= 2
		}
	}
}

// isBusy reports SQLITE_BUSY / SQLITE_LOCKED, which the driver may wrap.
func isBusy(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sqlite3.BUSY) || errors.Is(err, sqlite3.LOCKED) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "database table is locked")
}

// migrate re-reads user_version inside an IMMEDIATE transaction so two
// processes opening a fresh file at once do not both run step 1.
func (s *Store) migrate() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var v int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v >= len(migrations) {
		return nil
	}
	for i := v; i < len(migrations); i++ {
		if _, err := tx.Exec(migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", len(migrations))); err != nil {
		return err
	}
	return tx.Commit()
}

// Close closes the DB (SQLite checkpoints the WAL on last close).
func (s *Store) Close() error { return s.db.Close() }

// cleanupDue reports whether meta.last_cleanup is missing or older than every.
func cleanupDue(q interface {
	QueryRow(string, ...any) *sql.Row
}, now time.Time, every time.Duration) (bool, error) {
	var last string
	err := q.QueryRow("SELECT value FROM meta WHERE key = 'last_cleanup'").Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	t, err := time.Parse(time.RFC3339, last)
	return err != nil || now.Sub(t) >= every, nil
}

// CleanupIfDue deletes sources older than retention (all projects) when
// meta.last_cleanup is missing or older than every. Returns sources deleted.
// When not due it only reads: no write lock is taken.
// Port of cleanupStaleSources (ST:1641) gated per spec §3.1.
func (s *Store) CleanupIfDue(now time.Time, retention, every time.Duration) (int, error) {
	if due, err := cleanupDue(s.db, now, every); err != nil || !due {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Re-check under the write lock: another process may have just cleaned.
	if due, err := cleanupDue(tx, now, every); err != nil || !due {
		return 0, err
	}
	cutoff := now.Add(-retention).UTC().Format(sqliteTime)
	old := "SELECT id FROM sources WHERE datetime(indexed_at) < datetime(?)"
	if _, err := tx.Exec("DELETE FROM chunks WHERE source_id IN ("+old+")", cutoff); err != nil {
		return 0, err
	}
	if _, err := tx.Exec("DELETE FROM chunks_trigram WHERE source_id IN ("+old+")", cutoff); err != nil {
		return 0, err
	}
	res, err := tx.Exec("DELETE FROM sources WHERE datetime(indexed_at) < datetime(?)", cutoff)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec("INSERT INTO meta(key, value) VALUES('last_cleanup', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		now.UTC().Format(time.RFC3339)); err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): add session_meta and session_events (migration 2)"
```

### Task 6: InsertEvents, SessionEvents and the 32 KiB cut

**Files:**
- Create: `internal/store/events.go`
- Test: `internal/store/events_test.go`

**Interfaces:**
- Consumes: migration 2 (Task 5); `(*Store).db`; `openTest` from `store_test.go`
- Produces: `type Event struct{ Type, Category string; Priority int; Data string; BytesAvoided int64 }`; `type StoredEvent struct{ ID int64; SessionID, Project, Type, Category string; Priority int; Data, DataHash, SourceHook string; BytesAvoided int64; CreatedAt string }`; `const MaxEventsPerSession = 1000, EventDedupWindow = 5, MaxEventData = 32 << 10, SessionRetention = 7 * 24 * time.Hour`; `func EventDataHash(data string) string`; `func (s *Store) InsertEvents(sessionID, project, sourceHook string, evs []Event) (int, error)`; `func (s *Store) SessionEvents(sessionID string) ([]StoredEvent, error)`; test helpers `metaRow`, `sqliteTimeRe`

- [ ] **Step 1: Write the failing test**

The concurrency test opens two `Store` handles on one file and inserts from both at once, like `open_concurrent_test.go`.

`internal/store/events_test.go`:

```go
package store

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func TestEventDataHash(t *testing.T) {
	// sha256("hello") = 2cf24dba5fb0a30e...
	if got := EventDataHash("hello"); got != "2CF24DBA5FB0A30E" {
		t.Fatalf("EventDataHash = %q", got)
	}
}

func metaRow(t *testing.T, s *Store, sid string) (project, started, last string, count int) {
	t.Helper()
	err := s.db.QueryRow("SELECT project, started_at, last_event_at, event_count FROM session_meta WHERE session_id = ?", sid).
		Scan(&project, &started, &last, &count)
	if err != nil {
		t.Fatal(err)
	}
	return
}

var sqliteTimeRe = regexp.MustCompile(`^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d$`)

func TestInsertEventsStoresRowsAndMeta(t *testing.T) {
	s := openTest(t)
	n, err := s.InsertEvents("s1", "/p", "PostToolUse", []Event{
		{Type: "file_read", Category: "file", Priority: 1, Data: "/p/a.go", BytesAvoided: 10},
		{Type: "git", Category: "git", Priority: 2, Data: "status"},
	})
	if err != nil || n != 2 {
		t.Fatalf("InsertEvents = %d, %v", n, err)
	}
	evs, err := s.SessionEvents("s1")
	if err != nil || len(evs) != 2 {
		t.Fatalf("SessionEvents = %d rows, %v", len(evs), err)
	}
	e := evs[0]
	if e.SessionID != "s1" || e.Project != "/p" || e.Type != "file_read" || e.Category != "file" || e.Priority != 1 ||
		e.Data != "/p/a.go" || e.DataHash != EventDataHash("/p/a.go") || e.SourceHook != "PostToolUse" ||
		e.BytesAvoided != 10 || !sqliteTimeRe.MatchString(e.CreatedAt) || evs[1].ID <= e.ID || evs[1].Type != "git" {
		t.Fatalf("rows = %+v", evs)
	}
	project, started, last, count := metaRow(t, s, "s1")
	if project != "/p" || !sqliteTimeRe.MatchString(started) || last < started || count != 2 {
		t.Fatalf("meta = %q %q %q %d", project, started, last, count)
	}

	// An empty batch inserts nothing but still registers a new session.
	if n, err := s.InsertEvents("s2", "/q", "SessionStart", nil); err != nil || n != 0 {
		t.Fatalf("empty InsertEvents = %d, %v", n, err)
	}
	if project, _, _, count := metaRow(t, s, "s2"); project != "/q" || count != 0 {
		t.Fatalf("s2 meta = %q %d", project, count)
	}
	if evs, _ := s.SessionEvents("s2"); len(evs) != 0 {
		t.Fatalf("s2 rows = %+v", evs)
	}
}

// The cut lands on a rune start and the hash is of the stored bytes.
func TestInsertEventsCutsData(t *testing.T) {
	s := openTest(t)
	long := strings.Repeat("a", MaxEventData-1) + "é" + "tail" // é spans bytes 32767-32768
	if _, err := s.InsertEvents("s", "/p", "PostToolUse", []Event{{Type: "t", Category: "c", Priority: 3, Data: long}}); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.SessionEvents("s")
	want := strings.Repeat("a", MaxEventData-1)
	if len(evs) != 1 || evs[0].Data != want || evs[0].DataHash != EventDataHash(want) {
		t.Fatalf("stored %d bytes, hash %q", len(evs[0].Data), evs[0].DataHash)
	}
}

// Two hook processes writing to one session at once: no busy error, no lost row.
func TestInsertEventsConcurrentStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	const stores, calls = 2, 100
	var wg sync.WaitGroup
	errs := make(chan error, stores*calls)
	handles := make([]*Store, stores)
	for i := range handles {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		handles[i] = s
	}
	start := make(chan struct{})
	for i, s := range handles {
		for j := 0; j < calls; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				ev := Event{Type: "t", Category: "c", Priority: 2, Data: fmt.Sprintf("%d-%d", i, j)}
				if _, err := s.InsertEvents("s", "/p", "PostToolUse", []Event{ev}); err != nil {
					errs <- err
				}
			}()
		}
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent InsertEvents: %v", err)
	}
	evs, _ := handles[0].SessionEvents("s")
	if _, _, _, count := metaRow(t, handles[0], "s"); len(evs) != stores*calls || count != stores*calls {
		t.Fatalf("rows=%d event_count=%d, want %d", len(evs), count, stores*calls)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestEventDataHash|TestInsertEvents' -v`
Expected: FAIL with "internal/store/events_test.go:14:12: undefined: EventDataHash"

- [ ] **Step 3: Write minimal implementation**

One IMMEDIATE transaction per call: meta upsert, inserts, meta update. No dedup or cap yet.

`internal/store/events.go`:

```go
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"
)

// Event is one session event a hook records (hooks spec §3).
type Event struct {
	Type, Category string
	Priority       int // 1 most important .. 4 least
	Data           string
	BytesAvoided   int64
}

// StoredEvent is a session_events row.
type StoredEvent struct {
	ID                                 int64
	SessionID, Project, Type, Category string
	Priority                           int
	Data, DataHash, SourceHook         string
	BytesAvoided                       int64
	CreatedAt                          string // 'YYYY-MM-DD HH:MM:SS' UTC
}

const (
	MaxEventsPerSession = 1000               // DB:639
	EventDedupWindow    = 5                  // DB:642
	MaxEventData        = 32 << 10           // bytes, cut at a UTF-8 boundary
	SessionRetention    = 7 * 24 * time.Hour // DB:1696 cleanupOldSessions default
)

// EventDataHash ports the dedup hash of DB:1145: sha256 hex, first 16, upper case.
func EventDataHash(data string) string {
	sum := sha256.Sum256([]byte(data))
	return strings.ToUpper(hex.EncodeToString(sum[:8]))
}

// cutData shortens data to MaxEventData bytes without splitting a rune.
func cutData(data string) string {
	if len(data) <= MaxEventData {
		return data
	}
	i := MaxEventData
	for i > 0 && !utf8.RuneStart(data[i]) {
		i--
	}
	return data[:i]
}

// InsertEvents implements spec §3.2 in one transaction and returns the number
// of rows inserted. An empty evs still runs the session_meta upsert, so
// SessionStart can register a session.
func (s *Store) InsertEvents(sessionID, project, sourceHook string, evs []Event) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT OR IGNORE INTO session_meta(session_id, project, started_at, last_event_at)
VALUES(?, ?, datetime('now'), datetime('now'))`, sessionID, project); err != nil {
		return 0, err
	}
	inserted := 0
	for _, ev := range evs {
		data := cutData(ev.Data)
		if _, err := tx.Exec(`INSERT INTO session_events(session_id, project, type, category, priority, data, data_hash, source_hook, bytes_avoided, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
			sessionID, project, ev.Type, ev.Category, ev.Priority, data, EventDataHash(data), sourceHook, ev.BytesAvoided); err != nil {
			return 0, err
		}
		inserted++
	}
	if _, err := tx.Exec("UPDATE session_meta SET last_event_at = datetime('now'), event_count = event_count + ? WHERE session_id = ?",
		inserted, sessionID); err != nil {
		return 0, err
	}
	return inserted, tx.Commit()
}

// SessionEvents returns a session's rows ordered by id.
func (s *Store) SessionEvents(sessionID string) ([]StoredEvent, error) {
	rows, err := s.db.Query(`SELECT id, session_id, project, type, category, priority, data, data_hash, source_hook, bytes_avoided, created_at
FROM session_events WHERE session_id = ? ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredEvent
	for rows.Next() {
		var e StoredEvent
		if err := rows.Scan(&e.ID, &e.SessionID, &e.Project, &e.Type, &e.Category, &e.Priority,
			&e.Data, &e.DataHash, &e.SourceHook, &e.BytesAvoided, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run 'TestEventDataHash|TestInsertEvents' -race -count=1 -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/events.go internal/store/events_test.go
git commit -m "feat(store): insert and list session events"
```

### Task 7: Dedup window and eviction at the cap

**Files:**
- Modify: `internal/store/events.go`
- Test: `internal/store/events_test.go`

**Interfaces:**
- Consumes: `InsertEvents`, `SessionEvents`, `metaRow` (Task 6)
- Produces: `InsertEvents` skips an event whose `(type, data_hash)` is among the session's last `EventDedupWindow` rows, and at `MaxEventsPerSession` rows deletes `ORDER BY priority DESC, id ASC LIMIT 1` before inserting; test helper `insertOne`

- [ ] **Step 1: Write the failing test**

Add `insertOne`, `TestInsertEventsDedup` and `TestInsertEventsEvictsLeastImportant` at the end of the file.

`internal/store/events_test.go`:

```go
package store

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func TestEventDataHash(t *testing.T) {
	// sha256("hello") = 2cf24dba5fb0a30e...
	if got := EventDataHash("hello"); got != "2CF24DBA5FB0A30E" {
		t.Fatalf("EventDataHash = %q", got)
	}
}

func metaRow(t *testing.T, s *Store, sid string) (project, started, last string, count int) {
	t.Helper()
	err := s.db.QueryRow("SELECT project, started_at, last_event_at, event_count FROM session_meta WHERE session_id = ?", sid).
		Scan(&project, &started, &last, &count)
	if err != nil {
		t.Fatal(err)
	}
	return
}

var sqliteTimeRe = regexp.MustCompile(`^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d$`)

func TestInsertEventsStoresRowsAndMeta(t *testing.T) {
	s := openTest(t)
	n, err := s.InsertEvents("s1", "/p", "PostToolUse", []Event{
		{Type: "file_read", Category: "file", Priority: 1, Data: "/p/a.go", BytesAvoided: 10},
		{Type: "git", Category: "git", Priority: 2, Data: "status"},
	})
	if err != nil || n != 2 {
		t.Fatalf("InsertEvents = %d, %v", n, err)
	}
	evs, err := s.SessionEvents("s1")
	if err != nil || len(evs) != 2 {
		t.Fatalf("SessionEvents = %d rows, %v", len(evs), err)
	}
	e := evs[0]
	if e.SessionID != "s1" || e.Project != "/p" || e.Type != "file_read" || e.Category != "file" || e.Priority != 1 ||
		e.Data != "/p/a.go" || e.DataHash != EventDataHash("/p/a.go") || e.SourceHook != "PostToolUse" ||
		e.BytesAvoided != 10 || !sqliteTimeRe.MatchString(e.CreatedAt) || evs[1].ID <= e.ID || evs[1].Type != "git" {
		t.Fatalf("rows = %+v", evs)
	}
	project, started, last, count := metaRow(t, s, "s1")
	if project != "/p" || !sqliteTimeRe.MatchString(started) || last < started || count != 2 {
		t.Fatalf("meta = %q %q %q %d", project, started, last, count)
	}

	// An empty batch inserts nothing but still registers a new session.
	if n, err := s.InsertEvents("s2", "/q", "SessionStart", nil); err != nil || n != 0 {
		t.Fatalf("empty InsertEvents = %d, %v", n, err)
	}
	if project, _, _, count := metaRow(t, s, "s2"); project != "/q" || count != 0 {
		t.Fatalf("s2 meta = %q %d", project, count)
	}
	if evs, _ := s.SessionEvents("s2"); len(evs) != 0 {
		t.Fatalf("s2 rows = %+v", evs)
	}
}

// The cut lands on a rune start and the hash is of the stored bytes.
func TestInsertEventsCutsData(t *testing.T) {
	s := openTest(t)
	long := strings.Repeat("a", MaxEventData-1) + "é" + "tail" // é spans bytes 32767-32768
	if _, err := s.InsertEvents("s", "/p", "PostToolUse", []Event{{Type: "t", Category: "c", Priority: 3, Data: long}}); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.SessionEvents("s")
	want := strings.Repeat("a", MaxEventData-1)
	if len(evs) != 1 || evs[0].Data != want || evs[0].DataHash != EventDataHash(want) {
		t.Fatalf("stored %d bytes, hash %q", len(evs[0].Data), evs[0].DataHash)
	}
}

// Two hook processes writing to one session at once: no busy error, no lost row.
func TestInsertEventsConcurrentStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	const stores, calls = 2, 100
	var wg sync.WaitGroup
	errs := make(chan error, stores*calls)
	handles := make([]*Store, stores)
	for i := range handles {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		handles[i] = s
	}
	start := make(chan struct{})
	for i, s := range handles {
		for j := 0; j < calls; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				ev := Event{Type: "t", Category: "c", Priority: 2, Data: fmt.Sprintf("%d-%d", i, j)}
				if _, err := s.InsertEvents("s", "/p", "PostToolUse", []Event{ev}); err != nil {
					errs <- err
				}
			}()
		}
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent InsertEvents: %v", err)
	}
	evs, _ := handles[0].SessionEvents("s")
	if _, _, _, count := metaRow(t, handles[0], "s"); len(evs) != stores*calls || count != stores*calls {
		t.Fatalf("rows=%d event_count=%d, want %d", len(evs), count, stores*calls)
	}
}

func insertOne(t *testing.T, s *Store, typ string, priority int, data string) int {
	t.Helper()
	n, err := s.InsertEvents("s", "/p", "PostToolUse", []Event{{Type: typ, Category: "c", Priority: priority, Data: data}})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestInsertEventsDedup(t *testing.T) {
	s := openTest(t)
	// Same (type, data) twice in one batch: the second sees the first.
	n, err := s.InsertEvents("s", "/p", "PostToolUse", []Event{
		{Type: "git", Category: "c", Priority: 2, Data: "status"},
		{Type: "git", Category: "c", Priority: 2, Data: "status"},
	})
	if err != nil || n != 1 {
		t.Fatalf("same batch: n=%d err=%v", n, err)
	}
	if insertOne(t, s, "git", 2, "status") != 0 {
		t.Fatal("duplicate in a later call was inserted")
	}
	if insertOne(t, s, "file_read", 2, "status") != 1 {
		t.Fatal("same data with another type was skipped")
	}
	// Push "git status" out of the 5-row window, then it is new again.
	for i := 0; i < EventDedupWindow; i++ {
		insertOne(t, s, "other", 3, fmt.Sprint(i))
	}
	if insertOne(t, s, "git", 2, "status") != 1 {
		t.Fatal("event outside the dedup window was skipped")
	}
	evs, _ := s.SessionEvents("s")
	if _, _, _, count := metaRow(t, s, "s"); len(evs) != 8 || count != 8 {
		t.Fatalf("rows=%d event_count=%d, want 8", len(evs), count)
	}
}

// At the cap the oldest row of the least important priority goes first; the
// priority-1 rows survive (TS evicted them first, R2 Port note 1).
func TestInsertEventsEvictsLeastImportant(t *testing.T) {
	s := openTest(t)
	evs := make([]Event, MaxEventsPerSession)
	for i := range evs {
		evs[i] = Event{Type: "t", Category: "c", Priority: 1 + i%4, Data: fmt.Sprint(i)}
	}
	if n, err := s.InsertEvents("s", "/p", "PostToolUse", evs); err != nil || n != MaxEventsPerSession {
		t.Fatalf("fill: n=%d err=%v", n, err)
	}
	var victim int64
	s.db.QueryRow("SELECT min(id) FROM session_events WHERE priority = 4").Scan(&victim)
	if insertOne(t, s, "t", 1, "new") != 1 {
		t.Fatal("event at the cap was not inserted")
	}
	var total, ones, gone int
	s.db.QueryRow("SELECT count(*), sum(priority = 1), sum(id = ?) FROM session_events", victim).Scan(&total, &ones, &gone)
	if total != MaxEventsPerSession || ones != MaxEventsPerSession/4+1 || gone != 0 {
		t.Fatalf("total=%d priority1=%d victim left=%d", total, ones, gone)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestInsertEventsDedup|TestInsertEventsEvicts' -v`
Expected: FAIL with "events_test.go:139: same batch: n=2 err=<nil>"

- [ ] **Step 3: Write minimal implementation**

`internal/store/events.go`:

```go
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"
)

// Event is one session event a hook records (hooks spec §3).
type Event struct {
	Type, Category string
	Priority       int // 1 most important .. 4 least
	Data           string
	BytesAvoided   int64
}

// StoredEvent is a session_events row.
type StoredEvent struct {
	ID                                 int64
	SessionID, Project, Type, Category string
	Priority                           int
	Data, DataHash, SourceHook         string
	BytesAvoided                       int64
	CreatedAt                          string // 'YYYY-MM-DD HH:MM:SS' UTC
}

const (
	MaxEventsPerSession = 1000               // DB:639
	EventDedupWindow    = 5                  // DB:642
	MaxEventData        = 32 << 10           // bytes, cut at a UTF-8 boundary
	SessionRetention    = 7 * 24 * time.Hour // DB:1696 cleanupOldSessions default
)

// EventDataHash ports the dedup hash of DB:1145: sha256 hex, first 16, upper case.
func EventDataHash(data string) string {
	sum := sha256.Sum256([]byte(data))
	return strings.ToUpper(hex.EncodeToString(sum[:8]))
}

// cutData shortens data to MaxEventData bytes without splitting a rune.
func cutData(data string) string {
	if len(data) <= MaxEventData {
		return data
	}
	i := MaxEventData
	for i > 0 && !utf8.RuneStart(data[i]) {
		i--
	}
	return data[:i]
}

// InsertEvents implements spec §3.2 in one transaction and returns the number
// of rows inserted. An empty evs still runs the session_meta upsert, so
// SessionStart can register a session.
func (s *Store) InsertEvents(sessionID, project, sourceHook string, evs []Event) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT OR IGNORE INTO session_meta(session_id, project, started_at, last_event_at)
VALUES(?, ?, datetime('now'), datetime('now'))`, sessionID, project); err != nil {
		return 0, err
	}
	var count int
	if err := tx.QueryRow("SELECT count(*) FROM session_events WHERE session_id = ?", sessionID).Scan(&count); err != nil {
		return 0, err
	}
	inserted := 0
	for _, ev := range evs {
		data := cutData(ev.Data)
		hash := EventDataHash(data)
		// DB:962-968; the window sees rows inserted earlier in this transaction.
		var dup int
		err := tx.QueryRow(`SELECT count(*) FROM (
  SELECT type, data_hash FROM session_events WHERE session_id = ? ORDER BY id DESC LIMIT ?
) WHERE type = ? AND data_hash = ?`, sessionID, EventDedupWindow, ev.Type, hash).Scan(&dup)
		if err != nil {
			return 0, err
		}
		if dup > 0 {
			continue
		}
		if count >= MaxEventsPerSession {
			// Evict the oldest of the least important rows (TS: priority ASC, R2 Port note 1).
			if _, err := tx.Exec(`DELETE FROM session_events WHERE id = (
  SELECT id FROM session_events WHERE session_id = ? ORDER BY priority DESC, id ASC LIMIT 1)`, sessionID); err != nil {
				return 0, err
			}
		} else {
			count++
		}
		if _, err := tx.Exec(`INSERT INTO session_events(session_id, project, type, category, priority, data, data_hash, source_hook, bytes_avoided, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
			sessionID, project, ev.Type, ev.Category, ev.Priority, data, hash, sourceHook, ev.BytesAvoided); err != nil {
			return 0, err
		}
		inserted++
	}
	if _, err := tx.Exec("UPDATE session_meta SET last_event_at = datetime('now'), event_count = event_count + ? WHERE session_id = ?",
		inserted, sessionID); err != nil {
		return 0, err
	}
	return inserted, tx.Commit()
}

// SessionEvents returns a session's rows ordered by id.
func (s *Store) SessionEvents(sessionID string) ([]StoredEvent, error) {
	rows, err := s.db.Query(`SELECT id, session_id, project, type, category, priority, data, data_hash, source_hook, bytes_avoided, created_at
FROM session_events WHERE session_id = ? ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredEvent
	for rows.Next() {
		var e StoredEvent
		if err := rows.Scan(&e.ID, &e.SessionID, &e.Project, &e.Type, &e.Category, &e.Priority,
			&e.Data, &e.DataHash, &e.SourceHook, &e.BytesAvoided, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -race -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/events.go internal/store/events_test.go
git commit -m "feat(store): dedup session events and evict least important at the cap"
```

### Task 8: Sweep inactive sessions in CleanupIfDue

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: `InsertEvents`, `SessionRetention` (Tasks Task 6, Task 7); `sqliteTime`
- Produces: `CleanupIfDue(now time.Time, retention, every time.Duration) (int, error)` (signature unchanged) also deletes `session_events` and `session_meta` rows of sessions with `last_event_at < now - SessionRetention`, in the same transaction

- [ ] **Step 1: Write the failing test**

Add `TestCleanupIfDueSessions` before `TestCleanupNotDueTakesNoWriteLock`.

`internal/store/store_test.go`:

```go
package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "sub", "cmgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenSchemaAndPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "cmgo.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	var jm string
	var busy, fk, syncMode int
	s.db.QueryRow("PRAGMA user_version").Scan(&v)
	s.db.QueryRow("PRAGMA journal_mode").Scan(&jm)
	s.db.QueryRow("PRAGMA busy_timeout").Scan(&busy)
	s.db.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	s.db.QueryRow("PRAGMA synchronous").Scan(&syncMode)
	if v != 2 || jm != "wal" || busy != 30000 || fk != 1 || syncMode != 1 {
		t.Fatalf("user_version=%d journal=%s busy=%d fk=%d sync=%d", v, jm, busy, fk, syncMode)
	}
	for _, tbl := range []string{"meta", "sources", "chunks", "chunks_trigram", "vocabulary"} {
		var n int
		if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = ?", tbl).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s: n=%d err=%v", tbl, n, err)
		}
	}
	if _, err := s.db.Exec("INSERT INTO sources(project, label, chunk_count, code_chunk_count) VALUES('p','x',0,0)"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Reopen: migrations are not re-run and data survives.
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	s.db.QueryRow("SELECT count(*) FROM sources").Scan(&n)
	if n != 1 {
		t.Fatalf("sources after reopen = %d", n)
	}
}

// A DB written by the core (user_version 1) gains the session tables on Open
// and keeps its sources.
func TestMigrateFromV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	all := migrations
	migrations = all[:1] // the core code path: only migration 1 exists
	old, err := Open(path)
	migrations = all
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec("INSERT INTO sources(project, label, chunk_count, code_chunk_count) VALUES('p','x',0,0)"); err != nil {
		t.Fatal(err)
	}
	old.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v, n int
	s.db.QueryRow("PRAGMA user_version").Scan(&v)
	s.db.QueryRow("SELECT count(*) FROM sources").Scan(&n)
	if v != 2 || n != 1 {
		t.Fatalf("user_version=%d sources=%d, want 2 and 1", v, n)
	}
	for _, name := range []string{"session_meta", "session_events", "session_events_session", "session_events_project", "session_events_evict"} {
		if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = ?", name).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s: n=%d err=%v", name, n, err)
		}
	}
}

func TestCleanupIfDue(t *testing.T) {
	s := openTest(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO sources(id, project, label, chunk_count, code_chunk_count, indexed_at) VALUES(1,'p','old',1,0,'2020-01-01 00:00:00')")
	exec("INSERT INTO sources(id, project, label, chunk_count, code_chunk_count) VALUES(2,'q','fresh',1,0)")
	for _, tbl := range []string{"chunks", "chunks_trigram"} {
		exec("INSERT INTO " + tbl + "(title, content, source_id, content_type) VALUES('t','old body',1,'prose')")
		exec("INSERT INTO " + tbl + "(title, content, source_id, content_type) VALUES('t','fresh body',2,'prose')")
	}

	now := time.Now()
	const day = 24 * time.Hour
	n, err := s.CleanupIfDue(now, 14*day, day)
	if err != nil || n != 1 {
		t.Fatalf("first cleanup n=%d err=%v", n, err)
	}
	var left, chunks, tri int
	s.db.QueryRow("SELECT count(*) FROM sources").Scan(&left)
	s.db.QueryRow("SELECT count(*) FROM chunks").Scan(&chunks)
	s.db.QueryRow("SELECT count(*) FROM chunks_trigram").Scan(&tri)
	if left != 1 || chunks != 1 || tri != 1 {
		t.Fatalf("after cleanup sources=%d chunks=%d trigram=%d", left, chunks, tri)
	}

	// Not due again within 24h, even if an old source appears.
	exec("UPDATE sources SET indexed_at = '2020-01-01 00:00:00' WHERE id = 2")
	if n, err := s.CleanupIfDue(now.Add(23*time.Hour), 14*day, day); err != nil || n != 0 {
		t.Fatalf("second cleanup n=%d err=%v", n, err)
	}
	// Due after 24h.
	if n, err := s.CleanupIfDue(now.Add(25*time.Hour), 14*day, day); err != nil || n != 1 {
		t.Fatalf("third cleanup n=%d err=%v", n, err)
	}
	var last string
	s.db.QueryRow("SELECT value FROM meta WHERE key='last_cleanup'").Scan(&last)
	if last != now.Add(25*time.Hour).UTC().Format(time.RFC3339) {
		t.Fatalf("last_cleanup = %q", last)
	}
}

// Sessions are swept by last activity, not start time: an old session that is
// still active stays.
func TestCleanupIfDueSessions(t *testing.T) {
	s := openTest(t)
	ev := []Event{{Type: "t", Category: "c", Priority: 1, Data: "x"}}
	for _, sid := range []string{"stale", "active", "fresh"} {
		if _, err := s.InsertEvents(sid, "/p", "PostToolUse", ev); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	ago := func(d time.Duration) string { return now.Add(-d).UTC().Format(sqliteTime) }
	const day = 24 * time.Hour
	s.db.Exec("UPDATE session_meta SET started_at = ?, last_event_at = ? WHERE session_id = 'stale'", ago(9*day), ago(8*day))
	s.db.Exec("UPDATE session_meta SET started_at = ?, last_event_at = ? WHERE session_id = 'active'", ago(30*day), ago(6*day))

	if _, err := s.CleanupIfDue(now, 14*day, day); err != nil {
		t.Fatal(err)
	}
	var metas, events int
	s.db.QueryRow("SELECT count(*) FROM session_meta WHERE session_id = 'stale'").Scan(&metas)
	s.db.QueryRow("SELECT count(*) FROM session_events WHERE session_id = 'stale'").Scan(&events)
	if metas != 0 || events != 0 {
		t.Fatalf("stale session left: meta=%d events=%d", metas, events)
	}
	s.db.QueryRow("SELECT count(*) FROM session_meta").Scan(&metas)
	s.db.QueryRow("SELECT count(*) FROM session_events").Scan(&events)
	if metas != 2 || events != 2 {
		t.Fatalf("kept meta=%d events=%d, want 2 and 2", metas, events)
	}
}

// A not-due CleanupIfDue must only read. Another Store holds the write lock;
// the not-due call must return at once, while a due call (control) waits.
func TestCleanupNotDueTakesNoWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	now := time.Now()
	const day = 24 * time.Hour
	if _, err := s.CleanupIfDue(now, 14*day, day); err != nil {
		t.Fatal(err)
	}
	lock, err := other.db.Begin() // BEGIN IMMEDIATE: holds the write lock
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { _, err := s.CleanupIfDue(now.Add(time.Hour), 14*day, day); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		lock.Rollback()
		t.Fatal("not-due CleanupIfDue waited for the write lock")
	}

	// Control: a due call needs the lock, so it waits until the lock is released.
	go func() { _, err := s.CleanupIfDue(now.Add(25*time.Hour), 14*day, day); done <- err }()
	select {
	case <-done:
		lock.Rollback()
		t.Fatal("due CleanupIfDue did not wait for the write lock")
	case <-time.After(300 * time.Millisecond):
	}
	lock.Rollback()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestCleanupIfDue' -v`
Expected: FAIL with "store_test.go:162: stale session left: meta=1 events=1"

- [ ] **Step 3: Write minimal implementation**

The gate and the sources cleanup stay as they are; the two session deletes go before the `last_cleanup` write.

`internal/store/store.go`:

```go
// Package store is the SQLite FTS5 knowledge base: schema, chunkers, search
// and snippets. Port of context-mode src/store.ts (ST) with the differences
// listed in the cmgo core spec §2.2 and §3.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

// ContentType is the chunk kind stored in the content_type column.
type ContentType string

const (
	Code  ContentType = "code"
	Prose ContentType = "prose"
)

// sqliteTime is the layout of datetime('now') values in sources.indexed_at.
const sqliteTime = "2006-01-02 15:04:05"

// Store owns one *sql.DB with a single connection kept open for the life of
// the process.
type Store struct {
	db *sql.DB

	mu         sync.Mutex
	indexCalls int               // Index calls in this process; FTS optimize every 50 (ST:1132)
	fuzzyKeys  []string          // LRU order of fuzzyVals, oldest first (ST:470)
	fuzzyVals  map[string]string // word -> correction; "" means no correction
}

// migrations[i] moves the schema from user_version i to i+1.
var migrations = []string{
	`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE sources (
  id INTEGER PRIMARY KEY,
  project TEXT NOT NULL,
  label TEXT NOT NULL,
  chunk_count INTEGER NOT NULL,
  code_chunk_count INTEGER NOT NULL,
  indexed_at TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE (project, label)
);
CREATE VIRTUAL TABLE chunks USING fts5(
  title, content, source_id UNINDEXED, content_type UNINDEXED,
  tokenize = 'porter unicode61');
CREATE VIRTUAL TABLE chunks_trigram USING fts5(
  title, content, source_id UNINDEXED, content_type UNINDEXED,
  tokenize = 'trigram');
CREATE TABLE vocabulary (word TEXT PRIMARY KEY);`,
	// Session event capture (hooks spec §3.1).
	`CREATE TABLE session_meta (
  session_id TEXT PRIMARY KEY,
  project TEXT NOT NULL,
  started_at TEXT NOT NULL,         -- UTC 'YYYY-MM-DD HH:MM:SS', like sources.indexed_at
  last_event_at TEXT NOT NULL,
  event_count INTEGER NOT NULL DEFAULT 0,
  compact_count INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE session_events (
  id INTEGER PRIMARY KEY,
  session_id TEXT NOT NULL,
  project TEXT NOT NULL,
  type TEXT NOT NULL,
  category TEXT NOT NULL,
  priority INTEGER NOT NULL,        -- 1 = most important ... 4 = least
  data TEXT NOT NULL,
  data_hash TEXT NOT NULL,          -- sha256(data) hex, first 16, upper case (DB:1145)
  source_hook TEXT NOT NULL,
  bytes_avoided INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL          -- UTC 'YYYY-MM-DD HH:MM:SS'
);
CREATE INDEX session_events_session ON session_events(session_id, id);
CREATE INDEX session_events_project ON session_events(project, id);
CREATE INDEX session_events_evict ON session_events(session_id, priority, id);`,
}

// Open creates the parent directory, opens the DB with WAL pragmas and
// applies pending migrations.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// journal_mode is not in the DSN: WAL persists in the file, and switching
	// to it needs an exclusive lock that SQLite refuses with SQLITE_BUSY
	// without consulting busy_timeout. Re-applying it on every connection made
	// a session fail to start while another session was writing.
	dsn := (&url.URL{Scheme: "file", Path: abs}).String() +
		"?_txlock=immediate" +
		"&_pragma=busy_timeout(30000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)"
	db, err := driver.Open(dsn, fts5.Register)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	s := &Store{db: db, fuzzyVals: map[string]string{}}
	if err := s.ensureWAL(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// walSwitchTimeout bounds retries of the WAL switch; it matches busy_timeout.
const walSwitchTimeout = 30 * time.Second

// ensureWAL switches the file to WAL once. Reading the mode takes no lock;
// the switch is retried with backoff while another connection holds a lock.
func (s *Store) ensureWAL() error {
	deadline := time.Now().Add(walSwitchTimeout)
	wait := 10 * time.Millisecond
	for {
		var mode string
		err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
		if err == nil && mode == "wal" {
			return nil
		}
		if err == nil {
			err = s.db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode)
			if err == nil && mode == "wal" {
				return nil
			}
			if err == nil {
				err = fmt.Errorf("journal_mode is %q, want wal", mode)
			}
		}
		if !isBusy(err) || time.Now().After(deadline) {
			return fmt.Errorf("enable WAL: %w", err)
		}
		time.Sleep(wait)
		if wait < 250*time.Millisecond {
			wait *= 2
		}
	}
}

// isBusy reports SQLITE_BUSY / SQLITE_LOCKED, which the driver may wrap.
func isBusy(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sqlite3.BUSY) || errors.Is(err, sqlite3.LOCKED) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "database table is locked")
}

// migrate re-reads user_version inside an IMMEDIATE transaction so two
// processes opening a fresh file at once do not both run step 1.
func (s *Store) migrate() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var v int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v >= len(migrations) {
		return nil
	}
	for i := v; i < len(migrations); i++ {
		if _, err := tx.Exec(migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", len(migrations))); err != nil {
		return err
	}
	return tx.Commit()
}

// Close closes the DB (SQLite checkpoints the WAL on last close).
func (s *Store) Close() error { return s.db.Close() }

// cleanupDue reports whether meta.last_cleanup is missing or older than every.
func cleanupDue(q interface {
	QueryRow(string, ...any) *sql.Row
}, now time.Time, every time.Duration) (bool, error) {
	var last string
	err := q.QueryRow("SELECT value FROM meta WHERE key = 'last_cleanup'").Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	t, err := time.Parse(time.RFC3339, last)
	return err != nil || now.Sub(t) >= every, nil
}

// CleanupIfDue deletes sources older than retention (all projects) when
// meta.last_cleanup is missing or older than every, and in the same
// transaction the sessions inactive for SessionRetention. Returns sources deleted.
// When not due it only reads: no write lock is taken.
// Port of cleanupStaleSources (ST:1641) gated per spec §3.1.
func (s *Store) CleanupIfDue(now time.Time, retention, every time.Duration) (int, error) {
	if due, err := cleanupDue(s.db, now, every); err != nil || !due {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Re-check under the write lock: another process may have just cleaned.
	if due, err := cleanupDue(tx, now, every); err != nil || !due {
		return 0, err
	}
	cutoff := now.Add(-retention).UTC().Format(sqliteTime)
	old := "SELECT id FROM sources WHERE datetime(indexed_at) < datetime(?)"
	if _, err := tx.Exec("DELETE FROM chunks WHERE source_id IN ("+old+")", cutoff); err != nil {
		return 0, err
	}
	if _, err := tx.Exec("DELETE FROM chunks_trigram WHERE source_id IN ("+old+")", cutoff); err != nil {
		return 0, err
	}
	res, err := tx.Exec("DELETE FROM sources WHERE datetime(indexed_at) < datetime(?)", cutoff)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	// Sessions inactive for SessionRetention (DB:1696-1705, keyed on activity).
	stale := now.Add(-SessionRetention).UTC().Format(sqliteTime)
	if _, err := tx.Exec("DELETE FROM session_events WHERE session_id IN (SELECT session_id FROM session_meta WHERE last_event_at < ?)", stale); err != nil {
		return 0, err
	}
	if _, err := tx.Exec("DELETE FROM session_meta WHERE last_event_at < ?", stale); err != nil {
		return 0, err
	}
	if _, err := tx.Exec("INSERT INTO meta(key, value) VALUES('last_cleanup', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		now.UTC().Format(time.RFC3339)); err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -race -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): delete sessions inactive for 7 days during cleanup"
```

### Task 9: EvaluateCommand with Bash allow and ask rules

**Files:**
- Modify: `internal/security/settings.go`
- Create: `internal/security/evaluate.go`
- Test: `internal/security/evaluate_test.go`

**Interfaces:**
- Consumes: core `internal/security`: `(p *Policy) rules() []*fileRules`, `matchCommand`, `collectCommandElements`, `globToRegex`, `toolPattern`, `caseInsensitive`; test helpers `newTestPolicy`, `writeFile`, `localSettings`, `sharedSettings`, `homeSettings`
- Produces: `func (p *Policy) EvaluateCommand(cmd string) (decision, pattern string)`; unexported `fileRules.bashAsk`, `fileRules.bashAllow []bashRule`, `firstMatch(rules []bashRule, el string) string`

- [ ] **Step 1: Write the failing test**

Expected values were checked against `evaluateCommand` from the TS `build/security.js`, imported directly with the same policies.

`internal/security/evaluate_test.go`:

```go
package security

import "testing"

type evalCase struct{ cmd, decision, pattern string }

func checkEval(t *testing.T, p *Policy, cases []evalCase) {
	t.Helper()
	for _, c := range cases {
		if d, pat := p.EvaluateCommand(c.cmd); d != c.decision || pat != c.pattern {
			t.Errorf("EvaluateCommand(%q) = %q, %q; want %q, %q", c.cmd, d, pat, c.decision, c.pattern)
		}
	}
}

func TestEvaluateCommandOneFile(t *testing.T) {
	p, _, home, _ := newTestPolicy(t)
	writeFile(t, homeSettings(home), `{"permissions":{
"deny":["Bash(sudo *)"],
"ask":["Bash(git push:*)"],
"allow":["Bash(sudo *)","Bash(git:*)","Bash(echo:*)","Read(**)"]}}`)
	checkEval(t, p, []evalCase{
		{"sudo ls", "deny", "Bash(sudo *)"},                  // deny wins over allow
		{"echo ok && sudo rm -rf /", "deny", "Bash(sudo *)"}, // any element
		{"echo $(sudo id)", "deny", "Bash(sudo *)"},          // subshell element
		{"git push origin main", "ask", "Bash(git push:*)"},
		{"echo hi; git push", "ask", "Bash(git push:*)"},
		{"git status && echo done", "allow", "Bash(echo:*)"}, // TS reports the last element's rule
		{"git status && rm x", "ask", ""},                    // one element not allowed
		{"cat /etc/hosts", "ask", ""},                        // Read(**) is not a Bash rule
		{" ; && ", "ask", ""},                                // no elements: never allow
	})
}

func TestEvaluateCommandNoSettings(t *testing.T) {
	p, _, _, _ := newTestPolicy(t)
	checkEval(t, p, []evalCase{{"ls", "ask", ""}})
}

// Files are tried in order project-local, project, home; the first file that
// asks or allows every element decides, even if a later file would ask.
func TestEvaluateCommandFilePrecedence(t *testing.T) {
	p, project, home, _ := newTestPolicy(t)
	writeFile(t, localSettings(project), `{"permissions":{"allow":["Bash(git:*)"],"ask":["Bash(make:*)"]}}`)
	writeFile(t, sharedSettings(project), `{"permissions":{"allow":["Bash(npm:*)","Bash(git:*)","Bash(make:*)"]}}`)
	writeFile(t, homeSettings(home), `{"permissions":{"ask":["Bash(git:*)","Bash(npm:*)"],"deny":["Bash(rm *)"]}}`)
	checkEval(t, p, []evalCase{
		{"git status", "allow", "Bash(git:*)"},             // local allows; home's ask is never reached
		{"make all", "ask", "Bash(make:*)"},                // local ask beats shared allow
		{"git status && npm test", "allow", "Bash(npm:*)"}, // local partial, shared allows all
		{"git status && rm x", "deny", "Bash(rm *)"},       // deny in any file wins first
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/security/ -run TestEvaluateCommand -v`
Expected: FAIL with "internal/security/evaluate_test.go:10:18: p.EvaluateCommand undefined (type *Policy has no field or method EvaluateCommand)"

- [ ] **Step 3: Write minimal implementation**

`parseSettingsFile` keeps `Bash(...)` entries of `permissions.allow` and `permissions.ask`; existing tests stay unchanged.

`internal/security/settings.go`:

```go
// Package security ports context-mode's server-side deny firewall
// (src/security.ts and its wiring in src/server.ts).
package security

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// caseInsensitive mirrors TS `process.platform === "darwin"` (win32 dropped).
// It applies to Bash deny, the project boundary, Read allow and Read deny.
var caseInsensitive = runtime.GOOS == "darwin"

// bashRule is one `Bash(glob)` entry; pattern is the full entry text.
type bashRule struct {
	pattern string
	re      *regexp.Regexp
}

// fileRule is one `Read(glob)` entry; glob is the text inside the parens.
type fileRule struct {
	glob string
	re   *regexp.Regexp
}

// fileRules holds the rules parsed from one settings file.
type fileRules struct {
	bashDeny  []bashRule
	bashAsk   []bashRule // read by EvaluateCommand only
	bashAllow []bashRule // read by EvaluateCommand only
	readDeny  []fileRule
	readAllow []fileRule
}

type cacheEntry struct {
	mtime time.Time
	size  int64
	rules *fileRules // nil when the file could not be read or parsed
}

// Policy evaluates commands and paths against Claude Code settings files.
type Policy struct {
	projectDir string
	home       string
	warn       io.Writer

	mu    sync.Mutex
	cache map[string]cacheEntry
}

// NewPolicy returns a Policy reading settings under projectDir and home.
// An empty projectDir or home skips the files rooted there.
func NewPolicy(projectDir, home string) *Policy {
	return &Policy{projectDir: projectDir, home: home, warn: os.Stderr, cache: map[string]cacheEntry{}}
}

// settingsPaths ports readBashPolicies / readToolPermissionPatterns file order
// (security.ts:363-394, 426-490). Only the claude global is read: adapter
// globals and CLAUDE_CONFIG_DIR are dropped (spec §4.5).
func (p *Policy) settingsPaths() []string {
	var paths []string
	if p.projectDir != "" {
		paths = append(paths,
			filepath.Join(p.projectDir, ".claude", "settings.local.json"),
			filepath.Join(p.projectDir, ".claude", "settings.json"))
	}
	if p.home != "" {
		paths = append(paths, filepath.Join(p.home, ".claude", "settings.json"))
	}
	return paths
}

// rules returns the parsed rules of every readable settings file in
// precedence order. Each file costs one stat; it is re-parsed only when its
// mtime or size changes.
func (p *Policy) rules() []*fileRules {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []*fileRules
	for _, path := range p.settingsPaths() {
		fi, err := os.Stat(path)
		if err != nil {
			delete(p.cache, path)
			continue // missing file: silently skipped, as in TS
		}
		e, ok := p.cache[path]
		if !ok || !e.mtime.Equal(fi.ModTime()) || e.size != fi.Size() {
			e = cacheEntry{mtime: fi.ModTime(), size: fi.Size()}
			r, err := parseSettingsFile(path)
			if err != nil {
				// Fail-open like TS; warn once per file version, not per call.
				fmt.Fprintf(p.warn, "context-mode: ignoring settings file %s: %v\n", path, err)
			} else {
				e.rules = r
			}
			p.cache[path] = e
		}
		if e.rules != nil {
			out = append(out, e.rules)
		}
	}
	return out
}

// toolPattern ports parseToolPattern (security.ts:37-44).
var toolPattern = regexp.MustCompile(`^(\w+)\((.+)\)$`)

// parseSettingsFile ports readSingleSettings + readToolPermissionPatterns
// extractGlobs (security.ts:321-351, 434-461): permissions.deny Bash()/Read(),
// permissions.allow Bash()/Read() and permissions.ask Bash() entries are kept.
func parseSettingsFile(path string) (*fileRules, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	root, _ := doc.(map[string]any)
	perms, _ := root["permissions"].(map[string]any)
	r := &fileRules{}
	for _, entry := range stringsOf(perms["deny"]) {
		m := toolPattern.FindStringSubmatch(entry)
		if m == nil {
			continue
		}
		switch m[1] {
		case "Bash":
			r.bashDeny = append(r.bashDeny, bashRule{pattern: entry, re: globToRegex(m[2], caseInsensitive)})
		case "Read":
			// Intentional difference from TS (server.ts:1222 folds only on win32): fold on darwin so .ENV cannot bypass Read(.env) on APFS.
			r.readDeny = append(r.readDeny, fileRule{glob: m[2], re: fileGlobToRegex(m[2], caseInsensitive)})
		}
	}
	for _, entry := range stringsOf(perms["allow"]) {
		m := toolPattern.FindStringSubmatch(entry)
		if m == nil {
			continue
		}
		switch m[1] {
		case "Bash":
			r.bashAllow = append(r.bashAllow, bashRule{pattern: entry, re: globToRegex(m[2], caseInsensitive)})
		case "Read":
			r.readAllow = append(r.readAllow, fileRule{glob: m[2], re: fileGlobToRegex(m[2], caseInsensitive)})
		}
	}
	for _, entry := range stringsOf(perms["ask"]) {
		if m := toolPattern.FindStringSubmatch(entry); m != nil && m[1] == "Bash" {
			r.bashAsk = append(r.bashAsk, bashRule{pattern: entry, re: globToRegex(m[2], caseInsensitive)})
		}
	}
	return r, nil
}

// stringsOf returns the string elements of a JSON array, skipping the rest.
func stringsOf(v any) []string {
	arr, _ := v.([]any)
	var out []string
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// globToRegex ports security.ts:69-90. "cmd:args" becomes ^cmd(\s args)?$;
// otherwise * becomes .* and everything else is literal.
func globToRegex(glob string, fold bool) *regexp.Regexp {
	var expr string
	if cmd, args, ok := strings.Cut(glob, ":"); ok {
		expr = `^` + regexp.QuoteMeta(cmd) + `(\s` + convertGlobPart(args) + `)?$`
	} else {
		expr = `^` + convertGlobPart(glob) + `$`
	}
	if fold {
		expr = `(?i)` + expr
	}
	return regexp.MustCompile(expr)
}

// convertGlobPart ports security.ts:56-60.
func convertGlobPart(glob string) string {
	return strings.ReplaceAll(regexp.QuoteMeta(glob), `\*`, `.*`)
}

// fileGlobToRegex ports security.ts:101-135: ** spans directories, * and ?
// stay within one path segment.
func fileGlobToRegex(glob string, fold bool) *regexp.Regexp {
	var b strings.Builder
	for i := 0; i < len(glob); {
		switch {
		case glob[i] == '*' && i+1 < len(glob) && glob[i+1] == '*':
			if i+2 < len(glob) && glob[i+2] == '/' {
				b.WriteString(`(.*/)?`)
				i += 3
			} else {
				b.WriteString(`.*`)
				i += 2
			}
		case glob[i] == '*':
			b.WriteString(`[^/]*`)
			i++
		case glob[i] == '?':
			b.WriteString(`[^/]`)
			i++
		default:
			b.WriteString(regexp.QuoteMeta(glob[i : i+1]))
			i++
		}
	}
	expr := `^` + b.String() + `$`
	if fold {
		expr = `(?i)` + expr
	}
	return regexp.MustCompile(expr)
}
```

`internal/security/evaluate.go`:

```go
package security

// EvaluateCommand ports evaluateCommand (security.ts:512-563). decision is
// "deny", "ask" or "allow"; pattern is the full rule ("Bash(sudo *)"), or ""
// for the default ask. A deny in any file wins first; then, file by file, an
// ask on any element returns ask, and allow needs every element allowed.
func (p *Policy) EvaluateCommand(cmd string) (decision, pattern string) {
	rules := p.rules()
	if blocked, pat := matchCommand(rules, cmd); blocked {
		return "deny", pat
	}
	els := collectCommandElements(cmd)
	for _, r := range rules {
		allAllowed, allowPat := len(els) > 0, ""
		for _, el := range els {
			if pat := firstMatch(r.bashAsk, el); pat != "" {
				return "ask", pat
			}
			if pat := firstMatch(r.bashAllow, el); pat != "" {
				allowPat = pat // TS reports the last element's allow rule
			} else {
				allAllowed = false
			}
		}
		if allAllowed {
			return "allow", allowPat
		}
	}
	return "ask", ""
}

// firstMatch ports matchesAnyPattern (security.ts:141-152).
func firstMatch(rules []bashRule, el string) string {
	for _, r := range rules {
		if r.re.MatchString(el) {
			return r.pattern
		}
	}
	return ""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/security/ -race -count=1 -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/security/settings.go internal/security/evaluate.go internal/security/evaluate_test.go
git commit -m "feat(security): add EvaluateCommand with Bash allow and ask rules"
```

---

### Task 10: Order-preserving JSON tree

**Files:**
- Create: `internal/install/json.go`
- Test: `internal/install/json_test.go`

**Interfaces:**
- Consumes: none (stdlib only)
- Produces: unexported `type object struct{ keys []string; vals map[string]any }`; `func newObject() *object`; `(o *object) get(k string) (any, bool)`; `(o *object) set(k string, v any)`; `(o *object) del(k string)`; `func parseJSON(b []byte) (any, error)`; `func encodeJSON(v any) []byte`

- [ ] **Step 1: Write the failing test**

`internal/install/json_test.go`:

```go
package install

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONRoundTrip(t *testing.T) {
	// Already in JSON.stringify(x, null, 2) form, so it must come back byte for byte.
	in := `{
  "z": 1.50,
  "a": [
    1e3,
    -0,
    true,
    false,
    null,
    "<b>&amp;</b> \"q\" \\ é 😀"
  ],
  "empty": {},
  "list": [],
  "nested": {
    "k": {
      "x": []
    }
  }
}
`
	v, err := parseJSON([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(encodeJSON(v)); got != in {
		t.Fatalf("round trip:\n%s\nwant:\n%s", got, in)
	}
}

func TestJSONReformat(t *testing.T) {
	v, err := parseJSON([]byte(` {"b":{"c":[1,{}]},"a":"é<"} `))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"b\": {\n    \"c\": [\n      1,\n      {}\n    ]\n  },\n  \"a\": \"é<\"\n}\n"
	if got := string(encodeJSON(v)); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestJSONDuplicateKey(t *testing.T) {
	// JSON.parse keeps the first position and the last value.
	v, err := parseJSON([]byte(`{"a":1,"b":2,"a":3}`))
	if err != nil {
		t.Fatal(err)
	}
	o := v.(*object)
	if strings.Join(o.keys, ",") != "a,b" || o.vals["a"] != json.Number("3") {
		t.Fatalf("keys %v vals %v", o.keys, o.vals)
	}
}

func TestJSONErrors(t *testing.T) {
	for _, in := range []string{``, `{"a": 1,`, `{"a":1,}`, `{"a":1} x`, `{} {}`, `[1`, `{"a" 1}`} {
		if _, err := parseJSON([]byte(in)); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
}

func TestObjectEdit(t *testing.T) {
	o := newObject()
	o.set("a", "1")
	o.set("b", "2")
	o.set("c", "3")
	o.set("a", "x")
	o.del("b")
	o.del("missing")
	if strings.Join(o.keys, ",") != "a,c" || o.vals["a"] != "x" {
		t.Fatalf("keys %v vals %v", o.keys, o.vals)
	}
	if _, ok := o.get("b"); ok {
		t.Fatal("b still present")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/install/ -run 'TestJSON|TestObjectEdit' -v`
Expected: FAIL with "undefined: parseJSON"

- [ ] **Step 3: Write minimal implementation**

`internal/install/json.go`:

```go
package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

// object is a JSON object that keeps key order, so a rewritten settings file
// differs from the original only where cmgo changed it. Values are *object,
// []any, string, json.Number (the original text), bool or nil.
type object struct {
	keys []string
	vals map[string]any
}

func newObject() *object { return &object{vals: map[string]any{}} }

func (o *object) get(k string) (any, bool) {
	v, ok := o.vals[k]
	return v, ok
}

// set replaces a value in place, or appends the key at the end.
func (o *object) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	o.keys = slices.DeleteFunc(o.keys, func(s string) bool { return s == k })
}

// parseJSON decodes exactly one JSON value. A duplicate key keeps its first
// position and its last value, like JSON.parse.
func parseJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("invalid data after the top-level value")
	}
	return v, nil
}

func parseValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err == io.EOF {
		return nil, io.ErrUnexpectedEOF
	}
	if err != nil {
		return nil, err
	}
	switch tok {
	case json.Delim('{'):
		o := newObject()
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := parseValue(dec)
			if err != nil {
				return nil, err
			}
			o.set(k.(string), v)
		}
		return o, closeDelim(dec)
	case json.Delim('['):
		a := []any{}
		for dec.More() {
			v, err := parseValue(dec)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		return a, closeDelim(dec)
	}
	if d, ok := tok.(json.Delim); ok {
		return nil, fmt.Errorf("unexpected %q", rune(d))
	}
	return tok, nil
}

func closeDelim(dec *json.Decoder) error {
	_, err := dec.Token()
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

// encodeJSON writes v like JS JSON.stringify(v, null, 2) + "\n": 2-space
// indent, "{}" and "[]" for empty values, no HTML escaping.
func encodeJSON(v any) []byte {
	var b bytes.Buffer
	encodeValue(&b, v, "")
	b.WriteByte('\n')
	return b.Bytes()
}

func encodeValue(b *bytes.Buffer, v any, indent string) {
	in := indent + "  "
	switch t := v.(type) {
	case *object:
		if len(t.keys) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteByte('{')
		for i, k := range t.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString("\n" + in)
			encodeString(b, k)
			b.WriteString(": ")
			encodeValue(b, t.vals[k], in)
		}
		b.WriteString("\n" + indent + "}")
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString("\n" + in)
			encodeValue(b, e, in)
		}
		b.WriteString("\n" + indent + "]")
	case string:
		encodeString(b, t)
	case json.Number:
		b.WriteString(string(t))
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case nil:
		b.WriteString("null")
	}
}

func encodeString(b *bytes.Buffer, s string) {
	e := json.NewEncoder(b)
	e.SetEscapeHTML(false)
	_ = e.Encode(s)         // a string always encodes
	b.Truncate(b.Len() - 1) // Encode adds a newline
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/install/ -run 'TestJSON|TestObjectEdit' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/install/json.go internal/install/json_test.go
git commit -m "feat(install): order-preserving JSON tree for settings files"
```

### Task 11: HookEvents, own-command detection, strip and add

**Files:**
- Create: `internal/install/hooks.go`
- Test: `internal/install/hooks_test.go`

**Interfaces:**
- Consumes: `newObject`, `parseJSON`, `encodeJSON` and the `object` methods (Task 10)
- Produces: `var HookEvents = []struct{ Event, Arg, Matcher string }`; unexported `func hookCommand(binary, arg string) string`; `func isOwnCommand(cmd string) bool`; `func stripHooks(root *object) int`; `func addHooks(root *object, binary string) error`; test helpers `const bin`, `func mustParse(t *testing.T, s string) *object`

- [ ] **Step 1: Write the failing test**

`internal/install/hooks_test.go`:

```go
package install

import (
	"strings"
	"testing"
)

const bin = "/opt/cmgo/bin/cmgo"

func mustParse(t *testing.T, s string) *object {
	t.Helper()
	v, err := parseJSON([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v.(*object)
}

func TestHookEvents(t *testing.T) {
	post := "Bash|Read|Write|Edit|NotebookEdit|Glob|Grep|TodoWrite|TaskCreate|TaskUpdate|EnterPlanMode|ExitPlanMode|Skill|Agent|AskUserQuestion|EnterWorktree"
	want := []struct{ Event, Arg, Matcher string }{
		{"PreToolUse", "pretooluse", "Bash|WebFetch|Read|Grep|Agent"},
		{"PostToolUse", "posttooluse", post},
		{"PostToolUseFailure", "posttoolusefailure", post},
		{"UserPromptSubmit", "userpromptsubmit", ""},
		{"SessionStart", "sessionstart", ""},
		{"Stop", "stop", ""},
	}
	if len(HookEvents) != len(want) {
		t.Fatalf("len %d", len(HookEvents))
	}
	for i := range want {
		if HookEvents[i] != want[i] {
			t.Errorf("%d: %+v, want %+v", i, HookEvents[i], want[i])
		}
	}
}

func TestHookCommand(t *testing.T) {
	if got := hookCommand(bin, "stop"); got != `"/opt/cmgo/bin/cmgo" hook stop` {
		t.Fatalf("got %s", got)
	}
}

func TestIsOwnCommand(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{`"/opt/cmgo/bin/cmgo" hook pretooluse`, true},
		{`"/old place/cmgo" hook stop`, true},
		{`/usr/local/bin/cmgo hook sessionstart`, true},
		{`cmgo hook posttoolusefailure`, true},
		{`"/x/cmgo"   hook   stop  `, true},
		{`"/x/cmgo" hook stop extra`, false},
		{`"/x/cmgo" hook unknown`, false},
		{`"/x/cmgo" serve`, false},
		{`"/x/cmgo-dev" hook stop`, false},
		{`/x/cmgo/run.sh hook stop`, false},
		{`"/x/cmgo hook stop`, false},
		{`node "/p/hooks/pretooluse.mjs"`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := isOwnCommand(c.cmd); got != c.want {
			t.Errorf("%q: %v, want %v", c.cmd, got, c.want)
		}
	}
}

func TestAddHooksEmpty(t *testing.T) {
	root := newObject()
	if err := addHooks(root, bin); err != nil {
		t.Fatal(err)
	}
	got := string(encodeJSON(root))
	for _, he := range HookEvents {
		want := `"` + he.Event + `": [
      {
        "matcher": "` + he.Matcher + `",
        "hooks": [
          {
            "type": "command",
            "command": "\"/opt/cmgo/bin/cmgo\" hook ` + he.Arg + `"
          }
        ]
      }
    ]`
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s entry in\n%s", he.Event, got)
		}
	}
	hooks, _ := root.get("hooks")
	if strings.Join(hooks.(*object).keys, ",") != "PreToolUse,PostToolUse,PostToolUseFailure,UserPromptSubmit,SessionStart,Stop" {
		t.Fatalf("order %v", hooks.(*object).keys)
	}
}

func TestAddHooksBadShape(t *testing.T) {
	for _, s := range []string{`{"hooks":[]}`, `{"hooks":{"Stop":{}}}`} {
		if err := addHooks(mustParse(t, s), bin); err == nil {
			t.Errorf("%s: no error", s)
		}
	}
}

func TestStripHooks(t *testing.T) {
	root := mustParse(t, `{"hooks":{
		"PreToolUse":[
			{"matcher":"Bash","hooks":[{"type":"command","command":"user.sh"},{"type":"command","command":"\"/old/cmgo\" hook pretooluse"}]},
			{"matcher":"Read","hooks":[{"type":"command","command":"/old/cmgo hook pretooluse"}]}
		],
		"Notification":[{"matcher":"","hooks":[{"type":"command","command":"cmgo hook stop"}]}],
		"Stop":[],
		"Weird":[1,{"hooks":"x"},{"hooks":[2]}]
	},"other":true}`)
	if n := stripHooks(root); n != 3 {
		t.Fatalf("removed %d, want 3", n)
	}
	want := mustParse(t, `{"hooks":{
		"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"user.sh"}]}],
		"Stop":[],
		"Weird":[1,{"hooks":"x"},{"hooks":[2]}]
	},"other":true}`)
	if got, w := string(encodeJSON(root)), string(encodeJSON(want)); got != w {
		t.Fatalf("got\n%s\nwant\n%s", got, w)
	}
}

func TestStripHooksDropsEmptiedHooksKey(t *testing.T) {
	root := mustParse(t, `{"a":1,"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"cmgo hook stop"}]}]}}`)
	stripHooks(root)
	if got := string(encodeJSON(root)); got != "{\n  \"a\": 1\n}\n" {
		t.Fatalf("got %s", got)
	}
	// Nothing of ours: an empty hooks object stays.
	root = mustParse(t, `{"hooks":{}}`)
	if n := stripHooks(root); n != 0 {
		t.Fatalf("removed %d", n)
	}
	if _, ok := root.get("hooks"); !ok {
		t.Fatal("hooks removed")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/install/ -run 'TestHook|TestIsOwnCommand|TestAddHooks|TestStripHooks' -v`
Expected: FAIL with "undefined: HookEvents"

- [ ] **Step 3: Write minimal implementation**

`internal/install/hooks.go`:

```go
package install

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

const postMatcher = "Bash|Read|Write|Edit|NotebookEdit|Glob|Grep|TodoWrite|TaskCreate|TaskUpdate|EnterPlanMode|ExitPlanMode|Skill|Agent|AskUserQuestion|EnterWorktree"

// HookEvents lists (settings event name, cmgo hook arg, matcher) in spec §3.5 order.
var HookEvents = []struct{ Event, Arg, Matcher string }{
	{"PreToolUse", "pretooluse", "Bash|WebFetch|Read|Grep|Agent"},
	{"PostToolUse", "posttooluse", postMatcher},
	{"PostToolUseFailure", "posttoolusefailure", postMatcher},
	{"UserPromptSubmit", "userpromptsubmit", ""},
	{"SessionStart", "sessionstart", ""},
	{"Stop", "stop", ""},
}

// hookCommand quotes the binary so a path with spaces still runs.
func hookCommand(binary, arg string) string { return `"` + binary + `" hook ` + arg }

// isOwnCommand reports whether cmd is a cmgo hook command from any install
// location: first token (quoted or bare) with basename cmgo, then "hook <arg>".
func isOwnCommand(cmd string) bool {
	var exe, rest string
	if s, ok := strings.CutPrefix(cmd, `"`); ok {
		i := strings.IndexByte(s, '"')
		if i < 0 {
			return false
		}
		exe, rest = s[:i], s[i+1:]
	} else {
		exe, rest, _ = strings.Cut(cmd, " ")
	}
	f := strings.Fields(rest)
	return filepath.Base(exe) == "cmgo" && len(f) == 2 && f[0] == "hook" &&
		slices.ContainsFunc(HookEvents, func(h struct{ Event, Arg, Matcher string }) bool { return h.Arg == f[1] })
}

// stripHooks removes cmgo's inner commands under every event and returns how
// many it removed. Entries, event arrays and the hooks key are dropped only
// when this call emptied them, so user-made empty values survive. Values of an
// unexpected shape are left alone.
func stripHooks(root *object) int {
	hv, _ := root.get("hooks")
	hooks, ok := hv.(*object)
	if !ok {
		return 0
	}
	total := 0
	for _, ev := range slices.Clone(hooks.keys) {
		entries, ok := hooks.vals[ev].([]any)
		if !ok {
			continue
		}
		kept := []any{}
		for _, e := range entries {
			if n, empty := stripEntry(e); n > 0 {
				total += n
				if empty {
					continue
				}
			}
			kept = append(kept, e)
		}
		switch {
		case len(kept) == len(entries):
		case len(kept) == 0:
			hooks.del(ev)
		default:
			hooks.set(ev, kept)
		}
	}
	if total > 0 && len(hooks.keys) == 0 {
		root.del("hooks")
	}
	return total
}

// stripEntry removes cmgo's commands from one matcher entry and reports how
// many it removed and whether none are left.
func stripEntry(e any) (removed int, empty bool) {
	eo, ok := e.(*object)
	if !ok {
		return 0, false
	}
	inner, ok := eo.vals["hooks"].([]any)
	if !ok {
		return 0, false
	}
	kept := []any{}
	for _, h := range inner {
		if ho, ok := h.(*object); ok {
			if c, ok := ho.vals["command"].(string); ok && isOwnCommand(c) {
				continue
			}
		}
		kept = append(kept, h)
	}
	removed = len(inner) - len(kept)
	if removed > 0 {
		eo.set("hooks", kept)
	}
	return removed, len(kept) == 0
}

// addHooks appends one matcher entry per HookEvents row, creating the hooks
// object and event arrays as needed.
func addHooks(root *object, binary string) error {
	hv, ok := root.get("hooks")
	if !ok {
		hv = newObject()
		root.set("hooks", hv)
	}
	hooks, ok := hv.(*object)
	if !ok {
		return fmt.Errorf(`"hooks" is not an object`)
	}
	for _, he := range HookEvents {
		cmd := newObject()
		cmd.set("type", "command")
		cmd.set("command", hookCommand(binary, he.Arg))
		entry := newObject()
		entry.set("matcher", he.Matcher)
		entry.set("hooks", []any{cmd})
		ev, ok := hooks.get(he.Event)
		if !ok {
			hooks.set(he.Event, []any{entry})
			continue
		}
		arr, ok := ev.([]any)
		if !ok {
			return fmt.Errorf(`"hooks.%s" is not an array`, he.Event)
		}
		hooks.set(he.Event, append(arr, entry))
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/install/ -run 'TestHook|TestIsOwnCommand|TestAddHooks|TestStripHooks' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/install/hooks.go internal/install/hooks_test.go
git commit -m "feat(install): register cmgo hook entries and strip only cmgo's commands"
```

### Task 12: Install and Uninstall settings file: read, backup, atomic write, dry run, TS plugin

**Files:**
- Create: `internal/install/install.go`
- Test: `internal/install/install_test.go`

**Interfaces:**
- Consumes: `stripHooks`, `addHooks`, `hookCommand`, `bin`, `mustParse` (Task 11); `parseJSON`, `encodeJSON`, `newObject` (Task 10)
- Produces: `type Options struct{ SettingsPath, ClaudeJSONPath, Binary string; DisableTSPlugin, DryRun bool; Stdout, Stderr io.Writer; LookPath func(string) (string, error); Run func(name string, args ...string) error }`; `func Install(o Options) error`; `func Uninstall(o Options) error`; unexported `loadSettings(path string) (*settings, error)`, `(s *settings) save(o Options) error`, `writeAtomic(path string, b []byte, mode fs.FileMode) error`, `checkPlugins(root *object, o Options)`; test helpers `type fake`, `newOpts(t) (Options, *fake)`, `writeSettings`, `readFile`, `exists`, `freshHooks`, `const userSettings`

- [ ] **Step 1: Write the failing test**

`internal/install/install_test.go`:

```go
package install

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fake records what Install and Uninstall print and run.
type fake struct {
	stdout, stderr bytes.Buffer
	claude         bool     // LookPath("claude") succeeds
	runs           []string // each Run call, name and args joined by spaces
}

// newOpts returns options over a fresh temp dir whose settings dir does not exist yet.
func newOpts(t *testing.T) (Options, *fake) {
	t.Helper()
	dir := t.TempDir()
	f := &fake{}
	return Options{
		SettingsPath:   filepath.Join(dir, "claude", "settings.json"),
		ClaudeJSONPath: filepath.Join(dir, ".claude.json"),
		Binary:         bin,
		Stdout:         &f.stdout,
		Stderr:         &f.stderr,
		LookPath: func(name string) (string, error) {
			if f.claude && name == "claude" {
				return "/usr/local/bin/claude", nil
			}
			return "", exec.ErrNotFound
		},
		Run: func(name string, args ...string) error {
			f.runs = append(f.runs, strings.Join(append([]string{name}, args...), " "))
			return nil
		},
	}, f
}

func writeSettings(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// freshHooks is the file Install writes when it starts from {}.
func freshHooks(t *testing.T, binary string) string {
	t.Helper()
	root := newObject()
	if err := addHooks(root, binary); err != nil {
		t.Fatal(err)
	}
	return string(encodeJSON(root))
}

// commands returns, per matcher entry of event, the inner command strings.
func commands(t *testing.T, content, event string) [][]string {
	t.Helper()
	hooks, _ := mustParse(t, content).get("hooks")
	ev, _ := hooks.(*object).get(event)
	var out [][]string
	for _, e := range ev.([]any) {
		inner, _ := e.(*object).get("hooks")
		var cmds []string
		for _, h := range inner.([]any) {
			c, _ := h.(*object).get("command")
			cmds = append(cmds, c.(string))
		}
		out = append(out, cmds)
	}
	return out
}

const userSettings = `{
  "model": "opus",
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "user.sh"
          },
          {
            "type": "command",
            "command": "\"/old/cmgo\" hook pretooluse"
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "say done",
            "timeout": 5
          }
        ]
      }
    ]
  },
  "theme": "dark"
}
`

func TestInstallMissingFile(t *testing.T) {
	o, f := newOpts(t)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, o.SettingsPath), freshHooks(t, bin); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	fi, _ := os.Stat(o.SettingsPath)
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if exists(o.SettingsPath + ".cmgo-bak") {
		t.Fatal("backup of a missing file")
	}
	if !strings.Contains(f.stdout.String(), "wrote "+o.SettingsPath) {
		t.Fatalf("stdout %q", f.stdout.String())
	}
}

func TestInstallKeepsUserHooks(t *testing.T) {
	o, _ := newOpts(t)
	writeSettings(t, o.SettingsPath, userSettings, 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, o.SettingsPath)
	own := func(arg string) []string { return []string{hookCommand(bin, arg)} }
	if c := commands(t, got, "PreToolUse"); !slices.EqualFunc(c, [][]string{{"user.sh"}, own("pretooluse")}, slices.Equal) {
		t.Fatalf("PreToolUse %q", c)
	}
	if c := commands(t, got, "Stop"); !slices.EqualFunc(c, [][]string{{"say done"}, own("stop")}, slices.Equal) {
		t.Fatalf("Stop %q", c)
	}
	if c := commands(t, got, "PostToolUse"); !slices.EqualFunc(c, [][]string{own("posttooluse")}, slices.Equal) {
		t.Fatalf("PostToolUse %q", c)
	}
	root := mustParse(t, got)
	if strings.Join(root.keys, ",") != "model,hooks,theme" {
		t.Fatalf("keys %v", root.keys)
	}
	if !strings.Contains(got, `"timeout": 5`) || strings.Contains(got, "/old/") {
		t.Fatalf("got\n%s", got)
	}
	if readFile(t, o.SettingsPath+".cmgo-bak") != userSettings {
		t.Fatal("backup is not the original")
	}
}

func TestInstallReplacesOldPath(t *testing.T) {
	o, _ := newOpts(t)
	writeSettings(t, o.SettingsPath, freshHooks(t, "/old place/cmgo"), 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, o.SettingsPath), freshHooks(t, bin); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestInstallIdempotent(t *testing.T) {
	for name, orig := range map[string]string{"missing": "", "user hooks": userSettings} {
		t.Run(name, func(t *testing.T) {
			o, f := newOpts(t)
			if orig != "" {
				writeSettings(t, o.SettingsPath, orig, 0o644)
			}
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			first := readFile(t, o.SettingsPath)
			f.stdout.Reset()
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			if second := readFile(t, o.SettingsPath); second != first {
				t.Fatalf("second install changed the file:\n%s\nfirst:\n%s", second, first)
			}
			if !strings.Contains(f.stdout.String(), "is up to date") {
				t.Fatalf("stdout %q", f.stdout.String())
			}
			bak := o.SettingsPath + ".cmgo-bak"
			if orig == "" && exists(bak) {
				t.Fatal("backup created")
			}
			if orig != "" && readFile(t, bak) != orig {
				t.Fatal("backup overwritten")
			}
		})
	}
}

func TestInstallUnparseable(t *testing.T) {
	for _, bad := range []string{`{"a": 1,`, `[]`, `{"hooks": []}`, `{"hooks": {"Stop": {}}}`} {
		o, f := newOpts(t)
		writeSettings(t, o.SettingsPath, bad, 0o644)
		err := Install(o)
		if err == nil || !strings.Contains(err.Error(), o.SettingsPath) {
			t.Fatalf("%s: err %v", bad, err)
		}
		if readFile(t, o.SettingsPath) != bad || exists(o.SettingsPath+".cmgo-bak") || len(f.runs) != 0 {
			t.Fatalf("%s: wrote or ran something", bad)
		}
		if err := Uninstall(o); bad == `{"a": 1,` && err == nil {
			t.Fatal("uninstall parsed a broken file")
		}
	}
}

func TestInstallBlankFile(t *testing.T) {
	o, _ := newOpts(t)
	writeSettings(t, o.SettingsPath, " \n", 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if readFile(t, o.SettingsPath) != freshHooks(t, bin) {
		t.Fatal("blank file not treated as {}")
	}
}

func TestInstallBadBinary(t *testing.T) {
	for _, b := range []string{"cmgo", `/a"b/cmgo`} {
		o, _ := newOpts(t)
		o.Binary = b
		if err := Install(o); err == nil || exists(o.SettingsPath) {
			t.Fatalf("%s: err %v", b, err)
		}
	}
}

func TestUninstallRoundTrip(t *testing.T) {
	plainUser := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "user.sh"
          }
        ]
      }
    ],
    "Notification": []
  },
  "model": "opus"
}
`
	origs := map[string]string{
		"no hooks key": "{\n  \"model\": \"opus\",\n  \"n\": 1.50,\n  \"s\": \"<a> & b\"\n}\n",
		"user hooks":   plainUser,
	}
	for name, orig := range origs {
		t.Run(name, func(t *testing.T) {
			o, _ := newOpts(t)
			writeSettings(t, o.SettingsPath, orig, 0o644)
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			if err := Uninstall(o); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, o.SettingsPath); got != orig {
				t.Fatalf("got\n%s\nwant\n%s", got, orig)
			}
		})
	}
}

func TestUninstallNothingToRemove(t *testing.T) {
	o, f := newOpts(t)
	if err := Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if exists(o.SettingsPath) {
		t.Fatal("uninstall created a settings file")
	}
	// Four-space indent: nothing of ours, so no reformatting either.
	orig := "{\n    \"hooks\": {}\n}\n"
	writeSettings(t, o.SettingsPath, orig, 0o644)
	if err := Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if readFile(t, o.SettingsPath) != orig || exists(o.SettingsPath+".cmgo-bak") {
		t.Fatal("file rewritten")
	}
	if !strings.Contains(f.stdout.String(), "no cmgo hooks in") {
		t.Fatalf("stdout %q", f.stdout.String())
	}
}

func TestTSPlugin(t *testing.T) {
	orig := `{"enabledPlugins": {"other@x": true, "context-mode@context-mode": true, "context-mode@old": false}}`
	o, f := newOpts(t)
	writeSettings(t, o.SettingsPath, orig, 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.stderr.String(), "warning: plugin context-mode@context-mode is enabled") || strings.Contains(f.stderr.String(), "@old") {
		t.Fatalf("stderr %q", f.stderr.String())
	}
	if !strings.Contains(readFile(t, o.SettingsPath), `"context-mode@context-mode": true`) {
		t.Fatal("plugin changed without DisableTSPlugin")
	}

	o, f = newOpts(t)
	o.DisableTSPlugin = true
	writeSettings(t, o.SettingsPath, orig, 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, o.SettingsPath)
	if !strings.Contains(got, `"context-mode@context-mode": false`) || !strings.Contains(got, `"other@x": true`) {
		t.Fatalf("got\n%s", got)
	}
	if !strings.Contains(f.stderr.String(), "disabled plugin context-mode@context-mode") {
		t.Fatalf("stderr %q", f.stderr.String())
	}
}

func TestDryRun(t *testing.T) {
	for name, orig := range map[string]string{"missing": "", "user hooks": userSettings} {
		t.Run(name, func(t *testing.T) {
			o, f := newOpts(t)
			if orig != "" {
				writeSettings(t, o.SettingsPath, orig, 0o644)
			}
			o.DryRun = true
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			if orig == "" && exists(filepath.Dir(o.SettingsPath)) {
				t.Fatal("dry run created the settings dir")
			}
			if orig != "" {
				if readFile(t, o.SettingsPath) != orig {
					t.Fatal("dry run wrote the file")
				}
				if entries, _ := os.ReadDir(filepath.Dir(o.SettingsPath)); len(entries) != 1 {
					t.Fatalf("dry run left files: %v", entries)
				}
			}
			if len(f.runs) != 0 {
				t.Fatalf("ran %v", f.runs)
			}
			printed := f.stdout.String()
			o.DryRun, o.Stdout = false, &bytes.Buffer{}
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(printed, readFile(t, o.SettingsPath)) {
				t.Fatalf("dry run printed\n%s", printed)
			}
		})
	}
}

func TestModePreserved(t *testing.T) {
	o, _ := newOpts(t)
	writeSettings(t, o.SettingsPath, "{}", 0o600)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{o.SettingsPath, o.SettingsPath + ".cmgo-bak"} {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", p, fi.Mode())
		}
	}
}

func TestSymlinkedSettings(t *testing.T) {
	// Dotfile managers link settings.json; the link must survive.
	o, _ := newOpts(t)
	real := filepath.Join(t.TempDir(), "settings.json")
	writeSettings(t, real, "{}", 0o644)
	if err := os.MkdirAll(filepath.Dir(o.SettingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, o.SettingsPath); err != nil {
		t.Fatal(err)
	}
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(o.SettingsPath); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced by a file")
	}
	if readFile(t, real) != freshHooks(t, bin) || readFile(t, real+".cmgo-bak") != "{}" {
		t.Fatal("target not updated")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/install/ -count=1 -v`
Expected: FAIL with "undefined: Options"

- [ ] **Step 3: Write minimal implementation**

`internal/install/install.go`:

```go
// Package install registers cmgo's hooks in Claude Code's settings.json and
// its MCP server through the claude CLI (spec §5).
package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Options are the inputs of Install and Uninstall. The caller resolves every
// path and injects LookPath and Run so tests never run the real claude.
type Options struct {
	SettingsPath    string // resolved by the caller (CLAUDE_CONFIG_DIR or ~/.claude)
	ClaudeJSONPath  string // ~/.claude.json
	Binary          string // absolute path to cmgo
	DisableTSPlugin bool
	DryRun          bool
	Stdout, Stderr  io.Writer
	LookPath        func(string) (string, error) // exec.LookPath in production
	Run             func(name string, args ...string) error
}

// Install replaces cmgo's hook commands in the settings file with the
// HookEvents entries for o.Binary, keeping every other command.
func Install(o Options) error {
	if !filepath.IsAbs(o.Binary) || strings.Contains(o.Binary, `"`) {
		return fmt.Errorf("binary path must be absolute and have no double quote: %q", o.Binary)
	}
	s, err := loadSettings(o.SettingsPath)
	if err != nil {
		return err
	}
	stripHooks(s.root)
	if err := addHooks(s.root, o.Binary); err != nil {
		return fmt.Errorf("%s: %w", o.SettingsPath, err)
	}
	checkPlugins(s.root, o)
	return s.save(o)
}

// Uninstall removes cmgo's hook commands. It leaves enabledPlugins alone.
func Uninstall(o Options) error {
	s, err := loadSettings(o.SettingsPath)
	if err != nil {
		return err
	}
	// Nothing removed: do not create or reformat the user's file.
	if stripHooks(s.root) == 0 && !o.DryRun {
		fmt.Fprintf(o.Stdout, "cmgo: no cmgo hooks in %s\n", s.path)
		return nil
	}
	return s.save(o)
}

// checkPlugins warns about an enabled TS context-mode plugin, which would
// inject its routing text next to cmgo's, and turns it off when asked.
func checkPlugins(root *object, o Options) {
	pv, _ := root.get("enabledPlugins")
	p, ok := pv.(*object)
	if !ok {
		return
	}
	for _, k := range p.keys {
		if !strings.HasPrefix(k, "context-mode@") || p.vals[k] != true {
			continue
		}
		if o.DisableTSPlugin {
			p.set(k, false)
			fmt.Fprintf(o.Stderr, "cmgo: disabled plugin %s\n", k)
			continue
		}
		fmt.Fprintf(o.Stderr, "cmgo: warning: plugin %s is enabled; it and cmgo will both inject routing text (rerun with --disable-ts-plugin to turn it off)\n", k)
	}
}

// settings is a parsed settings file.
type settings struct {
	path    string // the real file, symlinks resolved
	existed bool
	orig    []byte
	mode    fs.FileMode
	root    *object
}

// loadSettings reads path; a missing or blank file is {}. A file that does
// not parse is an error, so nothing is ever written over it.
func loadSettings(path string) (*settings, error) {
	s := &settings{path: path, mode: 0o644, root: newObject()}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		s.path = real // write through a dotfile manager's link, not over it
	}
	fi, err := os.Stat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	s.existed, s.orig, s.mode = true, b, fi.Mode().Perm()
	if len(bytes.TrimSpace(b)) == 0 {
		return s, nil
	}
	v, err := parseJSON(b)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	root, ok := v.(*object)
	if !ok {
		return nil, fmt.Errorf("parse %s: not a JSON object", path)
	}
	s.root = root
	return s, nil
}

// save writes the edited file when its bytes changed, after keeping the
// original once as <file>.cmgo-bak. DryRun prints it instead.
func (s *settings) save(o Options) error {
	out := encodeJSON(s.root)
	if o.DryRun {
		_, err := o.Stdout.Write(out)
		return err
	}
	if s.existed && bytes.Equal(out, s.orig) {
		fmt.Fprintf(o.Stdout, "cmgo: %s is up to date\n", s.path)
		return nil
	}
	if s.existed {
		bak := s.path + ".cmgo-bak"
		if _, err := os.Lstat(bak); errors.Is(err, fs.ErrNotExist) {
			if err := writeAtomic(bak, s.orig, s.mode); err != nil {
				return err
			}
			fmt.Fprintf(o.Stdout, "cmgo: saved the original as %s\n", bak)
		} else if err != nil {
			return err
		}
	} else if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	if err := writeAtomic(s.path, out, s.mode); err != nil {
		return err
	}
	fmt.Fprintf(o.Stdout, "cmgo: wrote %s\n", s.path)
	return nil
}

// writeAtomic writes a temp file in the same dir and renames it over path, so
// a crash never leaves a half-written settings file.
func writeAtomic(path string, b []byte, mode fs.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) // fails harmlessly after the rename
	_, err = f.Write(b)
	if err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/install/ -count=1 -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/install/install.go internal/install/install_test.go
git commit -m "feat(install): edit settings.json atomically with backup, dry run and TS plugin check"
```

### Task 13: MCP registration through the claude CLI

**Files:**
- Create: `internal/install/mcp.go`
- Modify: `internal/install/install.go`
- Test: `internal/install/mcp_test.go`

**Interfaces:**
- Consumes: `Options`, `Install`, `Uninstall`, `newOpts`, `fake` (Task 12); `bin` (Task 11)
- Produces: unexported `func mcpRegistered(path string) bool`, `func registerMCP(o Options, add bool) error`, `func claude(o Options, args ...string) error`, `func shellQuote(s string) string`; `Install` and `Uninstall` now end with `registerMCP`

- [ ] **Step 1: Write the failing test**

`internal/install/mcp_test.go`:

```go
package install

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

const (
	addCmd    = "claude mcp add --scope user cmgo -- " + bin + " serve"
	removeCmd = "claude mcp remove --scope user cmgo"
)

func writeClaudeJSON(t *testing.T, o Options, content string) {
	t.Helper()
	if err := os.WriteFile(o.ClaudeJSONPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInstallMCP(t *testing.T) {
	cases := []struct {
		name, claudeJSON string // "" = no ~/.claude.json
		claude, dryRun   bool
		runs             []string
		printed          string
	}{
		{name: "no claude.json, claude on PATH", claude: true, runs: []string{addCmd}},
		{name: "unparseable claude.json", claudeJSON: `{"mcpServers":`, claude: true, runs: []string{addCmd}},
		{name: "other servers only", claudeJSON: `{"mcpServers":{"x":{}}}`, claude: true, runs: []string{addCmd}},
		{name: "claude not on PATH", printed: "run: " + addCmd + "\n"},
		{name: "dry run", claude: true, dryRun: true, printed: "run: " + addCmd + "\n"},
		{name: "registered", claudeJSON: `{"mcpServers":{"cmgo":{"command":"cmgo"}}}`, claude: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, f := newOpts(t)
			f.claude, o.DryRun = c.claude, c.dryRun
			if c.claudeJSON != "" {
				writeClaudeJSON(t, o, c.claudeJSON)
			}
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(f.runs, c.runs) {
				t.Fatalf("runs %q, want %q", f.runs, c.runs)
			}
			if got := strings.Contains(f.stdout.String(), "run: claude"); got != (c.printed != "") ||
				(c.printed != "" && !strings.HasSuffix(f.stdout.String(), c.printed)) {
				t.Fatalf("stdout %q, want suffix %q", f.stdout.String(), c.printed)
			}
		})
	}
}

func TestInstallMCPQuotesBinary(t *testing.T) {
	o, f := newOpts(t)
	o.Binary = "/Users/me/my bin/it's/cmgo"
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	want := `run: claude mcp add --scope user cmgo -- '/Users/me/my bin/it'\''s/cmgo' serve` + "\n"
	if !strings.HasSuffix(f.stdout.String(), want) {
		t.Fatalf("stdout %q", f.stdout.String())
	}
}

func TestInstallMCPRunError(t *testing.T) {
	o, _ := newOpts(t)
	o.LookPath = func(string) (string, error) { return "/bin/claude", nil }
	o.Run = func(string, ...string) error { return errors.New("exit status 1") }
	if err := Install(o); err == nil || !strings.Contains(err.Error(), "claude mcp add") {
		t.Fatalf("err %v", err)
	}
}

func TestUninstallMCP(t *testing.T) {
	cases := []struct {
		name, claudeJSON string
		claude, dryRun   bool
		runs             []string
		printed          bool
	}{
		{name: "registered, claude on PATH", claudeJSON: `{"mcpServers":{"cmgo":{}}}`, claude: true, runs: []string{removeCmd}},
		{name: "registered, no claude", claudeJSON: `{"mcpServers":{"cmgo":{}}}`, printed: true},
		{name: "registered, dry run", claudeJSON: `{"mcpServers":{"cmgo":{}}}`, claude: true, dryRun: true, printed: true},
		{name: "not registered", claudeJSON: `{"mcpServers":{}}`, claude: true},
		{name: "no claude.json", claude: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, f := newOpts(t)
			f.claude, o.DryRun = c.claude, c.dryRun
			if c.claudeJSON != "" {
				writeClaudeJSON(t, o, c.claudeJSON)
			}
			if err := Uninstall(o); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(f.runs, c.runs) {
				t.Fatalf("runs %q, want %q", f.runs, c.runs)
			}
			if got := strings.HasSuffix(f.stdout.String(), "run: "+removeCmd+"\n"); got != c.printed {
				t.Fatalf("stdout %q", f.stdout.String())
			}
		})
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/local/bin/cmgo": "/usr/local/bin/cmgo",
		"a b":                 "'a b'",
		"it's":                `'it'\''s'`,
		"":                    "''",
		"$HOME":               "'$HOME'",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("%q: %s, want %s", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/install/ -count=1 -race -v`
Expected: FAIL with "undefined: shellQuote"

- [ ] **Step 3: Write minimal implementation**

`internal/install/mcp.go`:

```go
package install

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// mcpRegistered reports whether ~/.claude.json has mcpServers.cmgo. A missing
// or unparseable file counts as not registered; cmgo never writes it, the
// claude CLI does.
func mcpRegistered(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var v struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	return json.Unmarshal(b, &v) == nil && v.MCPServers["cmgo"] != nil
}

// registerMCP adds (add) or removes the user-scope cmgo MCP server when that
// is needed.
func registerMCP(o Options, add bool) error {
	if mcpRegistered(o.ClaudeJSONPath) == add {
		return nil
	}
	args := []string{"mcp", "remove", "--scope", "user", "cmgo"}
	if add {
		args = []string{"mcp", "add", "--scope", "user", "cmgo", "--", o.Binary, "serve"}
	}
	return claude(o, args...)
}

// claude runs the claude CLI, or prints the command when it is not on PATH or
// in a dry run.
func claude(o Options, args ...string) error {
	if !o.DryRun {
		if _, err := o.LookPath("claude"); err == nil {
			if err := o.Run("claude", args...); err != nil {
				return fmt.Errorf("claude %s %s: %w", args[0], args[1], err)
			}
			return nil
		}
	}
	q := []string{"claude"}
	for _, a := range args {
		q = append(q, shellQuote(a))
	}
	_, err := fmt.Fprintf(o.Stdout, "run: %s\n", strings.Join(q, " "))
	return err
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote quotes s for a POSIX shell, leaving plain words as they are.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
```

`internal/install/install.go`:

```go
// Package install registers cmgo's hooks in Claude Code's settings.json and
// its MCP server through the claude CLI (spec §5).
package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Options are the inputs of Install and Uninstall. The caller resolves every
// path and injects LookPath and Run so tests never run the real claude.
type Options struct {
	SettingsPath    string // resolved by the caller (CLAUDE_CONFIG_DIR or ~/.claude)
	ClaudeJSONPath  string // ~/.claude.json
	Binary          string // absolute path to cmgo
	DisableTSPlugin bool
	DryRun          bool
	Stdout, Stderr  io.Writer
	LookPath        func(string) (string, error) // exec.LookPath in production
	Run             func(name string, args ...string) error
}

// Install replaces cmgo's hook commands in the settings file with the
// HookEvents entries for o.Binary, keeping every other command, then registers
// the MCP server.
func Install(o Options) error {
	if !filepath.IsAbs(o.Binary) || strings.Contains(o.Binary, `"`) {
		return fmt.Errorf("binary path must be absolute and have no double quote: %q", o.Binary)
	}
	s, err := loadSettings(o.SettingsPath)
	if err != nil {
		return err
	}
	stripHooks(s.root)
	if err := addHooks(s.root, o.Binary); err != nil {
		return fmt.Errorf("%s: %w", o.SettingsPath, err)
	}
	checkPlugins(s.root, o)
	if err := s.save(o); err != nil {
		return err
	}
	return registerMCP(o, true)
}

// Uninstall removes cmgo's hook commands and the MCP server. It leaves
// enabledPlugins alone.
func Uninstall(o Options) error {
	s, err := loadSettings(o.SettingsPath)
	if err != nil {
		return err
	}
	// Nothing removed: do not create or reformat the user's file.
	if stripHooks(s.root) == 0 && !o.DryRun {
		fmt.Fprintf(o.Stdout, "cmgo: no cmgo hooks in %s\n", s.path)
	} else if err := s.save(o); err != nil {
		return err
	}
	return registerMCP(o, false)
}

// checkPlugins warns about an enabled TS context-mode plugin, which would
// inject its routing text next to cmgo's, and turns it off when asked.
func checkPlugins(root *object, o Options) {
	pv, _ := root.get("enabledPlugins")
	p, ok := pv.(*object)
	if !ok {
		return
	}
	for _, k := range p.keys {
		if !strings.HasPrefix(k, "context-mode@") || p.vals[k] != true {
			continue
		}
		if o.DisableTSPlugin {
			p.set(k, false)
			fmt.Fprintf(o.Stderr, "cmgo: disabled plugin %s\n", k)
			continue
		}
		fmt.Fprintf(o.Stderr, "cmgo: warning: plugin %s is enabled; it and cmgo will both inject routing text (rerun with --disable-ts-plugin to turn it off)\n", k)
	}
}

// settings is a parsed settings file.
type settings struct {
	path    string // the real file, symlinks resolved
	existed bool
	orig    []byte
	mode    fs.FileMode
	root    *object
}

// loadSettings reads path; a missing or blank file is {}. A file that does
// not parse is an error, so nothing is ever written over it.
func loadSettings(path string) (*settings, error) {
	s := &settings{path: path, mode: 0o644, root: newObject()}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		s.path = real // write through a dotfile manager's link, not over it
	}
	fi, err := os.Stat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	s.existed, s.orig, s.mode = true, b, fi.Mode().Perm()
	if len(bytes.TrimSpace(b)) == 0 {
		return s, nil
	}
	v, err := parseJSON(b)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	root, ok := v.(*object)
	if !ok {
		return nil, fmt.Errorf("parse %s: not a JSON object", path)
	}
	s.root = root
	return s, nil
}

// save writes the edited file when its bytes changed, after keeping the
// original once as <file>.cmgo-bak. DryRun prints it instead.
func (s *settings) save(o Options) error {
	out := encodeJSON(s.root)
	if o.DryRun {
		_, err := o.Stdout.Write(out)
		return err
	}
	if s.existed && bytes.Equal(out, s.orig) {
		fmt.Fprintf(o.Stdout, "cmgo: %s is up to date\n", s.path)
		return nil
	}
	if s.existed {
		bak := s.path + ".cmgo-bak"
		if _, err := os.Lstat(bak); errors.Is(err, fs.ErrNotExist) {
			if err := writeAtomic(bak, s.orig, s.mode); err != nil {
				return err
			}
			fmt.Fprintf(o.Stdout, "cmgo: saved the original as %s\n", bak)
		} else if err != nil {
			return err
		}
	} else if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	if err := writeAtomic(s.path, out, s.mode); err != nil {
		return err
	}
	fmt.Fprintf(o.Stdout, "cmgo: wrote %s\n", s.path)
	return nil
}

// writeAtomic writes a temp file in the same dir and renames it over path, so
// a crash never leaves a half-written settings file.
func writeAtomic(path string, b []byte, mode fs.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) // fails harmlessly after the rename
	_, err = f.Write(b)
	if err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/install/ -count=1 -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/install/mcp.go internal/install/install.go internal/install/mcp_test.go
git commit -m "feat(install): register and remove the cmgo MCP server via claude mcp"
```

### Task 14: hook commands run cmgo-hook

**Files:**
- Modify: `internal/install/hooks.go`
- Modify: `internal/install/install.go`
- Test: `internal/install/hooks_test.go`
- Test: `internal/install/install_test.go`

**Interfaces:**
- Consumes: `Options`, `Install`, `addHooks`, `stripHooks`, `isOwnCommand`, `hookCommand` (Tasks Task 11 to Task 13)
- Produces: `Options.HookBinary string` (absolute path to cmgo-hook; "" means `filepath.Join(filepath.Dir(Binary), "cmgo-hook")`); `hookCommand(hookBinary, arg string) string` now returns `"<hookBinary>" <arg>`; `isOwnCommand` also matches `cmgo-hook <arg>`; `addHooks(root *object, hookBinary string) error`; test const `hookBin = "/opt/cmgo/bin/cmgo-hook"`

- [ ] **Step 1: Write the failing test**

`internal/install/hooks_test.go`:

```go
package install

import (
	"strings"
	"testing"
)

const (
	bin     = "/opt/cmgo/bin/cmgo"
	hookBin = "/opt/cmgo/bin/cmgo-hook" // the default next to bin
)

func mustParse(t *testing.T, s string) *object {
	t.Helper()
	v, err := parseJSON([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v.(*object)
}

func TestHookEvents(t *testing.T) {
	post := "Bash|Read|Write|Edit|NotebookEdit|Glob|Grep|TodoWrite|TaskCreate|TaskUpdate|EnterPlanMode|ExitPlanMode|Skill|Agent|AskUserQuestion|EnterWorktree"
	want := []struct{ Event, Arg, Matcher string }{
		{"PreToolUse", "pretooluse", "Bash|WebFetch|Read|Grep|Agent"},
		{"PostToolUse", "posttooluse", post},
		{"PostToolUseFailure", "posttoolusefailure", post},
		{"UserPromptSubmit", "userpromptsubmit", ""},
		{"SessionStart", "sessionstart", ""},
		{"Stop", "stop", ""},
	}
	if len(HookEvents) != len(want) {
		t.Fatalf("len %d", len(HookEvents))
	}
	for i := range want {
		if HookEvents[i] != want[i] {
			t.Errorf("%d: %+v, want %+v", i, HookEvents[i], want[i])
		}
	}
}

func TestHookCommand(t *testing.T) {
	if got := hookCommand(hookBin, "stop"); got != `"/opt/cmgo/bin/cmgo-hook" stop` {
		t.Fatalf("got %s", got)
	}
}

func TestIsOwnCommand(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{`"/opt/cmgo/bin/cmgo-hook" pretooluse`, true},
		{`"/old place/cmgo-hook" stop`, true},
		{`/usr/local/bin/cmgo-hook sessionstart`, true},
		{`cmgo-hook posttoolusefailure`, true},
		{`"/x/cmgo-hook" hook stop`, false},
		{`"/x/cmgo-hook" stop extra`, false},
		{`"/x/cmgo-hook" unknown`, false},
		{`"/x/cmgo" stop`, false},
		{`"/opt/cmgo/bin/cmgo" hook pretooluse`, true}, // the old form, from before cmgo-hook
		{`"/old place/cmgo" hook stop`, true},
		{`/usr/local/bin/cmgo hook sessionstart`, true},
		{`cmgo hook posttoolusefailure`, true},
		{`"/x/cmgo"   hook   stop  `, true},
		{`"/x/cmgo" hook stop extra`, false},
		{`"/x/cmgo" hook unknown`, false},
		{`"/x/cmgo" serve`, false},
		{`"/x/cmgo-dev" hook stop`, false},
		{`/x/cmgo/run.sh hook stop`, false},
		{`"/x/cmgo hook stop`, false},
		{`node "/p/hooks/pretooluse.mjs"`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := isOwnCommand(c.cmd); got != c.want {
			t.Errorf("%q: %v, want %v", c.cmd, got, c.want)
		}
	}
}

func TestAddHooksEmpty(t *testing.T) {
	root := newObject()
	if err := addHooks(root, hookBin); err != nil {
		t.Fatal(err)
	}
	got := string(encodeJSON(root))
	for _, he := range HookEvents {
		want := `"` + he.Event + `": [
      {
        "matcher": "` + he.Matcher + `",
        "hooks": [
          {
            "type": "command",
            "command": "\"/opt/cmgo/bin/cmgo-hook\" ` + he.Arg + `"
          }
        ]
      }
    ]`
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s entry in\n%s", he.Event, got)
		}
	}
	hooks, _ := root.get("hooks")
	if strings.Join(hooks.(*object).keys, ",") != "PreToolUse,PostToolUse,PostToolUseFailure,UserPromptSubmit,SessionStart,Stop" {
		t.Fatalf("order %v", hooks.(*object).keys)
	}
}

func TestAddHooksBadShape(t *testing.T) {
	for _, s := range []string{`{"hooks":[]}`, `{"hooks":{"Stop":{}}}`} {
		if err := addHooks(mustParse(t, s), hookBin); err == nil {
			t.Errorf("%s: no error", s)
		}
	}
}

func TestStripHooks(t *testing.T) {
	root := mustParse(t, `{"hooks":{
		"PreToolUse":[
			{"matcher":"Bash","hooks":[{"type":"command","command":"user.sh"},{"type":"command","command":"\"/old/cmgo\" hook pretooluse"}]},
			{"matcher":"Read","hooks":[{"type":"command","command":"/old/cmgo hook pretooluse"},{"type":"command","command":"\"/new/cmgo-hook\" pretooluse"}]}
		],
		"Notification":[{"matcher":"","hooks":[{"type":"command","command":"cmgo hook stop"}]}],
		"Stop":[],
		"Weird":[1,{"hooks":"x"},{"hooks":[2]}]
	},"other":true}`)
	if n := stripHooks(root); n != 4 {
		t.Fatalf("removed %d, want 4", n)
	}
	want := mustParse(t, `{"hooks":{
		"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"user.sh"}]}],
		"Stop":[],
		"Weird":[1,{"hooks":"x"},{"hooks":[2]}]
	},"other":true}`)
	if got, w := string(encodeJSON(root)), string(encodeJSON(want)); got != w {
		t.Fatalf("got\n%s\nwant\n%s", got, w)
	}
}

func TestStripHooksDropsEmptiedHooksKey(t *testing.T) {
	root := mustParse(t, `{"a":1,"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"cmgo hook stop"}]}]}}`)
	stripHooks(root)
	if got := string(encodeJSON(root)); got != "{\n  \"a\": 1\n}\n" {
		t.Fatalf("got %s", got)
	}
	// Nothing of ours: an empty hooks object stays.
	root = mustParse(t, `{"hooks":{}}`)
	if n := stripHooks(root); n != 0 {
		t.Fatalf("removed %d", n)
	}
	if _, ok := root.get("hooks"); !ok {
		t.Fatal("hooks removed")
	}
}
```

`internal/install/install_test.go`:

```go
package install

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fake records what Install and Uninstall print and run.
type fake struct {
	stdout, stderr bytes.Buffer
	claude         bool     // LookPath("claude") succeeds
	runs           []string // each Run call, name and args joined by spaces
}

// newOpts returns options over a fresh temp dir whose settings dir does not exist yet.
func newOpts(t *testing.T) (Options, *fake) {
	t.Helper()
	dir := t.TempDir()
	f := &fake{}
	return Options{
		SettingsPath:   filepath.Join(dir, "claude", "settings.json"),
		ClaudeJSONPath: filepath.Join(dir, ".claude.json"),
		Binary:         bin,
		Stdout:         &f.stdout,
		Stderr:         &f.stderr,
		LookPath: func(name string) (string, error) {
			if f.claude && name == "claude" {
				return "/usr/local/bin/claude", nil
			}
			return "", exec.ErrNotFound
		},
		Run: func(name string, args ...string) error {
			f.runs = append(f.runs, strings.Join(append([]string{name}, args...), " "))
			return nil
		},
	}, f
}

func writeSettings(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// freshHooks is the file Install writes when it starts from {}.
func freshHooks(t *testing.T, hookBinary string) string {
	t.Helper()
	root := newObject()
	if err := addHooks(root, hookBinary); err != nil {
		t.Fatal(err)
	}
	return string(encodeJSON(root))
}

// commands returns, per matcher entry of event, the inner command strings.
func commands(t *testing.T, content, event string) [][]string {
	t.Helper()
	hooks, _ := mustParse(t, content).get("hooks")
	ev, _ := hooks.(*object).get(event)
	var out [][]string
	for _, e := range ev.([]any) {
		inner, _ := e.(*object).get("hooks")
		var cmds []string
		for _, h := range inner.([]any) {
			c, _ := h.(*object).get("command")
			cmds = append(cmds, c.(string))
		}
		out = append(out, cmds)
	}
	return out
}

const userSettings = `{
  "model": "opus",
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "user.sh"
          },
          {
            "type": "command",
            "command": "\"/old/cmgo\" hook pretooluse"
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "say done",
            "timeout": 5
          }
        ]
      }
    ]
  },
  "theme": "dark"
}
`

func TestInstallMissingFile(t *testing.T) {
	o, f := newOpts(t)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, o.SettingsPath), freshHooks(t, hookBin); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	fi, _ := os.Stat(o.SettingsPath)
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if exists(o.SettingsPath + ".cmgo-bak") {
		t.Fatal("backup of a missing file")
	}
	if !strings.Contains(f.stdout.String(), "wrote "+o.SettingsPath) {
		t.Fatalf("stdout %q", f.stdout.String())
	}
}

func TestInstallKeepsUserHooks(t *testing.T) {
	o, _ := newOpts(t)
	writeSettings(t, o.SettingsPath, userSettings, 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, o.SettingsPath)
	own := func(arg string) []string { return []string{hookCommand(hookBin, arg)} }
	if c := commands(t, got, "PreToolUse"); !slices.EqualFunc(c, [][]string{{"user.sh"}, own("pretooluse")}, slices.Equal) {
		t.Fatalf("PreToolUse %q", c)
	}
	if c := commands(t, got, "Stop"); !slices.EqualFunc(c, [][]string{{"say done"}, own("stop")}, slices.Equal) {
		t.Fatalf("Stop %q", c)
	}
	if c := commands(t, got, "PostToolUse"); !slices.EqualFunc(c, [][]string{own("posttooluse")}, slices.Equal) {
		t.Fatalf("PostToolUse %q", c)
	}
	root := mustParse(t, got)
	if strings.Join(root.keys, ",") != "model,hooks,theme" {
		t.Fatalf("keys %v", root.keys)
	}
	if !strings.Contains(got, `"timeout": 5`) || strings.Contains(got, "/old/") {
		t.Fatalf("got\n%s", got)
	}
	if readFile(t, o.SettingsPath+".cmgo-bak") != userSettings {
		t.Fatal("backup is not the original")
	}
}

func TestInstallReplacesOldPath(t *testing.T) {
	o, _ := newOpts(t)
	writeSettings(t, o.SettingsPath, freshHooks(t, "/old place/cmgo-hook"), 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, o.SettingsPath), freshHooks(t, hookBin); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestInstallIdempotent(t *testing.T) {
	for name, orig := range map[string]string{"missing": "", "user hooks": userSettings} {
		t.Run(name, func(t *testing.T) {
			o, f := newOpts(t)
			if orig != "" {
				writeSettings(t, o.SettingsPath, orig, 0o644)
			}
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			first := readFile(t, o.SettingsPath)
			f.stdout.Reset()
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			if second := readFile(t, o.SettingsPath); second != first {
				t.Fatalf("second install changed the file:\n%s\nfirst:\n%s", second, first)
			}
			if !strings.Contains(f.stdout.String(), "is up to date") {
				t.Fatalf("stdout %q", f.stdout.String())
			}
			bak := o.SettingsPath + ".cmgo-bak"
			if orig == "" && exists(bak) {
				t.Fatal("backup created")
			}
			if orig != "" && readFile(t, bak) != orig {
				t.Fatal("backup overwritten")
			}
		})
	}
}

func TestInstallUnparseable(t *testing.T) {
	for _, bad := range []string{`{"a": 1,`, `[]`, `{"hooks": []}`, `{"hooks": {"Stop": {}}}`} {
		o, f := newOpts(t)
		writeSettings(t, o.SettingsPath, bad, 0o644)
		err := Install(o)
		if err == nil || !strings.Contains(err.Error(), o.SettingsPath) {
			t.Fatalf("%s: err %v", bad, err)
		}
		if readFile(t, o.SettingsPath) != bad || exists(o.SettingsPath+".cmgo-bak") || len(f.runs) != 0 {
			t.Fatalf("%s: wrote or ran something", bad)
		}
		if err := Uninstall(o); bad == `{"a": 1,` && err == nil {
			t.Fatal("uninstall parsed a broken file")
		}
	}
}

func TestInstallBlankFile(t *testing.T) {
	o, _ := newOpts(t)
	writeSettings(t, o.SettingsPath, " \n", 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if readFile(t, o.SettingsPath) != freshHooks(t, hookBin) {
		t.Fatal("blank file not treated as {}")
	}
}

func TestInstallBadBinary(t *testing.T) {
	for _, b := range []string{"cmgo", `/a"b/cmgo`} {
		o, _ := newOpts(t)
		o.Binary = b
		if err := Install(o); err == nil || exists(o.SettingsPath) {
			t.Fatalf("%s: err %v", b, err)
		}
		o.Binary, o.HookBinary = bin, b+"-hook"
		if err := Install(o); err == nil || exists(o.SettingsPath) {
			t.Fatalf("hook binary %s: err %v", o.HookBinary, err)
		}
	}
}

func TestInstallHookBinary(t *testing.T) {
	o, f := newOpts(t)
	o.HookBinary = "/elsewhere/cmgo-hook"
	f.claude = true
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, o.SettingsPath), freshHooks(t, o.HookBinary); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	// The MCP server is still the main binary.
	if len(f.runs) != 1 || f.runs[0] != "claude mcp add --scope user cmgo -- "+bin+" serve" {
		t.Fatalf("runs %q", f.runs)
	}
}

func TestUninstallRoundTrip(t *testing.T) {
	plainUser := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "user.sh"
          }
        ]
      }
    ],
    "Notification": []
  },
  "model": "opus"
}
`
	origs := map[string]string{
		"no hooks key": "{\n  \"model\": \"opus\",\n  \"n\": 1.50,\n  \"s\": \"<a> & b\"\n}\n",
		"user hooks":   plainUser,
	}
	for name, orig := range origs {
		t.Run(name, func(t *testing.T) {
			o, _ := newOpts(t)
			writeSettings(t, o.SettingsPath, orig, 0o644)
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			if err := Uninstall(o); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, o.SettingsPath); got != orig {
				t.Fatalf("got\n%s\nwant\n%s", got, orig)
			}
		})
	}
}

func TestUninstallNothingToRemove(t *testing.T) {
	o, f := newOpts(t)
	if err := Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if exists(o.SettingsPath) {
		t.Fatal("uninstall created a settings file")
	}
	// Four-space indent: nothing of ours, so no reformatting either.
	orig := "{\n    \"hooks\": {}\n}\n"
	writeSettings(t, o.SettingsPath, orig, 0o644)
	if err := Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if readFile(t, o.SettingsPath) != orig || exists(o.SettingsPath+".cmgo-bak") {
		t.Fatal("file rewritten")
	}
	if !strings.Contains(f.stdout.String(), "no cmgo hooks in") {
		t.Fatalf("stdout %q", f.stdout.String())
	}
}

func TestTSPlugin(t *testing.T) {
	orig := `{"enabledPlugins": {"other@x": true, "context-mode@context-mode": true, "context-mode@old": false}}`
	o, f := newOpts(t)
	writeSettings(t, o.SettingsPath, orig, 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.stderr.String(), "warning: plugin context-mode@context-mode is enabled") || strings.Contains(f.stderr.String(), "@old") {
		t.Fatalf("stderr %q", f.stderr.String())
	}
	if !strings.Contains(readFile(t, o.SettingsPath), `"context-mode@context-mode": true`) {
		t.Fatal("plugin changed without DisableTSPlugin")
	}

	o, f = newOpts(t)
	o.DisableTSPlugin = true
	writeSettings(t, o.SettingsPath, orig, 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, o.SettingsPath)
	if !strings.Contains(got, `"context-mode@context-mode": false`) || !strings.Contains(got, `"other@x": true`) {
		t.Fatalf("got\n%s", got)
	}
	if !strings.Contains(f.stderr.String(), "disabled plugin context-mode@context-mode") {
		t.Fatalf("stderr %q", f.stderr.String())
	}
}

func TestDryRun(t *testing.T) {
	for name, orig := range map[string]string{"missing": "", "user hooks": userSettings} {
		t.Run(name, func(t *testing.T) {
			o, f := newOpts(t)
			if orig != "" {
				writeSettings(t, o.SettingsPath, orig, 0o644)
			}
			o.DryRun = true
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			if orig == "" && exists(filepath.Dir(o.SettingsPath)) {
				t.Fatal("dry run created the settings dir")
			}
			if orig != "" {
				if readFile(t, o.SettingsPath) != orig {
					t.Fatal("dry run wrote the file")
				}
				if entries, _ := os.ReadDir(filepath.Dir(o.SettingsPath)); len(entries) != 1 {
					t.Fatalf("dry run left files: %v", entries)
				}
			}
			if len(f.runs) != 0 {
				t.Fatalf("ran %v", f.runs)
			}
			printed := f.stdout.String()
			o.DryRun, o.Stdout = false, &bytes.Buffer{}
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(printed, readFile(t, o.SettingsPath)) {
				t.Fatalf("dry run printed\n%s", printed)
			}
		})
	}
}

func TestModePreserved(t *testing.T) {
	o, _ := newOpts(t)
	writeSettings(t, o.SettingsPath, "{}", 0o600)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{o.SettingsPath, o.SettingsPath + ".cmgo-bak"} {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", p, fi.Mode())
		}
	}
}

func TestSymlinkedSettings(t *testing.T) {
	// Dotfile managers link settings.json; the link must survive.
	o, _ := newOpts(t)
	real := filepath.Join(t.TempDir(), "settings.json")
	writeSettings(t, real, "{}", 0o644)
	if err := os.MkdirAll(filepath.Dir(o.SettingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, o.SettingsPath); err != nil {
		t.Fatal(err)
	}
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(o.SettingsPath); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced by a file")
	}
	if readFile(t, real) != freshHooks(t, hookBin) || readFile(t, real+".cmgo-bak") != "{}" {
		t.Fatal("target not updated")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/install/ -count=1 -v`
Expected: FAIL with "internal/install/install_test.go:258:15: o.HookBinary undefined (type Options has no field or method HookBinary)"

- [ ] **Step 3: Write minimal implementation**

`internal/install/hooks.go`:

```go
package install

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

const postMatcher = "Bash|Read|Write|Edit|NotebookEdit|Glob|Grep|TodoWrite|TaskCreate|TaskUpdate|EnterPlanMode|ExitPlanMode|Skill|Agent|AskUserQuestion|EnterWorktree"

// HookEvents lists (settings event name, cmgo hook arg, matcher) in spec §3.5 order.
var HookEvents = []struct{ Event, Arg, Matcher string }{
	{"PreToolUse", "pretooluse", "Bash|WebFetch|Read|Grep|Agent"},
	{"PostToolUse", "posttooluse", postMatcher},
	{"PostToolUseFailure", "posttoolusefailure", postMatcher},
	{"UserPromptSubmit", "userpromptsubmit", ""},
	{"SessionStart", "sessionstart", ""},
	{"Stop", "stop", ""},
}

// hookCommand quotes the hook binary so a path with spaces still runs.
func hookCommand(hookBinary, arg string) string { return `"` + hookBinary + `" ` + arg }

// isOwnCommand reports whether cmd is a cmgo hook command from any install
// location: first token (quoted or bare) with basename cmgo-hook, then
// "<arg>"; or the form from before cmgo-hook existed, basename cmgo, then
// "hook <arg>". Both match so a reinstall replaces either.
func isOwnCommand(cmd string) bool {
	var exe, rest string
	if s, ok := strings.CutPrefix(cmd, `"`); ok {
		i := strings.IndexByte(s, '"')
		if i < 0 {
			return false
		}
		exe, rest = s[:i], s[i+1:]
	} else {
		exe, rest, _ = strings.Cut(cmd, " ")
	}
	f := strings.Fields(rest)
	var arg string
	switch filepath.Base(exe) {
	case "cmgo-hook":
		if len(f) == 1 {
			arg = f[0]
		}
	case "cmgo":
		if len(f) == 2 && f[0] == "hook" {
			arg = f[1]
		}
	}
	return arg != "" && slices.ContainsFunc(HookEvents, func(h struct{ Event, Arg, Matcher string }) bool { return h.Arg == arg })
}

// stripHooks removes cmgo's inner commands under every event and returns how
// many it removed. Entries, event arrays and the hooks key are dropped only
// when this call emptied them, so user-made empty values survive. Values of an
// unexpected shape are left alone.
func stripHooks(root *object) int {
	hv, _ := root.get("hooks")
	hooks, ok := hv.(*object)
	if !ok {
		return 0
	}
	total := 0
	for _, ev := range slices.Clone(hooks.keys) {
		entries, ok := hooks.vals[ev].([]any)
		if !ok {
			continue
		}
		kept := []any{}
		for _, e := range entries {
			if n, empty := stripEntry(e); n > 0 {
				total += n
				if empty {
					continue
				}
			}
			kept = append(kept, e)
		}
		switch {
		case len(kept) == len(entries):
		case len(kept) == 0:
			hooks.del(ev)
		default:
			hooks.set(ev, kept)
		}
	}
	if total > 0 && len(hooks.keys) == 0 {
		root.del("hooks")
	}
	return total
}

// stripEntry removes cmgo's commands from one matcher entry and reports how
// many it removed and whether none are left.
func stripEntry(e any) (removed int, empty bool) {
	eo, ok := e.(*object)
	if !ok {
		return 0, false
	}
	inner, ok := eo.vals["hooks"].([]any)
	if !ok {
		return 0, false
	}
	kept := []any{}
	for _, h := range inner {
		if ho, ok := h.(*object); ok {
			if c, ok := ho.vals["command"].(string); ok && isOwnCommand(c) {
				continue
			}
		}
		kept = append(kept, h)
	}
	removed = len(inner) - len(kept)
	if removed > 0 {
		eo.set("hooks", kept)
	}
	return removed, len(kept) == 0
}

// addHooks appends one matcher entry per HookEvents row, creating the hooks
// object and event arrays as needed.
func addHooks(root *object, hookBinary string) error {
	hv, ok := root.get("hooks")
	if !ok {
		hv = newObject()
		root.set("hooks", hv)
	}
	hooks, ok := hv.(*object)
	if !ok {
		return fmt.Errorf(`"hooks" is not an object`)
	}
	for _, he := range HookEvents {
		cmd := newObject()
		cmd.set("type", "command")
		cmd.set("command", hookCommand(hookBinary, he.Arg))
		entry := newObject()
		entry.set("matcher", he.Matcher)
		entry.set("hooks", []any{cmd})
		ev, ok := hooks.get(he.Event)
		if !ok {
			hooks.set(he.Event, []any{entry})
			continue
		}
		arr, ok := ev.([]any)
		if !ok {
			return fmt.Errorf(`"hooks.%s" is not an array`, he.Event)
		}
		hooks.set(he.Event, append(arr, entry))
	}
	return nil
}
```

`internal/install/install.go`:

```go
// Package install registers cmgo's hooks in Claude Code's settings.json and
// its MCP server through the claude CLI (spec §5).
package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Options are the inputs of Install and Uninstall. The caller resolves every
// path and injects LookPath and Run so tests never run the real claude.
type Options struct {
	SettingsPath    string // resolved by the caller (CLAUDE_CONFIG_DIR or ~/.claude)
	ClaudeJSONPath  string // ~/.claude.json
	Binary          string // absolute path to cmgo
	HookBinary      string // absolute path to cmgo-hook; "" means cmgo-hook next to Binary
	DisableTSPlugin bool
	DryRun          bool
	Stdout, Stderr  io.Writer
	LookPath        func(string) (string, error) // exec.LookPath in production
	Run             func(name string, args ...string) error
}

// Install replaces cmgo's hook commands in the settings file with the
// HookEvents entries for o.HookBinary, keeping every other command, then
// registers o.Binary as the MCP server.
func Install(o Options) error {
	if o.HookBinary == "" {
		o.HookBinary = filepath.Join(filepath.Dir(o.Binary), "cmgo-hook")
	}
	for _, b := range []string{o.Binary, o.HookBinary} {
		if !filepath.IsAbs(b) || strings.Contains(b, `"`) {
			return fmt.Errorf("binary path must be absolute and have no double quote: %q", b)
		}
	}
	s, err := loadSettings(o.SettingsPath)
	if err != nil {
		return err
	}
	stripHooks(s.root)
	if err := addHooks(s.root, o.HookBinary); err != nil {
		return fmt.Errorf("%s: %w", o.SettingsPath, err)
	}
	checkPlugins(s.root, o)
	if err := s.save(o); err != nil {
		return err
	}
	return registerMCP(o, true)
}

// Uninstall removes cmgo's hook commands and the MCP server. It leaves
// enabledPlugins alone.
func Uninstall(o Options) error {
	s, err := loadSettings(o.SettingsPath)
	if err != nil {
		return err
	}
	// Nothing removed: do not create or reformat the user's file.
	if stripHooks(s.root) == 0 && !o.DryRun {
		fmt.Fprintf(o.Stdout, "cmgo: no cmgo hooks in %s\n", s.path)
	} else if err := s.save(o); err != nil {
		return err
	}
	return registerMCP(o, false)
}

// checkPlugins warns about an enabled TS context-mode plugin, which would
// inject its routing text next to cmgo's, and turns it off when asked.
func checkPlugins(root *object, o Options) {
	pv, _ := root.get("enabledPlugins")
	p, ok := pv.(*object)
	if !ok {
		return
	}
	for _, k := range p.keys {
		if !strings.HasPrefix(k, "context-mode@") || p.vals[k] != true {
			continue
		}
		if o.DisableTSPlugin {
			p.set(k, false)
			fmt.Fprintf(o.Stderr, "cmgo: disabled plugin %s\n", k)
			continue
		}
		fmt.Fprintf(o.Stderr, "cmgo: warning: plugin %s is enabled; it and cmgo will both inject routing text (rerun with --disable-ts-plugin to turn it off)\n", k)
	}
}

// settings is a parsed settings file.
type settings struct {
	path    string // the real file, symlinks resolved
	existed bool
	orig    []byte
	mode    fs.FileMode
	root    *object
}

// loadSettings reads path; a missing or blank file is {}. A file that does
// not parse is an error, so nothing is ever written over it.
func loadSettings(path string) (*settings, error) {
	s := &settings{path: path, mode: 0o644, root: newObject()}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		s.path = real // write through a dotfile manager's link, not over it
	}
	fi, err := os.Stat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	s.existed, s.orig, s.mode = true, b, fi.Mode().Perm()
	if len(bytes.TrimSpace(b)) == 0 {
		return s, nil
	}
	v, err := parseJSON(b)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	root, ok := v.(*object)
	if !ok {
		return nil, fmt.Errorf("parse %s: not a JSON object", path)
	}
	s.root = root
	return s, nil
}

// save writes the edited file when its bytes changed, after keeping the
// original once as <file>.cmgo-bak. DryRun prints it instead.
func (s *settings) save(o Options) error {
	out := encodeJSON(s.root)
	if o.DryRun {
		_, err := o.Stdout.Write(out)
		return err
	}
	if s.existed && bytes.Equal(out, s.orig) {
		fmt.Fprintf(o.Stdout, "cmgo: %s is up to date\n", s.path)
		return nil
	}
	if s.existed {
		bak := s.path + ".cmgo-bak"
		if _, err := os.Lstat(bak); errors.Is(err, fs.ErrNotExist) {
			if err := writeAtomic(bak, s.orig, s.mode); err != nil {
				return err
			}
			fmt.Fprintf(o.Stdout, "cmgo: saved the original as %s\n", bak)
		} else if err != nil {
			return err
		}
	} else if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	if err := writeAtomic(s.path, out, s.mode); err != nil {
		return err
	}
	fmt.Fprintf(o.Stdout, "cmgo: wrote %s\n", s.path)
	return nil
}

// writeAtomic writes a temp file in the same dir and renames it over path, so
// a crash never leaves a half-written settings file.
func writeAtomic(path string, b []byte, mode fs.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) // fails harmlessly after the rename
	_, err = f.Write(b)
	if err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/install/ -count=1 -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/install/hooks.go internal/install/install.go internal/install/hooks_test.go internal/install/install_test.go
git commit -m "feat(install): register cmgo-hook for the hook commands"
```

---

### Task 15: JS whitespace, regex sources and parseInt

**Files:**
- Create: `internal/routing/jsspace.go`
- Test: `internal/routing/jsspace_test.go`

**Interfaces:**
- Consumes: none (stdlib only)
- Produces: unexported `const jsSpaceClass string`; `func isJSSpace(r rune) bool`; `func jsTrim(s string) string`; `func jsRegexp(src string) *regexp.Regexp`; `func jsParseInt(s string) (n int64, ok bool)`

- [ ] **Step 1: Write the failing test**

`internal/routing/jsspace_test.go`:
```go
package routing

import (
	"math"
	"testing"
)

func TestIsJSSpace(t *testing.T) {
	// Expected values from node: /\s/.test(c) for each code point.
	for _, r := range []rune{'\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2000, 0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff} {
		if !isJSSpace(r) {
			t.Errorf("isJSSpace(%U) = false, want true", r)
		}
	}
	for _, r := range []rune{'a', '-', 0x85, 0x180e, 0x200b, 0} {
		if isJSSpace(r) {
			t.Errorf("isJSSpace(%U) = true, want false", r)
		}
	}
}

func TestJSTrim(t *testing.T) {
	cases := map[string]string{
		"  pwd  ":            "pwd",
		"\u00a0pwd\ufeff":    "pwd",
		"\u2028git status\v": "git status",
		"\u0085pwd":          "\u0085pwd", // NEL is not JS whitespace
		"":                   "",
		" \t ":               "",
	}
	for in, want := range cases {
		if got := jsTrim(in); got != want {
			t.Errorf("jsTrim(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJSRegexpSpace(t *testing.T) {
	re := jsRegexp(`^a\s+b\S$`)
	for _, s := range []string{"a bc", "a\u00a0b-", "a\u3000\tbx"} {
		if !re.MatchString(s) {
			t.Errorf("%q should match", s)
		}
	}
	for _, s := range []string{"a\u0085bc", "a b ", "abc"} {
		if re.MatchString(s) {
			t.Errorf("%q should not match", s)
		}
	}
}

func TestJSParseInt(t *testing.T) {
	// Expected values from node: parseInt(s, 10).
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"42", 42, true},
		{"  7", 7, true},
		{"\u00a07", 7, true},
		{"\ufeff8", 8, true},
		{"10abc", 10, true},
		{"-5", -5, true},
		{"+3", 3, true},
		{"0x10", 0, true},
		{"3.9", 3, true},
		{"abc", 0, false},
		{"", 0, false},
		{"  ", 0, false},
		{"-", 0, false},
		{"\u0085 9", 0, false},
		{"99999999999999999999", math.MaxInt64, true},
	}
	for _, c := range cases {
		got, ok := jsParseInt(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("jsParseInt(%q) = %d, %v; want %d, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/routing/ -run 'TestIsJSSpace|TestJSTrim|TestJSRegexpSpace|TestJSParseInt' -v`
Expected: FAIL with "./jsspace_test.go:11:7: undefined: isJSSpace"

- [ ] **Step 3: Write minimal implementation**

`internal/routing/jsspace.go`:
```go
// Package routing ports the TS PreToolUse decision tree (hooks/core/routing.mjs),
// its Claude Code formatter (hooks/core/formatters.mjs) and the routing block
// text (hooks/routing-block.mjs), with cmgo tool names.
package routing

import (
	"math"
	"regexp"
	"strings"
)

// jsSpaceClass is the body of a character class equal to JS \s (WhiteSpace
// plus LineTerminator). Go's \s is only [\t\n\f\r ].
const jsSpaceClass = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// isJSSpace reports whether r is in JS \s, the set String.prototype.trim strips.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsTrim is JS s.trim().
func jsTrim(s string) string {
	return strings.TrimFunc(s, isJSSpace)
}

// jsRegexp compiles a TS regex source after rewriting \s and \S to the JS
// classes. The sources it is used on have no \s inside brackets and no
// escaped backslash before an s.
func jsRegexp(src string) *regexp.Regexp {
	src = strings.ReplaceAll(src, `\s`, "["+jsSpaceClass+"]")
	src = strings.ReplaceAll(src, `\S`, "[^"+jsSpaceClass+"]")
	return regexp.MustCompile(src)
}

// jsParseInt is JS parseInt(s, 10): leading JS whitespace, an optional sign,
// then the longest digit run; ok is false where JS gives NaN. Values past
// int64 saturate (JS would keep a float, which every caller rejects anyway).
func jsParseInt(s string) (n int64, ok bool) {
	s = strings.TrimLeftFunc(s, isJSSpace)
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	for i := 0; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		ok = true
		d := int64(s[i] - '0')
		if n > (math.MaxInt64-d)/10 {
			n = math.MaxInt64
		} else if n != math.MaxInt64 {
			n = n*10 + d
		}
	}
	if neg {
		n = -n
	}
	return n, ok
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/routing/ -run 'TestIsJSSpace|TestJSTrim|TestJSRegexpSpace|TestJSParseInt' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/routing/jsspace.go internal/routing/jsspace_test.go
git commit -m "feat(routing): JS whitespace, regex and parseInt helpers"
```

### Task 16: Heredoc and quote stripping, structurally bounded commands

**Files:**
- Create: `internal/routing/shell.go`
- Test: `internal/routing/shell_test.go`

**Interfaces:**
- Consumes: `isJSSpace`, `jsTrim`, `jsRegexp` from Task 15
- Produces: unexported `func stripHeredocs(cmd string) string`; `func stripQuotedContent(cmd string) string`; `func hasShellControl(s string) bool`; `func isStructurallyBounded(command string) bool`; helpers `heredocAt`, `skipJSSpace(s string, i int) int`, `isWordByte(c byte) bool`, `type safePattern`, `var safeCommands []safePattern`

- [ ] **Step 1: Write the failing test**

`internal/routing/shell_test.go`:
```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/routing/ -run 'TestStripHeredocs|TestStripQuotedContent|TestIsStructurallyBounded|TestHasShellControl' -v`
Expected: FAIL with "./shell_test.go:22:13: undefined: stripHeredocs"

- [ ] **Step 3: Write minimal implementation**

`internal/routing/shell.go`:
```go
package routing

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// stripHeredocs ports routing.mjs:228-230,
// cmd.replace(/<<-?\s*["']?(\w+)["']?[\s\S]*?\n\s*\1/g, ""). RE2 has no
// backreference, so this walks the same backtracking order by hand: at each
// "<<", the word is tried longest first and, for each length, the earliest
// newline whose following spaces lead to that word. The other choice points
// (-?, \s*, the quotes) cannot change the outcome: giving one back leaves a
// character the next step cannot match.
func stripHeredocs(cmd string) string {
	var out strings.Builder
	last := 0
	for i := 0; i+1 < len(cmd); {
		if cmd[i] != '<' || cmd[i+1] != '<' {
			i++
			continue
		}
		end, ok := heredocAt(cmd, i)
		if !ok {
			i++
			continue
		}
		out.WriteString(cmd[last:i])
		last, i = end, end
	}
	if last == 0 {
		return cmd
	}
	out.WriteString(cmd[last:])
	return out.String()
}

// heredocAt matches the heredoc regex at cmd[i:], which starts with "<<",
// and returns the end of the match.
func heredocAt(cmd string, i int) (int, bool) {
	p := i + 2
	if p < len(cmd) && cmd[p] == '-' {
		p++
	}
	p = skipJSSpace(cmd, p)
	if p < len(cmd) && (cmd[p] == '"' || cmd[p] == '\'') {
		p++
	}
	wordEnd := p
	for wordEnd < len(cmd) && isWordByte(cmd[wordEnd]) {
		wordEnd++
	}
	for we := wordEnd; we > p; we-- {
		word := cmd[p:we]
		for nl := we; nl < len(cmd); nl++ {
			if cmd[nl] != '\n' {
				continue
			}
			if q := skipJSSpace(cmd, nl+1); strings.HasPrefix(cmd[q:], word) {
				return q + len(word), true
			}
		}
	}
	return 0, false
}

// skipJSSpace returns the index after the run of JS whitespace at s[i:].
func skipJSSpace(s string, i int) int {
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		if !isJSSpace(r) {
			break
		}
		i += n
	}
	return i
}

// isWordByte is JS \w (ASCII only, no u flag).
func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

var (
	singleQuoted = regexp.MustCompile(`'[^']*'`)
	doubleQuoted = regexp.MustCompile(`"[^"]*"`)
)

// stripQuotedContent ports routing.mjs:237-241: heredocs removed, then quoted
// strings emptied, so the curl and build-tool tests see only command tokens.
func stripQuotedContent(cmd string) string {
	s := singleQuoted.ReplaceAllLiteralString(stripHeredocs(cmd), "''")
	return doubleQuoted.ReplaceAllLiteralString(s, `""`)
}

// hasShellControl ports SHELL_CONTROL_OPERATORS (routing.mjs:339),
// /[|`\n\r]|\$\(|>>|>|<(?!<)|&(?!&)|&&|\|\||;/. The last < or & of a string
// is never followed by another, so the lookaheads always find a match when
// the character occurs at all.
func hasShellControl(s string) bool {
	return strings.ContainsAny(s, "|`\n\r>;<&") || strings.Contains(s, "$(")
}

// safePattern is one SAFE_COMMAND_PATTERNS entry: re must match and none of
// not may. The TS negative lookaheads all sit right after the command name
// at ^, so each becomes a prefix regex in not.
type safePattern struct {
	re  *regexp.Regexp
	not []*regexp.Regexp
}

func safe(src string, not ...string) safePattern {
	p := safePattern{re: jsRegexp(src)}
	for _, n := range not {
		p.not = append(p.not, jsRegexp(n))
	}
	return p
}

// noVerbose builds the mv/cp/rm/ln entry,
// /^mv(?!\s+-[a-zA-Z]*v[a-zA-Z]*)(?!\s+--verbose\b)\s+[^\r\n]+$/. Only the
// first flag token is checked, as in TS (mv -f -v a b is bounded).
func noVerbose(cmd string) safePattern {
	return safe(`^`+cmd+`\s+[^\r\n]+$`, `^`+cmd+`\s+-[a-zA-Z]*v`, `^`+cmd+`\s+--verbose\b`)
}

// safeCommands ports SAFE_COMMAND_PATTERNS (routing.mjs:260-324) in order.
var safeCommands = []safePattern{
	safe(`^pwd$`),
	safe(`^whoami$`),
	safe(`^hostname(?:\s+-[a-zA-Z]+)?$`),
	safe(`^uname(?:\s+-[a-zA-Z]+)?$`),
	safe(`^id(?:\s+\S+)?$`),
	safe(`^date(?:\s+[^\r\n]+)?$`),
	safe(`^echo\s`),
	safe(`^printf\s`),
	safe(`^which\s+\S+(?:\s+\S+)*$`),
	safe(`^type\s+\S+(?:\s+\S+)*$`),
	safe(`^command\s+-v\s+\S+(?:\s+\S+)*$`),
	safe(`^readlink(?:\s+[^\r\n]+)?$`),
	safe(`^basename(?:\s+[^\r\n]+)?$`),
	safe(`^dirname(?:\s+[^\r\n]+)?$`),
	safe(`^realpath(?:\s+[^\r\n]+)?$`),
	safe(`^cd(?:\s+[^\r\n]+)?$`),
	safe(`^mkdir(?:\s+[^\r\n]+)?$`),
	safe(`^touch\s+[^\r\n]+$`),
	noVerbose("mv"),
	noVerbose("cp"),
	noVerbose("rm"),
	noVerbose("ln"),
	safe(`^ls(?:\s+[^\r\n]+)?$`, `^ls\s+-[a-zA-Z]*R`, `^ls\s+--recursive`),
	safe(`^git\s+status(?:\s+[^\r\n]+)?$`),
	safe(`^git\s+rev-parse(?:\s+[^\r\n]+)?$`),
	safe(`^git\s+remote(?:\s+-v|\s+show\s+\S+)?$`),
	safe(`^git\s+branch(?:\s+[^\r\n]+)?$`),
	safe(`^git\s+config\s+--get(?:\s+[^\r\n]+)?$`),
	safe(`^git\s+diff\s+--stat(?:\s+[^\r\n]+)?$`),
	safe(`^git\s+diff\s+--name-only(?:\s+[^\r\n]+)?$`),
	safe(`^git\s+stash\s+list$`),
	safe(`^git\s+tag(?:\s+-l(?:\s+[^\r\n]+)?)?$`),
	safe(`^git\s+log\s+-\d{1,2}(?:\s+[^\r\n]+)?$`),
	safe(`(?:^|\s)--version(?:\s|$)`),
	safe(`^\S+\s+-V(?:\s|$)`),
}

// isStructurallyBounded ports routing.mjs:347-352: a command with no shell
// control operator that matches a safe pattern gets no Bash guidance.
func isStructurallyBounded(command string) bool {
	if command == "" {
		return false
	}
	s := jsTrim(command)
	if hasShellControl(s) {
		return false
	}
	for _, p := range safeCommands {
		if p.matches(s) {
			return true
		}
	}
	return false
}

func (p safePattern) matches(s string) bool {
	if !p.re.MatchString(s) {
		return false
	}
	for _, n := range p.not {
		if n.MatchString(s) {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/routing/ -run 'TestStripHeredocs|TestStripQuotedContent|TestIsStructurallyBounded|TestHasShellControl' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/routing/shell.go internal/routing/shell_test.go
git commit -m "feat(routing): heredoc stripping and bounded-command allowlist"
```

### Task 17: curl/wget, inline-HTTP and build-tool matchers

**Files:**
- Create: `internal/routing/redirect.go`
- Test: `internal/routing/redirect_test.go`

**Interfaces:**
- Consumes: `jsRegexp`, `jsTrim` from Task 15; `stripHeredocs`, `stripQuotedContent` from Task 16
- Produces: unexported `func curlCheck(stripped string) (found, dangerous bool)`; `func inlineHTTP(noHeredoc string) bool`; `func buildTool(stripped string) bool`; `var verboseFlag *regexp.Regexp`

- [ ] **Step 1: Write the failing test**

`internal/routing/redirect_test.go`:
```go
package routing

import "testing"

// Expected values in this file come from node running the TS regex literals
// and the segment loop of routing.mjs:733-786 on the same inputs.

func TestCurlCheck(t *testing.T) {
	cases := []struct{ cmd, want string }{
		{"curl https://x.com", "dangerous"},
		{"curl -s -o out.json https://x.com", "safe"},
		{"curl -s https://x.com 2>/dev/null", "safe"}, // R1 P5: any > counts as file output
		{"wget -q -O - https://x.com", "dangerous"},
		{"gh issue edit --body \"use curl here\"", "none"},
		{"curl https://example.com", "dangerous"},
		{"wget https://example.com/file.tar.gz", "dangerous"},
		{"curl -sL https://example.com/file.tar.gz -o /tmp/file.tar.gz", "safe"},
		{"curl -s --output /tmp/s.tgz https://g.com/s.tgz", "safe"},
		{"wget -q -O /tmp/t.zip https://h.com/t.zip", "safe"},
		{"curl -s https://example.com/data.json > /tmp/data.json", "safe"},
		{"curl -s -o - https://example.com", "dangerous"},
		{"curl -L -o /tmp/f https://e.com/f", "dangerous"},
		{"curl -s --verbose -o /tmp/f https://e.com/f", "dangerous"},
		{"curl -sL -o /tmp/f https://e.com/a && curl https://e.com/api", "dangerous"},
		{"curl -sL -o /tmp/f https://e.com/a && tar xzf /tmp/f -C /tmp", "safe"},
		{"curl -s -o /dev/stdout u", "dangerous"},
		{"CURL -s -o f u", "safe"},
		{"x|curl u", "safe"}, // a single | does not split, and the segment test needs ^ or \s
		{"x;wget u", "dangerous"},
		{"curl\u00a0u", "dangerous"},
		{"curl -s -o f -D - u", "safe"},
		{"curl -s -o f -D -x u", "dangerous"},
		{"wget --quiet -O f u", "safe"},
		{"wget -O f u", "dangerous"},
		{"curl --silent -o f u", "safe"},
		{"echo curl", "none"},
		{"curl", "none"},
		{"curl -s -o f u | sh", "safe"},
		{"curl -s -o f u || curl v", "dangerous"},
		{"curl -so f u", "dangerous"},
		{"cat <<EOF\ncurl x\nEOF", "none"},
		{"curl -s -o f --trace u", "dangerous"},
		{"wget\tu", "dangerous"},
		{"a&&curl u", "dangerous"},
	}
	for _, c := range cases {
		found, dangerous := curlCheck(stripQuotedContent(c.cmd))
		got := "none"
		if found {
			got = "safe"
			if dangerous {
				got = "dangerous"
			}
		}
		if got != c.want {
			t.Errorf("curlCheck(%q) = %s, want %s", c.cmd, got, c.want)
		}
	}
}

func TestInlineHTTP(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"node -e \"fetch('https://a.b')\"", true},
		{"python -c 'import requests; requests.get(u)'", true},
		{"node -e \"fetch('http://api.example.com/data')\"", true},
		{"FETCH ( \"httpx", true},
		{"fetch(url)", false},
		{"requests.post (x)", true},
		{"requests.delete(x)", false},
		{"http.get(u)", true},
		{"http.request (u)", true},
		{"https.get(u)", false},
		{"cat <<EOF\nrequests.get(u)\nEOF", false},
		{"x requests.get\u00a0(u)", true},
	}
	for _, c := range cases {
		if got := inlineHTTP(stripHeredocs(c.cmd)); got != c.want {
			t.Errorf("inlineHTTP(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

func TestBuildTool(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"./gradlew build", true},
		{"mvn \"test\" C:\\x", true},
		{"gradle test --info", true},
		{"mvn clean package -DskipTests", true},
		{"./mvnw verify", true},
		{"find . -name \"run gradle build to compile\"", false},
		{"sbt compile", true},
		{"./sbt test", true},
		{"ls gradle-wrapper-config", false},
		{"echo mvnDocker-image", false},
		{"MVN", true},
		{"x && mvn", true},
		{"x|sbt t", true},
		{"x;gradle", true},
		{"mvn\u00a0test", true},
		{"echo 'mvn test'", false},
		{"gradlew", true},
	}
	for _, c := range cases {
		if got := buildTool(stripQuotedContent(c.cmd)); got != c.want {
			t.Errorf("buildTool(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

func TestVerboseFlag(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{" -v", true},
		{" -vv", false},
		{"x -v x", true},
		{" --verbose", true},
		{" --trace-ascii", true},
		{" -D -", false}, // \b after a final - never holds
		{" -D -x", true},
		{"\u00a0-v", true},
		{" -verbose", false},
		{"-v", false},
		{" -D  -h", true},
	}
	for _, c := range cases {
		if got := verboseFlag.MatchString(c.s); got != c.want {
			t.Errorf("verboseFlag(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/routing/ -run 'TestCurlCheck|TestInlineHTTP|TestBuildTool|TestVerboseFlag' -v`
Expected: FAIL with "./redirect_test.go:47:23: undefined: curlCheck"

- [ ] **Step 3: Write minimal implementation**

`internal/routing/redirect.go`:
```go
package routing

import "strings"

// The TS regexes of the Bash redirects (routing.mjs:733-818), in RE2 form.
// (?i) folds U+017F and U+212A to s and k, which JS /i without u does not;
// no TS example depends on it.
var (
	curlTrigger  = jsRegexp(`(?i)(^|\s|&&|\||;)(curl|wget)\s`)
	chainSplit   = jsRegexp(`\s*(?:&&|\|\||;)\s*`)
	curlSegment  = jsRegexp(`(?i)(^|\s)(curl|wget)\s`)
	curlWord     = jsRegexp(`(?i)\bcurl\b`)
	wgetWord     = jsRegexp(`(?i)\bwget\b`)
	curlOutput   = jsRegexp(`\s(-o|--output)\s`)
	wgetOutput   = jsRegexp(`\s(-O|--output-document)\s`)
	curlStdout   = jsRegexp(`\s(-o|--output)\s+(-|/dev/stdout)(\s|$)`)
	wgetStdout   = jsRegexp(`\s(-O|--output-document)\s+(-|/dev/stdout)(\s|$)`)
	verboseFlag  = jsRegexp(`\s(-v|--verbose|--trace|-D\s+-)\b`) // RE2 \b is ASCII, like JS
	curlSilent   = jsRegexp(`\s-[a-zA-Z]*s|--silent`)
	wgetQuiet    = jsRegexp(`\s-[a-zA-Z]*q|--quiet`)
	inlineFetch  = jsRegexp(`(?i)fetch\s*\(\s*['"](https?://|http)`)
	inlineReqs   = jsRegexp(`(?i)requests\.(get|post|put)\s*\(`)
	inlineHTTPGo = jsRegexp(`(?i)http\.(get|request)\s*\(`)
	buildCommand = jsRegexp(`(?i)(^|\s|&&|\||;)(\./gradlew|gradlew|gradle|\./mvnw|mvnw|mvn|\./sbt|sbt)(\s|$)`)
)

// curlCheck ports the curl/wget block on the quote-stripped command. found
// means the trigger matched; dangerous means some chained segment can flood
// the context. found && !dangerous makes TS return null at once, skipping
// every later Bash check (R1 P6).
func curlCheck(stripped string) (found, dangerous bool) {
	if !curlTrigger.MatchString(stripped) {
		return false, false
	}
	for _, seg := range chainSplit.Split(stripped, -1) {
		if curlSegmentDangerous(jsTrim(seg)) {
			return true, true
		}
	}
	return true, false
}

// curlSegmentDangerous is the per-segment test of routing.mjs:738-767. Any
// '>' counts as file output (R1 P5), so curl -s URL 2>/dev/null is safe.
func curlSegmentDangerous(s string) bool {
	if !curlSegment.MatchString(s) {
		return false
	}
	isCurl := curlWord.MatchString(s)
	isWget := wgetWord.MatchString(s)
	output, silent := wgetOutput, wgetQuiet
	if isCurl {
		output, silent = curlOutput, curlSilent
	}
	if !output.MatchString(s) && !strings.Contains(s, ">") {
		return true
	}
	if isCurl && curlStdout.MatchString(s) || isWget && wgetStdout.MatchString(s) {
		return true
	}
	if verboseFlag.MatchString(s) {
		return true
	}
	return !silent.MatchString(s)
}

// inlineHTTP ports routing.mjs:793-798 on the heredoc-stripped command
// (quotes kept, so code passed by -e or -c is seen).
func inlineHTTP(noHeredoc string) bool {
	return inlineFetch.MatchString(noHeredoc) || inlineReqs.MatchString(noHeredoc) || inlineHTTPGo.MatchString(noHeredoc)
}

// buildTool ports routing.mjs:810 on the quote-stripped command.
func buildTool(stripped string) bool {
	return buildCommand.MatchString(stripped)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/routing/ -run 'TestCurlCheck|TestInlineHTTP|TestBuildTool|TestVerboseFlag' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/routing/redirect.go internal/routing/redirect_test.go
git commit -m "feat(routing): curl/wget, inline HTTP and build tool matchers"
```

### Task 18: Routing block, guidance and redirect texts

**Files:**
- Create: `internal/routing/text.go`
- Test: `internal/routing/text_test.go`

**Interfaces:**
- Consumes: none (stdlib only)
- Produces: `const ToolPrefix = "mcp__cmgo__"`; `func RoutingBlock(includeCommands, toolSearchBootstrap bool) string`; unexported `func named(s string) string`, `var readGuidance, grepGuidance, bashGuidance, curlMessage, inlineMessage string`, `func buildMessage(command string) string`, `func webFetchMessage(url string) string`; test helpers `type pinned struct{ name string; size int; hash string }` and `checkPinned(t *testing.T, p pinned, got string)`

- [ ] **Step 1: Write the failing test**

`internal/routing/text_test.go`:
```go
package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// The expected texts are the TS output (hooks/routing-block.mjs and
// hooks/core/routing.mjs at a5e6dd31) with mcp__plugin_context-mode_context-mode__
// replaced by mcp__cmgo__, pinned by byte length and sha256. testdata/golden/
// hooks-strings.json carries the full texts (TestGoldenStrings).
type pinned struct {
	name string
	size int
	hash string
}

func checkPinned(t *testing.T, p pinned, got string) {
	t.Helper()
	sum := sha256.Sum256([]byte(got))
	if len(got) != p.size || hex.EncodeToString(sum[:]) != p.hash {
		t.Errorf("%s: %d bytes, sha256 %x; want %d bytes, %s\ngot:\n%s", p.name, len(got), sum, p.size, p.hash, got)
	}
}

func TestRoutingBlock(t *testing.T) {
	cases := []struct {
		commands, bootstrap bool
		want                pinned
	}{
		{true, false, pinned{"routing block with commands", 4239, "52c54ed411336a4795911e32dee0ea8c103c0d41b793d942355d3eca57ca4e47"}},
		{false, true, pinned{"agent routing suffix", 4105, "8c756c25026a96477fac96011f6869f83d1a4974640c5bff5373d76ba5de762c"}},
		{false, false, pinned{"SessionStart routing block", 3481, "6b7c33490c32133a9bfe5a11b240236f6732a0f700c581ad90f46d6868edd023"}},
	}
	for _, c := range cases {
		checkPinned(t, c.want, RoutingBlock(c.commands, c.bootstrap))
	}
	if strings.Contains(RoutingBlock(false, false), "mcp__plugin_") {
		t.Error("routing block still names the TS plugin tools")
	}
}

func TestGuidanceAndRedirectTexts(t *testing.T) {
	cases := []struct {
		got  string
		want pinned
	}{
		{bashGuidance, pinned{"guidance-bash", 597, "8e5b6e0792263eb68968bdf22bf1ad96db5ab44c58b3d0b686ef59144d885100"}},
		{readGuidance, pinned{"guidance-read", 377, "480cdbe6726c56c5e9d30043d59b710fd69760afa3bcd18c4bcc1b84d1f5f11c"}},
		{grepGuidance, pinned{"guidance-grep", 477, "3759bf1be31f94b6c5f49281c80144dc629201c1410ff6a1850ce98729713c88"}},
		{curlMessage, pinned{"deny-curl-wget", 467, "8445533370d36a76361daaa46de4c9046d37a4c49980a04d71bca1374a7207b9"}},
		{inlineMessage, pinned{"deny-inline-http", 342, "da11642dff800f1658d28902eddc419ada0b3f3e0912f3e96b4dfb02f4bdc356"}},
		{webFetchMessage("https://example.com/a"), pinned{"deny-webfetch-example", 563, "4a134a67a5f425ddb67b4c38b81e67d9a6939d3f16a1976f734756a8025c9ed9"}},
		{buildMessage("./gradlew build"), pinned{"deny-build-gradlew-example", 402, "f97ec9b71a06e587b3529da87527eb15345ea76f4c53cdaf03dfd48bb2412ef6"}},
	}
	for _, c := range cases {
		checkPinned(t, c.want, c.got)
	}
}

func TestBuildMessageEscapes(t *testing.T) {
	// routing.mjs:811: backslashes doubled, then quotes escaped.
	got := buildMessage(`mvn "test" C:\x`)
	if want := `code: \"mvn \"test\" C:\\x 2>&1 | tail -30\")`; !strings.Contains(got, want) {
		t.Errorf("buildMessage = %q, want it to contain %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/routing/ -run 'TestRoutingBlock|TestGuidanceAndRedirectTexts|TestBuildMessageEscapes' -v`
Expected: FAIL with "internal/routing/text_test.go:38:26: undefined: RoutingBlock"

- [ ] **Step 3: Write minimal implementation**

The template text below is the TS source cut at its two `${…}` slots, with `${t("x")}` written as `$Tx`.

`internal/routing/text.go`:
```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/routing/ -run 'TestRoutingBlock|TestGuidanceAndRedirectTexts|TestBuildMessageEscapes' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/routing/text.go internal/routing/text_test.go
git commit -m "feat(routing): routing block and guidance texts with cmgo tool names"
```

### Task 19: Guidance throttle and prune

**Files:**
- Create: `internal/routing/guidance.go`
- Test: `internal/routing/guidance_test.go`

**Interfaces:**
- Consumes: `hookio.FileID(sid string) string`
- Produces: `func PruneGuidance(runDir string, now time.Time, maxAge time.Duration)`; unexported `func guidanceOnce(runDir, sid, typ string) bool`

- [ ] **Step 1: Write the failing test**

`internal/routing/guidance_test.go`:
```go
package routing

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/hookio"
)

func TestGuidanceOnce(t *testing.T) {
	run := filepath.Join(t.TempDir(), "run") // created on first use
	if !guidanceOnce(run, "s1", "bash") {
		t.Fatal("first bash guidance not shown")
	}
	if guidanceOnce(run, "s1", "bash") {
		t.Error("second bash guidance shown")
	}
	if !guidanceOnce(run, "s1", "read") {
		t.Error("throttle is not per type")
	}
	if !guidanceOnce(run, "s2", "bash") {
		t.Error("throttle is not per session")
	}
	if fi, err := os.Stat(filepath.Join(run, "guidance-s1", "bash")); err != nil || fi.Size() != 0 {
		t.Errorf("marker: %v, %v; want an empty file", fi, err)
	}
}

func TestGuidanceOnceUnsafeSessionID(t *testing.T) {
	run := t.TempDir()
	if !guidanceOnce(run, "../escape", "grep") {
		t.Fatal("guidance not shown")
	}
	if _, err := os.Stat(filepath.Join(run, "guidance-"+hookio.FileID("../escape"), "grep")); err != nil {
		t.Error(err)
	}
}

func TestGuidanceOnceUnwritable(t *testing.T) {
	// TS: any error creating the marker means no guidance.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if guidanceOnce(filepath.Join(file, "run"), "s1", "bash") {
		t.Error("guidance shown although the marker cannot be created")
	}
}

func TestPruneGuidance(t *testing.T) {
	run := t.TempDir()
	now := time.Now()
	old := now.Add(-8 * 24 * time.Hour)
	mk := func(name string, dir bool, mtime time.Time) string {
		p := filepath.Join(run, name)
		var err error
		if dir {
			err = os.MkdirAll(filepath.Join(p, "bash"), 0o700)
		} else {
			err = os.WriteFile(p, nil, 0o600)
		}
		if err == nil {
			err = os.Chtimes(p, mtime, mtime)
		}
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	gone := mk("guidance-old", true, old)
	kept := []string{
		mk("guidance-new", true, now),
		mk("other-old", true, old),
		mk("guidance-file", false, old),
		mk("mcp-ready-1", false, old),
	}
	PruneGuidance(run, now, 7*24*time.Hour)
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Errorf("old guidance dir kept: %v", err)
	}
	for _, p := range kept {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s removed: %v", filepath.Base(p), err)
		}
	}
	PruneGuidance(filepath.Join(run, "missing"), now, time.Hour) // no panic
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/routing/ -run 'TestGuidanceOnce|TestPruneGuidance' -v`
Expected: FAIL with "./guidance_test.go:14:6: undefined: guidanceOnce"

- [ ] **Step 3: Write minimal implementation**

`internal/routing/guidance.go`:
```go
package routing

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/hookio"
)

// guidanceOnce ports guidanceOnce (routing.mjs:121-144): it reports true the
// first time guidance of typ is due in session sid. The marker is an empty
// file created with O_EXCL, so concurrent hooks agree on one winner; any
// error (EEXIST, an unwritable dir) means no guidance, as in TS. TS also kept
// a process-wide in-memory set, which a one-shot hook never consults.
func guidanceOnce(runDir, sid, typ string) bool {
	dir := filepath.Join(runDir, "guidance-"+hookio.FileID(sid))
	_ = os.MkdirAll(dir, 0o700) // an error shows up in the open below
	f, err := os.OpenFile(filepath.Join(dir, typ), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// PruneGuidance removes the guidance-* dirs under runDir whose mtime is more
// than maxAge before now. TS never cleaned them (R1 P18); SessionStart calls
// this on startup (spec §4). Errors are ignored: a dir left behind only keeps
// guidance off for a session that is long over.
func PruneGuidance(runDir string, now time.Time, maxAge time.Duration) {
	ents, err := os.ReadDir(runDir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "guidance-") {
			continue
		}
		if fi, err := e.Info(); err == nil && now.Sub(fi.ModTime()) > maxAge {
			_ = os.RemoveAll(filepath.Join(runDir, e.Name()))
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/routing/ -run 'TestGuidanceOnce|TestPruneGuidance' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/routing/guidance.go internal/routing/guidance_test.go
git commit -m "feat(routing): once-per-session guidance markers and prune"
```

### Task 20: MCP-ready sentinel

**Files:**
- Create: `internal/routing/mcpready.go`
- Test: `internal/routing/mcpready_test.go`

**Interfaces:**
- Consumes: `jsParseInt` from Task 15
- Produces: `func MCPReady(runDir string) bool`; `func WriteSentinel(runDir string, pid int) (remove func(), err error)`; unexported `const sentinelPrefix = "mcp-ready-"`, `func pidLive(pid int64) bool`; test helpers `writeSentinelFile`, `exists`, `const deadPID`

- [ ] **Step 1: Write the failing test**

`internal/routing/mcpready_test.go`:
```go
package routing

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// deadPID is above every pid_max (TS tests use the same value).
const deadPID = "2147483647"

func writeSentinelFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestMCPReadyMissingDir(t *testing.T) {
	if MCPReady(filepath.Join(t.TempDir(), "run")) {
		t.Error("ready without a run dir")
	}
}

func TestMCPReadyLivePID(t *testing.T) {
	dir := t.TempDir()
	p := writeSentinelFile(t, dir, "mcp-ready-x", strconv.Itoa(os.Getpid()))
	if !MCPReady(dir) {
		t.Error("live pid not ready")
	}
	if !exists(p) {
		t.Error("live sentinel removed")
	}
}

func TestMCPReadyEPERMIsLive(t *testing.T) {
	// pid 1 belongs to root: kill(1, 0) is EPERM for a normal user, nil for root.
	dir := t.TempDir()
	writeSentinelFile(t, dir, "mcp-ready-1", "1")
	if !MCPReady(dir) {
		t.Error("pid 1 not ready")
	}
}

func TestMCPReadyRemovesDead(t *testing.T) {
	dir := t.TempDir()
	dead := writeSentinelFile(t, dir, "mcp-ready-"+deadPID, deadPID)
	zero := writeSentinelFile(t, dir, "mcp-ready-0", "0") // kill(0, 0) would signal our own group
	if MCPReady(dir) {
		t.Error("dead pids reported ready")
	}
	if exists(dead) || exists(zero) {
		t.Error("dead sentinels kept")
	}
}

func TestMCPReadySkipsGarbage(t *testing.T) {
	// parseInt NaN: TS skips the file and leaves it (it may be mid-write).
	dir := t.TempDir()
	empty := writeSentinelFile(t, dir, "mcp-ready-empty", "")
	garbage := writeSentinelFile(t, dir, "mcp-ready-garbage", "abc")
	other := writeSentinelFile(t, dir, "other-"+deadPID, deadPID)
	if MCPReady(dir) {
		t.Error("garbage reported ready")
	}
	if !exists(empty) || !exists(garbage) || !exists(other) {
		t.Error("a skipped file was removed")
	}
	writeSentinelFile(t, dir, "mcp-ready-live", " "+strconv.Itoa(os.Getpid())+"\n") // parseInt skips spaces
	if !MCPReady(dir) {
		t.Error("padded live pid not ready")
	}
}

func TestWriteSentinel(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cmgo", "run")
	remove, err := WriteSentinel(dir, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "mcp-ready-"+strconv.Itoa(os.Getpid()))
	if b, err := os.ReadFile(p); err != nil || string(b) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("sentinel = %q, %v", b, err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("run dir mode: %v, %v; want 0700", fi, err)
	}
	if !MCPReady(dir) {
		t.Error("not ready after WriteSentinel")
	}
	remove()
	if exists(p) || MCPReady(dir) {
		t.Error("sentinel still there after remove")
	}
}

func TestWriteSentinelError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSentinel(filepath.Join(file, "run"), 1); err == nil {
		t.Error("no error for a run dir under a file")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/routing/ -run 'TestMCPReady|TestWriteSentinel' -v`
Expected: FAIL with "./mcpready_test.go:28:5: undefined: MCPReady"

- [ ] **Step 3: Write minimal implementation**

`internal/routing/mcpready.go`:
```go
package routing

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// sentinelPrefix names the files cmgo serve writes in $CMGO_DIR/run.
const sentinelPrefix = "mcp-ready-"

// MCPReady ports isMCPReady (hooks/core/mcp-ready.mjs:73-109) over runDir
// (spec §2.5): true at the first sentinel whose pid is live (kill(pid, 0)
// succeeds or fails with EPERM). The file of a dead pid is removed. TS kept
// a dead pid's file for 90 s because its server refreshed it every 30 s;
// cmgo serve writes it once, so there is no grace period. A file whose text
// parseInt cannot read is skipped and left alone, as in TS (it may be
// mid-write).
func MCPReady(runDir string) bool {
	ents, err := os.ReadDir(runDir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), sentinelPrefix) {
			continue
		}
		p := filepath.Join(runDir, e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		pid, ok := jsParseInt(string(b))
		if !ok {
			continue
		}
		if pidLive(pid) {
			return true
		}
		_ = os.Remove(p) // best effort, as TS
	}
	return false
}

// pidLive reports kill(pid, 0) success or EPERM. A pid <= 0 counts as dead:
// TS would call kill(0, 0) or kill(-1, 0), which signal a process group and
// report a server that does not exist.
func pidLive(pid int64) bool {
	if pid <= 0 || pid > math.MaxInt32 {
		return false
	}
	err := syscall.Kill(int(pid), 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// WriteSentinel creates runDir (0700) and runDir/mcp-ready-<pid> holding the
// pid. remove deletes the file; cmgo serve defers it.
func WriteSentinel(runDir string, pid int) (remove func(), err error) {
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return nil, err
	}
	p := filepath.Join(runDir, sentinelPrefix+strconv.Itoa(pid))
	if err := os.WriteFile(p, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		return nil, err
	}
	return func() { _ = os.Remove(p) }, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/routing/ -run 'TestMCPReady|TestWriteSentinel' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/routing/mcpready.go internal/routing/mcpready_test.go
git commit -m "feat(routing): MCP-ready sentinel in the cmgo run dir"
```

### Task 21: Route

**Files:**
- Create: `internal/routing/route.go`
- Test: `internal/routing/route_test.go`

**Interfaces:**
- Consumes: `security.NewPolicy(projectDir, home string) *security.Policy`, `(*security.Policy).EvaluateCommand(cmd string) (decision, pattern string)`; `hookio.Object` (`ParseObject`, `String`, `Has`, `Set`, `SetString`, `Clone`, `MarshalJSON`), `hookio.JSPrefix`, `hookio.JSLen`, `hookio.QuoteString`; Task 15 to Task 19
- Produces: `type Deps struct{ ProjectDir, SessionID, RunDir string; Policy *security.Policy; Getenv func(string) string; MCPReady func() bool }`; `type Redirect struct{ Tool, Type string; BytesAvoided int64; Summary string }`; `type Decision struct{ Action, Reason string; UpdatedInput *hookio.Object; AdditionalContext string; Redirect *Redirect }`; `func Route(toolName string, toolInput *hookio.Object, subagent bool, d Deps) Decision`; unexported `func redirectTo(msg string) Decision`; test helpers `testDeps(t) Deps`, `input(t, js string) *hookio.Object`, `bash(cmd string) *hookio.Object`, `echo(msg string) string`, `check(t, name string, dec Decision, action, payload string)`

- [ ] **Step 1: Write the failing test**

`internal/routing/route_test.go`:
```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/routing/ -run 'TestRoute' -v`
Expected: FAIL with "./route_test.go:15:29: undefined: Deps"

- [ ] **Step 3: Write minimal implementation**

`internal/routing/route.go`:
```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/routing/ -run 'TestRoute' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/routing/route.go internal/routing/route_test.go
git commit -m "feat(routing): PreToolUse decision tree for Claude Code"
```

### Task 22: Claude Code output format and PreToolUse events

**Files:**
- Create: `internal/routing/format.go`
- Test: `internal/routing/format_test.go`

**Interfaces:**
- Consumes: `hookio.Marshal(v any) ([]byte, error)`, `hookio.Object`; `store.Event{Type, Category string; Priority int; Data string; BytesAvoided int64}`; `Decision`, `Redirect`, `redirectTo` and the test helpers from Task 21; texts from Task 18
- Produces: `func Format(dec Decision) []byte`; `func Events(toolName string, dec Decision) []store.Event`; unexported `func denyReason(dec Decision) (string, bool)`, `const fallbackReason`, `var echoReason`

- [ ] **Step 1: Write the failing test**

`internal/routing/format_test.go`:
```go
package routing

import (
	"reflect"
	"testing"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

func TestFormat(t *testing.T) {
	agent := &hookio.Object{}
	agent.SetString("prompt", "a<b>&c")
	agent.Set("n", []byte(`1`))
	notEcho := &hookio.Object{}
	notEcho.SetString("command", "ls")
	cases := []struct {
		name string
		dec  Decision
		want string
	}{
		{"passthrough", Decision{}, ""},
		{"deny", Decision{Action: "deny", Reason: "no <x> & \u2028"},
			"{\"hookSpecificOutput\":{\"hookEventName\":\"PreToolUse\",\"permissionDecision\":\"deny\",\"permissionDecisionReason\":\"no <x> & \u2028\"}}\n"},
		{"ask", Decision{Action: "ask", Reason: "ignored"},
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask"}}` + "\n"},
		{"modify agent", Decision{Action: "modify", UpdatedInput: agent},
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":{"prompt":"a<b>&c","n":1}}}` + "\n"},
		{"modify command", redirectTo("line1\n\"q\" \\x"),
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"line1\n\"q\" \\x"}}` + "\n"},
		{"modify command not echo", Decision{Action: "modify", UpdatedInput: notEcho},
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"` + fallbackReason + `"}}` + "\n"},
		{"context", Decision{Action: "context", AdditionalContext: "<context_guidance>"},
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"<context_guidance>"}}` + "\n"},
		{"empty context", Decision{Action: "context"},
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":""}}` + "\n"},
	}
	for _, c := range cases {
		if got := string(Format(c.dec)); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	if Format(Decision{}) != nil {
		t.Error("passthrough is not nil")
	}
}

func TestEvents(t *testing.T) {
	d := testDeps(t)
	curl := Route("Bash", bash("curl https://x.com"), false, d)
	web := Route("WebFetch", input(t, `{"url":"https://e.com/a"}`), false, d)
	cases := []struct {
		name, tool string
		dec        Decision
		want       []store.Event
	}{
		{"security deny", "Bash", Route("Bash", bash("sudo ls"), false, d), []store.Event{
			{Type: "rejected", Category: "rejected-approach", Priority: 2, Data: "Bash: Blocked by security policy: matches deny pattern Bash(sudo *)"},
		}},
		{"curl", "Bash", curl, []store.Event{
			{Type: "bash-redirected", Category: "redirect", Priority: 2, Data: "Bash: curl https://x.com", BytesAvoided: 8192},
			{Type: "rejected", Category: "rejected-approach", Priority: 2, Data: "Bash: " + curlMessage},
		}},
		{"inline", "Bash", Route("Bash", bash(`node -e "fetch('https://a.b')"`), false, d), []store.Event{
			{Type: "rejected", Category: "rejected-approach", Priority: 2, Data: "Bash: " + inlineMessage},
		}},
		{"webfetch", "WebFetch", web, []store.Event{
			{Type: "webfetch-redirected", Category: "redirect", Priority: 2, Data: "WebFetch: https://e.com/a", BytesAvoided: 16384},
			{Type: "rejected", Category: "rejected-approach", Priority: 2, Data: "WebFetch: " + webFetchMessage("https://e.com/a")},
		}},
		{"large read", "Read", Decision{Action: "context", AdditionalContext: readGuidance, Redirect: &Redirect{Tool: "Read", Type: "read-redirected", BytesAvoided: 50001, Summary: "/a/big.txt"}}, []store.Event{
			{Type: "read-redirected", Category: "redirect", Priority: 2, Data: "Read: /a/big.txt", BytesAvoided: 50001},
		}},
		{"ask", "Bash", Decision{Action: "ask"}, nil},
		{"agent", "Agent", Route("Agent", input(t, `{"prompt":"x"}`), false, d), nil},
		{"guidance", "Grep", Decision{Action: "context", AdditionalContext: grepGuidance}, nil},
		{"passthrough", "Bash", Decision{}, nil},
	}
	for _, c := range cases {
		if got := Events(c.tool, c.dec); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/routing/ -run 'TestFormat|TestEvents' -v`
Expected: FAIL with "./format_test.go:32:117: undefined: fallbackReason"

- [ ] **Step 3: Write minimal implementation**

`internal/routing/format.go`:
```go
package routing

import (
	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// echoReason pulls the message out of a Bash redirect (formatters.mjs:53).
var echoReason = jsRegexp(`(?s)^echo\s+"(.+)"$`)

// fallbackReason is the deny text for a modify whose command is not an echo
// (formatters.mjs:56); routing never builds one.
const fallbackReason = "Redirected to ctx_execute / ctx_fetch_and_index. Call ctx_execute(language, code) to fetch and derive your answer in one round trip, or call ctx_fetch_and_index(url, source) when you want to query the response later via ctx_search. Both have full network access. Retry the same call on a transient DNS error (EAI_AGAIN, ETIMEDOUT, ENETUNREACH)."

// hookOutput is hookSpecificOutput with the keys in TS order.
type hookOutput struct {
	HookEventName            string         `json:"hookEventName"`
	PermissionDecision       string         `json:"permissionDecision,omitempty"`
	PermissionDecisionReason *string        `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             *hookio.Object `json:"updatedInput,omitempty"`
	AdditionalContext        *string        `json:"additionalContext,omitempty"`
}

// Format ports the Claude Code formatter (formatters.mjs:7-71): nil for a
// passthrough, else one compact JSON line ending in "\n". A modify that
// carries a command is a deny, because Claude Code ignores a replaced Bash
// command; any other modify is updatedInput alone, with no
// permissionDecision (R1 P23).
func Format(dec Decision) []byte {
	out := hookOutput{HookEventName: "PreToolUse"}
	if reason, ok := denyReason(dec); ok {
		out.PermissionDecision, out.PermissionDecisionReason = "deny", &reason
	} else {
		switch dec.Action {
		case "ask":
			out.PermissionDecision = "ask"
		case "modify":
			out.UpdatedInput = dec.UpdatedInput
		case "context":
			out.AdditionalContext = &dec.AdditionalContext
		default:
			return nil
		}
	}
	b, err := hookio.Marshal(struct {
		HookSpecificOutput hookOutput `json:"hookSpecificOutput"`
	}{out})
	if err != nil {
		return nil // updatedInput holds JSON that already parsed, so this cannot happen
	}
	return append(b, '\n')
}

// denyReason returns the reason Claude Code is shown when dec is emitted as
// a deny: a deny, or a modify carrying a command.
func denyReason(dec Decision) (string, bool) {
	switch {
	case dec.Action == "deny":
		return dec.Reason, true
	case dec.Action == "modify" && dec.UpdatedInput != nil && dec.UpdatedInput.Has("command"):
		cmd, _ := dec.UpdatedInput.String("command")
		if m := echoReason.FindStringSubmatch(cmd); m != nil {
			return m[1], true
		}
		return fallbackReason, true
	}
	return "", false
}

// Events returns the store events PreToolUse records for dec (spec §2.4):
// the redirect event first, then a rejected event for anything Claude Code
// sees as a deny. TS wrote these as two separate marker files, so it had no
// order; redirect first keeps the cause before the refusal in the timeline.
func Events(toolName string, dec Decision) []store.Event {
	var evs []store.Event
	if r := dec.Redirect; r != nil {
		evs = append(evs, store.Event{Type: r.Type, Category: "redirect", Priority: 2, Data: r.Tool + ": " + r.Summary, BytesAvoided: r.BytesAvoided})
	}
	if reason, ok := denyReason(dec); ok {
		evs = append(evs, store.Event{Type: "rejected", Category: "rejected-approach", Priority: 2, Data: toolName + ": " + reason})
	}
	return evs
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/routing/ -run 'TestFormat|TestEvents' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/routing/format.go internal/routing/format_test.go
git commit -m "feat(routing): Claude Code hook output and PreToolUse events"
```

### Task 23: Golden replay of the TS PreToolUse cases

**Files:**
- Test: `internal/routing/golden_test.go`

**Interfaces:**
- Consumes: `Route`, `Format`, `MCPReady`, `WriteSentinel`, `Deps`, `Decision`, `RoutingBlock` and the texts from Tasks Task 18 to Task 22; `security.NewPolicy`; `hookio.ParseObject`; `testdata/golden/hooks-pretooluse.json` and `hooks-strings.json` (Task 1)
- Produces: test only (`TestGoldenPreToolUse`, `TestGoldenStrings`, `knownDiffs`, the `pretooluse` rows of `testdata/golden/HOOKS-KNOWN-DIFFS.md`)

The implementation already exists (Tasks Task 15 to Task 22). This task replays every TS case from Task 1 through `Route` and `Format`, and checks the routing texts against `hooks-strings.json`.

- [ ] **Step 1: Write the failing test**

`internal/routing/golden_test.go`:
```go
package routing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/security"
)

// goldenCase is one case of ../../testdata/golden/hooks-pretooluse.json,
// written by tools/golden-gen/gen-hooks.mjs from TS routePreToolUse +
// formatDecision, each case in a fresh node process. output is the exact
// stdout of the TS hook, or null when it writes nothing. Tool names are
// already cmgo's; <BASE> stands for the generator's temp dir.
type goldenCase struct {
	I          int             `json:"i"`
	Tool       string          `json:"tool"`
	Input      json.RawMessage `json:"input"`
	ProjectDir string          `json:"projectDir"` // "<proj>" or ""
	SessionID  string          `json:"sessionId"`
	Subagent   bool            `json:"subagent"`
	Output     *string         `json:"output"`
}

// knownDiff is a case where cmgo differs from TS on purpose, one row of
// testdata/golden/HOOKS-KNOWN-DIFFS.md. want is the cmgo output ("" for none).
type knownDiff struct{ reason, want string }

func knownDiffs() map[int]knownDiff {
	const ctxMatcher = "spec §2.3: ctx_execute, ctx_execute_file and ctx_batch_execute are not PreToolUse matchers; cmgo serve checks deny rules itself"
	return map[int]knownDiff{
		25: {ctxMatcher, ""},
		26: {ctxMatcher, ""},
		28: {ctxMatcher, ""},
		29: {ctxMatcher, ""},
		30: {ctxMatcher, ""},
		31: {"spec §2.3: external-MCP guidance dropped (the TS mcp__ matcher never fires, R1 P11)", ""},
	}
}

func TestGoldenPreToolUse(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", "hooks-pretooluse.json"))
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	raw = []byte(strings.ReplaceAll(string(raw), "<BASE>", base))
	var g struct {
		TSCommit string       `json:"tsCommit"`
		Cases    []goldenCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if g.TSCommit != "a5e6dd31" {
		t.Fatalf("hooks-pretooluse.json: commit %q", g.TSCommit)
	}
	cases := g.Cases

	// The state gen-hooks.mjs sets up: two files around the 50,000-byte
	// Read threshold, project rules, an empty home, a live MCP sentinel.
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	proj := filepath.Join(base, "proj")
	write(filepath.Join(proj, ".claude", "settings.json"),
		`{"permissions":{"deny":["Bash(sudo *)","Read(**/.env)"],"ask":["Bash(git push:*)"],"allow":["Bash(curl:*)"]}}`)
	write(filepath.Join(base, "big.txt"), strings.Repeat("x", 50001))
	write(filepath.Join(base, "small.txt"), strings.Repeat("x", 50000))
	home := filepath.Join(base, "home")
	run := filepath.Join(base, "run")
	remove, err := WriteSentinel(run, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer remove()

	diffs := knownDiffs()
	seen := map[int]bool{}
	for _, c := range cases { // in order: session s1's guidance markers carry over
		in, err := hookio.ParseObject(c.Input)
		if err != nil {
			t.Fatal(err)
		}
		pd := ""
		if c.ProjectDir == "<proj>" {
			pd = proj
		}
		d := Deps{
			ProjectDir: pd,
			SessionID:  c.SessionID,
			RunDir:     run,
			Policy:     security.NewPolicy(pd, home),
			Getenv:     func(string) string { return "" },
			MCPReady:   func() bool { return MCPReady(run) },
		}
		got := string(Format(Route(c.Tool, in, c.Subagent, d)))
		ts := ""
		if c.Output != nil {
			ts = *c.Output
		}
		if kd, ok := diffs[c.I]; ok {
			seen[c.I] = true
			if got == ts {
				t.Errorf("case %d (%s): listed difference is gone (%s)", c.I, c.Tool, kd.reason)
			} else if got != kd.want {
				t.Errorf("case %d (%s):\n got %q\nwant %q", c.I, c.Tool, got, kd.want)
			}
			continue
		}
		if got != ts {
			t.Errorf("case %d (%s %s):\n got %q\nwant %q", c.I, c.Tool, c.Input, got, ts)
		}
	}
	if len(cases) != 37 || len(seen) != len(diffs) {
		t.Errorf("%d cases, %d of %d known diffs seen", len(cases), len(seen), len(diffs))
	}
}

// TestGoldenStrings checks the texts in ../../testdata/golden/hooks-strings.json,
// rendered by gen-hooks.mjs from hooks/routing-block.mjs with the mcp__cmgo__
// namer, and the redirect texts taken from TS routePreToolUse decisions.
func TestGoldenStrings(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", "hooks-strings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		TSCommit      string `json:"tsCommit"`
		RoutingBlocks []struct {
			Name                string `json:"name"`
			IncludeCommands     bool   `json:"includeCommands"`
			ToolSearchBootstrap bool   `json:"toolSearchBootstrap"`
			Text                string `json:"text"`
		} `json:"routingBlocks"`
		Texts map[string]string `json:"texts"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if g.TSCommit != "a5e6dd31" || len(g.RoutingBlocks) != 3 {
		t.Fatalf("hooks-strings.json: commit %q, %d routing blocks", g.TSCommit, len(g.RoutingBlocks))
	}
	for _, b := range g.RoutingBlocks {
		if got := RoutingBlock(b.IncludeCommands, b.ToolSearchBootstrap); got != b.Text {
			t.Errorf("%s: RoutingBlock(%v, %v) differs\ngot:\n%s", b.Name, b.IncludeCommands, b.ToolSearchBootstrap, got)
		}
	}
	got := map[string]string{
		"guidance-bash":              bashGuidance,
		"guidance-read":              readGuidance,
		"guidance-grep":              grepGuidance,
		"deny-curl-wget":             curlMessage,
		"deny-inline-http":           inlineMessage,
		"deny-build-gradlew-example": buildMessage("./gradlew build"),
		"deny-webfetch-example":      webFetchMessage("https://example.com/a"),
	}
	if len(g.Texts) != len(got) {
		t.Errorf("hooks-strings.json has %d texts, want %d", len(g.Texts), len(got))
	}
	for name, want := range g.Texts {
		if got[name] != want {
			t.Errorf("%s:\ngot  %q\nwant %q", name, got[name], want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Move the golden file aside to see the test fail, then put it back:

Run: `mv testdata/golden/hooks-pretooluse.json testdata/golden/hooks-pretooluse.json.off && go test ./internal/routing/ -run TestGoldenPreToolUse -count=1; mv testdata/golden/hooks-pretooluse.json.off testdata/golden/hooks-pretooluse.json`
Expected: FAIL with "golden_test.go:48: open ../../testdata/golden/hooks-pretooluse.json: no such file or directory"

- [ ] **Step 3: Write minimal implementation**

No new code: the golden file is back in place and `Route`/`Format` already exist.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/routing/ -count=1 -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/routing/golden_test.go
git commit -m "test(routing): replay the TS PreToolUse golden cases"
```

---

### Task 24: JS string, regexp and JSON-text helpers

**Files:**
- Create: `internal/capture/js.go`
- Test: `internal/capture/js_test.go`

**Interfaces:**
- Consumes: `hookio.Object` (`Get`, `Set`, `MarshalJSON`)
- Produces: consts `jsWS`, `jsNotWS`; `func isJSSpace(r rune) bool`; `func jsTrim(s string) string`; `func asciiLower(s string) string`; `func jsString(raw json.RawMessage) string`; `func inputString(o *hookio.Object, key, def string) string`; `func responseText(raw json.RawMessage) string`; `func compactJS(raw json.RawMessage) string`

- [ ] **Step 1: Write the failing test**

`internal/capture/js_test.go`:
```go
package capture

import (
	"encoding/json"
	"regexp"
	"testing"
)

// Expected values come from node: String(JSON.parse(raw)) and
// JSON.stringify(JSON.parse(raw)).
func TestJSString(t *testing.T) {
	cases := []struct{ raw, want string }{
		{`"a b"`, "a b"},
		{`42`, "42"},
		{`true`, "true"},
		{`null`, "null"},
		{`{"prompt":"x"}`, "[object Object]"},
		{`["a",null,[1,2],{}]`, "a,,1,2,[object Object]"},
		{`[]`, ""},
	}
	for _, c := range cases {
		if got := jsString(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("jsString(%s) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestResponseText(t *testing.T) {
	cases := []struct{ raw, want string }{
		{``, `""`}, // missing: JSON.stringify(undefined ?? "")
		{`null`, `""`},
		{`"plain\ntext"`, "plain\ntext"},
		{`{ "stdout" : "<a>&b", "n" : [1, 2] }`, `{"stdout":"<a>&b","n":[1,2]}`},
		{`["\u2028"]`, "[\"\u2028\"]"}, // JSON.stringify writes U+2028 raw
		{`false`, `false`},
	}
	for _, c := range cases {
		if got := responseText(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("responseText(%s) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestJSTrimAndClasses(t *testing.T) {
	if got := jsTrim("\u00a0\ufeff x \u3000\n"); got != "x" {
		t.Errorf("jsTrim = %q, want %q", got, "x")
	}
	ws := regexp.MustCompile(`^a` + jsWS + `+` + jsNotWS + `+$`)
	if !ws.MatchString("a\u00a0\u2003bc") || ws.MatchString("a b\u3000c") {
		t.Error("jsWS / jsNotWS do not follow JS \\s / \\S")
	}
	if got := asciiLower("ÄBC.MD \u017f \u212a"); got != "Äbc.md \u017f \u212a" {
		t.Errorf("asciiLower = %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/capture/ -run 'TestJSString|TestResponseText|TestJSTrimAndClasses' -v`
Expected: FAIL with "internal/capture/js_test.go:22:13: undefined: jsString"

- [ ] **Step 3: Write minimal implementation**

`internal/capture/js.go`:
```go
package capture

import (
	"encoding/json"
	"strings"

	"github.com/lang315/context-mode-go/internal/hookio"
)

// JavaScript semantics the TS extractors rely on. JS `\s` and trim() cover
// Unicode spaces; Go's regexp `\s` is ASCII only. `\b`, `\w` and `\d` are
// ASCII in both, so they are used as is.
const wsBody = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

const (
	jsWS    = "[" + wsBody + "]"  // JS \s
	jsNotWS = "[^" + wsBody + "]" // JS \S
)

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// asciiLower lowercases A-Z only. A JS regexp with `i` and no `u` folds
// ASCII letters only, where Go's (?i) would also fold U+017F and U+212A.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

// jsString is JS String(v) for a parsed JSON value. Callers handle
// `?? default` themselves, because String(null) is "null". raw is never
// empty: it is a value from a parsed document.
// shortcut: numbers keep their JSON text (JS would print 1e2 as 100); upgrade if a tool sends such numbers.
func jsString(raw json.RawMessage) string {
	switch raw[0] {
	case '"':
		var s string
		_ = json.Unmarshal(raw, &s) // raw came from a parsed document
		return s
	case '{':
		return "[object Object]"
	case '[':
		var items []json.RawMessage
		_ = json.Unmarshal(raw, &items)
		parts := make([]string, len(items))
		for i, it := range items {
			if string(it) != "null" { // Array.join writes null as ""
				parts[i] = jsString(it)
			}
		}
		return strings.Join(parts, ",")
	}
	return string(raw) // null, true, false, numbers
}

// inputString is String(o[key] ?? def).
func inputString(o *hookio.Object, key, def string) string {
	raw, ok := o.Get(key)
	if !ok || string(raw) == "null" {
		return def
	}
	return jsString(raw)
}

// responseText is the text TS extracted from: posttooluse.mjs:51-58 passed a
// string response as is and JSON.stringify(r ?? "") otherwise, so a missing
// or null response is the two characters `""`.
func responseText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return `""`
	}
	if raw[0] == '"' {
		return jsString(raw)
	}
	return compactJS(raw)
}

// compactJS is JSON.stringify(JSON.parse(raw)) for the key order and
// escaping: hookio.Object compacts values and writes U+2028, U+2029 raw.
// shortcut: other escapes (A) and number spellings stay as received; Claude Code writes them as JSON.stringify does.
func compactJS(raw json.RawMessage) string {
	o := &hookio.Object{}
	o.Set("v", raw)
	b, err := o.MarshalJSON()
	if err != nil {
		return string(raw)
	}
	return string(b[len(`{"v":`) : len(b)-1])
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/capture/ -run 'TestJSString|TestResponseText|TestJSTrimAndClasses' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/capture/js.go internal/capture/js_test.go
git commit -m "feat(capture): JS string and JSON-text helpers for the extractors"
```

### Task 25: File, rule, cwd, env, task, skill, subagent and worktree extractors

**Files:**
- Create: `internal/capture/tools.go`
- Test: `internal/capture/tools_test.go`

**Interfaces:**
- Consumes: `store.Event`; `hookio.ParseObject`, `(*hookio.Object).Object/String/MarshalJSON`, `hookio.QuoteString` (test); Task 24 helpers
- Produces: `type call struct{ tool string; input *hookio.Object; raw json.RawMessage; resp string; home string }`; `func ev(typ, cat string, prio int, data string) store.Event`; `func isRuleFile(path string) bool`; `func ruleContent(c call) string`; `func fileEvents(c call) []store.Event`, `cwdEvents`, `envEvents`, `taskEvents`, `skillEvents`, `subagentEvents`, `worktreeEvents` (same signature); test helpers `func newCall(t *testing.T, tool, input, resp string) call`, `func checkEvents(t *testing.T, name string, got, want []store.Event)`

- [ ] **Step 1: Write the failing test**

`internal/capture/tools_test.go`:
```go
package capture

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// newCall builds the extractor input the way PostToolUse does.
func newCall(t *testing.T, tool, input, resp string) call {
	t.Helper()
	in, err := hookio.ParseObject([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(resp)
	return call{tool: tool, input: in, raw: raw, resp: responseText(raw), home: "/home/test"}
}

func checkEvents(t *testing.T, name string, got, want []store.Event) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %+v\nwant %+v", name, got, want)
	}
}

func TestFileEvents(t *testing.T) {
	cases := []struct {
		name, tool, input, resp string
		want                    []store.Event
	}{
		{"read plain", "Read", `{"file_path":"/p/a.ts"}`, `"x"`,
			[]store.Event{{Type: "file_read", Category: "file", Priority: 1, Data: "/p/a.ts"}}},
		{"read rule, string response", "Read", `{"file_path":"/p/CLAUDE.md"}`, `"# rules"`, []store.Event{
			{Type: "rule", Category: "rule", Priority: 1, Data: "/p/CLAUDE.md"},
			{Type: "rule_content", Category: "rule", Priority: 1, Data: "# rules"},
			{Type: "file_read", Category: "file", Priority: 1, Data: "/p/CLAUDE.md"}}},
		// spec §3.4: the file text, not the stringified response object.
		{"read rule, CC object response", "Read", `{"file_path":"/p/.claude/x.md"}`,
			`{"type":"text","file":{"filePath":"/p/.claude/x.md","content":"body\n"}}`, []store.Event{
				{Type: "rule", Category: "rule", Priority: 1, Data: "/p/.claude/x.md"},
				{Type: "rule_content", Category: "rule", Priority: 1, Data: "body\n"},
				{Type: "file_read", Category: "file", Priority: 1, Data: "/p/.claude/x.md"}}},
		{"read rule, object without file.content", "Read", `{"file_path":"/p/GEMINI.md"}`, `{"type":"image"}`, []store.Event{
			{Type: "rule", Category: "rule", Priority: 1, Data: "/p/GEMINI.md"},
			{Type: "rule_content", Category: "rule", Priority: 1, Data: `{"type":"image"}`},
			{Type: "file_read", Category: "file", Priority: 1, Data: "/p/GEMINI.md"}}},
		{"read rule, empty file content", "Read", `{"file_path":"/p/KIRO.md"}`, `{"file":{"content":""}}`, []store.Event{
			{Type: "rule", Category: "rule", Priority: 1, Data: "/p/KIRO.md"},
			{Type: "file_read", Category: "file", Priority: 1, Data: "/p/KIRO.md"}}},
		// JS /i without /u folds ASCII only: U+212A KELVIN SIGN is not "k".
		{"no unicode case folding", "Read", `{"file_path":"/p/\u212aIRO.md"}`, `"x"`,
			[]store.Event{{Type: "file_read", Category: "file", Priority: 1, Data: "/p/\u212aIRO.md"}}},
		{"edit", "Edit", `{"file_path":"/p/a.ts"}`, `{}`,
			[]store.Event{{Type: "file_edit", Category: "file", Priority: 1, Data: "/p/a.ts"}}},
		{"notebook", "NotebookEdit", `{"notebook_path":"/p/n.ipynb"}`, `"ok"`,
			[]store.Event{{Type: "file_edit", Category: "file", Priority: 1, Data: "/p/n.ipynb"}}},
		{"write missing path", "Write", `{}`, `"ok"`,
			[]store.Event{{Type: "file_write", Category: "file", Priority: 1, Data: ""}}},
		{"glob", "Glob", `{"pattern":"**/*.ts"}`, `"a"`,
			[]store.Event{{Type: "file_glob", Category: "file", Priority: 3, Data: "**/*.ts"}}},
		{"grep no path", "Grep", `{"pattern":"foo"}`, `"a"`,
			[]store.Event{{Type: "file_search", Category: "file", Priority: 3, Data: "foo in "}}},
		{"other tool", "Bash", `{"command":"ls"}`, `"a"`, nil},
	}
	for _, c := range cases {
		checkEvents(t, c.name, fileEvents(newCall(t, c.tool, c.input, c.resp)), c.want)
	}
}

func TestBashSimpleEvents(t *testing.T) {
	cwd := func(d string) []store.Event {
		return []store.Event{{Type: "cwd", Category: "cwd", Priority: 2, Data: d}}
	}
	env := func(d string) []store.Event {
		return []store.Event{{Type: "env", Category: "env", Priority: 2, Data: d}}
	}
	cases := []struct {
		name, cmd string
		cwd, env  []store.Event
	}{
		{"cd plain", "cd /p/sub && git pull", cwd("/p/sub"), nil},
		{"cd double quotes", `cd "a b" && ls`, cwd("a b"), nil},
		{"cd after NBSP", "cd\u00a0/x", cwd("/x"), nil},
		{"abcd is not cd", "abcd /x", nil, nil},
		{"export masked", "export API_KEY=sk-123 && npm install", nil, env("export API_KEY=*** && npm install")},
		{"export stops at U+3000", "export T=abc\u3000x", nil, env("export T=***\u3000x")},
		{"activate", "source .venv/bin/activate", nil, env("source .venv/bin/activate")},
		{"pyenv", "pyenv local 3.12", nil, env("pyenv local 3.12")},
		{"go install", "go install ./...", nil, env("go install ./...")},
		{"not env", "npm test", nil, nil},
	}
	for _, c := range cases {
		in := newCall(t, "Bash", `{"command":`+string(hookio.QuoteString(c.cmd))+`}`, `{}`)
		checkEvents(t, c.name+" cwd", cwdEvents(in), c.cwd)
		checkEvents(t, c.name+" env", envEvents(in), c.env)
	}
	checkEvents(t, "cd on Read", cwdEvents(newCall(t, "Read", `{"command":"cd /x"}`, `""`)), nil)
}

func TestToolSpecificEvents(t *testing.T) {
	checkEvents(t, "task", taskEvents(newCall(t, "TaskCreate", `{"subject":"a <b> & c", "n":1.5,"z":null}`, `"ok"`)),
		[]store.Event{{Type: "task_create", Category: "task", Priority: 1, Data: `{"subject":"a <b> & c","n":1.5,"z":null}`}})
	checkEvents(t, "todo", taskEvents(newCall(t, "TodoWrite", `{"todos":[]}`, `"ok"`)),
		[]store.Event{{Type: "task", Category: "task", Priority: 1, Data: `{"todos":[]}`}})
	checkEvents(t, "skill", skillEvents(newCall(t, "Skill", `{"skill":"caveman"}`, `"ok"`)),
		[]store.Event{{Type: "skill", Category: "skill", Priority: 2, Data: "caveman"}})
	checkEvents(t, "agent object", subagentEvents(newCall(t, "Agent", `{"description":"d","prompt":"find X"}`, `{"isAsync":true}`)),
		[]store.Event{{Type: "subagent_completed", Category: "subagent", Priority: 2, Data: `[completed] find X → {"isAsync":true}`}})
	// A missing response is the text `""`, so TS never saw a launch.
	checkEvents(t, "agent no response", subagentEvents(newCall(t, "Agent", `{"description":"d"}`, ``)),
		[]store.Event{{Type: "subagent_completed", Category: "subagent", Priority: 2, Data: `[completed] d → ""`}})
	checkEvents(t, "agent empty string", subagentEvents(newCall(t, "Agent", `{"prompt":null,"description":"d"}`, `""`)),
		[]store.Event{{Type: "subagent_launched", Category: "subagent", Priority: 3, Data: "[launched] d"}})
	checkEvents(t, "worktree", worktreeEvents(newCall(t, "EnterWorktree", `{}`, `"ok"`)),
		[]store.Event{{Type: "worktree", Category: "env", Priority: 2, Data: "entered worktree: unnamed"}})
	checkEvents(t, "exit worktree dropped", worktreeEvents(newCall(t, "ExitWorktree", `{}`, `"ok"`)), nil)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/capture/ -run 'TestFileEvents|TestBashSimpleEvents|TestToolSpecificEvents' -v`
Expected: FAIL with "internal/capture/tools_test.go:13:54: undefined: call"

- [ ] **Step 3: Write minimal implementation**

`internal/capture/tools.go`:
```go
package capture

import (
	"encoding/json"
	"regexp"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// call is one PostToolUse input as the extractors see it.
type call struct {
	tool  string
	input *hookio.Object
	raw   json.RawMessage // tool_response as received; nil when absent
	resp  string          // the text TS extracted from (responseText)
	home  string
}

func ev(typ, cat string, prio int, data string) store.Event {
	return store.Event{Type: typ, Category: cat, Priority: prio, Data: data}
}

// ruleFile is the rule-file test of X:195-200, lowercased: it runs on
// asciiLower(path) because the TS regexps use `i` without `u`.
var ruleFile = []*regexp.Regexp{
	regexp.MustCompile(`(?:claude|agents(?:\.override)?|gemini|qwen|kiro)\.md$`),
	regexp.MustCompile(`/copilot-instructions\.md$`),
	regexp.MustCompile(`/context-mode\.mdc$`),
	regexp.MustCompile(`\.claude[\\/]`),
	regexp.MustCompile(`[\\/]memor(?:y|ies)[\\/][^\\/]+\.md$`),
}

func isRuleFile(path string) bool {
	lower := asciiLower(path)
	for _, re := range ruleFile {
		if re.MatchString(lower) {
			return true
		}
	}
	return false
}

// fileEvents ports extractFileAndRule (X:176-306) without apply_patch.
func fileEvents(c call) []store.Event {
	switch c.tool {
	case "Read":
		path := inputString(c.input, "file_path", "")
		var evs []store.Event
		if isRuleFile(path) {
			evs = append(evs, ev("rule", "rule", 1, path))
			if text := ruleContent(c); text != "" {
				evs = append(evs, ev("rule_content", "rule", 1, text))
			}
		}
		return append(evs, ev("file_read", "file", 1, path))
	case "Edit":
		return []store.Event{ev("file_edit", "file", 1, inputString(c.input, "file_path", ""))}
	case "NotebookEdit":
		return []store.Event{ev("file_edit", "file", 1, inputString(c.input, "notebook_path", ""))}
	case "Write":
		return []store.Event{ev("file_write", "file", 1, inputString(c.input, "file_path", ""))}
	case "Glob":
		return []store.Event{ev("file_glob", "file", 3, inputString(c.input, "pattern", ""))}
	case "Grep":
		data := inputString(c.input, "pattern", "") + " in " + inputString(c.input, "path", "")
		return []store.Event{ev("file_search", "file", 3, data)}
	}
	return nil
}

// ruleContent is the file text when Claude Code sent a FileReadOutput
// object, else the TS text (spec §3.4).
func ruleContent(c call) string {
	if top, err := hookio.ParseObject(c.raw); err == nil {
		if f, ok := top.Object("file"); ok {
			if s, ok := f.String("content"); ok {
				return s
			}
		}
	}
	return c.resp
}

var cdRe = regexp.MustCompile(`\bcd` + jsWS + `+("([^"]+)"|'([^']+)'|(` + jsNotWS + `+))`)

// cwdEvents ports extractCwd (X:312-327): the first cd in the command.
func cwdEvents(c call) []store.Event {
	if c.tool != "Bash" {
		return nil
	}
	m := cdRe.FindStringSubmatch(inputString(c.input, "command", ""))
	if m == nil {
		return nil
	}
	dir := m[2]
	if dir == "" {
		dir = m[3]
	}
	if dir == "" {
		dir = m[4]
	}
	return []store.Event{ev("cwd", "cwd", 2, dir)}
}

// envPatterns is ENV_PATTERNS (X:810-829).
var envPatterns = func() []*regexp.Regexp {
	srcs := []string{
		`\bsource` + jsWS + `+` + jsNotWS + `*activate\b`,
		`\bexport` + jsWS + `+\w+=`,
		`\bnvm` + jsWS + `+use\b`,
		`\bpyenv` + jsWS + `+(shell|local|global)\b`,
		`\bconda` + jsWS + `+activate\b`,
		`\brbenv` + jsWS + `+(shell|local|global)\b`,
		`\bnpm` + jsWS + `+install\b`,
		`\bnpm` + jsWS + `+ci\b`,
		`\bpip` + jsWS + `+install\b`,
		`\bbun` + jsWS + `+install\b`,
		`\byarn` + jsWS + `+(add|install)\b`,
		`\bpnpm` + jsWS + `+(add|install)\b`,
		`\bcargo` + jsWS + `+(install|add)\b`,
		`\bgo` + jsWS + `+(install|get)\b`,
		`\brustup\b`,
		`\basdf\b`,
		`\bvolta\b`,
		`\bdeno` + jsWS + `+install\b`,
	}
	res := make([]*regexp.Regexp, len(srcs))
	for i, s := range srcs {
		res[i] = regexp.MustCompile(s)
	}
	return res
}()

var exportRe = regexp.MustCompile(`\bexport` + jsWS + `+(\w+)=` + jsNotWS + `*`)

// envEvents ports extractEnv (X:831-847), masking export values.
func envEvents(c call) []store.Event {
	if c.tool != "Bash" {
		return nil
	}
	cmd := inputString(c.input, "command", "")
	for _, re := range envPatterns {
		if re.MatchString(cmd) {
			return []store.Event{ev("env", "env", 2, exportRe.ReplaceAllString(cmd, "export ${1}=***"))}
		}
	}
	return nil
}

// taskEvents ports extractTask (X:653-668): data is JSON.stringify(tool_input).
func taskEvents(c call) []store.Event {
	types := map[string]string{"TodoWrite": "task", "TaskCreate": "task_create", "TaskUpdate": "task_update"}
	typ, ok := types[c.tool]
	if !ok {
		return nil
	}
	b, err := c.input.MarshalJSON()
	if err != nil {
		return nil
	}
	return []store.Event{ev(typ, "task", 1, string(b))}
}

// skillEvents ports extractSkill (X:853-863).
func skillEvents(c call) []store.Event {
	if c.tool != "Skill" {
		return nil
	}
	return []store.Event{ev("skill", "skill", 2, inputString(c.input, "skill", ""))}
}

// subagentEvents ports extractSubagent (X:900-915).
func subagentEvents(c call) []store.Event {
	if c.tool != "Agent" {
		return nil
	}
	prompt := inputString(c.input, "prompt", inputString(c.input, "description", ""))
	if c.resp != "" {
		return []store.Event{ev("subagent_completed", "subagent", 2, "[completed] "+prompt+" → "+c.resp)}
	}
	return []store.Event{ev("subagent_launched", "subagent", 3, "[launched] "+prompt)}
}

// worktreeEvents ports the EnterWorktree half of extractWorktree (X:1243-1252).
func worktreeEvents(c call) []store.Event {
	if c.tool != "EnterWorktree" {
		return nil
	}
	return []store.Event{ev("worktree", "env", 2, "entered worktree: "+inputString(c.input, "name", "unnamed"))}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/capture/ -run 'TestFileEvents|TestBashSimpleEvents|TestToolSpecificEvents' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/capture/tools.go internal/capture/tools_test.go
git commit -m "feat(capture): port file/rule, cwd, env, task, skill, subagent and worktree extractors"
```

### Task 26: Git extractor with the subcommand lookup fix

**Files:**
- Create: `internal/capture/git.go`
- Test: `internal/capture/git_test.go`

**Interfaces:**
- Consumes: Task 24 `jsWS`, `jsNotWS`, `inputString`; Task 25 `call`, `ev`, `checkEvents`
- Produces: `func gitEvents(c call) []store.Event`; `func parseGit(cmd string) (dir, sub string)`; `func expandHome(p, home string) string`; `func tokenize(cmd string) []string`; `func commitMessage(cmd string) string`; `func isEnvAssignment(t string) bool`; `func isRunner(t string) bool`; `func lowerRun(s string) bool`

- [ ] **Step 1: Write the failing test**

`internal/capture/git_test.go`:
```go
package capture

import (
	"reflect"
	"testing"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`echo a"b c"d`, []string{"echo", "ab cd"}},
		{"a\t 'b\\'c' \\ d", []string{"a", "b'c", " d"}},
		{"x\ny z", []string{"x\ny", "z"}}, // only space and tab separate
		{`unterminated "q r`, []string{"unterminated", "q r"}},
		{`\é`, []string{"é"}}, // a backslash keeps the whole next character
		{"  ", nil},
	}
	for _, c := range cases {
		if got := tokenize(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("tokenize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGitEvents(t *testing.T) {
	cwd := func(d string) store.Event { return ev("cwd", "cwd", 2, d) }
	git := func(op string) store.Event { return ev("git", "git", 2, op) }
	commit := func(m string) store.Event { return ev("git_commit", "git", 2, m) }
	cases := []struct {
		cmd, home string
		want      []store.Event
	}{
		// spec §3.4 fix: TS matched the subcommand against the op label and lost these.
		{"git -C /x checkout main", "/h", []store.Event{cwd("/x"), git("branch")}},
		{"git --no-pager checkout -b f", "/h", []store.Event{git("branch")}},
		{"git checkout main", "/h", []store.Event{git("branch")}},
		{"git -C /x merge dev", "/h", []store.Event{cwd("/x"), git("merge")}},
		{"git -C ~/repos/r status", "/h", []store.Event{cwd("/h/repos/r"), git("status")}},
		{"git -C ~ status", "/h", []store.Event{cwd("/h"), git("status")}},
		{"git -C ~/r status", "", []store.Event{cwd("~/r"), git("status")}}, // no home: TS expands to "~"
		{"git --directory=~other/foo status", "/h", []store.Event{cwd("~other/foo"), git("status")}},
		{`git -C "" status`, "/h", []store.Event{git("status")}},
		{"FOO=1 sudo git --no-pager log -n 3", "/h", []store.Event{git("log")}},
		{"/usr/bin/git stash pop", "/h", []store.Event{git("stash")}},
		{"cd /r && git status", "/h", []store.Event{git("status")}},
		{"git frob && git status", "/h", []store.Event{git("status")}},
		{"git status\ngit log", "/h", []store.Event{git("log")}}, // table order, not position
		{"cd /r && git merge", "/h", nil},
		{"git frob", "/h", nil},
		{"ls", "/h", nil},
		{`git commit -m "feat: x"`, "/h", []store.Event{commit("feat: x")}},
		{`git commit -am "stage+commit"`, "/h", []store.Event{commit("stage+commit")}},
		{"git commit --message=longform", "/h", []store.Event{commit("longform")}},
		{"git commit --message= -m later", "/h", []store.Event{commit("later")}}, // an empty --message= is skipped
		{"git commit --message 'two words'", "/h", []store.Event{commit("two words")}},
		{"git commit --amend --no-edit", "/h", []store.Event{git("commit")}},
		{`git commit -m ""`, "/h", []store.Event{git("commit")}},
		{`git commit -m "say \"hi\"" && git push`, "/h", []store.Event{commit(`say "hi"`)}},
		{"git -C /abs/repo commit -m x", "/h", []store.Event{cwd("/abs/repo"), commit("x")}},
	}
	for _, c := range cases {
		in := call{tool: "Bash", input: &hookio.Object{}, home: c.home}
		in.input.SetString("command", c.cmd)
		checkEvents(t, c.cmd, gitEvents(in), c.want)
	}
	if evs := gitEvents(call{tool: "Read", input: &hookio.Object{}}); evs != nil {
		t.Errorf("Read: %v", evs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/capture/ -run 'TestTokenize|TestGitEvents' -v`
Expected: FAIL with "internal/capture/git_test.go:24:13: undefined: tokenize"

- [ ] **Step 3: Write minimal implementation**

`internal/capture/git.go`:
```go
package capture

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/lang315/context-mode-go/internal/store"
)

func gitRe(rest string) *regexp.Regexp {
	return regexp.MustCompile(`\bgit` + jsWS + `+` + rest)
}

// gitOps is GIT_PATTERNS (X:352-371) in priority order, plus the subcommand
// each entry stands for. TS looked the parsed subcommand up by op label, so
// `checkout` (label "branch") was never found (spec §3.4).
var gitOps = []struct {
	sub, op string
	re      *regexp.Regexp
}{
	{"checkout", "branch", gitRe(`checkout\b`)},
	{"commit", "commit", gitRe(`commit\b`)},
	{"merge", "merge", gitRe(`merge` + jsWS + `+` + jsNotWS + `+`)},
	{"rebase", "rebase", gitRe(`rebase\b`)},
	{"stash", "stash", gitRe(`stash\b`)},
	{"push", "push", gitRe(`push\b`)},
	{"pull", "pull", gitRe(`pull\b`)},
	{"log", "log", gitRe(`log\b`)},
	{"diff", "diff", gitRe(`diff\b`)},
	{"status", "status", gitRe(`status\b`)},
	{"branch", "branch", gitRe(`branch\b`)},
	{"reset", "reset", gitRe(`reset\b`)},
	{"add", "add", gitRe(`add\b`)},
	{"cherry-pick", "cherry-pick", gitRe(`cherry-pick\b`)},
	{"tag", "tag", gitRe(`tag\b`)},
	{"fetch", "fetch", gitRe(`fetch\b`)},
	{"clone", "clone", gitRe(`clone\b`)},
	{"worktree", "worktree", gitRe(`worktree\b`)},
}

// gitEvents ports extractGit (X:373-440): the parsed subcommand first, then
// the first table regex that matches anywhere (so `cd /r && git status`
// still counts). A -C dir becomes a leading cwd event.
func gitEvents(c call) []store.Event {
	if c.tool != "Bash" {
		return nil
	}
	cmd := inputString(c.input, "command", "")
	dir, sub := parseGit(cmd)
	op := ""
	for _, g := range gitOps {
		if sub != "" && g.sub == sub {
			op = g.op
			break
		}
	}
	if op == "" {
		for _, g := range gitOps {
			if g.re.MatchString(cmd) {
				op = g.op
				break
			}
		}
	}
	if op == "" {
		return nil
	}
	var evs []store.Event
	if dir != "" {
		evs = append(evs, ev("cwd", "cwd", 2, expandHome(dir, c.home)))
	}
	if op == "commit" {
		if msg := commitMessage(cmd); msg != "" {
			return append(evs, ev("git_commit", "git", 2, msg))
		}
	}
	return append(evs, ev("git", "git", 2, op))
}

// parseGit ports parseGitInvocation (X:483-527). It returns the last -C /
// --directory value and the first bare token after git; "" means none
// (TS null, and an empty dir is falsy there too).
func parseGit(cmd string) (dir, sub string) {
	toks := tokenize(cmd)
	i := 0
	for i < len(toks) && isEnvAssignment(toks[i]) {
		i++
	}
	isGit := func(t string) bool { return t == "git" || strings.HasSuffix(t, "/git") }
	for i < len(toks) && !isGit(toks[i]) && isRunner(toks[i]) {
		i++
	}
	if i >= len(toks) || !isGit(toks[i]) {
		return "", ""
	}
	for i++; i < len(toks); i++ {
		t := toks[i]
		switch {
		case t == "-C" || t == "--directory":
			dir = ""
			if i+1 < len(toks) {
				dir = toks[i+1]
			}
			i++
		case strings.HasPrefix(t, "--directory="):
			dir = strings.TrimPrefix(t, "--directory=")
		case strings.HasPrefix(t, "-"):
		default:
			return dir, t
		}
	}
	return dir, ""
}

// expandHome ports expandHomeTilde (X:458-463): "~" and "~/x" only. With no
// home TS expanded to "~", which leaves the path unchanged.
func expandHome(p, home string) string {
	if home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return home + p[1:]
	}
	return p
}

// isEnvAssignment ports X:529-547: [A-Z_][A-Z0-9_]*= at the start.
func isEnvAssignment(t string) bool {
	for j := 0; j < len(t); j++ {
		c := t[j]
		switch {
		case j == 0:
			if !(c >= 'A' && c <= 'Z' || c == '_') {
				return false
			}
		case c == '=':
			return true
		case !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'):
			return false
		}
	}
	return false
}

func isRunner(t string) bool {
	switch t {
	case "sudo", "doas", "env", "exec", "time":
		return true
	}
	return false
}

// tokenize ports tokenizeCommand (X:567-601): space and tab separate,
// quotes group, a backslash takes the next character literally (inside
// either quote too), and adjacent pieces merge.
func tokenize(cmd string) []string {
	var toks []string
	n := len(cmd)
	// next returns the full character at i, so an escaped multi-byte
	// character stays whole (JS takes it one UTF-16 unit at a time).
	next := func(i int) string {
		_, size := utf8.DecodeRuneInString(cmd[i:])
		return cmd[i : i+size]
	}
	i := 0
	for i < n {
		for i < n && (cmd[i] == ' ' || cmd[i] == '\t') {
			i++
		}
		if i >= n {
			break
		}
		var buf strings.Builder
		for i < n && cmd[i] != ' ' && cmd[i] != '\t' {
			ch := cmd[i]
			switch {
			case ch == '"' || ch == '\'':
				i++
				for i < n && cmd[i] != ch {
					if cmd[i] == '\\' && i+1 < n {
						i++
					}
					s := next(i)
					buf.WriteString(s)
					i += len(s)
				}
				if i < n {
					i++ // closing quote
				}
			case ch == '\\' && i+1 < n:
				s := next(i + 1)
				buf.WriteString(s)
				i += 1 + len(s)
			default:
				buf.WriteByte(ch)
				i++
			}
		}
		toks = append(toks, buf.String())
	}
	return toks
}

// commitMessage ports extractCommitMessageFromCommand (X:609-638): the
// value of the first --message=V, --message V or -[a-z]*m V; "" for none.
func commitMessage(cmd string) string {
	argv := tokenize(cmd)
	arg := func(i int) string {
		if i < len(argv) {
			return argv[i]
		}
		return ""
	}
	for i, a := range argv {
		switch {
		case len(a) > len("--message=") && strings.HasPrefix(a, "--message="):
			return strings.TrimPrefix(a, "--message=")
		case a == "--message":
			return arg(i + 1)
		case len(a) >= 2 && a[0] == '-' && a[1] != '-' && a[len(a)-1] == 'm' && lowerRun(a[1:]):
			return arg(i + 1)
		}
	}
	return ""
}

func lowerRun(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'a' || s[i] > 'z' {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/capture/ -run 'TestTokenize|TestGitEvents' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/capture/git.go internal/capture/git_test.go
git commit -m "feat(capture): port the git extractor and match git -C subcommands"
```

### Task 27: Plan extractor and FNV-1a plan hash

**Files:**
- Create: `internal/capture/plan.go`
- Test: `internal/capture/plan_test.go`

**Interfaces:**
- Consumes: `hookio.ParseObject`, `hookio.JSLen`; Task 24 `jsString`, `inputString`, `asciiLower`; Task 25 `call`, `ev`, `newCall`, `checkEvents`
- Produces: `func planEvents(c call) []store.Event`; `func exitDetail(c call) string`; `func fnv1a32(s string) string`

- [ ] **Step 1: Write the failing test**

`internal/capture/plan_test.go`:
```go
package capture

import (
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

func TestFNV1a32(t *testing.T) {
	// From the TS bundle: plan_hash for these plans.
	for in, want := range map[string]string{"Refactor auth": "fd047da3", "计划😀": "c89e473f", "p": "f50c43ef", "": "811c9dc5"} {
		if got := fnv1a32(in); got != want {
			t.Errorf("fnv1a32(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestPlanEvents(t *testing.T) {
	cases := []struct {
		name, tool, input, resp string
		want                    []store.Event
	}{
		{"enter", "EnterPlanMode", `{}`, `"ok"`, []store.Event{ev("plan_enter", "plan", 2, "entered plan mode")}},
		{"exit approved", "ExitPlanMode", `{"plan":"Refactor auth","allowedPrompts":[{"prompt":"Edit src/auth.ts"},"run tests"]}`,
			`"User has approved your plan"`, []store.Event{
				ev("plan_exit", "plan", 2, "exited plan mode (allowed: Edit src/auth.ts, run tests) plan_bytes:13 plan_hash:fd047da3"),
				ev("plan_approved", "plan", 1, "plan approved by user")}},
		{"exit, plan from response, odd prompts", "ExitPlanMode",
			`{"allowedPrompts":[null,3,{"prompt":null},{"tool":"Bash"},["a",null]]}`,
			`{"plan":"计划😀","isAgent":false,"note":"User approved"}`, []store.Event{
				ev("plan_exit", "plan", 2, "exited plan mode (allowed: null, 3, null, [object Object], a,) plan_bytes:4 plan_hash:c89e473f"),
				ev("plan_approved", "plan", 1, "plan approved by user")}},
		{"exit, empty plans", "ExitPlanMode", `{"plan":"","allowedPrompts":[]}`, `"{\"plan\":\"\"}"`,
			[]store.Event{ev("plan_exit", "plan", 2, "exited plan mode")}},
		{"exit declined", "ExitPlanMode", `{"plan":"p"}`, `"User DECLINED"`, []store.Event{
			ev("plan_exit", "plan", 2, "exited plan mode plan_bytes:1 plan_hash:f50c43ef"),
			ev("plan_rejected", "plan", 2, "plan rejected: User DECLINED")}},
		{"exit rejected, no plan", "ExitPlanMode", `{}`, `"User rejected the plan"`, []store.Event{
			ev("plan_exit", "plan", 2, "exited plan mode"),
			ev("plan_rejected", "plan", 2, "plan rejected: User rejected the plan")}},
		{"write plan file", "Write", `{"file_path":"/home/u/.claude/plans/my-plan.md"}`, `"ok"`,
			[]store.Event{ev("plan_file_write", "plan", 2, "plan file: my-plan.md")}},
		{"edit plan file, backslashes", "Edit", `{"file_path":"C:\\u\\.claude\\plans\\p.md"}`, `"ok"`,
			[]store.Event{ev("plan_file_write", "plan", 2, "plan file: p.md")}},
		{"write other file", "Write", `{"file_path":"/p/.claude/x.md"}`, `"ok"`, nil},
		{"read plan file", "Read", `{"file_path":"/u/.claude/plans/p.md"}`, `"ok"`, nil},
	}
	for _, c := range cases {
		checkEvents(t, c.name, planEvents(newCall(t, c.tool, c.input, c.resp)), c.want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/capture/ -run 'TestFNV1a32|TestPlanEvents' -v`
Expected: FAIL with "internal/capture/plan_test.go:12:13: undefined: fnv1a32"

- [ ] **Step 3: Write minimal implementation**

`internal/capture/plan.go`:
```go
package capture

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

var planPath = regexp.MustCompile(`(?:^|[/\\])\.claude[/\\]plans[/\\]`)

// planEvents ports extractPlan (X:714-803) without apply_patch.
func planEvents(c call) []store.Event {
	switch c.tool {
	case "EnterPlanMode":
		return []store.Event{ev("plan_enter", "plan", 2, "entered plan mode")}
	case "ExitPlanMode":
		evs := []store.Event{ev("plan_exit", "plan", 2, exitDetail(c))}
		lower := asciiLower(c.resp) // the words are ASCII; no other letter lowercases into them
		switch {
		case strings.Contains(lower, "approve"): // also covers "approved"
			evs = append(evs, ev("plan_approved", "plan", 1, "plan approved by user"))
		case strings.Contains(lower, "rejected") || strings.Contains(lower, "decline") || strings.Contains(lower, "denied"):
			evs = append(evs, ev("plan_rejected", "plan", 2, "plan rejected: "+c.resp))
		}
		return evs
	case "Write", "Edit":
		path := inputString(c.input, "file_path", "")
		if planPath.MatchString(path) {
			return []store.Event{ev("plan_file_write", "plan", 2, "plan file: "+path[strings.LastIndexAny(path, `/\`)+1:])}
		}
	}
	return nil
}

// exitDetail is the plan_exit text: allowed prompts, then the plan's UTF-16
// length and hash (X:727-744).
func exitDetail(c call) string {
	detail := "exited plan mode"
	if raw, ok := c.input.Get("allowedPrompts"); ok {
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) == nil && len(items) > 0 {
			parts := make([]string, len(items))
			for i, it := range items {
				parts[i] = jsString(it)
				if o, err := hookio.ParseObject(it); err == nil && o.Has("prompt") {
					p, _ := o.Get("prompt")
					parts[i] = jsString(p)
				}
			}
			detail += " (allowed: " + strings.Join(parts, ", ") + ")"
		}
	}
	plan, _ := c.input.String("plan")
	if plan == "" { // extractExitPlanText: else the response's plan field
		if top, err := hookio.ParseObject([]byte(c.resp)); err == nil {
			plan, _ = top.String("plan")
		}
	}
	if plan != "" {
		detail += fmt.Sprintf(" plan_bytes:%d plan_hash:%s", hookio.JSLen(plan), fnv1a32(plan))
	}
	return detail
}

// fnv1a32 ports fnv1a32Hex (X:685-692): FNV-1a over UTF-16 code units.
func fnv1a32(s string) string {
	h := uint32(0x811c9dc5)
	for _, u := range utf16.Encode([]rune(s)) {
		h ^= uint32(u)
		h *= 0x01000193
	}
	return fmt.Sprintf("%08x", h)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/capture/ -run 'TestFNV1a32|TestPlanEvents' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/capture/plan.go internal/capture/plan_test.go
git commit -m "feat(capture): port the plan extractor with UTF-16 plan hash"
```

### Task 28: AskUserQuestion decision extractor

**Files:**
- Create: `internal/capture/decision.go`
- Test: `internal/capture/decision_test.go`

**Interfaces:**
- Consumes: `hookio.ParseObject`, `(*hookio.Object).Get/Object/Keys`; Task 24 `inputString`; Task 25 `call`, `ev`, `newCall`, `checkEvents`
- Produces: `func decisionEvents(c call) []store.Event`; `func answerText(resp, question string) string`; `func answerValue(raw json.RawMessage) string`

- [ ] **Step 1: Write the failing test**

`internal/capture/decision_test.go`:
```go
package capture

import (
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

func TestDecisionEvents(t *testing.T) {
	cases := []struct{ name, input, resp, want string }{
		{"CC object response", `{"questions":[{"question":"Which DB?","options":[]}]}`,
			`{"questions":[],"answers":{"Which DB?":"SQLite"}}`, "Q: Which DB? → A: SQLite"},
		{"string response", `{"questions":[{"question":"Which DB?"}]}`,
			`"{\"answers\":{\"Which DB?\":\"SQLite\"}}"`, "Q: Which DB? → A: SQLite"},
		{"multi-select keeps strings", `{"questions":[{"question":"Pick?"}]}`,
			`{"answers":{"Pick?":["a",1,"b"]}}`, "Q: Pick? → A: a | b"},
		{"fallback joins non-empty answers", `{"questions":[{"question":"Q1"}]}`,
			`{"answers":{"other":"x","empty":"","list":["y"]}}`, "Q: Q1 → A: x | y"},
		{"answers array", `{"questions":[{"question":"Q1"}]}`,
			`{"answers":["x",["y","z"]]}`, "Q: Q1 → A: x | y | z"},
		{"non-JSON response", `{"questions":[{"question":"Q"}]}`, `"not json"`, "Q: Q → A: "},
		{"no questions", `{}`, `{"answers":{"a":"b"}}`, "answer: b"},
		{"question not an object", `{"questions":["Q"]}`, `{"answers":{"a":"b"}}`, "answer: b"},
	}
	for _, c := range cases {
		got := decisionEvents(newCall(t, "AskUserQuestion", c.input, c.resp))
		checkEvents(t, c.name, got, []store.Event{ev("decision_question", "decision", 2, c.want)})
	}
	checkEvents(t, "other tool", decisionEvents(newCall(t, "Bash", `{}`, `"x"`)), nil)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/capture/ -run 'TestDecisionEvents' -v`
Expected: FAIL with "internal/capture/decision_test.go:26:10: undefined: decisionEvents"

- [ ] **Step 3: Write minimal implementation**

`internal/capture/decision.go`:
```go
package capture

import (
	"encoding/json"
	"strings"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// decisionEvents ports extractDecision (X:1096-1152): the first question and
// its answer from the response's answers map.
func decisionEvents(c call) []store.Event {
	if c.tool != "AskUserQuestion" {
		return nil
	}
	question := ""
	if raw, ok := c.input.Get("questions"); ok {
		var qs []json.RawMessage
		if json.Unmarshal(raw, &qs) == nil && len(qs) > 0 {
			if q, err := hookio.ParseObject(qs[0]); err == nil {
				question = inputString(q, "question", "")
			}
		}
	}
	answer := answerText(c.resp, question)
	data := "answer: " + answer
	if question != "" {
		data = "Q: " + question + " → A: " + answer
	}
	return []store.Event{ev("decision_question", "decision", 2, data)}
}

// answerText is the answer to question, else every non-empty answer joined
// with " | ". A response that is not JSON gives "".
// shortcut: Object.values puts integer-like keys first; insertion order is used. Upgrade if answers get numeric keys.
func answerText(resp, question string) string {
	top, err := hookio.ParseObject([]byte(resp))
	if err != nil {
		return ""
	}
	raw, ok := top.Get("answers")
	if !ok {
		return ""
	}
	var values []json.RawMessage
	if answers, ok := top.Object("answers"); ok {
		if v, ok := answers.Get(question); ok && question != "" {
			if s := answerValue(v); s != "" {
				return s
			}
		}
		for _, k := range answers.Keys() {
			v, _ := answers.Get(k)
			values = append(values, v)
		}
	} else if json.Unmarshal(raw, &values) != nil {
		return "" // a string or number has no answers
	}
	var out []string
	for _, v := range values {
		if s := answerValue(v); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, " | ")
}

// answerValue is toAnswerText: a string, or the strings of an array joined.
func answerValue(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return ""
	}
	var out []string
	for _, it := range items {
		if json.Unmarshal(it, &s) == nil {
			out = append(out, s)
		}
	}
	return strings.Join(out, " | ")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/capture/ -run 'TestDecisionEvents' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/capture/decision.go internal/capture/decision_test.go
git commit -m "feat(capture): port the AskUserQuestion decision extractor"
```

### Task 29: UserPrompt: skip rules, user_prompt and the user extractors

**Files:**
- Create: `internal/capture/user.go`
- Test: `internal/capture/user_test.go`

**Interfaces:**
- Consumes: `hookio.JSPrefix`, `hookio.RuneLen`; Task 24 `jsTrim`, `jsWS`, `jsNotWS`; Task 25 `ev`, `checkEvents`
- Produces: `func UserPrompt(prompt string) []store.Event`; `func userPlan(msg string) []store.Event`, `userDecision`, `userRole`, `userIntent`, `userGoal`, `userBlocker` (same signature); `func hasRoleCue(first string) bool`

- [ ] **Step 1: Write the failing test**

`internal/capture/user_test.go`:
```go
package capture

import (
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

func TestUserPromptSkips(t *testing.T) {
	for _, p := range []string{"", " \n\u3000", "<task-notification>x", "  <system-reminder>x", "<context_guidance>", "<tool-result>y"} {
		if evs := UserPrompt(p); evs != nil {
			t.Errorf("UserPrompt(%q) = %v, want nil", p, evs)
		}
	}
}

func TestUserPrompt(t *testing.T) {
	prompt := func(p string) store.Event { return ev("user_prompt", "user-prompt", 1, p) }
	intent := func(m string) store.Event { return ev("intent", "intent", 4, m) }
	cases := []struct {
		in   string
		want []store.Event
	}{
		// The stored prompt is raw; the extractors see it trimmed.
		{"  fix the login bug \n", []store.Event{prompt("  fix the login bug \n"), intent("implement")}},
		{"why does the build fail?", []store.Event{prompt("why does the build fail?"), intent("investigate")}},
		{"\t/plan\targ  ", []store.Event{prompt("\t/plan\targ  "), ev("plan_enter", "plan", 2, "plan via /plan slash: arg"), intent("implement")}},
		{"/plans x", []store.Event{prompt("/plans x"), intent("implement")}},
		{"/plan " + strings.Repeat("é", 130), []store.Event{prompt("/plan " + strings.Repeat("é", 130)),
			ev("plan_enter", "plan", 2, "plan via /plan slash: "+strings.Repeat("é", 120))}},
		{"不要用redis，用sqlite做缓存层就好了吧好吗", []store.Event{prompt("不要用redis，用sqlite做缓存层就好了吧好吗"),
			ev("decision", "decision", 2, "不要用redis，用sqlite做缓存层就好了吧好吗"), intent("implement")}},
		{"please always respond in English", []store.Event{prompt("please always respond in English"),
			ev("role", "role", 3, "please always respond in English"), intent("implement")}},
		{"あなたは経験豊富なエンジニアです。", []store.Event{prompt("あなたは経験豊富なエンジニアです。"),
			ev("role", "role", 3, "あなたは経験豊富なエンジニアです。"), intent("implement")}},
		{"That's fine for now", []store.Event{prompt("That's fine for now"), intent("implement")}},
		{"ok, you are a cat", []store.Event{prompt("ok, you are a cat"), ev("decision", "decision", 2, "ok, you are a cat"), intent("implement")}},
		{"/goal\nmulti\nline", []store.Event{prompt("/goal\nmulti\nline"), intent("implement"), ev("goal", "goal", 4, "multi\nline")}},
		{"GOAL:   x ", []store.Event{prompt("GOAL:   x "), intent("implement"), ev("goal", "goal", 4, "x")}},
		{"Objective :  ", []store.Event{prompt("Objective :  "), intent("implement")}},
		{"at Object.<anonymous> (/a/b.js:10:5)", []store.Event{prompt("at Object.<anonymous> (/a/b.js:10:5)"),
			intent("implement"), ev("blocker", "blocked-on", 2, "at Object.<anonymous> (/a/b.js:10:5)")}},
		{"TypeError: boom", []store.Event{prompt("TypeError: boom"), intent("implement")}}, // \b needs a word start
		{"✅ Error: gone", []store.Event{prompt("✅ Error: gone"), intent("implement"), ev("blocker_resolved", "blocked-on", 2, "✅ Error: gone")}},
		{strings.Repeat("a", 59), []store.Event{prompt(strings.Repeat("a", 59)), intent("implement")}},
		{strings.Repeat("a", 60), []store.Event{prompt(strings.Repeat("a", 60))}},
		{strings.Repeat("x", 1030), []store.Event{prompt(strings.Repeat("x", 1030))}}, // the data event is dropped
		{"1234 5678", []store.Event{prompt("1234 5678")}},
	}
	for _, c := range cases {
		checkEvents(t, c.in, UserPrompt(c.in), c.want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/capture/ -run 'TestUserPrompt' -v`
Expected: FAIL with "internal/capture/user_test.go:12:13: undefined: UserPrompt"

- [ ] **Step 3: Write minimal implementation**

`internal/capture/user.go`:
```go
package capture

import (
	"regexp"
	"strings"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// systemPrefixes mark prompts Claude Code generated (userpromptsubmit.mjs:40-43).
var systemPrefixes = []string{"<task-notification>", "<system-reminder>", "<context_guidance>", "<tool-result>"}

// UserPrompt ports userpromptsubmit.mjs:36-71: nil for a blank or system
// prompt, else user_prompt with the raw prompt, then extractUserEvents
// (X:2758-2774) on the trimmed prompt, without extractData.
func UserPrompt(prompt string) []store.Event {
	msg := jsTrim(prompt)
	if msg == "" {
		return nil
	}
	for _, p := range systemPrefixes {
		if strings.HasPrefix(msg, p) {
			return nil
		}
	}
	evs := []store.Event{ev("user_prompt", "user-prompt", 1, prompt)}
	for _, x := range []func(string) []store.Event{userPlan, userDecision, userRole, userIntent, userGoal, userBlocker} {
		evs = append(evs, x(msg)...)
	}
	return evs
}

// userPlan ports extractUserPlan (X:2929-2960): "/plan" after spaces or
// tabs, then the end or a space, tab, CR or LF. The checks are ASCII, so
// bytes give the same answer as UTF-16 units.
func userPlan(msg string) []store.Event {
	rest := strings.TrimLeft(msg, " \t")
	if !strings.HasPrefix(rest, "/plan") {
		return nil
	}
	rest = rest[len("/plan"):]
	if rest != "" && !strings.ContainsRune(" \t\n\r", rune(rest[0])) {
		return nil
	}
	detail := "plan via /plan slash"
	if arg := jsTrim(rest); arg != "" {
		detail += ": " + hookio.JSPrefix(arg, 120)
	}
	return []store.Event{ev("plan_enter", "plan", 2, detail)}
}

var (
	questionMark   = regexp.MustCompile(`[?？؟¿]`)
	letter         = regexp.MustCompile(`\pL`)
	clauseSep      = regexp.MustCompile(`[,;，；、،]`)
	twoWords       = regexp.MustCompile(`\pL+` + jsWS + `+\pL+`)
	letterRun      = regexp.MustCompile(`\pL{6,}`)
	goalDirective  = regexp.MustCompile(`(?is)^(?:/goal` + jsWS + `+|(?:goal|objective)` + jsWS + `*:` + jsWS + `*)(.+)$`)
	resolvedMark   = regexp.MustCompile(`[✓✔✅☑🎉]`)
	resolvedPrefix = regexp.MustCompile(`(?i)^` + jsWS + `*(?:fixed|resolved)` + jsWS + `*:`)
	blockerMarkers = regexp.MustCompile(`(?:\bError` + jsWS + `*:|\bException` + jsWS + `*:|\bTraceback\b|\bat` +
		jsWS + `+` + jsNotWS + `+` + jsWS + `*\([^)]*:\d+:\d+\))`)
)

// userDecision ports extractUserDecision (X:2218-2240): a non-question with
// a letter, a clause separator and 15..500 code points.
func userDecision(msg string) []store.Event {
	n := hookio.RuneLen(msg)
	if questionMark.MatchString(msg) || !letter.MatchString(msg) || !clauseSep.MatchString(msg) || n < 15 || n > 500 {
		return nil
	}
	return []store.Event{ev("decision", "decision", 2, msg)}
}

// Role cue lists, verbatim from X:2294-2320; the trailing spaces matter.
var (
	roleFillers  = map[string]bool{"ok": true, "okay": true, "sure": true, "yeah": true, "yep": true, "yup": true, "alright": true, "fine": true, "well": true, "so": true, "hmm": true, "right": true, "please": true}
	rolePrefixes = []string{
		"you are", "you're", "your role", "you will be", "you act", "you will act",
		"act as", "act like", "behave as", "behave like", "imagine you", "pretend you",
		"assume the role", "take the role", "play the role", "respond as",
		"tu es", "tu est", "vous etes", "vous êtes",
		"sen ", "siz ",
		"eres ", "tú eres", "usted es",
		"ты ", "вы ",
		"あなたは", "君は", "お前は", "あなたが",
		"你是", "您是",
		"तुम ", "आप ", "तू ",
		"أنت ", "انت ", "أنتَ ",
		// ROLE_DIRECTIVE_PREFIXES
		"always ", "never ", "respond ", "reply ", "answer ", "speak ",
		"write ", "prefer ", "format ", "output ", "communicate ", "use only ",
	}
)

// userRole ports extractRole (X:2341-2374), tested on the first clause.
func userRole(msg string) []store.Event {
	first := msg
	if i := strings.IndexAny(msg, ".!\n。！"); i >= 0 {
		first = msg[:i]
	}
	first = jsTrim(first)
	n := hookio.RuneLen(first)
	if questionMark.MatchString(first) || clauseSep.MatchString(first) || !letter.MatchString(first) || n < 8 || n > 120 {
		return nil
	}
	if !hasRoleCue(first) || !(twoWords.MatchString(first) || letterRun.MatchString(first)) {
		return nil
	}
	return []store.Event{ev("role", "role", 3, msg)}
}

// hasRoleCue ports X:2322-2339: drop leading filler words, then match a
// persona or directive prefix.
// shortcut: strings.ToLower maps per rune, JS toLowerCase is full (U+0130 becomes "i̇"); no listed prefix depends on it.
func hasRoleCue(first string) bool {
	var toks []string
	for _, t := range strings.Split(jsTrim(strings.ToLower(first)), " ") {
		if t != "" {
			toks = append(toks, t)
		}
	}
	for len(toks) > 0 && roleFillers[toks[0]] {
		toks = toks[1:]
	}
	norm := strings.Join(toks, " ")
	for _, p := range rolePrefixes {
		if norm != "" && strings.HasPrefix(norm, p) {
			return true
		}
	}
	return false
}

// userIntent ports extractIntent (X:2413-2433).
func userIntent(msg string) []store.Event {
	mode := ""
	switch {
	case questionMark.MatchString(msg):
		mode = "investigate"
	case letter.MatchString(msg) && hookio.RuneLen(msg) < 60:
		mode = "implement"
	}
	if mode == "" {
		return nil
	}
	return []store.Event{ev("intent", "intent", 4, mode)}
}

// userGoal ports extractGoal (X:2446-2462).
func userGoal(msg string) []store.Event {
	m := goalDirective.FindStringSubmatch(msg)
	if m == nil {
		return nil
	}
	goal := jsTrim(m[1])
	if goal == "" {
		return nil
	}
	return []store.Event{ev("goal", "goal", 4, goal)}
}

// userBlocker ports extractBlocker (X:2492-2520); resolved wins.
func userBlocker(msg string) []store.Event {
	if resolvedMark.MatchString(msg) || resolvedPrefix.MatchString(msg) {
		return []store.Event{ev("blocker_resolved", "blocked-on", 2, msg)}
	}
	if blockerMarkers.MatchString(msg) {
		return []store.Event{ev("blocker", "blocked-on", 2, msg)}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/capture/ -run 'TestUserPrompt' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/capture/user.go internal/capture/user_test.go
git commit -m "feat(capture): port UserPromptSubmit prompt capture and user extractors"
```

### Task 30: PostToolUseFailure, Stop and SessionStart events

**Files:**
- Create: `internal/capture/session.go`
- Test: `internal/capture/session_test.go`

**Interfaces:**
- Consumes: `hookio.QuoteString`, `hookio.JSPrefix`, `hookio.ParseObject`, `(*hookio.Object).String/Get/MarshalJSON`; Task 24 `jsTrim`, `compactJS`; Task 25 `ev`, `checkEvents`
- Produces: `func PostToolUseFailure(toolName string, payload *hookio.Object) []store.Event`; `func Stop(stopHookActive bool, lastAssistantMessage *string) store.Event`; `func SessionStartRules(home, projectDir string) []store.Event`; `func SessionStartEvent(source, projectDir string, now time.Time) store.Event`

- [ ] **Step 1: Write the failing test**

`internal/capture/session_test.go`:
```go
package capture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

func TestStop(t *testing.T) {
	done := "done <&>"
	long := strings.Repeat("a", 1999) + "😀b" // the pair would straddle unit 2000
	cases := []struct {
		active bool
		msg    *string
		want   string
	}{
		{false, &done, `{"stop_hook_active":false,"last_assistant_message":"done <&>"}`},
		{true, nil, `{"stop_hook_active":true,"last_assistant_message":null}`},
		{false, &long, `{"stop_hook_active":false,"last_assistant_message":"` + strings.Repeat("a", 1999) + `"}`},
	}
	for _, c := range cases {
		got := Stop(c.active, c.msg)
		if want := ev("turn_end", "session", 1, c.want); got != want {
			t.Errorf("Stop = %+v, want %+v", got, want)
		}
	}
}

func TestPostToolUseFailure(t *testing.T) {
	cases := []struct{ payload, want string }{
		{`{"tool_name":"Bash","error":"exit code 1: boom","tool_response":{"x":1}}`, "exit code 1: boom"},
		{`{"tool_name":"Bash","error":{"code":1},"tool_response":{ "stderr" : "<e>" }}`, `{"stderr":"<e>"}`},
		{`{"tool_name":"Bash","tool_response":"plain"}`, `"plain"`},
		{`{"tool_name":"Bash", "is_interrupt":true}`, `{"tool_name":"Bash","is_interrupt":true}`},
	}
	for _, c := range cases {
		p, err := hookio.ParseObject([]byte(c.payload))
		if err != nil {
			t.Fatal(err)
		}
		checkEvents(t, c.payload, PostToolUseFailure("Bash", p), []store.Event{ev("error_tool", "error", 2, c.want)})
	}
}

func TestSessionStartRules(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	userMD := filepath.Join(home, ".claude", "CLAUDE.md")
	write(userMD, "# user\n")
	write(filepath.Join(proj, "CLAUDE.md"), " \n\u3000\ufeff") // blank after JS trim
	nested := filepath.Join(proj, ".claude", "CLAUDE.md")
	write(nested, "nested")
	want := []store.Event{
		ev("rule", "rule", 1, userMD), ev("rule_content", "rule", 1, "# user\n"),
		ev("rule", "rule", 1, nested), ev("rule_content", "rule", 1, "nested"),
	}
	checkEvents(t, "rules", SessionStartRules(home, proj), want)
	checkEvents(t, "no home, no files", SessionStartRules("", t.TempDir()), nil)
}

func TestSessionStartEvent(t *testing.T) {
	got := SessionStartEvent("startup", `/p "q"`, time.Unix(1761000000, 999e6))
	want := ev("session_start", "session_start", 1, `{"source":"startup","project_dir":"/p \"q\"","started_at":1761000000}`)
	if got != want {
		t.Errorf("SessionStartEvent = %+v, want %+v", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/capture/ -run 'TestStop|TestPostToolUseFailure|TestSessionStart' -v`
Expected: FAIL with "internal/capture/session_test.go:27:10: undefined: Stop"

- [ ] **Step 3: Write minimal implementation**

`internal/capture/session.go`:
```go
package capture

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// PostToolUseFailure records a failed tool call (spec §3.3, new in cmgo):
// the payload's error string, else the compact JSON of tool_response, else
// the compact JSON of the whole payload. The field name comes from the
// Claude Code docs; the fallbacks keep a row when the shape differs.
// toolName is not part of the data: spec §3.3 stores the error text alone.
func PostToolUseFailure(toolName string, payload *hookio.Object) []store.Event {
	if payload == nil {
		payload = &hookio.Object{}
	}
	data, ok := payload.String("error")
	if !ok {
		if raw, has := payload.Get("tool_response"); has {
			data = compactJS(raw)
		} else if b, err := payload.MarshalJSON(); err == nil {
			data = string(b)
		}
	}
	return []store.Event{ev("error_tool", "error", 2, data)}
}

// Stop ports stop.mjs:32-43: turn_end with the first 2000 UTF-16 units of
// the last assistant message, or null when there is none.
func Stop(stopHookActive bool, lastAssistantMessage *string) store.Event {
	msg := "null"
	if lastAssistantMessage != nil {
		msg = string(hookio.QuoteString(hookio.JSPrefix(*lastAssistantMessage, 2000)))
	}
	return ev("turn_end", "session", 1, fmt.Sprintf(`{"stop_hook_active":%t,"last_assistant_message":%s}`, stopHookActive, msg))
}

// SessionStartRules ports sessionstart.mjs:341-360: rule and rule_content
// for each readable CLAUDE.md that is not blank, in TS order. TS read the
// first file from resolveConfigDir(); the contract passes home, so it is
// always ~/.claude. An empty home skips that file instead of reading a
// path relative to the cwd.
func SessionStartRules(home, projectDir string) []store.Event {
	paths := []string{filepath.Join(projectDir, "CLAUDE.md"), filepath.Join(projectDir, ".claude", "CLAUDE.md")}
	if home != "" {
		paths = append([]string{filepath.Join(home, ".claude", "CLAUDE.md")}, paths...)
	}
	var evs []store.Event
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil || jsTrim(string(b)) == "" {
			continue
		}
		evs = append(evs, ev("rule", "rule", 1, p), ev("rule_content", "rule", 1, string(b)))
	}
	return evs
}

// SessionStartEvent ports emitSessionStartLifecycle (sessionstart.mjs:61-73).
func SessionStartEvent(source, projectDir string, now time.Time) store.Event {
	data := fmt.Sprintf(`{"source":%s,"project_dir":%s,"started_at":%d}`,
		hookio.QuoteString(source), hookio.QuoteString(projectDir), now.Unix())
	return ev("session_start", "session_start", 1, data)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/capture/ -run 'TestStop|TestPostToolUseFailure|TestSessionStart' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/capture/session.go internal/capture/session_test.go
git commit -m "feat(capture): turn_end, error_tool, CLAUDE.md rule and session_start events"
```

### Task 31: PostToolUse dispatcher and the TS golden replay

**Files:**
- Create: `internal/capture/capture.go`
- Test: `internal/capture/golden_test.go`

**Interfaces:**
- Consumes: `hookio.ParseObject`, `(*hookio.Object).String/Object/Get`; Task 25..Task 29 extractors, `ev`, `checkEvents`
- Produces: `func PostToolUse(toolName string, toolInput *hookio.Object, toolResponse json.RawMessage, home string) []store.Event`; `var extractors []func(call) []store.Event`; test only: `TestGoldenToolCases`, `TestGoldenUserCases` over `testdata/golden/hooks-capture.json` (Task 1)

- [ ] **Step 1: Write the failing test**


`internal/capture/golden_test.go`:
```go
package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// ../../testdata/golden/hooks-capture.json holds TS outputs: the pure
// extractors of hooks/session-extract.bundle.mjs (TS repo at a5e6dd31), run
// by tools/golden-gen/gen-hooks.mjs on the research seed's 27 tool cases and
// 11 prompts, then on the cases added for this package. The expected cmgo
// output is the TS output minus the types in dropped, except for the cases in
// fixed. Both lists are documented in testdata/golden/HOOKS-KNOWN-DIFFS.md.

// dropped lists the event types spec §3.4 does not port. error_tool moved
// to PostToolUseFailure; data is the UserPromptSubmit extractData event.
var dropped = map[string]bool{
	"error_tool": true, "constraint_discovered": true, "bash_outcome": true, "file_read_metadata": true,
	"agent_finding": true, "external_ref": true, "mcp": true, "mcp_tool_call": true, "webfetch_metadata": true,
	"worktree_exit": true, "agent_usage": true, "error_resolved": true, "retry_detected": true, "data": true,
}

// fixed lists the cases a spec §3.4 fix changes, with the new expected
// events. The test fails if a fix stops making a difference.
var fixed = map[string][]store.Event{
	"git -C checkout (TS bug)": {ev("cwd", "cwd", 2, "/x"), ev("git", "git", 2, "branch")},
	"Read CLAUDE.md CC object response": {
		ev("rule", "rule", 1, "/p/CLAUDE.md"),
		ev("rule_content", "rule", 1, "# rules\n"),
		ev("file_read", "file", 1, "/p/CLAUDE.md"),
	},
}

type tsEvent struct {
	Type         string `json:"type"`
	Category     string `json:"category"`
	Data         string `json:"data"`
	Priority     int    `json:"priority"`
	BytesAvoided int64  `json:"bytes_avoided"`
}

type goldenFile struct {
	TSCommit  string `json:"tsCommit"`
	ToolCases []struct {
		Name   string          `json:"name"`
		Input  json.RawMessage `json:"input"`
		Output []tsEvent       `json:"output"`
	} `json:"toolCases"`
	UserCases []struct {
		Prompt string    `json:"prompt"`
		Events []tsEvent `json:"events"`
	} `json:"userCases"`
}

func kept(ts []tsEvent) []store.Event {
	var out []store.Event
	for _, e := range ts {
		if !dropped[e.Type] {
			out = append(out, store.Event{Type: e.Type, Category: e.Category, Priority: e.Priority, Data: e.Data, BytesAvoided: e.BytesAvoided})
		}
	}
	return out
}

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", "hooks-capture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g goldenFile
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	if g.TSCommit != "a5e6dd31" {
		t.Fatalf("hooks-capture.json: commit %q", g.TSCommit)
	}
	return g
}

func TestGoldenToolCases(t *testing.T) {
	g := loadGolden(t)
	seen := map[string]bool{}
	for _, c := range g.ToolCases {
		in, err := hookio.ParseObject(c.Input)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		name, _ := in.String("tool_name")
		toolInput, _ := in.Object("tool_input")
		resp, _ := in.Get("tool_response") // nil when absent, as on the hook path
		got := PostToolUse(name, toolInput, resp, "/home/test")
		want := kept(c.Output)
		if fix, ok := fixed[c.Name]; ok {
			seen[c.Name] = true
			if reflect.DeepEqual(want, fix) {
				t.Errorf("%s: listed as fixed, but TS already gives %+v", c.Name, want)
			}
			want = fix
		}
		checkEvents(t, c.Name, got, want)
	}
	for name := range fixed {
		if !seen[name] {
			t.Errorf("fixed case %q is missing from hooks-capture.json", name)
		}
	}
}

func TestGoldenUserCases(t *testing.T) {
	for _, c := range loadGolden(t).UserCases {
		want := append([]store.Event{ev("user_prompt", "user-prompt", 1, c.Prompt)}, kept(c.Events)...)
		checkEvents(t, c.Prompt, UserPrompt(c.Prompt), want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/capture/ -run 'TestGolden' -v`
Expected: FAIL with "internal/capture/golden_test.go:98:10: undefined: PostToolUse"

- [ ] **Step 3: Write minimal implementation**

`internal/capture/capture.go`:
```go
// Package capture turns hook inputs into session events (hooks spec §3.3).
// The extractors port src/session/extract.ts (X:) at a5e6dd31: same order,
// regexps, data formats and JS length rules, minus the extractors spec §3.4
// drops and with its fixes. Nothing here touches the store.
package capture

import (
	"encoding/json"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// extractors run in the extractEvents order (X:2710-2750).
var extractors = []func(call) []store.Event{
	fileEvents, cwdEvents, gitEvents, envEvents, taskEvents,
	planEvents, skillEvents, subagentEvents, decisionEvents, worktreeEvents,
}

// PostToolUse runs the kept extractors (spec §3.3) in TS order. home expands
// "~" in git -C paths. toolResponse is the raw JSON value, nil when absent.
func PostToolUse(toolName string, toolInput *hookio.Object, toolResponse json.RawMessage, home string) []store.Event {
	if toolInput == nil {
		toolInput = &hookio.Object{} // tool_input ?? {}
	}
	c := call{tool: toolName, input: toolInput, raw: toolResponse, resp: responseText(toolResponse), home: home}
	var evs []store.Event
	for _, x := range extractors {
		evs = append(evs, x(c)...)
	}
	return evs
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/capture/ -run 'TestGolden' -v`
Expected: PASS

Then the package gate:
Run: `go vet ./... && go test ./internal/capture/... -count=1 -race`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/capture/capture.go internal/capture/golden_test.go
git commit -m "feat(capture): PostToolUse dispatcher and TS golden replay"
```

---

### Task 32: run takes stdin

**Files:**
- Modify: `cmd/cmgo/main.go`
- Test: `cmd/cmgo/main_test.go`

**Interfaces:**
- Consumes: none
- Produces: `func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int`

- [ ] **Step 1: Write the failing test**

`cmd/cmgo/main_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if out.String() != "cmgo dev\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"nope"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit %d", code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/cmgo/ -run 'TestVersion|TestUnknownCommand' -v`
Expected: FAIL with "cmd/cmgo/main_test.go:11:67: too many arguments in call to run"

- [ ] **Step 3: Write minimal implementation**

`cmd/cmgo/main.go`:

```go
// Command cmgo is a Go rewrite of context-mode for Claude Code.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
)

// version is set at build time: go build -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run dispatches a subcommand. stdin feeds `hook`; serve keeps os.Stdin
// because the MCP transport needs a ReadCloser.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: cmgo <serve|version>")
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(stdout, "cmgo "+version)
		return 0
	case "serve":
		return serve(context.Background(), serveIO{
			getenv: os.Getenv, getwd: os.Getwd, in: os.Stdin, out: os.Stdout, stderr: stderr,
		})
	default:
		fmt.Fprintf(stderr, "cmgo: unknown command %q\n", args[0])
		return 2
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/cmgo/ -run 'TestVersion|TestUnknownCommand' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/cmgo/main.go cmd/cmgo/main_test.go
git commit -m "refactor(cmgo): pass stdin to run"
```

### Task 33: hookcmd: dispatcher, fail-open policy and PreToolUse

**Files:**
- Create: `internal/hookcmd/hookcmd.go`
- Test: `internal/hookcmd/hookcmd_test.go`

**Interfaces:**
- Consumes: `hookio.ReadInput(r io.Reader) ([]byte, error)`; `hookio.ParseObject(b []byte) (*hookio.Object, error)`; `hookio.SessionID(in *hookio.Object, getenv func(string) string, ppid int) string`; `config.Load(getenv func(string) string, getwd func() (string, error)) (config.Config, error)`; `routing.Route(toolName string, toolInput *hookio.Object, subagent bool, d routing.Deps) routing.Decision`; `routing.Format(dec routing.Decision) []byte`; `routing.Events(toolName string, dec routing.Decision) []store.Event`; `routing.MCPReady(runDir string) bool`; `routing.WriteSentinel(runDir string, pid int) (func(), error)` (test only); `security.NewPolicy(projectDir, home string) *security.Policy`; `store.Open(path string) (*store.Store, error)`; `(*store.Store).InsertEvents(sessionID, project, sourceHook string, evs []store.Event) (int, error)`; `(*store.Store).SessionEvents(sessionID string) ([]store.StoredEvent, error)` (test only)
- Produces: `type IO struct{ Getenv func(string) string; Getwd func() (string, error); Stdin io.Reader; Stdout io.Writer; Now func() time.Time }`; `func Run(event string, hio IO) int`; unexported `type hookCtx struct{ in *hookio.Object; cfg config.Config; sid, runDir string; getenv func(string) string; out io.Writer; now time.Time }`, `var stdinTimeout = 2 * time.Second`, `var hookHandlers map[string]func(*hookCtx) error`, `func readStdin(r io.Reader) ([]byte, error)`, `func logHookError(getenv func(string) string, event string, err error)`, `func (h *hookCtx) insertEvents(sourceHook string, evs []store.Event) error`, `func preToolUse(h *hookCtx) error`

- [ ] **Step 1: Write the failing test**

`internal/hookcmd/hookcmd_test.go`:

```go
package hookcmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/store"
)

// hookEnv is a temp HOME, CMGO_DIR and project dir, never the real ones.
func hookEnv(t *testing.T) map[string]string {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"HOME": filepath.Join(root, "home"), "CMGO_DIR": filepath.Join(root, "data"), "CLAUDE_PROJECT_DIR": project}
}

var hookNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

// callHook runs one hook fire and returns its stdout.
func callHook(t *testing.T, env map[string]string, event, stdin string) string {
	t.Helper()
	var out bytes.Buffer
	code := Run(event, IO{
		Getenv: func(k string) string { return env[k] },
		Getwd:  os.Getwd,
		Stdin:  strings.NewReader(stdin),
		Stdout: &out,
		Now:    func() time.Time { return hookNow },
	})
	if code != 0 {
		t.Fatalf("%s: exit %d", event, code)
	}
	return out.String()
}

// eventRows returns a session's rows as "type|category|priority|data|hook|bytes".
func eventRows(t *testing.T, env map[string]string, sid string) []string {
	t.Helper()
	st, err := store.Open(filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	evs, err := st.SessionEvents(sid)
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, e := range evs {
		rows = append(rows, fmt.Sprintf("%s|%s|%d|%s|%s|%d", e.Type, e.Category, e.Priority, e.Data, e.SourceHook, e.BytesAvoided))
	}
	return rows
}

func assertRows(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func noDB(t *testing.T, env map[string]string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(env["CMGO_DIR"], "cmgo.db")); err == nil {
		t.Fatal("the store was opened with no event to write")
	}
}

func hookErrors(env map[string]string) string {
	b, _ := os.ReadFile(filepath.Join(env["CMGO_DIR"], "hook-errors.log"))
	return string(b)
}

const curlInput = `{"tool_name":"Bash","tool_input":{"command":"curl https://x.com"},"session_id":"s1"}`

func TestHookPreToolUsePassthrough(t *testing.T) {
	env := hookEnv(t)
	for _, in := range []string{
		`{"tool_name":"Bash","tool_input":{"command":"git status"},"session_id":"s1"}`,
		curlInput, // no live cmgo serve: redirects are gated off
	} {
		if out := callHook(t, env, "pretooluse", in); out != "" {
			t.Fatalf("%s: got %q", in, out)
		}
	}
	// A missing or non-object tool_input is an empty command: Bash guidance, once.
	if out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","session_id":"s2"}`); !strings.Contains(out, `"additionalContext":"<context_guidance>`) {
		t.Fatalf("missing tool_input: got %q", out)
	}
	if out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","tool_input":"x","session_id":"s2"}`); out != "" {
		t.Fatalf("second guidance: got %q", out)
	}
	noDB(t, env)
	if s := hookErrors(env); s != "" {
		t.Fatalf("errors logged: %s", s)
	}
}

func TestHookPreToolUseRedirect(t *testing.T) {
	env := hookEnv(t)
	remove, err := routing.WriteSentinel(filepath.Join(env["CMGO_DIR"], "run"), os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	out := callHook(t, env, "pretooluse", curlInput)
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"context-mode: curl/wget redirected. Call mcp__cmgo__ctx_execute(language, code)`
	if !strings.HasPrefix(out, want) || !strings.HasSuffix(out, "\"}}\n") {
		t.Fatalf("got %q", out)
	}
	rows := eventRows(t, env, "s1")
	if len(rows) != 2 || rows[0] != "bash-redirected|redirect|2|Bash: curl https://x.com|PreToolUse|8192" ||
		!strings.HasPrefix(rows[1], "rejected|rejected-approach|2|Bash: context-mode: curl/wget redirected.") {
		t.Fatalf("rows: %q", rows)
	}
	// A subagent call is gated off even with a live server.
	if out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","tool_input":{"command":"curl https://x.com"},"session_id":"s1","agent_id":"a"}`); out != "" {
		t.Fatalf("subagent: got %q", out)
	}
}

func TestHookPreToolUseSecurityDeny(t *testing.T) {
	env := hookEnv(t)
	dir := filepath.Join(env["CLAUDE_PROJECT_DIR"], ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"permissions":{"deny":["Bash(rm -rf *)"]}}`), 0o644)
	out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","tool_input":{"command":"rm -rf /x"},"session_id":"s1"}`)
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Blocked by security policy: matches deny pattern Bash(rm -rf *)"}}` + "\n"
	if out != want {
		t.Fatalf("got %q", out)
	}
	assertRows(t, eventRows(t, env, "s1"),
		"rejected|rejected-approach|2|Bash: Blocked by security policy: matches deny pattern Bash(rm -rf *)|PreToolUse|0")
}

// The project dir comes from input.cwd when no env var names it.
func TestHookProjectFromInputCwd(t *testing.T) {
	env := hookEnv(t)
	project := env["CLAUDE_PROJECT_DIR"]
	delete(env, "CLAUDE_PROJECT_DIR")
	os.MkdirAll(filepath.Join(project, ".claude"), 0o755)
	os.WriteFile(filepath.Join(project, ".claude", "settings.json"), []byte(`{"permissions":{"deny":["Bash(rm -rf *)"]}}`), 0o644)
	in := fmt.Sprintf(`{"tool_name":"Bash","tool_input":{"command":"rm -rf /x"},"session_id":"s1","cwd":%q}`, project)
	if out := callHook(t, env, "pretooluse", in); !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatalf("got %q", out)
	}
}

var errLine = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\S* pid=\d+ pretooluse: \S`)

func TestHookFailOpen(t *testing.T) {
	env := hookEnv(t)
	if out := callHook(t, env, "pretooluse", `{"tool_name":`); out != "" {
		t.Fatalf("malformed: got %q", out)
	}
	if s := hookErrors(env); !errLine.MatchString(s) || strings.Count(s, "\n") != 1 {
		t.Fatalf("log: %q", s)
	}
	if out := callHook(t, env, "nope", curlInput); out != "" {
		t.Fatalf("unknown event: got %q", out)
	}
}

// Spec §1.2: empty stdin produces no output and is not an error.
func TestHookEmptyStdin(t *testing.T) {
	env := hookEnv(t)
	if out := callHook(t, env, "pretooluse", " \n"); out != "" {
		t.Fatalf("got %q", out)
	}
	if s := hookErrors(env); s != "" {
		t.Fatalf("log: %q", s)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { panic("boom") }

func TestHookRecoversPanic(t *testing.T) {
	env := hookEnv(t)
	dir := filepath.Join(env["CLAUDE_PROJECT_DIR"], ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"permissions":{"deny":["Bash(rm -rf *)"]}}`), 0o644)
	code := Run("pretooluse", IO{
		Getenv: func(k string) string { return env[k] },
		Getwd:  os.Getwd,
		Stdin:  strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"rm -rf /x"}}`),
		Stdout: failWriter{},
		Now:    time.Now,
	})
	if code != 0 || !strings.Contains(hookErrors(env), "pretooluse: panic: boom") {
		t.Fatalf("exit %d, log %q", code, hookErrors(env))
	}
}

func TestHookStdinTimeout(t *testing.T) {
	defer func(d time.Duration) { stdinTimeout = d }(stdinTimeout)
	stdinTimeout = 50 * time.Millisecond
	env := hookEnv(t)
	r, w := io.Pipe() // never closed: stdin that does not end
	defer w.Close()
	var out bytes.Buffer
	code := Run("pretooluse", IO{
		Getenv: func(k string) string { return env[k] }, Getwd: os.Getwd, Stdin: r, Stdout: &out, Now: time.Now,
	})
	if code != 0 || out.Len() != 0 || !strings.Contains(hookErrors(env), "pretooluse: stdin") {
		t.Fatalf("exit %d, out %q, log %q", code, out.String(), hookErrors(env))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hookcmd/ -v`
Expected: FAIL with "internal/hookcmd/hookcmd_test.go:35:10: undefined: Run"

- [ ] **Step 3: Write minimal implementation**

`internal/hookcmd/hookcmd.go`:

```go
// Package hookcmd runs one Claude Code hook fire (hooks spec §1-§4). It is
// what cmgo-hook executes; it links no MCP server, executor or fetcher, so a
// hook process starts fast.
package hookcmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
)

// stdinTimeout bounds the stdin read (spec §1.2). It is a fallback: Claude
// Code closes stdin. A var so the test need not wait 2 s.
var stdinTimeout = 2 * time.Second

// IO is what a hook fire talks over; tests inject env, cwd, stdin and clock.
type IO struct {
	Getenv func(string) string
	Getwd  func() (string, error)
	Stdin  io.Reader
	Stdout io.Writer
	Now    func() time.Time
}

// hookCtx is one parsed hook fire.
type hookCtx struct {
	in     *hookio.Object
	cfg    config.Config
	sid    string
	runDir string // $CMGO_DIR/run: guidance markers and MCP sentinels
	getenv func(string) string
	out    io.Writer
	now    time.Time
}

// hookHandlers maps a hook event name to its handler.
var hookHandlers = map[string]func(*hookCtx) error{
	"pretooluse": preToolUse,
}

// Run runs one hook fire and always exits 0 (spec §1.2): an error or panic
// is appended to $CMGO_DIR/hook-errors.log and nothing more is printed.
func Run(event string, hio IO) int {
	h, ok := hookHandlers[event]
	if !ok {
		return 0
	}
	defer func() {
		if r := recover(); r != nil {
			logHookError(hio.Getenv, event, fmt.Errorf("panic: %v", r))
		}
	}()
	if err := runHook(h, hio); err != nil {
		logHookError(hio.Getenv, event, err)
	}
	return 0
}

func runHook(h func(*hookCtx) error, hio IO) error {
	raw, err := readStdin(hio.Stdin)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil // spec §1.2: empty stdin, no output
	}
	in, err := hookio.ParseObject(raw)
	if err != nil {
		return err
	}
	getwd := hio.Getwd
	if cwd, ok := in.String("cwd"); ok && cwd != "" {
		getwd = func() (string, error) { return cwd, nil }
	}
	cfg, err := config.Load(hio.Getenv, getwd)
	if err != nil {
		return err
	}
	return h(&hookCtx{
		in:     in,
		cfg:    cfg,
		sid:    hookio.SessionID(in, hio.Getenv, os.Getppid()),
		runDir: filepath.Join(cfg.DataDir, "run"),
		getenv: hio.Getenv,
		out:    hio.Stdout,
		now:    hio.Now(),
	})
}

// readStdin reads the whole payload, giving up after stdinTimeout. The
// reader goroutine is left blocked on timeout; the process exits anyway.
func readStdin(r io.Reader) ([]byte, error) {
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := hookio.ReadInput(r)
		ch <- result{b, err}
	}()
	select {
	case res := <-ch:
		return res.b, res.err
	case <-time.After(stdinTimeout):
		return nil, errors.New("stdin: no end of input within " + stdinTimeout.String())
	}
}

// logHookError appends one line to $CMGO_DIR/hook-errors.log, best effort.
func logHookError(getenv func(string) string, event string, err error) {
	// The project dir does not affect DataDir, so a failed parse can still log.
	cfg, cerr := config.Load(getenv, func() (string, error) { return "/", nil })
	if cerr != nil {
		return
	}
	if os.MkdirAll(cfg.DataDir, 0o755) != nil {
		return
	}
	f, ferr := os.OpenFile(filepath.Join(cfg.DataDir, "hook-errors.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s pid=%d %s: %v\n", time.Now().Format(time.RFC3339), os.Getpid(), event, err)
}

// insertEvents writes evs in one transaction; with none it does not open the
// store at all (spec §1.1).
func (h *hookCtx) insertEvents(sourceHook string, evs []store.Event) error {
	if len(evs) == 0 {
		return nil
	}
	st, err := store.Open(h.cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	_, err = st.InsertEvents(h.sid, h.cfg.ProjectKey, sourceHook, evs)
	return err
}

// preToolUse routes the call (spec §2) and writes the decision before it
// opens the store, so the DB never delays Claude Code.
func preToolUse(h *hookCtx) error {
	tool, _ := h.in.String("tool_name")
	input, ok := h.in.Object("tool_input")
	if !ok {
		input = &hookio.Object{}
	}
	subagent := h.in.Has("agent_id") || h.in.Has("agent_type")
	dec := routing.Route(tool, input, subagent, routing.Deps{
		ProjectDir: h.cfg.ProjectDir,
		SessionID:  h.sid,
		RunDir:     h.runDir,
		Policy:     security.NewPolicy(h.cfg.ProjectDir, h.cfg.Home),
		Getenv:     h.getenv,
		MCPReady:   func() bool { return routing.MCPReady(h.runDir) },
	})
	if out := routing.Format(dec); out != nil {
		if _, err := h.out.Write(out); err != nil {
			return err
		}
	}
	return h.insertEvents("PreToolUse", routing.Events(tool, dec))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/hookcmd/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/hookcmd/hookcmd.go internal/hookcmd/hookcmd_test.go
git commit -m "feat(hookcmd): hook runner with fail-open policy and PreToolUse routing"
```

### Task 34: hookcmd: SessionStart

**Files:**
- Modify: `internal/hookcmd/hookcmd.go`
- Test: `internal/hookcmd/hookcmd_test.go`

**Interfaces:**
- Consumes: `routing.RoutingBlock(includeCommands, toolSearchBootstrap bool) string`; `routing.PruneGuidance(runDir string, now time.Time, maxAge time.Duration)`; `capture.SessionStartRules(home, projectDir string) []store.Event`; `capture.SessionStartEvent(source, projectDir string, now time.Time) store.Event`; `hookio.QuoteString(s string) json.RawMessage`
- Produces: `const guidanceMaxAge = 7 * 24 * time.Hour`; `func sessionStart(h *hookCtx) error`, registered as `hookHandlers["sessionstart"]`

- [ ] **Step 1: Write the failing test**

`internal/hookcmd/hookcmd_test.go`:

```go
package hookcmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/store"
)

// hookEnv is a temp HOME, CMGO_DIR and project dir, never the real ones.
func hookEnv(t *testing.T) map[string]string {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"HOME": filepath.Join(root, "home"), "CMGO_DIR": filepath.Join(root, "data"), "CLAUDE_PROJECT_DIR": project}
}

var hookNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

// callHook runs one hook fire and returns its stdout.
func callHook(t *testing.T, env map[string]string, event, stdin string) string {
	t.Helper()
	var out bytes.Buffer
	code := Run(event, IO{
		Getenv: func(k string) string { return env[k] },
		Getwd:  os.Getwd,
		Stdin:  strings.NewReader(stdin),
		Stdout: &out,
		Now:    func() time.Time { return hookNow },
	})
	if code != 0 {
		t.Fatalf("%s: exit %d", event, code)
	}
	return out.String()
}

// eventRows returns a session's rows as "type|category|priority|data|hook|bytes".
func eventRows(t *testing.T, env map[string]string, sid string) []string {
	t.Helper()
	st, err := store.Open(filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	evs, err := st.SessionEvents(sid)
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, e := range evs {
		rows = append(rows, fmt.Sprintf("%s|%s|%d|%s|%s|%d", e.Type, e.Category, e.Priority, e.Data, e.SourceHook, e.BytesAvoided))
	}
	return rows
}

func assertRows(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func noDB(t *testing.T, env map[string]string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(env["CMGO_DIR"], "cmgo.db")); err == nil {
		t.Fatal("the store was opened with no event to write")
	}
}

func hookErrors(env map[string]string) string {
	b, _ := os.ReadFile(filepath.Join(env["CMGO_DIR"], "hook-errors.log"))
	return string(b)
}

const curlInput = `{"tool_name":"Bash","tool_input":{"command":"curl https://x.com"},"session_id":"s1"}`

func TestHookPreToolUsePassthrough(t *testing.T) {
	env := hookEnv(t)
	for _, in := range []string{
		`{"tool_name":"Bash","tool_input":{"command":"git status"},"session_id":"s1"}`,
		curlInput, // no live cmgo serve: redirects are gated off
	} {
		if out := callHook(t, env, "pretooluse", in); out != "" {
			t.Fatalf("%s: got %q", in, out)
		}
	}
	// A missing or non-object tool_input is an empty command: Bash guidance, once.
	if out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","session_id":"s2"}`); !strings.Contains(out, `"additionalContext":"<context_guidance>`) {
		t.Fatalf("missing tool_input: got %q", out)
	}
	if out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","tool_input":"x","session_id":"s2"}`); out != "" {
		t.Fatalf("second guidance: got %q", out)
	}
	noDB(t, env)
	if s := hookErrors(env); s != "" {
		t.Fatalf("errors logged: %s", s)
	}
}

func TestHookPreToolUseRedirect(t *testing.T) {
	env := hookEnv(t)
	remove, err := routing.WriteSentinel(filepath.Join(env["CMGO_DIR"], "run"), os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	out := callHook(t, env, "pretooluse", curlInput)
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"context-mode: curl/wget redirected. Call mcp__cmgo__ctx_execute(language, code)`
	if !strings.HasPrefix(out, want) || !strings.HasSuffix(out, "\"}}\n") {
		t.Fatalf("got %q", out)
	}
	rows := eventRows(t, env, "s1")
	if len(rows) != 2 || rows[0] != "bash-redirected|redirect|2|Bash: curl https://x.com|PreToolUse|8192" ||
		!strings.HasPrefix(rows[1], "rejected|rejected-approach|2|Bash: context-mode: curl/wget redirected.") {
		t.Fatalf("rows: %q", rows)
	}
	// A subagent call is gated off even with a live server.
	if out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","tool_input":{"command":"curl https://x.com"},"session_id":"s1","agent_id":"a"}`); out != "" {
		t.Fatalf("subagent: got %q", out)
	}
}

func TestHookPreToolUseSecurityDeny(t *testing.T) {
	env := hookEnv(t)
	dir := filepath.Join(env["CLAUDE_PROJECT_DIR"], ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"permissions":{"deny":["Bash(rm -rf *)"]}}`), 0o644)
	out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","tool_input":{"command":"rm -rf /x"},"session_id":"s1"}`)
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Blocked by security policy: matches deny pattern Bash(rm -rf *)"}}` + "\n"
	if out != want {
		t.Fatalf("got %q", out)
	}
	assertRows(t, eventRows(t, env, "s1"),
		"rejected|rejected-approach|2|Bash: Blocked by security policy: matches deny pattern Bash(rm -rf *)|PreToolUse|0")
}

// The project dir comes from input.cwd when no env var names it.
func TestHookProjectFromInputCwd(t *testing.T) {
	env := hookEnv(t)
	project := env["CLAUDE_PROJECT_DIR"]
	delete(env, "CLAUDE_PROJECT_DIR")
	os.MkdirAll(filepath.Join(project, ".claude"), 0o755)
	os.WriteFile(filepath.Join(project, ".claude", "settings.json"), []byte(`{"permissions":{"deny":["Bash(rm -rf *)"]}}`), 0o644)
	in := fmt.Sprintf(`{"tool_name":"Bash","tool_input":{"command":"rm -rf /x"},"session_id":"s1","cwd":%q}`, project)
	if out := callHook(t, env, "pretooluse", in); !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatalf("got %q", out)
	}
}

var errLine = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\S* pid=\d+ pretooluse: \S`)

func TestHookFailOpen(t *testing.T) {
	env := hookEnv(t)
	if out := callHook(t, env, "pretooluse", `{"tool_name":`); out != "" {
		t.Fatalf("malformed: got %q", out)
	}
	if s := hookErrors(env); !errLine.MatchString(s) || strings.Count(s, "\n") != 1 {
		t.Fatalf("log: %q", s)
	}
	if out := callHook(t, env, "nope", curlInput); out != "" {
		t.Fatalf("unknown event: got %q", out)
	}
}

// Spec §1.2: empty stdin produces no output and is not an error.
func TestHookEmptyStdin(t *testing.T) {
	env := hookEnv(t)
	if out := callHook(t, env, "pretooluse", " \n"); out != "" {
		t.Fatalf("got %q", out)
	}
	if s := hookErrors(env); s != "" {
		t.Fatalf("log: %q", s)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { panic("boom") }

func TestHookRecoversPanic(t *testing.T) {
	env := hookEnv(t)
	dir := filepath.Join(env["CLAUDE_PROJECT_DIR"], ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"permissions":{"deny":["Bash(rm -rf *)"]}}`), 0o644)
	code := Run("pretooluse", IO{
		Getenv: func(k string) string { return env[k] },
		Getwd:  os.Getwd,
		Stdin:  strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"rm -rf /x"}}`),
		Stdout: failWriter{},
		Now:    time.Now,
	})
	if code != 0 || !strings.Contains(hookErrors(env), "pretooluse: panic: boom") {
		t.Fatalf("exit %d, log %q", code, hookErrors(env))
	}
}

func TestHookStdinTimeout(t *testing.T) {
	defer func(d time.Duration) { stdinTimeout = d }(stdinTimeout)
	stdinTimeout = 50 * time.Millisecond
	env := hookEnv(t)
	r, w := io.Pipe() // never closed: stdin that does not end
	defer w.Close()
	var out bytes.Buffer
	code := Run("pretooluse", IO{
		Getenv: func(k string) string { return env[k] }, Getwd: os.Getwd, Stdin: r, Stdout: &out, Now: time.Now,
	})
	if code != 0 || out.Len() != 0 || !strings.Contains(hookErrors(env), "pretooluse: stdin") {
		t.Fatalf("exit %d, out %q, log %q", code, out.String(), hookErrors(env))
	}
}

func sessionStartOut() string {
	return `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":` +
		string(hookio.QuoteString(routing.RoutingBlock(false, false))) + "}}\n"
}

func TestHookSessionStartStartup(t *testing.T) {
	env := hookEnv(t)
	project := env["CLAUDE_PROJECT_DIR"]
	homeRules := filepath.Join(env["HOME"], ".claude", "CLAUDE.md")
	os.MkdirAll(filepath.Dir(homeRules), 0o755)
	os.WriteFile(homeRules, []byte("home rules"), 0o644)
	os.WriteFile(filepath.Join(project, "CLAUDE.md"), []byte("project rules"), 0o644)
	runDir := filepath.Join(env["CMGO_DIR"], "run")
	for _, d := range []string{"guidance-old", "guidance-new"} {
		os.MkdirAll(filepath.Join(runDir, d), 0o700)
	}
	old := hookNow.Add(-8 * 24 * time.Hour)
	os.Chtimes(filepath.Join(runDir, "guidance-old"), old, old)

	if out := callHook(t, env, "sessionstart", `{"session_id":"s1","source":"startup"}`); out != sessionStartOut() {
		t.Fatalf("got %q", out)
	}
	assertRows(t, eventRows(t, env, "s1"),
		"rule|rule|1|"+homeRules+"|SessionStart|0",
		"rule_content|rule|1|home rules|SessionStart|0",
		"rule|rule|1|"+filepath.Join(project, "CLAUDE.md")+"|SessionStart|0",
		"rule_content|rule|1|project rules|SessionStart|0",
		fmt.Sprintf(`session_start|session_start|1|{"source":"startup","project_dir":%q,"started_at":%d}|SessionStart|0`, project, hookNow.Unix()))
	if _, err := os.Stat(filepath.Join(runDir, "guidance-old")); err == nil {
		t.Fatal("old guidance dir not pruned")
	}
	if _, err := os.Stat(filepath.Join(runDir, "guidance-new")); err != nil {
		t.Fatal("fresh guidance dir removed")
	}
	// A missing or null source is startup, as TS input.source ?? "startup".
	callHook(t, env, "sessionstart", `{"session_id":"s2"}`)
	callHook(t, env, "sessionstart", `{"session_id":"s3","source":null}`)
	for _, sid := range []string{"s2", "s3"} {
		if rows := eventRows(t, env, sid); len(rows) != 5 {
			t.Fatalf("%s rows: %q", sid, rows)
		}
	}
}

func TestHookSessionStartOtherSources(t *testing.T) {
	env := hookEnv(t)
	for _, src := range []string{"resume", "compact", "clear"} {
		if out := callHook(t, env, "sessionstart", `{"session_id":"s1","source":"`+src+`"}`); out != sessionStartOut() {
			t.Fatalf("%s: got %q", src, out)
		}
	}
	noDB(t, env)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hookcmd/ -run 'TestHookSessionStart' -v`
Expected: FAIL with "--- FAIL: TestHookSessionStartStartup" after "hookcmd_test.go:244: got \"\""

- [ ] **Step 3: Write minimal implementation**

`internal/hookcmd/hookcmd.go`:

```go
// Package hookcmd runs one Claude Code hook fire (hooks spec §1-§4). It is
// what cmgo-hook executes; it links no MCP server, executor or fetcher, so a
// hook process starts fast.
package hookcmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/lang315/context-mode-go/internal/capture"
	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
)

// stdinTimeout bounds the stdin read (spec §1.2). It is a fallback: Claude
// Code closes stdin. A var so the test need not wait 2 s.
var stdinTimeout = 2 * time.Second

// guidanceMaxAge is how long a guidance marker dir is kept (spec §4).
const guidanceMaxAge = 7 * 24 * time.Hour

// IO is what a hook fire talks over; tests inject env, cwd, stdin and clock.
type IO struct {
	Getenv func(string) string
	Getwd  func() (string, error)
	Stdin  io.Reader
	Stdout io.Writer
	Now    func() time.Time
}

// hookCtx is one parsed hook fire.
type hookCtx struct {
	in     *hookio.Object
	cfg    config.Config
	sid    string
	runDir string // $CMGO_DIR/run: guidance markers and MCP sentinels
	getenv func(string) string
	out    io.Writer
	now    time.Time
}

// hookHandlers maps a hook event name to its handler.
var hookHandlers = map[string]func(*hookCtx) error{
	"pretooluse":   preToolUse,
	"sessionstart": sessionStart,
}

// Run runs one hook fire and always exits 0 (spec §1.2): an error or panic
// is appended to $CMGO_DIR/hook-errors.log and nothing more is printed.
func Run(event string, hio IO) int {
	h, ok := hookHandlers[event]
	if !ok {
		return 0
	}
	defer func() {
		if r := recover(); r != nil {
			logHookError(hio.Getenv, event, fmt.Errorf("panic: %v", r))
		}
	}()
	if err := runHook(h, hio); err != nil {
		logHookError(hio.Getenv, event, err)
	}
	return 0
}

func runHook(h func(*hookCtx) error, hio IO) error {
	raw, err := readStdin(hio.Stdin)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil // spec §1.2: empty stdin, no output
	}
	in, err := hookio.ParseObject(raw)
	if err != nil {
		return err
	}
	getwd := hio.Getwd
	if cwd, ok := in.String("cwd"); ok && cwd != "" {
		getwd = func() (string, error) { return cwd, nil }
	}
	cfg, err := config.Load(hio.Getenv, getwd)
	if err != nil {
		return err
	}
	return h(&hookCtx{
		in:     in,
		cfg:    cfg,
		sid:    hookio.SessionID(in, hio.Getenv, os.Getppid()),
		runDir: filepath.Join(cfg.DataDir, "run"),
		getenv: hio.Getenv,
		out:    hio.Stdout,
		now:    hio.Now(),
	})
}

// readStdin reads the whole payload, giving up after stdinTimeout. The
// reader goroutine is left blocked on timeout; the process exits anyway.
func readStdin(r io.Reader) ([]byte, error) {
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := hookio.ReadInput(r)
		ch <- result{b, err}
	}()
	select {
	case res := <-ch:
		return res.b, res.err
	case <-time.After(stdinTimeout):
		return nil, errors.New("stdin: no end of input within " + stdinTimeout.String())
	}
}

// logHookError appends one line to $CMGO_DIR/hook-errors.log, best effort.
func logHookError(getenv func(string) string, event string, err error) {
	// The project dir does not affect DataDir, so a failed parse can still log.
	cfg, cerr := config.Load(getenv, func() (string, error) { return "/", nil })
	if cerr != nil {
		return
	}
	if os.MkdirAll(cfg.DataDir, 0o755) != nil {
		return
	}
	f, ferr := os.OpenFile(filepath.Join(cfg.DataDir, "hook-errors.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s pid=%d %s: %v\n", time.Now().Format(time.RFC3339), os.Getpid(), event, err)
}

// insertEvents writes evs in one transaction; with none it does not open the
// store at all (spec §1.1).
func (h *hookCtx) insertEvents(sourceHook string, evs []store.Event) error {
	if len(evs) == 0 {
		return nil
	}
	st, err := store.Open(h.cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	_, err = st.InsertEvents(h.sid, h.cfg.ProjectKey, sourceHook, evs)
	return err
}

// preToolUse routes the call (spec §2) and writes the decision before it
// opens the store, so the DB never delays Claude Code.
func preToolUse(h *hookCtx) error {
	tool, _ := h.in.String("tool_name")
	input, ok := h.in.Object("tool_input")
	if !ok {
		input = &hookio.Object{}
	}
	subagent := h.in.Has("agent_id") || h.in.Has("agent_type")
	dec := routing.Route(tool, input, subagent, routing.Deps{
		ProjectDir: h.cfg.ProjectDir,
		SessionID:  h.sid,
		RunDir:     h.runDir,
		Policy:     security.NewPolicy(h.cfg.ProjectDir, h.cfg.Home),
		Getenv:     h.getenv,
		MCPReady:   func() bool { return routing.MCPReady(h.runDir) },
	})
	if out := routing.Format(dec); out != nil {
		if _, err := h.out.Write(out); err != nil {
			return err
		}
	}
	return h.insertEvents("PreToolUse", routing.Events(tool, dec))
}

// sessionStart prints the routing block for every source (spec §4). Only
// startup also prunes old guidance markers and records the rule and
// session_start events; resume and compact context is sub-project 3.
func sessionStart(h *hookCtx) error {
	out := `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":` +
		string(hookio.QuoteString(routing.RoutingBlock(false, false))) + "}}\n"
	if _, err := io.WriteString(h.out, out); err != nil {
		return err
	}
	src, _ := h.in.String("source")
	if raw, ok := h.in.Get("source"); !ok || string(raw) == "null" {
		src = "startup" // TS: input.source ?? "startup"
	}
	if src != "startup" {
		return nil
	}
	routing.PruneGuidance(h.runDir, h.now, guidanceMaxAge)
	evs := append(capture.SessionStartRules(h.cfg.Home, h.cfg.ProjectDir), capture.SessionStartEvent(src, h.cfg.ProjectDir, h.now))
	return h.insertEvents("SessionStart", evs)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/hookcmd/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/hookcmd/hookcmd.go internal/hookcmd/hookcmd_test.go
git commit -m "feat(hookcmd): SessionStart prints the routing block and records startup events"
```

### Task 35: hookcmd: PostToolUse, PostToolUseFailure, UserPromptSubmit and Stop

**Files:**
- Modify: `internal/hookcmd/hookcmd.go`
- Test: `internal/hookcmd/hookcmd_test.go`

**Interfaces:**
- Consumes: `capture.PostToolUse(toolName string, toolInput *hookio.Object, toolResponse json.RawMessage, home string) []store.Event`; `capture.PostToolUseFailure(toolName string, payload *hookio.Object) []store.Event`; `capture.UserPrompt(prompt string) []store.Event`; `capture.Stop(stopHookActive bool, lastAssistantMessage *string) store.Event`
- Produces: `func postToolUse(h *hookCtx) error`; `func postToolUseFailure(h *hookCtx) error`; `func userPromptSubmit(h *hookCtx) error`; `func stop(h *hookCtx) error`, registered as `posttooluse`, `posttoolusefailure`, `userpromptsubmit`, `stop`

- [ ] **Step 1: Write the failing test**

`internal/hookcmd/hookcmd_test.go`:

```go
package hookcmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/store"
)

// hookEnv is a temp HOME, CMGO_DIR and project dir, never the real ones.
func hookEnv(t *testing.T) map[string]string {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"HOME": filepath.Join(root, "home"), "CMGO_DIR": filepath.Join(root, "data"), "CLAUDE_PROJECT_DIR": project}
}

var hookNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

// callHook runs one hook fire and returns its stdout.
func callHook(t *testing.T, env map[string]string, event, stdin string) string {
	t.Helper()
	var out bytes.Buffer
	code := Run(event, IO{
		Getenv: func(k string) string { return env[k] },
		Getwd:  os.Getwd,
		Stdin:  strings.NewReader(stdin),
		Stdout: &out,
		Now:    func() time.Time { return hookNow },
	})
	if code != 0 {
		t.Fatalf("%s: exit %d", event, code)
	}
	return out.String()
}

// eventRows returns a session's rows as "type|category|priority|data|hook|bytes".
func eventRows(t *testing.T, env map[string]string, sid string) []string {
	t.Helper()
	st, err := store.Open(filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	evs, err := st.SessionEvents(sid)
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, e := range evs {
		rows = append(rows, fmt.Sprintf("%s|%s|%d|%s|%s|%d", e.Type, e.Category, e.Priority, e.Data, e.SourceHook, e.BytesAvoided))
	}
	return rows
}

func assertRows(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func noDB(t *testing.T, env map[string]string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(env["CMGO_DIR"], "cmgo.db")); err == nil {
		t.Fatal("the store was opened with no event to write")
	}
}

func hookErrors(env map[string]string) string {
	b, _ := os.ReadFile(filepath.Join(env["CMGO_DIR"], "hook-errors.log"))
	return string(b)
}

const curlInput = `{"tool_name":"Bash","tool_input":{"command":"curl https://x.com"},"session_id":"s1"}`

func TestHookPreToolUsePassthrough(t *testing.T) {
	env := hookEnv(t)
	for _, in := range []string{
		`{"tool_name":"Bash","tool_input":{"command":"git status"},"session_id":"s1"}`,
		curlInput, // no live cmgo serve: redirects are gated off
	} {
		if out := callHook(t, env, "pretooluse", in); out != "" {
			t.Fatalf("%s: got %q", in, out)
		}
	}
	// A missing or non-object tool_input is an empty command: Bash guidance, once.
	if out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","session_id":"s2"}`); !strings.Contains(out, `"additionalContext":"<context_guidance>`) {
		t.Fatalf("missing tool_input: got %q", out)
	}
	if out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","tool_input":"x","session_id":"s2"}`); out != "" {
		t.Fatalf("second guidance: got %q", out)
	}
	noDB(t, env)
	if s := hookErrors(env); s != "" {
		t.Fatalf("errors logged: %s", s)
	}
}

func TestHookPreToolUseRedirect(t *testing.T) {
	env := hookEnv(t)
	remove, err := routing.WriteSentinel(filepath.Join(env["CMGO_DIR"], "run"), os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	out := callHook(t, env, "pretooluse", curlInput)
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"context-mode: curl/wget redirected. Call mcp__cmgo__ctx_execute(language, code)`
	if !strings.HasPrefix(out, want) || !strings.HasSuffix(out, "\"}}\n") {
		t.Fatalf("got %q", out)
	}
	rows := eventRows(t, env, "s1")
	if len(rows) != 2 || rows[0] != "bash-redirected|redirect|2|Bash: curl https://x.com|PreToolUse|8192" ||
		!strings.HasPrefix(rows[1], "rejected|rejected-approach|2|Bash: context-mode: curl/wget redirected.") {
		t.Fatalf("rows: %q", rows)
	}
	// A subagent call is gated off even with a live server.
	if out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","tool_input":{"command":"curl https://x.com"},"session_id":"s1","agent_id":"a"}`); out != "" {
		t.Fatalf("subagent: got %q", out)
	}
}

func TestHookPreToolUseSecurityDeny(t *testing.T) {
	env := hookEnv(t)
	dir := filepath.Join(env["CLAUDE_PROJECT_DIR"], ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"permissions":{"deny":["Bash(rm -rf *)"]}}`), 0o644)
	out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","tool_input":{"command":"rm -rf /x"},"session_id":"s1"}`)
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Blocked by security policy: matches deny pattern Bash(rm -rf *)"}}` + "\n"
	if out != want {
		t.Fatalf("got %q", out)
	}
	assertRows(t, eventRows(t, env, "s1"),
		"rejected|rejected-approach|2|Bash: Blocked by security policy: matches deny pattern Bash(rm -rf *)|PreToolUse|0")
}

// The project dir comes from input.cwd when no env var names it.
func TestHookProjectFromInputCwd(t *testing.T) {
	env := hookEnv(t)
	project := env["CLAUDE_PROJECT_DIR"]
	delete(env, "CLAUDE_PROJECT_DIR")
	os.MkdirAll(filepath.Join(project, ".claude"), 0o755)
	os.WriteFile(filepath.Join(project, ".claude", "settings.json"), []byte(`{"permissions":{"deny":["Bash(rm -rf *)"]}}`), 0o644)
	in := fmt.Sprintf(`{"tool_name":"Bash","tool_input":{"command":"rm -rf /x"},"session_id":"s1","cwd":%q}`, project)
	if out := callHook(t, env, "pretooluse", in); !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatalf("got %q", out)
	}
}

var errLine = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\S* pid=\d+ pretooluse: \S`)

func TestHookFailOpen(t *testing.T) {
	env := hookEnv(t)
	if out := callHook(t, env, "pretooluse", `{"tool_name":`); out != "" {
		t.Fatalf("malformed: got %q", out)
	}
	if s := hookErrors(env); !errLine.MatchString(s) || strings.Count(s, "\n") != 1 {
		t.Fatalf("log: %q", s)
	}
	if out := callHook(t, env, "nope", curlInput); out != "" {
		t.Fatalf("unknown event: got %q", out)
	}
}

// Spec §1.2: empty stdin produces no output and is not an error.
func TestHookEmptyStdin(t *testing.T) {
	env := hookEnv(t)
	if out := callHook(t, env, "pretooluse", " \n"); out != "" {
		t.Fatalf("got %q", out)
	}
	if s := hookErrors(env); s != "" {
		t.Fatalf("log: %q", s)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { panic("boom") }

func TestHookRecoversPanic(t *testing.T) {
	env := hookEnv(t)
	dir := filepath.Join(env["CLAUDE_PROJECT_DIR"], ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"permissions":{"deny":["Bash(rm -rf *)"]}}`), 0o644)
	code := Run("pretooluse", IO{
		Getenv: func(k string) string { return env[k] },
		Getwd:  os.Getwd,
		Stdin:  strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"rm -rf /x"}}`),
		Stdout: failWriter{},
		Now:    time.Now,
	})
	if code != 0 || !strings.Contains(hookErrors(env), "pretooluse: panic: boom") {
		t.Fatalf("exit %d, log %q", code, hookErrors(env))
	}
}

func TestHookStdinTimeout(t *testing.T) {
	defer func(d time.Duration) { stdinTimeout = d }(stdinTimeout)
	stdinTimeout = 50 * time.Millisecond
	env := hookEnv(t)
	r, w := io.Pipe() // never closed: stdin that does not end
	defer w.Close()
	var out bytes.Buffer
	code := Run("pretooluse", IO{
		Getenv: func(k string) string { return env[k] }, Getwd: os.Getwd, Stdin: r, Stdout: &out, Now: time.Now,
	})
	if code != 0 || out.Len() != 0 || !strings.Contains(hookErrors(env), "pretooluse: stdin") {
		t.Fatalf("exit %d, out %q, log %q", code, out.String(), hookErrors(env))
	}
}

func sessionStartOut() string {
	return `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":` +
		string(hookio.QuoteString(routing.RoutingBlock(false, false))) + "}}\n"
}

func TestHookSessionStartStartup(t *testing.T) {
	env := hookEnv(t)
	project := env["CLAUDE_PROJECT_DIR"]
	homeRules := filepath.Join(env["HOME"], ".claude", "CLAUDE.md")
	os.MkdirAll(filepath.Dir(homeRules), 0o755)
	os.WriteFile(homeRules, []byte("home rules"), 0o644)
	os.WriteFile(filepath.Join(project, "CLAUDE.md"), []byte("project rules"), 0o644)
	runDir := filepath.Join(env["CMGO_DIR"], "run")
	for _, d := range []string{"guidance-old", "guidance-new"} {
		os.MkdirAll(filepath.Join(runDir, d), 0o700)
	}
	old := hookNow.Add(-8 * 24 * time.Hour)
	os.Chtimes(filepath.Join(runDir, "guidance-old"), old, old)

	if out := callHook(t, env, "sessionstart", `{"session_id":"s1","source":"startup"}`); out != sessionStartOut() {
		t.Fatalf("got %q", out)
	}
	assertRows(t, eventRows(t, env, "s1"),
		"rule|rule|1|"+homeRules+"|SessionStart|0",
		"rule_content|rule|1|home rules|SessionStart|0",
		"rule|rule|1|"+filepath.Join(project, "CLAUDE.md")+"|SessionStart|0",
		"rule_content|rule|1|project rules|SessionStart|0",
		fmt.Sprintf(`session_start|session_start|1|{"source":"startup","project_dir":%q,"started_at":%d}|SessionStart|0`, project, hookNow.Unix()))
	if _, err := os.Stat(filepath.Join(runDir, "guidance-old")); err == nil {
		t.Fatal("old guidance dir not pruned")
	}
	if _, err := os.Stat(filepath.Join(runDir, "guidance-new")); err != nil {
		t.Fatal("fresh guidance dir removed")
	}
	// A missing or null source is startup, as TS input.source ?? "startup".
	callHook(t, env, "sessionstart", `{"session_id":"s2"}`)
	callHook(t, env, "sessionstart", `{"session_id":"s3","source":null}`)
	for _, sid := range []string{"s2", "s3"} {
		if rows := eventRows(t, env, sid); len(rows) != 5 {
			t.Fatalf("%s rows: %q", sid, rows)
		}
	}
}

func TestHookSessionStartOtherSources(t *testing.T) {
	env := hookEnv(t)
	for _, src := range []string{"resume", "compact", "clear"} {
		if out := callHook(t, env, "sessionstart", `{"session_id":"s1","source":"`+src+`"}`); out != sessionStartOut() {
			t.Fatalf("%s: got %q", src, out)
		}
	}
	noDB(t, env)
}

func TestHookPostToolUse(t *testing.T) {
	env := hookEnv(t)
	if out := callHook(t, env, "posttooluse", `{"tool_name":"Read","tool_input":{"file_path":"/p/a.go"},"tool_response":{"type":"text","file":{"filePath":"/p/a.go","content":"x"}},"session_id":"s1"}`); out != "" {
		t.Fatalf("got %q", out)
	}
	assertRows(t, eventRows(t, env, "s1"), "file_read|file|1|/p/a.go|PostToolUse|0")

	env = hookEnv(t)
	callHook(t, env, "posttooluse", `{"tool_name":"WebSearch","tool_input":{"query":"x"},"session_id":"s1"}`)
	noDB(t, env)
}

func TestHookPostToolUseFailure(t *testing.T) {
	env := hookEnv(t)
	if out := callHook(t, env, "posttoolusefailure", `{"tool_name":"Bash","tool_input":{"command":"false"},"error":"exit 1","session_id":"s1"}`); out != "" {
		t.Fatalf("got %q", out)
	}
	assertRows(t, eventRows(t, env, "s1"), "error_tool|error|2|exit 1|PostToolUseFailure|0")
}

func TestHookUserPromptSubmit(t *testing.T) {
	env := hookEnv(t)
	if out := callHook(t, env, "userpromptsubmit", `{"prompt":"hello","session_id":"s1"}`); out != "" {
		t.Fatalf("got %q", out)
	}
	callHook(t, env, "userpromptsubmit", `{"message":"from message","session_id":"s1"}`) // TS: prompt ?? message
	assertRows(t, eventRows(t, env, "s1"),
		"user_prompt|user-prompt|1|hello|UserPromptSubmit|0",
		"intent|intent|4|implement|UserPromptSubmit|0", // short prompt; the second one is deduped
		"user_prompt|user-prompt|1|from message|UserPromptSubmit|0")

	env = hookEnv(t)
	callHook(t, env, "userpromptsubmit", `{"prompt":"<system-reminder>x","session_id":"s1"}`)
	noDB(t, env)
}

func TestHookStop(t *testing.T) {
	env := hookEnv(t)
	if out := callHook(t, env, "stop", `{"session_id":"s1","stop_hook_active":false,"last_assistant_message":"done"}`); out != "{}\n" {
		t.Fatalf("got %q", out)
	}
	if out := callHook(t, env, "stop", `{"session_id":"s1","stop_hook_active":true}`); out != "{}\n" {
		t.Fatalf("got %q", out)
	}
	assertRows(t, eventRows(t, env, "s1"),
		`turn_end|session|1|{"stop_hook_active":false,"last_assistant_message":"done"}|Stop|0`,
		`turn_end|session|1|{"stop_hook_active":true,"last_assistant_message":null}|Stop|0`)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hookcmd/ -run 'TestHookPostToolUse|TestHookUserPromptSubmit|TestHookStop' -v`
Expected: FAIL with "hookcmd_test.go:283: rows:" with no rows, "want: file_read|file|1|/p/a.go|PostToolUse|0"

- [ ] **Step 3: Write minimal implementation**

`internal/hookcmd/hookcmd.go`:

```go
// Package hookcmd runs one Claude Code hook fire (hooks spec §1-§4). It is
// what cmgo-hook executes; it links no MCP server, executor or fetcher, so a
// hook process starts fast.
package hookcmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/lang315/context-mode-go/internal/capture"
	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
)

// stdinTimeout bounds the stdin read (spec §1.2). It is a fallback: Claude
// Code closes stdin. A var so the test need not wait 2 s.
var stdinTimeout = 2 * time.Second

// guidanceMaxAge is how long a guidance marker dir is kept (spec §4).
const guidanceMaxAge = 7 * 24 * time.Hour

// IO is what a hook fire talks over; tests inject env, cwd, stdin and clock.
type IO struct {
	Getenv func(string) string
	Getwd  func() (string, error)
	Stdin  io.Reader
	Stdout io.Writer
	Now    func() time.Time
}

// hookCtx is one parsed hook fire.
type hookCtx struct {
	in     *hookio.Object
	cfg    config.Config
	sid    string
	runDir string // $CMGO_DIR/run: guidance markers and MCP sentinels
	getenv func(string) string
	out    io.Writer
	now    time.Time
}

// hookHandlers maps a hook event name to its handler.
var hookHandlers = map[string]func(*hookCtx) error{
	"pretooluse":         preToolUse,
	"posttooluse":        postToolUse,
	"posttoolusefailure": postToolUseFailure,
	"userpromptsubmit":   userPromptSubmit,
	"stop":               stop,
	"sessionstart":       sessionStart,
}

// Run runs one hook fire and always exits 0 (spec §1.2): an error or panic
// is appended to $CMGO_DIR/hook-errors.log and nothing more is printed.
func Run(event string, hio IO) int {
	h, ok := hookHandlers[event]
	if !ok {
		return 0
	}
	defer func() {
		if r := recover(); r != nil {
			logHookError(hio.Getenv, event, fmt.Errorf("panic: %v", r))
		}
	}()
	if err := runHook(h, hio); err != nil {
		logHookError(hio.Getenv, event, err)
	}
	return 0
}

func runHook(h func(*hookCtx) error, hio IO) error {
	raw, err := readStdin(hio.Stdin)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil // spec §1.2: empty stdin, no output
	}
	in, err := hookio.ParseObject(raw)
	if err != nil {
		return err
	}
	getwd := hio.Getwd
	if cwd, ok := in.String("cwd"); ok && cwd != "" {
		getwd = func() (string, error) { return cwd, nil }
	}
	cfg, err := config.Load(hio.Getenv, getwd)
	if err != nil {
		return err
	}
	return h(&hookCtx{
		in:     in,
		cfg:    cfg,
		sid:    hookio.SessionID(in, hio.Getenv, os.Getppid()),
		runDir: filepath.Join(cfg.DataDir, "run"),
		getenv: hio.Getenv,
		out:    hio.Stdout,
		now:    hio.Now(),
	})
}

// readStdin reads the whole payload, giving up after stdinTimeout. The
// reader goroutine is left blocked on timeout; the process exits anyway.
func readStdin(r io.Reader) ([]byte, error) {
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := hookio.ReadInput(r)
		ch <- result{b, err}
	}()
	select {
	case res := <-ch:
		return res.b, res.err
	case <-time.After(stdinTimeout):
		return nil, errors.New("stdin: no end of input within " + stdinTimeout.String())
	}
}

// logHookError appends one line to $CMGO_DIR/hook-errors.log, best effort.
func logHookError(getenv func(string) string, event string, err error) {
	// The project dir does not affect DataDir, so a failed parse can still log.
	cfg, cerr := config.Load(getenv, func() (string, error) { return "/", nil })
	if cerr != nil {
		return
	}
	if os.MkdirAll(cfg.DataDir, 0o755) != nil {
		return
	}
	f, ferr := os.OpenFile(filepath.Join(cfg.DataDir, "hook-errors.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s pid=%d %s: %v\n", time.Now().Format(time.RFC3339), os.Getpid(), event, err)
}

// insertEvents writes evs in one transaction; with none it does not open the
// store at all (spec §1.1).
func (h *hookCtx) insertEvents(sourceHook string, evs []store.Event) error {
	if len(evs) == 0 {
		return nil
	}
	st, err := store.Open(h.cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	_, err = st.InsertEvents(h.sid, h.cfg.ProjectKey, sourceHook, evs)
	return err
}

// preToolUse routes the call (spec §2) and writes the decision before it
// opens the store, so the DB never delays Claude Code.
func preToolUse(h *hookCtx) error {
	tool, _ := h.in.String("tool_name")
	input, ok := h.in.Object("tool_input")
	if !ok {
		input = &hookio.Object{}
	}
	subagent := h.in.Has("agent_id") || h.in.Has("agent_type")
	dec := routing.Route(tool, input, subagent, routing.Deps{
		ProjectDir: h.cfg.ProjectDir,
		SessionID:  h.sid,
		RunDir:     h.runDir,
		Policy:     security.NewPolicy(h.cfg.ProjectDir, h.cfg.Home),
		Getenv:     h.getenv,
		MCPReady:   func() bool { return routing.MCPReady(h.runDir) },
	})
	if out := routing.Format(dec); out != nil {
		if _, err := h.out.Write(out); err != nil {
			return err
		}
	}
	return h.insertEvents("PreToolUse", routing.Events(tool, dec))
}

// postToolUse records the kept extractors' events (spec §3.3).
func postToolUse(h *hookCtx) error {
	tool, _ := h.in.String("tool_name")
	input, _ := h.in.Object("tool_input")
	resp, _ := h.in.Get("tool_response") // nil when absent
	return h.insertEvents("PostToolUse", capture.PostToolUse(tool, input, resp, h.cfg.Home))
}

func postToolUseFailure(h *hookCtx) error {
	tool, _ := h.in.String("tool_name")
	return h.insertEvents("PostToolUseFailure", capture.PostToolUseFailure(tool, h.in))
}

func userPromptSubmit(h *hookCtx) error {
	prompt, ok := h.in.String("prompt")
	if !ok {
		prompt, _ = h.in.String("message") // TS: input.prompt ?? input.message ?? ""
	}
	return h.insertEvents("UserPromptSubmit", capture.UserPrompt(prompt))
}

// stop always records turn_end, then prints {} as TS stop.mjs does.
func stop(h *hookCtx) error {
	active, _ := h.in.Bool("stop_hook_active")
	var msg *string
	if s, ok := h.in.String("last_assistant_message"); ok {
		msg = &s
	}
	if err := h.insertEvents("Stop", []store.Event{capture.Stop(active, msg)}); err != nil {
		return err
	}
	_, err := io.WriteString(h.out, "{}\n")
	return err
}

// sessionStart prints the routing block for every source (spec §4). Only
// startup also prunes old guidance markers and records the rule and
// session_start events; resume and compact context is sub-project 3.
func sessionStart(h *hookCtx) error {
	out := `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":` +
		string(hookio.QuoteString(routing.RoutingBlock(false, false))) + "}}\n"
	if _, err := io.WriteString(h.out, out); err != nil {
		return err
	}
	src, _ := h.in.String("source")
	if raw, ok := h.in.Get("source"); !ok || string(raw) == "null" {
		src = "startup" // TS: input.source ?? "startup"
	}
	if src != "startup" {
		return nil
	}
	routing.PruneGuidance(h.runDir, h.now, guidanceMaxAge)
	evs := append(capture.SessionStartRules(h.cfg.Home, h.cfg.ProjectDir), capture.SessionStartEvent(src, h.cfg.ProjectDir, h.now))
	return h.insertEvents("SessionStart", evs)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/hookcmd/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/hookcmd/hookcmd.go internal/hookcmd/hookcmd_test.go
git commit -m "feat(hookcmd): PostToolUse, PostToolUseFailure, UserPromptSubmit and Stop hooks"
```

### Task 36: cmgo-hook binary, cmgo hook alias and built-binary hook tests

**Files:**
- Create: `cmd/cmgo-hook/main.go`
- Modify: `cmd/cmgo/main.go`
- Test: `cmd/cmgo-hook/main_test.go`
- Test: `cmd/cmgo/main_test.go`
- Test: `cmd/cmgo/hook_exec_test.go`

**Interfaces:**
- Consumes: `hookcmd.Run(event string, hio hookcmd.IO) int`; `hookcmd.IO`; `hookio.QuoteString`, `routing.RoutingBlock`, `store.Open`, `(*store.Store).SessionEvents` (tests)
- Produces: `cmgo-hook <event>` (exit 2 with no event); `run` dispatches `hook <event>` to `hookcmd.Run`; test helpers `func TestMain(m *testing.M)`, `func builtBinary(t *testing.T, name string) string` (builds `cmgo` and `cmgo-hook` once per test binary), `func hookEnv(t *testing.T) map[string]string`, `func execEnv(env map[string]string) []string`, `func runHookBinary(t *testing.T, env map[string]string, stdin string, args ...string) (string, int)`, `func eventRows(t *testing.T, env map[string]string, sid string) []string`, `func hookErrors(env map[string]string) string`, `const curlInput`

- [ ] **Step 1: Write the failing test**

`cmd/cmgo-hook/main_test.go`:

```go
package main

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// cmgo-hook exists to start fast (hooks spec §1.1): it must not link the
// MCP server or anything only the server needs.
func TestNoServerDeps(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	forbidden := []string{
		"github.com/modelcontextprotocol/go-sdk",
		"net/http",
		"github.com/JohannesKaufmann/html-to-markdown",
		"github.com/lang315/context-mode-go/internal/executor",
		"github.com/lang315/context-mode-go/internal/fetch",
		"github.com/lang315/context-mode-go/internal/mcpserver",
	}
	deps := strings.Fields(string(out))
	if !slices.Contains(deps, "github.com/lang315/context-mode-go/internal/hookcmd") {
		t.Fatalf("cmgo-hook does not link internal/hookcmd; deps: %v", deps)
	}
	for _, dep := range deps {
		for _, f := range forbidden {
			if dep == f || strings.HasPrefix(dep, f+"/") {
				t.Errorf("cmgo-hook links %s", dep)
			}
		}
	}
}
```

`cmd/cmgo/main_test.go`:

```go
package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if out.String() != "cmgo dev\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"nope"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

func TestRunHook(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CMGO_DIR", filepath.Join(dir, "data"))
	t.Setenv("CMGO_PROJECT_DIR", dir)
	var out, errb bytes.Buffer
	if code := run([]string{"hook", "stop"}, strings.NewReader(`{"session_id":"s1"}`), &out, &errb); code != 0 || out.String() != "{}\n" {
		t.Fatalf("stop: exit %d, out %q, stderr %q", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"hook", "nope"}, strings.NewReader("{}"), &out, &errb); code != 0 || out.Len() != 0 {
		t.Fatalf("unknown event: exit %d, out %q", code, out.String())
	}
	if code := run([]string{"hook"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("missing event: exit %d", code)
	}
}
```

`cmd/cmgo/hook_exec_test.go`:

```go
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/store"
)

var (
	binOnce sync.Once
	binDir  string
	binErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if binDir != "" {
		os.RemoveAll(binDir)
	}
	os.Exit(code)
}

// builtBinary builds cmgo and cmgo-hook once per test binary, on first use,
// and returns the path of name.
func builtBinary(t *testing.T, name string) string {
	t.Helper()
	binOnce.Do(func() {
		if binDir, binErr = os.MkdirTemp("", "cmgo-bin-"); binErr != nil {
			return
		}
		if out, err := exec.Command("go", "build", "-o", binDir+"/", ".", "../cmgo-hook").CombinedOutput(); err != nil {
			binErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if binErr != nil {
		t.Fatal(binErr)
	}
	return filepath.Join(binDir, name)
}

// hookEnv is a temp HOME, CMGO_DIR and project dir, never the real ones.
func hookEnv(t *testing.T) map[string]string {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"HOME": filepath.Join(root, "home"), "CMGO_DIR": filepath.Join(root, "data"), "CLAUDE_PROJECT_DIR": project}
}

// execEnv is env plus PATH and nothing else, so no CLAUDE_* or CMGO_* value
// from the developer's shell leaks into the child.
func execEnv(env map[string]string) []string {
	out := []string{"PATH=" + os.Getenv("PATH")}
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// runHookBinary runs the built cmgo-hook with stdin and returns stdout and
// the exit code.
func runHookBinary(t *testing.T, env map[string]string, stdin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(builtBinary(t, "cmgo-hook"), args...)
	cmd.Env = execEnv(env)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out.String(), exit.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	if errb.Len() > 0 {
		t.Errorf("%v: stderr %q", args, errb.String())
	}
	return out.String(), 0
}

// eventRows returns a session's rows as "type|category|priority|data|hook|bytes".
func eventRows(t *testing.T, env map[string]string, sid string) []string {
	t.Helper()
	st, err := store.Open(filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	evs, err := st.SessionEvents(sid)
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, e := range evs {
		rows = append(rows, fmt.Sprintf("%s|%s|%d|%s|%s|%d", e.Type, e.Category, e.Priority, e.Data, e.SourceHook, e.BytesAvoided))
	}
	return rows
}

func hookErrors(env map[string]string) string {
	b, _ := os.ReadFile(filepath.Join(env["CMGO_DIR"], "hook-errors.log"))
	return string(b)
}

const curlInput = `{"tool_name":"Bash","tool_input":{"command":"curl https://x.com"},"session_id":"s1"}`

func TestBinaryHookEvents(t *testing.T) {
	env := hookEnv(t)
	sessionStart := `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":` +
		string(hookio.QuoteString(routing.RoutingBlock(false, false))) + "}}\n"
	cases := []struct {
		event, stdin string
		check        func(string) bool
	}{
		{"pretooluse", `{"tool_name":"Agent","tool_input":{"prompt":"hi"},"session_id":"s1"}`, func(s string) bool {
			return strings.HasPrefix(s, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":{"prompt":"hi\n<context_window_protection>`) &&
				strings.HasSuffix(s, "\"}}}\n")
		}},
		{"posttooluse", `{"tool_name":"Read","tool_input":{"file_path":"/p/a.go"},"tool_response":"x","session_id":"s1"}`, func(s string) bool { return s == "" }},
		{"posttoolusefailure", `{"tool_name":"Bash","error":"exit 1","session_id":"s1"}`, func(s string) bool { return s == "" }},
		{"userpromptsubmit", `{"prompt":"hello","session_id":"s1"}`, func(s string) bool { return s == "" }},
		{"stop", `{"session_id":"s1"}`, func(s string) bool { return s == "{}\n" }},
		{"sessionstart", `{"session_id":"s1","source":"startup"}`, func(s string) bool { return s == sessionStart }},
	}
	for _, c := range cases {
		out, code := runHookBinary(t, env, c.stdin, c.event)
		if code != 0 || !c.check(out) {
			t.Errorf("%s: exit %d, stdout %q", c.event, code, out)
		}
	}
	seen := map[string]bool{}
	for _, r := range eventRows(t, env, "s1") {
		seen[strings.Split(r, "|")[4]] = true
	}
	var hooks []string
	for h := range seen {
		hooks = append(hooks, h)
	}
	sort.Strings(hooks)
	if got := strings.Join(hooks, ","); got != "PostToolUse,PostToolUseFailure,SessionStart,Stop,UserPromptSubmit" {
		t.Fatalf("source hooks in the DB: %s", got)
	}
	if s := hookErrors(env); s != "" {
		t.Fatalf("errors logged: %s", s)
	}
}

func TestBinaryHookMalformedStdin(t *testing.T) {
	env := hookEnv(t)
	out, code := runHookBinary(t, env, `{`, "pretooluse")
	if code != 0 || out != "" {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	if s := hookErrors(env); !regexp.MustCompile(`^\S+ pid=\d+ pretooluse: .+\n$`).MatchString(s) {
		t.Fatalf("log %q", s)
	}
	if _, code := runHookBinary(t, env, ""); code != 2 {
		t.Fatalf("no event: exit %d", code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/cmgo-hook/ ./cmd/cmgo/ -run 'TestNoServerDeps|TestRunHook|TestBinaryHook' -v`
Expected: FAIL with "--- FAIL: TestNoServerDeps" after "main_test.go:27: cmgo-hook does not link internal/hookcmd; deps: [...]" (and `cmd/cmgo` fails too: `TestRunHook` gets exit 2 and the build of `../cmgo-hook` fails)

- [ ] **Step 3: Write minimal implementation**

`cmd/cmgo-hook/main.go`:

```go
// Command cmgo-hook runs one Claude Code hook fire: `cmgo-hook <event>`. It
// is a separate binary from cmgo so a hook process does not load the MCP
// server, executor and fetcher, whose start-up cost every hook fire would
// otherwise pay (hooks spec §1.1).
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/lang315/context-mode-go/internal/hookcmd"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: cmgo-hook <event>")
		os.Exit(2)
	}
	os.Exit(hookcmd.Run(os.Args[1], hookcmd.IO{
		Getenv: os.Getenv, Getwd: os.Getwd, Stdin: os.Stdin, Stdout: os.Stdout, Now: time.Now,
	}))
}
```

`cmd/cmgo/main.go`:

```go
// Command cmgo is a Go rewrite of context-mode for Claude Code.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/lang315/context-mode-go/internal/hookcmd"
)

// version is set at build time: go build -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run dispatches a subcommand. stdin feeds `hook`; serve keeps os.Stdin
// because the MCP transport needs a ReadCloser.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: cmgo <serve|hook|version>")
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(stdout, "cmgo "+version)
		return 0
	case "serve":
		return serve(context.Background(), serveIO{
			getenv: os.Getenv, getwd: os.Getwd, in: os.Stdin, out: os.Stdout, stderr: stderr,
		})
	case "hook": // the same code as cmgo-hook, for manual runs; install registers cmgo-hook
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: cmgo hook <event>")
			return 2
		}
		return hookcmd.Run(args[1], hookcmd.IO{Getenv: os.Getenv, Getwd: os.Getwd, Stdin: stdin, Stdout: stdout, Now: time.Now})
	default:
		fmt.Fprintf(stderr, "cmgo: unknown command %q\n", args[0])
		return 2
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/cmgo-hook/ ./cmd/cmgo/ -run 'TestNoServerDeps|TestRunHook|TestBinaryHook' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/cmgo-hook/main.go cmd/cmgo-hook/main_test.go cmd/cmgo/hook_exec_test.go cmd/cmgo/main.go cmd/cmgo/main_test.go
git commit -m "feat(cmgo-hook): small hook binary, and cmgo hook as an alias"
```

### Task 37: serve writes the MCP-ready sentinel

**Files:**
- Modify: `cmd/cmgo/serve.go`
- Test: `cmd/cmgo/serve_test.go`
- Test: `cmd/cmgo/hook_exec_test.go`

**Interfaces:**
- Consumes: `routing.WriteSentinel(runDir string, pid int) (remove func(), err error)`; `routing.MCPReady(runDir string) bool` (through cmgo-hook)
- Produces: `serve` creates `$CMGO_DIR/run/mcp-ready-<pid>` after the store opens and removes it when it returns; test `TestBinaryServeSentinelGatesRedirect` (spec §6.3 cross-process test: `cmgo serve` plus `cmgo-hook`)

- [ ] **Step 1: Write the failing test**

`cmd/cmgo/serve_test.go`:

```go
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// rpcPeer drives serve() over in-process pipes with newline-delimited JSON-RPC.
type rpcPeer struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *bufio.Reader
	done chan int
	errb *bytes.Buffer
}

func startServe(t *testing.T, env map[string]string) *rpcPeer {
	t.Helper()
	return startServeCtx(t, context.Background(), env)
}

func startServeCtx(t *testing.T, ctx context.Context, env map[string]string) *rpcPeer {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	errb := &bytes.Buffer{}
	p := &rpcPeer{t: t, in: inW, out: bufio.NewReader(outR), done: make(chan int, 1), errb: errb}
	go func() {
		p.done <- serve(ctx, serveIO{
			getenv: func(k string) string { return env[k] },
			getwd:  os.Getwd,
			in:     inR,
			out:    outW,
			stderr: errb,
		})
		outW.Close()
	}()
	return p
}

func (p *rpcPeer) send(msg map[string]any) {
	p.t.Helper()
	b, _ := json.Marshal(msg)
	written := make(chan error, 1)
	go func() { _, err := p.in.Write(append(b, '\n')); written <- err }()
	select {
	case err := <-written:
		if err != nil {
			p.t.Fatal(err)
		}
	case code := <-p.done:
		p.t.Fatalf("server exited with %d before reading the request (stderr: %s)", code, p.errb.String())
	case <-time.After(10 * time.Second):
		p.t.Fatalf("server did not read the request within 10s (stderr: %s)", p.errb.String())
	}
}

func (p *rpcPeer) recv(id int) map[string]any {
	p.t.Helper()
	for {
		line, err := p.out.ReadBytes('\n')
		if err != nil {
			p.t.Fatalf("read: %v (stderr: %s)", err, p.errb.String())
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			p.t.Fatalf("bad frame %q: %v", line, err)
		}
		if got, ok := m["id"].(float64); ok && int(got) == id {
			return m
		}
	}
}

func (p *rpcPeer) initialize() {
	p.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "0"},
	}})
	if r := p.recv(1); r["error"] != nil {
		p.t.Fatalf("initialize: %v", r["error"])
	}
	p.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

func testEnv(t *testing.T) map[string]string {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"HOME": filepath.Join(root, "home"), "CMGO_DIR": filepath.Join(root, "data"), "CMGO_PROJECT_DIR": project}
}

func TestServeListsFiveToolsAndExitsOnEOF(t *testing.T) {
	env := testEnv(t)
	p := startServe(t, env)
	p.initialize()
	p.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	r := p.recv(2)
	var names []string
	for _, tool := range r["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	sort.Strings(names)
	want := "ctx_batch_execute,ctx_execute,ctx_execute_file,ctx_fetch_and_index,ctx_search"
	if strings.Join(names, ",") != want {
		t.Fatalf("tools = %v", names)
	}
	p.in.Close()
	select {
	case code := <-p.done:
		if code != 0 {
			t.Fatalf("exit %d: %s", code, p.errb.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not exit after stdin EOF")
	}
	if _, err := os.Stat(filepath.Join(env["CMGO_DIR"], "cmgo.db")); err != nil {
		t.Fatalf("db not created: %v", err)
	}
}

// Spec §2.5: the MCP-ready sentinel lives exactly as long as serve.
func TestServeWritesSentinel(t *testing.T) {
	env := testEnv(t)
	p := startServe(t, env)
	p.initialize() // the store is open, so the sentinel is written
	sentinel := filepath.Join(env["CMGO_DIR"], "run", fmt.Sprintf("mcp-ready-%d", os.Getpid()))
	if b, err := os.ReadFile(sentinel); err != nil || string(b) != fmt.Sprint(os.Getpid()) {
		t.Fatalf("sentinel %q: %v", b, err)
	}
	p.in.Close()
	if code := <-p.done; code != 0 {
		t.Fatalf("exit %d: %s", code, p.errb.String())
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("sentinel left after serve returned")
	}
}

// A sentinel that cannot be written costs the redirects, not the server.
func TestServeSentinelErrorIsAWarning(t *testing.T) {
	env := testEnv(t)
	os.MkdirAll(env["CMGO_DIR"], 0o755)
	os.WriteFile(filepath.Join(env["CMGO_DIR"], "run"), []byte("x"), 0o644) // run/ cannot be a dir
	p := startServe(t, env)
	p.initialize()
	p.in.Close()
	if code := <-p.done; code != 0 || !strings.Contains(p.errb.String(), "cmgo: mcp-ready sentinel skipped:") {
		t.Fatalf("exit %d, stderr %q", code, p.errb.String())
	}
}

func TestServeFailsOnUnusableDataDir(t *testing.T) {
	env := testEnv(t)
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	env["CMGO_DIR"] = filepath.Join(blocker, "sub") // parent is a file
	p := startServe(t, env)
	select {
	case code := <-p.done:
		if code != 1 || !strings.Contains(p.errb.String(), "cmgo: open") {
			t.Fatalf("exit %d stderr %q", code, p.errb.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve should fail fast")
	}
}

// pgrepCount counts processes whose command line contains pattern.
func pgrepCount(t *testing.T, pattern string) int {
	t.Helper()
	out, _ := exec.Command("pgrep", "-f", pattern).Output()
	return len(strings.Fields(string(out)))
}

// Review focus: Claude Code closing a session while a foreground command is
// still running must not leave that command (or its children) behind.
func TestServeShutdownKillsForegroundRun(t *testing.T) {
	env := testEnv(t)
	p := startServe(t, env)
	p.initialize()
	// Built at runtime so no other command line (e.g. the shell running go test) contains it.
	marker := fmt.Sprintf("sleep %d", 4000+os.Getpid()%5000)
	p.send(map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/call", "params": map[string]any{
		"name": "ctx_execute", "arguments": map[string]any{"language": "shell", "code": marker + " & " + marker + "; wait"},
	}})
	deadline := time.Now().Add(5 * time.Second)
	for pgrepCount(t, marker) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if pgrepCount(t, marker) < 2 {
		t.Fatal("command never started")
	}
	p.in.Close()
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not exit while a foreground command was running")
	}
	deadline = time.Now().Add(3 * time.Second)
	for pgrepCount(t, marker) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := pgrepCount(t, marker); n > 0 {
		exec.Command("pkill", "-f", marker).Run()
		t.Fatalf("%d process(es) left running after shutdown", n)
	}
}

// SIGINT/SIGTERM reach serve as a cancelled ctx: a running foreground command
// with no timeout must be killed and serve must return promptly.
func TestServeCancelKillsForegroundRun(t *testing.T) {
	env := testEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := startServeCtx(t, ctx, env)
	p.initialize()
	marker := fmt.Sprintf("sleep %d", 4001+os.Getpid()%5000)
	p.send(map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/call", "params": map[string]any{
		"name": "ctx_execute", "arguments": map[string]any{"language": "shell", "code": marker},
	}})
	deadline := time.Now().Add(5 * time.Second)
	for pgrepCount(t, marker) < 1 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if pgrepCount(t, marker) < 1 {
		t.Fatal("command never started")
	}
	cancel()
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		exec.Command("pkill", "-f", marker).Run()
		t.Fatal("serve did not return within 3s of cancel")
	}
	deadline = time.Now().Add(2 * time.Second)
	for pgrepCount(t, marker) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := pgrepCount(t, marker); n > 0 {
		exec.Command("pkill", "-f", marker).Run()
		t.Fatalf("%d process(es) left running after cancel", n)
	}
}

// Review focus: two sessions on the same project share one DB; concurrent
// indexing from both must not surface SQLITE_BUSY to the model.
func TestServeConcurrentSessionsSameDB(t *testing.T) {
	env := testEnv(t)
	a, b := startServe(t, env), startServe(t, env)
	a.initialize()
	b.initialize()
	var wg sync.WaitGroup
	errs := make(chan string, 20)
	for _, p := range []*rpcPeer{a, b} {
		wg.Add(1)
		go func(p *rpcPeer) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				id := 100 + i
				p.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{
					"name": "ctx_batch_execute", "arguments": map[string]any{
						"commands": []any{map[string]any{"label": fmt.Sprintf("seq%d", i), "command": "seq 1 2000"}},
						"queries":  []any{"1999"},
					},
				}})
				r := p.recv(id)
				res, _ := r["result"].(map[string]any)
				if r["error"] != nil || res == nil || res["isError"] == true {
					errs <- fmt.Sprintf("call %d: %v %v", i, r["error"], res)
				}
			}
		}(p)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	a.in.Close()
	b.in.Close()
	<-a.done
	<-b.done
}
```

`cmd/cmgo/hook_exec_test.go`:

```go
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/store"
)

var (
	binOnce sync.Once
	binDir  string
	binErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if binDir != "" {
		os.RemoveAll(binDir)
	}
	os.Exit(code)
}

// builtBinary builds cmgo and cmgo-hook once per test binary, on first use,
// and returns the path of name.
func builtBinary(t *testing.T, name string) string {
	t.Helper()
	binOnce.Do(func() {
		if binDir, binErr = os.MkdirTemp("", "cmgo-bin-"); binErr != nil {
			return
		}
		if out, err := exec.Command("go", "build", "-o", binDir+"/", ".", "../cmgo-hook").CombinedOutput(); err != nil {
			binErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if binErr != nil {
		t.Fatal(binErr)
	}
	return filepath.Join(binDir, name)
}

// hookEnv is a temp HOME, CMGO_DIR and project dir, never the real ones.
func hookEnv(t *testing.T) map[string]string {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"HOME": filepath.Join(root, "home"), "CMGO_DIR": filepath.Join(root, "data"), "CLAUDE_PROJECT_DIR": project}
}

// execEnv is env plus PATH and nothing else, so no CLAUDE_* or CMGO_* value
// from the developer's shell leaks into the child.
func execEnv(env map[string]string) []string {
	out := []string{"PATH=" + os.Getenv("PATH")}
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// runHookBinary runs the built cmgo-hook with stdin and returns stdout and
// the exit code.
func runHookBinary(t *testing.T, env map[string]string, stdin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(builtBinary(t, "cmgo-hook"), args...)
	cmd.Env = execEnv(env)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out.String(), exit.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	if errb.Len() > 0 {
		t.Errorf("%v: stderr %q", args, errb.String())
	}
	return out.String(), 0
}

// eventRows returns a session's rows as "type|category|priority|data|hook|bytes".
func eventRows(t *testing.T, env map[string]string, sid string) []string {
	t.Helper()
	st, err := store.Open(filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	evs, err := st.SessionEvents(sid)
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, e := range evs {
		rows = append(rows, fmt.Sprintf("%s|%s|%d|%s|%s|%d", e.Type, e.Category, e.Priority, e.Data, e.SourceHook, e.BytesAvoided))
	}
	return rows
}

func hookErrors(env map[string]string) string {
	b, _ := os.ReadFile(filepath.Join(env["CMGO_DIR"], "hook-errors.log"))
	return string(b)
}

const curlInput = `{"tool_name":"Bash","tool_input":{"command":"curl https://x.com"},"session_id":"s1"}`

func TestBinaryHookEvents(t *testing.T) {
	env := hookEnv(t)
	sessionStart := `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":` +
		string(hookio.QuoteString(routing.RoutingBlock(false, false))) + "}}\n"
	cases := []struct {
		event, stdin string
		check        func(string) bool
	}{
		{"pretooluse", `{"tool_name":"Agent","tool_input":{"prompt":"hi"},"session_id":"s1"}`, func(s string) bool {
			return strings.HasPrefix(s, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":{"prompt":"hi\n<context_window_protection>`) &&
				strings.HasSuffix(s, "\"}}}\n")
		}},
		{"posttooluse", `{"tool_name":"Read","tool_input":{"file_path":"/p/a.go"},"tool_response":"x","session_id":"s1"}`, func(s string) bool { return s == "" }},
		{"posttoolusefailure", `{"tool_name":"Bash","error":"exit 1","session_id":"s1"}`, func(s string) bool { return s == "" }},
		{"userpromptsubmit", `{"prompt":"hello","session_id":"s1"}`, func(s string) bool { return s == "" }},
		{"stop", `{"session_id":"s1"}`, func(s string) bool { return s == "{}\n" }},
		{"sessionstart", `{"session_id":"s1","source":"startup"}`, func(s string) bool { return s == sessionStart }},
	}
	for _, c := range cases {
		out, code := runHookBinary(t, env, c.stdin, c.event)
		if code != 0 || !c.check(out) {
			t.Errorf("%s: exit %d, stdout %q", c.event, code, out)
		}
	}
	seen := map[string]bool{}
	for _, r := range eventRows(t, env, "s1") {
		seen[strings.Split(r, "|")[4]] = true
	}
	var hooks []string
	for h := range seen {
		hooks = append(hooks, h)
	}
	sort.Strings(hooks)
	if got := strings.Join(hooks, ","); got != "PostToolUse,PostToolUseFailure,SessionStart,Stop,UserPromptSubmit" {
		t.Fatalf("source hooks in the DB: %s", got)
	}
	if s := hookErrors(env); s != "" {
		t.Fatalf("errors logged: %s", s)
	}
}

func TestBinaryHookMalformedStdin(t *testing.T) {
	env := hookEnv(t)
	out, code := runHookBinary(t, env, `{`, "pretooluse")
	if code != 0 || out != "" {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	if s := hookErrors(env); !regexp.MustCompile(`^\S+ pid=\d+ pretooluse: .+\n$`).MatchString(s) {
		t.Fatalf("log %q", s)
	}
	if _, code := runHookBinary(t, env, ""); code != 2 {
		t.Fatalf("no event: exit %d", code)
	}
}

// Spec §6.3: cmgo serve and cmgo-hook are separate processes that must
// resolve the same sentinel dir from the same CMGO_DIR.
func TestBinaryServeSentinelGatesRedirect(t *testing.T) {
	env := hookEnv(t)
	serve := exec.Command(builtBinary(t, "cmgo"), "serve")
	serve.Env = execEnv(env)
	stdin, err := serve.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var serveErr bytes.Buffer
	serve.Stderr = &serveErr
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- serve.Wait() }()
	defer func() {
		serve.Process.Kill()
		<-done
	}()

	sentinel := filepath.Join(env["CMGO_DIR"], "run", fmt.Sprintf("mcp-ready-%d", serve.Process.Pid))
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(sentinel); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no sentinel after 10s (serve stderr %q)", serveErr.String())
		}
	}
	out, code := runHookBinary(t, env, curlInput, "pretooluse")
	if code != 0 || !strings.Contains(out, `"permissionDecision":"deny","permissionDecisionReason":"context-mode: curl/wget redirected.`) {
		t.Fatalf("with serve: exit %d, stdout %q", code, out)
	}

	stdin.Close()
	select {
	case err := <-done:
		done <- err // for the deferred cleanup
		if err != nil {
			t.Fatalf("serve: %v (stderr %q)", err, serveErr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not exit after stdin closed")
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("sentinel left after serve exited")
	}
	if out, code := runHookBinary(t, env, curlInput, "pretooluse"); code != 0 || out != "" {
		t.Fatalf("without serve: exit %d, stdout %q", code, out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/cmgo/ -run 'TestServe|TestBinary' -v`
Expected: FAIL with "hook_exec_test.go:207: no sentinel after 10s (serve stderr \"\")" then "--- FAIL: TestBinaryServeSentinelGatesRedirect"

- [ ] **Step 3: Write minimal implementation**

`cmd/cmgo/serve.go`:

```go
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/lang315/context-mode-go/internal/fetch"
	"github.com/lang315/context-mode-go/internal/mcpserver"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	sourceRetention = 14 * 24 * time.Hour // spec §2.1 SOURCE_RETENTION
	cleanupEvery    = 24 * time.Hour      // spec §2.2: at most once per 24 h
)

// serveIO is what `cmgo serve` talks over; tests pass pipes.
type serveIO struct {
	getenv func(string) string
	getwd  func() (string, error)
	in     io.ReadCloser
	out    io.WriteCloser
	stderr io.Writer
}

// serve runs the MCP server until stdin closes or SIGINT/SIGTERM arrives,
// then terminates background process groups and closes the DB.
func serve(ctx context.Context, sio serveIO) int {
	cfg, err := config.Load(sio.getenv, sio.getwd)
	if err != nil {
		fmt.Fprintf(sio.stderr, "cmgo: config: %v\n", err)
		return 1
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(sio.stderr, "cmgo: open %s: %v\n", cfg.DBPath, err)
		return 1
	}
	defer st.Close()
	// Hooks redirect curl and WebFetch only while a server is live (spec §2.5).
	if remove, err := routing.WriteSentinel(filepath.Join(cfg.DataDir, "run"), os.Getpid()); err != nil {
		fmt.Fprintf(sio.stderr, "cmgo: mcp-ready sentinel skipped: %v\n", err)
	} else {
		defer remove()
	}
	if _, err := st.CleanupIfDue(time.Now(), sourceRetention, cleanupEvery); err != nil {
		fmt.Fprintf(sio.stderr, "cmgo: cleanup skipped: %v\n", err) // never fatal
	}
	ex := executor.New(cfg.ProjectDir)
	defer ex.Shutdown()

	d := &mcpserver.Deps{
		Cfg:    cfg,
		Store:  st,
		Exec:   ex,
		Policy: security.NewPolicy(cfg.ProjectDir, cfg.Home),
		Fetch:  fetch.New(cfg.FetchStrict),
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// A signal cancels ctx; kill running commands now instead of waiting for Run to drain.
	defer context.AfterFunc(ctx, ex.Shutdown)()
	// Lift the 16 MB frame cap: TS accepted any size of tool argument.
	t := &mcp.IOTransport{Reader: sio.in, Writer: sio.out, MaxLineLength: -1}
	if err := mcpserver.New(d, version).Run(ctx, t); err != nil && ctx.Err() == nil && err != io.EOF {
		fmt.Fprintf(sio.stderr, "cmgo: serve: %v\n", err)
		return 1
	}
	return 0
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/cmgo/ -run 'TestServe|TestBinary' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/cmgo/hook_exec_test.go cmd/cmgo/serve.go cmd/cmgo/serve_test.go
git commit -m "feat(cmgo): serve writes the MCP-ready sentinel for the hooks"
```

### Task 38: install and uninstall commands

**Files:**
- Create: `cmd/cmgo/install.go`
- Modify: `cmd/cmgo/main.go`
- Test: `cmd/cmgo/install_test.go`

**Interfaces:**
- Consumes: `install.Install(o install.Options) error`; `install.Uninstall(o install.Options) error`; `install.Options{SettingsPath, ClaudeJSONPath, Binary, HookBinary string; DisableTSPlugin, DryRun bool; Stdout, Stderr io.Writer; LookPath func(string) (string, error); Run func(name string, args ...string) error}` (Task 14)
- Produces: `func installCmd(name string, args []string, stdout, stderr io.Writer) int`; `func runInstall(name, binary string, disableTS, dryRun bool, stdout, stderr io.Writer) error`; `func settingsPath(home string) string`; `run` dispatches `install` and `uninstall`; the usage line lists `serve|hook|install|uninstall|version`

- [ ] **Step 1: Write the failing test**

`cmd/cmgo/install_test.go`:

```go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installEnv points HOME at a temp dir and PATH at a dir holding only a fake
// claude that logs its arguments, so no test reaches the real ~/.claude.
func installEnv(t *testing.T) (home, claudeLog string) {
	t.Helper()
	home = t.TempDir()
	bin := t.TempDir()
	claudeLog = filepath.Join(bin, "claude.log")
	script := "#!/bin/sh\necho \"$@\" >> '" + claudeLog + "'\necho fake-claude-out\necho fake-claude-err >&2\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("PATH", bin)
	return home, claudeLog
}

// fakeBinaries makes a dir holding cmgo and cmgo-hook and returns cmgo's path.
func fakeBinaries(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range []string{"cmgo", "cmgo-hook"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "cmgo")
}

func runCmd(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func TestInstallAndUninstall(t *testing.T) {
	home, claudeLog := installEnv(t)
	cmgo := fakeBinaries(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	code, out, errs := runCmd(t, "install", "--binary", cmgo)
	if code != 0 || !strings.Contains(out, "cmgo: wrote "+settings) || !strings.Contains(out, "fake-claude-out") || !strings.Contains(errs, "fake-claude-err") {
		t.Fatalf("exit %d\nstdout %q\nstderr %q", code, out, errs)
	}
	b, _ := os.ReadFile(settings)
	if !strings.Contains(string(b), `"command": "\"`+cmgo+`-hook\" pretooluse"`) {
		t.Fatalf("settings:\n%s", b)
	}
	// claude.json now says cmgo is registered, as claude mcp add would leave it.
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"cmgo":{}}}`), 0o644)
	if code, out, errs := runCmd(t, "uninstall"); code != 0 {
		t.Fatalf("uninstall: exit %d\nstdout %q\nstderr %q", code, out, errs)
	}
	if b, _ := os.ReadFile(settings); strings.Contains(string(b), "cmgo") {
		t.Fatalf("settings after uninstall:\n%s", b)
	}
	log, _ := os.ReadFile(claudeLog)
	want := "mcp add --scope user cmgo -- " + cmgo + " serve\nmcp remove --scope user cmgo\n"
	if string(log) != want {
		t.Fatalf("claude calls %q, want %q", log, want)
	}
}

func TestInstallConfigDirAndNoClaude(t *testing.T) {
	home, _ := installEnv(t)
	cmgo := fakeBinaries(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "~/cfg")
	t.Setenv("PATH", t.TempDir()) // no claude on PATH: print the command
	code, out, errs := runCmd(t, "install", "--binary", cmgo)
	settings := filepath.Join(home, "cfg", "settings.json")
	if code != 0 || !strings.Contains(out, "cmgo: wrote "+settings) ||
		!strings.Contains(out, "run: claude mcp add --scope user cmgo -- "+cmgo+" serve\n") {
		t.Fatalf("exit %d\nstdout %q\nstderr %q", code, out, errs)
	}
}

// A dry run needs no cmgo-hook on disk: it only shows the file.
func TestInstallDefaultBinaryDryRun(t *testing.T) {
	home, claudeLog := installEnv(t)
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	code, out, errs := runCmd(t, "install", "--dry-run")
	if code != 0 || !strings.Contains(out, `\"`+filepath.Join(filepath.Dir(exe), "cmgo-hook")+`\" sessionstart`) {
		t.Fatalf("exit %d\nstdout %q\nstderr %q", code, out, errs)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err == nil {
		t.Fatal("dry run wrote the settings file")
	}
	if _, err := os.Stat(claudeLog); err == nil {
		t.Fatal("dry run ran claude")
	}
}

func TestInstallErrors(t *testing.T) {
	home, claudeLog := installEnv(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	alone := filepath.Join(t.TempDir(), "cmgo") // no cmgo-hook next to it
	if code, _, errs := runCmd(t, "install", "--binary", alone); code != 1 || !strings.HasPrefix(errs, "cmgo: hook binary missing: ") {
		t.Fatalf("no cmgo-hook: exit %d, stderr %q", code, errs)
	}
	if _, err := os.Stat(settings); err == nil {
		t.Fatal("settings written without cmgo-hook")
	}
	if _, err := os.Stat(claudeLog); err == nil {
		t.Fatal("claude ran without cmgo-hook")
	}
	os.MkdirAll(filepath.Dir(settings), 0o755)
	os.WriteFile(settings, []byte("{not json"), 0o644)
	if code, _, errs := runCmd(t, "install", "--binary", fakeBinaries(t)); code != 1 || !strings.HasPrefix(errs, "cmgo: parse ") {
		t.Fatalf("unparseable: exit %d, stderr %q", code, errs)
	}
	if b, _ := os.ReadFile(settings); string(b) != "{not json" {
		t.Fatalf("settings changed: %q", b)
	}
	if code, _, _ := runCmd(t, "install", "--nope"); code != 2 {
		t.Fatalf("bad flag: exit %d", code)
	}
	if code, _, _ := runCmd(t, "uninstall", "--binary", "/x"); code != 2 {
		t.Fatalf("uninstall --binary: exit %d", code)
	}
	if code, _, _ := runCmd(t, "install", "extra"); code != 2 {
		t.Fatalf("extra argument: exit %d", code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/cmgo/ -run 'TestInstall' -v`
Expected: FAIL with "install_test.go:53: exit 2" with stderr "cmgo: unknown command \"install\"", then "--- FAIL: TestInstallAndUninstall"

- [ ] **Step 3: Write minimal implementation**

`cmd/cmgo/install.go`:

```go
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lang315/context-mode-go/internal/install"
)

// installCmd runs `cmgo install` or `cmgo uninstall` (spec §5). A usage
// error exits 2, any other error 1.
func installCmd(name string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cmgo "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var binary string
	var disableTS bool
	if name == "install" {
		fs.StringVar(&binary, "binary", "", "path to the cmgo binary (default: this executable)")
		fs.BoolVar(&disableTS, "disable-ts-plugin", false, "turn off an enabled context-mode TS plugin")
	}
	dryRun := fs.Bool("dry-run", false, "print the new settings file instead of writing it")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "cmgo %s: unexpected argument %q\n", name, fs.Arg(0))
		return 2
	}
	if err := runInstall(name, binary, disableTS, *dryRun, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "cmgo: %v\n", err)
		return 1
	}
	return 0
}

func runInstall(name, binary string, disableTS, dryRun bool, stdout, stderr io.Writer) error {
	home := os.Getenv("HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		home = h
	}
	o := install.Options{
		SettingsPath:    settingsPath(home),
		ClaudeJSONPath:  filepath.Join(home, ".claude.json"),
		DisableTSPlugin: disableTS,
		DryRun:          dryRun,
		Stdout:          stdout,
		Stderr:          stderr,
		LookPath:        exec.LookPath,
		Run: func(name string, args ...string) error {
			c := exec.Command(name, args...)
			c.Stdout, c.Stderr = stdout, stderr
			return c.Run()
		},
	}
	if name == "uninstall" {
		return install.Uninstall(o)
	}
	var err error
	if binary == "" {
		// The hooks must keep working through a symlink that later moves.
		if binary, err = os.Executable(); err == nil {
			binary, err = filepath.EvalSymlinks(binary)
		}
	} else {
		binary, err = filepath.Abs(binary)
	}
	if err != nil {
		return err
	}
	o.Binary = binary
	// Claude Code would run a missing hook binary on every event and fail.
	o.HookBinary = filepath.Join(filepath.Dir(binary), "cmgo-hook")
	if _, err := os.Stat(o.HookBinary); err != nil && !dryRun {
		return fmt.Errorf("hook binary missing: %w (build cmd/cmgo-hook next to cmgo)", err)
	}
	return install.Install(o)
}

// settingsPath is $CLAUDE_CONFIG_DIR/settings.json ("~" expanded), else
// ~/.claude/settings.json.
func settingsPath(home string) string {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	switch {
	case dir == "":
		dir = filepath.Join(home, ".claude")
	case dir == "~" || strings.HasPrefix(dir, "~/"):
		dir = filepath.Join(home, dir[1:])
	}
	return filepath.Join(dir, "settings.json")
}
```

`cmd/cmgo/main.go`:

```go
// Command cmgo is a Go rewrite of context-mode for Claude Code.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/lang315/context-mode-go/internal/hookcmd"
)

// version is set at build time: go build -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run dispatches a subcommand. stdin feeds `hook`; serve keeps os.Stdin
// because the MCP transport needs a ReadCloser.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: cmgo <serve|hook|install|uninstall|version>")
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(stdout, "cmgo "+version)
		return 0
	case "serve":
		return serve(context.Background(), serveIO{
			getenv: os.Getenv, getwd: os.Getwd, in: os.Stdin, out: os.Stdout, stderr: stderr,
		})
	case "hook": // the same code as cmgo-hook, for manual runs; install registers cmgo-hook
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: cmgo hook <event>")
			return 2
		}
		return hookcmd.Run(args[1], hookcmd.IO{Getenv: os.Getenv, Getwd: os.Getwd, Stdin: stdin, Stdout: stdout, Now: time.Now})
	case "install", "uninstall":
		return installCmd(args[0], args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "cmgo: unknown command %q\n", args[0])
		return 2
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go vet ./... && go test -race ./cmd/... ./internal/hookcmd/ -count=1 -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/cmgo/install.go cmd/cmgo/install_test.go cmd/cmgo/main.go
git commit -m "feat(cmgo): install and uninstall commands"
```

### Task 39: hook latency bench

**Files:**
- Create: `bench/hook-latency.py`

**Interfaces:**
- Consumes: the built `cmgo-hook` binary (`cmgo-hook <event>`, stdin JSON, exit 0)
- Produces: `python3 -I bench/hook-latency.py <cmgo-hook> [runs=200]`, printing the load average, two context timings, `<case>: p50 X ms p95 Y ms PASS|FAIL` per case and a final `PASS` or `FAIL` (exit 0 or 1)

- [ ] **Step 1: Write the failing test**

The bench is its own acceptance check (spec §6.4 criterion 1); there is no Go test. Build the binary it measures:

```bash
go build -o bin/ ./cmd/cmgo-hook
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -I bench/hook-latency.py bin/cmgo-hook`
Expected: FAIL with "can't open file '.../bench/hook-latency.py': [Errno 2] No such file or directory"

- [ ] **Step 3: Write minimal implementation**

`bench/hook-latency.py`:

```python
#!/usr/bin/env python3 -I
"""Criterion 1: `cmgo-hook <event>` wall time p95 <= 10 ms per case (spec §6.4).

Run: go build -o bin/ ./cmd/cmgo-hook && python3 -I bench/hook-latency.py bin/cmgo-hook [runs]

Every case runs `runs` times against a temp CMGO_DIR, HOME and project dir
(never the real ones), after one warm-up call that creates the DB. The time
is subprocess.run wall clock, so it includes the fork/exec cost. Then
posttooluse runs from 4 threads at once, `runs` fires each, into the same DB.
For context it also times /usr/bin/true and cmgo-hook with no arguments
(usage, exit 2) the same way, and prints the 1-minute load average.
"""
import json
import math
import os
import shutil
import statistics
import subprocess
import sys
import tempfile
import threading
import time

LIMIT_MS, WORKERS = 10.0, 4


def timed(argv, env, data=b""):
    t0 = time.perf_counter()
    p = subprocess.run(argv, input=data, env=env, capture_output=True)
    return (time.perf_counter() - t0) * 1000, p


def fire(binary, env, event, payload):
    """Runs one hook; returns (ms, stdout). A non-zero exit is a bench error."""
    ms, p = timed([binary, event], env, json.dumps(payload).encode())
    if p.returncode != 0:
        raise SystemExit(f"{event}: exit {p.returncode}: {p.stderr.decode()}")
    return ms, p.stdout


def p95(xs):
    return sorted(xs)[math.ceil(0.95 * len(xs)) - 1]


def stats(times):
    return f"p50 {statistics.median(times):.2f} ms p95 {p95(times):.2f} ms"


def report(name, times):
    ok = p95(times) <= LIMIT_MS
    print(f"{name}: {stats(times)} {'PASS' if ok else 'FAIL'}")
    return ok


def read_payload(i, sid):
    return {"tool_name": "Read", "tool_input": {"file_path": f"/src/file{i}.go"},
            "tool_response": {"type": "text", "file": {"filePath": f"/src/file{i}.go", "content": "package x\n"}},
            "session_id": sid}


def main(binary, runs):
    binary = os.path.abspath(binary)
    root = tempfile.mkdtemp(prefix="cmgo-hook-bench-")
    try:
        data, home, project = (os.path.join(root, d) for d in ("data", "home", "project"))
        for d in (home, project):
            os.makedirs(d)
        env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "HOME": home,
               "CMGO_DIR": data, "CMGO_PROJECT_DIR": project}
        print(f"load average (1 min) at start: {os.getloadavg()[0]:.2f}")
        for name, argv in (("/usr/bin/true", ["/usr/bin/true"]), ("cmgo-hook, no args", [binary])):
            print(f"context {name}: {stats([timed(argv, env)[0] for _ in range(runs)])}")
        fire(binary, env, "posttooluse", read_payload(-1, "warm"))  # creates and migrates the DB

        # A fake live server so the curl redirect fires: this process's pid.
        run_dir = os.path.join(data, "run")
        os.makedirs(run_dir, exist_ok=True)
        with open(os.path.join(run_dir, f"mcp-ready-{os.getpid()}"), "w") as f:
            f.write(str(os.getpid()))

        cases = [
            ("pretooluse passthrough", "pretooluse",
             lambda i: {"tool_name": "Bash", "tool_input": {"command": "git status"}, "session_id": "bench"},
             lambda out: out == b""),
            ("pretooluse curl redirect", "pretooluse",
             lambda i: {"tool_name": "Bash", "tool_input": {"command": "curl https://example.com"}, "session_id": "bench"},
             lambda out: b'"permissionDecision":"deny"' in out),
            ("posttooluse Read capture", "posttooluse", lambda i: read_payload(i, "bench"), lambda out: out == b""),
            ("userpromptsubmit", "userpromptsubmit",
             lambda i: {"prompt": f"please look at item {i} of the list and explain it in detail to me", "session_id": "bench"},
             lambda out: out == b""),
            ("sessionstart startup", "sessionstart",
             lambda i: {"source": "startup", "session_id": f"bench-start-{i}"},
             lambda out: out.startswith(b'{"hookSpecificOutput":{"hookEventName":"SessionStart"')),
        ]
        ok = True
        for name, event, payload, check in cases:
            times = []
            for i in range(runs):
                ms, out = fire(binary, env, event, payload(i))
                if i == 0 and not check(out):
                    raise SystemExit(f"{name}: unexpected stdout {out[:200]!r}")
                times.append(ms)
            ok &= report(name, times)

        times, lock, errors = [], threading.Lock(), []

        def worker(w):
            try:
                for i in range(runs):
                    ms, _ = fire(binary, env, "posttooluse", read_payload(w * runs + i, f"bench-w{w}"))
                    with lock:
                        times.append(ms)
            except BaseException as e:  # SystemExit from fire included
                errors.append(e)

        threads = [threading.Thread(target=worker, args=(w,)) for w in range(WORKERS)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        if errors:
            raise SystemExit(f"concurrent posttooluse: {errors[0]}")
        ok &= report(f"posttooluse x{WORKERS} concurrent", times)

        log = os.path.join(data, "hook-errors.log")
        if os.path.exists(log):
            with open(log) as f:
                lines = f.read().splitlines()
            print(f"FAIL: {len(lines)} hook error(s), first: {lines[0]}")
            ok = False
        print(f"load average (1 min) at end: {os.getloadavg()[0]:.2f}")
        print("PASS" if ok else "FAIL")
        return 0 if ok else 1
    finally:
        shutil.rmtree(root, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1], int(sys.argv[2]) if len(sys.argv) > 2 else 200))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -I bench/hook-latency.py bin/cmgo-hook`
Expected: PASS (every case p95 ≤ 10 ms, the concurrent case included, and an empty `hook-errors.log`). Every number includes Python's spawn cost, and all of them grow with machine load; the context lines and the load average show how much. Output of the draft on 2026-10-21 (macOS arm64, other agents' builds running):

```text
load average (1 min) at start: 5.74
context /usr/bin/true: p50 2.02 ms p95 2.87 ms
context cmgo-hook, no args: p50 5.21 ms p95 6.02 ms
pretooluse passthrough: p50 5.31 ms p95 6.50 ms PASS
pretooluse curl redirect: p50 5.95 ms p95 7.65 ms PASS
posttooluse Read capture: p50 6.25 ms p95 7.27 ms PASS
userpromptsubmit: p50 6.37 ms p95 7.49 ms PASS
sessionstart startup: p50 6.26 ms p95 7.35 ms PASS
posttooluse x4 concurrent: p50 7.69 ms p95 9.79 ms PASS
load average (1 min) at end: 5.88
PASS
```

- [ ] **Step 5: Commit**

```bash
git add bench/hook-latency.py
git commit -m "bench: cmgo-hook latency per event and under 4 concurrent writers"
```

---

## Appendix A: Porting notes by package

These notes are the deliberate choices made while drafting. The ones that change user-visible behavior are also rows in hooks spec §2.3 or §3.4. If a review disagrees with one, change the spec first, then the code.

### golden files (Task 1)

This fragment ports the TS hook behavior that spec §6.2 pins with golden files. `tools/golden-gen/gen-hooks.mjs` runs by hand on Node against the TS fork at `$TS_REPO`, commit `a5e6dd31`, and never runs under `go test`. It merges the two research generators. The first is `golden-seed/gen-pretooluse.mjs`, which calls `routePreToolUse` (`hooks/core/routing.mjs:670`) and `formatDecision` (`hooks/core/formatters.mjs:341`). The second is `internal/capture/testdata/gen-capture.mjs`, which calls `extractEvents` and `extractUserEvents` from `hooks/session-extract.bundle.mjs`. It also renders `createRoutingBlock` and the guidance factories (`hooks/routing-block.mjs:16-96`). It writes `testdata/golden/hooks-pretooluse.json` (37 cases), `hooks-capture.json` (72 tool cases, 36 prompts) and `hooks-strings.json` (3 routing blocks, 7 texts). The routing and capture golden tests then read these files from `../../testdata/golden/`, and their package-local copies are deleted. `testdata/golden/HOOKS-KNOWN-DIFFS.md` lists every case whose expected value differs by design.

**Parity notes**
- **One process per PreToolUse case.** TS keeps a process-wide in-memory guidance set (`routing.mjs:51,121-142`, R1 P18). The seed generator ran all cases in one process, so cases 19 (`Read`, session `s2`) and 36 (subagent `Bash`, session `sub1`) got no guidance there. A real hook is a fresh process and shows guidance in both cases. The generator spawns one child `node` per case. The children run in order and share one `TMPDIR`, so the on-disk markers of session `s1` (`routing.mjs:116-119`) carry over as they do between real hook calls. After this change, 19 and 36 match cmgo and are not listed as differences.
- **Sentinel dir.** `sentinelDir()` (`hooks/core/mcp-ready.mjs`) is `/tmp` on Unix unless `CONTEXT_MODE_MCP_SENTINEL_DIR` is set. The generator sets it to `<temp>/sentinel`, so it never writes to `/tmp` and never sees a context-mode server that is running on the machine. The parent writes the sentinel with its own pid; it outlives every child.
- **Clean environment.** Children get only `PATH`, `LANG`, `TS_REPO`, `HOME`, `TMPDIR`, `CONTEXT_MODE_DIR` and `CONTEXT_MODE_MCP_SENTINEL_DIR`, all pointing into the temp dir. No `CLAUDE_*` or `CONTEXT_MODE_*` variable from the developer's shell reaches the TS code.
- **Tool names.** Every `mcp__plugin_context-mode_context-mode__` in the written text becomes `mcp__cmgo__` (spec §1.3). The routing blocks and guidance texts are rendered with the namer `t => "mcp__cmgo__" + t`. The redirect texts (curl, inline HTTP, build tool, WebFetch) are not exported by TS, so the generator takes them from `routePreToolUse` decisions: it strips the `echo "` … `"` wrapper, or takes the deny `reason`.
- **Capture data.** The capture cases and the line-per-case writer, including `\u` escapes for invisible characters, are copied unchanged from `gen-capture.mjs`. `hooks-capture.json` equals the old `internal/capture/testdata/capture.json` byte for byte, apart from the added `"tsCommit"` line.
- **Paths.** The temp dir is resolved with `realpathSync` and written as `<BASE>`. Two runs give identical bytes.
- **Known differences.** The two strings tests in `internal/routing/text_test.go` still read `internal/routing/testdata/strings/`, which this fragment does not own. The new `TestGoldenStrings` checks the same texts against `hooks-strings.json`, so the old test and its files can be retired later.

### internal/hookio (Tasks 2-4)

Ports the stdin and JSON plumbing every TS hook shares: `parseStdin` (BOM strip, trim, empty input gives `{}`, `hooks/session-helpers.mjs:248-251`), the stdin reader (`hooks/core/stdin.mjs`), and `getSessionId` (`hooks/session-helpers.mjs:364-376`, R2 §5.1, R1 §1.1). It adds what Go needs to reproduce JS output byte for byte: an order-preserving JSON object (JS objects keep insertion order, Go maps do not), a JSON writer whose string escaping equals `JSON.stringify`, and UTF-16 length helpers for the JS `.length` and `.slice` limits other packages port.

**Parity notes (decided, flag in review if wrong):**
- String escaping equals `JSON.stringify`: no HTML escaping of `<`, `>`, `&`; `\b \f \n \r \t \" \\`; other controls as a lowercase `\u00XX` escape; DEL and everything else raw. Go's encoder always escapes U+2028 and U+2029 (even with `SetEscapeHTML(false)`) and writes invalid UTF-8 as an escaped U+FFFD. `Marshal` undoes exactly those three escapes with a scanner that walks escape pairs, so an escaped backslash followed by the text `u2028` is never touched. Expected values in the tests come from `node -e 'JSON.stringify(...)'`.
- `QuoteString(s)` is `Marshal(s)`, so there is one escaping path.
- `Object.MarshalJSON` keeps each value's bytes as received, compacted with `json.Compact` (which adds no escapes), then runs the same unescape pass. So `o.MarshalJSON()` and `Marshal(o)` agree. Other escapes in input values stay as received (input `\u0041` is echoed as is; JS would write `A`). The two forms are the same JSON value.
- Lone surrogates cannot occur in a Go string: invalid UTF-8 counts as U+FFFD (one UTF-16 unit) in `JSLen`, and `Marshal` writes it as a raw U+FFFD, as JS does after decoding stdin.
- `JSPrefix` stops before a surrogate pair where JS `.slice(0, n)` would split it, so the result is always valid UTF-8 (contract).
- `ParseObject` trims Unicode space plus U+FEFF from both ends, as `raw.replace(/^\uFEFF/, "").trim()` does. Go's `unicode.IsSpace` also counts U+0085, which JS `trim` does not; the only effect is that such input parses instead of failing. Trailing data after the object (`{"a":1}x`) is an error, as in `JSON.parse`. A repeated key keeps its first position and its last value, as `JSON.parse` does.
- `Marshal` of a Go map sorts its keys (encoding/json). Use a struct or an `*Object` when key order must follow insertion.
- `ReadInput` has no idle timeout: the contract signature takes only a reader, so the 2 s stdin idle fallback (spec §1.2) belongs to `cmd/cmgo`. It does not repair invalid UTF-8; Node's UTF-8 stdin decoding would.
- `SessionID` follows spec §1.4: `conversation_id` and `sessionId` are dropped (not sent by Claude Code). A non-string `transcript_path` or `session_id` falls through to the next rule, where TS would crash (`.match` on a number) or use the number. Empty strings fall through, as falsy values do in JS.
- `FileID` also hashes `.` and `..`. They match `^[A-Za-z0-9._-]{1,128}$` but would name a directory when used as a path element. This is stricter than spec §1.4 and changes nothing for real session ids.

### internal/store and internal/security (Tasks 5-9)

Adds the session event tables to `cmgo.db` and the hook-side command evaluator. The store part ports the session event writer of `src/session/db.ts`: the constants (DB:638-642), the dedup and eviction statements (DB:962-983), `insertEvent` / `bulkInsertEvents` (DB:1137-1303) and the startup sweep `cleanupOldSessions` (DB:1696-1705), on top of the core `internal/store` connection rules (one connection, `_txlock=immediate`, `busy_timeout(30000)`). The security part ports `evaluateCommand` (`src/security.ts:512-563`, with `matchesAnyPattern` at 141-152) and extends the settings parser to keep `permissions.allow` and `permissions.ask` `Bash(...)` entries (`readSingleSettings`, security.ts:321-351). Spec: hooks design §2.2, §3.1, §3.2.

**Parity notes (decided, flag in review if wrong):**
- Eviction at the cap removes `ORDER BY priority DESC, id ASC LIMIT 1`, the oldest of the least important rows. TS used `priority ASC` and evicted priority-1 rows first (R2 Port note 1, spec §3.2).
- `event_count` grows by the number of rows inserted. TS `bulkInsertEvents` added 1 per call, whatever the batch size.
- An empty batch still runs the `session_meta` upsert and the `last_event_at` update, so SessionStart can register a session. TS returned early.
- `data` is cut to 32 KiB at a UTF-8 boundary before hashing, so `data_hash` is the hash of the stored bytes. TS did not cut.
- Dedup reads the last 5 rows of the session inside the transaction, so rows inserted earlier in the same batch count (DB:962-968). The row count is read once per transaction and tracked in Go, as `bulkInsertEvents` does.
- Timestamps are `datetime('now')`, UTC `YYYY-MM-DD HH:MM:SS`, like `sources.indexed_at`. The cleanup cutoff is formatted in Go with the same layout (`sqliteTime`), so a plain string comparison is exact.
- The session sweep is keyed on `last_event_at` (TS used `started_at`), uses `SessionRetention` (7 days), not the sources `retention` argument, and runs inside the existing `CleanupIfDue` transaction behind the same gate. The return value still counts sources only.
- The attribution columns (`project_dir`, `attribution_source`, `attribution_confidence`, `bytes_returned`) are dropped (spec §3.4).
- `EvaluateCommand` returns the allow rule matched by the last element, as TS does. A settings file without `permissions` is an empty rule set in Go and is skipped in TS; both give the same decisions. Case folding on darwin and the single global file (`<home>/.claude/settings.json`) are inherited from the core package.
- `CheckCommand` is untouched: it still reads only deny rules.

### internal/install (Tasks 10-14)

Ports the Claude Code settings.json hook registration of the TS standalone path (`src/adapters/claude-code/index.ts:200-215` read/write, `:465-619` `configureAllHooks`; the strip-only rule is `:513-557`, from #415) and the hook matchers of `hooks/hooks.json` (`src/adapters/claude-code/hooks.ts:57-114`), rewritten for spec §5 and the §3.5 matcher table. `Install` edits the settings file through an order-preserving JSON tree, removes cmgo's own inner commands from every event, appends one matcher entry per `HookEvents` row with the command `"<binary>" hook <arg>`, warns about (or disables) an enabled TS `context-mode@` plugin, writes atomically after a one-time `settings.json.cmgo-bak`, and registers the MCP server with `claude mcp add --scope user cmgo -- <binary> serve` (TS never ran this; r4 §C.3). `Uninstall` reverses the hook and MCP parts.

**Parity notes (decided, flag in review if wrong):**
- Strip, never replace: TS replaces a whole matcher entry that holds a context-mode command (`index.ts:566-614`), which drops co-located user commands. cmgo removes only its own inner commands and always adds a separate entry of its own.
- TS registers only PreToolUse and SessionStart in standalone mode (r4 finding C-1). cmgo registers all six `HookEvents` rows.
- Own-command detection is by shape, not by substring: the first token (double-quoted or bare) has basename `cmgo` and the rest is exactly `hook <arg>` for an arg in `HookEvents`. A cmgo command at an old absolute path is replaced; `cmgo-dev`, `cmgo serve` or an extra argument is left alone. Bare tokens are split at a space only, not at a tab.
- Pruning is limited to what this run emptied: a matcher entry, an event array or the `hooks` key is dropped only when cmgo's removal left it empty. TS prunes every empty entry (`index.ts:542-544`). Known limit of having no state: an original `"hooks": {}` or `"Stop": []` that `Install` appends to cannot come back after `Uninstall` (the key is removed). A user-made empty value that cmgo never touched survives.
- Idempotency is byte-exact for an immediate re-run. Strip-then-append moves an event key that held only cmgo's entry to the end of `hooks`, so if the user later adds an event key after cmgo's keys, the next `Install` rewrites the file with cmgo's keys behind it (same meaning, no new backup).
- JSON output is `JSON.stringify(x, null, 2) + "\n"` style (2-space indent, `{}`/`[]` for empty values, no HTML escaping), with two differences that need no golden: numbers keep their original text (`1.50` stays `1.50`), and strings go through Go `encoding/json`, which escapes U+2028/U+2029 and replaces invalid UTF-8. Duplicate keys keep the first position and the last value, like `JSON.parse`. Trailing data after the top-level value is a parse error. A UTF-8 BOM is a parse error too (unlike `hookio.ParseObject`); Claude Code does not write one.
- A missing settings file starts from `{}`; a blank (whitespace-only) file is also `{}`, since nothing can be lost. Anything else that does not parse, or a top level that is not an object, or `hooks` (or an event under it) of the wrong type when adding, is an error naming the file, and nothing is written or run.
- Writes resolve symlinks first and write through to the real file, because a rename over a dotfile manager's link would turn it into a plain file. The backup sits next to the real file. A new file gets mode 0644 and its parent dir is created (0755); an existing file keeps its mode, and so does the backup.
- A write happens only when the bytes change. `Uninstall` that removes nothing writes nothing, so a user's own formatting is never reformatted and a missing file is never created.
- `Binary` must be absolute and contain no `"`: the quoted command cannot represent a quote, and detection would never match it again.
- `~/.claude.json` is only read: missing or unparseable counts as not registered. A failed `claude` run is returned as an error after the settings file is written. The printed fallback is `run: claude ...`, POSIX single-quoted where needed.
- Messages go to Stdout (`cmgo: wrote ...`, `cmgo: saved the original as ...`, `cmgo: ... is up to date`, `cmgo: no cmgo hooks in ...`, `run: ...`); plugin warnings and the disable notice go to Stderr, so a dry run prints the would-be file first on Stdout, followed only by a `run:` line when one is due.
- Hook commands run the separate `cmgo-hook` binary (Task 14; reviewer decision after the hook latency finding): `"<HookBinary>" <arg>`, with `HookBinary` defaulting to `cmgo-hook` next to `Binary`. The MCP server stays `<Binary> serve`. Own-command detection matches both `cmgo-hook <arg>` and the earlier `cmgo hook <arg>`, so a reinstall replaces either form. `HookBinary` gets the same absolute/no-quote check as `Binary`; whether the file exists is checked by `cmd/cmgo`, not here, so these tests can use fake paths.

### internal/routing (Tasks 15-23)

Ports the Claude Code path of the TS PreToolUse hook: the decision tree `routePreToolUse` (`hooks/core/routing.mjs:670-916`: Bash `:704-841`, Read `:848-867`, Grep `:870-872`, WebFetch `:875-890`, Agent `:894-916`), its helpers (`stripHeredocs`/`stripQuotedContent` `:228-241`, `SAFE_COMMAND_PATTERNS` `:260-324`, `SHELL_CONTROL_OPERATORS` and `isStructurallyBounded` `:339-352`, `guidanceOnce` `:121-144`, `mcpRedirect` `:28-32`, `getBashNudgeMinCommandBytes` `:97-108`), `isMCPReady` (`hooks/core/mcp-ready.mjs:73-109`), the Claude Code formatter (`hooks/core/formatters.mjs:7-71`) and `createRoutingBlock` plus the guidance texts (`hooks/routing-block.mjs:16-92`), with cmgo tool names (`mcp__cmgo__`). It also writes the spec §2.4 events and the spec §2.5 sentinel. Spec: §2 and §4 of `docs/superpowers/specs/2026-10-21-cmgo-hooks-design.md`; TS behavior: `../research/r1-pretooluse.md`.

**Parity notes (decided, flag in review if wrong):**
- JS `\s` and `trim()` are ported with one helper (`jsSpaceClass`, `isJSSpace`, `jsTrim`, `jsRegexp`): every TS regex source is compiled after `\s`/`\S` become the JS classes (R1 P2). U+0085 is not JS whitespace; U+FEFF is.
- Hand-written matchers (R1 P1): `SHELL_CONTROL_OPERATORS` reduces to "contains one of ``|`\n\r>;<&`` or `$(`", because `<(?!<)` and `&(?!&)` always match the last `<` or `&` of a string. The mv/cp/rm/ln/ls lookaheads sit right after the command name at `^`, so each becomes a "must not match" prefix regex. `stripHeredocs` walks the regex's backtracking order by hand (word longest first, then the earliest newline).
- Deviation from spec §2.1.1: the verbose test `/\s(-v|--verbose|--trace|-D\s+-)\b/` is a plain RE2 regex. RE2 supports `\b` with the same ASCII definition as JS, so no hand matcher is needed; node-verified cases pin it.
- `(?i)` in RE2 folds U+017F to `s` and U+212A to `k`; JS `/i` without `u` does not. No TS example depends on it.
- Every hand-written or rewritten matcher is pinned by tables whose expected values come from node running the TS regex literals (and the exported `isStructurallyBounded`) on the same inputs.
- `MCPReady`: no 90 s grace for a dead pid (cmgo serve never refreshes the file), and a pid <= 0 counts as dead, because `kill(0, 0)` and `kill(-1, 0)` signal a process group and TS would report a server that does not exist. A file `parseInt` cannot read is skipped and kept, as in TS.
- Guidance markers live in `<RunDir>/guidance-<hookio.FileID(sid)>/<type>`; the TS in-memory throttle set is not ported (one process per hook).
- Agent: a non-string prompt counts as `""` (TS would stringify it; Claude Code always sends a string).
- Only the Claude Code input fields are read: `command`, `file_path`, `url` (R1 §9). Agent keeps the six prompt field names (spec §2.1.6).
- `Events`: the redirect event comes first, then `rejected`; TS wrote two separate marker files, so it had no order. `rejected` is written for every decision Claude Code sees as a deny (a deny, or a Bash redirect modify), with the reason Claude Code is shown.
- Golden replay: cases 19 and 36 of the seed are null only because the TS generator ran every case in one process and its in-memory throttle set is process-wide (R1 P18). cmgo shows the guidance there. The later `gen-hooks.mjs` task should reset that set per session (or run one process per case) so the final goldens agree.

### internal/capture (Tasks 24-31)

Ports the session-event extractors Claude Code hooks feed into `cmgo.db` (hooks spec §3.3-3.4 and the capture half of §4). PostToolUse runs the kept extractors of `src/session/extract.ts` (X:) in the `extractEvents` order (X:2710-2750): file and rule (X:176-306), cwd (X:312-327), git (X:373-440, with its tokenizer X:483-647), env (X:810-847), task (X:653-668), plan (X:685-803), skill (X:853-863), subagent (X:900-915), decision (X:1096-1152) and EnterWorktree (X:1243-1252). UserPromptSubmit ports `hooks/userpromptsubmit.mjs:36-71` plus `extractUserEvents` (X:2758-2774, X:2218-2535, X:2929-2960) without `extractData`. Stop ports `hooks/stop.mjs:32-43`. SessionStart ports the CLAUDE.md rule capture (`hooks/sessionstart.mjs:341-360`) and the `session_start` event (`hooks/sessionstart.mjs:61-73`). PostToolUseFailure is new (spec §3.3). The package only builds `store.Event` values; `cmd/cmgo` writes them.

**Parity notes (decided, flag in review if wrong):**
- **Response text.** TS extracted from `typeof r === "string" ? r : JSON.stringify(r ?? "")` (`hooks/posttooluse.mjs:51-58`). `responseText` gives the same text: a JSON string is decoded; a missing or `null` response is the two characters `""` (so `rule_content` is `""` and Agent is always `subagent_completed … → ""`, as in TS); anything else is the raw bytes compacted through `hookio.Object`, which keeps key order, adds no HTML escaping and writes U+2028/U+2029 raw. Escapes such as `\u0041` and number spellings stay as received where JS would normalize them; Claude Code writes `JSON.stringify` output, so they do not occur.
- **`String(x ?? def)`** is `inputString`/`jsString`: strings decoded, numbers and booleans as their JSON text, objects `[object Object]`, arrays joined with `,` (null items empty), `String(null)` is `"null"` (the `allowedPrompts` golden pins this).
- **Regexps.** Every TS `\s` / `\S` uses the JS whitespace class (`jsWS`, `jsNotWS`); `\b`, `\w`, `\d` are ASCII in JS and RE2 alike. Patterns with `i` and no `u` (rule files, the plan approval words) run on an ASCII-lowercased string, because Go's `(?i)` would also fold U+017F and U+212A. `/^\s*(?:fixed|resolved)\s*:/iu` folds Unicode in JS too, so it is `(?i)`. The goal pattern is `(?is)`. The git fallback takes the first table entry that matches (table order, not text position), as `GIT_PATTERNS.find` does.
- **Lengths.** `plan_bytes`, the FNV-1a plan hash, the `/plan` argument cut (120) and the Stop message cut (2000) count UTF-16 units (`hookio.JSLen`, `utf16.Encode`, `hookio.JSPrefix`); `JSPrefix` stops before a surrogate pair where `.slice` would split it. Decision, role and intent limits count code points (`hookio.RuneLen`). The tokenizer keeps an escaped multi-byte character whole, which gives the same token JS builds one unit at a time.
- **Fixes (spec §3.4).** Git looks the parsed subcommand up by subcommand, so `git -C /x checkout main` gives `cwd:/x` then `git:branch`; every other git behavior (regex fallback, commit-message parse, `-C ""` gives no cwd, `~`/`~/` expansion only) is kept. `rule_content` stores `tool_response.file.content` when the response is an object with that string; it is skipped when that text is empty (TS stored the JSON; an empty file has no rule text to keep).
- **Dropped extractors** are not ported: `error_tool` from PostToolUse, `constraint_discovered`, `bash_outcome`, `file_read_metadata`, `agent_finding`, `external_ref`, `mcp`, `mcp_tool_call`, `webfetch_metadata`, `worktree_exit`, Task `agent_usage`, `error_resolved`, `retry_detected`, `data`, and every `apply_patch` branch.
- **PostToolUseFailure** data is the payload `error` string; else the compact JSON of `tool_response` (a string response keeps its quotes, as spec §3.3 says "compact JSON"); else the compact JSON of the whole payload, so a row is never empty when the field name differs. `toolName` is not part of the data.
- **Not mirrored:** the outer `try/catch` of `extractEvents` that returned `[]` on any throw (for example `questions:[null]`); Go returns the other extractors' events and treats the question as `""`. `Object.values(answers)` lists integer-like keys first in JS; Go uses insertion order. `hasRoleCue` uses `strings.ToLower` (per rune) where JS `toLowerCase` is full mapping (U+0130 becomes two units); no listed prefix depends on the difference.
- **SessionStart.** `SessionStartRules` reads `<home>/.claude/CLAUDE.md`, `<project>/CLAUDE.md`, `<project>/.claude/CLAUDE.md` in that order, skipping unreadable files and files blank after JS `trim`. TS took the first path from `resolveConfigDir()`, which honors `CLAUDE_CONFIG_DIR`; the contract passes only `home`, so spec §3.3's `~/.claude` is used. An empty `home` skips that file instead of reading a cwd-relative path. File bytes are stored as read (Node would turn invalid UTF-8 into U+FFFD). `SessionStartEvent` data is `{"source":…,"project_dir":…,"started_at":<now.Unix()>}`.
- **Stop and UserPrompt** take typed values: `stop_hook_active` is a bool (TS echoed any JSON value) and the caller resolves `prompt ?? message`. The stored `user_prompt` is the raw prompt; the extractors see the JS-trimmed prompt.
- **Golden data.** `testdata/capture.json` is regenerated by `testdata/gen-capture.mjs` from the shipped TS bundle (pure functions, temp HOME); for the research seed's 27 tool cases and 11 prompts it equals `golden-seed/capture.json` (checked), except that the 1030-character prompt is stored in full instead of abbreviated. The test expects the TS events minus the dropped types, and lists the two fixed cases with their new events; it fails if a fix stops making a difference.

### internal/hookcmd, cmd/cmgo-hook, cmd/cmgo, bench (Tasks 32-39)

Wires the sub-project 2 packages into two binaries built from this module. `internal/hookcmd` runs one hook fire and replaces the TS hook scripts: `hooks/pretooluse.mjs:170-225` (route, format, one JSON line), `hooks/posttooluse.mjs:39-82`, `hooks/userpromptsubmit.mjs:36-71`, `hooks/stop.mjs:32-43,82` (`{}` on stdout) and `hooks/sessionstart.mjs:189,341-373,458-463` (source default, startup rules, the `additionalContext` line), with the stdin handling of `hooks/session-helpers.mjs:248-336`. `cmd/cmgo-hook` is the small binary Claude Code runs per hook event; `cmgo hook <event>` is the same code, kept for manual runs. `cmgo serve` takes over the MCP-ready sentinel that `src/server.ts:4860-4880` wrote to the temp dir (spec §2.5), and `cmgo install` / `uninstall` drive `internal/install` (spec §5; Task 14 must be done before Task 38). The fragment ends with the spec §6.3 end-to-end tests (in-process and built binaries, including `cmgo serve` and `cmgo-hook` as separate processes) and `bench/hook-latency.py` (spec §6.4).

**Parity notes (decided, flag in review if wrong):**
- Two binaries, not one (reviewer decision; reverses spec §7's single-binary choice). With the MCP SDK, executor and fetcher linked in, `cmgo version` alone took about 6.7 ms against 1.8 ms for `/usr/bin/true`, and the passthrough hook missed the 10 ms p95. The same hook code in a binary without them took about 4.5 ms. `cmd/cmgo-hook` imports only `internal/hookcmd`, and a test fails if `go list -deps` shows the MCP SDK, `net/http`, html-to-markdown, `internal/executor`, `internal/fetch` or `internal/mcpserver`.
- Failure policy follows spec §1.2, not TS: malformed stdin gives no stdout on every event. TS still printed the routing block from SessionStart and `{}` from Stop on a parse error (`stop.mjs:82` writes it unconditionally). cmgo's Stop prints `{}` only after the `turn_end` row is written.
- Empty (whitespace-only) stdin is spec §1.2 "no output" and is not logged. TS mapped it to `{}`, so its SessionStart still printed and its Stop still recorded a row.
- The stdin timeout is a 2 s total deadline on the read (a goroutine plus `time.After`), not the TS 1500 ms idle timer. Claude Code closes stdin, so it only bounds a stuck caller. The blocked reader goroutine is abandoned; the process exits right after.
- An unknown event exits 0 without reading stdin. `cmgo-hook` or `cmgo hook` with no event is a usage error (exit 2), since it is not a hook fire.
- The error log is `$CMGO_DIR/hook-errors.log`, resolved by `config.Load` with a dummy cwd, because the project dir never affects `DataDir` and a parse failure must still log. Panics are logged as `panic: <value>`.
- The project dir is `config.Load` with `getwd` replaced by `input.cwd` when that is a non-empty string (spec §1.4). `routing.Deps.Getenv` gets the same injected getenv as `config.Load` (`os.Getenv` in production).
- The store is opened only when there is an event to write; Stop always has one. PreToolUse and SessionStart write stdout before they open the store, so the DB never delays Claude Code's decision.
- SessionStart: a missing or `null` `source` is startup (`input.source ?? "startup"`). Any other value, including a non-string, prints the block and does nothing else.
- UserPromptSubmit: `prompt`, else `message`, as `input.prompt ?? input.message ?? ""`. A non-string `prompt` falls through to `message` (TS would throw on `.trim()` and log).
- Stop: a non-boolean `stop_hook_active` is recorded as `false` (TS stringified the raw value).
- `run` gains an `stdin io.Reader` parameter for `cmgo hook`; `serve` keeps `os.Stdin` because its transport needs a `ReadCloser`.
- `serve`: a sentinel that cannot be written is a warning on stderr; the server still runs, and hooks then pass the redirects through.
- `install`: `--binary` is made absolute with `filepath.Abs`; the default is `os.Executable()` with symlinks resolved. The hook binary is `cmgo-hook` in the same dir; when it does not exist, `install` exits 1 with `cmgo: hook binary missing: ...` before touching any file, unless `--dry-run`. HOME is `$HOME`, else `os.UserHomeDir()`, like `config.Load`. A usage error (unknown flag, extra argument, `uninstall --binary`) exits 2; an install error prints `cmgo: <error>` and exits 1. `claude` runs with stdout and stderr passed through.
- The latency bench measures `cmgo-hook` and times `subprocess.run` wall clock, so Python's fork/exec cost is inside every number. It prints `/usr/bin/true`, `cmgo-hook` with no arguments and the 1-minute load average next to the cases. It fakes a live server by writing `mcp-ready-<python pid>`, so the curl case measures the redirect path without starting `serve`.
## Appendix B: Verification (2026-10-21, macOS arm64)

**Replay of this plan** (`replay_plan2.py`): each task's files were written into a fresh worktree of `sp2-hooks` (sub-project 1 code present), then `go vet ./...` and that task's package tests were run. All 39 tasks were `ok`. Final step: `go test ./... && go test -race ./...`.

```
FINAL ok
ok  	github.com/lang315/context-mode-go/cmd/cmgo	4.215s
ok  	github.com/lang315/context-mode-go/cmd/cmgo-hook	0.711s
ok  	github.com/lang315/context-mode-go/internal/capture	1.356s
ok  	github.com/lang315/context-mode-go/internal/config	1.207s
ok  	github.com/lang315/context-mode-go/internal/executor	15.411s
ok  	github.com/lang315/context-mode-go/internal/fetch	1.977s
ok  	github.com/lang315/context-mode-go/internal/hookcmd	1.065s
ok  	github.com/lang315/context-mode-go/internal/hookio	0.763s
ok  	github.com/lang315/context-mode-go/internal/install	0.349s
ok  	github.com/lang315/context-mode-go/internal/mcpserver	5.670s
ok  	github.com/lang315/context-mode-go/internal/routing	1.990s
ok  	github.com/lang315/context-mode-go/internal/security	1.758s
ok  	github.com/lang315/context-mode-go/internal/store	3.600s
ok  	github.com/lang315/context-mode-go/cmd/cmgo	10.344s
ok  	github.com/lang315/context-mode-go/cmd/cmgo-hook	2.196s
ok  	github.com/lang315/context-mode-go/internal/capture	3.187s
ok  	github.com/lang315/context-mode-go/internal/config	2.872s
ok  	github.com/lang315/context-mode-go/internal/executor	14.949s
ok  	github.com/lang315/context-mode-go/internal/fetch	2.776s
ok  	github.com/lang315/context-mode-go/internal/hookcmd	3.965s
ok  	github.com/lang315/context-mode-go/internal/hookio	2.503s
ok  	github.com/lang315/context-mode-go/internal/install	2.092s
ok  	github.com/lang315/context-mode-go/internal/mcpserver	15.097s
ok  	github.com/lang315/context-mode-go/internal/routing	2.224s
ok  	github.com/lang315/context-mode-go/internal/security	2.299s
ok  	github.com/lang315/context-mode-go/internal/store	11.951s
```

`go test ./... -count=1 -v`: 327 `--- PASS`, 0 `--- FAIL`. The php, r, elixir and csharp subtests of `TestRunHelloEachLanguage` were skipped because those runtimes are not installed. After the replay, `diff -rq` of the replayed tree against the draft module showed no differences in code, tests, golden files or bench scripts.

**Acceptance scripts, run by hand on the draft binaries:**

```
$ python3 -I bench/hook-latency.py bin/cmgo-hook
load average (1 min) at start: 5.74
context /usr/bin/true: p50 2.02 ms p95 2.87 ms
context cmgo-hook, no args: p50 5.21 ms p95 6.02 ms
pretooluse passthrough: p50 5.31 ms p95 6.50 ms PASS
pretooluse curl redirect: p50 5.95 ms p95 7.65 ms PASS
posttooluse Read capture: p50 6.25 ms p95 7.27 ms PASS
userpromptsubmit: p50 6.37 ms p95 7.49 ms PASS
sessionstart startup: p50 6.26 ms p95 7.35 ms PASS
posttooluse x4 concurrent: p50 7.69 ms p95 9.79 ms PASS
load average (1 min) at end: 5.88
PASS
$ python3 -I bench/idle-writes.py bin/cmgo 60
8 idle servers over 60s: 0 bytes written = 0.000 MB/min
PASS
```

The machine was busy with other sessions during the latency run (load average about 5.8). A TS hook starts `node`, and `node -e ''` alone took about 20 ms on this machine.

**What the replay did not exercise:**
- **The fail-first steps:** no task's Step 2 was run during the replay. The draft authors recorded each "Expected: FAIL" line from a real run, by replaying their own tasks on a copy of the module.
- **The Node generator:** Task 1 was not re-run; the replay copied its three JSON files from the draft. It had been run twice, with identical md5s both times.
- **The latency bench:** the concurrent case passed with 0.2 ms to spare. Package-level regexes in `routing`, `capture`, `hookio`, `security` and `store` cost about 0.9 ms of the hook's start-up (`GODEBUG=inittrace=1`). Compiling them lazily is a possible follow-up.
- **The installer:** it was never run against the real `~/.claude`, by design.
- **Live Claude Code:** the PostToolUseFailure `error` field, and whether Claude Code applies `updatedInput` without a `permissionDecision` for `Agent`, were not checked against Claude Code itself. Both come from the TS behavior and the Claude Code docs.
