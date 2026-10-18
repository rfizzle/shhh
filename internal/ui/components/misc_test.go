package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/golden"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

func TestCockpit_Segments(t *testing.T) {
	c := Cockpit{Mode: "accept edits", ModeKind: CockpitPermissive, Round: "round 7 of 25",
		CtxPct: 62, Tokens: "↑41.2k ↓9.8k", Spend: "$0.14",
		Agents: 2}
	view := c.View(120)
	for _, want := range []string{"⏵⏵ accept edits", "round 7 of 25", "ctx", "62%", "▰", "$0.14", "◇2"} {
		if !strings.Contains(view, want) {
			t.Fatalf("cockpit should contain %q:\n%s", want, view)
		}
	}
	// The agents segment is a count: a child waiting on the user says so on
	// its own lane and in the frame's title, not here as well
	// (docs/interface/departures.md#the-childrens-tally-says-who-needs-you-first).
	if strings.Contains(view, "⚠") {
		t.Fatalf("the agents segment should carry no blocked badge:\n%s", view)
	}
	gated := Cockpit{Mode: "read-only", ModeKind: CockpitGated, CtxPct: -1}
	if !strings.Contains(gated.View(80), "⏸ read-only") {
		t.Fatal("gated modes render ⏸")
	}
	checking := Cockpit{Mode: "deciding", ModeKind: CockpitChecking, CtxPct: -1}
	if !strings.Contains(checking.View(80), "✦ deciding") {
		t.Fatal("classifier checks render ✦")
	}
}

// TestGolden_CockpitFlowChip captures the mode segment with a flow's mode
// beside the session's, which no width sheds: the session's mark and word,
// an arrow, and the flow's mark and word, with nothing drawn where the two
// are equal and `✦ deciding` standing in for the whole segment.
func TestGolden_CockpitFlowChip(t *testing.T) {
	captureGolden(t, "cockpit-flow-chip", "the mode segment with a flow's mode", goldenWidths, func(width int) []golden.Panel {
		seg := func(c Cockpit) string {
			c.Round, c.CtxPct, c.CtxWord, c.Spend = "round 3 of 25", 12, "context", "$0.14"
			return FitRail(c.RailSegments(), sty.status.Render(" · "), width)
		}
		return []golden.Panel{
			{Label: "a manual session, no flow", View: seg(Cockpit{Mode: "manual", ModeKind: CockpitGated})},
			{Label: "a manual session under a run's writing step", View: seg(Cockpit{Mode: "manual", ModeKind: CockpitGated, Flow: "auto", FlowKind: CockpitPermissive})},
			{Label: "an auto session under a reading", View: seg(Cockpit{Mode: "auto", ModeKind: CockpitPermissive, Flow: "plan", FlowKind: CockpitGated})},
			{Label: "the classifier deciding while a flow holds", View: seg(Cockpit{Mode: "deciding", ModeKind: CockpitChecking, Flow: "auto", FlowKind: CockpitPermissive})},
		}
	})
}

// The chip is mark and word again, after an arrow, and never the flow's word
// alone; and the segment carrying it is the one no width sheds.
func TestCockpit_FlowChipFollowsTheMode(t *testing.T) {
	c := Cockpit{Mode: "manual", ModeKind: CockpitGated, Flow: "auto", FlowKind: CockpitPermissive, CtxPct: 40, Spend: "$1.00", Round: "round 1 of 25"}
	if got := ansi.Strip(c.View(120)); !strings.HasPrefix(got, "⏸ manual → ⏵⏵ auto · ") {
		t.Fatalf("segment = %q", got)
	}
	if got := ansi.Strip(FitRail(c.RailSegments(), " · ", 20)); !strings.HasPrefix(got, "⏸ manual → ⏵⏵ auto") {
		t.Fatalf("a narrow rail shed the chip: %q", got)
	}
	plan := Cockpit{Mode: "auto", ModeKind: CockpitPermissive, Flow: "plan", FlowKind: CockpitGated, CtxPct: -1}
	if got := ansi.Strip(plan.View(80)); !strings.HasPrefix(got, "⏵⏵ auto → ⏸ plan") {
		t.Fatalf("segment = %q", got)
	}
	checking := Cockpit{Mode: "deciding", ModeKind: CockpitChecking, Flow: "auto", FlowKind: CockpitPermissive, CtxPct: -1}
	if got := ansi.Strip(checking.View(80)); strings.Contains(got, "→") {
		t.Fatalf("deciding should replace the whole segment: %q", got)
	}
}

func TestCockpit_CtxMeterFillAndThresholds(t *testing.T) {
	c := Cockpit{Mode: "manual", ModeKind: CockpitGated, CtxPct: 50}
	// The number leads the bar, which is how every rail carrying a context
	// meter draws one: the percentage is the figure, the bar is the shape it
	// is read against.
	if view := c.View(120); !strings.Contains(view, "ctx 50% ▰▰▰▰▱▱▱▱") {
		t.Fatalf("50%% should fill 4 of 8 cells behind its number:\n%s", view)
	}
	hidden := Cockpit{Mode: "manual", ModeKind: CockpitGated, CtxPct: -1}
	if view := hidden.View(120); strings.Contains(view, "ctx") {
		t.Fatalf("a negative CtxPct hides the meter:\n%s", view)
	}
	// Host-supplied thresholds (the trim warnings) override the defaults
	// without changing the bar's content.
	overridden := Cockpit{Mode: "manual", ModeKind: CockpitGated, CtxPct: 65, WarnPct: 60, AlertPct: 80}
	if view := overridden.View(120); !strings.Contains(view, "65%") {
		t.Fatalf("overridden thresholds keep the meter rendering:\n%s", view)
	}
}

func TestCockpit_DropsTrailingSegmentsWhenNarrow(t *testing.T) {
	c := Cockpit{Mode: "manual", ModeKind: CockpitGated, CtxPct: 42,
		Tokens: "↑41.2k ↓9.8k", Spend: "$0.14"}
	view := c.View(30)
	if lipgloss.Width(view) > 30 {
		t.Fatalf("narrow cockpit overflowed:\n%s", view)
	}
	if !strings.Contains(view, "manual") {
		t.Fatalf("the mode segment survives narrowing:\n%s", view)
	}
}

func TestAgentList_ViewAndKeys(t *testing.T) {
	l := &AgentList{Rows: []AgentRow{
		{State: AgentCurrent, Name: "orchestrator", Status: "round 7", Spend: "$0.14"},
		{State: AgentRunning, Name: "researcher-1", Task: "auth flow survey", Status: "running…", Spend: "$0.02",
			Progress: &AgentProgress{State: FanoutRunning}},
		{State: AgentBlocked, Name: "writer-1", Status: "waiting approval", Spend: "$0.05"},
		{State: AgentDone, Name: "researcher-2", Status: "done · 14 tools"},
		{State: AgentFailed, Name: "writer-2", Status: "failed · round limit"},
	}}
	view := l.View(90)
	for _, want := range []string{"●", "◇", "✓ done", "✗", "⚠ waiting approval", "orchestrator", "[enter] attach"} {
		if !strings.Contains(ansi.Strip(view), want) {
			t.Fatalf("agent list should contain %q:\n%s", want, view)
		}
	}

	l.Update(key("j"))
	l.Update(key("j"))
	if l.Focus != 2 {
		t.Fatalf("j should move focus, got %d", l.Focus)
	}
	if done, result := l.Update(key("d")); done || result.Action != AgentStop {
		t.Fatal("d should request the stop confirm and keep the list open")
	}
	done, result := l.Update(key("enter"))
	if !done || result != (agentListResult{Action: AgentAttach, Index: 2}) {
		t.Fatalf("enter should attach to the focused agent, got %v", result)
	}
	done, result = l.Update(key("esc"))
	if !done || result.Action != AgentBack {
		t.Fatal("esc should dismiss the list")
	}
}

func TestRenderCard_FrameAndClip(t *testing.T) {
	card := Card{Title: "Title"}.Render([]string{"row one", strings.Repeat("x", 200)}, 40)
	lines := strings.Split(card, "\n")
	if len(lines) != 4 {
		t.Fatalf("expected top, two rows, bottom; got %d lines", len(lines))
	}
	plain := stripANSI(card)
	if !strings.Contains(plain, "╭─ Title") || !strings.Contains(plain, "╰") {
		t.Fatalf("card should be framed with its title:\n%s", plain)
	}
	for i, l := range strings.Split(plain, "\n") {
		if w := len([]rune(l)); w != 40 {
			t.Fatalf("line %d should be exactly 40 cells, got %d: %q", i, w, l)
		}
	}
	if !strings.Contains(plain, "…") {
		t.Fatal("overlong rows clip with an ellipsis")
	}
}

// A key row takes another row rather than losing anything to a clip
// (docs/interface/principles.md#fold-never-hide), and it packs the rows
// greedily: one segment per row was three rows where two would do, and on a
// card whose hints come off its own list that is two entries the reader does
// not see.
func TestHintRows_WrapRatherThanClip(t *testing.T) {
	segments := []KeyOffer{{Key: "[space]", Label: "toggle"}, {Key: "[a]", Label: "all/none"},
		{Key: "[enter]", Label: "apply"}, keyOfferAs(keys.Select.Cancel, EscBack)}
	if rows := hintRows(segments, 80); len(rows) != 1 {
		t.Fatalf("a row that fits is one row, got %d", len(rows))
	}
	for _, width := range []int{30, 40, 55, 64, 70} {
		rows := hintRows(segments, width)
		plain := strings.Join(rows, " ")
		for _, seg := range segments {
			if !strings.Contains(ansi.Strip(plain), seg.Key+" "+seg.Label) {
				t.Errorf("at width %d the row lost %q:\n%s", width, seg.Key, plain)
			}
		}
		for _, row := range rows {
			if w := lipgloss.Width(ansi.Strip(row)); w > width-cardFrameWidth {
				t.Errorf("at width %d a row is %d columns wide: %q", width, w, row)
			}
		}
	}
}

// The rail must terminate at every width, including the ones where the right
// side has just been emptied: a shedding chain that can re-widen loops for
// ever, and it loops inside the render path of a narrow terminal.
func TestCockpit_ViewTerminatesAtEveryWidth(t *testing.T) {
	c := Cockpit{Mode: "accept edits", ModeKind: CockpitPermissive, Round: "round 7 of 25",
		CtxPct: 62, Tokens: "↑41.2k ↓9.8k", Spend: "$0.14", Agents: 2,
		Extra: []string{"1 queued"}}
	for w := 0; w <= 120; w++ {
		if got := lipgloss.Width(c.View(w)); got > w && w > 0 {
			t.Fatalf("width %d rendered %d columns", w, got)
		}
	}
}
