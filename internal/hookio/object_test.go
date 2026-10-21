package hookio

import (
	"encoding/json"
	"reflect"
	"testing"
)

func mustParse(t *testing.T, s string) *Object {
	t.Helper()
	o, err := ParseObject([]byte(s))
	if err != nil {
		t.Fatalf("ParseObject(%q): %v", s, err)
	}
	return o
}

func TestParseObjectKeepsOrderAndRoundTrips(t *testing.T) {
	o := mustParse(t, "\xef\xbb\xbf \n{ \"z\": 1, \"a\" : { \"y\": [1, 2], \"b\": \"<&>\" }, \"m\": null }\n ")
	if got, want := o.Keys(), []string{"z", "a", "m"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Keys = %v, want %v", got, want)
	}
	out, err := o.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"z":1,"a":{"y":[1,2],"b":"<&>"},"m":null}`; string(out) != want {
		t.Errorf("MarshalJSON = %s, want %s", out, want)
	}
	// Marshal of a struct holding the object gives the same bytes.
	wrapped, err := Marshal(struct {
		O *Object `json:"o"`
		N *Object `json:"n"`
	}{O: o})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"o":` + string(out) + `,"n":null}`; string(wrapped) != want {
		t.Errorf("Marshal(wrapped) = %s, want %s", wrapped, want)
	}
}

func TestParseObjectEmptyAndErrors(t *testing.T) {
	for _, in := range []string{"", "  \n", "\xef\xbb\xbf", "\xef\xbb\xbf  "} {
		o := mustParse(t, in)
		if len(o.Keys()) != 0 {
			t.Errorf("ParseObject(%q) keys = %v, want none", in, o.Keys())
		}
		if out, _ := o.MarshalJSON(); string(out) != "{}" {
			t.Errorf("empty MarshalJSON = %s", out)
		}
	}
	for _, in := range []string{"[]", "null", `"x"`, "1", "{", `{"a":}`, `{"a":1}x`, `{"a":1}{}`, `{1:2}`} {
		if _, err := ParseObject([]byte(in)); err == nil {
			t.Errorf("ParseObject(%q) succeeded, want error", in)
		}
	}
}

func TestDuplicateKeyKeepsFirstPositionLastValue(t *testing.T) {
	o := mustParse(t, `{"a":1,"b":2,"a":3}`)
	out, _ := o.MarshalJSON()
	if want := `{"a":3,"b":2}`; string(out) != want {
		t.Errorf("got %s, want %s", out, want)
	}
}

func TestTypedGetters(t *testing.T) {
	o := mustParse(t, `{"s":"h\`+`u00e9","n":1,"t":true,"f":false,"o":{"k":"v"},"arr":[],"nul":null}`)
	if v, ok := o.String("s"); !ok || v != "hé" {
		t.Errorf(`String("s") = %q, %v`, v, ok)
	}
	for _, k := range []string{"n", "t", "o", "nul", "missing"} {
		if _, ok := o.String(k); ok {
			t.Errorf("String(%q) ok, want not ok", k)
		}
	}
	if v, ok := o.Bool("t"); !ok || !v {
		t.Errorf(`Bool("t") = %v, %v`, v, ok)
	}
	if v, ok := o.Bool("f"); !ok || v {
		t.Errorf(`Bool("f") = %v, %v`, v, ok)
	}
	for _, k := range []string{"s", "n", "nul", "missing"} {
		if _, ok := o.Bool(k); ok {
			t.Errorf("Bool(%q) ok, want not ok", k)
		}
	}
	if n, ok := o.Object("o"); !ok {
		t.Error(`Object("o") not ok`)
	} else if v, _ := n.String("k"); v != "v" {
		t.Errorf(`nested k = %q`, v)
	}
	for _, k := range []string{"arr", "nul", "s", "missing"} {
		if _, ok := o.Object(k); ok {
			t.Errorf("Object(%q) ok, want not ok", k)
		}
	}
	if raw, ok := o.Get("n"); !ok || string(raw) != "1" {
		t.Errorf(`Get("n") = %s, %v`, raw, ok)
	}
	if !o.Has("nul") || o.Has("missing") {
		t.Error("Has wrong")
	}
}

func TestSetAndClone(t *testing.T) {
	o := mustParse(t, `{"a":1,"b":2}`)
	c := o.Clone()
	o.Set("a", json.RawMessage(`[true]`))
	o.SetString("c", "<x>")
	out, _ := o.MarshalJSON()
	if want := `{"a":[true],"b":2,"c":"<x>"}`; string(out) != want {
		t.Errorf("after Set = %s, want %s", out, want)
	}
	cout, _ := c.MarshalJSON()
	if want := `{"a":1,"b":2}`; string(cout) != want {
		t.Errorf("clone changed: %s", cout)
	}
	var z Object // the zero value is usable
	z.SetString("k", "v")
	if zout, _ := z.MarshalJSON(); string(zout) != `{"k":"v"}` {
		t.Errorf("zero Object = %s", zout)
	}
}

// Raw values written by JS output U+2028 and stray-byte U+FFFD raw, so a
// re-emitted object does too; other escapes stay as received.
func TestMarshalJSONUnescapesLikeJS(t *testing.T) {
	in := `{"a":"x\` + `u2028y","b":"\` + `u0041\\` + `u2028"}`
	o := mustParse(t, in)
	out, _ := o.MarshalJSON()
	want := `{"a":"x` + ls + `y","b":"\` + `u0041\\` + `u2028"}`
	if string(out) != want {
		t.Errorf("MarshalJSON = %q, want %q", out, want)
	}
	if v, _ := o.String("a"); v != "x"+ls+"y" {
		t.Errorf("String(a) = %q", v)
	}
}
