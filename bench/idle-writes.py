#!/usr/bin/env python3 -I
"""Criterion 2: 8 idle `cmgo serve` processes write <= 0.1 MB/min over 60 s.

Run: go build -o bin/cmgo ./cmd/cmgo && python3 -I bench/idle-writes.py bin/cmgo [seconds]
"""
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import driver  # noqa: E402

N, LIMIT_MB_PER_MIN = 8, 0.1


def main(binary, seconds):
    env = driver.bench_env()
    url = driver.serve_html()
    servers = []
    for _ in range(N):
        s = driver.Server(binary, env)
        s.initialize()
        driver.call_every_tool(s, url)  # warm: DB open, schema, one write each
        servers.append(s)
    before = [driver.rusage(s.p.pid)[2] for s in servers]
    time.sleep(seconds)
    after = [driver.rusage(s.p.pid)[2] for s in servers]
    for s in servers:
        s.stop()
    written = sum(a - b for a, b in zip(after, before))
    rate = written / 2**20 * 60 / seconds
    print(f"{N} idle servers over {seconds}s: {written} bytes written = {rate:.3f} MB/min")
    ok = rate <= LIMIT_MB_PER_MIN
    print("PASS" if ok else f"FAIL: {rate:.3f} MB/min > {LIMIT_MB_PER_MIN}")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1], int(sys.argv[2]) if len(sys.argv) > 2 else 60))
