package install

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONRoundTrip(t *testing.T) {
	// Already in JSON.stringify(x, null, 2) form, so it must come back byte for byte.
	in := `{
  "z": 1.50,
  "a": [
    1e3,
    -0,
    true,
    false,
    null,
    "<b>&amp;</b> \"q\" \\ é 😀"
  ],
  "empty": {},
  "list": [],
  "nested": {
    "k": {
      "x": []
    }
  }
}
`
	v, err := parseJSON([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(encodeJSON(v)); got != in {
		t.Fatalf("round trip:\n%s\nwant:\n%s", got, in)
	}
}

func TestJSONReformat(t *testing.T) {
	v, err := parseJSON([]byte(` {"b":{"c":[1,{}]},"a":"é<"} `))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"b\": {\n    \"c\": [\n      1,\n      {}\n    ]\n  },\n  \"a\": \"é<\"\n}\n"
	if got := string(encodeJSON(v)); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestJSONDuplicateKey(t *testing.T) {
	// JSON.parse keeps the first position and the last value.
	v, err := parseJSON([]byte(`{"a":1,"b":2,"a":3}`))
	if err != nil {
		t.Fatal(err)
	}
	o := v.(*object)
	if strings.Join(o.keys, ",") != "a,b" || o.vals["a"] != json.Number("3") {
		t.Fatalf("keys %v vals %v", o.keys, o.vals)
	}
}

func TestJSONErrors(t *testing.T) {
	for _, in := range []string{``, `{"a": 1,`, `{"a":1,}`, `{"a":1} x`, `{} {}`, `[1`, `{"a" 1}`} {
		if _, err := parseJSON([]byte(in)); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
}

func TestObjectEdit(t *testing.T) {
	o := newObject()
	o.set("a", "1")
	o.set("b", "2")
	o.set("c", "3")
	o.set("a", "x")
	o.del("b")
	o.del("missing")
	if strings.Join(o.keys, ",") != "a,c" || o.vals["a"] != "x" {
		t.Fatalf("keys %v vals %v", o.keys, o.vals)
	}
	if _, ok := o.get("b"); ok {
		t.Fatal("b still present")
	}
}
