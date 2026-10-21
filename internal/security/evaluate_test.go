package security

import "testing"

type evalCase struct{ cmd, decision, pattern string }

func checkEval(t *testing.T, p *Policy, cases []evalCase) {
	t.Helper()
	for _, c := range cases {
		if d, pat := p.EvaluateCommand(c.cmd); d != c.decision || pat != c.pattern {
			t.Errorf("EvaluateCommand(%q) = %q, %q; want %q, %q", c.cmd, d, pat, c.decision, c.pattern)
		}
	}
}

func TestEvaluateCommandOneFile(t *testing.T) {
	p, _, home, _ := newTestPolicy(t)
	writeFile(t, homeSettings(home), `{"permissions":{
"deny":["Bash(sudo *)"],
"ask":["Bash(git push:*)"],
"allow":["Bash(sudo *)","Bash(git:*)","Bash(echo:*)","Read(**)"]}}`)
	checkEval(t, p, []evalCase{
		{"sudo ls", "deny", "Bash(sudo *)"},                  // deny wins over allow
		{"echo ok && sudo rm -rf /", "deny", "Bash(sudo *)"}, // any element
		{"echo $(sudo id)", "deny", "Bash(sudo *)"},          // subshell element
		{"git push origin main", "ask", "Bash(git push:*)"},
		{"echo hi; git push", "ask", "Bash(git push:*)"},
		{"git status && echo done", "allow", "Bash(echo:*)"}, // TS reports the last element's rule
		{"git status && rm x", "ask", ""},                    // one element not allowed
		{"cat /etc/hosts", "ask", ""},                        // Read(**) is not a Bash rule
		{" ; && ", "ask", ""},                                // no elements: never allow
	})
}

func TestEvaluateCommandNoSettings(t *testing.T) {
	p, _, _, _ := newTestPolicy(t)
	checkEval(t, p, []evalCase{{"ls", "ask", ""}})
}

// Files are tried in order project-local, project, home; the first file that
// asks or allows every element decides, even if a later file would ask.
func TestEvaluateCommandFilePrecedence(t *testing.T) {
	p, project, home, _ := newTestPolicy(t)
	writeFile(t, localSettings(project), `{"permissions":{"allow":["Bash(git:*)"],"ask":["Bash(make:*)"]}}`)
	writeFile(t, sharedSettings(project), `{"permissions":{"allow":["Bash(npm:*)","Bash(git:*)","Bash(make:*)"]}}`)
	writeFile(t, homeSettings(home), `{"permissions":{"ask":["Bash(git:*)","Bash(npm:*)"],"deny":["Bash(rm *)"]}}`)
	checkEval(t, p, []evalCase{
		{"git status", "allow", "Bash(git:*)"},             // local allows; home's ask is never reached
		{"make all", "ask", "Bash(make:*)"},                // local ask beats shared allow
		{"git status && npm test", "allow", "Bash(npm:*)"}, // local partial, shared allows all
		{"git status && rm x", "deny", "Bash(rm *)"},       // deny in any file wins first
	})
}
