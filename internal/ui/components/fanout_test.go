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

// TestFanoutProgressNeedsADeclaredCount is S-094's rule on a lane: a bar only
// where the spawn declared a step count, the spinner everywhere else, and
// never a ratio nobody supplied.
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
// what it found, and stops drawing progress that no longer measures anything.
func TestFanoutFinishedLaneKeepsItsResult(t *testing.T) {
	done := FanoutLane{State: FanoutDone, Name: "tester-4", Task: "internal/agent tests",
		Step: 2, Steps: 5, Tools: 9, Spend: "$0.03", Elapsed: "41s",
		Summary: "all four packages pass"}
	view := ansi.Strip(done.View(110))
	if !strings.Contains(view, "✓") || !strings.Contains(view, "done") {
		t.Fatalf("a finished lane should state its outcome in glyph and word: %q", view)
	}
	if !strings.Contains(view, "all four packages pass") {
		t.Fatalf("a finished lane should keep its result summary: %q", view)
	}
	if strings.Contains(view, "2/5") {
		t.Fatalf("a finished lane is still drawing progress: %q", view)
	}

	failed := FanoutLane{State: FanoutFailed, Name: "patcher-5", Summary: "round limit (25) reached"}
	fview := ansi.Strip(failed.View(110))
	if !strings.Contains(fview, "✗") || !strings.Contains(fview, "failed") {
		t.Fatalf("a broken lane should state its outcome in glyph and word: %q", fview)
	}
	if !strings.Contains(fview, "round limit") {
		t.Fatalf("a broken lane should say why: %q", fview)
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
