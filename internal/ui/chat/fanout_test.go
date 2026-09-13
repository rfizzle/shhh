package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// spawnInto starts one child and waits until the supervisor knows about it,
// so a test can look at the batch rather than at a race.
func spawnInto(t *testing.T, sup *subagent.Supervisor, args string) {
	t.Helper()
	exec := sup.WrapExecutor("", nil)
	if _, err := exec(subagent.SpawnToolName, json.RawMessage(args)); err != nil {
		t.Fatal(err)
	}
}

// spawnRow is the transcript entry a successful spawn produces before the
// fan-out decides whether it stays a row.
func spawnRowEntry(task string) entry {
	return entry{kind: entryTool, toolName: subagent.SpawnToolName,
		toolArgs: `{"role":"researcher","task":"` + task + `"}`, toolResult: "Spawned it."}
}

// fanoutEntries counts what the transcript holds, which is the whole question
// for a batch: one block, or a row per child.
func fanoutEntries(m Model) (blocks, spawnRows int) {
	for _, e := range m.transcript {
		switch {
		case e.kind == entryFanout:
			blocks++
		case e.kind == entryTool && e.toolName == subagent.SpawnToolName:
			spawnRows++
		}
	}
	return blocks, spawnRows
}

// TestSingleSpawnKeepsItsRow is the criterion that keeps the block for
// genuine fan-out: one child is not a fan-out, and its inline row stands.
func TestSingleSpawnKeepsItsRow(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	m.beginSpawnBatch()
	spawnInto(t, sup, `{"role":"researcher","task":"survey the loop"}`)
	m.appendSpawnEntry(spawnRowEntry("survey the loop"))

	blocks, rows := fanoutEntries(m)
	if blocks != 0 || rows != 1 {
		t.Fatalf("one spawn produced %d blocks and %d rows, want 0 and 1", blocks, rows)
	}
}

// TestTwoSpawnsBecomeOneBlock is the first criterion: a round that spawned
// two or more children renders as one block, in place of their rows, without
// disturbing the entries around it.
func TestTwoSpawnsBecomeOneBlock(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	m.appendEntry(entry{kind: entryAssistant, text: "Document and verify in parallel"})

	m.beginSpawnBatch()
	for _, task := range []string{"survey the loop", "survey the tests", "survey the folds"} {
		spawnInto(t, sup, `{"role":"researcher","task":"`+task+`"}`)
		m.appendSpawnEntry(spawnRowEntry(task))
	}

	blocks, rows := fanoutEntries(m)
	if blocks != 1 || rows != 0 {
		t.Fatalf("three spawns produced %d blocks and %d rows, want 1 and 0", blocks, rows)
	}
	if len(m.transcript) != 2 {
		t.Fatalf("transcript is %d entries, want the announcement and the block", len(m.transcript))
	}
	if m.transcript[0].kind != entryAssistant {
		t.Fatal("the fan-out disturbed the entry before it")
	}

	view := ansi.Strip(m.renderHistory())
	for _, want := range []string{"fan-out", "3 agents", "researcher-1", "researcher-2", "researcher-3"} {
		if !strings.Contains(view, want) {
			t.Fatalf("rendered history missing %q:\n%s", want, view)
		}
	}
}

// A hold is asked of the session and reaches every child, each parking at its
// own round boundary
// (docs/capabilities/subagents.md#a-hold-reaches-the-whole-fan-out). What the
// surfaces owe a reader who pressed it is the word: the lane says it where an
// idle child says idle, the header counts the parks as they land, and the
// rail's map row says it where a working child's row draws motion.
func TestFanoutHeldChildIsDrawnHeldAndNotIdle(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: heldChildEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	// The hold is taken before the children start, so their first round
	// boundary is the one they park at.
	sup.Hold()
	m.beginSpawnBatch()
	for _, task := range []string{"survey the loop", "survey the tests"} {
		spawnInto(t, sup, `{"role":"researcher","task":"`+task+`"}`)
		m.appendSpawnEntry(spawnRowEntry(task))
	}
	for _, name := range []string{"researcher-1", "researcher-2"} {
		waitFor(t, func() bool {
			st, ok := sup.Get(name)
			return ok && st.Held
		})
	}

	view := ansi.Strip(m.renderHistory())
	if n := strings.Count(view, "⏸ held"); n != 2 {
		t.Fatalf("both parked lanes should say they are held, %d did:\n%s", n, view)
	}
	if !strings.Contains(view, "2 held") {
		t.Fatalf("the header should count the parks:\n%s", view)
	}
	if strings.Contains(view, "idle") {
		t.Fatalf("a child parked on purpose is not an idle one:\n%s", view)
	}
	for _, a := range m.inspectorAgents() {
		if a.Self {
			continue
		}
		if a.State != components.FanoutHeld || a.Outcome != "held" {
			t.Fatalf("the rail's map row should say a parked child is held: %+v", a)
		}
	}
}

// TestSecondRoundIsItsOwnBlock keeps two fan-outs apart: a batch is a round,
// so the children of a later round never join an earlier block.
func TestSecondRoundIsItsOwnBlock(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	for range 2 {
		m.beginSpawnBatch()
		for _, task := range []string{"one", "two"} {
			spawnInto(t, sup, `{"role":"researcher","task":"`+task+`"}`)
			m.appendSpawnEntry(spawnRowEntry(task))
		}
	}

	blocks, rows := fanoutEntries(m)
	if blocks != 2 || rows != 0 {
		t.Fatalf("two rounds produced %d blocks and %d rows, want 2 and 0", blocks, rows)
	}
	for _, e := range m.transcript {
		if e.kind != entryFanout {
			continue
		}
		if n := len(m.fanoutBlockFor(e).Lanes); n != 2 {
			t.Fatalf("a block has %d lanes, want the 2 its own round spawned", n)
		}
	}
}

// TestFanoutLanesUpdateInPlace is the second criterion, and the reason the
// entry stores a batch number rather than rendered text: the block re-reads
// the supervisor every render, and a block that is no longer the last thing
// in the transcript still moves.
func TestFanoutLanesUpdateInPlace(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	m.beginSpawnBatch()
	for _, task := range []string{"one", "two"} {
		spawnInto(t, sup, `{"role":"researcher","task":"`+task+`"}`)
		m.appendSpawnEntry(spawnRowEntry(task))
	}
	waitFor(t, func() bool { a, _ := sup.ActiveCounts(); return a == 2 })

	// Rows land after the block, so everything before them would ordinarily
	// freeze into the render cache.
	m.appendEntry(entry{kind: entryAssistant, text: "Now wait for them"})
	m.appendEntry(entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"loop.go"}`, toolResult: "x"})
	if running := ansi.Strip(m.renderHistory()); !strings.Contains(running, "2 running") {
		t.Fatalf("live block does not report its running children:\n%s", running)
	}

	m.cancelSubagents()
	waitFor(t, func() bool { a, _ := sup.ActiveCounts(); return a == 0 })

	stopped := ansi.Strip(m.renderHistory())
	if strings.Contains(stopped, "2 running") {
		t.Fatalf("the block froze with its children still shown as running:\n%s", stopped)
	}
	if !strings.Contains(stopped, "failed") {
		t.Fatalf("the block does not report the cancelled children:\n%s", stopped)
	}
}

// TestFanoutBlockFreezesOnceEveryChildStops is the other half of that: the
// render cache is not given up forever, only for as long as the lanes move.
func TestFanoutBlockFreezesOnceEveryChildStops(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	m.beginSpawnBatch()
	for _, task := range []string{"one", "two"} {
		spawnInto(t, sup, `{"role":"researcher","task":"`+task+`"}`)
		m.appendSpawnEntry(spawnRowEntry(task))
	}
	waitFor(t, func() bool { a, _ := sup.ActiveCounts(); return a == 2 })

	blocks := m.blocksOf(m.transcript)
	if got := m.liveFanoutBlock(blocks); got != 0 {
		t.Fatalf("a live fan-out is at block %d, want 0 — nothing from there on may freeze", got)
	}

	m.cancelSubagents()
	waitFor(t, func() bool { a, _ := sup.ActiveCounts(); return a == 0 })
	if got := m.liveFanoutBlock(blocks); got != len(blocks) {
		t.Fatalf("a settled fan-out still blocks the cache at %d of %d", got, len(blocks))
	}
}

// gatedEnv builds children that immediately ask to run a command, so the
// supervisor parks them blocked on the parent user.
func gatedEnv() subagent.EnvFactory {
	return func(ctx context.Context, spec subagent.Spec) (subagent.Env, error) {
		// The same gated call every round, forever: these tests want a child
		// parked on an approval, not one that finishes. Honouring ctx is what
		// makes that safe — a real provider stream is bound to the child's
		// context, so cancelling it ends the tool loop. Without this the mock
		// is an infinite generator that only ever stopped because the round
		// cap stopped it, and a child with no cap would spin past
		// Close.
		stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			ch := make(chan provider.StreamEvent, 1)
			ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{{
				ID: "c1", Name: tools.ExecCommandName, Arguments: `{"command":"echo hi"}`,
			}}}
			close(ch)
			return ch, func() {}, nil
		}
		return subagent.Env{
			SystemPrompt: "sys",
			Stream:       stream,
			Executor:     func(string, json.RawMessage) (string, error) { return "", errors.New("unused") },
			Gated:        map[string]bool{tools.ExecCommandName: true},
		}, nil
	}
}

// TestFanoutBlockedLaneStatesWhatItNeeds is the third criterion at the host
// boundary: a child parked on an approval reaches the lane as the blocked
// state, with the thing it is waiting for stated under it.
func TestFanoutBlockedLaneStatesWhatItNeeds(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: gatedEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	m.beginSpawnBatch()
	for _, task := range []string{"one", "two"} {
		spawnInto(t, sup, `{"role":"researcher","task":"`+task+`"}`)
		m.appendSpawnEntry(spawnRowEntry(task))
	}
	waitFor(t, func() bool { _, blocked := sup.ActiveCounts(); return blocked == 2 })

	view := ansi.Strip(m.renderHistory())
	if !strings.Contains(view, "⚠ needs you") {
		t.Fatalf("a blocked child's lane does not say it needs you:\n%s", view)
	}
	if !strings.Contains(view, "echo hi") {
		t.Fatalf("a blocked child's lane does not say what it is waiting for:\n%s", view)
	}
	if !strings.Contains(view, "2 needs you") {
		t.Fatalf("the header does not carry the blocked count:\n%s", view)
	}
}

// TestFanoutDrawsADescendantUnderTheLaneThatSpawnedIt: a child a child
// spawned during the round joins the same block, drawn directly under the
// lane it hangs off whatever order the supervisor started it in, one column
// in behind the corner, and its parent's lane says how many are under it.
func TestFanoutDrawsADescendantUnderTheLaneThatSpawnedIt(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	m.beginSpawnBatch()
	for _, task := range []string{"one", "two"} {
		spawnInto(t, sup, `{"role":"researcher","task":"`+task+`"}`)
		m.appendSpawnEntry(spawnRowEntry(task))
	}
	waitFor(t, func() bool { running, _ := sup.ActiveCounts(); return running == 2 })
	// The first of them delegates, after the second was already spawned: in
	// the supervisor's own order the grandchild is last, and on the block it
	// belongs under the lane that asked for it.
	spawnUnder(t, sup, "researcher-1", subagent.RoleReviewer, "reviewer-1")

	var lanes []string
	var under int
	for _, e := range m.transcript {
		if e.kind != entryFanout {
			continue
		}
		for _, l := range m.fanoutBlockFor(e).Lanes {
			lanes = append(lanes, l.Name+"@"+strconv.Itoa(l.Depth))
			if l.Name == "researcher-1" {
				under = l.Under
			}
		}
	}
	if want := "researcher-1@1,reviewer-1@2,researcher-2@1"; strings.Join(lanes, ",") != want {
		t.Fatalf("lanes = %v, want %s", lanes, want)
	}
	if under != 1 {
		t.Fatalf("the parent's lane counts %d under it, want 1", under)
	}
	view := ansi.Strip(m.renderHistory())
	if !strings.Contains(view, "1 agent under it") {
		t.Fatalf("the parent's lane should say how many are under it:\n%s", view)
	}
	if !strings.Contains(view, "  └◇ agent   reviewer-1") {
		t.Fatalf("the grandchild's lane should draw behind the corner:\n%s", view)
	}
}

// TestFanoutRerendersOnResize is the last criterion: the block is a passive
// transcript entry, so a resize re-renders it at the new width like anything
// else in the feed.
func TestFanoutRerendersOnResize(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	m.beginSpawnBatch()
	for _, task := range []string{"one", "two"} {
		spawnInto(t, sup, `{"role":"researcher","task":"`+task+`"}`)
		m.appendSpawnEntry(spawnRowEntry(task))
	}
	waitFor(t, func() bool { a, _ := sup.ActiveCounts(); return a == 2 })

	wide := ansi.Strip(m.renderHistory())
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 62, Height: 40})
	m = updated.(Model)
	narrow := ansi.Strip(m.renderHistory())

	if wide == narrow {
		t.Fatal("the block did not re-render at the new width")
	}
	for _, line := range strings.Split(narrow, "\n") {
		if len([]rune(line)) > 62 {
			t.Fatalf("a line survived the resize at %d columns: %q", len([]rune(line)), line)
		}
	}
	if !strings.Contains(narrow, "researcher-1") {
		t.Fatalf("the narrow render dropped a child's name:\n%s", narrow)
	}
}

// A child's lane reports what the child was billed, not a re-pricing of its
// token pair at the fresh input rate. A child re-sends its prompt every round
// and the provider serves nearly all of it from cache, so the two figures are
// not close — and the lane, the agent map and the attached rail all read this
// one label.
func TestChildSpendLabel_ReportsTheBillNotTheFreshRate(t *testing.T) {
	table := pricing.NewTable(map[string]pricing.ModelPricing{
		"cheap-1": {
			InputCostPerToken:     0.0000015,
			CacheReadCostPerToken: 0.00000015,
			OutputCostPerToken:    0.000009,
		},
		"expensive-1": {InputCostPerToken: 0.00003, OutputCostPerToken: 0.00006},
	})
	m := New(nil, mockStream).WithPricing(table, "expensive-1")

	spend := meter.New(table)
	usage := provider.Usage{PromptTokens: 1_000_000, CachedTokens: 900_000, CompletionTokens: 1_000}
	spend.Record(meter.Origin{Source: meter.SourceSubagent, Label: "researcher-1"}, "cheap-1", usage)
	st := subagent.Status{Name: "researcher-1", Model: "cheap-1", Spend: spend.Total()}

	got := m.childSpendLabel(st)
	if want := formatCost(spend.Total().Cost); got != want {
		t.Fatalf("the lane should report what the child was billed: got %q, want %q", got, want)
	}
	// The same tokens with the split thrown away, which is what the lane
	// printed before: the whole input at the fresh rate, on the child's own
	// model. Naming it is the point — the two are the same child.
	in, out, _ := table.Cost("cheap-1", st.Spend.In, st.Spend.Out)
	if fresh := formatCost(in + out); got == fresh {
		t.Fatalf("a cache read billed at the fresh input rate: label and fresh-rate reading both %q", got)
	}
	// And never at the orchestrator's rate, which is the model the session is
	// on and not the one that answered.
	sin, sout, _ := table.Cost("expensive-1", st.Spend.In, st.Spend.Out)
	if got == formatCost(sin+sout) {
		t.Fatalf("the child was priced against the orchestrator's model: %q", got)
	}
}

// A child nothing could price falls back to its own model's fresh rate, not
// the orchestrator's — the fallback is a worse figure, not a wrong model.
func TestChildSpendLabel_UnpricedFallsBackToTheChildsModel(t *testing.T) {
	table := pricing.NewTable(map[string]pricing.ModelPricing{
		"cheap-1":     {InputCostPerToken: 0.0000015, OutputCostPerToken: 0.000009},
		"expensive-1": {InputCostPerToken: 0.00003, OutputCostPerToken: 0.00006},
	})
	m := New(nil, mockStream).WithPricing(table, "expensive-1")
	st := subagent.Status{Name: "researcher-1", Model: "cheap-1",
		Spend: meter.Totals{In: 1_000_000, Out: 1_000}}

	in, out, _ := table.Cost("cheap-1", 1_000_000, 1_000)
	if got, want := m.childSpendLabel(st), formatCost(in+out); got != want {
		t.Fatalf("an unpriced child = %q, want its own model's rate %q", got, want)
	}
}

// childReport is the text a scripted child ends on, written the way a child's
// final report is: a first line the lane keeps, and a section for what it
// assumed instead of asking.
const childReportText = "Counted the rounds.\n\n## Assumptions\n\n" +
	"- The limit is the one in loop.go.\n- Nothing else reads the counter."

// A settled lane folds the child's own report under it, the count of what it
// assumed goes on the detail line, and the key that opens every other fold in
// the transcript opens this one.
func TestFanoutSettledLaneOpensOnTheReport(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{
		Root: t.TempDir(), NewEnv: reportingEnv(childReportText)})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	m.beginSpawnBatch()
	for _, task := range []string{"one", "two"} {
		spawnInto(t, sup, `{"role":"researcher","task":"`+task+`"}`)
		m.appendSpawnEntry(spawnRowEntry(task))
	}
	waitFor(t, func() bool { a, _ := sup.ActiveCounts(); return a == 0 })

	shut := ansi.Strip(m.renderHistory())
	if !strings.Contains(shut, "▸ report · 6 lines · [enter] expand") {
		t.Fatalf("a settled lane should offer the report as a fold:\n%s", shut)
	}
	// The first line and the count, and no continuation marker between them:
	// the fold under the line is what says there is more of it, and says how
	// much.
	if !strings.Contains(shut, "Counted the rounds. · 2 assumptions") {
		t.Fatalf("the detail line should count the assumptions:\n%s", shut)
	}
	if strings.Contains(shut, "Nothing else reads the counter.") {
		t.Fatalf("a shut fold should hold the report back:\n%s", shut)
	}

	idx := -1
	for i, e := range m.transcript {
		if e.kind == entryFanout {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("the two spawns did not become a block")
	}
	if !m.selectableRow(m.transcript[idx]) {
		t.Fatal("the reading cursor cannot stop on a block holding a report")
	}
	m.focusIdx = idx
	if !m.focusedExpands() {
		t.Fatal("the bar does not offer the key the block honours")
	}
	updated, _ := m.openCursorRow(stateFocus)
	m = updated.(Model)

	open := ansi.Strip(m.renderHistory())
	if !strings.Contains(open, "▾ report · 6 lines") {
		t.Fatalf("the fold did not open:\n%s", open)
	}
	if !strings.Contains(open, "Nothing else reads the counter.") {
		t.Fatalf("the opened fold does not show the report:\n%s", open)
	}
}

// A block whose children are all still running has nothing to open, and the
// reading cursor does not stop on it: a key offered where it does nothing
// reads as a key that is broken.
func TestFanoutLiveBlockIsNotACursorStop(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{
		Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	m.beginSpawnBatch()
	for _, task := range []string{"one", "two"} {
		spawnInto(t, sup, `{"role":"researcher","task":"`+task+`"}`)
		m.appendSpawnEntry(spawnRowEntry(task))
	}
	waitFor(t, func() bool { a, _ := sup.ActiveCounts(); return a == 2 })

	for _, e := range m.transcript {
		if e.kind == entryFanout && m.selectableRow(e) {
			t.Fatal("a running fan-out offers a fold it does not have")
		}
	}
}

// A review's last line is its verdict, and the lane says it beside the state.
// Everything else a report can end on is prose, which the outcome field has
// no business quoting a clause of.
func TestReportVerdictReadsTheLastLine(t *testing.T) {
	for _, tc := range []struct{ name, report, want string }{
		{"bare", "Read it all.\n\napprove with changes", "approve with changes"},
		{"labelled", "Read it all.\n\nVerdict: request changes", "request changes"},
		{"bold label", "Read it all.\n\n**Verdict:** approve", "approve"},
		{"a label makes a sentence one", "Read it all.\n\nVerdict: ship it.", "ship it"},
		{"trailing blanks", "approve\n\n\n", "approve"},
		{"prose", "The change is fine but the test names are wrong and I would rename them.", ""},
		{"two statements", "Findings: three of them", ""},
		{"a sentence of few words", "It is fine. Ship it.", ""},
		// A report that ends on an ordinary short sentence ended on prose.
		// Unlabelled, a stop or a subject is what says so.
		{"a short sentence", "Read it all.\n\nShip it.", ""},
		{"a short verdict with a stop", "Read it all.\n\nApprove.", ""},
		{"no changes needed", "Read it all.\n\nNo changes needed.", ""},
		{"a subject and no stop", "Read it all.\n\nThe tests pass now", ""},
		{"nothing", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reportVerdict(tc.report); got != tc.want {
				t.Fatalf("reportVerdict(%q) = %q, want %q", tc.report, got, tc.want)
			}
		})
	}
}

// The assumptions a child states instead of asking are counted off the
// section it wrote them under, however it wrote the heading. A report with no
// such section counts nothing rather than asserting a zero.
func TestStatedAssumptionsCountsTheSection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report string
		want   int
	}{
		{"markdown heading", "Done.\n\n## Assumptions\n- one\n- two\n", 2},
		{"bold heading", "Done.\n\n**Assumptions**\n\n* one\n", 1},
		{"labelled", "Done.\n\nAssumptions:\n1. one\n2. two\n3. three\n", 3},
		{"stops at the next section", "## Assumptions\n- one\n\n## Findings\n- a\n- b\n", 1},
		{"no section", "Done. I assumed the limit was the one in loop.go.\n", 0},
		{"an empty section", "## Assumptions\n\nNone.\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := statedAssumptions(tc.report); got != tc.want {
				t.Fatalf("statedAssumptions(%q) = %d, want %d", tc.report, got, tc.want)
			}
		})
	}
}

// Only a review has a verdict to read off its last line: its prompt makes it
// end on one, and every other role ends on prose. Two children with the same
// last line, and only the reviewer's lane states it.
func TestFanoutVerdictIsTheReviewersAlone(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{
		Root: t.TempDir(), NewEnv: reportingEnv("Read it all.\n\napprove with changes")})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	m.beginSpawnBatch()
	for _, spawn := range []string{
		`{"role":"reviewer","task":"read the round change"}`,
		`{"role":"researcher","task":"survey the round accounting"}`,
	} {
		spawnInto(t, sup, spawn)
		m.appendSpawnEntry(spawnRowEntry("a task"))
	}
	waitFor(t, func() bool { a, _ := sup.ActiveCounts(); return a == 0 })

	view := ansi.Strip(m.renderHistory())
	if !strings.Contains(view, "✓ done · approve with changes") {
		t.Fatalf("the review's lane should carry its verdict:\n%s", view)
	}
	if n := strings.Count(view, "approve with changes"); n != 1 {
		t.Fatalf("%d lanes state a verdict, want the reviewer's alone:\n%s", n, view)
	}
}
