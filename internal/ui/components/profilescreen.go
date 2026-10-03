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

// profileStep is where in the flow the surface is. The four are the flow's
// own states and not a general wizard's: there is no step that can be
// revisited out of order, because each one's answer is what produced the
// next one.
type profileStep int

const (
	// ProfileBrief asks what the profile is for, with starting points under
	// the field for a person who has the wish but not the sentence.
	ProfileBrief profileStep = iota
	// ProfileQuestions is one of the drafter's questions, asked on its own.
	ProfileQuestions
	// ProfileWorking is the wait while the drafter writes.
	ProfileWorking
	// ProfileDraft is the finished profile above the decision.
	ProfileDraft
)

// profileAction is what the person asked the flow to do.
type profileAction int

const (
	// ProfileTake carries the brief or one answer in Text.
	ProfileTake profileAction = iota
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
	// profileDiscard drops the draft.
	profileDiscard
	// ProfileAbort stops a drafting turn that is still running.
	ProfileAbort
	// ProfilePicked is enter on the open selector: the host reads the boxes
	// off the Picker it opened.
	ProfilePicked
	// ProfileUnpicked is esc on the open selector: the section stays as it
	// was.
	ProfileUnpicked
	// ProfileMigrate is the older-shape offer taken: the profile opened from
	// its file is sent to the drafter to be moved into the sections.
	ProfileMigrate
)

// profileResult is the surface's Update result.
type profileResult struct {
	Action profileAction
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
	// Note is a dim clause after the headline: what has happened to a
	// profile opened from its file that nothing has written yet.
	Note string
}

// headline is the name and what it is for on one line.
func (d ProfileDraftView) headline() string {
	if d.Description == "" {
		return d.Name
	}
	return d.Name + " — " + d.Description
}

// ProfileScreen is the drafting flow's surface: a wizard over three widgets
// the step selects — the brief and its questions, the section editor, and
// the diff and migrate viewer. It keeps the step, the chrome and the card,
// and every fact the host sets. See
// docs/architecture.md#the-profile-drafters-widgets.
type ProfileScreen struct {
	// name is what the header calls the surface — the command that opens it.
	name string
	// Subject is the dim clause beside it: which session this is and what it
	// already has. The person about to describe a new colleague is exactly
	// the person who wants to know which ones exist.
	Subject string

	Step profileStep

	// brief, sections and review are the three widgets the step selects,
	// and field is the text field the first two are lent.
	brief    profileBrief
	sections profileSections
	review   profileReview
	field    textarea.Model

	// working is what the drafter is doing, in a word, animated on the
	// session's own frame counter; Elapsed is how long it has been at it.
	working string
	Frame   int
	Elapsed string

	// warning is what went wrong with the decision just taken — a file that
	// already exists, a revision that did not land — shown on the draft that
	// is asking again.
	warning string

	// MaxLines bounds the surface to the pane it is drawn into.
	MaxLines int

	// saves are the rows that write the file, in the order the card offers
	// them. Discard is the surface's own and is appended to them, so no host
	// has to know where the save rows end.
	saves []SelectOption

	decide *Select
	// Picker is the selector a field block opened, drawn where the card is
	// and holding the keyboard until it is taken or cancelled. The host
	// builds it and reads its boxes; the surface only routes and draws it.
	Picker *MultiSelect
	// card says the card rather than the sections has the keyboard; tab
	// moves it between the two
	// (docs/interface/surfaces.md#the-profile-drafter).
	card bool
	// from is the step the wait was entered from, which is the step the rail
	// keeps showing while it lasts: a drafting turn started from the brief
	// may still come back with questions, so a rail that jumped to the draft
	// the moment the request went out would be promising a step the flow has
	// not reached.
	from profileStep
	// keys reports that `?` has the register and the glyph legend showing
	// under the step.
	keys bool

	// FromFile says the profile was opened from its file rather than
	// drafted from a brief: there is no brief or questions behind it, so the
	// rail is not drawn, and esc closes it with nothing written
	// (docs/interface/surfaces.md#the-profile-drafter).
	FromFile bool
	// Migratable puts the older-shape offer at the head of the draft and
	// makes its key live: the profile's file was written before the five
	// sections. OlderNote is what the offer says about it.
	Migratable bool
	OlderNote  string
	// Original is the prompt as the file wrote it, readable beside the
	// sections while a migration is being reviewed — beside them where the
	// width allows, under them where it does not.
	Original string
	// Diff is the change the card's save would make, as unified-diff lines,
	// drawn above the card once the person asks to see it.
	Diff []string
	// DiscardDesc is what the card's Discard row says beside it.
	DiscardDesc string
}

// NewProfileScreen builds the surface with its text field. The field is one
// row and takes the draft's newline chords, which is the note field's rule
// and for the note field's reason: the surface answers enter itself.
//
// The field is one, and the brief and the section editor are each lent it:
// it carries the width the last draw gave it, and a note typed before the
// next draw lays itself out in that width.
func NewProfileScreen(name string) *ProfileScreen {
	ta := NewTextArea()
	ta.SetHeight(1)
	ta.Focus()
	p := &ProfileScreen{name: name, field: ta}
	p.brief = profileBrief{focus: -1, field: &p.field}
	p.sections = profileSections{field: &p.field}
	return p
}

// AskBrief puts the flow on its first step. It drops the exchange with it:
// coming back to the brief is reconsidering the thing the questions were
// asked about, so answers to them are not still true.
func (p *ProfileScreen) AskBrief(ask, lead string, starts []string) {
	p.Step = ProfileBrief
	p.brief.open(ask, lead, starts)
}

// AskQuestion puts one of the drafter's questions on screen, numbered.
func (p *ProfileScreen) AskQuestion(question string, at, of int) {
	p.Step = ProfileQuestions
	p.brief.question(question, at, of)
}

// Answered records an exchange, so the questions already answered stay on
// screen under the ones still being asked.
func (p *ProfileScreen) Answered(question, answer string) {
	p.brief.answered(question, answer)
}

// Forget drops the last exchange, for a step back onto the question that
// produced it.
func (p *ProfileScreen) Forget() { p.brief.forget() }

// SetText puts text in the field with the cursor after it, for a step the
// person is coming back to rather than meeting.
func (p *ProfileScreen) SetText(text string) {
	p.field.SetValue(text)
}

// Warn states what went wrong with the decision just taken, on the draft that
// is asking again. It is cleared by the next Show, because the next draft is
// not the one the warning was about.
func (p *ProfileScreen) Warn(text string) { p.warning = text }

// Work puts the surface on the wait while the drafter writes. A wait entered
// from the draft is one section being redrafted, and it is drawn under that
// section rather than in place of the draft: the other sections stand.
func (p *ProfileScreen) Work(doing string) {
	if p.Step != ProfileWorking {
		p.from = p.Step
	}
	p.Step = ProfileWorking
	p.working = doing
	p.review.migrating = false
	p.sections.wait()
}

// Migrating puts the surface on the wait while a profile opened from its
// file is moved into the sections. It is the whole draft's wait, not a
// section's: every section is about to be replaced.
func (p *ProfileScreen) Migrating(doing string) {
	p.Step, p.from = ProfileWorking, ProfileWorking
	p.working = doing
	p.review.migrating = true
	p.sections.wait()
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
	p.warning = ""
	p.saves = saves
	p.Picker = nil
	p.review.migrating = false
	p.sections.land(draft)
	options := append([]SelectOption{}, saves...)
	options = append(options, SelectOption{Label: "Discard", Desc: p.DiscardDesc})
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
	p.sections.reveal = true
}

// Selected is the section the pointer is on, for a host that words a wait
// or a warning about it.
func (p *ProfileScreen) Selected() int { return p.sections.section }

// Select puts the pointer on a block and the keyboard on the sections, for a
// host that lands a revision on a block the person should be standing on —
// a whole-draft note's own row, whose esc takes the revision back.
func (p *ProfileScreen) Select(index int) {
	if index < 0 || index >= len(p.sections.draft.Sections) {
		return
	}
	p.sections.section, p.sections.reveal = index, true
	p.card = false
	p.syncCard()
}

// syncCard tells the card whether it has the keyboard: idle, it lights no row
// and its key row is the one key that hands it over.
func (p *ProfileScreen) syncCard() {
	if p.decide == nil {
		return
	}
	p.decide.idle = !p.card
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

// Update routes one keystroke to the step that is up.
func (p *ProfileScreen) Update(msg tea.KeyPressMsg) (done bool, result profileResult) {
	if keys.Is(msg.String(), keys.Screen.List) && p.listLive() {
		p.keys = !p.keys
		return false, profileResult{}
	}
	switch p.Step {
	case ProfileBrief:
		return p.brief.updateBrief(msg)
	case ProfileQuestions:
		return p.brief.updateQuestion(msg)
	case ProfileWorking:
		// A wait offers one key, and it is the way out of the wait rather
		// than out of the flow: the drafting is stopped and the step that
		// started it comes back (invariant 5 — the surface holds the
		// keyboard, so it says what the one live key does).
		if keys.Is(msg.String(), keys.Profile.Back) {
			return true, profileResult{Action: ProfileAbort, Index: p.sections.section}
		}
		return false, profileResult{}
	default:
		return p.updateDraft(msg)
	}
}

// updateDraft answers the draft step. The sections and the card share it and
// tab moves the keyboard between them: the sections are where the draft is
// revised, one at a time, and the card is the only place anything is
// written (docs/interface/surfaces.md#the-profile-drafter).
func (p *ProfileScreen) updateDraft(msg tea.KeyPressMsg) (bool, profileResult) {
	if p.decide == nil {
		return true, profileResult{Action: profileDiscard}
	}
	if p.sections.refining {
		return p.sections.updateRefine(msg)
	}
	if p.Picker != nil {
		// The selector holds the keyboard whole, the scroll keys included:
		// it is a card of its own, with its own window.
		done, res := p.Picker.Update(msg)
		switch {
		case !done:
			return false, profileResult{}
		case res.Canceled:
			return true, profileResult{Action: ProfileUnpicked, Index: p.sections.section}
		}
		return true, profileResult{Action: ProfilePicked, Index: p.sections.section}
	}
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Profile.ScrollUp):
		p.scrollPane(-1)
		return false, profileResult{}
	case keys.Is(pressed, keys.Profile.ScrollDown):
		p.scrollPane(1)
		return false, profileResult{}
	case keys.Is(pressed, keys.Profile.Note):
		p.card = !p.card
		p.syncCard()
		return false, profileResult{}
	}
	if p.card {
		return p.updateCard(msg)
	}
	return p.sections.updateSections(msg, p.Migratable)
}

// updateCard answers the card once it has the keyboard: a save row, or
// Discard, or esc, which drops the draft the way the Discard row does.
func (p *ProfileScreen) updateCard(msg tea.KeyPressMsg) (bool, profileResult) {
	done, res := p.decide.Update(msg)
	if !done {
		return false, profileResult{}
	}
	if !res.Canceled && res.Index < len(p.saves) {
		return true, profileResult{Action: ProfileSave, Index: res.Index}
	}
	return true, profileResult{Action: profileDiscard}
}

// listLive reports that `?` is a key here rather than a character: a step
// with no field holding the keyboard. The brief's field, a question's answer
// and a section's open note all take it as text.
func (p *ProfileScreen) listLive() bool {
	switch p.Step {
	case ProfileBrief:
		return p.brief.focus >= 0
	case ProfileQuestions:
		return false
	case ProfileWorking:
		return true
	default:
		return p.decide != nil && !p.sections.refining && p.Picker == nil
	}
}

// keyList is every key the drafter has, for `?`.
func (p *ProfileScreen) keyList() []KeyOffer {
	list := []KeyOffer{
		keyOffer(keys.Profile.Move), keyOffer(keys.Profile.Take),
		keyOffer(keys.Profile.Refine), keyOffer(keys.Profile.RefineAll), keyOffer(keys.Profile.Edit),
		keyOffer(keys.Profile.Clear),
	}
	if p.Migratable {
		// Live only on a profile opened in the older shape.
		list = append(list, keyOffer(keys.Profile.Migrate))
	}
	return append(list, keyOffer(keys.Profile.Note),
		keyOffer(keys.Profile.ScrollUp), keyOffer(keys.Profile.ScrollDown),
		keyOffer(keys.Profile.Back))
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
	chrome := screenChrome{
		header:   p.header(),
		head:     []string{p.railRow(width), ""},
		maxLines: p.MaxLines,
	}
	if p.FromFile {
		// Opened from a file: no brief was given and no question asked, so
		// a rail of three steps would claim exchanges that never happened.
		chrome.head = nil
	}
	// The register takes the foot only while it is asked for and only where
	// `?` is still a key: a step that has since put a field in front of the
	// reader has taken the character back.
	if p.keys && p.listLive() {
		chrome.foot = keyFooter{register: p.keyList(), showing: true}.rows(width)
	}
	return chrome.view(width, func(budget int) []string { return p.bodyRows(width, budget) })
}

// header names the surface, what it is drafting into, and the way out.
func (p *ProfileScreen) header() screenHeader {
	h := screenHeader{
		left: []RailSegment{screenTitle(p.name)},
		keys: words(keys.Profile.Back, p.wayOutWords()),
	}
	if p.listLive() {
		h.keys = keys.Bracket(keys.Screen.List) + " " + keys.Words(keys.Screen.List) + " · " + h.keys
	}
	if p.Subject != "" {
		h.left = append(h.left, screenField(p.Subject))
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
	case p.Step == ProfileWorking && p.review.migrating:
		return "stop"
	case p.Step == ProfileWorking:
		return "stop drafting"
	case p.Step == ProfileDraft && p.FromFile && !p.sections.refining && p.Picker == nil:
		return "close, nothing written"
	case p.Step == ProfileDraft && (p.sections.refining || p.Picker != nil):
		return "leave it as it is"
	case p.Step == ProfileDraft:
		return "discard the draft"
	case p.Step == ProfileQuestions && len(p.brief.asked) > 0:
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
		case i == 1 && i < at && p.brief.of == 0:
			// A brief that was already a specification gets a draft and no
			// questions, and the rail says which happened: ⊘ is the mark for
			// a step that was skipped as well as for one that was refused —
			// both are a thing that did not happen — and a ✓ over a step
			// nobody was asked would be the rail claiming an exchange that
			// never happened
			// (docs/interface/departures.md#the-drafters-rail-marks-a-step-nothing-was-asked-at).
			// It takes Dim, the tone the kit gives ⊘, so a skipped step
			// does not share the Dimmer of a step still ahead.
			parts = append(parts, sty.dim.Render("⊘ "+name))
		case i < at:
			parts = append(parts, sty.dim.Render("✓ "+name))
		case i == at:
			parts = append(parts, sty.info.Render("● "+name))
		default:
			parts = append(parts, sty.dimmer.Render("· "+name))
		}
	}
	return Clip(strings.Repeat(" ", profileIndent)+strings.Join(parts, sty.dimmer.Render("   ")), width)
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

// flow is where the wizard stands, as the section editor draws it.
func (p *ProfileScreen) flow() profileFlow {
	return profileFlow{
		card:       p.card,
		working:    p.Step == ProfileWorking,
		redrafting: p.redrafting(),
		label:      p.workingLabel,
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
		rows = p.brief.briefRows(width)
	case p.Step == ProfileQuestions:
		rows = p.brief.questionRows(width)
	case p.Step == ProfileDraft || p.redrafting():
		// The draft's rows lay their own key row where it belongs — between
		// the sections and the card — so nothing is appended after them.
		rows = p.draftRows(width, budget)
		if budget <= 0 || len(rows) <= budget {
			return rows
		}
		return rows[:budget]
	case p.review.migrating:
		rows = []string{Clip(indent(sty.body.Render(p.sections.draft.headline())), width), ""}
		rows = append(rows, Clip(bodyIndent(p.workingLabel("the author's sentences, placed where they belong")), width), "")
	default:
		rows = p.workingRows(width)
	}
	rows = append(rows, p.hintFor(width)...)
	if budget <= 0 || len(rows) <= budget {
		return rows
	}
	return rows[:budget]
}

// workingRows is the wait: the label in motion and how long it has been.
func (p *ProfileScreen) workingRows(width int) []string {
	return []string{Clip(indent(p.workingLabel("")), width), ""}
}

// workingLabel is the wait's label in motion, with how long it has been and,
// for a section's wait, what it leaves alone.
func (p *ProfileScreen) workingLabel(after string) string {
	label := animLabel{frame: p.Frame, label: p.working, lead: Spinner{Frame: p.Frame}.Glyph() + " "}
	if suffix := strings.Join(nonEmpty(p.Elapsed, after), " · "); suffix != "" {
		label.suffix = sty.dim.Render("  " + suffix)
	}
	return label.view()
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
		segments = p.brief.briefHint()
	case ProfileQuestions:
		segments = []KeyOffer{
			keyOfferAs(keys.Profile.Take, "answer it"),
			keyOfferAs(keys.Profile.Back, p.brief.backWords()),
		}
	case ProfileWorking:
		if p.review.migrating {
			segments = []KeyOffer{keyOfferAs(keys.Profile.Back, "stop · the profile stays as the file has it")}
			break
		}
		if p.redrafting() && p.sections.whole {
			segments = []KeyOffer{keyOfferAs(keys.Profile.Back, "stop drafting · every section keeps its last text")}
			break
		}
		if p.redrafting() {
			segments = []KeyOffer{keyOfferAs(keys.Profile.Back, "stop drafting · the section keeps its last text")}
			break
		}
		segments = []KeyOffer{keyOfferAs(keys.Profile.Back, "stop drafting")}
	default:
		segments = p.sections.sectionHint(p.card || p.Picker != nil, p.Migratable, p.wayOutWords())
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
	var head []string
	if p.Migratable {
		head = append(head, Clip(indent(p.review.olderRow(p.OlderNote)), width), "")
	}
	headline := sty.body.Render(p.sections.draft.headline())
	if p.sections.draft.Note != "" {
		headline += sty.dim.Render(" · " + p.sections.draft.Note)
	}
	head = append(head, Clip(indent(headline), width), "")
	if p.warning != "" {
		// Wrapped rather than clipped: the warning is the loader's sentence
		// about why the save was refused, and the part a clip would cut is
		// the part that says what to change.
		for i, line := range wrapPlain(p.warning, width-profileIndent-2) {
			mark := "  "
			if i == 0 {
				mark = "⚠ "
			}
			head = append(head, Clip(indent(sty.warn.Render(mark+line)), width))
		}
		head = append(head, "")
	}
	flow := p.flow()
	blocks, starts := p.review.originalBeside(p.Original, width, func(width int) ([]string, []int) {
		return p.sections.sectionRows(width, flow)
	})
	// A note on the whole draft, and the wait after it, are under the
	// section list and never folded with it: the note holds the keyboard.
	tail := p.sections.wholeRows(width, flow)
	if len(hint) > 0 {
		tail = append(append(tail, ""), hint...)
	}
	if p.diffUp() {
		// The diff is what the card's decision is about while it is up, so
		// it takes the sections' place, in a window of its own, and the
		// card keeps its rows.
		room := len(p.Diff)
		if budget > 0 {
			room = budget - len(head) - len(tail) - len(card) - 1
		}
		rows := append(head, p.review.diffWindow(p.Diff, width, room)...)
		return append(append(append(rows, tail...), ""), card...)
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
	rows := append(head, p.sections.foldedSections(width, max(room, 0), blocks, starts)...)
	return append(rows, tail...)
}

// diffUp says the save's diff stands where the sections do: it was asked
// for and the card holds the keyboard.
func (p *ProfileScreen) diffUp() bool {
	return len(p.Diff) > 0 && p.card && p.Step == ProfileDraft && p.Picker == nil
}

// style is the mark's tone.
func (t ProfileMarkTone) style() lipgloss.Style {
	switch t {
	case ProfileMarkMine:
		return sty.add
	case ProfileMarkEmpty:
		return sty.del
	}
	return sty.dim
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
	if p.diffUp() {
		p.review.scroll(by)
		return
	}
	p.sections.scroll(by)
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
