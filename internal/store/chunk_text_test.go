package store

import (
	"fmt"
	"strings"
	"testing"
)

func TestChunkPlainTextGolden(t *testing.T) {
	r := strings.Repeat
	lines := func(n int, f func(i int) string) string {
		out := make([]string, n)
		for i := range out {
			out[i] = f(i)
		}
		return strings.Join(out, "\n")
	}

	checkChunks(t, "pt1", ChunkPlainText("alpha one\nalpha two\n\nbeta one\n\n  \n\ngamma one\ngamma two", 20), []golden{
		{"alpha one", false, 19, "f5fb195ae410"},
		{"beta one", false, 8, "0b467e2c5890"},
		{"gamma one", false, 19, "a4096bb5bbb0"},
	})
	checkChunks(t, "pt2", ChunkPlainText(lines(45, func(i int) string { return fmt.Sprintf("line %d", i+1) }), 20), []golden{
		{"line 1", false, 150, "92ccbe470b6f"},
		{"line 19", false, 159, "42cb3d357b9d"},
		{"line 37", false, 71, "e51c35f213b9"},
	})
	checkChunks(t, "pt3", ChunkPlainText("short\noutput", 20), []golden{
		{"Output", false, 12, "21d4d13b6bf8"},
	})
	checkChunks(t, "pt4", ChunkPlainText("head\n"+r("x", 5000)+" "+r("y", 5000)+"\ntail", 20), []golden{
		{"Output", false, 4, "9f2e6d33a371"},
		{"Output (2.1)", false, 4096, "a2e659dacb46"},
		{"Output (3.2)", false, 4096, "443c0d8c55e2"},
		{"Output (4.3)", false, 1809, "10af494c33de"},
		{"Output (5)", false, 4, "0c62f876ef1d"},
	})
	p80 := "s1 " + r("p", 77)
	checkChunks(t, "pt5", ChunkPlainText("s1 "+r("p", 4500)+"\n\nsec two\n\nsec three", 20), []golden{
		{p80, false, 4096, "4362514a60d5"},
		{p80 + " (2.2)", false, 407, "04aa9474a367"},
		{"sec two", false, 7, "4f47c669ee8c"},
		{"sec three", false, 9, "c993fc189ef6"},
	})
	pt6 := lines(30, func(i int) string {
		if i == 0 {
			return ""
		}
		return fmt.Sprintf("%s %d", r("w", 300), i)
	})
	checkChunks(t, "pt6", ChunkPlainText(pt6, 20), []golden{
		{"Lines 1-20", false, 3943, "8d343a32cd22"},
		{"Lines 1-20 (2)", false, 1823, "8f47b7d9052d"},
		{r("w", 80), false, 3647, "41f582236db0"},
	})
	checkChunks(t, "pt7", ChunkPlainText(r("日本語テキスト", 400)+"\nend", 20), []golden{
		{"Output", false, 4095, "7692b63703f2"},
		{"Output (2.2)", false, 4095, "7692b63703f2"},
		{"Output (3.3)", false, 210, "2a8ca8bd94e6"},
		{"Output (4)", false, 3, "361e48d0308f"},
	})
	if got := ChunkPlainText(" \n\t\n", 20); got != nil {
		t.Fatalf("blank input gave %v", got)
	}
}
