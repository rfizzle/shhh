package components

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// ApprovalVariant selects which body the approval card renders
// (docs/interface/surfaces.md#the-approval-card): a command, a file edit
// diff, or a generic tool summary.
type ApprovalVariant int

const (
	ApprovalCommand ApprovalVariant = iota
	ApprovalEdit
	ApprovalGeneric
)

// ApprovalDecision is the card's Update result once a decision key is
// pressed.
type ApprovalDecision int

const (
	// ApprovalWaiting is no decision at all: the key was none of the card's
	// answers and the card is still up. It is the zero value so that the
	// result of an unresolved press is inert — a host that read it without
	// the done flag would otherwise read a press of any letter as an
	// approval.
	ApprovalWaiting ApprovalDecision = iota
	// ApprovalApprove runs the pending action (y / enter).
	ApprovalApprove
	// ApprovalDeny declines it (n / esc / ctrl+c) — esc never destroys.
	ApprovalDeny
	// ApprovalAlways approves and auto-allows the category for the session
	// (a, only when AllowAlways is set).
	ApprovalAlways
	// ApprovalFullDiff opens the full-screen diff view (d, only when
	// FullDiff is set); the host returns to the card afterwards.
	ApprovalFullDiff
	// ApprovalBatch asks the host for the queue behind the card as a list:
	// this action and every queued action the session would classify the same
	// way, checked and unchecked rather than answered together (A, only when
	// Batch is set). It settles nothing, which is why the card hands it back
	// rather than deciding on it.
	ApprovalBatch
	// ApprovalRelease hands the keyboard back to the draft and asks the host
	// to deliver the keystroke there. Only a card holding the keyboard by
	// arrival returns it (HeldOnArrival): the reader never took the keyboard,
	// so a key the card has no answer for is the start of a sentence rather
	// than a mispress.
	ApprovalRelease
	// ApprovalApproveNoted and ApprovalDenyNoted are the same two answers
	// with a sentence to come (Y / N, only when Noted is set). They settle
	// nothing on their own: the host opens the field, and the answer is given
	// when the field is confirmed
	// (docs/capabilities/approvals-and-safety.md#a-no-can-say-why-and-a-yes-can-say-what-next).
	ApprovalApproveNoted
	ApprovalDenyNoted
)

// Severity is how much the pending action could cost, led with as a word
// rather than carried by the border colour alone (
// docs/interface/principles.md#colour-never-carries-meaning-alone).
// The border tracks it as reinforcement.
type Severity int

const (
	// SeverityNone leaves the card unrated: the plain gray frame.
	SeverityNone Severity = iota
	SeverityLow
	SeverityMedium
	SeverityHigh
)

// Level is the severity as one word. HIGH is shouted because it is the one
// level where the reader is meant to stop.
func (s Severity) Level() string {
	switch s {
	case SeverityLow:
		return "low"
	case SeverityMedium:
		return "medium"
	case SeverityHigh:
		return "HIGH"
	}
	return ""
}

// Word is the severity as the title chip prints it: the glyph and the level.
// The glyph belongs to the chip, which is where the card leads with the
// severity; the body row states the same level with the reading behind it, so
// the three ways the card says it are three statements rather than one
// sentence printed twice
// (docs/interface/principles.md#colour-never-carries-meaning-alone).
func (s Severity) Word() string {
	if level := s.Level(); level != "" {
		return "⚠ " + level
	}
	return ""
}

// border is the frame colour that reinforces the severity word.
func (s Severity) border() lipgloss.Style {
	switch s {
	case SeverityHigh:
		return sty.Del
	case SeverityMedium:
		return sty.Accent
	}
	return sty.Border
}

// FieldTone colours a blast-radius field's value. The tone never carries the
// meaning on its own — the value is always a word — it only makes the row
// findable at a glance.
type FieldTone int

const (
	// ToneNeutral is a plain statement of fact.
	ToneNeutral FieldTone = iota
	// ToneSafe marks the reassuring answer: undo is possible, network closed.
	ToneSafe
	// ToneOpen marks a door left open: the network is reachable.
	ToneOpen
	// ToneRisk marks the answer that should give the reader pause: nothing
	// can be undone, nothing is containing this.
	ToneRisk
)

func (t FieldTone) style() lipgloss.Style {
	switch t {
	case ToneSafe:
		return sty.Add
	case ToneOpen:
		return sty.Accent
	case ToneRisk:
		return sty.Del
	}
	return sty.Body
}

// CardField is one row of the blast-radius block: what the action touches,
// whether it can be taken back, whether the network is open — or, on a
// generic tool card, the equivalent facts for that tool.
type CardField struct {
	// Label is the field name, left-aligned in its own column.
	Label string
	// Value is the answer in one or two words. It is never a colour alone.
	Value string
	// Detail qualifies the value and is the first thing dropped when the
	// terminal is too narrow to carry both.
	Detail string
	Tone   FieldTone
}

// fieldLabelWidth is the blast-radius block's label column, matching the
// `touches / undo / network` gutter in the Approvals artboard of the shhh
// Design System project.
const fieldLabelWidth = 10

// ApprovalCard is the single surface for every approval-gated action. One
// container, three body variants.
type ApprovalCard struct {
	Variant ApprovalVariant
	// Title is the border title, e.g. "Approve command"; QueuePos ("2 of 5")
	// is appended when set.
	Title    string
	QueuePos string
	// Headline is the first body row, e.g. "Assistant wants to run: go test".
	Headline string
	// Was is the line the call carried, on a card whose headline is the line
	// the reader wrote in its place. It is a row of the body rather than a
	// chip, because the two lines differ by a flag as often as by a verb and
	// a difference that small has to be read side by side
	// (docs/capabilities/approvals-and-safety.md#an-amended-command-is-a-new-command).
	//
	// Empty on every card whose headline is the call's own line, which is
	// every card until one is amended — a row repeating the headline under
	// the headline would spend a bounded card's row saying nothing.
	Was string
	// Severity leads the card as a word and rides the top border as the last
	// chip; it also picks the border colour.
	Severity Severity
	// SeverityReason is what makes this call that severity, as a clause the
	// level is read with: `edits one file under internal/agent`. It is the
	// third of the three ways a card leading with severity has to state it —
	// the border and the title chip are the other two — and it is a reading
	// the host derived rather than the chip said again
	// (docs/interface/principles.md#colour-never-carries-meaning-alone).
	//
	// A variant with no reading behind it leaves it empty and the row states
	// the level alone, because a reason invented to fill the row would be the
	// one thing on the card a reader could not check
	// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
	SeverityReason string
	// Warnings are safety.Check risks, rendered as ⚠ rows; when present the
	// caller must not set AllowAlways (flagged actions are never
	// blanket-approved).
	Warnings []string
	// Chip is the containment state folded into the title rail, e.g.
	// "⛨ bwrap · workspace". Uncontained replaces it with
	// "⚠ UNCONTAINED" and promotes it ahead of the severity chip.
	Chip        string
	Uncontained bool
	// Amended rides the title rail beside those two: the line this card is
	// about is the reader's, not the call's. It sheds before the severity
	// chip and after the containment one, because the body's own `was` row
	// says the same thing and never drops, while severity is what the
	// decision turns on.
	Amended bool
	// Fields is the blast-radius block under the headline: what the action
	// touches, whether it can be undone, whether the network is open.
	Fields []CardField
	// Hunks is the edit variant's diff body; Syntax highlights its lines.
	Hunks  []diff.Hunk
	Syntax Syntax
	// FullDiff offers [d] to open the diff full screen.
	FullDiff bool
	// Reversibility rides the edit variant's stats line: whether the
	// change can be taken back, stated where it costs the diff no rows.
	Reversibility string
	// Summary is the generic variant's one-line description.
	Summary string
	// Question is the decision prompt, e.g. "Run this command?".
	Question string
	// AllowAlways offers [a] with AlwaysHint describing the session grant.
	AllowAlways bool
	AlwaysHint  string
	// Batch offers [A]: this action and every queued action the session
	// would classify the same way, opened as the list that answers them.
	// BatchHint
	// states the count on the key, because a key that answers an unstated
	// number of decisions is not an offer.
	Batch     bool
	BatchHint string
	// Noted offers the two answers that carry a sentence — [Y] and [N] —
	// beside the two that do not
	// (docs/capabilities/approvals-and-safety.md#a-no-can-say-why-and-a-yes-can-say-what-next).
	// It is off wherever there is nothing waiting to read the sentence: a
	// /run the reader typed has no model to correct, and a child's routed
	// request answers over a channel that carries a boolean.
	//
	// A card offering it gives up the capital-N default marker on the safe
	// answer, because the capital is a key now and a run that printed a key
	// meaning something else would be the one thing this card must never do.
	// Cards without the offer keep the marker exactly as they had it.
	Noted bool
	// NoteOpen is the field open under the card, holding the keyboard.
	// NoteAllow says which of the two answers it will carry, and NoteField
	// is the field as its host rendered it — the card draws the field but
	// does not own it, because what is typed there survives a frame and the
	// card does not (the host rebuilds one every frame).
	NoteOpen  bool
	NoteAllow bool
	NoteField string
	// AmendOpen is the command itself open in a field, holding the keyboard,
	// and AmendField is that field as its host rendered it — the same
	// division of labour the note field is under, and the same rows: only
	// one of the two is ever open, because only one thing can hold a
	// keyboard.
	//
	// AmendRefused is the rule that turned the last confirm away, drawn red
	// above the field with the line still in it. The field stays open
	// because the alternative is throwing the reader's line away at the
	// moment they most want it back — and the decision behind the card is
	// waiting either way
	// (docs/capabilities/approvals-and-safety.md#an-amended-command-is-a-new-command).
	AmendOpen    bool
	AmendField   string
	AmendRefused string
	// GrantOpen is the third surface the card can hold under itself: the
	// grants the always-allow key offers, one row each, with what the grant
	// covers on the row and when it ends in its short field
	// (docs/capabilities/approvals-and-safety.md#a-grant-says-when-it-ends).
	// GrantRows are those rows and GrantFocus is the one the pointer is on —
	// the same division of labour the two fields are under, because a list
	// the reader has moved through outlives a frame and the card does not.
	//
	// It is drawn with the selector's own row layout rather than a layout of
	// its own: a grant's end is the short right-aligned field every list in
	// the product puts a note in, and a second renderer for it would be a
	// second answer to what that field looks like.
	GrantOpen  bool
	GrantRows  []SelectOption
	GrantFocus int
	// ExtraHints are the keys beyond the decision run that the host answers
	// itself — [g] to attach to the agent that asked, the manager's chord.
	//
	// They are pairs rather than prose so that the card knows what it is
	// advertising: a sentence could name a key nothing routes, and neither
	// the card nor a test could tell. The liveness table walks KeyRun and
	// these together and presses every one of them through the surface's
	// real route.
	ExtraHints []KeyOffer
	// SafeDefault names the safe answer in words, for the cards where it is
	// not obvious from the keys — e.g. "[n] deny — the safe answer". It names
	// a key that answers, never esc, which hands the keyboard back instead
	//; Return is where esc's own meaning is stated.
	SafeDefault string
	// Footnote says why a key the reader might expect is absent. A missing
	// key with a stated reason teaches; a missing key without one reads as a
	// bug.
	Footnote string
	// FullLabel is what [d] opens, where "full diff" is not it — the command
	// card's full view. Empty keeps the register's own words.
	FullLabel string
	// MaxLines bounds the card's total height, frame included; a body that
	// does not fit scrolls behind counted tails rather than clipping
	// (docs/interface/surfaces.md#the-approval-card). 0 means unbounded.
	MaxLines int
	// BodyOffset is the first body row the bounded card shows; the decision
	// run and everything under the rule never scroll. PanOffset shifts every
	// body row left by that many columns, for a body wider than the panel.
	// Both are clamped at render; the host stores them, because the card is
	// rebuilt every frame.
	BodyOffset int
	PanOffset  int
	// Return names what esc does while the card holds the keyboard: it hands
	// it back to the draft and leaves the decision waiting rather than
	// answering it. Stated because it is not obvious — invariant 3
	// asks for the safe answer in words wherever it is not.
	Return string
	// NotYetLive says the card is on screen beside a draft that still holds
	// the keyboard. Its decision keys render as not-yet-live and
	// Update answers nothing, so a letter typed into the sentence stays a
	// letter. Handover is the key that changes that — the card's only live
	// key in this state.
	NotYetLive bool
	Handover   string

	// HeldOnArrival marks a card that took the keyboard by landing on a draft
	// nobody was typing into, rather than by a handover the reader asked for
	//. It claims less than a card that was handed the keyboard:
	// the two answers and the two ways out, and nothing whose consequence a
	// reader could not undo — [a] and [d] still want the handover, because
	// `always` and `always` are not what someone typing `also` meant. Every
	// other key releases the keyboard and goes into the draft.
	HeldOnArrival bool
	// Grace marks a held card whose arrival landed on a keyboard still warm:
	// the host is discarding its decision keys until the typing has settled
	// (the chat model owns the window and the routing), and the card says so
	// by drawing its run dimmed with the phrase. Render-only — Update never
	// sees the discarded keys.
	Grace bool
}

// arrivalKeys are what a HeldOnArrival card answers to. They are the keys a
// reader who walked up to the card came to press; everything else is prose.
func (c *ApprovalCard) arrivalKey(pressed string) (ApprovalDecision, bool) {
	switch {
	case keys.Is(pressed, keys.Decision.Allow):
		return ApprovalApprove, true
	case keys.Is(pressed, keys.Decision.Deny):
		return ApprovalDeny, true
	case c.Noted && keys.Is(pressed, keys.Decision.AllowNoted):
		return ApprovalApproveNoted, true
	case c.Noted && keys.Is(pressed, keys.Decision.DenyNoted):
		return ApprovalDenyNoted, true
	}
	return ApprovalWaiting, false
}

// Update maps decision keys, preserving the chat confirm prompt's y/n/esc
// semantics. Unrecognized keys — including [a] when AllowAlways is off —
// leave the card waiting.
func (c *ApprovalCard) Update(msg tea.KeyPressMsg) (done bool, result ApprovalDecision) {
	if c.NotYetLive {
		// The card does not hold the keyboard, so none of its keys exist yet
		// (invariant 5). The host owns the one key that changes that, and
		// everything else belongs to the draft — including enter, which is
		// how a sentence ends.
		return false, ApprovalWaiting
	}
	if c.HeldOnArrival {
		// The card has the keyboard, but nobody handed it over. It answers
		// what it was walked up to be asked and gives the keyboard back for
		// everything else, so a reader who came to type a message instead of
		// answering loses neither the first letter of it nor the decision.
		if result, ok := c.arrivalKey(msg.String()); ok {
			return true, result
		}
		return true, ApprovalRelease
	}
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Decision.Allow):
		return true, ApprovalApprove
	case keys.Is(pressed, keys.Decision.Always):
		if c.AllowAlways {
			return true, ApprovalAlways
		}
	// [A] is the queue's key when there is a queue behind the card, and
	// otherwise stays the shifted spelling of [a] it has always been.
	case keys.Is(pressed, keys.Decision.Batch):
		if c.Batch {
			return true, ApprovalBatch
		}
		if c.AllowAlways {
			return true, ApprovalAlways
		}
	case keys.Is(pressed, keys.Decision.Diff):
		if c.FullDiff {
			return true, ApprovalFullDiff
		}
	case keys.Is(pressed, keys.Decision.Deny):
		return true, ApprovalDeny
	// The two answers that carry a sentence. They are read after the plain
	// pair rather than before it because nothing distinguishes them but the
	// shift, and a card without the offer must go on reading the shifted
	// letter as whatever it read it as before.
	case keys.Is(pressed, keys.Decision.AllowNoted):
		if c.Noted {
			return true, ApprovalApproveNoted
		}
	case keys.Is(pressed, keys.Decision.DenyNoted):
		if c.Noted {
			return true, ApprovalDenyNoted
		}
	}
	return false, ApprovalWaiting
}

// View renders the card at the given width, bounded to MaxLines rows: a body
// that does not fit scrolls behind counted tails, and the decision block
// under the rule never moves.
func (c *ApprovalCard) View(width int) string {
	body, hints := c.buildRows(width)
	rows := append(c.windowBody(body, len(hints), width), hints...)

	title := c.Title
	if c.QueuePos != "" {
		title += " (" + c.QueuePos + ")"
	}
	style := c.Severity.border()
	if c.Uncontained {
		style = sty.Del
	}
	return Card{Title: title, Chips: c.chips(), Style: &style}.Render(rows, width)
}

// buildRows lays the card out as its two halves: the body — headline,
// severity, blast radius, and the edit variant's whole diff — and the block
// under the rule, which is pinned. The split is what the scroll works on, so
// View and ScrollBounds share it rather than agreeing by inspection.
func (c *ApprovalCard) buildRows(width int) (body, hints []string) {
	inner := width - cardFrameWidth
	body = []string{sty.Headline.Render(c.Headline)}
	// What the call asked for, directly under what will run instead, so the
	// two are read as one statement rather than as two facts a row apart.
	if c.Was != "" {
		body = append(body, sty.Dim.Render(Clip("was: "+c.Was, inner)))
	}
	// Severity leads the body, as the level and what makes it that. The
	// border and the title chip carry the level too, and three statements of
	// one fact is what makes the card survive mono and a colour-blind reader
	// alike — three copies of one phrase would not.
	body = append(body, c.severityRows()...)
	// The generic variant's one-liner belongs with the headline it qualifies,
	// above the blast-radius block rather than below it.
	if c.Variant == ApprovalGeneric && c.Summary != "" && c.Summary != c.Headline {
		body = append(body, sty.Dim.Render(Clip(c.Summary, inner)))
	}
	if len(c.Fields) > 0 {
		if len(body) > 1 {
			body = append(body, "")
		}
		for _, f := range c.Fields {
			body = append(body, f.render(inner))
		}
	}
	if c.Variant == ApprovalEdit {
		adds, dels := diff.Stats(c.Hunks)
		line := fmt.Sprintf("+%d −%d · %s", adds, dels, plural(len(c.Hunks), "hunk"))
		if c.Reversibility != "" {
			line += " · " + c.Reversibility
		}
		// The diff is laid out whole and the window below decides what shows,
		// so what the cap swallows is counted rather than dropped.
		body = append(body, UnifiedLines(c.Hunks, inner,
			UnifiedOpts{LineNumbers: true, Emphasis: true, Syntax: c.Syntax})...)
		body = append(body, sty.Dim.Render(line))
	}

	hints = c.hintRowsFor(width, inner)
	// The keys sit below a rule so they never blend into the body.
	return body, append([]string{cardRule}, hints...)
}

// visibleBody is how many body rows fit once the frame and the pinned block
// have theirs, floored at one: a panel whose hint block leaves no room still
// shows one row of body — the headline, or the counted tail standing for all
// of it — because a decision whose subject is entirely off screen is not one
// (the floor the pre-scroll diff budget always had). -1 means unbounded.
func (c *ApprovalCard) visibleBody(hintRows int) int {
	if c.MaxLines <= 0 {
		return -1
	}
	return max(c.MaxLines-2-hintRows, 1)
}

// windowBody applies the pan and the vertical window to the body rows. What
// either edge cuts is counted on the row it took
// (docs/interface/principles.md#fold-never-hide): the last visible row
// becomes `… N more lines · shift+↓`, the first `… N lines above · shift+↑`
// once scrolled, and a row running past the right edge ends in ›.
func (c *ApprovalCard) windowBody(body []string, hintRows int, width int) []string {
	inner := Card{}.Inner(width)
	body = panRows(body, max(c.PanOffset, 0), inner)
	visible := c.visibleBody(hintRows)
	if visible < 0 || len(body) <= visible {
		return body
	}
	p := Pager{Offset: c.BodyOffset, Height: visible}
	win := append([]string(nil), p.Window(body)...)
	if below := p.Below(); below > 0 {
		win[len(win)-1] = sty.Dim.Render(Clip(c.tailLabel(countedTail(below+1),
			keys.Shown(keys.Decision.ScrollDown)), inner))
	}
	if above := p.Above(); above > 0 {
		label := c.tailLabel(fmt.Sprintf("… %s above", plural(above+1, "line")),
			keys.Shown(keys.Decision.ScrollUp))
		win[0] = sty.Dim.Render(Clip(label, inner))
	}
	return win
}

// tailLabel is a counted tail with its key — except on a card whose keys are
// not live yet, where naming the chord would advertise a key the draft still
// owns. The count stays either way: the fold is a fact, the key an offer.
func (c *ApprovalCard) tailLabel(count, key string) string {
	if c.NotYetLive {
		return count
	}
	return count + " · " + key
}

// panRows shifts body rows left by x columns and marks a row that still runs
// past the right edge with › — the sign the pan exists, distinct from the …
// every other clip in the product uses because this one is recoverable in
// place.
func panRows(rows []string, x, inner int) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		w := lipgloss.Width(r)
		if x == 0 && w <= inner {
			out[i] = r
			continue
		}
		var cut string
		if x < w {
			cut = ansi.Cut(r, x, min(x+inner, w))
		}
		if x+inner < w {
			cut = ansi.Truncate(cut, max(inner-1, 0), "") + sty.Dim.Render("›")
		}
		out[i] = cut
	}
	return out
}

// ScrollBounds reports how far the body can move at this width: the highest
// useful BodyOffset and PanOffset. The host clamps its stored offsets
// against these, so fifty presses past the end cost one press back.
func (c *ApprovalCard) ScrollBounds(width int) (maxBody, maxPan int) {
	body, hints := c.buildRows(width)
	if visible := c.visibleBody(len(hints)); visible > 0 && len(body) > visible {
		maxBody = len(body) - visible
	}
	inner := Card{}.Inner(width)
	for _, r := range body {
		if w := lipgloss.Width(r); w > inner {
			maxPan = max(maxPan, w-inner)
		}
	}
	return maxBody, maxPan
}

// hintRowsFor is the block under the rule: the decision keys and everything
// that qualifies them. A card that does not hold the keyboard renders that
// block as not-yet-live instead — the consequences of a key nobody can
// press yet are noise, so only the keys and the handover are shown.
func (c *ApprovalCard) hintRowsFor(width, inner int) []string {
	if c.NotYetLive {
		return notYetLiveRows(c.Question+" "+c.keys(), c.Handover, width)
	}
	if c.NoteOpen {
		return append(typingRows(c.Question+" "+c.keys(), width), c.noteRows(width, inner)...)
	}
	if c.AmendOpen {
		return append(typingRows(c.Question+" "+c.keys(), width), c.amendRows(width, inner)...)
	}
	if c.GrantOpen {
		return append(chosenRows(c.Question+" "+c.keys(), width), c.grantRows(width, inner)...)
	}
	if c.HeldOnArrival && c.Grace {
		rows := graceRows(c.Question+" "+c.keys(), width)
		if rest := c.arrivalRest(); len(rest) > 0 {
			rows = append(rows, sty.Dim.Render(FitSegments(rest, inner)))
		}
		if c.Return != "" {
			rows = append(rows, sty.Dim.Render(Clip(c.Return, inner)))
		}
		return rows
	}
	hint := c.Question + " " + c.keys()
	// What [a] and [d] qualify is part of the offer, not decoration: [a] now
	// names the scope it grants (`always allow "go test"`), which is longer
	// than the word it replaced and is the half a clip would take. So the
	// qualifiers ride beside the keys where the terminal carries them and
	// drop to rows of their own where it does not — the judgement [A] has
	// made for batches, for the same reason.
	var quals []string
	// What the shifted pair buys rides the key row itself wherever the
	// terminal carries it, and drops in with the others where it does not.
	// It is the one qualifier a card holding the keyboard by arrival keeps,
	// because those two are among its answers (KeyRun) and a key in the run
	// with nothing saying what it does is half an offer.
	//
	// Which of the two asks for what is the field's own label, stated the
	// moment one is open, and the register carries the long form for the key
	// list — so the qualifier here says only that a sentence is what they
	// buy. At sixty columns the choice is between that and the row a fuller
	// one would push off the card, and a key explained one press later beats
	// a key explained nowhere.
	if c.Noted {
		noted := "(" + notedWords() + ")"
		if joined := hint + "  " + noted; lipgloss.Width(joined) <= inner {
			hint = joined
		} else {
			quals = append(quals, noted)
		}
	}
	if !c.HeldOnArrival {
		if c.AllowAlways && c.AlwaysHint != "" {
			quals = append(quals, "("+c.AlwaysHint+")")
		}
		if c.FullDiff {
			quals = append(quals, "("+keys.Shown(keys.Decision.Diff)+": "+c.fullWords()+")")
		}
	}
	qualRow := strings.Join(quals, "  ")
	if qualRow != "" {
		if joined := hint + "  " + qualRow; lipgloss.Width(joined) <= inner {
			hint, qualRow = joined, ""
		}
	}
	segments := []string{hint}
	// A card that took the keyboard by arriving claims the two answers and
	// nothing a mistyped word could have meant, so it advertises nothing else
	// either — the same reason [a] and [d] lose their qualifiers just above.
	// The offer worth removing is a bare letter, which the card would answer
	// by putting it in the draft; a chord among them goes on working and
	// loses only its row. That is the safe direction of the trade — a key
	// shown and dead is what this rule exists to stop, and a key live and
	// unshown costs a reader one thing they already knew — and it is worth
	// more than a per-offer exception in the one block that has to stay
	// readable at sixty columns
	// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
	if !c.HeldOnArrival {
		for _, o := range c.ExtraHints {
			segments = append(segments, o.Key+" "+o.Label)
		}
	}
	if rest := c.arrivalRest(); len(rest) > 0 {
		// Fitted into what the answer left of the row rather than wrapped:
		// the sentence about the draft is an annotation on the keys, not one
		// of them, and the panel a card is drawn in may take at most 40% of
		// the terminal (docs/interface/principles.md#one-interaction-panel),
		// so a row spent on it is a row the transcript gives up. The handover
		// in front of it is an offer and stays.
		room := inner - lipgloss.Width(strings.Join(segments, " · ")) - 3
		segments = append(segments, dropToFit(rest, room)...)
	}
	if c.SafeDefault != "" {
		segments = append(segments, c.SafeDefault)
	}
	hints := hintRows(segments, width)
	if qualRow != "" && len(hints) > 0 {
		// They travel together on one row rather than one row each: they
		// qualify the same key line, and a card is bounded to 40% of the
		// screen — rows spent here are rows the transcript gives up.
		hints = append([]string{hints[0], sty.Hint.Render(Clip(qualRow, inner))}, hints[1:]...)
	}
	// [A] gets a row of its own rather than a place in the joined run: the
	// count is the whole offer, and on an 80-column terminal a joined run is
	// exactly where it would be clipped away.
	if c.Batch && c.BatchHint != "" && !c.HeldOnArrival {
		hints = append(hints, sty.Hint.Render(Clip(c.BatchHint, inner)))
	}
	if c.Footnote != "" {
		hints = append(hints, sty.Dim.Render(Clip(c.Footnote, inner)))
	}
	if c.Return != "" {
		hints = append(hints, sty.Dim.Render(Clip(c.Return, inner)))
	}
	return hints
}

// CardKey is one key of the decision run: the spelling the card printed, and
// the keystroke it stands for. The two are the same everywhere but the safe
// answer, where the capital N is the card's default marker rather than a
// shifted key — which is exactly why a pointer cannot be told what it landed
// on by reading the letter off the screen.
type CardKey struct {
	Shown string
	Key   string
}

// KeyRun is the decision keys in the order the card draws them. [a] appears
// only where a session grant is allowed and [A] only where there is a queue
// behind the card, so the run is always exactly what the card will answer to.
//
// keys() is this list joined, and KeyAt walks it across the row it was drawn
// on, so the run a reader sees, the keys the card answers and the cells a
// click resolves against cannot become three different lists.
func (c *ApprovalCard) KeyRun() []CardKey {
	// The card spells its keys as one run rather than as a row of offers, so it
	// composes them from the register's spellings: `y`, `n`, `a`, `A`. The
	// capital N is not a key — it is the default marker the card draws on the
	// safe answer — which is why it is applied here rather than declared as a
	// second binding for the same keystroke.
	yes := CardKey{keys.Shown(keys.Decision.Allow), keys.Shown(keys.Decision.Allow)}
	no := CardKey{keys.Shown(keys.Decision.Deny), keys.Shown(keys.Decision.Deny)}
	def := CardKey{strings.ToUpper(no.Shown), no.Key}
	if c.Noted {
		// The capital is a key of its own here, so the default marker is
		// retired on this card rather than drawn over a key that means
		// something else: the answers come in pairs — press the letter, or
		// press it shifted and say more — and the pairing is what the run
		// has to make legible. Which answer is the safe one is stated in
		// words instead, where SafeDefault already states it.
		noted := func(b keys.Binding) CardKey {
			return CardKey{keys.Shown(b), keys.Shown(b)}
		}
		run := []CardKey{yes, noted(keys.Decision.AllowNoted), no, noted(keys.Decision.DenyNoted)}
		if c.HeldOnArrival {
			// A card holding the keyboard by arrival claims its answers and
			// nothing else, and these are its answers: neither settles
			// anything on its own, and esc closes the field they open.
			return run
		}
		if c.AllowAlways {
			always := keys.Shown(keys.Decision.Always)
			run = append(run, CardKey{always, always})
		}
		if c.Batch {
			batch := keys.Shown(keys.Decision.Batch)
			run = append(run, CardKey{batch, batch})
		}
		return run
	}
	if c.HeldOnArrival {
		// The card has the keyboard but nobody gave it: it answers the two
		// keys and offers nothing a mistyped word could have meant.
		return []CardKey{yes, def}
	}
	run := []CardKey{yes, def}
	if c.AllowAlways {
		always := keys.Shown(keys.Decision.Always)
		run = []CardKey{yes, no, {always, always}}
	}
	if c.Batch {
		// The capital N means "this is the default"; beside a capital A it
		// would only read as a second key, so the batch spelling drops it.
		if !c.AllowAlways {
			run = []CardKey{yes, no}
		}
		batch := keys.Shown(keys.Decision.Batch)
		run = append(run, CardKey{batch, batch})
	}
	return run
}

// keys is the decision prompt's key list as the card prints it.
func (c *ApprovalCard) keys() string {
	run := c.KeyRun()
	shown := make([]string, len(run))
	for i, k := range run {
		shown[i] = k.Shown
	}
	return "[" + strings.Join(shown, "/") + "]"
}

// KeyAt reports which decision key covers display column col of a rendered
// row, and whether the row carries the run at all.
//
// The geometry is read back out of the render rather than laid out a second
// time beside it. Crush builds a parallel compositor of hit layers and
// rebuilds it on every frame to keep the two honest (`common/button.go`);
// finding the run in the row it was drawn on means a key that is on the
// screen is clickable and a key a narrow terminal clipped away is not, by
// construction rather than by upkeep.
//
// The run is divided among its keys with nothing left over — the brackets
// belong to the keys at the ends and each separator to the key before it —
// because one cell is not a target, and a press that lands between two keys
// should mean the one it is standing on rather than nothing at all.
func (c *ApprovalCard) KeyAt(row string, col int) (string, bool) {
	run := c.KeyRun()
	if len(run) == 0 {
		return "", false
	}
	plain := ansi.Strip(row)
	i := strings.Index(plain, c.keys())
	if i < 0 {
		return "", false
	}
	// One cell past the opening bracket, measured in display cells: the row
	// carries a border, a pad and whatever the question said, and none of
	// that is one byte per column.
	at := ansi.StringWidth(plain[:i]) + 1
	for i, k := range run {
		w := ansi.StringWidth(k.Shown)
		lo, hi := at, at+w+1
		if i == 0 {
			lo--
		}
		if col >= lo && col < hi {
			return k.Key, true
		}
		at += w + 1
	}
	return "", false
}

// arrivalRest names what the handover still buys on a card that took the
// keyboard by arriving: the keys it deliberately did not claim, and the fact
// that everything else goes into the draft. It is the not-yet-live row turned
// around — there the handover buys every key, here it buys the ones a
// sentence could have produced by accident.
//
// The two are separate fields, in the order the row gives them up: the
// handover is an offer and the sentence after it is what explains the offer,
// so a terminal that cannot carry both drops the explanation whole rather
// than ending the row mid-sentence — the same order the chrome's header fits
// its halves in (docs/interface/principles.md#fold-never-hide).
func (c *ApprovalCard) arrivalRest() []string {
	if !c.HeldOnArrival {
		return nil
	}
	var rest []string
	if c.AllowAlways {
		rest = append(rest, keys.Shown(keys.Decision.Always))
	}
	if c.FullDiff {
		rest = append(rest, keys.Shown(keys.Decision.Diff))
	}
	if c.Batch {
		rest = append(rest, keys.Shown(keys.Decision.Batch))
	}
	if len(rest) == 0 || c.Handover == "" {
		return []string{arrivalDraftWords}
	}
	for i, k := range rest {
		rest[i] = "[" + k + "]"
	}
	return []string{
		"[" + c.Handover + "] for " + strings.Join(rest, "/"),
		arrivalDraftWords,
	}
}

// arrivalDraftWords is what a card that took the keyboard by arriving says
// about every key it did not claim. It is a sentence rather than an offer,
// which is why it is the field the row drops.
const arrivalDraftWords = "any other key goes to your draft"

// fullWords is what [d] is said to open: the register's own words unless the
// card means something more specific — the command card's full view.
func (c *ApprovalCard) fullWords() string {
	if c.FullLabel != "" {
		return c.FullLabel
	}
	return keys.Words(keys.Decision.Diff)
}

// The two words the shifted answers are offered under, and the labels their
// fields carry. They are the same two words in both places on purpose: what
// the key promised is what the field asks for, so a reader who pressed on the
// promise is not met with a differently worded request.
const (
	noteWhatNext = "what next"
	noteWhyNot   = "why not"
)

// noteIndent is how far the field sits in from the card's own left edge,
// under the ┄ label that names it. Two columns, the note selector's, because
// it is the same field under the same label.
const noteIndent = 2

// notedWords is the qualifier the shifted pair rides under: the two keys in
// the order the run prints them, and what pressing one buys, in the grammar
// the card's other qualifiers already use — the key, then the thing it opens.
// One segment rather than two, because they are one offer — the same answers,
// with a sentence — and two would spend two of a card's rows saying it twice.
//
// It is as short as it is on purpose: on an eighty-column terminal a longer
// one pushes the handover onto a row of its own, and the row it takes comes
// off the diff the card exists to show.
func notedWords() string {
	return keys.Shown(keys.Decision.AllowNoted) + "/" + keys.Shown(keys.Decision.DenyNoted) +
		": a note"
}

// noteLabel is what the open field asks for, which is the half of the pair
// the key that opened it stands for.
func (c *ApprovalCard) noteLabel() string {
	if c.NoteAllow {
		return noteWhatNext
	}
	return noteWhyNot
}

// FieldWidth is how wide the host should draw whichever field it has open:
// the card's inner width, less the indent the ┄ label puts it in by and the
// one cell the field's own caret stands in past its last character. The card
// owns the geometry and the host owns the field, so the number crosses rather
// than being guessed at either end.
//
// It has no floor under the room the card actually has. A field wider than
// the row it is drawn on would be clipped while the caret it reports was
// not, which puts the terminal's cursor outside the card on a terminal too
// narrow to draw one — and a cursor standing where nothing is being typed is
// worse than a field too narrow to read.
func FieldWidth(width int) int { return max(Card{}.Inner(width)-noteIndent-1, 1) }

// fieldRows are an open field under the dimmed decision run: the label, the
// field as its host rendered it, whatever refused the last confirm, and the
// two keys that close it.
//
// The shape is the note selector's, down to the ┄ label and the two-column
// indent under it (noteselect.go), because it is the same field doing the
// same job and a reader meets both in the same session. What differs is the
// hint, which is why it is the caller's: enter here does not confirm a
// selection, and what esc leaves behind is not the same on the card's two
// fields.
//
// The refusal sits under the field rather than over it so the ┄ label stays
// the last one on the card — FieldOrigin finds the field by that label, and
// a second one above it would put the terminal's cursor a row out.
func (c *ApprovalCard) fieldRows(label, view, refused, take, back string, width, inner int) []string {
	rows := []string{sty.Dim.Render(Clip("┄ "+label, inner))}
	for _, l := range strings.Split(view, "\n") {
		rows = append(rows, Clip(strings.Repeat(" ", noteIndent)+l, inner))
	}
	if refused != "" {
		rows = append(rows, sty.Err.Render(Clip(strings.Repeat(" ", noteIndent)+"⚠ "+refused, inner)))
	}
	// Wrapped rather than clipped: what esc does here is the half a narrow
	// terminal would take, and it is the half that has to be readable — a
	// reader who cannot see that esc settles nothing has no way out of the
	// field they can be sure of (docs/interface/principles.md#fold-never-hide).
	return append(rows, hintRows([]string{
		words(keys.Select.Take, take),
		words(keys.Select.Cancel, back),
	}, width)...)
}

// noteRows are the note field, open under the card.
func (c *ApprovalCard) noteRows(width, inner int) []string {
	send := "deny with this"
	if c.NoteAllow {
		send = "allow, and send this"
	}
	return c.fieldRows(c.noteLabel(), c.NoteField, "", send,
		"back to the card — nothing is answered", width, inner)
}

// amendRows are the command itself, open under the card for the reader to
// change before it runs.
//
// What esc leaves behind is worth spelling out separately from the note
// field's: there the answer the key stood for is still waiting, and here the
// line the call carried is what comes back — which is the difference between
// abandoning a sentence and abandoning an edit.
func (c *ApprovalCard) amendRows(width, inner int) []string {
	return c.fieldRows(amendWords, c.AmendField, c.AmendRefused, "run this line",
		"back to the card with the original", width, inner)
}

// amendWords is what the open command field asks for. It is the same phrase
// the key that opens it is offered under, so a reader who pressed on the
// promise is not met with a differently worded request.
const amendWords = "edit the command"

// grantWords labels the open grant list. It is the phrase the always-allow
// key is offered under and the phrase the documentation uses for the offer,
// so the reader who pressed on it is met with the words they pressed on.
const grantWords = "allow without asking"

// grantRows are the grants the card can make, open under it: the ┄ label,
// the rows themselves, and the keys that move through them and close them.
//
// The rows go through the single-select's own layout — the pointer, the
// label, the description and the short right-aligned field — because a grant
// naming its end in that field is the same row every list in the product
// draws, and drawing it here a second way would be a second answer to what a
// list looks like. The list built for it is a value rather than the card's:
// nothing about a query, a window or a title applies to three rows pinned
// under a decision.
func (c *ApprovalCard) grantRows(width, inner int) []string {
	list := Select{Options: c.GrantRows, Focus: c.GrantFocus}
	rows := []string{sty.Dim.Render(Clip("┄ "+grantWords, inner))}
	rows = append(rows, list.optionRows(width, false, 0, len(c.GrantRows))...)
	// What esc leaves behind is spelled out rather than left to the word
	// "cancel": the whole offer is that a grant is read before it is made,
	// and a reader who cannot see that leaving grants nothing has to guess
	// at what they have just done (docs/interface/principles.md#fold-never-hide).
	return append(rows, hintRows([]string{
		words(keys.Select.MoveJK, "choose"),
		words(keys.Select.Take, "grant it, and run"),
		words(keys.Select.Cancel, "back to the card — nothing is granted"),
	}, width)...)
}

// FieldOrigin is the cell an open field's own render starts at inside the
// rendered card. The host owns the field and so owns the caret inside it;
// what the card knows is where it put the field, and the two are added.
//
// It is counted off the same two halves View lays the card out from rather
// than measured a second time beside it, for the reason KeyAt reads its run
// out of the rendered row: a position that agreed with the layout only by
// upkeep is one that drifts a column the first time a row is added.
func (c *ApprovalCard) FieldOrigin(width int) (x, y int, ok bool) {
	if !c.NoteOpen && !c.AmendOpen {
		return 0, 0, false
	}
	body, hints := c.buildRows(width)
	rows := append(c.windowBody(body, len(hints), width), hints...)
	// The field is the row after its ┄ label — the run above it may have
	// wrapped to two rows, and the body above that is whatever fits.
	at := -1
	for i, row := range rows {
		if strings.HasPrefix(ansi.Strip(row), "┄ ") {
			at = i + 1
		}
	}
	if at < 0 || at >= len(rows) {
		return 0, 0, false
	}
	if width < minCardWidth {
		// Below the frame the rows are drawn bare and the rules are dropped
		// (Card.Render), so the row keeps its own indent and nothing else.
		for _, row := range rows[:at] {
			if row == cardRule {
				at--
			}
		}
		return noteIndent, at, true
	}
	// Inside the frame every row is preceded by the border and its padding
	// column, and the top border is a row of its own.
	return noteIndent + cardFrameWidth/2, at + 1, true
}

// severityRows are the severity as the body states it — the level and what
// makes it that — and the risks under it.
//
// The level is said in the level's own terms and not in the chip's: the chip
// on the title rail already reads `⚠ medium`, and a first body row reading
// `⚠ medium` under it spent a row of a bounded card saying the same three
// characters again. What the reader cannot get anywhere else is why this call
// is that level, so that is what the row carries
// (docs/interface/surfaces.md#the-approval-card).
//
// A flagged command needs no separate clause: its risks are the reason it is
// high, so the first of them is the reading, and the rest follow as ⚠ rows.
// A rated card with neither still states its level, because the word is what
// the border colour means.
func (c *ApprovalCard) severityRows() []string {
	level := c.Severity.Level()
	if level == "" {
		var rows []string
		for _, w := range c.Warnings {
			rows = append(rows, sty.Warn.Render("⚠ "+w))
		}
		return rows
	}
	style := sty.Warn
	if c.Severity == SeverityLow {
		style = sty.Dim
	}
	reason, rest := c.SeverityReason, c.Warnings
	if reason == "" && len(rest) > 0 {
		reason, rest = rest[0], rest[1:]
	}
	lead := style.Render(level)
	if reason != "" {
		lead += sty.Dim.Render(" · " + reason)
	}
	rows := []string{lead}
	for _, w := range rest {
		rows = append(rows, sty.Warn.Render("⚠ "+w))
	}
	return rows
}

// chips are the labels riding the top border, in drop order: the containment
// state goes first so it is the one shed on a narrow terminal, and the
// severity chip — the thing the decision turns on — is last and survives.
func (c *ApprovalCard) chips() []string {
	var chips []string
	switch {
	case c.Uncontained:
		chips = append(chips, "⚠ UNCONTAINED")
	case c.Chip != "":
		chips = append(chips, c.Chip)
	}
	if c.Amended {
		chips = append(chips, "✎ amended")
	}
	if word := c.Severity.Word(); word != "" {
		chips = append(chips, word)
	}
	return chips
}

// render lays one blast-radius field into its label column. The detail is
// dropped rather than clipped when the terminal cannot carry it, so what is
// left is a whole statement instead of half of one.
func (f CardField) render(inner int) string {
	label := padRight(f.Label, fieldLabelWidth-1) + " "
	value := f.Tone.style().Render(f.Value)
	head := sty.Dim.Render(label) + value
	if f.Detail == "" {
		return head
	}
	detail := " — " + f.Detail
	if lipgloss.Width(head)+lipgloss.Width(detail) > inner {
		return head
	}
	return head + sty.Dimmer.Render(detail)
}

// plural renders "1 hunk" / "3 hunks".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
