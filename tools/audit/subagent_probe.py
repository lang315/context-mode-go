#!/usr/bin/env python3
"""Per-subagent facts for the cmgo audit: agent type, routing block in the prompt, ctx tools listed
and used, ToolSearch use, and raw (Bash/Read/Grep/WebFetch) results over 5 KB.

Read-only on ~/.claude. Used by cmgo_audit.py (import by path); also runnable alone:

    python3 -I subagent_probe.py [--since 2026-09-30] [--out DIR]    # writes subagent_probe.json
"""
import argparse, collections, json, os, pickle, sys

ROOT = os.path.join(os.path.expanduser("~"), ".claude", "projects")
RAW = {"Bash", "Read", "Grep", "WebFetch"}
BIG = 5000
CACHE_VER = "probe-v1"  # bump when probe_one() output changes


def probe_one(p):
    meta = {}
    try:
        meta = json.load(open(p[:-6] + ".meta.json"))
    except Exception:
        pass
    r = dict(type=meta.get("agentType", "?"), project=os.path.relpath(p, ROOT).split(os.sep)[0], first=None,
             block_ts=False, block_cmgo=False, listed_cmgo=False, listed_ts=False, toolsearch=0, ctx_cmgo=0, ctx_ts=0,
             big_n=0, big_b=0, bigs=[], hook_cmgo=0, hook_ts=0)
    names = {}
    for l in open(p, errors="replace"):
        if l[:2] != "{\"":
            continue
        try:
            o = json.loads(l)
        except ValueError:
            continue
        ts = o.get("timestamp") or ""
        if ts and r["first"] is None:
            r["first"] = ts
        a = o.get("attachment")
        if isinstance(a, dict):
            t = a.get("type", "")
            if t == "deferred_tools_delta":
                s = json.dumps(a.get("addedNames", "")) + json.dumps(a.get("surfacedNames", ""))
                r["listed_cmgo"] |= "mcp__cmgo__ctx_" in s
                r["listed_ts"] |= "plugin_context-mode_context-mode__ctx_" in s
            elif t == "hook_success" and a.get("hookName", "").startswith("PreToolUse"):
                c = a.get("command", "")
                r["hook_cmgo"] += 1 if "cmgo-hook" in c else 0
                r["hook_ts"] += 1 if ("pretooluse.mjs" in c and "context-mode" in c) else 0
            continue
        m = o.get("message")
        if not isinstance(m, dict):
            continue
        c = m.get("content")
        if o.get("type") == "user" and r["first"] == ts and isinstance(c, (str, list)):
            s = c if isinstance(c, str) else json.dumps(c)
            r["block_ts"] |= "context_window_protection" in s and "plugin_context-mode" in s
            r["block_cmgo"] |= "context_window_protection" in s and "mcp__cmgo__" in s
        if not isinstance(c, list):
            continue
        for x in c:
            if not isinstance(x, dict):
                continue
            if x.get("type") == "tool_use":
                n = x.get("name", "")
                names[x["id"]] = (n, x.get("input") or {})
                if n == "ToolSearch":
                    r["toolsearch"] += 1
                if n.startswith("mcp__cmgo__ctx_"):
                    r["ctx_cmgo"] += 1
                if n.startswith("mcp__plugin_context-mode_context-mode__ctx_"):
                    r["ctx_ts"] += 1
            elif x.get("type") == "tool_result":
                n, inp = names.get(x.get("tool_use_id"), ("", {}))
                if n in RAW:
                    cc = x.get("content")
                    s = cc if isinstance(cc, str) else "".join(y.get("text", "") for y in cc if isinstance(y, dict)) if isinstance(cc, list) else ""
                    z = len(s.encode("utf-8", "replace"))
                    if z > BIG:
                        r["big_n"] += 1
                        r["big_b"] += z
                        r["bigs"].append((n, z, (inp.get("command") or inp.get("file_path") or inp.get("pattern") or inp.get("url") or "")[:300]))
    return r


def probe(since, cache_path=None):
    """Rows for every subagent transcript whose first line is on/after since (cached by size+mtime)."""
    cache = {}
    if cache_path:
        try:
            cache = pickle.load(open(cache_path, "rb"))
        except Exception:
            cache = {}
        if cache.get("_ver") != CACHE_VER:
            cache = {"_ver": CACHE_VER}
    rows = []
    for dp, _, fs in os.walk(ROOT):
        if "subagents" not in dp:
            continue
        for f in fs:
            if not f.endswith(".jsonl"):
                continue
            p = os.path.join(dp, f)
            st = os.stat(p)
            key = (st.st_size, st.st_mtime)
            if p not in cache or cache[p][0] != key:
                cache[p] = (key, probe_one(p))
            r = cache[p][1]
            if r["first"] and r["first"] >= since:
                rows.append(r)
    if cache_path:
        pickle.dump(cache, open(cache_path, "wb"))
    return rows


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--since", default="2026-09-30")
    ap.add_argument("--out", default=os.environ.get("CMGO_AUDIT_OUT") or os.path.join(os.path.expanduser("~"), ".cache", "cmgo-audit"))
    a = ap.parse_args()
    os.makedirs(a.out, exist_ok=True)
    rows = probe(a.since, os.path.join(a.out, ".probe-cache.pkl"))
    json.dump(rows, open(os.path.join(a.out, "subagent_probe.json"), "w"))
    print(len(rows), "subagent transcripts since", a.since, "->", os.path.join(a.out, "subagent_probe.json"))
