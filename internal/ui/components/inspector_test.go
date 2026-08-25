package components

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// fullRail is a rail with every block populated.
func fullRail() InspectorRail {
	return InspectorRail{
		Turn: &InspectorTurn{Step: 3, Steps: 4, Tools: 18, Elapsed: 64 * time.Second, Running: true},
		Changes: &InspectorChanges{
			Files: []InspectorFile{
				{Path: "agent/loop.go", Added: 18, Removed: 3},
				{Path: "ui/chat/model.go", Added: 9, Removed: 1},
			},
			Added: 27, Removed: 4,
			Failure: "go test ./...", FailureNote: OutcomeExit(1),
		},
		Agents: []InspectorAgent{{Name: "writer-1", Detail: "docs/loop.md", Spend: "$0.02", Tools: 4}},
		Context: &InspectorContext{Pct: 62, Tokens: 124000, Window: 200000,
			Tokens1: "↑41.2k", Tokens2: "↓9.8k", Burn: []float64{1, 2, 3, 3, 4, 5, 5, 6}},
		Spend: &InspectorSpend{Turn: "$0.14", Main: "$0.12", Children: "$0.02",
			Session: "$1.86", Model: "gpt-5.2"},
	}
}

// A context nobody vouched for says so, in a word and not just a sigil, so
// the claim survives a monochrome terminal (S-093).
func TestInspectorContext_EstimatedSaysSo(t *testing.T) {
	r := InspectorRail{Context: &InspectorContext{
		Pct: 41, Tokens: 82000, Window: 200000, Estimated: true,
	}}
	view := stripANSI(r.View(InspectorWidth, 0))
	for _, want := range []string{"CONTEXT", "41% of 200k", "~82k", "estimated"} {
		if !strings.Contains(view, want) {
			t.Fatalf("an estimated context should show %q:\n%s", want, view)
		}
	}
	// A reported one states the number plainly, with no hedge.
	reported := InspectorRail{Context: &InspectorContext{Pct: 41, Tokens: 82000, Window: 200000}}
	view = stripANSI(reported.View(InspectorWidth, 0))
	if strings.Contains(view, "estimated") || strings.Contains(view, "~82k") {
		t.Fatalf("a reported context should not hedge:\n%s", view)
	}
}

func TestInspectorRail_BlockOrderAndContents(t *testing.T) {
	view := stripANSI(fullRail().View(InspectorWidth, 0))
	for _, want := range []string{
		"THIS TURN", "step 3 of 4", "▰", "18 tools", "1m 04s elapsed",
		"CHANGES", "+27", "−4", "▎✎ agent/loop.go", "+18", "−3",
		"✗ go test ./...", "exit 1",
		"AGENTS", "1 running", "◇ writer-1", "$0.02", "docs/loop.md · 4 tools",
		"CONTEXT", "62% of 200k", "124k", "↑41.2k ↓9.8k", "per round",
		"SPEND", "$0.14", "gpt-5.2 · $0.12 main · $0.02 ◇", "session total $1.86",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("rail missing %q:\n%s", want, view)
		}
	}
	// The order is fixed (§15a).
	order := []string{"THIS TURN", "CHANGES", "AGENTS", "CONTEXT", "SPEND"}
	at := 0
	for _, label := range order {
		i := strings.Index(view[at:], label)
		if i < 0 {
			t.Fatalf("%q out of order in:\n%s", label, view)
		}
		at += i
	}
}

func TestInspectorRail_OmitsEmptyBlocks(t *testing.T) {
	if got := (InspectorRail{}).Lines(InspectorWidth, 0); got != nil {
		t.Fatalf("an empty rail renders nothing, got %q", got)
	}
	if !(InspectorRail{}).Empty() {
		t.Fatal("an empty rail reports Empty")
	}
	// A session with no children has no AGENTS heading at all (§15b).
	r := fullRail()
	r.Agents = nil
	r.Changes = nil
	view := stripANSI(r.View(InspectorWidth, 0))
	for _, absent := range []string{"AGENTS", "CHANGES", "running"} {
		if strings.Contains(view, absent) {
			t.Fatalf("rail should omit %q:\n%s", absent, view)
		}
	}
	if !strings.Contains(view, "THIS TURN") || !strings.Contains(view, "SPEND") {
		t.Fatalf("remaining blocks still render:\n%s", view)
	}
	// Blocks are never rendered empty: no heading without its rows.
	if strings.Contains(view, "\n\n\n") {
		t.Fatalf("rail has an empty block:\n%s", view)
	}
}

func TestInspectorRail_AgentLaneOnlyMetersDeclaredSteps(t *testing.T) {
	// No declared step count: the lane moves rather than drawing a ratio
	// nobody supplied (S-094).
	r := InspectorRail{
		Agents: []InspectorAgent{{Name: "writer-1", Detail: "editing docs/loop.md", Tools: 4}},
		Frame:  2,
	}
	view := stripANSI(r.View(InspectorWidth, 0))
	if strings.Contains(view, "▰") {
		t.Fatalf("no bar without a declared step count:\n%s", view)
	}
	if !strings.Contains(view, "⠹ editing docs/loop.md") {
		t.Fatalf("the spinner names what is running:\n%s", view)
	}
	// A declared count earns the five-cell lane meter, and the meter states
	// the count beside it.
	r.Agents[0].Step, r.Agents[0].Steps = 2, 4
	view = stripANSI(r.View(InspectorWidth, 0))
	if !strings.Contains(view, "▰▰▱▱▱ step 2 of 4") {
		t.Fatalf("a declared count draws the lane meter:\n%s", view)
	}
	if strings.Contains(view, "⠹") {
		t.Fatalf("a lane with a bar does not also spin:\n%s", view)
	}
	// A child waiting on the user is not running, so it gets neither.
	r.Agents[0].Step, r.Agents[0].Steps = 0, 0
	r.Agents[0].Blocked = true
	view = stripANSI(r.View(InspectorWidth, 0))
	if strings.Contains(view, "⠹") || strings.Contains(view, "▰") {
		t.Fatalf("a blocked lane shows no motion and no bar:\n%s", view)
	}
	if !strings.Contains(view, "⚠ writer-1") {
		t.Fatalf("a blocked lane says so:\n%s", view)
	}
}

func TestInspectorRail_RowsFitTheWidth(t *testing.T) {
	long := fullRail()
	long.Changes.Files = append(long.Changes.Files, InspectorFile{
		Path: "internal/ui/components/a/very/deeply/nested/path/name.go", Added: 120, Removed: 99})
	for _, line := range long.Lines(InspectorWidth, 0) {
		if w := lipgloss.Width(line); w > InspectorWidth {
			t.Fatalf("line %q is %d columns, rail is %d", stripANSI(line), w, InspectorWidth)
		}
	}
	// The right field is the number, so it survives and the path clips.
	view := stripANSI(long.View(InspectorWidth, 0))
	if !strings.Contains(view, "+120") || !strings.Contains(view, "−99") {
		t.Fatalf("counts must not clip:\n%s", view)
	}
	if !strings.Contains(view, "…") {
		t.Fatalf("an overlong path clips with an ellipsis:\n%s", view)
	}
}

func TestInspectorRail_HeadingMetaIsRightAligned(t *testing.T) {
	lines := fullRail().Lines(InspectorWidth, 0)
	for _, line := range lines {
		plain := stripANSI(line)
		if !strings.HasPrefix(plain, "  CONTEXT") {
			continue
		}
		if !strings.HasSuffix(plain, "62% of 200k") || lipgloss.Width(line) != InspectorWidth {
			t.Fatalf("heading meta sits on the rail's right edge, got %q (%d cols)", plain, lipgloss.Width(line))
		}
		return
	}
	t.Fatal("CONTEXT heading not found")
}

func TestInspectorRail_TruncatesLongestBlockFirst(t *testing.T) {
	r := fullRail()
	for i := 0; i < 12; i++ {
		r.Changes.Files = append(r.Changes.Files, InspectorFile{Path: "pkg/f.go", Added: 1, Removed: 1})
	}
	full := r.Lines(InspectorWidth, 0)
	height := len(full) - 6
	lines := r.Lines(InspectorWidth, height)
	if len(lines) > height {
		t.Fatalf("rail must fit %d rows, got %d", height, len(lines))
	}
	view := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(view, "… ") || !strings.Contains(view, "more") {
		t.Fatalf("a truncated block says what it swallowed:\n%s", view)
	}
	// The longest block gave up the rows; the short ones kept theirs.
	for _, want := range []string{"THIS TURN", "SPEND", "session total $1.86", "CONTEXT"} {
		if !strings.Contains(view, want) {
			t.Fatalf("truncation should not drop %q:\n%s", want, view)
		}
	}
}

func TestInspectorRail_NoDeclaredStepsNoRatio(t *testing.T) {
	r := InspectorRail{Turn: &InspectorTurn{Step: 3, Tools: 7, Elapsed: 2 * time.Second, Running: true}}
	view := stripANSI(r.View(InspectorWidth, 0))
	if !strings.Contains(view, "step 3") || strings.Contains(view, "of") {
		t.Fatalf("an observed step count has no denominator:\n%s", view)
	}
	if strings.Contains(view, "▰") {
		t.Fatalf("no meter without a declared step count:\n%s", view)
	}
	if !strings.Contains(view, "7 tools · 2.0s elapsed") {
		t.Fatalf("the block still states its counts:\n%s", view)
	}
	// A turn that has finished says so rather than counting on.
	done := InspectorRail{Turn: &InspectorTurn{Tools: 1, Elapsed: time.Second}}
	if !strings.Contains(stripANSI(done.View(InspectorWidth, 0)), "1 tool · 1.0s total") {
		t.Fatalf("a finished turn reports a total:\n%s", done.View(InspectorWidth, 0))
	}
}

func TestInspectorElapsedAndTokens(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{900 * time.Millisecond, "0.9s"}, {12 * time.Second, "12s"},
		{64 * time.Second, "1m 04s"}, {2 * time.Hour, "120m 00s"},
	} {
		if got := FormatElapsed(c.d); got != c.want {
			t.Fatalf("FormatElapsed(%s) = %q, want %q", c.d, got, c.want)
		}
	}
	for _, c := range []struct {
		n    int64
		want string
	}{{0, "0"}, {999, "999"}, {1200, "1.2k"}, {124000, "124k"}, {2_000_000, "2.0M"}} {
		if got := formatTokens(c.n); got != c.want {
			t.Fatalf("formatTokens(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
