package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var skillNames = []string{"ctx-doctor", "ctx-purge", "ctx-search", "ctx-stats", "ctx-upgrade"}

func skillPath(o Options, name string) string {
	return filepath.Join(filepath.Dir(o.SettingsPath), "skills", name, "SKILL.md")
}

// Install writes every cmgo skill next to settings.json, naming cmgo's tools
// and ending with the marker that lets a later run tell it is unedited.
func TestInstallWritesSkills(t *testing.T) {
	o, _ := newOpts(t)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	for _, name := range skillNames {
		got := readFile(t, skillPath(o, name))
		if !strings.HasPrefix(got, "---\nname: "+name+"\n") || !strings.Contains(got, "mcp__cmgo__ctx_") || !isOwnSkill([]byte(got)) {
			t.Errorf("%s:\n%s", name, got)
		}
	}
}

// A skill of the user's with the same name, or a cmgo skill the user edited,
// is left as it is, with a notice. An unedited cmgo skill from an earlier
// install is replaced.
func TestInstallKeepsUserSkills(t *testing.T) {
	o, f := newOpts(t)
	user := "---\nname: ctx-stats\n---\nmine\n"
	writeSettings(t, skillPath(o, "ctx-stats"), user, 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, skillPath(o, "ctx-stats")); got != user {
		t.Fatalf("user skill overwritten:\n%s", got)
	}
	if !strings.Contains(f.stderr.String(), skillPath(o, "ctx-stats")) {
		t.Fatalf("no notice for the kept skill; stderr:\n%s", f.stderr.String())
	}

	edited := strings.Replace(readFile(t, skillPath(o, "ctx-doctor")), "# cmgo doctor", "# my doctor", 1)
	writeSettings(t, skillPath(o, "ctx-doctor"), edited, 0o644)
	stale := strings.Replace(readFile(t, skillPath(o, "ctx-search")), "# cmgo search", "# old search", 1)
	writeSettings(t, skillPath(o, "ctx-search"), string(withMarker([]byte(stripMarker(stale)))), 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, skillPath(o, "ctx-doctor")); got != edited {
		t.Fatalf("edited skill overwritten:\n%s", got)
	}
	if got := readFile(t, skillPath(o, "ctx-search")); strings.Contains(got, "# old search") || !isOwnSkill([]byte(got)) {
		t.Fatalf("unedited cmgo skill not updated:\n%s", got)
	}
}

// Uninstall removes the unedited cmgo skills with their directories and
// leaves a user's skill, an edited cmgo skill, and other files alone.
func TestUninstallRemovesOnlyOwnSkills(t *testing.T) {
	o, _ := newOpts(t)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	user := "---\nname: ctx-stats\n---\nmine\n"
	writeSettings(t, skillPath(o, "ctx-stats"), user, 0o644)
	edited := readFile(t, skillPath(o, "ctx-doctor")) + "my note\n"
	writeSettings(t, skillPath(o, "ctx-doctor"), edited, 0o644)
	extra := filepath.Join(filepath.Dir(skillPath(o, "ctx-purge")), "notes.md")
	writeSettings(t, extra, "keep", 0o644)
	if err := Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Dir(skillPath(o, "ctx-search"))) || exists(skillPath(o, "ctx-upgrade")) {
		t.Fatal("unedited cmgo skills not removed")
	}
	if exists(skillPath(o, "ctx-purge")) || !exists(extra) {
		t.Fatal("ctx-purge: SKILL.md must go, the user's notes.md must stay")
	}
	if readFile(t, skillPath(o, "ctx-stats")) != user || readFile(t, skillPath(o, "ctx-doctor")) != edited {
		t.Fatal("user or edited skill removed")
	}
}

// A dry run writes no skill and says which it would write.
func TestDryRunWritesNoSkills(t *testing.T) {
	o, f := newOpts(t)
	o.DryRun = true
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(filepath.Dir(o.SettingsPath), "skills")) {
		t.Fatal("dry run created the skills dir")
	}
	if !strings.Contains(f.stdout.String(), skillPath(o, "ctx-stats")) {
		t.Fatalf("dry run does not list the skills; stdout:\n%s", f.stdout.String())
	}
}

// stripMarker drops the trailing marker line of an installed skill.
func stripMarker(s string) string { return s[:strings.LastIndex(s, "<!-- cmgo-skill")] }

// Skills reports which cmgo skills are missing, outdated (an unedited older
// cmgo copy) or foreign (a user's file or an edited copy).
func TestSkillsReport(t *testing.T) {
	o, _ := newOpts(t)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	r, err := Skills(o.SettingsPath)
	if err != nil || r.Total != len(skillNames) || len(r.Missing)+len(r.Outdated)+len(r.Foreign) != 0 ||
		r.Dir != filepath.Join(filepath.Dir(o.SettingsPath), "skills") {
		t.Fatalf("fresh install: %+v, %v", r, err)
	}
	removeFile(t, skillPath(o, "ctx-stats"))
	writeSettings(t, skillPath(o, "ctx-doctor"), readFile(t, skillPath(o, "ctx-doctor"))+"mine\n", 0o644)
	old := strings.Replace(stripMarker(readFile(t, skillPath(o, "ctx-search"))), "# cmgo search", "# old search", 1)
	writeSettings(t, skillPath(o, "ctx-search"), string(withMarker([]byte(old))), 0o644)
	r, err = Skills(o.SettingsPath)
	if err != nil || strings.Join(r.Missing, ",") != "ctx-stats" || strings.Join(r.Outdated, ",") != "ctx-search" || strings.Join(r.Foreign, ",") != "ctx-doctor" {
		t.Fatalf("got %+v, %v", r, err)
	}
}

func removeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}
