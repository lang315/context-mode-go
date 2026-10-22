import sys
if sys.version_info < (3, 9) and sys.executable:
    import os
    os.execv(sys.executable, [sys.executable, sys.argv[2]])
def cm_main():
    counts, script = sys.argv[1], sys.argv[2]
    import os, io, builtins, atexit, warnings
    orig_open = builtins.open
    with orig_open(script, "rb") as f:
        src = f.read()
    n = [0]
    undo = []
    boot = set()
    f = sys._getframe()
    while f is not None:
        boot.add(f)
        f = f.f_back
    groups = getattr(builtins, "BaseExceptionGroup", ())
    pid = os.getpid()
    def install():
        class CFile(io.FileIO):
            def readinto(self, b):
                r = io.FileIO.readinto(self, b)
                if r:
                    n[0] += r
                return r
            def readall(self):
                d = io.FileIO.readall(self)
                if d:
                    n[0] += len(d)
                return d
        def is_source(fp):
            return fp.endswith((b".py", b".pyw") if isinstance(fp, bytes) else (".py", ".pyw"))
        def cm_open(file, mode="r", buffering=-1, encoding=None, errors=None, newline=None, closefd=True, opener=None):
            if (mode in ("r", "rt", "rb") and buffering == -1 and isinstance(file, (str, bytes, os.PathLike))
                    and not (isinstance(file, str) and os.path.abspath(file) == script)
                    and not (mode == "rb" and isinstance(file, (str, bytes, os.PathLike)) and is_source(os.fspath(file)))
                    and (mode != "rb" or (encoding is None and errors is None and newline is None))):
                file = os.fspath(file)
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
                    if encoding is None and hasattr(io, "text_encoding"):
                        encoding = io.text_encoding(None, 2)
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
            peek = m.MSG_PEEK
            def recv(self, *a, **k):
                r = o_recv(self, *a, **k)
                if not ((a[1] if len(a) > 1 else k.get("flags", 0)) & peek):
                    n[0] += len(r)
                return r
            def recv_into(self, *a, **k):
                r = o_into(self, *a, **k)
                if not ((a[2] if len(a) > 2 else k.get("flags", 0)) & peek):
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
            if os.getpid() != pid:
                return
            try:
                with orig_open(counts, "w", encoding="ascii") as f:
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
    def clean(tb):
        import types
        keep = []
        while tb is not None:
            if tb.tb_frame not in boot and tb.tb_frame.f_code.co_filename != "<cm>":
                keep.append(tb)
            tb = tb.tb_next
        out = None
        for x in reversed(keep):
            out = types.TracebackType(out, x.tb_frame, x.tb_lasti, x.tb_lineno)
        return out
    def hook(t, v, tb):
        saved, todo, seen = [], [v], set()
        while todo:
            e = todo.pop()
            if e is None or id(e) in seen:
                continue
            seen.add(id(e))
            saved.append((e, e.__traceback__))
            todo.append(e.__cause__)
            todo.append(e.__context__)
            if isinstance(e, groups):
                todo.extend(e.exceptions)
        try:
            for e, t0 in saved:
                e.__traceback__ = clean(t0)
            oh(t, v, v.__traceback__)
        finally:
            for e, t0 in saved:
                e.__traceback__ = t0
    sys.excepthook = hook
    sys.argv = [script]
    if not getattr(sys.flags, "safe_path", False):
        sys.path[0] = os.path.dirname(os.path.realpath(script))
    ns = sys.modules["__main__"].__dict__
    ns["__file__"] = script
    ns["__cached__"] = None
    ns["__loader__"] = sys.modules["_frozen_importlib_external"].SourceFileLoader("__main__", script)
    try:
        with warnings.catch_warnings(record=True) as ws:
            code = compile(src, script, "exec", dont_inherit=True)
    except (SyntaxError, ValueError):
        if sys.executable:
            os.execv(sys.executable, [sys.executable, script])
        raise
    for w in ws:
        warnings.warn_explicit(w.message, w.category, w.filename, w.lineno)
    try:
        exec(code, ns)
    except SystemExit:
        raise
    except BaseException:
        ns.pop("__file__", None)
        ns.pop("__cached__", None)
        raise
    ns.pop("__file__", None)
    ns.pop("__cached__", None)
cm_main()
