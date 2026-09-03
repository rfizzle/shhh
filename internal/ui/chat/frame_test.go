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
	"github.com/rfizzle/shhh/internal/ui/keys"
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

// frameTopRail is the frame's top rail: the line the live turn status sits on
// the left of, and the identity on the right of.
func frameTopRail(view string) string {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "╭─") {
			return line
		}
	}
	return ""
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

	for _, want := range []string{"╭─", "├─", "╰─", "⏸ manual", "ctx ", "↑41.2k ↓9.8k", "$0.51", "gpt-4o", "enter send · shift+enter newline · ctrl+g editor · ctrl+v attach · ctrl+p palette · shift+tab mode", "idle"} {
		if !strings.Contains(view, want) {
			t.Fatalf("wide frame missing %q:\n%s", want, view)
		}
	}
	// The root session's rail is live status and nothing else. The static
	// title it used to open with said the same word on every frame of every
	// session — the header above the transcript already names the surface —
	// and it was width the phase, the clock and the spend could use.
	if rail := frameTopRail(view); strings.Contains(rail, "shhh") {
		t.Fatalf("the root top rail should carry no title:\n%s", rail)
	}
}

// The idle rail is full at the width it first appears at, which is the
// measurement the hint set is chosen against: another hint would push the
// row past its own corner and be clipped, and a clipped hint is a key
// nobody can read. Asserted here so adding a seventh fails a test rather
// than shipping a truncated rail on a 114-column terminal.
func TestFrame_IdleHintsFitTheRailAtItsThreshold(t *testing.T) {
	m := frameModel(t, frameWideWidth+4, 40) // the narrowest wide frame
	var rail string
	for _, line := range strings.Split(stripANSI(m.View().Content), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "╰─") {
			rail = strings.TrimSpace(line)
		}
	}
	if rail == "" {
		t.Fatal("no bottom rail at the wide threshold")
	}
	if !strings.HasSuffix(rail, "─╯") {
		t.Fatalf("the hints crowd out the rail's own end:\n%s", rail)
	}
	if !strings.Contains(rail, keys.Shown(keys.Draft.Mode)+" mode") {
		t.Fatalf("the last hint is clipped:\n%s", rail)
	}
}

func TestFrame_CompactSingleRail(t *testing.T) {
	m := frameModel(t, 100, 40) // content 96 → compact
	view := stripANSI(m.View().Content)

	if strings.Contains(view, "├─") {
		t.Fatalf("compact frame must not have a dedicated vitals rail:\n%s", view)
	}
	for _, want := range []string{"╭─", "╰─", "⏸ manual", "ctx "} {
		if !strings.Contains(view, want) {
			t.Fatalf("compact frame missing %q:\n%s", want, view)
		}
	}
	if rail := frameTopRail(view); strings.Contains(rail, "shhh") {
		t.Fatalf("the root top rail should carry no title:\n%s", rail)
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
	// Model detail and token counts drop first in the field-drop order; the
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
	// The activity slot is the running turn's status line now:
	// `WORKING` was true of every moment of every turn and said nothing.
	if !strings.Contains(view, "│ ▸ ") || !strings.Contains(view, "thinking…") {
		t.Fatalf("working frame missing the steering gutter and the turn status:\n%s", view)
	}
	if !strings.Contains(view, "ctrl+c cancels the turn · enter queues steering · / commands") {
		t.Fatalf("working frame missing the interrupt and steering hints:\n%s", view)
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
	// Ungated the card rides above a live frame; it takes the
	// panel only once the decision holds the keyboard.
	ungated := stripANSI(m.View().Content)
	if !strings.Contains(ungated, "╭─") {
		t.Fatalf("an ungated decision leaves the draft its frame:\n%s", ungated)
	}
	m = handover(t, m)
	view := stripANSI(m.View().Content)
	if strings.Contains(view, "╭─") {
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
	if want := 40 - inputHeight - (headerHeight + dividerHeight + bottomChromeHeight) - 1; m.viewport.Height() != want {
		t.Fatalf("wide viewport height = %d, want %d", m.viewport.Height(), want)
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(Model)
	if want := 40 - inputHeight - (headerHeight + dividerHeight + bottomChromeHeight); m.viewport.Height() != want {
		t.Fatalf("compact viewport height = %d, want %d", m.viewport.Height(), want)
	}
}

func TestFrame_CompletionMenuInsideFrame(t *testing.T) {
	m := typeChars(t, readyModel(t), "/mo")
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "╭─") {
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
	// Attached, the rail is the one place that says which session the
	// keyboard is in, so the identity it dropped at the root comes back.
	if rail := frameTopRail(view); !strings.Contains(rail, "orchestrator ▸ researcher-1") {
		t.Fatalf("attached top rail missing the breadcrumb:\n%s", view)
	}
	if !strings.Contains(view, "│ researcher-1 ❯ ") {
		t.Fatalf("attached gutter should carry the child's name:\n%s", view)
	}
	if !strings.Contains(view, "esc detach · ctrl+b agents") {
		t.Fatalf("attached frame missing the detach hints:\n%s", view)
	}
}

// The account is the only thing on the top rail that moves, so it opens the
// rail at the corner over the prompt glyph rather than closing it against the
// far edge — on a wide terminal that edge is a hundred columns from anything
// the reader is looking at. Asserted at three widths because the slot is
// measured per layout and a rail that leads correctly at 80 and trails at 130
// would be the exact bug this replaces.
func TestFrame_TopRailLeadsWithTheAccount(t *testing.T) {
	for _, width := range []int{80, 110, 130} {
		m := frameModel(t, width, 40)
		rail := strings.TrimSpace(stripANSI(frameTopRail(m.View().Content)))
		if rail == "" {
			t.Fatalf("no top rail at width %d", width)
		}
		if !strings.HasPrefix(rail, "╭─ idle ─") {
			t.Fatalf("width %d: the account should open the rail:\n%s", width, rail)
		}
		if !strings.HasSuffix(rail, "──╮") {
			t.Fatalf("width %d: nothing should close the root rail:\n%s", width, rail)
		}
	}
}

// The waiting chip is the account's slot saying what the turn is doing, not a
// label of its own, so it travels with it.
func TestFrame_WaitingChipLeadsTheRail(t *testing.T) {
	m := interruptedModel(t, "also add a --max-rounds flag")
	rail := strings.TrimSpace(stripANSI(frameTopRail(m.View().Content)))
	if !strings.HasPrefix(rail, "╭─ ⏸ 1 waiting ─") {
		t.Fatalf("the waiting chip should open the rail:\n%s", rail)
	}
}

// Attached, the rail is the one place that says which session the keyboard is
// in — so the breadcrumb stays, on the side the account gave up.
func TestFrame_AttachedBreadcrumbTakesTheFarSide(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)
	m.attach("researcher-1")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40}) // content 136 → wide
	m = updated.(Model)

	rail := strings.TrimSpace(stripANSI(frameTopRail(m.View().Content)))
	if !strings.HasSuffix(rail, "orchestrator ▸ researcher-1 ─╮") {
		t.Fatalf("the breadcrumb should close the attached rail:\n%s", rail)
	}
	if !strings.HasPrefix(rail, "╭─ ") {
		t.Fatalf("the account should open the attached rail:\n%s", rail)
	}
}

// A rail with room for one label keeps the account whole. The breadcrumb
// answers a question a key can ask again; an account clipped to `⠋W…` is a
// label nobody can read, and it is the only one on the rail that moves.
// Asserted across the widths where the breadcrumb stops fitting, because the
// failure this guards against is not a dropped account but a mangled one
// standing beside a pristine breadcrumb.
func TestFrame_IdentityDropsBeforeTheAccount(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)
	m.attach("researcher-1")

	left, right := m.topRailLabels(frameWide, 140)
	account := strings.TrimSpace(stripANSI(left))
	if account == "" || !strings.Contains(right, "researcher-1") {
		t.Fatalf("both labels should stand at 140: left %q right %q", left, right)
	}

	m.title = strings.Repeat("survey", 12)
	var dropped bool
	for width := 140; width >= 40; width-- {
		left, right = m.topRailLabels(frameWide, width)
		if got := strings.TrimSpace(stripANSI(left)); got != account {
			t.Fatalf("width %d: the account should never shed for the identity, got %q want %q", width, got, account)
		}
		if right == "" {
			dropped = true
		} else if dropped {
			t.Fatalf("width %d: the identity came back after it was dropped: %q", width, right)
		}
	}
	if !dropped {
		t.Fatal("the identity should shed once the rail cannot hold both")
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

// The box grows one row per draft line up to its cap, gives the viewport
// back what it took, and returns to three rows when the draft empties.
func TestDraftBoxGrowsAndShrinks(t *testing.T) {
	m := frameModel(t, 100, 40)
	if got := m.input.Height(); got != inputHeight {
		t.Fatalf("idle box height %d, want %d", got, inputHeight)
	}
	restRows := m.viewportHeight()

	m.input.SetValue(strings.Repeat("line\n", 8) + "line")
	updated, _ := m.Update(resizeSettledMsg{seq: m.resizeSeq})
	m = updated.(Model)
	if got := m.input.Height(); got != 11 {
		t.Fatalf("nine-line draft box height %d, want 11", got)
	}
	if got := m.viewportHeight(); got != restRows-8 {
		t.Fatalf("viewport %d rows, want %d — the box must take exactly what it grew", got, restRows-8)
	}

	m.input.SetValue("")
	updated, _ = m.Update(resizeSettledMsg{seq: m.resizeSeq})
	m = updated.(Model)
	if got := m.input.Height(); got != inputHeight {
		t.Fatalf("emptied box height %d, want %d", got, inputHeight)
	}
	if got := m.viewportHeight(); got != restRows {
		t.Fatalf("viewport %d rows after shrink, want %d restored", got, restRows)
	}
}

// Past the cap the box stops and the textarea scrolls inside it: twelve rows
// at this height, and never more than the panel's 40% share.
func TestDraftBoxCapped(t *testing.T) {
	m := frameModel(t, 100, 40)
	m.input.SetValue(strings.Repeat("line\n", 30) + "line")
	updated, _ := m.Update(resizeSettledMsg{seq: m.resizeSeq})
	m = updated.(Model)
	if got := m.input.Height(); got != maxDraftRows {
		t.Fatalf("box height %d, want the %d-row cap", got, maxDraftRows)
	}

	// A shorter terminal lowers the cap to the panel budget instead.
	short := frameModel(t, 100, 20)
	short.input.SetValue(strings.Repeat("line\n", 30) + "line")
	updated, _ = short.Update(resizeSettledMsg{seq: short.resizeSeq})
	short = updated.(Model)
	want := short.maxConfirmPanelHeight() - bottomChromeHeight
	if got := short.input.Height(); got != want {
		t.Fatalf("box height %d at 20 rows, want the %d-row budget", got, want)
	}
}

// A width change that re-wraps the draft moves the height in the same
// message — there is no second pass to wait for.
func TestDraftBoxGrowsOnWidthShrink(t *testing.T) {
	m := frameModel(t, 110, 40)
	m.input.SetValue(strings.Repeat("wrap me ", 12)) // ~96 cells, one row at w110
	updated, _ := m.Update(resizeSettledMsg{seq: m.resizeSeq})
	m = updated.(Model)
	before := m.input.Height()

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 40})
	m = updated.(Model)
	if got := m.input.Height(); got <= before {
		t.Fatalf("box height %d after the shrink, want more than %d", got, before)
	}
}

// A full-screen surface replaces the input with a one-line hint; a grown
// draft must not leave its rows behind as blank panel.
func TestDraftBoxRowsStayWithTheInput(t *testing.T) {
	m := frameModel(t, 100, 40)
	m.input.SetValue(strings.Repeat("line\n", 8) + "line")
	updated, _ := m.Update(resizeSettledMsg{seq: m.resizeSeq})
	m = updated.(Model)
	if m.input.Height() <= inputHeight {
		t.Fatal("fixture: the box should have grown")
	}
	m.state = stateDiffFull
	if got := m.bottomPanelHeight(); got != inputHeight {
		t.Fatalf("full-screen panel height %d, want the %d-row hint", got, inputHeight)
	}
}

// Growing the box moves the pane's rows, never its reading position: a
// reader scrolled up stays where they were, and a pane pinned to the live
// end stays pinned.
func TestDraftBoxGrowthKeepsTheScrollPosition(t *testing.T) {
	m := frameModel(t, 100, 40)
	for i := 0; i < 80; i++ {
		m.appendEntry(entry{kind: entrySystem, text: "row"})
	}
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoTop()

	m.input.SetValue("one\ntwo\nthree\nfour\nfive")
	updated, _ := m.Update(resizeSettledMsg{seq: m.resizeSeq})
	m = updated.(Model)
	if got := m.viewport.YOffset(); got != 0 {
		t.Fatalf("a reader scrolled up was snapped to offset %d", got)
	}

	pinned := frameModel(t, 100, 40)
	for i := 0; i < 80; i++ {
		pinned.appendEntry(entry{kind: entrySystem, text: "row"})
	}
	pinned.viewport.SetLines(pinned.renderHistoryLines())
	pinned.viewport.GotoBottom()
	pinned.input.SetValue("one\ntwo\nthree\nfour\nfive")
	updated, _ = pinned.Update(resizeSettledMsg{seq: pinned.resizeSeq})
	pinned = updated.(Model)
	if !pinned.viewport.AtBottom() {
		t.Fatal("a pane pinned to the live end must stay pinned through the height change")
	}
}
