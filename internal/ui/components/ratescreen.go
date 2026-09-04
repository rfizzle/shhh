package components

// The rating screen (docs/interface/surfaces.md#the-supporting-screens).
//
// `shhh rate` was the one surface in the product that asked a question over a
// `bufio` prompt loop: no colour, no glyph, no card, and no esc — a reader
// who wanted out had to know that `q` was a word this particular loop
// understood. It is re-cut here from parts that already exist: the header and
// rule every supporting screen leads with, the framed card the approval
// surface is drawn in, the column grid a shell command is always drawn on,
// and the foot key row with `[?]` behind it.
//
// It is the only one of these screens that asks rather than reports, and that
// is the whole of its shape: there is nothing to move between, because the
// answer is what moves. One card is up, three keys answer it, and the fourth
// is the way out — which is esc, because esc is always the safe answer
// (docs/interface/principles.md#esc-is-always-the-safe-answer).
//
// It is a passive component like the rest of this package. It owns no rating
// semantics: a key resolves to a RateAnswer the host writes to its own store,
// and the host counts what it wrote.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// rateMinCardBody is the smallest card the screen is still worth drawing:
// the prompt, the blank, and the row it is being asked about. Below
// it there is no question left on the screen.
const rateMinCardBody = 3

// RateRow is one unrated thing, already resolved to what the screen draws.
// The host formats every field — how long ago is "4m ago", an exit code is
// "exit 0" — because those are readings of the store and this is a renderer.
// It is why the screen has no idea that what it is asking about comes from
// two tables: a command the model wrote and a session it ran are one card
// with different words in it.
type RateRow struct {
	// ID is the host's own handle on the entry, carried back on an answer and
	// never drawn.
	ID string
	// Prompt is what was asked, or what the session was about. It is the
	// card's body and never clips: it is the half of the question the reader
	// is being asked to judge.
	Prompt string
	// Kind and Verb say what the row under the prompt is — a shell command
	// the model wrote, an agent run it worked through — and pick the glyph
	// and the word that lead it.
	Kind ActivityKind
	Verb string
	// Target is the thing itself, drawn on the column grid: the command, or
	// the conversation the session left behind.
	Target string
	// When is how long ago, in the row's own words — `4m ago`, `yesterday`. It
	// is the card's title.
	When string
	// Outcome is the closed outcome field the row ends in: `exit 0`,
	// `copied`, `not run`, `completed`, `abandoned`.
	Outcome string
	// State picks the glyph the row leads with and the colour its outcome
	// takes.
	State ActivityState
}

// RateAct is the answer a key gave for what is on the card.
type RateAct int

const (
	// RateWorked is `[y]`: it did what was wanted.
	RateWorked RateAct = iota
	// RateFailed is `[n]`: it did not.
	RateFailed
	// RateSkipped is `[s]`: no answer, and the entry stays unrated.
	RateSkipped
)

// RateAnswer is one answer for the host to write down. The screen has already
// moved on to the next card by the time the host sees it.
type RateAnswer struct {
	Act RateAct
	ID  string
}

// RateResult is how the screen closed: because the reader stopped, or because
// there was nothing left to ask about.
type RateResult struct {
	Stopped bool
	// Answer is what the reader said about the card that was showing. It
	// arrives with the screen still up on all but the last card, which is
	// what lets a run of entries be answered without a key between them.
	// nil is a key that answered nothing.
	Answer *RateAnswer
}

// RateScreen is `shhh rate`: a takeover surface, full width, no inspector
// rail, owning the keyboard for as long as it is up.
type RateScreen struct {
	// Rows are the unrated entries newest first, as the host read them.
	Rows []RateRow
	// Focus is the card showing. It only ever moves forward: an answer is a
	// write, and a screen that let the reader walk back onto a card they had
	// already answered would be offering to change the store without saying so.
	Focus int
	// MaxLines bounds the screen height; everything pinned comes off the
	// card's budget before it is framed. 0 is unbounded.
	MaxLines int
	// Notice is the line a key left behind — that a write failed, most often.
	// The host clears it on the next keystroke.
	Notice string

	keys bool
}

// Update is the screen's whole keyboard: three answers, the register, and the
// way out.
func (r *RateScreen) Update(msg tea.KeyPressMsg) (done bool, result RateResult) {
	pressed := msg.String()
	switch {
	case keys.Is(pressed, keys.Screen.List):
		r.keys = !r.keys
		return false, RateResult{}
	case keys.Is(pressed, keys.Screen.Quit):
		return true, RateResult{Stopped: true}
	}
	// With nothing left to answer the three answers are not offered, and a key
	// that is not offered does not act (invariant 5). The way out above is the
	// only key this card has, which is what the row it draws says.
	row := r.current()
	if row == nil {
		return false, RateResult{}
	}
	var act RateAct
	switch {
	case keys.Is(pressed, keys.Screen.Worked):
		act = RateWorked
	case keys.Is(pressed, keys.Screen.Failed):
		act = RateFailed
	case keys.Is(pressed, keys.Screen.Skip):
		act = RateSkipped
	default:
		return false, RateResult{}
	}
	// The card is answered and gone in the same keystroke: the answer is
	// carried back for the host to write, and the screen is already showing
	// the next question. A run of entries is answered without a key between
	// them, which is the whole reason this surface is a screen and not a list.
	r.Focus++
	return r.Focus >= len(r.Rows), RateResult{Answer: &RateAnswer{Act: act, ID: row.ID}}
}

// current is the row the card is over, or nil once every one has been
// answered.
func (r *RateScreen) current() *RateRow {
	if r.Focus < 0 || r.Focus >= len(r.Rows) {
		return nil
	}
	return &r.Rows[r.Focus]
}

// SetSize gives the screen the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (r *RateScreen) SetSize(_, height int) { r.MaxLines = height }

// View renders the screen: the shared chrome, with one card in the rows it
// leaves.
func (r *RateScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	return ScreenChrome{
		Header:   r.header(),
		Foot:     r.footer().Rows(width),
		Notice:   r.Notice,
		MaxLines: r.MaxLines,
	}.View(width, func(budget int) []string { return r.cardRows(width, budget) })
}

// header names the command and says how far through the entries the reader
// is.
func (r *RateScreen) header() ScreenHeader {
	h := ScreenHeader{Left: []RailSegment{screenTitle("shhh rate")}, Keys: screenHeaderKeys()}
	if subject := r.subject(); subject != "" {
		h.Left = append(h.Left, screenField(subject))
	}
	return h
}

// subject is `3 of 7`: which card is up and how many there were. It counts
// from one, because the reader is being asked a question rather than reading
// an index, and it stops counting once the last one has been answered.
func (r *RateScreen) subject() string {
	if len(r.Rows) == 0 {
		return ""
	}
	if r.current() == nil {
		return fmt.Sprintf("%d asked", len(r.Rows))
	}
	return fmt.Sprintf("%d of %d", r.Focus+1, len(r.Rows))
}

// cardRows is the question: one framed card holding the prompt that was
// asked and the command it produced. The card is what makes the two read as
// one thing rather than as two rows that happen to be adjacent.
func (r *RateScreen) cardRows(width, budget int) []string {
	row := r.current()
	if row == nil {
		return []string{sty.Dim.Render(Clip("every one of them has been asked about", width))}
	}
	inner := Card{}.Inner(width)
	body := wrapSpans([]styledSpan{{row.Prompt, sty.Body}}, inner)
	body = append(body, "")
	body = append(body, gridRows(ActivityRow{
		Kind: row.Kind, State: row.State, Verb: row.Verb, Outcome: row.Outcome,
	}, row.Target, inner)...)
	// The frame's own two rows come off the budget before the body is bounded,
	// and a card with no room left for a body is not drawn at all: half a frame
	// says less than the row that replaces it.
	if budget > 0 {
		if budget < rateMinCardBody+2 {
			return truncRows(body, budget, width)
		}
		body = truncRows(body, budget-2, inner)
	}
	return strings.Split(Card{Title: row.When}.Render(body, width), "\n")
}

// footer is the keys the screen offers, or the whole register behind `[?]`.
func (r *RateScreen) footer() KeyFooter {
	return KeyFooter{Offers: r.offers(), Register: r.keyList(), Showing: r.keys}
}

// offers is the key row. Once the last card has been answered the three
// answers are gone from it and only the way out is left: a key that cannot
// act is not an offer (invariant 5).
func (r *RateScreen) offers() []KeyOffer {
	out := keyOfferAs(keys.Select.Cancel, "stop")
	if r.current() == nil {
		return []KeyOffer{out}
	}
	return []KeyOffer{
		keyOffer(keys.Screen.Worked),
		keyOffer(keys.Screen.Failed),
		keyOffer(keys.Screen.Skip),
		out,
	}
}

// keyList is every key the screen has, for `[?]`.
func (r *RateScreen) keyList() []KeyOffer {
	return []KeyOffer{
		keyOfferAs(keys.Screen.Worked, "it did what was asked"),
		keyOfferAs(keys.Screen.Failed, "it did not"),
		keyOfferAs(keys.Screen.Skip, "no answer; it stays unrated"),
		keyOfferAs(keys.Select.Cancel, "stop, keeping every answer already given"),
		keyOfferAs(keys.Screen.Quit, "stop, keeping every answer already given"),
	}
}
