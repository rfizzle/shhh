package subagent

import (
	"fmt"
	"strings"
	"testing"
)

func planningWriter() *child {
	return &child{name: "writer-1", profile: Profile{Writes: true}, steps: 4}
}

// A writer's plan, named before its first call, is what its status counts,
// and the count moves only as its own progress lines mark steps.
func TestStatusSteps_AWritersOwnPlanIsCountedAsItMarksIt(t *testing.T) {
	c := planningWriter()
	c.streaming = "Plan:\n1. Read the loop\n2. Add the flag\n3. Test it"
	c.beginToolEntry("t1", "read_file", `{"path":"loop.go"}`)
	if got := c.status().Steps; got != (StepCount{Done: 0, Total: 3, Current: "Read the loop", Own: true}) {
		t.Fatalf("fresh plan = %+v, want 0 of 3 on the first step", got)
	}

	c.streaming = "Read it.\nprogress: 1\nprogress: 9"
	c.beginToolEntry("t2", "edit_file", `{"path":"loop.go"}`)
	if got := c.status().Steps; got != (StepCount{Done: 1, Total: 3, Current: "Add the flag", Own: true}) {
		t.Fatalf("after one mark = %+v, want 1 of 3 on the second step (a number off the plan marks nothing)", got)
	}

	// A numbered list later in the work does not replace the plan.
	c.streaming = "Now:\n1. something else\n2. and more\nprogress: 2"
	c.beginToolEntry("t3", "read_file", `{"path":"x.go"}`)
	if got := c.status().Steps; got.Total != 3 || got.Done != 2 || got.Current != "Test it" {
		t.Fatalf("after a second list = %+v, want the first plan at 2 of 3", got)
	}

	// The report's own marks count too, and a plan fully marked has no
	// current step.
	c.streaming = "Done.\nprogress: 3"
	c.flushStreaming()
	if got := c.status().Steps; got != (StepCount{Done: 3, Total: 3, Own: true}) {
		t.Fatalf("after the report = %+v, want 3 of 3", got)
	}
}

// A malformed plan leaves the status at no plan — the declared count, or
// nothing — rather than at zero of zero.
func TestStatusSteps_AMalformedPlanIsNoPlan(t *testing.T) {
	var long strings.Builder
	for i := 1; i <= MaxDeclaredSteps+1; i++ {
		fmt.Fprintf(&long, "%d. step %d\n", i, i)
	}
	for name, text := range map[string]string{
		"not from one":   "2. Read\n3. Write",
		"no list":        "I will read the loop and then change it.",
		"too long":       long.String(),
		"only a mark":    "progress: 1",
		"an empty entry": "1.\n2.",
	} {
		t.Run(name, func(t *testing.T) {
			c := planningWriter()
			c.streaming = text
			c.beginToolEntry("t1", "read_file", `{}`)
			got := c.status().Steps
			if got.Own || got.Current != "" || got.Total != 4 {
				t.Fatalf("steps = %+v, want the declared count and no plan", got)
			}
			c.steps = 0
			if got := c.status().Steps; got != (StepCount{}) {
				t.Fatalf("with nothing declared, steps = %+v, want none at all", got)
			}
		})
	}
}

// A numbered list in a message that ends the turn is a report, not a plan;
// and a child that writes nothing is not held to a plan it was never asked for.
func TestStatusSteps_OnlyAWriterPlansAndOnlyBeforeACall(t *testing.T) {
	c := planningWriter()
	c.streaming = "Changed:\n1. loop.go\n2. loop_test.go"
	c.flushStreaming()
	if got := c.status().Steps; got.Own {
		t.Fatalf("a report's list became a plan: %+v", got)
	}

	r := &child{name: "researcher-1"}
	r.streaming = "1. Read\n2. Report"
	r.beginToolEntry("t1", "read_file", `{}`)
	if got := r.status().Steps; got.Own {
		t.Fatalf("a reader's list became a plan: %+v", got)
	}
}

// A writer on the last step of its own plan is not reseeded until it has
// reported: what landed stays queued for its landing to meet.
// See docs/capabilities/subagents.md#how-far-along-is-three-numbers-not-one.
func TestReseed_AWriterOnItsLastStepIsLeftAlone(t *testing.T) {
	c := planningWriter()
	c.worktree = "/nowhere"
	c.streaming = "1. Read\n2. Write\n3. Test"
	c.beginToolEntry("t1", "read_file", `{}`)
	if c.nearDone() {
		t.Fatal("a writer on its first step is not near done")
	}
	c.streaming = "progress: 1\nprogress: 2"
	c.beginToolEntry("t2", "read_file", `{}`)
	c.landings = []landing{{from: "writer-2", patch: "diff"}}

	(&Supervisor{}).reseed(c)
	if len(c.landings) != 1 || c.reseeding != "" || c.reseeds != 0 {
		t.Fatalf("a writer on its last step was reseeded: landings=%d reseeds=%d", len(c.landings), c.reseeds)
	}

	// A follow-up is a new ask the plan was not written for.
	c.followUp = "and the docs"
	if c.nearDone() {
		t.Fatal("a follow-up should not count as the plan's last step")
	}
}

// ownProgress is what the reading and the check-in are handed: the child's
// own plan, and nothing for a count the spawn declared.
func TestOwnProgress_IsTheChildsOwnPlanOnly(t *testing.T) {
	c := planningWriter()
	if d, n, cur := c.ownProgress(); d != 0 || n != 0 || cur != "" {
		t.Fatalf("a declared count reached the reading: %d/%d %q", d, n, cur)
	}
	c.streaming = "1. Read\n2. Write\nprogress: 1"
	c.beginToolEntry("t1", "read_file", `{}`)
	if d, n, cur := c.ownProgress(); d != 1 || n != 2 || cur != "Write" {
		t.Fatalf("ownProgress = %d/%d %q, want 1/2 on Write", d, n, cur)
	}
}
