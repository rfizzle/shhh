package chat

// The sources screen (docs/interface/surfaces.md#the-supporting-screens):
// `/sources`, the ledger of every page this session and its children fetched
// and every search they made.
//
// `/evidence` answers what the store is holding and what it cost; this
// answers which URLs were read, which is the question a reader checking a
// claim actually has. The two are the same material read from opposite ends,
// which is why a row that has an evidence entry opens it here rather than
// naming an id for the reader to type somewhere else.
// See docs/capabilities/chat.md#what-was-read.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
	"github.com/rfizzle/shhh/internal/web"
)

const (
	// sourcesPeek is how much of a stored page the preview shows, and
	// sourcesPeekLines how many of its lines. It is a glance meant to say
	// "yes, this is the page I meant", not a reading — the whole thing is
	// one keystroke away.
	sourcesPeek      = 2 << 10
	sourcesPeekLines = 6
	// sourcesOpen is how much of a stored page `[enter]` opens. A fetch may
	// have kept two megabytes; the viewer holds what it is given in memory
	// and the reader is looking for a passage, not auditing bytes, so the
	// run is bounded and the notice says what is past it.
	sourcesOpen = 256 << 10
	// searchGroup is the header a search is filed under. It reached the one
	// endpoint the person configured rather than a site, so it has no host
	// to be grouped by.
	searchGroup = "searches"
)

// WithSources attaches the session's sources ledger. The model owns the
// session slot's name, so it is the one that binds the ledger to it — here,
// and again wherever the name changes.
func (m Model) WithSources(l *web.Ledger) Model {
	m.sourceLedger = l
	m.bindSources()
	return m
}

// bindSources points the ledger at the current session slot and tells it
// which turn is open. A bind that fails leaves the rows in memory, which is
// the session's working state either way; only the resume would have lost
// them.
func (m *Model) bindSources() {
	if m.sourceLedger == nil {
		return
	}
	_ = m.sourceLedger.Bind(m.sessionName)
	m.sourceLedger.SetTurn(m.turnCount)
}

// openSources puts the screen up. It is built once per opening, like the
// context surface: what it lists is what the session had read when the
// reader asked, and a screen that grew a row under them mid-read would be
// answering a question they had stopped asking.
func (m Model) openSources() (tea.Model, tea.Cmd) {
	if m.sourceLedger == nil {
		return m.systemNotice("This session has no web tools, so nothing has been read.")
	}
	screen := m.sourcesScreenData()
	m.sources = &screen
	m.enterSurface(stateSources)
	return m, nil
}

// updateSources routes keys while the screen is up. `[enter]` on a row whose
// page was kept opens that page in the full-screen viewer, which comes back
// here rather than to the prompt: the reader is walking a list, and a look at
// one entry is not leaving it.
func (m Model) updateSources(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.sources == nil {
		return m.closeSources()
	}
	m.sources.Notice = ""
	done, result := m.sources.Update(msg)
	if !done {
		return m, nil
	}
	if result.Open {
		return m.openSourcePage(result)
	}
	return m.closeSources()
}

// openSourcePage takes the stored page full screen. An entry the store no
// longer has says so on the row rather than opening an empty viewer — a
// purge or a prune is a thing that happens, and the ledger outlives it.
func (m Model) openSourcePage(result components.SourcesResult) (tea.Model, tea.Cmd) {
	text, ok := m.readEvidence(result.Evidence, sourcesOpen)
	if !ok {
		m.sources.Notice = "The page kept as " + result.Evidence + " is no longer in the evidence store."
		return m, nil
	}
	title := result.ID
	if row := m.sourceRow(result.ID); row != nil {
		title = row.FinalURL
	}
	return m.openOutputFull(&components.OutputView{
		Title: title,
		Lines: strings.Split(text, "\n"),
	}, noOutputEntry, stateSources)
}

// closeSources hands the screen back to the turn.
func (m Model) closeSources() (tea.Model, tea.Cmd) {
	m.sources = nil
	m.leaveSurface()
	m.syncViewport()
	return m, nil
}

// sourcesLines renders the screen, one row per line.
func (m Model) sourcesLines() []string {
	if m.sources == nil {
		return nil
	}
	return strings.Split(m.sources.View(m.contentWidth()), "\n")
}

// renderSourcesHint is the one line the screen leaves where the draft box
// was. The screen holds the keyboard, so the panel states the way out and
// nothing else, the way the context surface's does.
func (m Model) renderSourcesHint() string {
	hint := keys.Bracket(keys.Sources.Back) + " " + keys.Words(keys.Sources.Back)
	return sty.SystemMsg.Render("sources · "+hint) + strings.Repeat("\n", inputHeight-1)
}

// sourceRow is the screen's row with an id, or nil.
func (m Model) sourceRow(id string) *components.SourcesRow {
	if m.sources == nil {
		return nil
	}
	for i, row := range m.sources.Rows {
		if row.ID == id {
			return &m.sources.Rows[i]
		}
	}
	return nil
}

// sourcesScreenData builds the screen from the ledger. Every field is
// resolved to its words here rather than in the component, because what a
// row means — that a status of zero is a fetch that never got an answer,
// that a search's count is not an HTTP status — is a reading of the session.
func (m Model) sourcesScreenData() components.SourcesScreen {
	rows := m.sourceLedger.List()
	out := make([]components.SourcesRow, 0, len(rows))
	for _, s := range rows {
		out = append(out, m.sourcesRow(s))
	}
	return components.SourcesScreen{
		Rows:    out,
		Focus:   max(len(out)-1, 0),
		Subject: sourcesSubject(rows),
	}
}

// sourcesRow is one ledger row as the screen draws it.
func (m Model) sourcesRow(s web.Source) components.SourcesRow {
	row := components.SourcesRow{
		ID: fmt.Sprintf("s%d", s.ID), Kind: s.Kind,
		Requested: s.Requested, FinalURL: s.FinalURL, Title: s.Title,
		Cached: s.Cached, Agent: s.Agent, Evidence: s.Evidence,
		State: components.ActivityDone,
	}
	if s.Turn > 0 {
		row.Turn = fmt.Sprintf("turn %d", s.Turn)
	}
	if s.Kind == web.KindSearch {
		row.Group, row.Label = searchGroup, s.Query
		row.Status = results(s.Results)
		return row
	}
	row.Group = s.Host()
	if row.Group == "" {
		row.Group = "unresolved"
	}
	row.Label = sourcePath(s)
	row.Status = fetchStatus(s.Status)
	row.Bytes = sourceBytes(s.Bytes)
	if s.Status == 0 || s.Status >= 400 {
		row.State = components.ActivityFailed
	}
	if s.Evidence != "" {
		row.Head = m.evidenceHead(s.Evidence)
	}
	return row
}

// evidenceHead is the opening lines of a stored page, for the preview. A
// store that no longer has the entry draws no head rather than an error:
// the row is still a true record of a page that was read, and the fields
// above it are what the reader came for.
func (m Model) evidenceHead(id string) []string {
	text, ok := m.readEvidence(id, sourcesPeek)
	if !ok {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > sourcesPeekLines {
		lines = lines[:sourcesPeekLines]
	}
	return lines
}

// readEvidence asks the store for the opening of an entry; false is a
// session with no store, or an entry it no longer holds.
func (m Model) readEvidence(id string, limit int) (string, bool) {
	if m.evidence.Read == nil || id == "" {
		return "", false
	}
	return m.evidence.Read(id, limit)
}

// sourcesSubject is what the header says the screen is over: the pages, the
// hosts they came from and the searches. A page read twice is one page, the
// way the write-up's own sources block counts it.
func sourcesSubject(rows []web.Source) string {
	searches := 0
	for _, s := range rows {
		if s.Kind == web.KindSearch {
			searches++
		}
	}
	// Both counts are of what was read rather than of what was tried: a
	// fetch that came back 404 is a row on the screen and not a page, and
	// the host it did not answer from is not a host this session read.
	pages := web.Pages(rows)
	hosts := map[string]bool{}
	for _, s := range pages {
		if h := s.Host(); h != "" {
			hosts[h] = true
		}
	}
	parts := []string{plural(len(pages), "page"), plural(len(hosts), "host")}
	if searches > 0 {
		parts = append(parts, searchCount(searches))
	}
	return strings.Join(parts, " · ")
}

// searchCount is the searches field of the header. Its plural is not the
// bare "s" the shared helper adds.
func searchCount(n int) string {
	if n == 1 {
		return "1 search"
	}
	return fmt.Sprintf("%d searches", n)
}

// sourcePath is the row's target: what identifies the page under its host.
// The host is already the header the row sits under, so repeating it on
// every row would spend the column that carries the path.
func sourcePath(s web.Source) string {
	raw := s.FinalURL
	if raw == "" {
		raw = s.Requested
	}
	rest := raw
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		if path := rest[i:]; path != "/" {
			return path
		}
		return "/"
	}
	return "/"
}

// fetchStatus is the status as a field. Zero is a fetch that never got an
// answer — a refusal, a timeout, a host that would not resolve — and saying
// `0` would read as a status the host sent.
func fetchStatus(status int) string {
	if status == 0 {
		return "no answer"
	}
	return fmt.Sprintf("%d", status)
}

// results is a search's own outcome field.
func results(n int) string { return plural(n, "result") }

// sourceBytes is what a fetch brought back, in whole units. A read of
// nothing draws no field: the status beside it already says what happened.
func sourceBytes(n int) string {
	switch {
	case n <= 0:
		return ""
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
