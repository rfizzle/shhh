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
	"github.com/charmbracelet/colorprofile"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/persona"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/golden"
	"github.com/rfizzle/shhh/internal/ui/keys"
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
			toolResult: "x\ny", duration: 300 * time.Millisecond},
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

// TestGolden_PromptFrame captures the command-center surface in
// each of its four layout modes. frameWidths adds a terminal too narrow for
// the frame at all, which the four breakpoints do not reach: below
// minFrameWidth content columns the frame degrades to the bare input, and
// that degradation is worth capturing too.
func TestGolden_PromptFrame(t *testing.T) {
	frameWidths := append([]int{14}, goldenWidths...)
	captureGolden(t, "prompt-frame", "prompt frame", frameWidths, func(width int) []golden.Panel {
		idle := goldenModel(t, width)
		working := goldenModel(t, width)
		working.state = stateStreaming
		return []golden.Panel{
			{Label: "state · idle", View: promptSurface(idle)},
			{Label: "state · working", View: promptSurface(working)},
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
			m.steering = []string{"and check the parser"}
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
				m.steering = []string{"and check the parser"}
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
	return m.input.View()
}

// promptCapture is promptSurface with the cursor the surface placed inside
// it, in the render's own cells.
func promptCapture(m Model) (string, *golden.Cursor) {
	if !m.frameShowing() {
		// The bare input is its own render, so its cursor needs no offset.
		return m.input.View(), goldenCursor(m.input.Cursor())
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
	for _, width := range append([]int{14}, goldenWidths...) {
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
			{Label: "typing dismissed the list · the facts stay", View: typed()},
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
		// Numbered read rows, so a reader checking the thumb against the pane
		// can see which slice of the whole is showing without counting. They
		// are activity rows rather than prose because the subject here is one
		// column, and a markdown fixture would bury it under glamour's own
		// escapes in the ansi block.
		reads := func(n int) []entry {
			es := []entry{{kind: entryUser, text: "read the round accounting"}}
			for i := 1; i <= n; i++ {
				es = append(es, entry{kind: entryTool, toolName: "read_file",
					toolArgs:   fmt.Sprintf(`{"path":"internal/agent/round%02d.go"}`, i),
					toolResult: "a\nb", duration: 200 * time.Millisecond})
			}
			return es
		}
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
		long := reads(24)
		return []golden.Panel{
			{Label: "nothing to scroll · the column is reserved and empty",
				View: gutter(reads(2), func(m *Model) {})},
			{Label: "the live end · the thumb is on the last row",
				View: gutter(long, func(m *Model) {})},
			{Label: "scrolled halfway up",
				View: gutter(long, func(m *Model) { m.viewport.SetYOffset(m.viewport.TotalLineCount() / 2) })},
			{Label: "the top of the transcript · the thumb is on the first row",
				View: gutter(long, func(m *Model) { m.viewport.GotoTop() })},
		}
	})
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
	captureGolden(t, "syntax-register", "the diff body's syntax register", []int{80, 130}, func(width int) []golden.Panel {
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

// TestGolden_ReadingMode captures the surface the keyboard moves to (
// at the two widths where the artboard's rules bite: 130, where the
// labelled rail, the lit row and the two-line hint bar all have room, and 80,
// where the position field narrows. It is the pair that matters — the same
// screen with the keyboard in the other pane is captured beside it, because
// "only one pane is dressed" is a thing a reader checks by looking at both.
func TestGolden_ReadingMode(t *testing.T) {
	captureGolden(t, "reading-mode", "reading mode", []int{80, 130}, func(width int) []golden.Panel {
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
// the two widths reading mode is captured at: the query row where the mode's
// key bar was, with the pane marking what the query found, and the same
// search kept — the row closed, the pointer's occurrence reversed among the
// underlined ones, and the pair that walks them on the bar.
//
// It is the pane rather than the rendered transcript, because the marks are
// painted on the window and not on the lines the render produced, and the
// rail is above it because the count the reader steps by is up there.
func TestGolden_TranscriptSearch(t *testing.T) {
	captureGolden(t, "transcript-search", "the transcript search", []int{80, 130}, func(width int) []golden.Panel {
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
		return []golden.Panel{
			{Label: "the query row where the key bar was · every hit underlined", View: search(false)},
			{Label: "kept · the pointer reversed, [n/N] on the bar", View: search(true)},
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
		working.palette.query = "cl"
		working.refreshPalette()

		return []golden.Panel{
			{Label: "nothing typed yet", View: strings.Join(idle.pickerLines(), "\n")},
			{Label: "mid-turn, filtered to an idle-only command", View: strings.Join(working.pickerLines(), "\n")},
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
				m.summary.last.Round, m.summary.lastRound = 128, 128
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

// screenWidths adds two terminals wide enough to split to the four
// breakpoints. 144 columns is 140 content columns, just past the
// InspectorMinContentWidth rung, so the whole-screen capture carries the
// two-pane arrangement as well as the single-pane one; 200 is the wide
// terminal the rail grows on, and the pair is what shows that the growth goes
// to the rail's blocks rather than to the gap beside them.
var screenWidths = append(append([]int{}, goldenWidths...), 144, 200)

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
				m.summary.lastRound = 24
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
func TestGolden_ScreenAttached(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	spawnChild(t, sup, subagent.RoleReviewer, "reviewer-1")
	killChild(t, sup, "reviewer-1")
	captureGolden(t, "screen-attached", "the surface with the keyboard in a child",
		[]int{144}, func(width int) []golden.Panel {
			build := func(name string) string {
				m := frameModel(t, width, screenHeight)
				m.transcript = goldenTranscript()
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
// line a call the model simply malformed still gets.
func TestGolden_StaleEditRow(t *testing.T) {
	captureGolden(t, "stale-edit-row", "the refused stale edit", []int{80}, func(width int) []golden.Panel {
		build := func(open bool) string {
			m := frameModel(t, width, 40)
			m = m.WithWorkspace("/work/shhh")
			stale := m.skippedCallEntry(fmt.Errorf("invalid arguments: %w",
				tools.StaleError{Path: "/work/shhh/internal/agent/loop.go"}))
			stale.expanded = open
			m.transcript = []entry{
				{kind: entryUser, text: "rebase the round cap on what loop.go says now"},
				stale,
				m.skippedCallEntry(errors.New("invalid arguments: path is required")),
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
			Size: todo.SizeS, Priority: todo.PriorityHigh,
			Path: ".shhh/todo/cache-ttl.md",
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
// Two widths, at the breakpoints either side of the row. A notice is emitted
// as the sentence it is and the pane never re-wraps it, so the captures agree
// — which is the property being pinned: the one thing the boundary says is a
// sentence that reads the same in a narrow terminal as in a wide one, and a
// row that grew a layout would show up here as the pair disagreeing.
func TestGolden_NewSessionRow(t *testing.T) {
	captureGolden(t, "new-session-row", "the row a new session opens on", []int{80, 110}, func(width int) []golden.Panel {
		row := func(text string) string {
			m := frameModel(t, width, 40)
			m.appendEntry(entry{kind: entrySystem, text: text})
			return m.renderHistory()
		}
		it := todo.Item{
			Slug: "cache-ttl", Title: "Give the cache a lifetime",
			Size: todo.SizeS, Priority: todo.PriorityHigh,
			Path: ".shhh/todo/cache-ttl.md",
		}
		st := run.Start(it, "amber-lake", "manual", 1, run.Options{})
		st.Stage = run.StageImplement
		const slot, resume = "2026-09-04 11:20:07", "shhh code --continue"
		return []golden.Panel{
			{Label: "the slot left behind, and the command that reopens it",
				View: row(newSessionRow(slot, resume, ""))},
			{Label: "with a backlog run kept at its checkpoint",
				View: row(newSessionRow(slot, resume, todoRunKeptNote(it, st, "this session ended")))},
		}
	})
}

// TestGolden_ResumedRow captures the row a conversation comes back on: the
// branch it is looking at and how much is changed, folded, and the reading
// the conversation was actually given underneath it.
//
// Two widths either side of the narrow breakpoint, because the body is the
// part that has to survive one. The line is a sentence the pane never
// re-wraps; the body is wrapped rather than clipped, which is what a
// narrow capture pins — a body that clipped would promise a reading and show
// half a sentence.
func TestGolden_ResumedRow(t *testing.T) {
	captureGolden(t, "resumed-row", "the row a resumed conversation opens on", []int{60, 80}, func(width int) []golden.Panel {
		row := func(n ResumeNotice, expanded bool) string {
			m := frameModel(t, width, 40)
			m.appendEntry(entry{kind: entrySystem, text: n.Notice, toolResult: n.Text, expanded: expanded})
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
		m = m.WithTodos(Todos{Root: root, Manage: func([]string) string { return "" },
			Detail: func(*todo.Store, todo.Item) string { return "" }})
		proposals, ok := todo.ParseProposals(draftFixture)
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
		m = m.WithTodos(Todos{Root: root, Manage: func([]string) string { return "" },
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
			card, _ := p.openPlanCard(todo.ParsePlan(answer, p.todoStore.Ready(), nil))
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

	captureGolden(t, "multi-edit-card", "three edits in one file, one decision", []int{80}, func(width int) []golden.Panel {
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
				Checks: turnChecksRow(m.transcript),
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
func goldenRunItem(size todo.Size) todo.Item {
	return todo.Item{
		Slug: "cache-ttl", Title: "Give the cache a lifetime",
		Priority: todo.PriorityHigh, Size: size,
		Body: "## Tests\n- go test ./internal/cache\n",
	}
}

// goldenRunPlan is a research answer at a size: the shape the runner parses,
// with the numbered plan the row draws as the research stage's answer.
func goldenRunPlan(size todo.Size, questions string) string {
	if questions == "" {
		questions = "none"
	}
	return "## Plan\n\n1. Read the cache's own tests\n   files: internal/cache/cache_test.go\n\n" +
		"2. Give an entry a deadline\n   files: internal/cache/cache.go\n\n" +
		"size: " + string(size) + "\nquestions: " + questions + "\n"
}

// goldenRun starts a run with the clock pinned, so the duration field is the
// same string on every machine, and hands it back with its item.
func goldenRun(size todo.Size) (todo.Item, *run.State) {
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
	it, st := goldenRun(todo.SizeM)
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
					driveRun(r, st.First(it, ""), st.Observe(it, goldenRunPlan(todo.SizeS, "")))
				})},
			{Label: "a medium run spending a remediation round on a failed verify", View: goldenRunRow(t, width, false,
				func(it todo.Item, st *run.State, r *todoRunRow) {
					driveRun(r, st.First(it, ""), st.Observe(it, goldenRunPlan(todo.SizeM, "")),
						st.Observe(it, "Gave an entry a deadline."),
						st.VerifyResult(it, false, "--- FAIL: TestExpiry (0.01s)"))
				})},
			{Label: "reviewed, committed and done, opened to its answers", View: goldenRunRow(t, width, true,
				func(it todo.Item, st *run.State, r *todoRunRow) {
					driveRun(r, st.First(it, ""), st.Observe(it, goldenRunPlan(todo.SizeM, "")),
						st.Observe(it, "Gave an entry a deadline."),
						st.VerifyResult(it, true, "ok  internal/cache  0.4s"),
						st.ReviewResult(it, "verdict: clean"),
						st.Observe(it, "COMMIT:\nfeat(cache): give an entry a deadline\nREPORT:\nSummary: entries now expire.\n"),
						st.Committed([]string{"internal/cache/cache.go", "internal/cache/cache_test.go"}))
				})},
			{Label: "the same run after the round it spent · what it cost stays on the row", View: goldenRunRow(t, width, false,
				func(it todo.Item, st *run.State, r *todoRunRow) {
					driveRun(r, st.First(it, ""), st.Observe(it, goldenRunPlan(todo.SizeM, "")),
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
					driveRun(r, st.First(it, ""), st.Observe(it, goldenRunPlan(todo.SizeL, "")))
					// The pause a large item always takes, then the split's
					// own answer, which is what names the lanes.
					driveRun(r, st.Resume(it), st.Observe(it, goldenLanes))
					st.LanePatched(st.Lanes[0].Agent)
				})},
			{Label: "paused on a question research could not settle", View: goldenRunRow(t, width, false,
				func(it todo.Item, st *run.State, r *todoRunRow) {
					driveRun(r, st.First(it, ""),
						st.Observe(it, goldenRunPlan(todo.SizeM, "\n- should a stale read serve or block?")))
				})},
			{Label: "blocked, with the follow-up it wrote and the key that reopens it", View: goldenRunRow(t, width, false,
				func(it todo.Item, st *run.State, r *todoRunRow) {
					driveRun(r, st.First(it, ""), st.Observe(it, "I had a look but there is no plan here.\n"))
					r.followUp = "cache-ttl-plan"
				})},
			{Label: "picked up from a checkpoint · the stages it skipped are restored, not passed",
				View: goldenRunRow(t, width, false, func(it todo.Item, st *run.State, r *todoRunRow) {
					st.Stage, st.Plan = run.StageVerify, goldenRunPlan(todo.SizeM, "")
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
		it, err := todo.LoadFile(path)
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
		m = m.WithTodos(Todos{Root: root, Manage: func([]string) string { return "" },
			Detail: func(*todo.Store, todo.Item) string { return "" }})
		m.todoGroomer.item = it
		card, _ := m.openTodoGroomCard(r)
		return []golden.Panel{
			{Label: "four corrections and the stamp · moved, changed, already done, gone",
				View: card.(Model).panelView()},
		}
	})
}
