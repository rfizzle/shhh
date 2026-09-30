package plan

// An agent's working checklist: the steps it said it would take and which of
// them it has said are finished, as its last steps call left them
// (stepstool.go). The session and every child keep one each, in this one
// shape, so a step is the same thing on the rail, on a lane and on the plan
// card.

import (
	"encoding/json"
)

// MaxWorkingSteps is the longest list read as a checklist. A lane has room
// for a count, not for a backlog, and a list longer than this is a list — an
// inventory, a report — rather than the steps of one task.
const MaxWorkingSteps = 20

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
	// Done is the step numbers the last steps call marked finished.
	Done map[int]bool
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

func (l Checklist) has(n int) bool {
	for _, s := range l.Steps {
		if s.Number == n {
			return true
		}
	}
	return false
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
