package fetch

import (
	"strings"
	"testing"
)

const testPage = `<!DOCTYPE html>
<html><head><title>Doc Title</title><style>body{color:red}</style></head>
<body>
<header>SITE HEADER</header>
<nav><a href="/a">NAVLINK</a></nav>
<script>alert("SCRIPTBODY")</script>
<noscript>NOSCRIPTBODY</noscript>
<h1>Main</h1>
<h2>Section</h2>
<p>Some <strong>bold</strong>, <em>em</em> and <del>gone</del> text with <a href="https://example.com/x">a link</a>.</p>
<ul><li>one</li><li>two</li></ul>
<pre><code class="language-go">fmt.Println("hi")
</code></pre>
<table>
<thead><tr><th>Name</th><th>Value</th></tr></thead>
<tbody><tr><td>alpha</td><td>1</td></tr><tr><td>beta</td><td>2</td></tr></tbody>
</table>
<footer>SITE FOOTER</footer>
</body></html>`

func TestHTMLToMarkdown(t *testing.T) {
	md, err := HTMLToMarkdown(testPage)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Main\n",
		"## Section\n",
		"**bold**",
		"_em_",
		"* one\n* two",
		"~~gone~~",
		"[a link](https://example.com/x)",
		"```go\nfmt.Println(\"hi\")\n```",
		"| Name | Value |\n|---|---|\n| alpha | 1 |\n| beta | 2 |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
	for _, gone := range []string{"SITE HEADER", "NAVLINK", "SCRIPTBODY", "NOSCRIPTBODY", "SITE FOOTER", "color:red", "Doc Title", "THE END"} {
		if strings.Contains(md, gone) {
			t.Errorf("%q should be removed, got:\n%s", gone, md)
		}
	}
}
