package components

import (
	"strings"
	"testing"
)

// TestInspectorRail_AgentsMapFloatsBlockedChildrenUnderTheRoot: a child that
// wants something from you is the only row on this block that is a job rather
// than a reading, so it is drawn where the eye lands instead of wherever the
// fan-out happened to reach it. Everything else keeps spawn order, and so do
// the blocked rows among themselves.
func TestInspectorRail_AgentsMapFloatsBlockedChildrenUnderTheRoot(t *testing.T) {
	r := InspectorRail{
		Agents: []InspectorAgent{
			{Name: "orchestrator", Detail: "round 3", Self: true, State: FanoutRunning},
			{Name: "writer-1", Detail: "docs/loop.md", State: FanoutRunning},
			{Name: "reviewer-2", Detail: "waiting approval: apply patch", State: FanoutBlocked},
			{Name: "runner-3", Detail: "the package tests", State: FanoutRunning},
			{Name: "reviewer-4", Detail: "waiting approval: delete a file", State: FanoutBlocked},
		},
		Frame: 2,
	}
	view := stripANSI(r.View(InspectorMaxWidth, 0))
	at := func(name string) int { return strings.Index(view, name) }
	for _, pair := range [][2]string{
		{"orchestrator", "reviewer-2"}, {"reviewer-2", "reviewer-4"},
		{"reviewer-4", "writer-1"}, {"writer-1", "runner-3"},
	} {
		if at(pair[0]) > at(pair[1]) {
			t.Fatalf("%s is drawn above %s:\n%s", pair[0], pair[1], view)
		}
	}
}

// TestInspectorRail_AgentsMapGivesUpItsRowsInOrder pins what a rail short of
// height takes off the map: the trailer first, then the sessions that have
// stopped oldest first, then one that never started, and a session still
// working last of all.
func TestInspectorRail_AgentsMapGivesUpItsRowsInOrder(t *testing.T) {
	r := InspectorRail{
		Agents: []InspectorAgent{
			{Name: "orchestrator", Detail: "round 3", Self: true, State: FanoutRunning},
			{Name: "writer-1", Detail: "wrote two files", Outcome: "done", State: FanoutDone},
			{Name: "writer-2", Detail: "its worktree was dirty", Outcome: "failed", State: FanoutFailed},
			{Name: "runner-3", Detail: "the package tests", State: FanoutRunning},
			{Name: "reader-4", Detail: "waiting for a slot", State: FanoutQueued},
		},
		AgentsHint: railAgentsHint,
		Frame:      2,
	}
	everything := stripANSI(r.View(InspectorMaxWidth, 0))
	full := len(r.Lines(InspectorMaxWidth, 0))
	// What each successive row of pressure costs, in the order the block can
	// afford to lose it. The fold marker takes a row of its own the moment
	// anything is behind it, so the map buys back less than it gives.
	gone := func(rows int) string {
		return stripANSI(strings.Join(r.Lines(InspectorMaxWidth, full-rows), "\n"))
	}
	// One row of pressure costs the trailer and nothing else. It is checked as
	// the whole block rather than as a name still being present, because the
	// failure this is here for takes a session's second line while leaving its
	// first: the row that says what the session is doing goes, the name stays,
	// and every containment check still passes.
	if v, want := gone(1), strings.Join(strings.Split(everything, "\n")[:len(strings.Split(everything, "\n"))-1], "\n"); v != want {
		t.Fatalf("one row of pressure is the trailer and nothing else:\ngot\n%s\nwant\n%s", v, want)
	}
	// And it goes rather than folding: a trailer is not a session, so nothing
	// is behind a marker yet.
	if v := gone(1); strings.Contains(v, "… ") {
		t.Fatalf("the trailer is shed, not folded behind a count:\n%s", v)
	}
	// Three rows of pressure: the trailer, then the oldest stopped session,
	// whose two rows are replaced by the marker counting it.
	v := gone(3)
	if strings.Contains(v, "writer-1") || !strings.Contains(v, "… 1 more") {
		t.Fatalf("the oldest settled session goes before the newer one:\n%s", v)
	}
	if !strings.Contains(v, "writer-2") {
		t.Fatalf("the newer outcome is still on screen:\n%s", v)
	}
	// Under enough pressure both settled sessions and the queued one are
	// gone, and the running one is still there: it is pinned, and a pinned
	// row is taken only once nothing else on the rail has one to give.
	v = gone(6)
	if strings.Contains(v, "writer-2") || strings.Contains(v, "reader-4") {
		t.Fatalf("settled and queued go before anything running:\n%s", v)
	}
	if !strings.Contains(v, "runner-3") || !strings.Contains(v, "orchestrator") {
		t.Fatalf("a running session is the last thing taken:\n%s", v)
	}
}

// TestNextToHide_TieGoesToTheLowerBlock: the rail reads downwards, from the
// turn out to the session, so two blocks with the same number of rows are a
// tie the one nearer the top wins. Without it the turn's own block hands over
// a row while a block of session history keeps all of its.
func TestNextToHide_TieGoesToTheLowerBlock(t *testing.T) {
	upper := railBlock{heading: "THIS TURN"}
	upper.add("step 2")
	upper.add("6 tools")
	lower := railBlock{heading: "CHANGES"}
	lower.add("loop.go")
	lower.add("round.go")
	block, row, ok := nextToHide([]railBlock{upper, lower})
	if !ok || block != 1 || row != 1 {
		t.Fatalf("the lower of two equal blocks gives the row: block %d row %d ok %v", block, row, ok)
	}
	// One row longer and length decides again rather than position.
	upper.add("3 files")
	if block, _, _ := nextToHide([]railBlock{upper, lower}); block != 0 {
		t.Fatalf("the longer block still gives first, got block %d", block)
	}
}

// TestNextToHide_GiveBeatsThePositionOnTheRow: a block that says which of its
// rows it can afford to lose is answered in that order, and a block that says
// nothing loses its bottom row.
func TestNextToHide_GiveBeatsThePositionOnTheRow(t *testing.T) {
	silent := railBlock{heading: "CHANGES"}
	silent.add("loop.go")
	silent.add("round.go")
	if _, row, _ := nextToHide([]railBlock{silent}); row != 1 {
		t.Fatalf("a block that numbers nothing loses its bottom row, got %d", row)
	}
	ordered := railBlock{heading: "AGENTS"}
	ordered.rows = []railLine{
		{text: "writer-1", give: 1},
		{text: "writer-2", give: 3},
		{text: "writer-3", give: 2},
	}
	if _, row, _ := nextToHide([]railBlock{ordered}); row != 0 {
		t.Fatalf("the lowest-numbered row goes first, got %d", row)
	}
}

// TestInspectorRail_AgentsTrailerNamesTheKeys: the map ends on how to reach
// what it draws, and only where there is something to reach.
func TestInspectorRail_AgentsTrailerNamesTheKeys(t *testing.T) {
	r := InspectorRail{
		Agents: []InspectorAgent{
			{Name: "orchestrator", Detail: "round 3", Self: true, State: FanoutRunning},
			{Name: "writer-1", Detail: "docs/loop.md", State: FanoutRunning},
		},
		AgentsHint: railAgentsHint,
		Frame:      2,
	}
	// The trailer is drawn whole at the narrowest rail there is rather than
	// clipping: it is a row of keys, and a clipped key is one nobody can
	// press.
	whole := false
	for _, line := range r.Lines(InspectorWidth, 0) {
		if strings.Contains(stripANSI(line), railAgentsHint) {
			whole = true
		}
	}
	if !whole {
		t.Fatalf("the trailer is drawn whole at %d columns:\n%s",
			InspectorWidth, stripANSI(r.View(InspectorWidth, 0)))
	}
	// A run with no children is not a map, and a hint over one row would be
	// naming sessions that do not exist.
	r.Agents = r.Agents[:1]
	if view := stripANSI(r.View(InspectorWidth, 0)); strings.Contains(view, railAgentsHint) {
		t.Fatalf("the trailer needs a session to reach:\n%s", view)
	}
}

// TestInspectorRail_AgentLaneDrawsTheBudgetPastHalf: the one denominator
// nobody has to declare. Under half its budget a child with no step count
// spins; past it the lane is the budget, and the bar takes the spinner's
// place rather than standing beside it.
func TestInspectorRail_AgentLaneDrawsTheBudgetPastHalf(t *testing.T) {
	r := InspectorRail{
		Agents: []InspectorAgent{
			{Name: "writer-1", Detail: "docs/loop.md", Tools: 4, State: FanoutRunning,
				Fresh: 120_000, Budget: 300_000},
		},
		Frame: 2,
	}
	view := stripANSI(r.View(InspectorMaxWidth, 0))
	if strings.Contains(view, "▰") || !strings.Contains(view, "⠹ docs/loop.md") {
		t.Fatalf("under half its budget the lane still spins:\n%s", view)
	}
	// Exactly half is the first reading that draws: half spent is half left,
	// which is the point the distance to the ceiling becomes a decision.
	r.Agents[0].Fresh = 150_000
	if view := stripANSI(r.View(InspectorMaxWidth, 0)); !strings.Contains(view, "150k of 300k") {
		t.Fatalf("half its budget is where the bar starts:\n%s", view)
	}
	r.Agents[0].Fresh = 210_000
	view = stripANSI(r.View(InspectorMaxWidth, 0))
	if !strings.Contains(view, "▱ 210k of 300k · docs/loop.md · 4 tools") {
		t.Fatalf("past half the lane draws the budget:\n%s", view)
	}
	if strings.Contains(view, "⠹") {
		t.Fatalf("a lane with a bar does not also spin:\n%s", view)
	}
	// A declared step count still wins: it is what the child said it was
	// doing, and the budget is what somebody set around it.
	r.Agents[0].Step, r.Agents[0].Steps = 2, 4
	if view := stripANSI(r.View(InspectorMaxWidth, 0)); !strings.Contains(view, "step 2 of 4") {
		t.Fatalf("a declared step count is still the lane:\n%s", view)
	}
	// Nothing bounded the child: there is no denominator, so there is no bar.
	r.Agents[0].Step, r.Agents[0].Steps, r.Agents[0].Budget = 0, 0, 0
	if view := stripANSI(r.View(InspectorMaxWidth, 0)); strings.Contains(view, "▰") {
		t.Fatalf("an unbounded child has no ceiling to draw:\n%s", view)
	}
}

// TestInspectorRail_AgentsMapWarnsFromTheSecondSteer: one steer is the
// machinery working, so the row says nothing; two is an interruption
// delivered, answered, and the next reading finding the same departure.
func TestInspectorRail_AgentsMapWarnsFromTheSecondSteer(t *testing.T) {
	r := InspectorRail{
		Agents: []InspectorAgent{
			{Name: "runner-2", Detail: "the package tests", Tools: 12,
				State: FanoutRunning, Steers: 1},
		},
		Frame: 2,
	}
	if view := stripANSI(r.View(InspectorMaxWidth, 0)); strings.Contains(view, "off task") {
		t.Fatalf("one steer is not news:\n%s", view)
	}
	r.Agents[0].Steers = 2
	if view := stripANSI(r.View(InspectorMaxWidth, 0)); !strings.Contains(view, "⚠ off task ×2") {
		t.Fatalf("the second steer is stated with its count:\n%s", view)
	}
}

// TestInspectorRail_AgentsMapNamesTheHandoffAndItsKey: a failed child that
// left a record a replacement could resume from says so, and names the key —
// which stays the manager's, so the row itself points at the session and
// nothing else.
func TestInspectorRail_AgentsMapNamesTheHandoffAndItsKey(t *testing.T) {
	r := InspectorRail{
		Agents: []InspectorAgent{
			{Name: "writer-4", Detail: "token budget exceeded", Outcome: "failed",
				State: FanoutFailed, Handoff: true},
		},
	}
	view := stripANSI(r.View(InspectorMaxWidth, 0))
	if !strings.Contains(view, "token budget exceeded · handoff kept · [r] retry") {
		t.Fatalf("the row ends on what can still be done about it:\n%s", view)
	}
	for _, row := range r.Rows(InspectorMaxWidth, 0) {
		if strings.Contains(stripANSI(row.Text), "handoff kept") &&
			row.Target != (RailTarget{Kind: RailTargetSession, Name: "writer-4"}) {
			t.Fatalf("the row points at its session and nothing else: %+v", row.Target)
		}
	}
	r.Agents[0].Handoff = false
	if view := stripANSI(r.View(InspectorMaxWidth, 0)); strings.Contains(view, "handoff") {
		t.Fatalf("a failure with nothing to resume from says nothing:\n%s", view)
	}
}

// TestInspectorRail_AgentsMapNestsAChildsChild: a session another session
// started is drawn one column in, behind the frame's own corner, and its
// second line moves in with it.
func TestInspectorRail_AgentsMapNestsAChildsChild(t *testing.T) {
	r := InspectorRail{
		Agents: []InspectorAgent{
			{Name: "orchestrator", Detail: "round 3", Self: true, State: FanoutRunning},
			{Name: "writer-1", Detail: "docs/loop.md", Depth: 1, State: FanoutRunning},
			{Name: "reader-2", Detail: "round.go", Depth: 2, State: FanoutRunning},
		},
		Frame: 2,
	}
	var parent, nested, under string
	for _, line := range r.Lines(InspectorMaxWidth, 0) {
		switch plain := stripANSI(line); {
		case strings.Contains(plain, "writer-1"):
			parent = plain
		case strings.Contains(plain, "reader-2"):
			nested = plain
		case strings.Contains(plain, "round.go"):
			under = plain
		}
	}
	if !strings.HasPrefix(parent, "  ◇ writer-1") {
		t.Fatalf("a session the run started keeps the block's indent: %q", parent)
	}
	if !strings.HasPrefix(nested, "  └◇ reader-2") {
		t.Fatalf("a session a session started is drawn one column in: %q", nested)
	}
	if !strings.HasPrefix(under, "     ") {
		t.Fatalf("the nested session's own line moves in with it: %q", under)
	}
}

// TestInspectorRail_AgentsMapMovesASubtreeWhole: a session another session
// started stays directly under its parent whatever floats. A failed sibling
// keeps its place, a blocked one floats over the pair rather than into it,
// a grandchild's request floats its parent with it, and a finished parent
// with a live descendant is not folded out from over the corner.
func TestInspectorRail_AgentsMapMovesASubtreeWhole(t *testing.T) {
	names := func(r InspectorRail) string {
		shown, _ := r.mappedAgents()
		var got []string
		for _, a := range shown {
			got = append(got, a.Name)
		}
		return strings.Join(got, ",")
	}
	self := InspectorAgent{Name: "orchestrator", Self: true, State: FanoutRunning}
	cases := []struct {
		name   string
		agents []InspectorAgent
		want   string
	}{
		{
			name: "a failed sibling stays beside the pair",
			agents: []InspectorAgent{self,
				{Name: "writer-1", Outcome: "failed", Depth: 1, State: FanoutFailed},
				{Name: "writer-3", Depth: 1, State: FanoutRunning},
				{Name: "reader-3a", Depth: 2, State: FanoutRunning},
			},
			want: "orchestrator,writer-1,writer-3,reader-3a",
		},
		{
			name: "a blocked sibling floats over the pair, not into it",
			agents: []InspectorAgent{self,
				{Name: "writer-3", Depth: 1, State: FanoutRunning},
				{Name: "reader-3a", Depth: 2, State: FanoutRunning},
				{Name: "writer-1", Outcome: "failed", Depth: 1, State: FanoutFailed},
				{Name: "writer-4", Depth: 1, State: FanoutBlocked},
			},
			want: "orchestrator,writer-4,writer-3,reader-3a,writer-1",
		},
		{
			name: "a grandchild's request floats its parent with it",
			agents: []InspectorAgent{self,
				{Name: "writer-1", Outcome: "failed", Depth: 1, State: FanoutFailed},
				{Name: "writer-3", Depth: 1, State: FanoutRunning},
				{Name: "reader-3a", Depth: 2, State: FanoutBlocked},
			},
			want: "orchestrator,writer-3,reader-3a,writer-1",
		},
		{
			name: "a finished parent is not folded from over a live child",
			agents: []InspectorAgent{self,
				{Name: "writer-1", Outcome: "done", Depth: 1, State: FanoutDone},
				{Name: "reader-1a", Depth: 2, State: FanoutRunning},
				{Name: "writer-2", Outcome: "done", Depth: 1, State: FanoutDone},
				{Name: "writer-3", Outcome: "failed", Depth: 1, State: FanoutFailed},
				{Name: "writer-4", Outcome: "done", Depth: 1, State: FanoutDone},
			},
			want: "orchestrator,writer-1,reader-1a,writer-3,writer-4",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := names(InspectorRail{Agents: tc.agents}); got != tc.want {
				t.Fatalf("the map draws %s, want %s", got, tc.want)
			}
		})
	}
}

// TestInspectorRail_AgentsMapShedsAChildWithItsParent: a rail short of
// height gives up a nested session before the session that started it, so at
// a height with room for one of the pair but not both, the one left is the
// parent and the corner never hangs under a stranger. A finished parent over
// a child still working is kept with it, and wherever a session has gone the
// marker counts it.
func TestInspectorRail_AgentsMapShedsAChildWithItsParent(t *testing.T) {
	self := InspectorAgent{Name: "orchestrator", Detail: "round 3", Self: true, State: FanoutRunning}
	cases := []struct {
		name   string
		agents []InspectorAgent
	}{
		{
			name: "a finished pair",
			agents: []InspectorAgent{self,
				{Name: "writer-1", Detail: "wrote two files", Outcome: "done", Depth: 1, State: FanoutDone},
				{Name: "reader-1a", Detail: "read the loop", Outcome: "done", Depth: 2, State: FanoutDone},
				{Name: "writer-2", Detail: "internal/agent/loop.go", Depth: 1, State: FanoutRunning},
			},
		},
		{
			name: "a finished parent over a child still working",
			agents: []InspectorAgent{self,
				{Name: "writer-1", Detail: "wrote two files", Outcome: "done", Depth: 1, State: FanoutDone},
				{Name: "reader-1a", Detail: "read the loop", Depth: 2, State: FanoutRunning},
				{Name: "writer-2", Detail: "wrote a test", Outcome: "failed", Depth: 1, State: FanoutFailed},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := InspectorRail{Agents: tc.agents, Frame: 2}
			full := len(r.Lines(InspectorMaxWidth, 0))
			childShed := false
			for h := full; h >= 3; h-- {
				v := stripANSI(strings.Join(r.Lines(InspectorMaxWidth, h), "\n"))
				child, parent := strings.Contains(v, "reader-1a"), strings.Contains(v, "writer-1")
				if child && !parent {
					t.Fatalf("at height %d the child is drawn without its parent:\n%s", h, v)
				}
				if parent && !child {
					childShed = true
					if !strings.Contains(v, "… ") {
						t.Fatalf("at height %d a session went and no marker counts it:\n%s", h, v)
					}
				}
			}
			if !childShed {
				t.Fatalf("no height kept the parent and shed the child")
			}
		})
	}
}
