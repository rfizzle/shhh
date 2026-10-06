package components

// The flakes screen (docs/interface/surfaces.md#the-supporting-screens):
// `/gate flakes`, every check that failed and then passed on its rerun in
// this checkout, with how many times it has. The row a turn's close draws
// says a check flaked; this is where a reader finds out whether it was the
// first time or the ninth, which is the difference between a machine that was
// busy and a check that needs fixing
// (docs/capabilities/testing.md#a-flake-is-counted-where-it-happened).
//
// A row is one check in one suite. The ledger is the host's; this is a
// renderer, and every field arrives already worded.

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// flakesStackWidth is the width below which the panes stack. The
	// preview is a handful of short lines and the command, so two columns
	// fit sooner than a screen whose preview is a transcript row.
	flakesStackWidth = 80
	// flakesListMin / flakesListMax bound the list column. A row is a
	// check's name, its suite and count, and when it last flaked.
	flakesListMin = 30
	flakesListMax = 52
	// flakesMinPreview is the smallest preview the stacked layout leaves
	// standing: the title and the count.
	flakesMinPreview = 3
)

// FlakesRow is one check's row in the checkout's ledger, already worded.
type FlakesRow struct {
	Check, Suite string
	// Command is the check's command as it last ran.
	Command string
	// Seen is how many times the check has flaked in this checkout.
	Seen int
	// LastSeen and FirstSeen are when, as the host words a moment — `2h
	// ago`.
	LastSeen, FirstSeen string
	// FirstExit is the code the failing run exited with the last time.
	FirstExit int
	// Session names the session the last flake happened in; empty where it
	// was not recorded.
	Session string
}

// FlakesScreen is `/gate flakes`: a takeover in the chat, full width, owning
// the keyboard for as long as it is up.
type FlakesScreen struct {
	// The pointer is an index into Rows.
	listScreen[FlakesRow]
	// Rows are the most recently flaked check first, the order the list
	// draws them in.
	Rows []FlakesRow
	// maxLines bounds the screen height. 0 is unbounded.
	maxLines int
}

// Update is the screen's whole keyboard: it moves, it shows its keys, and it
// leaves. It reports whether the screen is done.
func (s *FlakesScreen) Update(msg tea.KeyPressMsg) (done bool) {
	pressed := msg.String()
	switch {
	case s.moved(s.Rows, pressed, keys.Screen.Move):
	case keys.Is(pressed, keys.Screen.List):
		s.keys = !s.keys
	case keys.Is(pressed, keys.Screen.Quit):
		return true
	}
	return false
}

// SetSize gives the screen the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (s *FlakesScreen) SetSize(_, height int) { s.maxLines = height }

// View renders the screen: the shared chrome, with the two panes in the rows
// it leaves.
func (s *FlakesScreen) View(width int) string { return s.view(width, s) }

// chrome is the header over the panes and the keys under them.
func (s *FlakesScreen) chrome(width int) screenChrome {
	field := s.footField()
	return screenChrome{
		header:   s.header(),
		foot:     s.footer(s.offers(width, field), s.keyList(), field).rows(width),
		maxLines: s.maxLines,
	}
}

// panes is the body: on the left the checks, the latest flake first.
func (s *FlakesScreen) panes() screenPanes {
	return screenPanes{
		stackAt: flakesStackWidth, listMin: flakesListMin,
		listMax: flakesListMax, minPreview: flakesMinPreview,
		list: func(width, budget int) []string {
			return s.listRows(s.Rows, "no check has flaked in this checkout", width, budget)
		},
		preview: s.previewRows,
	}
}

// previewRows is the right pane: the check under the pointer, its count and
// when, and the command it runs, dim, because it is the check's label rather
// than news about it.
func (s *FlakesScreen) previewRows(width int) []string {
	r := s.current(s.Rows)
	if r == nil {
		return []string{sty.dim.Render(Clip("no check selected", width))}
	}
	rows := []string{paneTitle(brightStyle().Render(r.Check), sty.dim.Render(r.Suite), width), ""}
	line := func(text string) { rows = append(rows, Clip(text, width)) }
	line(sty.body.Render("flaked " + flakeTimes(r.Seen) + " in this checkout"))
	when := "last " + r.LastSeen
	if r.Seen > 1 && r.FirstSeen != "" {
		when += " · first " + r.FirstSeen
	}
	line(sty.dim.Render(when))
	line(sty.dim.Render(fmt.Sprintf("the failing run exited %d", r.FirstExit)))
	if r.Session != "" {
		line(sty.dim.Render("last in session " + r.Session))
	}
	rows = append(rows, "")
	for _, l := range wrapPlain(r.Command, max(width-2, 1)) {
		line(sty.dimmer.Render("  " + l))
	}
	return rows
}

// flakeTimes is a count of flakes in the words the flaked line uses.
func flakeTimes(n int) string {
	if n == 1 {
		return "1 time"
	}
	return fmt.Sprintf("%d times", n)
}

// header names the surface and what it counts.
func (s *FlakesScreen) header() screenHeader {
	h := screenHeader{left: []RailSegment{screenTitle("/gate flakes")}, keys: s.headerKeys(keys.Screen.List, keys.Screen.Quit)}
	if len(s.Rows) > 0 {
		total := 0
		for _, r := range s.Rows {
			total += r.Seen
		}
		checks := "1 check"
		if len(s.Rows) != 1 {
			checks = fmt.Sprintf("%d checks", len(s.Rows))
		}
		flakes := "1 flake"
		if total != 1 {
			flakes = fmt.Sprintf("%d flakes", total)
		}
		h.left = append(h.left, screenField(checks+" · "+flakes))
	}
	return h
}

// offers is the key row: the pointer's keys and the way out, and the way out
// alone where the field leaves no room for both.
func (s *FlakesScreen) offers(width int, field string) []KeyOffer {
	return offersBeside(keyOffer(keys.Screen.Move), []KeyOffer{wayOut(backToPrompt)}, field, width)
}

// keyList is every key the screen has, for `[?]`.
func (s *FlakesScreen) keyList() []KeyOffer {
	return []KeyOffer{
		keyOfferAs(keys.Screen.Move, "move between checks"),
		wayOut(backToPrompt),
		keyOfferAs(keys.Screen.Quit, backToPrompt),
	}
}

// footField says what a row counts: a check that failed and passed on its
// rerun, which the gate counted as passed.
func (s *FlakesScreen) footField() string {
	if len(s.Rows) == 0 {
		return ""
	}
	return "failed, then passed on the rerun"
}

// sync rebuilds the list from Rows. It runs before every View because the
// host may replace Rows, and the pointer has to survive that.
func (s *FlakesScreen) sync() {
	s.clamp(len(s.Rows))
	opts := make([]SelectOption, 0, len(s.Rows))
	for _, r := range s.Rows {
		opts = append(opts, SelectOption{Label: r.Check,
			Detail: []DetailSpan{{Text: r.Suite, Tone: ToneQuiet}, {Text: detailSep + flakeTimes(r.Seen), Tone: ToneNeutral}},
			Meta:   r.LastSeen, metaTone: ToneQuiet})
	}
	s.show(opts, 0, s.Focus)
}
