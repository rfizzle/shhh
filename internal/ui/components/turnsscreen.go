package components

// The turns screen (docs/interface/surfaces.md#the-supporting-screens):
// every turn the session has run, the table the rail's THIS TURN block holds
// only the present row of.
//
// A row is one turn — its number, how it ended, what it changed and what it
// cost. The preview is that turn's close block drawn by the close's own
// renderer, from the very figures the transcript's close row was drawn from,
// so the screen and the row cannot report one turn two ways. A turn still in
// flight has no close yet and is drawn from the rail's reading of it; a turn
// the session holds files for and no close — one from a sitting that has
// ended — is drawn as its number and its files, and says its figures were
// not kept rather than reporting zeros nobody measured
// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
// This is a renderer; the turns are the host's.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
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

// TurnsItem is one turn, already resolved to what the screen draws. Exactly
// one of Running and Close says what the turn came to; with neither, the
// turn is one whose figures were not kept.
type TurnsItem struct {
	// N is the turn's number, the one its close row and its review carry.
	N int64
	// Running is the turn in flight, as the rail's THIS TURN block reads it.
	Running *InspectorTurn
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
	// Turns are newest first, the order the list draws them in.
	Turns []TurnsItem
	// Focus is an index into Turns.
	Focus int
	// Subject is what the header counts — `6 turns` — and Tools the calls
	// they made, the field after it and the one that gives way first; Spend
	// is what the session has spent, the tally in front of the keys.
	Subject string
	Tools   string
	Spend   string
	// maxLines bounds the screen height. 0 is unbounded.
	maxLines int

	list Select
	keys bool
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
	case s.moved(pressed):
	case keys.Is(pressed, keys.Screen.List):
		s.keys = !s.keys
	case keys.Is(pressed, keys.Screen.Take):
		// Only for a turn with a changeset: a key that cannot act is not an
		// offer, and the footer draws it grey (invariant 5).
		if t := s.current(); t != nil && t.Reviewable {
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
func (s *TurnsScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	s.sync()
	return screenChrome{
		header:   s.header(),
		foot:     s.footer(width).rows(width),
		maxLines: s.maxLines,
	}.view(width, func(budget int) []string { return s.panes().rows(width, budget) })
}

// panes is the body, split the way every screen with a list and a preview
// splits it (screenpanes.go).
func (s *TurnsScreen) panes() screenPanes {
	return screenPanes{
		stackAt: turnsStackWidth, listMin: turnsListMin,
		listMax: turnsListMax, minPreview: turnsMinPreview,
		list:    s.listRows,
		preview: s.previewRows,
	}
}

// listRows is the left pane: the turns, newest first.
func (s *TurnsScreen) listRows(width, budget int) []string {
	if len(s.Turns) == 0 {
		return []string{sty.dim.Render(Clip("the session has run no turns", width))}
	}
	body, _ := s.list.visibleRows(cardWidthFor(width), budget, false)
	return body
}

// previewRows is the right pane: the turn under the pointer — its close as
// the transcript drew it, or what can be said in its place — and the files
// it changed.
func (s *TurnsScreen) previewRows(width int) []string {
	t := s.current()
	if t == nil {
		return []string{sty.dim.Render(Clip("no turn selected", width))}
	}
	word, _ := turnWord(*t)
	rows := []string{paneTitle(brightStyle().Render(fmt.Sprintf("turn %d", t.N)),
		sty.dim.Render(word), width), ""}
	switch {
	case t.Running != nil:
		rows = append(rows, runningRows(*t.Running, width)...)
	case t.Close != nil:
		rows = append(rows, strings.Split(t.Close.readOnly().View(width), "\n")...)
	default:
		// No close is held for the turn — it was never saved with a resumed
		// conversation, or the turn stopped at its round limit and closed
		// with the pause row — so the one thing this screen can say about it
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
// closed, ▸ for the one in flight, · for one whose figures were not kept. It
// is plain rather than painted for the steps screen's reason, and the word
// beside it says the same thing (invariant 1).
func turnGlyph(t TurnsItem) string {
	switch {
	case t.Running != nil:
		return "▸"
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
	h := screenHeader{left: []RailSegment{screenTitle("/turns")}, keys: s.headerKeys()}
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

// headerKeys is the pair the header ends with, as on every screen of the
// family (docs/interface/surfaces.md#the-supporting-screens).
func (s *TurnsScreen) headerKeys() string {
	list := keys.Bracket(keys.Screen.List) + " " + keys.Words(keys.Screen.List)
	if s.keys {
		list = keys.Bracket(keys.Screen.List) + " hide the keys"
	}
	return list + " · " + words(keys.Screen.Quit, "back")
}

// footer is the keys the screen offers and the field that annotates them.
func (s *TurnsScreen) footer(width int) keyFooter {
	field := s.footField()
	return keyFooter{offers: s.offers(width, field), register: s.keyList(),
		showing: s.keys, field: field}
}

// offers is the key row: the pointer's keys, the review, and the way out,
// and the last two alone where the field leaves no room for all three. The
// review names the turn it opens, and a turn with nothing to review draws it
// grey rather than dropping it, so the row does not change shape under a
// pointer walking the list.
func (s *TurnsScreen) offers(width int, field string) []KeyOffer {
	var acts []KeyOffer
	if t := s.current(); t != nil {
		review := keyOfferAs(keys.Screen.Take, fmt.Sprintf("review turn %d", t.N))
		review.inert = !t.Reviewable
		acts = append(acts, review)
	}
	acts = append(acts, wayOut(backToPrompt))
	full := append([]KeyOffer{keyOffer(keys.Screen.Move)}, acts...)
	if field == "" || fitsBeside(full, field, width) {
		return full
	}
	return acts
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
	s.Focus = min(max(s.Focus, 0), max(len(s.Turns)-1, 0))
	opts := make([]SelectOption, 0, len(s.Turns))
	for _, t := range s.Turns {
		opt := SelectOption{Label: fmt.Sprintf("%s turn %d", turnGlyph(t), t.N), Detail: turnDetail(t)}
		opt.Value, opt.valueTone = turnWord(t)
		if t.Close != nil && t.Close.Spend != "" {
			opt.Meta, opt.metaTone = t.Close.Spend, ToneQuiet
		}
		opts = append(opts, opt)
	}
	s.list.Options = opts
	s.list.Unnumbered = true
	s.list.Focus = s.Focus
}

// moved walks the pointer between turns.
func (s *TurnsScreen) moved(pressed string) bool {
	if len(s.Turns) == 0 {
		return false
	}
	l := List[TurnsItem]{Items: s.Turns, Focus: s.Focus}
	if !l.Move(pressed, keys.Screen.Move) {
		return false
	}
	s.Focus = l.Focus
	return true
}

// current is the turn under the pointer, or nil for an empty list.
func (s *TurnsScreen) current() *TurnsItem {
	if s.Focus < 0 || s.Focus >= len(s.Turns) {
		return nil
	}
	return &s.Turns[s.Focus]
}
