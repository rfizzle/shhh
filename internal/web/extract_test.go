package web

import (
	"strings"
	"testing"
)

func TestExtractHTML_TitleMetadataAndMain(t *testing.T) {
	doc := `<!doctype html>
<html>
<head>
<title>  Example   Page </title>
<meta name="description" content="A page about examples.">
<script>var tracking = "junk";</script>
<style>body { color: red }</style>
</head>
<body>
<nav>Home | About | Contact</nav>
<header>Site chrome</header>
<main>
<h1>Welcome</h1>
<p>First paragraph with <b>bold</b> text.</p>
<h2>Details</h2>
<ul><li>alpha</li><li>beta</li></ul>
<pre>  indented
    code</pre>
</main>
<footer>Copyright junk</footer>
</body>
</html>`

	ex := ExtractHTML([]byte(doc), "https://example.com/page")
	if ex.Title != "Example Page" {
		t.Errorf("Title = %q", ex.Title)
	}
	if ex.Description != "A page about examples." {
		t.Errorf("Description = %q", ex.Description)
	}
	for _, want := range []string{"# Welcome", "## Details", "- alpha", "- beta", "First paragraph with bold text.", "```\n  indented\n    code\n```"} {
		if !strings.Contains(ex.Text, want) {
			t.Errorf("Text missing %q:\n%s", want, ex.Text)
		}
	}
	for _, banned := range []string{"tracking", "color: red", "Home | About", "Site chrome", "Copyright junk"} {
		if strings.Contains(ex.Text, banned) {
			t.Errorf("Text leaked %q:\n%s", banned, ex.Text)
		}
	}
}

func TestExtractHTML_FallsBackToBody(t *testing.T) {
	doc := `<html><body><p>No main element here.</p><script>junk()</script></body></html>`
	ex := ExtractHTML([]byte(doc), "https://example.com/page")
	if !strings.Contains(ex.Text, "No main element here.") {
		t.Errorf("Text = %q", ex.Text)
	}
	if strings.Contains(ex.Text, "junk") {
		t.Errorf("script leaked: %q", ex.Text)
	}
}

func TestExtractHTML_ArticlePreferredOverBodyChrome(t *testing.T) {
	doc := `<html><body><div>sidebar junk</div><article><p>The story.</p></article></body></html>`
	ex := ExtractHTML([]byte(doc), "https://example.com/page")
	if !strings.Contains(ex.Text, "The story.") {
		t.Errorf("Text = %q", ex.Text)
	}
	if strings.Contains(ex.Text, "sidebar junk") {
		t.Errorf("chrome leaked: %q", ex.Text)
	}
}

func TestExtractHTML_OGDescriptionFallback(t *testing.T) {
	doc := `<html><head><meta property="og:description" content="Social summary."></head><body><p>x</p></body></html>`
	ex := ExtractHTML([]byte(doc), "https://example.com/page")
	if ex.Description != "Social summary." {
		t.Errorf("Description = %q", ex.Description)
	}
}

func TestExtractHTML_BlankLinesFolded(t *testing.T) {
	doc := `<html><body><div></div><div></div><div></div><p>one</p><div></div><div></div><p>two</p></body></html>`
	ex := ExtractHTML([]byte(doc), "https://example.com/page")
	if strings.Contains(ex.Text, "\n\n\n") {
		t.Errorf("blank-line runs not folded:\n%q", ex.Text)
	}
}

// A parameter table is the shape a reference page states its contract in, and
// without a separator its row reads as one run-on word.
func TestExtractHTML_TableCellsKeepTheirBoundaries(t *testing.T) {
	doc := `<html><body><main><table>
<tr><th>name</th><th>type</th><th>what it does</th></tr>
<tr><td>timeout</td><td>string</td><td>how long one request may take</td></tr>
</table></main></body></html>`
	ex := ExtractHTML([]byte(doc), "https://example.com/reference")
	for _, want := range []string{"name | type | what it does", "timeout | string | how long one request may take"} {
		if !strings.Contains(ex.Text, want) {
			t.Errorf("Text missing %q:\n%s", want, ex.Text)
		}
	}

	// A cell that wraps its text in a <p> is the commoner shape, and a row
	// whose cells each ended a line would arrive as one column with the
	// separators stranded between the pieces.
	wrapped := `<html><body><main><table>
<tr><td><p>timeout</p></td><td><div>string</div></td><td><p>how long one request may take</p></td></tr>
</table></main></body></html>`
	ex = ExtractHTML([]byte(wrapped), "https://example.com/reference")
	if !strings.Contains(ex.Text, "timeout | string | how long one request may take") {
		t.Errorf("a cell with block content broke the row:\n%q", ex.Text)
	}
}

// A link whose text is its own address says it once: a list of bare URLs is
// the commonest shape a link list takes, and writing each twice is the whole
// slice spent on saying everything two times.
func TestExtractHTML_ALinkThatIsItsOwnURLIsNotWrittenTwice(t *testing.T) {
	doc := `<html><body><main><p>See <a href="https://go.dev/ref/spec">https://go.dev/ref/spec</a> for it.</p></main></body></html>`
	ex := ExtractHTML([]byte(doc), "https://example.com/page")
	if got, want := ex.Text, "See https://go.dev/ref/spec for it."; got != want {
		t.Errorf("Text = %q, want %q", got, want)
	}
}

// A documentation index is a list of link texts; without the addresses it is a
// list of labels the next fetch has to guess a URL from.
func TestExtractHTML_LinksCarryTheirDestinations(t *testing.T) {
	doc := `<html><body><main>
<p><a href="https://go.dev/ref/spec">The spec</a></p>
<p><a href="/reference/api">The API</a></p>
<p><a href="guide.html">The guide</a></p>
<p><a href="#section">This page</a></p>
<p><a href="javascript:open()">A script</a></p>
<p><a href="mailto:someone@example.com">An address</a></p>
<p>Read <a href="/reference/api">the API</a> first.</p>
<p><img src="/d.png" alt="the request lifecycle"></p>
</main></body></html>`
	ex := ExtractHTML([]byte(doc), "https://docs.example.com/v2/index.html")
	for _, want := range []string{
		"The spec (https://go.dev/ref/spec)",
		"The API (https://docs.example.com/reference/api)",
		"The guide (https://docs.example.com/v2/guide.html)",
		"Read the API (https://docs.example.com/reference/api) first.",
		"[image: the request lifecycle]",
	} {
		if !strings.Contains(ex.Text, want) {
			t.Errorf("Text missing %q:\n%s", want, ex.Text)
		}
	}
	for _, banned := range []string{"#section", "javascript:", "mailto:"} {
		if strings.Contains(ex.Text, banned) {
			t.Errorf("Text carries %q, which no fetch can follow:\n%s", banned, ex.Text)
		}
	}
	// A relative href on a document with no address of its own is dropped
	// rather than half-resolved: a URL the reader cannot fetch is worse than
	// a label.
	ex = ExtractHTML([]byte(doc), "")
	if !strings.Contains(ex.Text, "The API\n") {
		t.Errorf("a relative link kept a destination it cannot have:\n%s", ex.Text)
	}
}

// A <pre> closes an open <p>, so the parser reconstructs the paragraph's
// anchor inside the preformatted block: a linked code sample arrives as a link
// whose text is the code, and an address spliced in there would read as part
// of the source.
func TestExtractHTML_ALinkInsideAPreDoesNotSpliceItsAddressIntoTheCode(t *testing.T) {
	doc := "<html><body><main><p><a href=\"/x\"><pre>code\nhere</pre></a></p></main></body></html>"
	ex := ExtractHTML([]byte(doc), "https://example.com/base/")
	if got, want := ex.Text, "```\ncode\nhere\n```"; got != want {
		t.Errorf("Text = %q, want %q", got, want)
	}
}

// Every element on the page is chrome, so there is nothing to read — and the
// extractor says that by returning nothing rather than by falling back to the
// chrome it just dropped.
func TestExtractHTML_AllChromePageYieldsNothing(t *testing.T) {
	doc := `<html><head><title>Portal</title></head><body>
<nav><a href="/a">A</a><a href="/b">B</a></nav>
<header>Portal</header>
<aside>Related</aside>
<form><input name="q"></form>
<footer>Copyright</footer>
</body></html>`
	ex := ExtractHTML([]byte(doc), "https://example.com/portal")
	if ex.Text != "" {
		t.Errorf("Text = %q, want nothing readable", ex.Text)
	}
	if ex.Title != "Portal" {
		t.Errorf("Title = %q — the title is still worth having", ex.Title)
	}
}

// <main> wins wherever it is, including inside the <article> that would
// otherwise have been chosen: a page that marks both means the inner one.
func TestExtractHTML_MainNestedInArticleWins(t *testing.T) {
	doc := `<html><body><article><p>The byline and the standfirst.</p>
<main><p>The body of the piece.</p></main></article></body></html>`
	ex := ExtractHTML([]byte(doc), "https://example.com/story")
	if !strings.Contains(ex.Text, "The body of the piece.") {
		t.Errorf("Text = %q", ex.Text)
	}
	if strings.Contains(ex.Text, "standfirst") {
		t.Errorf("the article around <main> leaked:\n%s", ex.Text)
	}
}
