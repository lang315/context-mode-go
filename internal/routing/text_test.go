package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// The expected texts are the TS output (hooks/routing-block.mjs and
// hooks/core/routing.mjs at a5e6dd31) with mcp__plugin_context-mode_context-mode__
// replaced by mcp__cmgo__, pinned by byte length and sha256. testdata/golden/
// hooks-strings.json carries the full texts (TestGoldenStrings).
type pinned struct {
	name string
	size int
	hash string
}

func checkPinned(t *testing.T, p pinned, got string) {
	t.Helper()
	sum := sha256.Sum256([]byte(got))
	if len(got) != p.size || hex.EncodeToString(sum[:]) != p.hash {
		t.Errorf("%s: %d bytes, sha256 %x; want %d bytes, %s\ngot:\n%s", p.name, len(got), sum, p.size, p.hash, got)
	}
}

func TestRoutingBlock(t *testing.T) {
	cases := []struct {
		commands, bootstrap bool
		want                pinned
	}{
		{true, false, pinned{"routing block with commands", 4239, "52c54ed411336a4795911e32dee0ea8c103c0d41b793d942355d3eca57ca4e47"}},
		{false, true, pinned{"agent routing suffix", 4105, "8c756c25026a96477fac96011f6869f83d1a4974640c5bff5373d76ba5de762c"}},
		{false, false, pinned{"SessionStart routing block", 3481, "6b7c33490c32133a9bfe5a11b240236f6732a0f700c581ad90f46d6868edd023"}},
	}
	for _, c := range cases {
		checkPinned(t, c.want, RoutingBlock(c.commands, c.bootstrap))
	}
	if strings.Contains(RoutingBlock(false, false), "mcp__plugin_") {
		t.Error("routing block still names the TS plugin tools")
	}
}

func TestGuidanceAndRedirectTexts(t *testing.T) {
	cases := []struct {
		got  string
		want pinned
	}{
		{bashGuidance, pinned{"guidance-bash", 597, "8e5b6e0792263eb68968bdf22bf1ad96db5ab44c58b3d0b686ef59144d885100"}},
		{readGuidance, pinned{"guidance-read", 377, "480cdbe6726c56c5e9d30043d59b710fd69760afa3bcd18c4bcc1b84d1f5f11c"}},
		{grepGuidance, pinned{"guidance-grep", 477, "3759bf1be31f94b6c5f49281c80144dc629201c1410ff6a1850ce98729713c88"}},
		{curlMessage, pinned{"deny-curl-wget", 467, "8445533370d36a76361daaa46de4c9046d37a4c49980a04d71bca1374a7207b9"}},
		{inlineMessage, pinned{"deny-inline-http", 342, "da11642dff800f1658d28902eddc419ada0b3f3e0912f3e96b4dfb02f4bdc356"}},
		{webFetchMessage("https://example.com/a"), pinned{"deny-webfetch-example", 563, "4a134a67a5f425ddb67b4c38b81e67d9a6939d3f16a1976f734756a8025c9ed9"}},
		{buildMessage("./gradlew build"), pinned{"deny-build-gradlew-example", 402, "f97ec9b71a06e587b3529da87527eb15345ea76f4c53cdaf03dfd48bb2412ef6"}},
	}
	for _, c := range cases {
		checkPinned(t, c.want, c.got)
	}
}

func TestBuildMessageEscapes(t *testing.T) {
	// routing.mjs:811: backslashes doubled, then quotes escaped.
	got := buildMessage(`mvn "test" C:\x`)
	if want := `code: \"mvn \"test\" C:\\x 2>&1 | tail -30\")`; !strings.Contains(got, want) {
		t.Errorf("buildMessage = %q, want it to contain %q", got, want)
	}
}
