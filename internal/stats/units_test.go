package stats

import (
	"testing"
	"time"
)

func TestKB(t *testing.T) {
	// Expected values from node: kb(b) imported from build/session/analytics.js.
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"}, {-5, "0 B"}, {1, "1 B"}, {512, "512 B"}, {1023, "1023 B"},
		{1024, "1.0 KB"}, {1075, "1.0 KB"}, {1126, "1.1 KB"},
		// Exact ties: JS toFixed rounds up, Go's FormatFloat rounds to even.
		{1280, "1.3 KB"}, {2816, "2.8 KB"}, {3328, "3.3 KB"},
		{102399, "100.0 KB"}, {102400, "100 KB"}, {102912, "101 KB"}, {1048575, "1024 KB"},
		{1048576, "1.0 MB"}, {1310720, "1.3 MB"}, {104857599, "100.0 MB"}, {104857600, "100 MB"}, {1073741823, "1024 MB"},
		{1073741824, "1.00 GB"}, {1207959552, "1.13 GB"}, {1476395008, "1.38 GB"},
		{107374182399, "100.00 GB"}, {107374182400, "100.0 GB"}, {1099511627776, "1024.0 GB"},
	}
	for _, c := range cases {
		if got := KB(c.in); got != c.want {
			t.Errorf("KB(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDuration(t *testing.T) {
	// Expected values from node: formatDuration((ms / 60000).toFixed(1)),
	// the function text taken from build/session/analytics.js.
	cases := []struct {
		ms   int64
		want string
	}{
		{-5000, "< 1 min"}, {0, "< 1 min"}, {1000, "< 1 min"},
		{57000, "< 1 min"}, // 0.95 is stored just below .95, so toFixed(1) gives "0.9"
		{57001, "1 min"}, {59999, "1 min"}, {60000, "1 min"}, {89999, "2 min"}, {90000, "2 min"},
		{3539999, "59 min"}, {3597000, "1h"}, {3597600, "1h"}, {3600000, "1h"}, {3630000, "1h 1m"},
		{7173000, "1h 60m"}, {7176000, "1h 60m"}, // TS quirk: Math.round(min % 60) can be 60
		{7200000, "2h"}, {86400000, "24h"},
	}
	for _, c := range cases {
		if got := Duration(time.Duration(c.ms) * time.Millisecond); got != c.want {
			t.Errorf("Duration(%dms) = %q, want %q", c.ms, got, c.want)
		}
	}
}

func TestPct(t *testing.T) {
	cases := []struct {
		kept, raw int64
		want      int
	}{
		{0, 0, 0}, {5, 0, 0}, {0, 10, 0}, {1, 3, 33}, {2, 3, 67}, {1, 200, 1}, {1, 201, 0}, {10, 10, 100},
	}
	for _, c := range cases {
		if got := Pct(c.kept, c.raw); got != c.want {
			t.Errorf("Pct(%d, %d) = %d, want %d", c.kept, c.raw, got, c.want)
		}
	}
}
