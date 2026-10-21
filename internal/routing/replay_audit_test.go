//go:build audit

// Replays the owner's transcript audit against the files still on this
// machine, so it reads the real home. It is not part of the normal build:
//
//	CMGO_AUDIT_JSON=<audit>/transcripts-raw.json go test -tags audit -run TestReplayAudit -v ./internal/routing
//
// It reports, it does not assert. The unit test of replay() with temp files
// is TestReplayFixture in replay_test.go.
package routing

import (
	"encoding/json"
	"os"
	"testing"
)

// TestReplayAudit replays the audit named by CMGO_AUDIT_JSON.
func TestReplayAudit(t *testing.T) {
	path := os.Getenv("CMGO_AUDIT_JSON")
	if path == "" {
		t.Skip("CMGO_AUDIT_JSON not set")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Waste []struct {
			Tool, Project, Cmd string
			Bytes              int64
		}
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	var rows []replayRow
	var total int64
	for _, w := range raw.Waste {
		if w.Tool == "Bash" {
			rows = append(rows, replayRow{w.Project, w.Cmd, w.Bytes})
			total += w.Bytes
		}
	}
	home, _ := os.UserHomeDir()
	for _, s := range replay(rows, home, 8192, 12288, 16384, 20480, 30000) {
		t.Logf("threshold %6d: flagged %4d (%5.1f%% of %d KB large-Bash bytes), over-estimates %d, replayed %d, skipped %d (%.0f%%), not a reader %d",
			s.Threshold, s.Flagged, 100*float64(s.FlaggedBytes)/float64(total), total/1024, s.OverEstimates, s.Replayed, s.Skipped,
			100*float64(s.Skipped)/float64(max(1, s.Replayed+s.Skipped)), s.NotReader)
	}
}
