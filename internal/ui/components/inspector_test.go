package components

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

// fullRail is a rail with every block populated.
func fullRail() InspectorRail {
	return InspectorRail{
		Turn: &InspectorTurn{Step: 3, Steps: 4, Tools: 18, Elapsed: 64 * time.Second, Running: true,
			Files: 2, Added: 27, Removed: 4},
		Changes: &InspectorChanges{
			Files: []InspectorFile{
				{Path: "agent/loop.go", Added: 18, Removed: 3, Turns: 3, ThisTurn: true},
				{Path: "ui/chat/model.go", Added: 9, Removed: 1, ThisTurn: true},
			},
			Added: 27, Removed: 4,
			Alerts: []InspectorAlert{{Label: "go test ./...", Note: OutcomeExit(1), Turn: 7}},
		},
		Agents: []InspectorAgent{
			{Name: "writer-1", Detail: "docs/loop.md", Spend: "$0.02", Tools: 4, State: FanoutRunning},
		},
		Context: &InspectorContext{Pct: 62, Tokens: 124000, Window: 200000,
			Tokens1: "↑41.2k", Tokens2: "↓9.8k", Burn: []float64{1, 2, 3, 3, 4, 5, 5, 6}},
		Spend: &InspectorSpend{Turn: "$0.14", Main: "$0.12", Children: "$0.02",
			Session: "$1.86", Model: "gpt-5.2"},
	}
}

// A context nobody vouched for says so, in a word and not just a sigil, so
// the claim survives a monochrome terminal.
func TestInspectorContext_EstimatedSaysSo(t *testing.T) {
	r := InspectorRail{Context: &InspectorContext{
		Pct: 41, Tokens: 82000, Window: 200000, Estimated: true,
	}}
	view := stripANSI(r.View(InspectorWidth, 0))
	for _, want := range []string{"CONTEXT", "41% of 200k", "~82k", "estimated"} {
		if !strings.Contains(view, want) {
			t.Fatalf("an estimated context should show %q:\n%s", want, view)
		}
	}
	// A reported one states the number plainly, with no hedge.
	reported := InspectorRail{Context: &InspectorContext{Pct: 41, Tokens: 82000, Window: 200000}}
	view = stripANSI(reported.View(InspectorWidth, 0))
	if strings.Contains(view, "estimated") || strings.Contains(view, "~82k") {
		t.Fatalf("a reported context should not hedge:\n%s", view)
	}
}

// A corrected estimate is a third kind of number, and it goes beside the
// count rather than under the bar: the sparkline's own label owns that row
// for most of a session, and a figure whose meaning changed has to say so
// whenever it is on screen.
func TestInspectorContext_CorrectedSaysSo(t *testing.T) {
	r := InspectorRail{Context: &InspectorContext{
		Pct: 41, Tokens: 82000, Window: 200000, Estimated: true, Corrected: true,
		Burn: []float64{1, 2, 3, 4},
	}}
	view := stripANSI(r.View(InspectorWidth, 0))
	for _, want := range []string{"~82k", "corrected", "per round"} {
		if !strings.Contains(view, want) {
			t.Fatalf("a corrected estimate should show %q:\n%s", want, view)
		}
	}
	// The bar keeps its full width beside the longer label.
	if !strings.Contains(view, strings.Repeat("▰", 9)+strings.Repeat("▱", 13)) {
		t.Fatalf("the meter should be undisturbed by the label:\n%s", view)
	}
	// An uncorrected estimate says nothing about a correction.
	plain := InspectorRail{Context: &InspectorContext{
		Pct: 41, Tokens: 82000, Window: 200000, Estimated: true,
	}}
	if strings.Contains(stripANSI(plain.View(InspectorWidth, 0)), "corrected") {
		t.Fatal("nothing corrected an estimate nobody measured")
	}
}

func TestInspectorRail_BlockOrderAndContents(t *testing.T) {
	view := stripANSI(fullRail().View(InspectorWidth, 0))
	for _, want := range []string{
		"THIS TURN", "step 3 of 4", "▰", "2 files this turn", "18 tools", "1m 04s",
		"CHANGES", "session · ", "+27", "−4", "▎✎ agent/loop.go", "+18", "−3", "3t",
		"✗ go test ./...", "turn 7", "exit 1",
		"AGENTS", "1 running", "◇ writer-1", "$0.02", "docs/loop.md · 4 tools",
		"CONTEXT", "62% of 200k", "124k", "↑41.2k ↓9.8k", "per round",
		"SPEND", "$0.14", "gpt-5.2 · $0.12 main · $0.02 ◇", "session total $1.86",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("rail missing %q:\n%s", want, view)
		}
	}
	// The order is fixed.
	order := []string{"THIS TURN", "CHANGES", "AGENTS", "CONTEXT", "SPEND"}
	at := 0
	for _, label := range order {
		i := strings.Index(view[at:], label)
		if i < 0 {
			t.Fatalf("%q out of order in:\n%s", label, view)
		}
		at += i
	}
}

func TestInspectorRail_OmitsEmptyBlocks(t *testing.T) {
	if got := (InspectorRail{}).Lines(InspectorWidth, 0); got != nil {
		t.Fatalf("an empty rail renders nothing, got %q", got)
	}
	if !(InspectorRail{}).Empty() {
		t.Fatal("an empty rail reports Empty")
	}
	// A session with no children has no AGENTS heading at all.
	r := fullRail()
	r.Agents = nil
	r.Changes = nil
	view := stripANSI(r.View(InspectorWidth, 0))
	for _, absent := range []string{"AGENTS", "CHANGES", "running"} {
		if strings.Contains(view, absent) {
			t.Fatalf("rail should omit %q:\n%s", absent, view)
		}
	}
	if !strings.Contains(view, "THIS TURN") || !strings.Contains(view, "SPEND") {
		t.Fatalf("remaining blocks still render:\n%s", view)
	}
	// Blocks are never rendered empty: no heading without its rows.
	if strings.Contains(view, "\n\n\n") {
		t.Fatalf("rail has an empty block:\n%s", view)
	}
}

func TestInspectorRail_AgentLaneOnlyMetersDeclaredSteps(t *testing.T) {
	// No declared step count: the lane moves rather than drawing a ratio
	// nobody supplied.
	r := InspectorRail{
		Agents: []InspectorAgent{
			{Name: "writer-1", Detail: "editing docs/loop.md", Tools: 4, State: FanoutRunning},
		},
		Frame: 2,
	}
	view := stripANSI(r.View(InspectorWidth, 0))
	if strings.Contains(view, "▰") {
		t.Fatalf("no bar without a declared step count:\n%s", view)
	}
	if !strings.Contains(view, "⠹ editing docs/loop.md") {
		t.Fatalf("the spinner names what is running:\n%s", view)
	}
	// A declared count earns the five-cell lane meter, and the meter states
	// the count beside it.
	r.Agents[0].Step, r.Agents[0].Steps = 2, 4
	view = stripANSI(r.View(InspectorWidth, 0))
	if !strings.Contains(view, "▰▰▱▱▱ step 2 of 4") {
		t.Fatalf("a declared count draws the lane meter:\n%s", view)
	}
	if strings.Contains(view, "⠹") {
		t.Fatalf("a lane with a bar does not also spin:\n%s", view)
	}
	// A child waiting on the user is not running, so it gets neither.
	r.Agents[0].Step, r.Agents[0].Steps = 0, 0
	r.Agents[0].State = FanoutBlocked
	view = stripANSI(r.View(InspectorWidth, 0))
	if strings.Contains(view, "⠹") || strings.Contains(view, "▰") {
		t.Fatalf("a blocked lane shows no motion and no bar:\n%s", view)
	}
	if !strings.Contains(view, "⚠ writer-1") {
		t.Fatalf("a blocked lane says so:\n%s", view)
	}
}

// mapRail is the block the map's own tests read: the orchestrator, two
// children still working and two that have stopped, one each way.
func mapRail() InspectorRail {
	return InspectorRail{
		Agents: []InspectorAgent{
			{Name: "orchestrator", Detail: "ready", Spend: "$0.12", Self: true, State: FanoutIdle},
			{Name: "writer-1", Detail: "docs/loop.md", Spend: "$0.02", Tools: 4, State: FanoutRunning},
			{Name: "writer-2", Detail: "wrote 2 files", Spend: "$0.03", Outcome: "done", State: FanoutDone},
			{Name: "runner-3", Detail: "go test ./...", Spend: "$0.01", State: FanoutRunning},
			{Name: "reader-4", Detail: "no such path", Spend: "$0.01", Outcome: "failed", State: FanoutFailed},
		},
		Frame: 2,
	}
}

// TestInspectorRail_AgentsMapEveryStateInSpawnOrder is the map as a whole:
// the orchestrator leads it, every child follows in the order it was spawned,
// each carries the glyph for its state, and the two that have stopped say how
// rather than disappearing into the manager.
func TestInspectorRail_AgentsMapEveryStateInSpawnOrder(t *testing.T) {
	view := stripANSI(mapRail().View(InspectorMaxWidth, 0))
	for _, want := range []string{
		"\u2298 orchestrator", "\u25c7 writer-1", "\u2713 writer-2", "\u25c7 runner-3", "\u2717 reader-4",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the map is missing %q:\n%s", want, view)
		}
	}
	if strings.Index(view, "orchestrator") > strings.Index(view, "writer-1") {
		t.Fatalf("the orchestrator leads the map:\n%s", view)
	}
	if strings.Index(view, "writer-1") > strings.Index(view, "runner-3") {
		t.Fatalf("children follow in spawn order:\n%s", view)
	}
	// A finished child states its own outcome, and nothing under a row that
	// has stopped moving pretends it still is.
	if !strings.Contains(view, "done") || !strings.Contains(view, "failed") {
		t.Fatalf("a finished child states its outcome:\n%s", view)
	}
	if strings.Contains(view, "\u2839 wrote 2 files") {
		t.Fatalf("a finished child does not spin:\n%s", view)
	}
	// The heading tallies the children and leaves the orchestrator out of it:
	// two are running, and the finished ones are left to the rows.
	if !strings.Contains(view, "2 running") {
		t.Fatalf("the heading tallies the children alone:\n%s", view)
	}
}

// TestInspectorRail_AgentsMapMarksTheFocusedRow is what makes the rail safe
// to leave up beside a child's transcript: one row is marked, and the mark
// moves with the keyboard rather than staying on the orchestrator.
func TestInspectorRail_AgentsMapMarksTheFocusedRow(t *testing.T) {
	r := mapRail()
	r.Agents[0].Focused = true
	marked := func(name string) bool {
		for _, l := range r.Lines(InspectorMaxWidth, 0) {
			if l := stripANSI(l); strings.Contains(l, name) && strings.HasPrefix(l, "\u25b8 ") {
				return true
			}
		}
		return false
	}
	if !marked("orchestrator") {
		t.Fatal("the focused row is marked")
	}
	r.Agents[0].Focused, r.Agents[1].Focused = false, true
	if marked("orchestrator") || !marked("writer-1") {
		t.Fatal("the mark follows the keyboard")
	}
	// The mark sits in the indent, so an unmarked row starts where it did.
	for _, l := range r.Lines(InspectorMaxWidth, 0) {
		if l := stripANSI(l); strings.Contains(l, "runner-3") && !strings.HasPrefix(l, "  \u25c7") {
			t.Fatalf("an unmarked row keeps the block's indent: %q", l)
		}
	}
}

// TestInspectorRail_AgentsMapFoldsFinishedChildren pins the fold: the map
// keeps the last few outcomes and counts the rest, and it counts sessions
// rather than the rows they take. The orchestrator and the focused row are
// never what folds, whatever state they are in.
func TestInspectorRail_AgentsMapFoldsFinishedChildren(t *testing.T) {
	r := mapRail()
	if view := stripANSI(r.View(InspectorMaxWidth, 0)); strings.Contains(view, "\u2026 ") {
		t.Fatalf("two finished children are inside the budget:\n%s", view)
	}
	for i := range 3 {
		r.Agents = append(r.Agents, InspectorAgent{
			Name: fmt.Sprintf("reader-%d", 5+i), Detail: "read 3 files",
			Spend: "$0.01", Outcome: "done", State: FanoutDone,
		})
	}
	view := stripANSI(r.View(InspectorMaxWidth, 0))
	// Three past a budget of two: the three earliest finished children fold,
	// and the marker counts children rather than the six rows they were
	// taking.
	if !strings.Contains(view, "\u2026 3 more") {
		t.Fatalf("the fold counts the sessions it took:\n%s", view)
	}
	if !strings.Contains(view, "reader-7") {
		t.Fatalf("the newest outcomes are the ones kept:\n%s", view)
	}
	// A finished child the keyboard is in stays on screen whatever the budget
	// says: it is the row the rest of the rail is read against.
	r.Agents[2].Focused = true
	if view := stripANSI(r.View(InspectorMaxWidth, 0)); !strings.Contains(view, "writer-2") {
		t.Fatalf("the focused row never folds:\n%s", view)
	}
}

func TestInspectorRail_RowsFitTheWidth(t *testing.T) {
	long := fullRail()
	long.Changes.Files = append(long.Changes.Files, InspectorFile{
		Path: "internal/ui/components/a/very/deeply/nested/path/name.go", Added: 120, Removed: 99})
	for _, line := range long.Lines(InspectorWidth, 0) {
		if w := lipgloss.Width(line); w > InspectorWidth {
			t.Fatalf("line %q is %d columns, rail is %d", stripANSI(line), w, InspectorWidth)
		}
	}
	// The right field is the number, so it survives and the path clips.
	view := stripANSI(long.View(InspectorWidth, 0))
	if !strings.Contains(view, "+120") || !strings.Contains(view, "−99") {
		t.Fatalf("counts must not clip:\n%s", view)
	}
	if !strings.Contains(view, "…") {
		t.Fatalf("an overlong path clips with an ellipsis:\n%s", view)
	}
}

func TestInspectorRail_HeadingMetaIsRightAligned(t *testing.T) {
	lines := fullRail().Lines(InspectorWidth, 0)
	for _, line := range lines {
		plain := stripANSI(line)
		if !strings.HasPrefix(plain, "  CONTEXT") {
			continue
		}
		if !strings.HasSuffix(plain, "62% of 200k") || lipgloss.Width(line) != InspectorWidth {
			t.Fatalf("heading meta sits on the rail's right edge, got %q (%d cols)", plain, lipgloss.Width(line))
		}
		return
	}
	t.Fatal("CONTEXT heading not found")
}

func TestInspectorRail_TruncatesLongestBlockFirst(t *testing.T) {
	r := fullRail()
	for i := 0; i < 12; i++ {
		r.Changes.Files = append(r.Changes.Files, InspectorFile{Path: "pkg/f.go", Added: 1, Removed: 1})
	}
	full := r.Lines(InspectorWidth, 0)
	height := len(full) - 6
	lines := r.Lines(InspectorWidth, height)
	if len(lines) > height {
		t.Fatalf("rail must fit %d rows, got %d", height, len(lines))
	}
	view := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(view, "… ") || !strings.Contains(view, "more") {
		t.Fatalf("a truncated block says what it swallowed:\n%s", view)
	}
	// The longest block gave up the rows; the short ones kept theirs.
	for _, want := range []string{"THIS TURN", "SPEND", "session total $1.86", "CONTEXT"} {
		if !strings.Contains(view, want) {
			t.Fatalf("truncation should not drop %q:\n%s", want, view)
		}
	}
}

func TestInspectorRail_NoDeclaredStepsNoRatio(t *testing.T) {
	r := InspectorRail{Turn: &InspectorTurn{Step: 3, Tools: 7, Elapsed: 2 * time.Second, Running: true}}
	view := stripANSI(r.View(InspectorWidth, 0))
	if !strings.Contains(view, "step 3") || strings.Contains(view, "of") {
		t.Fatalf("an observed step count has no denominator:\n%s", view)
	}
	if strings.Contains(view, "▰") {
		t.Fatalf("no meter without a declared step count:\n%s", view)
	}
	// The row is the artboard's: the turn's own file count said in
	// words, then its tools and its clock. Whether that clock is still
	// running is the live turn status's answer, not a word repeated
	// here — and a turn that wrote nothing still says so.
	if !strings.Contains(view, "0 files this turn · 7 tools · 2.0s") {
		t.Fatalf("the block still states its counts:\n%s", view)
	}
	done := InspectorRail{Turn: &InspectorTurn{Tools: 1, Elapsed: time.Second, Files: 1, Added: 4, Removed: 2}}
	if !strings.Contains(stripANSI(done.View(InspectorWidth, 0)), "1 file this turn +4 −2 · 1 tool · 1.0s") {
		t.Fatalf("a turn's own files are counted beside its tools:\n%s", done.View(InspectorWidth, 0))
	}
}

func TestInspectorElapsedAndTokens(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{900 * time.Millisecond, "0.9s"}, {12 * time.Second, "12s"},
		{64 * time.Second, "1m 04s"}, {2 * time.Hour, "120m 00s"},
	} {
		if got := FormatElapsed(c.d); got != c.want {
			t.Fatalf("FormatElapsed(%s) = %q, want %q", c.d, got, c.want)
		}
	}
	for _, c := range []struct {
		n    int64
		want string
	}{{0, "0"}, {999, "999"}, {1200, "1.2k"}, {124000, "124k"}, {2_000_000, "2.0M"}} {
		if got := formatTokens(c.n); got != c.want {
			t.Fatalf("formatTokens(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

// The two blocks that can count files say their scope in words, which is what
// stops "2 files this turn" and "session · +96 −11" reading as a
// contradiction.
func TestInspectorRail_BothFileCountsSayTheirScope(t *testing.T) {
	view := stripANSI(fullRail().View(InspectorWidth, 0))
	if !strings.Contains(view, "2 files this turn") {
		t.Fatalf("THIS TURN states its scope:\n%s", view)
	}
	if !strings.Contains(view, "session · ") {
		t.Fatalf("CHANGES states its scope:\n%s", view)
	}
}

// Repeat edits to one path collapse to a single row carrying the turns behind
// it; one turn's worth of edits says nothing, because "1t" is not news.
func TestInspectorChanges_RepeatEditsCarryTheirTurnCount(t *testing.T) {
	r := InspectorRail{Changes: &InspectorChanges{
		Files: []InspectorFile{
			{Path: "agent/loop.go", Added: 21, Removed: 4, Turns: 3},
			{Path: "go.mod", Added: 1, Turns: 1},
		},
		Added: 22, Removed: 4,
	}}
	view := stripANSI(r.View(InspectorWidth, 0))
	if !strings.Contains(view, "+21 −4 3t") {
		t.Fatalf("a path edited in three turns says so:\n%s", view)
	}
	if strings.Contains(view, "1t") {
		t.Fatalf("one turn is not a count worth printing:\n%s", view)
	}
	if n := strings.Count(view, "agent/loop.go"); n != 1 {
		t.Fatalf("one row per path, got %d:\n%s", n, view)
	}
}

// The fold keeps the rows this turn wrote and carries the counts of the ones
// it took, so nothing the session changed goes unaccounted for (invariant 4).
func TestInspectorChanges_FoldKeepsThisTurnAndCarriesItsCounts(t *testing.T) {
	r := InspectorRail{Changes: &InspectorChanges{
		Files: []InspectorFile{
			{Path: "agent/loop.go", Added: 21, Removed: 4, Turns: 3, ThisTurn: true},
			{Path: "agent/loop_test.go", Added: 18, Turns: 2},
			{Path: "ui/chat/model.go", Added: 9, Removed: 1, ThisTurn: true},
			{Path: "agent/round.go", Added: 6, Removed: 2},
			{Path: "tool/exec.go", Added: 4, Removed: 4},
		},
		Added: 58, Removed: 11,
	}}
	view := stripANSI(r.View(InspectorWidth, 4))
	for _, want := range []string{"agent/loop.go", "ui/chat/model.go", "… 3 more", "+28", "−6"} {
		if !strings.Contains(view, want) {
			t.Fatalf("folded rail missing %q:\n%s", want, view)
		}
	}
	for _, absent := range []string{"loop_test.go", "round.go", "exec.go"} {
		if strings.Contains(view, absent) {
			t.Fatalf("%q should be behind the fold:\n%s", absent, view)
		}
	}
}

// An alert outlives the turn that caused it, names that turn, and is the last
// thing the fold takes — a red row that scrolls itself away is the failure
// the block exists to prevent.
func TestInspectorChanges_AlertsOutliveTheirTurn(t *testing.T) {
	r := InspectorRail{Changes: &InspectorChanges{
		Files: []InspectorFile{
			{Path: "agent/loop.go", Added: 21, Removed: 4},
			{Path: "go.mod", Added: 1},
		},
		Added: 22, Removed: 4,
		Alerts: []InspectorAlert{
			{Label: "go test ./...", Note: OutcomeExit(1), Turn: 7},
			{Label: "go build ./...", Note: OutcomeExit(2), Turn: 9},
		},
	}}
	view := stripANSI(r.View(InspectorWidth, 0))
	for _, want := range []string{"✗ go test ./...", "turn 7", "exit 1", "✗ go build ./...", "turn 9"} {
		if !strings.Contains(view, want) {
			t.Fatalf("alert missing %q:\n%s", want, view)
		}
	}
	// The alerts sit above the file rows.
	if strings.Index(view, "go build ./...") > strings.Index(view, "agent/loop.go") {
		t.Fatalf("alerts belong above the file rows:\n%s", view)
	}
	// Squeezed, the files go first.
	tight := stripANSI(r.View(InspectorWidth, 5))
	if !strings.Contains(tight, "go test ./...") || !strings.Contains(tight, "go build ./...") {
		t.Fatalf("the alerts are the last rows truncation takes:\n%s", tight)
	}
	if strings.Contains(tight, "agent/loop.go") {
		t.Fatalf("the file rows go before the alerts do:\n%s", tight)
	}
}

// A file whose whole change is its permissions has no lines to count. The row
// states the change it has where it would state them, and the heading drops
// the total rather than printing a pair of zeros over a real change.
func TestInspectorChanges_AModeOnlyRowStatesTheModeNotZeros(t *testing.T) {
	r := InspectorRail{Changes: &InspectorChanges{
		Files: []InspectorFile{{Path: "scripts/build.sh", Mode: "mode 0644 → 0755"}},
	}}
	view := stripANSI(r.View(InspectorWidth, 0))
	if !strings.Contains(view, "mode 0644 → 0755") {
		t.Fatalf("the row states the mode it changed:\n%s", view)
	}
	if strings.Contains(view, "+0 −0") {
		t.Fatalf("no row on the rail counts a change it did not measure:\n%s", view)
	}
	if !strings.Contains(view, "session") {
		t.Fatalf("the block still says what it is scoped to:\n%s", view)
	}
}

// A file that moved lines as well has counts, and the counts are what the
// one field on the rail is for: the mode is stated where there is room for
// both, which is that file's review row and its diff header.
func TestInspectorChanges_AFileWithCountsStatesThem(t *testing.T) {
	r := InspectorRail{Changes: &InspectorChanges{
		Files: []InspectorFile{{Path: "scripts/build.sh", Added: 3, Removed: 1, Mode: "mode 0644 → 0755"}},
		Added: 3, Removed: 1,
	}}
	view := stripANSI(r.View(InspectorWidth, 0))
	if !strings.Contains(view, "+3 −1") {
		t.Fatalf("the row states the lines it counted:\n%s", view)
	}
	if strings.Contains(view, "mode 0644") {
		t.Fatalf("the rail's one field belongs to the counts:\n%s", view)
	}
}

// Rows with no counts of their own fold behind a bare marker: a fold that
// totalled them would print the zero the rows themselves refused to.
func TestInspectorChanges_ModeOnlyRowsFoldWithoutATotal(t *testing.T) {
	r := InspectorRail{Changes: &InspectorChanges{
		Files: []InspectorFile{
			{Path: "scripts/one.sh", Mode: "mode 0644 → 0755"},
			{Path: "scripts/two.sh", Mode: "mode 0644 → 0755"},
			{Path: "scripts/three.sh", Mode: "mode 0600 → 0700"},
		},
	}}
	view := stripANSI(r.View(InspectorWidth, 3))
	if !strings.Contains(view, "… 2 more") {
		t.Fatalf("the rows it could not fit go behind a marker:\n%s", view)
	}
	if strings.Contains(view, "+0 −0") {
		t.Fatalf("the marker carries no total it did not measure:\n%s", view)
	}
}

// A block with nothing but alerts is still a block: the session changed
// nothing this time and something is still broken.
func TestInspectorChanges_AlertsAloneStillRender(t *testing.T) {
	r := InspectorRail{Changes: &InspectorChanges{
		Alerts: []InspectorAlert{{Label: "go test ./...", Note: OutcomeExit(1), Turn: 3}},
	}}
	view := stripANSI(r.View(InspectorWidth, 0))
	if !strings.Contains(view, "CHANGES") || !strings.Contains(view, "go test ./...") {
		t.Fatalf("an alert with no files still renders:\n%s", view)
	}
	if strings.Contains(view, "session · ") {
		t.Fatal("a session that changed nothing states no total")
	}
}

// The SUMMARY block is the rail's one prose block, and it sits
// first: it says what is happening, and every block under it is the detail of
// that.
func TestInspectorSummary_LeadsTheRail(t *testing.T) {
	r := fullRail()
	r.Summary = &InspectorSummary{
		Text:  "Wiring the round-limit pause into the chat model.",
		State: SummaryOnTarget, Round: 24,
	}
	view := stripANSI(r.View(InspectorWidth, 0))
	for _, want := range []string{"SUMMARY", "as of round 24", "Wiring the round-limit", "▸ on target"} {
		if !strings.Contains(view, want) {
			t.Fatalf("summary block missing %q:\n%s", want, view)
		}
	}
	if strings.Index(view, "SUMMARY") > strings.Index(view, "THIS TURN") {
		t.Fatalf("SUMMARY leads the rail:\n%s", view)
	}
}

// A reading nobody has taken is a block with nothing to say, and a block with
// nothing to say is omitted rather than drawn empty.
func TestInspectorSummary_OmittedWithoutAReading(t *testing.T) {
	for _, s := range []*InspectorSummary{nil, {Text: "   ", Round: 4}} {
		r := InspectorRail{Summary: s, Turn: &InspectorTurn{Tools: 2, Running: true}}
		if view := stripANSI(r.View(InspectorWidth, 0)); strings.Contains(view, "SUMMARY") {
			t.Fatalf("an unread summary draws no block:\n%s", view)
		}
	}
}

// Every state says itself in a glyph and in words, so the row reads the same
// on a monochrome terminal.
func TestInspectorSummary_EveryStateStatesItself(t *testing.T) {
	cases := []struct {
		state SummaryTone
		want  string
	}{
		{SummaryOnTarget, "▸ on target"},
		{SummaryOffTarget, "⚠ off target"},
		{SummaryUnclear, "· target unclear"},
	}
	for _, tc := range cases {
		r := InspectorRail{Summary: &InspectorSummary{Text: "Reading the loop.", State: tc.state, Round: 3}}
		if view := stripANSI(r.View(InspectorWidth, 0)); !strings.Contains(view, tc.want) {
			t.Fatalf("state row missing %q:\n%s", tc.want, view)
		}
	}
}

// A departure earns the row that explains it; "on target because…" is the
// model narrating, and the block has no row for that.
func TestInspectorSummary_ReasonQualifiesADeparture(t *testing.T) {
	r := InspectorRail{Summary: &InspectorSummary{
		Text: "Rewriting the README.", State: SummaryOffTarget,
		Reason: "docs were not asked for", Round: 31,
	}}
	view := stripANSI(r.View(InspectorWidth, 0))
	if !strings.Contains(view, "⚠ off target") {
		t.Fatalf("the state row states the departure:\n%s", view)
	}
	// The reason gets its own row, one indent past the state it explains, so
	// it is read whole rather than clipped into a suffix.
	if !strings.Contains(view, "    docs were not asked for") {
		t.Fatalf("the reason follows the state row, indented:\n%s", view)
	}
	// An on-target reading has no reason row at all.
	clean := InspectorRail{Summary: &InspectorSummary{Text: "Fixing the loop.", State: SummaryOnTarget, Round: 3}}
	if lines := clean.Lines(InspectorWidth, 0); len(lines) != 3 {
		t.Fatalf("an on-target block is heading, sentence, state: got %d rows", len(lines))
	}
}

// A reading the session has outrun says so in the heading rather than letting
// an old sentence pass for a current one.
func TestInspectorSummary_StaleSaysSo(t *testing.T) {
	r := InspectorRail{Summary: &InspectorSummary{
		Text: "Running the tests.", State: SummaryOnTarget, Round: 12, Stale: true,
	}}
	view := stripANSI(r.View(InspectorWidth, 0))
	if !strings.Contains(view, "as of round 12 · stale") {
		t.Fatalf("a stale reading says so:\n%s", view)
	}
	fresh := InspectorRail{Summary: &InspectorSummary{Text: "Running the tests.", Round: 12}}
	if strings.Contains(stripANSI(fresh.View(InspectorWidth, 0)), "stale") {
		t.Fatal("a current reading does not hedge")
	}
}

// The prose wraps to the rail rather than being clipped — a sentence cut at
// 44 columns is a sentence nobody can finish — and stops at summaryLines.
func TestInspectorSummary_WrapsAndBounds(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("wiring the round limit into the chat model ", 8))
	r := InspectorRail{Summary: &InspectorSummary{Text: long, State: SummaryOnTarget, Round: 9}}
	lines := r.Lines(InspectorWidth, 0)
	for _, line := range lines {
		if lipgloss.Width(line) > InspectorWidth {
			t.Fatalf("row overruns the rail (%d): %q", lipgloss.Width(line), stripANSI(line))
		}
	}
	// Heading, the bounded prose, and the state row.
	if want := 2 + summaryLines; len(lines) != want {
		t.Fatalf("summary block = %d rows, want %d:\n%s", len(lines), want, strings.Join(lines, "\n"))
	}
	if last := strings.TrimRight(stripANSI(lines[summaryLines]), " "); !strings.HasSuffix(last, "…") {
		t.Fatalf("a bounded reading says it was cut:\n%s", last)
	}
}

// Truncation takes the tail of the reading, never the sentence's first line
// or the state row — a block reduced to a heading and half a word is worse
// than one that says it folded.
func TestInspectorSummary_KeepsItsFirstLineAndItsState(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("wiring the round limit into the chat model ", 8))
	r := InspectorRail{
		Summary: &InspectorSummary{Text: long, State: SummaryOffTarget, Reason: "docs", Round: 9},
		Changes: &InspectorChanges{Files: []InspectorFile{
			{Path: "internal/agent/loop.go", Added: 3},
			{Path: "internal/agent/round.go", Added: 2},
		}, Added: 5},
	}
	// Eight rows is room for the heading, one line of the sentence, the state
	// row and the fold — the state row outranks the sentence's later lines.
	view := stripANSI(r.View(InspectorWidth, 8))
	if !strings.Contains(view, "SUMMARY") || !strings.Contains(view, "wiring the round") {
		t.Fatalf("the sentence's first line survives:\n%s", view)
	}
	if !strings.Contains(view, "off target") {
		t.Fatalf("the state row survives:\n%s", view)
	}
	if !strings.Contains(view, "more") {
		t.Fatalf("a folded block says what it swallowed:\n%s", view)
	}
}

// A session with no tool source outside shhh has no way to have lost one, so
// the block is omitted rather than drawn with a row saying the obvious.
func TestInspectorTools_OmittedWithoutASource(t *testing.T) {
	for _, tools := range []*InspectorTools{nil, {}} {
		r := InspectorRail{Tools: tools, Turn: &InspectorTurn{Tools: 2, Running: true}}
		if view := stripANSI(r.View(InspectorWidth, 0)); strings.Contains(view, "TOOLS") {
			t.Fatalf("no sources draws no block:\n%s", view)
		}
	}
}

// Every state says itself in a glyph and in words, so a source that failed to
// register reads the same on a monochrome terminal.
func TestInspectorTools_EveryStateStatesItself(t *testing.T) {
	cases := []struct {
		state ToolSourceState
		want  string
	}{
		{ToolSourceUp, "✓ docs"},
		{ToolSourceBlocked, "⚠ docs"},
		{ToolSourceOff, "⊘ docs"},
		{ToolSourceFailed, "✗ docs"},
	}
	for _, tc := range cases {
		r := InspectorRail{Tools: &InspectorTools{
			Sources: []InspectorToolSource{{Name: "docs", State: tc.state}},
		}}
		view := stripANSI(r.View(InspectorWidth, 0))
		if !strings.Contains(view, tc.want) {
			t.Fatalf("source row missing %q:\n%s", tc.want, view)
		}
		if word := ToolSourceWord(tc.state); !strings.Contains(view, word) {
			t.Fatalf("source row missing the word %q:\n%s", word, view)
		}
	}
}

// The heading counts every source the session has, not only the rows that fit.
func TestInspectorTools_HeadingCountsWhatTheFoldTook(t *testing.T) {
	r := InspectorRail{Tools: &InspectorTools{
		Sources: []InspectorToolSource{
			{Name: "built-in", State: ToolSourceUp, Note: "18 tools"},
			{Name: "docs", State: ToolSourceUp, Note: "9 tools"},
		},
		Up:   4,
		More: 3,
	}}
	view := stripANSI(r.View(InspectorWidth, 0))
	// Two of the three sources the fold took are up too, and the heading
	// counts them: a ratio that changed with the fold would be a lie.
	if !strings.Contains(view, "4 of 5 up") {
		t.Fatalf("the heading counts every source:\n%s", view)
	}
	if !strings.Contains(view, "… 3 more") {
		t.Fatalf("the fold says what it took:\n%s", view)
	}
}

// A memory the recall budget left out of the prompt is otherwise
// indistinguishable from one that was never written, so the block says how
// many — and says it even in a session whose only tools are shhh's own.
func TestInspectorTools_SaysWhatRecallLeftOut(t *testing.T) {
	r := InspectorRail{Tools: &InspectorTools{MemoryOmitted: 3}}
	view := stripANSI(r.View(InspectorWidth, 0))
	if !strings.Contains(view, "TOOLS") || !strings.Contains(view, "⚠ memory") {
		t.Fatalf("the omission earns the block on its own:\n%s", view)
	}
	if !strings.Contains(view, "3 did not fit") {
		t.Fatalf("the row carries the count:\n%s", view)
	}
	if strings.Contains(view, "0 of 0 up") {
		t.Fatalf("a session with no source gets no ratio, not a zero one:\n%s", view)
	}

	// Nothing left out, nothing said.
	quiet := InspectorRail{Tools: &InspectorTools{
		Sources: []InspectorToolSource{{Name: "docs", State: ToolSourceUp}},
	}}
	if view := stripANSI(quiet.View(InspectorWidth, 0)); strings.Contains(view, "memory") {
		t.Fatalf("a session that recalled everything draws no row:\n%s", view)
	}
}

// The glyph carries the distinction, so a monochrome terminal reads the same
// verdict as a colour one — which means no two states may share one.
func TestSummaryTone_EveryStateHasItsOwnGlyphAndWords(t *testing.T) {
	glyphs := map[string]SummaryTone{}
	words := map[string]SummaryTone{}
	for _, s := range []SummaryTone{SummaryUnclear, SummaryOnTarget, SummaryOffTarget, SummarySufficient} {
		glyph, label, _ := summaryTone(s)
		if glyph == "" || label == "" {
			t.Fatalf("state %v has no rendering", s)
		}
		if prev, seen := glyphs[glyph]; seen {
			t.Errorf("states %v and %v share the glyph %q", prev, s, glyph)
		}
		if prev, seen := words[label]; seen {
			t.Errorf("states %v and %v share the words %q", prev, s, label)
		}
		glyphs[glyph] = s
		words[label] = s
	}
}

// TestInspectorWidthFor_IsTheLadder pins the rule the layout reads: the floor
// at the rung and below it, one column for every four the content has above
// it, and a ceiling.
func TestInspectorWidthFor_IsTheLadder(t *testing.T) {
	for _, c := range []struct{ content, want int }{
		{80, InspectorWidth},
		{InspectorMinContentWidth, InspectorWidth},
		{144, 49},
		{160, 53},
		{200, 63},
		{260, InspectorMaxWidth},
		{999, InspectorMaxWidth},
	} {
		if got := InspectorWidthFor(c.content); got != c.want {
			t.Errorf("content %d: rail is %d columns, want %d", c.content, got, c.want)
		}
	}
}

func TestParseRailWidth(t *testing.T) {
	for _, c := range []struct {
		in      string
		want    int
		refused bool
	}{
		{"", 0, false},
		{RailWidthAuto, 0, false},
		{" AUTO ", 0, false},
		{"60", 60, false},
		{"40", 40, false}, // held to the floor by the layout, not refused here
		{"wide", 0, true},
		{"0", 0, true},
		{"-3", 0, true},
	} {
		got, err := ParseRailWidth(c.in)
		if (err != nil) != c.refused {
			t.Errorf("%q: err = %v, refused = %v", c.in, err, c.refused)
		}
		if got != c.want {
			t.Errorf("%q: got %d, want %d", c.in, got, c.want)
		}
	}
}

// TestInspectorRail_EveryWidthInTheRangeFits: the rail is rendered against
// whatever width it is handed, so no row may be wider than the rail and no
// block may stop short of its right edge — a heading that ends at column 46
// inside a 62-column rail is the shape a constant read in the wrong place
// leaves behind.
func TestInspectorRail_EveryWidthInTheRangeFits(t *testing.T) {
	r := fullRail()
	r.Summary = &InspectorSummary{Text: "Wiring the round-limit pause into the chat model.", State: SummaryOnTarget, Round: 24}
	for width := InspectorWidth; width <= InspectorMaxWidth; width++ {
		for _, line := range r.Lines(width, 0) {
			if w := lipgloss.Width(line); w > width {
				t.Fatalf("width %d: line %q is %d columns", width, stripANSI(line), w)
			}
		}
		for _, line := range r.Lines(width, 0) {
			plain := stripANSI(line)
			if !strings.HasPrefix(plain, "  CONTEXT") {
				continue
			}
			if lipgloss.Width(line) != width {
				t.Fatalf("width %d: the CONTEXT heading ends at column %d", width, lipgloss.Width(line))
			}
		}
	}
}

// TestInspectorRail_FoldMarkerSpansTheRail: the row a truncated block leaves
// behind is a row of the rail like any other, and read the rail's floor once
// instead of the width it was given.
func TestInspectorRail_FoldMarkerSpansTheRail(t *testing.T) {
	r := InspectorRail{Todo: &InspectorTodo{Open: 9, Rows: []InspectorTodoRow{
		{Slug: "rail-width", Priority: "H", Size: "M", State: TodoReady},
		{Slug: "rail-setting", Priority: "M", Size: "S", State: TodoReady},
		{Slug: "rail-goldens", Priority: "L", Size: "S", State: TodoReady},
	}}}
	const width = InspectorMaxWidth
	lines := r.Lines(width, 3)
	last := lines[len(lines)-1]
	if !strings.Contains(stripANSI(last), "more") {
		t.Fatalf("the last row should be the fold marker, got %q", stripANSI(last))
	}
	if got := lipgloss.Width(last); got != width {
		t.Fatalf("the fold marker is %d columns in a %d-column rail", got, width)
	}
}

// TestInspectorRail_MetersScaleWithTheRail: the extra columns go to the runs
// rather than to the gap beside them, which is the whole of what a wider rail
// is for. The count is the meter's own, so this reads the bar rather than the
// row it sits on.
func TestInspectorRail_MetersScaleWithTheRail(t *testing.T) {
	r := fullRail()
	// The series is as long as the widest rail can draw, which is what the
	// host that feeds it keeps: a run that is short because the samples ran
	// out would say nothing about the cells.
	r.Context.Burn = make([]float64, SparkCellsRailMax)
	for i := range r.Context.Burn {
		r.Context.Burn[i] = float64(i + 1)
	}
	runLen := func(width int, glyphs string) int {
		var longest int
		for _, line := range r.Lines(width, 0) {
			n := 0
			for _, ch := range stripANSI(line) {
				if strings.ContainsRune(glyphs, ch) {
					n++
					continue
				}
				longest, n = max(longest, n), 0
			}
			longest = max(longest, n)
		}
		return longest
	}
	for _, glyphs := range []string{"▰▱", "▁▂▃▄▅▆▇█"} {
		narrow, wide := runLen(InspectorWidth, glyphs), runLen(InspectorMaxWidth, glyphs)
		if wide-narrow != InspectorMaxWidth-InspectorWidth {
			t.Errorf("%q run is %d cells at %d columns and %d at %d",
				glyphs, narrow, InspectorWidth, wide, InspectorMaxWidth)
		}
	}
}

// TestInspectorRail_WideRailKeepsThePathAndTheStats: the extra columns reach
// the path before anything clips, and the counts are never what gives way.
func TestInspectorRail_WideRailKeepsThePathAndTheStats(t *testing.T) {
	const path = "internal/ui/components/inspector.go"
	r := InspectorRail{Changes: &InspectorChanges{
		Files: []InspectorFile{{Path: path, Added: 120, Removed: 99}}, Added: 120, Removed: 99}}
	narrow := stripANSI(r.View(InspectorWidth, 0))
	if strings.Contains(narrow, path) {
		t.Fatalf("the narrow rail is supposed to clip this path:\n%s", narrow)
	}
	wide := stripANSI(r.View(InspectorMaxWidth, 0))
	for _, want := range []string{path, "+120", "−99"} {
		if !strings.Contains(wide, want) {
			t.Fatalf("a wide rail keeps %q:\n%s", want, wide)
		}
	}
}

// TestSparkCellsRailMax_IsTheRunAtTheCeiling keeps the constant a host bounds
// its series by and the run the widest rail draws from drifting apart.
func TestSparkCellsRailMax_IsTheRunAtTheCeiling(t *testing.T) {
	if got := railCells(SparkCells, InspectorMaxWidth); got != SparkCellsRailMax {
		t.Fatalf("the widest run is %d cells, SparkCellsRailMax is %d", got, SparkCellsRailMax)
	}
}

// The rail's rows carry what they name. A hit test that had to read a target
// back out of the drawn text would be parsing a clipped path out of a styled
// string with a stats field on the end of it — and would be wrong the first
// time a path was long enough to clip.
func TestInspectorRail_RowsCarryTheirTargets(t *testing.T) {
	r := fullRail()
	r.Agents = []InspectorAgent{
		{Name: "orchestrator", Detail: "idle", Self: true, Focused: true, State: FanoutIdle},
		{Name: "writer-1", Detail: "docs/loop.md", Spend: "$0.02", State: FanoutRunning},
	}
	rows := r.Rows(InspectorWidth, 0)

	// The headings name blocks rather than anything in them, and a session
	// takes two rows that both name that session.
	for _, c := range []struct {
		reads string
		want  RailTarget
	}{
		{"THIS TURN", RailTarget{}},
		{"CHANGES", RailTarget{}},
		{"AGENTS", RailTarget{}},
		{"CONTEXT", RailTarget{}},
		{"SPEND", RailTarget{}},
		{"go test ./...", RailTarget{}},
		{"writer-1", RailTarget{Kind: RailTargetSession, Name: "writer-1"}},
		{"docs/loop.md", RailTarget{Kind: RailTargetSession, Name: "writer-1"}},
	} {
		found := false
		for _, row := range rows {
			if !strings.Contains(stripANSI(row.Text), c.reads) {
				continue
			}
			found = true
			if row.Target != c.want {
				t.Fatalf("the row reading %q points at %+v, want %+v", c.reads, row.Target, c.want)
			}
		}
		if !found {
			t.Fatalf("no row reads %q:\n%s", c.reads, stripANSI(r.View(InspectorWidth, 0)))
		}
	}

	// A file row names its path whole, whatever the drawn row did to it.
	var files []RailTarget
	for _, row := range rows {
		if row.Target.Kind == RailTargetFile {
			files = append(files, row.Target)
		}
	}
	if len(files) != 2 || files[0].Name != "agent/loop.go" || files[1].Name != "ui/chat/model.go" {
		t.Fatalf("CHANGES should point at both paths in order, got %+v", files)
	}

}

// The rail's own session has no name to attach to, so its row's target is the
// empty one every host spells the orchestrator as.
func TestInspectorRail_TheOwnSessionRowNamesNoSession(t *testing.T) {
	r := InspectorRail{Agents: []InspectorAgent{
		{Name: "orchestrator", Self: true, Focused: true, State: FanoutRunning},
		{Name: "writer-1", State: FanoutRunning},
	}}
	for _, row := range r.Rows(InspectorWidth, 0) {
		if !strings.Contains(stripANSI(row.Text), "orchestrator") {
			continue
		}
		if want := (RailTarget{Kind: RailTargetSession}); row.Target != want {
			t.Fatalf("the orchestrator's row points at %+v, want %+v", row.Target, want)
		}
		return
	}
	t.Fatal("the map did not draw the orchestrator")
}

// A fold marker stands for rows that are not on screen, so it names none of
// them: a click on it would have to guess which.
func TestInspectorRail_FoldMarkersPointAtNothing(t *testing.T) {
	r := fullRail()
	rows := r.Rows(InspectorWidth, 12)
	marked := false
	for _, row := range rows {
		if !strings.Contains(stripANSI(row.Text), "more") {
			continue
		}
		marked = true
		if row.Target.Kind != RailTargetNone {
			t.Fatalf("a fold marker points at %+v, want nothing", row.Target)
		}
	}
	if !marked {
		t.Fatalf("nothing folded at 12 rows:\n%s", stripANSI(r.View(InspectorWidth, 12)))
	}
}

// Lines is Rows without the targets, and the two have to stay one rendering:
// a host that draws one and hit-tests the other would answer clicks against
// rows nobody is looking at.
func TestInspectorRail_LinesAreTheRowsText(t *testing.T) {
	for _, height := range []int{0, 14, 30} {
		r := fullRail()
		lines := r.Lines(InspectorWidth, height)
		rows := r.Rows(InspectorWidth, height)
		if len(lines) != len(rows) {
			t.Fatalf("height %d: %d lines against %d rows", height, len(lines), len(rows))
		}
		for i := range lines {
			if lines[i] != rows[i].Text {
				t.Fatalf("height %d row %d: %q against %q", height, i, lines[i], rows[i].Text)
			}
		}
	}
}
