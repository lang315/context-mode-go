package stats

import "testing"

func TestStatusLine(t *testing.T) {
	cases := []struct {
		in   Line
		want string
	}{
		{Line{}, "cmgo  ●  ready"},
		{Line{OK: false, SessionKept: 5000, LifetimeKept: 9000, Pct: 50}, "cmgo  ●  ready"},
		{Line{OK: true, LifetimeKept: 5000000}, "cmgo  ●  4.8 MB kept out lifetime"},
		{Line{OK: true}, "cmgo  ●  0 B kept out lifetime"},
		{Line{OK: true, SessionKept: 370000, LifetimeKept: 5000000, Pct: 98}, "cmgo  ●  361 KB this chat  ·  4.8 MB lifetime  ·  98% kept out"},
	}
	for _, c := range cases {
		if got := StatusLine(c.in); got != c.want {
			t.Errorf("StatusLine(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}
