package components

// The commit card, and the message editor behind its edit key
// (docs/interface/surfaces.md#the-turns-close).
//
// A commit is the one act of a turn that cannot be taken back, so it is put
// on a card rather than answered from a row: the message that will be
// written, what will be staged, what will deliberately not be, the branch it
// lands on, whether the checkout's own hooks run, and the fact that nothing
// is pushed. Five statements is what it takes to press enter on a commit
// without going to look anything up
// (docs/capabilities/approvals-and-safety.md#the-writing-half-of-git-is-a-tool-too).
//
// It is a passive renderer like every other card here. What the fields say is
// the host's reading of the repository; what this owns is the shape.

import (
	"fmt"

	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// commitTitle is the card's own title. It names the subject rather than the
// verb — the keys under the rule carry the verb — so the border says which
// commit is being proposed and not merely that one is.
const commitTitle = "Commit this turn"

// subjectBudget is the subject-line convention the message editor counts down
// from, and bodyWrap the width a body is wrapped at. They are the shape git's
// own tooling and every log viewer assume, and the editor states them rather
// than enforcing them: a message that runs long is a judgement the writer is
// allowed to make, and a field that refused the fifty-first character would
// be making it for them.
const (
	subjectBudget = 50
	bodyWrap      = 72
)

// CommitCard is the card the changed-files row opens: the message, what the
// commit will carry, and the four facts about where it lands.
type CommitCard struct {
	// Message is the proposed subject line, read from the changeset and the
	// turn's own title by the host.
	Message string
	// Files, Added and Removed are what will be staged — this turn's own
	// changeset and nothing else.
	Files          int
	Added, Removed int
	// StagesNote qualifies the counts, e.g. `exactly what this turn
	// changed`.
	StagesNote string
	// Fields are the statements under the counts: what is left out of the
	// commit, the branch, the hooks, and the push that never happens. They
	// are the blast-radius block's rows, because the question a commit card
	// answers is the question an approval card answers.
	Fields []CardField
	// Failure is why the last attempt made no commit — a hook that exited
	// non-zero, a git that is not there. It stands on the card because the
	// card is where the answer was given, and it says the changeset survived.
	Failure string
	// Running says the commit has been asked for and has not come back. The
	// card stays up with its keys drawn and none of them live, which is the
	// same grammar every other surface uses for keys that cannot be pressed
	// yet (card.go) — and the reason it stays up rather than closing
	// optimistically is that the answer is still owed: a hook can refuse,
	// and a card that had already gone would have nowhere to say so.
	Running bool
}

// View renders the card at the given width.
func (c CommitCard) View(width int) string {
	inner := Card{}.Inner(width)
	rows := []string{c.messageRow(inner), ""}
	rows = append(rows, c.stagesRow(inner))
	for _, f := range c.Fields {
		rows = append(rows, f.render(inner))
	}
	if c.Failure != "" {
		rows = append(rows, "", sty.Err.Render(Clip("✗ "+c.Failure, inner)))
	}
	rows = append(rows, cardRule)
	if c.Running {
		rows = append(rows, deadRows(plainCommitRun(), committingWords, width)...)
	} else {
		rows = append(rows, runRows(commitRun(), inner)...)
		rows = append(rows, commitEscRow(inner))
	}
	// The tone is the mutation rail's, the same Accent the changed-files row
	// that offered this key is drawn in, and the chip says the level in
	// words beside it (invariant 1). A commit is rated rather than unrated
	// because it is a write, and low rather than higher because everything
	// it carries was already written to the tree — the card is about where
	// the work goes, not about whether it happens.
	style := SeverityLow.tone()
	return Card{
		Title: commitTitle,
		Chips: []string{SeverityLow.Word()},
		Style: &style,
	}.Render(rows, width)
}

// messageRow is the first body row, in the shape every card's first row has:
// the kind of act in the accent every glyph column carries, and the act
// itself bright, because it is the one thing on the card a reader has to read
// before answering (ApprovalCard.actRow).
//
// The glyph is `$`, the same one a commit's own activity row and receipt
// carry, so the card and the row it will leave behind say the same thing
// about the same act. There is no mutation rail beside it: a rail marks the
// rows that changed something among rows that did not, and a card has no
// neighbours to be marked out from
// (docs/interface/principles.md#weight-tracks-risk).
func (c CommitCard) messageRow(inner int) string {
	return sty.Accent.Render("$") + " " + sty.Bright.Render(Clip(c.Message, max(inner-2, 1)))
}

// stagesRow is the first field, laid out in the same label column as the
// others but with its counts in the diff's own two colours rather than in one
// tone — the counts are the whole of what a reader checks this row for.
func (c CommitCard) stagesRow(inner int) string {
	label := sty.Status.Render(padRight("stages", fieldLabelWidth-1) + " ")
	head := label + sty.Body.Render(plural(c.Files, "file")+" ") + DiffStat(c.Added, c.Removed)
	if c.StagesNote == "" {
		return head
	}
	note := " — " + c.StagesNote
	if lipgloss.Width(head)+lipgloss.Width(note) > inner {
		return head
	}
	return head + sty.Dimmer.Render(note)
}

// commitRun is the card's decision keys, drawn from the register so the
// spelling offered is the spelling answered. The staging key carries the
// surface's own words after it, because what `[s]` opens is the review the
// product already has and a reader should be told that before pressing it.
func commitRun() []string {
	return []string{
		offerSegment(keys.Bracket(keys.Commit.Take), keys.Words(keys.Commit.Take)),
		offerSegment(keys.Bracket(keys.Commit.Edit), keys.Words(keys.Commit.Edit)),
		offerSegment(keys.Bracket(keys.Commit.Hunks), keys.Words(keys.Commit.Hunks)+" — the review surface"),
	}
}

// committingWords is what the key row says while git has the question. It is
// words rather than a dimming alone, for the reason every other dead key row
// in this package states its state in words
// (docs/interface/principles.md#colour-never-carries-meaning-alone). Esc is
// not among the keys, and deliberately: a commit that has been asked for is
// being made or refused, and a key promising to take it back before either
// has happened would be promising something nothing here can deliver.
const committingWords = "committing — git has it"

// plainCommitRun is the card's keys unpainted, for the row that draws the
// whole run in one grey because none of them is live.
func plainCommitRun() []string {
	return []string{
		keys.Bracket(keys.Commit.Take) + " " + keys.Words(keys.Commit.Take),
		keys.Bracket(keys.Commit.Edit) + " " + keys.Words(keys.Commit.Edit),
		keys.Bracket(keys.Commit.Hunks) + " " + keys.Words(keys.Commit.Hunks),
	}
}

// commitEscRow is the row the card ends on. Esc is the safe answer here in
// the fullest sense the product has: nothing was written, the changeset is
// where it was, and the key that opened this card is still on the row
// (docs/interface/principles.md#esc-is-always-the-safe-answer).
func commitEscRow(inner int) string {
	esc := keys.Shown(keys.Commit.Cancel)
	words := keys.Words(keys.Commit.Cancel) + " — the changeset stays, and so does the offer"
	return Clip(safeSegment(esc, fitClauses("["+esc+"] ", words, inner)), inner)
}

// CommitMessage is the proposed message opened as a draft: the field under a
// labelled rail the host draws, in a frame whose title is the subject-line
// budget counting down.
//
// The budget is the title because that is where a number belongs that the
// writer needs while typing and never has to act on. Put in the body it would
// be a row that says nothing about the message; put after the text it would
// move as the text grew.
type CommitMessage struct {
	// Subject is the first line of what has been typed, which is what the
	// budget is counted against. A body under it is the writer's business
	// and is not counted.
	Subject string
	// Rows are the field's own rendered lines, cursor included, which the
	// host owns because the textarea is where the keyboard is.
	Rows []string
}

// budgetTitle is the frame's title: what is left of the subject line, and the
// width a body wraps at. It counts down and then says how far past it the
// subject has gone, because a budget that stopped at zero would leave the one
// case the writer wants a number for unnumbered.
func (c CommitMessage) budgetTitle() string {
	left := subjectBudget - lipgloss.Width(c.Subject)
	if left < 0 {
		return fmt.Sprintf("%d over · a body wraps at %d", -left, bodyWrap)
	}
	return fmt.Sprintf("%d · a body wraps at %d", left, bodyWrap)
}

// View renders the editor at the given width.
//
// The keys are rows under a rule rather than labels on the bottom border the
// artboard drew them on. A card is a box-drawing rectangle and its border
// carries the title and the chips; a second kind of label on a second edge
// would be a notation this product has nowhere else, and the offers are the
// one thing on this surface that must not be read as decoration
// (docs/interface/principles.md#fold-never-hide).
func (c CommitMessage) View(width int) string {
	inner := Card{}.Inner(width)
	rows := make([]string, 0, len(c.Rows)+4)
	for _, r := range c.Rows {
		rows = append(rows, sty.Info.Render("▸ ")+r)
	}
	rows = append(rows, cardRule,
		offerSegment(keys.Bracket(keys.Select.Take), "commit with this"))
	esc := keys.Shown(keys.Select.Cancel)
	rows = append(rows, Clip(safeSegment(esc,
		fitClauses("["+esc+"] ", "back to the card, edits kept", inner)), inner))
	// The frame is Add rather than the decision tone: what is inside it is
	// the text that will be written, and the surface is the one place in
	// this flow where nothing is being decided — the decision was taken on
	// the card and comes back to it.
	style := sty.Add
	return Card{Title: c.budgetTitle(), Style: &style}.Render(rows, width)
}
