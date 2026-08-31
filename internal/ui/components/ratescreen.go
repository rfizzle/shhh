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
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// rateMinCardBody is the smallest card the screen is still worth drawing:
// the prompt, the blank, and the command row it is being asked about. Below
// it there is no question left on the screen.
const rateMinCardBody = 3

// RateRow is one unrated command, already resolved to what the screen draws.
// The host formats every field — how long ago is "4m ago", an exit code is
// "exit 0" — because those are readings of the store and this is a renderer.
type RateRow struct {
	// ID is the host's own handle on the entry, carried back on an answer and
	// never drawn.
	ID string
	// Prompt is what was asked. It is the card's body and never clips: it is
	// the half of the question the reader is being asked to judge.
	Prompt string
	// Command is what came back, drawn on the column grid.
	Command string
	// When is how long ago, in the row's own words — `4m ago`, `yesterday`. It
	// is the card's title.
	When string
	// Outcome is the closed outcome field the command row ends in: `exit 0`,
	// `copied`, `not run`.
	Outcome string
	// State picks the glyph the command row leads with and the colour its
	// outcome takes.
	State ActivityState
}

// RateAct is the answer a key gave for the command on the card.
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
type RateResult struct{ Stopped bool }

// RateScreen is `shhh rate`: a takeover surface, full width, no inspector
// rail, owning the keyboard for as long as it is up.
type RateScreen struct {
	// Rows are the unrated commands newest first, as the host read them.
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
func (r *RateScreen) Update(msg tea.KeyPressMsg) (done bool, result any) {
	pressed := msg.String()
	switch {
	case keys.Is(pressed, keys.Screen.List):
		r.keys = !r.keys
		return false, nil
	case keys.Is(pressed, keys.Screen.Quit):
		return true, RateResult{Stopped: true}
	}
	// With nothing left to answer the three answers are not offered, and a key
	// that is not offered does not act (invariant 5). The way out above is the
	// only key this card has, which is what the row it draws says.
	row := r.current()
	if row == nil {
		return false, nil
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
		return false, nil
	}
	// The card is answered and gone in the same keystroke: the answer is
	// carried back for the host to write, and the screen is already showing
	// the next question. A run of entries is answered without a key between
	// them, which is the whole reason this surface is a screen and not a list.
	r.Focus++
	return r.Focus >= len(r.Rows), RateAnswer{Act: act, ID: row.ID}
}

// current is the row the card is over, or nil once every one has been
// answered.
func (r *RateScreen) current() *RateRow {
	if r.Focus < 0 || r.Focus >= len(r.Rows) {
		return nil
	}
	return &r.Rows[r.Focus]
}

// View renders the screen: the start-screen header and its rule, one card,
// and the key row at the foot.
func (r *RateScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	foot := r.footRows(width)
	head := []string{r.headerRow(width), reviewRule(width), ""}

	pinned := len(head) + 1 + len(foot)
	if r.Notice != "" {
		pinned++
	}
	rows := append(head, r.cardRows(width, r.budget(pinned))...)
	rows = append(rows, "")
	rows = append(rows, foot...)
	if r.Notice != "" {
		rows = append(rows, sty.Dim.Render(clip(r.Notice, width)))
	}
	return strings.Join(rows, "\n")
}

// budget is how many rows the card may spend: the screen's height less
// everything pinned around it. An unbounded screen folds nothing.
func (r *RateScreen) budget(pinned int) int {
	if r.MaxLines <= 0 {
		return 0
	}
	return max(r.MaxLines-pinned, 1)
}

// headerRow is the start-screen header: the command, how far through the
// entries the reader is, and the two keys every one of these screens offers.
//
// These keys drop before the count does on a terminal too narrow for both,
// which is the history browser's trade rather than the doctor's. The two are
// not in disagreement: doctor and metrics keep `[q]` because dropping it would
// leave a takeover with no stated way out (invariant 5), and this screen has a
// foot key row that states the way out whatever the header does.
func (r *RateScreen) headerRow(width int) string {
	left := brightStyle().Render("shhh rate")
	if subject := r.subject(); subject != "" {
		left += sty.Dim.Render(" · " + subject)
	}
	right := sty.Dim.Render(screenHeaderKeys())
	if pad := width - lipgloss.Width(left) - lipgloss.Width(right); pad >= 2 {
		return left + strings.Repeat(" ", pad) + right
	}
	return clip(left, width)
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
		return []string{sty.Dim.Render(clip("every one of them has been asked about", width))}
	}
	inner := max(width-cardFrameWidth, 1)
	body := wrapSpans([]styledSpan{{row.Prompt, sty.Body}}, inner)
	body = append(body, "")
	body = append(body, commandGridRows(row.Command, row.Outcome, "", row.State, inner)...)
	// The frame's own two rows come off the budget before the body is bounded,
	// and a card with no room left for a body is not drawn at all: half a frame
	// says less than the row that replaces it.
	if budget > 0 {
		if budget < rateMinCardBody+2 {
			return truncRows(body, budget, width)
		}
		body = truncRows(body, budget-2, inner)
	}
	return strings.Split(renderCard(row.When, body, width), "\n")
}

// footRows are the keys the screen offers, or the whole register behind
// `[?]`. Nothing here truncates: the offers wrap (invariant 4).
func (r *RateScreen) footRows(width int) []string {
	if r.keys {
		rows := make([]string, 0, len(r.keyList())+1)
		for _, offer := range r.keyList() {
			rows = append(rows, clip(keyOffers([]KeyOffer{offer}), width))
		}
		return append(rows, clip(keyOffers([]KeyOffer{hideKeysOffer()}), width))
	}
	return wrapOffers(r.offers(), width)
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
