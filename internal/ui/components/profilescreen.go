package components

// The profile drafter (docs/interface/surfaces.md#the-profile-drafter).
//
// Drafting a profile is a conversation with a shape: a brief, at most three
// questions, a draft on a card. It used to be run through the transcript —
// the starting points were a numbered list in system text you answered by
// typing a digit into the ordinary input, and the drafter's questions arrived
// as a list to be answered "in one line, in order". Both are the interface
// declining to be one: every other list in the product is a selector, and
// there is no other place where three answers are typed into one line with
// no way to see which one you are on.
//
// So the flow is a surface. It takes the keyboard for as long as it lasts,
// which is what lets a step be typed into and picked from at the same time,
// and it says where in the flow you are — the rail across the top — because a
// flow whose length is not stated is a flow you cannot decide to start.
//
// It is a passive renderer like the rest of this package, with one exception
// it shares with the selector family: the text field and the decision card
// are stateful widgets, so the host holds a pointer and the surface owns the
// keystrokes it is handed. Every fact it draws — what the drafter asked, what
// the draft says, where a file would go — is the host's.
//
// Nothing on it writes anything. The one row that does is on the draft card
// at the end, which is the same rule the scaffold card keeps: a decision gets
// a card, and the card is the last thing in the flow rather than a step in
// it (invariant 2).

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// profileIndent is the column the body starts at, the same one the
	// context surface's panels and the metrics table use.
	profileIndent = 2
	// profileFactLabel is the label column of the draft's fields. It fits
	// "permissions" with a space after it, which is the longest label the
	// host has.
	profileFactLabel = 12
	// profilePromptRows is how much of the drafted prompt the card shows
	// before it scrolls. Six lines is enough to tell a reviewer from a test
	// writer at a glance, which is what the card is for; reading the whole
	// thing is what the scroll keys are for.
	profilePromptRows = 6
)

// ProfileStep is where in the flow the surface is. The four are the flow's
// own states and not a general wizard's: there is no step that can be
// revisited out of order, because each one's answer is what produced the
// next one.
type ProfileStep int

const (
	// ProfileBrief asks what the profile is for, with starting points under
	// the field for a person who has the wish but not the sentence.
	ProfileBrief ProfileStep = iota
	// ProfileQuestions is one of the drafter's questions, asked on its own.
	ProfileQuestions
	// ProfileWorking is the wait while the drafter writes.
	ProfileWorking
	// ProfileDraft is the finished profile above the decision.
	ProfileDraft
)

// ProfileAction is what the person asked the flow to do.
type ProfileAction int

const (
	// ProfileTake carries the brief or one answer in Text.
	ProfileTake ProfileAction = iota
	// ProfileBack unwinds one exchange; from the first, it leaves.
	ProfileBack
	// ProfileSave takes one of the save rows, named by Index into Saves.
	ProfileSave
	// ProfileRefine sends the draft back with the note in Text.
	ProfileRefine
	// ProfileDiscard drops the draft.
	ProfileDiscard
	// ProfileAbort stops a drafting turn that is still running.
	ProfileAbort
)

// ProfileResult is the surface's Update result.
type ProfileResult struct {
	Action ProfileAction
	// Text is the brief, the answer, or the refinement note. An empty answer
	// is a real answer — the person has no preference — and the host is what
	// words it for the drafter.
	Text string
	// Index picks the save row on a ProfileSave.
	Index int
}

// ProfileQA is one question the drafter asked and the answer it got, kept on
// screen above the question being asked now. A flow that forgot what it had
// already been told would be asking the person to hold it in their head.
type ProfileQA struct {
	Question string
	Answer   string
}

// ProfileFact is one field of the drafted profile: what it may touch, which
// model writes it, what it may spend. The tone only makes the field that
// matters findable — the value is always a word (invariant 1).
type ProfileFact struct {
	Label string
	Value string
	// Detail qualifies the value in dim text and is dropped first when the
	// terminal cannot carry both.
	Detail string
	Tone   FieldTone
}

// ProfileDraftView is the profile as the card states it: what it is called,
// what it is for, the fields a decision needs, the drafter's own reason, and
// the profile itself.
type ProfileDraftView struct {
	Name        string
	Description string
	Facts       []ProfileFact
	Why         string
	Prompt      string
}

// headline is the name and what it is for on one line.
func (d ProfileDraftView) headline() string {
	if d.Description == "" {
		return d.Name
	}
	return d.Name + " — " + d.Description
}

// ProfileScreen is the drafting flow's surface.
type ProfileScreen struct {
	// Name is what the header calls the surface — the command that opens it.
	Name string
	// Subject is the dim clause beside it: which session this is and what it
	// already has. The person about to describe a new colleague is exactly
	// the person who wants to know which ones exist.
	Subject string

	Step ProfileStep

	// Ask is the question this step puts, in the drafter's words or the
	// surface's own.
	Ask string
	// Lead introduces the starting points and Starts are the starting
	// points; both are empty once the flow is past the brief.
	Lead   string
	Starts []string
	// FieldLabel names the text field: what is wanted in it.
	FieldLabel string
	// Placeholder is what the empty field says.
	Placeholder string

	// Asked is the exchange so far, and At/Of number the question being
	// asked now. A flow whose length is not stated is one nobody can decide
	// to finish.
	Asked  []ProfileQA
	At, Of int

	// Working is what the drafter is doing, in a word, animated on the
	// session's own frame counter; Elapsed is how long it has been at it.
	Working string
	Frame   int
	Elapsed string

	// Draft is the profile the card is about.
	Draft ProfileDraftView
	// promptLines is the draft's prompt wrapped to the width it was last
	// drawn at.
	promptLines []string
	// Warning is what went wrong with the decision just taken — a file that
	// already exists — shown on the card that is asking again.
	Warning string

	// MaxLines bounds the surface to the pane it is drawn into.
	MaxLines int

	// saves are the rows that write the file, in the order the card offers
	// them. Refine and Discard are the surface's own and are appended to
	// them, so no host has to know where the save rows end.
	saves []SelectOption

	// focus is -1 while the text field has it, and the starting point's
	// index otherwise.
	focus  int
	field  textarea.Model
	decide *NoteSelect
	// scroll is how far into Prompt the draft card's profile pane has been
	// pushed.
	scroll int
	// from is the step the wait was entered from, which is the step the rail
	// keeps showing while it lasts: a drafting turn started from the brief
	// may still come back with questions, so a rail that jumped to the draft
	// the moment the request went out would be promising a step the flow has
	// not reached.
	from ProfileStep
}

// NewProfileScreen builds the surface with its text field. The field is one
// row and takes the draft's newline chords, which is the note field's rule
// and for the note field's reason: the surface answers enter itself.
func NewProfileScreen(name string) *ProfileScreen {
	ta := textarea.New()
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.KeyMap.InsertNewline.SetKeys(keys.Draft.Newline.Keys()[1:]...)
	ta.Focus()
	return &ProfileScreen{Name: name, focus: -1, field: ta}
}

// AskBrief puts the flow on its first step. It drops the exchange with it:
// coming back to the brief is reconsidering the thing the questions were
// asked about, so answers to them are not still true.
func (p *ProfileScreen) AskBrief(ask, lead string, starts []string) {
	p.Step = ProfileBrief
	p.Ask, p.Lead, p.Starts = ask, lead, starts
	p.Asked, p.At, p.Of = nil, 0, 0
	p.FieldLabel, p.Placeholder = "in your own words", "what it is for"
	p.resetField()
}

// AskQuestion puts one of the drafter's questions on screen, numbered.
func (p *ProfileScreen) AskQuestion(question string, at, of int) {
	p.Step = ProfileQuestions
	p.Ask, p.Lead, p.Starts = question, "", nil
	p.At, p.Of = at, of
	p.FieldLabel, p.Placeholder = "your answer", "enter alone says you have no preference"
	p.resetField()
}

// Answered records an exchange, so the questions already answered stay on
// screen under the ones still being asked.
func (p *ProfileScreen) Answered(question, answer string) {
	p.Asked = append(p.Asked, ProfileQA{Question: question, Answer: answer})
}

// Forget drops the last exchange, for a step back onto the question that
// produced it.
func (p *ProfileScreen) Forget() {
	if len(p.Asked) > 0 {
		p.Asked = p.Asked[:len(p.Asked)-1]
	}
}

// SetText puts text in the field with the cursor after it, for a step the
// person is coming back to rather than meeting.
func (p *ProfileScreen) SetText(text string) {
	p.field.SetValue(text)
}

// Warn states what went wrong with the decision just taken, on the card that
// is asking again. It is cleared by the next Show, because the next draft is
// not the one the warning was about.
func (p *ProfileScreen) Warn(text string) { p.Warning = text }

// Work puts the surface on the wait while the drafter writes.
func (p *ProfileScreen) Work(doing string) {
	if p.Step != ProfileWorking {
		p.from = p.Step
	}
	p.Step = ProfileWorking
	p.Working = doing
	p.field.Blur()
}

// Show puts the finished draft up over the decision. saves are the rows that
// write it; the card appends its own refine and discard.
func (p *ProfileScreen) Show(draft ProfileDraftView, saves []SelectOption) {
	p.Step = ProfileDraft
	p.Draft = draft
	p.Warning = ""
	p.saves = saves
	p.scroll = 0
	p.field.Blur()
	options := append([]SelectOption{}, saves...)
	options = append(options,
		SelectOption{Label: "Refine", Desc: "say what to change in the note", RequireNote: true},
		SelectOption{Label: "Discard"})
	// The card's title names the profile, so the question stays answerable
	// on a terminal too short to keep the headline above it: "keep this
	// profile" without saying which one is a decision with the subject
	// removed.
	p.decide = NewNoteSelect("Keep "+draft.Name+"?", options)
	p.decide.Note.Placeholder = "what to change (for Refine)"
}

// Note is the refinement note as it stands, which a host that reopens the
// card after a failed save carries over.
func (p *ProfileScreen) Note() string {
	if p.decide == nil {
		return ""
	}
	return strings.TrimSpace(p.decide.Note.Value())
}

// resetField empties the field and puts the cursor back in it.
func (p *ProfileScreen) resetField() {
	p.field.Reset()
	p.field.Placeholder = p.Placeholder
	p.focus = -1
	p.field.Focus()
}

// Update routes one keystroke to the step that is up.
func (p *ProfileScreen) Update(msg tea.KeyPressMsg) (done bool, result ProfileResult) {
	switch p.Step {
	case ProfileBrief:
		return p.updateBrief(msg)
	case ProfileQuestions:
		return p.updateQuestion(msg)
	case ProfileWorking:
		// A wait offers one key, and it is the way out of the wait rather
		// than out of the flow: the drafting is stopped and the step that
		// started it comes back (invariant 5 — the surface holds the
		// keyboard, so it says what the one live key does).
		if keys.Is(msg.String(), keys.Profile.Back) {
			return true, ProfileResult{Action: ProfileAbort}
		}
		return false, ProfileResult{}
	default:
		return p.updateDraft(msg)
	}
}

// updateBrief answers the first step: the field has the keyboard, the
// starting points are under it, and ↑↓ is what moves between them. The
// arrows and not j/k, because everything else on this step is text.
func (p *ProfileScreen) updateBrief(msg tea.KeyPressMsg) (bool, ProfileResult) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Profile.Back):
		return true, ProfileResult{Action: ProfileBack}
	case keys.Is(pressed, keys.Profile.Take):
		// A brief is the one answer the flow cannot supply for itself, so
		// enter with nothing to take does nothing rather than starting a
		// drafting from an empty sentence.
		if text := p.taken(); text != "" {
			return true, ProfileResult{Action: ProfileTake, Text: text}
		}
		return false, ProfileResult{}
	case pressed == "up":
		p.moveFocus(-1)
		return false, ProfileResult{}
	case pressed == "down":
		p.moveFocus(1)
		return false, ProfileResult{}
	}
	if p.focus < 0 {
		p.field, _ = p.field.Update(msg)
	}
	return false, ProfileResult{}
}

// updateQuestion answers one of the drafter's questions. There is nothing to
// pick here, so every key that is not the answer or the way back is text.
func (p *ProfileScreen) updateQuestion(msg tea.KeyPressMsg) (bool, ProfileResult) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Profile.Back):
		return true, ProfileResult{Action: ProfileBack}
	case keys.Is(pressed, keys.Profile.Take):
		// An empty answer is an answer: someone who has no preference about
		// which languages a reviewer covers should not be held at the
		// question until they invent one.
		return true, ProfileResult{Action: ProfileTake, Text: strings.TrimSpace(p.field.Value())}
	}
	p.field, _ = p.field.Update(msg)
	return false, ProfileResult{}
}

// updateDraft answers the card at the end: the profile scrolls under the
// decision, and the decision is the note-selector every other card of this
// shape uses.
func (p *ProfileScreen) updateDraft(msg tea.KeyPressMsg) (bool, ProfileResult) {
	if p.decide == nil {
		return true, ProfileResult{Action: ProfileDiscard}
	}
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Profile.ScrollUp):
		p.scroll = max(p.scroll-1, 0)
		return false, ProfileResult{}
	case keys.Is(pressed, keys.Profile.ScrollDown):
		p.scroll = min(p.scroll+1, max(len(p.promptLines)-profilePromptRows, 0))
		return false, ProfileResult{}
	}
	done, res := p.decide.Update(msg)
	if !done {
		return false, ProfileResult{}
	}
	if res.Canceled {
		return true, ProfileResult{Action: ProfileDiscard}
	}
	switch {
	case res.Index < len(p.saves):
		return true, ProfileResult{Action: ProfileSave, Index: res.Index}
	case res.Index == len(p.saves):
		return true, ProfileResult{Action: ProfileRefine, Text: res.Note}
	}
	return true, ProfileResult{Action: ProfileDiscard}
}

// taken is what enter takes on the brief step: what has been typed, or the
// starting point the pointer is on.
func (p *ProfileScreen) taken() string {
	if p.focus >= 0 && p.focus < len(p.Starts) {
		return p.Starts[p.focus]
	}
	return strings.TrimSpace(p.field.Value())
}

// moveFocus walks the pointer between the field and the starting points. The
// field is above the list rather than a row in it, so leaving the top of the
// list is how you get back to typing.
func (p *ProfileScreen) moveFocus(delta int) {
	next := min(max(p.focus+delta, -1), len(p.Starts)-1)
	if len(p.Starts) == 0 {
		next = -1
	}
	p.focus = next
	if p.focus < 0 {
		p.field.Focus()
	} else {
		p.field.Blur()
	}
}

// SetSize gives the surface the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (p *ProfileScreen) SetSize(_, height int) { p.MaxLines = height }

// View renders the surface at the given width: the shared chrome with the
// step rail pinned under its rule, and the flow in the rows it leaves.
func (p *ProfileScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	// The profile is wrapped here rather than by the host: how wide the pane
	// is is a fact of the render, and a host that guessed it would be the
	// reason a line ends in an ellipsis on a terminal wide enough to hold it.
	p.promptLines = wrapBlock(p.Draft.Prompt, width-profileIndent-2)
	return ScreenChrome{
		Header:   p.header(),
		Head:     []string{p.railRow(width), ""},
		MaxLines: p.MaxLines,
	}.View(width, func(budget int) []string { return p.bodyRows(width, budget) })
}

// header names the surface, what it is drafting into, and the way out.
func (p *ProfileScreen) header() ScreenHeader {
	h := ScreenHeader{
		Left: []RailSegment{screenTitle(p.Name)},
		Keys: words(keys.Profile.Back, p.wayOutWords()),
	}
	if p.Subject != "" {
		h.Left = append(h.Left, screenField(p.Subject))
	}
	return h
}

// wayOutWords is what esc does from where the flow is standing. A takeover
// states its way out (invariant 5), and on a flow that is four different
// sentences: leaving before anything was drafted, unwinding one answer,
// stopping a drafting turn, and dropping a finished draft are not the same
// act and must not be worded as if they were.
func (p *ProfileScreen) wayOutWords() string {
	switch {
	case p.Step == ProfileWorking:
		return "stop drafting"
	case p.Step == ProfileDraft:
		return "discard the draft"
	case p.Step == ProfileQuestions && len(p.Asked) > 0:
		return "back a step"
	}
	return "leave"
}

// railRow is the flow drawn as a flow: which steps are behind you, which one
// you are on, which are still to come. The glyphs are the manager's own — ✓
// for finished, ● for the one you are on — so a reader who knows the agent
// list already knows this, and the word beside each carries the meaning on a
// terminal with no colour (invariant 1).
func (p *ProfileScreen) railRow(width int) string {
	names := []string{"brief", "questions", "draft"}
	at := p.railStep()
	var parts []string
	for i, name := range names {
		switch {
		case i == 1 && i < at && p.Of == 0:
			// A brief that was already a specification gets a draft and no
			// questions, and the rail says which happened: ⊘ is the glyph
			// for a step that was skipped, and a ✓ over a step nobody was
			// asked would be the rail claiming an exchange that never
			// happened.
			parts = append(parts, sty.Dimmer.Render("⊘ "+name))
		case i < at:
			parts = append(parts, sty.Dim.Render("✓ "+name))
		case i == at:
			parts = append(parts, sty.Info.Render("● "+name))
		default:
			parts = append(parts, sty.Dimmer.Render("· "+name))
		}
	}
	return Clip(strings.Repeat(" ", profileIndent)+strings.Join(parts, sty.Dimmer.Render("   ")), width)
}

// railStep is which of the rail's three the surface is on. The wait belongs
// to the step it will land on — a person waiting on a first draft is on their
// way to the draft — because a rail with a fourth position for "waiting"
// would be counting the flow's mechanics rather than its exchanges.
func (p *ProfileScreen) railStep() int {
	step := p.Step
	if step == ProfileWorking {
		step = p.from
	}
	switch step {
	case ProfileBrief:
		return 0
	case ProfileQuestions:
		return 1
	default:
		return 2
	}
}

// bodyRows is the step that is up, trimmed to the budget.
func (p *ProfileScreen) bodyRows(width, budget int) []string {
	var rows []string
	switch p.Step {
	case ProfileBrief:
		rows = p.briefRows(width)
	case ProfileQuestions:
		rows = p.questionRows(width)
	case ProfileWorking:
		rows = p.workingRows(width)
	default:
		rows = p.draftRows(width, budget)
	}
	rows = append(rows, p.hintFor(width)...)
	if budget <= 0 || len(rows) <= budget {
		return rows
	}
	return rows[:budget]
}

// briefRows is the first step: the question, the field, the starting points,
// and what the session already has.
func (p *ProfileScreen) briefRows(width int) []string {
	rows := p.askRows(width)
	rows = append(rows, p.fieldRows(width)...)
	if len(p.Starts) > 0 {
		rows = append(rows, "")
		if p.Lead != "" {
			rows = append(rows, Clip(indent(sty.Dim.Render(p.Lead)), width))
		}
		rows = append(rows, p.startRows(width)...)
	}
	return append(rows, "")
}

// briefHint is the first step's key row. It leads with what enter takes,
// because which of the two the pointer is on is the one thing about this step
// that is not obvious from looking at it.
func (p *ProfileScreen) briefHint() []string {
	take := words(keys.Profile.Take, "draft from what you typed")
	if p.focus >= 0 {
		take = words(keys.Profile.Take, "draft from this one")
	}
	segments := []string{take}
	if len(p.Starts) > 0 {
		segments = append(segments, words(keys.Profile.Move, "the field or a starting point"))
	}
	return append(segments, words(keys.Profile.Back, "nothing is drafted"))
}

// questionRows is one question with the answers already given above it. The
// answered run is dim and the question is not: what is being asked now is
// what the eye should land on.
func (p *ProfileScreen) questionRows(width int) []string {
	var rows []string
	for _, qa := range p.Asked {
		answer := qa.Answer
		if answer == "" {
			answer = "no preference"
		}
		rows = append(rows,
			Clip(indent(sty.Dim.Render("✓ "+qa.Question)), width),
			Clip(indent(sty.Dimmer.Render("  "+answer)), width))
	}
	if len(rows) > 0 {
		rows = append(rows, "")
	}
	if p.Of > 0 {
		rows = append(rows, Clip(indent(sty.Dim.Render(fmt.Sprintf("question %d of %d", p.At, p.Of))), width))
	}
	rows = append(rows, p.askRows(width)...)
	rows = append(rows, p.fieldRows(width)...)
	return append(rows, "")
}

// backWords is what esc does from where the flow is standing. It says which
// of the two it is, because "back" on the first question and "back" on the
// third are a cancelled drafting and a corrected answer.
func (p *ProfileScreen) backWords() string {
	if len(p.Asked) == 0 {
		return "nothing is drafted"
	}
	return "back to the last answer"
}

// workingRows is the wait: the label in motion and how long it has been.
func (p *ProfileScreen) workingRows(width int) []string {
	label := Anim{Frame: p.Frame, Label: p.Working, Lead: Spinner{Frame: p.Frame}.Glyph() + " "}
	if p.Elapsed != "" {
		label.Suffix = sty.Dim.Render("  " + p.Elapsed)
	}
	return []string{Clip(indent(label.View()), width), ""}
}

// hintFor is the step's key row. Nothing on a key row is ever truncated
// (invariant 4), so a row too long for the terminal stacks its segments
// rather than losing one to a clip — the rule every card's hints keep.
func (p *ProfileScreen) hintFor(width int) []string {
	var segments []string
	switch p.Step {
	case ProfileBrief:
		segments = p.briefHint()
	case ProfileQuestions:
		segments = []string{
			words(keys.Profile.Take, "answer it"),
			words(keys.Profile.Back, p.backWords()),
		}
	case ProfileWorking:
		segments = []string{words(keys.Profile.Back, "stop drafting")}
	default:
		// The draft step's keys are on the card, which draws its own row.
		return nil
	}
	rows := hintRows(segments, width-profileIndent)
	for i, row := range rows {
		rows[i] = Clip(indent(row), width)
	}
	return rows
}

// draftRows is the finished profile over the decision: what it is, what it
// may touch, why it was drawn this way, the profile itself, and the card.
//
// The card is what the surface is for, so it is the one thing that never
// gives ground — a decision whose keys were cut off by the height is not one.
// Everything above it gives way in order: the profile pane shrinks first (it
// still counts what it is holding back), then the drafter's why, then the
// fields from the bottom, because the permission line is the one a reader
// must not decide without and the budget is the one they can look up.
func (p *ProfileScreen) draftRows(width, budget int) []string {
	card := p.cardRows(width, budget)
	// The blank row between the profile and the card comes off the room the
	// rest has, so what is above the card is never measured as if the card
	// started one row lower than it does.
	room := 0
	if budget > 0 {
		room = max(budget-len(card)-1, 0)
	}
	facts := p.factRows(width)
	pane, why := profilePromptRows, p.Draft.Why != ""
	above := func() []string {
		rows := []string{Clip(indent(sty.Body.Render(p.Draft.headline())), width), ""}
		rows = append(rows, facts...)
		if why {
			rows = append(rows, "", Clip(indent(sty.Dim.Render("why  ")+sty.Status.Render(p.Draft.Why)), width))
		}
		if p.Warning != "" {
			rows = append(rows, "", Clip(indent(sty.Warn.Render("⚠ "+p.Warning)), width))
		}
		if pane >= 0 {
			rows = append(rows, "")
			rows = append(rows, p.promptRows(width, pane)...)
		}
		return rows
	}
	rows := above()
	for budget > 0 && len(rows) > room {
		// The ladder: the profile pane shrinks to one line and then goes,
		// then the drafter's why, then the fields from the bottom — the
		// permission line is the one nobody should decide without and the
		// budget is the one they can look up. The headline is what is left,
		// because a card asking about a profile has to name it.
		switch {
		case pane > 1:
			pane--
		case why:
			why = false
		case len(facts) > 1:
			facts = facts[:len(facts)-1]
		case pane >= 0:
			pane = -1
		case len(facts) > 0:
			facts = nil
		default:
			return append(append(clipRows(rows, room), ""), card...)
		}
		rows = above()
	}
	return append(append(rows, ""), card...)
}

// clipRows drops rows from the bottom to fit, for the surface too short even
// for the headline and the card.
func clipRows(rows []string, room int) []string {
	if room <= 0 {
		return nil
	}
	return rows[:min(len(rows), room)]
}

// cardRows is the decision, windowed to the height when even it does not fit:
// the selector's own window is what a card too tall for its surface has
// always done, and it counts the rows it is not showing.
func (p *ProfileScreen) cardRows(width, budget int) []string {
	if p.decide == nil {
		return nil
	}
	p.decide.Select.MaxLines = 0
	if budget > 0 {
		p.decide.Select.MaxLines = budget
	}
	return strings.Split(p.decide.View(width), "\n")
}

// factRows is the draft's fields in a column, the way a card's blast radius
// is drawn: label, value, and the detail that qualifies it.
func (p *ProfileScreen) factRows(width int) []string {
	rows := make([]string, 0, len(p.Draft.Facts))
	for _, f := range p.Draft.Facts {
		row := indent(sty.Dim.Render(padRight(f.Label, profileFactLabel)) + f.Tone.style().Render(f.Value))
		if f.Detail != "" {
			if full := row + sty.Dim.Render("  "+f.Detail); lipgloss.Width(full) <= width {
				row = full
			}
		}
		rows = append(rows, Clip(row, width))
	}
	return rows
}

// promptRows is the profile itself under a fold that counts what it is
// holding back (invariant 4) and says which key opens it.
func (p *ProfileScreen) promptRows(width, pane int) []string {
	head := indent(sty.Dim.Render("┄ the profile"))
	if len(p.promptLines) == 0 || pane <= 0 {
		return []string{Clip(head, width)}
	}
	p.scroll = min(p.scroll, max(len(p.promptLines)-pane, 0))
	end := min(p.scroll+pane, len(p.promptLines))
	rows := []string{Clip(head, width)}
	for _, line := range p.promptLines[p.scroll:end] {
		rows = append(rows, Clip(indent("  "+sty.Status.Render(line)), width))
	}
	if rest := len(p.promptLines) - end; rest > 0 {
		rows = append(rows, Clip(indent("  "+sty.Dim.Render(fmt.Sprintf("⋮ %s · %s",
			plural(rest, "more line"), words(keys.Profile.ScrollDown, "read on")))), width))
	}
	return rows
}

// askRows is the step's question, wrapped by the caller and drawn as the one
// bright thing on the step.
func (p *ProfileScreen) askRows(width int) []string {
	if p.Ask == "" {
		return nil
	}
	var rows []string
	for _, line := range wrapBlock(p.Ask, width-profileIndent) {
		rows = append(rows, Clip(indent(sty.Body.Render(line)), width))
	}
	return append(rows, "")
}

// fieldRows is the text field: the label that names what is wanted, and the
// field under it. It is the note field's shape rather than a box of its own,
// because a second kind of text input would be a second thing to learn.
func (p *ProfileScreen) fieldRows(width int) []string {
	inner := max(width-profileIndent-2, 8)
	p.field.SetWidth(inner)
	view := p.field.View()
	if p.focus >= 0 {
		// Unfocused, the field echoes as plain text: a blurred textarea
		// still draws cursor artifacts, and the pointer is in the list.
		text := strings.TrimSpace(p.field.Value())
		if text == "" {
			text = "(nothing typed)"
		}
		view = sty.Dimmer.Render(Clip(text, inner))
	}
	rows := []string{Clip(indent(sty.Dim.Render("┄ "+p.FieldLabel)), width)}
	for _, line := range strings.Split(view, "\n") {
		rows = append(rows, Clip(indent("  "+line), width))
	}
	return rows
}

// startRows is the starting points, drawn as the start screen draws its
// offers: a pointer outside the highlight, and the focused row lit whole.
func (p *ProfileScreen) startRows(width int) []string {
	rows := make([]string, 0, len(p.Starts))
	for i, start := range p.Starts {
		if i == p.focus {
			rows = append(rows, Clip(sty.FocusPointer.Render("❯ ")+sty.FocusRow.Render(Clip(start, max(width-2, 1))), width))
			continue
		}
		rows = append(rows, Clip("  "+sty.Status.Render(start), width))
	}
	return rows
}

// wrapBlock wraps text to a width, keeping the breaks the text already has:
// a profile written in paragraphs is read in paragraphs, and joining them
// would be the surface editing what the drafter wrote.
func wrapBlock(text string, width int) []string {
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		if strings.TrimSpace(para) == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, wrapPlain(para, width)...)
	}
	return lines
}

// indent puts a row in the body's column.
func indent(row string) string { return strings.Repeat(" ", profileIndent) + row }
