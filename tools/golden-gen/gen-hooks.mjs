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
