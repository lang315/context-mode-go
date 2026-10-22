package executor

import (
	"context"
	"os"
	"testing"
	"time"
)

// BenchmarkPyRun compares a Python run with the counters off and on.
func BenchmarkPyRun(b *testing.B) {
	progs := map[string]string{
		"print":  "print(1)",
		"json":   "import json\nprint(json.dumps(1))",
		"urllib": "import urllib.request\nprint(1)",
	}
	for name, code := range progs {
		for _, mode := range []string{"off", "on"} {
			b.Run(name+"/"+mode, func(b *testing.B) {
				e := New(b.TempDir())
				e.getenv = func(k string) string {
					if k == "CMGO_PY_COUNTERS" && mode == "off" {
						return "0"
					}
					return os.Getenv(k)
				}
				for i := 0; i < b.N; i++ {
					if _, err := e.Run(context.Background(), Request{Language: Python, Code: code, Timeout: 30 * time.Second}); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
