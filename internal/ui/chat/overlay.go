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
// under it (selectableSurface), the slash command that opens it with its
// completion row and its /help paragraph (runCommand, slashCommands,
// helpSheet), and the rail block whose heading is its door (railDoors). Those
// readers derive from the register through the one lazy accessor below, so a
// screen is its own file plus one row here, not one row in each of them.
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
	// keyList names the register row (internal/ui/keys) whose keys `?` lists
	// over this mode, and answers "" where `?` is not a key right now — a
	// field on the card has the keyboard, and there it is a character. nil
	// is a mode that answers `?` itself or never takes it as a key
	// (keylist.go).
	keyList func(m Model) string
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
	// command is the slash command whose job is opening this mode: its
	// completion row, its /help paragraph and what typing it does. nil is a
	// mode no command of its own opens.
	command *surfaceCommand
	// door is the rail block whose heading and fold marker open this mode
	// (railclick.go). nil is a mode the rail has no door to.
	door *surfaceDoor
	// holds reports that the mode's own state lives in the session's
	// heldScreens under this row's state while it is up, rather than as a
	// field of the Model. It is the screens built once per opening and
	// nothing else — one pointer each, with no return state or confirm of
	// its own beside it.
	holds bool
}

// surfaceCommand is the slash command a register row declares. The embedded
// row is what the completion menu and /help read; open is what typing it
// does.
type surfaceCommand struct {
	slashCommand
	// bare is a command that opens its surface only when typed alone. With
	// words after it the line goes on through the rest of the dispatch and
	// is answered as any form the session does not know.
	bare bool
	// open carries the command out; parts is the whole line split on
	// whitespace, the command name included.
	open func(m Model, parts []string) (tea.Model, tea.Cmd)
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

func (h heldScreens) spend() *components.SpendScreen {
	return heldAs[components.SpendScreen](h, stateSpend)
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
			keyList:   staticKeyList("the plan card"),
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
			command: &surfaceCommand{
				slashCommand: slashCommand{name: "/help", desc: "every key, by group, to filter; with words, the whole help as a row",
					key: keys.Shown(keys.Draft.KeyList),
					help: `open the key list: every key this session answers, grouped as a keybindings.toml names them and spelled the way they are bound now — type to filter by key or words, ` +
						keys.Bracket(keys.KeyList.Close) + ` or ` + keys.Bracket(keys.Draft.KeyList) + ` closes it, and the draft is as you left it. With words after it (/help keys), the whole sheet — the commands, what happens mid-turn, the keys and the approval policy — is written to the transcript instead`},
				bare: true,
				open: bareOpen(Model.openKeyPopup),
			},
		},
		// The card the rewind picker opens once a turn has been taken. It
		// borrows the panel and the keyboard the picker already had, so
		// every letter on it is live (internal/ui/keys/register.go).
		stateRewindScope: {
			place:   placePanel,
			borrows: true,
			lines:   panelRows((Model).rewindScopeLines),
			answer:  (*Model).updateRewindScope,
			keyList: staticKeyList("the rewind scope card"),
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
			keyList: staticKeyList("the scaffold card"),
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
			keyList: staticKeyList("the toolchain card"),
			// The install writes only into shhh's own directory and never
			// into the conversation, so it is not idle-only: a turn running
			// is when the model finds the tool missing.
			command: &surfaceCommand{
				slashCommand: slashCommand{name: setupCommandName, desc: "install the tools this checkout's toolchain declaration names (asks first)",
					enabled: func(m *Model) bool { return m.setupWired() },
					help:    `install what this checkout's .shhh/toolchain.toml names — the card lists every install line, where the tools land and what the lines may reach before anything runs, and they run contained exactly as the assistant's commands are. The start screen offers it when a declared tool is not on PATH`},
				bare: true,
				open: bareOpen(Model.setupCommand),
			},
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
			keyList: staticKeyList("the inline confirm and the undo confirm"),
		},
		// The card a line from another session waits on. It borrows the
		// panel and opens only onto an empty draft, so both of its keys are
		// live from the moment it is drawn (inbound.go).
		stateInboundHold: {
			place:   placePanel,
			borrows: true,
			lines:   panelRows((Model).heldLineLines),
			keys:    (Model).updateHeldLine,
			keyList: staticKeyList("the held-line card"),
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
			keyList: staticKeyList("the message queue"),
		},
		stateQuitConfirm: {
			place:    placePanel,
			borrows:  true,
			ownsQuit: true,
			lines:    panelRows((Model).quitConfirmLines),
			keys:     (Model).updateQuitConfirm,
			keyList:  staticKeyList("the inline confirm and the undo confirm"),
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
			keyList: staticKeyList("the commit card"),
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
			keyList: staticKeyList("the context-pressure card"),
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
			hint: (Model).renderDiffFullHint,
			keys: (Model).updateDiffFull,
			// Bare, the cumulative session diff; with a path, that one file's,
			// which is the keyboard's way to the door a click on a CHANGES row
			// opens (railclick.go). The argument is a path and not a turn
			// number, because the rail's rows are paths and the two surfaces
			// answer the same question.
			command: &surfaceCommand{
				slashCommand: slashCommand{name: "/diff", args: "[path]", desc: "cumulative session diff, full screen — bare, or one file's",
					enabled:  func(m *Model) bool { return m.changes != nil && m.codingSurfaces() },
					argSpecs: []argSpec{{dynamic: sessionFileArgs, fuzzy: true}},
					help:     `show what this session changed, full screen, or one file's — read from the session's own changeset, so it works outside a git repository`},
				open: func(m Model, parts []string) (tea.Model, tea.Cmd) {
					if len(parts) > 1 {
						return m.openFileDiff(strings.Join(parts[1:], " "))
					}
					return m.openSessionDiff()
				},
			},
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
		stateReview: {
			place:       placePane,
			borrows:     true,
			hidesRail:   true,
			noSelection: true,
			lines: func(m Model, width, height int) []string {
				if m.review == nil {
					return nil
				}
				m.review.SetSize(width, height)
				return strings.Split(m.review.View(width), "\n")
			},
			hint: (Model).renderReviewHint,
			keys: (Model).updateReview,
			// Review mode over a turn's changeset; bare takes the most recent
			// turn that changed anything.
			command: &surfaceCommand{
				slashCommand: slashCommand{name: "/review", args: "[turn]", desc: "review what a turn changed — files, hunks, staging",
					enabled:  func(m *Model) bool { return m.changes != nil && m.codingSurfaces() },
					argSpecs: []argSpec{{dynamic: reviewTurnArgs}},
					help:     `review what a turn changed: file list, hunks and the turn's verdict (bare reviews the last turn that changed anything). Also a turn's changed-files row, clicked or selected and opened with enter. It reads and changes nothing; /undo takes a turn back`},
				open: (Model).reviewCommand,
			},
			// The CHANGES door is /diff's bare form: the session's whole
			// changeset, read in review mode.
			door: &surfaceDoor{components.RailChanges, railDoor{Model.openSessionDiff, reviewShowing, Model.closeReview}},
		},
		stateContext: {
			place:       placePane,
			holds:       true,
			borrows:     true,
			ownsQuit:    true,
			hidesRail:   true,
			noSelection: true,
			lines: func(m Model, width, height int) []string {
				screen := m.screens.contextScreen()
				if screen == nil {
					return nil
				}
				screen.SetSize(width, height)
				return strings.Split(screen.View(width), "\n")
			},
			hint:   (Model).renderContextHint,
			answer: (*Model).answerContext,
			// The occupancy surface reads the conversation and changes nothing
			// in it, so it is not idleOnly: a window filling up mid-turn is
			// exactly when the question gets asked.
			command: &surfaceCommand{
				slashCommand: slashCommand{name: "/context", desc: "the window as a meter, itemised down to the tool",
					help: `the window as a meter, by category, with the tools itemised`},
				bare: true,
				open: bareOpen(Model.openContext),
			},
			door: &surfaceDoor{components.RailContext, railDoor{Model.openContext, contextShowing, Model.closeContextScreen}},
		},
		stateSources: {
			place:       placePane,
			holds:       true,
			borrows:     true,
			hidesRail:   true,
			noSelection: true,
			lines: func(m Model, width, height int) []string {
				screen := m.screens.sources()
				if screen == nil {
					return nil
				}
				screen.SetSize(width, height)
				return strings.Split(screen.View(width), "\n")
			},
			hint: (Model).renderSourcesHint,
			keys: (Model).updateSources,
			// The ledger of what the session read. Like the occupancy surface
			// it reads and changes nothing, so it is not idleOnly: mid-turn is
			// exactly when somebody asks where a claim came from.
			command: &surfaceCommand{
				slashCommand: slashCommand{name: "/sources", desc: "what this session read: every fetch and search, by host",
					enabled: func(m *Model) bool { return m.sourceLedger != nil },
					help:    `what this session read: every fetch and every search, its own and its children's, grouped by host — with the whole page under [enter] where the fetch kept one`},
				bare: true,
				open: bareOpen(Model.openSources),
			},
		},
		stateSteps: {
			place:       placePane,
			holds:       true,
			borrows:     true,
			hidesRail:   true,
			noSelection: true,
			lines: func(m Model, width, height int) []string {
				screen := m.screens.steps()
				if screen == nil {
					return nil
				}
				screen.SetSize(width, height)
				return strings.Split(screen.View(width), "\n")
			},
			hint: (Model).renderStepsHint,
			keys: (Model).updateSteps,
			// The session's whole working list, each step beside what the
			// transcript recorded for it. It reads and changes nothing, so it
			// is not idleOnly: mid-turn is when somebody asks where the agent
			// is (worksteps.go).
			command: &surfaceCommand{
				slashCommand: slashCommand{name: "/steps", desc: "the session's own working list, each step beside what the transcript recorded for it",
					enabled: func(m *Model) bool { return m.codingSurfaces() },
					help:    `the session's own working list on one screen: every step it declared, the paths each said it would touch, which it has marked done and the one it is on — and beside each, the calls the transcript titled for it, or not started where there are none. It reads and changes nothing`},
				bare: true,
				open: bareOpen(Model.openSteps),
			},
			door: &surfaceDoor{components.RailSteps, railDoor{Model.openSteps, stepsShowing, Model.closeStepsScreen}},
		},
		stateReadings: {
			place:       placePane,
			holds:       true,
			borrows:     true,
			hidesRail:   true,
			noSelection: true,
			lines: func(m Model, width, height int) []string {
				screen := m.screens.readings()
				if screen == nil {
					return nil
				}
				screen.SetSize(width, height)
				return strings.Split(screen.View(width), "\n")
			},
			hint: (Model).renderReadingsHint,
			keys: (Model).updateReadings,
			// Every reading the session has taken of its own run. It reads and
			// changes nothing, so it is not idleOnly: mid-turn is when somebody
			// asks what the run has been saying about itself (readings.go).
			command: &surfaceCommand{
				slashCommand: slashCommand{name: "/readings", desc: "every reading the session has taken of its own run, each whole",
					help: `every reading the session has taken of its own run on one screen, newest first: the round, the verdict, the whole reading with its reason and the instruction it was judged against, and whether it steered the turn and whether that steer was taken back. Quiet readings are kept here too. It reads and changes nothing`},
				bare: true,
				open: bareOpen(Model.openReadings),
			},
			door: &surfaceDoor{components.RailSummary, railDoor{Model.openReadings, readingsShowing, Model.closeReadingsScreen}},
		},
		stateTurns: {
			place:       placePane,
			holds:       true,
			borrows:     true,
			hidesRail:   true,
			noSelection: true,
			lines: func(m Model, width, height int) []string {
				screen := m.screens.turns()
				if screen == nil {
					return nil
				}
				screen.SetSize(width, height)
				return strings.Split(screen.View(width), "\n")
			},
			hint: (Model).renderTurnsHint,
			keys: (Model).updateTurns,
			// Every turn the session has run, as its close row reads it. It
			// reads and changes nothing, so it is not idleOnly: mid-turn is when
			// somebody asks what the turns before this one cost (turns.go).
			command: &surfaceCommand{
				slashCommand: slashCommand{name: "/turns", desc: "every turn the session has run, as its close row reads it, each one's review a key away",
					help: `every turn the session has run on one screen, newest first: how it ended, its steps, tools, time and spend, what it changed, its commit and its checks' verdict — the figures its close row drew, beside the close itself — with the turn in flight on top. [enter] opens a turn's review where it changed files. A turn from an ended sitting shows its files and says its figures were not kept. It reads and changes nothing`},
				bare: true,
				open: bareOpen(Model.openTurns),
			},
			door: &surfaceDoor{components.RailTurn, railDoor{Model.openTurns, turnsShowing, Model.closeTurnsScreen}},
		},
		stateAlerts: {
			place:       placePane,
			holds:       true,
			borrows:     true,
			hidesRail:   true,
			noSelection: true,
			lines: func(m Model, width, height int) []string {
				screen := m.screens.alerts()
				if screen == nil {
					return nil
				}
				screen.SetSize(width, height)
				return strings.Split(screen.View(width), "\n")
			},
			hint: (Model).renderAlertsHint,
			keys: (Model).updateAlerts,
			// Every alert the session has had, as the rail reads it. It reads
			// and changes nothing, so it is not idleOnly: mid-turn is when
			// somebody asks what has been failing and what fixed it (alerts.go).
			command: &surfaceCommand{
				slashCommand: slashCommand{name: "/alerts", desc: "every command this session broke, standing and superseded, each run a key away",
					help: `every alert the session has had on one screen, standing first and then superseded, newest first: the command, its last outcome, its runs, the turn it first broke in and what answered it — a clean run or the quality gate passing, with the turn. [enter] shows each run: its turn, how it ended, how long it took and the evidence id its output was kept under where it was cut. It reads the rail's own alerts and changes nothing`},
				bare: true,
				open: bareOpen(Model.openAlerts),
			},
			door: &surfaceDoor{components.RailAlerts, railDoor{Model.openAlerts, alertsShowing, Model.closeAlertsScreen}},
		},
		stateSpend: {
			place:       placePane,
			holds:       true,
			borrows:     true,
			hidesRail:   true,
			noSelection: true,
			lines: func(m Model, width, height int) []string {
				screen := m.screens.spend()
				if screen == nil {
					return nil
				}
				screen.SetSize(width, height)
				return strings.Split(screen.View(width), "\n")
			},
			hint: (Model).renderStatsHint,
			keys: (Model).updateStats,
			// The session's whole bill, as the rail's SPEND block reads it. It
			// reads and changes nothing, so it is not idleOnly: mid-turn is when
			// somebody asks what the run is costing (stats.go). While attached
			// to a child, /stats is the child's own answer and never reaches
			// this row (attach.go).
			command: &surfaceCommand{
				slashCommand: slashCommand{name: "/stats", desc: "the session's whole bill: by model, by child and by turn",
					help: `the session's whole bill on one screen, as the rail's SPEND block reads it: the session total with the kinds of request that make it up, each model's share with its own kinds and what the children on it cost, each child's share by name, and each turn's cost as its close row states it. [enter] on a turn opens it on the turns screen. What the context window is occupied by is /context. While attached to an agent, /stats is that agent's own. It reads and changes nothing`},
				bare: true,
				open: bareOpen(Model.openStats),
			},
			door: &surfaceDoor{components.RailSpend, railDoor{Model.openStats, statsShowing, Model.closeStatsScreen}},
		},
		stateSafety: {
			place:       placePane,
			holds:       true,
			borrows:     true,
			hidesRail:   true,
			noSelection: true,
			lines:       (Model).safetyLines,
			hint:        (Model).renderSafetyHint,
			keys:        (Model).updateSafety,
			// The session's whole boundary. It reads and changes nothing, so it
			// is not idleOnly: a turn that just asked for something is when a
			// person wants to see what it may do (safety.go).
			command: &surfaceCommand{
				slashCommand: slashCommand{name: "/safety", aliases: []string{"/security"}, desc: "everything this session may do, and what fences it, in one place",
					help: `the session's whole boundary on one screen (also /security): the mode and grants, where it may write, what contains its commands, the hosts it reaches, what the checkout was let load, its servers, secrets and tools — each section naming the command that changes it. It reads and changes nothing`},
				bare: true,
				open: bareOpen(Model.openSafety),
			},
		},
		stateNotes: {
			place:       placePane,
			holds:       true,
			borrows:     true,
			hidesRail:   true,
			noSelection: true,
			lines: func(m Model, width, height int) []string {
				screen := m.screens.notes()
				if screen == nil {
					return nil
				}
				screen.SetSize(width, height)
				return strings.Split(screen.View(width), "\n")
			},
			hint: (Model).renderNotesHint,
			keys: (Model).updateNotes,
			command: &surfaceCommand{
				slashCommand: slashCommand{name: "/notes", args: "[drop <n>|clear]", desc: "the session's shared notebook, as a screen: what the agents wrote for each other",
					enabled: func(m *Model) bool { return m.notebook != nil },
					argSpecs: staticArgs(
						argOption{"drop", "remove one note by number"},
						argOption{"clear", "empty the notebook, after confirming it"},
					),
					help: `the session's shared notebook — what the agents wrote for each other, and what a backlog run wrote up, listed by author. Dropping is yours alone: drop <n> removes one, clear empties it`},
				open: func(m Model, parts []string) (tea.Model, tea.Cmd) { return m.notesCommand(parts[1:]) },
			},
		},
		stateBacklog: {
			place:       placePane,
			holds:       true,
			borrows:     true,
			hidesRail:   true,
			noSelection: true,
			lines: func(m Model, width, height int) []string {
				if m.screens.backlog() == nil {
					return nil
				}
				return strings.Split(m.backlogPane(width, height), "\n")
			},
			hint: (Model).renderTodoScreenHint,
			keys: (Model).updateTodoScreen,
			// Bare /todo opens the backlog screen; the subcommands are textual,
			// and edit hands the item file to the editor.
			command: &surfaceCommand{
				slashCommand: slashCommand{name: "/todo", args: "[show|edit|new|add|groom|block|open|done|drop|run|sprint|status|stop]", desc: "the project's backlog (bare /todo opens the screen)",
					enabled: func(m *Model) bool { return m.todosEnabled() },
					argSpecs: []argSpec{
						{options: []argOption{
							{"show", "print an item"},
							{"edit", "open an item in your editor"},
							{"add", "read this session into items, or add one from a sentence"},
							{"groom", "read an item against the tree and propose the corrections"},
							{"block", "mark an item blocked, with why"},
							{"open", "reopen a blocked item"},
							{"done", "archive an item"},
							{"drop", "delete an item outright"},
							{"run", "work an item through to a commit (bare run takes the next ready one)"},
							{"sprint", "the set being worked: bare shows it, plan proposes one"},
							{"status", "where the run is"},
							{"stop", "abandon the run; the item goes back to open"},
						}},
						{after: []string{"show", "edit", "groom", "block", "open", "done", "drop", "run"}, dynamic: todoSlugArgs, fuzzy: true},
					},
					help: `the project's backlog: bare opens a picker · show|edit <slug> · add (reads this session into proposed items you accept or drop) · add <text> · block <slug> [why] · open|done|drop <slug> · new <text> · groom <slug> (reads an item against the tree and proposes the corrections) · run [slug|--next] works an item through its profile's run · sprint · status · stop`},
				open: (Model).todoCommand,
			},
			door: &surfaceDoor{components.RailTodo, railDoor{Model.openTodoDoor, backlogShowing, Model.closeTodoScreen}},
		},
		// The one pane overlay that can write a file. It writes on `[w]`
		// alone and asks before it walks away from anything staged, which is
		// the screen's own rule rather than the register's (config.go).
		stateConfig: {
			place:       placePane,
			holds:       true,
			borrows:     true,
			hidesRail:   true,
			noSelection: true,
			lines:       (Model).configScreenLines,
			hint:        (Model).renderConfigHint,
			answer:      (*Model).answerConfig,
			// The whole settings file, where /ui is the handful of its keys a
			// session flips often enough to have a word for. Not idleOnly: the
			// settings a person wants to change mid-session are the ones the
			// running turn just made them think about, and nothing the screen
			// stages reaches the file until [w] — which writes the user's own
			// config file and not the tree the turn is working in.
			command: &surfaceCommand{
				slashCommand: slashCommand{name: "/config", desc: "every setting, where its value came from, and what changing it costs",
					enabled: func(m *Model) bool { return m.openConfig != nil },
					help:    `every setting, staged: what each one is set to, where that value came from, and what [enter] offers instead of typing it. Nothing reaches your config file until [w], and the way out asks before discarding what is staged. The running session keeps the settings it started on`},
				bare: true,
				open: bareOpen(Model.openConfigScreen),
			},
		},
		statePersona: {
			place:     placePane,
			borrows:   true,
			hidesRail: true,
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
			place:     placeNone,
			borrows:   true,
			hidesRail: true,
			answer:    (*Model).answerModelList,
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
		place:   placePanel,
		lines:   panelRows((Model).agentListLines),
		keys:    (Model).updateAgentList,
		keyList: (Model).agentListKeyList,
		command: &surfaceCommand{
			slashCommand: slashCommand{name: "/agents", args: "[new [brief]]", desc: "agent manager; new drafts a profile from a sentence",
				key: keys.Shown(keys.Draft.Agents),
				// The manager opens on a session that can spawn agents or draft
				// a profile for one. Drafting alone is enough: the list is where
				// the offer to draft lives (attach.go).
				enabled:  func(m *Model) bool { return m.subagents != nil || m.personas.Enabled },
				argSpecs: staticArgs(argOption{"new", "draft an agent profile with the model's help"}),
				help: `agent manager: attach, answer, steer, retry, cancel and kill sub-agents from the row each is on (also ` + keys.Bracket(keys.Draft.Agents) + `)
new [brief]   draft an agent profile from a sentence with the model's help: answer its questions if it has any, then keep, refine or discard the draft on a card. Bare offers starting points`},
			open: func(m Model, parts []string) (tea.Model, tea.Cmd) {
				if len(parts) > 1 && parts[1] == "new" {
					return m.startPersona(strings.Join(parts[2:], " "))
				}
				return m.openAgentList()
			},
		},
		door: &surfaceDoor{components.RailAgents, railDoor{Model.openAgentList, agentListShowing, Model.closeAgentList}},
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
		keyList: staticKeyList("the approval card and the /run confirm"),
	}
}

// panelRows adapts a mode whose rows are measured against a width it reads
// off the session itself. Every panel overlay is one: the panel's width is
// the content width, and a mode that asked for it as an argument would be
// free to answer at a width the panel is not.
func panelRows(f func(Model) []string) func(Model, int, int) []string {
	return func(m Model, _, _ int) []string { return f(m) }
}

// bareOpen adapts a surface's opener to a command that takes no words.
func bareOpen(open func(Model) (tea.Model, tea.Cmd)) func(Model, []string) (tea.Model, tea.Cmd) {
	return func(m Model, _ []string) (tea.Model, tea.Cmd) { return open(m) }
}

var (
	overlayOnce  sync.Once
	overlayTable map[state]*mode
	// The tables derived from the register's rows, built in the same Do as
	// the rows themselves: a command by every name and alias it answers to,
	// and a rail door by the block it opens from.
	registerCommands  map[string]*surfaceCommand
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
		overlayTable = buildOverlays()
		registerCommands = map[string]*surfaceCommand{}
		registerDoors = map[string]railDoor{}
		registerDoorNames = map[string]bool{}
		// The agent manager is a row the state cannot name (coverOverlay),
		// and the only such row with a command and a door.
		rows := []*mode{agentListMode()}
		for _, o := range overlayTable {
			rows = append(rows, o)
		}
		for _, o := range rows {
			if c := o.command; c != nil {
				registerCommands[c.name] = c
				for _, a := range c.aliases {
					registerCommands[a] = c
				}
			}
			if d := o.door; d != nil {
				registerDoors[d.block] = d.railDoor
				registerDoorNames[d.block] = true
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

// registeredCommand is the command a register row declares under this name
// or one of its aliases.
func registeredCommand(name string) (*surfaceCommand, bool) {
	overlays()
	c, ok := registerCommands[name]
	return c, ok
}

// registeredSlash is a register row's command as the completion registry
// lists it (complete.go), which is where the menu's order is kept. A name no
// row declares is a registry that has drifted from the register, and there
// is no row to show for it.
func registeredSlash(name string) slashCommand {
	c, ok := registeredCommand(name)
	if !ok {
		panic("no register row declares the command " + name)
	}
	return c.slashCommand
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
		if surface := o.keyList(m); surface != "" {
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
