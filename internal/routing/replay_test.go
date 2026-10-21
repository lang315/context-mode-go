package routing

import (
	"os"
	"strings"
	"testing"
)

// The replay (bash-read-guidance spec §5.1) runs recorded Bash commands
// through estimateRead against the real files, where they still exist, and
// counts what each threshold would flag. A command whose files are gone is
// skipped, never guessed from its recorded result size.

// replayRow is one recorded Bash call.
type replayRow struct {
	Cwd, Command string
	Recorded     int64 // bytes the call returned
}

// replayStat is the outcome at one threshold.
type replayStat struct {
	Threshold     int64
	Flagged       int   // estimate at or above the threshold
	FlaggedBytes  int64 // recorded bytes of the flagged calls
	OverEstimates int   // flagged although the recorded result was under 5 KiB
	Replayed      int   // reader commands with at least one real file
	Skipped       int   // reader commands with no real file
	NotReader     int   // not a cat/nl/tac/bat/head/tail read (heredocs, sed, git, ...)
}

func isReader(command string) bool {
	chains, ok := tokenize(command)
	if !ok {
		return false
	}
	for _, ch := range chains {
		if len(ch) > 0 && len(ch[0].words) > 0 {
			switch ch[0].words[0].s {
			case "cat", "nl", "tac", "bat", "head", "tail":
				return true
			}
		}
	}
	return false
}

func replay(rows []replayRow, home string, thresholds ...int64) []replayStat {
	out := make([]replayStat, len(thresholds))
	for i, t := range thresholds {
		out[i].Threshold = t
	}
	for _, r := range rows {
		reader := isReader(r.Command)
		total, largest := estimateRead(r.Command, r.Cwd, home)
		for i := range out {
			switch {
			case !reader:
				out[i].NotReader++
			case largest == "":
				out[i].Skipped++
			default:
				out[i].Replayed++
				if total >= out[i].Threshold {
					out[i].Flagged++
					out[i].FlaggedBytes += r.Recorded
					if r.Recorded < 5120 {
						out[i].OverEstimates++
					}
				}
			}
		}
	}
	return out
}

func TestReplayFixture(t *testing.T) {
	proj, home := readFixture(t)
	rows := []replayRow{
		{proj, "cat a.txt", 20000},
		{proj, "cat b.txt", 3000},
		{proj, "cat gone.txt", 9000},
		{proj, "cd sub && cat c.txt", 29000},
		{proj, "sed -n '1,500p' a.txt", 31000},
		{proj, "cat > a.txt <<EOF\nx\nEOF", 0},
		{proj, "cat a.txt | head -5", 320},
	}
	got := replay(rows, home, 8192, 16384, 30000)
	want := []replayStat{
		{Threshold: 8192, Flagged: 2, FlaggedBytes: 49000, Replayed: 4, Skipped: 1, NotReader: 2},
		{Threshold: 16384, Flagged: 2, FlaggedBytes: 49000, Replayed: 4, Skipped: 1, NotReader: 2},
		{Threshold: 30000, Flagged: 1, FlaggedBytes: 29000, Replayed: 4, Skipped: 1, NotReader: 2},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("threshold %d:\n got %+v\nwant %+v", want[i].Threshold, got[i], want[i])
		}
	}
}

// The extension is listed in HOOKS-KNOWN-DIFFS.md and visible as a decision
// change: with the switch off the same command gets only the generic tip.
func TestKnownDiffsRowExists(t *testing.T) {
	b, err := os.ReadFile("../../testdata/golden/HOOKS-KNOWN-DIFFS.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "`routing:bash-read-guidance`") {
		t.Fatal("HOOKS-KNOWN-DIFFS.md has no routing:bash-read-guidance row")
	}
	on, _ := readDeps(t, nil)
	off, _ := readDeps(t, map[string]string{"CMGO_BASH_READ_MIN_BYTES": "0"})
	if a, b := Route("Bash", bash("cat a.txt"), false, on), Route("Bash", bash("cat a.txt"), false, off); a.AdditionalContext == b.AdditionalContext {
		t.Fatal("the listed difference is gone: the switch changes nothing")
	}
}
