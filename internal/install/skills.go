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
	"slices"
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

// skillsDir is the skills dir next to the settings file, which is the Claude
// config dir.
func skillsDir(settingsPath string) string {
	return filepath.Join(filepath.Dir(settingsPath), "skills")
}

// skillPath is where the named skill's SKILL.md goes in dir.
func skillPath(dir, name string) string { return filepath.Join(dir, name, "SKILL.md") }

// SkillReport is the state of cmgo's skills in Dir, by name in sort order:
// Current ones match this cmgo, Missing ones are absent, Outdated ones are
// unedited copies of an older cmgo, and Foreign ones are a user's file or an
// edited copy.
type SkillReport struct {
	Dir                                 string
	Current, Missing, Outdated, Foreign []string
}

// Skills reports the cmgo skills next to the settings file. Install,
// uninstall and cmgo doctor all act on it, so they agree on what is cmgo's.
func Skills(settingsPath string) (SkillReport, error) {
	r := SkillReport{Dir: skillsDir(settingsPath)}
	entries, err := fs.ReadDir(skillFS, "skills")
	if err != nil {
		return r, err
	}
	for _, e := range entries {
		name := e.Name()
		want, err := embeddedSkill(name)
		if err != nil {
			return r, err
		}
		got, err := os.ReadFile(skillPath(r.Dir, name))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			r.Missing = append(r.Missing, name)
		case err != nil:
			return r, err
		case bytes.Equal(got, want):
			r.Current = append(r.Current, name)
		case isOwnSkill(got):
			r.Outdated = append(r.Outdated, name)
		default:
			r.Foreign = append(r.Foreign, name)
		}
	}
	return r, nil
}

// embeddedSkill is the named skill as install writes it, marker included.
func embeddedSkill(name string) ([]byte, error) {
	body, err := skillFS.ReadFile("skills/" + name + "/SKILL.md")
	if err != nil {
		return nil, err
	}
	return withMarker(body), nil
}

// noticeForeign says which skills install and uninstall leave alone.
func noticeForeign(o Options, r SkillReport) {
	for _, name := range r.Foreign {
		fmt.Fprintf(o.Stderr, "cmgo: notice: %s is not cmgo's or was edited; left as is\n", skillPath(r.Dir, name))
	}
}

// installSkills writes the missing and outdated cmgo skills. A dry run lists
// the files it would write.
func installSkills(o Options) error {
	r, err := Skills(o.SettingsPath)
	if err != nil {
		return err
	}
	noticeForeign(o, r)
	for _, name := range slices.Concat(r.Missing, r.Outdated) {
		path := skillPath(r.Dir, name)
		if o.DryRun {
			fmt.Fprintf(o.Stdout, "skill: %s\n", path)
			continue
		}
		want, err := embeddedSkill(name)
		if err != nil {
			return err
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

// uninstallSkills removes the current and outdated cmgo skills, then each
// one's directory if that is empty.
func uninstallSkills(o Options) error {
	r, err := Skills(o.SettingsPath)
	if err != nil {
		return err
	}
	noticeForeign(o, r)
	for _, name := range slices.Concat(r.Current, r.Outdated) {
		path := skillPath(r.Dir, name)
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
