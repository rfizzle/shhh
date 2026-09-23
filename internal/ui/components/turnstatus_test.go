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
	return TurnStatus{Phase: PhaseActing, Elapsed: "12.4s"}
}

func plainStatus(s TurnStatus, width int) string { return ansi.Strip(s.View(width)) }

func TestTurnStatus_PhaseVocabularyIsClosed(t *testing.T) {
	cases := []struct {
		phase TurnPhase
		want  string
	}{
		{PhaseThinking, "thinking…"},
		{PhaseDeciding, "deciding…"},
		{PhaseActing, "acting…"},
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
	if want := "⠋ acting… · turn 12.4s"; full != want {
		t.Fatalf("full line = %q, want %q", full, want)
	}
	for _, c := range []struct {
		width int
		want  string
	}{
		{lipgloss.Width(full), "⠋ acting… · turn 12.4s"},
		{lipgloss.Width(full) - 1, "⠋ acting…"},
		{14, "⠋ acting…"},
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
		if width >= 9 && !strings.Contains(got, "acting…") {
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

// The running line says whose clock it is stating. The feed under it carries
// a clock per row and a ticking one on the command in flight, so an
// unlabelled figure here is a second reading of an operation the reader is
// already watching (docs/interface/surfaces.md#the-input-frame).
func TestTurnStatus_TheClockSaysItIsTheTurns(t *testing.T) {
	if got := plainStatus(liveStatus(), 200); !strings.Contains(got, "turn 12.4s") {
		t.Fatalf("the running line's elapsed is unlabelled: %q", got)
	}
}

// The resolved line is the outcome and nothing after it. The stopped clock,
// the tools the turn ran and what it was billed are the close row's, which
// the turn leaves a few rows above this line and which still states them when
// the turn has scrolled away (docs/interface/surfaces.md#the-input-frame).
func TestTurnStatus_ResolvesIntoTheOutcome(t *testing.T) {
	cases := []struct {
		outcome TurnState
		want    string
	}{
		{TurnDone, "✓ done"},
		{TurnCancelled, "⊘ cancelled"},
		{TurnFailed, "✗ failed"},
	}
	for _, c := range cases {
		// A host that leaves the clock filled in does not get it restated.
		s := TurnStatus{Done: true, Outcome: c.outcome, Elapsed: "1m 04s"}
		if got := plainStatus(s, 200); got != c.want {
			t.Fatalf("resolved %d = %q, want %q", c.outcome, got, c.want)
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
