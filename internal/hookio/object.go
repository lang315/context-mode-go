package hookio

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode"
)

// Object is a JSON object that keeps key order, as a JS object does, so a
// hook can echo tool_input back with its keys where the host put them.
// Values stay raw JSON. The zero value is an empty object.
type Object struct {
	keys []string
	vals map[string]json.RawMessage
}

// ParseObject parses b as one JSON object. Like TS parseStdin it first trims
// a BOM and surrounding space, and empty input gives an empty object.
// Anything else that is not exactly one JSON object is an error.
func ParseObject(b []byte) (*Object, error) {
	// JS trim() also strips U+FEFF, which unicode.IsSpace does not.
	b = bytes.TrimFunc(b, func(r rune) bool { return unicode.IsSpace(r) || r == 0xFEFF })
	o := &Object{}
	if len(b) == 0 {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if tok, err := dec.Token(); err != nil {
		return nil, err
	} else if tok != json.Delim('{') {
		return nil, errors.New("hookio: input is not a JSON object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string) // the decoder only yields strings in key position
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		o.Set(key, raw) // a repeated key keeps its first position, last value, as JSON.parse does
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("hookio: data after the JSON object")
	}
	return o, nil
}

// Keys returns the keys in order.
func (o *Object) Keys() []string {
	return append([]string(nil), o.keys...)
}

// Get returns the raw value for key.
func (o *Object) Get(key string) (json.RawMessage, bool) {
	v, ok := o.vals[key]
	return v, ok
}

// Has reports whether key is present, whatever its value.
func (o *Object) Has(key string) bool {
	_, ok := o.vals[key]
	return ok
}

// String returns the value for key when it is a JSON string.
func (o *Object) String(key string) (string, bool) {
	raw, ok := o.vals[key]
	if !ok || len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// Bool returns the value for key when it is JSON true or false.
func (o *Object) Bool(key string) (v bool, ok bool) {
	switch string(o.vals[key]) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// Object returns the value for key when it is a JSON object.
func (o *Object) Object(key string) (*Object, bool) {
	raw, ok := o.vals[key]
	if !ok || len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	n, err := ParseObject(raw)
	return n, err == nil
}

// Set replaces the value for key in place, or appends key at the end.
func (o *Object) Set(key string, v json.RawMessage) {
	if o.vals == nil {
		o.vals = map[string]json.RawMessage{}
	}
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

// SetString sets key to the JSON string v.
func (o *Object) SetString(key, v string) {
	o.Set(key, QuoteString(v))
}

// MarshalJSON writes the object compact with keys in order. Values keep the
// bytes they arrived with, compacted (json.Compact never adds escapes), except
// that the escapes JSON.stringify writes raw are unescaped, so the output is
// what a JS JSON.parse + JSON.stringify round trip gives for those characters.
func (o *Object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(QuoteString(k))
		buf.WriteByte(':')
		if err := json.Compact(&buf, o.vals[k]); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return unescapeJS(buf.Bytes()), nil
}

// Clone returns a copy whose keys and values can be set without touching o.
func (o *Object) Clone() *Object {
	c := &Object{keys: o.Keys(), vals: make(map[string]json.RawMessage, len(o.vals))}
	for k, v := range o.vals {
		c.vals[k] = v
	}
	return c
}
