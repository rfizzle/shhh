package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// planFixture is the card every test here starts from: four steps, a computed
// summary, and the five options plan approval offers.
func planFixture() *PlanCard {
	return &PlanCard{
		Title: "Plan · make the round limit recoverable",
		Chip:  "4 steps",
		Steps: []PlanStep{
			{Number: 1, Title: "Locate the round accounting",
				Detail: "internal/agent/loop.go · internal/agent/round.go",
				Kind:   "read only", KindTone: ToneSafe},
			{Number: 2, Title: "Add a RoundsExhausted sentinel",
				Detail: "internal/agent/errors.go · new type, no signature changes",
				Kind:   "✎ creates 1 file"},
			{Number: 3, Title: "Return it from runRound and handle it in Run",
				Detail: "internal/agent/loop.go", Kind: "✎ edits 1 file"},
			{Number: 4, Title: "Offer more rounds in the chat model",
				Detail: "internal/ui/chat/model.go", Kind: "✎ edits 1 file"},
		},
		Summary: []PlanFact{
			{Text: "3 files touched"},
			{Text: "no deletes", Tone: ToneSafe},
			{Text: "no network", Tone: ToneSafe},
			{Text: "reversible", Tone: ToneSafe},
		},
		SummaryDetail: "every file is tracked in git",
		Options: []SelectOption{
			{Label: "Run the whole plan — accept-edits mode", Desc: "edits apply as they come"},
			{Label: "Run it unattended — auto mode", Desc: "the classifier judges the rest"},
			{Label: "Step through it — manual approvals", Desc: "every edit asks you first"},
		},
		Hint: "↑↓/jk move · enter select · s save · esc keep planning",
	}
}

func planView(c *PlanCard, width int) string {
	return ansi.Strip(c.View(width))
}

func TestPlanCard_StepsCarryTheirPaths(t *testing.T) {
	view := planView(planFixture(), 100)
	for _, want := range []string{
		"1 Locate the round accounting",
		"internal/agent/loop.go · internal/agent/round.go",
		"4 Offer more rounds in the chat model",
		"internal/ui/chat/model.go",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view should carry %q:\n%s", want, view)
		}
	}
}

func TestPlanCard_StepKindIsRightAligned(t *testing.T) {
	view := planView(planFixture(), 100)
	for _, line := range strings.Split(view, "\n") {
		if !strings.Contains(line, "Locate the round accounting") {
			continue
		}
		if !strings.HasSuffix(strings.TrimRight(line, " │"), "read only") {
			t.Fatalf("the intent should sit at the right edge, got %q", line)
		}
		return
	}
	t.Fatal("step 1 never rendered")
}

func TestPlanCard_TitleAndStepCount(t *testing.T) {
	view := planView(planFixture(), 100)
	top := strings.SplitN(view, "\n", 2)[0]
	if !strings.Contains(top, "Plan · make the round limit recoverable") {
		t.Errorf("the title belongs in the border, got %q", top)
	}
	if !strings.Contains(top, "4 steps") {
		t.Errorf("the step count belongs on the title rail, got %q", top)
	}
}

func TestPlanCard_SummaryLine(t *testing.T) {
	view := planView(planFixture(), 100)
	want := "3 files touched · no deletes · no network · reversible — every file is tracked in git"
	if !strings.Contains(view, want) {
		t.Errorf("view should carry the computed summary %q:\n%s", want, view)
	}
}

func TestPlanCard_SummaryDetailDropsBeforeItIsClipped(t *testing.T) {
	// At a width that cannot carry both, the qualifier goes and the clauses
	// stay whole — half a statement is worse than a shorter one.
	view := planView(planFixture(), 60)
	if !strings.Contains(view, "no deletes") {
		t.Fatalf("the clauses must survive the narrow width:\n%s", view)
	}
	if strings.Contains(view, "tracked in git") {
		t.Errorf("the detail should have been dropped at 60 columns:\n%s", view)
	}
}

func TestPlanCard_OnlyTheFocusedOptionExplainsItself(t *testing.T) {
	c := planFixture()
	c.Focus = 1
	view := planView(c, 100)
	if !strings.Contains(view, "❯ 2. Run it unattended — auto mode") {
		t.Errorf("the pointer should be on option 2:\n%s", view)
	}
	if !strings.Contains(view, "the classifier judges the rest") {
		t.Errorf("the focused option should explain itself:\n%s", view)
	}
	for _, unwanted := range []string{"edits apply as they come", "every edit asks you first"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("only the focused option explains itself, found %q:\n%s", unwanted, view)
		}
	}
}

func TestPlanCard_OptionsAreNumberedAndKeyed(t *testing.T) {
	view := planView(planFixture(), 100)
	for _, want := range []string{"1. Run the whole plan", "3. Step through it", "s save"} {
		if !strings.Contains(view, want) {
			t.Errorf("view should carry %q:\n%s", want, view)
		}
	}
}

func TestPlanCard_BoundedHeightCountsWhatItDrops(t *testing.T) {
	c := planFixture()
	c.MaxLines = 16
	view := planView(c, 100)
	if got := len(strings.Split(view, "\n")); got > c.MaxLines {
		t.Fatalf("card rendered %d rows, bound is %d:\n%s", got, c.MaxLines, view)
	}
	if !strings.Contains(view, "more step") {
		t.Errorf("dropped steps should be counted, not lost:\n%s", view)
	}
	// The decision survives the squeeze: options and keys are never what the
	// bound takes.
	for _, want := range []string{"Run the whole plan", "esc keep planning"} {
		if !strings.Contains(view, want) {
			t.Errorf("the bound must not eat %q:\n%s", want, view)
		}
	}
}

func TestPlanCard_TooShortForAStepSaysSo(t *testing.T) {
	c := planFixture()
	c.MaxLines = 11
	view := planView(c, 100)
	if !strings.Contains(view, "4 steps, no room to show them") {
		t.Errorf("a card with room for no step should say so, not count 'more':\n%s", view)
	}
}

func TestPlanCard_ProseFallback(t *testing.T) {
	c := planFixture()
	c.Steps, c.Chip, c.Summary, c.SummaryDetail = nil, "", nil, ""
	c.Title = "Plan ready"
	c.Prose = []string{
		"I'd add a sentinel error to the agent package and return it",
		"from the round loop, then handle it in the chat model.",
	}
	view := planView(c, 100)
	if !strings.Contains(view, "add a sentinel error") {
		t.Errorf("an unstructured plan should still render:\n%s", view)
	}
	if !strings.Contains(view, "1. Run the whole plan") {
		t.Errorf("the options belong below the prose:\n%s", view)
	}
	if strings.Contains(view, "files touched") {
		t.Errorf("nothing was computed, so nothing should be claimed:\n%s", view)
	}
}

func TestPlanCard_ProseFallbackIsBounded(t *testing.T) {
	c := planFixture()
	c.Steps, c.Summary, c.SummaryDetail = nil, nil, ""
	c.MaxLines = 12
	for range 30 {
		c.Prose = append(c.Prose, "a line of the plan the model wrote as prose")
	}
	view := planView(c, 100)
	if got := len(strings.Split(view, "\n")); got > c.MaxLines {
		t.Fatalf("prose fallback rendered %d rows, bound is %d", got, c.MaxLines)
	}
	if !strings.Contains(view, "…") {
		t.Errorf("a truncated fallback should say so:\n%s", view)
	}
}

func TestPlanCard_NarrowTerminalKeepsTheStepTitle(t *testing.T) {
	// The intent label goes before a title is cut in half: a title cut in
	// half says less than a missing label does.
	view := planView(planFixture(), 44)
	if !strings.Contains(view, "Locate the round") {
		t.Errorf("the step title should survive a narrow terminal:\n%s", view)
	}
}

func TestPlanCard_RowsNeverExceedTheWidth(t *testing.T) {
	for _, width := range []int{40, 60, 80, 110, 130} {
		for _, line := range strings.Split(planView(planFixture(), width), "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Errorf("width %d: row is %d cells wide: %q", width, got, line)
			}
		}
	}
}
