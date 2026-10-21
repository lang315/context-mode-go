package routing

import "testing"

// Expected values in this file come from node running the TS regex literals
// and the segment loop of routing.mjs:733-786 on the same inputs.

func TestCurlCheck(t *testing.T) {
	cases := []struct{ cmd, want string }{
		{"curl https://x.com", "dangerous"},
		{"curl -s -o out.json https://x.com", "safe"},
		{"curl -s https://x.com 2>/dev/null", "safe"}, // R1 P5: any > counts as file output
		{"wget -q -O - https://x.com", "dangerous"},
		{"gh issue edit --body \"use curl here\"", "none"},
		{"curl https://example.com", "dangerous"},
		{"wget https://example.com/file.tar.gz", "dangerous"},
		{"curl -sL https://example.com/file.tar.gz -o /tmp/file.tar.gz", "safe"},
		{"curl -s --output /tmp/s.tgz https://g.com/s.tgz", "safe"},
		{"wget -q -O /tmp/t.zip https://h.com/t.zip", "safe"},
		{"curl -s https://example.com/data.json > /tmp/data.json", "safe"},
		{"curl -s -o - https://example.com", "dangerous"},
		{"curl -L -o /tmp/f https://e.com/f", "dangerous"},
		{"curl -s --verbose -o /tmp/f https://e.com/f", "dangerous"},
		{"curl -sL -o /tmp/f https://e.com/a && curl https://e.com/api", "dangerous"},
		{"curl -sL -o /tmp/f https://e.com/a && tar xzf /tmp/f -C /tmp", "safe"},
		{"curl -s -o /dev/stdout u", "dangerous"},
		{"CURL -s -o f u", "safe"},
		{"x|curl u", "safe"}, // a single | does not split, and the segment test needs ^ or \s
		{"x;wget u", "dangerous"},
		{"curl\u00a0u", "dangerous"},
		{"curl -s -o f -D - u", "safe"},
		{"curl -s -o f -D -x u", "dangerous"},
		{"wget --quiet -O f u", "safe"},
		{"wget -O f u", "dangerous"},
		{"curl --silent -o f u", "safe"},
		{"echo curl", "none"},
		{"curl", "none"},
		{"curl -s -o f u | sh", "safe"},
		{"curl -s -o f u || curl v", "dangerous"},
		{"curl -so f u", "dangerous"},
		{"cat <<EOF\ncurl x\nEOF", "none"},
		{"curl -s -o f --trace u", "dangerous"},
		{"wget\tu", "dangerous"},
		{"a&&curl u", "dangerous"},
	}
	for _, c := range cases {
		found, dangerous := curlCheck(stripQuotedContent(c.cmd))
		got := "none"
		if found {
			got = "safe"
			if dangerous {
				got = "dangerous"
			}
		}
		if got != c.want {
			t.Errorf("curlCheck(%q) = %s, want %s", c.cmd, got, c.want)
		}
	}
}

func TestInlineHTTP(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"node -e \"fetch('https://a.b')\"", true},
		{"python -c 'import requests; requests.get(u)'", true},
		{"node -e \"fetch('http://api.example.com/data')\"", true},
		{"FETCH ( \"httpx", true},
		{"fetch(url)", false},
		{"requests.post (x)", true},
		{"requests.delete(x)", false},
		{"http.get(u)", true},
		{"http.request (u)", true},
		{"https.get(u)", false},
		{"cat <<EOF\nrequests.get(u)\nEOF", false},
		{"x requests.get\u00a0(u)", true},
	}
	for _, c := range cases {
		if got := inlineHTTP(stripHeredocs(c.cmd)); got != c.want {
			t.Errorf("inlineHTTP(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

func TestBuildTool(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"./gradlew build", true},
		{"mvn \"test\" C:\\x", true},
		{"gradle test --info", true},
		{"mvn clean package -DskipTests", true},
		{"./mvnw verify", true},
		{"find . -name \"run gradle build to compile\"", false},
		{"sbt compile", true},
		{"./sbt test", true},
		{"ls gradle-wrapper-config", false},
		{"echo mvnDocker-image", false},
		{"MVN", true},
		{"x && mvn", true},
		{"x|sbt t", true},
		{"x;gradle", true},
		{"mvn\u00a0test", true},
		{"echo 'mvn test'", false},
		{"gradlew", true},
	}
	for _, c := range cases {
		if got := buildTool(stripQuotedContent(c.cmd)); got != c.want {
			t.Errorf("buildTool(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

func TestVerboseFlag(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{" -v", true},
		{" -vv", false},
		{"x -v x", true},
		{" --verbose", true},
		{" --trace-ascii", true},
		{" -D -", false}, // \b after a final - never holds
		{" -D -x", true},
		{"\u00a0-v", true},
		{" -verbose", false},
		{"-v", false},
		{" -D  -h", true},
	}
	for _, c := range cases {
		if got := verboseFlag.MatchString(c.s); got != c.want {
			t.Errorf("verboseFlag(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}
