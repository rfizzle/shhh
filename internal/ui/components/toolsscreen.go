package components

// The tools screen (docs/interface/surfaces.md#the-supporting-screens): every
// place this session's tools came from, the list the rail's TOOLS block holds
// only the first few rows of.
//
// A row is one source — the built-in toolset, an MCP server, a language
// server, a binary found on PATH, a web tool — carrying the rail's own reading
// of it where the rail draws one, so the state and the note are the TOOLS
// row's words for the same fact. The preview is that source laid out: what it
// reaches, every tool it registered, and for one that is not up what that
// costs and what would move it, in the words the listing behind `/mcp` has
// always given. A server a checkout declared carries the checkout's answer as
// `[a]`, asked about first. This is a renderer; the sources are the host's.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// toolsStackWidth is the width below which the panes stack. The preview
	// is an account and a wrapped list of tool names, about as wide as the
	// alerts screen's, so the panes stand side by side as soon as its do.
	toolsStackWidth = 88
	// toolsListMin / toolsListMax bound the list column. A row is a mark and
	// a source's name, its state word and its note; past the ceiling the
	// columns are worth more to the tools and the fix beside it.
	toolsListMin = 30
	toolsListMax = 56
	// toolsMinPreview is the smallest preview the stacked layout leaves
	// standing: the title, and the first line of the account.
	toolsMinPreview = 3
	// toolsLabelWidth is the account's label column, wide enough for its
	// longest label and the gap after it.
	toolsLabelWidth = 10
)

// toolsGroup is which kind of source a row is, and the heading it is listed
// under. The order of the constants is the order of the list.
type toolsGroup int

const (
	// ToolsBuiltin is the toolset shhh registers itself, the rail's
	// `built-in` row. It heads the list on its own, under no heading.
	ToolsBuiltin toolsGroup = iota
	// ToolsServers are the MCP servers the session was told to reach, each
	// the rail's own row.
	ToolsServers
	// ToolsUnloaded are server definitions that could not be read at all,
	// so never became a server — the listing's diagnostics.
	ToolsUnloaded
	// ToolsLanguage are the language servers detected on PATH.
	ToolsLanguage
	// ToolsBinaries are the optional binaries the structural tools wrap.
	ToolsBinaries
	// ToolsWeb are the web tools: fetch, and search where a backend is set.
	ToolsWeb
)

// heading is the group rail a kind of source is listed under.
func (g toolsGroup) heading() string {
	switch g {
	case ToolsServers:
		return "mcp servers"
	case ToolsUnloaded:
		return "definitions not loaded"
	case ToolsLanguage:
		return "language servers"
	case ToolsBinaries:
		return "binaries on PATH"
	case ToolsWeb:
		return "web"
	}
	return ""
}

// toolsOffer is the one act a row can carry: the checkout's trust answer, on
// a server the checkout declared.
type toolsOffer int

const (
	// ToolsOfferNone is a row with nothing to answer.
	ToolsOfferNone toolsOffer = iota
	// ToolsOfferTrust trusts the checkout, which is what starts its servers
	// from the next session on.
	ToolsOfferTrust
	// ToolsOfferDistrust withdraws that answer.
	ToolsOfferDistrust
)

// label is what the key row calls the offer.
func (o toolsOffer) label() string {
	switch o {
	case ToolsOfferTrust:
		return "trust this checkout"
	case ToolsOfferDistrust:
		return "withdraw trust"
	}
	return ""
}

// ToolsSource is one place tools came from, already resolved to what the
// screen draws.
type ToolsSource struct {
	Group toolsGroup
	// Source is the row as the rail's TOOLS block draws it — the name, the
	// state and the note — and not a copy of its words made again, so a
	// server here and its row there are one reading.
	Source InspectorToolSource
	// Detail is what the source reaches: a server's command or url, a
	// language server's files, a binary's path.
	Detail string
	// Tools are the names it registered, as the model knows them.
	Tools []string
	// Consequence is what a source that is not up costs the session.
	Consequence string
	// Fix is what would move it, one line each.
	Fix []string
	// Offer is the act the row carries, and Ask the question the confirm
	// puts before it is carried out.
	Offer toolsOffer
	Ask   string
}

// ToolsScreen is `/mcp` and the TOOLS heading: a takeover in the chat, full
// width, owning the keyboard for as long as it is up.
type ToolsScreen struct {
	// The pointer is an index into Sources.
	listScreen[ToolsSource]
	// Sources are in group order — the order the list draws them in.
	Sources []ToolsSource
	// Said is what the last answer came to, in the words the command behind
	// it gives, drawn under the account of the row it was taken on. Moving
	// the pointer drops it.
	Said string
	// maxLines bounds the screen height. 0 is unbounded.
	maxLines int

	optAt map[int]int
	// confirm is the question standing between `[a]` and the answer it
	// records: none of the supporting screens changes the machine without
	// asking (docs/interface/surfaces.md#the-supporting-screens).
	confirm *Confirm
	asking  toolsResult
}

// toolsResult is an act the screen hands the host: the offer a confirmed
// `[a]` was about, and the row it was taken on. The zero value is nothing.
type toolsResult struct {
	Offer toolsOffer
	at    int
}

// Update is the screen's whole keyboard: it moves, it asks about a row's
// offer and hands the answer over, it shows its keys, and it leaves. It
// reports whether the screen is done and any act a confirm let through.
func (s *ToolsScreen) Update(msg tea.KeyPressMsg) (done bool, result toolsResult) {
	if s.confirm != nil {
		if answered, yes := confirmed(&s.confirm, msg); answered && yes {
			return false, s.asking
		}
		return false, toolsResult{}
	}
	pressed := msg.String()
	switch {
	case s.walked(pressed):
	case keys.Is(pressed, keys.Screen.Apply):
		// Only where the row carries an offer, and even there it asks first.
		if src := s.current(s.Sources); src != nil && src.Offer != ToolsOfferNone {
			s.asking = toolsResult{Offer: src.Offer, at: s.Focus}
			s.confirm = &Confirm{Prompt: sty.body.Render(src.Ask)}
		}
	case keys.Is(pressed, keys.Screen.List):
		s.keys = !s.keys
	case keys.Is(pressed, keys.Screen.Quit):
		return true, toolsResult{}
	}
	return false, toolsResult{}
}

// SetSize gives the screen the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (s *ToolsScreen) SetSize(_, height int) { s.maxLines = height }

// View renders the screen: the shared chrome, with the two panes in the rows
// it leaves.
func (s *ToolsScreen) View(width int) string { return s.view(width, s) }

// chrome is the header over the panes and the keys under them. The confirm
// borrows the foot row while it is up, as the doctor's does.
func (s *ToolsScreen) chrome(width int) screenChrome {
	field := s.footField()
	foot := s.footer(s.offers(width, field), s.keyList(), field)
	if s.confirm != nil {
		foot.taken = s.confirm.View(width)
	}
	return screenChrome{header: s.header(), foot: foot.rows(width), maxLines: s.maxLines}
}

// panes is the body, split the way every screen with a list and a preview
// splits it (screenpanes.go): on the left the sources under their headings.
func (s *ToolsScreen) panes() screenPanes {
	return screenPanes{
		stackAt: toolsStackWidth, listMin: toolsListMin,
		listMax: toolsListMax, minPreview: toolsMinPreview,
		list: func(width, budget int) []string {
			return s.listRows(s.Sources, "this session registered no tools", width, budget)
		},
		preview: s.previewRows,
	}
}

// previewRows is the right pane: the source under the pointer, what it
// reaches and brought, and for one that is not up what it costs and what
// would move it.
func (s *ToolsScreen) previewRows(width int) []string {
	src := s.current(s.Sources)
	if src == nil {
		return []string{sty.dim.Render(Clip("nothing selected", width))}
	}
	word := ToolSourceWord(src.Source.State)
	rows := []string{paneTitle(brightStyle().Render(src.Source.Name), sty.dim.Render(word), width), ""}
	state := word
	if src.Source.Note != "" {
		state += " · " + src.Source.Note
	}
	rows = append(rows, toolsField("state", state, width)...)
	if src.Detail != "" {
		rows = append(rows, toolsField("reaches", src.Detail, width)...)
	}
	if src.Consequence != "" {
		rows = append(rows, toolsField("costs", src.Consequence, width)...)
	}
	if len(src.Tools) > 0 {
		rows = append(rows, "", "  "+sty.status.Render(plural(len(src.Tools), "tool")))
		for _, line := range wrapPlain(strings.Join(src.Tools, ", "), max(width-4, 8)) {
			rows = append(rows, Clip("    "+sty.body.Render(line), width))
		}
	}
	if len(src.Fix) > 0 {
		// Wrapped rather than clipped: a server's reason is the one line the
		// reader opened the screen for, and its tail is usually the part that
		// names what is missing.
		rows = append(rows, "", "  "+sty.status.Render("what would move it"))
		for _, line := range src.Fix {
			// A line that fits is kept as written: the listing aligns its
			// comments with runs of spaces, and wrapping would fold them.
			parts := []string{line}
			if lipgloss.Width(line) > width-4 {
				parts = wrapPlain(line, max(width-4, 8))
			}
			for _, part := range parts {
				rows = append(rows, Clip("    "+sty.dim.Render(part), width))
			}
		}
	}
	if s.Said != "" {
		rows = append(rows, "")
		for _, line := range wrapPlain(s.Said, max(width-2, 8)) {
			rows = append(rows, Clip("  "+sty.body.Render(line), width))
		}
	}
	return rows
}

// toolsField is one entry of the account: a dim label in a fixed column and
// the value beside it, wrapped under itself rather than clipped, since a
// server's reason runs longer than a pane is wide.
func toolsField(label, value string, width int) []string {
	lead := "  " + sty.status.Render(fmt.Sprintf("%-*s", toolsLabelWidth, label))
	indent := strings.Repeat(" ", 2+toolsLabelWidth)
	var rows []string
	for i, part := range wrapPlain(value, max(width-len(indent), 8)) {
		if i == 0 {
			rows = append(rows, Clip(lead+sty.body.Render(part), width))
			continue
		}
		rows = append(rows, Clip(indent+sty.body.Render(part), width))
	}
	return rows
}

// toolsGlyph is the row's leading mark, the rail's glyph for the same state.
// It is plain rather than painted for the steps screen's reason, and the
// word beside it says the same thing (invariant 1).
func toolsGlyph(st ToolSourceState) string {
	switch st {
	case ToolSourceUp:
		return "✓"
	case ToolSourceBlocked:
		return "⚠"
	case ToolSourceOff:
		return "⊘"
	case ToolSourceStarting:
		return "▸"
	}
	return "✗"
}

// toolsTone is the weight a state word carries in the list: the rail's own
// reading, where up and off are quiet and a failure is what the eye lands on.
func toolsTone(st ToolSourceState) FieldTone {
	switch st {
	case ToolSourceUp, ToolSourceOff, ToolSourceStarting:
		return ToneQuiet
	case ToolSourceFailed:
		return ToneRisk
	}
	return ToneNeutral
}

// option is a source as the list draws it: the rail row's mark and name,
// its state word, and its note.
func (src ToolsSource) option() SelectOption {
	opt := SelectOption{Label: toolsGlyph(src.Source.State) + " " + src.Source.Name,
		Value: ToolSourceWord(src.Source.State), valueTone: toolsTone(src.Source.State)}
	if src.Source.Note != "" {
		opt.Detail = []DetailSpan{{Text: src.Source.Note, Tone: ToneQuiet}}
	}
	return opt
}

// header names the surface, how many servers it lists, and the TOOLS
// heading's own ratio — the built-in toolset and the servers, the sources the
// block draws — so the figure on the screen and the figure on the rail are
// one. A session with no servers has no block, and no ratio either.
func (s *ToolsScreen) header() screenHeader {
	h := screenHeader{left: []RailSegment{screenTitle("/mcp")}, keys: s.headerKeys(keys.Screen.List, keys.Screen.Quit)}
	servers, counted, up := 0, 0, 0
	for _, src := range s.Sources {
		if src.Group != ToolsBuiltin && src.Group != ToolsServers {
			continue
		}
		counted++
		if src.Group == ToolsServers {
			servers++
		}
		if src.Source.State == ToolSourceUp {
			up++
		}
	}
	if servers > 0 {
		h.left = append(h.left, screenField(plural(servers, "server")),
			screenField(fmt.Sprintf("%d of %d up", up, counted)))
	}
	return h
}

// offers is the key row: the pointer's keys, the row's offer where it has
// one, and the way out, and the last two alone where the field leaves no room
// for all of them. The offer is dropped rather than drawn grey off a row that
// has none: it is the one key here that changes the machine, and a grey
// `trust this checkout` beside the built-in toolset would say it could.
func (s *ToolsScreen) offers(width int, field string) []KeyOffer {
	var acts []KeyOffer
	if src := s.current(s.Sources); src != nil && src.Offer != ToolsOfferNone {
		acts = append(acts, keyOfferAs(keys.Screen.Apply, src.Offer.label()))
	}
	acts = append(acts, wayOut(backToPrompt))
	return offersBeside(keyOffer(keys.Screen.Move), acts, field, width)
}

// keyList is every key the screen has, for `[?]`.
func (s *ToolsScreen) keyList() []KeyOffer {
	return []KeyOffer{
		keyOfferAs(keys.Screen.Move, "move between sources"),
		keyOfferAs(keys.Screen.Apply, "trust this checkout, or withdraw it, on a server it declared — after confirming"),
		wayOut(backToPrompt),
		keyOfferAs(keys.Screen.Quit, backToPrompt),
	}
}

// footField annotates the key row with where the readings come from: the
// rail's own, so a row here and the TOOLS row there are one source.
func (s *ToolsScreen) footField() string {
	if len(s.Sources) == 0 {
		return ""
	}
	return "where this session's tools came from, as the rail reads it"
}

// FocusGroup puts the pointer on the first source of a group, and reports
// whether there was one — `/mcp` opens on the servers.
func (s *ToolsScreen) FocusGroup(g toolsGroup) bool {
	for i, src := range s.Sources {
		if src.Group == g {
			s.Focus = i
			return true
		}
	}
	return false
}

// sync rebuilds the list from Sources, with a heading over each group. It
// runs before every View because the host replaces Sources as it reads them
// again, and the pointer has to survive that.
func (s *ToolsScreen) sync() {
	s.clamp(len(s.Sources))
	s.optAt = make(map[int]int, len(s.Sources))
	opts := make([]SelectOption, 0, len(s.Sources)+5)
	group := ""
	for i, src := range s.Sources {
		if g := src.Group.heading(); g != group {
			group = g
			if g != "" {
				opts = append(opts, SelectOption{Label: g, Header: true})
			}
		}
		s.optAt[i] = len(opts)
		opts = append(opts, src.option())
	}
	// The count the window's marker states is of sources, not of options:
	// the headings are rows on the screen and not places tools came from.
	s.show(opts, len(s.Sources), s.optAt[s.Focus])
}

// walked walks the pointer through the sources, over the headings. What the
// last answer said belongs to the row it was taken on, so it goes when the
// pointer does.
func (s *ToolsScreen) walked(pressed string) bool {
	from := s.Focus
	if !s.moved(s.Sources, pressed, keys.Screen.Move) {
		return false
	}
	if s.Focus != from {
		s.Said = ""
	}
	return true
}
