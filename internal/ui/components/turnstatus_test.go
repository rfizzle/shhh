package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// liveStatus is the fullest live line every drop-order test starts from —
// the phase and the turn's elapsed — so what a narrower width removes is
// visible. There is no third field: the call the phase is for is a row in the
// feed and not a copy on this line.
func liveStatus() TurnStatus {
	return TurnStatus{Phase: PhaseRunning, Elapsed: "12.4s"}
}

func plainStatus(s TurnStatus, width int) string { return ansi.Strip(s.View(width)) }

func TestTurnStatus_PhaseVocabularyIsClosed(t *testing.T) {
	cases := []struct {
		phase TurnPhase
		want  string
	}{
		{PhaseThinking, "thinking…"},
		{PhaseDeciding, "deciding…"},
		{PhaseRunning, "running"},
		{PhaseStreaming, "streaming…"},
		// A phase nobody defined picks the nearest of the four rather than
		// rendering blank or inventing a fifth.
		{TurnPhase(42), "thinking…"},
	}
	for _, c := range cases {
		if got := c.phase.Word(); got != c.want {
			t.Fatalf("phase %d word = %q, want %q", c.phase, got, c.want)
		}
	}
}

// The whole ladder in one table: what each width leaves, with the phase still
// standing at the floor. The live line has one field to shed now — the
// elapsed — and it sheds it last of all.
func TestTurnStatus_DropOrder(t *testing.T) {
	s := liveStatus()
	full := plainStatus(s, 200)
	if want := "⠋ running · turn 12.4s"; full != want {
		t.Fatalf("full line = %q, want %q", full, want)
	}
	for _, c := range []struct {
		width int
		want  string
	}{
		{lipgloss.Width(full), "⠋ running · turn 12.4s"},
		{lipgloss.Width(full) - 1, "⠋ running"},
		{14, "⠋ running"},
	} {
		if got := plainStatus(s, c.width); got != c.want {
			t.Fatalf("at width %d = %q, want %q", c.width, got, c.want)
		}
	}
}

func TestTurnStatus_PhaseNeverDrops(t *testing.T) {
	s := liveStatus()
	for width := 1; width <= 60; width++ {
		got := plainStatus(s, width)
		if lipgloss.Width(got) > width {
			t.Fatalf("width %d overflowed: %q", width, got)
		}
		if width >= 9 && !strings.Contains(got, "running") {
			t.Fatalf("width %d dropped the phase: %q", width, got)
		}
	}
}

// A slot too small even for the floor still says what it is doing, clipped —
// rendering nothing there would be worse than rendering less.
func TestTurnStatus_ClipsRatherThanVanishes(t *testing.T) {
	if got := plainStatus(liveStatus(), 6); got == "" || lipgloss.Width(got) > 6 {
		t.Fatalf("narrow slot = %q, want a clipped line of at most 6 columns", got)
	}
	if got := liveStatus().View(0); got != "" {
		t.Fatalf("a slot with no room = %q, want nothing", got)
	}
}

// The cost belongs to the resolved line and to nothing else. A turn in
// flight can only be priced at the fresh input rate, which charges every
// cached prompt read as if it were new, so a host that fills the field early
// gets no dollars for it (docs/interface/surfaces.md#the-input-frame).
func TestTurnStatus_TheRunningLineStatesNoCost(t *testing.T) {
	s := liveStatus()
	s.Cost = "$0.06"
	if got := plainStatus(s, 200); strings.Contains(got, "$") {
		t.Fatalf("a turn still running was priced: %q", got)
	}
	s.Done, s.Duration, s.Tools = true, "12.4s", 18
	if got := plainStatus(s, 200); !strings.Contains(got, "$0.06") {
		t.Fatalf("the resolved line should carry what the turn was billed: %q", got)
	}
}

// Both forms of the line say whose clock they are stating. The feed under
// them carries a clock per row and a ticking one on the command in flight, so
// an unlabelled figure here is a second reading of an operation the reader is
// already watching (docs/interface/surfaces.md#the-input-frame).
func TestTurnStatus_TheClockSaysItIsTheTurns(t *testing.T) {
	if got := plainStatus(liveStatus(), 200); !strings.Contains(got, "turn 12.4s") {
		t.Fatalf("the running line's elapsed is unlabelled: %q", got)
	}
	done := TurnStatus{Done: true, Duration: "1m 04s", Tools: 18, Cost: "$0.14"}
	if got := plainStatus(done, 200); !strings.Contains(got, "turn 1m 04s") {
		t.Fatalf("the resolved line's duration is unlabelled: %q", got)
	}
}

func TestTurnStatus_ResolvesIntoTheSummary(t *testing.T) {
	cases := []struct {
		outcome TurnState
		want    string
	}{
		{TurnDone, "✓ done · turn 1m 04s · 18 tools · $0.14"},
		{TurnCancelled, "⊘ cancelled · turn 1m 04s · 18 tools · $0.14"},
		{TurnFailed, "✗ failed · turn 1m 04s · 18 tools · $0.14"},
	}
	for _, c := range cases {
		s := TurnStatus{Done: true, Outcome: c.outcome, Duration: "1m 04s", Tools: 18, Cost: "$0.14"}
		if got := plainStatus(s, 200); got != c.want {
			t.Fatalf("resolved %d = %q, want %q", c.outcome, got, c.want)
		}
	}
}

func TestTurnStatus_ResolvedLineDropsInTheSameOrder(t *testing.T) {
	s := TurnStatus{Done: true, Duration: "1m 04s", Tools: 18, Cost: "$0.14"}
	for _, c := range []struct {
		width int
		want  string
	}{
		{39, "✓ done · turn 1m 04s · 18 tools · $0.14"},
		{29, "✓ done · turn 1m 04s · $0.14"},
		{16, "✓ done · $0.14"},
	} {
		if got := plainStatus(s, c.width); got != c.want {
			t.Fatalf("resolved at width %d = %q, want %q", c.width, got, c.want)
		}
	}
}

// The frame index comes from the host's one tick source, so the same
// frame drives this line and every other spinner on screen.
func TestTurnStatus_FrameFollowsTheTickSource(t *testing.T) {
	for i := range SpinnerFrames {
		s := TurnStatus{Phase: PhaseThinking, Frame: i}
		if got := plainStatus(s, 40); !strings.HasPrefix(got, SpinnerFrames[i]) {
			t.Fatalf("frame %d rendered %q, want it to lead with %q", i, got, SpinnerFrames[i])
		}
	}
}
