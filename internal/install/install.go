// Package install registers cmgo's hooks in Claude Code's settings.json and
// its MCP server through the claude CLI (spec §5).
package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Options are the inputs of Install and Uninstall. The caller resolves every
// path and injects LookPath and Run so tests never run the real claude.
type Options struct {
	SettingsPath    string // resolved by the caller (CLAUDE_CONFIG_DIR or ~/.claude)
	ClaudeJSONPath  string // ~/.claude.json
	Binary          string // absolute path to cmgo
	HookBinary      string // absolute path to cmgo-hook; "" means cmgo-hook next to Binary
	DisableTSPlugin bool
	DryRun          bool
	Stdout, Stderr  io.Writer
	LookPath        func(string) (string, error) // exec.LookPath in production
	Run             func(name string, args ...string) error
}

// Install replaces cmgo's hook commands in the settings file with the
// HookEvents entries for o.HookBinary, keeping every other command, then
// registers o.Binary as the MCP server.
func Install(o Options) error {
	if o.HookBinary == "" {
		o.HookBinary = filepath.Join(filepath.Dir(o.Binary), "cmgo-hook")
	}
	for _, b := range []string{o.Binary, o.HookBinary} {
		if !filepath.IsAbs(b) || strings.Contains(b, `"`) {
			return fmt.Errorf("binary path must be absolute and have no double quote: %q", b)
		}
	}
	s, err := loadSettings(o.SettingsPath)
	if err != nil {
		return err
	}
	stripHooks(s.root)
	if err := addHooks(s.root, o.HookBinary); err != nil {
		return fmt.Errorf("%s: %w", o.SettingsPath, err)
	}
	checkPlugins(s.root, o)
	if err := s.save(o); err != nil {
		return err
	}
	return registerMCP(o, true)
}

// Uninstall removes cmgo's hook commands and the MCP server. It leaves
// enabledPlugins alone.
func Uninstall(o Options) error {
	s, err := loadSettings(o.SettingsPath)
	if err != nil {
		return err
	}
	// Nothing removed: do not create or reformat the user's file.
	if stripHooks(s.root) == 0 && !o.DryRun {
		fmt.Fprintf(o.Stdout, "cmgo: no cmgo hooks in %s\n", s.path)
	} else if err := s.save(o); err != nil {
		return err
	}
	return registerMCP(o, false)
}

// checkPlugins warns about an enabled TS context-mode plugin, which would
// inject its routing text next to cmgo's, and turns it off when asked.
func checkPlugins(root *object, o Options) {
	pv, _ := root.get("enabledPlugins")
	p, ok := pv.(*object)
	if !ok {
		return
	}
	for _, k := range p.keys {
		if !strings.HasPrefix(k, "context-mode@") || p.vals[k] != true {
			continue
		}
		if o.DisableTSPlugin {
			p.set(k, false)
			fmt.Fprintf(o.Stderr, "cmgo: disabled plugin %s\n", k)
			continue
		}
		fmt.Fprintf(o.Stderr, "cmgo: warning: plugin %s is enabled; it and cmgo will both inject routing text (rerun with --disable-ts-plugin to turn it off)\n", k)
	}
}

// settings is a parsed settings file.
type settings struct {
	path    string // the real file, symlinks resolved
	existed bool
	orig    []byte
	mode    fs.FileMode
	root    *object
}

// loadSettings reads path; a missing or blank file is {}. A file that does
// not parse is an error, so nothing is ever written over it.
func loadSettings(path string) (*settings, error) {
	s := &settings{path: path, mode: 0o644, root: newObject()}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		s.path = real // write through a dotfile manager's link, not over it
	}
	fi, err := os.Stat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	s.existed, s.orig, s.mode = true, b, fi.Mode().Perm()
	if len(bytes.TrimSpace(b)) == 0 {
		return s, nil
	}
	v, err := parseJSON(b)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	root, ok := v.(*object)
	if !ok {
		return nil, fmt.Errorf("parse %s: not a JSON object", path)
	}
	s.root = root
	return s, nil
}

// save writes the edited file when its bytes changed, after keeping the
// original once as <file>.cmgo-bak. DryRun prints it instead.
func (s *settings) save(o Options) error {
	out := encodeJSON(s.root)
	if o.DryRun {
		_, err := o.Stdout.Write(out)
		return err
	}
	if s.existed && bytes.Equal(out, s.orig) {
		fmt.Fprintf(o.Stdout, "cmgo: %s is up to date\n", s.path)
		return nil
	}
	if s.existed {
		bak := s.path + ".cmgo-bak"
		if _, err := os.Lstat(bak); errors.Is(err, fs.ErrNotExist) {
			if err := writeAtomic(bak, s.orig, s.mode); err != nil {
				return err
			}
			fmt.Fprintf(o.Stdout, "cmgo: saved the original as %s\n", bak)
		} else if err != nil {
			return err
		}
	} else if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	if err := writeAtomic(s.path, out, s.mode); err != nil {
		return err
	}
	fmt.Fprintf(o.Stdout, "cmgo: wrote %s\n", s.path)
	return nil
}

// writeAtomic writes a temp file in the same dir and renames it over path, so
// a crash never leaves a half-written settings file.
func writeAtomic(path string, b []byte, mode fs.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) // fails harmlessly after the rename
	_, err = f.Write(b)
	if err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
