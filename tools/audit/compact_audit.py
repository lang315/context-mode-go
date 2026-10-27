#!/usr/bin/env python3
"""Bash compaction effect (bash-compact spec §5). Read-only on ~/.claude.
    python3 -I compact_audit.py [--since 2026-10-28] [--self-test]
M1 saved = bash-compact kept bytes (DB). M2 recovery = share of compactions
followed within 3 tool calls by a ctx_search naming the source or a re-run of
the same command (with or without # cmgo:full). M3 net = M1 - bytes those
recovering calls returned.
"""
import argparse, glob, json, os, re, shutil, sqlite3, sys, tempfile

HOME = os.path.expanduser("~")
DB = os.path.join(HOME, ".claude", "cmgo", "cmgo.db")
ROOT = os.path.join(HOME, ".claude", "projects")
LABEL_RE = re.compile(r'\[cmgo: the full output is indexed as "(bash:[^"]+)"')
WINDOW = 3


def norm(cmd):
    return " ".join(cmd.replace("# cmgo:full", "").split())


def size(content):
    if isinstance(content, str):
        return len(content.encode())
    return sum(len(x.get("text", "").encode()) for x in content or [] if isinstance(x, dict))


def scan(lines, since=""):
    """Compactions and their recovery from one transcript's JSON lines;
    messages stamped before since are skipped."""
    calls, results, order = {}, {}, []
    for line in lines:
        try:
            m = json.loads(line)
        except ValueError:
            continue
        if m.get("timestamp", since) < since:
            continue
        content = (m.get("message") or {}).get("content")
        if not isinstance(content, list):
            continue
        for x in content:
            if x.get("type") == "tool_use":
                calls[x["id"]] = (x["name"], x.get("input") or {})
                order.append(x["id"])
            elif x.get("type") == "tool_result":
                results[x.get("tool_use_id")] = x.get("content")
    out = []
    for i, cid in enumerate(order):
        name, inp = calls[cid]
        res = results.get(cid)
        text = res if isinstance(res, str) else " ".join(y.get("text", "") for y in res or [] if isinstance(y, dict))
        mm = LABEL_RE.search(text or "") if name == "Bash" else None
        if not mm:
            continue
        label, back = mm.group(1), 0
        recovered = False
        for nid in order[i + 1:i + 1 + WINDOW]:
            n2, i2 = calls[nid]
            if (n2.endswith("ctx_search") and label in json.dumps(i2)) or (n2 == "Bash" and norm(i2.get("command", "")) == norm(inp.get("command", ""))):
                recovered = True
                back += size(results.get(nid))
        out.append((label, recovered, back))
    return out


def self_test():
    t = [
        {"message": {"content": [{"type": "tool_use", "id": "a", "name": "Bash", "input": {"command": "go test ./..."}}]}},
        {"message": {"content": [{"type": "tool_result", "tool_use_id": "a", "content": 'x [cmgo: the full output is indexed as "bash:a". y'}]}},
        {"message": {"content": [{"type": "tool_use", "id": "b", "name": "mcp__cmgo__ctx_search", "input": {"queries": ["q"], "source": "bash:a"}}]}},
        {"message": {"content": [{"type": "tool_result", "tool_use_id": "b", "content": "12345"}]}},
        {"message": {"content": [{"type": "tool_use", "id": "c", "name": "Bash", "input": {"command": "ls"}}]}},
        {"message": {"content": [{"type": "tool_result", "tool_use_id": "c", "content": '[cmgo: the full output is indexed as "bash:c".'}]}},
        {"message": {"content": [{"type": "tool_use", "id": "d", "name": "Bash", "input": {"command": "ls   # cmgo:full"}}]}},
        {"message": {"content": [{"type": "tool_result", "tool_use_id": "d", "content": "abc"}]}},
        {"message": {"content": [{"type": "tool_use", "id": "e", "name": "Bash", "input": {"command": "pwd"}}]}},
        {"message": {"content": [{"type": "tool_result", "tool_use_id": "e", "content": '[cmgo: the full output is indexed as "bash:e".'}]}},
    ]
    got = scan(json.dumps(x) for x in t)
    assert got == [("bash:a", True, 5), ("bash:c", True, 3), ("bash:e", False, 0)], got
    assert norm("ls   # cmgo:full") == "ls"
    print("self-test ok")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--since", default="2026-10-28")
    ap.add_argument("--self-test", action="store_true")
    a = ap.parse_args()
    if a.self_test:
        return self_test()
    tmp = tempfile.mkdtemp(prefix="cmgo-compact-audit-")
    try:
        db = os.path.join(tmp, "cmgo.db")
        shutil.copy(DB, db)
        if os.path.exists(DB + "-wal"):
            shutil.copy(DB + "-wal", db + "-wal")
        c = sqlite3.connect(db)
        calls, kept, sessions = c.execute(
            "SELECT coalesce(sum(calls),0), coalesce(sum(bytes_kept),0), count(DISTINCT window) FROM tool_stats WHERE tool = 'bash-compact' AND updated_at >= ?",
            (a.since,)).fetchone()
        c.close()
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
    found = []
    for f in glob.glob(os.path.join(ROOT, "*", "*.jsonl")):
        if "scratchpad" in f or "mem-observer" in f:
            continue
        with open(f, errors="replace") as fh:
            found += scan(fh, a.since)
    n = len(found)
    rec = sum(1 for _, r, _ in found if r)
    back = sum(b for _, _, b in found)
    m2 = rec / n if n else 0.0
    m3 = kept - back
    print(f"compactions: DB {calls} calls in {sessions} sessions; transcripts {n}")
    print(f"M1 saved: {kept} bytes ({kept / max(1, sessions):.0f} per session)")
    print(f"M2 recovery: {rec} of {n} ({100 * m2:.0f}%), bringing back {back} bytes")
    print(f"M3 net: {m3} bytes ({100 * m3 / max(1, kept):.0f}% of M1)")
    if n < 200:
        print(f"verdict: not enough data ({n} of 200 compactions)")
    elif m2 > 0.5 or m3 < 0:
        print("verdict: remove (spec §5)")
    elif m2 > 0.3:
        print("verdict: raise the default threshold to 8192 once (spec §5)")
    elif m3 >= 0.5 * kept:
        print("verdict: keep (spec §5)")
    else:
        print("verdict: net below 50% of saved; raise the threshold to 8192 once (spec §5)")


if __name__ == "__main__":
    sys.exit(main())
