package chat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
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

// A running step is the same card: its own glyph, the running command's
// last line under the body, and the clock with it. The row under the
// transcript does not draw the command a second time.
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
	if !strings.HasPrefix(h, " ▎✎ wrote") {
		t.Errorf("the running card keeps its own glyph: %q", h)
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

// brailleFrames are the spinner's frames, which nothing but the input
// frame's status draws while a turn runs.
const brailleFrames = "⠋⠙⠹⠸⠼⠴⠦⠧"

// A running step's card draws its kind's own glyph, held still: the frame's
// status already says the turn is working, so a spinner in the glyph slot
// would be a second animation telling it again. The duration still ticks and
// the running command's tail still stands under the body
// (docs/interface/surfaces.md#the-step).
func TestCard_ARunningCardHasNoSpinner(t *testing.T) {
	holdClock(t)
	m := activityModel(t)
	m.transcript = []entry{
		{kind: entryUser, text: "run the ui tests"},
		{kind: entryAssistant, text: "Running the UI tests once more."},
		{kind: entryCommand, text: "go test ./internal/ui/", toolResult: "ok", duration: 4 * time.Second},
	}
	m.state = stateRunningCmd
	m.turnStarted = goldenNow.Add(-time.Minute)
	m.pendingApproval = &approvalRequest{kind: approvalExec}
	m.runningCommand = "go test ./internal/ui/"
	m.runStart = goldenNow.Add(-8 * time.Second)
	m.runTail = &commandTail{}
	m.runTail.Set("ok  github.com/rfizzle/shhh/internal/ui  0.412s")
	for frame := range 3 {
		m.spinFrame = frame
		m.invalidateRenderCache()
		lines := cardLines(m)
		// The receipt, then the clock flush right: the first command's `ok`
		// is not the answer of a step with a command still running.
		h := strings.TrimRight(cardLine(lines, "ran go test"), " ")
		if !strings.HasPrefix(h, " ▎$ ran go test ./internal/ui/ ") || !strings.HasSuffix(h, " 12s") ||
			strings.Join(strings.Fields(h), " ") != "▎$ ran go test ./internal/ui/ 12s" {
			t.Errorf("frame %d: the running card's header is %q, want the $ glyph, the receipt and 12s", frame, h)
		}
		if l := cardLine(lines, "ok  github.com"); !strings.HasPrefix(l, strings.Repeat(" ", components.CardBodyIndent)+"ok") {
			t.Errorf("frame %d: the command's tail stands under the body: %q", frame, l)
		}
		if card := strings.Join(lines, "\n"); strings.ContainsAny(card, brailleFrames) {
			t.Errorf("frame %d: the running card draws a spinner frame:\n%s", frame, card)
		}
	}

	// Every kind keeps its own mark while its step runs.
	for _, tc := range []struct {
		kind components.ActivityKind
		want string
	}{
		{components.ActivityTool, "⚙"},
		{components.ActivityCommand, "$"},
		{components.ActivityEdit, "✎"},
		{components.ActivitySubagent, "◇"},
	} {
		c := components.StepCard{Kind: tc.kind, State: components.ActivityRunning, Verb: "ran", Subject: "x", Duration: "3s"}
		view := stripANSI(c.View(80))
		if h := strings.Split(view, "\n")[1]; !strings.HasPrefix(strings.TrimLeft(h, " "), tc.want+" ran") {
			t.Errorf("a running %s card's header should lead with its own glyph: %q", tc.want, h)
		}
		if strings.ContainsAny(view, brailleFrames) {
			t.Errorf("a running %s card draws a spinner frame:\n%s", tc.want, view)
		}
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

// The ladder for a card: low draws it as its header alone and leaves
// thinking out, normal draws header, body and footer with thinking as prose,
// high opens every card onto its groups; and a reader's own fold or open
// outranks the rung (docs/interface/principles.md#density-is-one-ladder).
func TestDensity_ACardAtEachRung(t *testing.T) {
	const (
		header  = "ran git status --short .plan/"
		body    = "Confirming .plan/ is untracked"
		footer  = "?? .plan/"
		strip   = "in order "
		thought = "weighing the cap against the tests"
		// The write's card, kept on the sentence that titled it.
		write, writeBody = 8, "Writing the epic and two stories now."
		// The reads' card, the one the reader opens.
		reads, readsGroup = 1, "▾ read 2 files"
	)
	cases := []struct {
		name  string
		rung  verbosity
		folds map[int]foldState
		has   []string
		lacks []string
		// folded is the header that has to start with ▸, where one does.
		folded string
	}{
		{name: "low · each card its header alone, thinking left out", rung: verbosityLow,
			has: []string{header}, lacks: []string{body, footer, strip, thought, writeBody}},
		{name: "normal · header, body and footer, thinking as prose", rung: verbosityNormal,
			has: []string{header, body, footer, thought, writeBody}, lacks: []string{strip, readsGroup}},
		{name: "high · every card open with its groups", rung: verbosityHigh,
			has: []string{header, body, strip, readsGroup, thought, writeBody}},
		{name: "low · a card the reader folded draws ▸", rung: verbosityLow,
			folds: map[int]foldState{write: foldClosed}, has: []string{header},
			lacks: []string{writeBody}, folded: "wrote .plan/BACKLOG.md"},
		{name: "high · the reader's fold outranks the rung", rung: verbosityHigh,
			folds: map[int]foldState{write: foldClosed}, has: []string{header, strip},
			lacks: []string{writeBody}, folded: "wrote .plan/BACKLOG.md"},
		{name: "low · a card the reader gave back draws whole", rung: verbosityLow,
			folds: map[int]foldState{write: foldCard}, has: []string{writeBody}, lacks: []string{body}},
		{name: "low · a card the reader opened draws its groups", rung: verbosityLow,
			folds: map[int]foldState{reads: foldOpen}, has: []string{readsGroup, strip}, lacks: []string{body}},
		{name: "normal · a card the reader opened draws its groups", rung: verbosityNormal,
			folds: map[int]foldState{reads: foldOpen}, has: []string{readsGroup, footer}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := cardModel(t)
			m.transcript = append(m.transcript, entry{kind: entryThink, text: thought})
			for idx, f := range tc.folds {
				m.transcript[idx].stepFold = f
			}
			m.verbosity = tc.rung
			m.invalidateRenderCache()
			lines := cardLines(m)
			view := strings.Join(lines, "\n")
			for _, s := range tc.has {
				if !strings.Contains(view, s) {
					t.Errorf("no %q:\n%s", s, view)
				}
			}
			for _, s := range tc.lacks {
				if strings.Contains(view, s) {
					t.Errorf("%q is drawn:\n%s", s, view)
				}
			}
			if tc.folded != "" {
				if h := cardLine(lines, tc.folded); !strings.HasPrefix(h, "▸") {
					t.Errorf("the folded card does not draw ▸ in the pointer column: %q", h)
				}
			}
			if tc.rung == verbosityLow && tc.folds == nil {
				// One blank between two headers: at low a card has no
				// padding rows, so it is one line on the band.
				h := cardLine(lines, header)
				for i, l := range lines {
					if l == h && (i < 2 || lines[i-1] != "" || lines[i-2] == "") {
						t.Errorf("a low card stands on more than one blank:\n%s", view)
					}
				}
			}
		})
	}
}

// The pointer on a card: its header is the control, the rest of it is text.
// A click on the header folds the card and a second unfolds it; a click on
// the sentence or the evidence does nothing; a click on a call's row inside
// an open card opens that call as the row's click does anywhere; and a drag
// across the card still selects it (docs/interface/surfaces.md#the-step).
func TestCard_TheHeaderIsThePointersControl(t *testing.T) {
	const (
		card   = 5 // the command's card, kept on its titling sentence
		call   = 7 // the command inside it
		header = "ran git status --short .plan/"
	)
	pointed := func(t *testing.T, c *clip, open bool) Model {
		t.Helper()
		m := selectModel(t, c, cardModel(t).transcript...)
		if open {
			(*m.entries())[card].stepFold = foldOpen
			m.invalidateRenderCache()
			m.viewport.SetLines(m.renderHistoryLines())
		}
		return m
	}
	cases := []struct {
		name     string
		open     bool
		on       string
		fold     foldState
		expanded bool
	}{
		{name: "the header folds the card", on: header, fold: foldClosed},
		{name: "the sentence does nothing", on: "Confirming .plan/ is untracked", fold: foldAuto},
		{name: "the evidence does nothing", on: "?? .plan/", fold: foldAuto},
		{name: "an open card's header folds it", open: true, on: header, fold: foldClosed},
		{name: "an open card's sentence does nothing", open: true, on: "Confirming .plan/ is untracked", fold: foldOpen},
		{name: "a call's row in an open card opens the call", open: true, on: "$ git status --short .plan/",
			fold: foldOpen, expanded: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := pointed(t, &clip{}, tc.open)
			x, y := rowCell(t, m, tc.on)
			m = click(t, m, x, y)
			es := *m.entries()
			if es[card].stepFold != tc.fold {
				t.Errorf("the card's fold is %v, want %v", es[card].stepFold, tc.fold)
			}
			if es[call].expanded != tc.expanded {
				t.Errorf("the call's row open = %v, want %v", es[call].expanded, tc.expanded)
			}
		})
	}

	t.Run("a second click on the header gives the card back", func(t *testing.T) {
		m := pointed(t, &clip{}, false)
		x, y := rowCell(t, m, header)
		m = click(t, m, x, y)
		x, y = rowCell(t, m, header)
		m = click(t, m, x, y)
		if got := (*m.entries())[card].stepFold; got == foldClosed {
			t.Fatal("a second click should give the card back")
		}
	})

	t.Run("an open card's closing padding does nothing", func(t *testing.T) {
		// The write's card has one call, so its closing padding rides that
		// call's row, opened here onto a body long enough to want the
		// screen.
		const write, call = 8, 9
		m := pointed(t, &clip{}, false)
		es := *m.entries()
		es[write].stepFold = foldOpen
		es[call].toolResult = strings.Repeat("a line of output\n", 60)
		es[call].expanded = true
		m.invalidateRenderCache()
		m.viewport.SetLines(m.renderHistoryLines())
		pad := -1
		for line := 0; line < len(contentLines(m)); line++ {
			if u, off, ok := m.unitAt(line); ok && u.closesCard && off == strings.Count(u.text, "\n")-1 {
				pad = line
			}
		}
		if pad < 0 {
			t.Fatal("no open card's closing padding on the screen")
		}
		m.viewport.SetYOffset(max(pad-5, 0))
		x, y := at(t, m, pad, 4)
		state := m.state
		m = click(t, m, x, y)
		if m.state != state || (*m.entries())[write].stepFold != foldOpen {
			t.Errorf("a click on the closing padding acted: state %v → %v, fold %v", state, m.state, (*m.entries())[write].stepFold)
		}
	})

	t.Run("a drag across the card selects it", func(t *testing.T) {
		c := &clip{}
		m := pointed(t, c, false)
		m = dragLines(t, m, lineOf(t, m, header), lineOf(t, m, "?? .plan/"))
		if !strings.Contains(c.text, "Confirming .plan/ is untracked") {
			t.Errorf("the drag copied %q, not the card's lines", c.text)
		}
		if got := (*m.entries())[card].stepFold; got != foldAuto {
			t.Errorf("the drag folded the card: %v", got)
		}
	})
}

// The band and the screen under it reach the terminal as the catalogue's
// pair: #1c1c1c on #0f1117 where the terminal shows truecolour, 234 on 233
// where it shows 256, and never the 235 a band once stepped to. It is read
// off the frame the terminal is sent, through the profile the palette
// resolves against, rather than off the tokens: a token that is right and a
// screen that draws something else is the failure this holds.
func TestCard_TheBandAndTheGroundAreTheCataloguesPair(t *testing.T) {
	cases := []struct {
		profile             colorprofile.Profile
		band, ground, never string
	}{
		{colorprofile.TrueColor, "48;2;28;28;28", "48;2;15;17;23", "48;2;38;38;38"},
		{colorprofile.ANSI256, "48;5;234", "48;5;233", "48;5;235"},
	}
	for _, c := range cases {
		themeRestore(t)
		components.SetMono(false)
		was := components.Profile()
		components.SetProfile(c.profile)
		m := cardModel(t)
		m.syncViewport()
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		screen := m.View().Content
		components.SetProfile(was)

		lines := strings.Split(screen, "\n")
		header, blank := -1, -1
		for i, l := range lines {
			plain := stripANSI(l)
			if header < 0 && strings.Contains(plain, "wrote .plan/BACKLOG.md") {
				header = i
			}
			if blank < 0 && i > 2 && strings.TrimSpace(plain) == "" {
				blank = i
			}
		}
		if header < 0 || blank < 0 {
			t.Fatalf("%v: no write card and no blank row on the screen:\n%s", c.profile, stripANSI(screen))
		}
		for _, row := range []int{header - 1, header} {
			if !strings.Contains(lines[row], c.band) {
				t.Errorf("%v: card row %d is not on the band %s: %q", c.profile, row, c.band, lines[row])
			}
		}
		if !strings.Contains(lines[blank], c.ground) || strings.Contains(lines[blank], c.band) {
			t.Errorf("%v: the screen under the transcript is not the ground %s: %q", c.profile, c.ground, lines[blank])
		}
		if strings.Contains(screen, c.never) {
			t.Errorf("%v: the band stepped to %s", c.profile, c.never)
		}
	}
}
