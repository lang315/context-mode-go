import sys
if sys.version_info < (3, 9) and sys.executable:
    import os
    os.execv(sys.executable, [sys.executable, sys.argv[2]])
def cm_main():
    counts, script = sys.argv[1], sys.argv[2]
    import os, io, builtins, atexit
    orig_open = builtins.open
    with orig_open(script, "rb") as f:
        src = f.read()
    n = [0]
    undo = []
    def install():
        class CFile(io.FileIO):
            def readinto(self, b):
                r = io.FileIO.readinto(self, b)
                if r:
                    n[0] += r
                return r
            def readall(self):
                d = io.FileIO.readall(self)
                n[0] += len(d)
                return d
        def cm_open(file, mode="r", buffering=-1, encoding=None, errors=None, newline=None, closefd=True, opener=None):
            if (mode in ("r", "rt", "rb") and buffering == -1 and isinstance(file, (str, bytes, os.PathLike))
                    and not (isinstance(file, str) and os.path.abspath(file) == script)
                    and (mode != "rb" or (encoding is None and errors is None and newline is None))):
                raw = CFile(file, "r", closefd, opener)
                try:
                    size = io.DEFAULT_BUFFER_SIZE
                    try:
                        bs = os.fstat(raw.fileno()).st_blksize
                        if bs > 1:
                            size = bs
                    except Exception:
                        pass
                    buf = io.BufferedReader(raw, size)
                    if mode == "rb":
                        return buf
                    txt = io.TextIOWrapper(buf, encoding, errors, newline, raw.isatty())
                    txt.mode = mode
                    return txt
                except BaseException:
                    raw.close()
                    raise
            return orig_open(file, mode, buffering, encoding, errors, newline, closefd, opener)
        builtins.open = io.open = cm_open
        undo.append(lambda: (setattr(builtins, "open", orig_open), setattr(io, "open", orig_open)))

        def count_socket(m):
            S = m.socket
            if "_cm" in S.__dict__:
                return
            o_recv, o_into = S.recv, S.recv_into
            def recv(self, *a, **k):
                r = o_recv(self, *a, **k)
                n[0] += len(r)
                return r
            def recv_into(self, *a, **k):
                r = o_into(self, *a, **k)
                n[0] += r
                return r
            S.recv, S.recv_into, S._cm = recv, recv_into, True
        def count_ssl(m):
            S = m.SSLSocket
            if "_cm" in S.__dict__:
                return
            o_read = S.read
            def read(self, *a, **k):
                r = o_read(self, *a, **k)
                n[0] += r if isinstance(r, int) else len(r)
                return r
            S.read, S._cm = read, True
        patchers = {"socket": count_socket, "ssl": count_ssl}
        class Finder:
            busy = set()
            def find_spec(self, name, path=None, target=None):
                if name not in patchers or name in self.busy:
                    return None
                self.busy.add(name)
                try:
                    spec = None
                    for f in sys.meta_path:
                        fs = getattr(f, "find_spec", None)
                        if f is self or fs is None:
                            continue
                        spec = fs(name, path, target)
                        if spec is not None:
                            break
                finally:
                    self.busy.discard(name)
                if spec is None or spec.loader is None or not hasattr(spec.loader, "exec_module"):
                    return spec
                ld = spec.loader
                oe = ld.exec_module
                def exec_module(module):
                    oe(module)
                    try:
                        patchers[name](module)
                    except Exception:
                        pass
                ld.exec_module = exec_module
                return spec
        fin = Finder()
        sys.meta_path.insert(0, fin)
        undo.append(lambda: fin in sys.meta_path and sys.meta_path.remove(fin))
        for name, p in patchers.items():
            if name in sys.modules:
                p(sys.modules[name])
        def flush():
            try:
                with orig_open(counts, "w") as f:
                    f.write(str(n[0]))
            except BaseException:
                pass
        atexit.register(flush)
    try:
        install()
    except BaseException:
        for u in reversed(undo):
            try:
                u()
            except BaseException:
                pass
    oh = sys.excepthook
    def hook(t, v, tb):
        while tb is not None and tb.tb_frame.f_code.co_filename in ("<string>", "<cm>"):
            tb = tb.tb_next
        oh(t, v.with_traceback(tb), tb)
    sys.excepthook = hook
    sys.argv = [script]
    if not getattr(sys.flags, "safe_path", False):
        sys.path[0] = os.path.dirname(script)
    ns = sys.modules["__main__"].__dict__
    ns["__file__"] = script
    ns["__cached__"] = None
    ns["__loader__"] = sys.modules["_frozen_importlib_external"].SourceFileLoader("__main__", script)
    try:
        code = compile(src, script, "exec", dont_inherit=True)
    except SyntaxError:
        if sys.executable:
            os.execv(sys.executable, [sys.executable, script])
        raise
    exec(code, ns)
cm_main()
