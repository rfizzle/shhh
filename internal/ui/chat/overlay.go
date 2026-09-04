package chat

// The register of overlays: every mode that borrows the screen, as one table.
//
// A mode used to be six lists. Adding one meant a state constant, a branch of
// the predicate that says a state is a surface, a rung of the key ladder, a
// case in the switch that renders the bottom panel, a case in the switch that
// renders the transcript pane, and a case in the switch that draws the hint
// where the draft box was. Six places to add a row to and six places to
// forget one — and nothing that could say which of them a given mode was
// missing from, because a mode missing from one of the six is not a compile
// error, it is a surface that draws and cannot be typed into, or one that
// answers keys and cannot be seen.
//
// So a mode is one row here and its own file. The row says where the mode
// draws, whether it takes the screen from the turn, how tall it may grow,
// what it puts where the draft was, and what it does with a key. Everything
// that used to switch on the state reads the row instead.
//
// What the register deliberately does not become: a place a mode's own
// behaviour moves to. The row points at the mode's file; the flow, the
// wording and the state stay there. This is a dispatch table, and a dispatch
// table that grows logic is the switch it replaced with extra steps.

import (
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/subagent"
)

// placement is where the one-panel grammar draws an overlay. There are three
// and there is no fourth: the grammar fixes where a surface may go, so a mode
// that wanted a fourth would be asking to break it rather than to be added.
// See docs/interface/principles.md#the-grammar.
type placement int

const (
	// placePanel borrows the bottom panel — the rows the draft box was in,
	// taken out of the transcript by the vertical split.
	placePanel placement = iota
	// placePane takes the transcript pane over, full width, and leaves a
	// one-line hint where the draft box was.
	placePane
	// placeFloating rides above the prompt frame rather than filling the
	// panel: the decision card that arrived on top of a sentence, which is
	// showing while the draft still holds the keyboard. It fills the panel
	// once the handover gives it the keyboard, which is why the placement is
	// read against the frame rather than on its own.
	placeFloating
	// placeNone is a mode that owns the keyboard without drawing a block of
	// its own — the retry countdown, which is a row of the live tail, and
	// the model-list wait, which draws nothing at all.
	placeNone
)

// overlayAction is what an overlay leaves for the host once it has answered a
// key: the transcript row the answer earned, whether the screen goes back to
// the turn, and the session work the answer started.
//
// A mode whose answer is a decision says that much and no more, and the host
// closes the surface, puts the row in the transcript and repaints, in that
// order. Not every mode's answer fits those terms, and the ones that do not
// hand the session back themselves with the command they built: their answers
// are jobs the session owns — a compaction, an undo, the next stage of a
// backlog run — or a return to somewhere other than what was underneath, the
// way the full-screen diff goes back to reading mode when that is the door it
// was opened by. That is the seam's edge and not a move half made: work the
// session owns is not something a mode should be describing.
type overlayAction struct {
	// close hands the screen back to the turn the overlay borrowed it from.
	close bool
	// note is the transcript row the answer leaves behind; empty leaves none.
	note string
	// run is the command a mode that could not answer in the two fields
	// above built for itself.
	run tea.Cmd
}

// overlay is one mode borrowing the screen.
//
// Lines takes the session as well as the width because a mode's state lives
// on the Model and not in the row — the row is a table entry, not a widget —
// and it takes a height because a pane overlay scrolls inside the rectangle
// it was given, which a panel overlay never does.
type overlay interface {
	// Placement is where the grammar draws this mode.
	Placement() placement
	// Lines renders the mode into the rectangle it was given, one row per
	// line. A nil answer means the mode has nothing to draw and whatever was
	// under it stands.
	Lines(m Model, width, height int) []string
	// Bound is the most panel rows this mode may take. It is only asked of a
	// panel overlay; the pane takes what the vertical split leaves it.
	Bound(m Model) int
	// Update answers one key press.
	Update(m Model, key tea.KeyPressMsg) (Model, overlayAction)
}

// mode is one row of the register. Everything that used to switch on a state
// constant reads these fields instead.
type mode struct {
	// place is where the grammar draws it.
	place placement
	// borrows reports that the mode takes the screen from the session's own
	// turn, which keeps running underneath (turn.go). A mode that does not —
	// the decision cards, the retry countdown — is a stage of the turn that
	// happens to own the keyboard, and parking the turn under it would park
	// it under itself.
	borrows bool
	// aboveDecision routes the mode's keys ahead of the handover chord and
	// the grace window. It is the three viewers that replace the pane and
	// nothing else: two of them are where a decision card's own [d] goes, so
	// the reader is inside the card's detail and the chord that would gate
	// the card behind it is not what the key means there. Moving one of them
	// below the handover would spend a key meant for the diff on the card
	// underneath it.
	aboveDecision bool
	// ownsQuit reports that the mode answers the quit chord itself instead of
	// through surfaceKey. Two do: the quit confirm, because that surface is
	// the question the chord asks, and the context screen, which has never
	// answered it.
	ownsQuit bool
	// lines renders the mode; nil draws nothing.
	lines func(m Model, width, height int) []string
	// bound caps the panel rows; nil takes the panel's own bound.
	bound func(m Model) int
	// hint is the one line a pane overlay leaves where the draft box was.
	hint func(m Model) string
	// cursor is where the terminal's own cursor stands inside the mode's
	// rows, for a mode that is typed into. nil is a mode that is read rather
	// than written, which is most of them, and the terminal hides its cursor
	// over one — a filter row with no cursor at all would say nothing about
	// where the next character goes.
	cursor func(m Model, width int) *tea.Cursor
	// keys is the mode's key handler, which stays in the mode's own file.
	// A mode supplies this or answer, never both.
	keys func(m Model, key tea.KeyPressMsg) (tea.Model, tea.Cmd)
	// answer is the handler of a mode whose answer the host can carry out:
	// the mode reports that it is done and what row it leaves behind, and
	// closing the surface, putting the row in the transcript and repainting
	// happen once in routeOverlay rather than at the end of every mode.
	// done is false for a key the mode consumed without finishing.
	answer func(m *Model, key tea.KeyPressMsg) (done bool, act overlayAction)
}

func (o *mode) Placement() placement { return o.place }

func (o *mode) Lines(m Model, width, height int) []string {
	if o.lines == nil {
		return nil
	}
	return o.lines(m, width, height)
}

func (o *mode) Bound(m Model) int {
	if o.bound == nil {
		return m.maxConfirmPanelHeight()
	}
	return o.bound(m)
}

func (o *mode) Update(m Model, key tea.KeyPressMsg) (Model, overlayAction) {
	if o.answer != nil {
		done, act := o.answer(&m, key)
		if !done {
			return m, overlayAction{}
		}
		return m, act
	}
	next, cmd := o.keys(m, key)
	if mm, ok := next.(Model); ok {
		return mm, overlayAction{run: cmd}
	}
	return m, overlayAction{run: cmd}
}

// buildOverlays is the register. One row per mode, and adding a mode is this
// row plus the mode's own file.
func buildOverlays() map[state]*mode {
	return map[state]*mode{
		// The two decision cards. They float while the draft still holds the
		// keyboard and fill the panel once the handover has given it to them, so
		// their placement is read against the frame rather than on its own
		// (resolvePanel). Neither borrows the screen: a decision is a stage of
		// the turn that asked for it.
		stateConfirmRun: {
			place: placeFloating,
			lines: panelRows((Model).confirmPanelLines),
			bound: (Model).confirmPanelBound,
			keys:  (Model).updateConfirmRun,
		},
		statePlanApprove: {
			place: placeFloating,
			lines: panelRows((Model).planPanelLines),
			bound: func(m Model) int { return m.planPanelBound() + m.gatedExtraRows() },
			keys:  (Model).updatePlanApprove,
		},

		// The panel overlays: the cards and selectors that take the draft box's
		// rows out of the transcript.
		statePick: {
			place:   placePanel,
			borrows: true,
			lines:   panelRows((Model).pickerLines),
			cursor:  (Model).pickCursor,
			keys:    (Model).updatePick,
		},
		stateTodoPropose: {
			place:   placePanel,
			borrows: true,
			lines:   panelRows((Model).todoProposeLines),
			answer:  (*Model).answerTodoPropose,
		},
		stateTodoDraft: {
			place:   placePanel,
			borrows: true,
			lines:   panelRows((Model).todoDraftLines),
			answer:  (*Model).answerTodoDraft,
		},
		statePasteDrop: {
			place:   placePanel,
			borrows: true,
			lines:   panelRows((Model).pasteDropLines),
			keys:    (Model).updatePasteDrop,
		},
		stateScaffold: {
			place:   placePanel,
			borrows: true,
			lines:   panelRows((Model).scaffoldLines),
			// A decision whose keys were cut off by the panel bound is not one,
			// so the card gets the plan card's headroom the way the pressure card
			// does (scaffold.go).
			bound:  (Model).planPanelBound,
			answer: (*Model).answerScaffold,
		},
		stateTodoPause: {
			place:   placePanel,
			borrows: true,
			lines:   panelRows((Model).todoPauseLines),
			keys:    (Model).updateTodoPause,
		},
		stateUndoConfirm: {
			place:   placePanel,
			borrows: true,
			lines:   panelRows((Model).undoConfirmLines),
			keys:    (Model).updateUndoConfirm,
		},
		stateQuitConfirm: {
			place:    placePanel,
			borrows:  true,
			ownsQuit: true,
			lines:    panelRows((Model).quitConfirmLines),
			keys:     (Model).updateQuitConfirm,
		},
		stateKeyEntry: {
			place:   placePanel,
			borrows: true,
			lines:   panelRows((Model).keyEntryLines),
			answer:  (*Model).answerKeyEntry,
		},
		stateFocus: {
			place:   placePanel,
			borrows: true,
			// The reading bar is normally the input's three rows; `[?]` grows it
			// into the mode's key register, and the panel pays for it out of the
			// transcript the way every other panel does.
			lines: panelRows((Model).focusHintLines),
			// The one row of this mode that is typed into rather than read is
			// the transcript search's query, so the cursor is placed on it
			// and nowhere else (navigate.go).
			cursor: (Model).readingSearchCursor,
			keys:   (Model).updateFocus,
		},
		statePressure: {
			place:   placePanel,
			borrows: true,
			lines:   panelRows((Model).pressureLines),
			// The card is a decision, and a decision whose action bar was cut off
			// by the panel bound is not one: it gets the plan card's headroom.
			bound: (Model).planPanelBound,
			keys:  (Model).updatePressure,
		},

		// The pane overlays: full width, the rail hidden, a one-line hint where
		// the draft box was. Each does its own scrolling inside the rectangle,
		// which is why they are the modes that read the height.
		stateDiffFull: {
			place:         placePane,
			borrows:       true,
			aboveDecision: true,
			lines: func(m Model, width, height int) []string {
				if m.fullDiff == nil {
					return nil
				}
				m.fullDiff.SetSize(width, height)
				return strings.Split(m.fullDiff.View(width), "\n")
			},
			hint: (Model).renderDiffFullHint,
			keys: (Model).updateDiffFull,
		},
		stateOutputFull: {
			place:         placePane,
			borrows:       true,
			aboveDecision: true,
			lines: func(m Model, width, height int) []string {
				if m.fullOutput == nil {
					return nil
				}
				m.fullOutput.SetSize(width, height)
				return strings.Split(m.fullOutput.View(width), "\n")
			},
			hint: (Model).renderOutputFullHint,
			keys: (Model).updateOutputFull,
		},
		statePreview: {
			place:         placePane,
			borrows:       true,
			aboveDecision: true,
			lines: func(m Model, width, height int) []string {
				if m.preview == nil {
					return nil
				}
				m.preview.SetSize(width, height)
				return strings.Split(m.preview.View(width), "\n")
			},
			hint:   (Model).renderPreviewHint,
			answer: (*Model).answerPreview,
		},
		stateReview: {
			place:   placePane,
			borrows: true,
			lines: func(m Model, width, height int) []string {
				if m.review == nil {
					return nil
				}
				m.review.SetSize(width, height)
				return strings.Split(m.review.View(width), "\n")
			},
			hint: (Model).renderReviewHint,
			keys: (Model).updateReview,
		},
		stateContext: {
			place:    placePane,
			borrows:  true,
			ownsQuit: true,
			lines: func(m Model, width, height int) []string {
				if m.context == nil {
					return nil
				}
				m.context.SetSize(width, height)
				return strings.Split(m.context.View(width), "\n")
			},
			hint:   (Model).renderContextHint,
			answer: (*Model).answerContext,
		},
		stateBacklog: {
			place:   placePane,
			borrows: true,
			lines: func(m Model, width, height int) []string {
				if m.backlog == nil {
					return nil
				}
				return strings.Split(m.backlogPane(width, height), "\n")
			},
			hint: (Model).renderTodoScreenHint,
			keys: (Model).updateTodoScreen,
		},
		statePersona: {
			place:   placePane,
			borrows: true,
			// The profile drafter is a flow rather than a reading and needs the
			// room for the same reason the readings do: the draft it ends on is a
			// whole file.
			lines: func(m Model, width, height int) []string {
				if m.personaScreen == nil {
					return nil
				}
				return strings.Split(m.personaPane(width, height), "\n")
			},
			hint: (Model).renderPersonaHint,
			keys: (Model).updatePersona,
		},

		// The two modes that own the keyboard without drawing a block of their
		// own. The retry countdown is a row of the live tail; the model-list wait
		// draws nothing while the provider is asked what it offers.
		stateRetryWait: {
			place: placeNone,
			keys:  (Model).updateRetryWait,
		},
		stateModelList: {
			place:   placeNone,
			borrows: true,
			answer:  (*Model).answerModelList,
		},
	}
}

// The three overlays the session's state cannot name. Each rides over
// whatever mode the state is in — a child's ask and the agent manager cover
// the panel, the memory prompt replaces the approval card's body — so each
// used to be a hand-written check in the five places a state constant is
// read. They are rows like the rest now; what differs is only how they are
// found, which is coverOverlay rather than the state.
func agentListMode() *mode {
	return &mode{
		place: placePanel,
		lines: panelRows((Model).agentListLines),
		keys:  (Model).updateAgentList,
	}
}

func memoryAskMode() *mode {
	return &mode{
		place: placePanel,
		lines: panelRows((Model).memoryAskLines),
		keys:  (Model).updateMemoryAsk,
	}
}

// childAskMode is the routed approval of a child agent. It is built per ask
// rather than declared once because the ask is the row's subject: the card
// draws it and the keys answer it.
func childAskMode(ask *subagent.Ask) *mode {
	return &mode{
		place: placePanel,
		lines: panelRows(func(m Model) []string { return m.childAskPanelLines(ask) }),
		keys: func(m Model, key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
			return m.updateChildAsk(key, ask)
		},
	}
}

// panelRows adapts a mode whose rows are measured against a width it reads
// off the session itself. Every panel overlay is one: the panel's width is
// the content width, and a mode that asked for it as an argument would be
// free to answer at a width the panel is not.
func panelRows(f func(Model) []string) func(Model, int, int) []string {
	return func(m Model, _, _ int) []string { return f(m) }
}

var (
	overlayOnce  sync.Once
	overlayTable map[state]*mode
)

// overlayFor is the row for a state, or nil when the state is the session's
// own turn rather than a mode over it.
//
// The table is built on first use rather than at initialisation. Its rows
// name the session's own methods, and every one of those eventually asks the
// register a question back — a mode's rows are measured against a width that
// depends on which mode is up — so a package-level table is an initialisation
// cycle the compiler refuses.
func overlayFor(s state) *mode {
	overlayOnce.Do(func() { overlayTable = buildOverlays() })
	return overlayTable[s]
}

// coverOverlay is the mode covering whatever the state is showing: the agent
// manager, a child's routed ask, or nothing. Both are found on the model
// rather than in the state because both can be up over any of it — children
// only exist while the parent's turn is in flight, which is exactly when the
// state is busy saying something else.
func (m Model) coverOverlay() *mode {
	if m.agentList != nil {
		return agentListMode()
	}
	if ask := m.activeChildAsk(); ask != nil {
		return childAskMode(ask)
	}
	return nil
}

// panelCovered reports that a cover overlay owns the panel's rows whatever
// the state under it was showing. It is a question of its own rather than
// "coverOverlay returned something" because the frame holds a cover back from
// drawing without handing the rows to what is under it: a manager the frame
// is covering still takes the panel away from the completion menu below.
func (m Model) panelCovered() bool {
	if m.agentList != nil {
		return true
	}
	return m.activeChildAsk() != nil && !m.decisionUngated()
}

// askOverlay is the memory prompt when one is open. It replaces the approval
// card's body rather than covering the panel, so it is resolved where the
// card's keys and rows are and nowhere else.
func (m Model) askOverlay() overlay {
	if m.memoryAsk != nil {
		return memoryAskMode()
	}
	return nil
}

// routeOverlay hands a key to a mode and carries out what the mode asked
// for, answering the quit chord ahead of it for every mode that does not
// answer that itself. The order is fixed here — the surface closes, then the
// row goes in the transcript, then the work starts — so that a mode which
// only has to say what it decided cannot get the order wrong, and so that
// changing the order is one edit rather than one per mode.
func (m Model) routeOverlay(o *mode, key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if o.ownsQuit {
		return m.applyOverlay(o, key)
	}
	return m.surfaceKey(key, func(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
		return m.applyOverlay(o, k)
	})
}

// applyOverlay is routeOverlay without the quit chord in front of it: the
// mode answers the key and the host carries out what it asked for. It is
// what a key that is already an answer to the card reaches the mode
// through — the chord that denies a decision, and a click on one of its
// keys (interrupt.go).
func (m Model) applyOverlay(o *mode, key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	next, act := o.Update(m, key)
	if act.close {
		next.leaveSurface()
		next.syncViewport()
	}
	if act.note != "" {
		noted, cmd := next.systemNotice(act.note)
		return noted, tea.Batch(act.run, cmd)
	}
	return next, act.run
}

// panelOverlay is the mode that owns the bottom panel: the state's own row,
// when it draws into the panel at all. A pane overlay draws over the
// transcript and leaves the panel its one-line hint; the two modes that draw
// nothing leave it the draft box.
func (m Model) panelOverlay() overlay {
	o := overlayFor(m.state)
	if o == nil || o.lines == nil {
		return nil
	}
	switch o.place {
	case placePanel, placeFloating:
		return o
	}
	return nil
}
