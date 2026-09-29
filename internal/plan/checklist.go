package plan

// An agent's working checklist: the steps it said it would take, read out of
// its own messages, and which of them it has said are finished. The session
// and every child keep one each, through this one reader, so a step is the
// same thing on the rail, on a lane and on the plan card.

import (
	"encoding/json"
	"maps"
	"regexp"
	"strings"
)

// MaxWorkingSteps is the longest list read as a checklist. A lane has room
// for a count, not for a backlog, and a list longer than this is a list — an
// inventory, a report — rather than the steps of one task.
const MaxWorkingSteps = 20

// revisionPattern is the line a revised list stands under: `steps:` on a
// line of its own, optionally bulleted.
var revisionPattern = regexp.MustCompile(`(?i)^\s*(?:[-*+]\s+)?steps\s*:\s*$`)

// Checklist is an agent's own working steps. It is not an approved plan and
// never becomes one: nobody approved it, nothing is checked against it but
// the agent's own account of its work, and marking every step finished says
// the agent believes so, not that the task is done.
//
// The zero value holds no list, which every surface draws as nothing at all
// rather than as zero of zero.
type Checklist struct {
	// Steps are the list in order, each with the number it is marked by.
	Steps []Step
	// Done is the step numbers a progress line has marked finished.
	Done map[int]bool
	// fresh says a new turn has begun since the list was declared, so the
	// turn's first message that goes on to a call may declare another.
	fresh bool
}

// Note reads one message into the checklist and reports whether it moved.
//
// A list is taken only from text that goes on to a call (beforeCall): a
// message that ends a turn is a report, and a report listing what changed in
// numbered lines is not a plan. It is taken once — a later numbered list is
// text — unless it stands under a `steps:` line, which revises it: the steps
// already marked stay marked, the unfinished ones are replaced by the new
// list, and the new ones are numbered after the last finished step whatever
// numbers the message gave them. A list that does not parse, or one longer
// than MaxWorkingSteps, changes nothing.
//
// A `progress: <n>` line marks step n finished, in any message, where n is
// one of the list's own numbers. Marks written before a revision's marker are
// read before the revision, so a message that finishes a step and then
// changes course keeps the step it finished.
// See docs/capabilities/coding-agent.md#the-session-keeps-its-own-working-steps.
func (l *Checklist) Note(text string, beforeCall bool) bool {
	before, after, revised := splitRevision(text)
	if !beforeCall {
		// A turn-ending message revises nothing, but what it marks counts.
		return l.mark(text)
	}
	open := len(l.Steps) == 0 || l.fresh
	l.fresh = false
	if revised {
		moved := l.mark(before)
		moved = l.revise(after) || moved
		return l.mark(after) || moved
	}
	moved := false
	if open {
		if p := parse(text, false); len(p.Steps) > 0 && len(p.Steps) <= MaxWorkingSteps {
			l.Steps, l.Done = titles(p.Steps), nil
			moved = true
		}
	}
	return l.mark(text) || moved
}

// Reopen says a new turn has begun: the first message of it that goes on to
// a call may declare a list that replaces this one, since a new instruction
// is a task the old list was not written for. Until it does, the old list
// stays where the reader can see it.
func (l *Checklist) Reopen() {
	if len(l.Steps) > 0 {
		l.fresh = true
	}
}

// Tally is the list as a count: how many steps are marked, how many there
// are, and the title of the first unmarked one — empty when every step is
// marked, because a finished list has no step it is on.
func (l Checklist) Tally() (done, total int, current string) {
	for _, s := range l.Steps {
		switch {
		case l.Done[s.Number]:
			done++
		case current == "":
			current = s.Title
		}
	}
	return done, len(l.Steps), current
}

// mark applies text's progress lines and reports whether any was new. The
// map is copied before it is written: a checklist is carried by value in a
// Bubble Tea model, and a copy writing into the map it shares with the one
// it was copied from would move both.
func (l *Checklist) mark(text string) bool {
	moved := false
	for _, n := range Progress(text) {
		if l.Done[n] || !l.has(n) {
			continue
		}
		next := make(map[int]bool, len(l.Done)+1)
		maps.Copy(next, l.Done)
		next[n] = true
		l.Done, moved = next, true
	}
	return moved
}

// revise replaces the unfinished steps with the list in text, numbering the
// new ones after the last finished step.
func (l *Checklist) revise(text string) bool {
	p := parse(text, true)
	if len(p.Steps) == 0 {
		return false
	}
	var kept []Step
	last := 0
	for _, s := range l.Steps {
		if l.Done[s.Number] {
			kept = append(kept, s)
			last = max(last, s.Number)
		}
	}
	if len(kept)+len(p.Steps) > MaxWorkingSteps {
		return false
	}
	for i, s := range titles(p.Steps) {
		s.Number = last + 1 + i
		kept = append(kept, s)
	}
	l.Steps = kept
	return true
}

func (l Checklist) has(n int) bool {
	for _, s := range l.Steps {
		if s.Number == n {
			return true
		}
	}
	return false
}

// titles keeps what a checklist is: a number, a title and the paths the step
// said it would touch, which the steps screen draws under it. The plan card's
// action and note belong to a plan somebody approves.
func titles(steps []Step) []Step {
	out := make([]Step, len(steps))
	for i, s := range steps {
		out[i] = Step{Number: s.Number, Title: s.Title, Paths: s.Paths}
	}
	return out
}

// splitRevision cuts text at its last `steps:` line: what came before it and
// what stands under it.
func splitRevision(text string) (before, after string, ok bool) {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if revisionPattern.MatchString(stripMarks(lines[i])) {
			return strings.Join(lines[:i], "\n"), strings.Join(lines[i+1:], "\n"), true
		}
	}
	return text, "", false
}

// checklistJSON is the checklist as a slot keeps it.
type checklistJSON struct {
	Steps []checklistStep `json:"steps"`
	Done  []int           `json:"done,omitempty"`
}

type checklistStep struct {
	N     int      `json:"n"`
	Title string   `json:"title"`
	Paths []string `json:"paths,omitempty"`
}

// Encode is the checklist as a slot stores it, and "" for no list.
func (l Checklist) Encode() string {
	if len(l.Steps) == 0 {
		return ""
	}
	var c checklistJSON
	for _, s := range l.Steps {
		c.Steps = append(c.Steps, checklistStep{N: s.Number, Title: s.Title, Paths: s.Paths})
		if l.Done[s.Number] {
			c.Done = append(c.Done, s.Number)
		}
	}
	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return string(b)
}

// DecodeChecklist reads a stored checklist back. Anything it cannot read is
// no list, which is what a slot written before lists were kept holds too.
func DecodeChecklist(s string) Checklist {
	var c checklistJSON
	if s == "" || json.Unmarshal([]byte(s), &c) != nil || len(c.Steps) == 0 || len(c.Steps) > MaxWorkingSteps {
		return Checklist{}
	}
	var l Checklist
	for _, s := range c.Steps {
		l.Steps = append(l.Steps, Step{Number: s.N, Title: s.Title, Paths: s.Paths})
	}
	for _, n := range c.Done {
		if l.has(n) {
			if l.Done == nil {
				l.Done = map[int]bool{}
			}
			l.Done[n] = true
		}
	}
	return l
}
