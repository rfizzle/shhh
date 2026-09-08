package chat

// Golden-file render tests for the host surfaces: the step outline
// the transcript folds a turn into, and the prompt frame in each of its four
// layout modes. The component catalog's own captures live beside it in
// internal/ui/components.
//
// Regenerate after an intended change:
//
//	go test ./internal/ui/components ./internal/ui/chat -update-golden

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/persona"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/golden"
	"github.com/rfizzle/shhh/internal/ui/keys"
	"github.com/rfizzle/shhh/internal/web"
)

func TestMain(m *testing.M) { os.Exit(golden.Run(m)) }

// goldenWidths are the terminal widths behind the breakpoints of
// guidelines/layout-breakpoints in the shhh Design System project. They are
// terminal columns, not content columns: the surface loses horizontalPadding
// on each side before any of these thresholds are read.
var goldenWidths = []int{60, 80, 110, 130}

// captureGolden renders one surface at every width in both palettes. The
// colour profile is forced because a test binary's stdout is not a terminal,
// so lipgloss would otherwise emit no escapes at all and the ansi block would
// be a copy of the layout block.
func captureGolden(t *testing.T, name, surface string, widths []int, panels func(width int) []golden.Panel) {
	t.Helper()
	captureCases(t, name, surface, widths, func(width int) ([]golden.Panel, *golden.Cursor) {
		return panels(width), nil
	})
}

// captureBoundedGolden is captureGolden for a surface that promises to fit
// its pane: every rendered line is measured against the width before it is
// captured, so a row that ran past the right edge fails the test rather than
// being written into the file it is checked against.
func captureBoundedGolden(t *testing.T, name, surface string, widths []int, panels func(width int) []golden.Panel) {
	t.Helper()
	captureGolden(t, name, surface, widths, func(width int) []golden.Panel {
		ps := panels(width)
		golden.Within(t, surface, width, ps)
		return ps
	})
}

// captureCursorGolden is captureGolden for a surface that owns the terminal's
// cursor: the coordinate goes in the header, where it is the only record of a
// cursor the render itself shows nothing for. It takes one panel because the
// header records one cursor — two states in one file would leave it speaking
// for whichever of them the fixture built.
func captureCursorGolden(t *testing.T, name, surface string, widths []int, panel func(width int) (golden.Panel, *golden.Cursor)) {
	t.Helper()
	captureCases(t, name, surface, widths, func(width int) ([]golden.Panel, *golden.Cursor) {
		p, cur := panel(width)
		return []golden.Panel{p}, cur
	})
}

func captureCases(t *testing.T, name, surface string, widths []int, build func(width int) ([]golden.Panel, *golden.Cursor)) {
	t.Helper()
	was := components.Profile()
	components.SetProfile(colorprofile.ANSI256)
	t.Cleanup(func() { components.SetProfile(was) })

	for _, mono := range []bool{false, true} {
		label := "color"
		if mono {
			label = "mono"
		}
		t.Run(label, func(t *testing.T) {
			monoRestore(t)
			components.SetMono(mono)
			for _, width := range widths {
				panels, cur := build(width)
				golden.Assert(t, name+".w"+strconv.Itoa(width), golden.Case{
					Surface: surface,
					Width:   width,
					Mono:    mono,
					Cursor:  cur,
					Panels:  panels,
				})
			}
		})
	}
}

// goldenTranscript is a two-step turn: a batch that read and searched, then a
// batch that edited and broke a test, and the rows it closes with. It is the
// steps fixture with a folded read-only run added, so the outline, the group
// row, a failing step and the turn close all appear in one capture.
func goldenTranscript() []entry {
	return []entry{
		{kind: entryUser, text: "fix the round limit"},
		{kind: entryAssistant, text: "Locate the round accounting"},
		{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"internal/agent/loop.go"}`,
			toolResult: "a\nb\nc", duration: 400 * time.Millisecond},
		{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"internal/agent/tools.go"}`,
			toolResult: "a\nb", duration: 300 * time.Millisecond},
		{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"internal/agent/context.go"}`,
			toolResult: "a", duration: 200 * time.Millisecond},
		{kind: entryTool, toolName: "search", toolArgs: `{"pattern":"ErrRoundLimit"}`,
			toolResult: searchHits, duration: 300 * time.Millisecond},
		{kind: entryAssistant, text: "Thread the sentinel through the loop"},
		{kind: entryTool, toolName: "edit_file", toolArgs: `{"path":"internal/agent/loop.go"}`,
			toolResult: "edited", duration: 1100 * time.Millisecond},
		{kind: entryCommand, text: "go test ./internal/agent/...",
			toolResult: "--- FAIL: TestRoundLimit", exitCode: 1, duration: 21400 * time.Millisecond},
		{kind: entryTurnClose, close: &components.TurnClose{
			Steps: 2, Tools: 6, Elapsed: "24.7s", Spend: "$0.14", Note: "round 2/25",
			Changes: &components.TurnChanges{
				Files: 1, Added: 12, Removed: 4,
				Keys: []components.TurnKey{{Key: "[v]", Label: "review"}, {Key: "[u]", Label: "undo turn"}},
				Note: "all tracked in git",
			},
			Checks: &components.TurnChecks{
				Failed: true, Label: "go test ./internal/agent/...", Counts: "exit 1 · 21s",
			},
		}},
	}
}

// goldenModel is a ready model at one width with usage, pricing and a model
// name, so every vitals segment has something to show and nothing in the
// render depends on the clock.
func goldenModel(t *testing.T, width int) Model {
	t.Helper()
	m := frameModel(t, width, 40)
	m.transcript = goldenTranscript()
	m.invalidateRenderCache()
	return m
}

// TestGolden_StepOutline captures the transcript's step grammar (
// at each breakpoint: the numbered headers with their state glyph and
// stats, the folded read-only group row, and the step that stays open because
// it contains a failure.
func TestGolden_StepOutline(t *testing.T) {
	captureGolden(t, "step-outline", "transcript step outline", goldenWidths, func(width int) []golden.Panel {
		m := goldenModel(t, width)
		normal := m.renderHistory()
		// Step 1 finished, so it collapsed to its header; opening it is what
		// puts the counted group row on the sheet.
		m.toggleStepFold(1)
		m.invalidateRenderCache()
		opened := m.renderHistory()
		m.toggleStepFold(1)
		// Ctrl+O on step 1: it unfolds, its rows give the counted group back,
		// and every one of them carries its bounded body — one step deep,
		// with step 2 beside it untouched.
		blk, ok := m.stepBlockAt(m.transcript, 1)
		if !ok {
			t.Fatal("step 1 not found in the golden transcript")
		}
		m.toggleStepDetail(blk.step)
		m.invalidateRenderCache()
		detail := m.renderHistory()
		m.toggleStepDetail(blk.step)
		m.toggleStepFold(1)
		m.verbosity = verbosityHigh
		m.invalidateRenderCache()
		high := m.renderHistory()
		m.verbosity = verbosityLow
		m.invalidateRenderCache()
		low := m.renderHistory()
		return []golden.Panel{
			{Label: "verbosity · normal (a finished step collapses)", View: normal},
			{Label: "verbosity · normal, step 1 opened (read-only run folds to a group row)", View: opened},
			{Label: "/step · step 1's detail, one step deep", View: detail},
			{Label: "verbosity · high (every row, with detail)", View: high},
			{Label: "verbosity · low (step headers only)", View: low},
		}
	})
}

// TestGolden_PlanChecklist captures the outline an approved plan numbers
// : declared steps carrying the plan's own numbers and titles in the
// order the run reached them, one group the plan never named marked off it,
// and the declared-but-not-started steps trailing as queued headers. It is
// the one shape of the outline that does not come from the prose.
func TestGolden_PlanChecklist(t *testing.T) {
	captureGolden(t, "plan-checklist", "plan checklist outline", goldenWidths, func(width int) []golden.Panel {
		build := func(st state) string {
			m := frameModel(t, width, 40)
			m.transcript = []entry{{kind: entryUser, text: planApprovedMessage}}
			m.planRun = newPlanRun(plan.Parse(planFixture), 0)
			for _, a := range []struct {
				title  string
				d      time.Duration
				failed bool
			}{
				{"Now let me locate the round accounting", 6200 * time.Millisecond, false},
				{"Return it from runRound", 38100 * time.Millisecond, true},
				{"Rebuild the changeset store", 3900 * time.Millisecond, false},
			} {
				announce(t, &m, a.title, a.d, a.failed)
			}
			m.state = st
			m.invalidateRenderCache()
			return m.renderHistory()
		}
		return []golden.Panel{
			{Label: "mid-run (the last step is still working)", View: build(stateStreaming)},
			{Label: "turn over (every step settled)", View: build(stateInput)},
		}
	})
}

// frameWidths are the widths the prompt frame is captured at: the standing
// four, plus the rungs they do not land on — 70, the terminal the vitals fold
// into the bottom border at; 12, the narrowest terminal that still frames the
// draft; and 8, under the rung, where there is no box and the bare prompt
// stands in for it (guidelines/layout-breakpoints). 14 is kept because it is
// inside the band the rung moved across, and is the fixture that says what a
// terminal there draws now.
var frameWidths = append([]int{8, 12, 14, 70}, goldenWidths...)

// TestGolden_PromptFrame captures the command-center surface in
// each of its four layout modes, at every rung of
// guidelines/layout-breakpoints and on both sides of the narrowest.
func TestGolden_PromptFrame(t *testing.T) {
	captureGolden(t, "prompt-frame", "prompt frame", frameWidths, func(width int) []golden.Panel {
		idle := goldenModel(t, width)
		working := goldenModel(t, width)
		working.state = stateStreaming
		// The same turn with turns behind it. What the session has spent is
		// no longer what this turn has spent, so the top rail says the
		// turn's and the rail below says the session's; on the panel above,
		// where the two are one figure, the top rail says neither
		// (docs/interface/surfaces.md#the-input-frame).
		behind := goldenModel(t, width)
		behind.state = stateStreaming
		behind.TotalTokensIn += 120_000
		behind.TotalTokensOut += 30_000
		return []golden.Panel{
			{Label: "state · idle", View: promptSurface(idle)},
			{Label: "state · working, the first turn of the session", View: promptSurface(working)},
			{Label: "state · working, with turns behind it", View: promptSurface(behind)},
		}
	})
}

// TestGolden_QuestionWaiting captures the frame with a question handed to the
// draft (question.go): the notice rail's count beside a steering count, the
// gutter saying the draft is answering rather than steering, and the bottom
// rail's two ways back to the card. The two counts are pinned side by side
// because at 60 columns they compete for the one rail, and because they are
// three different promises the reader has to be able to tell apart.
func TestGolden_QuestionWaiting(t *testing.T) {
	captureGolden(t, "question-waiting", "a question waiting behind the draft", goldenWidths, func(width int) []golden.Panel {
		build := func(steering int) string {
			m := frameModel(t, width, 40).WithAsk()
			m.state = stateStreaming
			updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{{
				ID: "call_q", Name: ask.ToolName, Arguments: `{"question":"Which store should the cache use?","shape":"choose","options":[
					{"label":"SQLite","detail":"in the checkout already","recommended":true},
					{"label":"Postgres","detail":"one more service to run"}]}`,
			}}})
			next := updated.(Model)
			esc, _ := next.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			next = esc.(Model)
			for i := 0; i < steering; i++ {
				next.steering = append(next.steering, steeringItem{text: "and keep the migration reversible"})
			}
			next.input.SetValue("the one that needs no new service")
			next.syncInputHeight()
			return promptSurface(next)
		}
		return []golden.Panel{
			{Label: "esc \u00b7 the rail counts the question and the draft answers it", View: build(0)},
			{Label: "beside a steer \u00b7 three promises, told apart", View: build(1)},
		}
	})
}

// TestGolden_GrownDraft captures the box grown around a multi-line draft
// (frame.go, syncInputHeight): one row per line up to the cap, with the
// transcript paying for the rows above it.
func TestGolden_GrownDraft(t *testing.T) {
	captureCursorGolden(t, "grown-draft", "the grown draft box", []int{80}, func(width int) (golden.Panel, *golden.Cursor) {
		m := goldenModel(t, width)
		m.input.SetValue("first the failing test\nthen the fix in loop.go\nthen the fixture\nthen make ci\nthen stop")
		m.syncInputHeight()
		view, cur := promptCapture(m)
		return golden.Panel{Label: "draft \u00b7 five lines", View: view}, cur
	})
}

// TestGolden_PressAgain captures the two-press windows and the quit question
// (cancel.go): the armed hint at every width — the wide layout says it on the
// bottom rail, the narrower ones on the notice rail, because the invariant
// that the surface says what a key will do cannot depend on the terminal
// being wide — and the inline confirm quitting over a live turn opens.
func TestGolden_PressAgain(t *testing.T) {
	captureGolden(t, "press-again", "the two-press windows", goldenWidths, func(width int) []golden.Panel {
		armed := func(kind armKind, key string, st state) string {
			m := goldenModel(t, width)
			m.state = st
			m.armed = armedPress{kind: kind, key: key, deadline: time.Now().Add(time.Hour), seq: 1}
			return promptSurface(m)
		}
		confirm := func() string {
			m := goldenModel(t, width)
			m.state = stateStreaming
			mm, _ := m.openQuitConfirm()
			m = mm.(Model)
			return m.takeoverPanel(m.contentWidth())
		}
		return []golden.Panel{
			// The key is read from the register rather than written down:
			// only the cancel chord can arm the interrupt, and a literal
			// here would go on printing whatever it was written as.
			{Label: "cancel armed · a second press abandons the turn", View: armed(armCancel, keys.Shown(keys.Draft.Cancel), stateStreaming)},
			{Label: "quit armed · idle", View: armed(armQuit, keys.Shown(keys.Draft.Quit), stateInput)},
			{Label: "quit confirm · over a live turn", View: confirm()},
		}
	})
}

// TestGolden_HistorySearch captures the reverse search stating itself under
// the draft (historysearch.go): the match in the box with the query and its
// count on the row below, the no-match reading, and the notice rail carrying
// the one-time keys-changed row a rebinding release ships with.
func TestGolden_HistorySearch(t *testing.T) {
	captureGolden(t, "history-search", "the input history search", goldenWidths, func(width int) []golden.Panel {
		search := func(query string) string {
			m := goldenModel(t, width)
			m.inputHistory = []string{"go test ./internal/agent/...", "go build ./..."}
			m.historyIdx = len(m.inputHistory)
			m.histSearch = &historySearch{query: query}
			m.placeHistoryMatch()
			return promptSurface(m)
		}
		notice := func() string {
			m := goldenModel(t, width).WithKeysNotice(KeysChangedNotice())
			return promptSurface(m)
		}
		return []golden.Panel{
			{Label: "search · a match in the box, the query on the row", View: search("bui")},
			{Label: "search · no match", View: search("zz")},
			{Label: "the keys-changed notice on the rail", View: notice()},
		}
	})
}

// TestGolden_DraftGrammar captures what the draft means before it is sent
// (bang.go, mention.go, followup.go): the gutter swapped for a bang draft,
// the file-mention menu under the box, and the notice rail counting the
// follow-up queue apart from steering — held after a cancel.
func TestGolden_DraftGrammar(t *testing.T) {
	captureGolden(t, "draft-grammar", "the draft grammar", []int{80}, func(width int) []golden.Panel {
		bang := func() string {
			m := goldenModel(t, width)
			m.input.SetValue("!go test ./internal/agent/...")
			return promptSurface(m)
		}
		mention := func() string {
			m := goldenModel(t, width)
			m.recentFiles = func() []project.RecentFile {
				return []project.RecentFile{
					{Path: "go.mod", Mod: time.Now().Add(-time.Minute)},
					{Path: "internal/ui/chat/model.go", Mod: time.Now().Add(-9 * time.Minute)},
					{Path: "internal/ui/chat/modelutil.go", Mod: time.Now().Add(-26 * time.Minute)},
				}
			}
			m.input.SetValue("@mod")
			m.syncCompletions()
			return promptSurface(m)
		}
		queues := func(held bool) string {
			m := goldenModel(t, width)
			m.state = stateStreaming
			m.steering = []steeringItem{{text: "and check the parser"}}
			m.followUps = []string{"then update the docs"}
			m.followUpsHeld = held
			return promptSurface(m)
		}
		return []golden.Panel{
			{Label: "a bang draft · the gutter says it is a command", View: bang()},
			{Label: "the @ mention menu under the draft", View: mention()},
			{Label: "both queues counted apart", View: queues(false)},
			{Label: "the follow-up held after a cancel", View: queues(true)},
		}
	})
}

// TestGolden_HelpKeys pins the key section as `?` prints it — one width,
// because the row wraps like any system row and the words are what is under
// test: a rebind that reaches the dispatch without reaching this sheet is
// the drift the register exists to stop.
func TestGolden_HelpKeys(t *testing.T) {
	captureGolden(t, "help-keys", "the /help key section as a system row", []int{80}, func(width int) []golden.Panel {
		m := frameModel(t, width, 40)
		mm, _ := m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
		m = mm.(Model)
		return []golden.Panel{
			{Label: "? on an empty draft", View: m.renderHistory()},
		}
	})
}

// TestGolden_HelpChat pins the command list a conversation is given, beside
// the one a coding session is. The two are one rendering of one registry now,
// and the sheet is where a reader sees that the difference between them is
// what the session has wired and nothing else — a row for every command that
// can be typed here, and none for one that would answer that it is not part
// of this session.
//
// One width, because the rows wrap like any system row and the words are what
// is under test.
func TestGolden_HelpChat(t *testing.T) {
	captureGolden(t, "help-chat", "the /help command list in each session", []int{80}, func(width int) []golden.Panel {
		root := t.TempDir()
		if err := os.MkdirAll(todo.Dir(root), 0o755); err != nil {
			t.Fatal(err)
		}
		backlog := Todos{Profile: todo.BuiltinCode(), Root: root,
			Manage: func([]string) string { return "" },
			Detail: func(*todo.Store, todo.Item) string { return "" }}
		build := func(m Model) string {
			m = m.WithTodos(backlog).WithNotebook(notebook.New(nil))
			return strings.Join(strings.Split(helpText(&m), "\n\nKeys:")[:1], "")
		}
		return []golden.Panel{
			{Label: "a conversation", View: build(frameModel(t, width, 40).WithConversation())},
			{Label: "a coding session", View: build(frameModel(t, width, 40))},
		}
	})
}

// TestGolden_StagedRail captures the frame with something waiting to ride
// : the chips between the notices and the box they will leave
// with, at every width, so the rail's own ladder and its place in the stack
// are on one sheet.
//
// A staged paste is on the sheet because it is the chip that has to carry the
// most on its own: it has no name anybody chose and no file behind it, so the
// height beside the size is what tells the reader which log they are about to
// send.
//
// The last panel is the pair that matters — a notice above the chips —
// because "the staged rail sits under anything transient the session is
// saying" is a claim a reader checks by looking at both rows at once.
func TestGolden_StagedRail(t *testing.T) {
	png := make([]byte, 412<<10)
	pdf := make([]byte, 1126<<10)
	md := bytes.Repeat([]byte("a note about the parser\n"), 84)
	paste := bytes.Repeat([]byte("goroutine 1 [running]:\n"), 178)
	captureGolden(t, "staged-rail", "the frame's staged rail", goldenWidths, func(width int) []golden.Panel {
		frame := func(mut func(*Model)) string {
			m := goldenModel(t, width)
			m.attachments = []provider.Attachment{
				{Kind: provider.AttachmentImage, Name: "shot.png", Data: png},
			}
			mut(&m)
			return promptSurface(m)
		}
		return []golden.Panel{
			{Label: "one screenshot waiting", View: frame(func(m *Model) {})},
			{Label: "one of each kind · only the text has lines to count", View: frame(func(m *Model) {
				m.attachments = append(m.attachments,
					provider.Attachment{Kind: provider.AttachmentText, Name: "notes.md", Data: md},
					provider.Attachment{Kind: provider.AttachmentDocument, Name: "spec.pdf", Data: pdf})
			})},
			{Label: "a staged paste · the height is what names it", View: frame(func(m *Model) {
				m.attachments = []provider.Attachment{
					{Kind: provider.AttachmentText, Name: "paste-1.txt", Data: paste},
				}
			})},
			{Label: "a notice above it · transient first, then what rides", View: frame(func(m *Model) {
				m.steering = []steeringItem{{text: "and check the parser"}}
			})},
		}
	})
}

// TestGolden_TurnStatus captures the frame's activity slot while a turn runs
// : the phases in place on the top rail, and the summary the line
// resolves into when the turn ends. The slot is whatever the identity leaves
// of the rail, so the narrow captures are where the turn-status drop order
// shows.
func TestGolden_TurnStatus(t *testing.T) {
	captureGolden(t, "turn-status", "running turn status", goldenWidths, func(width int) []golden.Panel {
		frame := func(mut func(*Model)) string {
			m := goldenModel(t, width)
			m.turnCount = 1
			// A start stamp far from any rounding boundary, so the ticking
			// field is captured without the capture depending on the clock.
			m.turnStarted = time.Now().Add(-64500 * time.Millisecond)
			m.state = stateStreaming
			mut(&m)
			return promptSurface(m)
		}
		return []golden.Panel{
			{Label: "phase · thinking", View: frame(func(m *Model) {})},
			{Label: "phase · running, named", View: frame(func(m *Model) {
				m.state = stateRunningCmd
				m.runningCommand = "go test ./internal/agent/..."
			})},
			{Label: "phase · streaming", View: frame(func(m *Model) {
				m.streaming = "The round limit is enforced in the loop, not in the tool."
			})},
			// The sweep in situ, mid-pass. The entrance is not
			// capturable here — it is read off the turn's own age, and this
			// fixture's turn is a minute old so its elapsed does not depend
			// on the clock — so the components catalog captures that half.
			{Label: "phase · thinking, mid-sweep", View: frame(func(m *Model) {
				m.spinFrame = 8
			})},
			// One frame of a round's report arriving. The top rail's account
			// and the vitals rail's total are on the same step of the same
			// climb, because the round moved both targets by the same amount
			// on the same frame (vitals.go), and both print every digit
			// while the turn is still spending them.
			{Label: "counts · mid-climb", View: frame(func(m *Model) {
				m.easeCounts()
				// The round's report, folded in by hand so the capture is of
				// the climb and not of the context meter moving beside it.
				m.vitals.current.In += 8000
				m.vitals.current.Out += 2000
				m.TotalTokensIn += 8000
				m.TotalTokensOut += 2000
				m.spinFrame++
				m.easeCounts()
			})},
			{Label: "resolved · the summary it becomes", View: frame(func(m *Model) {
				m.state = stateInput
				m.transcript = append(m.transcript, entry{kind: entryTurnClose, turn: 1,
					close: &components.TurnClose{State: components.TurnDone,
						Tools: 18, Elapsed: "1m 04s", Spend: "$0.14"}})
			})},
			// The two halves of a hold. Neither is a phase — the first is
			// still in one and the second is in none — so both take the slot
			// whole, in the chip a waiting decision wears (hold.go).
			{Label: "held · asked, the round still running", View: frame(func(m *Model) {
				m.holdAsked = true
			})},
			{Label: "held · parked at the boundary", View: frame(func(m *Model) {
				m.state = stateInput
				m.turnOpen = true
				m.hold = &turnHold{turn: 1, rounds: 12}
			})},
		}
	})
}

// Every surface the golden suite asked for now has a capture. Review mode and
// the fan-out block are captured beside the
// component catalog (review-mode.*, fanout-block.*), which is why the
// placeholder that used to stand here for them is gone.

// promptSurface is the bottom panel the product shows: the frame where it
// fits, and the plain input row where it does not (Model.View's
// frameShowing branch).
func promptSurface(m Model) string {
	if m.frameShowing() {
		return m.renderPromptFrame()
	}
	// The frameless layout is the draft panel rather than the field alone:
	// what stands in for the box is the prompt glyph in front of it
	// (paint.go), and a capture of the field on its own would not show it.
	return m.draftPanel()
}

// promptCapture is promptSurface with the cursor the surface placed inside
// it, in the render's own cells.
func promptCapture(m Model) (string, *golden.Cursor) {
	if !m.frameShowing() {
		// The bare input is its own render, and the only thing in front of
		// it is the prompt glyph the frameless layout draws.
		cur := m.input.Cursor()
		if cur != nil {
			at := *cur
			at.X += lipgloss.Width(m.plainPrompt())
			cur = &at
		}
		return m.draftPanel(), goldenCursor(cur)
	}
	var cur cursorSink
	view := m.renderPromptFrameWith(&cur)
	return view, goldenCursor(cur.at)
}

func goldenCursor(cur *tea.Cursor) *golden.Cursor {
	if cur == nil {
		return nil
	}
	return &golden.Cursor{X: cur.X, Y: cur.Y}
}

// widthsCoverEveryFrameLayout guards the fixture itself: the capture is only
// "all four layout modes" if the widths it uses actually reach all four.
func TestGolden_PromptFrameWidthsCoverEveryLayout(t *testing.T) {
	seen := map[frameLayout]int{}
	for _, width := range frameWidths {
		m := frameModel(t, width, 40)
		seen[m.frameLayout()] = width
	}
	for _, want := range []frameLayout{framePlain, frameNarrow, frameCompact, frameWide} {
		if _, ok := seen[want]; !ok {
			t.Fatalf("the golden widths never produce layout %d; the capture claims four modes it does not have", want)
		}
	}
}

// TestGolden_StartScreen captures the first-contact screen as the host
// assembles it: the survey's facts, the gate in effect, and the
// three offers a dirty Go checkout with a session to pick up produces —
// against the same screen in a clean checkout with nothing saved and no gate,
// which is the other end of what the survey can find.
func TestGolden_StartScreen(t *testing.T) {
	captureGolden(t, "start-screen", "first-contact screen", goldenWidths, func(width int) []golden.Panel {
		build := func(mut func(*StartInfo)) string {
			info := startFixture()
			mut(&info)
			m := frameModel(t, width, 40).WithStartScreen(info)
			return m.renderHistory()
		}
		typed := func() string {
			m := frameModel(t, width, 40).WithStartScreen(startFixture())
			m.input.SetValue("why is the round limit off by one")
			return m.renderHistory()
		}
		return []golden.Panel{
			{Label: "first contact · a dirty checkout with a session to pick up", View: build(func(i *StartInfo) {})},
			{Label: "the pointer on the offer that costs an approval", View: func() string {
				m := frameModel(t, width, 40).WithStartScreen(startFixture())
				m.startFocus = 2
				return m.renderHistory()
			}()},
			{Label: "clean tree · nothing saved, no gate, no project context", View: build(func(i *StartInfo) {
				i.Project.Dirty = 0
				i.Project.ContextFiles = nil
				i.Gate = StartGate{Path: ".shhh/quality.json"}
				i.Recent = StartRecent{}
			})},
			{Label: "outside a repository · the package count is a floor", View: build(func(i *StartInfo) {
				i.Project.Repo, i.Project.Branch, i.Project.Dirty = false, "", 0
				i.Project.Partial = true
			})},
			{Label: "nothing read · the third offer writes the file that would be", View: func() string {
				// The offer is only made where nothing was read, so the
				// context line and the row agree: this is the one screen
				// where "nothing read" is actionable.
				info := startFixture()
				info.Project.ContextFiles = nil
				m := frameModel(t, width, 40).
					WithStartScreen(info).
					WithScaffold(Scaffold{Offer: true, Paths: scaffoldFixturePaths(),
						Write: func() (string, error) { return project.ContextFile, nil }})
				return m.renderHistory()
			}()},
			{Label: "somebody else is in this checkout too", View: build(func(i *StartInfo) {
				i.Project.Sibling = startSibling
			})},
			{Label: "a checkout nobody has answered for · what it is holding back", View: build(func(i *StartInfo) {
				// The gate is one of the withheld kinds, so nothing loaded
				// it and the offer that costs an approval falls back to the
				// toolchain's own tests, exactly as it does with no gate.
				i.Gate = StartGate{Path: ".shhh/quality.json"}
				i.Trust = Trust{Withheld: []string{"skills", "agent profiles", "quality suites"}}
			})},
			{Label: "the checkout brought its own settings and its own wordings", View: build(func(i *StartInfo) {
				i.Project.ConfigFile = ".shhh/config.toml"
				i.Wordings = []string{"steer", "todo_standards", "todo_review"}
			})},
			{Label: "typing dismissed the list · the facts stay", View: typed()},
		}
	})
}

// TestGolden_StartProfile captures the screen of a project whose backlog is
// not a checkout of code's. The profile decides what an item is called, which
// fields it carries and which steps a run takes, so it is named beside the
// root and says where it was read from — a session working under one the
// reader did not choose would draw a backlog they cannot account for.
//
// It is a capture of its own rather than a panel on the first-contact one
// because the ordinary project says nothing here, and the screen that says
// nothing is the one the other capture is about.
func TestGolden_StartProfile(t *testing.T) {
	captureGolden(t, "start-profile", "the start screen naming the backlog profile", goldenWidths, func(width int) []golden.Panel {
		build := func(profile StartProfile) string {
			info := startFixture()
			info.Profile = profile
			m := frameModel(t, width, 40).WithStartScreen(info)
			return m.renderHistory()
		}
		return []golden.Panel{
			{Label: "the checkout carries its own profile", View: build(StartProfile{
				Name: "research", From: project.TodoProfileDir + "/"})},
			{Label: "a profile of the reader's own, by name", View: build(StartProfile{
				Name: "ops", From: "~/.config/shhh/todo/ops/"})},
			{Label: "one of the profiles shhh ships", View: build(StartProfile{
				Name: "notes", From: "built in"})},
		}
	})
}

// TestGolden_ScaffoldCard captures the card the scaffold offer opens: every
// path it would create before it asks, and the two ways of not writing that
// differ in what they leave behind.
//
// It captures the panel rather than the card, because the panel is what the
// bound is applied to — a card whose decision run is cut off the bottom of
// it is not a decision, and rendering the card alone would not show that.
func TestGolden_ScaffoldCard(t *testing.T) {
	captureGolden(t, "scaffold-card", "the scaffolding card in the panel", goldenWidths, func(width int) []golden.Panel {
		m := frameModel(t, width, 40).WithScaffold(Scaffold{
			Offer: true, Paths: scaffoldFixturePaths(),
			Write: func() (string, error) { return project.ContextFile, nil },
		})
		next, _ := m.scaffoldCommand()
		return []golden.Panel{
			{Label: "nothing written yet", View: next.(Model).panelView()},
		}
	})
}

// TestGolden_ProviderFailures captures the session's own mapping from a
// classified failure to a row: which class earns ⚠ and which
// earns ✗, what each says in its outcome, and which keys the session can
// honour for it. The component sheet in internal/ui/components captures the
// row; this captures the decisions the session makes about one.
func TestGolden_ProviderFailures(t *testing.T) {
	captureGolden(t, "provider-failures", "provider failures in a session", goldenWidths, func(width int) []golden.Panel {
		build := func(f *provider.Failure) string {
			m := frameModel(t, width, 40)
			m.modelName = "gpt-4o"
			m.providerName = "openai"
			m.replaceKeyFn = func(string) error { return nil }
			m.switchProviderFn = func(string) error { return nil }
			m.transcript = []entry{
				{kind: entryUser, text: "rename the round-limit sentinel"},
				{kind: entryFailure, fail: f, duration: 340 * time.Millisecond},
			}
			m.invalidateRenderCache()
			return m.renderHistory()
		}
		return []golden.Panel{
			{Label: "auth · the key it sent is named, and a new one can be entered", View: build(&provider.Failure{
				Class: provider.ClassAuth, Status: 401, Provider: "openai",
				Message: "Incorrect API key provided", KeyEnv: "SHHH_API_KEY or OPENAI_API_KEY", KeyTail: "4f9c",
			})},
			{Label: "rate limit · a stall, with the wait the provider named", View: build(&provider.Failure{
				Class: provider.ClassRateLimit, Status: 429, Provider: "openai",
				Message: "Rate limit reached for gpt-4o. Please try again in 38s.", RetryAfter: 38 * time.Second,
			})},
			{Label: "context length · the only class with a remedy of its own", View: build(&provider.Failure{
				Class: provider.ClassContextLength, Status: 400, Provider: "openai",
				Message: "This model's maximum context length is 128000 tokens",
			})},
			{Label: "unclassified · the message is the whole point of the row", View: build(&provider.Failure{
				Class: provider.ClassUnclassified, Status: 400, Provider: "openai",
				Message: "Unknown parameter: 'reasoning.effort'",
			})},
		}
	})
}

// TestGolden_RoundLimitPause captures the checkpoint a turn stops on when it
// runs out of tool rounds — the `rounds` row standing where the close
// block would be, in the four shapes the session can produce it: a turn that
// changed files and never re-ran the suite, one that changed nothing, one
// that has already been granted a block of rounds (which is where the doubled
// grant and [!] show up), and one whose offer has been taken.
//
// The numbers are the real ones a session produces: the default ceiling, and
// a second stop derived from the block rather than written out, so the panel
// and the row cannot disagree about what the grant buys.
func TestGolden_RoundLimitPause(t *testing.T) {
	captureGolden(t, "round-limit-pause", "the round-limit pause", goldenWidths, func(width int) []golden.Panel {
		build := func(p *roundPause) string {
			m := frameModel(t, width, 40)
			m.transcript = []entry{
				{kind: entryUser, text: "rename the round-limit sentinel"},
				{kind: entryRoundPause, turn: 7, pause: p, duration: 4*time.Minute + 12*time.Second},
			}
			m.invalidateRenderCache()
			return m.renderHistory()
		}
		// cap0 is the ceiling a turn starts with and cap1 the one it stops at
		// after taking the first grant.
		cap0 := DefaultMaxToolRounds
		cap1 := cap0 + roundGrantBlock
		return []golden.Panel{
			{Label: "the edits are unchecked · all three ways on", View: build(&roundPause{
				turn: 7, used: cap0, limit: cap0, files: 3, added: 30, removed: 4, stale: true,
			})},
			{Label: "nothing changed · only the grant can be honoured", View: build(&roundPause{
				turn: 7, used: cap0, limit: cap0,
			})},
			{Label: "a second stop · the grant doubles and [!] arrives", View: build(&roundPause{
				turn: 7, used: cap1, limit: cap1, granted: roundGrantBlock,
				files: 5, added: 112, removed: 40,
			})},
			{Label: "the offer is spent · the row keeps its words", View: build(&roundPause{
				turn: 7, used: cap0, limit: cap0, files: 3, added: 30, removed: 4, stale: true, spent: true,
			})},
		}
	})
}

// TestGolden_PressureCard captures the context-pressure card where the
// session actually raises it: in the bottom panel, at the end of a turn that
// left the window at the alert threshold.
// TestGolden_ContextScreen captures the context surface through the host:
// the pane it takes over, built from a real session's accounting rather than
// from a fixture, so the columns the component draws are checked against
// numbers the product actually produces.
func TestGolden_ContextScreen(t *testing.T) {
	captureGolden(t, "context-screen", "the context surface in the pane", goldenWidths, func(width int) []golden.Panel {
		m := contextModel(t, width)
		m = sendText(t, m, "/context")
		folded := strings.Join(m.contextLines(), "\n")
		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = updated.(Model)
		return []golden.Panel{
			{Label: "as it opens · both groups folded", View: folded},
			{Label: "the tool definitions opened", View: strings.Join(m.contextLines(), "\n")},
		}
	})
}

// TestGolden_SourcesScreen captures the sources screen through the host: the
// rows are resolved from a real ledger rather than from a fixture of drawn
// strings, so what the screen says a fetch cost is what the ledger recorded.
func TestGolden_SourcesScreen(t *testing.T) {
	captureGolden(t, "sources-screen", "the ledger of what the session read", goldenWidths, func(width int) []golden.Panel {
		m := sendText(t, sourcesModel(t, width), "/sources")
		opened := strings.Join(m.sourcesLines(), "\n")
		m.sources.Focus = 1
		return []golden.Panel{
			{Label: "as it opens · the pointer on the last thing read", View: opened},
			{Label: "a page that was kept · the preview opens it", View: strings.Join(m.sourcesLines(), "\n")},
		}
	})
}

// TestGolden_ProfileDrafter captures the drafting flow through the host: the
// surface is built from a session's own wiring — which kind of profile this
// is, which roles it already has, where a file could go — so the words on it
// are checked against what the product actually says rather than a fixture.
func TestGolden_ProfileDrafter(t *testing.T) {
	captureGolden(t, "profile-drafter", "the profile drafter in the pane", goldenWidths, func(width int) []golden.Panel {
		draft := &persona.Draft{
			Name:        "test-writer",
			Description: "adds table-driven tests for a package and runs them",
			Permissions: []string{"write", "execute"},
			MaxTokens:   8000,
			Why:         "a writer that could not run the tests would be proposing them, not adding them",
			Prompt: "You add table-driven tests for one package at a time. Read the package first, " +
				"then the tests it already has, then write the cases the existing table is missing.\n" +
				"Run the package's tests and fix what you broke. Do not touch any file outside the " +
				"package's own directory.",
		}
		m, _, _ := personaModel(t, persona.KindCode,
			persona.Outcome{Questions: []string{"Which package should it start from?", "Should it run the tests as well as write them?"}},
			persona.Outcome{Draft: draft},
		)
		m.width, m.height = width, 40
		m.syncInputWidth()
		pane := func(m Model) string { return m.personaPane(width, 26) }

		// Each step is captured as it stands: the surface is one object the
		// model holds a pointer to, so a view taken after the flow moved on
		// is a view of where it moved to.
		m = submitLine(t, m, "/agents new")
		brief := pane(m)
		m = submitLine(t, m, "/agents new something for tests")
		first := pane(m)
		m = pressOn(t, typeInto(t, m, "internal/agent"), tea.KeyPressMsg{Code: tea.KeyEnter})
		second := pane(m)
		m = pressOn(t, typeInto(t, m, "yes"), tea.KeyPressMsg{Code: tea.KeyEnter})
		return []golden.Panel{
			{Label: "the brief · the roles this session already has are on the header", View: brief},
			{Label: "the drafter's first question, asked on its own", View: first},
			{Label: "the second, with the first answer still above it", View: second},
			{Label: "the draft · both places a coding agent's profile can live", View: pane(m)},
		}
	})
}

func TestGolden_PressureCard(t *testing.T) {
	captureGolden(t, "pressure-card", "context pressure in the panel", goldenWidths, func(width int) []golden.Panel {
		m := pressureModel(t, width)
		m.armPressureCard()
		return []golden.Panel{
			{Label: "at the alert threshold, with the turns to keep", View: strings.Join(m.pressureLines(), "\n")},
		}
	})
}

// TestGolden_Interrupt captures a decision landing on a half-typed sentence
// : the card ungated above a live frame, and the same card once
// the card has been given the keyboard, with the draft held undressed
// beneath it.
// Read the two panels together — the pair is what invariant 5 asks a reader
// to check, and covering the colours must still answer "who has the
// keyboard".
func TestGolden_Interrupt(t *testing.T) {
	captureGolden(t, "interrupt", "a decision landing mid-sentence", goldenWidths, func(width int) []golden.Panel {
		const draft = "also add a --max-rounds flag while you're in there"
		ungated := interruptedModel(t, draft)
		ungated.width, ungated.height = width, 40
		ungated.syncInputWidth()
		ungated.syncViewport()
		gated := handover(t, ungated)
		// A session with turns behind it, because the vitals are what the
		// pair is being read for: the frame under the card states the mode,
		// the pressure and the spend a decision is answered against, and a
		// session that has spent nothing has none of them to state. The
		// totals go on after the handover has run, because the rail's
		// counters ease toward a figure they have not shown before and a
		// fixture wants the figure rather than a frame of the climb
		// (turnstatus.go).
		for _, m := range []*Model{&ungated, &gated} {
			m.TotalTokensIn += 41_200
			m.TotalTokensOut += 9_800
		}
		// A card that landed on a warm, empty keyboard: held, with the grace
		// window open and the run dimmed (interrupt.go).
		grace := interruptedModel(t, "")
		grace.width, grace.height = width, 40
		grace.syncInputWidth()
		grace.releaseDecision()
		grace.lastDecisionLeft = time.Time{}
		grace.lastKeypress = time.Now()
		grace.armDecision(stateConfirmRun)
		grace.syncViewport()
		return []golden.Panel{
			{Label: "ungated · the draft still has the keyboard", View: interruptSurface(ungated)},
			{Label: "gated · the handover, and the card has it", View: interruptSurface(gated)},
			{Label: "grace · held on a warm keyboard, keys a moment away", View: interruptSurface(grace)},
		}
	})
}

// TestGolden_DecisionNote captures the card with its note field open: the
// decision run drawn dead because the field has the keyboard, the ┄ label
// naming what the key that opened it asked for, and the two keys that close
// it (docs/interface/surfaces.md#the-approval-card).
//
// It records the cursor, which is the whole reason it is a capture of the
// panel rather than of the card: the field is the card's and the caret is the
// host's, and where the two meet is the one thing neither of them can be
// asked about on its own.
func TestGolden_DecisionNote(t *testing.T) {
	// One width below the card's own frame threshold, where the rows are
	// drawn bare and the rules are dropped: the field's place is counted
	// differently there, and that is the arithmetic worth a fixture.
	captureCursorGolden(t, "decision-note", "the approval card's note field", append([]int{14}, goldenWidths...),
		func(width int) (golden.Panel, *golden.Cursor) {
			m := interruptedModel(t, "")
			m.width, m.height = width, 40
			m.syncInputWidth()
			m = handover(t, m)
			m = typeInto(t, press(t, m, "N"), "not that file")
			m.syncViewport()
			return golden.Panel{
					Label: "the field open, and every letter going into it",
					View:  strings.Join(m.confirmPanelLines(), "\n"),
				},
				goldenCursor(m.confirmCursor(m.contentWidth()))
		})
}

// amendGoldenModel is a command card at a fixed width, with the keyboard, in
// a tree of its own so the blast radius is read somewhere this test owns.
func amendGoldenModel(t *testing.T, width int, command string) Model {
	t.Helper()
	m := gatedModel(t, nil, nil).WithWorkspace(t.TempDir()).
		WithRunner(func(context.Context, string) (string, int) { return "", 0 })
	m.width, m.height = width, 40
	m.syncInputWidth()
	m = execApproval(t, m, command)
	m.syncViewport()
	return m
}

// TestGolden_CommandAmend captures the command card's field: the decision run
// drawn dead because the field has the keyboard, the ┄ label naming what the
// key asked for, the line itself under it, and the two keys that close it
// (docs/interface/surfaces.md#the-approval-card).
//
// It records the cursor for the reason the note field's capture does: the
// field is the card's and the caret is the host's, and where the two meet is
// the one thing neither of them can be asked about on its own.
func TestGolden_CommandAmend(t *testing.T) {
	captureCursorGolden(t, "command-amend", "the approval card's command field", goldenWidths,
		func(width int) (golden.Panel, *golden.Cursor) {
			m := amendGoldenModel(t, width, "npm test")
			m = typeInto(t, press(t, m, keys.Shown(keys.Decision.Amend)), " -- --runInBand")
			m.syncViewport()
			return golden.Panel{
					Label: "the command open, and every letter going into it",
					View:  strings.Join(m.confirmPanelLines(), "\n"),
				},
				goldenCursor(m.confirmCursor(m.contentWidth()))
		})
}

// TestGolden_CommandAmended captures the two states either side of that
// field: the offer as it rides beside the decision run, and the card a line
// heavy enough to be asked about again draws — the `was` row under the
// headline, the chip on the title rail, and a blast radius resolved for the
// line that will actually run
// (docs/capabilities/approvals-and-safety.md#an-amended-command-is-a-new-command).
//
// The second panel is where the whole story is visible at once: at sixty
// columns the chip and the `was` row are competing for a card that is also
// carrying a warning, which is exactly the case a reader has to be able to
// read.
func TestGolden_CommandAmended(t *testing.T) {
	captureGolden(t, "command-amended", "the approval card's amendment", goldenWidths,
		func(width int) []golden.Panel {
			offered := amendGoldenModel(t, width, "npm test")
			amended := amendGoldenModel(t, width, "echo hi")
			amended = typeInto(t, press(t, amended, keys.Shown(keys.Decision.Amend)), "")
			e := *amended.commandEdit
			e.field.SetValue("rm -rf ./build")
			amended.commandEdit = &e
			updated, _ := amended.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			amended = updated.(Model)
			amended.syncViewport()
			return []golden.Panel{
				{Label: "the offer, beside the decision run", View: strings.Join(offered.confirmPanelLines(), "\n")},
				{Label: "the line the reader wrote, read again", View: strings.Join(amended.confirmPanelLines(), "\n")},
			}
		})
}

// TestGolden_QueueList captures the queue opened as the list that answers it:
// the rows the strip marked, each with the short field and its severity chip
// right-aligned, and the decisions that stay their own card counted on a dim
// row rather than dropped (docs/interface/surfaces.md#the-approval-card).
//
// Both panels are here because the fold is the half with no other witness: a
// list that simply left the flagged rows out would look exactly like this one
// at any width, and at sixty columns the chip and the target compete for the
// row where that would be lost.
func TestGolden_QueueList(t *testing.T) {
	captureGolden(t, "queue-list", "the approval queue as a list", goldenWidths,
		func(width int) []golden.Panel {
			build := func(t *testing.T, calls []provider.ToolCall) string {
				var ran []string
				m := execModel(t, &ran)
				m.width, m.height = width, 40
				m.syncInputWidth()
				updated, _ := m.Update(toolCallsMsg{calls: calls})
				m = openQueue(t, handover(t, updated.(Model)))
				m.syncViewport()
				return strings.Join(m.confirmLines(), "\n")
			}
			whole := build(t, []provider.ToolCall{
				execCall("c1", "go test ./internal/agent"),
				execCall("c2", "go build ./..."),
				execCall("c3", "gofmt -w internal/ui/chat/queue.go"),
			})
			folded := build(t, []provider.ToolCall{
				execCall("c1", "go test ./internal/agent"),
				execCall("c2", "git reset --hard"),
				execCall("c3", "go build ./..."),
				execCall("c4", "git reset --hard HEAD~2"),
			})
			return []golden.Panel{
				{Label: "three decisions \u00b7 every row checked as it opens", View: whole},
				{Label: "two left out \u00b7 counted, not hidden", View: folded},
			}
		})
}

// TestGolden_GrantList captures the grants the always-allow key opens: the
// decision run drawn dead because the list has the keyboard, the ┄ label the
// key was offered under, the three rows with what each one covers and — in
// the short right-aligned field — when it ends
// (docs/capabilities/approvals-and-safety.md#a-grant-says-when-it-ends).
//
// Sixty columns is the width the fixture exists for: the covered prefix and
// the end are competing for one row there, and the row gives up the prefix
// rather than the end, because the end is the whole reason the list is drawn
// before the grant is made.
func TestGolden_GrantList(t *testing.T) {
	captureGolden(t, "grant-list", "the grants the card can make", goldenWidths,
		func(width int) []golden.Panel {
			m := amendGoldenModel(t, width, "npm test --watch")
			m = press(t, m, keys.Shown(keys.Decision.Always))
			m.syncViewport()
			moved := press(t, m, "j")
			moved.syncViewport()
			return []golden.Panel{
				{Label: "the list open, the shortest grant under the pointer",
					View: strings.Join(m.confirmPanelLines(), "\n")},
				{Label: "one row down, on the grant that stands",
					View: strings.Join(moved.confirmPanelLines(), "\n")},
			}
		})
}

// TestGolden_ExplainView captures the screen the command card's explain key
// opens on, in both the states it has: the paragraph with the footer that
// names who said it and what asking took, and the reading that did not happen
// (docs/interface/surfaces.md#the-approval-card).
//
// Both panels are here because the failure is the half that has no other
// witness: a screen that renders an error as an empty body looks exactly like
// a screen whose model had nothing to say, and at sixty columns the footer is
// where the difference would be lost.
func TestGolden_ExplainView(t *testing.T) {
	captureGolden(t, "explain-view", "the command card's explanation", goldenWidths,
		func(width int) []golden.Panel {
			read := explainView(explainDoneMsg{
				command: "rsync -a --delete src/ dst/",
				verdict: agent.ExplainVerdict{
					Text: "Copies the contents of src/ into dst/, preserving permissions, " +
						"timestamps and symlinks, and deletes anything already in dst/ that " +
						"is not in src/.\n\nThe deletion is the part worth reading twice: " +
						"dst/ is made to match src/ rather than added to.",
					Model: "claude-haiku-4-5",
					Usage: provider.Usage{PromptTokens: 214, CompletionTokens: 96},
				},
			})
			read.SetSize(width, 20)
			failed := explainView(explainDoneMsg{
				command: "rsync -a --delete src/ dst/",
				verdict: agent.ExplainVerdict{
					Failed: true,
					Model:  "claude-haiku-4-5",
					Err:    "the explanation could not be read: context deadline exceeded",
				},
			})
			failed.SetSize(width, 14)
			return []golden.Panel{
				{Label: "the paragraph, and what asking it cost", View: read.View(width)},
				{Label: "a reading that did not happen", View: failed.View(width)},
			}
		})
}

// interruptSurface is the bottom panel a decision produces: ungated it is the
// card, its DRAFT rail and the live frame under them; gated it is the whole
// panel the card takes over.
func interruptSurface(m Model) string {
	if m.frameShowing() {
		return m.renderInterrupt(m.contentWidth()) + "\n" + m.renderPromptFrame()
	}
	return strings.Join(m.confirmPanelLines(), "\n")
}

// TestGolden_ScrollGutter captures the transcript pane's right-hand column
// in the four states it has: nothing to scroll, pinned to the
// live end with plenty above, halfway up, and at the top. The gutter is the
// only thing that changes between them, which is the point — the transcript
// wraps to the same width whether or not there is anything to draw in it, so
// the pane never reflows underneath a reader who scrolled.
//
// It is the whole viewport rather than the gutter alone: a column captured on
// its own would not show that it lands where the pane ends.
func TestGolden_ScrollGutter(t *testing.T) {
	captureGolden(t, "scroll-gutter", "the transcript's scroll gutter", []int{80, 130}, func(width int) []golden.Panel {
		// A short viewport, so a golden a reader has to check by counting
		// rows is small enough to count.
		gutter := func(entries []entry, mut func(*Model)) string {
			m := frameModel(t, width, 26)
			m.transcript = entries
			m.invalidateRenderCache()
			m.viewport.SetHeight(8)
			m.viewport.SetLines(m.renderHistoryLines())
			m.viewport.GotoBottom()
			mut(&m)
			return m.transcriptBody()
		}
		long := scrollFixture(24)
		return []golden.Panel{
			{Label: "nothing to scroll · the column is reserved and empty",
				View: gutter(scrollFixture(2), func(m *Model) {})},
			{Label: "the live end · the thumb is on the last row",
				View: gutter(long, func(m *Model) {})},
			{Label: "scrolled halfway up",
				View: gutter(long, func(m *Model) { m.viewport.SetYOffset(m.viewport.TotalLineCount() / 2) })},
			{Label: "the top of the transcript · the thumb is on the first row",
				View: gutter(long, func(m *Model) { m.viewport.GotoTop() })},
		}
	})
}

// TestGolden_ScrollGutterBesideTheDivider is the one capture that puts the
// gutter next to the thing it must not be mistaken for. Above the split
// threshold the transcript pane ends in the gutter and the rail begins one
// column later behind a `│`, so two columns of chrome sit side by side and
// only their shape keeps them apart
// (docs/interface/principles.md#colour-never-carries-meaning-alone).
//
// It is captured at the narrowest terminal that splits, because that is where
// the two columns are closest to the text on either side of them, and in both
// palettes, because mono is where every rung of chrome collapses onto the one
// grey and the mark has only its stroke and its length left.
// That terminal is the breakpoint itself: the rung is stated in terminal
// columns and read in content ones, and the constant carries the conversion
// (components/inspector.go), so a 130-column terminal is the first
// arrangement with a rail in it.
func TestGolden_ScrollGutterBesideTheDivider(t *testing.T) {
	captureGolden(t, "scroll-gutter-rail", "the scroll gutter beside the rail's divider",
		[]int{components.InspectorMinContentWidth + 2*horizontalPadding}, func(width int) []golden.Panel {
			build := func(mut func(*Model)) string {
				m := frameModel(t, width, screenHeight)
				m.transcript = scrollFixture(90)
				m.invalidateRenderCache()
				m.syncViewport()
				m.viewport.SetLines(m.renderHistoryLines())
				m.viewport.GotoBottom()
				mut(&m)
				return m.View().Content
			}
			return []golden.Panel{
				{Label: "the live end · the thumb is on the last row",
					View: build(func(m *Model) {})},
				{Label: "scrolled halfway up · the thumb and the divider a column apart",
					View: build(func(m *Model) { m.viewport.SetYOffset(m.viewport.TotalLineCount() / 2) })},
			}
		})
}

// scrollFixture is n read rows behind one prompt, numbered so a reader
// checking the thumb against the pane can see which slice of the whole is
// showing without counting rows. Callers pick an n that overflows the pane
// they built, since a gutter is only drawn when something is below. They are
// activity rows rather than prose because the subject is one column, and a
// markdown fixture would bury it under glamour's own escapes in the ansi
// block.
func scrollFixture(n int) []entry {
	es := []entry{{kind: entryUser, text: "read the round accounting"}}
	for i := 1; i <= n; i++ {
		es = append(es, entry{kind: entryTool, toolName: "read_file",
			toolArgs:   fmt.Sprintf(`{"path":"internal/agent/round%02d.go"}`, i),
			toolResult: "a\nb", duration: 200 * time.Millisecond})
	}
	return es
}

// TestGolden_SyntaxRegister captures the diff body's syntax register (
// P2-1): the palette read as a syntax register, where the monokai greens and
// pinks of a foreign theme used to sit next to an add/del gutter drawn from
// the product's own tokens.
//
// The fixture is chosen to exercise every rung at once — a comment, a
// declaration keyword, a function name, a string, a number, and the operators
// between them — so the ansi block is a table of the register's assignments
// rather than a sample of one of them. The mono pair is the other half of the
// claim: mono declines the register outright rather than collapsing it, so
// the same body comes back in the plain +/- styling.
func TestGolden_SyntaxRegister(t *testing.T) {
	captureGolden(t, "syntax-register", "the diff body's syntax register", goldenWidths, func(width int) []golden.Panel {
		hunks := []diff.Hunk{{
			OldStart: 12, OldCount: 5, NewStart: 12, NewCount: 6,
			Lines: []diff.Line{
				{Kind: diff.Context, Text: "// retryAfter is the backoff one 429 asks for.", OldNo: 12, NewNo: 12},
				{Kind: diff.Context, Text: "func retryAfter(h http.Header) time.Duration {", OldNo: 13, NewNo: 13},
				{Kind: diff.Del, Text: "\treturn 30 * time.Second", OldNo: 14},
				{Kind: diff.Add, Text: "\tif v := h.Get(\"Retry-After\"); v != \"\" {", NewNo: 14},
				{Kind: diff.Add, Text: "\t\treturn parseSeconds(v)", NewNo: 15},
				{Kind: diff.Add, Text: "\t}", NewNo: 16},
				{Kind: diff.Context, Text: "}", OldNo: 15, NewNo: 17},
			},
		}}
		body := func(mode components.DiffMode) string {
			d := &components.DiffView{
				Path: "internal/provider/retry.go", Verb: "edit",
				Hunks: hunks, Mode: mode, Height: 14,
				Syntax: diffSyntax("internal/provider/retry.go"),
			}
			return d.View(width)
		}
		return []golden.Panel{
			{Label: "expanded · the register over the diff kinds", View: body(components.DiffExpanded)},
			{Label: "full screen · the same body with room to breathe", View: body(components.DiffFull)},
		}
	})
}

// TestGolden_ReadingMode captures the surface the keyboard moves to, at every
// breakpoint: the labelled rail, the lit row and the hint bar have room at
// the wide end and the position field narrows at the tight one. The same
// screen with the keyboard in the other pane is captured beside it, because
// "only one pane is dressed" is a thing a reader checks by looking at both.
func TestGolden_ReadingMode(t *testing.T) {
	captureGolden(t, "reading-mode", "reading mode", goldenWidths, func(width int) []golden.Panel {
		reading := func(mut func(*Model)) string {
			m := goldenModel(t, width)
			next, _ := m.enterFocusMode()
			rm := next.(Model)
			mut(&rm)
			return readingSurface(rm)
		}
		return []golden.Panel{
			{Label: "the transcript has the keyboard", View: reading(func(m *Model) {})},
			{Label: "the input has it · plain rail, no row lit, the frame is accented",
				View: readingSurface(goldenModel(t, width))},
			{Label: "the cursor on a row that changed the machine", View: reading(func(m *Model) {
				m.moveFocus(-1)
				m.moveFocus(-1)
			})},
			{Label: "expanded under the cursor · [-] joins the bar", View: reading(func(m *Model) {
				m.moveFocus(-1)
				m.moveFocus(-1)
				next, _ := m.updateFocus(tea.KeyPressMsg{Code: tea.KeyEnter})
				*m = next.(Model)
			})},
			// The register with the cursor on a row that offers keys of its
			// own: the mode's keys, then the row's under its own rail, which
			// is the whole of what the keyboard can do from here.
			{Label: "[?] · the mode's whole key register, where the bar was", View: reading(func(m *Model) {
				next, _ := m.updateFocus(tea.KeyPressMsg{Code: '?', Text: "?"})
				*m = next.(Model)
			})},
			{Label: "prose · the cursor on a message: [y] copies its markdown source", View: func() string {
				m := frameModel(t, width, 40)
				m.transcript = []entry{
					{kind: entryUser, text: "why is the round limit fatal"},
					{kind: entryAssistant, text: "Round exhaustion is fatal in Agent.runRound: the loop\nreturns ErrRoundLimit, and the chat model treats any error\nfrom a round as terminal."},
				}
				m.invalidateRenderCache()
				next, _ := m.enterFocusMode()
				return readingSurface(next.(Model))
			}()},
		}
	})
}

// TestGolden_TranscriptSearch captures the way into the transcript search at
// every breakpoint: the query row where the mode's key bar was, with the pane
// marking what the query found, and the same search kept — the row closed,
// the pointer's occurrence bold on the lit row, and the pair that walks them
// on the bar.
//
// Then the half a search over rendered lines could not do. The last two
// panels are a query whose only occurrence is inside a step that is folded:
// the header counts it and offers the key, and the key opens the step onto
// the counted run that is still covering it. The rail carries the surface's
// own name, the query and a count that includes what nothing is drawing.
//
// It is the pane rather than the rendered transcript, because the marks are
// painted on the window and not on the lines the render produced, and the
// rail is above it because the count the reader steps by is up there.
func TestGolden_TranscriptSearch(t *testing.T) {
	captureGolden(t, "transcript-search", "the transcript search", goldenWidths, func(width int) []golden.Panel {
		// One path said four times over, which is what a reader searches a
		// transcript for: where was this file touched.
		reads := []entry{{kind: entryUser, text: "where does the round limit come from"}}
		for i, path := range []string{
			"internal/agent/loop.go", "internal/agent/tools.go",
			"internal/agent/loop.go", "internal/provider/retry.go",
			"internal/agent/loop.go",
		} {
			reads = append(reads, entry{kind: entryTool, toolName: "read_file",
				toolArgs:   fmt.Sprintf(`{"path":%q}`, path),
				toolResult: "a\nb", duration: time.Duration(200+i*10) * time.Millisecond})
		}
		search := func(keep bool) string {
			m := frameModel(t, width, 24)
			m.transcript = reads
			m.invalidateRenderCache()
			next, _ := m.enterFocusMode()
			rm := next.(Model)
			for _, msg := range []tea.KeyPressMsg{slashKey, {Code: 'l', Text: "l"},
				{Code: 'o', Text: "o"}, {Code: 'o', Text: "o"}, {Code: 'p', Text: "p"},
				{Code: '.', Text: "."}, {Code: 'g', Text: "g"}, {Code: 'o', Text: "o"}} {
				next, _ = rm.updateFocus(msg)
				rm = next.(Model)
			}
			if keep {
				// Enter closes the row; the pair then walks what it found.
				for _, msg := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, nextMatchKey} {
					next, _ = rm.updateFocus(msg)
					rm = next.(Model)
				}
			}
			// A short pane, so a golden a reader checks by looking for marks
			// is not mostly the blank rows under them.
			rm.viewport.SetHeight(10)
			rm.viewport.GotoTop()
			return searchSurface(rm)
		}
		// The only occurrence of this path is on a read inside a step that
		// has finished and folded, so nothing on screen is drawing it: the
		// header is what has to say it is there.
		inFold := func(open bool) string {
			m := goldenModel(t, width)
			next, _ := m.enterFocusMode()
			rm := next.(Model)
			// The cursor stands on the header, which is the row covering the
			// match and so the row the key is offered on.
			rm.focusIdx = 1
			rm.refreshFocusView()
			keys := []tea.KeyPressMsg{slashKey}
			for _, r := range "context.go" {
				keys = append(keys, tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			if open {
				// Enter closes the query row; enter again is the fold's.
				keys = append(keys, tea.KeyPressMsg{Code: tea.KeyEnter}, tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			for _, msg := range keys {
				next, _ = rm.updateFocus(msg)
				rm = next.(Model)
			}
			rm.viewport.SetHeight(12)
			rm.viewport.GotoTop()
			return searchSurface(rm)
		}
		return []golden.Panel{
			{Label: "the query row where the key bar was · every match bold", View: search(false)},
			{Label: "kept · the pointer's occurrence bold on the lit row, [n/N] on the bar", View: search(true)},
			{Label: "the only match is behind a fold · the header counts it and offers the key",
				View: inFold(false)},
			{Label: "[enter] opened the step · the run it holds still says what it is covering",
				View: inFold(true)},
		}
	})
}

// searchSurface is readingSurface through the pane: the marks a search leaves
// are painted on the window the transcript is read through, so a capture of
// the rendered lines would show the search finding nothing.
func searchSurface(m Model) string {
	return m.readingRail(m.contentWidth()) + "\n" + m.transcriptBody() + "\n" +
		dividerStyle(m.contentWidth()) + "\n" + m.panelView()
}

// readingSurface is the rail, the transcript and the bottom panel together —
// the whole of what says which pane holds the keyboard.
func readingSurface(m Model) string {
	rail := m.readingRail(m.contentWidth())
	body := m.renderHistory()
	if m.state == stateFocus {
		body, _, _ = m.renderFocusHistory()
		return rail + "\n" + body + "\n" + dividerStyle(m.contentWidth()) + "\n" + m.panelView()
	}
	return rail + "\n" + body + "\n" + promptSurface(m)
}

// TestGolden_KeyEntry captures the masked prompt an auth failure's [k] opens
// in the bottom panel, where a diff preview or an approval card would
// otherwise be.
func TestGolden_KeyEntry(t *testing.T) {
	captureGolden(t, "key-entry", "masked key entry in the panel", goldenWidths, func(width int) []golden.Panel {
		m := frameModel(t, width, 40)
		m.providerName = "openai"
		m.replaceKeyFn = func(string) error { return nil }
		next, _ := m.openKeyEntry(&provider.Failure{
			Class: provider.ClassAuth, KeyEnv: "SHHH_API_KEY or OPENAI_API_KEY", KeyTail: "4f9c",
		})
		opened := next.(Model)
		return []golden.Panel{
			{Label: "nothing pasted yet", View: strings.Join(opened.keyEntryLines(), "\n")},
		}
	})
}

// TestGolden_Palette captures the command palette in the bottom panel
// : the query line, the group rails, a command that cannot run
// while the agent works, and the count of what did not fit.
func TestGolden_Palette(t *testing.T) {
	captureGolden(t, "palette", "the command palette in the panel", goldenWidths, func(width int) []golden.Panel {
		m := frameModel(t, width, 40)
		m.recentFiles = func() []project.RecentFile {
			return []project.RecentFile{
				{Path: "internal/agent/loop.go", Mod: time.Now().Add(-4*time.Minute - time.Second)},
				{Path: "README.md", Mod: time.Now().Add(-2*time.Hour - time.Second)},
			}
		}
		opened, _ := m.openPalette()
		idle := opened.(Model)

		working := idle
		working.setTurnState(stateStreaming)
		reopened, _ := working.openPalette()
		working = reopened.(Model)
		working.palette.query = "co"
		working.refreshPalette()

		return []golden.Panel{
			{Label: "nothing typed yet", View: strings.Join(idle.pickerLines(), "\n")},
			{Label: "mid-turn, an idle-only command among the runnable ones", View: strings.Join(working.pickerLines(), "\n")},
		}
	})
}

// TestGolden_CompletionMenu captures the slash menu under the draft while a
// turn runs: the command that needs an idle session stays on the list behind
// ⊘ with the reason at the end of its row, rather than leaving the reader who
// typed /comp with an empty menu and no way to tell a command that is waiting
// from one this build does not have.
func TestGolden_CompletionMenu(t *testing.T) {
	captureGolden(t, "completion-menu", "the slash menu mid-turn", goldenWidths, func(width int) []golden.Panel {
		menu := func(text string, working bool) string {
			m := goldenModel(t, width)
			if working {
				m.setTurnState(stateStreaming)
			}
			m.input.SetValue(text)
			m.syncCompletions()
			return promptSurface(m)
		}
		return []golden.Panel{
			{Label: "idle · every command on the list is runnable", View: menu("/co", false)},
			{Label: "mid-turn · the greyed rows say why", View: menu("/co", true)},
			{Label: "mid-turn · the one command the prefix names", View: menu("/comp", true)},
		}
	})
}

// TestGolden_StatusRow captures the row that stands in for the inspector rail
// below the width the surface splits at. All four house widths are below that
// threshold, so every one of them draws it: the reading's verdict with the
// round it was taken at, and what the running turn or the idle session
// changed. The last panel is the row with nowhere left to go — at 60 columns
// the file count is dropped whole rather than clipped mid-word, and the
// verdict is what stands.
func TestGolden_StatusRow(t *testing.T) {
	captureGolden(t, "status-row", "the status row below the rail threshold", goldenWidths, func(width int) []golden.Panel {
		build := func(mut func(*Model)) string {
			m := statusRowModel(t, width)
			mut(&m)
			return m.statusRow()
		}
		return []golden.Panel{
			{Label: "working · the reading and what this turn has changed", View: build(func(m *Model) {
				m.state = stateStreaming
			})},
			{Label: "idle · the reading and the session's net change", View: build(func(m *Model) {})},
			{Label: "working · a reading the session has outrun, and 12 files", View: build(func(m *Model) {
				m.state = stateStreaming
				m.summary.last.Round = 128
				m.summary.schedule.Read(128)
				outrun(m, 128)
				for i := range 12 {
					m.changes.Add(1, changeset.Record{
						Path: "internal/agent/f" + string(rune('a'+i)) + ".go", AfterExists: true, After: "x\n",
					})
				}
			})},
			{Label: "the row in its slot, between the notices and the box", View: statusRowModel(t, width).renderPromptFrame()},
		}
	})
}

// screenWidths adds the rungs the standing widths do not land on and two
// terminals past the widest of them. 12 is the narrowest terminal the draft
// is still framed in and 70 is where the vitals fold into the border, so the
// whole-screen capture carries every rung of guidelines/layout-breakpoints;
// 144 and 200 are past the split, where the rail grows with the terminal, and
// the pair is what shows that the growth goes to the rail's blocks rather
// than to the gap beside them.
var screenWidths = append(append([]int{12, 70}, goldenWidths...), 144, 200)

// screenHeight is the row count every whole-screen panel is captured at. It
// is fixed because the capture's subject is the vertical arrangement: the
// chrome, the pane, the live tail under it and the bottom panel have to add
// up to exactly this many rows at every width and in every state.
const screenHeight = 30

// TestGolden_Screen captures the whole surface — everything View() paints,
// chrome and padding included. The other captures in this file each
// hold one block of it; this one holds the arrangement, which is the thing
// no substring assertion and no per-block golden can see: that the header,
// the reading rail, the transcript pane, whatever the turn is doing under it
// and the bottom panel together fill the terminal exactly once.
func TestGolden_Screen(t *testing.T) {
	captureGolden(t, "screen", "the whole surface", screenWidths, func(width int) []golden.Panel {
		build := func(mut func(*Model)) string {
			m := frameModel(t, width, screenHeight)
			m.transcript = goldenTranscript()
			mut(&m)
			m.invalidateRenderCache()
			m.syncViewport()
			m.viewport.SetLines(m.renderHistoryLines())
			m.viewport.GotoBottom()
			return m.View().Content
		}
		return []golden.Panel{
			{Label: "idle · the draft has the keyboard", View: build(func(m *Model) {})},
			{Label: "working · the live tail sits under the pane", View: build(func(m *Model) {
				m.state = stateStreaming
				m.streaming = ""
			})},
			// The session summary leads the rail where there is a rail to
			// lead; below 130 columns the same panel is the single-pane
			// surface with the status row standing in for it above the
			// input, which is how the capture shows what the narrow terminal
			// keeps of the block and what it has to ask for.
			{Label: "working · a reading of the session leads the rail", View: build(func(m *Model) {
				m.state = stateStreaming
				m.streaming = ""
				m.summarizer = agent.NewSummarizer(&readingProvider{}, agent.SummaryConfig{Model: "fast"})
				m.summary.last = &agent.SummaryVerdict{
					Text:  "Wiring the round-limit pause into the chat model; the sentinel is in and nothing has run the tests yet.",
					State: agent.SummaryOnTarget,
					Round: 24,
					Model: "fast",
				}
				m.summary.schedule.Read(24)
			})},
		}
	})
}

// TestGolden_ScreenAttached captures the arrangement this surface had no
// picture of: the keyboard in a child's session, with the rail still up
// beside it. One width, because the rail's presence is what the sheet is
// about and a terminal too narrow to split has no rail to keep — and the
// whole screen rather than the rail alone, because the fact being pinned is
// that the child's transcript on the left and the session's numbers on the
// right can be told apart at a glance, which only the two together show.
//
// The child is given a conversation before it is drawn, because a rail with
// nothing to report is not the rail this fixture is for: the frame's top rail
// names the phase off the call the child still has open, and the vitals rail
// measures the pressure off the conversation behind it.
func TestGolden_ScreenAttached(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(),
		NewEnv: billedEnv(provider.Usage{PromptTokens: 4200, CompletionTokens: 900})})
	t.Cleanup(sup.Close)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	spawnChild(t, sup, subagent.RoleReviewer, "reviewer-1")
	killChild(t, sup, "reviewer-1")
	waitFor(t, func() bool {
		st, ok := sup.Get("researcher-1")
		return ok && st.Spend.In > 0
	})
	noteChild(t, sup, "researcher-1", subagent.TranscriptEntry{
		Kind: subagent.EntryAssistant, Text: "Reading the round accounting first."})
	noteChild(t, sup, "researcher-1", subagent.TranscriptEntry{
		Kind: subagent.EntryTool, Tool: "read_file", Args: `{"path":"internal/agent/loop.go"}`,
		Result: strings.Repeat("internal/agent/loop.go:118 the round counter is read here\n", 220)})
	noteChild(t, sup, "researcher-1", subagent.TranscriptEntry{
		Kind: subagent.EntryTool, Tool: "read_file", Args: `{"path":"internal/agent/round.go"}`,
		Pending: true})
	captureGolden(t, "screen-attached", "the surface with the keyboard in a child",
		[]int{144}, func(width int) []golden.Panel {
			build := func(name string) string {
				m := frameModel(t, width, screenHeight)
				m.transcript = goldenTranscript()
				// The turn the transcript closes on spent two rounds, and the
				// counter states them at rest: the rail sheds the round third
				// and the model first (guidelines/layout-drop-order).
				for range 2 {
					m.agent.BeginToolRound("", nil, nil)
				}
				m = m.WithSubagents(sup)
				m.attach(name)
				m.invalidateRenderCache()
				m.syncViewport()
				m.viewport.SetLines(m.renderHistoryLines())
				m.viewport.GotoBottom()
				return m.View().Content
			}
			return []golden.Panel{
				{Label: "the keyboard in this session · the map marks its first row", View: build("")},
				{Label: "the keyboard in a child · the rail stays, marked", View: build("researcher-1")},
			}
		})
}

// TestGolden_StaleEditRow pins the row an edit refused for staleness leaves
// behind: the file and what happened to it on one line, the sentence the
// model was given under it once the row is opened, and — beside them — the
// row a call the model simply malformed gets, which names its tool and folds
// its own sentence the same way.
func TestGolden_StaleEditRow(t *testing.T) {
	captureBoundedGolden(t, "stale-edit-row", "the refused stale edit", []int{80}, func(width int) []golden.Panel {
		build := func(open bool) string {
			m := frameModel(t, width, 40)
			m = m.WithWorkspace("/work/shhh")
			stale := m.skippedCallEntry("write_file", fmt.Errorf("invalid arguments: %w",
				tools.StaleError{Path: "/work/shhh/internal/agent/loop.go"}))
			stale.expanded = open
			bad := m.skippedCallEntry("write_file", errors.New("invalid arguments: path is required"))
			bad.expanded = open
			m.transcript = []entry{
				{kind: entryUser, text: "rebase the round cap on what loop.go says now"},
				stale,
				bad,
			}
			m.invalidateRenderCache()
			return m.renderHistory()
		}
		return []golden.Panel{
			{Label: "the row · a file that moved, and a call that was malformed", View: build(false)},
			{Label: "the row opened · the sentence the model was given", View: build(true)},
		}
	})
}

// TestGolden_AutoApproved pins what a call nobody was asked about says about
// itself. Each of the three is one row and not two: the account of who
// allowed it sits in the act's own outcome field, where the row already
// bounds what it prints.
//
// The three are the three shapes the account has to survive. The edit keeps
// it on the diff row, beside the stats. The staging keeps it beside a receipt
// and a target the row cuts to `first +19` — twenty paths spelled on a line
// above the row was what the account used to cost. And the command carries
// the one rule whose judgement is billed in seconds, which is why the
// classifier's row is the only one that states a second figure beside the
// call's own duration.
//
// Three widths, because the account is what the row gives up when it runs
// out of room: at 110 every row states it, at 80 the two that can still
// afford it do, and at 60 none of them do and every row spends what it saved
// on its target. That give-way is the point of capturing the narrow widths —
// the account is worth a row's spare columns and never worth its target.
func TestGolden_AutoApproved(t *testing.T) {
	captureBoundedGolden(t, "auto-approved", "what allowed an act nobody was asked about", []int{60, 80, 110}, func(width int) []golden.Panel {
		paths := make([]string, 20)
		for i := range paths {
			paths[i] = fmt.Sprintf("internal/ui/chat/row%02d.go", i+1)
		}
		staged, err := json.Marshal(map[string]any{"verb": "add", "paths": paths})
		if err != nil {
			t.Fatal(err)
		}
		m := frameModel(t, width, 40)
		m.transcript = []entry{
			{kind: entryDiff, diff: &components.DiffView{
				Path: "internal/ui/chat/approval.go", Verb: "edit",
				Hunks: []diff.Hunk{{
					OldStart: 417, OldCount: 3, NewStart: 417, NewCount: 2,
					Lines: []diff.Line{
						{Kind: diff.Context, Text: "\t\treq.autoRule = reason", OldNo: 417, NewNo: 417},
						{Kind: diff.Del, Text: "\t\tm.appendEntry(entry{kind: entrySystem, text: notice})", OldNo: 418},
						{Kind: diff.Context, Text: "\t\tif req.kind == approvalExec {", OldNo: 419, NewNo: 418},
					},
				}},
				Mode: components.DiffCollapsed, MaxLines: maxDiffExpandedLines,
				Allowed: allowedLabel("auto mode", 0),
			}},
			{kind: entryTool, toolName: structural.GitWriteToolName, toolArgs: string(staged),
				toolResult: "staged 20 files", duration: 300 * time.Millisecond,
				allowedBy: "auto mode"},
			{kind: entryCommand, text: "go test ./internal/ui/...", duration: 27 * time.Second,
				toolResult: "ok  \tgithub.com/rfizzle/shhh/internal/ui/chat\t27.107s",
				allowedBy:  classifierRule, allowElapsed: 2100 * time.Millisecond},
		}
		m.invalidateRenderCache()
		return []golden.Panel{{Label: "an edit, a staging and a command, one row each", View: m.renderHistory()}}
	})
}

// TestGolden_ChildAutoApproved pins the same rule one level down: a parent
// attached to a child draws the child's auto-approved calls the way it draws
// its own, one row each with the account in the outcome field. The fan-out
// view was the last surface that stated an act twice — a notice naming the
// call, then the row naming it again — and both of the shapes that notice
// covered are here: an edit the child's mode allowed outright, and a command
// the classifier was paid to think about.
//
// One width, because the give-way when the account runs out of room is the
// row's own and is pinned at three widths by TestGolden_AutoApproved. What
// this sheet is about is that the account crosses the mirror at all.
func TestGolden_ChildAutoApproved(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	// The child opens its own transcript with the task it was spawned on,
	// and it does that as its first round starts rather than as it is
	// spawned — so the rows below go on the end of a transcript that is
	// already there, and the sheet is the same page every run.
	waitFor(t, func() bool { return len(sup.Transcript("researcher-1")) > 0 })
	note := func(e subagent.TranscriptEntry) {
		t.Helper()
		if err := sup.Note("researcher-1", e); err != nil {
			t.Fatal(err)
		}
	}
	note(subagent.TranscriptEntry{Kind: subagent.EntryTool, Tool: tools.EditFileName,
		Args:   `{"path":"internal/agent/loop.go","old_text":"maxRounds","new_text":"roundCap"}`,
		Result: "edited internal/agent/loop.go", AllowedBy: "auto mode"})
	note(subagent.TranscriptEntry{Kind: subagent.EntryTool, Tool: tools.ExecCommandName,
		Args:      `{"command":"go test ./internal/agent/..."}`,
		Result:    "ok  \tgithub.com/rfizzle/shhh/internal/agent\t4.209s",
		AllowedBy: classifierRule, AllowElapsed: 2100 * time.Millisecond})

	captureGolden(t, "child-auto-approved", "a child's acts, mirrored by the parent",
		[]int{110}, func(width int) []golden.Panel {
			m := frameModel(t, width, 40)
			m = m.WithSubagents(sup)
			m.attach("researcher-1")
			m.invalidateRenderCache()
			return []golden.Panel{{Label: "an edit the mode allowed and a command the classifier did", View: m.renderAttachedHistory()}}
		})
}

// TestGolden_GitWriteRows pins the rows a turn's git writes leave behind: the
// four verbs on the accent rail under the command glyph, the receipt in the
// outcome column where the field never clips, and the close of a turn that
// committed — which names the sha, says what undo does not reach, and no
// longer offers [u].
//
// One width, because 80 columns is where the receipt and the target compete
// for the row: wider and both simply fit.
func TestGolden_GitWriteRows(t *testing.T) {
	captureGolden(t, "git-write-rows", "the rows a git write leaves", []int{80}, func(width int) []golden.Panel {
		row := func(args, result string) entry {
			return entry{kind: entryTool, toolName: structural.GitWriteToolName,
				toolArgs: args, toolResult: result, duration: 300 * time.Millisecond}
		}
		build := func(es ...entry) string {
			m := frameModel(t, width, 40)
			m.transcript = es
			m.invalidateRenderCache()
			return m.renderHistory()
		}
		return []golden.Panel{
			{Label: "the four verbs", View: build(
				row(`{"verb":"add","paths":["internal/agent/loop.go","internal/agent/mode.go"]}`, "staged 2 files"),
				row(`{"verb":"commit","message":"feat(agent): cap rounds at the limit instead of erroring"}`,
					"committed 3 files as a41f2c9 on master"),
				row(`{"verb":"branch","branch":"topic"}`, "created branch topic"),
				row(`{"verb":"switch","branch":"master"}`,
					"switched to master\nreads dropped · the working tree changed under every prior read"),
			)},
			{Label: "refused · a file the session did not change", View: build(
				row(`{"verb":"add","paths":["README.md"]}`,
					"error: README.md is not this session's work; commit it yourself"),
			)},
			{Label: "committed on an untrusted checkout · the hooks that did not run", View: build(
				row(`{"verb":"commit","message":"feat(agent): cap rounds at the limit"}`,
					"committed 3 files as a41f2c9 on master\nhooks skipped · checkout not trusted — /trust to run them"),
			)},
			{Label: "the turn's close · the sha, and what undo does not reach", View: build(
				row(`{"verb":"commit","message":"feat(agent): cap rounds at the limit"}`,
					"committed 3 files as a41f2c9 on master"),
				entry{kind: entryTurnClose, turn: 1, close: &components.TurnClose{
					State: components.TurnDone, Steps: 4, Tools: 18, Elapsed: "1m 04s", Spend: "$0.14",
					Changes: &components.TurnChanges{Files: 3, Added: 30, Removed: 4,
						Keys: []components.TurnKey{{Key: "[v]", Label: "review"}},
						Note: "all tracked in git"},
					Commit: &components.TurnCommit{Receipt: "committed 3 files as a41f2c9 on master"},
				}},
			)},
		}
	})
}

// TestGolden_SearchSweep pins what a run of searches leaves on the feed. A
// search's row is the one row read to tell a session asking many questions
// from a session asking one question many times, and it can only do that if
// the pattern is in it: led by the directory, the two panels below would be
// the same six rows.
//
// Every breakpoint, because the target is the whole subject here: at the
// tight end a pattern and its scope compete for the field, and at the wide
// end both simply fit.
func TestGolden_SearchSweep(t *testing.T) {
	captureGolden(t, "search-sweep", "searches of one package on the feed", goldenWidths, func(width int) []golden.Panel {
		row := func(pattern, result string, d time.Duration) entry {
			return entry{kind: entryTool, toolName: "search",
				toolArgs:   fmt.Sprintf(`{"pattern":%q,"path":"internal/ui/chat"}`, pattern),
				toolResult: result, duration: d}
		}
		build := func(es ...entry) string {
			m := frameModel(t, width, 40)
			m.transcript = es
			m.invalidateRenderCache()
			return m.renderHistory()
		}
		hits := "internal/ui/chat/compose.go:41:\tsteeringItem{}"
		return []golden.Panel{
			{Label: "three questions about one package", View: build(
				row("steeringItem", hits, 300*time.Millisecond),
				row("queuedSteer", hits+"\ninternal/ui/chat/queue.go:12:\tqueuedSteer", 400*time.Millisecond),
				row("authorOf", tools.NoMatchesFound, 200*time.Millisecond),
			)},
			{Label: "one question three times · the shape a reader is watching for", View: build(
				row("steeringItem", hits, 300*time.Millisecond),
				row("steeringItem", hits, 300*time.Millisecond),
				row("steeringItem", hits, 400*time.Millisecond),
			)},
		}
	})
}

// TestGolden_SearchCounts pins the counts field of a reader's row: what the
// call found, beside what it printed.
//
// The four rows are one result each. A truncated search prints three hundred
// lines and found fifty, and the row has to say both the fifty and that there
// are more — `50+` — because a reader told `298 matches` reads the sweep as
// exhaustive at the moment the tool said it was cut short. files_only counts
// files, since that is what its lines are. A path list is one line per path
// with the same notice on the end, which is the shape that hid this. And a
// search that found nothing leaves the field empty rather than claiming the
// sentence saying so as a finding.
func TestGolden_SearchCounts(t *testing.T) {
	captureGolden(t, "search-counts", "what a reader's row counts", goldenWidths, func(width int) []golden.Panel {
		var sweep strings.Builder
		for i := 1; i <= tools.MaxSearchResults; i++ {
			fmt.Fprintf(&sweep, "internal/ui/chat/queue.go:%d- \tqueue := m.pending\n", i*10-1)
			fmt.Fprintf(&sweep, "internal/ui/chat/queue.go:%d: \tsteeringItem{}\n", i*10)
			fmt.Fprintf(&sweep, "internal/ui/chat/queue.go:%d- \treturn queue\n", i*10+1)
			sweep.WriteString("--\n")
		}
		sweep.WriteString("… (truncated at 50 matches; narrow the pattern or path, " +
			"or raise limit to at most 500, or use files_only to see which files are involved)")
		tool := func(name, args, result string, d time.Duration) entry {
			return entry{kind: entryTool, toolName: name, toolArgs: args, toolResult: result, duration: d}
		}
		m := frameModel(t, width, 40)
		m.transcript = []entry{
			tool("search", `{"pattern":"steeringItem","path":"internal/ui/chat"}`,
				sweep.String(), 900*time.Millisecond),
			tool("search", `{"pattern":"queuedSteer","path":"internal/ui/chat","files_only":true}`,
				"internal/ui/chat/queue.go: 41 matches\ninternal/ui/chat/compose.go: 9 matches", 400*time.Millisecond),
			tool("glob", `{"pattern":"**/*.go","path":"internal/ui/chat"}`,
				"queue.go\ncompose.go\n… (truncated at 2 files; narrow the pattern or path to see more)",
				200*time.Millisecond),
			tool("search", `{"pattern":"authorOf","path":"internal/ui/chat"}`,
				tools.NoMatchesFound, 200*time.Millisecond),
		}
		m.invalidateRenderCache()
		return []golden.Panel{{Label: "found, not printed", View: m.renderHistory()}}
	})
}

// TestGolden_FetchWait pins the row a paced fetch draws while the host it
// asked is being waited out: the seconds left and the host that asked for
// them, in the outcome field where the row's reason to be read goes, and the
// fetch to another host beside it still running — because pacing one site
// says nothing about another.
//
// One width, because the fields are the grid's own: 80 columns is where the
// URL and the countdown compete for the row, and wider they simply fit.
func TestGolden_FetchWait(t *testing.T) {
	captureGolden(t, "fetch-wait", "a fetch waiting out a host's refusal", []int{80}, func(width int) []golden.Panel {
		waiting := func(left time.Duration) Model {
			return frameModel(t, width, 40).WithFetchWaits(func(host string) (time.Duration, bool) {
				if host != "docs.rs" {
					return 0, false
				}
				return left, true
			}, func() {})
		}
		mirrored := func(left time.Duration) string {
			m := waiting(left)
			m.transcript = []entry{
				{kind: entryTool, toolName: web.FetchToolName, toolResult: pendingToolResult,
					toolArgs: `{"url":"https://docs.rs/tokio/latest/tokio/runtime/index.html"}`},
				{kind: entryTool, toolName: web.FetchToolName, toolResult: pendingToolResult,
					toolArgs: `{"url":"https://pkg.go.dev/net/http"}`},
			}
			m.invalidateRenderCache()
			return m.renderHistory()
		}
		own := waiting(20 * time.Second)
		own.pendingApproval = &approvalRequest{call: provider.ToolCall{Name: web.FetchToolName,
			Arguments: `{"url":"https://docs.rs/tokio/latest/tokio/runtime/index.html"}`}}
		row, _ := own.fetchWaitRow(width)
		return []golden.Panel{
			{Label: "a child's rows · one host waited out, another still running", View: mirrored(8 * time.Second)},
			{Label: "the last second of the wait", View: mirrored(900 * time.Millisecond)},
			{Label: "the session's own fetch · the live row under the transcript", View: row},
		}
	})
}

// TestGolden_TodoNoRepository captures the two rows a backlog run draws in
// a directory that is not a repository: the refusal, which arrives before
// the first stage has spent a turn, and the close of a run asked for
// without a commit, whose report has to say where the change is because
// there is no history to find it in.
//
// The pair is on one sheet because they are the two halves of one answer —
// what the run will not do, and what it does instead. One width: a notice
// is rendered as the sentence it is and does not reflow, so the other three
// captures would be copies of this one.
func TestGolden_TodoNoRepository(t *testing.T) {
	captureGolden(t, "todo-no-repo", "a backlog run without a repository", []int{80}, func(width int) []golden.Panel {
		row := func(text string) string {
			m := frameModel(t, width, 40)
			m.appendEntry(entry{kind: entrySystem, text: text})
			return m.renderHistory()
		}
		it := todo.Item{
			Slug: "cache-ttl", Title: "Give the cache a lifetime",
			Profile: todo.BuiltinCode(), Fields: map[string]string{"size": "S"},
			Priority: todo.PriorityHigh,
			Path:     ".shhh/todo/cache-ttl.md",
		}
		st := run.Start(it, "amber-lake", "manual", 1, run.Options{NoCommit: true})
		st.Paths = []string{"internal/provider/cache.go", "internal/provider/cache_test.go"}
		st.Stage = run.StageReview
		st.Observe(it, "verdict: clean")
		return []golden.Panel{
			{Label: "the refusal, before any stage has spent a turn",
				View: row(todoNoRepoNotice("~/scratch/notes", it.Slug))},
			{Label: "a run asked for without a commit, archived",
				View: row(todoRunDoneNote(st, ".shhh/todo/done/cache-ttl.md") + "\n\n" + st.Report)},
		}
	})
}

// TestGolden_NewSessionRow captures the row a session boundary opens the new
// conversation on. It is where the exit banner would have been — the slot the
// last conversation is in and the command that reopens it — plus, when a
// backlog run was let go of at its checkpoint, the command that continues it.
//
// Two widths, at the breakpoints either side of the row. The boundary itself
// is a row on the grid — the slot in the growing field, how to get the
// conversation back in the outcome — so the property being pinned is that
// both fit their pane: the prose this used to be ran to a hundred and
// sixteen columns at either width. The offer beside it is prose still, and
// wraps rather than running past the edge.
func TestGolden_NewSessionRow(t *testing.T) {
	captureBoundedGolden(t, "new-session-row", "the row a new session opens on", []int{80, 110}, func(width int) []golden.Panel {
		rows := func(es ...entry) string {
			m := frameModel(t, width, 40)
			m.appendEntries(es)
			return m.renderHistory()
		}
		it := todo.Item{
			Slug: "cache-ttl", Title: "Give the cache a lifetime",
			Profile: todo.BuiltinCode(), Fields: map[string]string{"size": "S"},
			Priority: todo.PriorityHigh,
			Path:     ".shhh/todo/cache-ttl.md",
		}
		st := run.Start(it, "amber-lake", "manual", 1, run.Options{})
		st.Stage = run.StageImplement
		const slot, resume = "2026-09-04 11:20:07", "shhh code --continue"
		boundary := entry{kind: entrySystem, notice: newSessionRow(slot, resume)}
		kept := entry{kind: entrySystem, text: todoRunKeptNote(it, st, "this session ended")}
		return []golden.Panel{
			{Label: "the slot left behind, and the command that reopens it",
				View: rows(boundary)},
			{Label: "with a backlog run kept at its checkpoint",
				View: rows(boundary, kept)},
			{Label: "a conversation that was never written down",
				View: rows(entry{kind: entrySystem, notice: newSessionRow("", "")})},
		}
	})
}

// TestGolden_ResumedRow captures the row a conversation comes back on: the
// branch it is looking at and how much is changed, folded, and the reading
// the conversation was actually given underneath it.
//
// Two widths either side of the narrow breakpoint, because the body is the
// part that has to survive one. The line is a row on the grid — `resumed` in
// the verb column, the branch and how much is changed beside it — and the
// body is wrapped rather than clipped, which is what a narrow capture pins:
// a body that clipped would promise a reading and show half a sentence.
func TestGolden_ResumedRow(t *testing.T) {
	captureBoundedGolden(t, "resumed-row", "the row a resumed conversation opens on", []int{60, 80}, func(width int) []golden.Panel {
		row := func(n ResumeNotice, expanded bool) string {
			m := frameModel(t, width, 40)
			m.appendEntry(entry{kind: entrySystem, toolResult: n.Text, expanded: expanded,
				notice: &components.ActivityNotice{Verb: resumeVerb, Subject: n.Subject}})
			return m.renderHistory()
		}
		const (
			was = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"
			now = "e4f5a6b7c8d9e0f1a2b3c4d5e6f708192a3b4c5d"
		)
		here := project.Info{Dir: "/w", Repo: true, Branch: "master", Dirty: 3, Head: now}
		still := resumeNotice(here, storage.ChatResume{Head: now})
		moved := resumeNotice(here, storage.ChatResume{Head: was,
			Summary: "The cache work is half done: the lifetime is read from config and honoured on get, " +
				"and the eviction pass is written but not yet called from anywhere."})
		return []golden.Panel{
			{Label: "folded, which is how it opens", View: row(still, false)},
			{Label: "opened, on a checkout that has not moved", View: row(still, true)},
			{Label: "opened, on one that moved and with a summary from its last compaction",
				View: row(moved, true)},
		}
	})
}

// TestGolden_CompactReceipt captures what a compaction leaves on the
// transcript: the receipt line naming the turns it kept, the summary quoted
// under it, and the kept turns themselves below.
//
// The capture is here for the slant. The summary is the model's own words and
// is the only italic run the transcript draws — every hint, marker and notice
// around it is upright — so the fixture is where that stays true: a chrome
// style that reaches for italic again shows up as a second italic run in a
// file whose whole point is that there is one.
func TestGolden_CompactReceipt(t *testing.T) {
	captureGolden(t, "compact-receipt", "the receipt a compaction leaves", goldenWidths, func(width int) []golden.Panel {
		const summary = "Rounds are counted in the round loop; the limit lived in three places and " +
			"disagreed. The first three turns established the loop as the owner, the fourth moved " +
			"the constant, and the fifth's tests pass except the one on the limit itself."
		panel := func(kept []provider.Message) string {
			m := frameModel(t, width, 40)
			m.appendEntry(entry{kind: entrySystem,
				text: compactedNotice(len(kept) > 0, m.keptTurnCount(kept))})
			m.appendEntry(entry{kind: entryCompactSummary, text: summary})
			m.appendMessageEntries(kept)
			return m.renderHistory()
		}
		return []golden.Panel{
			{Label: "the receipt and the summary quoted under it", View: panel(nil)},
			{Label: "with the turns the compaction kept verbatim", View: panel([]provider.Message{
				{Role: provider.RoleUser, Content: "Move the round limit into the loop."},
				{Role: provider.RoleAssistant, Content: "Moved it, and the *limit* is read from one place now."},
			})},
		}
	})
}

// TestGolden_ItemDraft captures the card an item is written on without
// leaving the session: the header as rows a key steps in place, the slug the
// title will become on the title rail, the body rendered by the renderer the
// transcript uses, and — pinned above the key row, where it cannot scroll
// away — the warning about a dependency that names nothing.
//
// The second panel is the same card with the dependency row opened. The
// picker is the backlog itself, which is what makes a dependency a slug that
// exists rather than a name somebody typed.
func TestGolden_ItemDraft(t *testing.T) {
	captureGolden(t, "item-draft", "the item draft card", goldenWidths, func(width int) []golden.Panel {
		root := todoTestRoot(t)
		m := frameModel(t, width, 40)
		m.sessionName = "2026-09-04 09:00:00"
		m = m.WithTodos(Todos{Profile: todo.BuiltinCode(), Root: root, Manage: func([]string) string { return "" },
			Detail: func(*todo.Store, todo.Item) string { return "" }})
		proposals, ok := todo.ParseProposals(todo.BuiltinCode(), draftFixture)
		if !ok {
			t.Fatal("the fixture should parse as a proposal")
		}
		m.openTodoDraft(proposals[0], -1)
		drafted := m.panelView()
		opened := m
		for _, k := range []tea.KeyPressMsg{keyDown, keyDown, keyDown, keySpace} {
			updated, _ := opened.Update(k)
			opened = updated.(Model)
		}
		return []golden.Panel{
			{Label: "as it was drafted · the header on rows, the reading under them",
				View: drafted},
			{Label: "the dependency row opened on the backlog",
				View: opened.panelView()},
		}
	})
}

// TestGolden_TodoSprint captures the surface the sprint is chosen on: the
// proposal on the backlog screen's sprint tab, each row carrying the line
// the reading wrote about why that item is in the set, and under them the
// candidates it left out with the word for each.
//
// The card is the half of the sprint that is this surface's. The view
// `/todo sprint` prints is a report, rendered where every other textual
// answer to a backlog command is rendered and pinned by that package's own
// tests; what is captured here is what a report cannot be — a proposal on
// the tab it is about, at the two widths it lays itself out across.
func TestGolden_TodoSprint(t *testing.T) {
	captureGolden(t, "todo-sprint", "the sprint plan card", []int{80, 110}, func(width int) []golden.Panel {
		root := t.TempDir()
		dir := todo.Dir(root)
		if err := os.MkdirAll(filepath.Join(dir, todo.DoneSubdir), 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range map[string]string{
			"cache-ttl.md":        "---\ntitle: Give the cache a lifetime\npriority: high\nsize: S\n---\n",
			"cache-invalidate.md": "---\ntitle: Invalidate on write\npriority: high\nsize: M\n---\n",
			"cache-metrics.md":    "---\ntitle: Count the hits and the misses\npriority: medium\nsize: S\ndepends_on: [cache-ttl]\n---\n",
			"cache-warm.md":       "---\ntitle: Warm the cache on start\npriority: low\nsize: M\n---\n",
			"cache-audit.md":      "---\ntitle: An audit trail for every eviction\npriority: low\nsize: L\n---\n",
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		m := frameModel(t, width, 40)
		m = m.WithTodos(Todos{Profile: todo.BuiltinCode(), Root: root, Manage: func([]string) string { return "" },
			Detail: func(*todo.Store, todo.Item) string { return "" }})
		// The card is opened from a reading, because that is the only way a
		// card comes up: the answer below is a planning turn's, read by the
		// same parser the session reads one with.
		answer := "goal: Make the cache expire what it should and say what it did.\n" +
			"release: minor\n" +
			"item: cache-ttl\n" +
			"why: nothing else can be measured until entries expire on a clock somebody set\n" +
			"item: cache-invalidate\n" +
			"why: the same package, and it is the other half of what makes a stale entry impossible\n" +
			"out: cache-warm unrelated\n" +
			"out: cache-audit too big\n"
		planned := func() Model {
			p := m
			p.todoPlanner = todoPlanState{going: true, candidates: p.todoStore.Ready()}
			card, _ := p.openPlanCard(todo.ParsePlan(p.todos.Profile, answer, p.todoStore.Ready(), nil))
			return card.(Model)
		}
		// Each panel plans again: the card is a pointer the screen holds, so
		// a key pressed for one panel would otherwise be pressed for the
		// panels already captured above it.
		foldedView := planned().backlogPane(width, 24)
		open, _ := planned().updateTodoScreen(key('o'))
		openView := open.(Model).backlogPane(width, 24)
		dropped, _ := planned().updateTodoScreen(key('j'))
		dropped, _ = dropped.(Model).updateTodoScreen(key(' '))
		return []golden.Panel{
			{Label: "the set · a line per item, and what was left out folded under it",
				View: foldedView},
			{Label: "what was left out · the word beside each candidate the reading did not take",
				View: openView},
			{Label: "a row dropped · it keeps its place and loses its tick",
				View: dropped.(Model).backlogPane(width, 24)},
		}
	})
}

// TestGolden_MultiEditCard pins the card a call that changes three places in
// one file puts up. The point of the capture is what is not on it: one
// headline, one diff and one set of keys, where the same three changes as
// three calls would have cost three cards and three answers. One width — the
// card's own layout is captured across the four in the component catalog, and
// what this sheet is about is the diff behind a single decision.
func TestGolden_MultiEditCard(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "loop.go")
	const source = "package agent\n\n" +
		"const maxRounds = 40\n\n" +
		"func (a *Agent) Run() error {\n" +
		"\tfor a.round < maxRounds {\n" +
		"\t\ta.round++\n" +
		"\t}\n" +
		"\treturn nil\n" +
		"}\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]any{
		"path": path,
		"edits": []map[string]string{
			{"old_text": "const maxRounds = 40", "new_text": "const maxRounds = 64"},
			{"old_text": "func (a *Agent) Run() error {", "new_text": "func (a *Agent) Run(ctx context.Context) error {"},
			{"old_text": "\t\ta.round++", "new_text": "\t\ta.round++\n\t\ta.checkIn()"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	captureGolden(t, "multi-edit-card", "three edits in one file, one decision", goldenWidths, func(width int) []golden.Panel {
		msgs := []provider.Message{
			{Role: provider.RoleSystem, Content: "sys"},
			{Role: provider.RoleUser, Content: "raise the round cap and check in on the way past"},
		}
		// A terminal tall enough that the card's own bound does not clip the
		// third hunk: the sheet is about three changes arriving as one diff,
		// and a capture that hides one of them shows nothing.
		m := New(msgs, mockStream).WithWorkspace(dir)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 64})
		m = updated.(Model)
		m.state = stateStreaming
		updated, _ = m.Update(toolCallsMsg{calls: []provider.ToolCall{
			{ID: "call_e", Name: "edit_file", Arguments: string(args)},
		}})
		m = updated.(Model)
		if m.pendingApproval == nil {
			t.Fatal("the three-edit call should arm one decision")
		}
		// The diff, the hunks and the card are the real ones; only the name
		// on them is swapped, because the fixture lives at a temporary path
		// that would be a different string in the golden on every machine.
		m.pendingApproval.path = filepath.Join("internal", "agent", "loop.go")
		m.pendingApproval.title = m.pendingApproval.verb + " " + m.pendingApproval.path
		// The severity's reading names that path too, and it was taken when
		// the decision was armed — before the swap.
		m.pendingBlast.reason = editReason(m.pendingApproval.path)
		return []golden.Panel{
			{Label: "one card · three places in one file", View: strings.Join(m.confirmLines(), "\n")},
		}
	})
}

// TestGolden_OnCloseGate captures how a turn that checked itself closes: the
// gate row the run left, and the close block reading its verdict off that row
// rather than off anything the run kept to itself.
//
// Both verdicts are captured because the failing one is the whole point of
// the mechanism — a turn never closes with a hidden failure — and because the
// two rows differ in more than a colour: the failing check contributes its
// output excerpt, which is what makes the block a different height.
func TestGolden_OnCloseGate(t *testing.T) {
	captureGolden(t, "on-close-gate", "the close of a turn that ran its own checks", []int{60, 80, 110}, func(width int) []golden.Panel {
		rows := func(res *quality.Result) string {
			m := frameModel(t, width, 40)
			m.appendCloseGateRow(res.Suite, res.Format(res.Fingerprint))
			m.appendEntry(entry{kind: entryTurnClose, turn: 1, close: &components.TurnClose{
				State: components.TurnDone, Steps: 2, Tools: 5,
				Elapsed: "41.3s", Spend: "$0.12", Note: "round 4/25",
				Changes: &components.TurnChanges{
					Files: 2, Added: 31, Removed: 7,
					Keys: []components.TurnKey{{Key: "[v]", Label: "review"}, {Key: "[u]", Label: "undo turn"}},
					Note: "all tracked in git",
				},
				// Read off the row above, the way the live close reads it.
				Checks: turnChecksRow(m.transcript, false),
			}})
			m.invalidateRenderCache()
			return m.renderHistory()
		}
		fp := quality.Fingerprint{}
		contained := "landlock (workspace read-only)"
		passed := &quality.Result{
			Suite: "fast", Verdict: quality.VerdictPass, Trusted: true,
			Contained: contained, Fingerprint: fp, Duration: 1700 * time.Millisecond,
			Checks: []quality.CheckResult{
				{Name: "vet", Command: "go vet ./...", Duration: 1300 * time.Millisecond},
				{Name: "docs", Command: "python3 scripts/check-docs.py", Duration: 400 * time.Millisecond},
			},
		}
		failed := &quality.Result{
			Suite: "fast", Verdict: quality.VerdictFail, Trusted: true,
			Contained: contained, Fingerprint: fp, Duration: 2100 * time.Millisecond,
			Checks: []quality.CheckResult{
				{Name: "vet", Command: "go vet ./...", ExitCode: 1, Duration: 1600 * time.Millisecond,
					Output: "internal/agent/loop.go:214:2: declared and not used: rounds"},
				{Name: "docs", Command: "python3 scripts/check-docs.py", Duration: 400 * time.Millisecond},
			},
		}
		return []golden.Panel{
			{Label: "the suite passed, and the turn closes on it", View: rows(passed)},
			{Label: "the suite failed after its last hand-back", View: rows(failed)},
		}
	})
}

// goldenRunItem is the item every run-row capture is a run of.
func goldenRunItem(size string) todo.Item {
	return todo.Item{
		Slug: "cache-ttl", Title: "Give the cache a lifetime",
		Priority: todo.PriorityHigh, Profile: todo.BuiltinCode(),
		Fields: map[string]string{"kind": "story", "size": size},
		Body:   "## Tests\n- go test ./internal/cache\n",
	}
}

// goldenRunPlan is a research answer at a size: the shape the runner parses,
// with the numbered plan the row draws as the research stage's answer.
func goldenRunPlan(size, questions string) string {
	if questions == "" {
		questions = "none"
	}
	return "## Plan\n\n1. Read the cache's own tests\n   files: internal/cache/cache_test.go\n\n" +
		"2. Give an entry a deadline\n   files: internal/cache/cache.go\n\n" +
		"size: " + size + "\nquestions: " + questions + "\n"
}

// goldenRun starts a run with the clock pinned, so the duration field is the
// same string on every machine, and hands it back with its item.
func goldenRun(size string) (todo.Item, *run.State) {
	it := goldenRunItem(size)
	st := run.Start(it, "2026-09-04 10:00:00", "manual", 1, run.Options{Repo: true})
	st.Started = time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	return it, st
}

// goldenRunRow renders a row after driving its run through build, which is
// the real machine every time: a fixture that set the stages by hand would
// capture a row nothing can produce.
func goldenRunRow(t *testing.T, width int, expanded bool, build func(it todo.Item, st *run.State, r *todoRunRow)) string {
	t.Helper()
	it, st := goldenRun("M")
	r := newTodoRunRow(st)
	build(it, st, r)
	// Pinned last: the machine stamps Updated on every save, and the row's
	// span is measured between the two ends the checkpoint carries.
	st.Updated = st.Started.Add(4*time.Minute + 12*time.Second)
	m := frameModel(t, width, 40)
	m.appendEntry(entry{kind: entryTodoRun, todorun: r, expanded: expanded})
	m.invalidateRenderCache()
	return m.renderHistory()
}

// drive runs the machine and tells the row about every step, which is what
// the session does on every transition.
func driveRun(r *todoRunRow, steps ...run.Step) {
	for _, step := range steps {
		r.observe(step)
	}
}

// TestGolden_TodoRunRow captures the run the transcript draws in place of the
// scatter of notices a run used to be: a small one researching and then
// building, a medium one spending a remediation round, one reviewed,
// committed and opened to its answers, a large one built in three lanes, the
// pause, the block with the follow-up it wrote — and a run picked up from a
// checkpoint, which is the one that must not draw a tick on a stage it never
// watched. A run that remediated and then finished is captured too: the
// rounds it spent stay on the row after the stage is ticked, and what that
// reads like beside an all-green strip is the point of the sheet.
func TestGolden_TodoRunRow(t *testing.T) {
	captureGolden(t, "todo-run-row", "a backlog run drawn as it goes", goldenWidths, func(width int) []golden.Panel {
		return []golden.Panel{
			{Label: "a small run, researching", View: goldenRunRow(t, width, false,
				func(it todo.Item, st *run.State, r *todoRunRow) {
					driveRun(r, st.First(it, ""))
				})},
			{Label: "the same run building what research planned", View: goldenRunRow(t, width, false,
				func(it todo.Item, st *run.State, r *todoRunRow) {
					driveRun(r, st.First(it, ""), st.Observe(it, goldenRunPlan("S", "")))
				})},
			{Label: "a medium run spending a remediation round on a failed verify", View: goldenRunRow(t, width, false,
				func(it todo.Item, st *run.State, r *todoRunRow) {
					driveRun(r, st.First(it, ""), st.Observe(it, goldenRunPlan("M", "")),
						st.Observe(it, "Gave an entry a deadline."),
						st.VerifyResult(it, false, "--- FAIL: TestExpiry (0.01s)"))
				})},
			{Label: "reviewed, committed and done, opened to its answers", View: goldenRunRow(t, width, true,
				func(it todo.Item, st *run.State, r *todoRunRow) {
					driveRun(r, st.First(it, ""), st.Observe(it, goldenRunPlan("M", "")),
						st.Observe(it, "Gave an entry a deadline."),
						st.VerifyResult(it, true, "ok  internal/cache  0.4s"),
						st.ReviewResult(it, "verdict: clean"),
						st.Observe(it, "COMMIT:\nfeat(cache): give an entry a deadline\nREPORT:\nSummary: entries now expire.\n"),
						st.Committed([]string{"internal/cache/cache.go", "internal/cache/cache_test.go"}))
				})},
			{Label: "the same run after the round it spent · what it cost stays on the row", View: goldenRunRow(t, width, false,
				func(it todo.Item, st *run.State, r *todoRunRow) {
					driveRun(r, st.First(it, ""), st.Observe(it, goldenRunPlan("M", "")),
						st.Observe(it, "Gave an entry a deadline."),
						st.VerifyResult(it, false, "--- FAIL: TestExpiry (0.01s)"),
						st.Observe(it, "Fixed the expiry."),
						st.VerifyResult(it, true, "ok  internal/cache  0.4s"),
						st.ReviewResult(it, "verdict: clean"),
						st.Observe(it, "COMMIT:\nfeat(cache): give an entry a deadline\nREPORT:\nSummary: entries now expire.\n"),
						st.Committed([]string{"internal/cache/cache.go"}))
				})},
			{Label: "a large run in three lanes, one landed", View: goldenRunRow(t, width, false,
				func(it todo.Item, st *run.State, r *todoRunRow) {
					driveRun(r, st.First(it, ""), st.Observe(it, goldenRunPlan("L", "")))
					// The pause a large item always takes, then the split's
					// own answer, which is what names the lanes.
					driveRun(r, st.Resume(it), st.Observe(it, goldenLanes))
					st.LanePatched(st.Lanes[0].Agent)
				})},
			{Label: "paused on a question research could not settle", View: goldenRunRow(t, width, false,
				func(it todo.Item, st *run.State, r *todoRunRow) {
					driveRun(r, st.First(it, ""),
						st.Observe(it, goldenRunPlan("M", "\n- should a stale read serve or block?")))
				})},
			{Label: "blocked, with the follow-up it wrote and the key that reopens it", View: goldenRunRow(t, width, false,
				func(it todo.Item, st *run.State, r *todoRunRow) {
					driveRun(r, st.First(it, ""), st.Observe(it, "I had a look but there is no plan here.\n"))
					r.followUp = "cache-ttl-plan"
				})},
			{Label: "picked up from a checkpoint · the stages it skipped are restored, not passed",
				View: goldenRunRow(t, width, false, func(it todo.Item, st *run.State, r *todoRunRow) {
					st.Stage, st.Plan = run.StageVerify, goldenRunPlan("M", "")
					st.Steps = []string{"Read the cache's own tests", "Give an entry a deadline"}
					// The row is opened on the checkpoint, which is what the
					// session does before it continues a run.
					*r = *newTodoRunRow(st)
					driveRun(r, st.Continue(it))
				})},
		}
	})
}

// goldenLanes is a split answer in the shape the runner parses: three lanes
// with disjoint paths.
const goldenLanes = "LANE: store\npaths: internal/cache/cache.go\ntask: give an entry a deadline\n\n" +
	"LANE: tests\npaths: internal/cache/cache_test.go\ntask: cover the expiry\n\n" +
	"LANE: bench\npaths: internal/cache/bench_test.go\ntask: measure the eviction\n"

// TestGolden_TodoGroom pins the card a reading of one item against the tree
// leaves. What the sheet is for is the row: a diff of one line, the text it
// replaces struck through beside it, the evidence dim behind that, and the
// verdict right-aligned — one row per correction, because the unit being
// decided on here is a line and not a hunk. Every verdict that proposes an
// edit is on it, plus the header's own stamp as the last row, which is what
// makes an accepted reading one accepted line rather than a side effect.
//
// Two widths: the row's three fields are what give ground as the card
// narrows, and 80 is where the evidence starts to go.
func TestGolden_TodoGroom(t *testing.T) {
	captureGolden(t, "todo-groom", "the grooming card", []int{80, 130}, func(width int) []golden.Panel {
		root := t.TempDir()
		dir := todo.Dir(root)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		const item = "---\ntitle: Give the cache a lifetime\npriority: high\nsize: M\ndepends_on: [cache-store]\n---\n\n" +
			"## Acceptance criteria\n" +
			"- [ ] internal/cache/store.go:88 takes the lifetime from the config\n" +
			"- [ ] The reader drops an entry past its age\n" +
			"- [ ] A hit past the deadline counts as a miss\n\n" +
			"## Notes\nToday the reader serves a stale entry rather than refusing.\n"
		path := filepath.Join(dir, "cache-ttl.md")
		if err := os.WriteFile(path, []byte(item), 0o644); err != nil {
			t.Fatal(err)
		}
		it, err := todo.LoadFile(todo.BuiltinCode(), path)
		if err != nil {
			t.Fatal(err)
		}
		const answer = "claim: - [ ] internal/cache/store.go:88 takes the lifetime from the config\n" +
			"verdict: moved\n" +
			"now: - [ ] internal/cache/reader.go:120 takes the lifetime from the config\n" +
			"evidence: the constructor moved to reader.go in 9f2a11c\n\n" +
			"claim: Today the reader serves a stale entry rather than refusing.\n" +
			"verdict: changed\n" +
			"now: Today the reader refuses a stale entry.\n" +
			"evidence: reader.go:52 returns ErrStale\n\n" +
			"claim: - [ ] The reader drops an entry past its age\n" +
			"verdict: already done\n" +
			"now: - [x] The reader drops an entry past its age (2f9c0aa)\n" +
			"evidence: reader.go:44 checks the age, added in 2f9c0aa\n\n" +
			"claim: depends_on: [cache-store]\n" +
			"verdict: gone\n" +
			"evidence: cache-store is in neither the backlog nor its archive\n\n" +
			"claim: - [ ] A hit past the deadline counts as a miss\n" +
			"verdict: holds\n" +
			"evidence: nothing in the tree counts one either way yet\n\n" +
			"claim: size: M\n" +
			"verdict: unknown\n" +
			"evidence: the config reader is generated and this checkout does not build it\n"
		r, err := todo.Groom(it, answer)
		if err != nil {
			t.Fatal(err)
		}
		r.Head, r.Read = "1a2b3c4d5e6f", time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
		m := frameModel(t, width, 40)
		m = m.WithTodos(Todos{Profile: todo.BuiltinCode(), Root: root, Manage: func([]string) string { return "" },
			Detail: func(*todo.Store, todo.Item) string { return "" }})
		m.todoGroomer.item = it
		card, _ := m.openTodoGroomCard(r)
		return []golden.Panel{
			{Label: "four corrections and the stamp · moved, changed, already done, gone",
				View: card.(Model).panelView()},
		}
	})
}

// TestGolden_ChatTodo captures the backlog where it had not been drawn: a
// conversation. The two panels are the two places `/todo` shows up before it
// is typed — the completion menu, which is the answer to "what can I ask for
// here", and the rail's block, which is the answer to "what is on the list"
// — because the disagreement this fixes was between exactly those and the
// session's answer when the command was typed anyway.
//
// Two widths: the menu's description column and the rail's rows are what
// give ground as the surface narrows.
func TestGolden_ChatTodo(t *testing.T) {
	captureGolden(t, "chat-todo", "the backlog in a conversation", []int{80, 110}, func(width int) []golden.Panel {
		root := t.TempDir()
		dir := todo.Dir(root)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range map[string]string{
			"cache-ttl.md":     "---\ntitle: Give the cache a lifetime\npriority: high\nsize: S\n---\n",
			"cache-metrics.md": "---\ntitle: Count the hits and the misses\npriority: medium\nsize: M\n---\n",
			"cache-warm.md":    "---\ntitle: Warm the cache on start\npriority: low\nsize: M\nstatus: blocked\n---\n",
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		m := frameModel(t, width, 40).WithConversation()
		m = m.WithTodos(Todos{Profile: todo.BuiltinCode(), Root: root,
			Manage: func([]string) string { return "" },
			Detail: func(*todo.Store, todo.Item) string { return "" }})
		return []golden.Panel{
			{Label: "the completion offers /todo in a conversation",
				View: strings.Join(typeChars(t, m, "/todo").completionMenuLines(), "\n")},
			{Label: "the backlog block on the inspector rail",
				View: strings.Join(m.inspectorData().Lines(components.InspectorWidth, 0), "\n")},
		}
	})
}

// TestGolden_NotebookRows captures the two places a shared notebook reaches
// the screen: the line a turn closes with when its children wrote something
// down, and /notes, which is where the person reads and corrects a store the
// agents write to without asking.
//
// One width. The close line is a clause and the listing is prose the
// transcript wraps like any other; neither has a layout that changes with
// the terminal, so three more captures would be copies of this one.
func TestGolden_NotebookRows(t *testing.T) {
	captureGolden(t, "notebook-rows", "the notebook on the screen", []int{80}, func(width int) []golden.Panel {
		nb := notebook.New(nil)
		nb.SetTurn(4)
		_, _, _ = nb.Write(notebook.Orchestrator, "The freeze is the target",
			"From here the work is making what exists better, not wider.")
		_, _, _ = nb.Write("reviewer-1", "The deny list is read before the tier",
			"policy.Decide matches on the command, so an entry refuses the verb in every mode.")
		_, _, _ = nb.Write("researcher-1", "Where the goldens live",
			"internal/ui/chat/testdata/golden, one file per width and one per palette.")

		closeBlock := func() string {
			m := frameModel(t, width, 40)
			m.transcript = []entry{{kind: entryTurnClose, turn: 4, close: &components.TurnClose{
				State: components.TurnDone, Steps: 2, Tools: 11, Elapsed: "1m 12s", Spend: "$0.21",
				Notes: "2 notes from reviewer-1, researcher-1",
			}}}
			m.invalidateRenderCache()
			return m.renderHistory()
		}
		listing := func() string {
			m := frameModel(t, width, 40).WithNotebook(nb)
			m.appendEntry(entry{kind: entrySystem, text: m.notesCommand(nil)})
			return m.renderHistory()
		}
		return []golden.Panel{
			{Label: "the turn's close · what the fan-out wrote down", View: closeBlock()},
			{Label: "/notes · the notebook by the agent that wrote each entry", View: listing()},
		}
	})
}

// TestGolden_ChildAskCard pins the routed card with the real resolver behind
// it: the command's paths stat-ed in the child's own directory, the
// containment the session is running under, and a writer's finished patch —
// the one child request that writes the reader's own files, and the card that
// used to be a title, a diff and two keys.
//
// The held panel is the pair the last one exists for. A card that took the
// keyboard by arriving answers two keys and advertises two keys; [g] and the
// manager's chord belong to the draft in that state, and drawing them would
// be offering a key that puts a letter in the sentence.
func TestGolden_ChildAskCard(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"shhh", "shhh.test"} {
		if err := os.WriteFile(filepath.Join(dir, "build", name), []byte("binary\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sup := subagent.New(context.Background(), subagent.Options{Root: dir, NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)

	captureGolden(t, "child-ask-card", "a child agent's routed approval", goldenWidths, func(width int) []golden.Panel {
		build := func(ask *subagent.Ask, hold bool) string {
			m := frameModel(t, width, 40)
			m = m.WithSubagents(sup).WithChangeset(changeset.New(64), nil).WithContainment(Containment{
				Status: "bwrap · workspace", Mechanism: "bwrap", Profile: "workspace",
			})
			updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventAsk, Ask: ask}})
			m = updated.(Model)
			if hold {
				m = handover(t, m)
			}
			return strings.Join(m.childAskLines(ask), "\n")
		}
		command := func() *subagent.Ask {
			ask := subagent.NewAsk("writer-1", subagent.AskCommand, "run rm -rf build")
			ask.Command = "rm -rf build"
			ask.Root, ask.Worktree = dir, true
			return ask
		}
		return []golden.Panel{
			{Label: "a child's command · resolved in the agent's own checkout", View: build(command(), true)},
			{Label: "the same card, held by arriving · two answers, and nothing else offered",
				View: build(command(), false)},
			{Label: "a writer's patch · your files, and the diff behind a counted tail",
				View: build(longPatchAsk(dir), true)},
		}
	})
}

// TestGolden_QuestionCard captures the four dressings a question is asked in
// (docs/interface/surfaces.md#the-question-card): the pick-one list with its
// recommendation, its short fields and the row that cannot be taken; the same
// card with the keyboard in the note; the pick-several list; the free answer,
// which is the note field with no rows above it; and the yes-or-no, which is
// the inline confirm.
//
// The pick-one panel is where the two words this card added show up: the
// `recommended` beside the model's own short field, and the ⊘ row whose
// reason is a phrase rather than a dimming — both of which have to survive
// the mono capture, because that is the whole reason they are words.
func TestGolden_QuestionCard(t *testing.T) {
	captureGolden(t, "question-card", "the model's question in the panel", goldenWidths, func(width int) []golden.Panel {
		build := func(args string, mut func(Model) Model) string {
			m := frameModel(t, width, 40).WithAsk()
			m.state = stateStreaming
			updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{{
				ID: "call_q", Name: ask.ToolName, Arguments: args,
			}}})
			next := updated.(Model)
			if mut != nil {
				next = mut(next)
			}
			return strings.Join(next.questionLines(), "\n")
		}
		const choose = `{"question":"Which store should the cache use?","shape":"choose","options":[
			{"label":"SQLite","detail":"in the checkout already","field":"3 files","recommended":true},
			{"label":"Postgres","detail":"one more service to run","field":"9 files"},
			{"label":"Redis","unavailable":"no client in this project"}]}`
		return []golden.Panel{
			{Label: "pick one · the recommendation leads and says so, and the ⊘ row says why not",
				View: build(choose, nil)},
			{Label: "the note has the keyboard · the list's digits are text until tab hands it back",
				View: build(choose, func(m Model) Model {
					m.question.openNote()
					return m
				})},
			{Label: "pick several · the boxes, with the same field under them",
				View: build(`{"question":"Which packages should the flag reach?","shape":"choose_many","options":[
					{"label":"internal/agent"},{"label":"internal/cli"},{"label":"internal/ui/chat"}]}`, nil)},
			{Label: "a short answer in your own words · the field and no rows above it",
				View: build(`{"question":"What should the flag be called?","shape":"text","note":"required"}`, nil)},
			{Label: "yes or no · the inline confirm, whose enter is the answer that changes nothing",
				View: build(`{"question":"Should the migration be reversible?","shape":"confirm"}`, nil)},
		}
	})
}

// TestGolden_QuestionTabs captures a call that asked several questions at once
// (docs/interface/surfaces.md#the-question-card): the strip with a tab
// answered and a tab not, the free answer whose field is shut until the note
// key opens it, and the tab that ends the set.
//
// It is pinned in both palettes because the whole of what the strip says has
// to survive a terminal with one colour: the marks say which tab is done and
// which has the keyboard, and the tail says the same thing in words along with
// what leaving a tab open would cost. Four questions is the most a call may
// carry (ask.MaxQuestions), which is the width the strip is tightest at.
func TestGolden_QuestionTabs(t *testing.T) {
	captureGolden(t, "question-tabs", "several questions on one card", goldenWidths, func(width int) []golden.Panel {
		build := func(args string, steps ...tea.KeyPressMsg) string {
			m := frameModel(t, width, 40).WithAsk()
			m.state = stateStreaming
			updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{{
				ID: "call_q", Name: ask.ToolName, Arguments: args,
			}}})
			next := updated.(Model)
			for _, s := range steps {
				again, _ := next.Update(s)
				next = again.(Model)
			}
			return strings.Join(next.questionLines(), "\n")
		}
		const three = `{"questions":[
			{"question":"Which store should the cache use?","shape":"choose","options":[
				{"label":"SQLite","detail":"in the checkout already","field":"3 files","recommended":true},
				{"label":"Postgres","detail":"one more service to run","field":"9 files"}]},
			{"question":"Should the migration be reversible?","shape":"confirm"},
			{"question":"What should the flag be called?","shape":"text"}]}`
		const four = `{"questions":[
			{"question":"Which store should the cache use?","shape":"choose","options":[
				{"label":"SQLite","recommended":true},{"label":"Postgres"}]},
			{"question":"Should the migration be reversible?","shape":"confirm"},
			{"question":"Which packages should the flag reach?","shape":"choose_many","options":[
				{"label":"internal/agent"},{"label":"internal/cli"}]},
			{"question":"What should the flag be called?","shape":"text"}]}`
		enter := tea.KeyPressMsg{Code: tea.KeyEnter}
		right := tea.KeyPressMsg{Code: tea.KeyRight}
		return []golden.Panel{
			{Label: "three questions · the first tab has the keyboard and none is answered yet",
				View: build(three)},
			{Label: "one answered · the mark turns and the tail counts what is still open",
				View: build(three, enter)},
			{Label: "the free answer · its field is shut so the arrows stay the strip's",
				View: build(three, enter, tea.KeyPressMsg{Code: 'y', Text: "y"})},
			{Label: "the tab that ends the set · what enter sends, and what leaving a tab open costs",
				View: build(three, enter, tea.KeyPressMsg{Code: 'y', Text: "y"}, right)},
			{Label: "four questions · the most one call may carry, which is where the strip is tightest",
				View: build(four, enter, right)},
		}
	})
}

// TestGolden_FoldedRows captures the notice rail after esc folded the rows
// the reader had opened, and after a press that found only the verbosity's
// (readinghint.go). The two are pinned side by side because they are the
// same key on the same empty draft answering two different panes, and the
// rail is the only thing that says which of the two just happened.
func TestGolden_FoldedRows(t *testing.T) {
	captureGolden(t, "folded-rows", "the notice rail after a fold", goldenWidths, func(width int) []golden.Panel {
		build := func(open bool) string {
			m := frameModel(t, width, 40)
			m.transcript = goldenTranscript()
			if open {
				m.transcript[1].stepFold = foldOpen
				m.transcript[2].expanded = true
				m.transcript[7].expanded = true
				m.transcript[8].expanded = true
			} else {
				m.verbosity = verbosityHigh
			}
			m.invalidateRenderCache()
			m.refreshTranscript()
			next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			return promptSurface(next.(Model))
		}
		return []golden.Panel{
			{Label: "esc · four rows folded back to their resting form", View: build(true)},
			{Label: "esc · nothing of yours was open, so the setting is named", View: build(false)},
		}
	})
}
