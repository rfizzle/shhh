package components

// The patterns screen (docs/interface/surfaces.md#the-supporting-screens):
// `/patterns`, what this checkout's sessions kept doing, each offered as the
// one thing that would stop it — a memory, an allowlist line or a skill
// (docs/capabilities/sessions-and-memory.md#memory-is-what-shhh-knows-about-your-project).
// The screen writes nothing: enter opens the proposal's card, and the card is
// where it is answered.
//
// A row is one proposal. What is proposed is the host's; this is a renderer,
// and every field arrives already worded.

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// patternsStackWidth is the width below which the panes stack: a row is
	// a kind word and a path or a command, and the preview a few short lines.
	patternsStackWidth = 80
	// patternsListMin / patternsListMax bound the list column.
	patternsListMin = 30
	patternsListMax = 56
	// patternsMinPreview is the smallest preview the stacked layout leaves
	// standing: the pattern and what it would become.
	patternsMinPreview = 3
)

// PatternsRow is one proposal, already worded.
type PatternsRow struct {
	// Kind is the word for what it would become: memory, allowlist, skill.
	Kind string
	// Pattern is what repeated, as the code names it — a path, a command,
	// a run of commands, a suite.
	Pattern string
	// Sessions is how many sessions it happened in.
	Sessions int
	// What is what happened, in a few words: `read`, `asked about and
	// allowed`, `failed before it passed`.
	What string
	// Becomes says what answering yes would write, and where.
	Becomes string
}

// PatternsScreen is `/patterns`: a takeover in the chat, full width, owning
// the keyboard for as long as it is up.
type PatternsScreen struct {
	// The pointer is an index into Rows.
	listScreen[PatternsRow]
	Rows []PatternsRow
	// Busy is what the screen is waiting on, drawn where the footer's field
	// goes — the proposal under the pointer being worded. Empty is nothing.
	Busy string
	// maxLines bounds the screen height. 0 is unbounded.
	maxLines int
}

// Update is the screen's whole keyboard: it moves, it takes the row under
// the pointer, it shows its keys and it leaves. It reports whether the screen
// is done, and whether the row under the pointer was taken.
func (s *PatternsScreen) Update(msg tea.KeyPressMsg) (done, take bool) {
	pressed := msg.String()
	switch {
	case s.moved(s.Rows, pressed, keys.Screen.Move):
	case keys.Is(pressed, keys.Screen.Take):
		return false, s.current(s.Rows) != nil
	case keys.Is(pressed, keys.Screen.List):
		s.keys = !s.keys
	case keys.Is(pressed, keys.Screen.Quit):
		return true, false
	}
	return false, false
}

// Current is the index of the row under the pointer, or -1 for none.
func (s *PatternsScreen) Current() int {
	if s.current(s.Rows) == nil {
		return -1
	}
	return s.Focus
}

// SetSize gives the screen the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (s *PatternsScreen) SetSize(_, height int) { s.maxLines = height }

// View renders the screen.
func (s *PatternsScreen) View(width int) string { return s.view(width, s) }

func (s *PatternsScreen) chrome(width int) screenChrome {
	field := s.footField()
	return screenChrome{
		header:   s.header(),
		foot:     s.footer(s.offers(width, field), s.keyList(), field).rows(width),
		maxLines: s.maxLines,
	}
}

func (s *PatternsScreen) panes() screenPanes {
	return screenPanes{
		stackAt: patternsStackWidth, listMin: patternsListMin,
		listMax: patternsListMax, minPreview: patternsMinPreview,
		list: func(width, budget int) []string {
			return s.listRows(s.Rows, "nothing repeated often enough to propose", width, budget)
		},
		preview: s.previewRows,
	}
}

// previewRows is the right pane: the pattern under the pointer, what it was,
// and what saying yes to it would write.
func (s *PatternsScreen) previewRows(width int) []string {
	r := s.current(s.Rows)
	if r == nil {
		return []string{sty.dim.Render(Clip("no proposal selected", width))}
	}
	rows := []string{paneTitle(brightStyle().Render(r.Kind), sty.dim.Render(patternSessions(r.Sessions)), width), ""}
	for _, l := range wrapPlain(r.Pattern, max(width, 1)) {
		rows = append(rows, Clip(sty.body.Render(l), width))
	}
	if r.What != "" {
		rows = append(rows, Clip(sty.dim.Render(r.What+" in "+patternSessions(r.Sessions)), width))
	}
	rows = append(rows, "")
	for _, l := range wrapPlain(r.Becomes, max(width, 1)) {
		rows = append(rows, Clip(sty.dim.Render(l), width))
	}
	return rows
}

func patternSessions(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}

func (s *PatternsScreen) header() screenHeader {
	h := screenHeader{left: []RailSegment{screenTitle("/patterns")}, keys: s.headerKeys(keys.Screen.List, keys.Screen.Quit)}
	if n := len(s.Rows); n > 0 {
		word := "proposals"
		if n == 1 {
			word = "proposal"
		}
		h.left = append(h.left, screenField(fmt.Sprintf("%d %s", n, word)))
	}
	return h
}

func (s *PatternsScreen) offers(width int, field string) []KeyOffer {
	acts := []KeyOffer{wayOut(backToPrompt)}
	if len(s.Rows) > 0 {
		acts = append([]KeyOffer{keyOfferAs(keys.Screen.Take, "open its card")}, acts...)
	}
	return offersBeside(keyOffer(keys.Screen.Move), acts, field, width)
}

func (s *PatternsScreen) keyList() []KeyOffer {
	return []KeyOffer{
		keyOfferAs(keys.Screen.Move, "move between proposals"),
		keyOfferAs(keys.Screen.Take, "open the proposal's card; nothing is written until you say yes there"),
		wayOut(backToPrompt),
		keyOfferAs(keys.Screen.Quit, backToPrompt),
	}
}

// footField says what the rows are, or what the screen is waiting on.
func (s *PatternsScreen) footField() string {
	if s.Busy != "" {
		return s.Busy
	}
	if len(s.Rows) == 0 {
		return ""
	}
	return "from what this checkout's sessions kept doing"
}

func (s *PatternsScreen) sync() {
	s.clamp(len(s.Rows))
	opts := make([]SelectOption, 0, len(s.Rows))
	for _, r := range s.Rows {
		opts = append(opts, SelectOption{Label: r.Kind,
			Detail: []DetailSpan{{Text: r.Pattern, Tone: ToneNeutral}},
			Meta:   patternSessions(r.Sessions), metaTone: ToneQuiet})
	}
	s.show(opts, 0, s.Focus)
}
