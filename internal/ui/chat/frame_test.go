package chat

import (
	"context"
	"fmt"
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
func frameModel(t testing.TB, width, height int) Model {
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
// the left of, and the identity on the right of. A card draws the same corner
// and lands above the frame, so the search runs from the bottom of the view —
// the frame is the last thing on the screen that opens a rail.
func frameTopRail(view string) string {
	lines := strings.Split(view, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "╭─") {
			return lines[i]
		}
	}
	return ""
}

// The rungs are stated in terminal columns and read in content columns, and
// the arithmetic between the two is the thing that drifted: the table is
// written as terminals so it can be read against the guideline, and each one
// is converted the once, here, where the conversion is the subject.
func TestFrameLayoutFor(t *testing.T) {
	cases := []struct {
		terminal int
		want     frameLayout
	}{
		{11, framePlain}, {12, frameNarrow}, {69, frameNarrow},
		{70, frameCompact}, {109, frameCompact}, {110, frameWide},
	}
	for _, c := range cases {
		content := c.terminal - horizontalPadding*2
		if got := frameLayoutFor(content); got != c.want {
			t.Fatalf("frameLayoutFor(%d) on a %d-column terminal = %d, want %d",
				content, c.terminal, got, c.want)
		}
	}
}

func TestFrame_WideTwoRails(t *testing.T) {
	m := frameModel(t, 130, 40) // the wide rung is a 110-column terminal
	view := stripANSI(m.View().Content)

	for _, want := range []string{"╭─", "├─", "╰─", "⏸ gated", "ctx ", "↑41.2k ↓9.8k", "$0.51", "gpt-4o", "[enter] send · [ctrl+g] editor · [ctrl+v] attach · [ctrl+/] palette · [shift+tab] mode · [ctrl+d] ×2 quit", "idle"} {
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

// The idle rail sheds whole offers until its run fits, at every width it is
// drawn at: an offer cut in half against the corner is a key nobody can read,
// and a run measured once against one terminal is a run that says nothing
// more on a wider one (docs/interface/principles.md#fold-never-hide).
//
// What never goes is the pair that says how a message leaves and under what
// mode, and the two-press quit beside them — a key whose first press is
// silent has to be named before it is pressed.
func TestFrame_IdleHintsFitEveryRailTheyAreDrawnOn(t *testing.T) {
	for _, terminal := range []int{frameWideWidth + horizontalPadding*2, 130, 160, 200} {
		m := frameModel(t, terminal, 40)
		var rail string
		for _, line := range strings.Split(stripANSI(m.View().Content), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "╰─") {
				rail = strings.TrimSpace(line)
			}
		}
		if rail == "" {
			t.Fatalf("no bottom rail at %d columns", terminal)
		}
		if !strings.HasSuffix(rail, "─╯") {
			t.Fatalf("at %d columns the hints crowd out the rail's own end:\n%s", terminal, rail)
		}
		if strings.Contains(rail, "…") {
			t.Fatalf("at %d columns an offer was clipped rather than shed:\n%s", terminal, rail)
		}
		for _, want := range []string{
			keys.Bracket(keys.Draft.Send) + " send",
			keys.Bracket(keys.Draft.Mode) + " mode",
			keys.Bracket(keys.Draft.Quit) + " ×2 quit",
		} {
			if !strings.Contains(rail, want) {
				t.Fatalf("at %d columns the rail dropped %q:\n%s", terminal, want, rail)
			}
		}
	}
}

// And the widest terminal gets the whole run, so nothing on it is written
// down and never drawn.
func TestFrame_TheWidestIdleRailOffersEverythingItHas(t *testing.T) {
	m := frameModel(t, 200, 40)
	rail := stripANSI(m.frameHints(200))
	for _, want := range []string{
		keys.Bracket(keys.Draft.Send) + " send",
		keys.Bracket(keys.Draft.Newline) + " newline",
		keys.Bracket(keys.Draft.Editor) + " editor",
		keys.Bracket(keys.Draft.Attach) + " attach",
		keys.Bracket(keys.Draft.Palette) + " palette",
		keys.Bracket(keys.Draft.Mode) + " mode",
		keys.Bracket(keys.Draft.Quit) + " ×2 quit",
	} {
		if !strings.Contains(rail, want) {
			t.Fatalf("the widest rail should offer %q, got %q", want, rail)
		}
	}
}

func TestFrame_CompactSingleRail(t *testing.T) {
	m := frameModel(t, 100, 40) // between the 70- and 110-column rungs
	view := stripANSI(m.View().Content)

	if strings.Contains(view, "├─") {
		t.Fatalf("compact frame must not have a dedicated vitals rail:\n%s", view)
	}
	for _, want := range []string{"╭─", "╰─", "⏸ gated", "ctx "} {
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
	m := frameModel(t, 60, 30) // between the 12- and 70-column rungs
	view := stripANSI(m.View().Content)

	for _, want := range []string{"╭─", "⏸ gated", "$0.51"} {
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

// Below the 12-column rung there is no box, and what stands in its place is
// the prompt glyph rather than blank rows: a terminal this narrow still has
// to say where you type (guidelines/layout-breakpoints).
func TestFrame_PlainBelowMinWidth(t *testing.T) {
	m := frameModel(t, 11, 30) // one column under the narrowest framed terminal
	view := stripANSI(m.View().Content)

	if strings.Contains(view, "╭") {
		t.Fatalf("sub-minimum widths must degrade to plain rows:\n%s", view)
	}
	if !strings.Contains(view, draftGutter) {
		t.Fatalf("the frameless layout still draws the prompt:\n%s", view)
	}
	// And the draft starts after the glyph rather than under it: the field
	// is narrowed by the columns the glyph takes and the cursor is moved by
	// the same, so the two cannot disagree about where the first character
	// lands.
	var cur cursorSink
	m.paint(&cur)
	if cur.at == nil {
		t.Fatal("the frameless draft still owns the terminal's cursor")
	}
	if want := horizontalPadding + lipgloss.Width(m.plainPrompt()); cur.at.X != want {
		t.Fatalf("cursor column %d, want %d — the cell after the prompt glyph", cur.at.X, want)
	}
}

// The rungs are terminal widths, and each is asserted on both sides of
// itself: a rung is a pair of answers, and a threshold read against the
// wrong datum is one that still gives the right answer four columns late.
func TestFrame_RungsAreTerminalColumns(t *testing.T) {
	view := func(terminal int) string {
		return stripANSI(frameModel(t, terminal, 40).View().Content)
	}
	// Two panes, by the arrangement rather than by a glyph: the rail's own
	// divider is the only thing on the screen that says there are two.
	if !frameModel(t, 130, 40).twoPane() {
		t.Fatal("a 130-column terminal splits into a transcript and a rail")
	}
	if frameModel(t, 129, 40).twoPane() {
		t.Fatal("a 129-column terminal is one pane")
	}
	// The vitals take a rail of their own inside the box, or fold into its
	// bottom border.
	if got := view(110); !strings.Contains(got, "├─") {
		t.Fatalf("a 110-column terminal gives the vitals their own rail:\n%s", got)
	}
	if got := view(109); strings.Contains(got, "├─") {
		t.Fatalf("a 109-column terminal folds the vitals into the border:\n%s", got)
	}
	// And the box itself, under which the prompt glyph stands in for it.
	if got := view(12); !strings.Contains(got, "╭─") {
		t.Fatalf("a 12-column terminal still frames the draft:\n%s", got)
	}
	if got := view(11); strings.Contains(got, "╭─") {
		t.Fatalf("an 11-column terminal draws no box:\n%s", got)
	}
}

func TestFrame_GutterAndHintsSwapWhileWorking(t *testing.T) {
	m := frameModel(t, 130, 40)
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "│ "+draftGutter+" ") {
		t.Fatalf("idle frame missing the draft's gutter:\n%s", view)
	}

	m.state = stateStreaming
	view = stripANSI(m.View().Content)
	// The activity slot is the running turn's status line now:
	// `WORKING` was true of every moment of every turn and said nothing.
	if !strings.Contains(view, "│ ▸ ") || !strings.Contains(view, "thinking…") {
		t.Fatalf("working frame missing the steering gutter and the turn status:\n%s", view)
	}
	if !strings.Contains(view, "[ctrl+c] ×2 stop the run · [enter] queues steering · [/] commands") {
		t.Fatalf("working frame missing the interrupt and steering hints:\n%s", view)
	}
	if strings.Contains(view, "[enter] send") {
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

	m.steering = []steeringItem{{text: "one"}, {text: "two"}}
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
	// panel only once the decision holds the keyboard. The card is drawn
	// with the frame's own corners, so what tells the two apart on the
	// screen is the account riding the frame's rail.
	ungated := stripANSI(m.View().Content)
	if !strings.Contains(ungated, "╰─ ⏸ gated") {
		t.Fatalf("an ungated decision leaves the draft its frame:\n%s", ungated)
	}
	m = handover(t, m)
	view := stripANSI(m.View().Content)
	if strings.Contains(view, "╰─ ⏸ gated") {
		t.Fatalf("takeover surfaces must replace the frame:\n%s", view)
	}
	if !strings.Contains(view, "⏸ gated") {
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
	if !strings.Contains(view, "/model") || !strings.Contains(view, "[tab] complete") {
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
	if !strings.Contains(view, "│ researcher-1 "+draftGutter+" ") {
		t.Fatalf("attached gutter should carry the child's name:\n%s", view)
	}
	if !strings.Contains(view, "[esc] detach · [alt+a] agents") {
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
// in — so the breadcrumb stays, on the side the account gave up. The path
// leads and the session's name follows it, because the path is the answer the
// far side is there to give and the name is the word the header above the
// transcript is already showing.
func TestFrame_AttachedBreadcrumbTakesTheFarSide(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)
	m.attach("researcher-1")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40}) // content 136 → wide
	m = updated.(Model)

	rail := strings.TrimSpace(stripANSI(frameTopRail(m.View().Content)))
	if !strings.Contains(rail, "orchestrator ▸ researcher-1 · ") || !strings.HasSuffix(rail, " ─╮") {
		t.Fatalf("the breadcrumb should close the attached rail:\n%s", rail)
	}
	if !strings.HasPrefix(rail, "╭─ ") {
		t.Fatalf("the account should open the attached rail:\n%s", rail)
	}
	// Every glyph of it is a colour the palette issued: the path in Status,
	// the child in Info. Bare text inherits the terminal's own foreground,
	// which is the one colour on this surface nobody chose.
	styled := frameTopRail(m.View().Content)
	for _, want := range []string{
		sty.Frame.Identity.Render("orchestrator"),
		sty.Frame.IdentityChild.Render(" ▸ researcher-1"),
	} {
		if !strings.Contains(styled, want) {
			t.Fatalf("the breadcrumb should carry the palette's own tones, missing %q in:\n%q", want, styled)
		}
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

// attachedModel is a session with the keyboard in a child that is running,
// has been billed for a request and has a conversation behind it — the state
// a rail scoped to a child has to be able to report in full.
func attachedModel(t *testing.T, width int) Model {
	t.Helper()
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(),
		NewEnv: billedEnv(provider.Usage{PromptTokens: 4200, CompletionTokens: 900})})
	t.Cleanup(sup.Close)
	m := frameModel(t, width, 40).WithSubagents(sup)
	spawnBlockedChild(t, sup)
	waitFor(t, func() bool {
		st, ok := sup.Get("researcher-1")
		return ok && st.Spend.In > 0
	})
	noteChild(t, sup, "researcher-1", subagent.TranscriptEntry{
		Kind: subagent.EntryTool, Tool: "read_file", Args: `{"path":"internal/agent/loop.go"}`,
		Result: strings.Repeat("internal/agent/loop.go:118 the round counter is read here\n", 220)})
	m.agent.BeginToolRound("", nil, nil)
	m.attach("researcher-1")
	return m
}

// The fields the drop order never sheds are on the attached rail at every
// width: the permission mode, the child's context pressure and its spend
// against the session's. What goes, goes in the order the guideline fixes —
// the child's name is the detail rank and leaves first, the parent's round
// counter after it (guidelines/layout-drop-order). The rail used to carry
// none of the three, so the one place that reports what a child is burning
// went quiet exactly where somebody was watching it.
func TestChildRail_NeverDropsPressureSpendOrMode(t *testing.T) {
	m := attachedModel(t, 140)
	segs := m.childRailSegments()
	var vital []string
	for _, s := range segs {
		if s.Drop <= components.RailVital {
			vital = append(vital, stripANSI(s.Text))
		}
	}
	full := stripANSI(m.frameVitals(frameWide, 200))
	for _, want := range []string{"⏸ ", "ctx ", "▰", " of ", "parent round 1/", "researcher-1"} {
		if !strings.Contains(full, want) {
			t.Fatalf("a wide attached rail states everything, missing %q in %q", want, full)
		}
	}
	if len(vital) < 3 {
		t.Fatalf("mode, pressure and spend are all never-dropped fields, got %q", vital)
	}
	// Narrowing sheds the name, then the round, and never the three above.
	name := strings.Index(full, "researcher-1")
	round := strings.Index(full, "parent round")
	if name < round {
		t.Fatalf("the child's name is the detail rank and stands last: %q", full)
	}
	for width := 200; width >= 20; width -= 4 {
		rail := stripANSI(m.frameVitals(frameWide, width))
		if !strings.Contains(rail, "⏸ ") {
			t.Fatalf("width %d: the mode segment is never dropped: %q", width, rail)
		}
		if strings.Contains(rail, "parent round") && !strings.Contains(rail, "ctx ") {
			t.Fatalf("width %d: the round outlived the pressure: %q", width, rail)
		}
	}
	// And the narrow layout, which keeps only the never-dropped fields, keeps
	// all three of them.
	narrow := stripANSI(m.frameVitals(frameNarrow, 200))
	for _, want := range []string{"⏸ ", "ctx ", "$"} {
		if !strings.Contains(narrow, want) {
			t.Fatalf("the minimal rail keeps what never drops, missing %q in %q", want, narrow)
		}
	}
	if strings.Contains(narrow, "parent round") {
		t.Fatalf("the minimal rail keeps nothing below vital: %q", narrow)
	}
}

// A child waiting on the reader and a child that stopped are the two states
// the reader is attached to find out about, so neither is on the drop ladder:
// a rail that shed the word to make room for a round counter would be silent
// about the one thing it was opened for.
// The waiting child shares the branch and the rank, so this pins both.
func TestChildRail_NeverDropsAStoppedChildsState(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := frameModel(t, 140, 40).WithSubagents(sup)
	spawnBlockedChild(t, sup)
	killChild(t, sup, "researcher-1")
	m.attach("researcher-1")

	st, _ := sup.Get("researcher-1")
	if st.State != subagent.StateFailed || st.Detail == "" {
		t.Fatalf("the child should have stopped with something to say, got %v %q", st.State, st.Detail)
	}
	// Down to a rail with barely room for the mode beside it.
	for width := 200; width >= 30; width -= 5 {
		if rail := stripANSI(m.frameVitals(frameNarrow, width)); !strings.Contains(rail, st.Detail) {
			t.Fatalf("width %d: the stopped child's state was dropped: %q", width, rail)
		}
	}
	// And it is alert-styled, so the state is not left to the word alone.
	if !strings.Contains(m.frameVitals(frameNarrow, 200), sty.CtxAlert.Render(st.Detail)) {
		t.Fatalf("a stopped child's state carries the alert tone: %q", m.frameVitals(frameNarrow, 200))
	}
}

// The attached top rail names the child's phase in the same closed vocabulary
// a turn of this session's own is reported in, read off what the supervisor
// reports: a call the child still has open is `running`, and `WORKING` — true
// of every moment of every turn, and therefore an answer to nothing — is not
// one of the words (docs/interface/principles.md#closed-vocabularies).
func TestFrame_AttachedRailNamesThePhaseRatherThanWorking(t *testing.T) {
	m := attachedModel(t, 140)
	rail := stripANSI(m.frameActivity(120))
	if strings.Contains(strings.ToUpper(rail), "WORKING") {
		t.Fatalf("the phase is one of the four, not WORKING: %q", rail)
	}
	if !strings.Contains(rail, "thinking…") {
		t.Fatalf("a child with nothing open is reasoning before it acts: %q", rail)
	}
	noteChild(t, m.subagents, "researcher-1", subagent.TranscriptEntry{
		Kind: subagent.EntryTool, Tool: "execute_command",
		Args: `{"command":"go test ./internal/agent/..."}`, Pending: true})
	if rail := stripANSI(m.frameActivity(120)); !strings.Contains(rail, "running go test ./internal/agent/...") {
		t.Fatalf("an open call names itself on the rail: %q", rail)
	}
	// Two calls in flight are named by neither, which is the rule the
	// session's own status line follows.
	noteChild(t, m.subagents, "researcher-1", subagent.TranscriptEntry{
		Kind: subagent.EntryTool, Tool: "read_file", Args: `{"path":"round.go"}`, Pending: true})
	rail = stripANSI(m.frameActivity(120))
	if strings.Contains(rail, "round.go") || strings.Contains(rail, "go test") {
		t.Fatalf("a round of several calls is named by none of them: %q", rail)
	}
	if !strings.Contains(rail, "running") {
		t.Fatalf("it is still the running phase: %q", rail)
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
	m.policy.mode = agent.ModeAuto
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
	if got := m.input.Height(); got != 9 {
		t.Fatalf("nine-line draft box height %d, want a row per line", got)
	}
	if got := m.viewportHeight(); got != restRows-(9-inputHeight) {
		t.Fatalf("viewport %d rows, want %d — the box must take exactly what it grew", got, restRows-(9-inputHeight))
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
// message: the horizontal pass fits the box to the new width and the vertical
// one is taken after it, over the box as it came back.
func TestDraftBoxGrowsOnWidthShrink(t *testing.T) {
	m := frameModel(t, 110, 40)
	m.input.SetValue(strings.Repeat("wrap me ", 40)) // ~320 cells: four rows at w110
	updated, _ := m.Update(resizeSettledMsg{seq: m.resizeSeq})
	m = updated.(Model)
	before := m.input.Height()
	if before <= inputHeight {
		t.Fatalf("fixture: the draft should already wrap past %d rows, got %d", inputHeight, before)
	}

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 40})
	m = updated.(Model)
	if got := m.input.Height(); got <= before {
		t.Fatalf("box height %d after the shrink, want more than %d", got, before)
	}
}

// And it settles there. The two passes are ordered rather than iterated —
// the second changes no width, so nothing it counts can move under it — and
// this is what says so: fitting the surface again at the same size moves
// neither the box nor the rows the transcript was left with.
func TestResizeSettlesInOneExtraPass(t *testing.T) {
	m := frameModel(t, 110, 40)
	m.input.SetValue(strings.Repeat("wrap me ", 40))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 40})
	m = updated.(Model)

	box, rows := m.input.Height(), m.viewport.Height()
	if box <= inputHeight {
		t.Fatalf("fixture: the draft should have re-wrapped past %d rows, got %d", inputHeight, box)
	}
	// The rows the pane holds are the rows the split hands out over the box
	// as it now is, not as it was before the width moved.
	if want := m.viewportHeight(); rows != want {
		t.Fatalf("viewport %d rows, want the %d the split budgets after the re-wrap", rows, want)
	}
	m.fitDraft()
	m.syncInputHeight()
	if m.input.Height() != box || m.viewport.Height() != rows {
		t.Fatalf("a second pass moved the box to %d rows and the pane to %d, want %d and %d",
			m.input.Height(), m.viewport.Height(), box, rows)
	}
}

// The draft's cursor is the terminal's own, so the frame owes it a
// coordinate on every frame: the cell after what has been typed, inside the
// box rather than at the box's own corner.
func TestDraftCursorIsPlacedInTheBox(t *testing.T) {
	m := frameModel(t, 110, 40)
	var cur cursorSink
	m.paint(&cur)
	empty := cur.at
	if empty == nil {
		t.Fatal("an empty draft still has a cursor: it is where the first character goes")
	}
	// Nothing is painted there — the cell is a space — so the coordinate is
	// the only record of it.
	if empty.X <= 0 || empty.Y <= 0 {
		t.Fatalf("cursor at %v, want it inside the frame rather than at the screen corner", empty.Position)
	}

	m.input.SetValue("hello")
	m.syncInputHeight()
	cur = cursorSink{}
	m.paint(&cur)
	typed := cur.at
	if typed == nil {
		t.Fatal("a draft with text in it still owns the cursor")
	}
	if got, want := typed.X, empty.X+len("hello"); got != want {
		t.Fatalf("cursor column %d after five characters, want %d", got, want)
	}
	if typed.Y != empty.Y {
		t.Fatalf("a draft that has not wrapped should not have moved the cursor's row")
	}

	// A draft long enough to wrap puts it on the row it wrapped onto.
	m.input.SetValue(strings.Repeat("wrap me ", 40))
	m.syncInputHeight()
	cur = cursorSink{}
	m.paint(&cur)
	wrapped := cur.at
	if wrapped == nil {
		t.Fatal("a wrapped draft still owns the cursor")
	}
	if wrapped.Y <= empty.Y {
		t.Fatalf("cursor row %d on a wrapped draft, want it below the first row %d",
			wrapped.Y, empty.Y)
	}
	// The box grows upward from a bottom rail the panel holds still, so the
	// row being typed on is where the last row of the smallest box was.
	if want := empty.Y + inputHeight - 1; wrapped.Y != want {
		t.Fatalf("cursor row %d on the last of the box's %d rows, want %d",
			wrapped.Y, m.input.Height(), want)
	}
}

// And it does at every width, in every layout the frame has: the box's
// rectangle is resolved once and both the paint and the cursor read it, so a
// mode that placed the cursor somewhere the box does not own would be a
// rectangle the paint had drawn into too. The narrow modes are the ones with
// no test of their own otherwise — the plain layout below minFrameWidth draws
// no box at all, and the cursor comes off the bare input under the status
// bar, one prompt glyph in from the content's own edge. It walks the widths
// the frame is captured at, which is where the rungs and both sides of the
// narrowest of them are.
func TestDraftCursorInEveryLayout(t *testing.T) {
	for _, width := range frameWidths {
		for _, draft := range []string{"", "abc", strings.Repeat("wrap me ", 12)} {
			m := frameModel(t, width, 40)
			for _, r := range draft {
				updated, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
				m = updated.(Model)
			}
			var cur cursorSink
			rows := strings.Split(m.paint(&cur), "\n")
			if cur.at == nil {
				t.Errorf("w%d, %d characters typed: the draft has the keyboard and no cursor", width, len(draft))
				continue
			}
			if cur.at.Y < 0 || cur.at.Y >= len(rows) {
				t.Errorf("w%d: cursor row %d outside the %d-row screen", width, cur.at.Y, len(rows))
			}
			if cur.at.X < horizontalPadding || cur.at.X >= width-horizontalPadding {
				t.Errorf("w%d: cursor column %d outside the content columns", width, cur.at.X)
			}
		}
	}
}

// A draft that fills its last column reports the column after it, which is
// where the next character goes and is one past the cells the box owns. The
// cursor stands on the last cell rather than on the border beside it — and it
// stands somewhere, which is the part that matters: a cursor dropped at the
// wrap boundary would blink out every time a line filled.
func TestDraftCursorAtTheWrapBoundary(t *testing.T) {
	m := frameModel(t, 60, 40)
	for range m.input.Width() {
		updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
		m = updated.(Model)
	}
	var cur cursorSink
	m.paint(&cur)
	if cur.at == nil {
		t.Fatal("a draft filled to its last column still has a cursor")
	}
	if cur.at.X >= m.contentWidth()+horizontalPadding {
		t.Fatalf("cursor column %d, want it inside the content columns", cur.at.X)
	}
}

// The reverse search takes the keyboard off the draft, so the cursor goes to
// the row being typed into rather than staying in the match above it.
func TestHistorySearchTakesTheCursor(t *testing.T) {
	m := frameModel(t, 110, 40)
	m.inputHistory = []string{"go test ./internal/agent"}
	opened, _ := m.openHistorySearch()
	m = opened.(Model)
	m.histSearch.query = "test"
	m.placeHistoryMatch()

	var cur cursorSink
	m.paint(&cur)
	if cur.at == nil {
		t.Fatal("the search row is what is being typed into, so it owns the cursor")
	}
	if got, want := cur.at.X, lipgloss.Width(m.searchRowHead()); got <= want-1 {
		t.Fatalf("cursor column %d, want it past the %d-cell label and query", got, want)
	}
}

// The box's reported height is the rows it draws, at every width the captures
// cover. The panel budgets the transcript's rows from the number the textarea
// reports, so a box that drew one more row than it said would be a row
// nothing paid for — which is the accounting the whole vertical split rests
// on (layout.go).
func TestDraftBoxReportsTheRowsItDraws(t *testing.T) {
	drafts := []string{
		"",
		"one line",
		strings.Repeat("wrap me ", 40),
		strings.Repeat("line\n", 8) + "line",
		strings.Repeat("line\n", 30) + "line",
	}
	for _, width := range append([]int{14}, goldenWidths...) {
		for _, draft := range drafts {
			m := frameModel(t, width, 40)
			m.input.SetValue(draft)
			m.syncInputHeight()
			if got, want := lipgloss.Height(m.input.View()), m.input.Height(); got != want {
				t.Errorf("w%d: the box draws %d rows and reports %d", width, got, want)
			}
		}
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

// pickerModel is a ready model with the model picker holding the bottom
// panel — a takeover surface, which is what the vertical split used to render
// to count and then render again to draw.
func pickerModel(t testing.TB) Model {
	t.Helper()
	names := make([]string, 20)
	for i := range names {
		names[i] = fmt.Sprintf("model-%02d", i+1)
	}
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream).
		WithModelSwitcher(func(string) {}).
		WithPricing(nil, "model-01").
		WithModelOptions(names)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	m = updated.(Model)
	m.input.SetValue("/model")
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.state != statePick {
		t.Fatalf("the picker should hold the panel, got state %d", m.state)
	}
	return m
}

// BenchmarkPickerFrame paints one frame with a picker holding the panel.
func BenchmarkPickerFrame(b *testing.B) {
	m := pickerModel(b)
	b.ReportAllocs()
	for b.Loop() {
		_ = m.View()
	}
}

// BenchmarkStreamingFrame paints one streaming frame at the widest layout —
// the frame the surface repaints on every spinner tick.
func BenchmarkStreamingFrame(b *testing.B) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	table := pricing.NewTable(map[string]pricing.ModelPricing{
		"gpt-4o": {InputCostPerToken: 0.00001, OutputCostPerToken: 0.00001},
	})
	m := New(msgs, mockStream).WithPricing(table, "gpt-4o")
	m.accumulateUsage(&provider.Usage{PromptTokens: 41200, CompletionTokens: 9800})
	for i := range 60 {
		m.appendEntry(entry{kind: entryUser, text: "ask number " + strings.Repeat("x", i%17)})
		m.appendEntry(entry{kind: entryAssistant, text: strings.Repeat("answer body ", 12)})
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	sm := updated.(Model)
	sm.setTurnState(stateStreaming)
	sm.streaming = strings.Repeat("streamed token ", 20)
	sm.viewport.SetLines(sm.renderHistoryLines())
	sm.viewport.GotoBottom()
	b.ReportAllocs()
	for b.Loop() {
		_ = sm.View()
	}
}
