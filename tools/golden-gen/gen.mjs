#!/usr/bin/env node
// Golden parity generator for cmgo (spec §5.2). Run by hand, never by go test:
//
//   TS_REPO=/path/to/context-mode node tools/golden-gen/gen.mjs
//
// Reads testdata/corpus/ and writes testdata/golden/{chunks,sanitize,search,
// snippets,tools,html}.json from the TS fork at $TS_REPO (commit a5e6dd31).
// Everything the TS code writes goes to a fresh temp dir: HOME, TMPDIR,
// CONTEXT_MODE_DIR, CLAUDE_PROJECT_DIR and the server cwd all point there.
// Nothing is written to ~/.claude or $TS_REPO; start.mjs/postinstall never run.
import { spawn, spawnSync, execFileSync } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const SELF = fileURLToPath(import.meta.url);
const ROOT = resolve(dirname(SELF), "../..");
const CORPUS = join(ROOT, "testdata/corpus");
const OUT = join(ROOT, "testdata/golden");
const TS_REPO = resolve(process.env.TS_REPO || "/Users/lang/GolandProjects/github.com/lang315/context-mode");
const TS_COMMIT = "a5e6dd31";

const writeJSON = (name, v) => writeFileSync(join(OUT, name), JSON.stringify(v, null, 2) + "\n");

// Corpus files in a fixed order; the Go tests index them in this same order.
function corpusFiles() {
  const kinds = { md: "markdown", logs: "plain", json: "json" };
  const files = [];
  for (const dir of Object.keys(kinds)) {
    for (const f of readdirSync(join(CORPUS, dir)).sort()) {
      files.push({ path: `${dir}/${f}`, kind: kinds[dir] });
    }
  }
  return files;
}

// ─────────────────────────────────────────────────────────
// Store phase (runs under tsx: imports $TS_REPO/src/store.ts)
// ─────────────────────────────────────────────────────────

// [id, query, opts]; opts: limit, source, contentType, mode ("like" | "exact").
const SEARCH_CASES = [
  ["q01", "connection pool"],
  ["q02", "max_connections"],
  ["q03", "idle_timeout connect_timeout"],
  ["q04", "rate limiter"],
  ["q05", "Retry-After"],
  ["q06", "heartbeat worker"],
  ["q07", "rollback deploy"],
  ["q08", "connections"],
  ["q09", "configuring"],
  ["q10", "Middlewa"],
  ["q11", "TestPoolTimeout"],
  ["q12", "nil pointer dereference"],
  ["q13", "upstream connection reset"],
  ["q14", "status=429"],
  ["q15", "café"],
  ["q16", "データベース"],
  ["q17", "😀"],
  ["q18", "Ada Lovelace"],
  ["q19", "admin"],
  ["q20", "src/pool"],
  ["q21", "the and of"],
  ["q22", "conection timout"],
  ["q23", "hearbeat"],
  ["q24", "authentcation"],
  ["q25", "zzzz qqqq"],
  ["q26", "quick brown fox"],
  ["q27", "sentinelword"],
  ["q28", "EXPLAIN ANALYZE"],
  ["q29", "pool", { source: "md/0" }],
  ["q30", "pool", { contentType: "code" }],
  ["q31", "connection", { limit: 5 }],
  ["q32", "connection", { source: "logs/sections.log", mode: "exact" }],
  ["q33", "ECONNREFUSED"],
  ["q34", "\"quoted phrase\" (parens) AND NOT pool"],
  ["q35", "a b"],
  ["q36", "webhook", { limit: 1 }],
];

const EXTRA_SANITIZE = [
  "", "   ", "foo:bar baz^2 ~fuzzy", "NEAR near Near", "Error error ERROR",
  "x y zz", "café's naïve", "path/to/file.go", "a-b_c d.e", "(nested (parens))",
];

const SNIPPET_MAXES = [200, 1500, 3000];

async function storePhase(tmp) {
  const { ContentStore, sanitizeQuery, sanitizeTrigramQuery } = await import(pathToFileURL(join(TS_REPO, "src/store.ts")).href);

  // extractSnippet lives in server.ts and is not reachable without starting
  // the server, so copy its source slice (positionsFromHighlight,
  // stripMarkers, extractSnippet) into a temp module.
  const server = readFileSync(join(TS_REPO, "src/server.ts"), "utf8");
  const start = server.indexOf('const STX = "\\x02";');
  const end = server.indexOf("export type BatchQueryScope");
  if (start < 0 || end < start) throw new Error("extractSnippet slice anchors not found in server.ts");
  const snippetFile = join(tmp, "snippet.ts");
  writeFileSync(snippetFile, server.slice(start, end));
  const { extractSnippet } = await import(pathToFileURL(snippetFile).href);

  const store = new ContentStore(join(tmp, "store.db"));
  const files = corpusFiles();
  const chunkFiles = [];
  for (const f of files) {
    const text = readFileSync(join(CORPUS, f.path), "utf8");
    const r = f.kind === "markdown" ? store.index({ content: text, source: f.path })
      : f.kind === "plain" ? store.indexPlainText(text, f.path)
      : store.indexJSON(text, f.path);
    const chunks = store.getChunksBySource(r.sourceId).map((c) => ({
      title: c.title, content: c.content, hasCode: c.contentType === "code",
    }));
    chunkFiles.push({ ...f, chunks, terms: store.getDistinctiveTerms(r.sourceId) });
  }
  writeJSON("chunks.json", { tsCommit: TS_COMMIT, files: chunkFiles });

  const sanitize = [];
  for (const q of [...SEARCH_CASES.map((c) => c[1]), ...EXTRA_SANITIZE]) {
    sanitize.push({ in: q, porter: sanitizeQuery(q, "OR"), trigram: sanitizeTrigramQuery(q, "OR") });
  }
  writeJSON("sanitize.json", { tsCommit: TS_COMMIT, cases: sanitize });

  const cases = [];
  const snippets = [];
  for (const [id, query, o = {}] of SEARCH_CASES) {
    const limit = o.limit ?? 3;
    const rs = store.searchWithFallback(query, limit, o.source, o.contentType, o.mode ?? "like");
    cases.push({
      id, query, limit, source: o.source ?? "", contentType: o.contentType ?? "", mode: o.mode ?? "like",
      results: rs.map((r) => ({ key: `${r.source}::${r.title}`, contentType: r.contentType, highlighted: r.highlighted ?? "" })),
    });
    rs.forEach((r, i) => {
      const s = {};
      for (const max of SNIPPET_MAXES) s[max] = extractSnippet(r.content, query, max, r.highlighted);
      snippets.push({ id: `${id}#${i}`, query, content: r.content, highlighted: r.highlighted ?? "", snippets: s });
    });
  }
  writeJSON("search.json", { tsCommit: TS_COMMIT, files, cases });
  writeJSON("snippets.json", { tsCommit: TS_COMMIT, cases: snippets });
  store.close();
}

// ─────────────────────────────────────────────────────────
// Server phase: drive $TS_REPO/server.bundle.mjs over stdio JSON-RPC
// ─────────────────────────────────────────────────────────

// Masks applied to every tool text, here and in the Go test (read from tools.json).
const MASKS = [
  { re: "\\d{4}-\\d{2}-\\d{2} \\d{2}:\\d{2}", to: "<DATE>" },
  { re: "\\d+ms", to: "<N>ms" },
  { re: "\\d+\\.\\dKB", to: "<N.N>KB" },
  { re: "search calls in \\d+s", to: "search calls in <N>s" },
];
const mask = (s) => MASKS.reduce((acc, m) => acc.replace(new RegExp(m.re, "g"), m.to), s);

// Tool calls replayed in this order by internal/mcpserver/golden_test.go, one
// fresh server (and store, and search flood guard) per session.
// Batch commands read the corpus through relative paths (cwd = project dir),
// so the echoed "$ cmd" lines are identical in both implementations.
const TOOL_SESSIONS = [[
  ["t01", "ctx_search", { queries: ["pool"] }],
  ["t02", "ctx_batch_execute", {
    commands: [
      { label: "getting started", command: "cat corpus/md/01-getting-started.md" },
      { label: "troubleshooting", command: "cat corpus/md/09-troubleshooting.md" },
      { label: "unicode", command: "cat corpus/md/08-unicode.md" },
    ],
    queries: ["connection refused", "max_connections", "café", "zzzz qqqq"],
  }],
  ["t03", "ctx_batch_execute", {
    commands: [
      { label: "mixed streams", command: "echo stdout line; echo stderr line >&2" },
      { label: "silent", command: "true" },
      { label: "failing", command: "false" },
      { label: "stderr only", command: "echo only-err >&2" },
      { label: "long command", command: "echo" + " word   spaced".repeat(45) },
    ],
    queries: ["stderr line", "spaced"],
  }],
  ["t04", "ctx_batch_execute", {
    commands: [
      { label: "changelog", command: "cat corpus/md/10-changelog.md" },
      { label: "oversized", command: "cat corpus/md/05-oversized.md" },
    ],
    queries: ["pool", "connection ceiling", "rollback"],
    concurrency: 2,
  }],
  ["t05", "ctx_batch_execute", {
    commands: [{ label: "test log", command: "cat corpus/logs/sections.log" }],
    queries: ["connection refused", "TestPoolTimeout"],
    query_scope: "global",
  }],
  ["t06", "ctx_batch_execute", {
    commands: JSON.stringify([{ label: "api", command: "cat corpus/md/02-api-reference.md" }]),
    queries: "Pool.Get",
  }],
  ["t07", "ctx_search", { queries: ["connection pool", "rate limiter", "zzzz qqqq"] }],
  ["t08", "ctx_search", { queries: ["pool"], source: "changelog" }],
  ["t09", "ctx_search", { queries: ["Pool"], contentType: "code", limit: 5 }],
  ["t10", "ctx_search", { queries: [] }],
  ["t11", "ctx_search", { queries: ["deploy"], sort: "timeline" }],
  ["t12", "ctx_search", { queries: ["café", "データベース"] }],
  ["t13", "ctx_search", { queries: ["heartbeat"] }],
  ["t14", "ctx_search", { queries: ["webhook"] }],
  ["t15", "ctx_search", { queries: ["pool"] }],
  ["t16", "ctx_search", { queries: ["pool"] }],
], [
  // Own session: if the legacy `query` call is counted by the flood guard in
  // one implementation and not the other, later throttle lines would all shift.
  ["t17", "ctx_batch_execute", {
    commands: [{ label: "changelog", command: "cat corpus/md/10-changelog.md" }],
    queries: ["breaking"],
  }],
  ["t18", "ctx_search", { query: "rollback" }],
  ["t19", "ctx_search", { queries: ["migration guide"] }],
]];

function rpcClient(child) {
  let buf = "";
  let nextId = 1;
  const pending = new Map();
  child.stdout.setEncoding("utf8");
  child.stdout.on("data", (d) => {
    buf += d;
    let nl;
    while ((nl = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, nl).trim();
      buf = buf.slice(nl + 1);
      if (!line) continue;
      let msg;
      try { msg = JSON.parse(line); } catch { continue; }
      if (msg.id !== undefined && pending.has(msg.id)) {
        pending.get(msg.id)(msg);
        pending.delete(msg.id);
      }
    }
  });
  const send = (m) => child.stdin.write(JSON.stringify(m) + "\n");
  return {
    request(method, params) {
      const id = nextId++;
      return new Promise((res, rej) => {
        const timer = setTimeout(() => rej(new Error(`timeout waiting for ${method}`)), 60_000);
        pending.set(id, (m) => { clearTimeout(timer); m.error ? rej(new Error(JSON.stringify(m.error))) : res(m.result); });
        send({ jsonrpc: "2.0", id, method, params });
      });
    },
    notify(method, params) { send({ jsonrpc: "2.0", method, params }); },
  };
}

async function serverSession(tmp, session, toolSteps) {
  const project = join(tmp, "project");
  mkdirSync(project, { recursive: true });
  cpSync(CORPUS, join(project, "corpus"), { recursive: true });
  const env = {
    PATH: process.env.PATH,
    SHELL: process.env.SHELL || "/bin/bash",
    LANG: "en_US.UTF-8",
    HOME: tmp,
    TMPDIR: tmp,
    CONTEXT_MODE_DIR: join(tmp, "context-mode"),
    CLAUDE_PROJECT_DIR: project,
  };
  const child = spawn(process.execPath, [join(TS_REPO, "server.bundle.mjs")], {
    cwd: project, env, stdio: ["pipe", "pipe", "ignore"],
  });
  const rpc = rpcClient(child);
  try {
    await rpc.request("initialize", {
      protocolVersion: "2025-06-18", capabilities: {}, clientInfo: { name: "golden-gen", version: "0" },
    });
    rpc.notify("notifications/initialized", {});
    const { tools } = await rpc.request("tools/list", {});
    for (const need of ["ctx_batch_execute", "ctx_search"]) {
      if (!tools.some((t) => t.name === need)) throw new Error(`server did not list ${need}`);
    }
    const steps = [];
    for (const [id, tool, args] of toolSteps) {
      const r = await rpc.request("tools/call", { name: tool, arguments: args });
      const text = (r.content || []).filter((c) => c.type === "text").map((c) => c.text).join("");
      steps.push({ id, session, tool, args, isError: !!r.isError, text: mask(text) });
    }
    return steps;
  } finally {
    child.stdin.end();
    const exited = new Promise((r) => child.once("exit", r));
    const timer = setTimeout(() => child.kill("SIGKILL"), 5000);
    await exited;
    clearTimeout(timer);
  }
}

// ─────────────────────────────────────────────────────────
// HTML phase: Turndown with the options of buildFetchCode (S:3098-3101)
// ─────────────────────────────────────────────────────────

function htmlPhase() {
  const require = createRequire(join(TS_REPO, "package.json"));
  const TurndownService = require("turndown");
  const { gfm } = require("turndown-plugin-gfm");
  const cases = [];
  for (const f of readdirSync(join(CORPUS, "html")).sort()) {
    const td = new TurndownService({ headingStyle: "atx", codeBlockStyle: "fenced" });
    td.use(gfm);
    td.remove(["script", "style", "nav", "header", "footer", "noscript"]);
    cases.push({ path: `html/${f}`, markdown: td.turndown(readFileSync(join(CORPUS, "html", f), "utf8")) });
  }
  writeJSON("html.json", { tsCommit: TS_COMMIT, cases });
}

// ─────────────────────────────────────────────────────────

async function main() {
  const phase = process.argv.find((a) => a.startsWith("--phase="))?.slice(8);
  if (phase === "store") return storePhase(process.argv[process.argv.length - 1]);

  const head = execFileSync("git", ["-C", TS_REPO, "rev-parse", "HEAD"], { encoding: "utf8" }).trim();
  if (!head.startsWith(TS_COMMIT)) throw new Error(`TS_REPO is at ${head}, want ${TS_COMMIT}`);
  mkdirSync(OUT, { recursive: true });
  const tmp = mkdtempSync(join(tmpdir(), "cmgo-golden-"));
  try {
    const env = { ...process.env, HOME: tmp, TMPDIR: tmp };
    const r = spawnSync(join(TS_REPO, "node_modules/.bin/tsx"), [SELF, "--phase=store", tmp], { env, stdio: "inherit" });
    if (r.status !== 0) throw new Error(`store phase failed (status ${r.status})`);
    const steps = [];
    for (const [i, toolSteps] of TOOL_SESSIONS.entries()) {
      const dir = join(tmp, `session${i + 1}`);
      mkdirSync(dir);
      steps.push(...await serverSession(dir, i + 1, toolSteps));
    }
    writeJSON("tools.json", { tsCommit: TS_COMMIT, masks: MASKS, steps });
    htmlPhase();
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
  console.log(`golden files written to ${OUT}`);
}

main().catch((e) => { console.error(e); process.exit(1); });
