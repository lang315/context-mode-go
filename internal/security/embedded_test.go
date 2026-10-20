package security

import (
	"slices"
	"testing"
)

func TestExtractShellCommands(t *testing.T) {
	// Cases from tests/security.test.ts:623-735 plus quote-style variants.
	cases := []struct {
		lang, code string
		want       []string
	}{
		{"python", `os.system("sudo rm -rf /")`, []string{"sudo rm -rf /"}},
		{"python", `os.system( 'ls' )`, []string{"ls"}},
		{"python", `subprocess.run("sudo apt install vim")`, []string{"sudo apt install vim"}},
		{"python", `subprocess.check_output('whoami', shell=True)`, []string{"whoami"}},
		{"python", `subprocess.run(["rm", "-rf", "/"])`, []string{"rm -rf /"}},
		{"python", `subprocess.call(['sudo', 'reboot'])`, []string{"sudo reboot"}},
		{"javascript", `const r = execSync("sudo apt update")`, []string{"sudo apt update"}},
		{"javascript", "exec(`sudo reboot`)", []string{"sudo reboot"}},
		{"javascript", `spawnSync("sudo", ["rm", "-rf"])`, []string{"sudo"}},
		{"typescript", `execFileSync('sudo', ['ls'])`, []string{"sudo"}},
		{"ruby", `system("sudo rm -rf /tmp")`, []string{"sudo rm -rf /tmp"}},
		{"ruby", "out = `sudo reboot`", []string{"sudo reboot"}},
		{"go", `exec.Command("sudo", "rm", "-rf")`, []string{"sudo"}},
		{"go", "exec.Command(`sh`, `-c`, `x`)", []string{"sh"}},
		// TS parity (checked in node): "_exec(" also hits (?:^|[^.])exec\(, and
		// "->system(" is not preceded by ".", so both are reported.
		{"php", `shell_exec("sudo rm -rf /tmp")`, []string{"sudo rm -rf /tmp", "sudo rm -rf /tmp"}},
		{"php", `system("sudo reboot")`, []string{"sudo reboot"}},
		{"php", `$x->system("a"); passthru('sudo ls'); proc_open("sudo id", $d, $p);`, []string{"a", "sudo ls", "sudo id"}},
		{"php", `$o.system("skipped")`, nil},
		{"php", `exec("sudo halt")`, []string{"sudo halt"}},
		{"rust", `Command::new("sudo").arg("reboot")`, []string{"sudo"}},
		{"javascript", `const x = 1 + 2; console.log(x);`, nil},
		{"perl", `system("sudo reboot")`, nil},
		{"python", `os.system("")`, nil},
	}
	for _, c := range cases {
		if got := extractShellCommands(c.code, c.lang); !slices.Equal(got, c.want) {
			t.Errorf("extractShellCommands(%s, %q) = %q, want %q", c.lang, c.code, got, c.want)
		}
	}
}

func TestCheckCode(t *testing.T) {
	p, _, home, _ := newTestPolicy(t)
	writeFile(t, homeSettings(home), `{"permissions":{"deny":["Bash(sudo *)","Bash(rm:*)"]}}`)

	cases := []struct {
		lang, code   string
		cmd, pattern string // pattern "" = allowed
	}{
		{"shell", "echo ok && sudo reboot", "", "Bash(sudo *)"},
		{"shell", "echo ok", "", ""},
		{"python", "import os\nos.system(\"sudo reboot\")\n", "sudo reboot", "Bash(sudo *)"},
		{"python", `subprocess.run(["rm", "-rf", "/"])`, "rm -rf /", "Bash(rm:*)"},
		{"ruby", "puts `echo hi && sudo reboot`", "echo hi && sudo reboot", "Bash(sudo *)"},
		{"go", `exec.Command("rm", "-rf", "/")`, "rm", "Bash(rm:*)"},
		{"rust", `Command::new("rm").arg("-rf")`, "rm", "Bash(rm:*)"},
		{"php", `shell_exec('echo $(sudo id)');`, "echo $(sudo id)", "Bash(sudo *)"},
		{"javascript", `require("child_process").execSync("sudo reboot")`, "sudo reboot", "Bash(sudo *)"},
		{"typescript", `spawn("rm", ["-rf"])`, "rm", "Bash(rm:*)"},
		{"javascript", `console.log("sudo reboot")`, "", ""},
		{"elixir", `System.cmd("sudo", ["reboot"])`, "", ""},
	}
	for _, c := range cases {
		blocked, cmd, pattern := p.CheckCodeMatch(c.lang, c.code)
		if blocked != (c.pattern != "") || cmd != c.cmd || pattern != c.pattern {
			t.Errorf("CheckCodeMatch(%s, %q) = %v, %q, %q; want %q, %q", c.lang, c.code, blocked, cmd, pattern, c.cmd, c.pattern)
		}
		if b2, p2 := p.CheckCode(c.lang, c.code); b2 != blocked || p2 != pattern {
			t.Errorf("CheckCode(%s, %q) = %v, %q; disagrees with CheckCodeMatch", c.lang, c.code, b2, p2)
		}
	}
}

func TestEmbeddedBlockedMessage(t *testing.T) {
	want := `Command blocked by security policy: embedded shell command "sudo reboot" matches deny pattern Bash(sudo *)`
	if got := EmbeddedBlockedMessage("sudo reboot", "Bash(sudo *)"); got != want {
		t.Errorf("got %q", got)
	}
}
