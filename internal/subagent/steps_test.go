package subagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/plan"
)

func planningWriter() *child {
	return &child{name: "writer-1", profile: Profile{Writes: true}, steps: 4}
}

// setSteps makes a steps call on c, as the child's executor answers one.
func setSteps(t *testing.T, c *child, args string) error {
	t.Helper()
	_, err := c.stepsExecutor(nil)(plan.StepsToolName, json.RawMessage(args))
	return err
}

// A writer's own list is what its status counts, and the count moves only as
// its own steps calls mark steps.
func TestStatusSteps_AWritersOwnPlanIsCountedAsItMarksIt(t *testing.T) {
	c := planningWriter()
	if err := setSteps(t, c, `{"steps":[{"title":"Read the loop"},{"title":"Add the flag"},{"title":"Test it"}]}`); err != nil {
		t.Fatal(err)
	}
	if got := c.status().Steps; got != (StepCount{Done: 0, Total: 3, Current: "Read the loop", Own: true}) {
		t.Fatalf("fresh plan = %+v, want 0 of 3 on the first step", got)
	}
	if err := setSteps(t, c, `{"steps":[{"title":"Read the loop","done":true},{"title":"Add the flag"},{"title":"Test it"}]}`); err != nil {
		t.Fatal(err)
	}
	if got := c.status().Steps; got != (StepCount{Done: 1, Total: 3, Current: "Add the flag", Own: true}) {
		t.Fatalf("after one mark = %+v, want 1 of 3 on the second step", got)
	}
}

// A call the tool refuses leaves the status at no plan — the declared count,
// or nothing — rather than at zero of zero.
func TestStatusSteps_ARefusedCallIsNoPlan(t *testing.T) {
	long := `{"steps":[` + strings.TrimSuffix(strings.Repeat(`{"title":"x"},`, MaxDeclaredSteps+1), ",") + `]}`
	for name, args := range map[string]string{
		"too long":      long,
		"an empty step": `{"steps":[{"title":"Read"},{"title":" "}]}`,
		"not a list":    `{"steps":"read then write"}`,
	} {
		t.Run(name, func(t *testing.T) {
			c := planningWriter()
			if err := setSteps(t, c, args); err == nil {
				t.Fatal("the call should have been refused")
			}
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

// A child's prose is never read as a list, however it is numbered — a plan in
// words before a call and a report at the end are both text — and every
// role keeps a list of its own through the call, a reader included.
func TestStatusSteps_OnlyACallIsAList(t *testing.T) {
	c := planningWriter()
	c.streaming = "Plan:\n1. Read the loop\n2. Add the flag\nprogress: 1"
	c.beginToolEntry("t1", "read_file", `{}`)
	c.streaming = "Changed:\n1. loop.go\n2. loop_test.go"
	c.flushStreaming()
	if got := c.status().Steps; got.Own {
		t.Fatalf("prose became a plan: %+v", got)
	}

	r := &child{name: "researcher-1"}
	if err := setSteps(t, r, `{"steps":[{"title":"Read"},{"title":"Report"}]}`); err != nil {
		t.Fatal(err)
	}
	if got := r.status().Steps; got != (StepCount{Total: 2, Current: "Read", Own: true}) {
		t.Fatalf("a reader's list = %+v, want its own 0 of 2", got)
	}
}

// Two children's lists are their own: one child's call never moves another's
// count.
func TestStatusSteps_EachChildsListIsItsOwn(t *testing.T) {
	a, b := planningWriter(), &child{name: "researcher-1"}
	if err := setSteps(t, a, `{"steps":[{"title":"Read"},{"title":"Write"}]}`); err != nil {
		t.Fatal(err)
	}
	if err := setSteps(t, b, `{"steps":[{"title":"Look","done":true},{"title":"Say","done":true}]}`); err != nil {
		t.Fatal(err)
	}
	if got := a.status().Steps; got.Done != 0 || got.Total != 2 {
		t.Fatalf("another child's marks moved this one: %+v", got)
	}
}

// A follow-up starts with no list: the ask is new, and the list the last one
// named was not written for it. (A retry's reset is held beside restart.)
func TestStatusSteps_AFollowUpStartsClean(t *testing.T) {
	c := planningWriter()
	if err := setSteps(t, c, `{"steps":[{"title":"Read","done":true},{"title":"Write"}]}`); err != nil {
		t.Fatal(err)
	}
	c.claimFollowUp("and the docs")
	if got := c.status().Steps; got.Own {
		t.Fatalf("a follow-up kept the last list: %+v", got)
	}
	if err := setSteps(t, c, `{"steps":[{"title":"Docs","done":true}]}`); err != nil {
		t.Fatal(err)
	}
	if got := c.status().Steps; !got.Own || got.Total != 1 {
		t.Fatalf("the follow-up's own list = %+v, want 1 step", got)
	}
}

// A child whose every step is marked is still working: the marks are its own
// account of its list, and only the loop's own ending says the task is done.
func TestStatusSteps_AFullyMarkedListIsNotAFinishedTask(t *testing.T) {
	c := planningWriter()
	c.state = StateRunning
	if err := setSteps(t, c, `{"steps":[{"title":"Read","done":true},{"title":"Write","done":true}]}`); err != nil {
		t.Fatal(err)
	}
	st := c.status()
	if st.Steps != (StepCount{Done: 2, Total: 2, Own: true}) || st.State != StateRunning {
		t.Fatalf("status = %v %+v, want still running at 2 of 2", st.State, st.Steps)
	}
}

// A writer on the last step of its own plan is not reseeded until it has
// reported: what landed stays queued for its landing to meet.
// See docs/capabilities/subagents.md#how-far-along-is-three-numbers-not-one.
func TestReseed_AWriterOnItsLastStepIsLeftAlone(t *testing.T) {
	c := planningWriter()
	c.worktree = "/nowhere"
	if err := setSteps(t, c, `{"steps":[{"title":"Read"},{"title":"Write"},{"title":"Test"}]}`); err != nil {
		t.Fatal(err)
	}
	if c.nearDone() {
		t.Fatal("a writer on its first step is not near done")
	}
	if err := setSteps(t, c, `{"steps":[{"title":"Read","done":true},{"title":"Write","done":true},{"title":"Test"}]}`); err != nil {
		t.Fatal(err)
	}
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
	if err := setSteps(t, c, `{"steps":[{"title":"Read","done":true},{"title":"Write"}]}`); err != nil {
		t.Fatal(err)
	}
	if d, n, cur := c.ownProgress(); d != 1 || n != 2 || cur != "Write" {
		t.Fatalf("ownProgress = %d/%d %q, want 1/2 on Write", d, n, cur)
	}
}
