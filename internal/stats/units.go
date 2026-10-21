// Package stats renders the ctx_stats report and the statusline line from
// numbers the caller has already queried. It does no I/O.
package stats

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// KB formats a byte count like TS kb() (src/session/analytics.ts:1961-1977).
func KB(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	k := float64(n) / 1024
	if k < 1024 {
		if k < 100 {
			return toFixed(k, 1) + " KB"
		}
		return strconv.FormatFloat(math.Round(k), 'f', 0, 64) + " KB"
	}
	m := k / 1024
	if m < 1024 {
		if m < 100 {
			return toFixed(m, 1) + " MB"
		}
		return strconv.FormatFloat(math.Round(m), 'f', 0, 64) + " MB"
	}
	g := m / 1024
	if g < 100 {
		return toFixed(g, 2) + " GB"
	}
	return toFixed(g, 1) + " GB"
}

// Duration formats an uptime like TS formatDuration (analytics.ts:1980-1987),
// including the caller's toFixed(1) on the minutes (analytics.ts:464).
func Duration(d time.Duration) string {
	if d < 0 {
		d = 0 // TS gives "< 1 min" for any negative uptime too
	}
	min, _ := strconv.ParseFloat(toFixed(float64(d.Milliseconds())/60000, 1), 64)
	if min < 1 {
		return "< 1 min"
	}
	if min < 60 {
		return strconv.FormatFloat(math.Round(min), 'f', 0, 64) + " min"
	}
	h := math.Floor(min / 60)
	m := math.Round(math.Mod(min, 60)) // can be 60, as in TS
	if m > 0 {
		return strconv.FormatFloat(h, 'f', 0, 64) + "h " + strconv.FormatFloat(m, 'f', 0, 64) + "m"
	}
	return strconv.FormatFloat(h, 'f', 0, 64) + "h"
}

// Pct is round(kept / raw * 100), or 0 when raw is 0.
func Pct(kept, raw int64) int {
	if raw == 0 {
		return 0
	}
	return int(math.Round(float64(kept) / float64(raw) * 100))
}

// toFixed is JS Number.prototype.toFixed for x >= 0: it rounds the exact
// binary value and takes the larger candidate on a tie, where Go's
// FormatFloat rounds to even (1.25 gives "1.3", not "1.2").
func toFixed(x float64, digits int) string {
	r := new(big.Rat).SetFloat64(x)
	r.Mul(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)))
	r.Add(r, big.NewRat(1, 2))
	s := new(big.Int).Quo(r.Num(), r.Denom()).String() // floor, as r > 0
	if len(s) <= digits {
		s = strings.Repeat("0", digits-len(s)+1) + s
	}
	return s[:len(s)-digits] + "." + s[len(s)-digits:]
}
