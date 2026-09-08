package chat

// The question card: the model's own question, put to the person as a
// decision (docs/interface/surfaces.md#the-question-card,
// docs/capabilities/coding-agent.md#the-model-can-ask).
//
// It is a decision and not an approval, which is the whole of why it sits
// where it does in the queue: every gate the approval path has exists to
// decide which *acts* stop to ask, and a question is not an act. A --yes, a
// session grant, accept-edits and the classifier would each be answering
// "which of these three designs" at random, so none of them is given the
// chance (approval.go).
//
// Nothing on the machine changed, so the row it leaves carries no rail
// (docs/interface/principles.md#weight-tracks-risk).

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// somethingElse is the last row of every list the model writes. A list the
// model wrote is a list the model may have got wrong, and the reader must
// never have to leave the card to say so — so the row is always there, it is
// not the model's, and taking it opens the note with nothing picked.
const somethingElse = "Something else …"

// questionCard is one outstanding question and the dressing it is being
// asked in. Exactly one of the three surfaces is built, chosen by the shape.
type questionCard struct {
	q ask.Question
	// sel dresses choose and text: the pick-one list with a note under it,
	// and for text the same card with no options above the field. Both are
	// the note-selector because a note is one key on every shape, and the
	// required note is its refusal.
	sel *components.NoteSelect
	// multi dresses choose_many, with the same note field under the boxes.
	multi *components.MultiSelect
	// conf dresses confirm: the inline confirm, whose enter is the answer
	// that changes nothing (docs/interface/surfaces.md#the-inline-confirm),
	// with note the field beside it that the card owns because an inline
	// confirm has no body to put one in.
	conf *components.Confirm
	note *components.NoteBox
	// rows are the options as the card draws them — the model's own, the
	// recommendation leading, and the something-else row last. The answer
	// reads labels off here, so it never has to agree with the model's own
	// ordering about what index means.
	rows []ask.Option
	// sheet is the call this page belongs to where the call carried several
	// questions, and nil where it carried one. Every page of one call shares
	// it, which is what lets any of them draw the strip and step to the next.
	sheet *questionSheet
	// submit marks the page that ends the set rather than holding a question
	// — the last tab, whose enter sends every answer at once.
	submit bool
}

// questionSheet is one call's questions on one card: the pages in the order
// the model sent them, the answers gathered so far, and which tab has the
// keyboard.
//
// It rides the request rather than the model for the reason the dry run's own
// two fields do — it is a fact about this call, and one left behind on the
// model would be advertised over the next decision in the queue — and because
// it has to outlive the card: esc sets the whole card down with the answers
// already given still on it, and the card that comes back is that one
// (asideQuestion, reopenQuestion).
type questionSheet struct {
	// qs are the questions in send order, which is the order the tabs are
	// drawn in and the order the answers go back in.
	qs []ask.Question
	// pages are the cards for them, one per question, built with the call.
	pages []*questionCard
	// answers are what has been given, parallel to qs. A nil entry is a
	// question nobody has answered yet, which is a different thing from one
	// answered `skipped`: the first is still open and the second is a
	// decision (ask.AnsweredSkipped).
	answers []*ask.Answer
	// at is the tab the keyboard is on. len(pages) is the submit, which is a
	// tab like the others so the strip has one index to draw and the arrows
	// have one range to walk.
	at int
	// submit is the page that ends the set.
	submit *questionCard
}

// page is the card the sheet is showing.
func (s *questionSheet) page() *questionCard {
	if s.at < 0 || s.at >= len(s.pages) {
		return s.submit
	}
	return s.pages[s.at]
}

// answered counts the questions that have an answer on them.
func (s *questionSheet) answered() int {
	n := 0
	for _, a := range s.answers {
		if a != nil {
			n++
		}
	}
	return n
}

// step walks the strip, wrapping, which is what a strip of four tabs and a
// submit wants: the submit is one key back from the first question rather
// than four keys forward from it.
func (s *questionSheet) step(delta int) {
	n := len(s.pages) + 1
	s.at = ((s.at+delta)%n + n) % n
}

// record writes the answer to the tab the keyboard is on.
func (s *questionSheet) record(a ask.Answer) {
	if s.at < 0 || s.at >= len(s.answers) {
		return
	}
	s.answers[s.at] = &a
}

// forward moves off an answered tab to the next question still open, or to
// the submit where there is none. Answering is what steps the card, so a
// reader working through three questions never presses a movement key; the
// arrows are for going back to one they answered and changing their mind.
func (s *questionSheet) forward() {
	for i := 1; i <= len(s.pages); i++ {
		at := (s.at + i) % len(s.pages)
		if s.answers[at] == nil {
			s.at = at
			return
		}
	}
	s.at = len(s.pages)
}

// replies are the answers as they go back: what was given, and `skipped` for
// what was not.
//
// An unanswered tab is skipped rather than a refusal to send, because the
// card is an offer and not a toll gate (asideQuestion): a reader who has
// answered the one question they had an opinion about must not be held at the
// card by the other two. The strip says so before they reach the submit
// rather than after (questionSheet.strip).
func (s *questionSheet) replies() []ask.Answer {
	out := make([]ask.Answer, 0, len(s.qs))
	for _, a := range s.answers {
		if a == nil {
			out = append(out, ask.Answer{Answered: ask.AnsweredSkipped})
			continue
		}
		out = append(out, *a)
	}
	return out
}

// outcomeOf is the one word the transcript row carries for a call. Where the
// call asked one question it is that answer's; where it asked several the row
// still has one word for them, so it says the strongest thing that happened —
// a pick, then the reader's own words, then a call nobody chose anything on.
// The answers themselves are in the result, which is where a reader who wants
// them one by one opens the row.
func outcomeOf(as []ask.Answer) ask.Answered {
	if len(as) == 1 {
		return as[0].Answered
	}
	word := ask.AnsweredSkipped
	for _, a := range as {
		switch a.Answered {
		case ask.AnsweredOnCard:
			return ask.AnsweredOnCard
		case ask.AnsweredTyped:
			word = ask.AnsweredTyped
		}
	}
	return word
}

// strip is the tab bar over the card: one tab per question, the submit last,
// and the tail that says in words what the marks say in glyphs
// (docs/interface/principles.md#colour-never-carries-meaning-alone).
func (s *questionSheet) strip() components.TabStrip {
	tabs := make([]components.Tab, 0, len(s.pages)+1)
	for i := range s.pages {
		tab := components.Tab{Label: strconv.Itoa(i + 1), Answered: s.answers[i] != nil}
		if tab.Answered {
			// The word the mark stands for, which the strip draws beside
			// every answered tab wherever the row has room. It is what the
			// tab the keyboard is on has no mark left to say — that tab is
			// drawn as the tab you are standing on — and it is the reading a
			// terminal with one colour is left with
			// (docs/interface/principles.md#colour-never-carries-meaning-alone).
			tab.Word = "answered"
		}
		tabs = append(tabs, tab)
	}
	// The submit is a tab like the others and is never answered: there is
	// nothing on it to answer, and its mark is the same `not yet` the
	// questions still open carry.
	tabs = append(tabs, components.Tab{Label: "submit"})
	long, short := components.TabTally(s.at, len(s.pages), s.answered(), "will be sent as skipped")
	return components.TabStrip{Tabs: tabs, At: s.at, Tail: long, ShortTail: short}
}

// WithAsk says this session registered the question tool, which is the only
// condition under which the card is ever drawn: a session with nobody to ask
// never handed the model the tool, and a call to a tool the session does not
// have is answered as one rather than put on a card
// (docs/capabilities/coding-agent.md#nobody-to-ask).
func (m Model) WithAsk() Model {
	m.asks = true
	return m
}

// armQuestion is the question's place in the approval queue: a decision put
// to the person in every mode, because every gate below it exists to decide
// which *acts* stop to ask and a question is not an act.
//
// The turn's budget is spent here, where the question is asked, and not where
// it is answered. A question that reached the screen has already cost the
// reader the interruption whatever they then did with it — picked an answer,
// or set it down for their next message — so a card they have not got round
// to does not buy another one. And one past the budget draws nothing at all,
// because a card that appeared only to be answered by a rule would be an
// interruption charged for twice
// (docs/capabilities/coding-agent.md#the-model-can-ask).
func (m Model) armQuestion(req *approvalRequest) (tea.Model, tea.Cmd) {
	if m.questionsAsked >= ask.PerTurnBudget {
		return m.answerQuestionAs(ask.OverBudget(), overBudgetRule)
	}
	// One card is one interruption, whether it carries one question or the
	// four ask.MaxQuestions allows: what the budget bounds is how often a turn
	// stops and takes the keyboard, and a model that gathered its forks onto
	// one card did the thing the budget is asking for rather than the thing it
	// is guarding against.
	m.questionsAsked++
	m.recordDecision(observe.DecisionAsk, observe.ReasonUser)
	m.openQuestion(req)
	m.pendingQueue, m.pendingBatch = m.resolveQueue(req)
	m.setTurnState(stateQuestion)
	m.syncViewport()
	return m, nil
}

// openQuestion puts the call's card up: the one page a single question is, or
// the page of the tab the sheet was left on.
func (m *Model) openQuestion(req *approvalRequest) {
	if req.sheet == nil {
		m.question = m.questionPage(req.question, nil)
		return
	}
	if len(req.sheet.pages) == 0 {
		// Built once, with the call. A card that was set down and picked up
		// again is the same card — the answers already given are on it, and
		// the tab the reader left is the tab they come back to — which is
		// what a single question has no need of, because there is nothing on
		// it to keep.
		for _, q := range req.sheet.qs {
			req.sheet.pages = append(req.sheet.pages, m.questionPage(q, req.sheet))
		}
		req.sheet.submit = &questionCard{sheet: req.sheet, submit: true}
	}
	m.question = req.sheet.page()
}

// questionPage builds one question's card in the dressing its shape asks for.
func (m *Model) questionPage(q ask.Question, sheet *questionSheet) *questionCard {
	c := &questionCard{q: q, sheet: sheet}
	// The strip is drawn above the card and comes off the same panel, so a
	// page on a sheet has one row less of list than a page on its own.
	body := m.maxConfirmPanelHeight() - 1
	if sheet != nil {
		body--
	}
	switch q.Shape {
	case ask.ShapeChoose:
		c.rows = questionRows(q.Options)
		c.sel = components.NewNoteSelect(questionTitle, selectRows(c.rows))
		c.sel.Select.MaxLines = body
		c.sel.Actions = c.offers()
	case ask.ShapeChooseMany:
		c.rows = questionRows(q.Options)
		c.multi = components.NewMultiSelect(questionTitle, selectRows(c.rows))
		c.multi.Note = components.NewNoteBox()
		c.multi.MaxLines = body
		c.multi.Actions = c.offers()
	case ask.ShapeConfirm:
		c.conf = &components.Confirm{Prompt: firstLine(q.Question)}
		c.note = components.NewNoteBox()
	case ask.ShapeText:
		// No options above the field: the answer is the note, so the card
		// opens with the keyboard already in it.
		c.sel = components.NewNoteSelect(questionTitle, nil)
		c.sel.Require = true
		c.sel.Actions = c.offers()
		c.armNote()
	}
	if q.Note == ask.NoteRequired {
		// The model said a pick alone is not enough to act on, so the field
		// says so and opens with the card rather than a key later.
		switch {
		case c.sel != nil:
			c.sel.Require = true
		case c.multi != nil:
			c.multi.Note.Required = true
		case c.note != nil:
			c.note.Required = true
		}
		c.armNote()
	}
	return c
}

// armNote opens the field with the page, except on a tabbed card, where it is
// one key away instead.
//
// A field that opened with the page would hold the keyboard the moment the
// reader stepped onto that tab, and every letter and arrow under it is text —
// so the strip they were walking would stop moving on the tab they had not
// asked to type into. On a card that is the whole of the decision there is
// nowhere else for the keyboard to be and the field opens; on a card of tabs
// there is, so the note key opens it and gives it back.
func (c *questionCard) armNote() {
	if c.sheet != nil {
		return
	}
	c.openNote()
}

// offers are the keys this page has beyond the ones the dressing offers for
// itself: the marked row's long form where there are rows for one to be
// behind, and the strip where the call carried more than one question. Both
// are worded here rather than at the binding, because `d` and the arrows each
// mean a near thing elsewhere on the family and the row has to say which.
func (c *questionCard) offers() []string {
	var out []string
	if len(c.rows) > 0 {
		out = append(out, keys.Bracket(keys.Select.Long)+" "+keys.Words(keys.Select.Long))
	}
	if c.sheet == nil {
		return out
	}
	if c.freeAnswer() {
		// The free answer is the one page whose field is shut when the page
		// arrives (armNote), and the note-selector offers no note key on a
		// card with no rows to move between — so the offer and the key are
		// the card's here (updateQuestion).
		out = append(out, keys.Bracket(keys.Select.Note)+" answer")
	}
	return append(out, keys.Bracket(keys.Select.Tab)+" "+keys.Words(keys.Select.Tab))
}

// freeAnswer reports the dressing that is the field and nothing else.
func (c *questionCard) freeAnswer() bool { return c.sel != nil && len(c.rows) == 0 }

// questionRows is the list as the card draws it: the recommendation first,
// everything else in the model's own order, and the row the model did not
// write last.
func questionRows(opts []ask.Option) []ask.Option {
	rows := make([]ask.Option, 0, len(opts)+1)
	for _, o := range opts {
		if o.Recommended {
			rows = append(rows, o)
		}
	}
	for _, o := range opts {
		if !o.Recommended {
			rows = append(rows, o)
		}
	}
	return append(rows, ask.Option{Label: somethingElse})
}

// selectRows dresses the options as selector rows. An answer the model said
// cannot be taken is the ⊘ row with its reason in the short field — the glyph
// and the phrase the selector already draws, never merely dimmed
// (docs/interface/surfaces.md#selectors).
func selectRows(rows []ask.Option) []components.SelectOption {
	out := make([]components.SelectOption, 0, len(rows))
	for i, o := range rows {
		row := components.SelectOption{
			Label:       o.Label,
			Desc:        o.Detail,
			Meta:        o.Field,
			Recommended: o.Recommended,
		}
		if o.Unavailable != "" {
			row.Dim, row.Meta = true, o.Unavailable
		}
		if i == len(rows)-1 {
			// The row the reader takes to answer in their own words: it has
			// no meaning without them, so it is the one row on the card that
			// refuses an empty note. Found by position and not by its label,
			// because a model that wrote an option with the same words would
			// otherwise have written this row.
			row.RequireNote = true
			row.Desc = "answer in your own words"
		}
		out = append(out, row)
	}
	return out
}

// elsewhere reports the row the model did not write: the last one, always
// appended by questionRows.
func (c *questionCard) elsewhere(i int) bool { return i == len(c.rows)-1 }

// openNote puts the keyboard in the note field, whichever surface owns it.
func (c *questionCard) openNote() {
	switch {
	case c.sel != nil:
		c.sel.FocusNote = true
		c.sel.Note.Focus()
	case c.multi != nil:
		c.multi.Note.Open()
	case c.note != nil:
		c.note.Open()
	}
}

// digit is the row a number key addresses on the pick-one card, or -1. The
// rows are the list as drawn — no headers, no filter — so the number is the
// position and nothing has to be mapped.
func (c *questionCard) digit(msg tea.KeyPressMsg) int {
	if c.sel == nil || c.sel.FocusNote || c.sel.Select.Filtering {
		return -1
	}
	pressed := msg.String()
	if len(pressed) != 1 || pressed[0] < '1' || pressed[0] > '9' {
		return -1
	}
	if n := int(pressed[0] - '1'); n < len(c.rows) {
		return n
	}
	return -1
}

// typing reports that something on the card is being typed into — the note
// field, or the list's own query row — which is what makes the card's letter
// and arrow keys text.
func (c *questionCard) typing() bool {
	if c.sel != nil && c.sel.Select.Filtering {
		return true
	}
	return c.noteHolds()
}

// noteHolds reports that the note field has the keyboard, whichever surface
// owns it.
func (c *questionCard) noteHolds() bool {
	switch {
	case c.sel != nil:
		return c.sel.FocusNote
	case c.multi != nil:
		return c.multi.Note.Focused
	case c.note != nil:
		return c.note.Focused
	}
	return false
}

// dropNote empties the field and hands the keyboard back to the list.
func (c *questionCard) dropNote() {
	switch {
	case c.sel != nil:
		c.sel.Note.SetValue("")
		c.sel.Note.Blur()
		c.sel.FocusNote = false
	case c.multi != nil:
		c.multi.Note.Field.SetValue("")
		c.multi.Note.Toggle()
	case c.note != nil:
		c.note.Field.SetValue("")
		c.note.Toggle()
	}
}

// refuseNote says the answer needs words the card has not got, in the place
// the field already says it: the label turns red and reads `note required`.
// The keyboard goes with the refusal, because a refusal on a region the
// reader is not in is one they have to go looking for.
func (c *questionCard) refuseNote() {
	switch {
	case c.multi != nil:
		c.multi.Note.Refuse()
	case c.note != nil:
		c.note.Refuse()
	}
	c.openNote()
}

// questionPanelLines is the card in the bottom panel, dressed with the rail
// that names the keyboard's owner the way every decision is.
func (m Model) questionPanelLines() []string {
	return m.dressDecision(m.questionLines(), m.contentWidth())
}

// questionLines is the card alone: the queue strip above it, then whichever
// dressing the shape asked for.
func (m Model) questionLines() []string {
	c := m.question
	if c == nil {
		// Set aside: there is no card, and the queue strip above it
		// describes a decision that is not on the screen either
		// (asideQuestion).
		return nil
	}
	width := m.contentWidth()
	lines := m.pendingQueue.View(width)
	if c.sheet != nil {
		lines = append(lines, c.sheet.strip().View(width))
	}
	switch {
	case c.submit:
		lines = append(lines, c.sheet.submitRows(width)...)
	case c.sel != nil:
		c.sel.Select.Title, c.sel.Select.Tone = questionTitle, components.CardDecision
		c.sel.Select.Chips, c.sel.Select.Lead = []string{c.place()}, m.questionLead(c, width)
		lines = append(lines, strings.Split(c.sel.View(width), "\n")...)
	case c.multi != nil:
		c.multi.Title, c.multi.Tone = questionTitle, components.CardDecision
		c.multi.Chips, c.multi.Lead = []string{c.place()}, m.questionLead(c, width)
		lines = append(lines, strings.Split(c.multi.View(width), "\n")...)
	case c.conf != nil:
		lines = append(lines, components.Clip(c.conf.View(width), width))
		lines = append(lines, c.note.Rows(width)...)
		lines = append(lines, questionConfirmKeys(c, width)...)
	}
	return lines
}

// questionTitle is what the card is called. The question itself is a body row
// under it (questionLead): a title is clipped into the border it is drawn on,
// and the one thing on this card that must never be half-read is the question
// (docs/interface/surfaces.md#the-question-card).
const questionTitle = "Question"

// questionLead is the question as the card's first body rows, wrapped to the
// card. It is done at render rather than when the card is built because the
// wrap belongs to the width, and a card built once is drawn at every width
// the terminal is dragged through.
func (m Model) questionLead(c *questionCard, width int) []string {
	text := strings.TrimSpace(c.q.Question)
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(
		m.wordWrap(text, components.Card{}.Inner(width)), "\n"), "\n")
}

// place is which of the call's questions this card is, as the chip says it. A
// lone question says `1 of 1` rather than nothing: the chip is where a reader
// looks to find out whether more is coming, and an empty chip answers that
// only by omission.
func (c *questionCard) place() string {
	at, of := 1, 1
	if c.sheet != nil {
		at, of = c.sheet.at+1, len(c.sheet.qs)
	}
	return fmt.Sprintf("%d of %d", at, of)
}

// questionConfirmKeys is the yes-or-no shape's key row. The two list
// dressings draw their own from the register; the inline confirm has no card
// to draw one in, so the card states it here — from the register, not from a
// spelling written down beside it.
func questionConfirmKeys(c *questionCard, width int) []string {
	segs := []string{
		keys.Bracket(keys.Confirm.Yes) + " " + keys.Words(keys.Confirm.Yes),
		keys.Bracket(keys.Confirm.No) + " " + keys.Words(keys.Confirm.No),
		keys.Bracket(keys.Select.Note) + " note",
	}
	segs = append(segs, c.offers()...)
	// Wrapped between clauses rather than clipped, the way a card's own key
	// row is: this dressing is drawn bare in the panel, so the whole width is
	// the room and there is no frame to pay for.
	return components.HintRows(segs, width)
}

// submitRows is the page that ends the set: what enter would send, and — where
// some tab is still open — what leaving it open costs, said here as well as on
// the strip because this is the row the key is under.
func (s *questionSheet) submitRows(width int) []string {
	line := "send " + countedAnswers(s.answered())
	if left := len(s.qs) - s.answered(); left > 0 {
		line += " · " + strconv.Itoa(left) + " unanswered, sent as skipped"
	}
	segs := []string{
		keys.Bracket(keys.Select.Take) + " send",
		keys.Bracket(keys.Select.Tab) + " " + keys.Words(keys.Select.Tab),
		keys.Bracket(keys.Select.Cancel) + " answer in your own words",
	}
	rows := append([]string{components.Clip(line, width)}, components.CardHintRows(segs, width)...)
	return strings.Split(components.Card{Title: "the answers"}.Render(rows, width), "\n")
}

// countedAnswers is a count of answers as the rails write one.
func countedAnswers(n int) string {
	if n == 1 {
		return "1 answer"
	}
	return strconv.Itoa(n) + " answers"
}

// updateQuestion answers one key on the card. It is reached only once the
// card holds the keyboard, like every other decision.
func (m Model) updateQuestion(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := m.question
	if c == nil {
		return m, nil
	}
	if c.submit {
		return m.updateQuestionSubmit(msg, c)
	}
	// The strip and the long form, which are the card's own keys rather than
	// the dressing's, and are inert while a field holds the keyboard for the
	// reason every letter on the card is: there, an arrow moves the cursor
	// and `d` is a `d`
	// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
	// The free answer's own note key, which the note-selector does not offer
	// on a card with no rows to move between (offers). It is answered above
	// the inert check because it is the key that gives the keyboard *back*,
	// and a way out of a field that were inert while the field held it would
	// be no way out at all.
	if c.sheet != nil && c.freeAnswer() && keys.Match(msg, keys.Select.Note) {
		if c.noteHolds() {
			c.sel.FocusNote = false
			c.sel.Note.Blur()
			return m, nil
		}
		c.openNote()
		return m, nil
	}
	if !c.typing() {
		if d := keys.Step(msg.String(), keys.Select.Tab); d != 0 && c.sheet != nil {
			return m.stepQuestion(d)
		}
		if len(c.rows) > 0 && keys.Match(msg, keys.Select.Long) {
			return m.openQuestionLong(c)
		}
	}
	// Esc out of the note drops the note and keeps the pick: a way out that
	// lost the pick would not be the safe one, and the reader who opened the
	// field by mistake has not also un-chosen their answer
	// (docs/interface/surfaces.md#the-question-card). Esc again leaves the
	// card. It is spelled from the draft's own cancel because that binding
	// is esc alone — the card's cancel is esc *and* ctrl+c, and ctrl+c ends
	// a decision rather than backing out of a field of it.
	//
	// A card with no rows is the other branch: the note is the answer, so
	// there is no pick for esc to keep and it leaves outright — unless it is
	// a tab of several, where what esc would keep is the answers on the other
	// tabs. The note key is the way out that keeps the words; esc is the one
	// that drops them, which is what it means on every other dressing.
	if keys.Match(msg, keys.Draft.Clear) && (len(c.rows) > 0 || c.sheet != nil) && c.noteHolds() {
		c.dropNote()
		return m, nil
	}
	switch {
	case c.multi != nil:
		return m.updateQuestionMany(msg, c)
	case c.conf != nil:
		return m.updateQuestionConfirm(msg, c)
	}
	return m.updateQuestionOne(msg, c)
}

// updateQuestionOne answers the pick-one list and the free-text field, which
// are the same card with and without options above it.
func (m Model) updateQuestionOne(msg tea.KeyPressMsg, c *questionCard) (tea.Model, tea.Cmd) {
	c.sel.Select.Warning = ""
	if n := c.digit(msg); n >= 0 {
		// A digit takes its row outright, the way it does on every numbered
		// list in the product. The note-selector's own rule is that a digit
		// only moves — its answer is a pick *and* a note, so confirming on
		// the digit would skip the field — but here the note is beside the
		// pick rather than part of it, and a required one still refuses
		// below. Inert while the field has the keyboard, where a digit is a
		// digit (docs/interface/surfaces.md#the-question-card).
		c.sel.Select.Focus = n
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	done, res := c.sel.Update(msg)
	if !done {
		// A confirm the card refused for an empty required note takes the
		// keyboard with it, so the words it is asking for can be typed where
		// the refusal is showing.
		if c.sel.NoteMissing() {
			c.openNote()
		}
		return m, nil
	}
	if res.Canceled {
		return m.asideQuestion()
	}
	note := strings.TrimSpace(c.sel.Note.Value())
	if c.q.Shape == ask.ShapeText {
		return m.answerQuestion(ask.Answer{Answered: ask.AnsweredTyped, Note: note})
	}
	if res.Index < 0 || res.Index >= len(c.rows) {
		return m, nil
	}
	row := c.rows[res.Index]
	if row.Unavailable != "" {
		// Taking a row that cannot be taken re-states why instead of
		// answering: a key that looks ignored is a key the reader presses
		// harder (invariant 5).
		c.sel.Select.Warning = c.sel.Select.Options[res.Index].UnavailableNotice()
		return m, nil
	}
	if c.elsewhere(res.Index) {
		// The row's own RequireNote has already refused an empty one, so
		// the note here is the reader's answer in their own words.
		return m.answerQuestion(ask.Answer{Answered: ask.AnsweredTyped, Note: note})
	}
	return m.answerQuestion(ask.Answer{
		Answered: ask.AnsweredOnCard,
		Picked:   []string{row.Label},
		Note:     note,
	})
}

// updateQuestionSubmit answers the tab that ends the set: enter sends every
// answer at once, the arrows go back to a question, and esc sets the whole
// card down the way it does from any other tab.
func (m Model) updateQuestionSubmit(msg tea.KeyPressMsg, c *questionCard) (tea.Model, tea.Cmd) {
	if d := keys.Step(msg.String(), keys.Select.Tab); d != 0 {
		return m.stepQuestion(d)
	}
	switch {
	case keys.Match(msg, keys.Select.Cancel):
		return m.asideQuestion()
	case keys.Match(msg, keys.Select.Take):
		return m.submitQuestions()
	}
	return m, nil
}

// stepQuestion walks the strip one tab and draws it.
func (m Model) stepQuestion(delta int) (tea.Model, tea.Cmd) {
	s := m.question.sheet
	s.step(delta)
	m.question = s.page()
	return m, nil
}

// openQuestionLong puts the marked row's own long form on the full screen —
// the one the dry run and the full diff already open on — and gives the screen
// back with the question still waiting. It answers nothing: the panel has no
// room for a second column, and reading what an answer means must not cost
// the reader the answer (docs/interface/surfaces.md#the-question-card).
func (m Model) openQuestionLong(c *questionCard) (tea.Model, tea.Cmd) {
	i := c.marked()
	if i < 0 || i >= len(c.rows) {
		return m, nil
	}
	return m.openOutputFull(questionLongView(c, i), noOutputEntry, stateQuestion)
}

// marked is the row the pointer is on, whichever list dressing this is, or -1
// on a card with no list.
func (c *questionCard) marked() int {
	switch {
	case c.multi != nil:
		return c.multi.Focus
	case c.sel != nil:
		return c.sel.Select.Focus
	}
	return -1
}

// questionLongView is one row written out whole: the question above it, the
// answer itself, and everything the row could only show a clause of.
func questionLongView(c *questionCard, i int) *components.OutputView {
	o := c.rows[i]
	lines := []string{o.Label}
	add := func(s string) {
		if s != "" {
			lines = append(lines, "", s)
		}
	}
	if c.elsewhere(i) {
		// The one row the model did not write, so what it means is the
		// card's to say rather than the model's.
		add("Not one of the answers the model offered. Taking it opens the note, and what you write there is the answer, in your own words.")
	}
	if o.Recommended {
		add("recommended")
	}
	add(o.Field)
	add(o.Detail)
	if o.Unavailable != "" {
		add("⊘ " + o.Unavailable)
	}
	// Wrapped rather than clipped, for the reason the command card's own full
	// view is: the screen was opened to read this whole.
	return &components.OutputView{Title: firstLine(c.q.Question), Lines: lines, Wrap: true}
}

// updateQuestionMany answers the checkbox list.
func (m Model) updateQuestionMany(msg tea.KeyPressMsg, c *questionCard) (tea.Model, tea.Cmd) {
	done, res := c.multi.Update(msg)
	if !done {
		return m, nil
	}
	if res.Canceled {
		return m.asideQuestion()
	}
	note := c.multi.Note.Value()
	var picked []string
	typed := false
	for _, i := range res.Indices {
		if i < 0 || i >= len(c.rows) {
			continue
		}
		if c.elsewhere(i) {
			typed = true
			continue
		}
		picked = append(picked, c.rows[i].Label)
	}
	if (typed || c.q.Note == ask.NoteRequired) && note == "" {
		c.refuseNote()
		return m, nil
	}
	answered := ask.AnsweredOnCard
	if len(picked) == 0 {
		// The substance of the answer is the sentence and not the list.
		answered = ask.AnsweredTyped
	}
	return m.answerQuestion(ask.Answer{Answered: answered, Picked: picked, Note: note})
}

// updateQuestionConfirm answers the yes-or-no.
func (m Model) updateQuestionConfirm(msg tea.KeyPressMsg, c *questionCard) (tea.Model, tea.Cmd) {
	c.note.Settle()
	pressed := msg.String()
	if keys.Is(pressed, keys.Select.Note) {
		c.note.Toggle()
		return m, nil
	}
	if c.note.Focused {
		// The field has the keyboard, so y and n are letters — except the
		// one key that leaves, which every surface being typed into keeps.
		if keys.Is(pressed, keys.Select.Cancel) {
			return m.asideQuestion()
		}
		c.note.Update(msg)
		return m, nil
	}
	if keys.Is(pressed, keys.Select.Cancel) {
		return m.asideQuestion()
	}
	done, yes := c.conf.Update(msg)
	if !done {
		return m, nil
	}
	note := c.note.Value()
	if c.q.Note == ask.NoteRequired && note == "" {
		c.refuseNote()
		return m, nil
	}
	label := "no"
	if yes {
		label = "yes"
	}
	return m.answerQuestion(ask.Answer{
		Answered: ask.AnsweredOnCard,
		Picked:   []string{label},
		Note:     note,
	})
}

// asideQuestion is esc: the card closes and answers nothing. The question
// stays outstanding — the call is still at the head of the queue, the turn is
// still blocked on it — and the next message the reader sends is delivered as
// the answer in their own words
// (docs/interface/surfaces.md#the-question-card). So esc here means what it
// means everywhere: it leaves, it changes nothing, and it loses nothing
// (docs/interface/principles.md#esc-is-always-the-safe-answer).
//
// The card is an offer rather than a toll gate. A reader who would rather
// explain than pick is not made to pick first, and a reader who pressed esc
// by reflex does not have to answer in prose to get the list back: the
// handover chord brings the card back, which is the same act it is on every
// other decision — give the keyboard to the one that is waiting
// (reopenQuestion).
func (m Model) asideQuestion() (tea.Model, tea.Cmd) {
	m.question = nil
	// The keyboard goes back to the draft with the decision still waiting,
	// and nothing is left on the screen to say so — which is why the notice
	// rail counts it (frame.go).
	m.releaseDecision()
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	return m, nil
}

// questionAside reports a question the reader handed to the draft: no card on
// the screen, the call still outstanding, and the next message the answer.
//
// It is derived rather than stored so that it cannot outlive the question it
// describes. Both halves of it are cleared by every path that ends a
// question — an answer resolves the request, a cancel drops it — so there is
// no third fact to keep in step with those two.
func (m Model) questionAside() bool {
	req := m.pendingApproval
	return m.question == nil && req != nil && req.kind == approvalQuestion &&
		m.turnState() == stateQuestion
}

// outstandingQuestion is the question waiting for an answer — on the card, or
// set aside behind the draft — and whether there is one at all. The two are
// one fact to everything that asks what the session is waiting on: the
// summons, the notice rail, and the sentence that answers it.
func (m Model) outstandingQuestion() (ask.Question, bool) {
	if c := m.question; c != nil {
		if c.submit {
			// The tab that ends the set has no question of its own, so what
			// the session is waiting on is the call's first, which is what
			// the request carries for exactly this.
			return m.pendingApproval.question, true
		}
		return c.q, true
	}
	if m.questionAside() {
		return m.pendingApproval.question, true
	}
	return ask.Question{}, false
}

// questionsWaiting is how many questions the reader's next sentence is about
// to answer, which on a call that asked several is the tabs still open rather
// than the one card holding them: a rail reading `1 question waiting` over
// three of them would undercount what one sentence is about to do.
//
// It is never nought. A card whose tabs are all answered and not yet sent is
// still a call waiting on the reader, and the sentence they type still goes to
// it rather than into the conversation — which is the whole of what the count
// is there to say (docs/interface/surfaces.md#the-question-card).
func (m Model) questionsWaiting() int {
	req := m.pendingApproval
	if req == nil || req.sheet == nil {
		return 1
	}
	return max(1, len(req.sheet.qs)-req.sheet.answered())
}

// reopenQuestion draws the card again for a question that was set aside. It
// answers nothing: the card comes back exactly as it arrived, with the
// question still waiting.
func (m *Model) reopenQuestion() {
	if m.questionAside() {
		m.openQuestion(m.pendingApproval)
	}
}

// answerTyped delivers a sentence as the answer to a question that was set
// aside, in the reader's own words.
//
// It is not also a user message, and it is not a steer. A steer joins the
// conversation as a new instruction the model must reconcile with what it was
// doing; an answer resolves a call the model is already blocked on. Delivered
// as a steer it would leave the call outstanding for the rest of the turn,
// and the model would be told to change course by someone it thinks it is
// still waiting on. One sentence is one thing, and a reader who answered a
// question has not additionally changed the subject
// (docs/capabilities/coding-agent.md#the-model-can-ask).
//
// Both doors a sentence can arrive by come here — enter while the turn runs,
// and the follow-up queue when it is dispatched — so which door it came in by
// is not a fact about what it answers.
// A card that carried several questions takes the sentence as the answer to
// every one of them still open. Esc set the whole card down rather than one
// tab of it, so the reader who would rather explain than pick explained once,
// and a call that asked three questions is not three sentences.
func (m Model) answerTyped(text string) (tea.Model, tea.Cmd) {
	a := ask.Answer{Answered: ask.AnsweredTyped, Note: text}
	req := m.pendingApproval
	if req == nil || req.sheet == nil {
		return m.answerQuestion(a)
	}
	open := false
	for i, given := range req.sheet.answers {
		if given == nil {
			one := a
			req.sheet.answers[i] = &one
			open = true
		}
	}
	if !open {
		// Every tab was answered and the card set down before it was sent, so
		// the sentence is not an answer to a question — it is the words
		// beside the picks, which is what the note field on every one of them
		// is for. It goes where there are none; an answer that carries its own
		// was written against that question and wins.
		for _, given := range req.sheet.answers {
			if strings.TrimSpace(given.Note) == "" {
				given.Note = text
			}
		}
	}
	return m.submitQuestions()
}

// overBudgetRule is what answered a question the turn had no budget left
// for, in the row's own outcome field. It names the rule and not a person,
// because nobody saw this one.
const overBudgetRule = "over the turn's budget"

// answerQuestion takes one answer. On a call that asked one question that is
// the whole of it and the call resolves; on a call that asked several it fills
// in one tab and moves to the next question still open, because a call is
// answered once and not three times.
func (m Model) answerQuestion(a ask.Answer) (tea.Model, tea.Cmd) {
	if c := m.question; c != nil && c.sheet != nil && !c.submit {
		c.sheet.record(a)
		c.sheet.forward()
		m.question = c.sheet.page()
		return m, nil
	}
	return m.answerQuestionAs(a, "")
}

// answerQuestionAs is the same, for an answer no reader gave: rule names what
// gave it instead, and its presence is what keeps the record from counting an
// interruption that never happened.
//
// An answer nobody gave answers the whole call, which on a call that asked
// several questions is every one of them: nothing was put to anybody, so
// there is no tab that fared differently.
func (m Model) answerQuestionAs(a ask.Answer, rule string) (tea.Model, tea.Cmd) {
	req := m.pendingApproval
	if req == nil {
		m.question = nil
		return m, nil
	}
	qs, as := []ask.Question{req.question}, []ask.Answer{a}
	if req.sheet != nil {
		qs = req.sheet.qs
		as = make([]ask.Answer, len(qs))
		for i := range as {
			as[i] = a
		}
	}
	return m.resolveQuestion(req, qs, as, rule)
}

// submitQuestions sends every answer a tabbed card gathered, in the order the
// questions were sent, with the tabs nobody answered going back as `skipped`.
func (m Model) submitQuestions() (tea.Model, tea.Cmd) {
	req := m.pendingApproval
	if req == nil || req.sheet == nil {
		return m, nil
	}
	return m.resolveQuestion(req, req.sheet.qs, req.sheet.replies(), "")
}

// resolveQuestion resolves the call through the same seam every other answered
// decision goes through, so an interrupted turn gives an outstanding question
// the cancelled turn's synthetic result with no handling of its own.
func (m Model) resolveQuestion(req *approvalRequest, qs []ask.Question, as []ask.Answer, rule string) (tea.Model, tea.Cmd) {
	if rule == "" {
		m.recordDecision(observe.DecisionAsk, observe.ReasonUser)
	}
	// The question goes into the same window every other interaction does, so
	// a model that has put one question twice is told it already has the
	// answer. The sentence comes back rather than leading the result, because
	// this result is the JSON the answer is read out of (repeat.go). On a call
	// that asked several it rides the first answer: it is about the asking
	// rather than about one of the questions, and the first entry is the one
	// the model reads first.
	if notice := m.repeats.AskedBefore(json.RawMessage(req.call.Arguments)); notice != "" && len(as) > 0 && as[0].Notice == "" {
		as[0].Notice = notice
		m.signal(observe.SignalRepeat, req.call.Name)
	}
	result := ask.Reply(qs, as)
	m.question = nil
	m.pendingApproval = nil
	m.releaseDecision()
	m.agent.ResolveApproval(result)
	m.recordToolResult(req.call.Name, 0, result)
	m.appendEntry(entry{
		kind:       entryTool,
		toolName:   req.call.Name,
		toolArgs:   req.call.Arguments,
		toolResult: result,
		answered:   outcomeOf(as),
		answerRule: rule,
	})
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m.advanceApprovalQueue()
}

// buildQuestionApproval turns an ask call into its request. An unreadable
// question is a skipped call with the parse error as its result, the way
// every other malformed call is answered.
func (m Model) buildQuestionApproval(tc provider.ToolCall) (*approvalRequest, error) {
	qs, err := ask.Parse(json.RawMessage(tc.Arguments))
	if err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	q := qs[0]
	req := &approvalRequest{
		call:     tc,
		kind:     approvalQuestion,
		title:    "ask",
		summary:  firstLine(q.Question),
		question: q,
	}
	if len(qs) > 1 {
		// Several questions in one call are one card with a tab per question.
		// The first is still the request's own, because everything that asks
		// what the session is waiting on — the queue strip, the summons, the
		// transcript row — wants one question and this is the one the card
		// opens on.
		req.sheet = &questionSheet{qs: qs, answers: make([]*ask.Answer, len(qs))}
		req.summary = fmt.Sprintf("%s · and %d more", firstLine(q.Question), len(qs)-1)
	}
	return req, nil
}
