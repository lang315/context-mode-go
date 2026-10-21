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
