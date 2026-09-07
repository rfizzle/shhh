package components

// The sources screen (docs/interface/surfaces.md#the-supporting-screens):
// what the session read, as the fetcher recorded it rather than as the model
// remembered it.
//
// It is re-cut from parts that already exist, like every other screen in the
// family: the selector window with its headers grouping the rows, the preview
// pane beside it, and the shared chrome around both. The list is grouped by
// host because that is how a reader looks for a page they half remember —
// they know the site — and the rows under a host run oldest first, so the
// last row of a group is the last thing read there.
//
// It is a passive component. It owns no ledger semantics: the host formats
// every field, because how many bytes a fetch cost and which turn it happened
// in are readings of the session, and this is a renderer.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// sourcesStackWidth is the width below which the panes stack. The rows
	// carry a path and an agent and the preview carries whole URLs, so two
	// columns want as much room as the history browser's.
	sourcesStackWidth = 96
	// sourcesListMin / sourcesListMax bound the list column. A row carries
	// four fields, as the history browser's does — the path, the outcome,
	// the agent that read it and the size — so it takes very nearly half the
	// terminal, and the same ceiling: past it the preview is what the extra
	// column is worth, since that is where the whole URL is read.
	sourcesListMin = 30
	sourcesListMax = 64
	// sourcesMinPreview is the smallest preview the stacked layout leaves
	// standing: the title, the URL it answered from, the counts and the line
	// that says where the page is kept.
	sourcesMinPreview = 4
)

// SourcesRow is one read, already resolved to what the screen draws. Every
// field arrives as its words — `200`, `48 KB`, `turn 4` — because those are
// readings of the ledger.
type SourcesRow struct {
	// ID is the host's own handle on the row, carried back on a result and
	// never drawn.
	ID string
	// Group is the header the row is filed under: the host for a fetch, and
	// the one word a search is filed under for a search.
	Group string
	// Label is the row's target — the path under the host, or the words a
	// search was made of.
	Label string
	// Kind is `fetch` or `search`, in the preview's words.
	Kind string
	// Requested is the URL the model asked for; FinalURL the one that
	// answered. The preview says both only when they differ, because a
	// redirect is the one thing a citation can get wrong on its own.
	Requested string
	FinalURL  string
	// Title is the page's own title, where the extraction found one.
	Title string
	// Status is the row's outcome field — `200`, `404`, `3 results`. It is
	// the reason to read the row, so it never clips.
	Status string
	// Bytes is what came back, in whole units; Cached says it cost no
	// request.
	Bytes  string
	Cached bool
	// Turn and Agent are who read it and when — `turn 4`, `web-researcher`.
	Turn  string
	Agent string
	// Evidence is the store entry holding the whole page, and Head its
	// opening lines. A row with neither is a page that fitted in the
	// conversation and was never kept.
	Evidence string
	Head     []string
	// State picks the glyph the row leads with and the colour its outcome
	// takes.
	State ActivityState
}

// SourcesResult is how the screen closed: with a page to read, or with
// nothing.
type SourcesResult struct {
	// Open is `[enter]` on a row whose page was kept; ID and Evidence name
	// it. The host does the opening, because what "open" means is the
	// session's business and not this screen's.
	Open     bool
	ID       string
	Evidence string
	Canceled bool
}

// SourcesScreen is `/sources`: a takeover in the chat, full width, owning the
// keyboard for as long as it is up.
type SourcesScreen struct {
	// Rows are the reads in the order they happened, oldest first, as the
	// host read them out of the ledger.
	Rows []SourcesRow
	// Focus is an index into Rows and survives the host rebuilding them.
	Focus int
	// Subject is what the header says the screen is over — `12 pages · 3
	// hosts`. The host counts it.
	Subject string
	// MaxLines bounds the screen height; everything pinned comes off the
	// panes' budget. 0 is unbounded.
	MaxLines int
	// Notice is the line a key left behind. The host clears it on the next
	// keystroke.
	Notice string

	list  Select
	order []int
	optAt map[int]int
	keys  bool
}

// Update is the screen's whole keyboard.
func (s *SourcesScreen) Update(msg tea.KeyPressMsg) (done bool, result SourcesResult) {
	s.sync()
	pressed := msg.String()
	switch {
	case s.moved(pressed):
		return false, SourcesResult{}
	case keys.Is(pressed, keys.Sources.Open):
		// The one key that leaves with something to do, and only for a row
		// whose page was kept: a key that cannot act is not an offer
		// (invariant 5).
		if row := s.current(); row != nil && row.Evidence != "" {
			return true, SourcesResult{Open: true, ID: row.ID, Evidence: row.Evidence}
		}
		return false, SourcesResult{}
	case keys.Is(pressed, keys.Sources.List):
		s.keys = !s.keys
	case keys.Is(pressed, keys.Sources.Back):
		return true, SourcesResult{Canceled: true}
	}
	return false, SourcesResult{}
}

// SetSize gives the screen the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (s *SourcesScreen) SetSize(_, height int) { s.MaxLines = height }

// View renders the screen: the shared chrome, with the two panes in the rows
// it leaves.
func (s *SourcesScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	s.sync()
	return ScreenChrome{
		Header:   s.header(),
		Foot:     s.footer(width).Rows(width),
		Notice:   s.Notice,
		MaxLines: s.MaxLines,
	}.View(width, func(budget int) []string { return s.panes().rows(width, budget) })
}

// panes is the body, split the way every screen with a list and a preview
// splits it (screenpanes.go).
func (s *SourcesScreen) panes() screenPanes {
	return screenPanes{
		stackAt: sourcesStackWidth, listMin: sourcesListMin,
		listMax: sourcesListMax, minPreview: sourcesMinPreview,
		list:    s.listRows,
		preview: s.previewRows,
	}
}

// listRows is the left pane: the ledger grouped under its hosts.
func (s *SourcesScreen) listRows(width, budget int) []string {
	if len(s.Rows) == 0 {
		return []string{sty.Dim.Render(Clip("nothing has been read in this session", width))}
	}
	body, _ := s.list.visibleRows(cardWidthFor(width), budget, false)
	return body
}

// previewRows is the right pane: the row the pointer is on, field by field,
// and the opening of the page where one was kept.
//
// The requested URL is stated only when the redirect took the fetch
// somewhere else. That difference is the whole reason the ledger keeps both:
// a citation that names the URL asked for names a page nobody read.
func (s *SourcesScreen) previewRows(width int) []string {
	row := s.current()
	if row == nil {
		return []string{sty.Dim.Render(Clip("no source selected", width))}
	}
	rows := []string{paneTitle(brightStyle().Render(oneLine(s.subjectOf(*row))),
		sty.Dim.Render(row.Agent), width)}
	if row.Title != "" {
		rows = append(rows, "  "+sty.Dim.Render(Clip(oneLine(row.Title), max(width-2, 1))))
	}
	rows = append(rows, "")
	for _, f := range s.fields(*row) {
		rows = append(rows, "  "+sty.Dimmer.Render(f.label+" ")+
			sty.Dim.Render(Clip(f.value, max(width-lipgloss.Width(f.label)-3, 1))))
	}
	if len(row.Head) > 0 {
		rows = append(rows, "", sty.Dimmer.Render(Clip("  the page as it was kept", width)))
		for _, line := range row.Head {
			rows = append(rows, "  "+sty.Dim.Render(Clip(oneLine(line), max(width-2, 1))))
		}
	}
	return rows
}

// field is one labelled line of the preview.
type field struct{ label, value string }

// fields is the row as the preview states it, in the order a reader checking
// a citation reads them: what kind of read it was, where it went, what came
// back, and where the page is now.
func (s *SourcesScreen) fields(row SourcesRow) []field {
	out := []field{{"kind", row.Kind}}
	if row.FinalURL != "" {
		out = append(out, field{"url", row.FinalURL})
	}
	if row.Requested != "" && row.Requested != row.FinalURL {
		out = append(out, field{"asked for", row.Requested})
	}
	out = append(out, field{"status", s.outcomeOf(row)})
	if row.Bytes != "" {
		out = append(out, field{"read", row.Bytes})
	}
	if row.Turn != "" {
		out = append(out, field{"when", row.Turn})
	}
	if row.Evidence != "" {
		out = append(out, field{"kept as", row.Evidence})
	}
	return out
}

// subjectOf is what the preview names the row: the URL that answered, or the
// query a search was made of.
func (s *SourcesScreen) subjectOf(row SourcesRow) string {
	if row.FinalURL != "" {
		return row.FinalURL
	}
	if row.Requested != "" {
		return row.Requested
	}
	return row.Label
}

// outcomeOf is the status with the cache said beside it, because a cached
// read is the one row whose status did not come from the host this time.
func (s *SourcesScreen) outcomeOf(row SourcesRow) string {
	if row.Cached {
		return strings.TrimSpace(row.Status + " (cached)")
	}
	return row.Status
}

// header names the surface and what it is over.
func (s *SourcesScreen) header() ScreenHeader {
	h := ScreenHeader{Left: []RailSegment{screenTitle("/sources")}, Keys: s.headerKeys()}
	if s.Subject != "" {
		h.Left = append(h.Left, screenField(s.Subject))
	}
	return h
}

// headerKeys is the pair the header ends with: the key that shows the whole
// register, and the way back in the one word a header field is
// (docs/interface/surfaces.md#the-supporting-screens).
func (s *SourcesScreen) headerKeys() string {
	list := keys.Bracket(keys.Sources.List) + " " + keys.Words(keys.Sources.List)
	if s.keys {
		list = keys.Bracket(keys.Sources.List) + " hide the keys"
	}
	return list + " · " + words(keys.Sources.Back, "back")
}

// footer is the keys the screen offers and the field that annotates them.
func (s *SourcesScreen) footer(width int) KeyFooter {
	field := s.footField()
	return KeyFooter{Offers: s.offers(width, field), Register: s.keyList(),
		Showing: s.keys, Field: field}
}

// offers is the key row. A row whose page was never kept has nothing for
// `[enter]` to open, so it is not offered one (invariant 5).
func (s *SourcesScreen) offers(width int, field string) []KeyOffer {
	var acts []KeyOffer
	if row := s.current(); row != nil && row.Evidence != "" {
		acts = append(acts, keyOffer(keys.Sources.Open))
	}
	acts = append(acts, wayOut(backToPrompt))
	rungs := [][]KeyOffer{append([]KeyOffer{keyOffer(keys.Sources.Move)}, acts...), acts}
	if field == "" {
		return rungs[0]
	}
	for _, rung := range rungs {
		if fitsBeside(rung, field, width) {
			return rung
		}
	}
	return rungs[0]
}

// keyList is every key the screen has, for `[?]`.
func (s *SourcesScreen) keyList() []KeyOffer {
	return []KeyOffer{
		keyOfferAs(keys.Sources.Move, "move between sources"),
		keyOfferAs(keys.Sources.Open, "read the whole page the fetch kept"),
		wayOut(backToPrompt),
		keyOfferAs(keys.Sources.Back, backToPrompt),
	}
}

// footField annotates the key row. What it says is the thing this screen
// exists to say: the rows are the fetcher's own record, not the model's
// account of it.
func (s *SourcesScreen) footField() string {
	if len(s.Rows) == 0 {
		return ""
	}
	return "recorded by the fetch, not by the model"
}

// sync rebuilds the list from Rows. It runs before every Update and every
// View because the host replaces Rows whenever the session reads something
// else, and the pointer has to survive that.
func (s *SourcesScreen) sync() {
	s.order = s.grouped()
	s.optAt = make(map[int]int, len(s.order))
	opts := make([]SelectOption, 0, len(s.order))
	group := ""
	for _, i := range s.order {
		row := s.Rows[i]
		if row.Group != group {
			group = row.Group
			opts = append(opts, SelectOption{Label: group, Header: true})
		}
		s.optAt[i] = len(opts)
		opts = append(opts, SelectOption{
			Label:     sourcesGlyph(row.State) + " " + oneLine(row.Label),
			Value:     s.outcomeOf(row),
			ValueTone: sourcesTone(row.State),
			Desc:      sourcesAgent(row.Agent),
			Meta:      row.Bytes,
		})
	}
	s.list.Options = opts
	// The count the window's marker states is of sources, not of options:
	// the host headers are rows on the screen and not things that were read.
	s.list.Total = len(s.order)
	s.list.Unnumbered = true
	s.list.Focus = s.optIndex(s.Focus)
}

// grouped is the display order: the hosts in the order the session first
// reached them, and each host's reads oldest first under it. The ledger's own
// order is kept inside a group rather than re-sorted, because the last row of
// a host is the last page read there and that is the one a reader is looking
// for.
func (s *SourcesScreen) grouped() []int {
	var groups []string
	byGroup := map[string][]int{}
	for i, row := range s.Rows {
		if _, seen := byGroup[row.Group]; !seen {
			groups = append(groups, row.Group)
		}
		byGroup[row.Group] = append(byGroup[row.Group], i)
	}
	out := make([]int, 0, len(s.Rows))
	for _, g := range groups {
		out = append(out, byGroup[g]...)
	}
	return out
}

// moved walks the pointer between rows, stepping over the headers the way
// every list in the product does.
func (s *SourcesScreen) moved(pressed string) bool {
	if len(s.order) == 0 {
		return false
	}
	l := List[int]{Items: s.order, Focus: s.at()}
	if !l.Move(pressed, keys.Sources.Move) {
		return false
	}
	s.Focus = s.order[l.Focus]
	s.sync()
	return true
}

// at is where the pointer is among the rows in display order.
func (s *SourcesScreen) at() int {
	for i, row := range s.order {
		if row == s.Focus {
			return i
		}
	}
	return 0
}

// current is the row under the pointer, or nil for an empty ledger.
func (s *SourcesScreen) current() *SourcesRow {
	for _, i := range s.order {
		if i == s.Focus {
			return &s.Rows[i]
		}
	}
	return nil
}

// optIndex maps a row index to its place in the list, which carries a header
// per host as well as the rows.
func (s *SourcesScreen) optIndex(row int) int {
	if at, ok := s.optAt[row]; ok {
		return at
	}
	if len(s.order) > 0 {
		return s.optAt[s.order[0]]
	}
	return 0
}

// sourcesGlyph is the row's leading glyph, plain rather than painted for the
// history browser's reason: the label runs through the card's own emphasis,
// and an escape sequence inside it would be cut in half.
func sourcesGlyph(state ActivityState) string {
	switch state {
	case ActivityFailed:
		return "✗"
	case ActivityDenied:
		return "⊘"
	}
	return "⇢"
}

// sourcesTone reads the outcome the way a card field is read: a fetch that
// was refused or broke is at risk, and one that answered is a fact.
func sourcesTone(state ActivityState) FieldTone {
	if state == ActivityFailed || state == ActivityDenied {
		return ToneRisk
	}
	return ToneNeutral
}

// sourcesAgent is the `· web-researcher` continuation. The separator is the
// card's rather than the host's, so every row carries the same one.
func sourcesAgent(agent string) string {
	if agent == "" {
		return ""
	}
	return "· " + agent
}
