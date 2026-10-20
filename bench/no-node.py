#!/usr/bin/env python3 -I
"""Criterion 4: every tool works with node and bun absent from PATH
(JavaScript/TypeScript inside ctx_execute excepted).

Run: go build -o bin/cmgo ./cmd/cmgo && python3 -I bench/no-node.py bin/cmgo
"""
import os
import shutil
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import driver  # noqa: E402


def path_without_js_runtimes():
    keep = []
    for d in os.environ.get("PATH", "").split(os.pathsep):
        if any(os.path.exists(os.path.join(d, b)) for b in ("node", "bun")):
            continue
        keep.append(d)
    return os.pathsep.join(keep)


def main(binary):
    path = path_without_js_runtimes()
    for b in ("node", "bun"):
        if shutil.which(b, path=path):
            print(f"FAIL: could not remove {b} from PATH")
            return 1
    s = driver.Server(binary, driver.bench_env(path=path))
    s.initialize()
    try:
        driver.call_every_tool(s, driver.serve_html())
    except RuntimeError as e:
        print(f"FAIL: {e}")
        return 1
    finally:
        s.stop()
    print("PASS: all 5 tools ran without node or bun on PATH")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1]))
