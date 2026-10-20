package mcpserver

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Args is a tool's decoded arguments object. The getters reproduce the TS zod
// preprocessors, so loosely typed calls (numbers or booleans sent as strings,
// arrays sent as JSON strings) behave as they did in context-mode.
type Args map[string]any

// ParseArgs decodes the raw arguments of a tools/call request.
func ParseArgs(raw json.RawMessage) (Args, error) {
	a := Args{}
	if len(raw) == 0 || string(raw) == "null" {
		return a, nil
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&a); err != nil {
		return nil, fmt.Errorf("arguments must be a JSON object: %v", err)
	}
	return a, nil
}

// Has reports whether key is present and not null.
func (a Args) Has(key string) bool {
	v, ok := a[key]
	return ok && v != nil
}

// String returns a string argument; a missing key gives "" and ok=false.
func (a Args) String(key string) (string, bool, error) {
	v, ok := a[key]
	if !ok || v == nil {
		return "", false, nil
	}
	s, isStr := v.(string)
	if !isStr {
		return "", true, fmt.Errorf("%s: expected string", key)
	}
	return s, true, nil
}

// Number mirrors z.coerce.number(): numbers, numeric strings (JS Number()),
// and booleans are accepted; anything that becomes NaN is an error.
func (a Args) Number(key string) (float64, bool, error) {
	v, ok := a[key]
	if !ok || v == nil {
		return 0, false, nil
	}
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0, true, fmt.Errorf("%s: expected number", key)
		}
		return f, true, nil
	case string:
		t := strings.TrimSpace(x)
		if t == "" {
			return 0, true, nil // JS Number("") === 0
		}
		f, err := strconv.ParseFloat(t, 64)
		if err != nil || math.IsNaN(f) {
			return 0, true, fmt.Errorf("%s: expected number, received nan", key)
		}
		return f, true, nil
	case bool:
		if x {
			return 1, true, nil
		}
		return 0, true, nil
	default:
		return 0, true, fmt.Errorf("%s: expected number", key)
	}
}

// Bool mirrors TS coerceBoolean: only the literals "true"/"false" (any case,
// trimmed) are lifted; other strings are an error rather than truthy.
func (a Args) Bool(key string) (bool, bool, error) {
	v, ok := a[key]
	if !ok || v == nil {
		return false, false, nil
	}
	switch x := v.(type) {
	case bool:
		return x, true, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true":
			return true, true, nil
		case "false":
			return false, true, nil
		}
	}
	return false, true, fmt.Errorf("%s: expected boolean", key)
}

// Array mirrors TS coerceJsonArray: a JSON-array string is parsed, any other
// non-empty string becomes a one-element array, an empty string is an error.
func (a Args) Array(key string) ([]any, bool, error) {
	v, ok := a[key]
	if !ok || v == nil {
		return nil, false, nil
	}
	switch x := v.(type) {
	case []any:
		return x, true, nil
	case string:
		if strings.TrimSpace(x) == "" {
			return nil, true, fmt.Errorf("%s: expected non-empty array", key)
		}
		var parsed []any
		dec := json.NewDecoder(strings.NewReader(x))
		dec.UseNumber()
		if err := dec.Decode(&parsed); err == nil {
			return parsed, true, nil
		}
		return []any{x}, true, nil
	}
	return nil, true, fmt.Errorf("%s: expected array", key)
}

// Strings is Array restricted to string elements.
func (a Args) Strings(key string) ([]string, bool, error) {
	arr, ok, err := a.Array(key)
	if err != nil || !ok {
		return nil, ok, err
	}
	out := make([]string, 0, len(arr))
	for i, e := range arr {
		s, isStr := e.(string)
		if !isStr {
			return nil, true, fmt.Errorf("%s[%d]: expected string", key, i)
		}
		out = append(out, s)
	}
	return out, true, nil
}

// Command is one ctx_batch_execute entry.
type Command struct {
	Label   string
	Command string
}

// Commands mirrors TS coerceCommandsArray: Array plus bare strings becoming
// {label: "cmd_<i+1>", command: s}.
func (a Args) Commands(key string) ([]Command, bool, error) {
	arr, ok, err := a.Array(key)
	if err != nil || !ok {
		return nil, ok, err
	}
	out := make([]Command, 0, len(arr))
	for i, e := range arr {
		switch x := e.(type) {
		case string:
			out = append(out, Command{Label: fmt.Sprintf("cmd_%d", i+1), Command: x})
		case map[string]any:
			label, l := x["label"].(string)
			cmd, c := x["command"].(string)
			if !l || !c {
				return nil, true, fmt.Errorf("%s[%d]: expected {label, command} strings", key, i)
			}
			out = append(out, Command{Label: label, Command: cmd})
		default:
			return nil, true, fmt.Errorf("%s[%d]: expected object", key, i)
		}
	}
	return out, true, nil
}
