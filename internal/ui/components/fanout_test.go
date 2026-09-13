package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// fanoutFixture is the block every test here starts from: one child running
// against a declared step count, one running without one, one waiting on an
// answer, one finished with something to report, and one broken.
func fanoutFixture() FanoutBlock {
	return FanoutBlock{
		Elapsed: "1m12s",
		Keys:    []TurnKey{{Key: "[ctrl+a]", Label: "agents"}},
		Lanes: []FanoutLane{
			{State: FanoutRunning, Name: "writer-1", Task: "docs/loop.md",
				Step: 2, Steps: 5, Tools: 6, Spend: "$0.02", Elapsed: "12s"},
			{State: FanoutRunning, Name: "reader-2", Task: "survey internal/ui",
				Tools: 1, Spend: "$0.01", Elapsed: "3.0s"},
			{State: FanoutBlocked, Name: "scout-3", Task: "other callers",
				Tools: 3, Spend: "$0.01", Elapsed: "18s",
				Waiting: "waiting approval: read ../plugins/registry.go"},
			{State: FanoutDone, Name: "tester-4", Task: "internal/agent tests",
				Tools: 9, Spend: "$0.03", Elapsed: "41s", Summary: "all four packages pass"},
			{State: FanoutFailed, Name: "patcher-5", Task: "apply the patch",
				Tools: 12, Spend: "$0.05", Elapsed: "2m04s", Summary: "round limit (25) reached"},
		},
	}
}

// plainLines strips the ANSI and returns the block's lines, which is how
// every layout assertion here reads it.
func plainLines(view string) []string {
	return strings.Split(ansi.Strip(view), "\n")
}

// TestFanoutLanePerChild covers the first criterion: one lane per child, each
// carrying its name, its task, its progress, its tool count, its spend and
// its elapsed.
func TestFanoutLanePerChild(t *testing.T) {
	view := fanoutFixture().View(110)
	for _, want := range []string{
		"writer-1", "docs/loop.md", "2/5", "6 tools", "$0.02", "12s",
		"reader-2", "survey internal/ui", "1 tool", "$0.01", "3.0s",
	} {
		if !strings.Contains(ansi.Strip(view), want) {
			t.Fatalf("lane missing %q:\n%s", want, ansi.Strip(view))
		}
	}
	// Five children, five lanes, plus the header and the notes under three of
	// them and the offers line.
	lanes := 0
	for _, line := range plainLines(view) {
		if strings.Contains(line, "agent   ") {
			lanes++
		}
	}
	if lanes != 5 {
		t.Fatalf("rendered %d lanes, want 5:\n%s", lanes, ansi.Strip(view))
	}
}

// TestFanoutBlockedSortsToTheTop covers the criterion that a child needing an
// answer is never something you scroll to: its lane leads the block and says
// so in words, not only in colour.
func TestFanoutBlockedSortsToTheTop(t *testing.T) {
	lines := plainLines(fanoutFixture().View(110))
	if len(lines) < 2 {
		t.Fatalf("block too short:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[1], "scout-3") {
		t.Fatalf("the blocked lane is not first: %q", lines[1])
	}
	if !strings.Contains(lines[1], "⚠ needs you") {
		t.Fatalf("the blocked lane does not say what it needs: %q", lines[1])
	}
	if !strings.Contains(lines[2], "waiting approval") {
		t.Fatalf("the blocked lane does not say what it is waiting for: %q", lines[2])
	}
	if !strings.Contains(lines[0], "1 needs you") {
		t.Fatalf("the header does not carry the blocked count: %q", lines[0])
	}
}

// TestFanoutOffersOnlyWhileBlocked keeps the manager offer honest: it is the
// answer to a blocked child, so it is not on a block where nobody is waiting.
func TestFanoutOffersOnlyWhileBlocked(t *testing.T) {
	blocked := fanoutFixture()
	if !strings.Contains(ansi.Strip(blocked.View(110)), "[ctrl+a] agents") {
		t.Fatal("a blocked block should offer the manager")
	}

	var running FanoutBlock
	running.Keys = blocked.Keys
	for _, l := range blocked.Lanes {
		if l.State != FanoutBlocked {
			running.Lanes = append(running.Lanes, l)
		}
	}
	if strings.Contains(ansi.Strip(running.View(110)), "ctrl+a") {
		t.Fatal("a block with nobody waiting should offer nothing")
	}
}

// TestFanoutProgressNeedsADeclaredCount is the meter rule on a lane: a bar
// only where the spawn declared a step count, the spinner everywhere else,
// and never a ratio nobody supplied.
func TestFanoutProgressNeedsADeclaredCount(t *testing.T) {
	declared := FanoutLane{State: FanoutRunning, Name: "writer-1", Step: 2, Steps: 5}
	bar := ansi.Strip(declared.View(110))
	if !strings.Contains(bar, "▰") || !strings.Contains(bar, "2/5") {
		t.Fatalf("a declared step count should draw its bar and its number: %q", bar)
	}

	none := FanoutLane{State: FanoutRunning, Name: "writer-1", Frame: 2}
	spun := ansi.Strip(none.View(110))
	if strings.Contains(spun, "▰") || strings.Contains(spun, "▱") {
		t.Fatalf("a lane with no declared count drew a bar: %q", spun)
	}
	if !strings.Contains(spun, SpinnerFrames[2]) || !strings.Contains(spun, "working") {
		t.Fatalf("a lane with no declared count should spin beside a word: %q", spun)
	}
}

// TestFanoutFinishedLaneKeepsItsResult covers the second half of the
// update-in-place criterion: a lane that has stopped reports its outcome and
// what it found. A child that declared a step count keeps the bar it climbed,
// full and ticked, because the bar has stopped measuring and started stating
// — a lane that dropped it would answer "did it get through the five" by
// taking the five away.
func TestFanoutFinishedLaneKeepsItsResult(t *testing.T) {
	done := FanoutLane{State: FanoutDone, Name: "tester-4", Task: "internal/agent tests",
		Step: 2, Steps: 5, Tools: 9, Spend: "$0.03", Elapsed: "41s",
		Summary: "all four packages pass"}
	view := ansi.Strip(done.View(110))
	if !strings.Contains(view, "▰▰▰▰▰ ✓ 5/5") {
		t.Fatalf("a finished lane should state its outcome on a full meter: %q", view)
	}
	if strings.Contains(view, "2/5") || strings.Contains(view, "▱") {
		t.Fatalf("a finished lane still has room left on its bar: %q", view)
	}
	if !strings.Contains(view, "all four packages pass") {
		t.Fatalf("a finished lane should keep its result summary: %q", view)
	}

	bare := FanoutLane{State: FanoutDone, Name: "tester-5", Summary: "nothing to do"}
	bview := ansi.Strip(bare.View(110))
	if !strings.Contains(bview, "✓ done") {
		t.Fatalf("a finished lane that declared no count should say so in words: %q", bview)
	}

	failed := FanoutLane{State: FanoutFailed, Name: "patcher-5", Summary: "round limit (25) reached"}
	fview := ansi.Strip(failed.View(110))
	if !strings.Contains(fview, "✗ failed") {
		t.Fatalf("a broken lane should state its outcome in glyph and word: %q", fview)
	}
	if !strings.Contains(fview, "round limit") {
		t.Fatalf("a broken lane should say why: %q", fview)
	}
}

// TestFanoutLaneKeepsItsKindGlyph is the rule the Agents artboard draws and
// docs/interface/departures.md#a-fan-out-lane-keeps-its-kind-glyph-and-a-manager-row-does-not
// argues: the lane's glyph column says what the child is in every state, and
// the state is said in the outcome field beside it, in words. A monochrome
// terminal has to read the same lane, so the words are the assertion.
func TestFanoutLaneKeepsItsKindGlyph(t *testing.T) {
	for _, tc := range []struct {
		name    string
		lane    FanoutLane
		outcome string
	}{
		{"running", FanoutLane{State: FanoutRunning, Name: "a", Step: 2, Steps: 5}, "2/5"},
		{"blocked", FanoutLane{State: FanoutBlocked, Name: "a"}, "⚠ needs you"},
		{"done", FanoutLane{State: FanoutDone, Name: "a"}, "✓ done"},
		{"failed", FanoutLane{State: FanoutFailed, Name: "a"}, "✗ failed"},
		{"queued", FanoutLane{State: FanoutQueued, Name: "a"}, "queued"},
		{"idle", FanoutLane{State: FanoutIdle, Name: "a"}, "idle"},
		{"held", FanoutLane{State: FanoutHeld, Name: "a"}, "⏸ held"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := ansi.Strip(tc.lane.View(110))
			if !strings.Contains(view, "◇") {
				t.Errorf("the lane dropped its kind glyph: %q", view)
			}
			if !strings.Contains(view, tc.outcome) {
				t.Errorf("the outcome field should say %q: %q", tc.outcome, view)
			}
		})
	}
}

// A manager row is a row, and a row keeps the outcome table's rule: the two
// states that ask something of the reader take the glyph column, and a
// finished child does not.
func TestAgentRowFollowsTheOutcomeTable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state AgentState
		want  string
	}{
		{"blocked", AgentBlocked, "⚠"},
		{"failed", AgentFailed, "✗"},
		{"running", AgentRunning, "◇"},
		{"done", AgentDone, "◇"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ansi.Strip(AgentRow{State: tc.state, Name: "writer-1"}.stateGlyph())
			if got != tc.want {
				t.Errorf("stateGlyph() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A lane joins the child's name to what it was asked to do with the separator
// every row in the product joins two facts with.
func TestFanoutLaneJoinsNameAndTaskWithTheSeparator(t *testing.T) {
	lane := FanoutLane{State: FanoutRunning, Name: "writer-1", Task: "docs/loop.md"}
	if view := ansi.Strip(lane.View(110)); !strings.Contains(view, "writer-1 · docs/loop.md") {
		t.Fatalf("the lane should join its name and task with ` · `: %q", view)
	}
}

// The block's header mark sits in the marker gutter's own first column, where
// a step header's fold caret and a sent message's ❯ sit
// (docs/interface/surfaces.md#the-leading-columns).
func TestFanoutHeaderTakesThePointerColumn(t *testing.T) {
	head := plainLines(fanoutFixture().View(110))[0]
	if !strings.HasPrefix(head, "◇ ") {
		t.Fatalf("the header should start in the pointer column: %q", head)
	}
}

// TestFanoutNamesSurviveEveryWidth is why a lane's name is the growing target
// field rather than a word in the eight-column verb column: two children of
// the same role differ only in the digit a clipped name would eat.
func TestFanoutNamesSurviveEveryWidth(t *testing.T) {
	block := FanoutBlock{Lanes: []FanoutLane{
		{State: FanoutRunning, Name: "researcher-1", Task: "survey the loop", Tools: 2, Elapsed: "9s"},
		{State: FanoutRunning, Name: "researcher-2", Task: "survey the tests", Tools: 3, Elapsed: "8s"},
	}}
	for _, width := range []int{60, 80, 110, 130} {
		view := ansi.Strip(block.View(width))
		for _, name := range []string{"researcher-1", "researcher-2"} {
			if !strings.Contains(view, name) {
				t.Fatalf("width %d clipped %q:\n%s", width, name, view)
			}
		}
	}
}

// TestFanoutFitsItsWidth is the resize contract: every line of the block sits
// inside the terminal it was handed, at each breakpoint.
func TestFanoutFitsItsWidth(t *testing.T) {
	for _, width := range []int{60, 80, 110, 130} {
		for _, line := range plainLines(fanoutFixture().View(width)) {
			if w := lipgloss.Width(line); w > width {
				t.Fatalf("width %d: line is %d columns: %q", width, w, line)
			}
		}
	}
}

// TestFanoutEmptyRendersNothing keeps a batch whose children the supervisor
// no longer knows about from leaving a header with no lanes under it.
func TestFanoutEmptyRendersNothing(t *testing.T) {
	if v := (FanoutBlock{}).View(110); v != "" {
		t.Fatalf("an empty block rendered %q", v)
	}
}

// TestFanoutHeaderSettles covers the header's second reading: while children
// run it says what is outstanding, and once they have all stopped it reports
// the tally, because there is nothing outstanding left to say.
func TestFanoutHeaderSettles(t *testing.T) {
	settled := FanoutBlock{Lanes: []FanoutLane{
		{State: FanoutDone, Name: "a"}, {State: FanoutDone, Name: "b"}, {State: FanoutFailed, Name: "c"},
	}}
	header := plainLines(settled.View(110))[0]
	if !strings.Contains(header, "2 done") || !strings.Contains(header, "1 failed") {
		t.Fatalf("a settled block should report its tally: %q", header)
	}
	if !strings.Contains(header, "3 agents") {
		t.Fatalf("the header should name the size of the fan-out: %q", header)
	}
}

// A hold parks each child at its own boundary, so the header is what says how
// far through the fan-out that has got — and a park is not the idle a
// cancelled turn leaves, so it has a word of its own
// (docs/capabilities/subagents.md#a-hold-reaches-the-whole-fan-out).
func TestFanoutHeaderCountsTheParksAsTheyLand(t *testing.T) {
	block := FanoutBlock{Lanes: []FanoutLane{
		{State: FanoutHeld, Name: "a"}, {State: FanoutHeld, Name: "b"},
		{State: FanoutRunning, Name: "c"},
	}}
	header := plainLines(block.View(110))[0]
	if !strings.Contains(header, "2 held · 1 running") {
		t.Fatalf("the header should count the parks beside what is still going: %q", header)
	}
	// A child that asks for an answer while the parks land is what the field
	// keeps instead of the remainder: the tally says two things at most, and
	// the two are the ones the reader is acting on.
	block.Lanes = append(block.Lanes, FanoutLane{State: FanoutBlocked, Name: "d"})
	header = plainLines(block.View(110))[0]
	if !strings.Contains(header, "1 needs you · 2 held") || strings.Contains(header, "running") {
		t.Fatalf("the tally should keep the two clauses the reader acts on: %q", header)
	}
}

// A child the machinery has had to steer says so under its lane, where the
// seed line goes and where nothing has to give way for it — the right-hand
// field is what the child's name is clipped for, and a name clipped to an
// ellipsis is a lane the reader cannot tell from the one under it. A steer
// outranks the seed line, a settled lane keeps its result, and a child nobody
// has had to steer says nothing, which is almost every child.
func TestFanoutLaneCountsTheSteersItWasGiven(t *testing.T) {
	steered := FanoutLane{State: FanoutRunning, Name: "writer-1", Task: "docs/loop.md",
		Tools: 12, Spend: "$0.02", Steers: 2, Verdict: "off target", Seeded: 5}
	view := ansi.Strip(steered.View(110))
	if !strings.Contains(view, "2 steers · last read off target") {
		t.Fatalf("a steered lane should say how often under it: %q", view)
	}
	// The reading is stated, never inferred from the count: a child steered
	// twice and back on task is what the mechanism is for, and a lane that
	// read the count as the verdict would call that one off target.
	back := FanoutLane{State: FanoutRunning, Name: "writer-1", Steers: 2, Verdict: "on target"}
	if view := ansi.Strip(back.View(110)); !strings.Contains(view, "2 steers · last read on target") {
		t.Fatalf("the lane states the reading it has: %q", view)
	}
	if strings.Contains(view, "started from") {
		t.Fatalf("news outranks the seed line: %q", view)
	}
	if !strings.Contains(view, "writer-1") {
		t.Fatalf("the name stays whole: %q", view)
	}
	one := FanoutLane{State: FanoutRunning, Name: "writer-1", Tools: 3, Steers: 1}
	if view := ansi.Strip(one.View(110)); !strings.Contains(view, "1 steer") {
		t.Fatalf("one steer is one steer, and says so without a reading: %q", view)
	}
	done := FanoutLane{State: FanoutDone, Name: "writer-1", Steers: 2, Summary: "documented the sentinel"}
	if view := ansi.Strip(done.View(110)); !strings.Contains(view, "documented the sentinel") ||
		strings.Contains(view, "steer") {
		t.Fatalf("a finished lane keeps its result: %q", view)
	}
	none := FanoutLane{State: FanoutRunning, Name: "writer-1", Tools: 3}
	if view := ansi.Strip(none.View(110)); strings.Contains(view, "steer") {
		t.Fatalf("a child nobody steered should say nothing: %q", view)
	}
}

// Who steered the child is on the lane beside how often, because a steer now
// has more than one author: the child's own reader, you at this lane, and the
// orchestrator that wrote its task. The source stands on its own where the
// count is zero — that is what a redirect the child has already taken up
// looks like, and without it nothing would say anyone had spoken to it.
func TestFanoutLaneSaysWhoSteeredTheChild(t *testing.T) {
	read := FanoutLane{State: FanoutRunning, Name: "writer-1", Task: "docs/loop.md",
		Tools: 12, Steers: 2, SteerFrom: "reading", Verdict: "off target"}
	if view := ansi.Strip(read.View(110)); !strings.Contains(view, "2 steers · from reading · last read off target") {
		t.Fatalf("the lane should name the source beside the count: %q", view)
	}
	redirected := FanoutLane{State: FanoutRunning, Name: "writer-1", Tools: 12,
		SteerFrom: "parent", Verdict: "off target", Seeded: 5}
	view := ansi.Strip(redirected.View(110))
	if !strings.Contains(view, "steered from parent") {
		t.Fatalf("a redirect the child has taken up still says who sent it: %q", view)
	}
	if strings.Contains(view, "started from") {
		t.Fatalf("news outranks the seed line: %q", view)
	}
	lane := FanoutLane{State: FanoutRunning, Name: "writer-1", Tools: 3, SteerFrom: "lane"}
	if view := ansi.Strip(lane.View(110)); !strings.Contains(view, "steered from lane") {
		t.Fatalf("your own steer is named too: %q", view)
	}
}

// TestFanoutLaneSaysWhatItStartedFrom is the seeded-worktree criterion on a
// lane: a writer working from your uncommitted files says how many, while it
// is working and there is nothing else under the lane to say. A lane that has
// stopped, or one waiting on you, has something better to put there, and a
// child that started from the last commit has nothing to explain.
func TestFanoutLaneSaysWhatItStartedFrom(t *testing.T) {
	running := FanoutLane{State: FanoutRunning, Name: "writer-1", Task: "docs/loop.md", Seeded: 5}
	if view := ansi.Strip(running.View(110)); !strings.Contains(view, "started from 5 uncommitted files in your tree") {
		t.Fatalf("a seeded lane should say what it started from: %q", view)
	}
	one := FanoutLane{State: FanoutRunning, Name: "writer-1", Seeded: 1}
	if view := ansi.Strip(one.View(110)); !strings.Contains(view, "started from 1 uncommitted file in your tree") {
		t.Fatalf("one file is one file: %q", view)
	}

	none := FanoutLane{State: FanoutRunning, Name: "writer-1", Task: "docs/loop.md"}
	if view := ansi.Strip(none.View(110)); strings.Contains(view, "started from") {
		t.Fatalf("a child started from the last commit should say nothing: %q", view)
	}
	done := FanoutLane{State: FanoutDone, Name: "writer-1", Seeded: 5, Summary: "documented the sentinel"}
	if view := ansi.Strip(done.View(110)); !strings.Contains(view, "documented the sentinel") || strings.Contains(view, "started from") {
		t.Fatalf("a finished lane should keep its result and drop the seed line: %q", view)
	}
	blocked := FanoutLane{State: FanoutBlocked, Name: "writer-1", Seeded: 5, Waiting: "waiting approval: apply patch"}
	if view := ansi.Strip(blocked.View(110)); !strings.Contains(view, "waiting approval") || strings.Contains(view, "started from") {
		t.Fatalf("a blocked lane should say what it needs and nothing else: %q", view)
	}
}

// TestFanoutLaneDrawsADescendantUnderItsParent: a lane a child spawned is
// drawn behind the corner, in the gutter the pointer column and the mutation
// rail leave a lane — the same corner hard against the same glyph as the
// rail's map draws for the same child — and a lane the session spawned keeps
// that gutter blank.
func TestFanoutLaneDrawsADescendantUnderItsParent(t *testing.T) {
	child := FanoutLane{State: FanoutRunning, Name: "writer-1", Depth: 1}
	if line := ansi.Strip(child.View(110)); !strings.HasPrefix(line, "   ◇ agent") {
		t.Fatalf("a child of the session should keep the gutter blank: %q", line)
	}
	grandchild := FanoutLane{State: FanoutRunning, Name: "reviewer-1a", Depth: 2}
	if line := ansi.Strip(grandchild.View(110)); !strings.HasPrefix(line, "  └◇ agent") {
		t.Fatalf("a lane a child spawned should draw behind the corner: %q", line)
	}
	// The columns past the gutter are the grid's, so the nesting costs the
	// lane nothing: the verb, the name and the duration land where a lane the
	// session spawned lands them.
	if a, b := lipgloss.Width(child.View(110)), lipgloss.Width(grandchild.View(110)); a != b {
		t.Fatalf("nesting moved the grid: %d columns against %d", b, a)
	}
}

// TestFanoutLaneSaysHowManyAreUnderIt: a parent's lane states its live
// descendants while it has any, ahead of the seed line and behind a steer —
// what the child is doing now over where its files came from — and says
// nothing at all where it delegated nothing.
func TestFanoutLaneSaysHowManyAreUnderIt(t *testing.T) {
	two := FanoutLane{State: FanoutRunning, Name: "writer-1", Under: 2, Seeded: 5}
	if view := ansi.Strip(two.View(110)); !strings.Contains(view, "2 agents under it") {
		t.Fatalf("a delegating lane should say how many are under it: %q", view)
	}
	one := FanoutLane{State: FanoutRunning, Name: "writer-1", Under: 1}
	if view := ansi.Strip(one.View(110)); !strings.Contains(view, "1 agent under it") {
		t.Fatalf("one agent is one agent: %q", view)
	}
	steered := FanoutLane{State: FanoutRunning, Name: "writer-1", Under: 2, Steers: 2}
	if view := ansi.Strip(steered.View(110)); !strings.Contains(view, "2 steers") ||
		strings.Contains(view, "under it") {
		t.Fatalf("a steer is news and outranks the count: %q", view)
	}
	none := FanoutLane{State: FanoutRunning, Name: "writer-1", Seeded: 5}
	if view := ansi.Strip(none.View(110)); strings.Contains(view, "under it") {
		t.Fatalf("a lane that delegated nothing should say nothing: %q", view)
	}
	done := FanoutLane{State: FanoutDone, Name: "writer-1", Under: 2, Summary: "documented the sentinel"}
	if view := ansi.Strip(done.View(110)); !strings.Contains(view, "documented the sentinel") ||
		strings.Contains(view, "under it") {
		t.Fatalf("a finished lane keeps its result: %q", view)
	}
}

// TestFanoutBlockedFloatsTheWholeGroup: a request under a nested lane floats
// its parent with it, so the corner it is drawn behind always has the row it
// hangs off directly above it.
func TestFanoutBlockedFloatsTheWholeGroup(t *testing.T) {
	block := FanoutBlock{Lanes: []FanoutLane{
		{State: FanoutDone, Name: "reader-2", Depth: 1, Summary: "surveyed"},
		{State: FanoutRunning, Name: "writer-1", Depth: 1, Under: 1},
		{State: FanoutBlocked, Name: "reviewer-1a", Depth: 2, Waiting: "waiting approval: read loop.go"},
	}}
	var order []string
	for _, l := range block.sorted() {
		order = append(order, l.Name)
	}
	if want := "writer-1,reviewer-1a,reader-2"; strings.Join(order, ",") != want {
		t.Fatalf("the blocked group should float whole: got %v, want %s", order, want)
	}
}

// A settled lane folds the child's own report under it, in the transcript's
// own fold grammar and behind the transcript's own key: what the person acts
// on is the child's words, not the first line of them.
func TestFanoutSettledLaneFoldsItsReport(t *testing.T) {
	report := []string{"Counted the rounds.", "", "The counter is read once."}
	shut := FanoutLane{State: FanoutDone, Name: "reader-1", Summary: "Counted the rounds.",
		Report: report}
	view := ansi.Strip(shut.View(110))
	if !strings.Contains(view, "▸ report · 3 lines · [enter] expand") {
		t.Fatalf("a settled lane should offer its report as a fold: %q", view)
	}
	if strings.Contains(view, "The counter is read once.") {
		t.Fatalf("a shut fold should hold the report back: %q", view)
	}

	open := shut
	open.ReportOpen = true
	oview := ansi.Strip(open.View(110))
	if !strings.Contains(oview, "▾ report · 3 lines") {
		t.Fatalf("an opened fold should say it is open: %q", oview)
	}
	if !strings.Contains(oview, "The counter is read once.") {
		t.Fatalf("an opened fold should show the report: %q", oview)
	}

	// The bound is a fold like any other, so it counts what it swallowed.
	long := shut
	long.Report = []string{"one", "two", "three", "four"}
	long.ReportOpen, long.MaxReport = true, 2
	lview := ansi.Strip(long.View(110))
	if !strings.Contains(lview, "… 2 more lines") {
		t.Fatalf("a bounded report should count what it held back: %q", lview)
	}
	if strings.Contains(lview, "three") {
		t.Fatalf("a bounded report should stop at its bound: %q", lview)
	}
}

// Only a lane that has stopped has a report, and only a lane with one draws
// the fold: a running child's words are not a report yet.
func TestFanoutRunningLaneHasNoReportFold(t *testing.T) {
	for _, lane := range []FanoutLane{
		{State: FanoutRunning, Name: "a", Report: []string{"half a thought"}},
		{State: FanoutBlocked, Name: "a", Report: []string{"half a thought"}},
		{State: FanoutDone, Name: "a"},
	} {
		if view := ansi.Strip(lane.View(110)); strings.Contains(view, "report ·") {
			t.Fatalf("no fold belongs on this lane: %q", view)
		}
	}
}

// The assumptions a child stated instead of asking are counted on the lane's
// own detail line, beside the first line of what it reported. Zero states
// nothing: a child that assumed nothing and one that never wrote the section
// are the same lane.
func TestFanoutSettledLaneCountsTheAssumptions(t *testing.T) {
	lane := FanoutLane{State: FanoutDone, Name: "reader-1",
		Summary: "Counted the rounds.", Assumptions: 2}
	view := ansi.Strip(lane.View(110))
	if !strings.Contains(view, "Counted the rounds. · 2 assumptions") {
		t.Fatalf("the detail line should carry the count: %q", view)
	}

	none := lane
	none.Assumptions = 0
	if view := ansi.Strip(none.View(110)); strings.Contains(view, "assumption") {
		t.Fatalf("no assumptions is no field: %q", view)
	}

	bare := FanoutLane{State: FanoutDone, Name: "reader-1", Assumptions: 1}
	if view := ansi.Strip(bare.View(110)); !strings.Contains(view, "1 assumption") {
		t.Fatalf("a report with nothing else to say still counts them: %q", view)
	}
}

// A review's verdict stands beside the state in the outcome field, on the
// lane and on the manager row alike — the two draw a child through one
// renderer, and they differ in the glyph column and nowhere else.
func TestFanoutLaneCarriesTheReviewVerdict(t *testing.T) {
	lane := FanoutLane{State: FanoutDone, Name: "reviewer-1", Task: "the round change",
		ReportVerdict: "approve with changes"}
	if view := ansi.Strip(lane.View(110)); !strings.Contains(view, "✓ done · approve with changes") {
		t.Fatalf("the verdict belongs beside the state: %q", view)
	}

	metered := lane
	metered.Step, metered.Steps = 5, 5
	if view := ansi.Strip(metered.View(110)); !strings.Contains(view, "✓ 5/5 · approve with changes") {
		t.Fatalf("a review that declared a count keeps both: %q", view)
	}

	progress := lane.progressOf()
	row := AgentRow{State: AgentDone, Name: "reviewer-1", Progress: &progress}
	rendered := ansi.Strip(strings.Join(row.render(110, false), "\n"))
	if !strings.Contains(rendered, "approve with changes") {
		t.Fatalf("the manager row reads the same verdict: %q", rendered)
	}

	// What gives way as the pane narrows, and in what order: the cost first,
	// then the verdict, and never the name the lane is read by.
	costly := lane
	costly.Tools, costly.Spend = 9, "$0.03"
	tight := ansi.Strip(costly.View(80))
	if !strings.Contains(tight, "approve with changes") {
		t.Fatalf("the cost should give way before the verdict: %q", tight)
	}
	if strings.Contains(tight, "9 tools") {
		t.Fatalf("both the cost and the verdict cannot fit here: %q", tight)
	}
	narrow := ansi.Strip(costly.View(60))
	if strings.Contains(narrow, "approve with changes") {
		t.Fatalf("the verdict should give way before the name does: %q", narrow)
	}
	if !strings.Contains(narrow, "reviewer-1") {
		t.Fatalf("the lane lost the name it is read by: %q", narrow)
	}

	// A child still going has nothing to conclude, and a report with no
	// verdict on its last line leaves `done` standing alone.
	live := lane
	live.State = FanoutRunning
	if view := ansi.Strip(live.View(110)); strings.Contains(view, "approve with changes") {
		t.Fatalf("a running lane has no verdict to state: %q", view)
	}
	silent := lane
	silent.ReportVerdict = ""
	if view := ansi.Strip(silent.View(110)); !strings.Contains(view, "✓ done") {
		t.Fatalf("a report with no verdict draws done alone: %q", view)
	}
}
