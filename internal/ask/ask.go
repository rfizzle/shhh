// Package ask is the model-facing half of a question put to the person: the
// tool the model calls, the four shapes a question can take, and the answer
// that travels back as the result of the call.
//
// A question is a decision and not an act, which is why nothing here talks
// about approval: no mode, no standing grant and no classifier can answer one
// on the reader's behalf.
// See docs/capabilities/coding-agent.md#the-model-can-ask.
package ask

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/rfizzle/shhh/internal/provider"
)

// ToolName is the model-facing question tool. It is registered only where
// there is somebody to answer it, and refused by nothing: a run with nobody
// in front of it never sees the tool at all.
// See docs/capabilities/coding-agent.md#nobody-to-ask.
const ToolName = "ask"

// Shape is what kind of answer a question wants, and with it which dressing
// of the selector the card draws
// (docs/interface/surfaces.md#the-question-card). It is closed: a shape that
// is not one of these four is a parse error naming all four, so a model that
// invents one is told what it may have rather than left guessing.
type Shape string

const (
	// ShapeChoose is one answer from a list.
	ShapeChoose Shape = "choose"
	// ShapeChooseMany is any number of answers from a list.
	ShapeChooseMany Shape = "choose_many"
	// ShapeConfirm is a yes-or-no.
	ShapeConfirm Shape = "confirm"
	// ShapeText is a short answer in the reader's own words.
	ShapeText Shape = "text"
)

// Shapes is the closed set, in the order the tool's description names them.
var Shapes = []Shape{ShapeChoose, ShapeChooseMany, ShapeConfirm, ShapeText}

// shapeList is the four spelled for a refusal, so an unknown shape is
// answered with what the model may have written instead.
func shapeList() string {
	names := make([]string, 0, len(Shapes))
	for _, s := range Shapes {
		names = append(names, string(s))
	}
	return strings.Join(names, ", ")
}

// Note says whether the question wants a note beside the pick. Every shape
// offers one; "required" is the model saying a pick on its own is not enough
// to act on, and opens the field with the card.
type Note string

const (
	// NoteOptional is the default: the field is there and may be left empty.
	NoteOptional Note = "optional"
	// NoteRequired opens the field with the card and refuses an empty one.
	NoteRequired Note = "required"
)

// MaxQuestionLen bounds the question itself. The card has forty per cent of
// the terminal and the options are what a key can land on, so a question
// longer than a short paragraph would push the answers off the panel it is
// asking on (docs/interface/principles.md#one-interaction-panel).
const MaxQuestionLen = 400

// MaxOptions bounds a list for the same reason: past this the card is a
// catalog, and a catalog is walked rather than answered.
const MaxOptions = 8

// MaxLabelLen bounds one option's label, which is the only part of a row that
// is never dropped when the terminal is narrow.
const MaxLabelLen = 80

// PerTurnBudget is how many questions one turn may put to the person. One
// past it is answered Skipped without reaching anybody, and the budget is
// spent by the asking rather than by the answering: a question that reached
// the reader has already cost them the interruption whether or not they have
// got round to it, and refunding the ones they set down would make setting a
// question down cost more than answering it.
//
// The count is the unit the reader feels. A turn runs for four minutes or for
// forty, and "how often does this thing interrupt me" is a fact about the
// piece of work rather than about elapsed time, which is why this is a count
// and not a rate.
//
// Three is chosen against the two shapes it has to tell apart. It must not
// catch the legitimate pair — a question, its answer, and the one follow-up
// that answer made obvious — which is two, with a third left over for the
// turn that genuinely forks twice. It must catch the failure: four or more
// independent forks in one turn is a turn that should have stopped once and
// put the whole fork in prose, because a reader answering a fourth question
// has lost the thread of the first. Raising it buys a turn nothing it does
// not already have and costs the reader the interruption the number exists to
// bound; lowering it to two refuses the obvious follow-up, which is the one
// question the first answer has already earned.
// See docs/capabilities/coding-agent.md#the-model-can-ask.
const PerTurnBudget = 3

// MaxQuestions bounds how many questions one call may carry, because several
// in one call are drawn as tabs on one card and the card has forty per cent
// of the terminal (docs/interface/principles.md#one-interaction-panel). On a
// thirty-row terminal that is twelve rows, and a tab strip, a question line,
// four option rows and a key row is already eleven of them: a fifth tab
// could only be paid for out of the answers. So the fifth question is
// refused, naming the limit, rather than half-drawn.
//
// It is larger than PerTurnBudget and does not contradict it, because the two
// bound different things. The budget bounds interruptions — how often a turn
// stops and takes the keyboard — and one card carrying four questions is one
// interruption, which is the shape the budget is asking the model for. This
// bounds what fits on that one card.
const MaxQuestions = 4

// Option is one answer the model offers.
type Option struct {
	// Label is the answer itself, and is what comes back: an answer names
	// the label and never the index, so reordering the list cannot change
	// what an answer meant.
	Label string
	// Detail is the option's own continuation, drawn beside it.
	Detail string
	// Field is the short right-aligned field — a file count, a cost — so a
	// list of approaches can be compared rather than walked.
	Field string
	// Recommended leads the list and says so in a word.
	Recommended bool
	// Unavailable is why this answer cannot be taken here. An empty string
	// is an answer that can. The reason is stated on the row and re-stated
	// if the row is taken, because a row that only dimmed would be a row
	// whose refusal is a colour (docs/interface/principles.md#colour-never-carries-meaning-alone).
	Unavailable string
}

// Question is one question the model is putting to the person.
type Question struct {
	Question string
	Shape    Shape
	Options  []Option
	Note     Note
}

// Answered is the closed vocabulary of how an answer was given. The model is
// told which of the four it got, so it can tell a pick from a sentence from a
// question nobody answered.
// See docs/capabilities/coding-agent.md#the-model-can-ask.
type Answered string

const (
	// AnsweredOnCard is a pick from the list the model wrote.
	AnsweredOnCard Answered = "on the card"
	// AnsweredTyped is the reader's own words in place of a pick.
	AnsweredTyped Answered = "typed"
	// AnsweredSkipped is a question nobody chose an answer to: nothing was
	// picked, nothing was typed, and the model is told to state the
	// assumption and carry on. Leaving the card is not this — a question the
	// reader sets down is still outstanding, and the next message they send
	// is the answer (docs/interface/surfaces.md#the-question-card). What it is
	// is a question that reached nobody: the turn had no budget left to put
	// it (OverBudget).
	AnsweredSkipped Answered = "skipped"
	// AnsweredNobody is a question that had somebody to ask and lost them.
	AnsweredNobody Answered = "nobody to ask"
)

// ReaderAnswers is the closed set of answers a person can give, in the order
// the tool's description names them.
//
// AnsweredNobody is deliberately not among them. It is what a question gets
// when the person it was put to went away, which only the surface holding the
// question can know — so a client answering one over a protocol is held to
// these three and cannot claim the reader's absence on the reader's behalf
// (docs/capabilities/headless.md#a-client-answers-one-call-at-a-time).
var ReaderAnswers = []Answered{AnsweredOnCard, AnsweredTyped, AnsweredSkipped}

// answerList is the three spelled for a refusal, the way shapeList spells the
// four shapes.
func answerList() string {
	names := make([]string, 0, len(ReaderAnswers))
	for _, a := range ReaderAnswers {
		names = append(names, string(a))
	}
	return strings.Join(names, ", ")
}

// carryOn is what an unanswered question tells the model to do. A turn is
// never left waiting on a decision that is not coming, and a model told only
// "no answer" stops.
const carryOn = "state the assumption you would have asked about and carry on"

// Answer is what comes back from the card.
type Answer struct {
	// Answered is how it was answered.
	Answered Answered
	// Picked are the labels the reader took, in the order the card offered
	// them. Never indices: an index is a fact about a list the model wrote
	// and the reader may have reordered by picking the recommendation first.
	Picked []string
	// Note is the reader's own words beside the pick. An empty note is an
	// empty string and never a missing field, so the model never has to work
	// out whether a note was possible.
	Note string
	// Notice is what the run is told about the asking rather than about the
	// answer: that this question has already been put and answered, or that
	// the turn's budget for questions is spent. It is a field of the result
	// and never a line in front of it, which is where every other tool's
	// notice goes — the model reads its answer out of this JSON, and a
	// sentence above it would be one to skip past before finding what was
	// asked for.
	Notice string
}

// result is the wire shape of an answer. It is JSON because the model reads
// it: a sentence would have to be parsed back, which is the thing the whole
// tool exists to stop.
type result struct {
	// Ask is the question this answers, in the model's own words. It is
	// present only where a call carried several, because a call that asked
	// one question already knows which one came back and a field that was
	// always there would be a field to read on every answer.
	Ask      string   `json:"ask,omitempty"`
	Answered string   `json:"answered"`
	Picked   []string `json:"picked"`
	Note     string   `json:"note"`
	// Instruction is present only where nothing was chosen, and says what to
	// do about that.
	Instruction string `json:"instruction,omitempty"`
	// Notice is present only where there is something to say about the
	// asking itself.
	Notice string `json:"notice,omitempty"`
}

// wire is the answer as the model reads it, with no question beside it.
func (a Answer) wire() result {
	r := result{Answered: string(a.Answered), Picked: a.Picked, Note: a.Note, Notice: a.Notice}
	if r.Picked == nil {
		// An empty list and not null, for the reason the note is an empty
		// string: the field's absence would be a second thing to interpret.
		r.Picked = []string{}
	}
	switch a.Answered {
	case AnsweredSkipped, AnsweredNobody:
		r.Instruction = carryOn
	}
	return r
}

// Result is the tool result the model reads.
func (a Answer) Result() string {
	// The struct has no field that can fail to marshal.
	out, _ := json.Marshal(a.wire())
	return string(out)
}

// OverBudget is the answer to a question past the turn's budget: nothing was
// drawn, so nothing was chosen. The sentence says which fact this is rather
// than leaving a bare skip to be read as a reader who had nothing to say —
// without it a model spends the rest of its rounds asking again and being
// skipped again.
func OverBudget() Answer {
	return Answer{
		Answered: AnsweredSkipped,
		Notice: fmt.Sprintf(
			"this turn's budget of %d questions is spent, so this one was not put to anybody. "+
				"Do not ask again in this turn.", PerTurnBudget),
	}
}

// Reply is the whole of what one call gets back: the answer where it asked
// one question, and a list of them where it asked several.
//
// The list is in the order the questions were sent and every entry names its
// own question, so a model reading them back never has to match an answer to
// a question by counting. The reader may have answered the third tab first,
// and a list that could only be read positionally would make the order the
// answers were *given* in a fact the model has to reason about.
//
// A call that asked one question keeps the shape it has always had. The
// result's shape is a fact about the call rather than about the answering,
// which is one rule stated once rather than two shapes to tell apart.
func Reply(qs []Question, as []Answer) string {
	if len(qs) < 2 {
		if len(as) == 0 {
			return Nobody().Result()
		}
		return as[0].Result()
	}
	out := make([]result, 0, len(qs))
	for i, q := range qs {
		// A question the run never reached an answer for is one whose reader
		// went away, which is what Nobody says and what the loop that
		// collected these would have written anyway.
		a := Nobody()
		if i < len(as) {
			a = as[i]
		}
		r := a.wire()
		r.Ask = q.Question
		out = append(out, r)
	}
	res, _ := json.Marshal(out)
	return string(res)
}

// Nobody is the answer to a question that had somebody to ask and lost them —
// the reader's client went away with the question outstanding. It is minted
// where that loss is noticed and nowhere else, because nowhere else knows it.
func Nobody() Answer {
	return Answer{Answered: AnsweredNobody}
}

// Validate holds an answer to the rules the card enforces while it collects
// one, for a surface that did not draw the card itself: a client on the
// protocol collects the answer in a window of its own, and an answer that
// arrived from somewhere else still has to be one the model can act on.
//
// The rules are the card's rather than a second set. An on-the-card answer
// with nothing picked is a typed one; the words are the whole of a typed
// answer; and a note the model said it needs is refused empty. Skipping is
// always allowed, because it says nothing was chosen, which is what it is for.
func (a Answer) Validate(q Question) error {
	switch a.Answered {
	case AnsweredOnCard:
		if len(a.Picked) == 0 {
			return fmt.Errorf("%q picked nothing — an answer in the reader's own words is %q", AnsweredOnCard, AnsweredTyped)
		}
	case AnsweredTyped:
		if strings.TrimSpace(a.Note) == "" {
			return fmt.Errorf("%q is the reader's own words and this one carries none", AnsweredTyped)
		}
	case AnsweredSkipped:
		return nil
	default:
		return fmt.Errorf("unknown answer %q (valid: %s)", a.Answered, answerList())
	}
	if q.Note == NoteRequired && strings.TrimSpace(a.Note) == "" {
		return fmt.Errorf("the question was asked with a required note and the answer carries none")
	}
	return nil
}

// ToolDefinition is the ask tool the session registers where there is
// somebody to answer it.
func ToolDefinition() provider.Tool {
	return provider.Tool{
		Name: ToolName,
		Description: "Put a question to the person and wait for their answer. " +
			"Only for a fork you cannot decide and where the answers would lead to materially different work: " +
			"which of several approaches, whether a change should reach further, what a thing should be called. " +
			"Where the answers would lead to the same work, and for anything the request, the tree or the project's own documents already answer, do not ask: state the assumption you would have asked about and carry on. Asking instead of reading spends the person's attention. " +
			"A turn has room for only a few questions, and one past that count is answered skipped without reaching anybody. " +
			"Offer the answers you can see; the person can always answer with something you did not offer, or leave a note beside their pick. " +
			"You are told how they answered: a pick on the card, typed text, skipped, or nobody to ask. " +
			"An answer of skipped means nobody chose — state the assumption you would have asked about and carry on. " +
			"Where you need several answers before you can start, ask for them in one call through `questions` (at most four): " +
			"they are put on one card as tabs, so the person sees the third before answering the first, " +
			"and the answers come back in the order you sent them, each naming its own question.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				` + questionProperties + `,
				"questions": {
					"type": "array",
					"description": "Several questions in one call, at most four, drawn as tabs on one card with a submit at the end. Use this instead of the fields above, never as well as them.",
					"items": {
						"type": "object",
						"properties": {` + questionProperties + `},
						"required": ["question", "shape"]
					}
				}
			}
		}`),
	}
}

// questionProperties is one question's own fields. It is written once and
// spliced into both places a question can be written — the call itself, and
// an entry in its list — because two spellings of one shape is the drift the
// parse would then have to forgive.
//
// Neither place can be required at the top level for that reason: a call
// writes one or the other, and the refusal that says so is the parse's, where
// it can name which of the two was missing.
const questionProperties = `
				"question": {"type": "string", "description": "The question itself, in one or two short sentences"},
				"shape": {"type": "string", "enum": ["choose", "choose_many", "confirm", "text"], "description": "choose: one answer from the options. choose_many: any number of them. confirm: yes or no, no options. text: a short answer in their own words, no options"},
				"options": {
					"type": "array",
					"description": "The answers you can see, for choose and choose_many only. Two or more.",
					"items": {
						"type": "object",
						"properties": {
							"label": {"type": "string", "description": "The answer itself; this is what comes back"},
							"detail": {"type": "string", "description": "One short clause saying what taking it means"},
							"field": {"type": "string", "description": "A short comparable field: a file count, a cost, a duration"},
							"recommended": {"type": "boolean", "description": "Lead with this one as your recommendation; at most one option"},
							"unavailable": {"type": "string", "description": "Why this answer cannot be taken here; omit for an answer that can"}
						},
						"required": ["label"]
					}
				},
				"note": {"type": "string", "enum": ["optional", "required"], "description": "optional (the default) offers a note field beside the pick; required refuses an answer without one"}`

// questionArgs is one question as the model writes it. The same object
// whether it arrives as the call itself or as one entry in the call's list,
// which is why the parse below reads only this shape.
type questionArgs struct {
	Question string `json:"question"`
	Shape    string `json:"shape"`
	Note     string `json:"note"`
	Options  []struct {
		Label       string `json:"label"`
		Detail      string `json:"detail"`
		Field       string `json:"field"`
		Recommended bool   `json:"recommended"`
		Unavailable string `json:"unavailable"`
	} `json:"options"`
}

// QuestionText is the question an ask call is putting, or "" where the call
// cannot be read as one at all. It is the whole of what makes two calls the
// same question, and the only part of a call a reader telling one asking from
// another should be held to: a model that re-asks with its options reworded
// has asked the same thing twice.
//
// A call carrying several is all of them, in the order it sent them, because
// the card puts them as one asking: the same three questions asked again are
// the same asking again, and two calls that share one question out of three
// are not.
func QuestionText(raw json.RawMessage) string {
	var args struct {
		Question  string `json:"question"`
		Questions []struct {
			Question string `json:"question"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return ""
	}
	if len(args.Questions) == 0 {
		return strings.TrimSpace(args.Question)
	}
	asked := make([]string, 0, len(args.Questions))
	for _, q := range args.Questions {
		asked = append(asked, strings.TrimSpace(q.Question))
	}
	return strings.Join(asked, "\x00")
}

// Parse validates an ask call's arguments, in the order they were written.
//
// A call carries one question or a list of them, never both: the two spell
// the same thing and a call that wrote both would have to be told which half
// the reader saw. The list is capped at MaxQuestions, which is the card's
// number and not a taste.
func Parse(raw json.RawMessage) ([]Question, error) {
	var args struct {
		questionArgs
		Questions []questionArgs `json:"questions"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if len(args.Questions) == 0 {
		q, err := parseQuestion(args.questionArgs)
		if err != nil {
			return nil, err
		}
		return []Question{q}, nil
	}
	if strings.TrimSpace(args.Question) != "" || strings.TrimSpace(args.Shape) != "" {
		return nil, fmt.Errorf("a call asks one question or a list of them, not both — put every question in %q", "questions")
	}
	if len(args.Questions) > MaxQuestions {
		return nil, fmt.Errorf("too many questions (%d, max %d) — the card draws them as tabs and has forty per cent of the terminal; ask the rest once these are answered", len(args.Questions), MaxQuestions)
	}
	qs := make([]Question, 0, len(args.Questions))
	for i, one := range args.Questions {
		q, err := parseQuestion(one)
		if err != nil {
			// Numbered from one, in the order they were sent, because that
			// is the order the tabs are drawn in and the order the answers
			// come back in.
			return nil, fmt.Errorf("question %d: %w", i+1, err)
		}
		qs = append(qs, q)
	}
	return qs, nil
}

// parseQuestion validates one question, wherever it was written.
func parseQuestion(args questionArgs) (Question, error) {
	q := Question{Question: strings.TrimSpace(args.Question)}
	if q.Question == "" {
		return Question{}, fmt.Errorf("question is required")
	}
	if n := utf8.RuneCountInString(q.Question); n > MaxQuestionLen {
		// Counted in characters and not in bytes, because the cap is about
		// what fits on a card and the model is told the number in the units
		// the message names.
		return Question{}, fmt.Errorf("question is too long (%d chars, max %d) — a question is one or two short sentences", n, MaxQuestionLen)
	}
	switch Shape(args.Shape) {
	case ShapeChoose, ShapeChooseMany, ShapeConfirm, ShapeText:
		q.Shape = Shape(args.Shape)
	default:
		return Question{}, fmt.Errorf("unknown shape %q (valid: %s)", args.Shape, shapeList())
	}
	switch Note(args.Note) {
	case "":
		q.Note = NoteOptional
	case NoteOptional, NoteRequired:
		q.Note = Note(args.Note)
	default:
		return Question{}, fmt.Errorf("unknown note %q (valid: optional, required)", args.Note)
	}
	listed := q.Shape == ShapeChoose || q.Shape == ShapeChooseMany
	if !listed && len(args.Options) > 0 {
		return Question{}, fmt.Errorf("shape %q takes no options — use choose or choose_many for a question with a list", q.Shape)
	}
	if listed {
		if len(args.Options) < 2 {
			return Question{}, fmt.Errorf("shape %q needs at least two options — use confirm for a yes-or-no and text for a free answer", q.Shape)
		}
		if len(args.Options) > MaxOptions {
			return Question{}, fmt.Errorf("too many options (%d, max %d) — a list past that is a catalog rather than a choice", len(args.Options), MaxOptions)
		}
	}
	seen := map[string]bool{}
	recommended := 0
	for _, o := range args.Options {
		label := strings.TrimSpace(o.Label)
		if label == "" {
			return Question{}, fmt.Errorf("every option needs a label")
		}
		if n := utf8.RuneCountInString(label); n > MaxLabelLen {
			return Question{}, fmt.Errorf("option label is too long (%d chars, max %d)", n, MaxLabelLen)
		}
		if seen[label] {
			// Two rows with one label would come back as one answer and the
			// reader could not tell which they took.
			return Question{}, fmt.Errorf("two options share the label %q", label)
		}
		seen[label] = true
		if o.Recommended {
			recommended++
			if strings.TrimSpace(o.Unavailable) != "" {
				// A row that leads the list and cannot be taken is a
				// recommendation against itself, and the reader would have
				// to work out which half of it to believe.
				return Question{}, fmt.Errorf("option %q is recommended and unavailable at once", label)
			}
		}
		q.Options = append(q.Options, Option{
			Label:       label,
			Detail:      strings.TrimSpace(o.Detail),
			Field:       strings.TrimSpace(o.Field),
			Recommended: o.Recommended,
			Unavailable: strings.TrimSpace(o.Unavailable),
		})
	}
	if recommended > 1 {
		return Question{}, fmt.Errorf("%d options are marked recommended — a recommendation is one answer", recommended)
	}
	return q, nil
}
