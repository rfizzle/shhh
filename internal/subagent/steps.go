package subagent

import "github.com/rfizzle/shhh/internal/plan"

// StepCount is how far a child is through its steps. Total zero is a child
// with no steps to count — no plan of its own and none declared — and never
// a plan of zero steps: a malformed plan leaves the count where it was.
type StepCount struct {
	Done, Total int
	// Current is the title of the first step of the child's own plan it has
	// not marked done, and empty where every step is marked or the steps are
	// a declared count with no titles.
	Current string
	// Own marks steps the child named itself rather than a count the spawn
	// declared for it. Only its own plan has titles, marks and a last step,
	// so only its own plan is what the reading and the check-in hold it to.
	Own bool
}

// noteSteps reads a writer's own plan and its progress lines out of text the
// child has just written, with c.mu held.
//
// The plan is taken from text that goes on to a call — the message a writer
// opens its work with — and never from a message that ends a turn: a report
// listing what it changed in numbered lines is not a plan, and read as one it
// would put every lane at zero of the report's length. It is taken once, so a
// numbered list later in the work does not replace the steps the child is
// being held to. The grammar is the plan card's (internal/plan), so a step is
// the same thing on both surfaces, and a plan longer than a spawn may declare
// is a list rather than a plan and is not taken.
//
// A step is marked done by a `progress: <n>` line naming one of the plan's
// own numbers. A line is the whole mechanism, never a tool: the count is the
// child's own account of its work, written in the same messages as the work.
// See docs/capabilities/subagents.md#how-far-along-is-three-numbers-not-one.
func (c *child) noteSteps(text string, beforeCall bool) {
	if !c.profile.Writes {
		return
	}
	if c.ownSteps == nil && beforeCall {
		if p := plan.Parse(text); len(p.Steps) > 0 && len(p.Steps) <= MaxDeclaredSteps {
			c.ownSteps = p.Steps
		}
	}
	if c.ownSteps == nil {
		return
	}
	for _, n := range plan.Progress(text) {
		for _, s := range c.ownSteps {
			if s.Number != n {
				continue
			}
			if c.stepsDone == nil {
				c.stepsDone = map[int]bool{}
			}
			c.stepsDone[n] = true
		}
	}
}

// stepCount is the child's steps as its status states them, with c.mu held:
// its own plan where it named one, the spawn's declared count otherwise.
func (c *child) stepCount() StepCount {
	if len(c.ownSteps) == 0 {
		return StepCount{Done: min(c.step, c.steps), Total: c.steps}
	}
	sc := StepCount{Total: len(c.ownSteps), Own: true}
	for _, s := range c.ownSteps {
		switch {
		case c.stepsDone[s.Number]:
			sc.Done++
		case sc.Current == "":
			sc.Current = s.Title
		}
	}
	return sc
}

// ownProgress is the child's own plan as the reading and the check-in are
// handed it, and zero for a child that named none — a declared count is the
// spawn's guess at the work, not the child's word it can be held to.
func (c *child) ownProgress() (done, total int, current string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sc := c.stepCount()
	if !sc.Own {
		return 0, 0, ""
	}
	return sc.Done, sc.Total, sc.Current
}

// nearDone reports, with c.mu held, that a writer is on the last step of its
// own plan or past it, in the turn it was spawned for. A follow-up is a new
// ask the plan was not written for, so it does not count.
func (c *child) nearDone() bool {
	sc := c.stepCount()
	return sc.Own && c.followUp == "" && sc.Done >= sc.Total-1
}
