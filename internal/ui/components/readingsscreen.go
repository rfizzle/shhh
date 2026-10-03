package components

// The readings screen (docs/interface/surfaces.md#the-supporting-screens):
// every reading the session has taken of its own run, the history the rail's
// SUMMARY block holds only the latest line of.
//
// A row is one reading — the round it was taken at, its verdict in the rail's
// own marks, and whether it steered the turn and whether that steer was taken
// back. The preview is the reading whole, drawn by the host through the
// transcript's opened summary row, so a reading reads here exactly as it
// reads in the feed; this is a renderer, and the history is the host's.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// readingsStackWidth is the width below which the panes stack. The
	// preview is an opened activity row, which wants the transcript's grid,
	// so two columns want as much room as the steps screen's.
	readingsStackWidth = 96
	// readingsListMin / readingsListMax bound the list column. A row is a
	// mark, a round and a verdict, the steer's word and the turn, and past
	// the ceiling the columns are worth more to the reading beside it.
	readingsListMin = 30
	readingsListMax = 48
	// readingsMinPreview is the smallest preview the stacked layout leaves
	// standing: the title, and the reading's first line.
	readingsMinPreview = 3
)

// ReadingsItem is one reading, already resolved to what the list draws.
type ReadingsItem struct {
	// Round is the round the reading was taken at; Turn the turn it was
	// taken in, since a round only means something inside its turn.
	Round int
	Turn  int
	Tone  SummaryTone
	// Steered says the reading earned a steer that was delivered; Withdrawn
	// that the reader then took it back.
	Steered   bool
	Withdrawn bool
}

// ReadingsScreen is `/readings`: a takeover in the chat, full width, owning
// the keyboard for as long as it is up.
type ReadingsScreen struct {
	// Readings are newest first, the order the list draws them in.
	Readings []ReadingsItem
	// Row is the reading at an index as the transcript's opened summary row,
	// built at the width it will be drawn at so its prose wraps there. It is
	// the host's because the row's renderer is.
	Row func(index, width int) ActivityRow
	// focus is an index into Readings.
	focus int
	// Subject is what the header counts — `5 readings`; Cost is what they
	// have cost the session, the tally that goes first when the row is short.
	Subject string
	Cost    string
	// Dropped is how many of the oldest readings are no longer kept, and Kept
	// the bound they were dropped at. Zero drops say nothing.
	Dropped, Kept int
	// maxLines bounds the screen height. 0 is unbounded.
	maxLines int

	list Select
	keys bool
}

// Update is the screen's whole keyboard: it moves, it shows its keys, and it
// leaves. It reports whether the screen is done.
func (s *ReadingsScreen) Update(msg tea.KeyPressMsg) (done bool) {
	pressed := msg.String()
	switch {
	case s.moved(pressed):
	case keys.Is(pressed, keys.Screen.List):
		s.keys = !s.keys
	case keys.Is(pressed, keys.Screen.Quit):
		return true
	}
	return false
}

// SetSize gives the screen the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (s *ReadingsScreen) SetSize(_, height int) { s.maxLines = height }

// View renders the screen: the shared chrome, with the two panes in the rows
// it leaves.
func (s *ReadingsScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	s.sync()
	return screenChrome{
		header:   s.header(),
		head:     s.droppedRows(width),
		foot:     s.footer(width).rows(width),
		maxLines: s.maxLines,
	}.view(width, func(budget int) []string { return s.panes().rows(width, budget) })
}

// panes is the body, split the way every screen with a list and a preview
// splits it (screenpanes.go).
func (s *ReadingsScreen) panes() screenPanes {
	return screenPanes{
		stackAt: readingsStackWidth, listMin: readingsListMin,
		listMax: readingsListMax, minPreview: readingsMinPreview,
		list:    s.listRows,
		preview: s.previewRows,
	}
}

// droppedRows is the line pinned under the header once the oldest readings
// have gone: a history that silently began part-way through would read as a
// session that took its first reading late.
func (s *ReadingsScreen) droppedRows(width int) []string {
	if s.Dropped <= 0 {
		return nil
	}
	noun := "readings"
	if s.Dropped == 1 {
		noun = "reading"
	}
	line := fmt.Sprintf("%d oldest %s let go · the screen keeps the last %d", s.Dropped, noun, s.Kept)
	return []string{sty.dim.Render(Clip(line, width))}
}

// listRows is the left pane: the readings, newest first.
func (s *ReadingsScreen) listRows(width, budget int) []string {
	if len(s.Readings) == 0 {
		return []string{sty.dim.Render(Clip("the session has taken no readings", width))}
	}
	body, _ := s.list.visibleRows(cardWidthFor(width), budget, false)
	return body
}

// previewRows is the right pane: the reading under the pointer, whole.
func (s *ReadingsScreen) previewRows(width int) []string {
	r := s.current()
	if r == nil {
		return []string{sty.dim.Render(Clip("no reading selected", width))}
	}
	rows := []string{paneTitle(brightStyle().Render(readingLabel(*r)),
		sty.dim.Render(fmt.Sprintf("turn %d", r.Turn)), width), ""}
	if s.Row != nil {
		rows = append(rows, strings.Split(s.Row(s.focus, width).View(width), "\n")...)
	}
	if r.Steered {
		// What became of the reading is not in the row, which is the reading
		// as it landed; it is said once, under it.
		said := "  it steered the turn"
		if r.Withdrawn {
			said += ", and the steer was taken back"
		}
		rows = append(rows, "", sty.dimmer.Render(Clip(said, width)))
	}
	return rows
}

// readingLabel is a reading as its row names it: the round and the verdict,
// `r 14 · off target`.
func readingLabel(r ReadingsItem) string {
	return fmt.Sprintf("r %d · %s", r.Round, SummaryWord(r.Tone))
}

// readingOutcome is what became of a reading, in a word: `steered`,
// `withdrawn` once the steer was taken back, nothing for one that interrupted
// nobody.
func readingOutcome(r ReadingsItem) (string, FieldTone) {
	switch {
	case r.Withdrawn:
		return "withdrawn", ToneQuiet
	case r.Steered:
		return "steered", ToneOpen
	}
	return "", ToneNeutral
}

// header names the surface, what it counts and what the counting cost.
func (s *ReadingsScreen) header() screenHeader {
	h := screenHeader{left: []RailSegment{screenTitle("/readings")}, keys: s.headerKeys()}
	if s.Subject != "" {
		h.left = append(h.left, screenField(s.Subject))
	}
	if s.Cost != "" {
		h.tally = sty.dim.Render(s.Cost)
	}
	return h
}

// headerKeys is the pair the header ends with, as on every screen of the
// family (docs/interface/surfaces.md#the-supporting-screens).
func (s *ReadingsScreen) headerKeys() string {
	list := keys.Bracket(keys.Screen.List) + " " + keys.Words(keys.Screen.List)
	if s.keys {
		list = keys.Bracket(keys.Screen.List) + " hide the keys"
	}
	return list + " · " + words(keys.Screen.Quit, "back")
}

// footer is the keys the screen offers and the field that annotates them.
func (s *ReadingsScreen) footer(width int) keyFooter {
	field := s.footField()
	return keyFooter{offers: s.offers(width, field), register: s.keyList(),
		showing: s.keys, field: field}
}

// offers is the key row: the pointer's keys and the way out, and the way out
// alone where the field leaves no room for both.
func (s *ReadingsScreen) offers(width int, field string) []KeyOffer {
	acts := []KeyOffer{wayOut(backToPrompt)}
	full := append([]KeyOffer{keyOffer(keys.Screen.Move)}, acts...)
	if field == "" || fitsBeside(full, field, width) {
		return full
	}
	return acts
}

// keyList is every key the screen has, for `[?]`.
func (s *ReadingsScreen) keyList() []KeyOffer {
	return []KeyOffer{
		keyOfferAs(keys.Screen.Move, "move between readings"),
		wayOut(backToPrompt),
		keyOfferAs(keys.Screen.Quit, backToPrompt),
	}
}

// footField annotates the key row with what the readings are: a cheap
// model's account of a digest of the run, not the agent's own word
// (docs/capabilities/coding-agent.md#two-failures-two-interruptions).
func (s *ReadingsScreen) footField() string {
	if len(s.Readings) == 0 {
		return ""
	}
	return "a reader's account of the run, newest first"
}

// sync rebuilds the list from Readings. It runs before every View because the
// host may replace Readings, and the pointer has to survive that.
func (s *ReadingsScreen) sync() {
	s.focus = min(max(s.focus, 0), max(len(s.Readings)-1, 0))
	opts := make([]SelectOption, 0, len(s.Readings))
	for _, r := range s.Readings {
		// The rail's own mark leads, plain rather than painted for the steps
		// screen's reason: the label runs through the card's emphasis, and
		// the verdict's word beside it says the same thing.
		opt := SelectOption{Label: SummaryGlyph(r.Tone) + " " + readingLabel(r),
			Meta: fmt.Sprintf("turn %d", r.Turn), metaTone: ToneQuiet}
		opt.Value, opt.valueTone = readingOutcome(r)
		opts = append(opts, opt)
	}
	s.list.Options = opts
	s.list.Unnumbered = true
	s.list.Focus = s.focus
}

// moved walks the pointer between readings.
func (s *ReadingsScreen) moved(pressed string) bool {
	if len(s.Readings) == 0 {
		return false
	}
	l := List[ReadingsItem]{Items: s.Readings, Focus: s.focus}
	if !l.Move(pressed, keys.Screen.Move) {
		return false
	}
	s.focus = l.Focus
	return true
}

// current is the reading under the pointer, or nil for an empty list.
func (s *ReadingsScreen) current() *ReadingsItem {
	if s.focus < 0 || s.focus >= len(s.Readings) {
		return nil
	}
	return &s.Readings[s.focus]
}
