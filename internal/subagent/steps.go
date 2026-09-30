package subagent

import (
	"encoding/json"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/plan"
)

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

// stepsExecutor answers the child's steps calls on its own checklist and
// passes every other tool on. A call names the whole list, so what it leaves
// is exactly what it said, and the result is that list with the numbers the
// lane counts by. Every role keeps one, and each child's is its own —
// nothing here reads the session's or another child's.
// See docs/capabilities/subagents.md#how-far-along-is-three-numbers-not-one.
func (c *child) stepsExecutor(next agent.ToolExecutor) agent.ToolExecutor {
	return func(name string, args json.RawMessage) (string, error) {
		if name != plan.StepsToolName {
			return next(name, args)
		}
		l, err := plan.ParseStepsCall(args)
		if err != nil {
			return "", err
		}
		c.mu.Lock()
		c.own = l
		c.mu.Unlock()
		return l.Report(), nil
	}
}

// stepsCarried is the child's list as a compaction carries it past the
// summary, and nothing where it keeps none.
func (c *child) stepsCarried() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return plan.CarriedSteps(c.own)
}

// StepsOf is a checklist as the count every surface draws: a lane, the
// manager's row, and the session's own STEPS block on the rail.
func StepsOf(l plan.Checklist) StepCount {
	done, total, current := l.Tally()
	if total == 0 {
		return StepCount{}
	}
	return StepCount{Done: done, Total: total, Current: current, Own: true}
}

// stepCount is the child's steps as its status states them, with c.mu held:
// its own plan where it named one, the spawn's declared count otherwise.
func (c *child) stepCount() StepCount {
	if sc := StepsOf(c.own); sc.Own {
		return sc
	}
	return StepCount{Done: min(c.step, c.steps), Total: c.steps}
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
