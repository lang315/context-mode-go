"""Shared JSON-RPC driver for the cmgo acceptance scripts (macOS only).

Usage from the other scripts: start N `cmgo serve` processes against one temp
CMGO_DIR, run initialize, optionally call every tool once, and read per-process
memory and disk-write counters with proc_pid_rusage.
"""
import ctypes
import http.server
import json
import os
import subprocess
import tempfile
import threading

_libc = ctypes.CDLL("/usr/lib/libSystem.B.dylib")


def rusage(pid):
    """Return (resident MB, phys_footprint MB, diskio bytes written) or None."""
    # RUSAGE_INFO_V2: 16-byte uuid, then uint64 fields:
    # resident_size=6, phys_footprint=7, diskio_byteswritten=17
    b = (ctypes.c_uint64 * 64)()
    if _libc.proc_pid_rusage(pid, 2, ctypes.byref(b)) != 0:
        return None
    return b[2 + 6] / 2**20, b[2 + 7] / 2**20, b[2 + 17]


class Server:
    def __init__(self, binary, env):
        self.p = subprocess.Popen([binary, "serve"], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                  stderr=subprocess.PIPE, text=True, env=env)
        self.next_id = 1

    def rpc(self, method, params):
        i = self.next_id
        self.next_id += 1
        self.p.stdin.write(json.dumps({"jsonrpc": "2.0", "id": i, "method": method, "params": params}) + "\n")
        self.p.stdin.flush()
        while True:
            line = self.p.stdout.readline()
            if not line:
                raise RuntimeError("server exited: " + self.p.stderr.read())
            m = json.loads(line)
            if m.get("id") == i:
                if "error" in m:
                    raise RuntimeError(f"{method}: {m['error']}")
                return m["result"]

    def initialize(self):
        self.rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                "clientInfo": {"name": "bench", "version": "0"}})
        self.p.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n")
        self.p.stdin.flush()

    def call(self, name, args):
        r = self.rpc("tools/call", {"name": name, "arguments": args})
        text = "".join(c.get("text", "") for c in r.get("content", []))
        if r.get("isError"):
            raise RuntimeError(f"{name} returned isError: {text[:300]}")
        return text

    def stop(self):
        self.p.stdin.close()
        self.p.wait(timeout=10)


def serve_html():
    """Serve one small HTML page on 127.0.0.1; returns its URL."""
    class H(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            body = b"<html><body><h1>Bench</h1><p>context window protection</p></body></html>"
            self.send_response(200)
            self.send_header("Content-Type", "text/html")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *a):
            pass

    srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), H)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return f"http://127.0.0.1:{srv.server_address[1]}/page"


def bench_env(path=None):
    root = tempfile.mkdtemp(prefix="cmgo-bench-")
    project = os.path.join(root, "project")
    os.makedirs(project)
    with open(os.path.join(project, "notes.txt"), "w") as f:
        f.write("alpha\nbeta\ngamma\n")
    env = {**os.environ, "HOME": os.path.join(root, "home"), "CMGO_DIR": os.path.join(root, "data"),
           "CMGO_PROJECT_DIR": project}
    if path is not None:
        env["PATH"] = path
    return env


def call_every_tool(s, url):
    """One call per tool; uses only shell and python so Node is never needed."""
    s.call("ctx_execute", {"language": "shell", "code": "echo hello"})
    s.call("ctx_execute", {"language": "python", "code": "print(sum(range(10)))"})
    s.call("ctx_execute_file", {"path": "notes.txt", "language": "shell", "code": 'echo "$FILE_CONTENT" | wc -l'})
    s.call("ctx_batch_execute", {"commands": [{"label": "ls", "command": "ls"}], "queries": ["notes"]})
    s.call("ctx_fetch_and_index", {"url": url, "source": "bench"})
    s.call("ctx_search", {"queries": ["context window"]})
