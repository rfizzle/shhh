package web

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

// FetchToolName is approval-gated as an external action: it
// prompts in manual and accept-edits modes and is classifier-judged in auto.
// SearchToolName auto-runs like the read-only tools — it only reaches the
// user-configured search provider.
const (
	FetchToolName  = "web_fetch"
	SearchToolName = "web_search"
)

// MaxInlineBytes bounds the content a web tool result carries inline. The
// rest of a longer page is not lost: it goes to the evidence store whole and
// the result says how to read on from here.
const MaxInlineBytes = 48 << 10

const (
	// scriptShellBodyMin and scriptShellTextMax are the two halves of the
	// verdict on a page that turned out to be a shell for a script. Below
	// the first, a document with little text is simply a short document —
	// 4 KB of markup is about one screen of prose with its tags. Above the
	// second, there is something to read: a client-rendered shell yields its
	// <noscript> line and its skip-to-content link, which together run well
	// under 200 bytes, while an article's first paragraph alone clears it.
	scriptShellBodyMin = 4 << 10
	scriptShellTextMax = 200
)

// KeepFunc stores a page's whole text in the session's evidence store and
// returns the opaque id that pages it back; ok=false is a store that would
// not take it, and the inline cut then stands on its own.
//
// A function rather than the store itself, for the reason the process
// supervisor and the quality gate take one too: this package answers for what
// a fetch costs, not for where a session keeps what it has read.
type KeepFunc func(tool, content string) (id string, ok bool)

// Toolset bundles the web tools one session registers. Searcher may be nil:
// without a configured API key the search tool is not registered at all.
type Toolset struct {
	Fetcher  *Fetcher
	Searcher *Searcher

	// PDFText is the path of the PDF reader this machine has, or "" where it
	// has none, in which case a fetched PDF says so by name.
	PDFText string

	// keep and scrub are the session's evidence store, installed by
	// UseEvidence; nil is a session without one.
	keep  KeepFunc
	scrub func(string) string
}

// NewToolset builds the session toolset.
func NewToolset(fetcher *Fetcher, searcher *Searcher) *Toolset {
	return &Toolset{Fetcher: fetcher, Searcher: searcher}
}

// UseEvidence points a fetch at the session's evidence store: keep writes a
// page's whole text there, and scrub is the rewrite that copy goes through
// first. Either may be nil.
//
// The scrub is taken here rather than left to the door outside this one
// because the inline view has to be the opening bytes of the stored copy: cut
// one text and store another, and an offset the model reads out of the notice
// lands somewhere else in the original.
// See docs/capabilities/secrets.md#the-value-is-scrubbed-at-every-door.
//
// Both are installed once, while the session registers its tools and before
// any call runs, which is what makes reading them from the several goroutines
// a round's fetches run on safe. The toolset a session builds is the same
// object its children fetch through, so a child's page is kept in the same
// store and paged back with the same id.
func (t *Toolset) UseEvidence(keep KeepFunc, scrub func(string) string) {
	t.keep, t.scrub = keep, scrub
}

// Definitions returns the provider tool definitions to register.
func (t *Toolset) Definitions() []provider.Tool {
	defs := []provider.Tool{{
		Name: FetchToolName,
		Description: "Fetch a public http/https URL and return its readable content (HTML is reduced to text; PDF is read where a reader is installed; JSON and plain text pass through bounded). " +
			"The result includes the final URL for citation. A long page is kept whole as evidence and the result says how to read on from where it was cut — page it back, never fetch the same URL again for the rest. " +
			"Requests to private, loopback, and cloud-metadata addresses are blocked. " +
			"This is an external action: the user may be asked to approve it.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"url": {"type": "string", "description": "Absolute http or https URL to fetch"}
			},
			"required": ["url"]
		}`),
	}}
	if t.Searcher != nil {
		defs = append(defs, provider.Tool{
			Name:        SearchToolName,
			Description: "Search the web and return the top results (title, URL, description). Use web_fetch to read a result's full content.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"},
					"count": {"type": "integer", "description": "Number of results (1-10, default 10)"}
				},
				"required": ["query"]
			}`),
		})
	}
	return defs
}

// Has reports whether name is a web tool this session registered.
func (t *Toolset) Has(name string) bool {
	switch name {
	case FetchToolName:
		return true
	case SearchToolName:
		return t.Searcher != nil
	}
	return false
}

// Execute dispatches a web tool call.
func (t *Toolset) Execute(name string, args json.RawMessage) (string, error) {
	switch {
	case name == FetchToolName:
		return t.executeFetch(args)
	case name == SearchToolName && t.Searcher != nil:
		return t.executeSearch(args)
	}
	return "", fmt.Errorf("unknown web tool: %s", name)
}

// WrapExecutor returns an executor that dispatches web tools and hands
// everything else to next.
func (t *Toolset) WrapExecutor(next func(name string, args json.RawMessage) (string, error)) func(string, json.RawMessage) (string, error) {
	return func(name string, args json.RawMessage) (string, error) {
		if t.Has(name) {
			return t.Execute(name, args)
		}
		return next(name, args)
	}
}

type fetchArgs struct {
	URL string `json:"url"`
}

// FetchSummary validates fetch arguments and returns the one-line summary the
// approval card shows ("GET <url>"). Validation matches execution, so a call
// that previews cleanly only fails later on network conditions.
func (t *Toolset) FetchSummary(args json.RawMessage) (string, error) {
	var a fetchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(a.URL) == "" {
		return "", fmt.Errorf("url is required")
	}
	if _, err := t.Fetcher.Policy.ValidateURL(a.URL); err != nil {
		return "", err
	}
	return "GET " + a.URL, nil
}

// FetchPlan is what a fetch would do, for the approval card's blast-radius
// block. shhh cannot resolve an outbound request the way it resolves
// a shell command's paths, so the toolset that owns the request states it.
type FetchPlan struct {
	// Host is the domain the request leaves for.
	Host string
	// Sends is what goes out with it — deliberately a short, complete
	// sentence, because "what does this leak" is the question the field
	// exists to answer.
	Sends string
	// Receives is what comes back and where it lands.
	Receives string
}

// FetchPlan validates fetch arguments and describes the request they would
// make. It reports the same errors FetchSummary does, over the same
// validation, so a call that previews cleanly is one that could run.
func (t *Toolset) FetchPlan(args json.RawMessage) (FetchPlan, error) {
	var a fetchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return FetchPlan{}, fmt.Errorf("invalid arguments: %w", err)
	}
	target, err := t.Fetcher.Policy.ValidateURL(a.URL)
	if err != nil {
		return FetchPlan{}, err
	}
	// What the card promises depends on where the page lands. With a store
	// the conversation pays for the first slice and the rest is retrievable;
	// without one the cut is the end of the page, and saying "whole" would
	// promise a person something the session cannot do.
	receives := fmt.Sprintf("page text into the conversation, bounded to %s", formatBytes(t.Fetcher.maxBody()))
	if t.keep != nil {
		receives = fmt.Sprintf("page text, whole, into the evidence store; the first %s into the conversation",
			formatBytes(MaxInlineBytes))
	}
	return FetchPlan{
		Host:     target.Host,
		Sends:    "the URL and a " + userAgent + " user-agent",
		Receives: receives,
	}, nil
}

// formatBytes renders a ceiling in whole units.
func formatBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func (t *Toolset) executeFetch(args json.RawMessage) (string, error) {
	var a fetchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(a.URL) == "" {
		return "", fmt.Errorf("url is required")
	}
	res, err := t.Fetcher.Fetch(context.Background(), a.URL, nil)
	if err != nil {
		return "", err
	}
	return t.FormatFetchResult(res), nil
}

// FormatFetchResult renders a fetched response as the tool result: a header
// with the final URL for citation, then the readable content — whole in the
// evidence store where the session has one, and bounded inline either way.
//
// A response out of the cache goes through exactly this, which is what makes
// a cached page pageable: the cache keeps what was fetched rather than what
// was extracted, so the second read of a long page costs no request and still
// leaves an entry the model can read on from.
// See docs/capabilities/evidence.md#a-page-is-kept-whole.
func (t *Toolset) FormatFetchResult(res Result) string {
	mediaType := res.ContentType
	if mt, _, err := mime.ParseMediaType(res.ContentType); err == nil {
		mediaType = mt
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "URL: %s\n", res.FinalURL)
	fmt.Fprintf(&sb, "Status: %d\n", res.Status)
	fmt.Fprintf(&sb, "Content-Type: %s", mediaType)
	if res.FromCache {
		sb.WriteString(" (cached)")
	}
	sb.WriteString("\n")
	if res.Truncated {
		fmt.Fprintf(&sb, "Note: the response body exceeded the download ceiling and was truncated.\n")
	}
	sb.WriteString("\n")

	switch {
	case mediaType == "text/html" || mediaType == "application/xhtml+xml":
		ex := ExtractHTML(res.Body)
		if ex.Title != "" {
			fmt.Fprintf(&sb, "# %s\n", ex.Title)
		}
		if ex.Description != "" {
			fmt.Fprintf(&sb, "> %s\n", ex.Description)
		}
		if ex.Title != "" || ex.Description != "" {
			sb.WriteString("\n")
		}
		if verdict := scriptShell(ex.Text, len(res.Body)); verdict != "" {
			sb.WriteString(verdict)
			break
		}
		sb.WriteString(t.inline(ex.Text))
	case mediaType == "application/pdf":
		sb.WriteString(t.inlinePDF(res.Body))
	case mediaType == "application/json" || strings.HasPrefix(mediaType, "text/") ||
		strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml") ||
		mediaType == "application/xml":
		sb.WriteString(t.inline(string(res.Body)))
	default:
		fmt.Fprintf(&sb, "(binary content type %q, %d bytes — not rendered)", mediaType, len(res.Body))
	}
	return sb.String()
}

// inline is the model's view of a page's text: the whole of it goes to the
// evidence store first, and what comes back is the opening MaxInlineBytes of
// exactly those bytes with the notice that pages the rest.
//
// The store is written before the cut, never after. Cutting first and letting
// something downstream keep the result is the defect this order exists to
// prevent: what reaches the store is then the cut text, and the tail of a long
// page is gone before anything can hold it — while the notice still offers to
// page a page that is no longer there.
// See docs/capabilities/evidence.md#a-page-is-kept-whole.
func (t *Toolset) inline(text string) string {
	if t.scrub != nil {
		text = t.scrub(text)
	}
	if len(text) <= MaxInlineBytes {
		return text
	}
	// Only a page that does not fit is stored. An entry for one that does is
	// space in the store and a prune later for an id nothing will ever ask
	// for — the entry exists to back the notice, and there is no notice.
	var id string
	if t.keep != nil {
		if kept, ok := t.keep(FetchToolName, text); ok {
			id = kept
		}
	}
	cut, _ := tools.TruncateOutput(text, MaxInlineBytes)
	if id == "" {
		return cut + "\n… (content truncated at inline limit)"
	}
	// Worded like the notice a reduction leaves, so the one instruction the
	// toolbox gives about evidence — a notice carrying an id can be paged
	// back with the evidence tool — covers a cut page too. The offset is
	// spelled out because it is the one number the model would otherwise
	// have to guess, and a wrong guess reads as a page with a hole in it.
	return cut + fmt.Sprintf(
		"\n… [page text cut at %d of %d bytes; full output stored as evidence %s — retrieve it with the evidence tool (info/read/search): read from offset %d for the rest, or search it for a literal]",
		len(cut), len(text), id, len(cut))
}

// inlinePDF renders a fetched PDF through the reader this machine has. With
// no reader the result names the binary that would have read it: a fetch that
// merely failed invites the same URL again, and the bytes would be just as
// unreadable the second time.
func (t *Toolset) inlinePDF(body []byte) string {
	size := formatBytes(int64(len(body)))
	if t.PDFText == "" {
		return fmt.Sprintf("(PDF, %s — this machine has no %s on PATH, so a PDF cannot be read here. "+
			"Fetching it again will not change that: look for the same material as a page, or ask the user to install poppler.)",
			size, PDFTextBinary)
	}
	text, truncated, err := pdfToText(context.Background(), t.PDFText, body, t.textCeiling())
	if err != nil {
		return fmt.Sprintf("(PDF, %s — it could not be read: %v)", size, err)
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Sprintf("(PDF, %s — no text layer: its pages are images, and nothing was extracted. "+
			"Fetching it again returns the same bytes.)", size)
	}
	// The ceiling is said before the text rather than after it, so what a
	// cut page ends on is the notice that pages it back. A second line under
	// that notice reads as part of it and is about a different cut.
	if truncated {
		return fmt.Sprintf("(the extracted text passed the %s ceiling and was cut there)\n\n%s",
			formatBytes(int64(t.textCeiling())), t.inline(text))
	}
	return t.inline(text)
}

// textCeiling is how much text one fetch may yield. A PDF's text is bounded
// by the same ceiling as a page's bytes, because it answers the same question
// — how much of one document a single fetch may spend — and a document that
// expands into more text than it weighs is exactly what the bound is for.
func (t *Toolset) textCeiling() int {
	if t.Fetcher == nil {
		return DefaultMaxBodyBytes
	}
	return int(t.Fetcher.maxBody())
}

// scriptShell reports a page that arrived as a shell for a script: a document
// large enough to be an article that yielded almost no readable text. It
// returns "" for every page that has something to read.
//
// The verdict is worth a sentence because the alternative is silence. A fetch
// that returns a title and four words looks like a page that says four
// things, and the reader's next move is the same URL with a different guess
// at what to extract — which returns the same four words, at the same cost.
func scriptShell(text string, bodyBytes int) string {
	text = strings.TrimSpace(text)
	if bodyBytes < scriptShellBodyMin || len(text) > scriptShellTextMax {
		return ""
	}
	return fmt.Sprintf("(rendered by script; no readable text — %s of HTML yielded %d bytes of text. "+
		"Fetching it again returns the same shell: try the project's repository, a documentation mirror, "+
		"or the data endpoint the page itself reads.)", formatBytes(int64(bodyBytes)), len(text))
}

type searchArgs struct {
	Query string `json:"query"`
	Count int    `json:"count"`
}

func (t *Toolset) executeSearch(args json.RawMessage) (string, error) {
	var a searchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(a.Query) == "" {
		return "", fmt.Errorf("query is required")
	}
	results, err := t.Searcher.Search(context.Background(), a.Query, a.Count)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "No results.", nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d result(s):\n", len(results))
	for i, r := range results {
		fmt.Fprintf(&sb, "\n%d. %s\n   %s\n", i+1, strings.TrimSpace(r.Title), r.URL)
		if desc := strings.TrimSpace(r.Description); desc != "" {
			fmt.Fprintf(&sb, "   %s\n", desc)
		}
	}
	return sb.String(), nil
}
