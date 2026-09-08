package ui

// Golden-file renders of the one-shot generate UI. Every other test in this
// package asserts a substring — that the containment line is there, that `[u]`
// is offered — which is blind to the thing the artboard is actually about:
// which run on the screen is bold, which glyph leads the command, and which
// rung of the risk ladder a warning was drawn on. These capture the whole
// render, in colour and in mono, so a recoloured glyph is a failing test
// rather than something noticed three commits later.
//
// Regenerate after an intended change:
//
//	go test ./internal/ui -update-golden
//
// The four widths are the layout breakpoints. This surface measures nothing —
// it draws inline under the prompt it was typed at and asks the terminal for
// no size at all — so the four captures of a state agree, and that agreement
// is itself the record: a change that started reflowing the one-shot would
// break it.

import (
	"context"
	"os"
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

func TestMain(m *testing.M) { os.Exit(golden.Run(m)) }

// goldenWidths are the width breakpoints from docs/interface/principles.md#one-grid:
// minimal, folded, one-pane-with-vitals, and the two-pane split.
var goldenWidths = []int{60, 80, 110, 130}

// captureGolden renders one state at every width, in both palettes, and
// compares each against its checked-in file. view is called per width because
// that is the shape the other golden hosts have, and because a one-shot that
// grew a layout would want it.
func captureGolden(t *testing.T, name, surface string, view func(width int) []golden.Panel) {
	t.Helper()
	wasProfile := components.Profile()
	components.SetProfile(colorprofile.ANSI256)
	t.Cleanup(func() { components.SetProfile(wasProfile) })

	for _, mono := range []bool{false, true} {
		label := "color"
		if mono {
			label = "mono"
		}
		t.Run(label, func(t *testing.T) {
			was := components.Mono()
			components.SetMono(mono)
			t.Cleanup(func() { components.SetMono(was) })
			for _, width := range goldenWidths {
				golden.Assert(t, name+".w"+strconv.Itoa(width), golden.Case{
					Surface: surface,
					Width:   width,
					Mono:    mono,
					Panels:  view(width),
				})
			}
		})
	}
}

// The generation the goldens are taken of: the artboard's own question, with
// the sentence and the alternatives the model answered in the same response,
// so nothing here waits on a second request.
const goldenGeneration = "lsof -nP -iTCP -sTCP:LISTEN | awk '$9 ~ /:[89][0-9]{3}$/'\n" +
	"--- explanation\n" +
	"lsof lists listening TCP sockets without resolving names; awk keeps the rows whose address ends in a port from 8000-9999.\n" +
	"--- alternatives\n" +
	"ss -lntp 'sport > :8000'\n" +
	"# linux only, and faster on a busy machine\n" +
	"netstat -anv -p tcp\n" +
	"# everywhere, including a machine with no lsof\n"

// goldenDestructive is the command the safe default moves for: safety flags it,
// the radius resolves HIGH, and enter stops running it.
const goldenDestructive = "find ~/src -name node_modules -type d -prune -exec rm -rf {} +\n" +
	"--- explanation\n" +
	"-prune stops find from descending into a directory it just deleted.\n"

// goldenArmed streams one generation to completion and hands back the result
// surface it lands on.
func goldenArmed(t *testing.T, generation string) GenerateModel {
	t.Helper()
	m := NewGenerateModel(makeEvents(generation), noopCancel, nil, nil, nil, "")
	return drainStream(m, 2)
}

// The command still arriving: the wait with nothing to show yet, and the
// partial line once tokens are landing. Both are the streaming view, which is
// where the glyph has to lead the command as much as it does on the result.
func TestGolden_Generating(t *testing.T) {
	captureGolden(t, "one-shot-generating", "the one-shot while the command arrives",
		func(int) []golden.Panel {
			waiting := NewGenerateModel(make(chan provider.StreamEvent), noopCancel, nil, nil, nil, "")
			partial := NewGenerateModel(makeEvents("lsof -nP -iTCP"), noopCancel, nil, nil, nil, "")
			partial = drainStreamPending(partial, 1)
			return []golden.Panel{
				{Label: "waiting on the first token", View: waiting.View().Content},
				{Label: "part-way through the stream", View: partial.View().Content},
			}
		})
}

// The result surface: the command, the sentence under it, the containment
// line, and the key row whose one Add is the key that runs.
func TestGolden_Result(t *testing.T) {
	captureGolden(t, "one-shot-result", "the one-shot result",
		func(int) []golden.Panel {
			m := goldenArmed(t, goldenGeneration)
			return []golden.Panel{{View: m.View().Content}}
		})
}

// The destructive command: the same keys, with enter spent on saying what
// would be affected and the run behind a deliberate `y`. The warning is on the
// top rung of the ladder, which is the one place the one-shot shouts.
func TestGolden_Destructive(t *testing.T) {
	captureGolden(t, "one-shot-destructive", "the one-shot result on a destructive command",
		func(int) []golden.Panel {
			m := goldenArmed(t, goldenDestructive)
			affected, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			return []golden.Panel{
				{Label: "the safe default", View: m.View().Content},
				{Label: "what enter buys", View: settle(affected.(GenerateModel), cmd).View().Content},
			}
		})
}

// The revise ladder: the command being compared against, dimmed above the one
// that answers the feedback.
func TestGolden_Revise(t *testing.T) {
	captureGolden(t, "one-shot-revise", "the one-shot revise ladder",
		func(int) []golden.Panel {
			return []golden.Panel{{View: goldenRevised(t).View().Content}}
		})
}

// The alternatives the generation offered, the one on screen marked.
func TestGolden_Alternatives(t *testing.T) {
	captureGolden(t, "one-shot-alternatives", "the one-shot alternatives picker",
		func(int) []golden.Panel {
			m := goldenArmed(t, goldenGeneration)
			m, _ = m.openAlternatives()
			return []golden.Panel{{View: m.View().Content}}
		})
}

// goldenRevised takes the result surface through one revise, so the render
// carries the rung above it as well as the answer.
func goldenRevised(t *testing.T) GenerateModel {
	t.Helper()
	revised := "lsof -nP -iTCP -sTCP:LISTEN -u $(id -un) -Fpcn\n" +
		"--- explanation\n" +
		"-u filters to your uid; -F prints pid, command and name as parseable fields.\n"
	newStream := func([]provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		return makeEvents(revised), noopCancel, nil
	}
	m := NewGenerateModel(makeEvents(goldenGeneration), noopCancel, nil, newStream, nil, "")
	m = drainStream(m, 2)
	m = step(m, tea.KeyPressMsg{Code: 'r', Text: "r"})
	for _, r := range "only ones owned by me, and show the pid" {
		m = step(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	return drainStream(m, 2)
}
