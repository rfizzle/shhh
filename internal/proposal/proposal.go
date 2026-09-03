// Package proposal reads what the one-shot generator answered with (
// docs/interface/surfaces.md#the-one-shot-result).
//
// A generator that can only say one thing has already chosen for you. Asked
// for "every process listening above 8000" a model will weigh lsof against
// netstat against ss, pick one, and throw the reasoning away — and the answer
// it kept is the portable one when you wanted the fast one about as often as
// not. The alternatives were free; only the surface for them was missing.
//
// The one line saying what the command does rides along for the same reason.
// The surface shows it under every command, and asking for it separately made
// it a second request that could only start once the first had finished — a
// whole round trip added to a screen that is one sentence and a row of keys.
// The model has already decided what the command does by the time it writes
// it, so the sentence costs a few tokens where the command was generated and
// nothing at all in latency.
//
// The response is therefore structured rather than a bare command string: the
// command first, exactly as it always arrived, then that sentence, then an
// optional section listing the ways it was not taken, each with the one
// phrase that says why you might want it instead.
//
// The shape is line-oriented and command-first on purpose. JSON would have
// been the obvious envelope and is the wrong one here: the command streams
// onto the screen as it arrives, and a surface whose first frames are `{"com`
// is worse than the one it replaced. Everything before the first sentinel is
// the command, so the screen during a stream is what it has always been, and
// the sections that follow are the part nobody is reading yet.
//
// Parsing is total. A response with no sentinel is one choice, no
// alternatives and no explanation — which is every provider and profile that
// cannot produce the sections, and the reason asking for them costs nothing.
package proposal

import "strings"

// Sentinel opens the alternatives section. It is a line of its own, it is not
// valid shell, and it is what the prompt asks for verbatim.
const Sentinel = "--- alternatives"

// ExplainSentinel opens the sentence saying what the command does. It is the
// same shape as Sentinel for the same reasons, and it comes first because it
// describes the command directly above it.
const ExplainSentinel = "--- explanation"

// The word each sentinel is recognised by, read off the sentinel itself so
// what the prompt asks for and what the parser looks for cannot drift.
var (
	alternativesWord = strings.TrimPrefix(Sentinel, "--- ")
	explainWord      = strings.TrimPrefix(ExplainSentinel, "--- ")
)

// tradeoffPrefix marks the phrase that says why an alternative is on offer. A
// shell comment is the one prefix that cannot be confused with a command.
const tradeoffPrefix = "#"

// MaxAlternatives bounds what a picker is worth reading. Three ways to do
// something is a choice; nine is a search.
const MaxAlternatives = 3

// Choice is one command the generator offered, with the phrase that places it
// against the others. The primary's Tradeoff is usually empty — it is the
// answer, and the others are what they are relative to it — but a generator
// that wants to characterise its own pick may.
type Choice struct {
	Command  string
	Tradeoff string
}

// Proposal is a whole response read: the commands on offer, the one the
// generator led with first, and the sentence it said about that one.
// Explanation is empty for a response that carried none, which is what sends
// the surface back to asking for it on its own.
type Proposal struct {
	Choices     []Choice
	Explanation string
}

// Parse reads a generation into the command the surface shows, the sentence
// it shows under it, and the alternatives it can offer, primary first. It
// always returns at least one choice, so callers never have to ask whether
// there was an answer at all.
func Parse(raw string) Proposal {
	command, explain, alternatives := cut(raw)
	p := Proposal{
		Choices:     []Choice{{Command: strings.TrimSpace(command)}},
		Explanation: oneSentence(explain),
	}
	for _, line := range strings.Split(alternatives, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || sentinelLine(line) {
			// A model that opens the same section twice has said nothing
			// new, and the label is not a command. Left in, it becomes a row
			// in the picker that runs a line of dashes.
			continue
		}
		if phrase, ok := tradeoff(line); ok {
			// A tradeoff attaches to the command above it, and one that has
			// no command above it is the primary's own. The first phrase
			// wins: a second is the model restating itself, and the row has
			// space for one.
			last := &p.Choices[len(p.Choices)-1]
			if last.Tradeoff == "" {
				last.Tradeoff = phrase
			}
			continue
		}
		if len(p.Choices) > MaxAlternatives {
			break
		}
		if duplicate(p.Choices, line) {
			continue
		}
		p.Choices = append(p.Choices, Choice{Command: line})
	}
	return p
}

// CommandPart is the command as much of it as has arrived, for a stream still
// running. It stops at the first sentinel, and at a last line that could
// still become one, so neither the explanation nor the alternatives section
// ever flickers onto the screen as command text on its way to where it goes.
func CommandPart(raw string) string {
	head, _, _ := cut(raw)
	lines := strings.Split(head, "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" && partialSentinel(last) {
		lines = lines[:len(lines)-1]
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// Instructions is what the system prompt asks for. It lives here so the
// format has one definition and the parser cannot drift from the request.
func Instructions() string {
	return `After the command, say what it does. Write a line containing exactly ` + ExplainSentinel + `, then one sentence of at most 160 characters saying what the command operates on and what it produces — no preamble, no markdown, no restating the command. Everything above that line is the command itself and nothing else.

If there is a genuinely different way to do this — one that is faster, more portable, or needs a tool the first one does not — you may then offer alternatives.
Write a line containing exactly ` + Sentinel + `, after the explanation, then each alternative command on its own line, at most 3.
After an alternative, a line starting with ` + tradeoffPrefix + ` gives its tradeoff as one short phrase, not a sentence: ` + tradeoffPrefix + ` faster · needs fd, or ` + tradeoffPrefix + ` tracked files only. A ` + tradeoffPrefix + ` line placed directly under the ` + Sentinel + ` line describes the first command instead.
The alternatives section is optional and must never hold up the command itself. Omit it entirely — no sentinel line — when there is only one sensible way to do the task, when the alternatives would differ only in flags, or when you would have to think hard to name one.`
}

// cut splits a response into its three sections. The prompt asks for them in
// one order — command, explanation, alternatives — but the split does not
// depend on the model having obeyed it: each section opens at the first line
// naming it and runs to whichever sentinel comes next, and the command is
// whatever precedes the first of them. It is the one place the sections are
// separated, so nothing can take the command part without the explanation
// having been cut off it. A sentinel repeated further down opens nothing; it
// is dropped where the section it landed in is read.
func cut(raw string) (command, explain, alternatives string) {
	lines := strings.Split(raw, "\n")
	explainAt, altAt := -1, -1
	for i, line := range lines {
		switch {
		case explainAt < 0 && sentinelIs(line, explainWord):
			explainAt = i
		case altAt < 0 && sentinelIs(line, alternativesWord):
			altAt = i
		}
	}
	return strings.Join(lines[:earliest(len(lines), explainAt, altAt)], "\n"),
		section(lines, explainAt, altAt),
		section(lines, altAt, explainAt)
}

// earliest is the first sentinel that was found, or n when neither was.
func earliest(n int, at ...int) int {
	for _, i := range at {
		if i >= 0 && i < n {
			n = i
		}
	}
	return n
}

// section is what a sentinel on line at opens: everything below it, stopping
// at the other sentinel when that one comes later.
func section(lines []string, at, other int) string {
	if at < 0 {
		return ""
	}
	stop := len(lines)
	if other > at {
		stop = other
	}
	return strings.Join(lines[at+1:stop], "\n")
}

// oneSentence folds the explanation onto the single line the surface has for
// it, between the command and the keys. The prompt asks for one sentence; a
// model that answers with a paragraph would otherwise push the decision off
// the screen, and everything it had to say still reads as one line — minus
// any label it repeated, which is not part of what it said.
func oneSentence(section string) string {
	var words []string
	for _, line := range strings.Split(section, "\n") {
		if sentinelLine(line) {
			continue
		}
		words = append(words, strings.Fields(line)...)
	}
	return strings.Join(words, " ")
}

// sentinelLine reports whether a line opens either section.
func sentinelLine(line string) bool {
	return sentinelIs(line, explainWord) || sentinelIs(line, alternativesWord)
}

// sentinelIs is deliberately forgiving about the rule it is enforcing: a
// model that writes `---alternatives` or `--- Alternatives` meant the line it
// was asked for, and reading it as a command would be the worse mistake.
func sentinelIs(line, word string) bool {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "---") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(strings.Trim(line, "- ")), word)
}

// partialSentinel reports whether a line still being typed could turn into
// either sentinel. Only a line that is nothing but dashes and the start of
// one of the words can, and no shell command begins that way.
func partialSentinel(line string) bool {
	return partialOf(Sentinel, line) || partialOf(ExplainSentinel, line)
}

func partialOf(sentinel, line string) bool {
	if len(line) > len(sentinel) {
		return false
	}
	return strings.EqualFold(sentinel[:len(line)], line)
}

func tradeoff(line string) (string, bool) {
	if !strings.HasPrefix(line, tradeoffPrefix) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, tradeoffPrefix)), true
}

// duplicate is the model offering the command it already gave. It is not an
// alternative to anything, and a picker with the same row twice reads as a
// bug in the picker.
func duplicate(choices []Choice, command string) bool {
	for _, c := range choices {
		if c.Command == command {
			return true
		}
	}
	return false
}
