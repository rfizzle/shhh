package cli

// The session's tools screen, read from here: every place this session's
// tools came from outside the toolset shhh registers itself, in the words
// each owning command already gives — a server in `/mcp`'s listing's, a
// missing search backend in the doctor's
// (docs/interface/surfaces.md#the-supporting-screens).

import (
	"strings"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/web"
)

// sessionToolSources wires the tools screen. Every source is read again on
// every call, because the screen asks again on every frame: a server can stop
// answering and a language server can fail to start while it is up. The
// trust answer is the function behind /trust, and it is offered only where
// it can be recorded — the same condition `shhh mcp`'s row puts on its [a].
func sessionToolSources(session chatSession, cfg config.Config, db *storage.DB) chat.ToolSources {
	root := mcpRoot()
	canTrust := db != nil && root != ""
	out := chat.ToolSources{
		Read: func() []components.ToolsSource {
			var sources []components.ToolsSource
			sources = append(sources, mcpScreenSources(session.mcpTools, session.mcpCatalog, root, canTrust)...)
			sources = append(sources, lspScreenSources(session.lsp)...)
			sources = append(sources, binaryScreenSources(session.structural)...)
			return append(sources, webScreenSources(session.web, cfg)...)
		},
	}
	if canTrust {
		out.Trust = trustManager(db)
	}
	return out
}

// mcpScreenSources is each server as the rail reads it (mcpToolSources, the
// reading Restate hands the rail), with the listing's own row for what it
// reaches, what not being up costs and what would fix it (mcpServerRow), and
// every tool it registered; then each definition that could not be read at
// all, which the listing reports as a diagnostic. A server the checkout
// declared offers the checkout's answer, whichever way it stands now.
func mcpScreenSources(ts *mcp.Toolset, cat *mcp.Catalog, root string, canTrust bool) []components.ToolsSource {
	var out []components.ToolsSource
	if ts != nil {
		trust := projectTrust()
		for i, src := range mcpToolSources(ts) {
			rep := ts.Reports[i]
			row, _ := mcpServerRow(rep, root)
			s := components.ToolsSource{
				Group: components.ToolsServers, Source: src, Detail: row.Subject,
				Consequence: row.Consequence, Fix: row.Fix,
			}
			if rep.Status == mcp.StatusConnected && rep.Server != nil {
				for _, t := range rep.Server.RegisteredTools() {
					s.Tools = append(s.Tools, t.Name)
				}
			}
			if canTrust && rep.Definition.Scope == mcp.ScopeProject {
				if trust.Allows() {
					s.Offer = components.ToolsOfferDistrust
					s.Ask = "Withdraw trust from " + shortPath(trust.Root) + "? " + rep.Definition.Name +
						" and everything else the checkout declares stop loading from the next session on."
				} else {
					s.Offer, s.Ask = components.ToolsOfferTrust, mcpTrustPrompt(trust, rep.Definition)
				}
			}
			out = append(out, s)
		}
	}
	if cat != nil {
		for _, d := range cat.Diagnostics {
			out = append(out, components.ToolsSource{
				Group:       components.ToolsUnloaded,
				Source:      components.InspectorToolSource{Name: "definition", State: components.ToolSourceFailed, Note: firstLine(d)},
				Consequence: "it is not a server in this session",
				Fix:         strings.Split(d, "\n"),
			})
		}
	}
	return out
}

// lspScreenSources is each language server the session detected, and how the
// session stands with it: not started until a file it owns is touched,
// running, or failed at the start, which is remembered for the session.
func lspScreenSources(ts *lsp.Toolset) []components.ToolsSource {
	if ts == nil || ts.Manager == nil {
		return nil
	}
	var tools []string
	for _, d := range ts.Definitions() {
		tools = append(tools, d.Name)
	}
	var out []components.ToolsSource
	for _, st := range ts.Manager.States() {
		s := components.ToolsSource{
			Group:  components.ToolsLanguage,
			Source: components.InspectorToolSource{Name: st.Name, State: components.ToolSourceUp, Note: countOf(len(tools), "tool", "tools")},
			Detail: st.Command + " · " + strings.Join(st.Extensions, " "),
			Tools:  tools,
		}
		switch {
		case st.Err != "":
			s.Source.State, s.Source.Note = components.ToolSourceFailed, firstLine(st.Err)
			s.Consequence = "its files get no diagnostics after an edit and no answers from it until the next session"
			s.Fix = append(strings.Split(st.Err, "\n"), "shhh doctor names the language servers on PATH")
		case !st.Running:
			s.Source.Note += " · starts on the first file it owns"
		}
		out = append(out, s)
	}
	return out
}

// binaryScreenSources is each binary the structural tools found on PATH with
// the tools registered over it, and the optional ones missing as one row —
// the doctor's tools row, laid out.
func binaryScreenSources(ts *structural.Toolset) []components.ToolsSource {
	found, missing := ts.Binaries()
	var out []components.ToolsSource
	for _, b := range found {
		out = append(out, components.ToolsSource{
			Group:  components.ToolsBinaries,
			Source: components.InspectorToolSource{Name: b.Name, State: components.ToolSourceUp, Note: countOf(len(b.Tools), "tool", "tools")},
			Detail: b.Path,
			Tools:  b.Tools,
		})
	}
	if len(missing) > 0 {
		out = append(out, components.ToolsSource{
			Group:       components.ToolsBinaries,
			Source:      components.InspectorToolSource{Name: strings.Join(missing, ", "), State: components.ToolSourceOff, Note: "not on PATH"},
			Consequence: "the tools over them are not registered; the built-in tools do their work",
			Fix:         []string{"install them and start a new session; shhh doctor names what this machine has"},
		})
	}
	return out
}

// webScreenSources is the web tools: fetch, which a session with web tools
// always has, and search, which it has only where a backend is set. A missing
// backend is said in the doctor's own words for it.
func webScreenSources(ts *web.Toolset, cfg config.Config) []components.ToolsSource {
	if ts == nil {
		return nil
	}
	out := []components.ToolsSource{{
		Group:  components.ToolsWeb,
		Source: components.InspectorToolSource{Name: "fetch", State: components.ToolSourceUp, Note: "1 tool"},
		Detail: "public http and https pages, read against the host lists",
		Tools:  []string{web.FetchToolName},
	}}
	if ts.Searcher != nil {
		backend := ts.Searcher.Provider
		if backend == "" {
			backend = web.ProviderBrave
		}
		return append(out, components.ToolsSource{
			Group:  components.ToolsWeb,
			Source: components.InspectorToolSource{Name: "search", State: components.ToolSourceUp, Note: "1 tool"},
			Detail: backend,
			Tools:  []string{web.SearchToolName},
		})
	}
	var f doctorFinding
	switch backend := cfg.Web.SearchProvider; backend {
	case "", web.ProviderBrave:
		f = doctorSearchBrave(false)
	case web.ProviderSearXNG:
		f = doctorSearchNoInstance()
	default:
		f = doctorSearchUnknown(backend)
	}
	state := components.ToolSourceOff
	if f.State == components.DoctorWarned {
		state = components.ToolSourceBlocked
	}
	return append(out, components.ToolsSource{
		Group:       components.ToolsWeb,
		Source:      components.InspectorToolSource{Name: "search", State: state, Note: f.Subject},
		Detail:      f.Detail,
		Consequence: f.Consequence,
		Fix:         f.Fix,
	})
}
