package chat

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// frameModel is a ready model with usage, pricing, and a model name so every
// cockpit segment has something to show.
func frameModel(t *testing.T, width, height int) Model {
	t.Helper()
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	table := pricing.NewTable(map[string]pricing.ModelPricing{
		"gpt-4o": {InputCostPerToken: 0.00001, OutputCostPerToken: 0.00001},
	})
	m := New(msgs, mockStream).WithPricing(table, "gpt-4o")
	m.accumulateUsage(&provider.Usage{PromptTokens: 41200, CompletionTokens: 9800})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(Model)
}

func TestFrameLayoutFor(t *testing.T) {
	cases := []struct {
		width int
		want  frameLayout
	}{
		{11, framePlain}, {12, frameNarrow}, {69, frameNarrow},
		{70, frameCompact}, {109, frameCompact}, {110, frameWide},
	}
	for _, c := range cases {
		if got := frameLayoutFor(c.width); got != c.want {
			t.Fatalf("frameLayoutFor(%d) = %d, want %d", c.width, got, c.want)
		}
	}
}

func TestFrame_WideTwoRails(t *testing.T) {
	m := frameModel(t, 130, 40) // content 126 ≥ 110
	view := stripANSI(m.View().Content)

	for _, want := range []string{"╭─ shhh chat", "├─", "╰─", "⏸ manual", "ctx ", "↑41.2k ↓9.8k", "$0.51", "gpt-4o", "enter send · shift+enter newline · / commands · ctrl+v attach · ctrl+k palette · shift+tab mode", "idle"} {
		if !strings.Contains(view, want) {
			t.Fatalf("wide frame missing %q:\n%s", want, view)
		}
	}
}

func TestFrame_CompactSingleRail(t *testing.T) {
	m := frameModel(t, 100, 40) // content 96 → compact
	view := stripANSI(m.View().Content)

	if strings.Contains(view, "├─") {
		t.Fatalf("compact frame must not have a dedicated vitals rail:\n%s", view)
	}
	for _, want := range []string{"╭─ shhh chat", "╰─", "⏸ manual", "ctx "} {
		if !strings.Contains(view, want) {
			t.Fatalf("compact frame missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "enter send") {
		t.Fatalf("compact frame should drop the hints rail:\n%s", view)
	}
}

func TestFrame_NarrowMinimalRail(t *testing.T) {
	m := frameModel(t, 60, 30) // content 56 → narrow
	view := stripANSI(m.View().Content)

	for _, want := range []string{"╭─", "⏸ manual", "$0.51"} {
		if !strings.Contains(view, want) {
			t.Fatalf("narrow frame missing %q:\n%s", want, view)
		}
	}
	// Model detail and token counts drop first (COCKPIT_SPEC.md §3); the
	// narrow rail keeps only the never-dropped fields.
	if strings.Contains(view, "gpt-4o") || strings.Contains(view, "↑41.2k") {
		t.Fatalf("narrow frame must drop model detail and token counts:\n%s", view)
	}
}

func TestFrame_PlainBelowMinWidth(t *testing.T) {
	m := frameModel(t, 14, 30) // content 10 < minFrameWidth
	view := stripANSI(m.View().Content)

	if strings.Contains(view, "╭") {
		t.Fatalf("sub-minimum widths must degrade to plain rows:\n%s", view)
	}
}

func TestFrame_GutterAndHintsSwapWhileWorking(t *testing.T) {
	m := frameModel(t, 130, 40)
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "│ ❯ ") {
		t.Fatalf("idle frame missing the ❯ gutter:\n%s", view)
	}

	m.state = stateStreaming
	view = stripANSI(m.View().Content)
	// The activity slot is the running turn's status line now (S-118, §8d):
	// `WORKING` was true of every moment of every turn and said nothing.
	if !strings.Contains(view, "│ ▸ ") || !strings.Contains(view, "thinking…") {
		t.Fatalf("working frame missing the steering gutter and the turn status:\n%s", view)
	}
	if !strings.Contains(view, "enter queues steering · / commands · ctrl+c cancel") {
		t.Fatalf("working frame missing the steering hints:\n%s", view)
	}
	if strings.Contains(view, "enter send") {
		t.Fatalf("working frame should swap out the idle hints:\n%s", view)
	}
}

func TestFrame_NoticeRailAppearsAndCounts(t *testing.T) {
	m := frameModel(t, 100, 40)
	base := m.viewport.Height()
	if strings.Contains(stripANSI(m.View().Content), "update:") {
		t.Fatal("no notice rail expected on a quiet session")
	}

	m = m.WithUpdateNotice("update: v9.9.9")
	m.syncViewport()
	if !strings.Contains(stripANSI(m.View().Content), "update: v9.9.9") {
		t.Fatal("the notice rail should carry the update notice")
	}
	if m.viewport.Height() != base-1 {
		t.Fatalf("the notice rail must shrink the viewport (%d -> %d)", base, m.viewport.Height())
	}

	m.steering = []string{"one", "two"}
	if !strings.Contains(stripANSI(m.View().Content), "2 steering queued") {
		t.Fatal("the notice rail should show the queued steering count")
	}
}

func TestFrame_DenialNoticeClearsOnNextTurn(t *testing.T) {
	m := frameModel(t, 100, 40)
	m.denialNotice = "rm -rf /tmp/x"
	if !strings.Contains(stripANSI(m.View().Content), "auto denied: rm -rf /tmp/x") {
		t.Fatal("the notice rail should show the last auto-mode denial")
	}

	m.input.SetValue("try something else")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.denialNotice != "" {
		t.Fatal("a fresh user turn must clear the denial notice")
	}
}

func TestFrame_TakeoverKeepsPlainStack(t *testing.T) {
	m := frameModel(t, 100, 40)
	m.pendingRun = "echo hi"
	m.state = stateConfirmRun
	m.syncViewport()
	// Ungated the card rides above a live frame (S-117, §7b); it takes the
	// panel only once the decision holds the keyboard.
	ungated := stripANSI(m.View().Content)
	if !strings.Contains(ungated, "╭─ shhh chat") {
		t.Fatalf("an ungated decision leaves the draft its frame:\n%s", ungated)
	}
	m = handover(t, m)
	view := stripANSI(m.View().Content)
	if strings.Contains(view, "╭─ shhh chat") {
		t.Fatalf("takeover surfaces must replace the frame:\n%s", view)
	}
	if !strings.Contains(view, "⏸ manual") {
		t.Fatalf("takeover surfaces keep the status bar:\n%s", view)
	}
	if !strings.Contains(view, "echo hi") {
		t.Fatalf("approval card missing:\n%s", view)
	}
}

func TestFrame_WideViewportAccounting(t *testing.T) {
	m := frameModel(t, 130, 40)
	// The wide layout adds one dedicated vitals rail beyond the standard
	// chrome rows.
	if want := 40 - inputHeight - chromeHeight - 1; m.viewport.Height() != want {
		t.Fatalf("wide viewport height = %d, want %d", m.viewport.Height(), want)
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(Model)
	if want := 40 - inputHeight - chromeHeight; m.viewport.Height() != want {
		t.Fatalf("compact viewport height = %d, want %d", m.viewport.Height(), want)
	}
}

func TestFrame_CompletionMenuInsideFrame(t *testing.T) {
	m := typeChars(t, readyModel(t), "/mo")
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "╭─ shhh chat") {
		t.Fatalf("the frame should stay up while the completion menu is open:\n%s", view)
	}
	if !strings.Contains(view, "/model") || !strings.Contains(view, "tab complete") {
		t.Fatalf("the completion menu should render inside the frame:\n%s", view)
	}
}

func TestFrame_AttachedShowsChildGutterAndVitals(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup) // 100x40 → compact layout
	spawnBlockedChild(t, sup)
	m.attach("researcher-1")

	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "orchestrator ▸ researcher-1") {
		t.Fatalf("attached top rail missing the breadcrumb:\n%s", view)
	}
	if !strings.Contains(view, "│ researcher-1 ❯ ") {
		t.Fatalf("attached gutter should carry the child's name:\n%s", view)
	}
	if !strings.Contains(view, "esc detach · ctrl+a agents") {
		t.Fatalf("attached frame missing the detach hints:\n%s", view)
	}
}

func TestFitRail_DropOrder(t *testing.T) {
	segs := []components.RailSegment{
		{Text: "MODE", Drop: components.RailKeep},
		{Text: "CTX", Drop: components.RailVital},
		{Text: "ROUND", Drop: components.RailNormal},
		{Text: "TOKENS", Drop: components.RailTokens},
		{Text: "MODEL", Drop: components.RailDetail},
	}
	full := components.FitRail(segs, " · ", 200)
	for _, want := range []string{"MODE", "CTX", "ROUND", "TOKENS", "MODEL"} {
		if !strings.Contains(full, want) {
			t.Fatalf("nothing should drop at full width, missing %q in %q", want, full)
		}
	}

	tight := components.FitRail(segs, " · ", 20) // fits MODE · CTX · ROUND
	if strings.Contains(tight, "MODEL") || strings.Contains(tight, "TOKENS") {
		t.Fatalf("model detail and tokens must drop first, got %q", tight)
	}
	for _, want := range []string{"MODE", "CTX"} {
		if !strings.Contains(tight, want) {
			t.Fatalf("context pressure must survive, missing %q in %q", want, tight)
		}
	}
}

func TestFrame_RowsAlignAtEveryLayout(t *testing.T) {
	for _, width := range []int{130, 100, 74, 60, 20} {
		m := frameModel(t, width, 40)
		m = typeChars(t, m, "/mo") // completion menu rows must align too
		for i, line := range strings.Split(m.renderPromptFrame(), "\n") {
			if got := lipgloss.Width(line); got != m.contentWidth() {
				t.Fatalf("width %d row %d: display width %d, want %d:\n%q",
					width, i, got, m.contentWidth(), line)
			}
		}
	}
}

func TestFrame_ModeGlyphNeverDependsOnColorAlone(t *testing.T) {
	m := frameModel(t, 100, 40)
	if !strings.Contains(stripANSI(m.View().Content), "⏸") {
		t.Fatal("gated mode must keep its textual glyph in the vitals rail")
	}
	m.mode = agent.ModeAuto
	if !strings.Contains(stripANSI(m.View().Content), "⏵⏵") {
		t.Fatal("permissive mode must keep its textual glyph in the vitals rail")
	}
}
