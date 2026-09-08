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
	m.questionsAsked++
	m.recordDecision(observe.DecisionAsk, observe.ReasonUser)
	m.openQuestion(req)
	m.pendingQueue, m.pendingBatch = m.resolveQueue(req)
	m.setTurnState(stateQuestion)
	m.syncViewport()
	return m, nil
}

// openQuestion builds the card for a parsed question and puts it up.
func (m *Model) openQuestion(req *approvalRequest) {
	c := &questionCard{q: req.question}
	title := firstLine(req.question.Question)
	switch req.question.Shape {
	case ask.ShapeChoose:
		c.rows = questionRows(req.question.Options)
		c.sel = components.NewNoteSelect(title, selectRows(c.rows))
		c.sel.Select.MaxLines = m.maxConfirmPanelHeight() - 1
	case ask.ShapeChooseMany:
		c.rows = questionRows(req.question.Options)
		c.multi = components.NewMultiSelect(title, selectRows(c.rows))
		c.multi.Note = components.NewNoteBox()
		c.multi.MaxLines = m.maxConfirmPanelHeight() - 1
	case ask.ShapeConfirm:
		c.conf = &components.Confirm{Prompt: title}
		c.note = components.NewNoteBox()
	case ask.ShapeText:
		// No options above the field: the answer is the note, so the card
		// opens with the keyboard already in it.
		c.sel = components.NewNoteSelect(title, nil)
		c.sel.Require = true
		c.openNote()
	}
	if req.question.Note == ask.NoteRequired {
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
		c.openNote()
	}
	m.question = c
}

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
	switch {
	case c.sel != nil:
		lines = append(lines, strings.Split(c.sel.View(width), "\n")...)
	case c.multi != nil:
		lines = append(lines, strings.Split(c.multi.View(width), "\n")...)
	case c.conf != nil:
		lines = append(lines, components.Clip(c.conf.View(width), width))
		lines = append(lines, c.note.Rows(width)...)
		lines = append(lines, questionConfirmKeys()...)
	}
	return lines
}

// questionConfirmKeys is the yes-or-no shape's key row. The two list
// dressings draw their own from the register; the inline confirm has no card
// to draw one in, so the card states it here — from the register, not from a
// spelling written down beside it.
func questionConfirmKeys() []string {
	segs := []string{
		keys.Bracket(keys.Confirm.Yes) + " " + keys.Words(keys.Confirm.Yes),
		keys.Bracket(keys.Confirm.No) + " " + keys.Words(keys.Confirm.No),
		keys.Bracket(keys.Select.Note) + " note",
	}
	return []string{components.DimText(strings.Join(segs, " · "))}
}

// updateQuestion answers one key on the card. It is reached only once the
// card holds the keyboard, like every other decision.
func (m Model) updateQuestion(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := m.question
	if c == nil {
		return m, nil
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
	// there is no pick for esc to keep and it leaves outright.
	if keys.Match(msg, keys.Draft.Clear) && len(c.rows) > 0 && c.noteHolds() {
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
		return c.q, true
	}
	if m.questionAside() {
		return m.pendingApproval.question, true
	}
	return ask.Question{}, false
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
func (m Model) answerTyped(text string) (tea.Model, tea.Cmd) {
	return m.answerQuestion(ask.Answer{Answered: ask.AnsweredTyped, Note: text})
}

// overBudgetRule is what answered a question the turn had no budget left
// for, in the row's own outcome field. It names the rule and not a person,
// because nobody saw this one.
const overBudgetRule = "over the turn's budget"

// answerQuestion resolves the call through the same seam every other answered
// decision goes through, so an interrupted turn gives an outstanding question
// the cancelled turn's synthetic result with no handling of its own.
func (m Model) answerQuestion(a ask.Answer) (tea.Model, tea.Cmd) {
	return m.answerQuestionAs(a, "")
}

// answerQuestionAs is the same, for an answer no reader gave: rule names what
// gave it instead, and its presence is what keeps the record from counting an
// interruption that never happened.
func (m Model) answerQuestionAs(a ask.Answer, rule string) (tea.Model, tea.Cmd) {
	req := m.pendingApproval
	if req == nil {
		m.question = nil
		return m, nil
	}
	if rule == "" {
		m.recordDecision(observe.DecisionAsk, observe.ReasonUser)
	}
	// The question goes into the same window every other interaction does, so
	// a model that has put one question twice is told it already has the
	// answer. The sentence comes back rather than leading the result, because
	// this result is the JSON the answer is read out of (repeat.go).
	if notice := m.repeats.AskedBefore(json.RawMessage(req.call.Arguments)); notice != "" && a.Notice == "" {
		a.Notice = notice
		m.signal(observe.SignalRepeat, req.call.Name)
	}
	result := a.Result()
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
		answered:   a.Answered,
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
	// Several questions in one call is the tab strip, which this card does
	// not draw; the parse already answers in a list so the card can grow one
	// without the seam changing.
	q := qs[0]
	return &approvalRequest{
		call:     tc,
		kind:     approvalQuestion,
		title:    "ask",
		summary:  firstLine(q.Question),
		question: q,
	}, nil
}
