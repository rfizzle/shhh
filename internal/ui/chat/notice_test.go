package chat

import (
	"image"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// TestNotice_IsAFlatLine: what happened to the session rather than in it is
// one dim line at the glyph column, the notice mark in the slot and no band,
// no key and no rail — the tree moving, the window trimmed, a conversation
// reopened, a trust answer. A notice that leads with a mark of its own keeps
// it in that slot rather than standing behind a second one.
func TestNotice_IsAFlatLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    entry
		want string
	}{
		{"the tree moved", entry{kind: entrySystem, text: "tree moved — 14 paths changed outside this session"},
			"  · tree moved — 14 paths changed outside this session"},
		{"the window was trimmed", entry{kind: entrySystem, text: "context trimmed: 3 older tool results elided"},
			"  · context trimmed: 3 older tool results elided"},
		{"a conversation reopened", entry{kind: entrySystem, toolResult: "[resume: branch master]",
			notice: &components.ActivityNotice{Verb: resumeVerb, Subject: "master · 3 changed"}},
			"  · " + resumeVerb + " master · 3 changed"},
		{"a trust answer", entry{kind: entrySystem, text: "trust is not answered from this session; `shhh trust` records it"},
			"  · trust is not answered from this session; `shhh trust` records it"},
		{"a failure keeps its own mark", entry{kind: entrySystem, text: failed("plan", "could not save it")},
			"  ✗ plan  could not save it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := readyModel(t)
			raw := strings.TrimRight(m.renderEntry(tc.e, 110), "\n")
			if got := stripANSI(raw); got != tc.want {
				t.Fatalf("the notice line:\n got %q\nwant %q", got, tc.want)
			}
			if bg := "\x1b[48"; strings.Contains(raw, bg) {
				t.Fatalf("a notice stands on bare screen, no band:\n%q", raw)
			}
		})
	}
}

// TestNotice_TreeMovedIsAFooterMidStep: a tree notice that lands between two
// of a step's calls is hidden with the calls while the card is closed, so the
// card says it in its footer, in the step's own words; open, the notice
// stands among the calls as itself.
func TestNotice_TreeMovedIsAFooterMidStep(t *testing.T) {
	read := func(path string) entry {
		return entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"` + path + `"}`, toolResult: "package agent"}
	}
	for _, tc := range []struct {
		name string
		move treeMove
		want string
	}{
		{"a file it had read", treeMove{said: "1 file you read changed"}, "tree moved under me · 1 file I'd read changed"},
		{"the head and the paths", treeMove{said: "HEAD e5da10a → 056fe56, 43 paths changed outside this session · 5,811 ignored"},
			"tree moved under me · HEAD e5da10a → 056fe56, 43 paths changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := readyModel(t)
			mv := tc.move
			m.transcript = []entry{
				{kind: entryUser, text: "read the loop"},
				{kind: entryAssistant, text: "Reading the loop"},
				read("loop.go"),
				{kind: entrySystem, text: "tree moved — 43 paths changed outside this session", tree: &mv},
				read("round.go"),
			}
			m.invalidateRenderCache()
			view := stripANSI(m.renderHistory())
			if !strings.Contains(view, tc.want) {
				t.Fatalf("the card's footer should say %q:\n%s", tc.want, view)
			}
			if strings.Contains(view, "· tree moved —") {
				t.Fatalf("closed, the card stands in for the notice:\n%s", view)
			}

			m.transcript[1].stepFold = foldOpen
			m.invalidateRenderCache()
			open := stripANSI(m.renderHistory())
			if !strings.Contains(open, "· tree moved — 43 paths") || strings.Contains(open, tc.want) {
				t.Fatalf("open, the notice is itself among the calls:\n%s", open)
			}
		})
	}
}

// pictureModel is a session whose one call read a picture: the call's row,
// and the picture its result carried filed after it through the receipt.
func pictureModel(t *testing.T) Model {
	t.Helper()
	// Padded to a fixed size: the footer prints the picture's bytes, and what
	// image/png writes for the same pixels changes between Go releases (90
	// bytes on go1.27, 93 on go1.26), which is a red CI for a golden captured
	// on either.
	shot := fixedPNG(t, image.NewNRGBA(image.Rect(0, 0, 32, 16)), 128)
	m := focusModel(t)
	m.pointer.mouseOn = true
	m.transcript = nil
	m.appendEntry(entry{kind: entryUser, text: "look at the screenshot"})
	m.appendEntry(entry{kind: entryAssistant, text: "Reading the capture"})
	args := `{"path":"shot.png"}`
	result := "shot.png is an image (image/png), attached to this result for models that can see one; it has no text to return."
	m.appendEntry(entry{kind: entryTool, toolName: "read_file", toolArgs: args, toolResult: result})
	m.appendPicture("", m.callReceipt("read_file", args, result, provider.Attachment{
		Kind: provider.AttachmentImage, Name: "shot.png", MediaType: "image/png", Data: shot}))
	m.invalidateRenderCache()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoTop()
	m.atBottom = false
	return m
}

// TestCard_APictureResultIsAFooterRowThatOpens: a picture a call returned is
// a footer row of its card — the mark, the name, its dimensions and size,
// and what pressing it does on the right — and enter on it or a click on it
// opens the attachment card, through the door a sent picture opens by.
func TestCard_APictureResultIsAFooterRowThatOpens(t *testing.T) {
	m := pictureModel(t)
	view := stripANSI(m.renderHistory())
	line := ""
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "▣ shot.png") {
			line = l
		}
	}
	if !strings.HasPrefix(line, strings.Repeat(" ", components.CardBodyIndent)+"▣ shot.png · 32×16 · ") ||
		!strings.HasSuffix(strings.TrimRight(line, " "), "opens like an attachment") {
		t.Fatalf("the card's footer row for the picture:\n%s", view)
	}
	if strings.Contains(line, "[enter]") {
		t.Fatalf("the row prints no key: %q", line)
	}

	t.Run("enter", func(t *testing.T) {
		m := pictureModel(t)
		next, _ := m.enterFocusMode()
		m = next.(Model)
		m.focusIdx = 3
		next, _ = m.updateFocus(tea.KeyPressMsg{Code: tea.KeyEnter})
		if got := next.(Model); got.state != statePreview || got.preview == nil {
			t.Fatalf("enter on the row opens the attachment card, got state %d", got.state)
		}
	})
	t.Run("click", func(t *testing.T) {
		m := pictureModel(t)
		x, y := at(t, m, lineOf(t, m, "▣ shot.png"), components.CardBodyIndent+2)
		if got := click(t, m, x, y); got.state != statePreview || got.preview == nil {
			t.Fatalf("a click on the row opens the attachment card, got state %d", got.state)
		}
	})
	t.Run("a stop of its own", func(t *testing.T) {
		m := pictureModel(t)
		stops := m.expandableIndices()
		if len(stops) < 2 || stops[len(stops)-1] != 3 {
			t.Fatalf("the card and its picture are two stops, got %v", stops)
		}
	})
}

// goldenRead is a read_file call that found a file, for the fixtures below.
func goldenRead(path string) entry {
	return entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"` + path + `"}`,
		toolResult: "package agent", duration: 400 * time.Millisecond}
}

// TestGolden_ThinkingProse: the model's thinking as dimmer italic prose at
// the body column on bare screen — before a step it led to, and where it
// stood between two rounds of one, the card ending there and a card nothing
// titled taking the calls after it; and at the rung that drops it.
func TestGolden_ThinkingProse(t *testing.T) {
	captureBoundedGolden(t, "thinking-prose", "thinking drawn as prose", goldenWidths, func(width int) []golden.Panel {
		build := func(v verbosity) string {
			m := frameModel(t, width, 40)
			m.verbosity = v
			m.transcript = []entry{
				{kind: entryUser, text: "fix the round limit"},
				{kind: entryThink, text: "The limit lives in three places. The loop is the one that counts rounds, " +
					"so it should own the constant; the other two read it."},
				{kind: entryAssistant, text: "Locate the round accounting"},
				goldenRead("internal/agent/loop.go"),
				{kind: entryThink, text: "loop.go reads the constant from round.go. Now the caller."},
				goldenRead("internal/agent/round.go"),
				goldenRead("internal/agent/tool.go"),
				{kind: entryAssistant, text: "The loop owns the count; round.go only declares the constant."},
			}
			m.invalidateRenderCache()
			return m.renderHistory()
		}
		return []golden.Panel{
			{Label: "normal · a thought before the step it led to, and one inside a step", View: build(verbosityNormal)},
			{Label: "low · the rung drops thinking", View: build(verbosityLow)},
		}
	})
}

// TestGolden_ComposeRow: the round writing a call that has not landed, as a
// flat dim line at the glyph column under the prose that announced it.
func TestGolden_ComposeRow(t *testing.T) {
	captureBoundedGolden(t, "compose-row", "the compose row", goldenWidths, func(width int) []golden.Panel {
		m := frameModel(t, width, 40)
		m.setTurnState(stateStreaming)
		events := make(chan provider.StreamEvent)
		updated, _ := m.Update(streamStartedMsg{events: events})
		m = updated.(Model)
		m.appendEntry(entry{kind: entryUser, text: "write the copy handler"})
		m.appendEntry(entry{kind: entryAssistant, text: "Writing the copy handler and the flash together, so one batch carries both."})
		updated, _ = m.Update(fragment("call_1", 14*1024))
		m = updated.(Model)
		return []golden.Panel{{Label: "14 KB of a call written", View: m.renderHistory()}}
	})
}

// TestGolden_TreeMovedFooter: the tree moved while a step ran, said in the
// card's footer in the step's own words; beside evidence of its own, a row
// under it; and the card opened, where the notice is itself among the calls.
func TestGolden_TreeMovedFooter(t *testing.T) {
	captureBoundedGolden(t, "tree-moved-footer", "the tree-moved notice mid-step", goldenWidths, func(width int) []golden.Panel {
		build := func(withEvidence, open bool) string {
			m := frameModel(t, width, 40)
			mv := treeMove{said: "HEAD e5da10a → 056fe56 · 43 paths changed outside this session · 1 file you read changed"}
			second := goldenRead("internal/agent/round.go")
			if withEvidence {
				second = entry{kind: entryCommand, text: "go test ./internal/agent/...",
					toolResult: "--- FAIL: TestRoundLimit", exitCode: 1, duration: 21400 * time.Millisecond}
			}
			m.transcript = []entry{
				{kind: entryUser, text: "fix the round limit"},
				{kind: entryAssistant, text: "Next IDs are free. I'll check where the docs describe the limit."},
				goldenRead("internal/agent/loop.go"),
				{kind: entrySystem, text: "tree moved — HEAD e5da10a → 056fe56 · 43 paths changed outside this session · 1 file you read changed", tree: &mv},
				second,
			}
			if open {
				m.transcript[1].stepFold = foldOpen
			}
			m.invalidateRenderCache()
			return m.renderHistory()
		}
		return []golden.Panel{
			{Label: "a quiet step · the note is its footer", View: build(false, false)},
			{Label: "a step with evidence · the note is a row under it", View: build(true, false)},
			{Label: "opened · the notice among the calls", View: build(false, true)},
		}
	})
}

// TestGolden_PictureFooter: a picture a read returned, as its card's footer
// row, with and without the reading cursor on it.
func TestGolden_PictureFooter(t *testing.T) {
	captureBoundedGolden(t, "picture-footer", "a picture a call returned", goldenWidths, func(width int) []golden.Panel {
		m := pictureModel(t)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		m = updated.(Model)
		m.invalidateRenderCache()
		plain := m.renderHistory()
		next, _ := m.enterFocusMode()
		reading := next.(Model)
		reading.focusIdx = 3
		reading.refreshFocusView()
		body, _, _ := reading.renderFocusHistory()
		return []golden.Panel{
			{Label: "the read, and the picture it returned", View: plain},
			{Label: "reading mode · the cursor on the picture's row", View: body},
		}
	})
}
