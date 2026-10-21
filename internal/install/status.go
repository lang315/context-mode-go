package install

import "strings"

// Status is the cmgo state Install writes to settings.json, read back with
// Install's own command detection (cmgo doctor).
type Status struct {
	Events        []string // HookEvents events holding a cmgo command for their own arg, in HookEvents order
	HookBinary    string   // the binary of the first such command; "" when none
	StatusLine    string   // statusLine.command; "" when not set
	OwnStatusLine bool     // StatusLine runs cmgo-hook statusline
	TSPlugins     []string // enabled context-mode@ plugin keys
}

// Inspect reads settingsPath. A missing or blank file is an empty Status; a
// file that does not parse is an error.
func Inspect(settingsPath string) (Status, error) {
	s, err := loadSettings(settingsPath)
	if err != nil {
		return Status{}, err
	}
	var st Status
	hv, _ := s.root.get("hooks")
	hooks, _ := hv.(*object)
	for _, he := range HookEvents {
		if bin, ok := ownBinary(hooks, he.Event, he.Arg); ok {
			st.Events = append(st.Events, he.Event)
			if st.HookBinary == "" {
				st.HookBinary = bin
			}
		}
	}
	if v, ok := s.root.get("statusLine"); ok {
		if sl, ok := v.(*object); ok {
			st.StatusLine, _ = sl.vals["command"].(string)
			st.OwnStatusLine = isOwnStatusLine(sl)
		}
	}
	pv, _ := s.root.get("enabledPlugins")
	if p, ok := pv.(*object); ok {
		for _, k := range p.keys {
			if strings.HasPrefix(k, "context-mode@") && p.vals[k] == true {
				st.TSPlugins = append(st.TSPlugins, k)
			}
		}
	}
	return st, nil
}

// ownBinary finds a cmgo command for arg under hooks[event] and returns its
// binary. Values of an unexpected shape are skipped, as stripHooks does.
func ownBinary(hooks *object, event, arg string) (string, bool) {
	if hooks == nil {
		return "", false
	}
	entries, _ := hooks.vals[event].([]any)
	for _, e := range entries {
		eo, ok := e.(*object)
		if !ok {
			continue
		}
		inner, _ := eo.vals["hooks"].([]any)
		for _, h := range inner {
			ho, ok := h.(*object)
			if !ok {
				continue
			}
			if c, ok := ho.vals["command"].(string); ok && ownArg(c) == arg {
				bin, _, _ := shellWord(c)
				return bin, true
			}
		}
	}
	return "", false
}

// MCPServer returns the command and args of the user-scope cmgo MCP server
// in claudeJSONPath; ok is false when it is not registered.
func MCPServer(claudeJSONPath string) (command string, args []string, ok bool) {
	e, ok := mcpState(claudeJSONPath)
	return e.Command, e.Args, ok
}
