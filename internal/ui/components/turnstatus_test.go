package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// liveStatus is the fully-populated live line every drop-order test starts
// from: every field present, so what a narrower width removes is visible.
func liveStatus() TurnStatus {
	return TurnStatus{
		Phase: PhaseRunning, Tool: "go test",
		Elapsed: "12.4s", Up: "41.2k", Down: "2.1k", Cost: "$0.06",
	}
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
		// rendering blank or inventing a fifth (§8d).
		{TurnPhase(42), "thinking…"},
	}
	for _, c := range cases {
		if got := c.phase.Word(); got != c.want {
			t.Fatalf("phase %d word = %q, want %q", c.phase, got, c.want)
		}
	}
}

// The whole ladder in one table: what each width leaves, in the order §8d
// says fields leave — tool argument, token counts, elapsed — with the phase
// and the cost still standing at the floor.
func TestTurnStatus_DropOrder(t *testing.T) {
	s := liveStatus()
	full := plainStatus(s, 200)
	if want := "⠋ running go test 12.4s · ↑41.2k ↓2.1k · $0.06"; full != want {
		t.Fatalf("full line = %q, want %q", full, want)
	}
	for _, c := range []struct {
		width int
		want  string
	}{
		{lipgloss.Width(full), "⠋ running go test 12.4s · ↑41.2k ↓2.1k · $0.06"},
		{lipgloss.Width(full) - 1, "⠋ running 12.4s · ↑41.2k ↓2.1k · $0.06"},
		{30, "⠋ running 12.4s · $0.06"},
		{20, "⠋ running · $0.06"},
	} {
		if got := plainStatus(s, c.width); got != c.want {
			t.Fatalf("at width %d = %q, want %q", c.width, got, c.want)
		}
	}
}

func TestTurnStatus_PhaseAndCostNeverDrop(t *testing.T) {
	s := liveStatus()
	for width := 1; width <= 60; width++ {
		got := plainStatus(s, width)
		if lipgloss.Width(got) > width {
			t.Fatalf("width %d overflowed: %q", width, got)
		}
		if width < lipgloss.Width(plainStatus(s, 200)) && width >= 18 {
			if !strings.Contains(got, "running") || !strings.Contains(got, "$0.06") {
				t.Fatalf("width %d dropped the phase or the cost: %q", width, got)
			}
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

// Both counts or neither: one arrow alone is half a fact.
func TestTurnStatus_TokenCountsTravelTogether(t *testing.T) {
	s := liveStatus()
	s.Down = ""
	if got := plainStatus(s, 200); strings.Contains(got, "↑") {
		t.Fatalf("half a token count rendered: %q", got)
	}
}

// The tool argument belongs to `running` and to nothing else.
func TestTurnStatus_ToolNamesOnlyTheRunningPhase(t *testing.T) {
	s := liveStatus()
	s.Phase = PhaseStreaming
	if got := plainStatus(s, 200); strings.Contains(got, "go test") {
		t.Fatalf("streaming named a tool: %q", got)
	}
}

func TestTurnStatus_ResolvesIntoTheSummary(t *testing.T) {
	cases := []struct {
		outcome TurnState
		want    string
	}{
		{TurnDone, "✓ done · 1m 04s · 18 tools · $0.14"},
		{TurnCancelled, "⊘ cancelled · 1m 04s · 18 tools · $0.14"},
		{TurnFailed, "✗ failed · 1m 04s · 18 tools · $0.14"},
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
		{34, "✓ done · 1m 04s · 18 tools · $0.14"},
		{24, "✓ done · 1m 04s · $0.14"},
		{16, "✓ done · $0.14"},
	} {
		if got := plainStatus(s, c.width); got != c.want {
			t.Fatalf("resolved at width %d = %q, want %q", c.width, got, c.want)
		}
	}
}

// The frame index comes from the host's one tick source (§10c), so the same
// frame drives this line and every other spinner on screen.
func TestTurnStatus_FrameFollowsTheTickSource(t *testing.T) {
	for i := range SpinnerFrames {
		s := TurnStatus{Phase: PhaseThinking, Frame: i}
		if got := plainStatus(s, 40); !strings.HasPrefix(got, SpinnerFrames[i]) {
			t.Fatalf("frame %d rendered %q, want it to lead with %q", i, got, SpinnerFrames[i])
		}
	}
}
