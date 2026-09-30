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
	// profileBodyIndent is the column a section's prose starts at: under its
	// heading, two further in, so the headings are a column the eye runs
	// down to find a section.
	profileBodyIndent = 4
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
	// ProfileRefine asks the drafter to rewrite the section Index names,
	// with the note in Text; the other sections go with it unchanged.
	ProfileRefine
	// ProfileRefineAll asks the drafter to rewrite every prose section on
	// the note in Text. Include says the sections the person wrote go too;
	// without it they are sent unchanged with the fields.
	ProfileRefineAll
	// ProfileEdit hands the section Index names to the person's editor.
	ProfileEdit
	// ProfileClear empties the section Index names.
	ProfileClear
	// ProfileUndo takes back the last revision of the section Index names.
	ProfileUndo
	// ProfilePick is enter on a section that is a set of fields rather than
	// prose and offers a selector, named by Index. The host answers it by
	// opening the selector (OpenPicker).
	ProfilePick
	// ProfileDiscard drops the draft.
	ProfileDiscard
	// ProfileAbort stops a drafting turn that is still running.
	ProfileAbort
	// ProfilePicked is enter on the open selector: the host reads the boxes
	// off the Picker it opened.
	ProfilePicked
	// ProfileUnpicked is esc on the open selector: the section stays as it
	// was.
	ProfileUnpicked
)

// ProfileResult is the surface's Update result.
type ProfileResult struct {
	Action ProfileAction
	// Text is the brief, the answer, or the refinement note. An empty answer
	// is a real answer — the person has no preference — and the host is what
	// words it for the drafter.
	Text string
	// Index picks the save row on a ProfileSave, and the section on the acts
	// that revise one.
	Index int
	// Include is a whole-draft note's second press: the sections the person
	// wrote are redrafted with the rest rather than kept.
	Include bool
}

// ProfileQA is one question the drafter asked and the answer it got, kept on
// screen above the question being asked now. A flow that forgot what it had
// already been told would be asking the person to hold it in their head.
type ProfileQA struct {
	Question string
	Answer   string
}

// ProfileMarkTone is what has happened to a section, as the mark after its
// heading colours it. The words carry it too (invariant 1): the tone only
// makes the section that is the person's own, or the one that is empty,
// findable.
type ProfileMarkTone int

const (
	// ProfileMarkQuiet is a revision the drafter made: dim, because it is
	// the ordinary thing to have happened.
	ProfileMarkQuiet ProfileMarkTone = iota
	// ProfileMarkMine is a section the person wrote themselves, in the add
	// tone, because it is now theirs and no refine may touch it.
	ProfileMarkMine
	// ProfileMarkEmpty is a section with nothing in it, in the del tone.
	ProfileMarkEmpty
)

// ProfileSection is one block of the draft: a prose section of the prompt,
// or one of the fields the file carries beside it. A prose section is the
// only kind the person revises here; a field block is drawn as its value
// and what that value means.
type ProfileSection struct {
	// Name is the heading.
	Name string
	// Body is a prose section's text, wrapped as it is drawn.
	Body string
	// Value, Tone and Detail are a field block's line: the value in its
	// tone, and the clause that qualifies it dim after it.
	Value  string
	Tone   FieldTone
	Detail string
	// Mark is what has happened to the section, drawn after the heading.
	Mark     string
	MarkTone ProfileMarkTone
	// Prose says the section is text the person revises with a note, in the
	// editor, or by clearing it.
	Prose bool
	// Revised says the section has a revision esc can take back.
	Revised bool
	// Mine says the person wrote the section themselves, which a note on the
	// whole draft keeps unless its second press says otherwise.
	Mine bool
	// Whole says the block is the row a note on the whole draft left under
	// the sections rather than a section: esc on it takes that revision back
	// from every section it changed.
	Whole bool
	// Pick is what enter does on a field block that opens a selector, in
	// the words its key row offers it under; empty is a field block enter
	// does nothing on.
	Pick string
	// Revisable says a field block is revised the way a prose section is —
	// a note, the editor, clearing it — while it is still drawn as its
	// value line. Body then holds the text the editor opens on, and is
	// empty when there is nothing to clear.
	Revisable bool
}

// revisable says the section takes the prose acts: a note, the editor and
// clearing.
func (s ProfileSection) revisable() bool { return s.Prose || s.Revisable }

// ProfileDraftView is the profile as the draft step states it: what it is
// called, what it is for, and its sections in the order the file keeps them.
type ProfileDraftView struct {
	Name        string
	Description string
	Sections    []ProfileSection
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
	// Warning is what went wrong with the decision just taken — a file that
	// already exists, a revision that did not land — shown on the draft that
	// is asking again.
	Warning string

	// MaxLines bounds the surface to the pane it is drawn into.
	MaxLines int

	// saves are the rows that write the file, in the order the card offers
	// them. Discard is the surface's own and is appended to them, so no host
	// has to know where the save rows end.
	saves []SelectOption

	// focus is -1 while the text field has it, and the starting point's
	// index otherwise.
	focus  int
	field  textarea.Model
	decide *Select
	// Picker is the selector a field block opened, drawn where the card is
	// and holding the keyboard until it is taken or cancelled. The host
	// builds it and reads its boxes; the surface only routes and draws it.
	Picker *MultiSelect
	// section is the block the pointer is on, and card says the card rather
	// than the sections has the keyboard; tab moves it between the two
	// (docs/interface/surfaces.md#the-profile-drafter).
	section int
	card    bool
	// refining is the note open under the selected section: the field has
	// the keyboard, and enter sends it.
	refining bool
	// whole says the note, or the wait after it, is about the whole draft
	// rather than the selected section, and is drawn under the section list;
	// include is its second press, and sent the note as it went to the
	// drafter, which the wait keeps on screen above itself.
	whole   bool
	include bool
	sent    string
	// pane is the offset into the sections' rows, held as a Pager like every
	// other scrolling body's here, so the fold under it counts through the
	// same arithmetic the rest of the package folds through
	// (docs/interface/principles.md#fold-never-hide). reveal asks the next
	// draw to bring the selected section inside it: a move puts it there,
	// and a scroll the reader asked for does not snap back.
	pane   Pager
	reveal bool
	// from is the step the wait was entered from, which is the step the rail
	// keeps showing while it lasts: a drafting turn started from the brief
	// may still come back with questions, so a rail that jumped to the draft
	// the moment the request went out would be promising a step the flow has
	// not reached.
	from ProfileStep
	// keys reports that `?` has the register and the glyph legend showing
	// under the step.
	keys bool
}

// NewProfileScreen builds the surface with its text field. The field is one
// row and takes the draft's newline chords, which is the note field's rule
// and for the note field's reason: the surface answers enter itself.
func NewProfileScreen(name string) *ProfileScreen {
	ta := NewTextArea()
	ta.SetHeight(1)
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

// Warn states what went wrong with the decision just taken, on the draft that
// is asking again. It is cleared by the next Show, because the next draft is
// not the one the warning was about.
func (p *ProfileScreen) Warn(text string) { p.Warning = text }

// Work puts the surface on the wait while the drafter writes. A wait entered
// from the draft is one section being redrafted, and it is drawn under that
// section rather than in place of the draft: the other sections stand.
func (p *ProfileScreen) Work(doing string) {
	if p.Step != ProfileWorking {
		p.from = p.Step
	}
	p.Step = ProfileWorking
	p.Working = doing
	p.refining = false
	p.field.Blur()
}

// Show puts the finished draft up over the decision. saves are the rows that
// write it; the card appends its own discard.
//
// It is called again after every revision, so it keeps where the pointer is
// and which of the two holds the keyboard: a revision that sent the pointer
// back to Purpose would make the next revision of the same section cost the
// walk down to it again.
func (p *ProfileScreen) Show(draft ProfileDraftView, saves []SelectOption) {
	p.Step = ProfileDraft
	p.Draft = draft
	p.Warning = ""
	p.saves = saves
	p.refining = false
	p.whole, p.include, p.sent = false, false, ""
	p.Picker = nil
	p.section = min(max(p.section, 0), max(len(draft.Sections)-1, 0))
	p.reveal = true
	p.field.Blur()
	options := append([]SelectOption{}, saves...)
	options = append(options, SelectOption{Label: "Discard"})
	// The card's title names the profile, so the question stays answerable
	// on a terminal too short to keep the headline above it: "keep this
	// profile" without saying which one is a decision with the subject
	// removed. It has no Refine row and no note: revision is per section, so
	// the card is the ways out and nothing else.
	p.decide = &Select{Title: "Keep " + draft.Name + "?", Options: options}
	p.syncCard()
}

// OpenPicker puts a field block's selector where the card is, holding the
// keyboard. Show closes it: the next draft is the selector's answer.
func (p *ProfileScreen) OpenPicker(picker *MultiSelect) {
	p.Picker = picker
	p.reveal = true
}

// Selected is the section the pointer is on, for a host that words a wait
// or a warning about it.
func (p *ProfileScreen) Selected() int { return p.section }

// Select puts the pointer on a block and the keyboard on the sections, for a
// host that lands a revision on a block the person should be standing on —
// a whole-draft note's own row, whose esc takes the revision back.
func (p *ProfileScreen) Select(index int) {
	if index < 0 || index >= len(p.Draft.Sections) {
		return
	}
	p.section, p.card, p.reveal = index, false, true
	p.syncCard()
}

// CardFocused reports that the card rather than the sections has the
// keyboard.
func (p *ProfileScreen) CardFocused() bool { return p.card }

// syncCard tells the card whether it has the keyboard: idle, it lights no row
// and its key row is the one key that hands it over.
func (p *ProfileScreen) syncCard() {
	if p.decide == nil {
		return
	}
	p.decide.Idle = !p.card
	if p.card {
		p.decide.HintKeys = []KeyOffer{
			keyOfferAs(keys.Select.Take, "confirm"),
			keyOfferAs(keys.Profile.Note, "the sections"),
			p.decide.cancelOffer(),
		}
		return
	}
	p.decide.HintKeys = []KeyOffer{keyOfferAs(keys.Profile.Note, "the card")}
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
	if keys.Is(msg.String(), keys.Screen.List) && p.listLive() {
		p.keys = !p.keys
		return false, ProfileResult{}
	}
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
			return true, ProfileResult{Action: ProfileAbort, Index: p.section}
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
	case keys.Is(pressed, keys.Profile.Move):
		// The pointer runs from the field, at -1, through the starts, so what
		// moves is a step and not a list: the row above the first start is a
		// place the pointer lands and not an item it steps over.
		p.moveFocus(keys.Step(pressed, keys.Profile.Move))
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

// updateDraft answers the draft step. The sections and the card share it and
// tab moves the keyboard between them: the sections are where the draft is
// revised, one at a time, and the card is the only place anything is
// written (docs/interface/surfaces.md#the-profile-drafter).
func (p *ProfileScreen) updateDraft(msg tea.KeyPressMsg) (bool, ProfileResult) {
	if p.decide == nil {
		return true, ProfileResult{Action: ProfileDiscard}
	}
	if p.refining {
		return p.updateRefine(msg)
	}
	if p.Picker != nil {
		// The selector holds the keyboard whole, the scroll keys included:
		// it is a card of its own, with its own window.
		done, res := p.Picker.Update(msg)
		switch {
		case !done:
			return false, ProfileResult{}
		case res.Canceled:
			return true, ProfileResult{Action: ProfileUnpicked, Index: p.section}
		}
		return true, ProfileResult{Action: ProfilePicked, Index: p.section}
	}
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Profile.ScrollUp):
		p.scrollPane(-1)
		return false, ProfileResult{}
	case keys.Is(pressed, keys.Profile.ScrollDown):
		p.scrollPane(1)
		return false, ProfileResult{}
	case keys.Is(pressed, keys.Profile.Note):
		p.card = !p.card
		p.syncCard()
		return false, ProfileResult{}
	}
	if p.card {
		return p.updateCard(msg)
	}
	return p.updateSections(msg)
}

// updateCard answers the card once it has the keyboard: a save row, or
// Discard, or esc, which drops the draft the way the Discard row does.
func (p *ProfileScreen) updateCard(msg tea.KeyPressMsg) (bool, ProfileResult) {
	done, res := p.decide.Update(msg)
	if !done {
		return false, ProfileResult{}
	}
	if !res.Canceled && res.Index < len(p.saves) {
		return true, ProfileResult{Action: ProfileSave, Index: res.Index}
	}
	return true, ProfileResult{Action: ProfileDiscard}
}

// updateSections answers the sections while they have the keyboard. What
// enter, e and x do is the selected section's: a prose section is revised,
// and a field block hands enter to the host for the selector that picks it.
func (p *ProfileScreen) updateSections(msg tea.KeyPressMsg) (bool, ProfileResult) {
	sec, ok := p.selected()
	if !ok {
		if keys.Is(msg.String(), keys.Profile.Back) {
			return true, ProfileResult{Action: ProfileDiscard}
		}
		return false, ProfileResult{}
	}
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Profile.Move):
		p.section = min(max(p.section+keys.Step(pressed, keys.Profile.Move), 0), len(p.Draft.Sections)-1)
		p.reveal = true
	case keys.Is(pressed, keys.Profile.Refine):
		if !sec.revisable() {
			if sec.Pick == "" {
				return false, ProfileResult{}
			}
			return true, ProfileResult{Action: ProfilePick, Index: p.section}
		}
		p.refining = true
		p.field.Reset()
		p.field.Placeholder = "what to change in " + sec.Name
		p.field.Focus()
		p.reveal = true
	case keys.Is(pressed, keys.Profile.RefineAll):
		// One note for every section, whichever block the pointer is on: it
		// opens under the section list, since it is about all of them.
		p.refining, p.whole, p.include = true, true, false
		p.field.Reset()
		p.field.Placeholder = "what to change across the draft"
		p.field.Focus()
	case keys.Is(pressed, keys.Profile.Edit) && sec.revisable():
		return true, ProfileResult{Action: ProfileEdit, Index: p.section}
	case keys.Is(pressed, keys.Profile.Clear) && sec.revisable() && strings.TrimSpace(sec.Body) != "":
		return true, ProfileResult{Action: ProfileClear, Index: p.section}
	case keys.Is(pressed, keys.Profile.Back):
		// esc takes back the selected section's last revision while it has
		// one, and is the step's own esc on a section with none: every
		// revision is kept for the life of the flow, so the way back through
		// them is the key that is always the safe answer.
		if sec.Revised || sec.Whole {
			return true, ProfileResult{Action: ProfileUndo, Index: p.section}
		}
		return true, ProfileResult{Action: ProfileDiscard}
	}
	return false, ProfileResult{}
}

// updateRefine answers the note open under a section: enter sends it, esc
// closes it leaving the section as it is, and everything else is text.
func (p *ProfileScreen) updateRefine(msg tea.KeyPressMsg) (bool, ProfileResult) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Profile.Back):
		p.refining, p.whole, p.include = false, false, false
		p.field.Blur()
		return false, ProfileResult{}
	case p.toggles(pressed):
		p.include = !p.include
		return false, ProfileResult{}
	case keys.Is(pressed, keys.Profile.Refine):
		// A note is what a refine is made of, so enter over an empty one does
		// nothing rather than asking the drafter to guess.
		note := strings.TrimSpace(p.field.Value())
		if note == "" {
			return false, ProfileResult{}
		}
		if p.whole {
			p.sent = note
			return true, ProfileResult{Action: ProfileRefineAll, Text: note, Include: p.include}
		}
		return true, ProfileResult{Action: ProfileRefine, Index: p.section, Text: note}
	}
	p.field, _ = p.field.Update(msg)
	return false, ProfileResult{}
}

// toggles reports a keystroke that is the whole-draft note's second press
// rather than a letter of it: the note's own key, while nothing has been
// typed and some section is the person's to keep or include. Once the note
// has text in it the key is text, since a note is a sentence.
func (p *ProfileScreen) toggles(pressed string) bool {
	return p.whole && keys.Is(pressed, keys.Profile.RefineAll) &&
		p.field.Value() == "" && len(p.mine()) > 0
}

// mine is the names of the sections the person wrote themselves, in order.
func (p *ProfileScreen) mine() []string {
	var out []string
	for _, sec := range p.Draft.Sections {
		if sec.Mine {
			out = append(out, sec.Name)
		}
	}
	return out
}

// sectionCount is the draft's sections, leaving out the row a whole-draft
// note left under them.
func (p *ProfileScreen) sectionCount() int {
	n := 0
	for _, sec := range p.Draft.Sections {
		if !sec.Whole {
			n++
		}
	}
	return n
}

// selected is the section the pointer is on.
func (p *ProfileScreen) selected() (ProfileSection, bool) {
	if p.section < 0 || p.section >= len(p.Draft.Sections) {
		return ProfileSection{}, false
	}
	return p.Draft.Sections[p.section], true
}

// listLive reports that `?` is a key here rather than a character: a step
// with no field holding the keyboard. The brief's field, a question's answer
// and a section's open note all take it as text.
func (p *ProfileScreen) listLive() bool {
	switch p.Step {
	case ProfileBrief:
		return p.focus >= 0
	case ProfileQuestions:
		return false
	case ProfileWorking:
		return true
	default:
		return p.decide != nil && !p.refining && p.Picker == nil
	}
}

// keyList is every key the drafter has, for `?`.
func (p *ProfileScreen) keyList() []KeyOffer {
	return []KeyOffer{
		keyOffer(keys.Profile.Move), keyOffer(keys.Profile.Take),
		keyOffer(keys.Profile.Refine), keyOffer(keys.Profile.RefineAll), keyOffer(keys.Profile.Edit),
		keyOffer(keys.Profile.Clear), keyOffer(keys.Profile.Note),
		keyOffer(keys.Profile.ScrollUp), keyOffer(keys.Profile.ScrollDown),
		keyOffer(keys.Profile.Back),
	}
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
	chrome := ScreenChrome{
		Header:   p.header(),
		Head:     []string{p.railRow(width), ""},
		MaxLines: p.MaxLines,
	}
	// The register takes the foot only while it is asked for and only where
	// `?` is still a key: a step that has since put a field in front of the
	// reader has taken the character back.
	if p.keys && p.listLive() {
		chrome.Foot = KeyFooter{Register: p.keyList(), Showing: true}.Rows(width)
	}
	return chrome.View(width, func(budget int) []string { return p.bodyRows(width, budget) })
}

// header names the surface, what it is drafting into, and the way out.
func (p *ProfileScreen) header() ScreenHeader {
	h := ScreenHeader{
		Left: []RailSegment{screenTitle(p.Name)},
		Keys: words(keys.Profile.Back, p.wayOutWords()),
	}
	if p.listLive() {
		h.Keys = keys.Bracket(keys.Screen.List) + " " + keys.Words(keys.Screen.List) + " · " + h.Keys
	}
	if p.Subject != "" {
		h.Left = append(h.Left, screenField(p.Subject))
	}
	return h
}

// wayOutWords is what esc does from where the flow is standing. A takeover
// states its way out (invariant 5), and on a flow that is several different
// sentences: leaving before anything was drafted, unwinding one answer,
// stopping a drafting turn, closing a note and dropping a finished draft are
// not the same act and must not be worded as if they were.
func (p *ProfileScreen) wayOutWords() string {
	switch {
	case p.Step == ProfileWorking:
		return "stop drafting"
	case p.Step == ProfileDraft && (p.refining || p.Picker != nil):
		return "leave it as it is"
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
			// questions, and the rail says which happened: ⊘ is the mark for
			// a step that was skipped as well as for one that was refused —
			// both are a thing that did not happen — and a ✓ over a step
			// nobody was asked would be the rail claiming an exchange that
			// never happened
			// (docs/interface/departures.md#the-drafters-rail-marks-a-step-nothing-was-asked-at).
			// It takes Dim, the tone the kit gives ⊘, so a skipped step
			// does not share the Dimmer of a step still ahead.
			parts = append(parts, sty.Dim.Render("⊘ "+name))
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

// redrafting says the wait is one section being redrafted rather than the
// whole profile being drafted.
func (p *ProfileScreen) redrafting() bool {
	return p.Step == ProfileWorking && p.from == ProfileDraft
}

// bodyRows is the step that is up, trimmed to the budget.
func (p *ProfileScreen) bodyRows(width, budget int) []string {
	var rows []string
	switch {
	case p.Step == ProfileBrief:
		rows = p.briefRows(width)
	case p.Step == ProfileQuestions:
		rows = p.questionRows(width)
	case p.Step == ProfileDraft || p.redrafting():
		// The draft's rows lay their own key row where it belongs — between
		// the sections and the card — so nothing is appended after them.
		rows = p.draftRows(width, budget)
		if budget <= 0 || len(rows) <= budget {
			return rows
		}
		return rows[:budget]
	default:
		rows = p.workingRows(width)
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
func (p *ProfileScreen) briefHint() []KeyOffer {
	take := keyOfferAs(keys.Profile.Take, "draft from what you typed")
	if p.focus >= 0 {
		take = keyOfferAs(keys.Profile.Take, "draft from this one")
	}
	segments := []KeyOffer{take}
	if len(p.Starts) > 0 {
		segments = append(segments, keyOfferAs(keys.Profile.Move, "the field or a starting point"))
	}
	return append(segments, keyOfferAs(keys.Profile.Back, "nothing is drafted"))
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
	return []string{Clip(indent(p.workingLabel("")), width), ""}
}

// workingLabel is the wait's label in motion, with how long it has been and,
// for a section's wait, what it leaves alone.
func (p *ProfileScreen) workingLabel(after string) string {
	label := Anim{Frame: p.Frame, Label: p.Working, Lead: Spinner{Frame: p.Frame}.Glyph() + " "}
	if suffix := strings.Join(nonEmpty(p.Elapsed, after), " · "); suffix != "" {
		label.Suffix = sty.Dim.Render("  " + suffix)
	}
	return label.View()
}

// nonEmpty is the parts that say something.
func nonEmpty(parts ...string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// hintFor is the step's key row. Nothing on a key row is ever truncated
// (invariant 4), so a row too long for the terminal stacks its segments
// rather than losing one to a clip — the rule every card's hints keep.
func (p *ProfileScreen) hintFor(width int) []string {
	var segments []KeyOffer
	switch p.Step {
	case ProfileBrief:
		segments = p.briefHint()
	case ProfileQuestions:
		segments = []KeyOffer{
			keyOfferAs(keys.Profile.Take, "answer it"),
			keyOfferAs(keys.Profile.Back, p.backWords()),
		}
	case ProfileWorking:
		if p.redrafting() && p.whole {
			segments = []KeyOffer{keyOfferAs(keys.Profile.Back, "stop drafting · every section keeps its last text")}
			break
		}
		if p.redrafting() {
			segments = []KeyOffer{keyOfferAs(keys.Profile.Back, "stop drafting · the section keeps its last text")}
			break
		}
		segments = []KeyOffer{keyOfferAs(keys.Profile.Back, "stop drafting")}
	default:
		segments = p.sectionHint()
	}
	if len(segments) == 0 {
		return nil
	}
	rows := hintRows(segments, width-profileIndent)
	for i, row := range rows {
		rows[i] = indent(row)
	}
	return rows
}

// sectionHint is the draft step's own key row while the sections or a note
// under one have the keyboard. The card draws its own row, and while it has
// the keyboard this one is not drawn: two rows of live keys for one keyboard
// would be offering it twice.
func (p *ProfileScreen) sectionHint() []KeyOffer {
	if p.refining && p.whole {
		segments := []KeyOffer{keyOfferAs(keys.Profile.Refine, "redraft every section")}
		if len(p.mine()) > 0 && p.field.Value() == "" {
			again := "again to include the sections you edited"
			if p.include {
				again = "again to keep the sections you edited"
			}
			segments = append(segments, keyOfferAs(keys.Profile.RefineAll, again))
		}
		return append(segments, keyOfferAs(keys.Profile.Back, "leave it as it is"))
	}
	if p.refining {
		return []KeyOffer{
			keyOfferAs(keys.Profile.Refine, "refine it"),
			keyOfferAs(keys.Profile.Back, "leave it as it is"),
		}
	}
	if p.card || p.Picker != nil {
		return nil
	}
	sec, ok := p.selected()
	segments := []KeyOffer{keyOfferAs(keys.Profile.Move, "section")}
	if ok && !sec.revisable() && sec.Pick != "" {
		segments = append(segments, keyOfferAs(keys.Profile.Refine, sec.Pick))
	}
	if ok && sec.revisable() {
		segments = append(segments,
			keyOfferAs(keys.Profile.Refine, "refine it with a note"),
			keyOfferAs(keys.Profile.Edit, "edit it yourself"))
		if strings.TrimSpace(sec.Body) != "" {
			segments = append(segments, keyOfferAs(keys.Profile.Clear, "clear it"))
		}
	}
	segments = append(segments,
		keyOfferAs(keys.Profile.RefineAll, "all"),
		keyOfferAs(keys.Profile.Note, "the card"))
	if ok && sec.Whole {
		return append(segments, keyOfferAs(keys.Profile.Back, "take it back from every section it changed"))
	}
	if ok && sec.Revised {
		return append(segments, keyOfferAs(keys.Profile.Back, "take back its last revision"))
	}
	return append(segments, keyOfferAs(keys.Profile.Back, "discard the draft"))
}

// draftRows is the finished profile over the decision: what it is, its
// sections one block each, the key row that revises them, and the card.
//
// The card is what the surface is for, so it is the one thing that never
// gives ground — a decision whose keys were cut off by the height is not one.
// The sections are what give way: the pane folds from the bottom and counts
// the sections it folded, with the profile's own scroll keys to read on, and
// the selected section is kept inside it.
func (p *ProfileScreen) draftRows(width, budget int) []string {
	var card []string
	switch {
	case p.Step == ProfileDraft && p.Picker != nil:
		// The selector stands where the card stands and on the card's terms:
		// it is the decision on screen, so it is what never gives ground.
		p.Picker.MaxLines = max(budget, 0)
		card = strings.Split(p.Picker.View(width), "\n")
	case p.Step == ProfileDraft:
		card = p.cardRows(width, budget)
	}
	hint := p.hintFor(width)
	head := []string{Clip(indent(sty.Body.Render(p.Draft.headline())), width), ""}
	if p.Warning != "" {
		// Wrapped rather than clipped: the warning is the loader's sentence
		// about why the save was refused, and the part a clip would cut is
		// the part that says what to change.
		for i, line := range wrapPlain(p.Warning, width-profileIndent-2) {
			mark := "  "
			if i == 0 {
				mark = "⚠ "
			}
			head = append(head, Clip(indent(sty.Warn.Render(mark+line)), width))
		}
		head = append(head, "")
	}
	blocks, starts := p.sectionRows(width)
	// A note on the whole draft, and the wait after it, are under the
	// section list and never folded with it: the note holds the keyboard.
	tail := p.wholeRows(width)
	if len(hint) > 0 {
		tail = append(append(tail, ""), hint...)
	}
	if len(card) > 0 {
		tail = append(append(tail, ""), card...)
	}
	room := len(blocks)
	if budget > 0 {
		room = budget - len(head) - len(tail)
	}
	if room >= len(blocks) {
		rows := append(head, blocks...)
		return append(rows, tail...)
	}
	rows := append(head, p.foldedSections(width, max(room, 0), blocks, starts)...)
	return append(rows, tail...)
}

// foldedSections is the sections windowed to room rows, with a counted marker
// for what is folded above and below. The markers are paid for out of the
// room, so the window never draws a row past it.
func (p *ProfileScreen) foldedSections(width, room int, blocks []string, starts []int) []string {
	if room <= 0 {
		return nil
	}
	height := max(room-1, 1)
	p.pane.Height, p.pane.Total = height, len(blocks)
	if p.reveal {
		first, last := p.selectedRows(starts, len(blocks))
		p.pane.Reveal(last)
		p.pane.Reveal(first)
		p.reveal = false
	}
	p.pane.Offset = p.pane.Held()
	if p.pane.Offset > 0 && room > 2 {
		// A second marker costs a row of its own, so the window is shortened
		// by one more and the selected section brought back into it.
		height = room - 2
		p.pane.Height = height
		first, last := p.selectedRows(starts, len(blocks))
		if first < p.pane.Offset || last >= p.pane.Offset+height {
			p.pane.Reveal(last)
			p.pane.Reveal(first)
		}
		p.pane.Offset = p.pane.Held()
	}
	var rows []string
	if above := sectionsBefore(starts, p.pane.Offset); p.pane.Offset > 0 && room > 2 {
		rows = append(rows, Clip(indent(sty.Dim.Render(fmt.Sprintf("⋮ %s above · %s",
			plural(above, "more section"), words(keys.Profile.ScrollUp, "scroll up")))), width))
	}
	window := p.pane.Window(blocks)
	end := p.pane.Offset + len(window)
	// A section the window would cut off after its heading is folded whole
	// rather than shown as a heading with nothing under it — unless it is the
	// selected one, which the window is there to show.
	for i, start := range starts {
		last := len(blocks) - 1
		if i+1 < len(starts) {
			last = starts[i+1] - 2
		}
		if start > p.pane.Offset && start < end && last >= end && i != p.section {
			window = window[:start-1-p.pane.Offset]
			end = start
			break
		}
	}
	rows = append(rows, window...)
	if below := sectionsFrom(starts, end); below > 0 {
		rows = append(rows, Clip(indent(sty.Dim.Render(fmt.Sprintf("⋮ %s · %s",
			plural(below, "more section"), words(keys.Profile.ScrollDown, "scroll the profile")))), width))
	}
	if len(rows) > room {
		rows = rows[:room]
	}
	return rows
}

// selectedRows is the first and last row of the selected section.
func (p *ProfileScreen) selectedRows(starts []int, total int) (int, int) {
	if p.section < 0 || p.section >= len(starts) {
		return 0, 0
	}
	last := total - 1
	if p.section+1 < len(starts) {
		// The blank row between two sections belongs to neither.
		last = starts[p.section+1] - 2
	}
	return starts[p.section], max(last, starts[p.section])
}

// sectionsBefore counts the sections whose heading is above row, which is
// what a marker over a scrolled pane states.
func sectionsBefore(starts []int, row int) int {
	n := 0
	for _, s := range starts {
		if s < row {
			n++
		}
	}
	return n
}

// sectionsFrom counts the sections whose heading is at or below row, which
// is what the marker under a folded pane states.
func sectionsFrom(starts []int, row int) int {
	n := 0
	for _, s := range starts {
		if s >= row {
			n++
		}
	}
	return n
}

// sectionRows is every section as a block — the heading with its mark, then
// its prose or its field line — with a blank row between blocks, and the row
// each block starts at. The selected section takes the pointer and the focus
// tone on its heading while the sections have the keyboard; a note being
// written, or a redraft being waited on, is drawn under it.
func (p *ProfileScreen) sectionRows(width int) ([]string, []int) {
	var rows []string
	starts := make([]int, 0, len(p.Draft.Sections))
	// A whole-draft note is about every section, so no one of them is lit
	// while it is open or being waited on.
	lit := (!p.card || p.Step == ProfileWorking) && !p.whole
	for i, sec := range p.Draft.Sections {
		if i > 0 {
			rows = append(rows, "")
		}
		starts = append(starts, len(rows))
		selected := i == p.section && lit
		rows = append(rows, p.headingRow(sec, selected, width))
		rows = append(rows, p.sectionBody(sec, width)...)
		if i != p.section || p.whole {
			continue
		}
		switch {
		case p.refining:
			rows = append(rows, "")
			rows = append(rows, p.refineRows(sec, width)...)
		case p.redrafting():
			rows = append(rows, "", Clip(bodyIndent(p.workingLabel("the other sections stand")), width))
		}
	}
	return rows, starts
}

// headingRow is a section's heading with its mark after it.
func (p *ProfileScreen) headingRow(sec ProfileSection, selected bool, width int) string {
	heading := sty.Body.Render(sec.Name)
	lead := PointerColumn()
	if selected {
		heading = sty.Info.Render(sec.Name)
		lead = sty.FocusPointer.Render("❯") + " "
	}
	if sec.Mark != "" {
		heading += " " + sec.MarkTone.style().Render(sec.Mark)
	}
	return Clip(lead+heading, width)
}

// style is the mark's tone.
func (t ProfileMarkTone) style() lipgloss.Style {
	switch t {
	case ProfileMarkMine:
		return sty.Add
	case ProfileMarkEmpty:
		return sty.Del
	}
	return sty.Dim
}

// sectionBody is a section's prose wrapped under its heading, or a field
// block's value and what it means.
func (p *ProfileScreen) sectionBody(sec ProfileSection, width int) []string {
	if !sec.Prose {
		if sec.Value == "" {
			return nil
		}
		row := bodyIndent(sec.Tone.style().Render(sec.Value))
		if sec.Detail == "" {
			return []string{Clip(row, width)}
		}
		if full := row + sty.Dim.Render(" · "+sec.Detail); lipgloss.Width(full) <= width {
			return []string{full}
		}
		// What the value means goes under it rather than off the end of the
		// row: on the tools block it is the sentence that says the agent can
		// change things.
		rows := []string{Clip(row, width)}
		for _, line := range wrapPlain(sec.Detail, width-profileBodyIndent-2) {
			rows = append(rows, Clip(bodyIndent(sty.Dim.Render(line)), width))
		}
		return rows
	}
	body := strings.TrimSpace(sec.Body)
	if body == "" {
		// An empty section is its heading and the mark that says so; a blank
		// row under it would read as text nobody can see.
		return nil
	}
	var rows []string
	for _, line := range wrapBlock(body, width-profileBodyIndent-2) {
		rows = append(rows, Clip(bodyIndent(sty.Status.Render(line)), width))
	}
	return rows
}

// refineRows is the note under the section being refined: what it is for,
// and the field.
func (p *ProfileScreen) refineRows(sec ProfileSection, width int) []string {
	others := p.sectionCount() - 1
	label := fmt.Sprintf("┄ what to change in %s — the other %s are sent as fixed context", sec.Name, spellNumber(others))
	inner := max(width-profileBodyIndent-2, 8)
	p.field.SetWidth(inner)
	StyleTextArea(&p.field)
	// Wrapped rather than clipped: the half a clip would cut is the half that
	// says what else goes with the note.
	var rows []string
	for _, line := range wrapPlain(label, inner) {
		rows = append(rows, Clip(bodyIndent(sty.Dim.Render(line)), width))
	}
	for _, line := range strings.Split(p.field.View(), "\n") {
		rows = append(rows, Clip(bodyIndent(line), width))
	}
	return rows
}

// wholeRows is a note on the whole draft under the section list: what it is
// sent with — which sections it keeps, when some are the person's — and the
// field, or once it has gone, the note as sent and the wait under it.
func (p *ProfileScreen) wholeRows(width int) []string {
	if !p.whole || (!p.refining && !p.redrafting()) {
		return nil
	}
	label := "┄ a note on the whole draft — every section is redrafted on it; the tiers, tools and fields are sent as fixed context"
	if mine := p.mine(); len(mine) > 0 {
		verb := "is"
		if len(mine) > 1 {
			verb = "are"
		}
		if p.include {
			label += "; " + joinAnd(mine) + ", which you edited, " + verb + " redrafted too"
		} else {
			label += "; " + joinAnd(mine) + ", which you edited, " + verb + " kept"
		}
	}
	inner := max(width-profileIndent-2, 8)
	// Wrapped rather than clipped: the half a clip would cut is the half that
	// says which of the person's own sections the note may touch.
	rows := []string{""}
	for _, line := range wrapPlain(label, inner) {
		rows = append(rows, Clip(indent(sty.Dim.Render(line)), width))
	}
	if p.refining {
		p.field.SetWidth(inner)
		StyleTextArea(&p.field)
		for _, line := range strings.Split(p.field.View(), "\n") {
			rows = append(rows, Clip(indent("  "+line), width))
		}
		return rows
	}
	for _, line := range wrapPlain(p.sent, inner) {
		rows = append(rows, Clip(indent("  "+sty.Dimmer.Render(line)), width))
	}
	return append(rows, "", Clip(indent(p.workingLabel("")), width))
}

// joinAnd is names as a sentence lists them.
func joinAnd(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// cardRows is the decision, windowed to the height when even it does not fit:
// the selector's own window is what a card too tall for its surface has
// always done, and it counts the rows it is not showing.
func (p *ProfileScreen) cardRows(width, budget int) []string {
	if p.decide == nil {
		return nil
	}
	p.decide.MaxLines = 0
	if budget > 0 {
		p.decide.MaxLines = budget
	}
	return strings.Split(p.decide.View(width), "\n")
}

// scrollPane moves the sections by a row, held inside them against the rows
// the pane last drew them in.
func (p *ProfileScreen) scrollPane(by int) {
	p.pane.Offset = Pager{Offset: p.pane.Offset + by, Height: p.pane.Height, Total: p.pane.Total}.Held()
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
	StyleTextArea(&p.field)
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
			rows = append(rows, LitOption(start, width))
			continue
		}
		rows = append(rows, Clip(PointerColumn()+sty.Status.Render(start), width))
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

// bodyIndent puts a row in a section's prose column.
func bodyIndent(row string) string { return strings.Repeat(" ", profileBodyIndent) + row }
