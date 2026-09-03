package chat

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// statusRowModel is a below-threshold surface with a reading on it and one file
// changed, which is the state the row exists for.
func statusRowModel(t *testing.T, width int) Model {
	t.Helper()
	m := frameModel(t, width, 40)
	m.summarizer = agent.NewSummarizer(&readingProvider{}, agent.SummaryConfig{Model: "fast"})
	m.summary.last = &agent.SummaryVerdict{Text: "wiring the pause", State: agent.SummaryOnTarget, Round: 7}
	m.summary.lastRound = 7
	m.turnCount = 1
	m.changes.Add(1, changeset.Record{
		Path: "internal/agent/loop.go", BeforeExists: true, AfterExists: true,
		Before: "c\n", After: "a\nb\n",
	})
	return m
}

func TestStatusRow_LiveTurnCountsTheTurn(t *testing.T) {
	m := statusRowModel(t, 80)
	m.state = stateStreaming
	row := stripANSI(m.statusRow())
	for _, want := range []string{"▸ on target", "as of round 7", "1 file this turn"} {
		if !strings.Contains(row, want) {
			t.Fatalf("status row = %q, want %q in it", row, want)
		}
	}
	if strings.Contains(row, "session") {
		t.Fatalf("a live turn answers for the turn, not the session: %q", row)
	}
}

func TestStatusRow_IdleCountsTheSession(t *testing.T) {
	row := stripANSI(statusRowModel(t, 80).statusRow())
	for _, want := range []string{"▸ on target", "as of round 7", "session · +2 −1"} {
		if !strings.Contains(row, want) {
			t.Fatalf("status row = %q, want %q in it", row, want)
		}
	}
}

// outrun runs the agent past the reading taken at round n by two whole
// intervals, which is what makes a reading stale.
func outrun(m *Model, n int) {
	for range n + 2*m.summaryInterval() + 1 {
		m.agent.BeginToolRound("", []provider.ToolCall{{Name: "read_file"}}, nil)
	}
}

// A reading the session has outrun is marked exactly as the rail's own
// heading marks it, so the row never passes an old sentence off as current.
func TestStatusRow_StaleReadingSaysSo(t *testing.T) {
	m := statusRowModel(t, 80)
	outrun(&m, m.summary.lastRound)
	if row := stripANSI(m.statusRow()); !strings.Contains(row, "stale") {
		t.Fatalf("an outrun reading is marked stale: %q", row)
	}
}

// Nothing to say is no row at all, and the frame budgets no line for it.
func TestStatusRow_NothingToSay(t *testing.T) {
	m := frameModel(t, 80, 40)
	if row := m.statusRow(); row != "" {
		t.Fatalf("no reading and no changes is no row: %q", stripANSI(row))
	}
	before := m.frameExtraHeight()
	full := statusRowModel(t, 80)
	if after := full.frameExtraHeight(); after != before+1 {
		t.Fatalf("the row costs exactly one line: %d then %d", before, after)
	}
}

// A conversation has no changeset, so the session clause is absent rather
// than a zero it would have to invent.
func TestStatusRow_ConversationHasNoChanges(t *testing.T) {
	m := statusRowModel(t, 80).WithConversation()
	row := stripANSI(m.statusRow())
	if !strings.Contains(row, "on target") {
		t.Fatalf("the reading still stands in a conversation: %q", row)
	}
	if strings.Contains(row, "session") || strings.Contains(row, "file") {
		t.Fatalf("a conversation counts no files: %q", row)
	}
}

// A session whose only change is a file's permissions has a file to name and
// no lines to count, so the row says what it touched. `session · +0 −0` would
// report a measurement of nothing over a real change.
func TestStatusRow_ChangesWithNothingToCount(t *testing.T) {
	m := frameModel(t, 80, 40)
	m.turnCount = 1
	m.changes.Add(1, changeset.Record{
		Path: "scripts/build.sh", BeforeExists: true, AfterExists: true,
		Before: "one\n", After: "one\n", BeforeMode: 0o644, AfterMode: 0o755,
	})
	row := stripANSI(m.statusRow())
	if !strings.Contains(row, "session · 1 file") {
		t.Fatalf("the row names what the session changed: %q", row)
	}
	if strings.Contains(row, "+0 −0") {
		t.Fatalf("nothing counted this change: %q", row)
	}
	if got := m.summaryChanges(); got != "1 file" {
		t.Fatalf("the reading is told the same thing, got %q", got)
	}
}

// The first minute of a session has changes and no reading yet. The row is
// what there is to say, not everything it can say.
func TestStatusRow_ChangesWithoutAReading(t *testing.T) {
	m := statusRowModel(t, 80)
	m.summary.last = nil
	row := stripANSI(m.statusRow())
	if !strings.Contains(row, "session · +2 −1") {
		t.Fatalf("the changes still stand without a reading: %q", row)
	}
	if strings.Contains(row, "target") {
		t.Fatalf("no reading is no verdict: %q", row)
	}
}

// A takeover surface owns the whole panel, so there is no frame for the row to
// sit above — and no row in the budget either.
func TestStatusRow_AbsentUnderATakeover(t *testing.T) {
	m := statusRowModel(t, 80)
	m.state = stateContext
	if m.frameShowing() {
		t.Fatal("a takeover surface replaces the frame")
	}
	if m.frameExtraHeight() != 0 {
		t.Fatalf("a hidden frame budgets no rails: %d", m.frameExtraHeight())
	}
	if strings.Contains(stripANSI(m.View().Content), "on target") {
		t.Fatal("the row is not painted over a takeover")
	}
}

// The rail answers for itself where there is a rail; the row is what stands
// in when there is not.
func TestStatusRow_AbsentBesideTheRail(t *testing.T) {
	wide := components.InspectorMinContentWidth + horizontalPadding*2
	m := statusRowModel(t, wide)
	if !m.twoPane() {
		t.Fatalf("%d terminal columns should split", wide)
	}
	if row := m.statusRow(); row != "" {
		t.Fatalf("the rail is drawing this already: %q", stripANSI(row))
	}
	narrow := statusRowModel(t, wide-1)
	if narrow.twoPane() {
		t.Fatalf("%d terminal columns should not split", wide-1)
	}
	if narrow.statusRow() == "" {
		t.Fatal("below the threshold the row stands in for the rail")
	}
}

// The rail keeps its session-wide blocks beside an agent's transcript because
// it has room to mark which session the keyboard is in. This row does not:
// at these widths it is one line beside the agent's own status bar, and an
// unmarked session-wide reading there would read as the agent's.
func TestStatusRow_HiddenWhileAttached(t *testing.T) {
	m := statusRowModel(t, 80)
	m.attachedTo = "researcher-1"
	if row := m.statusRow(); row != "" {
		t.Fatalf("attached, the row is the wrong session's: %q", stripANSI(row))
	}
}

// The clauses drop from the right as the columns run out: the file count goes
// before the verdict, and the verdict is the last thing standing.
func TestStatusRow_DropsFromTheRight(t *testing.T) {
	m := statusRowModel(t, 60)
	m.state = stateStreaming
	m.summary.last.Round, m.summary.lastRound = 128, 128
	outrun(&m, 128)
	for i := range 12 {
		m.changes.Add(1, changeset.Record{
			Path: "internal/agent/f" + string(rune('a'+i)) + ".go", AfterExists: true, After: "x\n",
		})
	}
	row := stripANSI(m.statusRow())
	if strings.Contains(row, "this turn") {
		t.Fatalf("the rightmost clause goes first at 60 columns: %q", row)
	}
	if !strings.Contains(row, "on target") {
		t.Fatalf("the verdict is the last clause standing: %q", row)
	}
	if width := lipglossWidth(m.statusRow()); width > m.contentWidth() {
		t.Fatalf("the row is %d columns wide in %d", width, m.contentWidth())
	}
}

// The row is one line of the frame's own rows, not an extra the surface pays
// for twice: the panel it is drawn in is exactly as tall as it was budgeted.
func TestStatusRow_FitsTheBottomPanel(t *testing.T) {
	m := statusRowModel(t, 80)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m = updated.(Model)
	m.turnStarted = time.Now()
	rows := strings.Count(m.renderPromptFrame(), "\n") + 1
	if rows != m.bottomRows()-m.interruptHeight() {
		t.Fatalf("frame drew %d rows, budgeted %d", rows, m.bottomRows()-m.interruptHeight())
	}
	if !strings.Contains(stripANSI(m.View().Content), "on target") {
		t.Fatal("the row is on the painted screen")
	}
}
