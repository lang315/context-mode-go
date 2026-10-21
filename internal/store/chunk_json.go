package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/lang315/context-mode-go/internal/hookio"
)

// jsonObj is a parsed JSON object with JavaScript property order:
// integer-like keys ascending first, then the rest in insertion order.
type jsonObj struct {
	keys []string
	vals map[string]any
}

// Parsed values: nil, bool, float64, string, []any, *jsonObj.

// ChunkJSON walks a JSON document and emits chunks titled by key path
// ("a > b"); arrays are batched under maxBytes and titled by an identity
// field or index range. Port of indexJSON/#walkJSON/#chunkJSONArray
// (ST:1051-1076, ST:1978-2113). Returns an error for invalid or empty JSON;
// the caller then falls back to ChunkPlainText(text, 20), as TS does (also
// when no chunks come back).
func ChunkJSON(text string, maxBytes int) ([]Chunk, error) {
	if hookio.JSTrim(text) == "" {
		return nil, errors.New("empty JSON")
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	v, err := decodeJSON(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data after JSON value")
	}
	var chunks []Chunk
	walkJSON(v, nil, &chunks, maxBytes)
	return chunks, nil
}

func decodeJSON(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '[' {
			arr := []any{}
			for dec.More() {
				v, err := decodeJSON(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			_, err := dec.Token()
			return arr, err
		}
		obj := &jsonObj{vals: map[string]any{}}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			k := kt.(string)
			v, err := decodeJSON(dec)
			if err != nil {
				return nil, err
			}
			if _, dup := obj.vals[k]; !dup {
				obj.keys = append(obj.keys, k)
			}
			obj.vals[k] = v
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		var ints, rest []string
		for _, k := range obj.keys {
			if isArrayIndex(k) {
				ints = append(ints, k)
			} else {
				rest = append(rest, k)
			}
		}
		slices.SortFunc(ints, func(a, b string) int {
			x, _ := strconv.ParseUint(a, 10, 64)
			y, _ := strconv.ParseUint(b, 10, 64)
			return int(x) - int(y)
		})
		obj.keys = append(ints, rest...)
		return obj, nil
	case json.Number:
		f, err := strconv.ParseFloat(string(t), 64)
		if err != nil && !math.IsInf(f, 0) {
			return nil, err
		}
		return f, nil
	default: // nil, bool, string
		return t, nil
	}
}

// isArrayIndex: canonical uint32 below 2^32-1, which JS orders first.
func isArrayIndex(k string) bool {
	if k == "" || len(k) > 10 || (len(k) > 1 && k[0] == '0') {
		return false
	}
	n, err := strconv.ParseUint(k, 10, 64)
	return err == nil && n < math.MaxUint32
}

func walkJSON(v any, path []string, chunks *[]Chunk, maxBytes int) {
	title := "(root)"
	if len(path) > 0 {
		title = strings.Join(path, " > ")
	}
	serialized := jsonStringify(v, "")
	obj, isObj := v.(*jsonObj)
	if len(serialized) <= maxBytes {
		recurse := false
		if isObj {
			for _, k := range obj.keys {
				if isJSONContainer(obj.vals[k]) {
					recurse = true
					break
				}
			}
		}
		if !recurse {
			*chunks = append(*chunks, Chunk{Title: title, Content: serialized, HasCode: true})
			return
		}
	}
	if isObj {
		if len(obj.keys) == 0 {
			*chunks = append(*chunks, Chunk{Title: title, Content: serialized, HasCode: true})
			return
		}
		for _, k := range obj.keys {
			walkJSON(obj.vals[k], append(slices.Clip(path), k), chunks, maxBytes)
		}
		return
	}
	if arr, ok := v.([]any); ok {
		chunkJSONArray(arr, path, chunks, maxBytes)
		return
	}
	*chunks = append(*chunks, Chunk{Title: title, Content: serialized, HasCode: false})
}

func isJSONContainer(v any) bool {
	switch v.(type) {
	case *jsonObj, []any:
		return true
	}
	return false
}

// chunkJSONArray: ST:2077-2113. JSON.stringify(batch, null, 2) is built from
// items serialized once, so its length is tracked without re-serializing.
func chunkJSONArray(arr []any, path []string, chunks *[]Chunk, maxBytes int) {
	prefix := "(root)"
	if len(path) > 0 {
		prefix = strings.Join(path, " > ")
	}
	idField := findIdentityField(arr)
	var batch []any
	var items []string
	size := 0 // len("[\n" + strings.Join(items, ",\n") + "\n]")
	start := 0
	flush := func(end int) {
		if len(batch) == 0 {
			return
		}
		*chunks = append(*chunks, Chunk{
			Title:   jsonBatchTitle(prefix, start, end, batch, idField),
			Content: "[\n" + strings.Join(items, ",\n") + "\n]",
			HasCode: true,
		})
	}
	for i, v := range arr {
		item := "  " + jsonStringify(v, "  ")
		batch = append(batch, v)
		items = append(items, item)
		if len(items) == 1 {
			size = 4 + len(item)
		} else {
			size += 2 + len(item)
		}
		if size > maxBytes && len(batch) > 1 {
			batch, items = batch[:len(batch)-1], items[:len(items)-1]
			flush(i - 1)
			batch, items = []any{v}, []string{item}
			size = 4 + len(item)
			start = i
		}
	}
	flush(start + len(batch) - 1)
}

// findIdentityField: ST:2035-2048.
func findIdentityField(arr []any) string {
	if len(arr) == 0 {
		return ""
	}
	first, ok := arr[0].(*jsonObj)
	if !ok {
		return ""
	}
	for _, f := range []string{"id", "name", "title", "path", "slug", "key", "label"} {
		switch first.vals[f].(type) {
		case string, float64:
			return f
		}
	}
	return ""
}

// jsonBatchTitle: ST:2050-2075.
func jsonBatchTitle(prefix string, start, end int, batch []any, idField string) string {
	sep := ""
	if prefix != "" {
		sep = prefix + " > "
	}
	if idField == "" {
		if start == end {
			return fmt.Sprintf("%s[%d]", sep, start)
		}
		return fmt.Sprintf("%s[%d-%d]", sep, start, end)
	}
	id := func(item any) string {
		obj, ok := item.(*jsonObj)
		if !ok {
			return "undefined"
		}
		v, ok := obj.vals[idField]
		if !ok {
			return "undefined"
		}
		return jsString(v)
	}
	if len(batch) == 1 {
		return sep + id(batch[0])
	}
	if len(batch) <= 3 {
		ids := make([]string, len(batch))
		for i, b := range batch {
			ids[i] = id(b)
		}
		return sep + strings.Join(ids, ", ")
	}
	return sep + id(batch[0]) + "…" + id(batch[len(batch)-1])
}

// jsString is String(v) for a parsed JSON value.
func jsString(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return jsNumber(t)
	case string:
		return t
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			if e != nil {
				parts[i] = jsString(e)
			}
		}
		return strings.Join(parts, ",")
	default:
		return "[object Object]"
	}
}

// jsonStringify is JSON.stringify(v, null, 2) with every line after the
// first prefixed by indent.
func jsonStringify(v any, indent string) string {
	var b bytes.Buffer
	writeJSON(&b, v, indent)
	return b.String()
}

func writeJSON(b *bytes.Buffer, v any, indent string) {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(t))
	case float64:
		if math.IsInf(t, 0) || math.IsNaN(t) {
			b.WriteString("null")
		} else {
			b.WriteString(jsNumber(t))
		}
	case string:
		writeJSONString(b, t)
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return
		}
		inner := indent + "  "
		b.WriteString("[\n")
		for i, e := range t {
			if i > 0 {
				b.WriteString(",\n")
			}
			b.WriteString(inner)
			writeJSON(b, e, inner)
		}
		b.WriteString("\n" + indent + "]")
	case *jsonObj:
		if len(t.keys) == 0 {
			b.WriteString("{}")
			return
		}
		inner := indent + "  "
		b.WriteString("{\n")
		for i, k := range t.keys {
			if i > 0 {
				b.WriteString(",\n")
			}
			b.WriteString(inner)
			writeJSONString(b, k)
			b.WriteString(": ")
			writeJSON(b, t.vals[k], inner)
		}
		b.WriteString("\n" + indent + "}")
	}
}

// writeJSONString quotes like JSON.stringify: only `"`, `\` and control
// characters are escaped (no HTML or U+2028 escaping, unlike encoding/json).
func writeJSONString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// jsNumber is Number.prototype.toString() for finite values (ECMA-262
// Number::toString, radix 10).
func jsNumber(f float64) string {
	if f == 0 {
		return "0"
	}
	if f < 0 {
		return "-" + jsNumber(-f)
	}
	e := strconv.FormatFloat(f, 'e', -1, 64) // "d.ddde±XX"
	mant, exp, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	x, _ := strconv.Atoi(exp)
	k, n := len(digits), x+1
	switch {
	case k <= n && n <= 21:
		return digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return "0." + strings.Repeat("0", -n) + digits
	}
	sign := "+"
	if n-1 < 0 {
		sign = "-"
	}
	ex := strconv.Itoa(abs(n - 1))
	if k == 1 {
		return digits + "e" + sign + ex
	}
	return digits[:1] + "." + digits[1:] + "e" + sign + ex
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
