# cmgo session memory Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** After a compaction or a resume, give the model back what the session was doing:
- the session directive and auto-injection, from the events captured in sub-project 2;
- `ctx_search` access to session events (timeline and `source: "session-events"`), CLAUDE.md files and Claude Code's auto-memory;
- the `ctx_purge` tool.

**Architecture:**
- **`internal/memory` (new):** pure builders over `[]store.StoredEvent`, ported from TS `hooks/session-directive.mjs` and `hooks/auto-injection.mjs`, plus auto-memory file search.
- **`internal/store`:** event search, compaction and purge.
- **`internal/hookcmd`:** gains `precompact`, and SessionStart `compact`/`resume` context.
- **`internal/mcpserver`:** gains the timeline merge, the `session-events` source and `ctx_purge`.
- **Parity:** pinned by golden files generated from TS `a5e6dd31`, with the fixes listed in the spec.

**Tech Stack:**
- Go 1.27.2, existing dependencies only.
- Node, only to regenerate golden files.
- Python 3 for the bench.

**Spec:** `docs/superpowers/specs/2026-10-21-cmgo-session-memory-design.md` (sub-project 3 of 5). It builds on the core and hooks specs. Read it alongside this plan; §2.4 lists the fixes against TS.

**How the code in this plan was produced:**
- Every code block was built and tested in a draft module on top of the finished sub-project 2 branch, then copied in here verbatim.
- The assembled plan was replayed task by task, Task 1 through 18, into a fresh worktree of `sp2-hooks`, with `go vet` and the task's package tests run after each task. Appendix B has the result.
- Each code block is the complete file named on the line above it. When a later task shows a file again, replace the whole file.

## Global Constraints

- **Repo and branch:** repo `~/GolandProjects/github.com/lang315/context-mode-go`; work on branch `sp3-session-memory`, which is stacked on `sp2-hooks`. `go.mod` is unchanged.
- **Platforms:** macOS and Linux only. Verified on macOS arm64.
- **No schema migration** in this sub-project.
- **Tests:** they use `t.TempDir()`. Never write to the real `~/.claude`, `~/.claude.json` or the TS repo. Never run `cmgo install` against the real home.
- **Parity:** directive and auto-injection texts match TS `a5e6dd31` after `mcp__plugin_context-mode_context-mode__` → `mcp__cmgo__`, except the spec §2.4 fixes.
  - JS lengths are UTF-16 units (`hookio.JSLen`, `hookio.JSPrefix`).
  - JSON output keeps key order and does not escape HTML.
- **Hooks fail open:**
  - SessionStart always prints at least the routing block.
  - PreCompact and Stop always print `{}`.
  - Hook DB access uses the 2 s busy timeout (`store.OpenWith`).
- **Hook latency:** p95 ≤ 10 ms per case in `bench/hook-latency.py`, including `precompact` and `sessionstart compact` on a 1000-event session.
- **Process markers:** never put a literal process marker in a shell command that also runs `pkill` or `pgrep`.
- **Commits:** Conventional Commits, in English, one per task.

## Review Focus

These are the five conditions most likely to hurt someone using session memory that the spec implies but does not list. Each has a test in the task that owns the code.

1. **`ctx_purge` with project scope is called while other projects share `cmgo.db`.** Only the current project's sources, chunks and sessions may go. The test is `TestPurgeProjectLeavesOtherProjects` (Task 7).
2. **The DB is locked or broken when a compacted session restarts.** SessionStart must still print the routing block and exit 0. The test is `TestHookSessionStartCompactFailOpen` (Task 15).
3. **A session that is already at the 1000-event cap is compacted.** The cap must hold and the compaction event must be recorded. The test is `TestCompactSessionKeepsEventCap` (Task 6).
4. **A long session with several compactions and resumes.** "Last Request" must be the latest prompt, and only subagents launched before the last `session_start` count as the earlier leg. The tests are `TestGroupLastPromptIsLastUserPrompt` and `TestGroupLegBoundaryByID` (Task 2).
5. **A timeline search over a project with many old events, sources and memory files.** It must surface the newest matches from every source, oldest first in the output. The test is `TestSearchTimelineMergesSources` (Task 9).

## File Structure

| Path | Responsibility |
|---|---|
| `internal/memory` | Event grouping, the session directive, auto-injection, auto-memory search |
| `internal/store/memory.go`, `purge.go` (+ `events.go` refactor) | `SessionEventsForSearch`, `CompactSession`, `PurgeSession`, `PurgeProject` |
| `internal/mcpserver/search.go`, `purge.go` | Timeline merge, `session-events` source, restored description, `ctx_purge` |
| `internal/hookcmd` | `precompact`, SessionStart compact/resume context |
| `internal/routing/text.go` | `<ctx_commands>` with the purge entry |
| `internal/install/hooks.go` | PreCompact registration |
| `tools/golden-gen/gen-memory.mjs`, `testdata/golden/memory-*` | TS goldens and known differences |
| `bench/hook-latency.py` | precompact and compact cases |

## Task order

| Task | Content |
|---|---|
| 1 | Session memory golden generator and golden files |
| 2 | Group session events |
| 3 | Session directive |
| 4 | Auto-injection |
| 5 | Session events matching a timeline query |
| 6 | CompactSession records a compaction |
| 7 | PurgeSession and PurgeProject |
| 8 | Auto-memory search |
| 9 | ctx_search timeline merge and session-memory description |
| 10 | ctx_purge tool |
| 11 | Auto-memory reads the Claude config dir |
| 12 | Routing block commands section lists only ctx purge |
| 13 | Install registers PreCompact |
| 14 | cmgo-hook precompact |
| 15 | SessionStart compact and resume inject session memory |
| 16 | Bench precompact and sessionstart compact at 1000 events |
| 17 | Built-binary end-to-end for precompact and SessionStart compact |
| 18 | ctx_search source session-events reads session events |

Suggested review checkpoints, after:
- Task 1 (golden generator);
- Task 4 (`memory`);
- Task 10 (store, search, purge);
- Task 18 (hooks, bench and the `session-events` source).

---

### Task 1: Session memory golden generator and golden files

**Files:**
- Create: `tools/golden-gen/gen-memory.mjs`
- Create: `testdata/golden/MEMORY-KNOWN-DIFFS.md`
- Generate: `testdata/golden/memory-directive.json`, `testdata/golden/memory-autoinjection.json`

**Interfaces:**
- Consumes: TS fork at `$TS_REPO` (default `/Users/lang/GolandProjects/github.com/lang315/context-mode`) at commit `a5e6dd31`. It uses these exports: `groupEvents(events)`, `writeSessionEventsFile(events, eventsPath)` and `buildSessionDirective(source, eventMeta, toolNamer)` from `hooks/session-directive.mjs`; `buildAutoInjection(events)` from `hooks/auto-injection.mjs`.
- Produces:
  - `memory-directive.json` = `{ tsCommit, cases: [{ name ("<fixture>/<source>"), source ("compact" | "resume"), events: [{ id, type, category, data, created_at }], ts, expected }] }`;
  - `memory-autoinjection.json` = `{ tsCommit, cases: [{ name, events, expected }] }`.

  Tasks Task 3 and Task 4 read these files.

- [ ] **Step 1: Write the failing test**

The test runs the generator twice, each time with a fresh temp dir as `TMPDIR`. It passes only if both runs exit 0, both leave their temp dir empty (`rmdir` fails otherwise), and both write the same bytes:

```bash
gen() {
  T=$(mktemp -d) &&
  TMPDIR=$T TS_REPO=/Users/lang/GolandProjects/github.com/lang315/context-mode node tools/golden-gen/gen-memory.mjs >/dev/null &&
  rmdir "$T" && md5sum testdata/golden/memory-*.json
}
a=$(gen) && b=$(gen) && [ "$a" = "$b" ] && echo "$a" && echo "deterministic: OK"
```

- [ ] **Step 2: Run test to verify it fails**

Run: the Step 1 block from the repo root.
Expected: FAIL with `Error: Cannot find module '<repo>/tools/golden-gen/gen-memory.mjs'`, and no `deterministic: OK` line.

- [ ] **Step 3: Write minimal implementation**

`tools/golden-gen/gen-memory.mjs`:
```js
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
```

The known-differences list. Task 3 holds the same rows in `knownDiffs` (`internal/memory/golden_test.go`):

`testdata/golden/MEMORY-KNOWN-DIFFS.md`:
```markdown
# Known differences: cmgo session memory vs TS golden output

The `memory-*.json` files in this directory come from
`tools/golden-gen/gen-memory.mjs` (TS commit `a5e6dd31`). The search tool
name is `mcp__cmgo__ctx_search` (spec §2.4). cmgo does not build the TS
events markdown (spec §2.3), so there are no golden files for it.

Each directive case holds two texts:
- `ts` is what TS ships: `buildSessionDirective` gets the return value of
  `writeSessionEventsFile` (`hooks/sessionstart.mjs:205-206`).
- `expected` is what cmgo must print. The generator computes it with the same
  TS functions on fixed input: `user-prompt` rows become category `prompt`,
  `mcp` and `data` rows move to categories that no section reads,
  `created_at` becomes the zero-padded id, and the directive gets
  `groupEvents()` output.

The Go tests compare cmgo against `expected`, byte for byte, for every case.
They also check that `ts` differs from `expected` in exactly the cases listed
below (`knownDiffs` in `internal/memory/golden_test.go`): a listed difference
that goes away fails, and an unlisted one fails. `memory-autoinjection.json`
has no fix to apply, so it holds `expected` only and has no rows here.

## Fixes (spec §2.4)

| Fix | TS | cmgo |
|---|---|---|
| `user-prompt` | "Last Request" and `<continue_from>` read category `prompt`, which capture never writes (R3 headline 4) | they read the last `user-prompt` event |
| `leg-boundary` | the directive gets no leg boundary, so prior-leg subagents read as current (R3 headline 5) | an event is prior-leg when its id is lower than the id of the last `session_start` event |
| `no-mcp-data` | "MCP Tools Used" and "Data References" sections | left out: capture writes no `mcp` or `data` events (hooks spec §3.4) |
| order by id | `getSessionEvents` orders by `created_at` | rows are used in id order. Not exercised here: the generator feeds every fixture in id order, as cmgo reads them |

## Cases

Case IDs are `directive:<name>`, where `<name>` is the `name` field of a
case in `memory-directive.json` (`<fixture>/<source>`).

| Case ID | Fixes |
|---|---|
| `directive:full/compact` | `user-prompt` (Last Request, `<continue_from>`), `no-mcp-data` |
| `directive:full/resume` | `user-prompt` (Last Request), `no-mcp-data` |
| `directive:leg/resume` | `leg-boundary` |
| `directive:leg-tie/compact` | `leg-boundary` |
| `directive:trunc/compact` | `user-prompt` (Last Request cut at 297 + `...`, `<continue_from>`), `no-mcp-data` |
| `directive:trunc-exact/resume` | `user-prompt` (Last Request) |
| `directive:trunc-astral/compact` | `user-prompt` (Last Request, `<continue_from>`) |
| `directive:prompts/compact` | `user-prompt` (Last Request, `<continue_from>`) |
| `directive:prompts/resume` | `user-prompt` (Last Request) |
| `directive:mcp-data-only/resume` | `no-mcp-data` |
```

- [ ] **Step 4: Run test to verify it passes**

Run: the Step 1 block from the repo root.
Expected: PASS. The generator exits 0 both times, and the two runs give the same md5s. The gate is that equality, not the values. The author's run printed:
```
41796614a4b90e665e8eefe1ee1192ab  testdata/golden/memory-autoinjection.json
dc4783e4343795acab8e8414d207d17c  testdata/golden/memory-directive.json
deterministic: OK
```
The files have 1114 and 1236 lines. `git -C "$TS_REPO" status --short` shows no new changes.

- [ ] **Step 5: Commit**

```bash
git add tools/golden-gen/gen-memory.mjs testdata/golden/MEMORY-KNOWN-DIFFS.md testdata/golden/memory-directive.json testdata/golden/memory-autoinjection.json
git commit -m "test(golden): session memory golden generator and TS golden files"
```

---

### Task 2: Group session events

**Files:**
- Create: `internal/memory/group.go`
- Test: `internal/memory/group_test.go`

**Interfaces:**
- Consumes: `store.StoredEvent` (`internal/store/events.go`: fields `ID int64`, `Type`, `Category`, `Data`, `CreatedAt string`).
- Produces: unexported `type groups struct { cat map[string][]store.StoredEvent; lastPrompt string; files []string; leg int64 }`; `func group(events []store.StoredEvent) groups`; `func (g groups) subagents() (current, prior []store.StoredEvent)`; `func (g groups) tasks() (pending, done []string)`; `func truthy(raw json.RawMessage) bool`; `func jsNumber(s string) float64`; `func jsString(raw json.RawMessage) string`; `func isJSSpace(r rune) bool`; `func jsTrim(s string) string`; test helper `func evs(rows ...[3]string) []store.StoredEvent`.

- [ ] **Step 1: Write the failing test**

`internal/memory/group_test.go`:
```go
package memory

import (
	"reflect"
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

// evs builds one session's rows from {category, type, data} triples, with
// ids 1, 2, 3, … in order, as store.SessionEvents returns them.
func evs(rows ...[3]string) []store.StoredEvent {
	out := make([]store.StoredEvent, len(rows))
	for i, r := range rows {
		out[i] = store.StoredEvent{ID: int64(i + 1), Category: r[0], Type: r[1], Data: r[2]}
	}
	return out
}

func TestGroupLastPromptIsLastUserPrompt(t *testing.T) {
	g := group(evs(
		[3]string{"user-prompt", "user_prompt", "first"},
		[3]string{"prompt", "prompt", "never written by capture"},
		[3]string{"user-prompt", "user_prompt", "second"},
		[3]string{"decision", "decision", "d"},
	))
	if g.lastPrompt != "second" {
		t.Fatalf("lastPrompt = %q", g.lastPrompt)
	}
	if len(g.cat["user-prompt"]) != 0 || len(g.cat["prompt"]) != 1 || len(g.cat["decision"]) != 1 {
		t.Fatalf("cat = %v", g.cat)
	}
}

func TestGroupFileNames(t *testing.T) {
	g := group(evs(
		[3]string{"file", "file_search", "TODO in /p/src"},
		[3]string{"file", "file_write", `C:\w\f.txt`},
		[3]string{"file", "file_edit", "/a/b/ "},
		[3]string{"file", "file_search", "x in y in /z/w.go"},
		[3]string{"file", "file_glob", "**/*.ts"},
		[3]string{"file", "file_read", "/z/w.go"},
		[3]string{"file", "file_read", "/k/\u00a0s.md\ufeff"},
	))
	want := []string{"src", "f.txt", "w.go", "s.md"}
	if !reflect.DeepEqual(g.files, want) {
		t.Fatalf("files = %q, want %q", g.files, want)
	}
}

func TestGroupLegBoundaryByID(t *testing.T) {
	rows := evs(
		[3]string{"session_start", "session_start", "{}"},
		[3]string{"subagent", "subagent_completed", "old"},
		[3]string{"session_start", "session_start", "{}"},
		[3]string{"subagent", "subagent_launched", "new"},
	)
	// Same created_at everywhere: only the ids order the rows.
	for i := range rows {
		rows[i].CreatedAt = "2026-01-01 00:00:00"
	}
	current, prior := group(rows).subagents()
	if len(current) != 1 || current[0].Data != "new" || len(prior) != 1 || prior[0].Data != "old" {
		t.Fatalf("current %v, prior %v", current, prior)
	}
	// With no session_start, nothing is prior-leg.
	if current, prior := group(rows[1:2]).subagents(); len(current) != 1 || len(prior) != 0 {
		t.Fatalf("first leg: current %v, prior %v", current, prior)
	}
}

func TestTasksMatchByAscendingID(t *testing.T) {
	rows := evs(
		[3]string{"task", "task_create", `{"subject":"A"}`},
		[3]string{"task", "task_create", `{"subject":"B"}`},
		[3]string{"task", "task_create", `{"subject":"C"}`},
		[3]string{"task", "task_create", `{"subject":"D"}`},
		[3]string{"task", "task_create", `{"subject":42}`},
		[3]string{"task", "task_update", `{"taskId":"3","status":"completed"}`},
		[3]string{"task", "task_update", `{"taskId":"1","status":"in_progress"}`},
		[3]string{"task", "task_update", `{"taskId":"10","status":"deleted"}`},
		[3]string{"task", "task_update", `{"taskId":"2"}`},
		[3]string{"task", "task", `{"todos":[]}`},
		[3]string{"task", "task_create", "not json"},
		[3]string{"task", "task_create", "null"},
		[3]string{"task", "task_create", `{"subject":""}`},
	)
	g := group(rows)
	// ids sort as 1, 3, 10: A in_progress, B completed, C deleted, D and 42 unmatched.
	// Rows JSON.parse rejects, and null, are skipped.
	pending, done := g.tasks()
	if !reflect.DeepEqual(pending, []string{"A", "D", "42"}) || !reflect.DeepEqual(done, []string{"B", "C"}) {
		t.Fatalf("pending %q done %q", pending, done)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/memory/ -run 'TestGroup|TestTasks' -count=1`
Expected: FAIL with `internal/memory/group_test.go:21:7: undefined: group`

- [ ] **Step 3: Write minimal implementation**

`internal/memory/group.go`:
```go
// Package memory builds the texts SessionStart injects after a compaction
// or a resume: the session directive and the auto-injection block. They are
// pure functions over one session's events, ported from
// hooks/session-directive.mjs and hooks/auto-injection.mjs.
package memory

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/lang315/context-mode-go/internal/store"
)

// groups is one pass over a session's events (SD:54-73) for the directive.
type groups struct {
	cat        map[string][]store.StoredEvent // by category, in id order
	lastPrompt string                         // data of the last user-prompt event
	files      []string                       // unique basenames, first seen first (a JS Set)
	leg        int64                          // id of the last session_start event, 0 when none
}

// group sorts events by category. TS read category "prompt", which capture
// never writes; cmgo reads "user-prompt" (spec §2.4).
func group(events []store.StoredEvent) groups {
	g := groups{cat: map[string][]store.StoredEvent{}}
	for _, ev := range events {
		if ev.Category == "session_start" {
			g.leg = ev.ID
		}
		if ev.Category == "user-prompt" {
			g.lastPrompt = ev.Data
			continue
		}
		g.cat[ev.Category] = append(g.cat[ev.Category], ev)
	}
	seen := map[string]bool{}
	for _, ev := range g.cat["file"] {
		path := ev.Data
		if i := strings.LastIndex(path, " in "); i >= 0 {
			path = path[i+len(" in "):]
		}
		base := jsTrim(path[strings.LastIndexAny(path, `/\`)+1:])
		if base != "" && !strings.Contains(base, "*") && !seen[base] {
			seen[base] = true
			g.files = append(g.files, base)
		}
	}
	return g
}

// subagents splits the subagent events at the leg boundary (#780): prior
// ones were written before the last session_start, in an earlier leg. TS
// compared created_at strings of 1 s resolution; ids are exact.
func (g groups) subagents() (current, prior []store.StoredEvent) {
	for _, ev := range g.cat["subagent"] {
		if g.leg > 0 && ev.ID < g.leg {
			prior = append(prior, ev)
		} else {
			current = append(current, ev)
		}
	}
	return current, prior
}

// tasks splits task subjects into pending and done (SD:300-334).
// A create has a subject, an update a taskId and a status; creates match
// updates in ascending numeric taskId order.
func (g groups) tasks() (pending, done []string) {
	var creates, ids []string
	updates := map[string]json.RawMessage{}
	for _, ev := range g.cat["task"] {
		var f map[string]json.RawMessage
		_ = json.Unmarshal([]byte(ev.Data), &f) // bad JSON, null and non-objects leave f nil, so the row is skipped (TS catch)
		if truthy(f["subject"]) {
			creates = append(creates, jsString(f["subject"]))
		} else if truthy(f["taskId"]) && truthy(f["status"]) {
			id := jsString(f["taskId"])
			if _, dup := updates[id]; !dup {
				ids = append(ids, id)
			}
			updates[id] = f["status"]
		}
	}
	// shortcut: Object.keys lists integer keys first; numeric taskIds (all Claude Code writes) sort the same. Upgrade if ids like "01" or "abc" appear.
	sort.SliceStable(ids, func(i, j int) bool { return jsNumber(ids[i]) < jsNumber(ids[j]) })
	for i, subject := range creates {
		var status string
		if i < len(ids) {
			_ = json.Unmarshal(updates[ids[i]], &status) // a non-string status is never done
		}
		switch status {
		case "completed", "deleted", "failed":
			done = append(done, subject)
		default:
			pending = append(pending, subject)
		}
	}
	return pending, done
}

// truthy is JS truthiness for a parsed JSON value; a missing key is nil.
func truthy(raw json.RawMessage) bool {
	switch s := string(raw); s {
	case "", "null", "false", `""`:
		return false
	default:
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			return n != 0
		}
		return true
	}
}

// jsNumber is JS Number(s) for a taskId key, NaN when it is not a number.
func jsNumber(s string) float64 {
	s = jsTrim(s)
	if s == "" {
		return 0
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return n
}

// jsString is JS String(v) for a parsed JSON value.
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
	return string(raw) // true, numbers
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/memory/ -run 'TestGroup|TestTasks' -count=1 -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/group.go internal/memory/group_test.go
git commit -m "feat(memory): group session events by category, prompt, files, leg and tasks"
```

### Task 3: Session directive

**Files:**
- Create: `internal/memory/directive.go`
- Test: `internal/memory/directive_test.go`
- Test: `internal/memory/golden_test.go`

**Interfaces:**
- Consumes: `group`, `groups.subagents`, `groups.tasks` (Task 2); `hookio.JSLen(s string) int` and `hookio.JSPrefix(s string, n int) string` (`internal/hookio/jsstr.go`); `testdata/golden/memory-directive.json` (Task 1).
- Produces: `func Directive(source string, events []store.StoredEvent) string`; unexported `const searchTool = "mcp__cmgo__ctx_search"`; `func cut(s string, n int) string`; `func text(b *strings.Builder, title, body string)`; `func list(b *strings.Builder, title string, items []string)`; `func mapData(evs []store.StoredEvent, n int) []string`; `func dataOf(evs []store.StoredEvent) []string`; `func uniqueData(evs []store.StoredEvent) []string`; `func unique(xs []string) []string`; `func lastData(evs []store.StoredEvent) (string, bool)`; `func ruleParents(rules []store.StoredEvent) []string`; `func environment(g groups) []string`; `func hasType(evs []store.StoredEvent, typ string) bool`; `func planActive(plans []store.StoredEvent) bool`; test helpers `goldenCase`, `goldenFile`, `loadGolden(t, name) goldenFile`, `knownDiffs() map[string]string`, `checkKnownDiffs(t, kind, cases)`.

- [ ] **Step 1: Write the failing test**

`internal/memory/directive_test.go`:
```go
package memory

import (
	"strings"
	"testing"
)

func TestDirectiveNoEvents(t *testing.T) {
	if got := Directive("compact", nil); got != "" {
		t.Fatalf("Directive(nil) = %q", got)
	}
}

func TestDirectiveSourceAttribute(t *testing.T) {
	rows := evs([3]string{"user-prompt", "user_prompt", "go on"})
	compact, resume := Directive("compact", rows), Directive("resume", rows)
	if !strings.HasPrefix(compact, "\n<session_knowledge source=\"compact\">") || !strings.Contains(compact, "<continue_from>") {
		t.Fatalf("compact:\n%s", compact)
	}
	if !strings.HasPrefix(resume, "\n<session_knowledge source=\"continue\">") || strings.Contains(resume, "<continue_from>") {
		t.Fatalf("resume:\n%s", resume)
	}
}

func TestCutCountsUTF16Units(t *testing.T) {
	e := "\U0001F600" // 2 units
	cases := []struct{ in, want string }{
		{strings.Repeat("d", 150), strings.Repeat("d", 150)},
		{strings.Repeat("d", 151), strings.Repeat("d", 147) + "..."},
		{"a" + strings.Repeat(e, 75), "a" + strings.Repeat(e, 73) + "..."}, // 151 units, 76 runes
		{strings.Repeat(e, 75), strings.Repeat(e, 75)},                     // 150 units
	}
	for _, c := range cases {
		if got := cut(c.in, 150); got != c.want {
			t.Errorf("cut(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
```

The golden test. `knownDiffs` holds the rows of `testdata/golden/MEMORY-KNOWN-DIFFS.md`:

`internal/memory/golden_test.go`:
```go
package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

// goldenCase is one case of ../../testdata/golden/memory-*.json, written by
// tools/golden-gen/gen-memory.mjs. ts is the TS output and expected the
// cmgo output, computed by the same TS code with the spec §2.4 fixes
// applied to its input (see MEMORY-KNOWN-DIFFS.md).
type goldenCase struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Events []struct {
		ID        int64  `json:"id"`
		Type      string `json:"type"`
		Category  string `json:"category"`
		Data      string `json:"data"`
		CreatedAt string `json:"created_at"`
	} `json:"events"`
	TS       *string `json:"ts"`
	Expected string  `json:"expected"`
}

func (c goldenCase) stored() []store.StoredEvent {
	out := make([]store.StoredEvent, len(c.Events))
	for i, e := range c.Events {
		out[i] = store.StoredEvent{ID: e.ID, Type: e.Type, Category: e.Category, Data: e.Data, CreatedAt: e.CreatedAt}
	}
	return out
}

type goldenFile struct {
	TSCommit string       `json:"tsCommit"`
	Cases    []goldenCase `json:"cases"`
}

func loadGolden(t *testing.T, name string) goldenFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", name))
	if err != nil {
		t.Fatal(err)
	}
	var g goldenFile
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if g.TSCommit != "a5e6dd31" || len(g.Cases) == 0 {
		t.Fatalf("%s: commit %q, %d cases", name, g.TSCommit, len(g.Cases))
	}
	return g
}

// knownDiffs are the cases where TS output differs from cmgo's on purpose,
// the rows of testdata/golden/MEMORY-KNOWN-DIFFS.md, with their fixes.
func knownDiffs() map[string]string {
	return map[string]string{
		"directive:full/compact":         "user-prompt, no-mcp-data",
		"directive:full/resume":          "user-prompt, no-mcp-data",
		"directive:leg/resume":           "leg-boundary",
		"directive:leg-tie/compact":      "leg-boundary",
		"directive:trunc/compact":        "user-prompt, no-mcp-data",
		"directive:trunc-exact/resume":   "user-prompt",
		"directive:trunc-astral/compact": "user-prompt",
		"directive:prompts/compact":      "user-prompt",
		"directive:prompts/resume":       "user-prompt",
		"directive:mcp-data-only/resume": "no-mcp-data",
	}
}

// checkKnownDiffs fails when a case's TS text differs from its expected text
// without a knownDiffs row, or has a row but no longer differs.
func checkKnownDiffs(t *testing.T, kind string, cases []goldenCase) {
	t.Helper()
	diffs := knownDiffs()
	for _, c := range cases {
		id := kind + ":" + c.Name
		fix, listed := diffs[id]
		delete(diffs, id)
		if c.TS == nil {
			t.Errorf("%s: no ts text", id)
		} else if differs := *c.TS != c.Expected; differs != listed {
			t.Errorf("%s: TS differs from expected = %v, but listed = %v (%s)", id, differs, listed, fix)
		}
	}
	for id := range diffs {
		if strings.HasPrefix(id, kind+":") {
			t.Errorf("%s: listed but no such case", id)
		}
	}
}

func TestGoldenDirective(t *testing.T) {
	g := loadGolden(t, "memory-directive.json")
	checkKnownDiffs(t, "directive", g.Cases)
	for _, c := range g.Cases {
		if got := Directive(c.Source, c.stored()); got != c.Expected {
			t.Errorf("%s:\n--- got\n%s\n--- want\n%s", c.Name, got, c.Expected)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/memory/ -run 'TestDirective|TestCut|TestGoldenDirective' -count=1`
Expected: FAIL with `internal/memory/directive_test.go:9:12: undefined: Directive`

- [ ] **Step 3: Write minimal implementation**

`internal/memory/directive.go`:
```go
package memory

import (
	"strings"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// searchTool is the name TS built with toolNamer("ctx_search").
const searchTool = "mcp__cmgo__ctx_search"

// Directive ports buildSessionDirective (SD:275-500) with the spec §2.4
// fixes: Last Request and <continue_from> read the last user-prompt event,
// prior-leg subagents are split by id, and the MCP Tools Used and Data
// References sections are gone (capture writes no mcp or data events).
// source is "compact" or "resume"; events are one session's rows in id
// order. It returns "" for no events, the guard TS kept in its caller.
func Directive(source string, events []store.StoredEvent) string {
	if len(events) == 0 {
		return ""
	}
	g := group(events)
	isCompact := source == "compact"
	attr := "continue"
	if isCompact {
		attr = "compact"
	}
	var b strings.Builder
	b.WriteString("\n<session_knowledge source=\"" + attr + "\">")
	b.WriteString("\n<session_guide>")

	if g.lastPrompt != "" {
		text(&b, "Last Request", cut(g.lastPrompt, 300))
	}
	pending, _ := g.tasks()
	list(&b, "Pending Tasks", pending)
	list(&b, "Key Decisions", mapData(g.cat["decision"], 150))
	if len(g.files) > 0 {
		text(&b, "Files Modified", strings.Join(g.files, ", "))
	}
	list(&b, "Unresolved Errors", mapData(g.cat["error"], 150))
	if git := uniqueData(g.cat["git"]); len(git) > 0 {
		text(&b, "Git", strings.Join(git, ", "))
	}
	if rules := ruleParents(g.cat["rule"]); len(rules) > 0 {
		text(&b, "Project Rules", strings.Join(rules, ", "))
	}
	current, prior := g.subagents()
	list(&b, "Subagent Tasks", mapData(current, 120))
	list(&b, "Subagent Tasks (earlier session — not current)", mapData(prior, 120))
	if skills := uniqueData(g.cat["skill"]); len(skills) > 0 {
		text(&b, "Skills Used", strings.Join(skills, ", "))
	}
	if env := environment(g); len(env) > 0 {
		text(&b, "Environment", strings.Join(env, "\n"))
	}
	if last, ok := lastData(g.cat["intent"]); ok {
		text(&b, "Session Intent", last)
	}
	if last, ok := lastData(g.cat["role"]); ok {
		text(&b, "User Role", last)
	}
	if plans := g.cat["plan"]; len(plans) > 0 {
		var lines []string
		switch {
		case hasType(plans, "plan_approved"):
			lines = []string{"Status: APPROVED AND EXECUTED", "The plan was approved and executed. Do NOT re-enter plan mode or re-propose the same plan."}
		case hasType(plans, "plan_rejected"):
			lines = []string{"Status: REJECTED BY USER", "The user rejected the previous plan. Ask what they want changed before re-planning."}
		case planActive(plans):
			lines = []string{"Status: ACTIVE (in planning phase)"}
			if hasType(plans, "plan_file_write") {
				lines = append(lines, "Plan file has been written. Awaiting user approval via ExitPlanMode.")
			}
		default:
			lines = []string{"Status: COMPLETED", "The plan has been executed. Do NOT re-enter plan mode or re-propose the same plan."}
		}
		list(&b, "Plan Mode", lines)
	}
	b.WriteString("\n</session_guide>")

	b.WriteString("\n<session_search>")
	b.WriteString("\nDetailed session data is indexed in context-mode FTS5 (source: \"session-events\").")
	b.WriteString("\nUse " + searchTool + "(queries: [...], source: \"session-events\") when you need specifics.")
	b.WriteString("\nDo NOT call ctx_index() — data is already indexed.")
	b.WriteString("\n</session_search>")
	if g.lastPrompt != "" && isCompact {
		b.WriteString("\n<continue_from>Continue working on the last request. Do NOT ask the user to repeat themselves.</continue_from>")
	}
	b.WriteString("\n</session_knowledge>")
	return b.String()
}

// text writes a directive section whose body is one block of text.
func text(b *strings.Builder, title, body string) {
	b.WriteString("\n## " + title + "\n" + body + "\n")
}

// list writes a directive section of "- " lines; nothing when items is empty.
func list(b *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	b.WriteString("\n## " + title)
	for _, it := range items {
		b.WriteString("\n- " + it)
	}
	b.WriteString("\n")
}

// cut keeps s when it is at most n UTF-16 units, else its first n-3 units
// and "...", as TS s.length > n ? s.substring(0, n-3) + "..." : s.
func cut(s string, n int) string {
	if hookio.JSLen(s) <= n {
		return s
	}
	return hookio.JSPrefix(s, n-3) + "..."
}

// mapData returns each event's data, cut to n units.
func mapData(evs []store.StoredEvent, n int) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = cut(ev.Data, n)
	}
	return out
}

// dataOf returns each event's data.
func dataOf(evs []store.StoredEvent) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.Data
	}
	return out
}

// uniqueData returns the distinct data values, first seen first.
func uniqueData(evs []store.StoredEvent) []string {
	return unique(dataOf(evs))
}

// unique drops repeats, first seen first (a JS Set).
func unique(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

func lastData(evs []store.StoredEvent) (string, bool) {
	if len(evs) == 0 {
		return "", false
	}
	return evs[len(evs)-1].Data, true
}

// ruleParents returns the last two path segments of each rule path (not
// rule_content), unique (SD:372-385).
func ruleParents(rules []store.StoredEvent) []string {
	var paths []string
	for _, ev := range rules {
		if ev.Type == "rule_content" {
			continue
		}
		segs := strings.Split(strings.ReplaceAll(ev.Data, `\`, "/"), "/") // JS split(/[/\\]/)
		if len(segs) > 2 {
			segs = segs[len(segs)-2:]
		}
		paths = append(paths, strings.Join(segs, "/"))
	}
	return unique(paths)
}

// environment returns "cwd: <last cwd>" and then every env event's data,
// the lines of both Environment sections.
func environment(g groups) []string {
	var lines []string
	if cwd, ok := lastData(g.cat["cwd"]); ok {
		lines = append(lines, "cwd: "+cwd)
	}
	return append(lines, dataOf(g.cat["env"])...)
}

func hasType(evs []store.StoredEvent, typ string) bool {
	for _, ev := range evs {
		if ev.Type == typ {
			return true
		}
	}
	return false
}

// planActive: the last plan event entered plan mode or wrote the plan file.
func planActive(plans []store.StoredEvent) bool {
	last := plans[len(plans)-1].Type
	return last == "plan_enter" || last == "plan_file_write"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/memory/ -run 'TestDirective|TestCut|TestGoldenDirective' -count=1 -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/directive.go internal/memory/directive_test.go internal/memory/golden_test.go
git commit -m "feat(memory): session directive with the user-prompt and leg-boundary fixes"
```

### Task 4: Auto-injection

**Files:**
- Create: `internal/memory/autoinject.go`
- Test: `internal/memory/autoinject_test.go`
- Modify: `internal/memory/golden_test.go`

**Interfaces:**
- Consumes: `unique` (Task 3); `hookio.JSLen`, `hookio.JSPrefix`; `loadGolden` (Task 3); `testdata/golden/memory-autoinjection.json` (Task 1).
- Produces: `func AutoInjection(events []store.StoredEvent) string`.

- [ ] **Step 1: Write the failing test**

`internal/memory/autoinject_test.go`:
```go
package memory

import (
	"strings"
	"testing"
)

func TestAutoInjectionNothingToInject(t *testing.T) {
	if got := AutoInjection(nil); got != "" {
		t.Fatalf("AutoInjection(nil) = %q", got)
	}
	if got := AutoInjection(evs([3]string{"file", "file_read", "/a.ts"})); got != "" {
		t.Fatalf("AutoInjection(file only) = %q", got)
	}
}

func TestAutoInjectionBudgetDropsIntent(t *testing.T) {
	rows := [][3]string{{"role", "role", strings.Repeat("r", 450)}}
	for i := 0; i < 10; i++ {
		rows = append(rows, [3]string{"skill", "skill", strings.Repeat("s", 200) + string(rune('a'+i))})
	}
	rows = append(rows, [3]string{"intent", "intent", "review"})
	got := AutoInjection(evs(rows...))
	if !strings.Contains(got, "<behavioral_directive>\n"+strings.Repeat("r", 400)+"\n</behavioral_directive>") {
		t.Fatalf("role not cut to 400 units:\n%s", got)
	}
	if !strings.Contains(got, "<active_skills>") || strings.Contains(got, "<session_mode>") {
		t.Fatalf("want skills and no intent once the budget is spent:\n%s", got)
	}
}
```

`TestGoldenAutoInjection` is added to the golden test:

`internal/memory/golden_test.go`:
```go
package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

// goldenCase is one case of ../../testdata/golden/memory-*.json, written by
// tools/golden-gen/gen-memory.mjs. ts is the TS output and expected the
// cmgo output, computed by the same TS code with the spec §2.4 fixes
// applied to its input (see MEMORY-KNOWN-DIFFS.md).
type goldenCase struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Events []struct {
		ID        int64  `json:"id"`
		Type      string `json:"type"`
		Category  string `json:"category"`
		Data      string `json:"data"`
		CreatedAt string `json:"created_at"`
	} `json:"events"`
	TS       *string `json:"ts"`
	Expected string  `json:"expected"`
}

func (c goldenCase) stored() []store.StoredEvent {
	out := make([]store.StoredEvent, len(c.Events))
	for i, e := range c.Events {
		out[i] = store.StoredEvent{ID: e.ID, Type: e.Type, Category: e.Category, Data: e.Data, CreatedAt: e.CreatedAt}
	}
	return out
}

type goldenFile struct {
	TSCommit string       `json:"tsCommit"`
	Cases    []goldenCase `json:"cases"`
}

func loadGolden(t *testing.T, name string) goldenFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", name))
	if err != nil {
		t.Fatal(err)
	}
	var g goldenFile
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if g.TSCommit != "a5e6dd31" || len(g.Cases) == 0 {
		t.Fatalf("%s: commit %q, %d cases", name, g.TSCommit, len(g.Cases))
	}
	return g
}

// knownDiffs are the cases where TS output differs from cmgo's on purpose,
// the rows of testdata/golden/MEMORY-KNOWN-DIFFS.md, with their fixes.
func knownDiffs() map[string]string {
	return map[string]string{
		"directive:full/compact":         "user-prompt, no-mcp-data",
		"directive:full/resume":          "user-prompt, no-mcp-data",
		"directive:leg/resume":           "leg-boundary",
		"directive:leg-tie/compact":      "leg-boundary",
		"directive:trunc/compact":        "user-prompt, no-mcp-data",
		"directive:trunc-exact/resume":   "user-prompt",
		"directive:trunc-astral/compact": "user-prompt",
		"directive:prompts/compact":      "user-prompt",
		"directive:prompts/resume":       "user-prompt",
		"directive:mcp-data-only/resume": "no-mcp-data",
	}
}

// checkKnownDiffs fails when a case's TS text differs from its expected text
// without a knownDiffs row, or has a row but no longer differs.
func checkKnownDiffs(t *testing.T, kind string, cases []goldenCase) {
	t.Helper()
	diffs := knownDiffs()
	for _, c := range cases {
		id := kind + ":" + c.Name
		fix, listed := diffs[id]
		delete(diffs, id)
		if c.TS == nil {
			t.Errorf("%s: no ts text", id)
		} else if differs := *c.TS != c.Expected; differs != listed {
			t.Errorf("%s: TS differs from expected = %v, but listed = %v (%s)", id, differs, listed, fix)
		}
	}
	for id := range diffs {
		if strings.HasPrefix(id, kind+":") {
			t.Errorf("%s: listed but no such case", id)
		}
	}
}

func TestGoldenDirective(t *testing.T) {
	g := loadGolden(t, "memory-directive.json")
	checkKnownDiffs(t, "directive", g.Cases)
	for _, c := range g.Cases {
		if got := Directive(c.Source, c.stored()); got != c.Expected {
			t.Errorf("%s:\n--- got\n%s\n--- want\n%s", c.Name, got, c.Expected)
		}
	}
}

func TestGoldenAutoInjection(t *testing.T) {
	for _, c := range loadGolden(t, "memory-autoinjection.json").Cases {
		if got := AutoInjection(c.stored()); got != c.Expected {
			t.Errorf("%s:\n--- got\n%s\n--- want\n%s", c.Name, got, c.Expected)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/memory/ -run 'TestAutoInjection|TestGoldenAutoInjection' -count=1`
Expected: FAIL with `internal/memory/autoinject_test.go:9:12: undefined: AutoInjection`

- [ ] **Step 3: Write minimal implementation**

`internal/memory/autoinject.go`:
```go
package memory

import (
	"strings"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// AutoInjection ports buildAutoInjection (AI:30-102): the last role, the
// last 5 decisions, the last 10 unique skills and the last intent, in that
// priority, under a 500-token budget (a token is 4 UTF-16 units, rounded
// up). It returns "" when there is nothing to inject.
func AutoInjection(events []store.StoredEvent) string {
	var role, intent *store.StoredEvent
	var decisions, skills []string
	for i, ev := range events {
		switch ev.Category {
		case "role":
			role = &events[i]
		case "decision":
			decisions = append(decisions, ev.Data)
		case "skill":
			skills = append(skills, ev.Data)
		case "intent":
			intent = &events[i]
		}
	}
	skills = unique(skills)

	var parts []string
	budget := 500
	add := func(s string) {
		parts = append(parts, s)
		budget -= (hookio.JSLen(s) + 3) / 4
	}
	if role != nil {
		add("<behavioral_directive>\n" + hookio.JSPrefix(role.Data, 400) + "\n</behavioral_directive>")
	}
	if len(decisions) > 0 {
		// TS falls back to the last 3 when this block exceeds the budget. It
		// cannot: the role costs at most 112 tokens and 5 decisions 139.
		lines := make([]string, 0, 5)
		for _, d := range decisions[max(0, len(decisions)-5):] {
			lines = append(lines, "- "+hookio.JSPrefix(d, 100))
		}
		add("<rules>\nFollow these decisions:\n" + strings.Join(lines, "\n") + "\n</rules>")
	}
	if len(skills) > 0 && budget > 50 {
		add("<active_skills>\nRe-invoke if relevant: " + strings.Join(skills[max(0, len(skills)-10):], ", ") +
			"\nTo reload: call the Skill tool with the skill name.\n</active_skills>")
	}
	if intent != nil && budget > 20 {
		parts = append(parts, "<session_mode>"+intent.Data+"</session_mode>")
	}
	if len(parts) == 0 {
		return ""
	}
	return "<session_state source=\"compaction\">\n\n" + strings.Join(parts, "\n\n") + "\n\n</session_state>"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/memory/ -run 'TestGroup|TestTasks|TestDirective|TestCut|TestAutoInjection|TestGolden' -count=1 -race -v && go vet ./internal/memory/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/autoinject.go internal/memory/autoinject_test.go internal/memory/golden_test.go
git commit -m "feat(memory): compaction auto-injection block"
```

---

### Task 5: Session events matching a timeline query

**Files:**
- Create: `internal/store/memory.go`
- Test: `internal/store/memory_test.go`

**Interfaces:**
- Consumes: `func jsFields(s string) []string`, `func likeParam(s string) string` (internal/store/search.go); the `session_events` table and `StoredEvent` (internal/store/events.go)
- Produces: `func (s *Store) SessionEventsForSearch(project, query, source string, limit int) ([]StoredEvent, error)`; unexported `func scanEvents(rows *sql.Rows) ([]StoredEvent, error)`

- [ ] **Step 1: Write the failing test**

`internal/store/memory_test.go`:
```go
package store

import (
	"strings"
	"testing"
)

func insertTestEvents(t *testing.T, s *Store, sid, project string, evs ...Event) {
	t.Helper()
	if _, err := s.InsertEvents(sid, project, "PostToolUse", evs); err != nil {
		t.Fatal(err)
	}
}

func eventData(evs []StoredEvent) string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Data)
	}
	return strings.Join(out, "|")
}

func TestSessionEventsForSearch(t *testing.T) {
	s := openTest(t)
	insertTestEvents(t, s, "s1", "/p",
		Event{Type: "decision", Category: "decision", Priority: 2, Data: "use Alpha cache with BETA flag"},
		Event{Type: "file_read", Category: "file", Priority: 1, Data: "alpha.go"},
		Event{Type: "error_tool", Category: "error", Priority: 2, Data: "rate 50% exceeded"},
	)
	insertTestEvents(t, s, "s2", "/p",
		Event{Type: "decision", Category: "decision", Priority: 2, Data: "alpha beta second session"},
		Event{Type: "git", Category: "git", Priority: 2, Data: "rate 500 commits"},
	)
	insertTestEvents(t, s, "s3", "/q",
		Event{Type: "decision", Category: "decision", Priority: 2, Data: "alpha beta other project"},
	)

	cases := []struct {
		query, source string
		limit         int
		want          string
	}{
		// Every term must occur (case-insensitive) in data or category; all sessions of the project; newest first.
		{"alpha beta", "", 10, "alpha beta second session|use Alpha cache with BETA flag"},
		{"ALPHA", "", 10, "alpha beta second session|alpha.go|use Alpha cache with BETA flag"},
		{"alpha  beta", "", 1, "alpha beta second session"},
		// A term may match the category instead of the data.
		{"deci alpha", "", 10, "alpha beta second session|use Alpha cache with BETA flag"},
		// source must equal the category exactly.
		{"alpha", "file", 10, "alpha.go"},
		{"alpha", "fil", 10, ""},
		// % and _ are literal.
		{"50%", "", 10, "rate 50% exceeded"},
		{"rate_", "", 10, ""},
		// No terms: every row of the project, newest first.
		{"   ", "", 2, "rate 500 commits|alpha beta second session"},
	}
	for _, c := range cases {
		evs, err := s.SessionEventsForSearch("/p", c.query, c.source, c.limit)
		if err != nil {
			t.Fatalf("%q/%q: %v", c.query, c.source, err)
		}
		if got := eventData(evs); got != c.want {
			t.Errorf("%q/%q limit %d = %q, want %q", c.query, c.source, c.limit, got, c.want)
		}
	}

	evs, err := s.SessionEventsForSearch("/p", "deci", "decision", 1)
	if err != nil || len(evs) != 1 {
		t.Fatalf("full row: %v %v", evs, err)
	}
	if e := evs[0]; e.SessionID != "s2" || e.Project != "/p" || e.Type != "decision" || e.Category != "decision" ||
		e.Priority != 2 || e.SourceHook != "PostToolUse" || !sqliteTimeRe.MatchString(e.CreatedAt) {
		t.Fatalf("row = %+v", e)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestSessionEventsForSearch -v`
Expected: FAIL with "internal/store/memory_test.go:59:17: s.SessionEventsForSearch undefined (type *Store has no field or method SessionEventsForSearch)"

- [ ] **Step 3: Write minimal implementation**

`internal/store/memory.go`:
```go
package store

import "database/sql"

// SessionEventsForSearch returns the rows of project whose data or category
// contains every whitespace-separated term of query (SQLite LIKE: ASCII
// case folding, % and _ literal). category must equal source when source is
// not "". Newest first (id DESC), at most limit rows. A query with no terms
// matches every row, as TS's LIKE '%%' did.
// TS searchEvents (db.ts:1387-1421) matched the whole query as one substring
// and kept the oldest rows; spec §3.2 splits terms and keeps the newest.
func (s *Store) SessionEventsForSearch(project, query, source string, limit int) ([]StoredEvent, error) {
	where := "project = ?"
	args := []any{project}
	for _, t := range jsFields(query) {
		where += ` AND (data LIKE ? ESCAPE '\' OR category LIKE ? ESCAPE '\')`
		args = append(args, likeParam(t), likeParam(t))
	}
	if source != "" {
		where += " AND category = ?"
		args = append(args, source)
	}
	args = append(args, limit)
	rows, err := s.db.Query(`SELECT id, session_id, project, type, category, priority, data, data_hash, source_hook, bytes_avoided, created_at
FROM session_events WHERE `+where+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

// scanEvents reads full session_events rows in query order and closes rows.
func scanEvents(rows *sql.Rows) ([]StoredEvent, error) {
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

Run: `go test ./internal/store/ -run TestSessionEventsForSearch -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/memory.go internal/store/memory_test.go
git commit -m "feat(store): search session events by query terms"
```

### Task 6: CompactSession records a compaction

**Files:**
- Modify: `internal/store/events.go`
- Modify: `internal/store/memory.go`
- Test: `internal/store/memory_test.go`

**Interfaces:**
- Consumes: `func (s *Store) InsertEvents(sessionID, project, sourceHook string, evs []Event) (int, error)` (its body moves into `insertEvents`); `func scanEvents(rows *sql.Rows) ([]StoredEvent, error)` (Task 5)
- Produces: `func (s *Store) CompactSession(sessionID, project string) (n, files int, err error)`; unexported `func insertEvents(tx *sql.Tx, sessionID, project, sourceHook string, evs []Event) (int, error)`

- [ ] **Step 1: Write the failing test**

`internal/store/memory_test.go`:
```go
package store

import (
	"fmt"
	"strings"
	"testing"
)

func insertTestEvents(t *testing.T, s *Store, sid, project string, evs ...Event) {
	t.Helper()
	if _, err := s.InsertEvents(sid, project, "PostToolUse", evs); err != nil {
		t.Fatal(err)
	}
}

func eventData(evs []StoredEvent) string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Data)
	}
	return strings.Join(out, "|")
}

func TestSessionEventsForSearch(t *testing.T) {
	s := openTest(t)
	insertTestEvents(t, s, "s1", "/p",
		Event{Type: "decision", Category: "decision", Priority: 2, Data: "use Alpha cache with BETA flag"},
		Event{Type: "file_read", Category: "file", Priority: 1, Data: "alpha.go"},
		Event{Type: "error_tool", Category: "error", Priority: 2, Data: "rate 50% exceeded"},
	)
	insertTestEvents(t, s, "s2", "/p",
		Event{Type: "decision", Category: "decision", Priority: 2, Data: "alpha beta second session"},
		Event{Type: "git", Category: "git", Priority: 2, Data: "rate 500 commits"},
	)
	insertTestEvents(t, s, "s3", "/q",
		Event{Type: "decision", Category: "decision", Priority: 2, Data: "alpha beta other project"},
	)

	cases := []struct {
		query, source string
		limit         int
		want          string
	}{
		// Every term must occur (case-insensitive) in data or category; all sessions of the project; newest first.
		{"alpha beta", "", 10, "alpha beta second session|use Alpha cache with BETA flag"},
		{"ALPHA", "", 10, "alpha beta second session|alpha.go|use Alpha cache with BETA flag"},
		{"alpha  beta", "", 1, "alpha beta second session"},
		// A term may match the category instead of the data.
		{"deci alpha", "", 10, "alpha beta second session|use Alpha cache with BETA flag"},
		// source must equal the category exactly.
		{"alpha", "file", 10, "alpha.go"},
		{"alpha", "fil", 10, ""},
		// % and _ are literal.
		{"50%", "", 10, "rate 50% exceeded"},
		{"rate_", "", 10, ""},
		// No terms: every row of the project, newest first.
		{"   ", "", 2, "rate 500 commits|alpha beta second session"},
	}
	for _, c := range cases {
		evs, err := s.SessionEventsForSearch("/p", c.query, c.source, c.limit)
		if err != nil {
			t.Fatalf("%q/%q: %v", c.query, c.source, err)
		}
		if got := eventData(evs); got != c.want {
			t.Errorf("%q/%q limit %d = %q, want %q", c.query, c.source, c.limit, got, c.want)
		}
	}

	evs, err := s.SessionEventsForSearch("/p", "deci", "decision", 1)
	if err != nil || len(evs) != 1 {
		t.Fatalf("full row: %v %v", evs, err)
	}
	if e := evs[0]; e.SessionID != "s2" || e.Project != "/p" || e.Type != "decision" || e.Category != "decision" ||
		e.Priority != 2 || e.SourceHook != "PostToolUse" || !sqliteTimeRe.MatchString(e.CreatedAt) {
		t.Fatalf("row = %+v", e)
	}
}

func compactCount(t *testing.T, s *Store, sid string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT compact_count FROM session_meta WHERE session_id = ?", sid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCompactSession(t *testing.T) {
	s := openTest(t)
	// No events: nothing is written, not even a session_meta row (TS precompact skips, no idle writes).
	if n, files, err := s.CompactSession("empty", "/p"); err != nil || n != 0 || files != 0 {
		t.Fatalf("empty = %d %d %v", n, files, err)
	}
	var metas int
	if err := s.db.QueryRow("SELECT count(*) FROM session_meta").Scan(&metas); err != nil || metas != 0 {
		t.Fatalf("meta rows = %d %v", metas, err)
	}

	insertTestEvents(t, s, "s1", "/p",
		Event{Type: "file_read", Category: "file", Priority: 1, Data: "a.go"},
		Event{Type: "file_write", Category: "file", Priority: 1, Data: "b.go"},
		Event{Type: "git", Category: "git", Priority: 2, Data: "status"},
	)
	n, files, err := s.CompactSession("s1", "/p")
	if err != nil || n != 3 || files != 2 {
		t.Fatalf("CompactSession = %d %d %v", n, files, err)
	}
	evs, _ := s.SessionEvents("s1")
	last := evs[len(evs)-1]
	if len(evs) != 4 || last.Type != "compaction_summary" || last.Category != "compaction" || last.Priority != 1 ||
		last.Data != "Session compacted. 3 events, 2 files touched." || last.SourceHook != "PreCompact" || last.Project != "/p" {
		t.Fatalf("rows = %+v", evs)
	}
	if _, _, _, count := metaRow(t, s, "s1"); count != 4 || compactCount(t, s, "s1") != 1 {
		t.Fatalf("meta event_count %d compact_count %d", count, compactCount(t, s, "s1"))
	}

	// The second compaction counts the first summary.
	if n, files, err := s.CompactSession("s1", "/p"); err != nil || n != 4 || files != 2 {
		t.Fatalf("second = %d %d %v", n, files, err)
	}
	evs, _ = s.SessionEvents("s1")
	if evs[len(evs)-1].Data != "Session compacted. 4 events, 2 files touched." || compactCount(t, s, "s1") != 2 {
		t.Fatalf("second rows = %+v", evs)
	}
}

func TestCompactSessionKeepsEventCap(t *testing.T) {
	s := openTest(t)
	evs := make([]Event, MaxEventsPerSession)
	for i := range evs {
		evs[i] = Event{Type: "note", Category: "file", Priority: 4, Data: fmt.Sprint("e", i)}
	}
	insertTestEvents(t, s, "s1", "/p", evs...)
	if n, files, err := s.CompactSession("s1", "/p"); err != nil || n != MaxEventsPerSession || files != MaxEventsPerSession {
		t.Fatalf("CompactSession = %d %d %v", n, files, err)
	}
	got, _ := s.SessionEvents("s1")
	if len(got) != MaxEventsPerSession || got[0].Data != "e1" || got[len(got)-1].Type != "compaction_summary" {
		t.Fatalf("%d rows, first %q last %q", len(got), got[0].Data, got[len(got)-1].Type)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestCompactSession|TestInsertEvents|TestSessionEvents' -v`
Expected: FAIL with "internal/store/memory_test.go:91:24: s.CompactSession undefined (type *Store has no field or method CompactSession)"

- [ ] **Step 3: Write minimal implementation**

`InsertEvents` keeps its signature and behavior; its transaction body becomes `insertEvents(tx, ...)` so `CompactSession` applies the same meta upsert, dedup window and 1000-row cap inside its own transaction. `SessionEvents` now reads rows with `scanEvents`.

`internal/store/events.go`:
```go
package store

import (
	"crypto/sha256"
	"database/sql"
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
	inserted, err := insertEvents(tx, sessionID, project, sourceHook, evs)
	if err != nil {
		return 0, err
	}
	return inserted, tx.Commit()
}

// insertEvents is InsertEvents inside the caller's transaction, so other
// writers (CompactSession) apply the same meta upsert, dedup and cap.
func insertEvents(tx *sql.Tx, sessionID, project, sourceHook string, evs []Event) (int, error) {
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
			// Evict the oldest of the least important rows (TS: priority ASC, R2 Port note 1),
			// unless every row is more important than the new event: then drop the new one.
			var victimID int64
			var victimPriority int
			if err := tx.QueryRow(`SELECT id, priority FROM session_events WHERE session_id = ?
ORDER BY priority DESC, id ASC LIMIT 1`, sessionID).Scan(&victimID, &victimPriority); err != nil {
				return 0, err
			}
			if victimPriority < ev.Priority {
				continue
			}
			if _, err := tx.Exec("DELETE FROM session_events WHERE id = ?", victimID); err != nil {
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
	return inserted, nil
}

// SessionEvents returns a session's rows ordered by id.
func (s *Store) SessionEvents(sessionID string) ([]StoredEvent, error) {
	rows, err := s.db.Query(`SELECT id, session_id, project, type, category, priority, data, data_hash, source_hook, bytes_avoided, created_at
FROM session_events WHERE session_id = ? ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}
```

`internal/store/memory.go`:
```go
package store

import (
	"database/sql"
	"fmt"
)

// SessionEventsForSearch returns the rows of project whose data or category
// contains every whitespace-separated term of query (SQLite LIKE: ASCII
// case folding, % and _ literal). category must equal source when source is
// not "". Newest first (id DESC), at most limit rows. A query with no terms
// matches every row, as TS's LIKE '%%' did.
// TS searchEvents (db.ts:1387-1421) matched the whole query as one substring
// and kept the oldest rows; spec §3.2 splits terms and keeps the newest.
func (s *Store) SessionEventsForSearch(project, query, source string, limit int) ([]StoredEvent, error) {
	where := "project = ?"
	args := []any{project}
	for _, t := range jsFields(query) {
		where += ` AND (data LIKE ? ESCAPE '\' OR category LIKE ? ESCAPE '\')`
		args = append(args, likeParam(t), likeParam(t))
	}
	if source != "" {
		where += " AND category = ?"
		args = append(args, source)
	}
	args = append(args, limit)
	rows, err := s.db.Query(`SELECT id, session_id, project, type, category, priority, data, data_hash, source_hook, bytes_avoided, created_at
FROM session_events WHERE `+where+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

// scanEvents reads full session_events rows in query order and closes rows.
func scanEvents(rows *sql.Rows) ([]StoredEvent, error) {
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

// CompactSession records a compaction (spec §2.1) in one transaction: it
// counts the session's events (n) and its file-category events (files),
// inserts the compaction_summary event with InsertEvents' dedup and cap,
// and increments compact_count. A session with no events is left untouched,
// as TS precompact.mjs:47 skips it.
func (s *Store) CompactSession(sessionID, project string) (n, files int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	if err := tx.QueryRow("SELECT count(*), count(CASE WHEN category = 'file' THEN 1 END) FROM session_events WHERE session_id = ?",
		sessionID).Scan(&n, &files); err != nil {
		return 0, 0, err
	}
	if n == 0 {
		return 0, 0, nil
	}
	ev := Event{Type: "compaction_summary", Category: "compaction", Priority: 1,
		Data: fmt.Sprintf("Session compacted. %d events, %d files touched.", n, files)}
	if _, err := insertEvents(tx, sessionID, project, "PreCompact", []Event{ev}); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec("UPDATE session_meta SET compact_count = compact_count + 1 WHERE session_id = ?", sessionID); err != nil {
		return 0, 0, err
	}
	return n, files, tx.Commit()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run 'TestCompactSession|TestInsertEvents|TestSessionEvents' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/events.go internal/store/memory.go internal/store/memory_test.go
git commit -m "feat(store): record compactions with CompactSession"
```

### Task 7: PurgeSession and PurgeProject

**Files:**
- Create: `internal/store/purge.go`
- Test: `internal/store/purge_test.go`

**Interfaces:**
- Consumes: `func (s *Store) Index(project, label string, chunks []Chunk) (IndexResult, error)`, `func (s *Store) ChunkCount(project string) (int, error)`, `func insertTestEvents(t *testing.T, s *Store, sid, project string, evs ...Event)` (memory_test.go, Task 5)
- Produces: `func (s *Store) PurgeSession(sessionID string) (rows int64, err error)`; `func (s *Store) PurgeProject(project string) (sources, sessions int64, err error)`; unexported `func execCount(tx *sql.Tx, query string, args ...any) (int64, error)`

- [ ] **Step 1: Write the failing test**

`internal/store/purge_test.go`:
```go
package store

import "testing"

func tableCount(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPurgeSession(t *testing.T) {
	s := openTest(t)
	insertTestEvents(t, s, "s1", "/p", Event{Type: "a", Category: "file", Priority: 1, Data: "x"}, Event{Type: "b", Category: "git", Priority: 2, Data: "y"})
	insertTestEvents(t, s, "s2", "/p", Event{Type: "a", Category: "file", Priority: 1, Data: "z"})
	if _, err := s.Index("/p", "notes", []Chunk{{Title: "T", Content: "kept content"}}); err != nil {
		t.Fatal(err)
	}

	// 2 events + 1 meta row.
	if rows, err := s.PurgeSession("s1"); err != nil || rows != 3 {
		t.Fatalf("PurgeSession = %d, %v", rows, err)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM session_events WHERE session_id = 's1'") +
		tableCount(t, s, "SELECT count(*) FROM session_meta WHERE session_id = 's1'"); n != 0 {
		t.Fatalf("s1 rows left: %d", n)
	}
	if evs, _ := s.SessionEvents("s2"); len(evs) != 1 {
		t.Fatalf("s2 events = %d", len(evs))
	}
	if n, _ := s.ChunkCount("/p"); n != 1 {
		t.Fatalf("chunks = %d", n)
	}
	if rows, err := s.PurgeSession("s1"); err != nil || rows != 0 {
		t.Fatalf("second PurgeSession = %d, %v", rows, err)
	}
}

func TestPurgeProjectLeavesOtherProjects(t *testing.T) {
	s := openTest(t)
	for _, p := range []string{"/p", "/q"} {
		for _, label := range []string{"notes", "docs"} {
			if _, err := s.Index(p, label, []Chunk{{Title: "T", Content: "gizmo widget " + p}, {Title: "U", Content: "second chunk"}}); err != nil {
				t.Fatal(err)
			}
		}
		insertTestEvents(t, s, p+"-s1", p, Event{Type: "a", Category: "file", Priority: 1, Data: "x"})
		insertTestEvents(t, s, p+"-s2", p, Event{Type: "a", Category: "file", Priority: 1, Data: "y"})
	}
	vocab := tableCount(t, s, "SELECT count(*) FROM vocabulary")

	sources, sessions, err := s.PurgeProject("/p")
	if err != nil || sources != 2 || sessions != 2 {
		t.Fatalf("PurgeProject = %d %d %v", sources, sessions, err)
	}
	if n, _ := s.ChunkCount("/p"); n != 0 {
		t.Fatalf("/p chunks = %d", n)
	}
	if n, _ := s.ChunkCount("/q"); n != 4 {
		t.Fatalf("/q chunks = %d", n)
	}
	// Trigram rows go with their sources; /q keeps its 4.
	if n := tableCount(t, s, "SELECT count(*) FROM chunks_trigram"); n != 4 {
		t.Fatalf("trigram rows = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM sources WHERE project = '/p'"); n != 0 {
		t.Fatalf("/p sources = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM session_events WHERE project = '/p'") +
		tableCount(t, s, "SELECT count(*) FROM session_meta WHERE project = '/p'"); n != 0 {
		t.Fatalf("/p session rows = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM session_events WHERE project = '/q'"); n != 2 {
		t.Fatalf("/q events = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM session_meta WHERE project = '/q'"); n != 2 {
		t.Fatalf("/q meta = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM vocabulary"); n != vocab {
		t.Fatalf("vocabulary %d -> %d", vocab, n)
	}
	if sources, sessions, err := s.PurgeProject("/p"); err != nil || sources != 0 || sessions != 0 {
		t.Fatalf("second PurgeProject = %d %d %v", sources, sessions, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestPurge -v`
Expected: FAIL with "internal/store/purge_test.go:23:20: s.PurgeSession undefined (type *Store has no field or method PurgeSession)"

- [ ] **Step 3: Write minimal implementation**

`internal/store/purge.go`:
```go
package store

import "database/sql"

// PurgeSession deletes one session's session_events and session_meta rows
// and returns how many rows went. Port of the session scope of purgeSession
// (src/session/purge.ts:196-275); chunks carry no session id here.
func (s *Store) PurgeSession(sessionID string) (rows int64, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, q := range []string{
		"DELETE FROM session_events WHERE session_id = ?",
		"DELETE FROM session_meta WHERE session_id = ?",
	} {
		n, err := execCount(tx, q, sessionID)
		if err != nil {
			return 0, err
		}
		rows += n
	}
	return rows, tx.Commit()
}

// PurgeProject deletes every source of project with its porter and trigram
// chunks, and the events and meta rows of the project's sessions. The shared
// vocabulary is kept. Returns the sources and sessions (meta rows) deleted.
// This replaces the TS project scope, which unlinked the per-project DB files
// (purge.ts:277-338); cmgo's DB is shared, so only this project's rows go.
func (s *Store) PurgeProject(project string) (sources, sessions int64, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	ids := "SELECT id FROM sources WHERE project = ?"
	if _, err := tx.Exec("DELETE FROM chunks WHERE source_id IN ("+ids+")", project); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec("DELETE FROM chunks_trigram WHERE source_id IN ("+ids+")", project); err != nil {
		return 0, 0, err
	}
	if sources, err = execCount(tx, "DELETE FROM sources WHERE project = ?", project); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec("DELETE FROM session_events WHERE project = ? OR session_id IN (SELECT session_id FROM session_meta WHERE project = ?)",
		project, project); err != nil {
		return 0, 0, err
	}
	if sessions, err = execCount(tx, "DELETE FROM session_meta WHERE project = ?", project); err != nil {
		return 0, 0, err
	}
	return sources, sessions, tx.Commit()
}

// execCount runs a statement and returns its RowsAffected.
func execCount(tx *sql.Tx, query string, args ...any) (int64, error) {
	res, err := tx.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestPurge -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/purge.go internal/store/purge_test.go
git commit -m "feat(store): purge one session or one project"
```

### Task 8: Auto-memory search

**Files:**
- Create: `internal/memory/automemory.go`
- Test: `internal/memory/automemory_test.go`

**Interfaces:**
- Consumes: `func hookio.JSLen(s string) int`
- Produces: `type MemoryHit struct { Title, Content, Source string; Timestamp time.Time }`; `func ProjectSlug(projectDir string) string`; `func SearchAutoMemory(query string, limit int, projectDir, home string) []MemoryHit`; unexported `autoMemoryTerms`, `autoMemorySnippet`, `indexU16`, `lastIndexU16`, `autoMemorySpace`

- [ ] **Step 1: Write the failing test**

`internal/memory/automemory_test.go`:
```go
package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectSlug(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/lang/x.y": "-Users-lang-x-y",
		"/Users/lang/GolandProjects/github.com/lang315/context-mode": "-Users-lang-GolandProjects-github-com-lang315-context-mode",
		"/tmp/My Project": "-tmp-My Project",
	} {
		if got := ProjectSlug(in); got != want {
			t.Errorf("ProjectSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// memoryFixture returns a project dir and home with the three candidate kinds.
func memoryFixture(t *testing.T) (project, home, memDir string) {
	root := t.TempDir()
	project = filepath.Join(root, "work", "my.app")
	home = filepath.Join(root, "home")
	memDir = filepath.Join(home, ".claude", "projects", ProjectSlug(project), "memory")
	writeFile(t, filepath.Join(project, "CLAUDE.md"), "Project rules: run the widget tests.")
	writeFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), "User rules: prefer widget names.")
	writeFile(t, filepath.Join(memDir, "b.md"), "Widget note B.")
	writeFile(t, filepath.Join(memDir, "a.md"), "Widget note A.")
	writeFile(t, filepath.Join(memDir, "c.txt"), "widget ignored: not markdown")
	// Another project's memory dir is never read.
	writeFile(t, filepath.Join(home, ".claude", "projects", "-other", "memory", "x.md"), "widget elsewhere")
	return project, home, memDir
}

func hitLabels(hits []MemoryHit) string {
	var out []string
	for _, h := range hits {
		out = append(out, h.Source)
	}
	return strings.Join(out, ",")
}

func TestSearchAutoMemoryCandidates(t *testing.T) {
	project, home, memDir := memoryFixture(t)
	hits := SearchAutoMemory("WIDGET", 10, project, home)
	if got := hitLabels(hits); got != "project/CLAUDE.md,user/CLAUDE.md,memory/a.md,memory/b.md" {
		t.Fatalf("labels = %s", got)
	}
	h := hits[2]
	if h.Title != "[auto-memory] memory/a.md" || h.Content != "Widget note A." {
		t.Fatalf("hit = %+v", h)
	}
	mtime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(memDir, "a.md"), mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if hits := SearchAutoMemory("widget", 10, project, home); !hits[2].Timestamp.Equal(mtime) {
		t.Fatalf("timestamp = %v", hits[2].Timestamp)
	}
	if got := hitLabels(SearchAutoMemory("widget", 2, project, home)); got != "project/CLAUDE.md,user/CLAUDE.md" {
		t.Fatalf("limit 2 = %s", got)
	}
	if hits := SearchAutoMemory("widget", 0, project, home); len(hits) != 0 {
		t.Fatalf("limit 0 = %v", hits)
	}
	// No project or user CLAUDE.md, no memory dir: nothing to search.
	if hits := SearchAutoMemory("widget", 5, filepath.Join(home, "none"), filepath.Join(home, "nohome")); len(hits) != 0 {
		t.Fatalf("empty = %v", hits)
	}
}

func TestSearchAutoMemoryMatching(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "p")
	home := filepath.Join(root, "h")
	md := filepath.Join(project, "CLAUDE.md")
	// Expected values from node running the AM:149-170 code on the same inputs.
	cases := []struct {
		name, content, query string
		want                 string // "" = no hit
	}{
		{"word boundary", "the cached value", "cache", ""},
		{"word boundary hit", "Cache, then serve", "cache", "Cache, then serve"},
		{"short terms ignored", "go is fun", "go", ""},
		{"any term matches", "only zeta here", "alpha zeta", "only zeta here"},
		{"regex metachars are literal", "use a+b now", "a+b", "use a+b now"},
		{"underscore is a word char", "my_cache_key", "cache", ""},
	}
	for _, c := range cases {
		writeFile(t, md, c.content)
		hits := SearchAutoMemory(c.query, 5, project, home)
		got := ""
		if len(hits) > 0 {
			got = hits[0].Content
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	// Files over 1,000,000 bytes are skipped.
	writeFile(t, md, "widget "+strings.Repeat("x", 1_000_000))
	if hits := SearchAutoMemory("widget", 5, project, home); len(hits) != 0 {
		t.Fatal("large file was searched")
	}
}

func TestAutoMemorySnippet(t *testing.T) {
	// Expected values from node running the AM:149-170 code on the same inputs.
	pad := strings.Repeat("x", 250)
	ys := strings.Repeat("y", 600)
	emoji := strings.Repeat("😀", 150)
	cases := []struct {
		name, content, query, want string
	}{
		// start = idx-200 snaps forward to just after the last "\n\n" at or before it.
		{"snap start", "intro\n\n" + pad + " needle here\n\nmore text\n\ntail", "needle",
			pad + " needle here\n\nmore text\n\ntail"},
		// end = idx+500 snaps to the next "\n\n" at or after it.
		{"snap end", "needle start\n\n" + ys + "\n\nlast", "needle", "needle start\n\n" + ys},
		// idx is the first substring hit of any term, even inside a longer word.
		{"substring index", strings.Repeat("z", 300) + " cached then cache", "cache",
			strings.Repeat("z", 199) + " cached then cache"},
		// Windows count UTF-16 units: start 101 cuts an emoji in half (U+FFFD, as V8 writes it).
		{"utf16 window", emoji + " needle", "needle", "�" + strings.Repeat("😀", 99) + " needle"},
	}
	for _, c := range cases {
		got, ok := autoMemorySnippet(c.content, autoMemoryTerms(c.query))
		if !ok || got != c.want {
			t.Errorf("%s: ok=%v\ngot  %q\nwant %q", c.name, ok, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/memory/ -run 'TestProjectSlug|TestSearchAutoMemory|TestAutoMemorySnippet' -v`
Expected: FAIL with "internal/memory/automemory_test.go:17:13: undefined: ProjectSlug"

- [ ] **Step 3: Write minimal implementation**

Unexported helpers carry an `autoMemory` prefix or a `U16` suffix because the directive builder shares package `memory`.

`internal/memory/automemory.go`:
```go
package memory

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/lang315/context-mode-go/internal/hookio"
)

// MemoryHit is one auto-memory match for ctx_search timeline mode.
type MemoryHit struct {
	Title, Content, Source string    // "[auto-memory] <label>", snippet, label
	Timestamp              time.Time // file mtime
}

// autoMemoryMaxBytes: files above this size are skipped (AM:139).
const autoMemoryMaxBytes = 1_000_000

// ProjectSlug is Claude Code's directory name for a project under
// ~/.claude/projects: the absolute dir with every "/" and "." replaced by "-".
func ProjectSlug(projectDir string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(projectDir)
}

// SearchAutoMemory ports searchAutoMemory (AM:57-188) for one query over
// <projectDir>/CLAUDE.md, <home>/.claude/CLAUDE.md and the *.md files of
// Claude Code's memory dir for the project, in that order, with at most one
// hit per file and at most limit hits. TS read <configDir>/memory/<hash>/,
// a dir Claude Code does not write (spec §3.3).
func SearchAutoMemory(query string, limit int, projectDir, home string) []MemoryHit {
	type candidate struct{ path, label string }
	cands := []candidate{{filepath.Join(projectDir, "CLAUDE.md"), "project/CLAUDE.md"}}
	if userDir := filepath.Join(home, ".claude"); userDir != projectDir { // AM:103, no duplicate label
		cands = append(cands, candidate{filepath.Join(userDir, "CLAUDE.md"), "user/CLAUDE.md"})
	}
	memDir := filepath.Join(home, ".claude", "projects", ProjectSlug(projectDir), "memory")
	entries, _ := os.ReadDir(memDir) // sorted by name; a missing dir has no entries
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			cands = append(cands, candidate{filepath.Join(memDir, e.Name()), "memory/" + e.Name()})
		}
	}

	terms := autoMemoryTerms(query)
	var hits []MemoryHit
	for _, c := range cands {
		if len(hits) >= limit {
			break
		}
		info, err := os.Stat(c.path)
		if err != nil || info.Size() > autoMemoryMaxBytes {
			continue
		}
		b, err := os.ReadFile(c.path)
		if err != nil {
			continue
		}
		if snippet, ok := autoMemorySnippet(string(b), terms); ok {
			hits = append(hits, MemoryHit{Title: "[auto-memory] " + c.label, Content: snippet, Source: c.label, Timestamp: info.ModTime()})
		}
	}
	return hits
}

// autoMemoryTerms is query.toLowerCase().split(/\s+/) keeping terms of at
// least 3 UTF-16 units (AM:150).
func autoMemoryTerms(query string) []string {
	var terms []string
	for _, t := range strings.FieldsFunc(strings.ToLower(query), autoMemorySpace) {
		if hookio.JSLen(t) >= 3 {
			terms = append(terms, t)
		}
	}
	return terms
}

// autoMemorySnippet reports whether any term occurs word-bounded
// (case-insensitive) in content and returns the AM:160-170 window: from 200
// units before the first substring hit of any term to 500 after it, snapped
// out to the surrounding blank lines, trimmed. Positions are UTF-16 units,
// as in JS.
func autoMemorySnippet(content string, terms []string) (string, bool) {
	// RE2's \b is ASCII-only, like JS \b without the u flag.
	if !slices.ContainsFunc(terms, func(t string) bool {
		return regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(t) + `\b`).MatchString(content)
	}) {
		return "", false
	}
	u := utf16.Encode([]rune(content))
	lower := utf16.Encode([]rune(strings.ToLower(content)))
	idx := -1
	for _, t := range terms {
		if i := indexU16(lower, utf16.Encode([]rune(t)), 0); i >= 0 && (idx < 0 || i < idx) {
			idx = i
		}
	}
	start, end := max(0, idx-200), min(len(u), idx+500)
	blank := []uint16{'\n', '\n'}
	if p := lastIndexU16(u, blank, start); p >= 0 {
		start = p + 2
	}
	if p := indexU16(u, blank, end); p >= 0 {
		end = p
	}
	if start >= end { // JS slice(start, end) is "" then
		return "", true
	}
	return strings.TrimFunc(string(utf16.Decode(u[start:end])), autoMemorySpace), true
}

// indexU16 is JS s.indexOf(sub, from) over UTF-16 units.
func indexU16(s, sub []uint16, from int) int {
	for i := max(from, 0); i+len(sub) <= len(s); i++ {
		if slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// lastIndexU16 is JS s.lastIndexOf(sub, from): the last match starting at or
// before from.
func lastIndexU16(s, sub []uint16, from int) int {
	for i := min(from, len(s)-len(sub)); i >= 0; i-- {
		if slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// autoMemorySpace is the JS `\s` / trim() whitespace set.
func autoMemorySpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/memory/ -run 'TestProjectSlug|TestSearchAutoMemory|TestAutoMemorySnippet' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/automemory.go internal/memory/automemory_test.go
git commit -m "feat(memory): search Claude Code auto-memory files"
```

### Task 9: ctx_search timeline merge and session-memory description

**Files:**
- Modify: `internal/mcpserver/search.go`
- Test: `internal/mcpserver/search_memory_test.go`

**Interfaces:**
- Consumes: `func (s *Store) SessionEventsForSearch(project, query, source string, limit int) ([]StoredEvent, error)` (Task 5); `func memory.SearchAutoMemory(query string, limit int, projectDir, home string) []memory.MemoryHit` (Task 8); `func (s *Store) SearchWithFallback(query string, o SearchOpts) ([]SearchResult, error)`; harness helpers `newHarness`, `(*harness).call`, `(*harness).index`, `throttle1`
- Produces: unexported `type searchHit struct`, `func searchHits(d *Deps, q string, sa searchArgs, opts store.SearchOpts) ([]searchHit, error)`; test helpers `func (h *harness) events(sessionID, project string, evs ...store.Event)` and `const stamp`

- [ ] **Step 1: Write the failing test**

`internal/mcpserver/search_memory_test.go`:
```go
package mcpserver

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/store"
)

// events inserts session events for project (the harness project when "").
func (h *harness) events(sessionID, project string, evs ...store.Event) {
	h.t.Helper()
	if project == "" {
		project = h.Deps.Cfg.ProjectKey
	}
	if _, err := h.Deps.Store.InsertEvents(sessionID, project, "PostToolUse", evs); err != nil {
		h.t.Fatal(err)
	}
}

// stamp matches the "YYYY-MM-DD HH:MM" header field.
const stamp = `\d{4}-\d\d-\d\d \d\d:\d\d`

func TestSearchTimelineSessionEvents(t *testing.T) {
	h := newHarness(t, nil)
	h.events("s1", "",
		store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "first cache decision"},
		store.Event{Type: "file_read", Category: "file", Priority: 1, Data: "cache.go"},
	)
	h.events("s2", "", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "latest cache decision"})
	h.events("s9", "/other/project", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "foreign cache decision"})

	// Newest 2 matches of the project (all sessions), shown oldest to newest.
	out, isErr := h.call("ctx_search", `{"queries": ["CACHE"], "sort": "timeline"}`)
	re := regexp.MustCompile(`\A## CACHE\n\n` +
		`--- \[prior-session \| ` + stamp + ` \| prior-session\] ---\n### \[file\] file_read\n\ncache\.go\n\n` +
		`--- \[prior-session \| ` + stamp + ` \| prior-session\] ---\n### \[decision\] decision\n\nlatest cache decision` +
		regexp.QuoteMeta(throttle1) + `\z`)
	if isErr || !re.MatchString(out) {
		t.Fatalf("isErr=%v\n%s", isErr, out)
	}
	at, err := time.Parse("2006-01-02 15:04", regexp.MustCompile(stamp).FindString(out))
	if err != nil || time.Since(at) > 5*time.Minute || time.Since(at) < -time.Minute {
		t.Fatalf("timestamp %v is not the UTC insert time (%v)", at, err)
	}

	// source must equal the category; every term must match.
	out, _ = h.call("ctx_search", `{"queries": ["cache decision", "nothing here"], "sort": "timeline", "source": "decision", "limit": 1}`)
	if !strings.HasPrefix(out, "## cache decision\n\n--- [prior-session | ") ||
		!strings.Contains(out, "### [decision] decision\n\nlatest cache decision\n\n---\n\n## nothing here\nNo results found.") ||
		strings.Contains(out, "first cache") || strings.Contains(out, "foreign") {
		t.Fatalf("source filter:\n%s", out)
	}

	// Relevance mode does not read session events.
	h.index("notes", store.Chunk{Title: "Other", Content: "unrelated text"})
	if out, _ := h.call("ctx_search", `{"queries": ["latest cache decision"]}`); strings.Contains(out, "prior-session") {
		t.Fatalf("relevance reached session events:\n%s", out)
	}
}

func TestSearchTimelineMergesSources(t *testing.T) {
	h := newHarness(t, nil)
	md := filepath.Join(h.Project, "CLAUDE.md")
	if err := os.WriteFile(md, []byte("Widget rules live here."), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 5, 6, 7, 8, 9, 0, time.UTC)
	if err := os.Chtimes(md, old, old); err != nil {
		t.Fatal(err)
	}
	// Auto-memory alone: origin auto-memory, mtime in UTC, label as source.
	out, _ := h.call("ctx_search", `{"queries": ["widget"], "sort": "timeline"}`)
	if want := "## widget\n\n--- [auto-memory | 2020-05-06 07:08 | project/CLAUDE.md] ---\n### [auto-memory] project/CLAUDE.md\n\nWidget rules live here." + throttle1; out != want {
		t.Fatalf("auto-memory:\n%s", out)
	}

	h.index("notes", store.Chunk{Title: "Widget doc", Content: "widget content"})
	h.events("s1", "", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "widget decision"})
	// Three sources match; the old memory file is dropped, content then event remain.
	out, _ = h.call("ctx_search", `{"queries": ["widget"], "sort": "timeline"}`)
	re := regexp.MustCompile(`\A## widget\n\n` +
		`--- \[current-session \| ` + stamp + ` \| notes\] ---\n### Widget doc\n\nwidget content\n\n` +
		`--- \[prior-session \| ` + stamp + ` \| prior-session\] ---\n### \[decision\] decision\n\nwidget decision\n\n> Throttle`)
	if !re.MatchString(out) {
		t.Fatalf("merge:\n%s", out)
	}

	// A newer memory file outranks the content chunk and is shown last.
	future := time.Now().Add(24 * time.Hour)
	if err := os.Chtimes(md, future, future); err != nil {
		t.Fatal(err)
	}
	out, _ = h.call("ctx_search", `{"queries": ["widget"], "sort": "timeline"}`)
	re = regexp.MustCompile(`\A## widget\n\n--- \[prior-session \| [^\n]*\n### \[decision\] decision\n\nwidget decision\n\n` +
		`--- \[auto-memory \| ` + regexp.QuoteMeta(future.UTC().Format("2006-01-02 15:04")) + ` \| project/CLAUDE.md\] ---\n`)
	if !re.MatchString(out) {
		t.Fatalf("future memory:\n%s", out)
	}

	// A source filter skips auto-memory and filters the content label.
	h2 := newHarness(t, nil)
	if err := os.WriteFile(filepath.Join(h2.Project, "CLAUDE.md"), []byte("widget"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _ := h2.call("ctx_search", `{"queries": ["widget"], "sort": "timeline", "source": "notes"}`); !strings.HasPrefix(out, "## widget\nNo results found.") {
		t.Fatalf("source filter reached auto-memory:\n%s", out)
	}
}

func TestSearchDescriptionMentionsSessionMemory(t *testing.T) {
	for _, s := range []string{
		"AND auto-captured session memory written by hooks",
		"(recently indexed content, prior session events, auto-memory)",
		"You want a chronological view across current session + prior sessions + persistent auto-memory (pass `sort: \"timeline\"`",
		"AND no session memory has accumulated around it",
		"Common session-memory source labels: `decision`",
		`EXAMPLE: ctx_search(queries: ["what did we decide about caching"], source: "decision", sort: "timeline")`,
		`EXAMPLE: ctx_search(queries: ["last user prompt", "active skills", "open blockers"], sort: "timeline")`,
	} {
		if !strings.Contains(searchDescription, s) {
			t.Errorf("description lacks %q", s)
		}
	}
	for _, s := range []string{"ctx_stats", "CONTEXT_MODE_", "ctx_index", "content hash", "oldest first"} {
		if strings.Contains(searchDescription, s) {
			t.Errorf("description still has %q", s)
		}
	}
	if !strings.Contains(searchSchema, "'timeline': chronological across current session, prior sessions, and auto-memory.") {
		t.Error("sort schema description not restored")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcpserver/ -run TestSearch -v`
Expected: FAIL with "search_memory_test.go:44: isErr=false" (the output is "## CACHE\nNo results found." plus the throttle line: timeline mode reads only the content store)

- [ ] **Step 3: Write minimal implementation**

`internal/mcpserver/search.go`:
```go
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/memory"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	searchSnippetMax = 1500      // S:2750
	searchOutputCap  = 40 * 1024 // S:2672
)

// searchDescription is S:2564-2585 minus the ctx_stats, CONTEXT_MODE_* env
// and file-staleness sentences, the ctx_index tool (not in cmgo) and the
// "26 event categories" count (cmgo captures fewer).
const searchDescription = "Search a unified knowledge base with a multi-strategy ranking pipeline. Two parallel matchers run on every query: a Porter-stemming matcher (\"caching\" finds \"cached\", \"caches\", \"cach\") and a trigram-substring matcher (\"useEff\" finds \"useEffect\"). Their ranked lists are merged via Reciprocal Rank Fusion, so a document that ranks well in both surfaces above one that wins only on a single strategy. Multi-term queries get an additional proximity-rerank pass that boosts passages where the query terms appear close together. Typos are corrected via Levenshtein distance and re-searched. Result snippets are window-extracted around the matched terms, not blindly truncated.\n" +
	"\n" +
	"The knowledge base is unified: queries reach indexed content you stored (ctx_fetch_and_index, ctx_batch_execute output, and large ctx_execute / ctx_execute_file output) AND auto-captured session memory written by hooks (decisions, errors, blockers, plans, user prompts, rejected approaches, tool failures, compaction guides).\n" +
	"\n" +
	"WHEN:\n" +
	"  - You want to recall something that exists in storage (recently indexed content, prior session events, auto-memory) instead of re-reading raw sources\n" +
	"  - You have multiple related questions about the same body of knowledge — batch every question into one call (the ranking pipeline runs per-query but the round-trip cost is paid once)\n" +
	"  - You want to scope the query to one labelled source (pass `source` — partial match is fine)\n" +
	"  - You want a chronological view across current session + prior sessions + persistent auto-memory (pass `sort: \"timeline\"` — the default `relevance` mode only ranks within the current session)\n" +
	"  - You want to filter ranked results by content shape (pass `contentType: \"code\"` to surface implementation snippets or `contentType: \"prose\"` to surface explanations)\n" +
	"\n" +
	"WHEN NOT:\n" +
	"  - The data you want to query has never been stored in the knowledge base AND no session memory has accumulated around it — capture first (run a gather-and-index call), then come back here to query\n" +
	"  - You have one ad-hoc question against data that is not in the knowledge base — answer it inline by running code in the sandbox tool; one round-trip instead of capture-then-query\n" +
	"\n" +
	"RETURNS:\n" +
	"  Per-query ranked sections with window-extracted snippets. Use 2-4 specific technical terms per query. Common session-memory source labels: `decision` (user corrections / preferences), `error` and `error-resolution` (past failures + their fixes), `blocker`, `plan`, `user-prompt`, `rejected-approach`, `compaction` (post-compact session guide). Each response carries a throttle counter (call #N/M in the rolling time window); results taper toward the soft cap and calls block after the hard cap.\n" +
	"\n" +
	"EXAMPLE: ctx_search(queries: [\"root cause\", \"proposed fix\", \"test coverage\"], source: \"issue-#683\")\n" +
	"EXAMPLE: ctx_search(queries: [\"what did we decide about caching\"], source: \"decision\", sort: \"timeline\")\n" +
	"EXAMPLE: ctx_search(queries: [\"useEffect cleanup pattern\"], source: \"react-docs\", contentType: \"code\", limit: 5)\n" +
	"EXAMPLE: ctx_search(queries: [\"last user prompt\", \"active skills\", \"open blockers\"], sort: \"timeline\")"

// searchSchema mirrors buildCtxSearchInputSchema (ctx-search-schema.ts:62-105)
// without the shared-DB `project` field.
const searchSchema = `{
  "type": "object",
  "properties": {
    "queries": {"type": ["array", "string"], "items": {"type": "string"}, "description": "Array of search queries. Batch ALL questions in one call."},
    "limit": {"type": ["number", "string"], "default": 3, "description": "Results per query (default: 3)"},
    "source": {"type": "string", "description": "Filter to a specific indexed source (partial match)."},
    "contentType": {"type": "string", "enum": ["code", "prose"], "description": "Filter results by content type: 'code' or 'prose'."},
    "sort": {"type": "string", "enum": ["relevance", "timeline"], "default": "relevance", "description": "Sort mode. 'relevance' (default): BM25 ranked, current session only. 'timeline': chronological across current session, prior sessions, and auto-memory."}
  }
}`

const emptyKBMessage = "Knowledge base is empty — no content has been indexed yet.\n\n" +
	"ctx_search is a follow-up tool that queries previously indexed content. " +
	"To gather and index content first, use:\n" +
	"  • ctx_batch_execute(commands, queries) — run commands, auto-index output, and search in one call\n" +
	"  • ctx_fetch_and_index(url) — fetch a URL, index it, then search with ctx_search\n" +
	"  • ctx_index(content, source) — manually index text content\n\n" +
	"After indexing, ctx_search becomes available for follow-up queries."

func registerSearch(s *mcp.Server, d *Deps) {
	guard := &floodGuard{} // one server per process, so this is process-wide
	s.AddTool(&mcp.Tool{
		Name:        "ctx_search",
		Title:       "Search Indexed Content",
		Description: searchDescription,
		InputSchema: json.RawMessage(searchSchema),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: new(bool), OpenWorldHint: new(bool)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return searchTool(d, guard, req.Params.Arguments), nil
	})
}

type searchArgs struct {
	queries     []string
	limit       float64
	source      string
	contentType store.ContentType
	sort        string
}

func parseSearchArgs(raw json.RawMessage) (searchArgs, error) {
	sa := searchArgs{limit: 3, sort: "relevance"}
	a, err := ParseArgs(raw)
	if err != nil {
		return sa, err
	}
	if sa.queries, _, err = a.Strings("queries"); err != nil {
		return sa, err
	}
	// No legacy `query`: TS reads it (S:2621) but its zod schema has no such
	// field, so the SDK strips it and TS answers "provide query or queries".
	if n, ok, err := a.Number("limit"); err != nil {
		return sa, err
	} else if ok {
		sa.limit = n
	}
	if sa.source, _, err = a.String("source"); err != nil {
		return sa, err
	}
	if ct, ok, err := a.String("contentType"); err != nil {
		return sa, err
	} else if ok {
		if ct != string(store.Code) && ct != string(store.Prose) {
			return sa, errors.New(`contentType: expected "code" or "prose"`)
		}
		sa.contentType = store.ContentType(ct)
	}
	if so, ok, err := a.String("sort"); err != nil {
		return sa, err
	} else if ok {
		if so != "relevance" && so != "timeline" {
			return sa, errors.New(`sort: expected "relevance" or "timeline"`)
		}
		sa.sort = so
	}
	return sa, nil
}

// searchTool is the ctx_search handler (S:2592-2805). Relevance mode reads
// the content store; timeline mode merges it with session events and
// auto-memory (searchTimeline).
func searchTool(d *Deps, guard *floodGuard, raw json.RawMessage) *mcp.CallToolResult {
	sa, err := parseSearchArgs(raw)
	if err != nil {
		return ArgError(err)
	}
	project := d.Cfg.ProjectKey
	if sa.sort != "timeline" {
		n, err := d.Store.ChunkCount(project)
		if err != nil {
			return ErrorResult("Search error: %v", err)
		}
		if n == 0 {
			return TextResult(emptyKBMessage, true)
		}
	}
	if len(sa.queries) == 0 {
		return TextResult("Error: provide query or queries.", true)
	}

	now := d.Now()
	flood := guard.record(now)
	if flood.blocked {
		return ErrorResult("BLOCKED: %d search calls in %ds. "+
			"You're flooding context. STOP making individual search calls. "+
			"Use ctx_batch_execute(commands, queries) for your next research step.",
			flood.count, int(math.Round(now.Sub(flood.windowStart).Seconds())))
	}
	effectiveLimit := 1
	if !flood.softCapped {
		effectiveLimit = int(math.Min(sa.limit, 2)) // JS slice(0, 1.5) keeps 1
	}

	opts := store.SearchOpts{Project: project, Source: sa.source, ContentType: sa.contentType, Limit: effectiveLimit, Mode: sa.sort}
	totalSize := 0
	var sections []string
	for _, q := range sa.queries {
		if totalSize > searchOutputCap {
			sections = append(sections, "## "+q+"\n(output cap reached)\n")
			continue
		}
		var hits []searchHit
		if effectiveLimit > 0 { // TS slice(0, <=0) is empty; the store would default to 3
			if hits, err = searchHits(d, q, sa, opts); err != nil {
				return ErrorResult("Search error: %v", err)
			}
		}
		if len(hits) == 0 {
			sections = append(sections, "## "+q+"\nNo results found.")
			continue
		}
		parts := make([]string, len(hits))
		for i, r := range hits {
			ts := ""
			if !r.at.IsZero() {
				ts = " | " + r.at.UTC().Format("2006-01-02 15:04")
			}
			parts[i] = fmt.Sprintf("--- [%s%s | %s] ---\n### %s\n\n%s",
				r.origin, ts, r.source, r.title, store.ExtractSnippet(r.content, q, searchSnippetMax, r.highlighted))
		}
		formatted := strings.Join(parts, "\n\n")
		sections = append(sections, "## "+q+"\n\n"+formatted)
		totalSize += jsLen(formatted)
	}
	output := strings.Join(sections, "\n\n---\n\n")

	if flood.count >= searchSoftCapAt {
		output += fmt.Sprintf("\n\n⚠ search call #%d/%d in this window. "+
			"Results limited to %d/query. %d call(s) remaining before block. "+
			`Batch queries: ctx_search(queries: ["q1","q2","q3"]) or use ctx_batch_execute.`,
			flood.count, searchBlockAfter, effectiveLimit, max(0, searchBlockAfter-flood.count))
	} else {
		output += fmt.Sprintf("\n\n> Throttle: call #%d/%d in this window. "+
			"%d call(s) before soft cap. "+
			"Prefer ctx_search(queries: [...]) array form for multi-query workloads — it counts as a single call.",
			flood.count, searchBlockAfter, max(0, searchSoftCapAt-flood.count))
	}
	// TS S:2785 falls back to "No results found. Indexed sources: …" when the
	// output is blank; the throttle line above makes that unreachable.
	return TextResult(output, false)
}

// searchHit is one formatted ctx_search result from any source (S:2739-2756).
type searchHit struct {
	origin, source, title, content, highlighted string
	at                                          time.Time
}

// searchHits runs one query. Relevance mode returns the content store's
// ranked results. Timeline mode (spec §3.2) takes the newest opts.Limit
// matches from the content store, the project's session events (all
// sessions) and, without a source filter, auto-memory; keeps the newest
// opts.Limit overall and returns them oldest first. TS kept the oldest.
func searchHits(d *Deps, q string, sa searchArgs, opts store.SearchOpts) ([]searchHit, error) {
	res, err := d.Store.SearchWithFallback(q, opts)
	if err != nil {
		return nil, err
	}
	var hits []searchHit
	for _, r := range res { // timeline mode returns these oldest first
		hits = append(hits, searchHit{"current-session", r.Source, r.Title, r.Content, r.Highlighted, r.IndexedAt})
	}
	if sa.sort != "timeline" {
		return hits, nil
	}
	evs, err := d.Store.SessionEventsForSearch(d.Cfg.ProjectKey, q, sa.source, opts.Limit)
	if err != nil {
		return nil, err
	}
	for _, e := range slices.Backward(evs) { // newest first from the store
		at, _ := time.Parse("2006-01-02 15:04:05", e.CreatedAt)
		// TS labels every session event prior-session, the current session's too (unified.ts:130).
		hits = append(hits, searchHit{"prior-session", "prior-session", "[" + e.Category + "] " + e.Type, e.Data, "", at})
	}
	if sa.source == "" {
		// Claude Code names its memory dir after the project dir as given, so ProjectDir, not ProjectKey.
		for _, m := range memory.SearchAutoMemory(q, opts.Limit, d.Cfg.ProjectDir, d.Cfg.Home) {
			hits = append(hits, searchHit{"auto-memory", m.Source, m.Title, m.Content, "", m.Timestamp})
		}
	}
	// Stable: equal times (second resolution) keep source order, then id order.
	slices.SortStableFunc(hits, func(a, b searchHit) int { return a.at.Compare(b.at) })
	return hits[max(0, len(hits)-opts.Limit):], nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcpserver/ -run TestSearch -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver/search.go internal/mcpserver/search_memory_test.go
git commit -m "feat(mcpserver): merge session events and auto-memory into timeline search"
```

### Task 10: ctx_purge tool

**Files:**
- Create: `internal/mcpserver/purge.go`
- Modify: `internal/mcpserver/server.go`
- Test: `internal/mcpserver/purge_test.go`
- Modify: `internal/mcpserver/tools_list_test.go`
- Modify: `cmd/cmgo/serve_test.go`

**Interfaces:**
- Consumes: `func (s *Store) PurgeSession(sessionID string) (rows int64, err error)`, `func (s *Store) PurgeProject(project string) (sources, sessions int64, err error)` (Task 7); `func ParseArgs(raw json.RawMessage) (Args, error)` with `Bool`/`String`; `TextResult`, `ErrorResult`, `ArgError`; harness helpers incl. `(*harness).events` (Task 9) and `emptyKBMessage`
- Produces: unexported `func registerPurge(s *mcp.Server, d *Deps)`, `func purgeTool(d *Deps, raw json.RawMessage) *mcp.CallToolResult`, `const purgeDescription`, `const purgeSchema`; `New` registers six tools

- [ ] **Step 1: Write the failing test**

`internal/mcpserver/purge_test.go`:
```go
package mcpserver

import (
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

const (
	purgeCancelled = "Purge cancelled. Pass confirm: true to proceed."
	purgeAmbiguous = "Ambiguous purge: sessionId implies scope:'session', cannot combine with scope:'project'. " +
		"Use scope:'project' WITHOUT sessionId for the legacy whole-project wipe."
	purgeProjectMsg = "Purged: knowledge base (FTS5), session events. All session data for this project has been permanently deleted."
)

func TestPurgeArgumentChecks(t *testing.T) {
	h := newHarness(t, nil)
	h.events("s1", "", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "kept"})
	cases := []struct {
		args, want string
		isErr      bool
	}{
		{`{}`, purgeCancelled, false},
		{`{"confirm": false}`, purgeCancelled, false},
		{`{"confirm": "false", "sessionId": "s1"}`, purgeCancelled, false},
		// The ambiguity check runs before the confirm check (S:4510).
		{`{"confirm": false, "sessionId": "s1", "scope": "project"}`, purgeAmbiguous, true},
		{`{"confirm": true, "scope": "session"}`, "purgeSession: scope:'session' requires sessionId. Pass scope:'project' for the legacy whole-project wipe.", true},
		{`{"confirm": "yes"}`, "Invalid arguments: confirm: expected boolean", true},
		{`{"confirm": true, "scope": "all"}`, `Invalid arguments: scope: expected "session" or "project"`, true},
	}
	for _, c := range cases {
		if out, isErr := h.call("ctx_purge", c.args); out != c.want || isErr != c.isErr {
			t.Errorf("%s: isErr=%v %q", c.args, isErr, out)
		}
	}
	if evs, _ := h.Deps.Store.SessionEvents("s1"); len(evs) != 1 {
		t.Fatalf("events were deleted: %d left", len(evs))
	}
}

func TestPurgeSessionRoundTrip(t *testing.T) {
	h := newHarness(t, nil)
	h.events("s1", "", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "gizmo one"})
	h.events("s2", "", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "gizmo two"})
	h.index("notes", store.Chunk{Title: "Alpha", Content: "gizmo text"})

	if out, isErr := h.call("ctx_purge", `{"confirm": true, "sessionId": "s1"}`); isErr ||
		out != "Purged session s1: session events. Other sessions and project-wide stats preserved." {
		t.Fatalf("isErr=%v %q", isErr, out)
	}
	if out, _ := h.call("ctx_purge", `{"confirm": "true", "scope": "session", "sessionId": "s1"}`); out != "Purged session s1: no matching rows. Other sessions and project-wide stats preserved." {
		t.Fatalf("second purge: %q", out)
	}
	out, _ := h.call("ctx_search", `{"queries": ["gizmo"], "sort": "timeline"}`)
	if strings.Contains(out, "gizmo one") || !strings.Contains(out, "gizmo two") || !strings.Contains(out, "gizmo text") {
		t.Fatalf("after session purge:\n%s", out)
	}
}

func TestPurgeProjectRoundTrip(t *testing.T) {
	for _, args := range []string{`{"confirm": true}`, `{"confirm": true, "scope": "project"}`} {
		h := newHarness(t, nil)
		other := "/other/project"
		h.index("notes", store.Chunk{Title: "Alpha", Content: "gizmo text"})
		h.events("s1", "", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "gizmo one"})
		if _, err := h.Deps.Store.Index(other, "notes", []store.Chunk{{Title: "B", Content: "gizmo elsewhere"}}); err != nil {
			t.Fatal(err)
		}
		h.events("s9", other, store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "gizmo foreign"})

		if out, isErr := h.call("ctx_purge", args); isErr || out != purgeProjectMsg {
			t.Fatalf("%s: isErr=%v %q", args, isErr, out)
		}
		if out, isErr := h.call("ctx_search", `{"queries": ["gizmo"]}`); !isErr || out != emptyKBMessage {
			t.Fatalf("%s: KB not empty: %q", args, out)
		}
		if out, _ := h.call("ctx_search", `{"queries": ["gizmo"], "sort": "timeline"}`); !strings.HasPrefix(out, "## gizmo\nNo results found.") {
			t.Fatalf("%s: timeline after purge:\n%s", args, out)
		}
		if n, _ := h.Deps.Store.ChunkCount(other); n != 1 {
			t.Fatalf("%s: other project chunks = %d", args, n)
		}
		if evs, _ := h.Deps.Store.SessionEvents("s9"); len(evs) != 1 {
			t.Fatalf("%s: other project events = %d", args, len(evs))
		}
	}
}
```

`internal/mcpserver/tools_list_test.go`:
```go
package mcpserver

import (
	"context"
	"sort"
	"strings"
	"testing"
)

func TestServerRegistersSixTools(t *testing.T) {
	h := newHarness(t, nil)
	res, err := h.Session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := []string{"ctx_batch_execute", "ctx_execute", "ctx_execute_file", "ctx_fetch_and_index", "ctx_purge", "ctx_search"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v, want %v", names, want)
	}
}
```

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

func TestServeListsSixToolsAndExitsOnEOF(t *testing.T) {
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
	want := "ctx_batch_execute,ctx_execute,ctx_execute_file,ctx_fetch_and_index,ctx_purge,ctx_search"
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

// ctx_execute checks deny rules itself; the global ones live in
// $CLAUDE_CONFIG_DIR/settings.json when that is set.
func TestServeDeniesFromClaudeConfigDir(t *testing.T) {
	env := testEnv(t)
	cfg := filepath.Join(t.TempDir(), "claudecfg")
	os.MkdirAll(cfg, 0o755)
	os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(`{"permissions":{"deny":["Bash(echo cmgodenied*)"]}}`), 0o644)
	env["CLAUDE_CONFIG_DIR"] = cfg
	p := startServe(t, env)
	p.initialize()
	p.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "ctx_execute", "arguments": map[string]any{"language": "shell", "code": "echo cmgodenied"},
	}})
	res := p.recv(2)["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if res["isError"] != true || !strings.Contains(text, "deny") {
		t.Fatalf("not denied: %v", res)
	}
	p.in.Close()
	<-p.done
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcpserver/ ./cmd/cmgo/ -run 'TestPurge|TestServerRegistersSixTools|TestServeListsSixTools' -v`
Expected: FAIL with "purge_test.go:34: CallTool ctx_purge: calling \"tools/call\": unknown tool \"ctx_purge\""

- [ ] **Step 3: Write minimal implementation**

`internal/mcpserver/purge.go`:
```go
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// purgeDescription is S:4457-4481 minus the ctx_stats, stats-file, events
// markdown and per-session FTS5 chunk wording (cmgo has one DB and chunks
// carry no session) and the stderr deprecation warning.
const purgeDescription = `DESTRUCTIVE: permanently delete indexed content. Cannot be undone. Requires confirm:true and exactly one scope.

WHEN:
  - User explicitly asks to clear a specific session ('purge this session', 'wipe this conversation')
  - User explicitly asks to reset the whole project ('reset everything', 'wipe the knowledge base')

WHEN NOT:
  - User says 'reset', 'clear', or 'wipe' without naming a scope -> ask which scope before calling
  - User wants to free memory or improve performance -> do not purge

SCOPES (pass exactly one):
  - Per-session: ctx_purge(confirm: true, sessionId: "<uuid>") deletes that session's events (auto-captured decisions, errors, plans, user prompts, rejected approaches, etc.); sibling sessions are preserved.
  - Per-project: ctx_purge(confirm: true, scope: "project") wipes the FTS5 knowledge base and every session event of this project.

CONTRACT:
  - confirm:true is required; confirm:false returns 'purge cancelled'.
  - sessionId and scope:'project' together return 'ambiguous - pick one'.
  - scope:'session' without sessionId throws (sessionId required).
  - Bare {confirm:true} is deprecated: maps to scope:'project'.

RETURNS:
  A summary of removed rows + the resolved scope.

EXAMPLE: ctx_purge(confirm: true, sessionId: "7c8a-1234-5678-9abc-def012345678")
EXAMPLE: ctx_purge(confirm: true, scope: "project")`

// purgeSchema mirrors the TS zod object (S:4485-4503) minus the stats wording.
// confirm is not required: spec §4 answers a missing confirm with the
// cancel message instead of a schema error.
const purgeSchema = `{
  "type": "object",
  "properties": {
    "confirm": {"type": ["boolean", "string"], "description": "MUST be true. Destructive operation; false returns 'purge cancelled'."},
    "sessionId": {"type": "string", "description": "UUID of a single session. Pairs with confirm:true to wipe only that session's events. Sibling sessions are preserved. MUST NOT be combined with scope:'project'."},
    "scope": {"type": "string", "enum": ["session", "project"], "description": "Explicit scope selector. 'session' REQUIRES sessionId. 'project' wipes the entire project (FTS5 + every session). Omit only for the deprecated bare-{confirm:true} back-compat path."}
  }
}`

// registerPurge registers ctx_purge. Port of S:4445-4611 over cmgo's single DB.
func registerPurge(s *mcp.Server, d *Deps) {
	s.AddTool(&mcp.Tool{
		Name:        "ctx_purge",
		Title:       "Purge Knowledge Base",
		Description: purgeDescription,
		InputSchema: json.RawMessage(purgeSchema),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: new(bool)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return purgeTool(d, req.Params.Arguments), nil
	})
}

func purgeTool(d *Deps, raw json.RawMessage) *mcp.CallToolResult {
	a, err := ParseArgs(raw)
	if err != nil {
		return ArgError(err)
	}
	confirm, _, err := a.Bool("confirm")
	if err != nil {
		return ArgError(err)
	}
	sessionID, _, err := a.String("sessionId")
	if err != nil {
		return ArgError(err)
	}
	scope, _, err := a.String("scope")
	if err != nil {
		return ArgError(err)
	}
	if scope != "" && scope != "session" && scope != "project" {
		return ArgError(errors.New(`scope: expected "session" or "project"`))
	}
	if sessionID != "" && scope == "project" {
		return TextResult("Ambiguous purge: sessionId implies scope:'session', cannot combine with scope:'project'. "+
			"Use scope:'project' WITHOUT sessionId for the legacy whole-project wipe.", true)
	}
	if !confirm {
		return TextResult("Purge cancelled. Pass confirm: true to proceed.", false)
	}
	if scope == "" && sessionID != "" {
		scope = "session"
	}
	switch scope {
	case "session":
		if sessionID == "" { // purge.ts:189 throws
			return TextResult("purgeSession: scope:'session' requires sessionId. Pass scope:'project' for the legacy whole-project wipe.", true)
		}
		rows, err := d.Store.PurgeSession(sessionID)
		if err != nil {
			return ErrorResult("Purge error: %v", err)
		}
		what := "no matching rows"
		if rows > 0 {
			what = "session events"
		}
		return TextResult("Purged session "+sessionID+": "+what+". Other sessions and project-wide stats preserved.", false)
	default: // "project", or bare {confirm:true}
		if _, _, err := d.Store.PurgeProject(d.Cfg.ProjectKey); err != nil {
			return ErrorResult("Purge error: %v", err)
		}
		return TextResult("Purged: knowledge base (FTS5), session events. All session data for this project has been permanently deleted.", false)
	}
}
```

`internal/mcpserver/server.go`:
```go
// Package mcpserver registers cmgo's MCP tools and formats their output.
package mcpserver

import (
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/lang315/context-mode-go/internal/fetch"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Deps are the long-lived services every tool handler uses.
type Deps struct {
	Cfg    config.Config
	Store  *store.Store
	Exec   *executor.Executor
	Policy *security.Policy
	Fetch  *fetch.Fetcher
	Now    func() time.Time // injectable clock; defaults to time.Now
}

// New builds the MCP server with all six tools registered.
func New(d *Deps, version string) *mcp.Server {
	if d.Now == nil {
		d.Now = time.Now
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "cmgo", Version: version}, nil)
	registerExecute(s, d)
	registerExecuteFile(s, d)
	registerBatch(s, d)
	registerSearch(s, d)
	registerFetchAndIndex(s, d)
	registerPurge(s, d)
	return s
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcpserver/ ./cmd/cmgo/ -run 'TestPurge|TestServerRegistersSixTools|TestServeListsSixTools' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver/purge.go internal/mcpserver/server.go internal/mcpserver/purge_test.go internal/mcpserver/tools_list_test.go cmd/cmgo/serve_test.go
git commit -m "feat(mcpserver): add ctx_purge"
```

---

### Task 11: Auto-memory reads the Claude config dir

**Files:**
- Modify: `internal/memory/automemory.go`
- Modify: `internal/mcpserver/search.go`
- Test: `internal/memory/automemory_test.go`
- Test: `internal/mcpserver/harness_test.go`
- Test: `internal/mcpserver/search_memory_test.go`

**Interfaces:**
- Consumes: `config.Config.ClaudeDir` (internal/config/config.go); `func ProjectSlug(projectDir string) string` (internal/memory/automemory.go)
- Produces: `func SearchAutoMemory(query string, limit int, projectDir, claudeDir string) []MemoryHit` (the last parameter was `home`)

- [ ] **Step 1: Write the failing test**

The fixture now builds a config dir that is not under any home, and the duplicate guard gets a case. The mcpserver harness sets `ClaudeDir`, and a new search test points `ClaudeDir` away from `<home>/.claude`.

`internal/memory/automemory_test.go`:
```go
package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectSlug(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/lang/x.y": "-Users-lang-x-y",
		"/Users/lang/GolandProjects/github.com/lang315/context-mode": "-Users-lang-GolandProjects-github-com-lang315-context-mode",
		"/tmp/My Project": "-tmp-My Project",
	} {
		if got := ProjectSlug(in); got != want {
			t.Errorf("ProjectSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// memoryFixture returns a project dir and Claude config dir (what
// $CLAUDE_CONFIG_DIR names, else ~/.claude) with the three candidate kinds.
func memoryFixture(t *testing.T) (project, claudeDir, memDir string) {
	root := t.TempDir()
	project = filepath.Join(root, "work", "my.app")
	claudeDir = filepath.Join(root, "claude-config")
	memDir = filepath.Join(claudeDir, "projects", ProjectSlug(project), "memory")
	writeFile(t, filepath.Join(project, "CLAUDE.md"), "Project rules: run the widget tests.")
	writeFile(t, filepath.Join(claudeDir, "CLAUDE.md"), "User rules: prefer widget names.")
	writeFile(t, filepath.Join(memDir, "b.md"), "Widget note B.")
	writeFile(t, filepath.Join(memDir, "a.md"), "Widget note A.")
	writeFile(t, filepath.Join(memDir, "c.txt"), "widget ignored: not markdown")
	// Another project's memory dir is never read.
	writeFile(t, filepath.Join(claudeDir, "projects", "-other", "memory", "x.md"), "widget elsewhere")
	return project, claudeDir, memDir
}

func hitLabels(hits []MemoryHit) string {
	var out []string
	for _, h := range hits {
		out = append(out, h.Source)
	}
	return strings.Join(out, ",")
}

func TestSearchAutoMemoryCandidates(t *testing.T) {
	project, claudeDir, memDir := memoryFixture(t)
	hits := SearchAutoMemory("WIDGET", 10, project, claudeDir)
	if got := hitLabels(hits); got != "project/CLAUDE.md,user/CLAUDE.md,memory/a.md,memory/b.md" {
		t.Fatalf("labels = %s", got)
	}
	h := hits[2]
	if h.Title != "[auto-memory] memory/a.md" || h.Content != "Widget note A." {
		t.Fatalf("hit = %+v", h)
	}
	mtime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(memDir, "a.md"), mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if hits := SearchAutoMemory("widget", 10, project, claudeDir); !hits[2].Timestamp.Equal(mtime) {
		t.Fatalf("timestamp = %v", hits[2].Timestamp)
	}
	if got := hitLabels(SearchAutoMemory("widget", 2, project, claudeDir)); got != "project/CLAUDE.md,user/CLAUDE.md" {
		t.Fatalf("limit 2 = %s", got)
	}
	if hits := SearchAutoMemory("widget", 0, project, claudeDir); len(hits) != 0 {
		t.Fatalf("limit 0 = %v", hits)
	}
	// No project or user CLAUDE.md, no memory dir: nothing to search.
	if hits := SearchAutoMemory("widget", 5, filepath.Join(claudeDir, "none"), filepath.Join(claudeDir, "noconfig")); len(hits) != 0 {
		t.Fatalf("empty = %v", hits)
	}
	// A project that is the config dir itself reads its CLAUDE.md once (AM:103).
	if got := hitLabels(SearchAutoMemory("widget", 5, claudeDir, claudeDir)); got != "project/CLAUDE.md" {
		t.Fatalf("project = config dir: %s", got)
	}
}

func TestSearchAutoMemoryMatching(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "p")
	claudeDir := filepath.Join(root, "c")
	md := filepath.Join(project, "CLAUDE.md")
	// Expected values from node running the AM:149-170 code on the same inputs.
	cases := []struct {
		name, content, query string
		want                 string // "" = no hit
	}{
		{"word boundary", "the cached value", "cache", ""},
		{"word boundary hit", "Cache, then serve", "cache", "Cache, then serve"},
		{"short terms ignored", "go is fun", "go", ""},
		{"any term matches", "only zeta here", "alpha zeta", "only zeta here"},
		{"regex metachars are literal", "use a+b now", "a+b", "use a+b now"},
		{"underscore is a word char", "my_cache_key", "cache", ""},
	}
	for _, c := range cases {
		writeFile(t, md, c.content)
		hits := SearchAutoMemory(c.query, 5, project, claudeDir)
		got := ""
		if len(hits) > 0 {
			got = hits[0].Content
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	// Files over 1,000,000 bytes are skipped.
	writeFile(t, md, "widget "+strings.Repeat("x", 1_000_000))
	if hits := SearchAutoMemory("widget", 5, project, claudeDir); len(hits) != 0 {
		t.Fatal("large file was searched")
	}
}

func TestAutoMemorySnippet(t *testing.T) {
	// Expected values from node running the AM:149-170 code on the same inputs.
	pad := strings.Repeat("x", 250)
	ys := strings.Repeat("y", 600)
	emoji := strings.Repeat("😀", 150)
	cases := []struct {
		name, content, query, want string
	}{
		// start = idx-200 snaps forward to just after the last "\n\n" at or before it.
		{"snap start", "intro\n\n" + pad + " needle here\n\nmore text\n\ntail", "needle",
			pad + " needle here\n\nmore text\n\ntail"},
		// end = idx+500 snaps to the next "\n\n" at or after it.
		{"snap end", "needle start\n\n" + ys + "\n\nlast", "needle", "needle start\n\n" + ys},
		// idx is the first substring hit of any term, even inside a longer word.
		{"substring index", strings.Repeat("z", 300) + " cached then cache", "cache",
			strings.Repeat("z", 199) + " cached then cache"},
		// Windows count UTF-16 units: start 101 cuts an emoji in half (U+FFFD, as V8 writes it).
		{"utf16 window", emoji + " needle", "needle", "�" + strings.Repeat("😀", 99) + " needle"},
	}
	for _, c := range cases {
		got, ok := autoMemorySnippet(c.content, autoMemoryTerms(c.query))
		if !ok || got != c.want {
			t.Errorf("%s: ok=%v\ngot  %q\nwant %q", c.name, ok, got, c.want)
		}
	}
}
```

`internal/mcpserver/harness_test.go`:
```go
package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/lang315/context-mode-go/internal/fetch"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// harness is a connected server+client pair over in-memory transports,
// backed by a temp project dir, temp home and temp DB.
type harness struct {
	t       *testing.T
	Deps    *Deps
	Session *mcp.ClientSession
	Project string
	Home    string
}

// newHarness builds Deps rooted in t.TempDir(). now may be nil (time.Now).
func newHarness(t *testing.T, now func() time.Time) *harness {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	home := filepath.Join(root, "home")
	for _, d := range []string{project, home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{
		DataDir:    filepath.Join(root, "data"),
		DBPath:     filepath.Join(root, "data", "cmgo.db"),
		ProjectDir: project,
		ProjectKey: config.NormalizeProject(project),
		Home:       home,
		ClaudeDir:  filepath.Join(home, ".claude"),
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	ex := executor.New(project)
	t.Cleanup(func() { ex.Shutdown(); st.Close() })
	d := &Deps{Cfg: cfg, Store: st, Exec: ex, Policy: security.NewPolicy(project, home), Fetch: fetch.New(false), Now: now}

	srv := New(d, "test")
	ct, stt := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.Connect(ctx, stt, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close(); ss.Close() })
	return &harness{t: t, Deps: d, Session: cs, Project: project, Home: home}
}

// call invokes a tool with arguments given as a JSON object literal and
// returns the concatenated text content and the IsError flag.
func (h *harness) call(name, argsJSON string) (string, bool) {
	h.t.Helper()
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		h.t.Fatalf("bad test args %s: %v", argsJSON, err)
	}
	res, err := h.Session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		h.t.Fatalf("CallTool %s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

// writeSettings writes <project>/.claude/settings.json with the given deny rules.
func (h *harness) writeSettings(deny ...string) {
	h.t.Helper()
	dir := filepath.Join(h.Project, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"permissions": map[string]any{"deny": deny}})
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), body, 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func TestHarnessConnects(t *testing.T) {
	h := newHarness(t, nil)
	if _, err := h.Session.ListTools(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}
```

`internal/mcpserver/search_memory_test.go`:
```go
package mcpserver

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/memory"
	"github.com/lang315/context-mode-go/internal/store"
)

// events inserts session events for project (the harness project when "").
func (h *harness) events(sessionID, project string, evs ...store.Event) {
	h.t.Helper()
	if project == "" {
		project = h.Deps.Cfg.ProjectKey
	}
	if _, err := h.Deps.Store.InsertEvents(sessionID, project, "PostToolUse", evs); err != nil {
		h.t.Fatal(err)
	}
}

// stamp matches the "YYYY-MM-DD HH:MM" header field.
const stamp = `\d{4}-\d\d-\d\d \d\d:\d\d`

func TestSearchTimelineSessionEvents(t *testing.T) {
	h := newHarness(t, nil)
	h.events("s1", "",
		store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "first cache decision"},
		store.Event{Type: "file_read", Category: "file", Priority: 1, Data: "cache.go"},
	)
	h.events("s2", "", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "latest cache decision"})
	h.events("s9", "/other/project", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "foreign cache decision"})

	// Newest 2 matches of the project (all sessions), shown oldest to newest.
	out, isErr := h.call("ctx_search", `{"queries": ["CACHE"], "sort": "timeline"}`)
	re := regexp.MustCompile(`\A## CACHE\n\n` +
		`--- \[prior-session \| ` + stamp + ` \| prior-session\] ---\n### \[file\] file_read\n\ncache\.go\n\n` +
		`--- \[prior-session \| ` + stamp + ` \| prior-session\] ---\n### \[decision\] decision\n\nlatest cache decision` +
		regexp.QuoteMeta(throttle1) + `\z`)
	if isErr || !re.MatchString(out) {
		t.Fatalf("isErr=%v\n%s", isErr, out)
	}
	at, err := time.Parse("2006-01-02 15:04", regexp.MustCompile(stamp).FindString(out))
	if err != nil || time.Since(at) > 5*time.Minute || time.Since(at) < -time.Minute {
		t.Fatalf("timestamp %v is not the UTC insert time (%v)", at, err)
	}

	// source must equal the category; every term must match.
	out, _ = h.call("ctx_search", `{"queries": ["cache decision", "nothing here"], "sort": "timeline", "source": "decision", "limit": 1}`)
	if !strings.HasPrefix(out, "## cache decision\n\n--- [prior-session | ") ||
		!strings.Contains(out, "### [decision] decision\n\nlatest cache decision\n\n---\n\n## nothing here\nNo results found.") ||
		strings.Contains(out, "first cache") || strings.Contains(out, "foreign") {
		t.Fatalf("source filter:\n%s", out)
	}

	// Relevance mode does not read session events.
	h.index("notes", store.Chunk{Title: "Other", Content: "unrelated text"})
	if out, _ := h.call("ctx_search", `{"queries": ["latest cache decision"]}`); strings.Contains(out, "prior-session") {
		t.Fatalf("relevance reached session events:\n%s", out)
	}
}

func TestSearchTimelineMergesSources(t *testing.T) {
	h := newHarness(t, nil)
	md := filepath.Join(h.Project, "CLAUDE.md")
	if err := os.WriteFile(md, []byte("Widget rules live here."), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 5, 6, 7, 8, 9, 0, time.UTC)
	if err := os.Chtimes(md, old, old); err != nil {
		t.Fatal(err)
	}
	// Auto-memory alone: origin auto-memory, mtime in UTC, label as source.
	out, _ := h.call("ctx_search", `{"queries": ["widget"], "sort": "timeline"}`)
	if want := "## widget\n\n--- [auto-memory | 2020-05-06 07:08 | project/CLAUDE.md] ---\n### [auto-memory] project/CLAUDE.md\n\nWidget rules live here." + throttle1; out != want {
		t.Fatalf("auto-memory:\n%s", out)
	}

	h.index("notes", store.Chunk{Title: "Widget doc", Content: "widget content"})
	h.events("s1", "", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "widget decision"})
	// Three sources match; the old memory file is dropped, content then event remain.
	out, _ = h.call("ctx_search", `{"queries": ["widget"], "sort": "timeline"}`)
	re := regexp.MustCompile(`\A## widget\n\n` +
		`--- \[current-session \| ` + stamp + ` \| notes\] ---\n### Widget doc\n\nwidget content\n\n` +
		`--- \[prior-session \| ` + stamp + ` \| prior-session\] ---\n### \[decision\] decision\n\nwidget decision\n\n> Throttle`)
	if !re.MatchString(out) {
		t.Fatalf("merge:\n%s", out)
	}

	// A newer memory file outranks the content chunk and is shown last.
	future := time.Now().Add(24 * time.Hour)
	if err := os.Chtimes(md, future, future); err != nil {
		t.Fatal(err)
	}
	out, _ = h.call("ctx_search", `{"queries": ["widget"], "sort": "timeline"}`)
	re = regexp.MustCompile(`\A## widget\n\n--- \[prior-session \| [^\n]*\n### \[decision\] decision\n\nwidget decision\n\n` +
		`--- \[auto-memory \| ` + regexp.QuoteMeta(future.UTC().Format("2006-01-02 15:04")) + ` \| project/CLAUDE.md\] ---\n`)
	if !re.MatchString(out) {
		t.Fatalf("future memory:\n%s", out)
	}

	// A source filter skips auto-memory and filters the content label.
	h2 := newHarness(t, nil)
	if err := os.WriteFile(filepath.Join(h2.Project, "CLAUDE.md"), []byte("widget"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _ := h2.call("ctx_search", `{"queries": ["widget"], "sort": "timeline", "source": "notes"}`); !strings.HasPrefix(out, "## widget\nNo results found.") {
		t.Fatalf("source filter reached auto-memory:\n%s", out)
	}
}

// Auto-memory reads Claude Code's config dir, cfg.ClaudeDir ($CLAUDE_CONFIG_DIR
// when set), not <home>/.claude (spec §3.3).
func TestSearchTimelineReadsClaudeDir(t *testing.T) {
	h := newHarness(t, nil)
	h.Deps.Cfg.ClaudeDir = filepath.Join(t.TempDir(), "claude-config")
	write := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(h.Deps.Cfg.ClaudeDir, "projects", memory.ProjectSlug(h.Project), "memory", "notes.md"), "Gadget notes.")
	write(filepath.Join(h.Home, ".claude", "CLAUDE.md"), "gadget rules in home")
	out, _ := h.call("ctx_search", `{"queries": ["gadget"], "sort": "timeline"}`)
	if !strings.Contains(out, "### [auto-memory] memory/notes.md\n\nGadget notes.") || strings.Contains(out, "user/CLAUDE.md") {
		t.Fatalf("auto-memory:\n%s", out)
	}
}

func TestSearchDescriptionMentionsSessionMemory(t *testing.T) {
	for _, s := range []string{
		"AND auto-captured session memory written by hooks",
		"(recently indexed content, prior session events, auto-memory)",
		"You want a chronological view across current session + prior sessions + persistent auto-memory (pass `sort: \"timeline\"`",
		"AND no session memory has accumulated around it",
		"Common session-memory source labels: `decision`",
		`EXAMPLE: ctx_search(queries: ["what did we decide about caching"], source: "decision", sort: "timeline")`,
		`EXAMPLE: ctx_search(queries: ["last user prompt", "active skills", "open blockers"], sort: "timeline")`,
	} {
		if !strings.Contains(searchDescription, s) {
			t.Errorf("description lacks %q", s)
		}
	}
	for _, s := range []string{"ctx_stats", "CONTEXT_MODE_", "ctx_index", "content hash", "oldest first"} {
		if strings.Contains(searchDescription, s) {
			t.Errorf("description still has %q", s)
		}
	}
	if !strings.Contains(searchSchema, "'timeline': chronological across current session, prior sessions, and auto-memory.") {
		t.Error("sort schema description not restored")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/memory/ -run AutoMemory -count=1`
Expected: FAIL with `automemory_test.go:62: labels = project/CLAUDE.md`

Run: `go test ./internal/mcpserver/ -run TestSearchTimelineReadsClaudeDir -count=1`
Expected: FAIL with `--- FAIL: TestSearchTimelineReadsClaudeDir` (`search_memory_test.go:133: auto-memory:`)

- [ ] **Step 3: Write minimal implementation**

`internal/memory/automemory.go`:
```go
package memory

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/lang315/context-mode-go/internal/hookio"
)

// MemoryHit is one auto-memory match for ctx_search timeline mode.
type MemoryHit struct {
	Title, Content, Source string    // "[auto-memory] <label>", snippet, label
	Timestamp              time.Time // file mtime
}

// autoMemoryMaxBytes: files above this size are skipped (AM:139).
const autoMemoryMaxBytes = 1_000_000

// ProjectSlug is Claude Code's directory name for a project under
// <claude dir>/projects: the absolute dir with every "/" and "." replaced by "-".
func ProjectSlug(projectDir string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(projectDir)
}

// SearchAutoMemory ports searchAutoMemory (AM:57-188) for one query over
// <projectDir>/CLAUDE.md, <claudeDir>/CLAUDE.md and the *.md files of
// Claude Code's memory dir for the project, in that order, with at most one
// hit per file and at most limit hits. claudeDir is config.Config.ClaudeDir
// ($CLAUDE_CONFIG_DIR, else ~/.claude). TS read <configDir>/memory/<hash>/,
// a dir Claude Code does not write (spec §3.3).
func SearchAutoMemory(query string, limit int, projectDir, claudeDir string) []MemoryHit {
	type candidate struct{ path, label string }
	cands := []candidate{{filepath.Join(projectDir, "CLAUDE.md"), "project/CLAUDE.md"}}
	if claudeDir != projectDir { // AM:103, no duplicate label
		cands = append(cands, candidate{filepath.Join(claudeDir, "CLAUDE.md"), "user/CLAUDE.md"})
	}
	memDir := filepath.Join(claudeDir, "projects", ProjectSlug(projectDir), "memory")
	entries, _ := os.ReadDir(memDir) // sorted by name; a missing dir has no entries
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			cands = append(cands, candidate{filepath.Join(memDir, e.Name()), "memory/" + e.Name()})
		}
	}

	terms := autoMemoryTerms(query)
	var hits []MemoryHit
	for _, c := range cands {
		if len(hits) >= limit {
			break
		}
		info, err := os.Stat(c.path)
		if err != nil || info.Size() > autoMemoryMaxBytes {
			continue
		}
		b, err := os.ReadFile(c.path)
		if err != nil {
			continue
		}
		if snippet, ok := autoMemorySnippet(string(b), terms); ok {
			hits = append(hits, MemoryHit{Title: "[auto-memory] " + c.label, Content: snippet, Source: c.label, Timestamp: info.ModTime()})
		}
	}
	return hits
}

// autoMemoryTerms is query.toLowerCase().split(/\s+/) keeping terms of at
// least 3 UTF-16 units (AM:150).
func autoMemoryTerms(query string) []string {
	var terms []string
	for _, t := range strings.FieldsFunc(strings.ToLower(query), autoMemorySpace) {
		if hookio.JSLen(t) >= 3 {
			terms = append(terms, t)
		}
	}
	return terms
}

// autoMemorySnippet reports whether any term occurs word-bounded
// (case-insensitive) in content and returns the AM:160-170 window: from 200
// units before the first substring hit of any term to 500 after it, snapped
// out to the surrounding blank lines, trimmed. Positions are UTF-16 units,
// as in JS.
func autoMemorySnippet(content string, terms []string) (string, bool) {
	// RE2's \b is ASCII-only, like JS \b without the u flag.
	if !slices.ContainsFunc(terms, func(t string) bool {
		return regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(t) + `\b`).MatchString(content)
	}) {
		return "", false
	}
	u := utf16.Encode([]rune(content))
	lower := utf16.Encode([]rune(strings.ToLower(content)))
	idx := -1
	for _, t := range terms {
		if i := indexU16(lower, utf16.Encode([]rune(t)), 0); i >= 0 && (idx < 0 || i < idx) {
			idx = i
		}
	}
	start, end := max(0, idx-200), min(len(u), idx+500)
	blank := []uint16{'\n', '\n'}
	if p := lastIndexU16(u, blank, start); p >= 0 {
		start = p + 2
	}
	if p := indexU16(u, blank, end); p >= 0 {
		end = p
	}
	if start >= end { // JS slice(start, end) is "" then
		return "", true
	}
	return strings.TrimFunc(string(utf16.Decode(u[start:end])), autoMemorySpace), true
}

// indexU16 is JS s.indexOf(sub, from) over UTF-16 units.
func indexU16(s, sub []uint16, from int) int {
	for i := max(from, 0); i+len(sub) <= len(s); i++ {
		if slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// lastIndexU16 is JS s.lastIndexOf(sub, from): the last match starting at or
// before from.
func lastIndexU16(s, sub []uint16, from int) int {
	for i := min(from, len(s)-len(sub)); i >= 0; i-- {
		if slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// autoMemorySpace is the JS `\s` / trim() whitespace set.
func autoMemorySpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}
```

In `search.go`, only the `memory.SearchAutoMemory` call changes: it passes `d.Cfg.ClaudeDir`.

`internal/mcpserver/search.go`:
```go
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/memory"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	searchSnippetMax = 1500      // S:2750
	searchOutputCap  = 40 * 1024 // S:2672
)

// searchDescription is S:2564-2585 minus the ctx_stats, CONTEXT_MODE_* env
// and file-staleness sentences, the ctx_index tool (not in cmgo) and the
// "26 event categories" count (cmgo captures fewer).
const searchDescription = "Search a unified knowledge base with a multi-strategy ranking pipeline. Two parallel matchers run on every query: a Porter-stemming matcher (\"caching\" finds \"cached\", \"caches\", \"cach\") and a trigram-substring matcher (\"useEff\" finds \"useEffect\"). Their ranked lists are merged via Reciprocal Rank Fusion, so a document that ranks well in both surfaces above one that wins only on a single strategy. Multi-term queries get an additional proximity-rerank pass that boosts passages where the query terms appear close together. Typos are corrected via Levenshtein distance and re-searched. Result snippets are window-extracted around the matched terms, not blindly truncated.\n" +
	"\n" +
	"The knowledge base is unified: queries reach indexed content you stored (ctx_fetch_and_index, ctx_batch_execute output, and large ctx_execute / ctx_execute_file output) AND auto-captured session memory written by hooks (decisions, errors, blockers, plans, user prompts, rejected approaches, tool failures, compaction guides).\n" +
	"\n" +
	"WHEN:\n" +
	"  - You want to recall something that exists in storage (recently indexed content, prior session events, auto-memory) instead of re-reading raw sources\n" +
	"  - You have multiple related questions about the same body of knowledge — batch every question into one call (the ranking pipeline runs per-query but the round-trip cost is paid once)\n" +
	"  - You want to scope the query to one labelled source (pass `source` — partial match is fine)\n" +
	"  - You want a chronological view across current session + prior sessions + persistent auto-memory (pass `sort: \"timeline\"` — the default `relevance` mode only ranks within the current session)\n" +
	"  - You want to filter ranked results by content shape (pass `contentType: \"code\"` to surface implementation snippets or `contentType: \"prose\"` to surface explanations)\n" +
	"\n" +
	"WHEN NOT:\n" +
	"  - The data you want to query has never been stored in the knowledge base AND no session memory has accumulated around it — capture first (run a gather-and-index call), then come back here to query\n" +
	"  - You have one ad-hoc question against data that is not in the knowledge base — answer it inline by running code in the sandbox tool; one round-trip instead of capture-then-query\n" +
	"\n" +
	"RETURNS:\n" +
	"  Per-query ranked sections with window-extracted snippets. Use 2-4 specific technical terms per query. Common session-memory source labels: `decision` (user corrections / preferences), `error` and `error-resolution` (past failures + their fixes), `blocker`, `plan`, `user-prompt`, `rejected-approach`, `compaction` (post-compact session guide). Each response carries a throttle counter (call #N/M in the rolling time window); results taper toward the soft cap and calls block after the hard cap.\n" +
	"\n" +
	"EXAMPLE: ctx_search(queries: [\"root cause\", \"proposed fix\", \"test coverage\"], source: \"issue-#683\")\n" +
	"EXAMPLE: ctx_search(queries: [\"what did we decide about caching\"], source: \"decision\", sort: \"timeline\")\n" +
	"EXAMPLE: ctx_search(queries: [\"useEffect cleanup pattern\"], source: \"react-docs\", contentType: \"code\", limit: 5)\n" +
	"EXAMPLE: ctx_search(queries: [\"last user prompt\", \"active skills\", \"open blockers\"], sort: \"timeline\")"

// searchSchema mirrors buildCtxSearchInputSchema (ctx-search-schema.ts:62-105)
// without the shared-DB `project` field.
const searchSchema = `{
  "type": "object",
  "properties": {
    "queries": {"type": ["array", "string"], "items": {"type": "string"}, "description": "Array of search queries. Batch ALL questions in one call."},
    "limit": {"type": ["number", "string"], "default": 3, "description": "Results per query (default: 3)"},
    "source": {"type": "string", "description": "Filter to a specific indexed source (partial match)."},
    "contentType": {"type": "string", "enum": ["code", "prose"], "description": "Filter results by content type: 'code' or 'prose'."},
    "sort": {"type": "string", "enum": ["relevance", "timeline"], "default": "relevance", "description": "Sort mode. 'relevance' (default): BM25 ranked, current session only. 'timeline': chronological across current session, prior sessions, and auto-memory."}
  }
}`

const emptyKBMessage = "Knowledge base is empty — no content has been indexed yet.\n\n" +
	"ctx_search is a follow-up tool that queries previously indexed content. " +
	"To gather and index content first, use:\n" +
	"  • ctx_batch_execute(commands, queries) — run commands, auto-index output, and search in one call\n" +
	"  • ctx_fetch_and_index(url) — fetch a URL, index it, then search with ctx_search\n" +
	"  • ctx_index(content, source) — manually index text content\n\n" +
	"After indexing, ctx_search becomes available for follow-up queries."

func registerSearch(s *mcp.Server, d *Deps) {
	guard := &floodGuard{} // one server per process, so this is process-wide
	s.AddTool(&mcp.Tool{
		Name:        "ctx_search",
		Title:       "Search Indexed Content",
		Description: searchDescription,
		InputSchema: json.RawMessage(searchSchema),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: new(bool), OpenWorldHint: new(bool)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return searchTool(d, guard, req.Params.Arguments), nil
	})
}

type searchArgs struct {
	queries     []string
	limit       float64
	source      string
	contentType store.ContentType
	sort        string
}

func parseSearchArgs(raw json.RawMessage) (searchArgs, error) {
	sa := searchArgs{limit: 3, sort: "relevance"}
	a, err := ParseArgs(raw)
	if err != nil {
		return sa, err
	}
	if sa.queries, _, err = a.Strings("queries"); err != nil {
		return sa, err
	}
	// No legacy `query`: TS reads it (S:2621) but its zod schema has no such
	// field, so the SDK strips it and TS answers "provide query or queries".
	if n, ok, err := a.Number("limit"); err != nil {
		return sa, err
	} else if ok {
		sa.limit = n
	}
	if sa.source, _, err = a.String("source"); err != nil {
		return sa, err
	}
	if ct, ok, err := a.String("contentType"); err != nil {
		return sa, err
	} else if ok {
		if ct != string(store.Code) && ct != string(store.Prose) {
			return sa, errors.New(`contentType: expected "code" or "prose"`)
		}
		sa.contentType = store.ContentType(ct)
	}
	if so, ok, err := a.String("sort"); err != nil {
		return sa, err
	} else if ok {
		if so != "relevance" && so != "timeline" {
			return sa, errors.New(`sort: expected "relevance" or "timeline"`)
		}
		sa.sort = so
	}
	return sa, nil
}

// searchTool is the ctx_search handler (S:2592-2805). Relevance mode reads
// the content store; timeline mode merges it with session events and
// auto-memory (searchTimeline).
func searchTool(d *Deps, guard *floodGuard, raw json.RawMessage) *mcp.CallToolResult {
	sa, err := parseSearchArgs(raw)
	if err != nil {
		return ArgError(err)
	}
	project := d.Cfg.ProjectKey
	if sa.sort != "timeline" {
		n, err := d.Store.ChunkCount(project)
		if err != nil {
			return ErrorResult("Search error: %v", err)
		}
		if n == 0 {
			return TextResult(emptyKBMessage, true)
		}
	}
	if len(sa.queries) == 0 {
		return TextResult("Error: provide query or queries.", true)
	}

	now := d.Now()
	flood := guard.record(now)
	if flood.blocked {
		return ErrorResult("BLOCKED: %d search calls in %ds. "+
			"You're flooding context. STOP making individual search calls. "+
			"Use ctx_batch_execute(commands, queries) for your next research step.",
			flood.count, int(math.Round(now.Sub(flood.windowStart).Seconds())))
	}
	effectiveLimit := 1
	if !flood.softCapped {
		effectiveLimit = int(math.Min(sa.limit, 2)) // JS slice(0, 1.5) keeps 1
	}

	opts := store.SearchOpts{Project: project, Source: sa.source, ContentType: sa.contentType, Limit: effectiveLimit, Mode: sa.sort}
	totalSize := 0
	var sections []string
	for _, q := range sa.queries {
		if totalSize > searchOutputCap {
			sections = append(sections, "## "+q+"\n(output cap reached)\n")
			continue
		}
		var hits []searchHit
		if effectiveLimit > 0 { // TS slice(0, <=0) is empty; the store would default to 3
			if hits, err = searchHits(d, q, sa, opts); err != nil {
				return ErrorResult("Search error: %v", err)
			}
		}
		if len(hits) == 0 {
			sections = append(sections, "## "+q+"\nNo results found.")
			continue
		}
		parts := make([]string, len(hits))
		for i, r := range hits {
			ts := ""
			if !r.at.IsZero() {
				ts = " | " + r.at.UTC().Format("2006-01-02 15:04")
			}
			parts[i] = fmt.Sprintf("--- [%s%s | %s] ---\n### %s\n\n%s",
				r.origin, ts, r.source, r.title, store.ExtractSnippet(r.content, q, searchSnippetMax, r.highlighted))
		}
		formatted := strings.Join(parts, "\n\n")
		sections = append(sections, "## "+q+"\n\n"+formatted)
		totalSize += jsLen(formatted)
	}
	output := strings.Join(sections, "\n\n---\n\n")

	if flood.count >= searchSoftCapAt {
		output += fmt.Sprintf("\n\n⚠ search call #%d/%d in this window. "+
			"Results limited to %d/query. %d call(s) remaining before block. "+
			`Batch queries: ctx_search(queries: ["q1","q2","q3"]) or use ctx_batch_execute.`,
			flood.count, searchBlockAfter, effectiveLimit, max(0, searchBlockAfter-flood.count))
	} else {
		output += fmt.Sprintf("\n\n> Throttle: call #%d/%d in this window. "+
			"%d call(s) before soft cap. "+
			"Prefer ctx_search(queries: [...]) array form for multi-query workloads — it counts as a single call.",
			flood.count, searchBlockAfter, max(0, searchSoftCapAt-flood.count))
	}
	// TS S:2785 falls back to "No results found. Indexed sources: …" when the
	// output is blank; the throttle line above makes that unreachable.
	return TextResult(output, false)
}

// searchHit is one formatted ctx_search result from any source (S:2739-2756).
type searchHit struct {
	origin, source, title, content, highlighted string
	at                                          time.Time
}

// searchHits runs one query. Relevance mode returns the content store's
// ranked results. Timeline mode (spec §3.2) takes the newest opts.Limit
// matches from the content store, the project's session events (all
// sessions) and, without a source filter, auto-memory; keeps the newest
// opts.Limit overall and returns them oldest first. TS kept the oldest.
func searchHits(d *Deps, q string, sa searchArgs, opts store.SearchOpts) ([]searchHit, error) {
	res, err := d.Store.SearchWithFallback(q, opts)
	if err != nil {
		return nil, err
	}
	var hits []searchHit
	for _, r := range res { // timeline mode returns these oldest first
		hits = append(hits, searchHit{"current-session", r.Source, r.Title, r.Content, r.Highlighted, r.IndexedAt})
	}
	if sa.sort != "timeline" {
		return hits, nil
	}
	evs, err := d.Store.SessionEventsForSearch(d.Cfg.ProjectKey, q, sa.source, opts.Limit)
	if err != nil {
		return nil, err
	}
	for _, e := range slices.Backward(evs) { // newest first from the store
		at, _ := time.Parse("2006-01-02 15:04:05", e.CreatedAt)
		// TS labels every session event prior-session, the current session's too (unified.ts:130).
		hits = append(hits, searchHit{"prior-session", "prior-session", "[" + e.Category + "] " + e.Type, e.Data, "", at})
	}
	if sa.source == "" {
		// Claude Code names its memory dir after the project dir as given, so ProjectDir, not ProjectKey.
		for _, m := range memory.SearchAutoMemory(q, opts.Limit, d.Cfg.ProjectDir, d.Cfg.ClaudeDir) {
			hits = append(hits, searchHit{"auto-memory", m.Source, m.Title, m.Content, "", m.Timestamp})
		}
	}
	// Stable: equal times (second resolution) keep source order, then id order.
	slices.SortStableFunc(hits, func(a, b searchHit) int { return a.at.Compare(b.at) })
	return hits[max(0, len(hits)-opts.Limit):], nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/memory/ ./internal/mcpserver/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/automemory.go internal/memory/automemory_test.go internal/mcpserver/search.go internal/mcpserver/harness_test.go internal/mcpserver/search_memory_test.go
git commit -m "fix(memory): read auto-memory from the Claude config dir"
```


### Task 12: Routing block commands section lists only ctx purge

**Files:**
- Modify: `internal/routing/text.go`
- Test: `internal/routing/golden_test.go`
- Test: `internal/routing/text_test.go`

**Interfaces:**
- Consumes: `testdata/golden/hooks-strings.json` (`routing_block_claude`: the full TS text for `includeCommands: true`)
- Produces: `RoutingBlock(true, false)` now returns the TS text without the stats, doctor and upgrade entries (3799 bytes); test helpers `var missingCommands []string` and `func dropCommands(t *testing.T, ts string) string`

- [ ] **Step 1: Write the failing test**

`TestGoldenStrings` compares the `includeCommands` block with the golden text after `dropCommands`, and the length and sha256 pin moves to the cut text.

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

// missingCommands are the TS <ctx_commands> entries cmgo has no tool for
// yet (spec §2.5): stats arrives with sub-project 4, doctor and upgrade with
// sub-project 5. Each sub-project deletes its names here.
var missingCommands = []string{"stats", "doctor", "upgrade"}

// dropCommands derives cmgo's routing block from the TS text: it cuts the
// entry of each missing command, its two lines and the blank line after them.
func dropCommands(t *testing.T, ts string) string {
	t.Helper()
	for _, name := range missingCommands {
		i := strings.Index(ts, "    \"ctx "+name+"\" | ")
		if i < 0 {
			t.Fatalf("no ctx %s entry in the TS routing block", name)
		}
		j := strings.Index(ts[i:], "\n\n")
		ts = ts[:i] + ts[i+j+2:]
	}
	return ts
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
		want := b.Text
		if b.IncludeCommands {
			want = dropCommands(t, want)
		}
		if got := RoutingBlock(b.IncludeCommands, b.ToolSearchBootstrap); got != want {
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
		// The TS text minus the stats, doctor and upgrade entries (spec §2.5).
		{true, false, pinned{"routing block with commands", 3799, "704bf46686ec6416d3f89ae4dcb59812ea4509e45a1daf517a3bc5cbd5c91a87"}},
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

Run: `go test ./internal/routing/ -run 'TestGoldenStrings|TestRoutingBlock' -count=1`
Expected: FAIL with `golden_test.go:179: routing_block_claude: RoutingBlock(true, false) differs`

- [ ] **Step 3: Write minimal implementation**

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
	// Only the commands cmgo has a tool for (spec §2.5): the stats, doctor
	// and upgrade entries arrive with sub-projects 4 and 5.
	blockCommands = named(`
  <ctx_commands>
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

Run: `go test ./internal/routing/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/routing/text.go internal/routing/text_test.go internal/routing/golden_test.go
git commit -m "feat(routing): list only the ctx purge command in the routing block"
```


### Task 13: Install registers PreCompact

**Files:**
- Modify: `internal/install/hooks.go`
- Test: `internal/install/hooks_test.go`

**Interfaces:**
- Consumes: nothing new
- Produces: `install.HookEvents` gains `{"PreCompact", "precompact", ""}` as its seventh row

- [ ] **Step 1: Write the failing test**

`internal/install/hooks_test.go`:
```go
package install

import (
	"os"
	"os/exec"
	"path/filepath"
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
		{"PreCompact", "precompact", ""},
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
	if got := hookCommand(hookBin, "stop"); got != `/opt/cmgo/bin/cmgo-hook stop` {
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
            "command": "/opt/cmgo/bin/cmgo-hook ` + he.Arg + `"
          }
        ]
      }
    ]`
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s entry in\n%s", he.Event, got)
		}
	}
	hooks, _ := root.get("hooks")
	if strings.Join(hooks.(*object).keys, ",") != "PreToolUse,PostToolUse,PostToolUseFailure,UserPromptSubmit,SessionStart,Stop,PreCompact" {
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

// Claude Code runs the command through sh -c, so $, backtick and backslash in
// the install path must not expand.
func TestHookCommandIsShellSafe(t *testing.T) {
	for _, dir := range []string{"my$HOME dir", "it's here", "back`tick`", `back\slash`, `dq"uote`, "plain"} {
		t.Run(dir, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), dir, "cmgo-hook")
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("#!/bin/sh\nprintf '%s|%s\\n' \"$0\" \"$1\"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := hookCommand(p, "stop")
			out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
			if err != nil || string(out) != p+"|stop\n" {
				t.Fatalf("sh -c %s\n got %q (%v)\nwant %q", cmd, out, err, p+"|stop\n")
			}
			if !isOwnCommand(cmd) {
				t.Fatalf("isOwnCommand rejects its own command %s", cmd)
			}
		})
	}
}

func TestIsOwnCommandQuotingForms(t *testing.T) {
	for cmd, want := range map[string]bool{
		`'/opt/cmgo/bin/cmgo-hook' pretooluse`:  true,
		`'/old place/cmgo-hook' stop`:           true,
		`'/it'\''s/cmgo-hook' stop`:             true,
		`'/my$HOME dir/cmgo-hook' sessionstart`: true,
		`"/old/cmgo-hook" stop`:                 true, // old installs
		`/usr/local/bin/cmgo-hook stop`:         true,
		`'/x/cmgo-hook' unknown`:                false,
		`'/x/cmgo-hook' stop extra`:             false,
		`'/x/cmgo-dev' hook stop`:               false,
		`'/x/cmgo-hook stop`:                    false,
		`'/opt/cmgo/bin/cmgo' hook pretooluse`:  true,
	} {
		if got := isOwnCommand(cmd); got != want {
			t.Errorf("%q: %v, want %v", cmd, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/install/ -count=1`
Expected: FAIL with `hooks_test.go:37: len 6`

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

// HookEvents lists (settings event name, cmgo hook arg, matcher) in hooks
// spec §3.5 order, then PreCompact (session memory spec §1).
var HookEvents = []struct{ Event, Arg, Matcher string }{
	{"PreToolUse", "pretooluse", "Bash|WebFetch|Read|Grep|Agent"},
	{"PostToolUse", "posttooluse", postMatcher},
	{"PostToolUseFailure", "posttoolusefailure", postMatcher},
	{"UserPromptSubmit", "userpromptsubmit", ""},
	{"SessionStart", "sessionstart", ""},
	{"Stop", "stop", ""},
	{"PreCompact", "precompact", ""},
}

// hookCommand quotes the hook binary for sh -c, which Claude Code runs it
// through: $, backtick, backslash and quotes in the install path stay literal.
func hookCommand(hookBinary, arg string) string { return shellQuote(hookBinary) + " " + arg }

// shellWord reads the first word of a shell command line: single-quoted,
// double-quoted (older installs wrote that form) or bare, joined as sh does
// ('it'\”s' is it's). It returns the word and the rest of the line.
func shellWord(cmd string) (word, rest string, ok bool) {
	var b strings.Builder
	for i := 0; i < len(cmd); i++ {
		switch c := cmd[i]; c {
		case '\'':
			j := strings.IndexByte(cmd[i+1:], '\'')
			if j < 0 {
				return "", "", false
			}
			b.WriteString(cmd[i+1 : i+1+j])
			i += j + 1
		case '"':
			j := strings.IndexByte(cmd[i+1:], '"')
			if j < 0 {
				return "", "", false
			}
			b.WriteString(cmd[i+1 : i+1+j])
			i += j + 1
		case '\\':
			if i+1 < len(cmd) {
				i++
				b.WriteByte(cmd[i])
			}
		case ' ', '\t':
			return b.String(), cmd[i:], true
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), "", true
}

// isOwnCommand reports whether cmd is a cmgo hook command from any install
// location: first word (single-quoted, double-quoted or bare) with basename
// cmgo-hook, then "<arg>"; or the form from before cmgo-hook existed,
// basename cmgo, then "hook <arg>". Both match so a reinstall replaces either.
func isOwnCommand(cmd string) bool {
	exe, rest, ok := shellWord(cmd)
	if !ok {
		return false
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

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/install/ ./cmd/cmgo/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/install/hooks.go internal/install/hooks_test.go
git commit -m "feat(install): register the PreCompact hook"
```


### Task 14: cmgo-hook precompact

**Files:**
- Modify: `internal/hookcmd/hookcmd.go`
- Test: `internal/hookcmd/hookcmd_test.go`

**Interfaces:**
- Consumes: `func (s *Store) CompactSession(sessionID, project string) (n, files int, err error)`, `store.OpenWith`, `store.Options` (internal/store)
- Produces: hook event `precompact` in `hookHandlers`; unexported `func preCompact(h *hookCtx) error`

- [ ] **Step 1: Write the failing test**

`TestHookPreCompact` is new.

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

func TestHookPreCompact(t *testing.T) {
	env := hookEnv(t)
	// A session with no events is left untouched (TS precompact.mjs:47).
	if out := callHook(t, env, "precompact", `{"session_id":"s1","trigger":"auto"}`); out != "{}\n" {
		t.Fatalf("got %q", out)
	}
	if rows := eventRows(t, env, "s1"); len(rows) != 0 {
		t.Fatalf("rows: %q", rows)
	}
	callHook(t, env, "posttooluse", `{"tool_name":"Read","tool_input":{"file_path":"/p/a.go"},"tool_response":"x","session_id":"s1"}`)
	callHook(t, env, "userpromptsubmit", `{"prompt":"hello","session_id":"s1"}`)
	if out := callHook(t, env, "precompact", `{"session_id":"s1","trigger":"manual"}`); out != "{}\n" {
		t.Fatalf("got %q", out)
	}
	assertRows(t, eventRows(t, env, "s1"),
		"file_read|file|1|/p/a.go|PostToolUse|0",
		"user_prompt|user-prompt|1|hello|UserPromptSubmit|0",
		"intent|intent|4|implement|UserPromptSubmit|0",
		"compaction_summary|compaction|1|Session compacted. 3 events, 1 files touched.|PreCompact|0")
	if s := hookErrors(env); s != "" {
		t.Fatalf("errors logged: %s", s)
	}

	// Like stop, a failed DB open still prints {}.
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	env["CMGO_DIR"] = filepath.Join(blocker, "data") // parent is a regular file
	if out := callHook(t, env, "precompact", `{"session_id":"s1"}`); out != "{}\n" {
		t.Fatalf("no DB: got %q", out)
	}
}

// install writes $CLAUDE_CONFIG_DIR/settings.json, so a deny rule there must
// be enforced even though HOME/.claude has none.
func TestHookPreToolUseDenyFromClaudeConfigDir(t *testing.T) {
	env := hookEnv(t)
	cfg := filepath.Join(t.TempDir(), "claudecfg")
	os.MkdirAll(cfg, 0o755)
	os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(`{"permissions":{"deny":["Bash(rm -rf *)"]}}`), 0o644)
	env["CLAUDE_CONFIG_DIR"] = cfg
	out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","tool_input":{"command":"rm -rf /x"},"session_id":"s1"}`)
	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatalf("got %q", out)
	}
}

// TS stop.mjs prints {} after its try/catch, so a failed DB write must not
// swallow the output. The error is still logged where it can be.
func TestHookStopPrintsWhenDBWriteFails(t *testing.T) {
	env := hookEnv(t)
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	env["CMGO_DIR"] = filepath.Join(blocker, "data") // parent is a regular file
	if out := callHook(t, env, "stop", `{"session_id":"s1","stop_hook_active":false}`); out != "{}\n" {
		t.Fatalf("got %q", out)
	}

	// With a usable data dir but a locked DB the line is logged.
	env = hookEnv(t)
	callHook(t, env, "stop", `{"session_id":"s1"}`)
	holdDBLock(t, filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if out := callHook(t, env, "stop", `{"session_id":"s1"}`); out != "{}\n" {
		t.Fatalf("locked: got %q", out)
	}
	if log, _ := os.ReadFile(filepath.Join(env["CMGO_DIR"], "hook-errors.log")); !strings.Contains(string(log), "stop:") {
		t.Fatalf("no logged error: %q", log)
	}
}

// TS decides "subagent" with `agent_id != null`, so a null field is a main
// session call; anything else, an empty string included, is a subagent.
func TestHookNullAgentFieldsAreNotSubagent(t *testing.T) {
	env := hookEnv(t)
	remove, err := routing.WriteSentinel(filepath.Join(env["CMGO_DIR"], "run"), os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	for _, c := range []struct {
		fields   string
		redirect bool
	}{
		{`"agent_id":null`, true},
		{`"agent_type":null`, true},
		{`"agent_id":null,"agent_type":null`, true},
		{`"agent_id":null,"agent_type":"general-purpose"`, false},
		{`"agent_id":""`, false},
		{`"agent_id":"a1"`, false},
	} {
		out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","tool_input":{"command":"curl https://x.com"},"session_id":"s1",`+c.fields+`}`)
		if got := strings.Contains(out, "curl/wget redirected"); got != c.redirect {
			t.Errorf("%s: redirected=%v, want %v (out %q)", c.fields, got, c.redirect, out)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hookcmd/ -run PreCompact -count=1`
Expected: FAIL with `hookcmd_test.go:331: got ""`

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

// dbBusyTimeout bounds every wait for the shared DB's lock. Claude Code waits
// for the hook process to exit, so on BUSY the hook drops its events and
// logs one line (spec §1.2) instead of stalling the session.
const dbBusyTimeout = 2 * time.Second

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
	"precompact":         preCompact,
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
	st, err := store.OpenWith(h.cfg.DBPath, store.Options{BusyTimeout: dbBusyTimeout})
	if err != nil {
		return err
	}
	defer st.Close()
	_, err = st.InsertEvents(h.sid, h.cfg.ProjectKey, sourceHook, evs)
	return err
}

// notNull is JS `input[key] != null`: present and not JSON null.
func notNull(in *hookio.Object, key string) bool {
	raw, ok := in.Get(key)
	return ok && string(bytes.TrimSpace(raw)) != "null"
}

// preToolUse routes the call (spec §2) and writes the decision before it
// opens the store, so the DB never delays Claude Code.
func preToolUse(h *hookCtx) error {
	tool, _ := h.in.String("tool_name")
	input, ok := h.in.Object("tool_input")
	if !ok {
		input = &hookio.Object{}
	}
	subagent := notNull(h.in, "agent_id") || notNull(h.in, "agent_type")
	dec := routing.Route(tool, input, subagent, routing.Deps{
		ProjectDir: h.cfg.ProjectDir,
		SessionID:  h.sid,
		RunDir:     h.runDir,
		Policy:     security.NewPolicyDir(h.cfg.ProjectDir, h.cfg.ClaudeDir),
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
	// TS stop.mjs prints after its try/catch: a failed write must not swallow
	// the output, so print first and still return the write error to be logged.
	_, werr := io.WriteString(h.out, "{}\n")
	if err := h.insertEvents("Stop", []store.Event{capture.Stop(active, msg)}); err != nil {
		return err
	}
	return werr
}

// preCompact records the compaction (spec §2.1) and prints {} as TS
// precompact.mjs does, first, so a DB error cannot swallow it.
func preCompact(h *hookCtx) error {
	_, werr := io.WriteString(h.out, "{}\n")
	st, err := store.OpenWith(h.cfg.DBPath, store.Options{BusyTimeout: dbBusyTimeout})
	if err != nil {
		return err
	}
	defer st.Close()
	if _, _, err := st.CompactSession(h.sid, h.cfg.ProjectKey); err != nil {
		return err
	}
	return werr
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

Run: `go test ./internal/hookcmd/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/hookcmd/hookcmd.go internal/hookcmd/hookcmd_test.go
git commit -m "feat(hookcmd): record compactions in the precompact hook"
```


### Task 15: SessionStart compact and resume inject session memory

**Files:**
- Modify: `internal/hookcmd/hookcmd.go`
- Test: `internal/hookcmd/hookcmd_test.go`
- Test: `cmd/cmgo/hook_exec_test.go`

**Interfaces:**
- Consumes: `func Directive(source string, events []store.StoredEvent) string`, `func AutoInjection(events []store.StoredEvent) string` (internal/memory); `func (s *Store) SessionEvents(sessionID string) ([]StoredEvent, error)`, `func (s *Store) InsertEvents(sessionID, project, sourceHook string, evs []Event) (int, error)` (internal/store); `func SessionStartEvent(source, projectDir string, now time.Time) store.Event` (internal/capture); `RoutingBlock(true, false)` (Task 12)
- Produces: unexported `func (h *hookCtx) printSessionStart(context string) error` and `func (h *hookCtx) sessionResume(src, block string) error`

- [ ] **Step 1: Write the failing test**

`sessionStartOut` takes the context added after the routing block, and the block now has the commands section. `TestHookSessionStartOtherSources` changes: only `clear` leaves the DB alone. `TestHookSessionStartCompact`, `TestHookSessionStartResume` and `TestHookSessionStartCompactFailOpen` are new. In `cmd/cmgo/hook_exec_test.go`, only the expected SessionStart block changes, to `RoutingBlock(true, false)`.

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
	"github.com/lang315/context-mode-go/internal/memory"
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

// sessionStartOut is the SessionStart output: the routing block, then extra.
func sessionStartOut(extra string) string {
	return `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":` +
		string(hookio.QuoteString(routing.RoutingBlock(true, false)+extra)) + "}}\n"
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

	if out := callHook(t, env, "sessionstart", `{"session_id":"s1","source":"startup"}`); out != sessionStartOut("") {
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
	if out := callHook(t, env, "sessionstart", `{"session_id":"s1","source":"clear"}`); out != sessionStartOut("") {
		t.Fatalf("clear: got %q", out)
	}
	noDB(t, env)
	// With no events, resume and compact add no context and record only session_start.
	for _, src := range []string{"resume", "compact"} {
		if out := callHook(t, env, "sessionstart", `{"session_id":"s-`+src+`","source":"`+src+`"}`); out != sessionStartOut("") {
			t.Fatalf("%s: got %q", src, out)
		}
		assertRows(t, eventRows(t, env, "s-"+src), sessionStartRow(env, src))
	}
}

func sessionStartRow(env map[string]string, src string) string {
	return fmt.Sprintf(`session_start|session_start|1|{"source":%q,"project_dir":%q,"started_at":%d}|SessionStart|0`,
		src, env["CLAUDE_PROJECT_DIR"], hookNow.Unix())
}

// seedSession records three events for s1 and returns them as stored.
func seedSession(t *testing.T, env map[string]string) []store.StoredEvent {
	t.Helper()
	callHook(t, env, "posttooluse", `{"tool_name":"Read","tool_input":{"file_path":"/p/a.go"},"tool_response":"x","session_id":"s1"}`)
	callHook(t, env, "userpromptsubmit", `{"prompt":"fix the login bug","session_id":"s1"}`)
	st, err := store.Open(filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	evs, err := st.SessionEvents("s1")
	if err != nil || len(evs) != 3 {
		t.Fatalf("seed: %d events, %v", len(evs), err)
	}
	return evs
}

// Spec §2.2: compact adds the directive and auto-injection and records
// resume_completed and session_start.
func TestHookSessionStartCompact(t *testing.T) {
	env := hookEnv(t)
	evs := seedSession(t, env)
	ai := memory.AutoInjection(evs)
	if ai == "" {
		t.Fatal("seed events give no auto-injection")
	}
	want := sessionStartOut(memory.Directive("compact", evs) + "\n\n" + ai)
	if out := callHook(t, env, "sessionstart", `{"session_id":"s1","source":"compact"}`); out != want {
		t.Fatalf("got %q\nwant %q", out, want)
	}
	rows := eventRows(t, env, "s1")
	assertRows(t, rows[3:],
		"resume_completed|session-resume|1|Session resumed from compact. Prior events loaded.|SessionStart|0",
		sessionStartRow(env, "compact"))
	if s := hookErrors(env); s != "" {
		t.Fatalf("errors logged: %s", s)
	}
}

func TestHookSessionStartResume(t *testing.T) {
	env := hookEnv(t)
	evs := seedSession(t, env)
	want := sessionStartOut(memory.Directive("resume", evs))
	if out := callHook(t, env, "sessionstart", `{"session_id":"s1","source":"resume"}`); out != want {
		t.Fatalf("got %q\nwant %q", out, want)
	}
	assertRows(t, eventRows(t, env, "s1")[3:], sessionStartRow(env, "resume"))
}

// The directive needs the events, so the DB is read before printing; a
// failed read prints the routing block alone. Writes come after the output,
// so a locked DB still gets the directive out and logs the write error.
func TestHookSessionStartCompactFailOpen(t *testing.T) {
	env := hookEnv(t)
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	env["CMGO_DIR"] = filepath.Join(blocker, "data") // parent is a regular file
	if out := callHook(t, env, "sessionstart", `{"session_id":"s1","source":"compact"}`); out != sessionStartOut("") {
		t.Fatalf("no DB: got %q", out)
	}

	env = hookEnv(t)
	evs := seedSession(t, env)
	holdDBLock(t, filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if out := callHook(t, env, "sessionstart", `{"session_id":"s1","source":"resume"}`); out != sessionStartOut(memory.Directive("resume", evs)) {
		t.Fatalf("locked: got %q", out)
	}
	if !strings.Contains(hookErrors(env), "sessionstart:") {
		t.Fatalf("no logged error: %q", hookErrors(env))
	}
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

func TestHookPreCompact(t *testing.T) {
	env := hookEnv(t)
	// A session with no events is left untouched (TS precompact.mjs:47).
	if out := callHook(t, env, "precompact", `{"session_id":"s1","trigger":"auto"}`); out != "{}\n" {
		t.Fatalf("got %q", out)
	}
	if rows := eventRows(t, env, "s1"); len(rows) != 0 {
		t.Fatalf("rows: %q", rows)
	}
	callHook(t, env, "posttooluse", `{"tool_name":"Read","tool_input":{"file_path":"/p/a.go"},"tool_response":"x","session_id":"s1"}`)
	callHook(t, env, "userpromptsubmit", `{"prompt":"hello","session_id":"s1"}`)
	if out := callHook(t, env, "precompact", `{"session_id":"s1","trigger":"manual"}`); out != "{}\n" {
		t.Fatalf("got %q", out)
	}
	assertRows(t, eventRows(t, env, "s1"),
		"file_read|file|1|/p/a.go|PostToolUse|0",
		"user_prompt|user-prompt|1|hello|UserPromptSubmit|0",
		"intent|intent|4|implement|UserPromptSubmit|0",
		"compaction_summary|compaction|1|Session compacted. 3 events, 1 files touched.|PreCompact|0")
	if s := hookErrors(env); s != "" {
		t.Fatalf("errors logged: %s", s)
	}

	// Like stop, a failed DB open still prints {}.
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	env["CMGO_DIR"] = filepath.Join(blocker, "data") // parent is a regular file
	if out := callHook(t, env, "precompact", `{"session_id":"s1"}`); out != "{}\n" {
		t.Fatalf("no DB: got %q", out)
	}
}

// install writes $CLAUDE_CONFIG_DIR/settings.json, so a deny rule there must
// be enforced even though HOME/.claude has none.
func TestHookPreToolUseDenyFromClaudeConfigDir(t *testing.T) {
	env := hookEnv(t)
	cfg := filepath.Join(t.TempDir(), "claudecfg")
	os.MkdirAll(cfg, 0o755)
	os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(`{"permissions":{"deny":["Bash(rm -rf *)"]}}`), 0o644)
	env["CLAUDE_CONFIG_DIR"] = cfg
	out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","tool_input":{"command":"rm -rf /x"},"session_id":"s1"}`)
	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatalf("got %q", out)
	}
}

// TS stop.mjs prints {} after its try/catch, so a failed DB write must not
// swallow the output. The error is still logged where it can be.
func TestHookStopPrintsWhenDBWriteFails(t *testing.T) {
	env := hookEnv(t)
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	env["CMGO_DIR"] = filepath.Join(blocker, "data") // parent is a regular file
	if out := callHook(t, env, "stop", `{"session_id":"s1","stop_hook_active":false}`); out != "{}\n" {
		t.Fatalf("got %q", out)
	}

	// With a usable data dir but a locked DB the line is logged.
	env = hookEnv(t)
	callHook(t, env, "stop", `{"session_id":"s1"}`)
	holdDBLock(t, filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if out := callHook(t, env, "stop", `{"session_id":"s1"}`); out != "{}\n" {
		t.Fatalf("locked: got %q", out)
	}
	if log, _ := os.ReadFile(filepath.Join(env["CMGO_DIR"], "hook-errors.log")); !strings.Contains(string(log), "stop:") {
		t.Fatalf("no logged error: %q", log)
	}
}

// TS decides "subagent" with `agent_id != null`, so a null field is a main
// session call; anything else, an empty string included, is a subagent.
func TestHookNullAgentFieldsAreNotSubagent(t *testing.T) {
	env := hookEnv(t)
	remove, err := routing.WriteSentinel(filepath.Join(env["CMGO_DIR"], "run"), os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	for _, c := range []struct {
		fields   string
		redirect bool
	}{
		{`"agent_id":null`, true},
		{`"agent_type":null`, true},
		{`"agent_id":null,"agent_type":null`, true},
		{`"agent_id":null,"agent_type":"general-purpose"`, false},
		{`"agent_id":""`, false},
		{`"agent_id":"a1"`, false},
	} {
		out := callHook(t, env, "pretooluse", `{"tool_name":"Bash","tool_input":{"command":"curl https://x.com"},"session_id":"s1",`+c.fields+`}`)
		if got := strings.Contains(out, "curl/wget redirected"); got != c.redirect {
			t.Errorf("%s: redirected=%v, want %v (out %q)", c.fields, got, c.redirect, out)
		}
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
		string(hookio.QuoteString(routing.RoutingBlock(true, false))) + "}}\n"
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

Run: `go test ./internal/hookcmd/ -count=1`
Expected: FAIL with `hookcmd_test.go:246: got "{\"hookSpecificOutput\":{\"hookEventName\":\"SessionStart\",\"additionalContext\":\"\\n<context_window_protection>…` (TestHookSessionStartStartup: the routing block without `<ctx_commands>`)

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
	"github.com/lang315/context-mode-go/internal/memory"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
)

// stdinTimeout bounds the stdin read (spec §1.2). It is a fallback: Claude
// Code closes stdin. A var so the test need not wait 2 s.
var stdinTimeout = 2 * time.Second

// dbBusyTimeout bounds every wait for the shared DB's lock. Claude Code waits
// for the hook process to exit, so on BUSY the hook drops its events and
// logs one line (spec §1.2) instead of stalling the session.
const dbBusyTimeout = 2 * time.Second

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
	"precompact":         preCompact,
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
	st, err := store.OpenWith(h.cfg.DBPath, store.Options{BusyTimeout: dbBusyTimeout})
	if err != nil {
		return err
	}
	defer st.Close()
	_, err = st.InsertEvents(h.sid, h.cfg.ProjectKey, sourceHook, evs)
	return err
}

// notNull is JS `input[key] != null`: present and not JSON null.
func notNull(in *hookio.Object, key string) bool {
	raw, ok := in.Get(key)
	return ok && string(bytes.TrimSpace(raw)) != "null"
}

// preToolUse routes the call (spec §2) and writes the decision before it
// opens the store, so the DB never delays Claude Code.
func preToolUse(h *hookCtx) error {
	tool, _ := h.in.String("tool_name")
	input, ok := h.in.Object("tool_input")
	if !ok {
		input = &hookio.Object{}
	}
	subagent := notNull(h.in, "agent_id") || notNull(h.in, "agent_type")
	dec := routing.Route(tool, input, subagent, routing.Deps{
		ProjectDir: h.cfg.ProjectDir,
		SessionID:  h.sid,
		RunDir:     h.runDir,
		Policy:     security.NewPolicyDir(h.cfg.ProjectDir, h.cfg.ClaudeDir),
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
	// TS stop.mjs prints after its try/catch: a failed write must not swallow
	// the output, so print first and still return the write error to be logged.
	_, werr := io.WriteString(h.out, "{}\n")
	if err := h.insertEvents("Stop", []store.Event{capture.Stop(active, msg)}); err != nil {
		return err
	}
	return werr
}

// preCompact records the compaction (spec §2.1) and prints {} as TS
// precompact.mjs does, first, so a DB error cannot swallow it.
func preCompact(h *hookCtx) error {
	_, werr := io.WriteString(h.out, "{}\n")
	st, err := store.OpenWith(h.cfg.DBPath, store.Options{BusyTimeout: dbBusyTimeout})
	if err != nil {
		return err
	}
	defer st.Close()
	if _, _, err := st.CompactSession(h.sid, h.cfg.ProjectKey); err != nil {
		return err
	}
	return werr
}

// sessionStart prints the routing block for every source (spec §4). Startup
// also prunes old guidance markers and records the rule and session_start
// events; compact and resume add the session's memory (spec §2.2).
func sessionStart(h *hookCtx) error {
	block := routing.RoutingBlock(true, false)
	src, _ := h.in.String("source")
	if raw, ok := h.in.Get("source"); !ok || string(raw) == "null" {
		src = "startup" // TS: input.source ?? "startup"
	}
	switch src {
	case "compact", "resume":
		return h.sessionResume(src, block)
	case "startup":
		if err := h.printSessionStart(block); err != nil {
			return err
		}
		routing.PruneGuidance(h.runDir, h.now, guidanceMaxAge)
		evs := append(capture.SessionStartRules(h.cfg.Home, h.cfg.ProjectDir), capture.SessionStartEvent(src, h.cfg.ProjectDir, h.now))
		return h.insertEvents("SessionStart", evs)
	}
	return h.printSessionStart(block)
}

func (h *hookCtx) printSessionStart(context string) error {
	_, err := io.WriteString(h.out, `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":`+
		string(hookio.QuoteString(context))+"}}\n")
	return err
}

// sessionResume appends the directive (and, on compact, the auto-injection)
// built from the session's events to block, then records the resume events,
// as TS sessionstart.mjs:191-280 does. TS also indexed an events markdown;
// cmgo does not (spec §2.3). The directive needs the events, so the read
// comes before the output; a failed read prints block alone. Writes come
// after the output.
func (h *hookCtx) sessionResume(src, block string) error {
	st, err := store.OpenWith(h.cfg.DBPath, store.Options{BusyTimeout: dbBusyTimeout})
	if err != nil {
		return errors.Join(err, h.printSessionStart(block))
	}
	defer st.Close()
	evs, err := st.SessionEvents(h.sid)
	if err != nil {
		return errors.Join(err, h.printSessionStart(block))
	}
	context := block + memory.Directive(src, evs) // "" with no events
	var add []store.Event
	if len(evs) > 0 && src == "compact" {
		if ai := memory.AutoInjection(evs); ai != "" {
			context += "\n\n" + ai
		}
		add = append(add, store.Event{Type: "resume_completed", Category: "session-resume", Priority: 1,
			Data: "Session resumed from compact. Prior events loaded."})
	}
	if err := h.printSessionStart(context); err != nil {
		return err
	}
	add = append(add, capture.SessionStartEvent(src, h.cfg.ProjectDir, h.now))
	_, err = st.InsertEvents(h.sid, h.cfg.ProjectKey, "SessionStart", add)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/hookcmd/ ./cmd/cmgo/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/hookcmd/hookcmd.go internal/hookcmd/hookcmd_test.go cmd/cmgo/hook_exec_test.go
git commit -m "feat(hookcmd): inject session memory on SessionStart compact and resume"
```


### Task 16: Bench precompact and sessionstart compact at 1000 events

**Files:**
- Modify: `bench/hook-latency.py`

**Interfaces:**
- Consumes: the built `cmgo-hook` binary; the `session_events` table of `$CMGO_DIR/cmgo.db` (read-only count through Python's stdlib `sqlite3`)
- Produces: bench cases `precompact (1000 events)` and `sessionstart compact (1000 events)` against session `bench-mem`, filled through the hook binary

- [ ] **Step 1: Write the failing test**

The bench is the test. `fill_session` fires 1100 hooks into `bench-mem` (a prompt every fourth fire, Read captures otherwise). It then checks that the per-session cap left exactly 1000 events. The two new cases run against that session.

`bench/hook-latency.py`:
```python
#!/usr/bin/env python3 -I
"""Criterion 1: `cmgo-hook <event>` wall time p95 <= 10 ms per case (spec §6.4,
session memory spec §0 criterion 4).

Run: go build -o bin/ ./cmd/cmgo-hook && python3 -I bench/hook-latency.py bin/cmgo-hook [runs]

Every case runs `runs` times against a temp CMGO_DIR, HOME and project dir
(never the real ones), after one warm-up call that creates the DB. The
precompact and sessionstart compact cases run against a session pre-filled
to 1000 events (the per-session cap) through the hook itself. The time
is subprocess.run wall clock, so it includes the fork/exec cost. Then
posttooluse runs from 4 threads at once, `runs` fires each, into the same DB.
For context it also times /usr/bin/true and cmgo-hook with no arguments
(usage, exit 2) the same way, and prints the 1-minute load average.
"""
import json
import math
import os
import shutil
import sqlite3
import statistics
import subprocess
import sys
import tempfile
import threading
import time

LIMIT_MS, WORKERS, MEM_EVENTS, MEM_SID = 10.0, 4, 1000, "bench-mem"


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


def fill_session(binary, env, db):
    """Records MEM_EVENTS events for MEM_SID: Read captures with a prompt every
    fourth fire, more than the cap, so the cap's eviction leaves exactly 1000."""
    for i in range(MEM_EVENTS + MEM_EVENTS // 10):
        if i % 4 == 0:
            fire(binary, env, "userpromptsubmit", {"prompt": f"step {i}: fix the parser and add tests", "session_id": MEM_SID})
        else:
            fire(binary, env, "posttooluse", read_payload(i, MEM_SID))
    con = sqlite3.connect(db)
    try:
        n = con.execute("SELECT count(*) FROM session_events WHERE session_id = ?", (MEM_SID,)).fetchone()[0]
    finally:
        con.close()
    if n != MEM_EVENTS:
        raise SystemExit(f"fill: {n} events, want {MEM_EVENTS}")


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
        fill_session(binary, env, os.path.join(data, "cmgo.db"))

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
            (f"precompact ({MEM_EVENTS} events)", "precompact",
             lambda i: {"session_id": MEM_SID, "trigger": "auto"}, lambda out: out == b"{}\n"),
            (f"sessionstart compact ({MEM_EVENTS} events)", "sessionstart",
             lambda i: {"source": "compact", "session_id": MEM_SID},
             lambda out: b'<session_knowledge source=\\"compact\\">' in out),
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

- [ ] **Step 2: Run test to verify it fails**

Run: `go build -o bin/ ./cmd/cmgo-hook && python3 -I bench/hook-latency.py bin/cmgo-hook`
Expected: PASS here, because Tasks Task 14 and Task 15 already built both hooks; the bench pins their latency. A case over `LIMIT_MS` prints `FAIL` on its line, and the script then ends with `FAIL` and exit status 1.

- [ ] **Step 3: Write minimal implementation**

No production code: Tasks Task 14 and Task 15 built both hooks. If a case fails, profile the hook before changing anything; do not raise `LIMIT_MS` to hide it.

- [ ] **Step 4: Run test to verify it passes**

Run: `go build -o bin/ ./cmd/cmgo-hook && python3 -I bench/hook-latency.py bin/cmgo-hook`
Expected: every case prints PASS, and the script ends with `PASS` and exit status 0. Measured on darwin/arm64 at 1-minute load average 5.35 (start) and 4.26 (end):

```text
load average (1 min) at start: 5.35
context /usr/bin/true: p50 2.01 ms p95 2.77 ms
context cmgo-hook, no args: p50 4.61 ms p95 5.28 ms
pretooluse passthrough: p50 4.75 ms p95 5.92 ms PASS
pretooluse curl redirect: p50 6.61 ms p95 8.44 ms PASS
posttooluse Read capture: p50 6.19 ms p95 7.97 ms PASS
userpromptsubmit: p50 6.26 ms p95 7.53 ms PASS
sessionstart startup: p50 6.50 ms p95 7.84 ms PASS
precompact (1000 events): p50 6.56 ms p95 7.64 ms PASS
sessionstart compact (1000 events): p50 7.40 ms p95 8.80 ms PASS
posttooluse x4 concurrent: p50 7.56 ms p95 9.56 ms PASS
load average (1 min) at end: 4.26
PASS
```

- [ ] **Step 5: Commit**

```bash
git add bench/hook-latency.py
git commit -m "test(bench): time precompact and sessionstart compact at 1000 events"
```


### Task 17: Built-binary end-to-end for precompact and SessionStart compact

**Files:**
- Test: `cmd/cmgo/hook_memory_exec_test.go`

**Interfaces:**
- Consumes: `builtBinary`, `hookEnv`, `runHookBinary`, `eventRows`, `hookErrors` (cmd/cmgo/hook_exec_test.go); `memory.Directive`, `memory.AutoInjection`, `routing.RoutingBlock`, `hookio.QuoteString`, `store.Open`
- Produces: `TestBinaryPreCompactAndCompactDirective`

- [ ] **Step 1: Write the failing test**

`cmd/cmgo/hook_memory_exec_test.go`:
```go
package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/memory"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/store"
)

// Session memory spec §2.1-2.2 on the built binary: precompact records the
// compaction, then SessionStart(compact) returns the directive built from the
// session's events.
func TestBinaryPreCompactAndCompactDirective(t *testing.T) {
	env := hookEnv(t)
	for _, c := range []struct{ event, stdin string }{
		{"userpromptsubmit", `{"prompt":"fix the login bug","session_id":"s1"}`},
		{"posttooluse", `{"tool_name":"Read","tool_input":{"file_path":"/p/a.go"},"tool_response":"x","session_id":"s1"}`},
	} {
		if out, code := runHookBinary(t, env, c.stdin, c.event); code != 0 || out != "" {
			t.Fatalf("%s: exit %d, stdout %q", c.event, code, out)
		}
	}
	if out, code := runHookBinary(t, env, `{"session_id":"s1","trigger":"auto"}`, "precompact"); code != 0 || out != "{}\n" {
		t.Fatalf("precompact: exit %d, stdout %q", code, out)
	}
	rows := eventRows(t, env, "s1")
	if len(rows) != 4 || rows[3] != "compaction_summary|compaction|1|Session compacted. 3 events, 1 files touched.|PreCompact|0" {
		t.Fatalf("rows %q", rows)
	}

	st, err := store.Open(filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	evs, err := st.SessionEvents("s1")
	st.Close()
	if err != nil {
		t.Fatal(err)
	}
	ctx := routing.RoutingBlock(true, false) + memory.Directive("compact", evs) + "\n\n" + memory.AutoInjection(evs)
	want := `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":` + string(hookio.QuoteString(ctx)) + "}}\n"
	out, code := runHookBinary(t, env, `{"session_id":"s1","source":"compact"}`, "sessionstart")
	if code != 0 || out != want || !strings.Contains(out, `<session_knowledge source=\"compact\">`) {
		t.Fatalf("sessionstart compact: exit %d\ngot  %q\nwant %q", code, out, want)
	}
	if s := hookErrors(env); s != "" {
		t.Fatalf("errors logged: %s", s)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/cmgo/ -run TestBinaryPreCompact -count=1`
Expected: PASS here, because Tasks Task 14 and Task 15 already built both hooks; this test pins them on the built binaries. With `internal/hookcmd/hookcmd.go` as it was before Task 14, it fails with `hook_memory_exec_test.go:28: precompact: exit 0, stdout ""`.

- [ ] **Step 3: Write minimal implementation**

No production code: the test covers Tasks Task 14 and Task 15 through `cmgo-hook`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go vet ./... && go test ./... -count=1 && go test -race -count=1 ./internal/hookcmd/ ./internal/routing/ ./internal/install/ ./internal/memory/ ./internal/mcpserver/ ./cmd/cmgo/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/cmgo/hook_memory_exec_test.go
git commit -m "test(cmgo): end-to-end precompact and SessionStart compact on the built hook"
```


### Task 18: ctx_search source session-events reads session events

**Files:**
- Modify: `internal/mcpserver/search.go`
- Test: `internal/mcpserver/search_memory_test.go`

**Interfaces:**
- Consumes: `func (s *Store) SessionEventsForSearch(project, query, source string, limit int) ([]StoredEvent, error)` (Task 5; newest first); `func ExtractSnippet(content, query string, maxLen int, highlighted string) string` (internal/store); test helpers `newHarness`, `(*harness).call`, `(*harness).index`, `throttle1` (internal/mcpserver/harness_test.go, search_test.go), `(*harness).events` and `stamp` (search_memory_test.go)
- Produces: unexported `const sessionEventsSource = "session-events"`; `ctx_search` with `source` exactly `session-events` answers from session events in both sort modes (spec §3.1)

- [ ] **Step 1: Write the failing test**

`TestSearchSessionEventsSource` is new. With an empty content store it expects the project's two newest matching events, newest first, with the category as the header's source, and `No results found.` for a query with no match. In timeline mode it expects session events only, even though a content chunk and the project `CLAUDE.md` also match. A near-miss label (`session-event`) must not reach session events.

`internal/mcpserver/search_memory_test.go`:
```go
package mcpserver

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/memory"
	"github.com/lang315/context-mode-go/internal/store"
)

// events inserts session events for project (the harness project when "").
func (h *harness) events(sessionID, project string, evs ...store.Event) {
	h.t.Helper()
	if project == "" {
		project = h.Deps.Cfg.ProjectKey
	}
	if _, err := h.Deps.Store.InsertEvents(sessionID, project, "PostToolUse", evs); err != nil {
		h.t.Fatal(err)
	}
}

// stamp matches the "YYYY-MM-DD HH:MM" header field.
const stamp = `\d{4}-\d\d-\d\d \d\d:\d\d`

func TestSearchTimelineSessionEvents(t *testing.T) {
	h := newHarness(t, nil)
	h.events("s1", "",
		store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "first cache decision"},
		store.Event{Type: "file_read", Category: "file", Priority: 1, Data: "cache.go"},
	)
	h.events("s2", "", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "latest cache decision"})
	h.events("s9", "/other/project", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "foreign cache decision"})

	// Newest 2 matches of the project (all sessions), shown oldest to newest.
	out, isErr := h.call("ctx_search", `{"queries": ["CACHE"], "sort": "timeline"}`)
	re := regexp.MustCompile(`\A## CACHE\n\n` +
		`--- \[prior-session \| ` + stamp + ` \| prior-session\] ---\n### \[file\] file_read\n\ncache\.go\n\n` +
		`--- \[prior-session \| ` + stamp + ` \| prior-session\] ---\n### \[decision\] decision\n\nlatest cache decision` +
		regexp.QuoteMeta(throttle1) + `\z`)
	if isErr || !re.MatchString(out) {
		t.Fatalf("isErr=%v\n%s", isErr, out)
	}
	at, err := time.Parse("2006-01-02 15:04", regexp.MustCompile(stamp).FindString(out))
	if err != nil || time.Since(at) > 5*time.Minute || time.Since(at) < -time.Minute {
		t.Fatalf("timestamp %v is not the UTC insert time (%v)", at, err)
	}

	// source must equal the category; every term must match.
	out, _ = h.call("ctx_search", `{"queries": ["cache decision", "nothing here"], "sort": "timeline", "source": "decision", "limit": 1}`)
	if !strings.HasPrefix(out, "## cache decision\n\n--- [prior-session | ") ||
		!strings.Contains(out, "### [decision] decision\n\nlatest cache decision\n\n---\n\n## nothing here\nNo results found.") ||
		strings.Contains(out, "first cache") || strings.Contains(out, "foreign") {
		t.Fatalf("source filter:\n%s", out)
	}

	// Relevance mode does not read session events.
	h.index("notes", store.Chunk{Title: "Other", Content: "unrelated text"})
	if out, _ := h.call("ctx_search", `{"queries": ["latest cache decision"]}`); strings.Contains(out, "prior-session") {
		t.Fatalf("relevance reached session events:\n%s", out)
	}
}

func TestSearchTimelineMergesSources(t *testing.T) {
	h := newHarness(t, nil)
	md := filepath.Join(h.Project, "CLAUDE.md")
	if err := os.WriteFile(md, []byte("Widget rules live here."), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 5, 6, 7, 8, 9, 0, time.UTC)
	if err := os.Chtimes(md, old, old); err != nil {
		t.Fatal(err)
	}
	// Auto-memory alone: origin auto-memory, mtime in UTC, label as source.
	out, _ := h.call("ctx_search", `{"queries": ["widget"], "sort": "timeline"}`)
	if want := "## widget\n\n--- [auto-memory | 2020-05-06 07:08 | project/CLAUDE.md] ---\n### [auto-memory] project/CLAUDE.md\n\nWidget rules live here." + throttle1; out != want {
		t.Fatalf("auto-memory:\n%s", out)
	}

	h.index("notes", store.Chunk{Title: "Widget doc", Content: "widget content"})
	h.events("s1", "", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "widget decision"})
	// Three sources match; the old memory file is dropped, content then event remain.
	out, _ = h.call("ctx_search", `{"queries": ["widget"], "sort": "timeline"}`)
	re := regexp.MustCompile(`\A## widget\n\n` +
		`--- \[current-session \| ` + stamp + ` \| notes\] ---\n### Widget doc\n\nwidget content\n\n` +
		`--- \[prior-session \| ` + stamp + ` \| prior-session\] ---\n### \[decision\] decision\n\nwidget decision\n\n> Throttle`)
	if !re.MatchString(out) {
		t.Fatalf("merge:\n%s", out)
	}

	// A newer memory file outranks the content chunk and is shown last.
	future := time.Now().Add(24 * time.Hour)
	if err := os.Chtimes(md, future, future); err != nil {
		t.Fatal(err)
	}
	out, _ = h.call("ctx_search", `{"queries": ["widget"], "sort": "timeline"}`)
	re = regexp.MustCompile(`\A## widget\n\n--- \[prior-session \| [^\n]*\n### \[decision\] decision\n\nwidget decision\n\n` +
		`--- \[auto-memory \| ` + regexp.QuoteMeta(future.UTC().Format("2006-01-02 15:04")) + ` \| project/CLAUDE.md\] ---\n`)
	if !re.MatchString(out) {
		t.Fatalf("future memory:\n%s", out)
	}

	// A source filter skips auto-memory and filters the content label.
	h2 := newHarness(t, nil)
	if err := os.WriteFile(filepath.Join(h2.Project, "CLAUDE.md"), []byte("widget"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _ := h2.call("ctx_search", `{"queries": ["widget"], "sort": "timeline", "source": "notes"}`); !strings.HasPrefix(out, "## widget\nNo results found.") {
		t.Fatalf("source filter reached auto-memory:\n%s", out)
	}
}

// Spec §3.1: source "session-events" reads the project's session events
// directly, in both sort modes, newest first, with no empty-KB error.
func TestSearchSessionEventsSource(t *testing.T) {
	h := newHarness(t, nil)
	h.events("s1", "",
		store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "first cache decision"},
		store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "old cache decision"},
		store.Event{Type: "file_read", Category: "file", Priority: 1, Data: "cache.go"},
	)
	h.events("s2", "", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "new cache decision"})
	h.events("s9", "/other/project", store.Event{Type: "decision", Category: "decision", Priority: 2, Data: "foreign cache decision"})

	// Empty content store, relevance mode: newest 2 of the project's matches.
	out, isErr := h.call("ctx_search", `{"queries": ["cache decision", "nothing here"], "source": "session-events"}`)
	hdr := `--- \[prior-session \| ` + stamp + ` \| decision\] ---\n### \[decision\] decision\n\n`
	re := regexp.MustCompile(`\A## cache decision\n\n` + hdr + `new cache decision\n\n` + hdr + `old cache decision` +
		`\n\n---\n\n## nothing here\nNo results found\.` + regexp.QuoteMeta(throttle1) + `\z`)
	if isErr || !re.MatchString(out) {
		t.Fatalf("relevance: isErr=%v\n%s", isErr, out)
	}

	// Timeline mode: session events only, no content store or auto-memory.
	h.index("notes", store.Chunk{Title: "Cache doc", Content: "cache content"})
	if err := os.WriteFile(filepath.Join(h.Project, "CLAUDE.md"), []byte("cache rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ = h.call("ctx_search", `{"queries": ["cache"], "sort": "timeline", "source": "session-events", "limit": 1}`)
	if !regexp.MustCompile(`\A## cache\n\n` + hdr + `new cache decision\n\n> Throttle`).MatchString(out) {
		t.Fatalf("timeline:\n%s", out)
	}

	// Only the exact label reads session events.
	out, _ = h.call("ctx_search", `{"queries": ["cache decision"], "source": "session-event"}`)
	if strings.Contains(out, "prior-session") {
		t.Fatalf("near-miss label reached session events:\n%s", out)
	}
}

// Auto-memory reads Claude Code's config dir, cfg.ClaudeDir ($CLAUDE_CONFIG_DIR
// when set), not <home>/.claude (spec §3.3).
func TestSearchTimelineReadsClaudeDir(t *testing.T) {
	h := newHarness(t, nil)
	h.Deps.Cfg.ClaudeDir = filepath.Join(t.TempDir(), "claude-config")
	write := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(h.Deps.Cfg.ClaudeDir, "projects", memory.ProjectSlug(h.Project), "memory", "notes.md"), "Gadget notes.")
	write(filepath.Join(h.Home, ".claude", "CLAUDE.md"), "gadget rules in home")
	out, _ := h.call("ctx_search", `{"queries": ["gadget"], "sort": "timeline"}`)
	if !strings.Contains(out, "### [auto-memory] memory/notes.md\n\nGadget notes.") || strings.Contains(out, "user/CLAUDE.md") {
		t.Fatalf("auto-memory:\n%s", out)
	}
}

func TestSearchDescriptionMentionsSessionMemory(t *testing.T) {
	for _, s := range []string{
		"AND auto-captured session memory written by hooks",
		"(recently indexed content, prior session events, auto-memory)",
		"You want a chronological view across current session + prior sessions + persistent auto-memory (pass `sort: \"timeline\"`",
		"AND no session memory has accumulated around it",
		"Common session-memory source labels: `decision`",
		`EXAMPLE: ctx_search(queries: ["what did we decide about caching"], source: "decision", sort: "timeline")`,
		`EXAMPLE: ctx_search(queries: ["last user prompt", "active skills", "open blockers"], sort: "timeline")`,
	} {
		if !strings.Contains(searchDescription, s) {
			t.Errorf("description lacks %q", s)
		}
	}
	for _, s := range []string{"ctx_stats", "CONTEXT_MODE_", "ctx_index", "content hash", "oldest first"} {
		if strings.Contains(searchDescription, s) {
			t.Errorf("description still has %q", s)
		}
	}
	if !strings.Contains(searchSchema, "'timeline': chronological across current session, prior sessions, and auto-memory.") {
		t.Error("sort schema description not restored")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcpserver/ -run TestSearchSessionEventsSource -count=1`
Expected: FAIL with `search_memory_test.go:134: relevance: isErr=true` followed by the empty-knowledge-base message (`Knowledge base is empty — no content has been indexed yet.`)

- [ ] **Step 3: Write minimal implementation**

`searchTool` skips the empty-knowledge-base check for the `session-events` source, and `searchHits` answers that source first, before the relevance and timeline paths, so timeline mode does not pass it on as a category filter.

`internal/mcpserver/search.go`:
```go
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/memory"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	searchSnippetMax = 1500      // S:2750
	searchOutputCap  = 40 * 1024 // S:2672
)

// searchDescription is S:2564-2585 minus the ctx_stats, CONTEXT_MODE_* env
// and file-staleness sentences, the ctx_index tool (not in cmgo) and the
// "26 event categories" count (cmgo captures fewer).
const searchDescription = "Search a unified knowledge base with a multi-strategy ranking pipeline. Two parallel matchers run on every query: a Porter-stemming matcher (\"caching\" finds \"cached\", \"caches\", \"cach\") and a trigram-substring matcher (\"useEff\" finds \"useEffect\"). Their ranked lists are merged via Reciprocal Rank Fusion, so a document that ranks well in both surfaces above one that wins only on a single strategy. Multi-term queries get an additional proximity-rerank pass that boosts passages where the query terms appear close together. Typos are corrected via Levenshtein distance and re-searched. Result snippets are window-extracted around the matched terms, not blindly truncated.\n" +
	"\n" +
	"The knowledge base is unified: queries reach indexed content you stored (ctx_fetch_and_index, ctx_batch_execute output, and large ctx_execute / ctx_execute_file output) AND auto-captured session memory written by hooks (decisions, errors, blockers, plans, user prompts, rejected approaches, tool failures, compaction guides).\n" +
	"\n" +
	"WHEN:\n" +
	"  - You want to recall something that exists in storage (recently indexed content, prior session events, auto-memory) instead of re-reading raw sources\n" +
	"  - You have multiple related questions about the same body of knowledge — batch every question into one call (the ranking pipeline runs per-query but the round-trip cost is paid once)\n" +
	"  - You want to scope the query to one labelled source (pass `source` — partial match is fine)\n" +
	"  - You want a chronological view across current session + prior sessions + persistent auto-memory (pass `sort: \"timeline\"` — the default `relevance` mode only ranks within the current session)\n" +
	"  - You want to filter ranked results by content shape (pass `contentType: \"code\"` to surface implementation snippets or `contentType: \"prose\"` to surface explanations)\n" +
	"\n" +
	"WHEN NOT:\n" +
	"  - The data you want to query has never been stored in the knowledge base AND no session memory has accumulated around it — capture first (run a gather-and-index call), then come back here to query\n" +
	"  - You have one ad-hoc question against data that is not in the knowledge base — answer it inline by running code in the sandbox tool; one round-trip instead of capture-then-query\n" +
	"\n" +
	"RETURNS:\n" +
	"  Per-query ranked sections with window-extracted snippets. Use 2-4 specific technical terms per query. Common session-memory source labels: `decision` (user corrections / preferences), `error` and `error-resolution` (past failures + their fixes), `blocker`, `plan`, `user-prompt`, `rejected-approach`, `compaction` (post-compact session guide). Each response carries a throttle counter (call #N/M in the rolling time window); results taper toward the soft cap and calls block after the hard cap.\n" +
	"\n" +
	"EXAMPLE: ctx_search(queries: [\"root cause\", \"proposed fix\", \"test coverage\"], source: \"issue-#683\")\n" +
	"EXAMPLE: ctx_search(queries: [\"what did we decide about caching\"], source: \"decision\", sort: \"timeline\")\n" +
	"EXAMPLE: ctx_search(queries: [\"useEffect cleanup pattern\"], source: \"react-docs\", contentType: \"code\", limit: 5)\n" +
	"EXAMPLE: ctx_search(queries: [\"last user prompt\", \"active skills\", \"open blockers\"], sort: \"timeline\")"

// searchSchema mirrors buildCtxSearchInputSchema (ctx-search-schema.ts:62-105)
// without the shared-DB `project` field.
const searchSchema = `{
  "type": "object",
  "properties": {
    "queries": {"type": ["array", "string"], "items": {"type": "string"}, "description": "Array of search queries. Batch ALL questions in one call."},
    "limit": {"type": ["number", "string"], "default": 3, "description": "Results per query (default: 3)"},
    "source": {"type": "string", "description": "Filter to a specific indexed source (partial match)."},
    "contentType": {"type": "string", "enum": ["code", "prose"], "description": "Filter results by content type: 'code' or 'prose'."},
    "sort": {"type": "string", "enum": ["relevance", "timeline"], "default": "relevance", "description": "Sort mode. 'relevance' (default): BM25 ranked, current session only. 'timeline': chronological across current session, prior sessions, and auto-memory."}
  }
}`

// sessionEventsSource is the source the directive's <session_search> block
// names. cmgo answers it from the events table, not an indexed dump (spec §3.1).
const sessionEventsSource = "session-events"

const emptyKBMessage = "Knowledge base is empty — no content has been indexed yet.\n\n" +
	"ctx_search is a follow-up tool that queries previously indexed content. " +
	"To gather and index content first, use:\n" +
	"  • ctx_batch_execute(commands, queries) — run commands, auto-index output, and search in one call\n" +
	"  • ctx_fetch_and_index(url) — fetch a URL, index it, then search with ctx_search\n" +
	"  • ctx_index(content, source) — manually index text content\n\n" +
	"After indexing, ctx_search becomes available for follow-up queries."

func registerSearch(s *mcp.Server, d *Deps) {
	guard := &floodGuard{} // one server per process, so this is process-wide
	s.AddTool(&mcp.Tool{
		Name:        "ctx_search",
		Title:       "Search Indexed Content",
		Description: searchDescription,
		InputSchema: json.RawMessage(searchSchema),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: new(bool), OpenWorldHint: new(bool)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return searchTool(d, guard, req.Params.Arguments), nil
	})
}

type searchArgs struct {
	queries     []string
	limit       float64
	source      string
	contentType store.ContentType
	sort        string
}

func parseSearchArgs(raw json.RawMessage) (searchArgs, error) {
	sa := searchArgs{limit: 3, sort: "relevance"}
	a, err := ParseArgs(raw)
	if err != nil {
		return sa, err
	}
	if sa.queries, _, err = a.Strings("queries"); err != nil {
		return sa, err
	}
	// No legacy `query`: TS reads it (S:2621) but its zod schema has no such
	// field, so the SDK strips it and TS answers "provide query or queries".
	if n, ok, err := a.Number("limit"); err != nil {
		return sa, err
	} else if ok {
		sa.limit = n
	}
	if sa.source, _, err = a.String("source"); err != nil {
		return sa, err
	}
	if ct, ok, err := a.String("contentType"); err != nil {
		return sa, err
	} else if ok {
		if ct != string(store.Code) && ct != string(store.Prose) {
			return sa, errors.New(`contentType: expected "code" or "prose"`)
		}
		sa.contentType = store.ContentType(ct)
	}
	if so, ok, err := a.String("sort"); err != nil {
		return sa, err
	} else if ok {
		if so != "relevance" && so != "timeline" {
			return sa, errors.New(`sort: expected "relevance" or "timeline"`)
		}
		sa.sort = so
	}
	return sa, nil
}

// searchTool is the ctx_search handler (S:2592-2805). Relevance mode reads
// the content store; timeline mode merges it with session events and
// auto-memory. The session-events source reads only session events.
func searchTool(d *Deps, guard *floodGuard, raw json.RawMessage) *mcp.CallToolResult {
	sa, err := parseSearchArgs(raw)
	if err != nil {
		return ArgError(err)
	}
	project := d.Cfg.ProjectKey
	if sa.sort != "timeline" && sa.source != sessionEventsSource {
		n, err := d.Store.ChunkCount(project)
		if err != nil {
			return ErrorResult("Search error: %v", err)
		}
		if n == 0 {
			return TextResult(emptyKBMessage, true)
		}
	}
	if len(sa.queries) == 0 {
		return TextResult("Error: provide query or queries.", true)
	}

	now := d.Now()
	flood := guard.record(now)
	if flood.blocked {
		return ErrorResult("BLOCKED: %d search calls in %ds. "+
			"You're flooding context. STOP making individual search calls. "+
			"Use ctx_batch_execute(commands, queries) for your next research step.",
			flood.count, int(math.Round(now.Sub(flood.windowStart).Seconds())))
	}
	effectiveLimit := 1
	if !flood.softCapped {
		effectiveLimit = int(math.Min(sa.limit, 2)) // JS slice(0, 1.5) keeps 1
	}

	opts := store.SearchOpts{Project: project, Source: sa.source, ContentType: sa.contentType, Limit: effectiveLimit, Mode: sa.sort}
	totalSize := 0
	var sections []string
	for _, q := range sa.queries {
		if totalSize > searchOutputCap {
			sections = append(sections, "## "+q+"\n(output cap reached)\n")
			continue
		}
		var hits []searchHit
		if effectiveLimit > 0 { // TS slice(0, <=0) is empty; the store would default to 3
			if hits, err = searchHits(d, q, sa, opts); err != nil {
				return ErrorResult("Search error: %v", err)
			}
		}
		if len(hits) == 0 {
			sections = append(sections, "## "+q+"\nNo results found.")
			continue
		}
		parts := make([]string, len(hits))
		for i, r := range hits {
			ts := ""
			if !r.at.IsZero() {
				ts = " | " + r.at.UTC().Format("2006-01-02 15:04")
			}
			parts[i] = fmt.Sprintf("--- [%s%s | %s] ---\n### %s\n\n%s",
				r.origin, ts, r.source, r.title, store.ExtractSnippet(r.content, q, searchSnippetMax, r.highlighted))
		}
		formatted := strings.Join(parts, "\n\n")
		sections = append(sections, "## "+q+"\n\n"+formatted)
		totalSize += jsLen(formatted)
	}
	output := strings.Join(sections, "\n\n---\n\n")

	if flood.count >= searchSoftCapAt {
		output += fmt.Sprintf("\n\n⚠ search call #%d/%d in this window. "+
			"Results limited to %d/query. %d call(s) remaining before block. "+
			`Batch queries: ctx_search(queries: ["q1","q2","q3"]) or use ctx_batch_execute.`,
			flood.count, searchBlockAfter, effectiveLimit, max(0, searchBlockAfter-flood.count))
	} else {
		output += fmt.Sprintf("\n\n> Throttle: call #%d/%d in this window. "+
			"%d call(s) before soft cap. "+
			"Prefer ctx_search(queries: [...]) array form for multi-query workloads — it counts as a single call.",
			flood.count, searchBlockAfter, max(0, searchSoftCapAt-flood.count))
	}
	// TS S:2785 falls back to "No results found. Indexed sources: …" when the
	// output is blank; the throttle line above makes that unreachable.
	return TextResult(output, false)
}

// searchHit is one formatted ctx_search result from any source (S:2739-2756).
type searchHit struct {
	origin, source, title, content, highlighted string
	at                                          time.Time
}

// searchHits runs one query. Source session-events returns the project's
// newest opts.Limit matching session events, newest first, in either mode.
// Relevance mode returns the content store's ranked results. Timeline mode (spec §3.2) takes the newest opts.Limit
// matches from the content store, the project's session events (all
// sessions) and, without a source filter, auto-memory; keeps the newest
// opts.Limit overall and returns them oldest first. TS kept the oldest.
func searchHits(d *Deps, q string, sa searchArgs, opts store.SearchOpts) ([]searchHit, error) {
	if sa.source == sessionEventsSource {
		evs, err := d.Store.SessionEventsForSearch(d.Cfg.ProjectKey, q, "", opts.Limit)
		if err != nil {
			return nil, err
		}
		hits := make([]searchHit, len(evs))
		for i, e := range evs {
			at, _ := time.Parse("2006-01-02 15:04:05", e.CreatedAt)
			hits[i] = searchHit{"prior-session", e.Category, "[" + e.Category + "] " + e.Type, e.Data, "", at}
		}
		return hits, nil
	}
	res, err := d.Store.SearchWithFallback(q, opts)
	if err != nil {
		return nil, err
	}
	var hits []searchHit
	for _, r := range res { // timeline mode returns these oldest first
		hits = append(hits, searchHit{"current-session", r.Source, r.Title, r.Content, r.Highlighted, r.IndexedAt})
	}
	if sa.sort != "timeline" {
		return hits, nil
	}
	evs, err := d.Store.SessionEventsForSearch(d.Cfg.ProjectKey, q, sa.source, opts.Limit)
	if err != nil {
		return nil, err
	}
	for _, e := range slices.Backward(evs) { // newest first from the store
		at, _ := time.Parse("2006-01-02 15:04:05", e.CreatedAt)
		// TS labels every session event prior-session, the current session's too (unified.ts:130).
		hits = append(hits, searchHit{"prior-session", "prior-session", "[" + e.Category + "] " + e.Type, e.Data, "", at})
	}
	if sa.source == "" {
		// Claude Code names its memory dir after the project dir as given, so ProjectDir, not ProjectKey.
		for _, m := range memory.SearchAutoMemory(q, opts.Limit, d.Cfg.ProjectDir, d.Cfg.ClaudeDir) {
			hits = append(hits, searchHit{"auto-memory", m.Source, m.Title, m.Content, "", m.Timestamp})
		}
	}
	// Stable: equal times (second resolution) keep source order, then id order.
	slices.SortStableFunc(hits, func(a, b searchHit) int { return a.at.Compare(b.at) })
	return hits[max(0, len(hits)-opts.Limit):], nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go vet ./... && go test ./... -count=1 && go test -race -count=1 ./internal/hookcmd/ ./internal/routing/ ./internal/install/ ./internal/memory/ ./internal/mcpserver/ ./cmd/cmgo/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver/search.go internal/mcpserver/search_memory_test.go
git commit -m "feat(mcpserver): answer ctx_search source session-events from session events"
```

---

## Appendix A: Porting notes by package

These notes are the deliberate choices made while drafting. Choices that change user-visible behavior are also in spec §2.4, §2.3 or §3. If a review disagrees with one, change the spec first, then the code.

### golden files (Task 1)

This fragment pins the TS session-memory builders with golden files (spec §5). `tools/golden-gen/gen-memory.mjs` runs by hand on Node against the TS fork at `$TS_REPO`, commit `a5e6dd31`, and never runs under `go test`. It imports `groupEvents` (`hooks/session-directive.mjs:54-73`), `writeSessionEventsFile` (`:77-272`) and `buildSessionDirective` (`:275-500`), and `buildAutoInjection` (`hooks/auto-injection.mjs:30-102`). It writes `testdata/golden/memory-directive.json` (20 cases) and `memory-autoinjection.json` (10 cases). cmgo does not port the events markdown (spec §2.3); the generator calls `writeSessionEventsFile` only because TS's directive reads its return value. The `internal/memory` golden tests (Tasks Task 3 and Task 4) read these files from `../../testdata/golden/`. `testdata/golden/MEMORY-KNOWN-DIFFS.md` lists the spec §2.4 fixes and every case they change. This task is Task 1 of the sub-project 3 plan; it has no Go code.

**Parity notes**
- **Two texts per case.** Each directive case holds `ts`, what TS ships, and `expected`, what cmgo must print. `ts` follows the production path: `buildSessionDirective` gets the return value of `writeSessionEventsFile`, which has no `legBoundary` (`hooks/sessionstart.mjs:205-206`, R3 headline 5). `expected` runs the same TS functions on `fixed()` input, so most fixed cases compare exactly instead of through a hand-written patch:
  - `user-prompt` rows become category `prompt`, so Last Request and `<continue_from>` read them (R3 headline 4).
  - `mcp` and `data` rows move to the categories `uncaptured-mcp` and `uncaptured-data`, which no section reads. The MCP and Data References sections disappear.
  - `created_at` becomes the zero-padded id, so TS's string compare of `created_at` is cmgo's id compare.
  - The directive gets `groupEvents()` output, so the leg boundary reaches it.
- **Known-differences gate.** The Go tests compare cmgo with `expected` for every case. They also check that `ts` differs from `expected` in exactly the cases listed in `MEMORY-KNOWN-DIFFS.md`, so a fix that stops mattering, or a difference nobody listed, fails.
- **Fixtures.** The fixtures are inline. They keep the R3 research fixtures: the 27-event fixture that reaches every section, the #780 leg fixture, the truncation fixture and the plan branches. They add:
  - `trunc-exact`: every limit exactly reached, so nothing is cut;
  - `trunc-astral`: limits count UTF-16 units, with an ASCII prefix so that no cut falls inside a surrogate pair (JS `substring` would split the pair, and `hookio.JSPrefix` stops before it);
  - `leg-tie`: a subagent in the same second as the last `session_start` but with a lower id, prior-leg only by id;
  - task matching, file basenames, rule paths, environment without cwd, the last intent and role, several prompts, `mcp`/`data` only, and `rule_content` rows, which the directive does not list.
- **Ids.** Every event has `id` = its position + 1, and the fixtures are fed in id order, as cmgo reads them. TS's same-second order (spec §2.4, last row) is therefore not reproduced; the table says so.
- **Empty directive.** `buildSessionDirective` on no events returns the bare shell, but TS never calls it then (`sessionstart.mjs:203`). cmgo's `Directive` returns `""`, so there is no empty directive case; Task 3 unit-tests it. `only-session-start` pins the shell.
- **Auto-injection.** No §2.4 fix applies, so its cases hold `expected` only. The TS fallback to 3 decisions (`auto-injection.mjs:79-85`) and the `budget > 50` skills check cannot trigger: the role costs at most 112 tokens and 5 decisions at most 139, out of 500. No fixture reaches them. `intent-dropped` reaches the `budget > 20` check.
- **Clean environment.** `HOME`, `TMPDIR` and `CONTEXT_MODE_DIR` point into a fresh temp dir before the dynamic imports. `writeSessionEventsFile` writes `events.md` there; the generator does not read it. The temp dir is removed at the end. Nothing is written to `~/.claude` or `$TS_REPO`. The two modules import only `node:fs` and keep no process state, so one process runs every case.
- **Tool name.** The namer is `t => "mcp__cmgo__" + t`, so the directive says `mcp__cmgo__ctx_search` (spec §2.4).

### internal/memory (Tasks 2-4)

Ports the two texts that SessionStart builds on `compact` and `resume` (spec §2.2-§2.4): the session directive `buildSessionDirective` (`hooks/session-directive.mjs:275-500`) and the auto-injection block `buildAutoInjection` (`hooks/auto-injection.mjs:30-102`). The directive reads one grouping pass, a port of `groupEvents` (`:54-73`) with the directive's task matching (`:300-334`). The Markdown that `writeSessionEventsFile` writes (`:77-272`) is not ported (spec §2.3): `ctx_search` answers `source: "session-events"` from the events table instead. The functions are pure: they take one session's `[]store.StoredEvent` in id order (`store.SessionEvents`) and return a string. `internal/hookcmd` calls them. The auto-memory search lives in the same package, in other files. Golden files come from Task 1.

**Parity notes (decided, flag in review if wrong):**
- **Spec §2.4 fixes.**
  - Last Request and `<continue_from>` read the data of the last `user-prompt` event. Category `prompt`, which capture never writes, is an ordinary category that no section reads.
  - An event is prior-leg when its id is lower than the id of the last `session_start` event. TS compared `created_at` strings of 1 s resolution, and its directive never got the boundary.
  - The MCP Tools Used and Data References sections are gone, with the Data References sizing (`:30-51`): capture writes no `mcp` or `data` events (hooks spec §3.4).
  - Rows are used in input order (id order); nothing re-sorts them.
- **Units.** Every length and cut counts UTF-16 units (`hookio.JSLen`, `hookio.JSPrefix`), as JS `length`, `substring` and `slice` do. `JSPrefix` stops before a surrogate pair where JS would split it; the goldens never cut inside a pair.
- **Task matching.** A row whose data `JSON.parse` rejects, or whose value is `null`, is skipped, as the directive's TS catch block does. `subject`, `taskId` and `status` are tested for JS truthiness. Subjects and ids go through `jsString` (a copy of capture's `jsString`, with the same shortcut: numbers keep their JSON text). Update ids keep first-seen order and are sorted stably by `Number(id)`. That equals TS `Object.keys(...).sort(...)` for the numeric ids Claude Code writes; non-canonical ids such as `"01"` or `"abc"` carry a `shortcut:` comment.
- **JS string semantics.** File basenames are trimmed with JS `trim()` (U+00A0 and U+FEFF included); `isJSSpace` and `jsTrim` are per-package copies, as in `internal/capture` and `internal/store`.
- **Auto-injection.** The TS fallback to the last 3 decisions (AI:79-85) is not ported: it cannot trigger, because the role block costs at most 112 tokens and 5 decisions at most 139, out of 500. A comment says so. The `budget > 50` and `budget > 20` checks are kept.
- **No XML escaping**, as in TS: user data is written into the tags verbatim (R3 port note 16 stays open).
- **Empty input.** `Directive` returns `""` for no events: TS left that check to its caller (`hooks/sessionstart.mjs:203`). `AutoInjection` returns `""` when no part is built.

### store, search and ctx_purge (Tasks 5-10)

Ports the session-memory read side of `ctx_search` and the `ctx_purge` tool. It covers the timeline branch of `searchAllSources` (`src/search/unified.ts:70-176`) and the `searchEvents` query it calls (`src/session/db.ts:1387-1421`). It covers `searchAutoMemory` (`src/search/auto-memory.ts:57-188`) and the `ctx_search` result format (`src/server.ts:2739-2756`). It restores the session-memory sentences of the `ctx_search` description (`src/server.ts:2564-2585`, `src/search/ctx-search-schema.ts:104-111`). It ports `ctx_purge` (`src/server.ts:4445-4611`, `src/session/purge.ts:178-338`) onto cmgo's single DB. The store also gains `CompactSession`, the write side of the PreCompact hook (`hooks/precompact.mjs:47-87`, without the snapshot). Spec: §2.1, §3.2-3.4 and §4 of `docs/superpowers/specs/2026-10-21-cmgo-session-memory-design.md`.

**Parity notes (decided, flag in review if wrong):**
- `SessionEventsForSearch` splits the query on JS whitespace. Each term must occur in `data` or in `category` (SQLite `LIKE`, ASCII case folding, `%` and `_` escaped). TS matched the whole query as one substring. A query with no terms matches every row of the project, as TS's `LIKE '%%'` did.
- Session events are scoped by `project = cfg.ProjectKey`, across all sessions. TS also took rows with `project_dir = ''`; cmgo never writes an empty project.
- The newest rows are selected (`ORDER BY id DESC`). TS took the oldest, both in SQL and in the merge (spec §3.2).
- The timeline merge collects up to `effectiveLimit` rows from each source in this order: content store (already oldest first), session events (reversed to oldest first), then auto-memory when `source` is empty. It stable-sorts by time and keeps the last `effectiveLimit`. Equal times (all three sources have one-second resolution) keep that source order, and session events keep id order.
- The content-store leg is the existing timeline path: the top `effectiveLimit` by relevance, then sorted by index time. Only the session-event and auto-memory legs pick the newest matches. Spec §3.2 asks for the existing path.
- The restored `sort` schema text and the description's "only ranks within the current session" are TS wording. In cmgo, relevance mode ranks the project's content store, which includes the `session-events` label. The wording is kept verbatim per spec §3.4.
- Header fields follow TS. Session events show `prior-session` as both origin and source, even for the current session (`unified.ts:130`), with title `[<category>] <type>`. Auto-memory shows origin `auto-memory`, its label as the source, and title `[auto-memory] <label>`.
- All timestamps print in UTC as `YYYY-MM-DD HH:MM`: `sources.indexed_at`, `session_events.created_at` and the file mtime. TS printed `mtime.toISOString().slice(0,16)`, which is UTC too.
- Relevance mode is unchanged and does not read session events. They reach it only through the `session-events` source label (spec §3.1).
- Auto-memory reads `<home>/.claude/CLAUDE.md` and `<home>/.claude/projects/<ProjectSlug(ProjectDir)>/memory/*.md`, sorted by name. `CLAUDE_CONFIG_DIR` is not consulted, because cmgo's config has only `Home`.
- `ProjectDir`, not `ProjectKey`, feeds the slug and `<project>/CLAUDE.md`, because `ProjectKey` is lowercased on darwin. The user file is skipped when the project dir is `<home>/.claude` (AM:103).
- Auto-memory snippet windows count UTF-16 units, so a cut through a surrogate pair becomes U+FFFD, as V8 writes it. Expected values in the tests come from node running the AM code.
- Go `strings.ToLower` is used for JS `toLowerCase`. They differ only on special casings (U+0130, final sigma).
- RE2 `(?i)` folds U+017F and U+212A, which JS `/i` without `u` does not. `\b` is ASCII-only in both.
- `CompactSession` writes nothing when the session has no events: TS `precompact.mjs:47` skips the whole step, and the core no-idle-writes rule holds. The `snapshot-built` event is not written, because the snapshot is out of scope.
- `PurgeProject(P)` deletes the events whose `project` is P and every event of a session whose meta row names P. A session owned by another project keeps its rows of other projects.
- `ctx_purge`:
  - `confirm` is not `required` in the schema, so a missing `confirm` gets the cancel message (spec §4).
  - The ambiguity check runs before the confirm check, as in TS.
  - `scope: "session"` without `sessionId` returns the TS `purgeSession` TypeError text as a tool error.
  - An unknown `scope` is an `Invalid arguments` error, the zod enum's job in TS.
- `ctx_purge` messages:
  - The session message lists `session events`, or `no matching rows` when nothing was deleted. TS listed `session rows for <id>` and `FTS5 chunks for <id>`.
  - The project message is the fixed spec §4 text.
  - No stderr deprecation warning is written for a bare `{confirm:true}`, and the description no longer claims one.
- The restored `ctx_search` description keeps TS's wording and label list. It drops `ctx_index` (not a cmgo tool), `ctx_stats`, the `CONTEXT_MODE_*` env vars, file staleness and the "26 event categories" count, because cmgo captures fewer categories.

### hooks, routing, install, bench (Tasks 11-18)

Ports the hook side of session memory. `cmgo-hook precompact` records the compaction the way `hooks/precompact.mjs:47-99` does, without the snapshot. SessionStart `compact` and `resume` add the session directive and auto-injection, as the two branches of `hooks/sessionstart.mjs:191-295` do, but build and index no events markdown (spec §2.3). Instead, `ctx_search` answers `source: "session-events"` from the events table (spec §3.1). The routing block's `<ctx_commands>` section (`hooks/routing-block.mjs:62-77`) comes back with the `ctx purge` entry only, and SessionStart now includes it. `install` registers `PreCompact` (`hooks/hooks.json:15`). Auto-memory moves from `<home>/.claude` to Claude Code's config dir (`config.Config.ClaudeDir`). The bench gains the two session-memory cases. Spec: §1, §2.1-2.3, §2.5, §3.1, §3.3 and §5 of `docs/superpowers/specs/2026-10-21-cmgo-session-memory-design.md`.

**Parity notes (decided, flag in review if wrong):**
- `SearchAutoMemory`'s last parameter is now `claudeDir` instead of `home`. It reads `<claudeDir>/CLAUDE.md` and `<claudeDir>/projects/<slug>/memory/*.md`, and `ctx_search` passes `cfg.ClaudeDir`, which honors `$CLAUDE_CONFIG_DIR` (spec §3.3). The AM:103 duplicate guard now compares `projectDir` with `claudeDir`.
- `RoutingBlock(true, false)` is the TS block with the `ctx stats`, `ctx doctor` and `ctx upgrade` entries cut: each is two lines and the blank line after them. The `ctx purge` entry and the closing "After /clear or /compact" line stay byte for byte. `testdata/golden/hooks-strings.json` keeps the full TS text. `TestGoldenStrings` derives the cmgo text from it with `dropCommands`, which cuts every name in `missingCommands`. Sub-projects 4 and 5 delete their names from that list.
- SessionStart uses `RoutingBlock(true, false)` for every source (spec §2.5). TS on Claude Code also printed the commands section.
- `PreCompact` is appended after `Stop` in `HookEvents`, so the hooks spec §3.5 order of the first six stays as it is. TS `hooks.json` lists it second, but no code depends on the order.
- `precompact` prints `{}` and a newline first, then calls `CompactSession`. TS prints after its try/catch, so a DB error never swallows the output. `CompactSession` itself skips a session with no events. The store is opened even then, so a first `precompact` creates `cmgo.db`, as TS's `new SessionDB` did.
- SessionStart `compact` and `resume` read the session's events (`store.SessionEvents`, id order) before printing, because the directive needs them. If the open or the read fails, they print the routing block alone and log the error (fail-open). The inserts run after the output, in one store handle, under the 2 s hook busy timeout.
- With events, `compact` appends `Directive("compact")`, then `"\n\n" + AutoInjection` when that is non-empty. `resume` appends `Directive("resume")` only. The directive is appended with no separator, as TS `+=` does; it starts with its own newline.
- `resume_completed` (`session-resume`, priority 1) is written only by `compact` and only when the session has events, as in TS, where it sits inside `if (events.length > 0)`. TS's `snapshot-consumed` event is not written, because the snapshot is out of scope. `session_start` with the source is written on every `compact` and `resume`, as TS `emitSessionStartLifecycle` runs outside that guard. Both rows go in one `InsertEvents` call with source hook `SessionStart`.
- `resume` with no events does not fall back to a snapshot (TS `claimLatestUnconsumedResume`), because there is no snapshot table (spec §6).
- `clear` and unknown sources print the routing block and do not open the store.
- No events markdown (spec §2.3). TS wrote `writeSessionEventsFile` and the MCP server indexed it as `session-events`. Indexing that dump cost 3-4 ms of the hook and put `sessionstart compact` on 1000 events at p95 15.5 ms. Without it the case measures p95 8.8 ms (Task 16).
- `ctx_search` with `source` exactly `session-events` (Task 18) reads `store.SessionEventsForSearch(project, query, "", limit)` in both sort modes. It skips the content store and auto-memory, keeps the store's newest-first order, and prints the category as the header's source: `--- [prior-session | <time> | <category>] ---`. The timeline merge prints `prior-session` there instead, as TS `unified.ts:130` does. `limit` is the throttled `effectiveLimit`, as for the other sources. The empty-knowledge-base error does not apply. Any other label, `session-event` included, keeps the content-store path.
## Appendix B: Verification (2026-10-21, macOS arm64)

**Replay of this plan** (`replay_plan2.py`): each task's files were written into a fresh worktree of `sp2-hooks` (sub-projects 1-2 present), then `go vet ./...` and that task's package tests were run. All 18 tasks were `ok`. Final step: `go test ./... && go test -race ./...`.

```
FINAL ok
ok  	github.com/lang315/context-mode-go/cmd/cmgo	4.967s
ok  	github.com/lang315/context-mode-go/cmd/cmgo-hook	0.606s
ok  	github.com/lang315/context-mode-go/internal/capture	1.756s
ok  	github.com/lang315/context-mode-go/internal/config	0.271s
ok  	github.com/lang315/context-mode-go/internal/executor	15.138s
ok  	github.com/lang315/context-mode-go/internal/fetch	1.142s
ok  	github.com/lang315/context-mode-go/internal/hookcmd	10.225s
ok  	github.com/lang315/context-mode-go/internal/hookio	1.514s
ok  	github.com/lang315/context-mode-go/internal/install	4.928s
ok  	github.com/lang315/context-mode-go/internal/mcpserver	6.127s
ok  	github.com/lang315/context-mode-go/internal/memory	2.162s
ok  	github.com/lang315/context-mode-go/internal/routing	1.995s
ok  	github.com/lang315/context-mode-go/internal/security	1.685s
ok  	github.com/lang315/context-mode-go/internal/store	4.092s
ok  	github.com/lang315/context-mode-go/cmd/cmgo	11.227s
ok  	github.com/lang315/context-mode-go/cmd/cmgo-hook	1.609s
ok  	github.com/lang315/context-mode-go/internal/capture	1.819s
ok  	github.com/lang315/context-mode-go/internal/config	2.011s
ok  	github.com/lang315/context-mode-go/internal/executor	16.135s
ok  	github.com/lang315/context-mode-go/internal/fetch	3.370s
ok  	github.com/lang315/context-mode-go/internal/hookcmd	11.214s
ok  	github.com/lang315/context-mode-go/internal/hookio	2.836s
ok  	github.com/lang315/context-mode-go/internal/install	6.964s
ok  	github.com/lang315/context-mode-go/internal/mcpserver	15.099s
ok  	github.com/lang315/context-mode-go/internal/memory	3.081s
ok  	github.com/lang315/context-mode-go/internal/routing	3.139s
ok  	github.com/lang315/context-mode-go/internal/security	3.142s
ok  	github.com/lang315/context-mode-go/internal/store	17.717s
```

`go test ./... -count=1 -v`: 379 `--- PASS`, 0 `--- FAIL`. The php, r, elixir and csharp subtests of `TestRunHelloEachLanguage` skip, because those runtimes are not installed. `diff -rq` of the replayed tree against the draft showed no differences.

**Hook latency** (the Task 16 bench script, run on the draft binaries):

```
load average (1 min) at start: 5.35
context /usr/bin/true: p50 2.01 ms p95 2.77 ms
context cmgo-hook, no args: p50 4.61 ms p95 5.28 ms
pretooluse passthrough: p50 4.75 ms p95 5.92 ms PASS
pretooluse curl redirect: p50 6.61 ms p95 8.44 ms PASS
posttooluse Read capture: p50 6.19 ms p95 7.97 ms PASS
userpromptsubmit: p50 6.26 ms p95 7.53 ms PASS
sessionstart startup: p50 6.50 ms p95 7.84 ms PASS
precompact (1000 events): p50 6.56 ms p95 7.64 ms PASS
sessionstart compact (1000 events): p50 7.40 ms p95 8.80 ms PASS
posttooluse x4 concurrent: p50 7.56 ms p95 9.56 ms PASS
load average (1 min) at end: 4.26
PASS
```

The machine was shared with other sessions during the run.

**What the replay did not exercise:**
- **The fail-first steps:** no task's Step 2 was run in this replay. The drafting agents recorded each "Expected: FAIL" line from a real run of their own task-by-task replay. Tasks 16 and 17 add tests or a bench case that pass when first written; their Step 2 says so.
- **The Node generator:** Task 1 was not re-run; its two JSON files were copied from the draft. It had been run twice, with identical md5s both times (`memory-autoinjection.json` 41796614a4b90e665e8eefe1ee1192ab, `memory-directive.json` dc4783e4343795acab8e8414d207d17c).
- **Live Claude Code:** compaction and resume were not tried in a live session.
