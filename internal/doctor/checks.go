package doctor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// checkStorage creates the data dir if needed and writes and removes a probe
// file in it.
func checkStorage(dir string) string {
	err := os.MkdirAll(dir, 0o755)
	if err == nil {
		var f *os.File
		if f, err = os.CreateTemp(dir, ".doctor-*"); err == nil {
			_, err = f.WriteString("ok")
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			os.Remove(f.Name())
		}
	}
	if err != nil {
		return fmt.Sprintf("[FAIL] Storage: %s — %v", dir, err)
	}
	return fmt.Sprintf("[OK] Storage: %s (writable)", dir)
}

// checkDatabase opens the store as serve does (migrations included), reads
// its schema version and runs the TS FTS5 probe.
func checkDatabase(path string) string {
	st, err := store.Open(path)
	if err != nil {
		return "[FAIL] Database: " + err.Error()
	}
	defer st.Close()
	v, err := st.SchemaVersion()
	if err == nil {
		err = store.ProbeFTS5()
	}
	if err != nil {
		return "[FAIL] Database: " + err.Error()
	}
	return fmt.Sprintf("[OK] Database: %s schema v%d, FTS5 PASS", path, v)
}

// checkExecutor is the TS server test (S:4196-4211) in the shell: echo ok
// must exit 0 and print ok.
func checkExecutor(ctx context.Context, ex Executor, timeout time.Duration) string {
	res, err := ex.Run(ctx, executor.Request{Language: executor.Shell, Code: "echo ok", Timeout: timeout})
	if err != nil {
		return "[FAIL] Executor: FAIL — " + err.Error()
	}
	if res.ExitCode == 0 && strings.TrimSpace(res.Stdout) == "ok" {
		return "[OK] Executor: PASS (shell)"
	}
	detail := ""
	if s := strings.TrimSpace(res.Stderr); s != "" {
		detail = " (" + hookio.JSPrefix(s, 200) + ")"
	}
	return fmt.Sprintf("[FAIL] Executor: FAIL — exit %d%s", res.ExitCode, detail)
}

// checkRuntimes lists the languages ctx_execute can run here.
func checkRuntimes(ex Executor) string {
	var names []string
	for _, l := range ex.Available() {
		names = append(names, string(l))
	}
	return fmt.Sprintf("[OK] Runtimes: %d/%d — %s", len(names), len(executor.Languages), strings.Join(names, ", "))
}
