package eval

// The other side of a conversation, written down.
//
// A case that needs a clarifying turn or an answer to an approval cannot be
// settled by one prompt, so it is served (serve.go) and somebody has to be at
// the other end. That somebody is a script: a list of rules over the three
// things a served session puts to its client — a question, an approval, and a
// turn's close — each answered by the first rule whose expression matches.
// Nothing here is a model, so the asking side cannot drift between attempts;
// the agent's side is as variable as it always is, and --repeat tells a flaky
// case from a failing one exactly as it does for a single turn.
//
// The script answers and never decides. A case's verdict is its check and its
// declared assertions, and nothing reads what the script said for one.
// See docs/capabilities/evals.md#a-case-can-have-somebody-on-the-other-side.

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/rfizzle/shhh/internal/rpc"
)

// The events a persona line may name, and the words it answers with.
const (
	personaQuestion = "question"
	personaApproval = "approval"
	personaClose    = "close"
	// personaStop is a close rule's answer that ends the conversation rather
	// than starting another turn.
	personaStop = "stop"
	// personaArrow separates a line's expression from its answer.
	personaArrow = "=>"
	// noPreference is what a question no rule matched is answered: what a
	// person who had not thought about it would say, and not an invented
	// choice the case author never wrote.
	noPreference = "no preference"
)

// personaEvents is the closed set a line may begin with.
var personaEvents = []string{personaQuestion, personaApproval, personaClose}

// asker is the asking side of a served conversation, and the whole of what it
// may read: a question's text and the labels it offers, an approval's tool
// name, and the final text of the turn that just closed, with how many turns
// it has had. Every argument is a string, a list of strings or a count, so a
// tool result, the workspace, the case's check and its facts have no way in.
//
// A model standing in for the person would sit behind this same interface and
// add what it spent into the attempt under the same price table. None is built:
// a script answers every case the suite has.
type asker interface {
	answer(question string, options []string) (picked []string, note string, scripted bool)
	approve(tool string) (decision string, scripted bool)
	next(final string, taken int) (prompt string, more bool)
}

// rule is one persona line: which event it answers, the expression that has
// to match it, and the answer.
type rule struct {
	event  string
	match  *regexp.Regexp
	answer string
}

// persona is a case's script, and turns the ceiling on the user turns it may
// take, the first prompt included.
type persona struct {
	rules []rule
	turns int
}

// parsePersona reads a case's persona and turns keys. Every refusal names the
// file and the line, because the reader is editing it.
//
// The answer is everything after the first arrow, so it may hold one itself;
// an expression may not, and there is no escape for it.
func parsePersona(lines []string, turns int, path string) (*persona, error) {
	if len(lines) == 0 {
		if turns != 0 {
			return nil, fmt.Errorf("%s: turns needs a persona — it is the ceiling on the turns somebody answers, and without one there is nobody to take a second", path)
		}
		return nil, nil
	}
	if turns < 1 {
		return nil, fmt.Errorf("%s: a persona needs turns of one or more — the ceiling on the user turns, the first prompt included", path)
	}
	p := &persona{turns: turns}
	for i, line := range lines {
		r, err := parseRule(line)
		if err != nil {
			return nil, fmt.Errorf("%s: persona line %d %q: %w", path, i+1, line, err)
		}
		p.rules = append(p.rules, r)
	}
	return p, nil
}

func parseRule(line string) (rule, error) {
	event, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
	if !slices.Contains(personaEvents, event) {
		return rule{}, fmt.Errorf("a line begins with %s", strings.Join(personaEvents, ", "))
	}
	expr, answer, ok := strings.Cut(rest, personaArrow)
	if !ok {
		return rule{}, fmt.Errorf(`a line is "<event> <expression> => <answer>", and this one has no arrow`)
	}
	expr, answer = strings.TrimSpace(expr), strings.TrimSpace(answer)
	if expr == "" {
		return rule{}, fmt.Errorf("the expression is empty — write .* to match everything")
	}
	if answer == "" {
		return rule{}, fmt.Errorf("the answer is empty")
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return rule{}, fmt.Errorf("the expression does not compile: %w", err)
	}
	if event == personaApproval && answer != rpc.DecisionAllow && answer != rpc.DecisionDeny {
		return rule{}, fmt.Errorf("an approval is answered %s or %s", rpc.DecisionAllow, rpc.DecisionDeny)
	}
	return rule{event: event, match: re, answer: answer}, nil
}

// first is the answer of the first rule for the event whose expression matches
// the text.
func (p *persona) first(event, text string) (string, bool) {
	for _, r := range p.rules {
		if r.event == event && r.match.MatchString(text) {
			return r.answer, true
		}
	}
	return "", false
}

// answer is the reply to a question. Where the answer is one of the offered
// labels it is a pick from the card; anywhere else it is the person's own
// words, which the protocol takes in place of a pick on every shape — a
// script that had to choose a label it did not write would be answering a
// question it was never given. scripted is false for a question no rule
// matched, which is answered "no preference" and counted on the row.
func (p *persona) answer(question string, options []string) (picked []string, note string, scripted bool) {
	text, scripted := p.first(personaQuestion, question)
	if !scripted {
		text = noPreference
	}
	for _, o := range options {
		if strings.EqualFold(strings.TrimSpace(o), text) {
			return []string{o}, "", scripted
		}
	}
	return nil, text, scripted
}

// approve is the decision on one gated call. A call no rule matched is
// allowed, which is what --yes answers for a workspace case and so the
// approval a served case starts from.
func (p *persona) approve(tool string) (decision string, scripted bool) {
	if d, ok := p.first(personaApproval, tool); ok {
		return d, true
	}
	return rpc.DecisionAllow, false
}

// next is what is said once a turn has closed: another prompt, or nothing.
// taken is how many turns the conversation has had, and at the ceiling it
// ends whatever the script says; a close no rule matched ends it too.
func (p *persona) next(final string, taken int) (prompt string, more bool) {
	if taken >= p.turns {
		return "", false
	}
	text, ok := p.first(personaClose, final)
	if !ok || text == personaStop {
		return "", false
	}
	return text, true
}
