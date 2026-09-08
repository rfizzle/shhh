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
	// is the answer (docs/interface/surfaces.md#the-question-card).
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
}

// result is the wire shape of an answer. It is JSON because the model reads
// it: a sentence would have to be parsed back, which is the thing the whole
// tool exists to stop.
type result struct {
	Answered string   `json:"answered"`
	Picked   []string `json:"picked"`
	Note     string   `json:"note"`
	// Instruction is present only where nothing was chosen, and says what to
	// do about that.
	Instruction string `json:"instruction,omitempty"`
}

// Result is the tool result the model reads.
func (a Answer) Result() string {
	r := result{Answered: string(a.Answered), Picked: a.Picked, Note: a.Note}
	if r.Picked == nil {
		// An empty list and not null, for the reason the note is an empty
		// string: the field's absence would be a second thing to interpret.
		r.Picked = []string{}
	}
	switch a.Answered {
	case AnsweredSkipped, AnsweredNobody:
		r.Instruction = carryOn
	}
	// The struct has no field that can fail to marshal.
	out, _ := json.Marshal(r)
	return string(out)
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
		Description: "Put one question to the person and wait for their answer. " +
			"Only for a fork you cannot decide and where the readings would lead to materially different work: " +
			"which of several approaches, whether a change should reach further, what a thing should be called. " +
			"Never for something the request, the tree or the project's own documents already answer — asking instead of reading spends the person's attention. " +
			"Offer the answers you can see; the person can always answer with something you did not offer, or leave a note beside their pick. " +
			"You are told how they answered: a pick on the card, typed text, skipped, or nobody to ask. " +
			"An answer of skipped means nobody chose — state the assumption you would have asked about and carry on.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
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
				"note": {"type": "string", "enum": ["optional", "required"], "description": "optional (the default) offers a note field beside the pick; required refuses an answer without one"}
			},
			"required": ["question", "shape"]
		}`),
	}
}

// Parse validates an ask call's arguments.
//
// It answers a slice because a call carries a list of questions the moment
// several in one call are drawn; today it is always one, and a caller that
// reads only the first is reading the whole of what a call may hold.
func Parse(raw json.RawMessage) ([]Question, error) {
	var args struct {
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
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	q := Question{Question: strings.TrimSpace(args.Question)}
	if q.Question == "" {
		return nil, fmt.Errorf("question is required")
	}
	if n := utf8.RuneCountInString(q.Question); n > MaxQuestionLen {
		// Counted in characters and not in bytes, because the cap is about
		// what fits on a card and the model is told the number in the units
		// the message names.
		return nil, fmt.Errorf("question is too long (%d chars, max %d) — a question is one or two short sentences", n, MaxQuestionLen)
	}
	switch Shape(args.Shape) {
	case ShapeChoose, ShapeChooseMany, ShapeConfirm, ShapeText:
		q.Shape = Shape(args.Shape)
	default:
		return nil, fmt.Errorf("unknown shape %q (valid: %s)", args.Shape, shapeList())
	}
	switch Note(args.Note) {
	case "":
		q.Note = NoteOptional
	case NoteOptional, NoteRequired:
		q.Note = Note(args.Note)
	default:
		return nil, fmt.Errorf("unknown note %q (valid: optional, required)", args.Note)
	}
	listed := q.Shape == ShapeChoose || q.Shape == ShapeChooseMany
	if !listed && len(args.Options) > 0 {
		return nil, fmt.Errorf("shape %q takes no options — use choose or choose_many for a question with a list", q.Shape)
	}
	if listed {
		if len(args.Options) < 2 {
			return nil, fmt.Errorf("shape %q needs at least two options — use confirm for a yes-or-no and text for a free answer", q.Shape)
		}
		if len(args.Options) > MaxOptions {
			return nil, fmt.Errorf("too many options (%d, max %d) — a list past that is a catalog rather than a choice", len(args.Options), MaxOptions)
		}
	}
	seen := map[string]bool{}
	recommended := 0
	for _, o := range args.Options {
		label := strings.TrimSpace(o.Label)
		if label == "" {
			return nil, fmt.Errorf("every option needs a label")
		}
		if n := utf8.RuneCountInString(label); n > MaxLabelLen {
			return nil, fmt.Errorf("option label is too long (%d chars, max %d)", n, MaxLabelLen)
		}
		if seen[label] {
			// Two rows with one label would come back as one answer and the
			// reader could not tell which they took.
			return nil, fmt.Errorf("two options share the label %q", label)
		}
		seen[label] = true
		if o.Recommended {
			recommended++
			if strings.TrimSpace(o.Unavailable) != "" {
				// A row that leads the list and cannot be taken is a
				// recommendation against itself, and the reader would have
				// to work out which half of it to believe.
				return nil, fmt.Errorf("option %q is recommended and unavailable at once", label)
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
		return nil, fmt.Errorf("%d options are marked recommended — a recommendation is one answer", recommended)
	}
	return []Question{q}, nil
}
