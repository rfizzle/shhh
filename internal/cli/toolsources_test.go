package cli

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/web"
)

// Each server on the tools screen is the rail's own reading of it, with the
// listing's own words for what it reaches, what not being up costs and what
// would fix it — so the block, the screen and /mcp's listing say one thing.
func TestMCPScreenSourcesAreTheRailsReadingInTheListingsWords(t *testing.T) {
	withProjectTrust(t, project.Trust{Root: "/repo"})
	ts := mcp.FromReports([]mcp.Report{
		{Definition: mcp.Definition{Name: "docs", Scope: mcp.ScopeUser, Transport: mcp.TransportStdio, Command: "docs-mcp"},
			Status: mcp.StatusConnected, Server: &mcp.Server{Tools: []mcp.Tool{{Name: "docs__search", Remote: "search"}}}},
		{Definition: mcp.Definition{Name: "tracker", Scope: mcp.ScopeProject, Transport: mcp.TransportStdio, Command: "npx", Source: "/repo/.mcp.json"},
			Status: mcp.StatusUntrusted},
		{Definition: mcp.Definition{Name: "linear", Scope: mcp.ScopeUser, Transport: mcp.TransportStdio, Command: "linear-mcp"},
			Status: mcp.StatusFailed, Error: "server linear: connect: exec: \"linear-mcp\": executable file not found in $PATH"},
	})
	got := mcpScreenSources(ts, &mcp.Catalog{Diagnostics: []string{"/repo/.mcp.json: server Bad Name: bad"}}, "/repo", true)
	rail := mcpToolSources(ts)
	if len(got) != 4 {
		t.Fatalf("sources = %+v", got)
	}
	for i, rep := range ts.Reports() {
		row, _ := mcpServerRow(rep, "/repo")
		s := got[i]
		if s.Group != components.ToolsServers || s.Source != rail[i] {
			t.Errorf("%s: %+v is not the rail's %+v", rep.Definition.Name, s.Source, rail[i])
		}
		if s.Detail != row.Subject || s.Consequence != row.Consequence || strings.Join(s.Fix, "\n") != strings.Join(row.Fix, "\n") {
			t.Errorf("%s: the screen's words %+v are not the listing's %+v", rep.Definition.Name, s, row)
		}
	}
	if strings.Join(got[0].Tools, ",") != "docs__search" {
		t.Errorf("a connected server lists %v", got[0].Tools)
	}
	if !strings.Contains(strings.Join(got[2].Fix, "\n"), "executable file not found") {
		t.Errorf("a failed server's fix lacks why: %v", got[2].Fix)
	}
	// Only the server the checkout declared carries the checkout's answer,
	// in the words `shhh mcp` asks it in.
	if got[0].Offer != components.ToolsOfferNone || got[2].Offer != components.ToolsOfferNone {
		t.Error("a user server offers the checkout's answer")
	}
	if got[1].Offer != components.ToolsOfferTrust || got[1].Ask != mcpTrustPrompt(project.Trust{Root: "/repo"}, ts.Reports()[1].Definition) {
		t.Errorf("the untrusted project server offers %d asking %q", got[1].Offer, got[1].Ask)
	}
	if d := got[3]; d.Group != components.ToolsUnloaded || !strings.Contains(d.Source.Note, "Bad Name") {
		t.Errorf("the unreadable definition reads as %+v", d)
	}

	// Once the checkout is trusted the same row offers the withdrawal, and
	// where nothing can record an answer no row offers one.
	withProjectTrust(t, project.Trust{Root: "/repo", Granted: true})
	if s := mcpScreenSources(ts, nil, "/repo", true)[1]; s.Offer != components.ToolsOfferDistrust || !strings.Contains(s.Ask, "Withdraw trust") {
		t.Errorf("a trusted checkout's server offers %d asking %q", s.Offer, s.Ask)
	}
	for _, s := range mcpScreenSources(ts, nil, "/repo", false) {
		if s.Offer != components.ToolsOfferNone {
			t.Errorf("%s offers an answer nothing can record", s.Source.Name)
		}
	}
	if mcpScreenSources(nil, nil, "/repo", true) != nil {
		t.Error("a session with no servers has server rows")
	}
}

// A language server is up with its tools until its start fails, and says
// that it has not started yet where nothing has touched a file it owns. The
// manager is built and asked; nothing is spawned.
func TestLSPScreenSourcesStartNothing(t *testing.T) {
	mgr := lsp.NewManager(t.TempDir(), []lsp.ServerSpec{{Name: "gopls", Command: "gopls", Extensions: []string{".go"}}}, lsp.Options{})
	got := lspScreenSources(lsp.NewToolset(mgr))
	if len(got) != 1 {
		t.Fatalf("sources = %+v", got)
	}
	s := got[0]
	if s.Group != components.ToolsLanguage || s.Source.State != components.ToolSourceUp ||
		!strings.Contains(s.Source.Note, "starts on the first file it owns") || len(s.Tools) == 0 || s.Detail != "gopls · .go" {
		t.Errorf("an untouched language server reads as %+v", s)
	}
	if lspScreenSources(nil) != nil || binaryScreenSources(nil) != nil {
		t.Error("a session with neither has rows for them")
	}
}

// Fetch is up wherever the session has web tools; search is up with its
// backend, and without one it says so in the doctor's own words.
func TestWebScreenSourcesSayWhySearchIsMissing(t *testing.T) {
	if webScreenSources(nil, config.Config{}) != nil {
		t.Fatal("a session with no web tools has web rows")
	}
	got := webScreenSources(web.NewToolset(web.NewFetcher(web.Policy{}), nil), config.Config{})
	if len(got) != 2 || got[0].Source.Name != "fetch" || got[0].Source.State != components.ToolSourceUp {
		t.Fatalf("sources = %+v", got)
	}
	off := doctorSearchBrave(false)
	if s := got[1]; s.Source.State != components.ToolSourceOff || s.Source.Note != off.Subject || s.Consequence != off.Consequence {
		t.Errorf("search with no key reads as %+v", s)
	}
	cfg := config.Config{Web: config.WebConfig{SearchProvider: web.ProviderSearXNG}}
	if s := webScreenSources(web.NewToolset(web.NewFetcher(web.Policy{}), nil), cfg)[1]; s.Source.State != components.ToolSourceBlocked {
		t.Errorf("searxng with no instance reads as %+v", s)
	}
	up := webScreenSources(web.NewToolset(web.NewFetcher(web.Policy{}), &web.Searcher{Provider: web.ProviderSearXNG}), config.Config{})
	if s := up[1]; s.Source.State != components.ToolSourceUp || s.Detail != web.ProviderSearXNG || s.Tools[0] != web.SearchToolName {
		t.Errorf("a configured search reads as %+v", s)
	}
}
