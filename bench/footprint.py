#!/usr/bin/env python3 -I
"""Criterion 1: sum of phys_footprint for 8 `cmgo serve` processes <= 100 MB.

Run: go build -o bin/cmgo ./cmd/cmgo && python3 -I bench/footprint.py bin/cmgo
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import driver  # noqa: E402

N, LIMIT_MB = 8, 100.0


def main(binary):
    env = driver.bench_env()
    url = driver.serve_html()
    servers = []
    for _ in range(N):
        s = driver.Server(binary, env)
        s.initialize()
        driver.call_every_tool(s, url)
        servers.append(s)
    usage = [driver.rusage(s.p.pid) for s in servers]
    for s in servers:
        s.stop()
    rss = sum(u[0] for u in usage)
    fp = sum(u[1] for u in usage)
    print(f"{N} servers: RSS sum {rss:.1f} MB | phys_footprint sum {fp:.1f} MB ({fp / N:.1f}/proc)")
    ok = fp <= LIMIT_MB
    print("PASS" if ok else f"FAIL: phys_footprint sum {fp:.1f} MB > {LIMIT_MB} MB")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1]))
