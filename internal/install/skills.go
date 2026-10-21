package install

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// skillFS holds cmgo's user skills, one skills/<name>/SKILL.md each.
//
//go:embed skills
var skillFS embed.FS

// skillMarker starts the last line of an installed skill. The line ends with
// the sha256 of everything above it, so a later install or uninstall can tell
// an unedited cmgo skill from a user's file or an edited copy.
const skillMarker = "<!-- cmgo-skill: written by cmgo install, removed by cmgo uninstall unless edited. sha256:"

// withMarker returns body followed by its marker line.
func withMarker(body []byte) []byte {
	sum := sha256.Sum256(body)
	out := append(bytes.Clone(body), skillMarker...)
	return append(out, hex.EncodeToString(sum[:])+" -->\n"...)
}

// isOwnSkill reports whether b is a cmgo skill nobody has edited.
func isOwnSkill(b []byte) bool {
	i := bytes.LastIndex(b, []byte(skillMarker))
	return i >= 0 && bytes.Equal(withMarker(b[:i]), b)
}

// skillPaths maps each embedded skill to its SKILL.md in the skills dir next
// to the settings file, which is the Claude config dir.
func skillPaths(o Options) (map[string]string, error) {
	entries, err := fs.ReadDir(skillFS, "skills")
	if err != nil {
		return nil, err
	}
	paths := map[string]string{}
	for _, e := range entries {
		paths[e.Name()] = filepath.Join(filepath.Dir(o.SettingsPath), "skills", e.Name(), "SKILL.md")
	}
	return paths, nil
}

// installSkills writes or updates each cmgo skill. A file in its place that
// is not an unedited cmgo skill is left as it is, with a notice. A dry run
// lists the files it would write.
func installSkills(o Options) error {
	paths, err := skillPaths(o)
	if err != nil {
		return err
	}
	for name, path := range paths {
		body, err := skillFS.ReadFile("skills/" + name + "/SKILL.md")
		if err != nil {
			return err
		}
		want := withMarker(body)
		old, err := os.ReadFile(path)
		switch {
		case err == nil && bytes.Equal(old, want):
			continue
		case err == nil && !isOwnSkill(old):
			fmt.Fprintf(o.Stderr, "cmgo: notice: %s is not cmgo's or was edited; left as is\n", path)
			continue
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return err
		}
		if o.DryRun {
			fmt.Fprintf(o.Stdout, "skill: %s\n", path)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := writeAtomic(path, want, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// uninstallSkills removes each unedited cmgo skill and then its directory if
// that is empty. Other files, and edited copies, stay.
func uninstallSkills(o Options) error {
	paths, err := skillPaths(o)
	if err != nil {
		return err
	}
	for _, path := range paths {
		old, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !isOwnSkill(old) {
			fmt.Fprintf(o.Stderr, "cmgo: notice: %s is not cmgo's or was edited; left as is\n", path)
			continue
		}
		if o.DryRun {
			fmt.Fprintf(o.Stdout, "remove skill: %s\n", path)
			continue
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		_ = os.Remove(filepath.Dir(path)) // only succeeds when empty
	}
	return nil
}
