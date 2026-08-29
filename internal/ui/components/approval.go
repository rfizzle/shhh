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
	// ApprovalApprove runs the pending action (y / enter).
	ApprovalApprove ApprovalDecision = iota
	// ApprovalDeny declines it (n / esc / ctrl+c) — esc never destroys.
	ApprovalDeny
	// ApprovalAlways approves and auto-allows the category for the session
	// (a, only when AllowAlways is set).
	ApprovalAlways
	// ApprovalFullDiff opens the full-screen diff view (d, only when
	// FullDiff is set); the host returns to the card afterwards.
	ApprovalFullDiff
	// ApprovalBatch approves this action and every queued action the session
	// would classify the same way (A, only when Batch is set).
	ApprovalBatch
	// ApprovalRelease hands the keyboard back to the draft and asks the host
	// to deliver the keystroke there. Only a card holding the keyboard by
	// arrival returns it (HeldOnArrival): the reader never took the keyboard,
	// so a key the card has no answer for is the start of a sentence rather
	// than a mispress.
	ApprovalRelease
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

// Word is the severity as the card prints it. HIGH is shouted because it is
// the one level where the reader is meant to stop.
func (s Severity) Word() string {
	switch s {
	case SeverityLow:
		return "⚠ low"
	case SeverityMedium:
		return "⚠ medium"
	case SeverityHigh:
		return "⚠ HIGH"
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
	// Severity leads the card as a word and rides the top border as the last
	// chip; it also picks the border colour.
	Severity Severity
	// Warnings are safety.Check risks, rendered as ⚠ rows; when present the
	// caller must not set AllowAlways (flagged actions are never
	// blanket-approved).
	Warnings []string
	// Chip is the containment state folded into the title rail, e.g.
	// "⛨ bwrap · workspace". Uncontained replaces it with
	// "⚠ UNCONTAINED" and promotes it ahead of the severity chip.
	Chip        string
	Uncontained bool
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
	// would classify the same way, answered together. BatchHint
	// states the count on the key, because a key that answers an unstated
	// number of decisions is not an offer.
	Batch     bool
	BatchHint string
	// ExtraHints are additional key hints the host handles itself (e.g.
	// "g: attach to writer-1" on a routed child approval).
	ExtraHints []string
	// SafeDefault names the safe answer in words, for the cards where it is
	// not obvious from the keys — e.g. "[n] deny — the safe answer". It names
	// a key that answers, never esc, which hands the keyboard back instead
	//; Return is where esc's own meaning is stated.
	SafeDefault string
	// Footnote says why a key the reader might expect is absent. A missing
	// key with a stated reason teaches; a missing key without one reads as a
	// bug.
	Footnote string
	// MaxLines bounds the card's total height, frame included; the diff body
	// shrinks to fit. 0 means unbounded.
	MaxLines int
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
}

// arrivalKeys are what a HeldOnArrival card answers to. They are the keys a
// reader who walked up to the card came to press; everything else is prose.
func (c *ApprovalCard) arrivalKey(pressed string) (ApprovalDecision, bool) {
	switch {
	case keys.Is(pressed, keys.Decision.Allow):
		return ApprovalApprove, true
	case keys.Is(pressed, keys.Decision.Deny):
		return ApprovalDeny, true
	}
	return 0, false
}

// Update maps decision keys, preserving the chat confirm prompt's y/n/esc
// semantics. Unrecognized keys — including [a] when AllowAlways is off —
// leave the card waiting.
func (c *ApprovalCard) Update(msg tea.KeyPressMsg) (done bool, result any) {
	if c.NotYetLive {
		// The card does not hold the keyboard, so none of its keys exist yet
		// (invariant 5). The host owns the one key that changes that, and
		// everything else belongs to the draft — including enter, which is
		// how a sentence ends.
		return false, nil
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
	}
	return false, nil
}

// View renders the card at the given width, bounded to MaxLines rows.
func (c *ApprovalCard) View(width int) string {
	inner := width - cardFrameWidth
	rows := []string{sty.Headline.Render(c.Headline)}
	// Severity leads the body, as a word beside the first risk. The border
	// and the title chip say the same thing again, which is what makes the
	// card survive mono and a colour-blind reader alike.
	rows = append(rows, c.severityRows()...)
	// The generic variant's one-liner belongs with the headline it qualifies,
	// above the blast-radius block rather than below it.
	if c.Variant == ApprovalGeneric && c.Summary != "" && c.Summary != c.Headline {
		rows = append(rows, sty.Dim.Render(clip(c.Summary, inner)))
	}
	if len(c.Fields) > 0 {
		if len(rows) > 1 {
			rows = append(rows, "")
		}
		for _, f := range c.Fields {
			rows = append(rows, f.render(inner))
		}
	}

	hints := c.hintRowsFor(width, inner)
	// The keys sit below a rule so they never blend into the body.
	hints = append([]string{cardRule}, hints...)

	if c.Variant == ApprovalEdit {
		adds, dels := diff.Stats(c.Hunks)
		line := fmt.Sprintf("+%d −%d · %s", adds, dels, plural(len(c.Hunks), "hunk"))
		if c.Reversibility != "" {
			line += " · " + c.Reversibility
		}
		stats := sty.Dim.Render(line)
		// Frame (2) plus the fixed rows bound how much diff fits.
		budget := 0
		if c.MaxLines > 0 {
			budget = max(c.MaxLines-2-len(rows)-len(hints)-1, 1)
		}
		rows = append(rows, UnifiedLines(c.Hunks, inner,
			UnifiedOpts{LineNumbers: true, Emphasis: true, MaxLines: budget, Syntax: c.Syntax})...)
		rows = append(rows, stats)
	}
	rows = append(rows, hints...)

	title := c.Title
	if c.QueuePos != "" {
		title += " (" + c.QueuePos + ")"
	}
	style := c.Severity.border()
	if c.Uncontained {
		style = sty.Del
	}
	return renderChromeCard(cardChrome{title: title, chips: c.chips(), style: &style}, rows, width)
}

// hintRowsFor is the block under the rule: the decision keys and everything
// that qualifies them. A card that does not hold the keyboard renders that
// block as not-yet-live instead — the consequences of a key nobody can
// press yet are noise, so only the keys and the handover are shown.
func (c *ApprovalCard) hintRowsFor(width, inner int) []string {
	if c.NotYetLive {
		return notYetLiveRows(c.Question+" "+c.keys(), c.Handover, width)
	}
	hint := c.Question + " " + c.keys()
	// What [a] and [d] qualify is part of the offer, not decoration: [a] now
	// names the scope it grants (`always allow "go test"`), which is longer
	// than the word it replaced and is the half a clip would take. So the
	// qualifiers ride beside the keys where the terminal carries them and
	// drop to rows of their own where it does not — the judgement [A] has
	// made for batches, for the same reason.
	var quals []string
	if !c.HeldOnArrival {
		if c.AllowAlways && c.AlwaysHint != "" {
			quals = append(quals, "("+c.AlwaysHint+")")
		}
		if c.FullDiff {
			quals = append(quals, "("+keys.Shown(keys.Decision.Diff)+": "+keys.Words(keys.Decision.Diff)+")")
		}
	}
	qualRow := strings.Join(quals, "  ")
	if qualRow != "" {
		if joined := hint + "  " + qualRow; lipgloss.Width(joined) <= inner {
			hint, qualRow = joined, ""
		}
	}
	segments := append([]string{hint}, c.ExtraHints...)
	if rest := c.arrivalRest(); rest != "" {
		segments = append(segments, rest)
	}
	if c.SafeDefault != "" {
		segments = append(segments, c.SafeDefault)
	}
	hints := hintRows(segments, width)
	if qualRow != "" && len(hints) > 0 {
		// They travel together on one row rather than one row each: they
		// qualify the same key line, and a card is bounded to 40% of the
		// screen — rows spent here are rows the transcript gives up.
		hints = append([]string{hints[0], sty.Hint.Render(clip(qualRow, inner))}, hints[1:]...)
	}
	// [A] gets a row of its own rather than a place in the joined run: the
	// count is the whole offer, and on an 80-column terminal a joined run is
	// exactly where it would be clipped away.
	if c.Batch && c.BatchHint != "" && !c.HeldOnArrival {
		hints = append(hints, sty.Hint.Render(clip(c.BatchHint, inner)))
	}
	if c.Footnote != "" {
		hints = append(hints, sty.Dim.Render(clip(c.Footnote, inner)))
	}
	if c.Return != "" {
		hints = append(hints, sty.Dim.Render(clip(c.Return, inner)))
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
// around — there ctrl+g buys every key, here it buys the ones a sentence
// could have produced by accident.
func (c *ApprovalCard) arrivalRest() string {
	if !c.HeldOnArrival {
		return ""
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
		return "any other key goes to your draft"
	}
	for i, k := range rest {
		rest[i] = "[" + k + "]"
	}
	return "[" + c.Handover + "] for " + strings.Join(rest, "/") +
		" · any other key goes to your draft"
}

// severityRows are the ⚠ rows: the severity word leads the first risk, and
// further risks follow it. A rated card with no risks still states its level,
// because the word is what the border colour means.
func (c *ApprovalCard) severityRows() []string {
	word := c.Severity.Word()
	if word == "" {
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
	var rows []string
	if len(c.Warnings) == 0 {
		return append(rows, style.Render(word))
	}
	rows = append(rows, style.Render(word+"  ")+sty.Dim.Render(c.Warnings[0]))
	for _, w := range c.Warnings[1:] {
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
