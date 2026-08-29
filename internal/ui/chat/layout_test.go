package chat

// Tests for the surface's rectangle model (S-161, layout.go). The claim they
// stand behind is the one the arithmetic could never make: whatever the turn
// is doing, the rows add up to the terminal exactly once, and nothing a
// renderer hands over can reach past the rectangle it was given.

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// layoutStates is one model per shape of the pane the layout has to budget
// for: nothing under the transcript, and each of the live tails that can sit
// beneath it. The working children's rows are the fourth shape and are
// covered where the supervisor that produces them already is
// (subagents_test.go).
func layoutStates(t *testing.T, width, height int) map[string]Model {
	t.Helper()
	base := func() Model {
		m := frameModel(t, width, height)
		m.transcript = goldenTranscript()
		m.invalidateRenderCache()
		m.syncViewport()
		return m
	}
	out := map[string]Model{}

	out["idle"] = base()

	thinking := base()
	thinking.state = stateStreaming
	thinking.streaming = ""
	out["thinking"] = thinking

	classifying := base()
	classifying.state = stateClassifying
	out["classifying"] = classifying

	running := base()
	running.state = stateRunningCmd
	running.runningCommand = "go test ./internal/agent/..."
	running.runStart = time.Now()
	out["running a command"] = running

	// The two-row one: a countdown meter and the offers under it (§17a).
	waiting := base()
	waiting.state = stateRetryWait
	waiting.retry = &retryWait{
		fail:     &provider.Failure{Class: provider.ClassRateLimit, Message: "slow down"},
		attempt:  2,
		max:      5,
		wait:     30 * time.Second,
		deadline: time.Now().Add(18 * time.Second),
		fallback: "gpt-4o-mini",
	}
	out["waiting to retry"] = waiting

	return out
}

// TestLayout_RowsAddUpToTheTerminal is the defect that motivated the model
// (§10n): the live tail under the transcript was drawn on a row nothing had
// budgeted for, so the surface ran one row past the bottom of the terminal
// and the frame's closing rail went with it.
func TestLayout_RowsAddUpToTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{60, 20}, {80, 30}, {110, 24}, {130, 40}, {144, 30}} {
		for name, m := range layoutStates(t, size[0], size[1]) {
			view := m.View().Content
			lines := strings.Split(view, "\n")
			if len(lines) != m.height {
				t.Errorf("%dx%d %s: surface is %d rows, terminal is %d",
					size[0], size[1], name, len(lines), m.height)
			}
			for i, line := range lines {
				if w := lipgloss.Width(line); w != m.width {
					t.Errorf("%dx%d %s: row %d is %d columns, terminal is %d",
						size[0], size[1], name, i, w, m.width)
				}
			}
		}
	}
}

// TestLayout_VerticalSplitIsOneDivision says the same thing about the
// rectangles rather than the render: the four segments of the terminal's rows
// are a partition of it, and the pane's three are a partition of the body.
func TestLayout_VerticalSplitIsOneDivision(t *testing.T) {
	for name, m := range layoutStates(t, 110, 30) {
		s := m.surface()
		rows := s.header.Dy() + s.rail.Dy() + s.body.Dy() + s.bottom.Dy()
		if rows != m.height {
			t.Errorf("%s: header+rail+body+bottom = %d rows, terminal is %d", name, rows, m.height)
		}
		if inner := s.view.Dy() + s.tail.Dy() + s.agents.Dy(); inner != s.body.Dy() {
			t.Errorf("%s: view+tail+agents = %d rows, body is %d", name, inner, s.body.Dy())
		}
		if s.tail.Dy() != m.liveTailHeight() {
			t.Errorf("%s: the tail's rows (%d) are not what it asked for (%d)",
				name, s.tail.Dy(), m.liveTailHeight())
		}
	}
}

// TestLayout_RetryWaitIsPaidForByTheRowsItTakes is the specific rung that was
// wrong: the countdown is a meter *and* the offers under it, and a constant
// saying it was one row is how the surface came to overrun the terminal.
func TestLayout_RetryWaitIsPaidForByTheRowsItTakes(t *testing.T) {
	m := layoutStates(t, 110, 30)["waiting to retry"]
	tail := m.liveTail(m.paneWidth())
	if drawn := lipgloss.Height(tail); drawn != 2 {
		t.Fatalf("the retry countdown draws %d rows, expected 2:\n%s", drawn, tail)
	}
	if m.liveTailHeight() != lipgloss.Height(tail) {
		t.Fatalf("the layout budgeted %d rows for a block that draws %d",
			m.liveTailHeight(), lipgloss.Height(tail))
	}
}

// TestLayout_ColumnsMatchTheWidthLadder walks the rung the inspector rail
// hangs on (§8c, §15): below it the pane is the whole content, at or above
// it the rail and its divider take their columns off the right, and the
// scroll gutter's column comes off the pane either way (§10g).
func TestLayout_ColumnsMatchTheWidthLadder(t *testing.T) {
	for _, width := range []int{60, 80, 110, 130, 134, 144} {
		m := frameModel(t, width, 30)
		c := m.columns()
		if want := width - horizontalPadding*2; c.content.Dx() != want {
			t.Errorf("width %d: content is %d columns, want %d", width, c.content.Dx(), want)
		}
		split := c.content.Dx() >= components.InspectorMinContentWidth
		if m.twoPane() != split {
			t.Errorf("width %d: twoPane = %v, want %v", width, m.twoPane(), split)
		}
		wantPane := c.content.Dx()
		if split {
			wantPane -= components.InspectorWidth + paneDividerWidth
			if c.inspector.Dx() != components.InspectorWidth || c.divider.Dx() != paneDividerWidth {
				t.Errorf("width %d: rail %d + divider %d columns", width, c.inspector.Dx(), c.divider.Dx())
			}
		}
		if c.pane.Dx() != wantPane {
			t.Errorf("width %d: pane is %d columns, want %d", width, c.pane.Dx(), wantPane)
		}
		if c.feed.Dx()+c.gutter.Dx() != c.pane.Dx() {
			t.Errorf("width %d: feed %d + gutter %d is not the pane's %d",
				width, c.feed.Dx(), c.gutter.Dx(), c.pane.Dx())
		}
		if c.gutter.Dx() != components.ScrollGutterWidth {
			t.Errorf("width %d: the gutter is %d columns, want %d",
				width, c.gutter.Dx(), components.ScrollGutterWidth)
		}
	}
}

// TestLayout_TinyTerminalHasNoNegativeColumns: the arithmetic this replaced
// could hand a renderer a negative width, and the first strings.Repeat past
// it would have panicked. A rectangle cannot be smaller than nothing.
func TestLayout_TinyTerminalHasNoNegativeColumns(t *testing.T) {
	for width := 0; width < 8; width++ {
		m := frameModel(t, width, 6)
		c := m.columns()
		for label, r := range map[string]uv.Rectangle{
			"content": c.content, "pane": c.pane, "feed": c.feed, "gutter": c.gutter,
		} {
			if r.Dx() < 0 {
				t.Errorf("width %d: %s is %d columns", width, label, r.Dx())
			}
		}
		if m.contentWidth() < 0 {
			t.Errorf("width %d: contentWidth is %d", width, m.contentWidth())
		}
		// It must also still render rather than panicking on its own chrome.
		_ = m.View().Content
	}
}

// TestLayout_DrawInClipsToItsRectangle is the property the string
// concatenation never had: a block that does not fit is cut at the edge it
// was drawn against, in both directions, and one that is smaller leaves the
// rest of the rectangle alone.
func TestLayout_DrawInClipsToItsRectangle(t *testing.T) {
	scr := uv.NewScreenBuffer(6, 3)
	drawIn(scr, "..........\n..........\n..........\n..........", uv.Rect(1, 1, 3, 1))
	got := strings.Split(renderScreen(scr), "\n")
	want := []string{"      ", " ...  ", "      "}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %q, want %q (whole screen: %q)", i, got[i], want[i], got)
		}
	}
}

// TestLayout_TranscriptOriginIsThePanesCorner: the pointer and the render
// read the same rectangle, so a click can never name a row the transcript is
// not drawn on (S-145, S-159).
func TestLayout_TranscriptOriginIsThePanesCorner(t *testing.T) {
	for _, width := range []int{80, 144} {
		m := frameModel(t, width, 30)
		s := m.surface()
		at := m.transcriptOrigin()
		if at.X != s.pane.Min.X || at.Y != s.view.Min.Y {
			t.Errorf("width %d: origin %v, pane starts at x=%d and the view at y=%d",
				width, at, s.pane.Min.X, s.view.Min.Y)
		}
	}
}
