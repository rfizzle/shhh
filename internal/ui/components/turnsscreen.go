package components

// The turns screen (docs/interface/surfaces.md#the-supporting-screens):
// every turn the session has run, the table the rail's THIS TURN block holds
// only the present row of.
//
// A row is one turn — its number, how it ended, what it changed and what it
// cost. The preview is that turn's close block drawn by the close's own
// renderer, from the very figures the transcript's close row was drawn from,
// so the screen and the row cannot report one turn two ways. A turn still in
// flight has no close yet and is drawn from the rail's reading of it; one
// that stopped at its round limit has no close either, and is drawn from the
// figures its pause row kept; a turn the session holds files for and no
// close — one from a sitting that has ended — is drawn as its number and its
// files, and says its figures were not kept rather than reporting zeros
// nobody measured
// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
// This is a renderer; the turns are the host's.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// turnsStackWidth is the width below which the panes stack. The preview
	// is a close block, which wants the transcript's grid and its note
	// column, so two columns want as much room as the readings screen's.
	turnsStackWidth = 96
	// turnsListMin / turnsListMax bound the list column. A row is a mark and
	// a number, the ending's word, what the turn wrote and what it cost, and
	// past the ceiling the columns are worth more to the close beside it.
	turnsListMin = 30
	turnsListMax = 56
	// turnsMinPreview is the smallest preview the stacked layout leaves
	// standing: the title, and the close's first row.
	turnsMinPreview = 3
)

// noFiguresKept is what a turn the session holds no close for says, on the
// list and in the preview.
const noFiguresKept = "no figures kept"

// TurnsFile is one path a turn changed, as the session's records hold it.
type TurnsFile struct {
	Path           string
	Added, Removed int
}

// TurnsPause is a turn that stopped at its round limit and was never given
// more: it has no close, because the pause row stands in for one
// (docs/interface/surfaces.md#the-recovery-row), and these are that row's
// figures — the rounds it used and what the turn had cost when it stopped.
type TurnsPause struct {
	// Used and Limit are the round counter the pause row states.
	Used, Limit int
	// Elapsed is the turn's wall time when it stopped, pre-formatted by
	// FormatElapsed, and Spend its cost, or its token count where the
	// pricing table did not know the model, the way a close states both.
	Elapsed string
	Spend   string
}

// TurnsItem is one turn, already resolved to what the screen draws. Exactly
// one of Running, Paused and Close says what the turn came to; with none,
// the turn is one whose figures were not kept.
type TurnsItem struct {
	// N is the turn's number, the one its close row and its review carry.
	N int64
	// Running is the turn in flight, as the rail's THIS TURN block reads it.
	Running *InspectorTurn
	// Paused is the turn that stopped at its round limit, as its pause row
	// left it.
	Paused *TurnsPause
	// Close is the block the turn closed with — the transcript row's own
	// figures, not a copy of them.
	Close *TurnClose
	// Files are the paths the session's records hold for the turn, and
	// Added/Removed their totals. Empty for a turn that changed nothing.
	Files          []TurnsFile
	Added, Removed int
	// Reviewable says the turn has a changeset to review: the one question
	// that decides whether `[enter]` is an offer.
	Reviewable bool
}

// TurnsScreen is `/turns`: a takeover in the chat, full width, owning the
// keyboard for as long as it is up.
type TurnsScreen struct {
	// The pointer is an index into Turns.
	listScreen[TurnsItem]
	// Turns are newest first, the order the list draws them in.
	Turns []TurnsItem
	// Subject is what the header counts — `6 turns` — and Tools the calls
	// they made, the field after it and the one that gives way first; Spend
	// is what the session has spent, the tally in front of the keys.
	Subject string
	Tools   string
	Spend   string
	// maxLines bounds the screen height. 0 is unbounded.
	maxLines int
}

// turnsResult is what the screen leaves with: nothing, or the turn whose
// review the reader asked for.
type turnsResult struct {
	Review int64
}

// Update is the screen's whole keyboard: it moves, it shows its keys, it asks
// for a turn's review, and it leaves. It reports whether the screen is done
// and, where it is done by asking for a review, which turn.
func (s *TurnsScreen) Update(msg tea.KeyPressMsg) (done bool, result turnsResult) {
	pressed := msg.String()
	switch {
	case s.moved(s.Turns, pressed, keys.Screen.Move):
	case keys.Is(pressed, keys.Screen.List):
		s.keys = !s.keys
	case keys.Is(pressed, keys.Screen.Take):
		// Only for a turn with a changeset: a key that cannot act is not an
		// offer, and the footer draws it grey (invariant 5).
		if t := s.current(s.Turns); t != nil && t.Reviewable {
			return true, turnsResult{Review: t.N}
		}
	case keys.Is(pressed, keys.Screen.Quit):
		return true, turnsResult{}
	}
	return false, turnsResult{}
}

// SetSize gives the screen the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (s *TurnsScreen) SetSize(_, height int) { s.maxLines = height }

// View renders the screen: the shared chrome, with the two panes in the rows
// it leaves.
func (s *TurnsScreen) View(width int) string { return s.view(width, s) }

// chrome is the header over the panes and the keys under them.
func (s *TurnsScreen) chrome(width int) screenChrome {
	field := s.footField()
	return screenChrome{
		header:   s.header(),
		foot:     s.footer(s.offers(width, field), s.keyList(), field).rows(width),
		maxLines: s.maxLines,
	}
}

// panes is the body, split the way every screen with a list and a preview
// splits it (screenpanes.go): on the left the turns, newest first.
func (s *TurnsScreen) panes() screenPanes {
	return screenPanes{
		stackAt: turnsStackWidth, listMin: turnsListMin,
		listMax: turnsListMax, minPreview: turnsMinPreview,
		list: func(width, budget int) []string {
			return s.listRows(s.Turns, "the session has run no turns", width, budget)
		},
		preview: s.previewRows,
	}
}

// previewRows is the right pane: the turn under the pointer — its close as
// the transcript drew it, or what can be said in its place — and the files
// it changed.
func (s *TurnsScreen) previewRows(width int) []string {
	t := s.current(s.Turns)
	if t == nil {
		return []string{sty.dim.Render(Clip("no turn selected", width))}
	}
	word, _ := turnWord(*t)
	rows := []string{paneTitle(brightStyle().Render(fmt.Sprintf("turn %d", t.N)),
		sty.dim.Render(word), width), ""}
	switch {
	case t.Running != nil:
		rows = append(rows, runningRows(*t.Running, width)...)
	case t.Paused != nil:
		rows = append(rows, pausedRows(*t.Paused, width)...)
	case t.Close != nil:
		rows = append(rows, strings.Split(t.Close.readOnly().View(width), "\n")...)
	default:
		// No close is held for the turn — it was never saved with a resumed
		// conversation — so the one thing this screen can say about it
		// beyond its files is that.
		rows = append(rows,
			closeLine(closeLead("", sty.dim.Render("·")), sty.body.Render(noFiguresKept), "", width),
			closeLine(closeLead("", " "), sty.dimmer.Render("the session kept its files and not its close"), "", width))
	}
	if len(t.Files) > 0 {
		rows = append(rows, "")
		const label = "files "
		for i, f := range t.Files {
			lead := strings.Repeat(" ", len(label))
			if i == 0 {
				lead = sty.status.Render(label)
			}
			stat := " " + DiffStat(f.Added, f.Removed)
			room := max(width-len(label)-2-len(fmt.Sprintf(" +%d −%d", f.Added, f.Removed)), 1)
			rows = append(rows, "  "+lead+sty.body.Render(Clip(f.Path, room))+stat)
		}
	}
	return rows
}

// runningRows is a turn in flight in the rail's own words: the step it is
// on, its tools, and what it has written so far — the THIS TURN block's
// reading, drawn on the close's grid because this is where its close will go.
func runningRows(r InspectorTurn, width int) []string {
	var stats []string
	switch {
	case r.Steps > 0:
		stats = append(stats, fmt.Sprintf("step %d of %d", r.Step, r.Steps))
	case r.Step > 0:
		stats = append(stats, fmt.Sprintf("step %d", r.Step))
	}
	if r.Tools > 0 {
		stats = append(stats, plural(r.Tools, "tool"))
	}
	text := sty.body.Render("Running")
	if len(stats) > 0 {
		text += sty.dim.Render(" · " + strings.Join(stats, " · "))
	}
	rows := []string{closeLine(closeLead("", sty.info.Render("▸")), text, "", width)}
	files := sty.dim.Render(plural(r.Files, "file") + " this turn")
	if r.Files > 0 {
		files += " " + DiffStat(r.Added, r.Removed)
	}
	return append(rows, closeLine(closeLead(sty.accent.Render("▎"), sty.accent.Render("✎")), files, "", width))
}

// pausedRows is a turn stopped at its round limit, on the close's grid: the
// pause row's glyph and its counter, then the turn's wall time and cost as a
// close would state them, and a line saying why there is no close — the
// pause row stands in for it. Where the figures do not fit beside the words
// they carry on at the words' column, a whole figure at a time: a cost cut
// between its number and its unit is not a figure on either line.
func pausedRows(p TurnsPause, width int) []string {
	stats := []string{fmt.Sprintf("%d of %d rounds used", p.Used, p.Limit)}
	for _, f := range []string{p.Elapsed, p.Spend} {
		if f != "" {
			stats = append(stats, f)
		}
	}
	under := closeLead("", " ")
	sep := " · "
	line := closeLead("", sty.accent.Render("⚠")) + sty.body.Render("Paused at its round limit")
	var rows []string
	for _, f := range stats {
		if lipgloss.Width(line)+lipgloss.Width(sep+f) > width {
			rows = append(rows, strings.TrimRight(Clip(line, width), " "))
			line = under + sty.dim.Render(f)
			continue
		}
		line += sty.dim.Render(sep + f)
	}
	rows = append(rows, strings.TrimRight(Clip(line, width), " "))
	return append(rows, closeLine(under, sty.dimmer.Render("its pause row stands in for the close"), "", width))
}

// readOnly is the block as a preview draws it: the same figures with none of
// the row's offers, because the keys a close row offers are answered by
// reading mode on the transcript and this screen answers none of them.
func (c TurnClose) readOnly() TurnClose {
	c.KeysWaiting, c.Handover = false, ""
	if c.Changes != nil {
		ch := *c.Changes
		ch.Keys, ch.Back = nil, ""
		c.Changes = &ch
	}
	if c.Checks != nil {
		ck := *c.Checks
		ck.Again = ""
		c.Checks = &ck
	}
	return c
}

// turnWord is how a turn stands, in the word the list and the preview both
// say, and the tone the list reads it in.
func turnWord(t TurnsItem) (string, FieldTone) {
	switch {
	case t.Running != nil:
		return "running", ToneOpen
	case t.Paused != nil:
		return "paused", ToneOpen
	case t.Close == nil:
		return noFiguresKept, ToneQuiet
	}
	switch t.Close.State {
	case TurnCancelled:
		return "cancelled", ToneQuiet
	case TurnFailed:
		return "failed", ToneRisk
	}
	return "done", ToneSafe
}

// turnGlyph is the row's leading mark: the close's own glyph for a turn that
// closed, ▸ for the one in flight, the pause row's ⚠ for one stopped at its
// round limit, · for one whose figures were not kept. It
// is plain rather than painted for the steps screen's reason, and the word
// beside it says the same thing (invariant 1).
func turnGlyph(t TurnsItem) string {
	switch {
	case t.Running != nil:
		return "▸"
	case t.Paused != nil:
		return "⚠"
	case t.Close == nil:
		return "·"
	}
	switch t.Close.State {
	case TurnCancelled:
		return "⊘"
	case TurnFailed:
		return "✗"
	}
	return "✓"
}

// turnDetail is what the turn wrote, in the rewind picker's own spelling of
// it: the mutation mark, the file count and the lines. A turn that wrote
// nothing after running something that could have says so; one that only
// read says nothing, as its close does.
func turnDetail(t TurnsItem) []DetailSpan {
	files, added, removed := len(t.Files), t.Added, t.Removed
	if t.Running != nil && files == 0 {
		files, added, removed = t.Running.Files, t.Running.Added, t.Running.Removed
	}
	if files == 0 && t.Close != nil && t.Close.Changes != nil {
		files, added, removed = t.Close.Changes.Files, t.Close.Changes.Added, t.Close.Changes.Removed
	}
	if files > 0 {
		spans := []DetailSpan{
			{Text: "▎", Tone: ToneOpen},
			{Text: plural(files, "file") + " ", Tone: ToneQuiet},
		}
		return append(spans, DiffStatSpans(added, removed)...)
	}
	if t.Close != nil && t.Close.WroteNothing {
		return []DetailSpan{{Text: wroteNothing, Tone: ToneQuiet}}
	}
	return nil
}

// header names the surface, what it counts and what the session has spent.
func (s *TurnsScreen) header() screenHeader {
	h := screenHeader{left: []RailSegment{screenTitle("/turns")}, keys: s.headerKeys(keys.Screen.List, keys.Screen.Quit)}
	for _, f := range []string{s.Subject, s.Tools} {
		if f != "" {
			h.left = append(h.left, screenField(f))
		}
	}
	if s.Spend != "" {
		h.tally = sty.dim.Render(s.Spend)
	}
	return h
}

// offers is the key row: the pointer's keys, the review, and the way out,
// and the last two alone where the field leaves no room for all three. The
// review names the turn it opens, and a turn with nothing to review draws it
// grey rather than dropping it, so the row does not change shape under a
// pointer walking the list.
func (s *TurnsScreen) offers(width int, field string) []KeyOffer {
	var acts []KeyOffer
	if t := s.current(s.Turns); t != nil {
		review := keyOfferAs(keys.Screen.Take, fmt.Sprintf("review turn %d", t.N))
		review.inert = !t.Reviewable
		acts = append(acts, review)
	}
	acts = append(acts, wayOut(backToPrompt))
	return offersBeside(keyOffer(keys.Screen.Move), acts, field, width)
}

// keyList is every key the screen has, for `[?]`.
func (s *TurnsScreen) keyList() []KeyOffer {
	return []KeyOffer{
		keyOfferAs(keys.Screen.Move, "move between turns"),
		keyOfferAs(keys.Screen.Take, "review the turn's changes, where it made any"),
		wayOut(backToPrompt),
		keyOfferAs(keys.Screen.Quit, backToPrompt),
	}
}

// footField annotates the key row with where the figures come from: each
// turn's own close, so a row here and the row in the transcript are one
// reading.
func (s *TurnsScreen) footField() string {
	if len(s.Turns) == 0 {
		return ""
	}
	return "each turn as its close row reads it"
}

// sync rebuilds the list from Turns. It runs before every View because the
// host may replace Turns, and the pointer has to survive that.
func (s *TurnsScreen) sync() {
	s.clamp(len(s.Turns))
	opts := make([]SelectOption, 0, len(s.Turns))
	for _, t := range s.Turns {
		opt := SelectOption{Label: fmt.Sprintf("%s turn %d", turnGlyph(t), t.N), Detail: turnDetail(t)}
		opt.Value, opt.valueTone = turnWord(t)
		switch {
		case t.Close != nil && t.Close.Spend != "":
			opt.Meta, opt.metaTone = t.Close.Spend, ToneQuiet
		case t.Paused != nil && t.Paused.Spend != "":
			opt.Meta, opt.metaTone = t.Paused.Spend, ToneQuiet
		}
		opts = append(opts, opt)
	}
	s.show(opts, 0, s.Focus)
}
