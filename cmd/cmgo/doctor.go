package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/doctor"
	"github.com/lang315/context-mode-go/internal/executor"
)

// doctorCmd runs `cmgo doctor` (spec §1): it prints the report and exits 1
// when a line is [FAIL].
func doctorCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "cmgo doctor: unexpected argument %q\n", args[0])
		return 2
	}
	cfg, err := config.Load(os.Getenv, os.Getwd)
	if err != nil {
		fmt.Fprintf(stderr, "cmgo: config: %v\n", err)
		return 1
	}
	ex := executor.New(cfg.ProjectDir)
	defer ex.Shutdown()
	text, failed := doctor.Report(context.Background(), doctor.NewEnv(cfg, version, ex))
	fmt.Fprintln(stdout, text)
	if failed {
		return 1
	}
	return 0
}
