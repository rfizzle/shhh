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
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/golden"
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
				golden.Assert(t, name+".w"+strconv.Itoa(width), golden.Case{
					Surface: surface,
					Width:   width,
					Mono:    mono,
					Panels:  panels(width),
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
			{Label: "cancel armed · a second press abandons the turn", View: armed(armCancel, "esc", stateStreaming)},
			{Label: "quit armed · idle", View: armed(armQuit, "ctrl+d", stateInput)},
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
			{Label: "resolved · the summary it becomes", View: frame(func(m *Model) {
				m.state = stateInput
				m.transcript = append(m.transcript, entry{kind: entryTurnClose, turn: 1,
					close: &components.TurnClose{State: components.TurnDone,
						Tools: 18, Elapsed: "1m 04s", Spend: "$0.14"}})
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
			{Label: "typing dismissed the list · the facts stay", View: typed()},
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
		return []golden.Panel{
			{Label: "ungated · the draft still has the keyboard", View: interruptSurface(ungated)},
			{Label: "gated · the handover, and the card has it", View: interruptSurface(gated)},
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
			{Label: "prose · nothing expandable, so no cursor and no position", View: func() string {
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

// readingSurface is the rail, the transcript and the bottom panel together —
// the whole of what says which pane holds the keyboard.
func readingSurface(m Model) string {
	rail := m.readingRail(m.contentWidth())
	body := m.renderHistory()
	if m.state == stateFocus {
		body, _, _ = m.renderFocusHistory()
		return rail + "\n" + body + "\n" + dividerStyle(m.contentWidth()) + "\n" + m.renderFocusHint()
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

// screenWidths adds a terminal wide enough to split to the four
// breakpoints: 144 columns is 140 content columns, past the
// InspectorMinContentWidth rung, so the whole-screen capture carries the
// two-pane arrangement as well as the single-pane one.
var screenWidths = append(append([]int{}, goldenWidths...), 144)

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
			// lead; below 130 columns the same panel is the
			// single-pane surface, which is how the capture shows that
			// nothing was taken from the narrow terminal but the block.
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
