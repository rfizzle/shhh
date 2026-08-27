// Package proposal reads what the one-shot generator answered with (S-114,
// DESIGN-TUI.md §18b).
//
// A generator that can only say one thing has already chosen for you. Asked
// for "every process listening above 8000" a model will weigh lsof against
// netstat against ss, pick one, and throw the reasoning away — and the answer
// it kept is the portable one when you wanted the fast one about as often as
// not. The alternatives were free; only the surface for them was missing.
//
// The response is therefore structured rather than a bare command string: the
// command first, exactly as it always arrived, then an optional section
// listing the ways it was not taken, each with the one phrase that says why
// you might want it instead.
//
// The shape is line-oriented and command-first on purpose. JSON would have
// been the obvious envelope and is the wrong one here: the command streams
// onto the screen as it arrives, and a surface whose first frames are `{"com`
// is worse than the one it replaced. Everything before the sentinel is the
// command, so the screen during a stream is what it has always been, and the
// section that follows is the part nobody is reading yet.
//
// Parsing is total. A response with no sentinel is one choice and no
// alternatives — which is every provider and profile that cannot produce the
// section, and the reason asking for it costs nothing.
package proposal

import "strings"

// Sentinel opens the alternatives section. It is a line of its own, it is not
// valid shell, and it is what the prompt asks for verbatim.
const Sentinel = "--- alternatives"

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

// Parse splits a generation into the command the surface shows and the
// alternatives it can offer, primary first. It always returns at least one
// choice, so callers never have to ask whether there was an answer at all.
func Parse(raw string) []Choice {
	head, tail, found := cut(raw)
	choices := []Choice{{Command: strings.TrimSpace(head)}}
	if !found {
		return choices
	}
	for _, line := range strings.Split(tail, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if phrase, ok := tradeoff(line); ok {
			// A tradeoff attaches to the command above it, and one that has
			// no command above it is the primary's own. The first phrase
			// wins: a second is the model restating itself, and the row has
			// space for one.
			last := &choices[len(choices)-1]
			if last.Tradeoff == "" {
				last.Tradeoff = phrase
			}
			continue
		}
		if len(choices) > MaxAlternatives {
			break
		}
		if duplicate(choices, line) {
			continue
		}
		choices = append(choices, Choice{Command: line})
	}
	return choices
}

// CommandPart is the command as much of it as has arrived, for a stream still
// running. It stops at the sentinel, and at a last line that could still
// become one, so the alternatives section never flickers onto the screen on
// its way to the picker.
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
	return `If there is a genuinely different way to do this — one that is faster, more portable, or needs a tool the first one does not — you may offer alternatives after the command.
Write a line containing exactly ` + Sentinel + `, then each alternative command on its own line, at most 3.
After an alternative, a line starting with ` + tradeoffPrefix + ` gives its tradeoff as one short phrase, not a sentence: ` + tradeoffPrefix + ` faster · needs fd, or ` + tradeoffPrefix + ` tracked files only. A ` + tradeoffPrefix + ` line placed directly under the ` + Sentinel + ` line describes the first command instead.
The section is optional and must never hold up the command itself. Omit it entirely — no sentinel line — when there is only one sensible way to do the task, when the alternatives would differ only in flags, or when you would have to think hard to name one.`
}

// cut splits a response at the sentinel line.
func cut(raw string) (head, tail string, found bool) {
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		if isSentinel(line) {
			return strings.Join(lines[:i], "\n"), strings.Join(lines[i+1:], "\n"), true
		}
	}
	return raw, "", false
}

// isSentinel is deliberately forgiving about the rule it is enforcing: a
// model that writes `---alternatives` or `--- Alternatives` meant the line it
// was asked for, and reading it as a command would be the worse mistake.
func isSentinel(line string) bool {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "---") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(strings.Trim(line, "- ")), "alternatives")
}

// partialSentinel reports whether a line still being typed could turn into
// the sentinel. Only a line that is nothing but dashes and the start of the
// word can, and no shell command begins that way.
func partialSentinel(line string) bool {
	if len(line) > len(Sentinel) {
		return false
	}
	return strings.EqualFold(Sentinel[:len(line)], line)
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
