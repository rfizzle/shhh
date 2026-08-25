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

// FetchToolName is approval-gated as an external action (S-059/S-066): it
// prompts in manual and accept-edits modes and is classifier-judged in auto.
// SearchToolName auto-runs like the read-only tools — it only reaches the
// user-configured search provider.
const (
	FetchToolName  = "web_fetch"
	SearchToolName = "web_search"
)

// MaxInlineBytes bounds the content a web tool result carries inline. It sits
// well above the reduction threshold (S-064), so a large page is reduced for
// the model with the full extracted text retrievable as evidence.
const MaxInlineBytes = 48 << 10

// Toolset bundles the web tools one session registers. Searcher may be nil:
// without a configured API key the search tool is not registered at all.
type Toolset struct {
	Fetcher  *Fetcher
	Searcher *Searcher
}

// NewToolset builds the session toolset.
func NewToolset(fetcher *Fetcher, searcher *Searcher) *Toolset {
	return &Toolset{Fetcher: fetcher, Searcher: searcher}
}

// Definitions returns the provider tool definitions to register.
func (t *Toolset) Definitions() []provider.Tool {
	defs := []provider.Tool{{
		Name: FetchToolName,
		Description: "Fetch a public http/https URL and return its readable content (HTML is reduced to text; JSON and plain text pass through bounded). " +
			"The result includes the final URL for citation. Requests to private, loopback, and cloud-metadata addresses are blocked. " +
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
// block (S-101). shhh cannot resolve an outbound request the way it resolves
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
	return FetchPlan{
		Host:     target.Host,
		Sends:    "the URL and a " + userAgent + " user-agent",
		Receives: fmt.Sprintf("page text into the conversation, bounded to %s", formatBytes(t.Fetcher.maxBody())),
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
	return FormatFetchResult(res), nil
}

// FormatFetchResult renders a fetched response as the tool result: a header
// with the final URL for citation, then bounded readable content.
func FormatFetchResult(res Result) string {
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
		sb.WriteString(boundInline(ex.Text))
	case mediaType == "application/json" || strings.HasPrefix(mediaType, "text/") ||
		strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml") ||
		mediaType == "application/xml":
		sb.WriteString(boundInline(string(res.Body)))
	default:
		fmt.Fprintf(&sb, "(binary content type %q, %d bytes — not rendered)", mediaType, len(res.Body))
	}
	return sb.String()
}

// boundInline caps inline content at MaxInlineBytes with a truncation notice.
func boundInline(s string) string {
	if cut, truncated := tools.TruncateOutput(s, MaxInlineBytes); truncated {
		return cut + "\n… (content truncated at inline limit)"
	}
	return s
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
