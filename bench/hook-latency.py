#!/usr/bin/env python3 -I
"""Criterion 1: `cmgo-hook <event>` wall time p95 <= 10 ms per case (spec §6.4,
session memory spec §0 criterion 4, stats spec §0 criterion 3).

Run: go build -o bin/ ./cmd/cmgo-hook && python3 -I bench/hook-latency.py bin/cmgo-hook [runs]

Every case runs `runs` times against a temp CMGO_DIR, HOME and project dir
(never the real ones), after one warm-up call that creates the DB. The
precompact and sessionstart compact cases run against a session pre-filled
to 1000 events (the per-session cap) through the hook itself. The time
is subprocess.run wall clock, so it includes the fork/exec cost. Then
posttooluse runs from 4 threads at once, `runs` fires each, into the same DB.
Last, `cmgo-hook statusline` runs against a second DB that the binary creates
and migrates and that this script fills with sqlite3 to 100 sessions x 500
events plus linked tool_stats rows; the DB file must not change.
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
STAT_SESSIONS, STAT_EVENTS = 100, 500


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


def fill_stats(binary, env, db):
    """Lets the binary create and migrate db, then replaces its rows with
    STAT_SESSIONS sessions of STAT_EVENTS ~400-byte events each (1 in 20 a
    redirect with bytes_avoided), per session six tool_stats rows whose
    window is its id plus six of an older session of the same Claude Code
    process, and the stats_totals row of each project."""
    fire(binary, env, "posttooluse", read_payload(-1, "warm"))
    con = sqlite3.connect(db)
    try:
        if con.execute("PRAGMA user_version").fetchone()[0] < 3:
            raise SystemExit("stats fill: DB is older than migration 3")
        con.execute("DELETE FROM session_events")
        con.execute("DELETE FROM session_meta")
        con.execute("DELETE FROM tool_stats")
        con.execute("DELETE FROM stats_totals")
        data = "x" * 400
        for s in range(STAT_SESSIONS):
            sid, project, started = f"stat-{s}", f"/bench/p{s % 5}", f"2026-10-{1 + s % 20:02d} 10:{s % 60:02d}:00"
            con.execute("INSERT INTO session_meta(session_id, project, started_at, last_event_at, event_count, cc_pid, linked_at) "
                        "VALUES (?, ?, ?, ?, ?, ?, ?)", (sid, project, started, started, STAT_EVENTS, 1000 + s, started + ".000"))
            con.executemany(
                "INSERT INTO session_events(session_id, project, type, category, priority, data, data_hash, source_hook, "
                "bytes_avoided, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
                [(sid, project, "file_read", "redirect" if e % 20 == 0 else "file", 2, data, f"{s:08X}{e:08X}",
                  "PostToolUse", 8192 if e % 20 == 0 else 0, started) for e in range(STAT_EVENTS)])
            for window in (sid, f"older-{sid}"):
                con.executemany(
                    "INSERT INTO tool_stats(server_id, cc_pid, project, tool, calls, bytes_returned, bytes_kept, "
                    "started_at, window, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
                    [(f"srv-{s}", 1000 + s, project, f"ctx_tool{t}", 10, 4000, 90000, started, window, started)
                     for t in range(6)])
        con.execute("INSERT INTO stats_totals(project, calls, bytes_returned, bytes_kept, first_at) "
                    "SELECT project, SUM(calls), SUM(bytes_returned), SUM(bytes_kept), MIN(started_at) FROM tool_stats GROUP BY project")
        con.commit()
        n = con.execute("SELECT count(*), count(DISTINCT session_id) FROM session_events").fetchone()
    finally:
        con.close()
    if n != (STAT_SESSIONS * STAT_EVENTS, STAT_SESSIONS):
        raise SystemExit(f"stats fill: {n[0]} events in {n[1]} sessions")


def main(binary, runs):
    binary = os.path.abspath(binary)
    root = tempfile.mkdtemp(prefix="cmgo-hook-bench-")
    try:
        data, home, project = (os.path.join(root, d) for d in ("data", "home", "project"))
        for d in (home, project):
            os.makedirs(d)
        # The Bash read guidance is off unless set; 16384 is the old default, so older
        # binaries time the same work.
        env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "HOME": home,
               "CMGO_DIR": data, "CMGO_PROJECT_DIR": project, "CMGO_BASH_READ_MIN_BYTES": "16384"}
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

        # Bash read guidance: a 100 KB file, and eight 4 KB files for the 8-stat compound.
        with open(os.path.join(project, "big.txt"), "wb") as f:
            f.write(b"x" * 102400)
        for n in range(1, 9):
            with open(os.path.join(project, f"f{n}.txt"), "wb") as f:
                f.write(b"y" * 4096)
        cases = [
            ("pretooluse passthrough", "pretooluse",
             lambda i: {"tool_name": "Bash", "tool_input": {"command": "git status"}, "session_id": "bench"},
             lambda out: out == b""),
            ("pretooluse curl redirect", "pretooluse",
             lambda i: {"tool_name": "Bash", "tool_input": {"command": "curl https://example.com"}, "session_id": "bench"},
             lambda out: b'"permissionDecision":"deny"' in out),
            ("pretooluse bash large cat", "pretooluse",
             lambda i: {"tool_name": "Bash", "tool_input": {"command": "cat big.txt"}, "cwd": project, "session_id": "bench-cat"},
             lambda out: b"<context_guidance>" in out and b"about 100 KB (big.txt)" in out),
            ("pretooluse bash compound (8 stats)", "pretooluse",
             lambda i: {"tool_name": "Bash", "session_id": "bench-compound", "cwd": project,
                        "tool_input": {"command": f"cd {project} && cat " + " ".join(f"f{n}.txt" for n in range(1, 9))}},
             lambda out: b"<context_guidance>" in out and b"about 32 KB (f1.txt)" in out),
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

        stat_env = dict(env, CMGO_DIR=os.path.join(root, "stat-data"))
        stat_db = os.path.join(stat_env["CMGO_DIR"], "cmgo.db")
        fill_stats(binary, stat_env, stat_db)
        db_before = (os.stat(stat_db).st_size, os.stat(stat_db).st_mtime_ns)
        times = []
        for i in range(runs):
            ms, out = fire(binary, stat_env, "statusline", {"session_id": f"stat-{i % STAT_SESSIONS}"})
            if i == 0 and not (out.startswith("cmgo  ●  ".encode()) and b" this chat " in out):
                raise SystemExit(f"statusline: unexpected stdout {out[:200]!r}")
            times.append(ms)
        ok &= report(f"statusline ({STAT_SESSIONS} sessions, {STAT_SESSIONS * STAT_EVENTS} events)", times)
        if (os.stat(stat_db).st_size, os.stat(stat_db).st_mtime_ns) != db_before:
            print("FAIL: statusline changed the DB file")
            ok = False

        for log in (os.path.join(d, "hook-errors.log") for d in (data, stat_env["CMGO_DIR"])):
            if not os.path.exists(log):
                continue
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
