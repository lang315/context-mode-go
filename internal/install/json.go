package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

// object is a JSON object that keeps key order, so a rewritten settings file
// differs from the original only where cmgo changed it. Values are *object,
// []any, string, json.Number (the original text), bool or nil.
type object struct {
	keys []string
	vals map[string]any
}

func newObject() *object { return &object{vals: map[string]any{}} }

func (o *object) get(k string) (any, bool) {
	v, ok := o.vals[k]
	return v, ok
}

// set replaces a value in place, or appends the key at the end.
func (o *object) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	o.keys = slices.DeleteFunc(o.keys, func(s string) bool { return s == k })
}

// parseJSON decodes exactly one JSON value. A duplicate key keeps its first
// position and its last value, like JSON.parse.
func parseJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("invalid data after the top-level value")
	}
	return v, nil
}

func parseValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err == io.EOF {
		return nil, io.ErrUnexpectedEOF
	}
	if err != nil {
		return nil, err
	}
	switch tok {
	case json.Delim('{'):
		o := newObject()
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := parseValue(dec)
			if err != nil {
				return nil, err
			}
			o.set(k.(string), v)
		}
		return o, closeDelim(dec)
	case json.Delim('['):
		a := []any{}
		for dec.More() {
			v, err := parseValue(dec)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		return a, closeDelim(dec)
	}
	if d, ok := tok.(json.Delim); ok {
		return nil, fmt.Errorf("unexpected %q", rune(d))
	}
	return tok, nil
}

func closeDelim(dec *json.Decoder) error {
	_, err := dec.Token()
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

// encodeJSON writes v like JS JSON.stringify(v, null, 2) + "\n": 2-space
// indent, "{}" and "[]" for empty values, no HTML escaping.
func encodeJSON(v any) []byte {
	var b bytes.Buffer
	encodeValue(&b, v, "")
	b.WriteByte('\n')
	return b.Bytes()
}

func encodeValue(b *bytes.Buffer, v any, indent string) {
	in := indent + "  "
	switch t := v.(type) {
	case *object:
		if len(t.keys) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteByte('{')
		for i, k := range t.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString("\n" + in)
			encodeString(b, k)
			b.WriteString(": ")
			encodeValue(b, t.vals[k], in)
		}
		b.WriteString("\n" + indent + "}")
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString("\n" + in)
			encodeValue(b, e, in)
		}
		b.WriteString("\n" + indent + "]")
	case string:
		encodeString(b, t)
	case json.Number:
		b.WriteString(string(t))
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case nil:
		b.WriteString("null")
	}
}

func encodeString(b *bytes.Buffer, s string) {
	e := json.NewEncoder(b)
	e.SetEscapeHTML(false)
	_ = e.Encode(s)         // a string always encodes
	b.Truncate(b.Len() - 1) // Encode adds a newline
}
