package store

import (
	"fmt"
	"strings"
	"testing"
)

func TestJSNumber(t *testing.T) {
	// Expected values from node: String(x).
	cases := []struct {
		in   float64
		want string
	}{
		{1.0, "1"}, {1e21, "1e+21"}, {1e-7, "1e-7"}, {0.000001, "0.000001"},
		{123456789012345678901, "123456789012345680000"}, {0, "0"}, {1.5e300, "1.5e+300"},
		{0.1, "0.1"}, {100, "100"}, {1e20, "100000000000000000000"}, {123.456, "123.456"},
		{-2.5e-10, "-2.5e-10"}, {5e-324, "5e-324"}, {1.7976931348623157e308, "1.7976931348623157e+308"},
		{0.000123, "0.000123"}, {1234567.0001, "1234567.0001"},
	}
	for _, c := range cases {
		if got := jsNumber(c.in); got != c.want {
			t.Errorf("jsNumber(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestChunkJSONGolden(t *testing.T) {
	r := strings.Repeat
	chunk := func(name, text string, want []golden) {
		t.Helper()
		got, err := ChunkJSON(text, ChunkMaxBytes)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		checkChunks(t, name, got, want)
	}
	objs := func(n int, f func(i int) string) string {
		out := make([]string, n)
		for i := range out {
			out[i] = f(i)
		}
		return "[" + strings.Join(out, ",") + "]"
	}

	chunk("js1", `{"name":"demo","config":{"b":1,"2":true,"a":[1,2],"1":null},"empty":{},"list":[]}`, []golden{
		{"name", true, 6, "99e5095aacce"},
		{"config > 1", true, 4, "74234e98afe7"},
		{"config > 2", true, 4, "b5bea41b6c62"},
		{"config > b", true, 1, "6b86b273ff34"},
		{"config > a", true, 12, "17f5ce5ea0f8"},
		{"empty", true, 2, "44136fa355b3"},
		{"list", true, 2, "4f53cda18c2b"},
	})
	chunk("js2", `{"users":`+objs(60, func(i int) string {
		return fmt.Sprintf(`{"id":%d,"name":"user%d","bio":"%s"}`, i, i, r("x", 150))
	})+`}`, []golden{
		{"users > 0…18", true, 3953, "286804bcad7b"},
		{"users > 19…37", true, 3973, "851737e4735b"},
		{"users > 38…56", true, 3973, "ef891e4a6087"},
		{"users > 57, 58, 59", true, 629, "58a88a315846"},
	})
	chunk("js3", `[{"title":"A","v":1},{"title":"B","v":2}]`, []golden{
		{"(root)", true, 78, "697c41497aaa"},
	})
	chunk("js4", `{"n":[1.0, 1e21, 1e-7, 0.000001, 123456789012345678901, -0, 1.5e300, 0.1], "s":"<&>\u2028\u0001\"\\é", "big":"`+r("q", 5000)+`"}`, []golden{
		{"n", true, 85, "9d24013c5c98"},
		{"s", true, 20, "ac4f0188c9c8"},
		{"big", false, 5002, "7b8b9a69c0f7"},
	})
	chunk("js5", objs(40, func(i int) string { return fmt.Sprintf(`{"k":%d,"pad":"%s"}`, i, r("y", 200)) }), []golden{
		{"(root) > [0-16]", true, 4004, "6a8bdb1a9023"},
		{"(root) > [17-33]", true, 4014, "91fd6f5db46f"},
		{"(root) > [34-39]", true, 1418, "4c96b42e6380"},
	})
	chunk("js6", `{"items":`+objs(30, func(i int) string { return fmt.Sprintf(`{"slug":"s-%d","pad":"%s"}`, i, r("z", 300)) })+`}`, []golden{
		{"items > s-0…s-10", true, 3765, "58a918ff776b"},
		{"items > s-11…s-21", true, 3775, "4d391248663b"},
		{"items > s-22…s-29", true, 2746, "c9026658dafa"},
	})
	chunk("js7", `"just a string"`, []golden{{"(root)", true, 15, "3fe01def54b1"}})

	for _, bad := range []string{"not json {", "{} x", "", "  "} {
		if _, err := ChunkJSON(bad, ChunkMaxBytes); err == nil {
			t.Errorf("ChunkJSON(%q) want error", bad)
		}
	}
	// TS indexJSON falls back to plain text for invalid JSON (js8 golden).
	checkChunks(t, "js8", ChunkPlainText("not json {", 20), []golden{{"Output", false, 10, "c3f07c17117d"}})
}
