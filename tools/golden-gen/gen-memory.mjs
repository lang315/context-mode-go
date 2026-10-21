#!/usr/bin/env node
// Session-memory golden generator for cmgo (spec §5). Run by hand, never by go test:
//
//   TS_REPO=/path/to/context-mode node tools/golden-gen/gen-memory.mjs
//
// Writes testdata/golden/memory-{directive,autoinjection}.json from the TS
// fork at $TS_REPO (commit a5e6dd31). It imports hooks/session-directive.mjs
// (groupEvents, writeSessionEventsFile, buildSessionDirective) and
// hooks/auto-injection.mjs (buildAutoInjection) directly; it never runs a
// hook script, start.mjs or postinstall. HOME, TMPDIR and CONTEXT_MODE_DIR
// point into a fresh temp dir before the imports; writeSessionEventsFile
// writes its events.md into that dir, which is removed at the end. cmgo does
// not port events.md (spec §2.3); the file is only a side effect of the TS
// production path below.
//
// Each directive case holds two texts:
//   ts       — what TS ships: buildSessionDirective gets writeSessionEventsFile's
//              return value (sessionstart.mjs:205-206), events in fixture order.
//   expected — what cmgo must print (spec §2.4), computed by the same TS code
//              on fixed() input: user-prompt rows become category "prompt" (so
//              Last Request and <continue_from> read them), mcp and data rows
//              move to categories no section reads (no MCP or Data sections),
//              created_at becomes the zero-padded id, and the directive gets
//              groupEvents() output (leg boundary by id, not dropped).
// MEMORY-KNOWN-DIFFS.md lists every case where the two differ.
// Auto-injection has no §2.4 fix, so its cases hold one expected text.
// The search tool name is mcp__cmgo__ctx_search (namer t => "mcp__cmgo__" + t).
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const OUT = join(ROOT, "testdata/golden");
const TS_REPO = resolve(process.env.TS_REPO || "/Users/lang/GolandProjects/github.com/lang315/context-mode");
const TS_COMMIT = "a5e6dd31";
const namer = (t) => "mcp__cmgo__" + t;

// ─────────────────────────────────────────────────────────
// Fixtures: [category, type, data, second]. Ids follow the array order
// (cmgo orders by id); created_at is 2026-01-01 00:00:00 UTC + second.
// ─────────────────────────────────────────────────────────

const E = "\u{1F600}"; // one astral character: 2 UTF-16 units, 4 UTF-8 bytes
const big = (c, n) => c.repeat(n);

const FIXTURES = {
  // The 27-event research fixture: every section.
  full: [
    ["session_start", "session_start", JSON.stringify({ source: "startup" }), 0],
    ["rule", "rule", "/home/u/proj/CLAUDE.md", 1],
    ["rule", "rule_content", "# Rules\n## Sub\nUse tabs & <b>.", 1],
    ["user-prompt", "user_prompt", "Fix the login bug in auth.ts", 2],
    ["file", "file_read", "/home/u/proj/src/auth.ts", 3],
    ["file", "file_edit", "/home/u/proj/src/auth.ts", 4],
    ["file", "file_glob", "**/*.ts in /home/u/proj/src", 4],
    ["task", "task_create", JSON.stringify({ subject: "Write tests" }), 5],
    ["task", "task_create", JSON.stringify({ subject: "Ship fix" }), 5],
    ["task", "task_update", JSON.stringify({ taskId: "1", status: "completed" }), 6],
    ["decision", "decision", "use bcrypt not md5", 7],
    ["error", "error_tool", "TypeError: x is undefined", 8],
    ["git", "git", "commit", 9],
    ["git", "git", "commit", 9],
    ["cwd", "cwd", "/home/u/proj", 10],
    ["env", "env", "nvm use 20", 10],
    ["mcp", "mcp", "ctx_search: auth", 11],
    ["mcp", "mcp", "ctx_search: login", 11],
    ["subagent", "subagent_completed", "[completed] research → done", 12],
    ["skill", "skill", "tdd", 13],
    ["skill", "skill", "tdd", 13],
    ["role", "role", "act as senior Go reviewer", 14],
    ["intent", "intent", "implement", 15],
    ["goal", "goal", "login works with SSO", 15],
    ["data", "data", "short capture", 16],
    ["plan", "plan_enter", "entered plan mode", 17],
    ["plan", "plan_approved", "plan approved", 18],
  ],
  "only-session-start": [
    ["session_start", "session_start", JSON.stringify({ source: "startup" }), 0],
  ],
  // #780: a subagent before and after the last session_start.
  leg: [
    ["subagent", "subagent_completed", "[completed] build widget → done", 1],
    ["session_start", "session_start", "{}", 5],
    ["subagent", "subagent_launched", "[launched] analyze logs", 8],
  ],
  // Two legs; the second subagent shares the last session_start's second but
  // has a lower id, so only the id boundary calls it prior-leg.
  "leg-tie": [
    ["session_start", "session_start", "{}", 1],
    ["subagent", "subagent_completed", "[completed] first leg", 2],
    ["subagent", "subagent_completed", "[completed] same second", 6],
    ["session_start", "session_start", "{}", 6],
    ["subagent", "subagent_launched", "[launched] current leg", 7],
  ],
  // Every truncation suffix. Data rows only reach the TS text.
  trunc: [
    ["user-prompt", "user_prompt", big("P", 310), 1],
    ["decision", "decision", big("D", 160), 2],
    ["error", "error_tool", big("E", 151), 3],
    ["subagent", "subagent_launched", big("S", 121), 4],
    ...Array.from({ length: 10 }, (_, i) => ["data", "data", i === 9 ? big("X", 200) : `d${i}`, 5 + i]),
  ],
  // Exactly at each limit: nothing is cut.
  "trunc-exact": [
    ["user-prompt", "user_prompt", big("p", 300), 1],
    ["decision", "decision", big("d", 150), 2],
    ["error", "error_tool", big("e", 150), 3],
    ["subagent", "subagent_launched", big("s", 120), 4],
  ],
  // Limits count UTF-16 units: each astral character is 2. The ASCII prefix
  // puts every cut between two characters.
  "trunc-astral": [
    ["user-prompt", "user_prompt", "y" + big(E, 150), 1],
    ["decision", "decision", "a" + big(E, 80), 2],
    ["error", "error_tool", "b" + big(E, 76), 3],
    ["subagent", "subagent_launched", "x" + big(E, 60), 4],
  ],
  "plan-rejected": [["plan", "plan_enter", "p:plan_enter", 0], ["plan", "plan_rejected", "p:plan_rejected", 1]],
  "plan-active-filewrite": [["plan", "plan_enter", "p:plan_enter", 0], ["plan", "plan_file_write", "p:plan_file_write", 1]],
  "plan-active": [["plan", "plan_enter", "p:plan_enter", 0]],
  "plan-completed": [["plan", "plan_enter", "p:plan_enter", 0], ["plan", "plan_exit", "p:plan_exit", 1]],
  "plan-approved-after-rejected": [
    ["plan", "plan_enter", "p:plan_enter", 0], ["plan", "plan_rejected", "p:plan_rejected", 1],
    ["plan", "plan_enter", "p:plan_enter", 2], ["plan", "plan_approved", "p:plan_approved", 3],
  ],
  // Task matching (SD:300-334): creates match updates by ascending numeric taskId.
  tasks: [
    ["task", "task_create", JSON.stringify({ subject: "A" }), 1],
    ["task", "task_create", JSON.stringify({ subject: "B" }), 1],
    ["task", "task_create", JSON.stringify({ subject: "C" }), 1],
    ["task", "task_create", JSON.stringify({ subject: "D" }), 1],
    ["task", "task_create", JSON.stringify({ subject: 42 }), 1],
    ["task", "task_update", JSON.stringify({ taskId: "3", status: "completed" }), 2],
    ["task", "task_update", JSON.stringify({ taskId: "1", status: "in_progress" }), 2],
    ["task", "task_update", JSON.stringify({ taskId: "10", status: "deleted" }), 2],
    ["task", "task_update", JSON.stringify({ taskId: "2" }), 2],
    ["task", "task", JSON.stringify({ todos: [{ content: "a", status: "pending" }] }), 3],
    ["task", "task_create", "fix flaky test", 3],
    ["task", "task_create", "null", 3],
    ["task", "task_create", JSON.stringify({ subject: "" }), 3],
    ["cwd", "cwd", "/a", 4],
    ["cwd", "cwd", "/b", 5],
  ],
  "tasks-all-done": [
    ["task", "task_create", JSON.stringify({ subject: "Only task" }), 1],
    ["task", "task_update", JSON.stringify({ taskId: "1", status: "completed" }), 2],
  ],
  // File basenames, rule paths, env without cwd, last intent and role win.
  "files-rules-env": [
    ["file", "file_search", "TODO in /home/u/proj/src", 1],
    ["file", "file_write", "C:\\win\\path\\f.txt", 1],
    ["file", "file_edit", "/a/b/ ", 1],
    ["file", "file_search", "x in y in /z/w.go", 1],
    ["file", "file_glob", "**/*.ts", 1],
    ["file", "file_edit", "foo/", 1],
    ["file", "file_read", "/z/w.go", 1],
    ["file", "file_read", "/k/\u00a0spaced.md\u00a0", 1],
    ["rule", "rule", "/home/u/proj/CLAUDE.md", 2],
    ["rule", "rule", "C:\\Users\\u\\.claude\\CLAUDE.md", 2],
    ["rule", "rule", "/other/proj/CLAUDE.md", 2],
    ["rule", "rule", "CLAUDE.md", 2],
    ["git", "git", "branch", 3],
    ["git", "git", "commit", 3],
    ["git", "git", "branch", 3],
    ["env", "env", "export NODE_ENV=test", 4],
    ["env", "env", "venv: .venv", 4],
    ["skill", "skill", "b", 5],
    ["skill", "skill", "a", 5],
    ["skill", "skill", "b", 5],
    ["intent", "intent", "investigate", 6],
    ["intent", "intent", "review", 7],
    ["role", "role", "first role", 6],
    ["role", "role", "second role", 7],
  ],
  // The last user-prompt is the Last Request.
  prompts: [
    ["user-prompt", "user_prompt", "first prompt", 1],
    ["decision", "decision", "keep the API", 2],
    ["user-prompt", "user_prompt", "second prompt", 3],
    ["goal", "goal", "ship it", 4],
  ],
  // Only the categories cmgo never captures.
  "mcp-data-only": [
    ["mcp", "mcp", "ctx_search: a", 1],
    ["mcp", "mcp", "ctx_execute: b", 1],
    ["mcp", "mcp", "ctx_search: c", 1],
    ["data", "data", "captured output", 2],
  ],
  // Rule paths next to rule_content rows, which the directive does not list.
  "rules-headings": [
    ["rule", "rule", "/p/CLAUDE.md", 1],
    ["rule", "rule_content", "# H1\n## H2\n### H3\n#### H4\n#nospace\n  # indented\r\n## crlf\r# cr\u2028## ls", 1],
    ["rule", "rule_content", "plain body", 1],
    ["rule", "rule", "/p/.claude/rules/x.md", 2],
  ],
};

const DIRECTIVE_CASES = [
  ["full", "compact"], ["full", "resume"], ["only-session-start", "compact"],
  ["leg", "resume"], ["leg-tie", "compact"], ["trunc", "compact"], ["trunc-exact", "resume"],
  ["trunc-astral", "compact"], ["plan-rejected", "resume"], ["plan-active-filewrite", "resume"],
  ["plan-active", "resume"], ["plan-completed", "resume"], ["plan-approved-after-rejected", "resume"],
  ["tasks", "compact"], ["tasks-all-done", "resume"], ["files-rules-env", "resume"],
  ["prompts", "compact"], ["prompts", "resume"], ["mcp-data-only", "resume"], ["rules-headings", "resume"],
];

// Auto-injection fixtures (AI:30-102), [category, data] rows.
const AUTO = {
  full: FIXTURES.full.map(([c, , d]) => [c, d]),
  empty: [],
  "no-parts": [["file", "/a.ts"], ["goal", "g"]],
  "role-only": [["role", "reviewer"]],
  "decisions-7": Array.from({ length: 7 }, (_, i) => ["decision", `decision ${i}`]),
  // integration.test.ts:1145-1215: 20 long decisions, a 400-char role, 50 skills.
  heavy: [
    ["role", big("R", 400)],
    ...Array.from({ length: 20 }, (_, i) => ["decision", `${i}:` + big("d", 198)]),
    ...Array.from({ length: 50 }, (_, i) => ["skill", `skill-${i}`]),
    ["intent", "implement"],
  ],
  // Long skill names spend the budget, so the intent is dropped.
  "intent-dropped": [
    ["role", big("r", 450)],
    ...Array.from({ length: 5 }, (_, i) => ["decision", `${i}` + big("x", 120)]),
    ...Array.from({ length: 10 }, (_, i) => ["skill", `${i}` + big("s", 200)]),
    ["intent", "review"],
  ],
  // Slices count UTF-16 units; the prefix keeps every cut between characters.
  astral: [
    ["role", "rr" + big(E, 250)],
    ["decision", "dd" + big(E, 60)],
    ["skill", E],
    ["skill", E],
    ["intent", "mode " + E],
  ],
  "skills-dup": [..."abcdefghijkl", ..."abc"].map((s) => ["skill", s]),
  "last-role-intent": [["role", "one"], ["intent", "i1"], ["role", "two"], ["intent", "i2"]],
};

// ─────────────────────────────────────────────────────────

const sec = (s) => new Date(Date.UTC(2026, 0, 1, 0, 0, s)).toISOString().replace("T", " ").slice(0, 19);
const events = (rows) => rows.map(([category, type, data, s], i) =>
  ({ id: i + 1, type, category, data, created_at: sec(s) }));

// fixed applies the spec §2.4 fixes to TS input (see the header).
const RENAME = { "user-prompt": "prompt", mcp: "uncaptured-mcp", data: "uncaptured-data" };
function fixed(evs) {
  if (evs.some((e) => e.category === "prompt")) throw new Error("fixtures must not use category prompt");
  return evs.map((e) => ({ ...e,
    category: RENAME[e.category] ?? e.category,
    created_at: String(e.id).padStart(10, "0") }));
}

async function main() {
  const head = execFileSync("git", ["-C", TS_REPO, "rev-parse", "HEAD"], { encoding: "utf8" }).trim();
  if (!head.startsWith(TS_COMMIT)) throw new Error(`TS_REPO is at ${head}, want ${TS_COMMIT}`);
  mkdirSync(OUT, { recursive: true });
  const tmp = realpathSync(mkdtempSync(join(tmpdir(), "cmgo-memory-golden-")));
  try {
    Object.assign(process.env, { HOME: tmp, TMPDIR: tmp, CONTEXT_MODE_DIR: join(tmp, "context-mode") });
    const tsModule = (path) => import(pathToFileURL(join(TS_REPO, path)).href);
    const sd = await tsModule("hooks/session-directive.mjs");
    const { buildAutoInjection } = await tsModule("hooks/auto-injection.mjs");

    // writeSessionEventsFile writes events.md into tmp and returns the grouped meta.
    const mdPath = join(tmp, "events.md");

    const directive = DIRECTIVE_CASES.map(([fixture, source]) => {
      const evs = events(FIXTURES[fixture]);
      return {
        name: `${fixture}/${source}`, source, events: evs,
        ts: sd.buildSessionDirective(source, sd.writeSessionEventsFile(evs, mdPath), namer),
        expected: sd.buildSessionDirective(source, sd.groupEvents(fixed(evs)), namer),
      };
    });
    const autoinjection = Object.entries(AUTO).map(([name, rows]) => {
      const evs = rows.map(([category, data], i) => ({ id: i + 1, type: category, category, data, created_at: sec(0) }));
      return { name, events: evs, expected: buildAutoInjection(evs) };
    });

    const write = (name, obj) => writeFileSync(join(OUT, name), JSON.stringify({ tsCommit: TS_COMMIT, ...obj }, null, 2) + "\n");
    write("memory-directive.json", { cases: directive });
    write("memory-autoinjection.json", { cases: autoinjection });
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
  console.log(`memory golden files written to ${OUT}`);
}

main().catch((e) => { console.error(e); process.exit(1); });
