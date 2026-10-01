package chat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/ui/components"
)

func readEntry(path string, d time.Duration) entry {
	return entry{kind: entryTool, toolName: "read_file",
		toolArgs: fmt.Sprintf(`{"path":%q}`, path), toolResult: "a\nb", duration: d}
}

func searchEntry(pattern string, d time.Duration) entry {
	return entry{kind: entryTool, toolName: "search",
		toolArgs: fmt.Sprintf(`{"pattern":%q}`, pattern), toolResult: "hit", duration: d}
}

// foldModel builds a one-step turn whose calls are six reads and two searches
// followed by an edit and a broken command.
func foldModel(t *testing.T) Model {
	t.Helper()
	m := activityModel(t)
	m.transcript = []entry{
		{kind: entryUser, text: "fix the round limit"},
		{kind: entryAssistant, text: "Thread the sentinel through the loop"},
		readEntry("internal/agent/loop.go", 400*time.Millisecond),
		readEntry("internal/agent/round.go", 200*time.Millisecond),
		readEntry("internal/agent/tool.go", 300*time.Millisecond),
		readEntry("internal/agent/mode.go", 500*time.Millisecond),
		readEntry("internal/agent/session.go", 600*time.Millisecond),
		readEntry("internal/agent/context.go", 400*time.Millisecond),
		searchEntry("ErrRoundLimit", 800*time.Millisecond),
		searchEntry("roundLimit", 700*time.Millisecond),
		{kind: entryTool, toolName: "edit_file", toolArgs: `{"path":"internal/agent/loop.go"}`,
			toolResult: "edited", duration: 1100 * time.Millisecond},
		{kind: entryCommand, text: "go test ./internal/agent/...",
			toolResult: "--- FAIL: TestRoundLimit", exitCode: 1, duration: 21400 * time.Millisecond},
	}
	m.invalidateRenderCache()
	return m
}

// cardModel is a finished turn of three steps — reads only, reads then a
// command, a write — the way the catalogue's whole turn opens.
func cardModel(t *testing.T) Model {
	t.Helper()
	m := activityModel(t)
	m.transcript = []entry{
		{kind: entryUser, text: "write the copy story"},
		{kind: entryAssistant, text: "Listing .plan and reading the backlog's format before I write anything."},
		readEntry(".plan/BACKLOG.md", 300*time.Millisecond),
		readEntry(".plan/NOTES.md", 200*time.Millisecond),
		searchEntry("copy", 400*time.Millisecond),
		{kind: entryAssistant, text: "Confirming .plan/ is untracked, so nothing I write gets committed."},
		readEntry("docs/scenes.md", 100*time.Millisecond),
		{kind: entryCommand, text: "git status --short .plan/", toolResult: "?? .plan/", duration: 100 * time.Millisecond},
		{kind: entryAssistant, text: "Writing the epic and two stories now."},
		{kind: entryTool, toolName: "write_file", toolArgs: `{"path":".plan/BACKLOG.md"}`,
			toolResult: "wrote .plan/BACKLOG.md", duration: 1100 * time.Millisecond},
	}
	m.invalidateRenderCache()
	return m
}

// slicesContain reports whether idx is in idxs.
func slicesContain(idxs []int, idx int) bool {
	for _, i := range idxs {
		if i == idx {
			return true
		}
	}
	return false
}

// cardLines renders the transcript with the colour taken off, one line each.
func cardLines(m Model) []string {
	return strings.Split(stripANSI(m.renderHistory()), "\n")
}

// cardLine is the first rendered line holding s, or "".
func cardLine(lines []string, s string) string {
	for _, l := range lines {
		if strings.Contains(l, s) {
			return l
		}
	}
	return ""
}

// The header is the step's receipt: the glyph by precedence and the rail
// where the step wrote or ran, the verb, the rollup, and on the right the
// outcome and the time. A step of one call names what it was about.
func TestCard_HeaderIsTheStepReceipt(t *testing.T) {
	m := cardModel(t)
	lines := cardLines(m)
	cases := []struct {
		name, want string
	}{
		{"reads only: no rail, ⚙, counted by directory with the search beside them", "  ⚙ read 2 files in .plan/ · searched copy"},
		{"reads then a command: the rail, $, the command one call", " ▎$ read docs/scenes.md · ran git status --short .plan/"},
		{"a write: the rail, ✎, the file it wrote", " ▎✎ wrote .plan/BACKLOG.md"},
	}
	for _, tc := range cases {
		if cardLine(lines, tc.want) == "" {
			t.Errorf("%s: no header %q in\n%s", tc.name, tc.want, strings.Join(lines, "\n"))
		}
	}
	// The step's own time, summed from its calls, sits at the right.
	if h := cardLine(lines, "read 2 files"); !strings.HasSuffix(strings.TrimRight(h, " "), "0.9s") {
		t.Errorf("the step's time is not at the right of its header: %q", h)
	}

	m.transcript = foldModel(t).transcript
	m.invalidateRenderCache()
	h := cardLine(cardLines(m), "read 6 files")
	if !strings.HasPrefix(h, " ▎✗ read 6 files") {
		t.Errorf("a step whose command failed draws ✗ and a red rail over its write: %q", h)
	}
	if !strings.HasSuffix(strings.TrimRight(h, " "), "exit 1") {
		t.Errorf("a failed step's outcome is the failure, and it never drops: %q", h)
	}
	if strings.Contains(h, "tools") {
		t.Errorf("the card states what the calls did, not how many tools ran: %q", h)
	}
}

// The body is the prose that titled the step, whole however long, wrapped
// at the body column.
func TestCard_BodyIsTheTitlingProseWhole(t *testing.T) {
	m := cardModel(t)
	long := "Reading the round accounting end to end before touching it, because the counter is read in " +
		"three places and the sentinel has to be threaded through every one of them or the pause never fires."
	m.transcript[1].text = long
	m.invalidateRenderCache()
	view := stripANSI(m.renderHistory())
	if !strings.Contains(strings.Join(strings.Fields(view), " "), long) {
		t.Fatalf("the titling prose is not drawn whole:\n%s", view)
	}
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "Reading the round") && !strings.HasPrefix(l, strings.Repeat(" ", components.CardBodyIndent)+"Reading") {
			t.Errorf("the body does not start at the body column: %q", l)
		}
	}
	if blk, ok := m.stepBlockAt(m.transcript, 1); !ok || blk.step == nil {
		t.Fatal("a long sentence titles its step rather than standing above it")
	}
}

// Where the model said nothing the card has no body, and its footer follows
// the header; where a reading stands, the reading's sentence is the body.
func TestCard_NoProseNoBody(t *testing.T) {
	m := activityModel(t)
	m.transcript = []entry{
		{kind: entryUser, text: "check it"},
		{kind: entryCommand, text: "git status --short", toolResult: "?? .plan/"},
	}
	m.invalidateRenderCache()
	lines := cardLines(m)
	at := -1
	for i, l := range lines {
		if strings.Contains(l, "ran git status --short") {
			at = i
		}
	}
	if at < 0 || at+1 >= len(lines) || !strings.Contains(lines[at+1], "?? .plan/") {
		t.Fatalf("with no prose the footer follows the header:\n%s", strings.Join(lines, "\n"))
	}

	m.transcript = append(m.transcript, entry{kind: entrySummary, reading: &summaryReading{
		verdict: agent.SummaryVerdict{State: agent.SummaryOffTarget, Round: 2, Text: "Checking the tree before it writes."}}})
	m.invalidateRenderCache()
	view := m.renderHistory()
	lines = strings.Split(stripANSI(view), "\n")
	h := cardLine(lines, "ran git status --short")
	body := cardLine(lines, "Checking the tree before it writes.")
	if body == "" || !strings.HasPrefix(body, strings.Repeat(" ", components.CardBodyIndent)) {
		t.Fatalf("a reading stands in for prose the model never said:\n%s", strings.Join(lines, "\n"))
	}
	if strings.Contains(stripANSI(view), "summary round 2") {
		t.Errorf("a reading the card took is not drawn a second time as a row:\n%s", stripANSI(view))
	}
	if !strings.Contains(stripANSI(view), "≡ off target") || h == "" {
		t.Errorf("the reading's verdict is on the card:\n%s", stripANSI(view))
	}
}

// The footer is drawn only where the step's receipt has evidence: a step
// that only read, several files, has nothing one line could add.
func TestCard_FooterOnlyWithEvidence(t *testing.T) {
	m := activityModel(t)
	m.transcript = []entry{
		{kind: entryUser, text: "look"},
		{kind: entryAssistant, text: "Reading the two halves of the loop."},
		readEntry("internal/agent/loop.go", 0),
		readEntry("docs/loop.md", 0),
	}
	m.invalidateRenderCache()
	lines := cardLines(m)
	at := -1
	for i, l := range lines {
		if strings.Contains(l, "Reading the two halves") {
			at = i
		}
	}
	if at < 0 || strings.TrimSpace(lines[at+1]) != "" {
		t.Fatalf("a quiet read ends on its body and the padding row:\n%s", strings.Join(lines, "\n"))
	}

	m.transcript = append(m.transcript, entry{kind: entryCommand, text: "go vet ./...", toolResult: "vet: a.go:3: bad", exitCode: 1})
	m.invalidateRenderCache()
	if l := cardLine(cardLines(m), "vet: a.go:3: bad"); !strings.HasPrefix(l, strings.Repeat(" ", components.CardBodyIndent)+"vet:") {
		t.Errorf("a failed command's last line is the footer's evidence: %q", l)
	}
}

// A padding row inside the band above and below, one blank between two
// cards, and prose with no calls on bare screen with a blank either side.
func TestCard_OneBlankBetweenCards(t *testing.T) {
	m := cardModel(t)
	m.transcript = append(m.transcript, entry{kind: entryAssistant, text: "Done.\n\nTwo stories are in the backlog."})
	m.invalidateRenderCache()
	lines := cardLines(m)
	pads := 0
	for i := 1; i+1 < len(lines); i++ {
		if strings.Contains(lines[i+1], "ran git status") {
			// header ← padding row ← blank ← the last card's closing padding row
			if strings.TrimSpace(lines[i]) != "" || lines[i-1] != "" || strings.TrimSpace(lines[i-2]) != "" {
				t.Errorf("two cards are not one blank apart:\n%s", strings.Join(lines[i-3:i+2], "\n"))
			}
			pads++
		}
	}
	if pads != 1 {
		t.Fatalf("the second card's header was not found:\n%s", strings.Join(lines, "\n"))
	}
	if l := cardLine(lines, "Two stories are in the backlog."); strings.Contains(m.renderHistory(), "48;5;234m    Two") || l == "" {
		t.Errorf("prose with no calls is on bare screen: %q", l)
	}

	// The cache's open line still joins the frozen cards to the live tail
	// exactly as a render with no cache would.
	m.appendEntry(entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"z.go"}`, toolResult: "z"})
	warm := m.renderHistory()
	m.invalidateRenderCache()
	if cold := m.renderHistory(); warm != cold {
		t.Errorf("the cached render of cards drifted from a cold one:\nwarm:\n%s\ncold:\n%s", stripANSI(warm), stripANSI(cold))
	}
}

// A running step is the same card: the spinner in the glyph slot, the
// running command's last line under the body, and the clock with it. The
// row under the transcript does not draw the command a second time.
func TestCard_RunningIsTheSameCard(t *testing.T) {
	m := cardModel(t)
	m.state = stateRunningCmd
	m.turnStarted = goldenNow.Add(-time.Minute)
	m.pendingApproval = &approvalRequest{kind: approvalExec}
	m.runningCommand = "go test ./..."
	m.runStart = clock().Add(-42 * time.Second)
	m.runTail = &commandTail{}
	m.runTail.Set("--- FAIL: TestReplyGolden (0.42s)")
	m.invalidateRenderCache()
	lines := cardLines(m)
	h := cardLine(lines, "wrote .plan/BACKLOG.md")
	if glyph := []rune(h)[2]; glyph == '✎' || glyph == ' ' {
		t.Errorf("the running card has the spinner in the glyph slot: %q", h)
	}
	if !strings.Contains(h, "43s") {
		t.Errorf("the running card's clock counts the command still running: %q", h)
	}
	if l := cardLine(lines, "--- FAIL: TestReplyGolden"); !strings.HasPrefix(l, strings.Repeat(" ", components.CardBodyIndent)+"---") {
		t.Errorf("the command's last line stands under the body: %q", l)
	}
	if tail := m.liveTail(80); tail != "" {
		t.Errorf("the command is drawn twice, on the card and under it:\n%s", stripANSI(tail))
	}

	m.state = stateInput
	m.pendingApproval, m.runTail = nil, nil
	m.invalidateRenderCache()
	if h := cardLine(cardLines(m), "wrote .plan/BACKLOG.md"); !strings.HasPrefix(h, " ▎✎ wrote") {
		t.Errorf("the finished card is the same card with its own glyph: %q", h)
	}
}

// A finished card does not fold on its own. It folds to its header only on
// the reader's enter or click, and opens again the same way.
func TestCard_FoldsOnlyWhenAsked(t *testing.T) {
	m := cardModel(t)
	if v := stripANSI(m.renderHistory()); !strings.Contains(v, "Writing the epic and two stories now.") ||
		!strings.Contains(v, "Listing .plan and reading") {
		t.Fatalf("a finished card folded on its own:\n%s", v)
	}
	if !m.toggleCardFold(8) {
		t.Fatal("the write's card is kept on its titling entry")
	}
	m.invalidateRenderCache()
	lines := cardLines(m)
	if strings.Contains(strings.Join(lines, "\n"), "Writing the epic and two stories now.") {
		t.Errorf("the folded card still draws its body:\n%s", strings.Join(lines, "\n"))
	}
	if h := cardLine(lines, "wrote .plan/BACKLOG.md"); !strings.HasPrefix(h, "▸▎✎ wrote") {
		t.Errorf("a folded card draws ▸ in the pointer column: %q", h)
	}
	m.toggleCardFold(8)
	m.invalidateRenderCache()
	if !strings.Contains(stripANSI(m.renderHistory()), "Writing the epic and two stories now.") {
		t.Error("the card did not open again")
	}

	// At low a card is its header alone, and the reader's open outranks it.
	m.verbosity = verbosityLow
	m.invalidateRenderCache()
	if strings.Contains(stripANSI(m.renderHistory()), "Writing the epic") {
		t.Error("at low a card is its header alone")
	}
	m.toggleCardFold(8)
	m.invalidateRenderCache()
	if !strings.Contains(stripANSI(m.renderHistory()), "Writing the epic") {
		t.Error("the reader's open outranks the rung")
	}
}

// Reading mode's unit is the card: the cursor steps from card to card, enter
// folds and unfolds the one it is on, and the outline still names the same
// steps.
func TestReading_TheCursorStopsOnACard(t *testing.T) {
	m := cardModel(t)
	m.viewport.SetLines(m.renderHistoryLines())
	updated, _ := m.Update(readingChord())
	m = updated.(Model)
	stops := m.expandableIndices()
	want := []int{1, 5, 8}
	var cards []int
	for _, i := range stops {
		if _, ok := m.stepBlockAt(m.transcript, i); ok {
			cards = append(cards, i)
		}
	}
	if fmt.Sprint(cards) != fmt.Sprint(want) {
		t.Fatalf("the cards the cursor stops on are %v, want %v (all stops %v)", cards, want, stops)
	}
	for _, i := range stops {
		if m.transcript[i].kind == entryTool || m.transcript[i].kind == entryCommand {
			t.Errorf("the cursor stops on a call inside a closed card: %d", i)
		}
	}
	if m.focusIdx != 8 {
		t.Fatalf("the mode opens on the last card, got %d", m.focusIdx)
	}
	if h := cardLine(cardLines(m), "wrote .plan/BACKLOG.md"); !strings.HasPrefix(h, "❯") {
		t.Errorf("the cursor stands on the card's header: %q", h)
	}
	// Enter walks the card's depths: open onto its calls, folded to its
	// header, and the card again.
	enter := func() {
		updated, _ = m.updateFocus(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = updated.(Model)
	}
	enter()
	if !slicesContain(m.expandableIndices(), 9) || m.focusIdx != 8 {
		t.Errorf("open, the card's call is a stop and the cursor stays on the card: %v at %d", m.expandableIndices(), m.focusIdx)
	}
	enter()
	if strings.Contains(stripANSI(m.renderHistory()), "Writing the epic") {
		t.Error("enter on the open card did not fold it")
	}
	enter()
	if !strings.Contains(stripANSI(m.renderHistory()), "Writing the epic") {
		t.Error("enter on the folded card did not unfold it")
	}
	m.moveFocus(-1)
	if m.focusIdx != 5 {
		t.Errorf("the cursor did not step to the card above, got %d", m.focusIdx)
	}
	var titles []string
	for _, blk := range m.blocksOf(m.transcript) {
		if blk.step != nil {
			titles = append(titles, fmt.Sprintf("%d %s", blk.step.ordinal, blk.step.title))
		}
	}
	if want := "[1 Listing .plan and reading the backlog's format before I write anything. " +
		"2 Confirming .plan/ is untracked, so nothing I write gets committed. " +
		"3 Writing the epic and two stories now.]"; fmt.Sprint(titles) != want {
		t.Errorf("the outline names %v, want the same three steps", titles)
	}
}

// No key is printed on a row for what enter does: not on a card, not on a
// closed think row. The hint bar carries it.
func TestCard_NoHintKeyOnARow(t *testing.T) {
	m := cardModel(t)
	m.transcript = append(m.transcript, entry{kind: entryThink, text: "weighing\nthe cap\nagainst the tests"})
	m.invalidateRenderCache()
	view := stripANSI(m.renderHistory())
	if strings.Contains(view, "[enter]") {
		t.Errorf("a row prints enter's key:\n%s", view)
	}
	m.viewport.SetLines(m.renderHistoryLines())
	updated, _ := m.Update(readingChord())
	m = updated.(Model)
	m.focusIdx = 8
	if bar := stripANSI(strings.Join(m.focusHintLines(), "\n")); !strings.Contains(bar, "[enter]") {
		t.Errorf("the hint bar does not say what enter does on the card:\n%s", bar)
	}
}

// A run nothing titled is kept on its first call, so open, the card and that
// call's row share an index. A click on the card's own header still folds the
// card rather than opening the call under it, and a click on the folded
// header gives the card back.
func TestCard_AClickOnAnOpenCardsHeaderFoldsIt(t *testing.T) {
	m := clickModel(t)
	x, y := rowCell(t, m, "searched x · ran go test")
	m = click(t, m, x, y)
	es := *m.entries()
	if es[1].stepFold != foldClosed {
		t.Fatalf("the click should fold the open card, its fold is %v", es[1].stepFold)
	}
	if es[1].expanded {
		t.Fatal("the click opened the first call's body instead of folding the card")
	}
	x, y = rowCell(t, m, "searched x · ran go test")
	m = click(t, m, x, y)
	if got := (*m.entries())[1].stepFold; got == foldClosed {
		t.Fatal("a second click should give the card back")
	}
}
