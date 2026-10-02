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

// cardHeader is the fan-out card's header line: the one under its padding
// row, or the whole of a folded card.
func cardHeader(view string) string {
	lines := plainLines(view)
	if len(lines) == 1 {
		return lines[0]
	}
	return lines[1]
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
	// Five children, five rows in the card's footer, under the header and
	// the notes under them.
	lanes := 0
	for _, line := range plainLines(view) {
		if strings.HasPrefix(line, "    ◇ ") {
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
	if len(lines) < 3 {
		t.Fatalf("block too short:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[2], "scout-3") {
		t.Fatalf("the blocked lane is not first: %q", lines[2])
	}
	if !strings.Contains(lines[2], "blocked") {
		t.Fatalf("the blocked lane does not say what it needs: %q", lines[2])
	}
	if !strings.Contains(lines[2], "waiting approval") {
		t.Fatalf("the blocked lane does not say what it is waiting for: %q", lines[2])
	}
	// The header leaves the ask to the lane: the lane is what says it needs
	// you (docs/interface/departures.md#the-childrens-tally-says-who-needs-you-first).
	if !strings.Contains(lines[1], "in parallel") || strings.Contains(lines[1], "needs you") {
		t.Fatalf("the header should say the children work together and leave the ask to the lane: %q", lines[1])
	}
}

// TestFanoutOffersOnlyWhileBlocked keeps the manager offer honest: it is the
// answer to a blocked child, so it is not on a block where nobody is waiting.
func TestFanoutOffersOnlyWhileBlocked(t *testing.T) {
	blocked := fanoutFixture()
	if !strings.Contains(ansi.Strip(blocked.View(110)), "[ctrl+a] agents") {
		t.Fatal("a blocked block should offer the manager")
	}
	// The offer is on the row of the lane that needs you and on no other:
	// it is the one row in the card with a key. The answer itself is the
	// routed card's, so no key here offers it
	// (docs/interface/departures.md#a-fan-out-offers-the-manager-not-the-answer).
	lines := strings.Split(ansi.Strip(blocked.View(110)), "\n")
	keyed := 0
	for _, l := range lines {
		if strings.Contains(l, "[ctrl+a] agents") {
			keyed++
			if !strings.Contains(l, "scout-3") {
				t.Fatalf("the offer is off the blocked lane's row: %q", l)
			}
		}
	}
	if keyed != 1 {
		t.Fatalf("the offer should be on one row, it is on %d:\n%s", keyed, strings.Join(lines, "\n"))
	}
	if strings.Contains(strings.Join(lines, "\n"), "answer it here") {
		t.Fatal("the block offered the answer the routed card owns")
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
// only where the spawn declared a step count, a still word everywhere else,
// and never a ratio nobody supplied.
func TestFanoutProgressNeedsADeclaredCount(t *testing.T) {
	declared := FanoutLane{State: FanoutRunning, Name: "writer-1", Step: 2, Steps: 5}
	bar := ansi.Strip(declared.View(110))
	if !strings.Contains(bar, "▰") || !strings.Contains(bar, "2/5") {
		t.Fatalf("a declared step count should draw its bar and its number: %q", bar)
	}

	none := FanoutLane{State: FanoutRunning, Name: "writer-1", Writes: true}
	still := ansi.Strip(none.View(110))
	if strings.Contains(still, "▰") || strings.Contains(still, "▱") {
		t.Fatalf("a lane with no declared count drew a bar: %q", still)
	}
	if strings.ContainsAny(still, strings.Join(SpinnerFrames, "")) || !strings.Contains(still, "writing") {
		t.Fatalf("a lane with no declared count should say it is writing, still: %q", still)
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
// docs/interface/surfaces.md#the-agent-manager states: the lane's glyph column says what the child is in every state, and
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

// A lane's name stands in a slot of its own, so what each child is doing
// starts in one column down the card's footer.
func TestFanoutLaneSetsItsNameInASlot(t *testing.T) {
	for _, name := range []string{"writer-1", "researcher-12"} {
		lane := FanoutLane{State: FanoutRunning, Name: name, Task: "docs/loop.md"}
		view := ansi.Strip(lane.View(110))
		at := strings.Index(view, "docs/loop.md") - strings.Index(view, name)
		if want := max(laneNameSlot, len(name)+1); at != want {
			t.Fatalf("%s: the task starts %d columns after the name, want %d: %q", name, at, want, view)
		}
	}
}

// The card's header is a step card's: its glyph in the glyph column, the
// spawn's verb where every card's verb starts
// (docs/interface/surfaces.md#the-leading-columns).
func TestFanoutHeaderIsACardHeader(t *testing.T) {
	head := cardHeader(fanoutFixture().View(110))
	if !strings.HasPrefix(head, "  ◇ spawned 5 agents") {
		t.Fatalf("the header should lead with the glyph and the spawn's verb: %q", head)
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

// TestFanout_TheTallyCountsEachChildOnce: at low the header is the whole
// card, so it counts every child in the one state it is in — a child waiting
// on you is not also running, and the finished are said beside the live
// (docs/interface/surfaces.md#the-agent-manager).
func TestFanout_TheTallyCountsEachChildOnce(t *testing.T) {
	three := []FanoutLane{
		{State: FanoutDone, Name: "reader-1", Task: "survey internal/ui", Elapsed: "21s"},
		{State: FanoutRunning, Name: "writer-2", Task: "docs/loop.md", Elapsed: "37s"},
		{State: FanoutBlocked, Name: "scout-3", Task: "other callers", Elapsed: "18s",
			Waiting: "waiting approval: read ../plugins/registry.go"},
	}
	tests := []struct {
		name  string
		lanes []FanoutLane
		want  string
	}{
		{"one done, one running, one blocked", three, "1 done · 1 needs you · 1 running"},
		{"a failed child beside a blocked one", []FanoutLane{
			{State: FanoutFailed, Name: "patcher-1", Elapsed: "9s"},
			{State: FanoutBlocked, Name: "scout-2", Elapsed: "8s"},
		}, "1 failed · 1 needs you"},
		{"a slot wait beside a hold", []FanoutLane{
			{State: FanoutBlocked, Name: "scout-1", Elapsed: "9s"},
			{State: FanoutHeld, Name: "writer-2", Elapsed: "8s"},
			{State: FanoutHeld, Name: "writer-3", Elapsed: "7s", SlotWait: 2},
			{State: FanoutRunning, Name: "writer-4", Elapsed: "6s"},
		}, "1 needs you · 1 held · 1 waiting · 1 running"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := FanoutBlock{Elapsed: "37s", Low: true, Lanes: tt.lanes}
			if got := ansi.Strip(b.headerOutcome()); got != tt.want {
				t.Fatalf("the low header's tally = %q, want %q", got, tt.want)
			}
		})
	}
	// The header's two sides, the card's gap between them.
	head := cardHeader(FanoutBlock{Elapsed: "37s", Low: true, Lanes: three}.View(110))
	left, right, _ := strings.Cut(strings.TrimSpace(head), "  ")
	if left != "◇ spawned 3 agents" || strings.TrimSpace(right) != "1 done · 1 needs you · 1 running · 37s" {
		t.Fatalf("the low header reads %q", head)
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
	header := cardHeader(settled.View(110))
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
	header := cardHeader(block.View(110))
	if !strings.Contains(header, "2 held · 1 running") {
		t.Fatalf("the header should count the parks beside what is still going: %q", header)
	}
	// A child that asks for an answer while the parks land is a live child,
	// and the header counts it with the running: the ask is said on its lane.
	block.Lanes = append(block.Lanes, FanoutLane{State: FanoutBlocked, Name: "d"})
	header = cardHeader(block.View(110))
	if !strings.Contains(header, "2 held · 2 running") || strings.Contains(header, "needs you") {
		t.Fatalf("the header should count the parks beside the live children: %q", header)
	}
}

// A child parked in front of its own check is waiting for a check slot, not
// held: nobody parked it, so the header counts it apart from a park.
func TestFanoutHeaderCountsASlotWaitAsWaiting(t *testing.T) {
	block := FanoutBlock{Lanes: []FanoutLane{
		{State: FanoutHeld, Name: "a", SlotWait: 2}, {State: FanoutHeld, Name: "b", SlotWait: 2},
		{State: FanoutRunning, Name: "c"},
	}}
	header := cardHeader(block.View(110))
	if !strings.Contains(header, "2 waiting · 1 running") || strings.Contains(header, "held") {
		t.Fatalf("a slot wait should be counted as waiting, not held: %q", header)
	}
	block.Lanes[2] = FanoutLane{State: FanoutHeld, Name: "c"}
	header = cardHeader(block.View(110))
	if !strings.Contains(header, "1 held · 2 waiting") {
		t.Fatalf("a park and a slot wait should be counted apart, the park first: %q", header)
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

// A count with more than one author splits, and one with a single author does
// not. `2 steers · from reading` over one steer of yours and one of the
// check's is the reading this exists to stop: the total says the child has
// been redirected twice and the source says the check did it, and neither is
// the reader's own steer. Mixed, the total leads and the shares that belong to
// somebody are named; the check's is the remainder and stays unnamed.
func TestFanoutLaneSplitsAMixedSteerCount(t *testing.T) {
	mixed := FanoutLane{State: FanoutRunning, Name: "writer-1", Task: "docs/loop.md",
		Tools: 12, Steers: 1, Yours: 1, SteerFrom: "lane", Verdict: "off target"}
	if view := ansi.Strip(mixed.View(110)); !strings.Contains(view, "steered ×2 · 1 yours · last read off target") {
		t.Fatalf("a mixed count should say how many were yours: %q", view)
	}
	both := FanoutLane{State: FanoutRunning, Name: "writer-1", Tools: 12,
		Steers: 1, Yours: 2, FromParent: 1, SteerFrom: "parent"}
	if view := ansi.Strip(both.View(110)); !strings.Contains(view, "steered ×4 · 2 yours · 1 parent") {
		t.Fatalf("every author with a share is named: %q", view)
	}
	// One author is one clause, whichever author it is: a lane that split a
	// count nobody else contributed to would spend a line saying the same
	// thing twice.
	yours := FanoutLane{State: FanoutRunning, Name: "writer-1", Tools: 3, Yours: 2, SteerFrom: "lane"}
	if view := ansi.Strip(yours.View(110)); !strings.Contains(view, "2 steers · from lane") ||
		strings.Contains(view, "yours") {
		t.Fatalf("one author keeps today's clause: %q", view)
	}
	// And your steers count towards the total even where the clause does not
	// split: the count the lane states is every steer this turn, not the
	// check's share of them.
	one := FanoutLane{State: FanoutRunning, Name: "writer-1", Tools: 3, Yours: 1, SteerFrom: "lane"}
	if view := ansi.Strip(one.View(110)); !strings.Contains(view, "1 steer · from lane") {
		t.Fatalf("your own steer is counted: %q", view)
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

	// And how often the tree has been moved under it since, beside where it
	// started; a reseeding lane says so where a held one says held.
	moved := FanoutLane{State: FanoutRunning, Name: "writer-1", Seeded: 5, Reseeds: 2}
	if view := ansi.Strip(moved.View(110)); !strings.Contains(view, "started from 5 uncommitted files in your tree · reseeded ×2") {
		t.Fatalf("a reseeded lane should count its reseeds beside its seed: %q", view)
	}
	parked := FanoutLane{State: FanoutHeld, Reseeding: true, Name: "writer-1"}
	if view := ansi.Strip(parked.View(110)); !strings.Contains(view, "⏸ reseeding") {
		t.Fatalf("a lane parked for a reseed should say reseeding: %q", view)
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
	if line := ansi.Strip(child.View(110)); !strings.HasPrefix(line, "    ◇ writer-1") {
		t.Fatalf("a child of the session should keep the gutter blank: %q", line)
	}
	grandchild := FanoutLane{State: FanoutRunning, Name: "reviewer-1a", Depth: 2}
	if line := ansi.Strip(grandchild.View(110)); !strings.HasPrefix(line, "   └◇ reviewer-1a") {
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
// own fold grammar, printing no key — the hint bar names enter: what the
// person acts on is the child's words, not the first line of them.
func TestFanoutSettledLaneFoldsItsReport(t *testing.T) {
	report := []string{"Counted the rounds.", "", "The counter is read once."}
	shut := FanoutLane{State: FanoutDone, Name: "reader-1", Summary: "Counted the rounds.",
		Report: report}
	view := ansi.Strip(shut.View(110))
	if !strings.Contains(view, "▸ report · 3 lines") || strings.Contains(view, "[enter]") {
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
	if !strings.Contains(lview, "… 2 more lines, [enter] opens the whole of it") {
		t.Fatalf("a bounded report should count what it held back and offer the rest: %q", lview)
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
	if strings.Contains(tight, "$0.03") {
		t.Fatalf("the spend cannot fit beside the verdict here: %q", tight)
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

// A child asked a follow-up answered twice, and its lane says so: the answer
// it gave first is folded above the one that replaced it, headed with the
// turn it closed and counted, and the reader opens the current one.
func TestFanoutLaneFoldsTheReportAFollowUpReplaced(t *testing.T) {
	b := fanoutFixture()
	b.Lanes = []FanoutLane{{State: FanoutDone, Name: "reader-1", Task: "survey internal/ui",
		Tools: 9, Elapsed: "41s", Summary: "the rail is one component",
		Earlier: []LaneReport{{Turn: 1, Lines: []string{"the frame draws the rail", "and the pane"}}},
		Report:  []string{"the rail is one component", "inspector.go draws it"}, ReportOpen: true}}
	text := strings.Join(plainLines(b.View(100)), "\n")
	earlier := strings.Index(text, "▸ turn 1 report · 2 lines")
	current := strings.Index(text, "▾ report · 2 lines")
	if earlier < 0 || current < 0 || earlier > current {
		t.Fatalf("the replaced report should be folded above the current one:\n%s", text)
	}
	if strings.Contains(text, "the frame draws the rail") {
		t.Fatalf("the replaced report should stay folded:\n%s", text)
	}
}

// A failed lane is the parent transcript's account of the child, so it names
// the handoff's handle in full after the reason.
func TestFanoutFailedLaneNamesItsHandoff(t *testing.T) {
	lane := FanoutLane{State: FanoutFailed, Name: "patcher-3", Summary: "round limit (25) reached",
		Handoff: "handoff-130ebba38aed7"}
	if got := lane.note(); got != "round limit (25) reached · handoff handoff-130ebba38aed7" {
		t.Fatalf("the failed lane's note = %q", got)
	}
	lane.Handoff = ""
	if got := lane.note(); got != "round limit (25) reached" {
		t.Fatalf("a lane with no handoff says the reason alone: %q", got)
	}
}

// TestFanoutCountsAreDimAndTheStateIsTheGlyph: a lane's state is carried by
// its glyph and by the verdict after its bar, and every count around them is
// dim — the running count on the header, the step count on the lane and the
// rail's map, and the count beside a running bar
// (docs/interface/departures.md#the-childrens-tally-says-who-needs-you-first).
func TestFanoutCountsAreDimAndTheStateIsTheGlyph(t *testing.T) {
	block := FanoutBlock{Lanes: []FanoutLane{
		{State: FanoutRunning, Name: "a", Step: 2, Steps: 5},
		{State: FanoutRunning, Name: "b", Step: 1, Steps: 3, Planned: true},
		{State: FanoutBlocked, Name: "c"},
		{State: FanoutDone, Name: "d", Step: 5, Steps: 5},
	}}
	view := block.View(110)
	for _, want := range []struct{ what, render string }{
		{"the header's word for children working together in dim", sty.Dim.Render("in parallel")},
		{"a running bar's count in dim", sty.Dim.Render("2/5")},
		{"a planned lane's step count in dim", sty.Dim.Render("1 of 3 steps")},
		{"a running lane's glyph in info", sty.Info.Render("◇")},
		{"the ask in del", sty.Err.Render("blocked")},
		{"a finished lane's glyph in add", sty.Add.Render("◇")},
	} {
		if !strings.Contains(view, want.render) {
			t.Errorf("want %s:\n%s", want.what, view)
		}
	}
	rail := InspectorRail{Agents: []InspectorAgent{
		{Name: "orchestrator", Self: true, State: FanoutRunning},
		{Name: "b", State: FanoutRunning, Step: 1, Steps: 3, Planned: true},
	}}
	if got := rail.View(40, 30); !strings.Contains(got, sty.Dim.Render("1 of 3 steps")) ||
		!strings.Contains(got, sty.Dim.Render("1 running")) {
		t.Errorf("the rail's map should draw its counts in dim:\n%s", got)
	}
}

// A running lane that names its step gives up its costs before its task, and
// its task before its step: the token count, then the budget's share, then
// the tool count, then the task clips, and the step is the last thing kept.
func TestFanoutLaneKeepsItsStepLast(t *testing.T) {
	lane := FanoutLane{State: FanoutRunning, Name: "writer-1",
		Task: "Find every place a cache entry is written and read", Step: 1, Steps: 3,
		Planned: true, StepTitle: "Run gofmt", BudgetPct: 2, Tools: 2,
		Spend: "~26.2k tok", Elapsed: "41s"}
	cases := []struct {
		width      int
		kept, gone []string
	}{
		{200, []string{"written and read", "2% of budget", "2 tools", "~26.2k tok", "Run gofmt"}, nil},
		{130, []string{"written and read", "2% of budget", "2 tools", "Run gofmt"}, []string{"~26.2k tok"}},
		{115, []string{"written and read", "2 tools", "Run gofmt"}, []string{"~26.2k tok", "of budget"}},
		{100, []string{"writer-1  Find every", "1 of 3", "Run gofmt"}, []string{"~26.2k tok", "of budget", "tools"}},
		{80, []string{"writer-1  Find", "… · Run gofmt"}, []string{"~26.2k tok", "of budget", "tools"}},
	}
	for _, c := range cases {
		view := ansi.Strip(lane.View(c.width))
		for _, k := range c.kept {
			if !strings.Contains(view, k) {
				t.Errorf("at %d the lane lost %q: %q", c.width, k, view)
			}
		}
		for _, g := range c.gone {
			if strings.Contains(view, g) {
				t.Errorf("at %d %q should have given way: %q", c.width, g, view)
			}
		}
	}
}

// A finished lane gives way in the running lane's order: the token count,
// then the budget's share, then the tool count, and only then the task clips —
// so a settled lane beside a running one keeps its words at the same width.
func TestFanoutSettledLaneKeepsItsTaskBeforeItsCosts(t *testing.T) {
	lane := FanoutLane{State: FanoutDone, Name: "writer-2",
		Task: "Decide what a cache entry's lifetime should be", BudgetPct: 3, Tools: 2,
		Spend: "~39.3k tok", Elapsed: "1m12s"}
	cases := []struct {
		width      int
		kept, gone []string
	}{
		{200, []string{"lifetime should be", "3% of budget", "2 tools", "~39.3k tok"}, nil},
		{110, []string{"lifetime should be", "✓ done"}, []string{"~39.3k tok"}},
		{80, []string{"writer-2  Decide", "✓ done"}, []string{"~39.3k tok", "of budget", "tools"}},
	}
	for _, c := range cases {
		view := ansi.Strip(lane.View(c.width))
		for _, k := range c.kept {
			if !strings.Contains(view, k) {
				t.Errorf("at %d the lane lost %q: %q", c.width, k, view)
			}
		}
		for _, g := range c.gone {
			if strings.Contains(view, g) {
				t.Errorf("at %d %q should have given way: %q", c.width, g, view)
			}
		}
	}
}

// TestFanout_ARunningLaneIsStatic: a child still working with no declared
// step count says so in a still word beside its kind glyph — `writing` for a
// child whose role changes files, `running` for any other — and never draws
// a spinner frame, because the frame's status is the one thing on screen
// that animates. A declared count keeps its meter, and a blocked child keeps
// its word and its mark.
func TestFanout_ARunningLaneIsStatic(t *testing.T) {
	row := func(l FanoutLane) string {
		return strings.Join(strings.Fields(ansi.Strip(strings.Split(l.View(110), "\n")[0])), " ")
	}
	for _, tc := range []struct {
		name string
		lane FanoutLane
		want string
	}{
		{"a writer", FanoutLane{State: FanoutRunning, Name: "writer-1", Task: "docs/loop.md",
			Writes: true, Elapsed: "41s"}, "◇ writer-1 docs/loop.md writing · 41s"},
		{"a reader", FanoutLane{State: FanoutRunning, Name: "reader-3", Task: "survey internal/ui",
			Tools: 2, Elapsed: "39s"}, "◇ reader-3 survey internal/ui running · 2 tools · 39s"},
		{"a declared count", FanoutLane{State: FanoutRunning, Name: "writer-2", Task: "internal/agent/round.go",
			Writes: true, Step: 2, Steps: 5, Elapsed: "1m10s"}, "◇ writer-2 internal/agent/round.go ▰▰▱▱▱ 2/5 · 1m10s"},
		{"a blocked child", FanoutLane{State: FanoutBlocked, Name: "writer-3", Waiting: "approve a write",
			Writes: true, Elapsed: "12s"}, ""},
	} {
		got := row(tc.lane)
		if tc.lane.State == FanoutBlocked {
			if !strings.Contains(got, "blocked") || !strings.Contains(got, "⚠ needs you") {
				t.Errorf("%s: a blocked lane says so in words: %q", tc.name, got)
			}
		} else if got != tc.want {
			t.Errorf("%s: the running lane is %q, want %q", tc.name, got, tc.want)
		}
		if strings.ContainsAny(got, "⠋⠙⠹⠸⠼⠴⠦⠧") {
			t.Errorf("%s: a lane draws a spinner frame: %q", tc.name, got)
		}
	}
	if raw := (FanoutLane{State: FanoutRunning, Name: "writer-1", Writes: true}).View(110); !strings.Contains(raw, sty.SpinText.Render("writing")) {
		t.Errorf("the running word is in the spin colour: %q", raw)
	}
	block := FanoutBlock{Elapsed: "41s", Body: "Fanning two out.", Lanes: []FanoutLane{
		{State: FanoutRunning, Name: "writer-1", Task: "docs/loop.md", Writes: true, Elapsed: "41s"},
		{State: FanoutRunning, Name: "reader-3", Task: "survey internal/ui", Elapsed: "39s"},
	}}
	if view := ansi.Strip(block.View(80)); strings.ContainsAny(view, "⠋⠙⠹⠸⠼⠴⠦⠧") {
		t.Errorf("the running fan-out card draws a spinner frame:\n%s", view)
	}
}
