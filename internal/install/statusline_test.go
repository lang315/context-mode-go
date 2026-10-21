package install

import (
	"strings"
	"testing"
)

// statusLineOf returns the settings file's statusLine value as JSON, or "".
func statusLineOf(t *testing.T, content string) string {
	t.Helper()
	v, ok := mustParse(t, content).get("statusLine")
	if !ok {
		return ""
	}
	return strings.TrimSuffix(string(encodeJSON(v)), "\n")
}

func TestIsOwnStatusLine(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{`'/opt/cmgo/bin/cmgo-hook' statusline`, true},
		{`"/old place/cmgo-hook" statusline`, true},
		{`cmgo-hook statusline`, true},
		{`/x/cmgo hook statusline`, true},
		{`'/x/cmgo-hook' statusline --x`, false},
		{`'/x/cmgo-hook' stop`, false},
		{`/x/mine.sh statusline`, false},
		{`npx ccstatusline`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := ownArg(c.cmd) == "statusline"; got != c.want {
			t.Errorf("%q: %v, want %v", c.cmd, got, c.want)
		}
	}
}

// Stats spec §4: an absent statusLine becomes cmgo's.
func TestInstallSetsStatusLine(t *testing.T) {
	o, f := newOpts(t)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"type\": \"command\",\n  \"command\": \"/opt/cmgo/bin/cmgo-hook statusline\"\n}"
	if got := statusLineOf(t, readFile(t, o.SettingsPath)); got != want {
		t.Fatalf("statusLine\n%s\nwant\n%s", got, want)
	}
	if f.stderr.Len() != 0 {
		t.Fatalf("stderr %q", f.stderr.String())
	}
}

// Another statusline is kept, with a notice; uninstall keeps it too.
func TestInstallKeepsForeignStatusLine(t *testing.T) {
	o, f := newOpts(t)
	orig := "{\n  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": \"~/bin/mine.sh\",\n    \"padding\": 0\n  }\n}\n"
	writeSettings(t, o.SettingsPath, orig, 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if got, want := statusLineOf(t, readFile(t, o.SettingsPath)), statusLineOf(t, orig); got != want {
		t.Fatalf("statusLine changed:\n%s", got)
	}
	if !strings.Contains(f.stderr.String(), "statusLine") || !strings.Contains(f.stderr.String(), "/opt/cmgo/bin/cmgo-hook statusline") {
		t.Fatalf("stderr %q", f.stderr.String())
	}
	if err := Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, o.SettingsPath); got != orig {
		t.Fatalf("got\n%s\nwant\n%s", got, orig)
	}
}

// cmgo's own statusLine from an older path gets the new command and keeps
// its other fields (padding), as stats spec §4 says; uninstall removes it.
func TestInstallReplacesOwnStatusLine(t *testing.T) {
	o, f := newOpts(t)
	writeSettings(t, o.SettingsPath, "{\n  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": \"'/old/cmgo-hook' statusline\",\n    \"padding\": 1\n  },\n  \"model\": \"opus\"\n}\n", 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"type\": \"command\",\n  \"command\": \"/opt/cmgo/bin/cmgo-hook statusline\",\n  \"padding\": 1\n}"
	if got := statusLineOf(t, readFile(t, o.SettingsPath)); got != want {
		t.Fatalf("statusLine\n%s\nwant\n%s", got, want)
	}
	if f.stderr.Len() != 0 {
		t.Fatalf("stderr %q", f.stderr.String())
	}
	if err := Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, o.SettingsPath); got != "{\n  \"model\": \"opus\"\n}\n" {
		t.Fatalf("after uninstall:\n%s", got)
	}
}

// "statusLine": null means no statusline: cmgo sets its own, without a notice
// and without moving the key.
func TestInstallNullStatusLineCountsAsAbsent(t *testing.T) {
	o, f := newOpts(t)
	writeSettings(t, o.SettingsPath, "{\n  \"statusLine\": null,\n  \"model\": \"opus\"\n}\n", 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, o.SettingsPath)
	if want := "\"statusLine\": {\n    \"type\": \"command\",\n    \"command\": \"/opt/cmgo/bin/cmgo-hook statusline\"\n  },\n  \"model\": \"opus\""; !strings.Contains(got, want) {
		t.Fatalf("settings:\n%s", got)
	}
	if strings.Index(got, "statusLine") > strings.Index(got, "model") {
		t.Errorf("statusLine moved after model:\n%s", got)
	}
	if f.stderr.Len() != 0 {
		t.Fatalf("stderr %q", f.stderr.String())
	}
	// And the second install changes nothing.
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if again := readFile(t, o.SettingsPath); again != got {
		t.Fatalf("second install changed the file:\n%s", again)
	}
}
