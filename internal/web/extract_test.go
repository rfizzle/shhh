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

	ex := ExtractHTML([]byte(doc))
	if ex.Title != "Example Page" {
		t.Errorf("Title = %q", ex.Title)
	}
	if ex.Description != "A page about examples." {
		t.Errorf("Description = %q", ex.Description)
	}
	for _, want := range []string{"# Welcome", "## Details", "- alpha", "- beta", "First paragraph with bold text.", "  indented\n    code"} {
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
	ex := ExtractHTML([]byte(doc))
	if !strings.Contains(ex.Text, "No main element here.") {
		t.Errorf("Text = %q", ex.Text)
	}
	if strings.Contains(ex.Text, "junk") {
		t.Errorf("script leaked: %q", ex.Text)
	}
}

func TestExtractHTML_ArticlePreferredOverBodyChrome(t *testing.T) {
	doc := `<html><body><div>sidebar junk</div><article><p>The story.</p></article></body></html>`
	ex := ExtractHTML([]byte(doc))
	if !strings.Contains(ex.Text, "The story.") {
		t.Errorf("Text = %q", ex.Text)
	}
	if strings.Contains(ex.Text, "sidebar junk") {
		t.Errorf("chrome leaked: %q", ex.Text)
	}
}

func TestExtractHTML_OGDescriptionFallback(t *testing.T) {
	doc := `<html><head><meta property="og:description" content="Social summary."></head><body><p>x</p></body></html>`
	ex := ExtractHTML([]byte(doc))
	if ex.Description != "Social summary." {
		t.Errorf("Description = %q", ex.Description)
	}
}

func TestExtractHTML_BlankLinesFolded(t *testing.T) {
	doc := `<html><body><div></div><div></div><div></div><p>one</p><div></div><div></div><p>two</p></body></html>`
	ex := ExtractHTML([]byte(doc))
	if strings.Contains(ex.Text, "\n\n\n") {
		t.Errorf("blank-line runs not folded:\n%q", ex.Text)
	}
}
