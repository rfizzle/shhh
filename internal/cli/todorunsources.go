package cli

import (
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/rfizzle/shhh/internal/web"
)

// ledgerRows reads a stage's transcript rows back into the ledger's own
// shape, so the pages among them are chosen by web.Pages — the one
// definition of a page that was read — rather than by a second rule here.
func ledgerRows(rows []jsonSource) []web.Source {
	out := make([]web.Source, 0, len(rows))
	for _, r := range rows {
		out = append(out, web.Source{Kind: r.Kind, FinalURL: r.URL, Query: r.Query, Title: r.Title,
			Agent: r.Agent, Status: r.Status, Results: r.Results, Bytes: r.Bytes, Evidence: r.Evidence})
	}
	return out
}

// readSources folds the pages one stage read into the run's list, the first
// read of each page kept. Every stage is its own process with its own
// ledger, so the run's list is the union the checkpoint carries from stage
// to stage — and across a process that died between them — and not any one
// stage's answer. A page a server's tool handed back is not among them, for
// the reason web.Pages leaves it out.
// See docs/capabilities/todo.md#a-write-up-says-what-it-read.
func readSources(st *run.State, rows []web.Source) {
	if len(rows) > 0 {
		st.Consulted = true
	}
	listed := map[string]bool{}
	for _, s := range st.Sources {
		listed[web.CanonicalURL(s.URL)] = true
	}
	for _, s := range web.Pages(rows) {
		key := web.CanonicalURL(s.FinalURL)
		if listed[key] {
			continue
		}
		listed[key] = true
		st.Sources = append(st.Sources, run.Source{URL: s.FinalURL, Title: s.Title, Read: true})
	}
}

// citeSources adds, under what the stages read, each address the report
// cites that none of them did — the second list SourcesSection draws. It is
// asked only of a run whose stages' ledgers held a row, as a session asks it
// only of a ledger with rows in it: a run that searched and read nothing says
// so over the addresses it cites, the way the session's block does, while a
// run with no web at all has no reading to hold its citations to, and a
// block saying so on every item would be noise.
// See docs/capabilities/todo.md#a-write-up-says-what-it-read.
func citeSources(st *run.State) {
	if !st.Consulted && len(st.Sources) == 0 {
		return
	}
	listed := map[string]bool{}
	for _, s := range st.Sources {
		listed[web.CanonicalURL(s.URL)] = true
	}
	for _, url := range web.CitedURLs(st.Report) {
		key := web.CanonicalURL(url)
		if listed[key] {
			continue
		}
		listed[key] = true
		st.Sources = append(st.Sources, run.Source{URL: url})
	}
}
