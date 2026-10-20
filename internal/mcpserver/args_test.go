package mcpserver

import (
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mustArgs(t *testing.T, raw string) Args {
	t.Helper()
	a, err := ParseArgs([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestParseArgsRejectsNonObject(t *testing.T) {
	if _, err := ParseArgs([]byte(`[1]`)); err == nil {
		t.Fatal("expected error for array arguments")
	}
	if a, err := ParseArgs(nil); err != nil || len(a) != 0 {
		t.Fatalf("empty raw: %v %v", a, err)
	}
}

func TestNumberCoercion(t *testing.T) {
	a := mustArgs(t, `{"n":1500,"s":" 2000 ","e":"","b":true,"bad":"abc","f":1.5}`)
	cases := []struct {
		key  string
		want float64
		err  bool
	}{{"n", 1500, false}, {"s", 2000, false}, {"e", 0, false}, {"b", 1, false}, {"bad", 0, true}, {"f", 1.5, false}}
	for _, c := range cases {
		got, ok, err := a.Number(c.key)
		if !ok || (err != nil) != c.err || (!c.err && got != c.want) {
			t.Errorf("%s: got %v ok=%v err=%v", c.key, got, ok, err)
		}
	}
	if _, ok, _ := a.Number("missing"); ok {
		t.Error("missing key reported present")
	}
}

func TestBoolCoercionOnlyLiterals(t *testing.T) {
	a := mustArgs(t, `{"t":true,"st":" TRUE ","sf":"false","x":"yes","n":1}`)
	for key, want := range map[string]bool{"t": true, "st": true, "sf": false} {
		got, ok, err := a.Bool(key)
		if !ok || err != nil || got != want {
			t.Errorf("%s: got %v %v %v", key, got, ok, err)
		}
	}
	for _, key := range []string{"x", "n"} {
		if _, _, err := a.Bool(key); err == nil {
			t.Errorf("%s: expected error", key)
		}
	}
}

func TestArrayCoercion(t *testing.T) {
	a := mustArgs(t, `{"arr":["a","b"],"json":"[\"x\",\"y\"]","bare":"single query","empty":"  "}`)
	for key, want := range map[string][]string{"arr": {"a", "b"}, "json": {"x", "y"}, "bare": {"single query"}} {
		got, ok, err := a.Strings(key)
		if !ok || err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v %v %v", key, got, ok, err)
		}
	}
	if _, _, err := a.Strings("empty"); err == nil {
		t.Error("empty string must not be lifted")
	}
}

func TestCommandsCoercion(t *testing.T) {
	a := mustArgs(t, `{"c":["ls","pwd"],"o":[{"label":"L","command":"git status"}],"j":"[{\"label\":\"A\",\"command\":\"echo hi\"}]","bad":[{"label":"x"}]}`)
	got, _, err := a.Commands("c")
	if err != nil || !reflect.DeepEqual(got, []Command{{"cmd_1", "ls"}, {"cmd_2", "pwd"}}) {
		t.Fatalf("bare strings: %v %v", got, err)
	}
	got, _, _ = a.Commands("o")
	if !reflect.DeepEqual(got, []Command{{"L", "git status"}}) {
		t.Fatalf("objects: %v", got)
	}
	got, _, _ = a.Commands("j")
	if !reflect.DeepEqual(got, []Command{{"A", "echo hi"}}) {
		t.Fatalf("json string: %v", got)
	}
	if _, _, err := a.Commands("bad"); err == nil {
		t.Fatal("missing command must error")
	}
}

func TestTextResultSanitizesUTF8(t *testing.T) {
	r := TextResult("ok \xff end", true)
	tc := r.Content[0].(*mcp.TextContent)
	if tc.Text != "ok � end" || !r.IsError {
		t.Fatalf("%q %v", tc.Text, r.IsError)
	}
}

func TestKB(t *testing.T) {
	for n, want := range map[int]string{0: "0.0KB", 1024: "1.0KB", 1536: "1.5KB", 102400: "100.0KB"} {
		if got := KB(n); got != want {
			t.Errorf("KB(%d)=%s want %s", n, got, want)
		}
	}
}
