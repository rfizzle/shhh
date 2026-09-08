package chat

// Command-center prompt surface (
// docs/interface/surfaces.md#the-input-frame). The input sits in a
// rounded-corner frame whose borders carry information: the top rail shows
// the live activity state and, attached, the session identity beside it, the
// vitals rail re-homes the cockpit segments, and the bottom rail carries
// contextual key hints. A notice rail above the frame appears only while
// there is something to say, a status row under it stands in for the
// inspector rail the narrow layouts drop, and a staged rail under that while
// an attachment is waiting to ride.
// Takeover surfaces (approval cards, pickers, the agent list, routed child asks,
// focus/diff hints) replace the framed input wholesale and keep the divider +
// status-bar stack, so their geometry is unchanged.

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/ultraviolet/layout"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// Layout thresholds in content columns (guidelines/layout-breakpoints).
//
// The breakpoints the design states are the terminal's columns — an artboard
// is drawn at 130ch with the surface's own inset inside it — and everything
// on this surface is measured in content columns, which is the terminal less
// horizontalPadding on each side. So each rung is written as the terminal
// width the guideline names, less that inset: the number a reader can check
// against the guideline stays on the page, and what it is compared against
// stays the one datum the rest of the file uses
// (docs/interface/surfaces.md#the-input-frame).
const (
	// frameWideWidth is the 110-column terminal: the vitals get a rail of
	// their own inside the box and the bottom rail carries the key hints.
	frameWideWidth = 110 - horizontalPadding*2
	// frameCompactWidth is the 70-column terminal: the vitals fold into the
	// box's bottom border and the hints go.
	frameCompactWidth = 70 - horizontalPadding*2
	// minFrameWidth is the 12-column terminal, below which there is no frame
	// at all: the prompt surface degrades to plain rows (divider + status bar
	// + the bare prompt, paint.go). Twelve is the guideline's own line and
	// the last width the box is drawn at, cramped as it is there. What the
	// guideline is protecting is the prompt rather than the box — it is the
	// glyph, not the border, that says where you type — so the layout under
	// this rung keeps the glyph, and no terminal of any width shows a draft
	// with nothing in front of it.
	minFrameWidth = 12 - horizontalPadding*2
	// frameRailEnd is a rail's fixed end: the corner and the dash beside it.
	// It is the one part of a border row that never gives ground, which is
	// why it is a Len and the labels between the two ends are not.
	frameRailEnd = 2
)

type frameLayout int

const (
	framePlain frameLayout = iota
	frameNarrow
	frameCompact
	frameWide
)

func frameLayoutFor(width int) frameLayout {
	switch {
	case width < minFrameWidth:
		return framePlain
	case width < frameCompactWidth:
		return frameNarrow
	case width < frameWideWidth:
		return frameCompact
	default:
		return frameWide
	}
}

func (m Model) frameLayout() frameLayout {
	if m.framed == nil {
		return frameLayoutFor(m.contentWidth())
	}
	if m.framed.mode == nil {
		mode := frameLayoutFor(m.contentWidth())
		m.framed.mode = &mode
	}
	return *m.framed.mode
}

// frameShowing reports whether the framed input is the current bottom panel.
func (m Model) frameShowing() bool {
	if m.agentList != nil {
		return false
	}
	if m.decisionUngated() {
		// The card rides above the frame rather than replacing it (
		// the draft still holds the keyboard, so it is still on
		// screen, still accented, and still being typed into.
		return m.frameLayout() != framePlain
	}
	if m.activeChildAsk() != nil {
		return false
	}
	switch m.state {
	case stateInput, stateStreaming, stateRunningCmd, stateClassifying:
		return m.frameLayout() != framePlain
	}
	return false
}

// framePreRails are the rows the surface draws above the box, in the order
// they stand in: whatever the session is saying, the status row that stands
// in for a dropped inspector rail, and what is staged to ride out with the
// next message. Each is empty when it has nothing to say and takes no row.
// The order is the rails' scopes, narrowing towards the box: the notices are
// the session talking, the status row is what the session amounts to, and the
// staged rail belongs to the sentence being typed.
func (m Model) framePreRails() []string {
	if m.framed == nil {
		return m.resolveFramePreRails()
	}
	if m.framed.rails == nil {
		rails := m.resolveFramePreRails()
		m.framed.rails = &rails
	}
	return *m.framed.rails
}

// resolveFramePreRails renders the three rails. The row budget counts them
// and the draw prints them, so a frame renders them once.
func (m Model) resolveFramePreRails() []string {
	return []string{m.noticeLine(), m.statusRow(), m.stagedRail()}
}

// frameExtraHeight is what the frame adds beyond the standard chrome rows:
// the rails above the box and, in the wide layout, the
// dedicated vitals rail. The frame's top and bottom borders take the rows the
// bottom divider and status bar otherwise use, so the compact and narrow
// layouts add nothing.
func (m Model) frameExtraHeight() int {
	if !m.frameShowing() {
		return 0
	}
	extra := m.interruptHeight()
	for _, rail := range m.framePreRails() {
		if rail != "" {
			extra++
		}
	}
	if m.frameLayout() == frameWide {
		extra++
	}
	return extra
}

// frameWorking reports whether the focused agent is actively working — the
// top rail's WORKING state and the steering gutter glyph key off it.
func (m Model) frameWorking() bool {
	if m.attachedTo != "" {
		if m.subagents == nil {
			return false
		}
		st, ok := m.subagents.Get(m.attachedTo)
		// A held child is still `running` as far as its lifecycle goes — it
		// keeps its slot and its worktree — but it holds no stream, which is
		// the only thing this answer is used for: whether the frame may say
		// WORKING, and whether the suspend chord is refused (hold.go).
		return ok && st.State == subagent.StateRunning && !st.Held
	}
	switch m.turnState() {
	case stateStreaming, stateRunningCmd, stateClassifying:
		return true
	}
	return false
}

// frameAccentStyle is the mode-aware border accent: add for the
// permissive modes, accent for the gated ones, spin while the auto-mode
// classifier is checking. Attached, it reflects the child's mode. The mode
// glyphs in the vitals keep meaning independent of color.
func (m Model) frameAccentStyle() lipgloss.Style {
	if m.turnState() == stateClassifying {
		return sty.Frame.AccentChecking
	}
	mode := m.policy.mode
	if m.attachedTo != "" && m.subagents != nil {
		if cm, ok := m.subagents.AgentMode(m.attachedTo); ok {
			mode = cm
		}
	}
	switch mode {
	case agent.ModeAcceptEdits, agent.ModeAuto:
		return sty.Frame.AccentPermissive
	default:
		return sty.Frame.AccentGated
	}
}

// frameIdentity is the top rail's far side: the attached breadcrumb, or
// nothing at all in the root session.
//
// The rail used to open with the title on every frame of every session,
// which named the surface a second time — the header above the transcript
// already does — and spent the width the turn's live account of itself
// (turnstatus.go) reads best across. Attached, the rail is the one place
// that says which session the keyboard is in, so the identity stays.
func (m Model) frameIdentity() string {
	if m.attachedTo == "" {
		return ""
	}
	title := m.title
	if title == "" {
		title = defaultTitle
	}
	return title + " · " + m.breadcrumb()
}

// frameActivity is the top rail's near side, the corner over the prompt
// glyph: the running turn's status line while the turn works, the summary it
// resolved into once it is done, `⏸ N waiting` while decisions are queued
// and ungated, and dim `idle` when there is nothing to report. width is the
// room the slot has; a slot too small for even the phase says nothing rather
// than clipping the identity beside it.
func (m Model) frameActivity(width int) string {
	if width <= 0 {
		return ""
	}
	// A turn paused on a decision is not working, and what the rail should
	// say is how many answers it is waiting for.
	if n := m.waitingCount(); n > 0 {
		return sty.Frame.WaitingChip.Render(clipRow(fmt.Sprintf("⏸ %d waiting", n), width))
	}
	// A turn asked to hold, and a turn parked at the boundary, are the next
	// thing the slot says: a phase would be the wrong answer to both, since
	// the first is still in one and the second is in none (hold.go).
	if chip := m.holdChip(); chip != "" {
		return sty.Frame.WaitingChip.Render(clipRow(chip, width))
	}
	// Attached, the frame is scoped to the child and the child's phase is not
	// something the supervisor reports — a subagent is running, blocked or done.
	// Naming one of the turn status's four for it would be inventing the fact,
	// so the attached rail keeps the working indicator it had.
	if m.attachedTo != "" {
		if m.frameWorking() {
			return clipRow(m.spinner.View()+sty.Frame.Working.Render("WORKING"), width)
		}
		return sty.Frame.Idle.Render(clipRow("idle", width))
	}
	if s, ok := m.turnStatus(); ok {
		if line := s.View(width); line != "" {
			return line
		}
	}
	return sty.Frame.Idle.Render(clipRow("idle", width))
}

// frameHints is the contextual bottom-rail hint set, swapped by state; it
// absorbs the old static header hint and the textarea placeholder.
func (m Model) frameHints() string {
	var hints []string
	switch {
	case m.attachedTo == "" && m.questionAside():
		// Only where the frame is the orchestrator's own: attached, enter
		// acts on the child and this rail is the child's, so a hint about
		// the orchestrator's question would be an offer enter does not keep
		// (attach.go).
		//
		// The card is gone and the question is not, so the rail says the
		// two ways back to it — the sentence being typed, and the chord that
		// draws the card again — beside the chord that queues for after the
		// turn, which still means what it always did (question.go).
		hints = []string{
			keys.Shown(keys.Draft.Send) + " answers the question",
			keys.Shown(keys.Draft.Answer) + " the card again",
			keys.Shown(keys.Draft.Queue) + " queues for after",
		}
	case m.decisionUngated():
		// The three keys that matter while a decision waits. Stopping the run
		// is the cancel chord, never esc: esc on this surface goes back rather
		// than stopping anything, and ending a turn belongs to a chord no
		// reflex produces
		// (docs/interface/principles.md#esc-is-always-the-safe-answer).
		hints = []string{
			keys.Shown(keys.Draft.Answer) + " " + keys.Words(keys.Draft.Answer),
			keys.Shown(keys.Draft.Send) + " queues steering",
			keys.Shown(keys.Draft.Cancel) + " stop the run",
		}
	case m.attachedTo != "":
		// The quit chord acts on the whole session even from a child, so
		// its armed window is said here too.
		if note := m.armedNotice(); note != "" {
			hints = []string{note}
			break
		}
		hints = []string{
			keys.Shown(keys.Agent.Detach) + " detach",
			keys.Shown(keys.Draft.Agents) + " agents",
		}
	case m.heldAtBoundary():
		// A held turn is idle in every way the frame can see, so its rail
		// has to say the three things only it knows: the key that lets the
		// turn go, that what is typed now rides out with it, and that the
		// turn can still be given up on (hold.go).
		if note := m.armedNotice(); note != "" {
			hints = []string{note}
			break
		}
		hints = []string{
			keys.Shown(keys.Draft.Pause) + " resumes the turn",
			keys.Shown(keys.Draft.Send) + " queues steering",
			keys.Shown(keys.Draft.Cancel) + " cancels it",
		}
	case m.working():
		// An open two-press window replaces the hints: what the next
		// press does is the one thing the rail must say (cancel.go).
		if note := m.armedNotice(); note != "" {
			hints = []string{note}
			break
		}
		// Commands run mid-turn now, so the working rail says so;
		// with children in flight the agent manager is the first thing to
		// reach for. While the turn streams, the interrupt leads — and it
		// is the cancel chord, the only key that stops a turn
		// (docs/interface/principles.md#esc-is-always-the-safe-answer). The
		// rail is the one place that says so, because esc doing nothing
		// looks exactly like esc being unread.
		steer := keys.Shown(keys.Draft.Send) + " queues steering"
		cancel := keys.Shown(keys.Draft.Cancel) + " cancel"
		if m.turnState() == stateStreaming {
			interrupt := keys.Shown(keys.Draft.Cancel) + " cancels the turn"
			hints = []string{interrupt, steer, "/ commands"}
			if active, _ := m.activeAgents(); active > 0 {
				hints = []string{interrupt, steer, keys.Shown(keys.Draft.Agents) + " agents", "/ commands"}
			}
			break
		}
		hints = []string{steer, "/ commands", cancel}
		if active, _ := m.activeAgents(); active > 0 {
			hints = []string{steer, keys.Shown(keys.Draft.Agents) + " agents", "/ commands", cancel}
		}
	default:
		// The quit window's hint takes the idle rail the same way the
		// cancel window takes the working one.
		if note := m.armedNotice(); note != "" {
			hints = []string{note}
			break
		}
		// Six hints, because at the width the rail first appears the row has
		// 106 columns for them and these six spend 100 — a seventh is seven
		// columns more than there is (frame_test.go holds that measurement).
		// So the editor's chord takes the slash's place rather than joining
		// it: typing `/` opens the command menu on its own, which makes it
		// the one hint here that announces itself to a reader who never
		// looked at the rail, and a chord is the only kind of key that
		// cannot. The slash keeps its place on the working rail, which is
		// shorter and has the room.
		hints = []string{
			keys.Shown(keys.Draft.Send) + " send",
			keys.Shown(keys.Draft.Newline) + " newline",
			keys.Shown(keys.Draft.Editor) + " editor",
			keys.Shown(keys.Draft.Attach) + " attach",
			keys.Shown(keys.Draft.Palette) + " palette",
			keys.Shown(keys.Draft.Mode) + " mode",
		}
	}
	return sty.Frame.Hint.Render(strings.Join(hints, " · "))
}

// promptGutter is the input's leading glyph: ❯ idle, ▸ while the
// agent works (typed text becomes steering), ? while a question is waiting
// behind the draft (enter answers it), ! while the draft is in bang form
// (enter runs a command, through the confirm), and the child's name while
// attached.
func (m Model) promptGutter() string {
	if m.attachedTo != "" {
		return sty.Frame.GutterIdle.Render(m.attachedTo+" ❯") + " "
	}
	// Bang form outranks the working glyph: enter on this draft is a
	// command either way — confirmed idle, refused mid-turn — never
	// steering, and the gutter must not claim otherwise (bang.go). It
	// outranks the question's glyph for the same reason: a bang line is
	// answered as a command before anything asks what a sentence answers
	// (command.go).
	if m.bangDraft() {
		return sty.Frame.GutterBang.Render("!") + " "
	}
	// A question waiting behind the draft says so here, because an answer
	// and a steer reach the model differently — an answer is the call's
	// result, a steer is a message — and a reader must know which of the two
	// they are writing (question.go).
	if m.questionAside() {
		return sty.Frame.WaitingChip.Render("?") + " "
	}
	if m.frameWorking() {
		return sty.Frame.GutterWork.Render("▸") + " "
	}
	return sty.Frame.GutterIdle.Render("❯") + " "
}

// frameBox is the prompt frame's own rectangles: the box, the
// two border columns, what they leave between them, and the split a draft
// row makes of that — the prompt gutter's columns and the text's.
type frameBox struct {
	area   uv.Rectangle
	left   uv.Rectangle
	right  uv.Rectangle
	inner  uv.Rectangle
	gutter uv.Rectangle
	draft  uv.Rectangle
}

// frameBoxFor resolves the box inside a rectangle. The columns are the same
// at every row, which is why a caller that only wants a width can hand it a
// single row.
func (m Model) frameBoxFor(area uv.Rectangle) frameBox {
	var b frameBox
	b.area = area
	// border, its padding column, the content, and the same again mirrored.
	layout.Horizontal(
		layout.Len(1), layout.Len(1),
		layout.Fill(1),
		layout.Len(1), layout.Len(1),
	).Split(b.area).Assign(&b.left, new(uv.Rectangle), &b.inner, new(uv.Rectangle), &b.right)
	layout.Horizontal(
		layout.Len(lipgloss.Width(m.promptGutter())),
		layout.Fill(1),
	).Split(b.inner).Assign(&b.gutter, &b.draft)
	return b
}

// inputInnerWidth is the textarea's usable width inside the frame: what the
// box leaves after its borders and the prompt gutter. The plain
// (sub-minFrameWidth) layout has no borders to pay for, but it still draws
// the prompt glyph (paint.go), so it pays for that.
func (m Model) inputInnerWidth() int {
	if m.frameLayout() == framePlain {
		return max(m.contentWidth()-lipgloss.Width(m.plainPrompt()), 1)
	}
	return max(m.frameBoxFor(uv.Rect(0, 0, max(m.contentWidth(), 0), 1)).draft.Dx(), 1)
}

// syncInputWidth re-fits the textarea to the frame; call it when the width
// or the gutter (attach/detach) changes. The box counts its own rows against
// the width it is given, so this can move its height too — fitDraft is the
// caller that cares (layout.go).
func (m *Model) syncInputWidth() {
	m.input.SetWidth(m.inputInnerWidth())
}

// maxDraftRows caps how far the draft box grows before the textarea scrolls
// inside it instead. Twelve rows is where a prompt stops being written and
// starts being read — past it the box would be a pager, and $EDITOR (ctrl+g)
// is the surface for that.
const maxDraftRows = 12

// draftMaxRows is the box's ceiling on this terminal: maxDraftRows, or the
// panel's own bound where that is lower
// (docs/interface/principles.md#the-grammar: the bottom panel takes at most
// 40% of the terminal), less the two chrome rows around the box. It never
// falls below the three rows the box has always had, because a ceiling under
// the floor is a box that cannot show the line being typed into it.
func (m Model) draftMaxRows() int {
	return max(min(maxDraftRows, m.maxConfirmPanelHeight()-bottomChromeHeight), inputHeight)
}

// syncInputHeight settles what the draft box costs the transcript. The height
// itself is no longer counted here: the textarea grows and shrinks with its
// own content between draftMaxRows and the three rows the box starts at, and
// it wraps that content with the same rule it draws it by, so the box and its
// contents can no longer disagree about how many rows there are. What is left
// is the rows the viewport gives up or gets back, through the same split
// every panel change settles by (layout.go).
//
// It runs on the update tail, so a keystroke, a paste, a recalled message and
// a resize all settle the same way.
func (m *Model) syncInputHeight() {
	if !m.ready {
		return
	}
	// Only the pane's rows move; its lines are wrapped to a width this did
	// not change, so nothing is re-rendered — a keystroke that grows the box
	// must not snap a reader scrolled up back to the bottom, and a resize
	// mid-drag must not spend the render its settle window is deferring.
	// Follow mode is the exception it always is: a pane pinned to the live
	// end stays pinned through the height change.
	if vh := m.viewportHeight(); vh != m.viewport.Height() {
		follow := m.viewport.AtBottom()
		m.viewport.SetHeight(vh)
		if follow {
			m.viewport.GotoBottom()
		}
	}
}

// questionNotice is the notice rail's count of the questions waiting behind
// the draft, and what the reader's next message will do about them. The three
// queues are counted separately because "answer the question", "change what
// you are doing" and "when you are done, then" are three different promises,
// and a reader who has typed one sentence is owed which of them it keeps
// (docs/interface/surfaces.md#the-question-card).
func (m Model) questionNotice() string {
	if !m.questionAside() {
		return ""
	}
	// Below the wide breakpoint the frame has no hint rail, so the count
	// also says what the next message will do with the sentence in the box.
	// It is the move the armed window already makes above, for the same
	// reason: the invariant that the surface says what a key will do cannot
	// depend on the terminal being wide. Where the hint rail is there it
	// says so already, and the count stays the count — which is also what
	// keeps the three promises legible side by side at sixty columns, where
	// they compete for one rail.
	return questionNoticeFor(m.questionsWaiting(), m.frameLayout() != frameWide)
}

// questionNoticeFor is the wording, counted, in the shape followUpNotice
// uses: what is waiting, and the key fact about it after a dash where there
// is one to add. The count is a parameter rather than the one the queue can
// hold today, because the rail is the sentence and the arithmetic is not its
// business.
func questionNoticeFor(n int, sayTheKey bool) string {
	if n <= 0 {
		return ""
	}
	label := fmt.Sprintf("%d question", n)
	if n > 1 {
		label += "s"
	}
	label += " waiting"
	if sayTheKey {
		// Two words rather than a sentence: at sixty columns this rail
		// also has to hold the steering count beside it, and a promise
		// that pushed the other two off the row would be the rail
		// answering one of three questions.
		label += " — " + keys.Shown(keys.Draft.Send) + " answers"
	}
	return label
}

// noticeLine assembles the notice rail: update notice, queued
// steering, blocked sub-agents, and the latest auto-mode denial. Empty —
// rail hidden — when there is nothing to say; orchestrator-scoped, so it
// hides while attached.
func (m Model) noticeLine() string {
	if m.attachedTo != "" {
		return ""
	}
	var parts []string
	// Below the wide breakpoint the frame has no hint rail, so an open
	// two-press window says what the next press does here — the invariant
	// that the surface says what a key will do cannot depend on the
	// terminal being wide (cancel.go).
	if m.frameLayout() != frameWide {
		if note := m.armedNotice(); note != "" {
			parts = append(parts, sty.Frame.NoticeInfo.Render(note))
		}
	}
	// What the last esc folded, or why it folded nothing (readinghint.go).
	// It leads everything but the open two-press window because it is the
	// only thing here that accounts for the pane having just moved under the
	// reader, and it is gone by the next press: a rail that clipped it would
	// leave the movement unexplained for the one frame it could have been
	// explained in. It rides the rail rather than the transcript for the
	// reason the copy caption does — appending a row would scroll the reader
	// away from what they just put back.
	if m.foldNotice != "" {
		parts = append(parts, sty.Frame.NoticeInfo.Render(m.foldNotice))
	}
	if m.keysNotice != "" {
		// The rebind notice (keysnotice.go): shown for one session after a
		// release that moved keys, ahead of the counts below because it
		// explains what a reflex just failed to do.
		parts = append(parts, sty.Frame.NoticeInfo.Render(m.keysNotice))
	}
	if m.updateNotice != "" {
		parts = append(parts, sty.UpdateNotice.Render(m.updateNotice))
	}
	// A question the reader handed to the draft leads the three counts,
	// because it is the one that claims the next message: the draft holds
	// the keyboard and nothing is on the screen to say a question is
	// waiting, so this rail is the only thing that can (question.go).
	if note := m.questionNotice(); note != "" {
		parts = append(parts, sty.Frame.NoticeInfo.Render(note))
	}
	if n := len(m.steering); n > 0 {
		parts = append(parts, sty.Frame.NoticeInfo.Render(fmt.Sprintf("%d steering queued", n)))
	}
	// Follow-ups count separately from steering: one joins the running
	// turn, the other waits for it to end (followup.go).
	if note := m.followUpNotice(); note != "" {
		parts = append(parts, sty.Frame.NoticeInfo.Render(note))
	}
	// Scrolled off the live end, so the transcript has stopped following the
	// turn (navigate.go). The draft still holds the keyboard, so this
	// rail is the only thing that can say so.
	if note := m.followNotice(); note != "" {
		parts = append(parts, sty.Frame.NoticeInfo.Render(note))
	}
	// What the last mouse selection put on the clipboard (select.go).
	// It rides here rather than in the transcript because a copy is not part
	// of the conversation, and because appending a row would scroll the pane
	// away from the selection the reader is still looking at.
	if m.selNotice != "" {
		parts = append(parts, sty.Frame.NoticeInfo.Render(m.selNotice))
	}
	if m.subagents != nil {
		if _, blocked := m.subagents.ActiveCounts(); blocked > 0 {
			label := fmt.Sprintf("⚠ %d agents waiting approval", blocked)
			if blocked == 1 {
				label = "⚠ 1 agent waiting approval"
			}
			parts = append(parts, sty.Frame.NoticeAlert.Render(label))
		}
	}
	if m.denialNotice != "" {
		parts = append(parts, sty.Frame.NoticeAlert.Render("✗ auto denied: "+firstLine(m.denialNotice)+" (/permissions why)"))
	}
	if len(parts) == 0 {
		return ""
	}
	return clipRow(strings.Join(parts, sty.SystemMsg.Render(" · ")), m.contentWidth())
}

// railSlots splits a border row into the five rectangles it is drawn from:
// the two fixed ends, the left label, the dash fill between the labels, and
// the right label. It is separate from drawRail because the top rail has to
// know how wide the right-hand slot is *before* it can ask the status line
// what to put in it.
func railSlots(leftLabel, rightLabel string, width int) (head, left, fill, right, tail uv.Rectangle) {
	var labels uv.Rectangle
	layout.Horizontal(
		layout.Len(frameRailEnd),
		layout.Fill(1),
		layout.Len(frameRailEnd),
	).Split(uv.Rect(0, 0, max(width, 0), 1)).Assign(&head, &labels, &tail)

	// A right label too wide for what is left says nothing rather than
	// crowding the identity beside it. That is the design's rule, not
	// the fill's, so it is spelled out here rather than left to the solver's
	// idea of which segment should give ground.
	rw := lipgloss.Width(rightLabel)
	if rw > labels.Dx() {
		rw = 0
	}
	var rest uv.Rectangle
	layout.Horizontal(layout.Fill(1), layout.Len(rw)).Split(labels).Assign(&rest, &right)
	layout.Horizontal(
		layout.Len(min(lipgloss.Width(leftLabel), max(rest.Dx(), 0))),
		layout.Fill(1),
	).Split(rest).Assign(&left, &fill)
	return head, left, fill, right, tail
}

// drawRail draws one border row into those rectangles: corner and dash at
// each end, the labels in their slots, dashes across what is left. Nothing
// is measured against a remainder — a label wider than the columns it was
// given is cut by the edge it was drawn against.
func drawRail(scr uv.Screen, area uv.Rectangle, accent lipgloss.Style, leftCorner, rightCorner, leftLabel, rightLabel string) {
	head, left, fill, right, tail := railSlots(leftLabel, rightLabel, area.Dx())
	at := func(r uv.Rectangle) uv.Rectangle { return r.Add(area.Min) }
	drawIn(scr, accent.Render(leftCorner+"─"), at(head))
	drawIn(scr, leftLabel, at(left))
	drawIn(scr, accent.Render(strings.Repeat("─", max(fill.Dx(), 0))), at(fill))
	if right.Dx() > 0 {
		drawIn(scr, rightLabel, at(right))
	}
	drawIn(scr, accent.Render("─"+rightCorner), at(tail))
}

// frameVitals renders the vitals rail content: the cockpit segments with the
// layout modes' field-drop order. The narrow layout keeps only the
// never-dropped fields (minimal rail); attached, the vitals scope to the
// child.
func (m Model) frameVitals(layout frameLayout, width int) string {
	var segs []components.RailSegment
	if m.attachedTo != "" && m.subagents != nil {
		segs = m.childRailSegments()
	} else {
		segs = m.cockpitData(false).RailSegments()
	}
	if layout == frameNarrow {
		minimal := segs[:0:0]
		for _, s := range segs {
			if s.Drop <= components.RailVital {
				minimal = append(minimal, s)
			}
		}
		segs = minimal
	}
	return components.FitRail(segs, sty.StatusBar.Render(" · "), width)
}

// childRailSegments is the attached child's vitals: mode, live
// detail (alert-styled when blocked), spend, queued steering, and the
// child's name as the droppable-first detail field.
func (m Model) childRailSegments() []components.RailSegment {
	name := m.attachedTo
	st, ok := m.subagents.Get(name)
	if !ok {
		return []components.RailSegment{{Text: sty.StatusBar.Render(name), Drop: components.RailKeep}}
	}
	mode, _ := m.subagents.AgentMode(name)
	segs := []components.RailSegment{{Text: childModeSegment(mode), Drop: components.RailKeep}}
	detail, drop := sty.StatusBar.Render(st.Detail), components.RailNormal
	if st.State == subagent.StateBlocked {
		detail, drop = sty.CtxAlert.Render(st.Detail), components.RailVital
	}
	segs = append(segs, components.RailSegment{Text: detail, Drop: drop})
	if spend := m.childSpendLabel(st); spend != "" {
		segs = append(segs, components.RailSegment{Text: sty.StatusBar.Render(spend), Drop: components.RailVital})
	}
	if q := m.subagents.QueuedSteering(name); q > 0 {
		segs = append(segs, components.RailSegment{Text: sty.StatusBar.Render(fmt.Sprintf("queued %d", q)), Drop: components.RailNormal})
	}
	return append(segs, components.RailSegment{Text: sty.StatusBar.Render(st.Name), Drop: components.RailDetail})
}

// railLabelWidth is the room a rail label has once the ends and an
// already-placed label have taken theirs: the slot the split leaves, less the
// space on each side of the label itself.
func railLabelWidth(leftLabel string, width int) int {
	_, _, fill, _, _ := railSlots(leftLabel, "", width)
	var slot uv.Rectangle
	layout.Horizontal(layout.Len(1), layout.Fill(1), layout.Len(1)).
		Split(fill).Assign(new(uv.Rectangle), &slot, new(uv.Rectangle))
	return slot.Dx()
}

// topRailLabels is the top rail's two labels: the running turn's account of
// itself on the left and the identity on the right, except attached below the
// wide layout, where the identity keeps the left and the right is the hints
// rail that has nowhere else to go.
//
// The account leads because it is the only thing on the rail that moves, and
// the eye watching it is already on the prompt glyph two rows below the
// rail's left corner. On a three-thousand-pixel window the same figures sat
// against the right edge, a hundred and fifty columns from anything the
// reader was looking at (docs/interface/surfaces.md#the-input-frame).
func (m Model) topRailLabels(mode frameLayout, width int) (left, right string) {
	var identity, identityLabel string
	if m.attachedTo != "" && mode != frameNarrow {
		identity = m.frameIdentity()
		identityLabel = " " + identity + " "
	}
	if m.attachedTo != "" && mode != frameWide {
		// Compact/narrow drop the hints rail; the detach affordance moves to
		// the top rail, and takes the slot the account would have had.
		return identityLabel, " " + m.frameHints() + " "
	}
	// The account is measured against the whole rail and the identity takes
	// what is left of it, so a breadcrumb that does not fit is dropped whole
	// rather than squeezing the account into a fragment. Measuring the other
	// way round leaves a full-width breadcrumb beside an account clipped to
	// `⠋W…`, which is the one label on the rail nobody can read and the only
	// one that changes while a turn runs; which session the keyboard is in is
	// a fact a key can ask for again.
	activity := m.frameActivity(railLabelWidth("", width))
	if activity == "" {
		return "", ""
	}
	left = " " + activity + " "
	if lipgloss.Width(identity) > railLabelWidth(left, width) {
		return left, ""
	}
	return left, identityLabel
}

// draftView is the draft box's own render. It is the one place the field is
// drawn, because the palette's colours reach a field only where it is drawn
// (components.StyleTextArea) — a swap while a session is open otherwise
// leaves the draft in the table the session started with.
func (m Model) draftView() string {
	components.StyleTextArea(&m.input)
	return m.input.View()
}

// frameDraftLines is what goes inside the box: the textarea's rows and, under
// them, the completion menu. bottomPanelHeight already caps the pair
// at the confirm-panel bound, and the cut is taken here so the box's height
// and its contents can never be counted differently.
func (m Model) frameDraftLines() (lines, menu []string) {
	lines = strings.Split(m.draftView(), "\n")
	switch {
	case m.historySearching():
		// The search states itself where the completion menu would: both
		// are the input explaining what the next keystroke does to it, and
		// the two cannot be open at once.
		menu = m.historySearchLines()
	case m.completionActive() && m.attachedTo == "":
		menu = m.completionMenuLines()
	}
	if maxRows := m.bottomPanelHeight(); len(lines)+len(menu) > maxRows {
		if len(lines) > maxRows {
			return lines[:maxRows], nil
		}
		menu = menu[:maxRows-len(lines)]
	}
	return lines, menu
}

// promptRects is the frame's vertical split inside a rectangle: the rails
// above the box, the box's own columns, and the four bands of it. The paint
// reads it and so does the cursor, which is what keeps the two from
// disagreeing about which row the draft is on.
type promptRects struct {
	above  uv.Rectangle
	rails  []string
	box    frameBox
	top    uv.Rectangle
	drafts uv.Rectangle
	vitals uv.Rectangle
	bottom uv.Rectangle
}

// promptFrameRects resolves the frame inside a rectangle.
func (m Model) promptFrameRects(area uv.Rectangle) promptRects {
	var r promptRects
	r.rails = m.framePreRails()
	shown := 0
	for _, rail := range r.rails {
		if rail != "" {
			shown++
		}
	}
	var boxArea uv.Rectangle
	layout.Vertical(layout.Len(shown), layout.Fill(1)).Split(area).Assign(&r.above, &boxArea)

	// The wide layout gets a rail of its own for the vitals; the others hang
	// them on the closing rail.
	vitalsRows := 0
	if m.frameLayout() == frameWide {
		vitalsRows = 1
	}
	r.box = m.frameBoxFor(boxArea)
	// The rails are fixed and the draft rows absorb whatever is left, so a
	// box given more rows than it has content for stays closed rather than
	// trailing blank rows under its own bottom border.
	layout.Vertical(
		layout.Len(1),
		layout.Fill(1),
		layout.Len(vitalsRows),
		layout.Len(1),
	).Split(r.box.area).Assign(&r.top, &r.drafts, &r.vitals, &r.bottom)
	return r
}

// drawPromptFrame paints the whole surface into its rectangle: the rails
// above the box, then the box — top rail, gutter + input rows (+ completion
// menu), vitals rail, bottom rail — each in the rectangle promptFrameRects
// resolved for it. The rails above the box are rows of the
// surface rather than rows of the box, which is why they are split off first.
//
// cur is where the terminal's own cursor is reported from, because this is
// the one place that knows the screen cell the draft's first character lands
// in. A caller with no screen to place a cursor on passes nil.
func (m Model) drawPromptFrame(scr uv.Screen, area uv.Rectangle, cur *cursorSink) {
	mode := m.frameLayout()
	accent := m.frameAccentStyle()
	width := area.Dx()

	r := m.promptFrameRects(area)
	row := 0
	for _, rail := range r.rails {
		if rail == "" {
			continue
		}
		drawIn(scr, rail, rowAt(r.above, row))
		row++
	}

	lines, menu := m.frameDraftLines()
	box := r.box
	topLeft, topRight := m.topRailLabels(mode, width)
	drawRail(scr, r.top, accent, "╭", "╮", topLeft, topRight)

	for i := range r.drafts.Dy() {
		y := r.drafts.Min.Y - box.area.Min.Y + i
		drawIn(scr, accent.Render("│"), rowAt(box.left, y))
		drawIn(scr, accent.Render("│"), rowAt(box.right, y))
		switch {
		case i == 0:
			// The prompt glyph owns its columns and the draft wraps to the
			// rest; a continuation line simply leaves the gutter blank.
			drawIn(scr, m.promptGutter(), rowAt(box.gutter, y))
			drawIn(scr, lines[i], rowAt(box.draft, y))
		case i < len(lines):
			drawIn(scr, lines[i], rowAt(box.draft, y))
		case i-len(lines) < len(menu):
			// The menu is the frame's, not the draft's: it starts at the box
			// edge rather than under the text.
			drawIn(scr, menu[i-len(lines)], rowAt(box.inner, y))
		}
	}
	m.placeFrameCursor(cur, r, len(lines))

	vitals := " " + m.frameVitals(mode, railLabelWidth("", width)) + " "
	if mode == frameWide {
		drawRail(scr, r.vitals, accent, "├", "┤", vitals, "")
		drawRail(scr, r.bottom, accent, "╰", "╯", " "+m.frameHints()+" ", "")
		return
	}
	drawRail(scr, r.bottom, accent, "╰", "╯", vitals, "")
}

// placeFrameCursor reports where the terminal's cursor stands inside the
// frame just painted. The draft owns it — it is the surface's editor, and its
// cursor is the terminal's own rather than a glyph shhh paints
// (docs/interface/surfaces.md#the-input-frame) — except while the history
// search is open, where the draft shows the match and the row under it is
// what is being typed into. A frame nobody is typing into places none, and
// the terminal hides its cursor.
func (m Model) placeFrameCursor(cur *cursorSink, r promptRects, drafted int) {
	if cur == nil {
		return
	}
	if m.historySearching() {
		// The search states itself on the first menu row, which starts at the
		// box edge rather than under the draft's text.
		row := rowAt(r.box.inner, r.drafts.Min.Y-r.box.area.Min.Y+drafted)
		cur.place(m.searchCursor(), row)
		return
	}
	// The textarea reports its cursor against its own first cell, which is
	// the first draft row inside the gutter.
	draft := r.box.draft
	draft.Min.Y, draft.Max.Y = r.drafts.Min.Y, r.drafts.Max.Y
	cur.place(m.input.Cursor(), draft)
}

// renderPromptFrame is the same surface as a string, for the captures and for
// callers that hold no screen. Its height is the bottom panel's own rows less
// the interrupt card riding above it, which is the same accounting the
// vertical split hands out — the frame cannot be sized one way and budgeted
// for another.
func (m Model) renderPromptFrame() string { return m.renderPromptFrameWith(nil) }

// renderPromptFrameWith is the same render with somewhere to report the
// cursor to, for a caller holding the frame's own coordinates rather than the
// screen's.
func (m Model) renderPromptFrameWith(cur *cursorSink) string {
	scr := uv.NewScreenBuffer(max(m.contentWidth(), 0), max(m.bottomRows()-m.interruptHeight(), 0))
	m.drawPromptFrame(scr, scr.Bounds(), cur)
	return renderScreen(scr)
}
