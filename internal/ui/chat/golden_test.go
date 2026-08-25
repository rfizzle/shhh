package chat

// Golden-file render tests for the host surfaces (S-096): the step outline
// the transcript folds a turn into, and the prompt frame in each of its four
// layout modes. The component catalog's own captures live beside it in
// internal/ui/components.
//
// Regenerate after an intended change:
//
//	go test ./internal/ui/components ./internal/ui/chat -update-golden

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

func TestMain(m *testing.M) { os.Exit(golden.Run(m)) }

// goldenWidths are the terminal widths behind the breakpoints of
// guidelines/layout-breakpoints in the shhh Design System project. They are
// terminal columns, not content columns: the surface loses horizontalPadding
// on each side before any of these thresholds are read.
var goldenWidths = []int{60, 80, 110, 130}

// captureGolden renders one surface at every width in both palettes. The
// colour profile is forced because a test binary's stdout is not a terminal,
// so lipgloss would otherwise emit no escapes at all and the ansi block would
// be a copy of the layout block.
func captureGolden(t *testing.T, name, surface string, widths []int, panels func(width int) []golden.Panel) {
	t.Helper()
	was := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(was) })

	for _, mono := range []bool{false, true} {
		label := "color"
		if mono {
			label = "mono"
		}
		t.Run(label, func(t *testing.T) {
			monoRestore(t)
			components.SetMono(mono)
			for _, width := range widths {
				golden.Assert(t, name+".w"+strconv.Itoa(width), golden.Case{
					Surface: surface,
					Width:   width,
					Mono:    mono,
					Panels:  panels(width),
				})
			}
		})
	}
}

// goldenTranscript is a two-step turn: a batch that read and searched, then a
// batch that edited and broke a test, and the rows it closes with. It is the
// steps fixture with a folded read-only run added, so the outline, the group
// row, a failing step and the turn close (S-098) all appear in one capture.
func goldenTranscript() []entry {
	return []entry{
		{kind: entryUser, text: "fix the round limit"},
		{kind: entryAssistant, text: "Locate the round accounting"},
		{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"internal/agent/loop.go"}`,
			toolResult: "a\nb\nc", duration: 400 * time.Millisecond},
		{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"internal/agent/tools.go"}`,
			toolResult: "a\nb", duration: 300 * time.Millisecond},
		{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"internal/agent/context.go"}`,
			toolResult: "a", duration: 200 * time.Millisecond},
		{kind: entryTool, toolName: "search", toolArgs: `{"pattern":"ErrRoundLimit"}`,
			toolResult: "x\ny", duration: 300 * time.Millisecond},
		{kind: entryAssistant, text: "Thread the sentinel through the loop"},
		{kind: entryTool, toolName: "edit_file", toolArgs: `{"path":"internal/agent/loop.go"}`,
			toolResult: "edited", duration: 1100 * time.Millisecond},
		{kind: entryCommand, text: "go test ./internal/agent/...",
			toolResult: "--- FAIL: TestRoundLimit", exitCode: 1, duration: 21400 * time.Millisecond},
		{kind: entryTurnClose, close: &components.TurnClose{
			Steps: 2, Tools: 6, Elapsed: "24.7s", Spend: "$0.14", Note: "round 2/25",
			Changes: &components.TurnChanges{
				Files: 1, Added: 12, Removed: 4,
				Keys: []components.TurnKey{{Key: "[v]", Label: "review"}, {Key: "[u]", Label: "undo turn"}},
				Note: "all tracked in git",
			},
			Checks: &components.TurnChecks{
				Failed: true, Label: "go test ./internal/agent/...", Counts: "exit 1 · 21s",
			},
		}},
	}
}

// goldenModel is a ready model at one width with usage, pricing and a model
// name, so every vitals segment has something to show and nothing in the
// render depends on the clock.
func goldenModel(t *testing.T, width int) Model {
	t.Helper()
	m := frameModel(t, width, 40)
	m.transcript = goldenTranscript()
	m.invalidateRenderCache()
	return m
}

// TestGolden_StepOutline captures the transcript's step grammar (§6, S-090,
// S-091) at each breakpoint: the numbered headers with their state glyph and
// stats, the folded read-only group row, and the step that stays open because
// it contains a failure.
func TestGolden_StepOutline(t *testing.T) {
	captureGolden(t, "step-outline", "transcript step outline", goldenWidths, func(width int) []golden.Panel {
		m := goldenModel(t, width)
		normal := m.renderHistory()
		// Step 1 finished, so it collapsed to its header; opening it is what
		// puts the counted group row of S-091 on the sheet.
		m.toggleStepFold(1)
		m.invalidateRenderCache()
		opened := m.renderHistory()
		m.toggleStepFold(1)
		m.verbosity = verbosityHigh
		m.invalidateRenderCache()
		high := m.renderHistory()
		m.verbosity = verbosityLow
		m.invalidateRenderCache()
		low := m.renderHistory()
		return []golden.Panel{
			{Label: "verbosity · normal (a finished step collapses)", View: normal},
			{Label: "verbosity · normal, step 1 opened (read-only run folds to a group row)", View: opened},
			{Label: "verbosity · high (every row, with detail)", View: high},
			{Label: "verbosity · low (step headers only)", View: low},
		}
	})
}

// TestGolden_PromptFrame captures the command-center surface (§12, S-082) in
// each of its four layout modes. frameWidths adds a terminal too narrow for
// the frame at all, which the four breakpoints do not reach: below
// minFrameWidth content columns the frame degrades to the bare input, and
// that degradation is worth capturing too.
func TestGolden_PromptFrame(t *testing.T) {
	frameWidths := append([]int{14}, goldenWidths...)
	captureGolden(t, "prompt-frame", "prompt frame", frameWidths, func(width int) []golden.Panel {
		idle := goldenModel(t, width)
		working := goldenModel(t, width)
		working.state = stateStreaming
		return []golden.Panel{
			{Label: "state · idle", View: promptSurface(idle)},
			{Label: "state · working", View: promptSurface(working)},
		}
	})
}

// TestGolden_SurfacesNotYetBuilt keeps the surfaces S-096 asks for that
// nothing renders yet from being quietly forgotten. Review mode landed with
// S-099 and is captured beside the component catalog (review-mode.*); the
// fan-out block is S-110 and still has no renderer, so this skips with the
// reason rather than pretending the sheet is complete. Delete a line here
// when its story lands and the capture goes in beside the rest.
func TestGolden_SurfacesNotYetBuilt(t *testing.T) {
	for _, pending := range []string{
		"fan-out block — S-110, no renderer yet",
	} {
		t.Run(pending, func(t *testing.T) { t.Skip("no surface to capture yet") })
	}
}

// promptSurface is the bottom panel the product shows: the frame where it
// fits, and the plain input row where it does not (Model.View's
// frameShowing branch).
func promptSurface(m Model) string {
	if m.frameShowing() {
		return m.renderPromptFrame()
	}
	return m.input.View()
}

// widthsCoverEveryFrameLayout guards the fixture itself: the capture is only
// "all four layout modes" if the widths it uses actually reach all four.
func TestGolden_PromptFrameWidthsCoverEveryLayout(t *testing.T) {
	seen := map[frameLayout]int{}
	for _, width := range append([]int{14}, goldenWidths...) {
		m := frameModel(t, width, 40)
		seen[m.frameLayout()] = width
	}
	for _, want := range []frameLayout{framePlain, frameNarrow, frameCompact, frameWide} {
		if _, ok := seen[want]; !ok {
			t.Fatalf("the golden widths never produce layout %d; the capture claims four modes it does not have", want)
		}
	}
}
