package chat

// The approved plan as a live checklist (S-104): the declared list numbering
// the transcript's steps, the rail's PLAN block, /plan mid-turn, and what all
// three say when the agent departs from the plan.

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// planFixture is the four-step plan the artboard is drawn from.
const planFixture = `## Plan: make the round limit recoverable

1. Locate the round accounting
   files: internal/agent/loop.go
   action: read
2. Add a RoundsExhausted sentinel
   files: internal/agent/errors.go
   action: create
3. Return it from runRound
   files: internal/agent/loop.go
   action: edit
4. Offer more rounds in the chat model
   files: internal/ui/chat/model.go
   action: edit
`

// runningPlanModel is a session that has approved planFixture and is
// executing it, at the given terminal width.
func runningPlanModel(t *testing.T, width int) Model {
	t.Helper()
	m := frameModel(t, width, 40)
	m.transcript = []entry{{kind: entryUser, text: planApprovedMessage}}
	m.planRun = newPlanRun(plan.Parse(planFixture), 0)
	if m.planRun == nil {
		t.Fatal("the fixture plan should parse into steps")
	}
	m.invalidateRenderCache()
	return m
}

// announce appends the assistant line a step is titled by, stamped as
// stampStep does in the live path, followed by one finished tool call.
func announce(t *testing.T, m *Model, title string, d time.Duration, failed bool) {
	t.Helper()
	m.appendEntry(m.stampStep(entry{kind: entryAssistant, text: title}))
	e := entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"a.go"}`,
		toolResult: "ok", duration: d}
	if failed {
		e = entry{kind: entryCommand, text: "go test ./...", toolResult: "FAIL",
			exitCode: 1, duration: d}
	}
	m.appendEntry(e)
	m.invalidateRenderCache()
}

func TestPlanRun_ClaimsAnnouncementsAgainstDeclaredSteps(t *testing.T) {
	run := newPlanRun(plan.Parse(planFixture), 0)
	cases := []struct {
		announced string
		want      int
	}{
		// A restatement, however padded, is the step it restates.
		{"Now let me locate the round accounting", 1},
		{"Adding a RoundsExhausted sentinel", 2},
		// Nothing in the plan mentions the changeset store.
		{"Rebuild the changeset store from scratch", offPlanStep},
		{"Return it from runRound", 3},
	}
	for _, c := range cases {
		if got := run.claim(c.announced); got != c.want {
			t.Fatalf("claim(%q) = %d, want %d", c.announced, got, c.want)
		}
	}
	// A step is claimed once: announcing it again is the agent doing
	// something else, not a second copy of the step.
	if got := run.claim("Return it from runRound"); got != offPlanStep {
		t.Fatalf("re-announcing a claimed step = %d, want offPlanStep", got)
	}
	if run.complete() {
		t.Fatal("a plan with step 4 unclaimed is not complete")
	}
	if got := run.claim("Offer more rounds in the chat model"); got != 4 {
		t.Fatalf("claim(step 4) = %d, want 4", got)
	}
	if !run.complete() {
		t.Fatal("every step claimed should be complete")
	}
}

func TestPlanRun_ShortAnnouncementDoesNotMatchOnOneWord(t *testing.T) {
	run := newPlanRun(plan.Parse(planFixture), 0)
	// "round" alone is a word two steps share; matching on it would put the
	// wrong number in the outline, so the floor refuses.
	if got := run.claim("Round"); got != offPlanStep {
		t.Fatalf("a one-word line = %d, want offPlanStep", got)
	}
}

func TestPlanRun_UnstructuredPlanKeepsNoChecklist(t *testing.T) {
	if run := newPlanRun(plan.Parse("I would rewrite the loop and then test it."), 0); run != nil {
		t.Fatal("a plan that never adopted the step shape has no checklist to keep")
	}
}

func TestPlanOutline_DeclaredStepsNumberTheTranscript(t *testing.T) {
	m := runningPlanModel(t, 130)
	announce(t, &m, "Now let me locate the round accounting", 1200*time.Millisecond, false)
	announce(t, &m, "Return it from runRound", 900*time.Millisecond, false)

	var steps []*stepGroup
	for _, blk := range m.blocksOf(m.transcript) {
		if blk.step != nil {
			steps = append(steps, blk.step)
		}
	}
	if len(steps) != 4 {
		t.Fatalf("the outline should mirror all four declared steps, got %d", len(steps))
	}
	// Ordinals are the plan's, in the order the transcript ran them: step 3
	// before step 2 is shown as such rather than silently reordered.
	want := []int{1, 3, 2, 4}
	for i, g := range steps {
		if g.ordinal != want[i] {
			t.Fatalf("outline step %d has ordinal %d, want %d", i, g.ordinal, want[i])
		}
	}
	// A declared step's header carries the plan's own title, not the sentence
	// the model announced it with.
	if steps[0].title != "Locate the round accounting" {
		t.Fatalf("a declared step should be titled by the plan, got %q", steps[0].title)
	}
	// The two steps nobody reached are queued headers with no rows.
	for _, g := range steps[2:] {
		if !g.queued() {
			t.Fatalf("step %d should be queued, it has rows %d..%d", g.ordinal, g.start, g.end)
		}
	}
	if !steps[1].queued() && steps[1].ordinal != 3 {
		t.Fatal("step 3 ran and should not be queued")
	}
}

func TestPlanOutline_WorkOffThePlanIsMarkedNotRenumbered(t *testing.T) {
	m := runningPlanModel(t, 130)
	announce(t, &m, "Locate the round accounting", time.Second, false)
	announce(t, &m, "Rebuild the changeset store from scratch", time.Second, false)

	var off *stepGroup
	for _, blk := range m.blocksOf(m.transcript) {
		if blk.step != nil && blk.step.offPlan {
			off = blk.step
		}
	}
	if off == nil {
		t.Fatal("a group the plan never declared should be marked off it")
	}
	if off.ordinal != 0 {
		t.Fatalf("an off-plan step should carry no ordinal, got %d", off.ordinal)
	}
	if off.title != "Rebuild the changeset store from scratch" {
		t.Fatalf("an off-plan step keeps the prose that announced it, got %q", off.title)
	}
	header := stepHeader{Ordinal: off.ordinal, Title: off.title, State: stepDone, Tools: 1, OffPlan: true}
	if !strings.Contains(ansi.Strip(header.View(100)), "+ ") {
		t.Fatalf("an off-plan header should mark the ordinal column, got %q", ansi.Strip(header.View(100)))
	}
}

func TestPlanOutline_NoPlanLeavesInferredNumberingAlone(t *testing.T) {
	m := frameModel(t, 130, 40)
	m.transcript = goldenTranscript()
	var ordinals []int
	for _, blk := range m.blocksOf(m.transcript) {
		if blk.step != nil {
			ordinals = append(ordinals, blk.step.ordinal)
		}
	}
	if len(ordinals) != 2 || ordinals[0] != 1 || ordinals[1] != 2 {
		t.Fatalf("without a plan the outline still counts its own steps, got %v", ordinals)
	}
}

func TestPlanChecklist_StatesFollowTheirGroups(t *testing.T) {
	m := runningPlanModel(t, 130)
	announce(t, &m, "Locate the round accounting", 1200*time.Millisecond, false)
	announce(t, &m, "Add a RoundsExhausted sentinel", 4*time.Second, true)

	steps := m.planChecklist()
	if len(steps) != 4 {
		t.Fatalf("the checklist should hold every declared step, got %d", len(steps))
	}
	want := []components.PlanStepState{
		components.PlanStepDone, components.PlanStepFailed,
		components.PlanStepQueued, components.PlanStepQueued,
	}
	for i, s := range steps {
		if s.State != want[i] {
			t.Fatalf("step %d state = %d, want %d", s.Number, s.State, want[i])
		}
	}
	if steps[0].Elapsed == "" {
		t.Fatal("a finished step should report what it cost")
	}
	if steps[2].Elapsed != "" {
		t.Fatal("a step that never ran has no duration to report")
	}
	if got := planStepsDone(steps); got != 2 {
		t.Fatalf("done = %d, want 2 (a failed step finished)", got)
	}
}

func TestInspectorRail_PlanBlockAndTrueDenominator(t *testing.T) {
	m := runningPlanModel(t, 130)
	announce(t, &m, "Locate the round accounting", 1200*time.Millisecond, false)

	data := m.inspectorData()
	if data.Plan == nil {
		t.Fatal("an approved plan should put a PLAN block in the rail")
	}
	if data.Turn == nil || data.Turn.Steps != 4 {
		t.Fatalf("the declared total is the meter's denominator, got %+v", data.Turn)
	}
	view := ansi.Strip(data.Plan.Steps[0].Title)
	if view != "Locate the round accounting" {
		t.Fatalf("the block lists the plan's own titles, got %q", view)
	}
	rail := ansi.Strip(data.View(components.InspectorWidth, 0))
	for _, want := range []string{"PLAN", "1 of 4 done", "✓", "·", planHintRail} {
		if !strings.Contains(rail, want) {
			t.Fatalf("the rail should contain %q, got:\n%s", want, rail)
		}
	}
}

func TestInspectorRail_NoPlanNoBlock(t *testing.T) {
	m := frameModel(t, 130, 40)
	m.transcript = goldenTranscript()
	if data := m.inspectorData(); data.Plan != nil {
		t.Fatal("a session with no approved plan has no PLAN block")
	}
}

func TestPlanStatus_ChecklistAndDrift(t *testing.T) {
	m := runningPlanModel(t, 100)
	announce(t, &m, "Return it from runRound", time.Second, false)
	announce(t, &m, "Rebuild the changeset store from scratch", time.Second, false)
	announce(t, &m, "Locate the round accounting", time.Second, false)

	out := m.planStatus()
	for _, want := range []string{
		"Plan · make the round limit recoverable",
		"2 of 4 done",
		"✓ 1  Locate the round accounting",
		"· 4  Offer more rounds in the chat model · " + components.OutcomeQueued,
		"1 step off the plan",
		"step 3 ran before step 1",
		"1 step skipped so far (2)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("/plan should report %q, got:\n%s", want, out)
		}
	}
}

func TestPlanStatus_NoDriftSaysSo(t *testing.T) {
	m := runningPlanModel(t, 100)
	announce(t, &m, "Locate the round accounting", time.Second, false)
	announce(t, &m, "Add a RoundsExhausted sentinel", time.Second, false)

	out := m.planStatus()
	if !strings.Contains(out, "No drift") {
		t.Fatalf("a run following its plan should say so, got:\n%s", out)
	}
	if strings.Contains(out, "skipped") {
		t.Fatalf("steps nobody has reached yet are queued, not skipped, got:\n%s", out)
	}
}

func TestPlanStatus_IsAvailableBelowTheRailBreakpoint(t *testing.T) {
	// Below 130 content columns there is no rail (§8c), so /plan is the whole
	// of the checklist and must lose nothing.
	m := runningPlanModel(t, 80)
	announce(t, &m, "Locate the round accounting", time.Second, false)
	if m.twoPane() {
		t.Fatal("the fixture should be below the rail's breakpoint")
	}
	out := m.planStatus()
	for _, s := range m.planRun.doc.Steps {
		if !strings.Contains(out, s.Title) {
			t.Fatalf("/plan should list every step, missing %q:\n%s", s.Title, out)
		}
	}
}

func TestPlanDrop_ReturnsTheOutlineToInferredSteps(t *testing.T) {
	m := runningPlanModel(t, 130)
	announce(t, &m, "Locate the round accounting", time.Second, false)

	handled, result := m.handleSlashCommand("/plan drop")
	if !handled || !strings.Contains(result, "Dropped the approved plan") {
		t.Fatalf("/plan drop should drop it, got %q", result)
	}
	if m.planRun != nil {
		t.Fatal("/plan drop should forget the run")
	}
	for _, blk := range m.blocksOf(m.transcript) {
		if blk.step != nil && blk.step.queued() {
			t.Fatal("with no plan there are no declared-but-not-started steps")
		}
	}
}

func TestPlanRun_SurvivesAMessageWhileStepsRemain(t *testing.T) {
	m := runningPlanModel(t, 130)
	announce(t, &m, "Locate the round accounting", time.Second, false)

	next, _ := m.sendUserMessage("keep going")
	if next.(Model).planRun == nil {
		t.Fatal("a plan with steps left to run still answers \"where are we\"")
	}

	// Once the list is through, the next instruction retires it.
	done := runningPlanModel(t, 130)
	for _, s := range done.planRun.doc.Steps {
		announce(t, &done, s.Title, time.Second, false)
	}
	if !done.planRun.complete() {
		t.Fatal("every step announced should complete the run")
	}
	after, _ := done.sendUserMessage("something else")
	if after.(Model).planRun != nil {
		t.Fatal("a plan through its list is retired by the next instruction")
	}
}

func TestPlanRun_ClearedWithTheTranscript(t *testing.T) {
	m := runningPlanModel(t, 130)
	m.resetTranscript()
	if m.planRun != nil {
		t.Fatal("a checklist read off the transcript cannot outlive it")
	}
}

func TestPlanApprove_StartsTheRun(t *testing.T) {
	m := plannedModel(t, planFixture)
	updated, _ := m.approvePlan(agent.ModeAuto)
	got := updated.(Model)
	if got.planRun == nil {
		t.Fatal("approving a structured plan should start its run")
	}
	if n := len(got.planRun.doc.Steps); n != 4 {
		t.Fatalf("the run should carry the plan's four steps, got %d", n)
	}
	if got.planRun.start != len(got.transcript) {
		t.Fatalf("the run starts where the execution turn does, got %d of %d",
			got.planRun.start, len(got.transcript))
	}
}

func TestPlanApprove_ProsePlanStartsNoRun(t *testing.T) {
	m := plannedModel(t, "I would rewrite the loop, then test it.")
	updated, _ := m.approvePlan(agent.ModeAuto)
	if updated.(Model).planRun != nil {
		t.Fatal("a plan with no step list has no checklist to run")
	}
}

func TestPlanChecklist_QueuedStepsAreNotSelectableInFocus(t *testing.T) {
	m := runningPlanModel(t, 130)
	announce(t, &m, "Locate the round accounting", time.Second, false)
	for _, idx := range m.expandableIndices() {
		if idx < 0 || idx >= len(m.transcript) {
			t.Fatalf("focus mode offered index %d, outside the transcript", idx)
		}
	}
}

func TestPlanOutline_RenderIncludesQueuedHeaders(t *testing.T) {
	m := runningPlanModel(t, 130)
	announce(t, &m, "Locate the round accounting", time.Second, false)
	out := ansi.Strip(m.renderHistory())
	for _, want := range []string{
		"1  Locate the round accounting",
		"4  Offer more rounds in the chat model",
		components.OutcomeQueued,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the outline should render %q, got:\n%s", want, out)
		}
	}
}

func TestPlanStatus_OpensWhileTheTurnRuns(t *testing.T) {
	// "Where are we" is a question you ask mid-turn, so /plan is not one of
	// the commands that waits for the turn to finish.
	if reason, ok := idleOnlyReason("/plan"); ok {
		t.Fatalf("/plan should answer mid-turn, it waits for %q", reason)
	}
	m := runningPlanModel(t, 130)
	announce(t, &m, "Locate the round accounting", time.Second, false)
	m.state = stateStreaming
	if !m.working() {
		t.Fatal("the fixture should be mid-turn")
	}
	updated, _ := m.runCommand("/plan", "/plan")
	after := updated.(Model)
	out := ansi.Strip(after.renderHistory())
	if !strings.Contains(out, "Return it from runRound") {
		t.Fatalf("/plan mid-turn should print the checklist, got:\n%s", out)
	}
}
