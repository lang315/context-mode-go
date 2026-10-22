#!/usr/bin/env python3
"""cmgo effectiveness audit. Read-only on ~/.claude. Definitions: METHOD.md.

    python3 -I cmgo_audit.py [--since 2026-09-30] [--out DIR]

Writes report.md and report.json into --out (default $CMGO_AUDIT_OUT, else ~/.cache/cmgo-audit),
so nothing lands in the repo tree. Per-file transcript summaries are cached there too
(keyed by size+mtime), so a re-run only re-reads transcripts that changed.
Read-only on ~/.claude: the DB and its WAL are copied to a temp dir and opened there.
"""
import argparse, collections, datetime, importlib.util, json, os, pickle, re, shutil, sqlite3, statistics, sys, tempfile

sys.dont_write_bytecode = True  # importing subagent_probe must not drop __pycache__ into the repo

HOME = os.path.expanduser("~")
ROOT = os.path.join(HOME, ".claude", "projects")
DB = os.path.join(HOME, ".claude", "cmgo", "cmgo.db")
HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_OUT = os.environ.get("CMGO_AUDIT_OUT") or os.path.join(HOME, ".cache", "cmgo-audit")


def load_probe():
    spec = importlib.util.spec_from_file_location("subagent_probe", os.path.join(HERE, "subagent_probe.py"))
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod

ERA_CMGO = "2026-10-21T08:00:00"      # first cmgo tool_stats row (server start), UTC
PY_COUNTERS = "2026-10-22 05:36:00"   # PR #12 installed 12:36 local
UNMEAS_COL = "2026-10-22 06:30:00"    # migration 4 installed 13:30 local
TEST_WINDOW = "5f9ecd0a"              # context-mode-06 test session (2.79 MB call)
BIG = 5000
FAMILY = {"mcp__cmgo__": "cmgo", "mcp__plugin_context-mode_context-mode__": "ts"}
RAW_TOOLS = {"Bash", "Read", "Grep", "WebFetch"}
CLASS_A = re.compile(r"^\s*(?:cd\s+\S+\s*&&\s*)?(cat|nl|tac|bat|head|tail)\b")


def fam(name):
    for p, f in FAMILY.items():
        if name.startswith(p + "ctx_"):
            return f
    return None


def is_test_project(proj):
    p = proj.lower()
    return "scratchpad" in p or "-e2e" in p or p.startswith("-private-tmp")


def text_of(content):
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "".join(x.get("text", "") for x in content if isinstance(x, dict))
    return ""


# ---------------------------------------------------------------- transcripts
HOOK_KEYS = ('"hook_success"', '"hook_non_blocking_error"', '"hook_cancelled"')


def hook_family(cmd):
    if "cmgo-hook" in cmd:
        return "cmgo"
    if "context-mode" in cmd:
        return "ts"
    return None


def scan(path, since):
    rel = os.path.relpath(path, ROOT).split(os.sep)
    r = dict(project=rel[0], sub="subagents" in rel, session=os.path.splitext(rel[-1])[0] if "subagents" not in rel else rel[1],
             first=None, last=None, req=[], comp=[], seq=[], big=[], bash_n=0, bash_classA16=0,
             ctx_res=[], cmgo_err=[], hooks=[], hook_err=[], langs=collections.Counter(), reads=[], redirects=[])
    names, ids, reqs = {}, {}, {}
    for line in open(path, errors="replace"):
        has_tool = '"tool_use"' in line or '"tool_result"' in line
        has_hook = any(k in line for k in HOOK_KEYS) and ("cmgo-hook" in line or "context-mode" in line)
        is_asst = '"type":"assistant"' in line[:400] and '"usage"' in line
        is_comp = '"compact_boundary"' in line
        if not (has_tool or has_hook or is_asst or is_comp):
            continue
        try:
            o = json.loads(line)
        except ValueError:
            continue
        ts = o.get("timestamp") or ""
        if ts and ts < since:
            continue
        if ts:
            r["first"] = r["first"] or ts
            r["last"] = ts
        if is_comp and o.get("subtype") == "compact_boundary":
            r["comp"].append(ts)
            continue
        a = o.get("attachment")
        if isinstance(a, dict) and a.get("type", "").startswith("hook_"):
            f = hook_family(a.get("command", ""))
            if not f:
                continue
            t = a["type"]
            if t == "hook_success":
                so = a.get("stdout", "") or ""
                kind = "empty"
                if so.strip() not in ("", "{}"):
                    if '"permissionDecision":"deny"' in so.replace(" ", "") or '"decision":"block"' in so.replace(" ", ""):
                        kind = "deny"
                    elif "context_guidance" in so and ("This command prints about" in so or "KB of output (" in so):
                        kind = "bashread"
                    elif "context_guidance" in so:
                        kind = "guidance"
                    else:
                        kind = "other"
                r["hooks"].append((ts, a.get("hookName", ""), f, kind, int(a.get("durationMs") or 0), int(a.get("exitCode") or 0),
                                   ids.get(a.get("toolUseID"))))
            else:
                r["hook_err"].append((ts, a.get("hookName", ""), f, t, int(a.get("exitCode") or 0), (a.get("stderr") or "")[:160]))
            continue
        m = o.get("message")
        if not isinstance(m, dict):
            continue
        if o.get("type") == "assistant":
            u = m.get("usage")
            rid = o.get("requestId")
            tok = 0
            if isinstance(u, dict):
                tok = (u.get("input_tokens") or 0) + (u.get("cache_creation_input_tokens") or 0) + (u.get("cache_read_input_tokens") or 0)
            if rid:
                if rid not in reqs:
                    reqs[rid] = [ts, tok]
                    r["req"].append(reqs[rid])
                else:
                    reqs[rid][1] = max(reqs[rid][1], tok)
        c = m.get("content")
        if not isinstance(c, list):
            continue
        for x in c:
            if not isinstance(x, dict):
                continue
            if x.get("type") == "tool_use":
                n = x.get("name", "")
                names[x["id"]] = (n, x.get("input") or {})
                ids[x["id"]] = len(r["seq"])
                r["seq"].append((ts, n, (x.get("input") or {}).get("file_path", "") if n == "Read" else ""))
                if fam(n) and n.endswith("ctx_execute"):
                    r["langs"][(fam(n), (x.get("input") or {}).get("language", "?"))] += 1
            elif x.get("type") == "tool_result":
                n, inp = names.get(x.get("tool_use_id"), ("", {}))
                txt = text_of(x.get("content"))
                z = len(txt.encode("utf-8", "replace"))
                if n == "Read":
                    r["reads"].append((ts, inp.get("file_path", ""), z, ids.get(x.get("tool_use_id"))))
                head = txt.lstrip()[:200]
                if n in ("Bash", "WebFetch") and "redirected" in txt[:700] and (
                        (head.startswith("PreToolUse:") and "hook error: context-mode:" in head) or head.startswith("context-mode:")):
                    r["redirects"].append((ts, "cmgo" if "mcp__cmgo__" in txt[:700] else "ts" if "plugin_context-mode" in txt[:700] else "?", n, ids.get(x.get("tool_use_id"))))
                if n in RAW_TOOLS:
                    cmd = inp.get("command", "") if n == "Bash" else ""
                    if n == "Bash":
                        r["bash_n"] += 1
                        if z >= 16384 and CLASS_A.match(cmd):
                            r["bash_classA16"] += 1
                    if z > BIG:
                        spill = n == "Read" and bool(re.search(r"tool-results|/scratchpad/", inp.get("file_path", "")))
                        r["big"].append((ts, n, z, spill))
                f = fam(n)
                if f:
                    r["ctx_res"].append((ts, f, n.split("__")[-1], z, bool(x.get("is_error"))))
                if f == "cmgo" and x.get("is_error"):
                    r["cmgo_err"].append((ts, n, txt[:200]))
    return r


def load_transcripts(since, out):
    os.makedirs(out, exist_ok=True)
    cp = os.path.join(out, ".cache.pkl")
    try:
        cache = pickle.load(open(cp, "rb"))
    except Exception:
        cache = {}
    key_ = since + "|scan-v2"  # bump when scan() output changes
    if cache.get("_since") != key_:
        cache = {"_since": key_}
    out, n_new = [], 0
    cutoff = datetime.datetime.fromisoformat(since).timestamp()
    for dp, _, fs in os.walk(ROOT):
        for f in fs:
            if not f.endswith(".jsonl"):
                continue
            p = os.path.join(dp, f)
            st = os.stat(p)
            if st.st_mtime < cutoff:
                continue
            key = (st.st_size, st.st_mtime)
            if p not in cache or cache[p][0] != key:
                cache[p] = (key, scan(p, since))
                n_new += 1
            out.append(cache[p][1])
    pickle.dump(cache, open(cp, "wb"))
    return out, n_new


# ------------------------------------------------------------------------ db
def db_section(tmp):
    shutil.copy(DB, tmp)
    if os.path.exists(DB + "-wal"):
        shutil.copy(DB + "-wal", tmp + "-wal")
    c = sqlite3.connect(tmp)
    q = lambda s, *a: c.execute(s, a).fetchall()
    d = dict(user_version=q("PRAGMA user_version")[0][0])
    rows = q("SELECT server_id,project,tool,window,calls,bytes_returned,bytes_kept,unmeasured_calls,started_at,updated_at FROM tool_stats")
    d["rows"] = []
    for sid, proj, tool, win, calls, ret, kept, unm, st, up in rows:
        tp = proj.lower()
        test = "scratchpad" in tp or "/e2e" in tp or tp.startswith("/private/tmp")
        test_win = win.startswith(TEST_WINDOW)
        period = ("pre-JS(<10-21)" if up < "2026-10-21 00:00:00" else "pre-Python(<10-22 05:36Z)" if up < PY_COUNTERS else "python-era")
        d["rows"].append(dict(tool=tool, project=proj, window=win[:8], calls=calls, ret=ret, kept=kept, unm=unm, started=st, updated=up,
                              test=test, test_window=test_win, period=period, unmeasured_known=up >= UNMEAS_COL,
                              straddles=st < UNMEAS_COL <= up))
    d["totals"] = q("SELECT project,calls,bytes_returned,bytes_kept FROM stats_totals")
    d["redirects"] = q("SELECT substr(created_at,1,10),type,count(*),COALESCE(sum(bytes_avoided),0) FROM session_events WHERE category='redirect' GROUP BY 1,2 ORDER BY 1")
    d["read_events"] = q("SELECT session_id, created_at, substr(data,7), bytes_avoided FROM session_events WHERE category='redirect' AND type='read-redirected'")
    d["lifetime_redirect_bytes"] = q("SELECT COALESCE(sum(bytes_avoided),0), count(*) FROM session_events WHERE category='redirect'")[0]
    d["sessions"] = q("SELECT count(*), sum(compact_count>0), min(started_at), max(started_at) FROM session_meta")[0]
    c.close()
    return d


# ------------------------------------------------------------------- analysis
def med(v):
    return statistics.median(v) if v else 0


def pct(v, p):
    v = sorted(v)
    return v[min(len(v) - 1, int(len(v) * p))] if v else 0


def kb(n):
    return "%.1f KB" % (n / 1024) if n < 1048576 else "%.2f MB" % (n / 1048576)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--since", default="2026-09-30")
    ap.add_argument("--out", default=DEFAULT_OUT)
    args = ap.parse_args()
    now = datetime.datetime.now(datetime.timezone.utc)
    J, L = {}, []
    P = L.append

    P("# cmgo effectiveness baseline\n")
    P("Generated %s UTC by cmgo_audit.py (since %s). Definitions: METHOD.md. Every number below is printed by this script.\n" % (now.strftime("%Y-%m-%d %H:%M"), args.since))

    # ---------------- Q1: DB
    tmpd = tempfile.mkdtemp(prefix="cmgo-audit-")
    try:
        db = db_section(os.path.join(tmpd, "cmgo.db"))
    finally:
        shutil.rmtree(tmpd, ignore_errors=True)
    rows = db["rows"]
    J["db"] = db
    P("## Q1. Bytes kept out (DB `tool_stats`, schema v%d)\n" % db["user_version"])
    P("Rows: %d (one row = server x tool x session window). Period is the row's `updated_at`; a row cannot be split by time or language.\n" % len(rows))

    def agg(sel):
        g = collections.defaultdict(lambda: [0, 0, 0, 0])
        for r in rows:
            if sel(r):
                a = g[r["tool"]]
                a[0] += r["calls"]; a[1] += r["ret"]; a[2] += r["kept"]; a[3] += r["unm"]
        return g

    def table(g, title):
        P("**%s**\n" % title)
        P("| tool | calls | returned | kept | unmeasured_calls |")
        P("|---|---:|---:|---:|---:|")
        tot = [0, 0, 0, 0]
        for t, a in sorted(g.items(), key=lambda x: -x[1][2]):
            P("| %s | %d | %s | %s | %d |" % (t, a[0], kb(a[1]), kb(a[2]), a[3]))
            tot = [x + y for x, y in zip(tot, a)]
        P("| **total** | %d | %s | %s | %d |\n" % (tot[0], kb(tot[1]), kb(tot[2]), tot[3]))
        return tot

    t_all = table(agg(lambda r: True), "All rows (no exclusions)")
    t_clean = table(agg(lambda r: not r["test"] and not r["test_window"]), "Excluding test projects (scratchpad/e2e) and the context-mode-06 test window (%s...)" % TEST_WINDOW)
    tw = [r for r in rows if r["test_window"]]
    P("Excluded test-window rows: %d, calls %d, kept %s. Excluded test-project rows: %d.\n" % (
        len(tw), sum(r["calls"] for r in tw), kb(sum(r["kept"] for r in tw)), sum(1 for r in rows if r["test"])))
    J["q1"] = dict(all=t_all, clean=t_clean)
    P("**Kept by period (clean rows, ctx_execute only; what the kept number can contain)**\n")
    P("| period | rows | calls | kept | contains |")
    P("|---|---:|---:|---:|---|")
    contains = {"pre-JS(<10-21)": "stdout/stderr only", "pre-Python(<10-22 05:36Z)": "JS/TS reads counted (PR #10); Python and shell not", "python-era": "JS/TS and Python reads counted; shell/other not"}
    for per in ("pre-JS(<10-21)", "pre-Python(<10-22 05:36Z)", "python-era"):
        s = [r for r in rows if not r["test"] and not r["test_window"] and r["tool"] == "ctx_execute" and r["period"] == per]
        P("| %s | %d | %d | %s | %s |" % (per, len(s), sum(r["calls"] for r in s), kb(sum(r["kept"] for r in s)), contains[per]))
    P("")
    clean_exec = [r for r in rows if not r["test"] and not r["test_window"] and r["tool"] == "ctx_execute"]
    post = [r for r in clean_exec if r["unmeasured_known"]]
    P("`unmeasured_calls` is trustworthy only for rows updated after %s UTC: %d clean ctx_execute rows, %d calls, %d recorded as not measured. Rows straddling the migration: %d (their counts are partial). Older rows: measured status unknown.\n" % (
        UNMEAS_COL, len(post), sum(r["calls"] for r in post), sum(r["unm"] for r in post), sum(1 for r in clean_exec if r["straddles"])))
    P("`stats_totals` (lifetime, per project): " + "; ".join("%s calls=%d kept=%s" % (p[-40:], c_, kb(k)) for p, c_, _, k in db["totals"]) + "\n")
    tot_calls = sum(r["calls"] for r in rows)
    P("Reconciliation: stats_totals calls = %d, tool_stats calls = %d (equal unless a purge removed rows).\n" % (sum(c_ for _, c_, _, _ in db["totals"]), tot_calls))
    P("**Redirect events in the DB** (`session_events`, category redirect; TS events are imported, so before %s they are not cmgo):\n" % ERA_CMGO[:10])
    P("| day | type | n | bytes_avoided |\n|---|---|---:|---:|")
    for d_, ty, n, b in db["redirects"][-12:]:
        P("| %s | %s | %d | %s |" % (d_, ty, n, kb(b)))
    P("")

    # ---------------- transcripts
    tr, n_new = load_transcripts(args.since, args.out)
    main_s = [t for t in tr if not t["sub"] and not is_test_project(t["project"])]
    sub_s = [t for t in tr if t["sub"] and not is_test_project(t["project"])]
    P("## Q2. Adoption (transcripts since %s: %d files, %d re-read)\n" % (args.since, len(tr), n_new))
    P("Main sessions (non-test): %d. Subagent transcripts (non-test): %d, counted apart. Test-project transcripts excluded.\n" % (len(main_s), len(sub_s)))

    def ctx_n(t, f=None, lo=""):
        return sum(1 for ts, n, _ in t["seq"] if ts >= lo and fam(n) and (f is None or fam(n) == f))

    def active(t, lo):
        return (t["last"] or "") >= lo and any(ts >= lo for ts, _, _ in t["seq"])

    P("Eras are by activity, not by session start: hooks are live in every open session, so a session that started before %s counts in the cmgo era if it has tool calls after it.\n" % ERA_CMGO[:10])
    P("| scope | sessions with tool calls | any ctx_* | TS plugin ctx | cmgo ctx |\n|---|---:|---:|---:|---:|")
    J["q2_sessions"] = {}
    for nm, lo, hi in (("pre-cmgo era (%s to %s)" % (args.since, ERA_CMGO[:10]), args.since, ERA_CMGO), ("cmgo era (>= %s)" % ERA_CMGO[:10], ERA_CMGO, "9")):
        ss = [t for t in main_s if any(lo <= ts < hi for ts, _, _ in t["seq"])]
        cnt = lambda f: sum(1 for t in ss if any(lo <= ts < hi and fam(n) and (f is None or fam(n) == f) for ts, n, _ in t["seq"]))
        a, b, c_ = cnt(None), cnt("ts"), cnt("cmgo")
        P("| %s | %d | %d (%.0f%%) | %d | %d |" % (nm, len(ss), a, 100.0 * a / max(1, len(ss)), b, c_))
        J["q2_sessions"][nm] = [len(ss), a, b, c_]
    P("")
    calls = collections.Counter(); ecalls = collections.Counter(); langs = collections.Counter()
    for t in main_s:
        for ts, n, _ in t["seq"]:
            if fam(n):
                calls[(fam(n), n.split("__")[-1])] += 1
                if ts >= ERA_CMGO:
                    ecalls[(fam(n), n.split("__")[-1])] += 1
        langs.update(t["langs"])
    P("**ctx_* calls in main sessions** (since %s; cmgo-era column = on/after %s):\n" % (args.since, ERA_CMGO[:10]))
    P("| family | tool | calls | cmgo era |\n|---|---|---:|---:|")
    for (f, n), v in sorted(calls.items(), key=lambda x: (x[0][0], -x[1])):
        P("| %s | %s | %d | %d |" % (f, n, v, ecalls[(f, n)]))
    P("\n**ctx_execute language mix** (call counts from tool_use inputs, not bytes; unmeasured languages for cmgo: everything except javascript/typescript/python):\n")
    for f in ("cmgo", "ts"):
        l = {k[1]: v for k, v in langs.items() if k[0] == f}
        tot = sum(l.values())
        um = sum(v for k, v in l.items() if k not in ("javascript", "typescript", "python"))
        P("- %s: %s; not measurable by cmgo: %d of %d (%.0f%%)" % (f, ", ".join("%s %d (%.0f%%)" % (k, v, 100.0 * v / tot) for k, v in sorted(l.items(), key=lambda x: -x[1])) or "none", um, tot, 100.0 * um / max(1, tot)))
    sc = collections.Counter((fam(n), ts >= ERA_CMGO) for t in sub_s for ts, n, _ in t["seq"] if fam(n))
    P("ctx_* calls made inside subagents: TS plugin %d (before %s) / %d (after); cmgo %d / %d. Subagent transcripts with any tool call: %d." % (
        sc[("ts", False)], ERA_CMGO[:10], sc[("ts", True)], sc[("cmgo", False)], sc[("cmgo", True)], sum(1 for t in sub_s if t["seq"])))
    se = [t for t in sub_s if any(ts >= ERA_CMGO for ts, _, _ in t["seq"])]
    sp = [t for t in sub_s if any(ts < ERA_CMGO for ts, _, _ in t["seq"])]
    P("Subagent transcripts with tool calls: %d before the cmgo era (%d Bash/Read/Grep/WebFetch calls), %d in the cmgo era (%d such calls)." % (
        len(sp), sum(1 for t in sp for ts, n, _ in t["seq"] if ts < ERA_CMGO and n in RAW_TOOLS), len(se), sum(1 for t in se for ts, n, _ in t["seq"] if ts >= ERA_CMGO and n in RAW_TOOLS)))
    P("")
    J["q2_calls"] = {"%s/%s" % k: v for k, v in calls.items()}
    J["q2_langs"] = {"%s/%s" % k: v for k, v in langs.items()}

    def bigs(ts_, lo="", hi="9", strict=True):
        n = b = 0
        for t in ts_:
            for ts, tool, z, spill in t["big"]:
                if lo <= ts < hi and not (strict and spill):
                    n += 1; b += z
        return n, b

    P("**Raw results over 5 KB (Bash/Read/Grep/WebFetch, no dedupe).**\n")
    n1, b1 = bigs(tr, args.since, strict=False)
    P("- Cross-check of the earlier audit (all transcripts incl. subagents and test projects, no spill filter): %d results, %.1f MB. Earlier audit: 6,169 / 68.0 MB through 2026-10-21." % (n1, b1 / 1048576))
    n2, b2 = bigs(main_s, args.since)
    n3, b3 = bigs(sub_s, args.since)
    P("- Non-test, spilled-file Reads excluded: main sessions %d results / %.1f MB; subagents %d results / %.1f MB (subagents hold %.0f%% of the bytes)." % (n2, b2 / 1048576, n3, b3 / 1048576, 100.0 * b3 / max(1, b2 + b3)))
    P("")
    P("| day (UTC) | main raw>5KB | MB | subagent raw>5KB | MB | ctx_* calls (main) |\n|---|---:|---:|---:|---:|---:|")
    byday = collections.defaultdict(lambda: [0, 0, 0, 0, 0])
    for t in main_s + sub_s:
        k = 0 if not t["sub"] else 2
        for ts, tool, z, spill in t["big"]:
            if not spill:
                byday[ts[:10]][k] += 1; byday[ts[:10]][k + 1] += z
        if not t["sub"]:
            for ts, n, _ in t["seq"]:
                if fam(n):
                    byday[ts[:10]][4] += 1
    for d_ in sorted(byday)[-14:]:
        v = byday[d_]
        P("| %s | %d | %.1f | %d | %.1f | %d |" % (d_, v[0], v[1] / 1048576, v[2], v[3] / 1048576, v[4]))
    P("")
    J["q2_big"] = dict(crosscheck=[n1, b1], main=[n2, b2], subagent=[n3, b3], by_day={k: v for k, v in byday.items()})
    cn, cb = bigs(main_s + sub_s, ERA_CMGO)
    pn, pb = bigs(main_s + sub_s, args.since, ERA_CMGO)
    cd = max(1.0, (now - datetime.datetime.fromisoformat(ERA_CMGO).replace(tzinfo=datetime.timezone.utc)).total_seconds() / 86400)
    pd = (datetime.datetime.fromisoformat(ERA_CMGO) - datetime.datetime.fromisoformat(args.since)).days
    P("Per day, main + subagents, non-test: pre-cmgo %.0f results / %.2f MB per day (%d days); cmgo era %.0f results / %.2f MB per day (%.1f days). Descriptive only; day-to-day variation is large (see table).\n" % (
        pn / max(1, pd), pb / max(1, pd) / 1048576, pd, cn / cd, cb / cd / 1048576, cd))
    J["q2_big_rate"] = dict(pre=[pn, pb, pd], cmgo=[cn, cb, cd])

    # ---- subagent funnel (spec 2026-10-22-cmgo-subagent-routing-design.md, option O0)
    rows_ = [r_ for r_ in load_probe().probe(args.since, os.path.join(args.out, ".probe-cache.pkl")) if not is_test_project(r_["project"])]
    P("**Subagent funnel** (non-test subagent transcripts; block = routing block in the prompt, listed = a `ctx_*` tool in the deferred-tool list, ToolSearch = ran ToolSearch, ctx = called a `ctx_*` tool):\n")
    P("| period | agent type | subagents | block | listed | block+listed | ToolSearch | ctx called | raw>5KB MB |\n|---|---|---:|---:|---:|---:|---:|---:|---:|")
    J["funnel"] = {}
    gate_funnel = 0
    for per, test_ in (("TS plugin (< %s)" % ERA_CMGO[:10], lambda f: f < ERA_CMGO), ("cmgo (>= %s)" % ERA_CMGO[:10], lambda f: f >= ERA_CMGO)):
        S = [r_ for r_ in rows_ if test_(r_["first"])]
        bl = lambda r_: r_["block_ts"] or r_["block_cmgo"]
        li = lambda r_: r_["listed_ts"] or r_["listed_cmgo"]
        cx = lambda r_: r_["ctx_ts"] + r_["ctx_cmgo"] > 0
        tot = [len(S), sum(bl(r_) for r_ in S), sum(li(r_) for r_ in S), sum(bl(r_) and li(r_) for r_ in S), sum(1 for r_ in S if bl(r_) and li(r_) and r_["toolsearch"]),
               sum(1 for r_ in S if bl(r_) and li(r_) and cx(r_)), sum(r_["big_b"] for r_ in S)]
        P("| %s | **all** | %d | %d | %d | %d | %d | %d (%.1f%%) | %.1f |" % (per, tot[0], tot[1], tot[2], tot[3], tot[4], tot[5], 100.0 * tot[5] / max(1, tot[3]), tot[6] / 1048576))
        byt = collections.defaultdict(list)
        for r_ in S:
            byt[r_["type"]].append(r_)
        for ty, v in sorted(byt.items(), key=lambda x: -sum(r_["big_b"] for r_ in x[1]))[:6]:
            P("| | %s | %d | %d | %d | %d | %d | %d | %.1f |" % (ty[:28], len(v), sum(bl(r_) for r_ in v), sum(li(r_) for r_ in v), sum(bl(r_) and li(r_) for r_ in v),
                                                              sum(1 for r_ in v if bl(r_) and li(r_) and r_["toolsearch"]), sum(1 for r_ in v if bl(r_) and li(r_) and cx(r_)), sum(r_["big_b"] for r_ in v) / 1048576))
        J["funnel"][per] = tot
        if per.startswith("cmgo"):
            gate_funnel = tot[3]
    P("\nThe funnel to watch: block+listed -> ToolSearch -> ctx called. TS-plugin baseline (2026-10-22): 1,197 -> 167 (14%) -> 39 (3.3%). Revisit subagent routing (option O1 in the spec) when the cmgo row has at least 60 block+listed subagents.\n")

    # ---- hooks, redirects, guidance
    def follow(t, idx, k=3):
        if idx is None:
            return None
        return any(fam(n) for _, n, _ in t["seq"][idx + 1: idx + 1 + k])

    hc = collections.Counter()
    for t in main_s:
        for h in t["hooks"]:
            if h[1] == "Stop":
                hc[(h[2], "cmgo era" if h[0] >= ERA_CMGO else "pre-cmgo")] += 1
    P("Both hook families can run in the same session. `Stop` hook runs recorded (every turn end, so a proxy for which families are installed): " + "; ".join("%s %s %d" % (k[0], k[1], v) for k, v in sorted(hc.items())) + ".\n")
    P("**Hook guidance fires (PreToolUse `additionalContext`, structural, main sessions):**\n")
    H = [(t, h) for t in main_s for h in t["hooks"]]
    kinds = collections.Counter((h[2], h[1].replace("PreToolUse:", ""), h[3]) for _, h in H if h[1].startswith("PreToolUse") and h[3] != "empty")
    P("| family | tool | kind | n |\n|---|---|---|---:|")
    for k, v in sorted(kinds.items(), key=lambda x: -x[1])[:12]:
        P("| %s | %s | %s | %d |" % (k[0], k[1][:40], k[2], v))
    P("\n**Hard redirects** (Bash/WebFetch call rewritten or refused with a `context-mode: ... redirected` message; found in the tool result):\n")
    for f in ("ts", "cmgo"):
        rs = [(t, r_) for t in main_s for r_ in t["redirects"] if r_[1] == f]
        fol = [follow(t, r_[3]) for t, r_ in rs]
        kn = [x for x in fol if x is not None]
        P("- %s: %d redirects; followed by a ctx_* call within 3 actions: %d of %d resolvable (%.0f%%). Earlier audit: 38 redirects, 20 followed (53%%), both families together through 10-21." % (f, len(rs), sum(kn), len(kn), 100.0 * sum(kn) / max(1, len(kn))))
        J["q2_redirect_%s" % f] = [len(rs), sum(kn), len(kn)]
    P("")
    br = [(t, h) for t, h in H if h[2] == "cmgo" and h[3] == "bashread"]
    fol = [follow(t, h[6]) for t, h in br]
    kn = [x for x in fol if x is not None]
    firsts = min((h[0] for _, h in br), default=None)
    P("**Bash read guidance (PR #11 spec §5.3)**: fires so far %d." % len(br))
    if firsts:
        days = max(1.0, (now - datetime.datetime.fromisoformat(firsts.replace("Z", "+00:00"))).total_seconds() / 86400)
        rate = len(br) / days
        proj = (now + datetime.timedelta(days=max(0, (40 - len(br)) / rate))).strftime("%Y-%m-%d") if rate > 0 else "n/a"
        P("First %s, %.1f days, %.1f/day. M2 follow within 3 actions: %d of %d resolvable (%.0f%%; target >= 25%%). Rollback rule needs 40 fires: %s. Projected 40th fire at the current rate: %s." % (
            firsts[:16], days, rate, sum(kn), len(kn), 100.0 * sum(kn) / max(1, len(kn)), "REACHED" if len(br) >= 40 else "not reached", proj))
        J["q2_bashread"] = dict(fires=len(br), followed=sum(kn), resolvable=len(kn), first=firsts, per_day=rate, projected=proj)
    else:
        P("None found. The cmgo-hook Bash guidance class A fires only when a `cat/nl/tac/bat/head/tail` command's stat estimate is >= the threshold (default 16 KiB).")
        J["q2_bashread"] = dict(fires=0)
    base = ERA_CMGO
    ba = sum(1 for t in main_s for ts, n, _ in t["seq"] if n == "Bash" and ts >= base)
    ca = sum(t["bash_classA16"] for t in main_s if active(t, base))
    b0 = sum(t["bash_n"] for t in main_s if not active(t, base))
    c0 = sum(t["bash_classA16"] for t in main_s if not active(t, base))
    P("Bash calls on/after %s: %d (main sessions)." % (ERA_CMGO[:10], ba))
    for lbl, lo in (("PR #11 merged (2026-10-21T13:59Z; earliest it could be live)", "2026-10-21T13:59:00"), ("current hook binary installed (2026-10-22T06:30Z; certainly live)", "2026-10-22T06:30:00")):
        nb = sum(1 for t in main_s + sub_s for ts, n, _ in t["seq"] if n == "Bash" and ts >= lo)
        P("- Bash calls (main + subagents) since %s: %d. At the spec baseline (228 class-A results >= 16 KiB in 41,187 calls = 0.55 per 100) that is about %.1f expected; fires: %d. Calls needed for 40 fires at that baseline: about %d." % (lbl, nb, nb * 0.0055, len(br), 40 / 0.0055))
    P("M1 (class-A-like Bash results >= 16 KiB per 100 Bash calls, approximate first-stage test): sessions with no cmgo-era activity: %d in %d calls (%.2f per 100; spec baseline 0.55 in 41,187 calls); sessions active in the cmgo era (counts include their pre-era part): %d. Too coarse to judge M1: needs the 40+ fires. M3 (precision) needs a manual sample of 30 fires: not computed.\n" % (
        c0, b0, 100.0 * c0 / max(1, b0), ca))

    # ---- Read guidance recorded as 'redirect' in the DB
    P("**Read guidance vs the DB's `read-redirected` events.** The cmgo Read hook adds a tip and records a redirect event with `bytes_avoided = file size`, but the Read still runs. Checked against transcripts (event path vs a Read of the same path in the same session, at or after the event time):\n")
    sess_by_id = {t["session"]: t for t in main_s + sub_s}
    J["q2_read_events"] = {}
    P("| events | n | bytes_avoided claimed | matched to transcript | Read executed at/after event time | Read of that path at any time | bytes of the at/after group |\n|---|---:|---:|---:|---:|---:|---:|")
    for nm, pick in (("before %s (TS-era, imported)" % ERA_CMGO[:10], lambda c_: c_ < ERA_CMGO.replace("T", " ")), ("cmgo era (cmgo-hook wrote them)", lambda c_: c_ >= ERA_CMGO.replace("T", " "))):
        ev = [e for e in db["read_events"] if pick(e[1])]
        n_found = n_exec = by_exec = n_any = 0
        for sid, created, path, avoided in ev:
            t = sess_by_id.get(sid)
            if not t:
                continue
            n_found += 1
            lo = created.replace(" ", "T")
            if any(rp == path for rts, rp, rz, ri in t["reads"]):
                n_any += 1
            if any(rp == path and rts >= lo[:19] for rts, rp, rz, ri in t["reads"]):
                n_exec += 1; by_exec += avoided
        P("| %s | %d | %s | %d | %d (%.0f%%) | %d (%.0f%%) | %s |" % (nm, len(ev), kb(sum(e[3] for e in ev)), n_found, n_exec, 100.0 * n_exec / max(1, n_found), n_any, 100.0 * n_any / max(1, n_found), kb(by_exec)))
        J["q2_read_events"][nm] = dict(events=len(ev), claimed=sum(e[3] for e in ev), matched=n_found, executed=n_exec, claimed_executed=by_exec, any_time=n_any)
    P("")
    lr, ln = db["lifetime_redirect_bytes"]
    P("- Lifetime figure `ctx_stats` printed before the 2026-10-22 split (stats:lifetime-split) = stats_totals kept %s + redirect bytes_avoided %s (%d events) = %s; it now shows stats_totals only. Sandbox bytes measured on clean rows: %s." % (
        kb(sum(k for _, _, _, k in db["totals"])), kb(lr), ln, kb(sum(k for _, _, _, k in db["totals"]) + lr), kb(t_clean[2])))
    P("")

    # ---------------- Q3
    P("## Q3. Net effect on context\n")
    ck = t_clean[2]
    P("- Bytes cmgo kept (DB, clean rows): %s over %d calls. Raw results >5 KB that entered context in the cmgo era (main + subagents, no spill reads): %s in %d results. Descriptive: kept was measured inside the sandbox; the raw figure is what entered context." % (kb(ck), t_clean[0], kb(cb), cn))
    P("- Ratio kept : raw-dumped = %.2f : 1. Not a savings rate: the denominator is not what ctx_* would have received." % (ck / max(1, cb)))

    P("\nWhat a ctx_* call costs the context: result sizes returned to the model (bytes; main sessions).\n")
    P("| family | tool | n | median | p95 | total |\n|---|---|---:|---:|---:|---:|")
    rs = collections.defaultdict(list)
    for t in main_s:
        for ts, f, n, z, err in t["ctx_res"]:
            rs[(f, n)].append(z)
    for (f, n), v in sorted(rs.items(), key=lambda x: -sum(x[1]))[:8]:
        P("| %s | %s | %d | %d | %d | %s |" % (f, n, len(v), med(v), pct(v, .95), kb(sum(v))))
    allbig = [b[2] for t in main_s + sub_s for b in t["big"] if not b[3]]
    P("\nFor scale: a raw Bash/Read/Grep/WebFetch result over 5 KB has median %d bytes, p95 %d (n=%d).\n" % (med(allbig), pct(allbig, .95), len(allbig)))

    def era_metrics(t, lo, hi="9"):
        rq = [x for x in t["req"] if lo <= x[0] < hi]
        return dict(turns=len(rq), peak=max([x[1] for x in rq], default=0), comp=sum(1 for c_ in t["comp"] if lo <= c_ < hi),
                    big=sum(1 for b in t["big"] if lo <= b[0] < hi and not b[3]),
                    ctx=sum(1 for ts, n, _ in t["seq"] if lo <= ts < hi and fam(n)))

    J["q3_groups"] = {}
    for label, lo, hi in (("TS-plugin period %s to %s (n is larger; ctx_* here = TS plugin)" % (args.since, ERA_CMGO[:10]), args.since, ERA_CMGO),
                          ("cmgo period on/after %s (about 1 day so far)" % ERA_CMGO[:10], ERA_CMGO, "9")):
        grp = collections.defaultdict(list)
        for t in main_s:
            m_ = era_metrics(t, lo, hi)
            if m_["turns"] < 5:
                continue
            g = "none" if m_["ctx"] == 0 else "light(1-4)" if m_["ctx"] < 5 else "heavy(5+)"
            grp[g].append((t, m_))
        P("\n**%s: sessions with >= 5 assistant turns in the period, grouped by ctx_* calls in the period, observational:**\n" % label)
        P("| group | sessions | median turns | median peak ctx (tokens) | compactions per 100 turns | sessions with >=1 compaction | median raw>5KB per 100 turns |\n|---|---:|---:|---:|---:|---:|---:|")
        for g in ("none", "light(1-4)", "heavy(5+)"):
            ss = grp.get(g, [])
            if not ss:
                P("| %s | 0 | | | | | |" % g); continue
            tt = sum(m_["turns"] for _, m_ in ss)
            rate = 100.0 * sum(m_["comp"] for _, m_ in ss) / max(1, tt)
            P("| %s | %d | %d | %d | %.2f | %d | %.1f |" % (g, len(ss), med([m_["turns"] for _, m_ in ss]), med([m_["peak"] for _, m_ in ss]), rate,
                                                          sum(1 for _, m_ in ss if m_["comp"]), med([100.0 * m_["big"] / m_["turns"] for _, m_ in ss])))
            J["q3_groups"]["%s|%s" % (lo[:10], g)] = [len(ss), med([m_["turns"] for _, m_ in ss]), med([m_["peak"] for _, m_ in ss]), rate]
        byp = collections.defaultdict(lambda: collections.defaultdict(list))
        for g, ss in grp.items():
            for t, m_ in ss:
                byp[t["project"]]["none" if g == "none" else "ctx"].append(m_)
        P("\nSame-project pairs (projects with both a ctx_* session and a none session):\n")
        P("| project | none: n, med peak, comp/100t | ctx: n, med peak, comp/100t |\n|---|---|---|")
        f_ = lambda ms: "%d, %d, %.2f" % (len(ms), med([m_["peak"] for m_ in ms]), 100.0 * sum(m_["comp"] for m_ in ms) / max(1, sum(m_["turns"] for m_ in ms)))
        shown = 0
        for p, v in sorted(byp.items(), key=lambda x: -len(x[1]["ctx"])):
            if v["none"] and v["ctx"]:
                P("| %s | %s | %s |" % (p[-45:], f_(v["none"]), f_(v["ctx"]))); shown += 1
        if not shown:
            P("| (none) | | |")
        if lo == ERA_CMGO:
            cmgo_grp = grp
    P("\nConfounds: ctx_* use correlates with long, research-heavy sessions; project, task type and session length are not controlled; in the first period only the TS plugin's ctx_* tools and hooks existed, in the second only cmgo's (the TS plugin's hooks stopped after the install); sessions are not randomised. A difference here is at most \"consistent with\", never evidence of cause.\n")

    # ---------------- Q4
    P("## Q4. Cost side\n")
    hs = collections.defaultdict(list)
    for t, h in H:
        hs[(h[2], h[1])].append(h[4])
    P("Hook latency from Claude Code's own `durationMs` (process start included), main sessions, since %s. Only hook runs that Claude Code logged are included: `Stop`/`SessionStart` always, `PreToolUse` only when the hook produced output, so PreToolUse n is not the number of invocations.\n" % args.since)
    P("| family | hook | n | p50 ms | p95 ms | max ms |\n|---|---|---:|---:|---:|---:|")
    J["q4_latency"] = {}
    for (f, hn), v in sorted(hs.items(), key=lambda x: (x[0][0], -len(x[1]))):
        if len(v) >= 20:
            P("| %s | %s | %d | %d | %d | %d |" % (f, hn, len(v), med(v), pct(v, .95), max(v)))
            J["q4_latency"]["%s/%s" % (f, hn)] = [len(v), med(v), pct(v, .95), max(v)]
    P("\nPython counters overhead: PR #12 benchmark, +3.0 ms (print), +4.1 ms (json), +3.3 ms (urllib) median over plain python3 (quoted, not re-measured).\n")
    errs = [e for t in tr for e in t["cmgo_err"]]
    herr = [e for t in tr for e in t["hook_err"] if e[2] == "cmgo"]
    nz = [(t["session"][:8], h[:6]) for t in tr for h in t["hooks"] if h[2] == "cmgo" and h[5] != 0]
    ncm = sum(1 for t in tr for r_ in t["ctx_res"] if r_[1] == "cmgo")
    P("Failures attributable to cmgo: mcp__cmgo__* tool results with is_error: %d of %d cmgo results; cmgo-hook non_blocking_error/cancelled: %d; cmgo-hook non-zero exit in hook_success: %d." % (len(errs), ncm, len(herr), len(nz)))
    for e in errs[:8]:
        P("- tool error %s %s: %s" % (e[0][:16], e[1].split("__")[-1], e[2].replace("\n", " ")[:140]))
    for e in herr[:5]:
        P("- hook %s %s %s exit=%d %s" % (e[0][:16], e[1], e[3], e[4], e[5].replace("\n", " ")))
    hl = os.path.join(HOME, ".claude", "cmgo", "hook-errors.log")
    P("- hook-errors.log: %s" % ("present, %d bytes" % os.path.getsize(hl) if os.path.exists(hl) else "absent"))
    J["q4_errors"] = dict(tool_errors=len(errs), cmgo_results=ncm, hook_err=len(herr), nonzero=len(nz))
    P("")
    # sample sizes -> verdict gate
    P("## Sample-size gate (METHOD.md: under 30 events or under 3 days = not enough data)\n")
    gate = {
        "Q1 kept, post-Python clean ctx_execute calls": sum(r["calls"] for r in clean_exec if r["period"] == "python-era"),
        "Q1 unmeasured_calls era calls": sum(r["calls"] for r in post),
        "Q2 cmgo ctx calls (main)": sum(v for (f, n), v in calls.items() if f == "cmgo"),
        "Q2 cmgo-era sessions (with tool calls)": J["q2_sessions"][[k for k in J["q2_sessions"] if k.startswith("cmgo era")][0]][0],
        "Q2 cmgo hard redirects": J.get("q2_redirect_cmgo", [0])[0],
        "Q2 cmgo-era subagents with block and tools listed (O1 decision needs 60)": gate_funnel,
        "Q2 bash read guidance fires": len(br),
        "Q3 heavy-ctx sessions in cmgo era": len(cmgo_grp.get("heavy(5+)", [])),
        "Q3 none-group sessions in cmgo era": len(cmgo_grp.get("none", [])),
    }
    P("| item | n | enough (>=30) |\n|---|---:|---|")
    for k, v in gate.items():
        P("| %s | %d | %s |" % (k, v, "yes" if v >= 30 else "NO"))
    J["gate"] = gate
    os.makedirs(args.out, exist_ok=True)
    open(os.path.join(args.out, "report.md"), "w").write("\n".join(L) + "\n")
    json.dump(J, open(os.path.join(args.out, "report.json"), "w"), default=str, indent=1)
    print("wrote", os.path.join(args.out, "report.md"))


if __name__ == "__main__":
    main()
