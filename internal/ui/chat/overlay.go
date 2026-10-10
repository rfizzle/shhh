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
// The row also carries what the rest of the session used to keep a table of
// its own for: whether the mode stands over the inspector rail
// (inspectorHidden), whether the pointer can still select the transcript
// under it (selectableSurface), the slash command that opens it, by name —
// the command itself, with its completion row and its /help paragraph, is a
// row of the command table (command.go) — and the rail block whose heading is
// its door (railDoors). Those readers derive from the register through the
// one lazy accessor below, so a screen is its own file plus one row here, not
// one row in each of them.
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
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
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
	// nothing else: two of them are where a decision card's own [v] goes, so
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
	// keyList names, by its handle, the register row (internal/ui/keys) whose
	// keys `?` lists over this mode, and answers false where `?` is not a key
	// right now — a field on the card has the keyboard, and there it is a
	// character. nil is a mode that answers `?` itself or never takes it as a
	// key (keylist.go).
	keyList func(m Model) (keys.SurfaceID, bool)
	// hidesRail reports that the mode stands over the inspector rail, so the
	// surface does not split while it is up (inspectorHidden). Every pane
	// overlay but the two staged-attachment viewers does, and so does every
	// card that fills the panel rather than riding above the frame.
	hidesRail bool
	// noSelection reports that the pointer is over something other than the
	// plain transcript while the mode is up — a viewer with its own
	// scrolling, or reading mode's gutter — so a drag selects nothing
	// (selectableSurface).
	noSelection bool
	// command names the slash command whose job is opening this mode. The
	// command is a row of the command table (command.go), which is where its
	// completion row, its /help paragraph and what typing it does are
	// declared. "" is a mode no command of its own opens.
	command string
	// commandArgs are the words after command that open this mode, for a
	// command whose other forms do other things: `/gate flakes` is the
	// flakes screen, and `/gate run` is not. nil is the command alone.
	commandArgs []string
	// door is the rail block whose heading and fold marker open this mode
	// (railclick.go). nil is a mode the rail has no door to.
	door *surfaceDoor
	// doorCommand is the command a click on door runs, where it is not
	// command: the session's whole changeset read in review mode is /diff's
	// bare form, and /review is a turn's. "" is command's own (helpKeyRows).
	doorCommand string
	// doorAlso are further blocks whose heading and fold marker open this
	// mode through the same door. A block belongs here only where it is never
	// on the rail beside door's own, so the opener can tell from the session
	// alone which of them the reader was looking at.
	doorAlso []string
	// holds reports that the mode's own state lives in the session's
	// heldScreens under this row's state while it is up, rather than as a
	// field of the Model. It is the screens built once per opening and
	// nothing else — one pointer each, with no return state or confirm of
	// its own beside it.
	holds bool
}

// heldScreens is the state of every screen a register row holds, keyed by
// that row's state (mode.holds). It exists so a screen joining the register
// adds a row and not a field of the Model: the Model is copied on every
// message and every field is one more thing the session boundary has to
// remember to reset, so the screens are one field and one reset.
//
// It is written copy-on-write. A Model is a value and a map is shared by
// every copy of one, so a delete made on a copy — paint's, or the next model
// an Update builds — would reach every other copy. with and without hand back
// a new map instead, which gives each Model value the snapshot it was built
// with, exactly as the pointer fields this replaced did. The screens
// themselves are pointers, and what is written through one is shared the way
// it always was.
type heldScreens map[state]any

// with is the set with s holding v.
func (h heldScreens) with(s state, v any) heldScreens {
	next := make(heldScreens, len(h)+1)
	for k, held := range h {
		next[k] = held
	}
	next[s] = v
	return next
}

// without is the set with nothing held under s.
func (h heldScreens) without(s state) heldScreens {
	if _, ok := h[s]; !ok {
		return h
	}
	next := make(heldScreens, len(h))
	for k, held := range h {
		if k != s {
			next[k] = held
		}
	}
	return next
}

// keeping is the set with nothing held but what s holds: the session
// boundary's one reset (startNewSession). The surface that has the screen
// while the boundary is crossed keeps its state — a backlog sprint crosses it
// between two items while the reader may be looking at one of these — and
// every other screen goes.
func (h heldScreens) keeping(s state) heldScreens {
	if v, ok := h[s]; ok {
		return heldScreens{s: v}
	}
	return nil
}

// heldAs is the screen held under s, or nil while none is.
func heldAs[T any](h heldScreens, s state) *T {
	v, _ := h[s].(*T)
	return v
}

func (h heldScreens) contextScreen() *components.ContextScreen {
	return heldAs[components.ContextScreen](h, stateContext)
}

func (h heldScreens) sources() *components.SourcesScreen {
	return heldAs[components.SourcesScreen](h, stateSources)
}

func (h heldScreens) steps() *components.StepsScreen {
	return heldAs[components.StepsScreen](h, stateSteps)
}

func (h heldScreens) readings() *components.ReadingsScreen {
	return heldAs[components.ReadingsScreen](h, stateReadings)
}

func (h heldScreens) turns() *components.TurnsScreen {
	return heldAs[components.TurnsScreen](h, stateTurns)
}

func (h heldScreens) alerts() *components.AlertsScreen {
	return heldAs[components.AlertsScreen](h, stateAlerts)
}

func (h heldScreens) flakes() *components.FlakesScreen {
	return heldAs[components.FlakesScreen](h, stateFlakes)
}

func (h heldScreens) spend() *components.SpendScreen {
	return heldAs[components.SpendScreen](h, stateSpend)
}

func (h heldScreens) tools() *components.ToolsScreen {
	return heldAs[components.ToolsScreen](h, stateTools)
}

func (h heldScreens) keyPopup() *keyPopup {
	return heldAs[keyPopup](h, stateKeyPopup)
}

func (h heldScreens) safety() *components.SafetyScreen {
	return heldAs[components.SafetyScreen](h, stateSafety)
}

func (h heldScreens) notes() *components.NotesScreen {
	return heldAs[components.NotesScreen](h, stateNotes)
}

func (h heldScreens) backlog() *components.BacklogScreen {
	return heldAs[components.BacklogScreen](h, stateBacklog)
}

func (h heldScreens) config() *ConfigSession {
	return heldAs[ConfigSession](h, stateConfig)
}

// surfaceDoor is a register row's rail door: the block whose heading opens
// the mode, and how it opens, shows and closes.
type surfaceDoor struct {
	block string
	railDoor
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
		// An answer that did not finish the mode can still leave a row, the
		// way a settings write does while its screen stays up.
		_, act := o.answer(&m, key)
		return m, act
	}
	next, cmd := o.keys(m, key)
	if mm, ok := next.(Model); ok {
		return mm, overlayAction{run: cmd}
	}
	return m, overlayAction{run: cmd}
}

// buildOverlays is the register. One row per mode, and adding a mode is this
// row plus the mode's own file — except a pane screen, which is a row of the
// table its size passes in (screens.go) and joins here as one entry of it.
func buildOverlays(screens []screenSpec) map[state]*mode {
	rows := map[state]*mode{
		// The two decision cards. They float while the draft still holds the
		// keyboard and fill the panel once the handover has given it to them, so
		// their placement is read against the frame rather than on its own
		// (resolvePanel). Neither borrows the screen: a decision is a stage of
		// the turn that asked for it.
		stateConfirmRun: {
			place:     placeFloating,
			hidesRail: true,
			lines:     panelRows((Model).confirmPanelLines),
			bound:     (Model).confirmPanelBound,
			cursor:    (Model).confirmCursor,
			keys:      (Model).updateConfirmRun,
			keyList:   (Model).confirmKeyList,
		},
		statePlanApprove: {
			place:     placeFloating,
			hidesRail: true,
			lines:     panelRows((Model).planPanelLines),
			bound:     func(m Model) int { return m.planPanelBound() + m.gatedExtraRows() },
			keys:      (Model).updatePlanApprove,
			keyList:   staticKeyList(keys.OnPlanCard),
		},
		// The third of them: the model's own question (question.go). It
		// takes the confirm card's bound rather than the plan card's
		// headroom, because a question's answers are shorter than a plan's
		// steps and it has no reason to leave the forty per cent
		// (docs/interface/principles.md#one-interaction-panel) — except the
		// free answer that has grown past it, which is why it took the
		// screen (questionPanelBound).
		stateQuestion: {
			place:     placeFloating,
			hidesRail: true,
			lines:     panelRows((Model).questionPanelLines),
			bound:     (Model).questionPanelBound,
			keys:      (Model).updateQuestion,
			keyList:   (Model).questionKeyList,
		},

		// The panel overlays: the cards and selectors that take the draft box's
		// rows out of the transcript.
		statePick: {
			place:     placePanel,
			borrows:   true,
			hidesRail: true,
			lines:     panelRows((Model).pickerLines),
			cursor:    (Model).pickCursor,
			keys:      (Model).updatePick,
		},
		// The key list (keypopup.go): the palette's card over the register,
		// in the palette's panel, with the query line open from the first
		// keystroke. Bare /help is the command that opens it; with words
		// after it /help goes on to write the whole help sheet as a row.
		stateKeyPopup: {
			place:     placePanel,
			holds:     true,
			borrows:   true,
			hidesRail: true,
			lines:     panelRows((Model).keyPopupLines),
			cursor:    (Model).keyPopupCursor,
			keys:      (Model).updateKeyPopup,
			command:   "/help",
		},
		// The card the rewind picker opens once a turn has been taken. It
		// borrows the panel and the keyboard the picker already had, so
		// every letter on it is live (internal/ui/keys/register.go).
		stateRewindScope: {
			place:   placePanel,
			borrows: true,
			lines:   panelRows((Model).rewindScopeLines),
			answer:  (*Model).updateRewindScope,
			keyList: staticKeyList(keys.OnRewindScope),
		},
		stateTodoPropose: {
			place:     placePanel,
			borrows:   true,
			hidesRail: true,
			lines:     panelRows((Model).todoProposeLines),
			answer:    (*Model).answerTodoPropose,
		},
		stateTodoDraft: {
			place:     placePanel,
			borrows:   true,
			hidesRail: true,
			lines:     panelRows((Model).todoDraftLines),
			answer:    (*Model).answerTodoDraft,
		},
		stateTodoGroom: {
			place:   placePanel,
			borrows: true,
			lines:   panelRows((Model).todoGroomLines),
			keys:    (Model).updateTodoGroom,
		},
		statePasteDrop: {
			place:     placePanel,
			borrows:   true,
			hidesRail: true,
			lines:     panelRows((Model).pasteDropLines),
			keys:      (Model).updatePasteDrop,
		},
		stateScaffold: {
			place:     placePanel,
			borrows:   true,
			hidesRail: true,
			lines:     panelRows((Model).scaffoldLines),
			// A decision whose keys were cut off by the panel bound is not one,
			// so the card gets the plan card's headroom the way the pressure card
			// does (scaffold.go).
			bound:   (Model).planPanelBound,
			answer:  (*Model).answerScaffold,
			keyList: staticKeyList(keys.OnScaffold),
		},
		stateProposal: {
			place:     placePanel,
			borrows:   true,
			hidesRail: true,
			lines:     panelRows((Model).proposalLines),
			// The scaffold card's headroom, for the scaffold card's reason: a
			// decision whose keys the panel bound cut off is not one.
			bound:   (Model).planPanelBound,
			answer:  (*Model).answerProposal,
			keyList: proposalKeyList,
		},
		stateSetup: {
			place:     placePanel,
			borrows:   true,
			hidesRail: true,
			lines:     panelRows((Model).setupLines),
			// The scaffold card's headroom, for the scaffold card's reason: a
			// decision whose keys the panel bound cut off is not one.
			bound:   (Model).planPanelBound,
			answer:  (*Model).answerSetup,
			keyList: staticKeyList(keys.OnToolchain),
			command: setupCommandName,
		},
		stateToolchainDraft: {
			place:     placePanel,
			borrows:   true,
			hidesRail: true,
			lines:     panelRows((Model).toolchainDraftLines),
			// The scaffold card's headroom, for the scaffold card's reason: a
			// decision whose keys the panel bound cut off is not one.
			bound:   (Model).planPanelBound,
			answer:  (*Model).answerToolchainDraft,
			keyList: staticKeyList(keys.OnToolchainDraft),
			command: toolchainCommandName,
		},
		stateHandoff: {
			place:     placePanel,
			borrows:   true,
			hidesRail: true,
			lines:     panelRows((Model).handoffLines),
			// The scaffold card's headroom, for the scaffold card's reason: a
			// decision whose keys the panel bound cut off is not one.
			bound:   (Model).planPanelBound,
			answer:  (*Model).answerHandoff,
			keyList: staticKeyList(keys.OnHandoff),
			command: handoffCommandName,
		},
		stateTodoPause: {
			place:     placePanel,
			borrows:   true,
			hidesRail: true,
			lines:     panelRows((Model).todoPauseLines),
			keys:      (Model).updateTodoPause,
		},
		stateUndoConfirm: {
			place:   placePanel,
			borrows: true,
			lines:   panelRows((Model).undoConfirmLines),
			keys:    (Model).updateUndoConfirm,
			keyList: staticKeyList(keys.OnConfirm),
		},
		// The card a line from another session waits on. It borrows the
		// panel and opens only onto an empty draft, so both of its keys are
		// live from the moment it is drawn (inbound.go).
		stateInboundHold: {
			place:   placePanel,
			borrows: true,
			lines:   panelRows((Model).heldLineLines),
			keys:    (Model).updateHeldLine,
			keyList: staticKeyList(keys.OnHeldLine),
		},
		// The queue with the keyboard in it (msgqueue.go). It borrows the panel
		// and never the turn: steering is still delivered at every boundary
		// while it is up, which is why its pointer names a message rather
		// than a row.
		stateQueue: {
			place:   placePanel,
			borrows: true,
			lines:   panelRows((Model).queueLines),
			answer:  (*Model).answerQueue,
			keyList: staticKeyList(keys.OnQueue),
		},
		stateQuitConfirm: {
			place:    placePanel,
			borrows:  true,
			ownsQuit: true,
			lines:    panelRows((Model).quitConfirmLines),
			keys:     (Model).updateQuitConfirm,
			keyList:  staticKeyList(keys.OnConfirm),
		},
		// The commit card and the message behind its edit key. Both borrow
		// the bottom panel, and the card gets the plan card's headroom for
		// the reason every decision surface does: a decision whose keys were
		// cut off by the panel bound is not one.
		stateCommitCard: {
			place:   placePanel,
			borrows: true,
			lines:   panelRows((Model).commitCardLines),
			bound:   (Model).planPanelBound,
			keys:    (Model).updateCommitCard,
			keyList: staticKeyList(keys.OnCommitCard),
		},
		stateCommitMessage: {
			place:   placePanel,
			borrows: true,
			lines:   panelRows((Model).commitMessageLines),
			bound:   (Model).planPanelBound,
			cursor:  (Model).commitCursor,
			keys:    (Model).updateCommitMessage,
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
			// The transcript is drawn through reading mode's gutter, and a
			// cursor column is chrome nobody wants on their clipboard.
			noSelection: true,
			// The reading bar is normally the panel's three rest rows
			// (minPanelHeight); `[?]` grows it into the mode's key register, and
			// the panel pays for it out of the transcript the way every other
			// panel does.
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
			bound:   (Model).planPanelBound,
			keys:    (Model).updatePressure,
			keyList: staticKeyList(keys.OnPressure),
		},

		// The pane overlays: full width, the rail hidden, a one-line hint where
		// the draft box was. Each does its own scrolling inside the rectangle,
		// which is why they are the modes that read the height.
		stateDiffFull: {
			place:         placePane,
			borrows:       true,
			aboveDecision: true,
			hidesRail:     true,
			noSelection:   true,
			lines: func(m Model, width, height int) []string {
				if m.fullDiff == nil {
					return nil
				}
				m.fullDiff.SetSize(width, height)
				return strings.Split(m.fullDiff.View(width), "\n")
			},
			hint:    (Model).renderDiffFullHint,
			keys:    (Model).updateDiffFull,
			command: "/diff",
		},
		stateOutputFull: {
			place:         placePane,
			borrows:       true,
			aboveDecision: true,
			hidesRail:     true,
			noSelection:   true,
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
		// The key list `?` opens over a card and over reading mode
		// (keylist.go). It is above the decision for the reason the viewers
		// are: a card's own `?` opened it, so the reader is inside the card's
		// detail and the handover chord is not what a key means here.
		stateKeyList: {
			place:         placePane,
			borrows:       true,
			aboveDecision: true,
			hidesRail:     true,
			noSelection:   true,
			lines:         (Model).keyListLines,
			hint:          (Model).renderKeyListHint,
			keys:          (Model).updateKeyList,
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
		statePasteView: {
			place:   placePane,
			borrows: true,
			lines:   (Model).pasteReaderLines,
			hint:    (Model).renderPasteReaderHint,
			keys:    (Model).updatePasteReader,
		},
		// The two modes that own the keyboard without drawing a block of their
		// own. The retry countdown is a row of the live tail; the model-list wait
		// draws nothing while the provider is asked what it offers.
		stateRetryWait: {
			place: placeNone,
			keys:  (Model).updateRetryWait,
		},
		stateModelList: {
			place:     placeNone,
			borrows:   true,
			hidesRail: true,
			answer:    (*Model).answerModelList,
		},
	}
	for _, spec := range screens {
		rows[spec.state] = spec.mode()
	}
	return rows
}

// The three overlays the session's state cannot name. Each rides over
// whatever mode the state is in — a child's ask and the agent manager cover
// the panel, the memory prompt replaces the approval card's body — so each
// used to be a hand-written check in the five places a state constant is
// read. They are rows like the rest now; what differs is only how they are
// found, which is coverOverlay rather than the state.
func agentListMode() *mode {
	return &mode{
		place:   placePanel,
		lines:   panelRows((Model).agentListLines),
		keys:    (Model).updateAgentList,
		keyList: (Model).agentListKeyList,
		command: "/agents",
		door:    &surfaceDoor{components.RailAgents, railDoor{Model.openAgentList, agentListShowing, Model.closeAgentList}},
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
		keyList: staticKeyList(keys.OnApprovalCard),
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
	// The tables derived from the register's rows, built in the same Do as
	// the rows themselves: a rail door by the block it opens from.
	registerDoors     map[string]railDoor
	registerDoorNames map[string]bool
)

// overlays is the register, and the one lazy accessor every reader of it —
// the rows and the tables derived from them — goes through.
//
// The table is built on first use rather than at initialisation. Its rows
// name the session's own methods, and every one of those eventually asks the
// register a question back — a mode's rows are measured against a width that
// depends on which mode is up — so a package-level table is an initialisation
// cycle the compiler refuses. A derived table read before the register was
// built would be a nil map instead, which the compiler does not refuse, so
// none of them is ever read except through here.
func overlays() map[state]*mode {
	overlayOnce.Do(func() {
		overlayTable = buildOverlays(chatScreens())
		registerDoors = map[string]railDoor{}
		registerDoorNames = map[string]bool{}
		// The agent manager is a row the state cannot name (coverOverlay),
		// and the only such row with a door.
		rows := []*mode{agentListMode()}
		for _, o := range overlayTable {
			rows = append(rows, o)
		}
		for _, o := range rows {
			if d := o.door; d != nil {
				for _, block := range append([]string{d.block}, o.doorAlso...) {
					registerDoors[block] = d.railDoor
					registerDoorNames[block] = true
				}
			}
		}
	})
	return overlayTable
}

// overlayFor is the row for a state, or nil when the state is the session's
// own turn rather than a mode over it.
func overlayFor(s state) *mode {
	return overlays()[s]
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
	// `?` is answered here, once, for every mode that names a register row,
	// rather than in each card's own handler: it is the one key every
	// surface holding the keyboard answers the same way, and a card that had
	// to remember to is a card that will one day forget
	// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
	if o.keyList != nil && keys.Match(key, keys.Screen.List) {
		if surface, ok := o.keyList(m); ok {
			return m.openKeyList(surface, nil)
		}
	}
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
	case placeFloating:
		// A floating decision owns the panel only where there is no frame for
		// it to ride above: with the frame showing it draws above it and the
		// panel below stays the draft's (renderInterrupt, interrupt.go).
		if m.frameShowing() {
			return nil
		}
		return o
	case placePanel:
		return o
	}
	return nil
}
