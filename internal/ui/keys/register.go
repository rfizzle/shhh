package keys

// The register of keyed surfaces, as data.
//
// It is an audit: every surface that offers a bare letter, which of the two
// positions it is in, and how it gets the keyboard. It was written as a
// markdown table, which is the form a rule takes when nobody can check it —
// and the whole point of it is that "a rule nobody can check against a list
// is a rule each new surface gets to rediscover".
//
// So the list is here, beside the bindings, and the tests check the code
// against it: every binding belongs to exactly one surface, no surface
// answers one keystroke with two bindings, no surface that is not a takeover
// offers a bare letter without naming the key that hands the keyboard over,
// and nothing the input itself answers is a keystroke a sentence can
// produce.
//
// It is also what the key list renders and what /help's key section is built
// from, so the register a reader is shown is the register the handlers use.
// The input's row is read off its offers (input.go), which carry the
// paragraph /help keeps beside each key.
//
// Every row is written here and only here, keyed by a handle, and the chat
// session's modes name the surface they stand in by that handle rather than by
// repeating its name. It cannot be the other way round: the keymap loader, the
// reserved-key check, the doctor's keymap report and the generated keymap
// reference all read this register from programs that never link the chat
// session, so a register filled in from the session's table would be empty in
// exactly the programs that check a keymap against it.
// See docs/architecture.md#a-surface-declares-itself-once-in-the-key-register.

// Position is where a surface stands relative to the keyboard. The register
// allows two and says there is no third; Home is the thing those two are
// defined against, not a third position.
type Position int

const (
	// Home is the framed input. It holds the keyboard whenever nothing
	// has taken it, which is most of the time, and it is why every key in
	// this position is a chord but enter and esc — a bare key here is a
	// letter of the sentence being typed.
	Home Position = iota
	// Takeover holds the keyboard exclusively. Its state is routed before
	// the input sees a key and the input is not live while it is up, so its
	// letters are live because nothing else is listening.
	Takeover
	// Beside is a surface that does not hold the keyboard: it renders its
	// keys grey and offers the one key that hands the keyboard over, live,
	// next to them.
	Beside
)

func (p Position) String() string {
	switch p {
	case Home:
		return "holds the keyboard"
	case Takeover:
		return "takeover"
	default:
		return "beside a live draft"
	}
}

// Surface is one row of the register of keyed surfaces
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
type Surface struct {
	// Name is what the surface is called in the interface documentation,
	// lowercase, because it is read inside a sentence as often as above a
	// list.
	Name string
	// Section is the documentation that is normative for it.
	Section string
	// Position is where it stands relative to the keyboard.
	Position Position
	// Reached is how it gets the keyboard, in words — the last column of
	// that register.
	Reached string
	// Bindings are its keys, in the order it offers them.
	Bindings []Binding
	// Typed reports a takeover that is typed into as a whole — the editor
	// pane. It holds the keyboard the way any takeover does and keeps the
	// draft's rule while it does: every keystroke a sentence produces is
	// text there, so every key it answers is a chord but enter and esc, and
	// esc hands the keyboard back
	// (docs/interface/principles.md#a-surface-typed-into-keeps-the-drafts-rule).
	Typed bool
}

// SurfaceID is one row of the register, by the handle the code that draws the
// surface holds. Their order is the register's: the input first, then what
// takes the keyboard from it, then the rows that wait for it.
type SurfaceID int

const (
	OnInput SurfaceID = iota
	OnHistorySearch
	OnReading
	OnTranscriptSearch
	OnStagedStrip
	OnQueue
	OnStagedPaste
	OnContext
	OnSources
	OnNotes
	OnBacklog
	OnSprintPlan
	OnTranscriptRow
	OnCommitCard
	OnRewindScope
	OnCommitMessage
	OnApprovalCard
	OnApprovalNote
	OnApprovalCommand
	OnApprovalQueue
	OnApprovalGrant
	OnPlanCard
	OnQuestion
	OnYesNo
	OnScaffold
	OnToolchain
	OnToolchainDraft
	OnHandoff
	OnProposal
	OnConfirm
	OnHeldLine
	OnSelector
	OnChatPicker
	OnRewindPicker
	OnSelectorQuery
	OnPalette
	OnWholeKeyList
	OnReview
	OnAgentManager
	OnProfileDrafter
	OnProfileDraft
	OnEditor
	OnEditorLeave
	OnDiff
	OnOutput
	OnPreview
	OnKeyList
	OnRetry
	OnPressure
	OnMaskedKey

	// surfaceCount is how many rows the register has; it is not a surface.
	surfaceCount

	// NoSurface is the handle of a screen the register has no row for.
	NoSurface SurfaceID = -1
)

// Surfaces is the register in the order a reader meets them, which is the
// order of their handles. It is built from the bindings each time it is read,
// because a keymap file moves those after the program starts.
func Surfaces() []Surface {
	rows := register()
	return rows[:]
}

// Surface is the register's row for this handle, built from the bindings as
// they stand now.
func (id SurfaceID) Surface() Surface { return register()[id] }

// register is every row, keyed by its handle, so a row cannot be listed twice
// and a handle without a row is a zero Surface the tests refuse.
func register() [surfaceCount]Surface {
	return [surfaceCount]Surface{
		OnInput: {
			Name:     "the input",
			Section:  "docs/interface/surfaces.md#the-input-frame",
			Position: Home,
			Reached:  "it has the keyboard unless something has taken it",
			// Read off the offers (input.go), where each key is declared with
			// its paragraph, so the input cannot answer a key /help has no
			// words for.
			Bindings: inputBindings(),
		},
		OnHistorySearch: {
			// The reverse search over the input ring. It is typed into from
			// the first keystroke, like the palette: every letter filters, so
			// the only keys on the row are the three that do not.
			Name:     "the input history search",
			Section:  "docs/interface/surfaces.md#the-input-frame",
			Position: Takeover,
			Reached:  Shown(Draft.HistorySearch),
			Bindings: []Binding{Search.Older, Search.Keep, Search.Cancel},
		},
		OnReading: {
			Name:     "reading mode",
			Section:  "docs/interface/surfaces.md#reading-mode",
			Position: Takeover,
			Reached:  Shown(Draft.Reading),
			Bindings: Reading.All(),
		},
		OnTranscriptSearch: {
			// Reading mode with its query row open, which is a row of its
			// own here for the reason the selector's is: a surface being
			// typed into keeps every letter as text, so none of the mode's
			// bare letters are live while it is up and the two keys that are
			// not letters are the whole of what it answers.
			Name:     "the transcript search",
			Section:  "docs/interface/surfaces.md#reading-mode",
			Position: Takeover,
			Reached:  Bracket(Reading.Search) + " in reading mode",
			Bindings: Find.All(),
		},
		OnStagedStrip: {
			// Reading mode with its cursor on the staged strip. A row of its
			// own because the mode's enter and esc mean something else there:
			// enter opens the chip rather than a row, and esc goes back to the
			// draft from wherever the cursor stood.
			Name:     "the staged strip",
			Section:  "docs/interface/surfaces.md#a-staged-attachment",
			Position: Takeover,
			Reached:  "reading mode's last row, below the transcript",
			Bindings: Staged.All(),
		},
		OnQueue: {
			// What waits for the turn, once the keyboard has moved into it.
			// A takeover: the draft keeps its sentence but not the keyboard,
			// so the queue's letters are live while it is up, and one of them
			// cancels a message.
			Name:     "the message queue",
			Section:  "docs/interface/surfaces.md#the-input-frame",
			Position: Takeover,
			Reached:  Shown(Draft.Queued),
			Bindings: append(Queue.All(), Screen.List),
		},
		OnStagedPaste: {
			// The staged paste, opened from its chip on the staged strip or
			// by name. A takeover: it scrolls, and one of its keys drops the
			// paste, so nothing under it may be answering keys at the same
			// time.
			Name:     "the staged paste",
			Section:  "docs/interface/surfaces.md#the-input-frame",
			Position: Takeover,
			Reached:  "its chip on the staged strip, or /paste show",
			Bindings: Paste.All(),
		},
		OnContext: {
			Name:     "the context surface",
			Section:  "docs/interface/surfaces.md#the-context-surface",
			Position: Takeover,
			Reached:  "/context",
			Bindings: Context.All(),
		},
		OnSources: {
			// What the session read, as a screen: the ledger on the left
			// grouped by host, the row the pointer is on beside it, and the
			// page itself where the fetch was long enough to leave one.
			Name:     "the sources screen",
			Section:  "docs/interface/surfaces.md#the-supporting-screens",
			Position: Takeover,
			Reached:  "/sources",
			Bindings: Sources.All(),
		},
		OnNotes: {
			// The session's shared notebook, as a screen: the notes on the
			// left grouped under the agent that wrote each one, the note the
			// pointer is on beside it, and the one key that takes something
			// out of a store every agent can add to.
			Name:     "the notes screen",
			Section:  "docs/interface/surfaces.md#the-supporting-screens",
			Position: Takeover,
			Reached:  "/notes",
			Bindings: Notes.All(),
		},
		OnBacklog: {
			// The backlog as a screen rather than as a command that prints:
			// the list on the left and the item's own prose on the right,
			// with the keys that would otherwise be typed as verbs.
			Name:     "the backlog screen",
			Section:  "docs/interface/surfaces.md#the-backlog-screen",
			Position: Takeover,
			Reached:  Shown(Draft.Backlog) + ", or /todo",
			Bindings: append(Backlog.All(), Query.Rub),
		},
		OnSprintPlan: {
			// The sprint plan card, on that screen's sprint tab. It is a
			// surface of its own rather than a mode of the screen because
			// it answers every keystroke while it is up: the list under it
			// is drawn and not live, which is what lets the card keep the
			// pair the screen had to break.
			Name:     "the sprint plan card",
			Section:  "docs/interface/surfaces.md#the-sprint-board",
			Position: Takeover,
			Reached:  "/todo sprint plan",
			Bindings: Sprint.All(),
		},
		OnTranscriptRow: {
			Name:     "a transcript row's own offers",
			Section:  "docs/interface/surfaces.md#the-turns-close, docs/interface/surfaces.md#the-recovery-row",
			Position: Beside,
			Reached:  Shown(Draft.Answer) + " on a selected recovery or round-limit row, or " + Shown(Draft.Reading) + " and the cursor on the row",
			Bindings: []Binding{
				Row.Withdraw, Row.Retry, Row.Continue,
				Row.Key, Row.Provider, Row.Rounds, Row.Uncap,
			},
		},
		OnCommitCard: {
			// The card the handover opens on a selected changed-files row.
			// It is a takeover and not a card beside the draft: the key that
			// opened it is the handover itself, so the keyboard has already
			// left the draft and there is nothing more to hand over.
			Name:     "the commit card",
			Section:  "docs/interface/surfaces.md#the-turns-close, docs/capabilities/approvals-and-safety.md#the-writing-half-of-git-is-a-tool-too",
			Position: Takeover,
			Reached:  Shown(Draft.Answer) + " on a selected changed-files row",
			Bindings: append(Commit.All(), Screen.List),
		},
		OnRewindScope: {
			// The card the /rewind picker opens once a turn has been taken.
			// It is a takeover for the commit card's reason: the picker
			// already held the keyboard when the card arrived, so there is
			// nothing to hand over and every letter here is live.
			Name:     "the rewind scope card",
			Section:  "docs/interface/surfaces.md#the-rewind",
			Position: Takeover,
			Reached:  "a turn taken in the /rewind picker",
			Bindings: append(Rewind.All(), Screen.List),
		},
		OnCommitMessage: {
			// The proposed message as a draft, which is a row of its own for
			// the reason the approval card's note field is: a surface being
			// typed into keeps every letter as text, so none of the card's
			// own letters is live under it and the two keys that are not
			// letters are the whole of what it answers.
			Name:     "the commit card's message field",
			Section:  "docs/interface/surfaces.md#the-turns-close",
			Position: Takeover,
			Reached:  Bracket(Commit.Edit) + " on the commit card",
			Bindings: []Binding{Select.Take, Select.Cancel},
		},
		OnApprovalCard: {
			Name:     "the approval card and the /run confirm",
			Section:  "docs/interface/surfaces.md#the-approval-card, docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard",
			Position: Beside,
			Reached:  Shown(Draft.Answer),
			Bindings: []Binding{
				Decision.Allow, Decision.Deny,
				Decision.AllowNoted, Decision.DenyNoted,
				Decision.Always,
				Decision.Batch, Decision.Diff, Decision.DryRun,
				Decision.Explain, Decision.Amend, Agent.Go,
				Decision.ScrollUp, Decision.ScrollDown,
				Decision.PanLeft, Decision.PanRight, Screen.List,
			},
		},
		OnApprovalNote: {
			// A row of its own for the reason the transcript search has one:
			// a surface being typed into keeps every letter as text, so none
			// of the card's answers are live while the field is up and the
			// two keys that are not letters are the whole of what it
			// answers. The words are the field's rather than the selector's
			// — enter sends the sentence with the answer it was opened for,
			// and esc closes the field with that answer still waiting.
			Name:     "the approval card's note field",
			Section:  "docs/interface/surfaces.md#the-approval-card",
			Position: Takeover,
			Reached:  Bracket(Decision.AllowNoted) + " or " + Bracket(Decision.DenyNoted) + " on the card",
			Bindings: []Binding{Select.Take, Select.Cancel},
		},
		OnApprovalCommand: {
			// The command itself, open in a field for the reader to change
			// before it runs. It is a row of its own for the reason the note
			// field's is — a surface being typed into keeps every letter as
			// text — and it is a second row rather than the same one because
			// the two answer the same two keys to different ends: enter here
			// runs a command, and enter there sends a sentence with an
			// answer that was already chosen.
			Name:     "the approval card's command field",
			Section:  "docs/interface/surfaces.md#the-approval-card",
			Position: Takeover,
			Reached:  Bracket(Decision.Amend) + " on a command card",
			Bindings: []Binding{Select.Take, Select.Cancel},
		},
		OnApprovalQueue: {
			// The queue behind the card, opened as the list that answers it.
			// It is a row of its own because it is a selector and answers a
			// selector's keys — it moves, ticks, ticks everything and takes
			// — and because those keys are not the card's: `a` under the
			// card is a session grant and `a` here is all-or-none, which is
			// exactly the kind of collision a second surface on one row
			// would hide.
			Name:     "the approval card's queue list",
			Section:  "docs/interface/surfaces.md#the-approval-card, docs/interface/surfaces.md#selectors",
			Position: Takeover,
			Reached:  Bracket(Decision.Batch) + " on a card with a queue behind it",
			Bindings: []Binding{
				Select.MoveJK, Select.Toggle, Select.All,
				Select.Take, Select.Cancel, Screen.List,
			},
		},
		OnApprovalGrant: {
			// The grants the card can make, open under it. A row of its own
			// for the reason the two fields above have theirs — it holds the
			// keyboard, so none of the card's answers are live while it is
			// up — and a list rather than a field, so the keys are the
			// selector family's: it moves, it takes, it cancels. What enter
			// does here is make a grant and run the act, and what esc does is
			// leave with nothing granted and the decision still waiting.
			Name:     "the approval card's grant list",
			Section:  "docs/interface/surfaces.md#the-approval-card, docs/capabilities/approvals-and-safety.md#a-grant-says-when-it-ends",
			Position: Takeover,
			Reached:  Bracket(Decision.Always) + " on a card that offers a grant",
			Bindings: []Binding{Select.MoveJK, Select.Take, Select.Cancel, Screen.List},
		},
		OnPlanCard: {
			// A row of its own, because the card is a list and answers a
			// list's keys: it moves, takes and cancels the way every
			// selector does, and the two keys it has beyond that are its.
			// It sat under the approval card's row while nothing but the
			// approval card's keys were declared for it.
			Name:     "the plan card",
			Section:  "docs/interface/surfaces.md#selectors, docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard",
			Position: Beside,
			Reached:  Shown(Draft.Answer),
			Bindings: []Binding{
				Select.MoveJK, Select.Take, Plan.Jump, Plan.Save,
				// The session boundary, borrowed from the card that already
				// offers it in the same words: an approved plan can be run
				// here or carried into a conversation with nothing else in
				// it, and both cards are read where the window is the
				// question
				// (docs/capabilities/coding-agent.md#an-approved-plan-is-an-artifact).
				Wait.NewSession,
				// And the same boundary with the execution turn started on
				// the far side of it, in the mode its words name.
				Plan.Implement,
				Select.Cancel, Screen.List,
			},
		},
		OnQuestion: {
			// The model's own question, in the three dressings that are a
			// list: pick one, pick several, and the free answer, which is
			// the same card with the field and no rows above it. It is a
			// list and answers a list's keys, and the note is on tab — the
			// key every selector in the product already answers. `n` is free
			// on the family, but spending it here alone would make this the
			// one selector whose note is not on tab, and moving the note for
			// every selector at once is a change with no cause on this card.
			//
			// Which is what decides the strip: a call carrying several
			// questions draws them as tabs, and the keystroke a tab strip
			// has everywhere else is the one the note is already on. So the
			// strip takes the arrows — see Select.Tab — and `d` takes the
			// marked row's long form to the full screen, which is free here
			// because Select.Alt is not on this row and this card has no
			// default to set.
			Name:     "the question card",
			Section:  "docs/interface/surfaces.md#the-question-card, docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard",
			Position: Beside,
			Reached:  Shown(Draft.Answer),
			Bindings: []Binding{
				Select.MoveJK, Select.Take, Select.Toggle, Select.All,
				Select.Note, Select.Long, Select.Tab, Select.Cancel, Screen.List,
			},
		},
		OnYesNo: {
			// The fourth dressing, which is a row of its own because it is
			// not a list: a yes-or-no is the inline confirm, and its two
			// answers claim the enter the list's take claims. It steps
			// between the questions of one call on the same arrows the list
			// dressings do, and offers no long form because it has no rows
			// for one to be behind.
			Name:     "a yes-or-no question",
			Section:  "docs/interface/surfaces.md#the-question-card, docs/interface/surfaces.md#the-inline-confirm",
			Position: Beside,
			Reached:  Shown(Draft.Answer),
			Bindings: []Binding{Confirm.Yes, Confirm.No, Select.Note, Select.Tab, Screen.List},
		},
		OnScaffold: {
			// The one approval card the reader asks for rather than is
			// handed: it writes a file the session offered to write, so it
			// holds the keyboard from the moment it opens. That is also why
			// its no is Refuse rather than Deny — see the binding.
			Name:     "the scaffold card",
			Section:  "docs/interface/surfaces.md#the-approval-card",
			Position: Takeover,
			Reached:  "/init, or the start screen's scaffold offer",
			Bindings: []Binding{Decision.Accept, Decision.Refuse, Select.Cancel, Screen.List},
		},
		OnToolchain: {
			// The scaffold card's twin: asked for rather than handed, so it
			// holds the keyboard from the moment it opens, and its no is
			// Refuse for the same reason.
			Name:     "the toolchain card",
			Section:  "docs/interface/surfaces.md#the-approval-card",
			Position: Takeover,
			Reached:  "/setup, or the start screen's install offer",
			Bindings: []Binding{Decision.Accept, Decision.Refuse, Select.Cancel, Screen.List},
		},
		OnToolchainDraft: {
			// The scaffold card's shape again, for a file drafted rather
			// than templated, with the one key a draft needs that a template
			// does not: the file opens in $EDITOR before anything is written.
			Name:     "the toolchain draft card",
			Section:  "docs/interface/surfaces.md#the-approval-card",
			Position: Takeover,
			Reached:  "/toolchain, or the start screen's draft offer",
			Bindings: []Binding{Decision.Accept, Decision.Revise, Decision.Refuse, Select.Cancel, Screen.List},
		},
		OnHandoff: {
			// The toolchain draft card's keys for a note rather than a file:
			// written by the session, opened in $EDITOR on [e], and kept on
			// the conversation's slot only on the yes.
			Name:     "the handoff card",
			Section:  "docs/interface/surfaces.md#the-approval-card",
			Position: Takeover,
			Reached:  "/handoff, or the yes on the quit's offer of one",
			Bindings: []Binding{Decision.Accept, Decision.Revise, Decision.Refuse, Select.Cancel, Screen.List},
		},
		OnProposal: {
			// The scaffold card's shape for a write the record proposed: an
			// allowlist line or a skill, opened from /patterns. Its no comes
			// in two — for now and for good — because a proposal comes back
			// until it is answered for good (ProposalKeys).
			Name:     "the proposal card",
			Section:  "docs/interface/surfaces.md#the-approval-card",
			Position: Takeover,
			Reached:  "enter on a row of /patterns",
			Bindings: []Binding{Proposal.Write, Proposal.Later, Proposal.Never, Select.Cancel, Screen.List},
		},
		OnConfirm: {
			Name:     "the inline confirm and the undo confirm",
			Section:  "docs/interface/surfaces.md#the-inline-confirm",
			Position: Takeover,
			Reached:  "the key that opens it",
			Bindings: []Binding{Confirm.Yes, Confirm.Force, Confirm.No, Screen.List},
		},
		OnHeldLine: {
			// A line from another session has no default answer: passing a
			// colleague's words to the turn is a decision, so enter and esc
			// answer nothing and the no is Refuse, which is two letters.
			Name:     "the held-line card",
			Section:  "docs/capabilities/sessions-and-memory.md#a-session-can-hand-another-a-line",
			Position: Takeover,
			Reached:  "a line another session sent, where sessions.inbound holds it",
			Bindings: []Binding{Confirm.Yes, Decision.Refuse, Screen.List},
		},
		OnSelector: {
			Name:     "the selector family, the model and rewind pickers",
			Section:  "docs/interface/surfaces.md#selectors",
			Position: Takeover,
			Reached:  "the command or key that opens it",
			Bindings: []Binding{
				Select.MoveJK, Select.Take, Select.Alt, Select.Filter,
				Select.ClearQ, Select.Toggle, Select.All, Select.Note,
				Select.Cancel,
			},
		},
		OnChatPicker: {
			// The one card in the family with keys of its own: only the
			// saved-chat picker answers them, so only its row offers them.
			Name:     "the saved-chat picker",
			Section:  "docs/interface/surfaces.md#selectors, docs/capabilities/sessions-and-memory.md#housekeeping",
			Position: Takeover,
			Reached:  "/chats, or bare /load",
			Bindings: []Binding{Select.Delete, Select.Rename},
		},
		OnRewindPicker: {
			// The rewind picker's own key: what a rewind to the row under
			// the pointer would take back, read before the row is taken.
			Name:     "the rewind picker",
			Section:  "docs/interface/surfaces.md#the-rewind",
			Position: Takeover,
			Reached:  "/rewind",
			Bindings: []Binding{Rewind.Diff},
		},
		OnSelectorQuery: {
			// The same family with the query line open, which is why it is
			// a row of its own: a list being typed into keeps every letter
			// as text, so j/k are not keys and the arrows are the movement
			//. Nothing here is a bare letter.
			Name:     "a selector being typed into",
			Section:  "docs/interface/surfaces.md#selectors",
			Position: Takeover,
			Reached:  "a card that opens over a catalog, or " + Bracket(Select.Filter) + " on one that does not",
			Bindings: []Binding{
				Select.Move, Select.Take, Select.ClearQ, Query.Rub, Select.Cancel,
			},
		},
		OnPalette: {
			Name:     "the command palette",
			Section:  "docs/interface/surfaces.md#the-palette",
			Position: Takeover,
			Reached:  Shown(Draft.Palette),
			Bindings: []Binding{
				Select.Palette.Prev, Select.Palette.Next,
				Select.Palette.Run, Select.Palette.Write, Select.Cancel,
			},
		},
		OnWholeKeyList: {
			// Every key, by group, over the session. Typed into like the
			// palette, so the only keys on its row are the ones no sentence
			// produces, and the chord that opened it is one of the ways out.
			Name:     "the whole key list",
			Section:  "docs/interface/surfaces.md#the-key-list",
			Position: Takeover,
			Reached:  Shown(Draft.KeyList) + ", or /help",
			Bindings: append(KeyList.All(), Draft.KeyList),
		},
		OnReview: {
			Name:     "review mode",
			Section:  "docs/interface/surfaces.md#the-turns-close",
			Position: Takeover,
			Reached:  "a turn's changed-files row, clicked or opened with " + Bracket(Reading.Expand) + ", /review, /diff",
			Bindings: []Binding{
				Review.MoveFile, Review.MoveHunk, Review.SideBySide,
				Review.PageUp, Review.PageDown, Review.Back,
			},
		},
		OnAgentManager: {
			Name:     "the agent manager",
			Section:  "docs/interface/surfaces.md#the-agent-manager",
			Position: Takeover,
			Reached:  Shown(Draft.Agents) + ", /agents",
			Bindings: []Binding{
				Agent.Move, Agent.Attach, Agent.Answer, Agent.Steer,
				Agent.Retry, Agent.Review, Agent.Migrate, Agent.Edit, Agent.Cancel, Agent.Kill,
				Agent.KillAll, Agent.Back, Screen.List,
			},
		},
		OnProfileDrafter: {
			// The drafting flow, which is a takeover for the reason every
			// summoned surface is: it asks three things in order and each
			// answer is typed, so the input it would otherwise borrow is the
			// input it has to own.
			Name:     "the profile drafter",
			Section:  "docs/interface/surfaces.md#the-profile-drafter",
			Position: Takeover,
			Reached:  "/agents new, or the manager's own row",
			Bindings: []Binding{
				Profile.Move, Profile.Take, Screen.List, Profile.Back,
			},
		},
		OnProfileDraft: {
			// The draft step of the same flow: the sections, one at a time,
			// over the card that writes the file. It is a row of its own
			// because enter means something else here — a section is
			// refined, not an answer taken — and one row answering one
			// keystroke with two acts is what the register refuses.
			Name:     "the profile draft",
			Section:  "docs/interface/surfaces.md#the-profile-drafter",
			Position: Takeover,
			Reached:  "the drafter's last step",
			Bindings: []Binding{
				Profile.Move, Profile.Refine, Profile.RefineAll, Profile.Edit, Profile.Clear,
				Profile.Migrate, Profile.Note, Profile.Save, Profile.ScrollUp, Profile.ScrollDown, Screen.List, Profile.Back,
			},
		},
		OnEditor: {
			// A file open in a pane over the feed, typed into. Every letter
			// is the file's, `?` included, so the key list is on the draft's
			// chord here, and nothing the pane draws offers a bare letter.
			Name:     "the editor pane",
			Section:  "docs/interface/surfaces.md#the-editor-pane",
			Position: Takeover,
			Typed:    true,
			Reached:  "/edit <path> in shhh code",
			Bindings: Editor.All(),
		},
		OnEditorLeave: {
			// The question esc asks over a buffer that differs from the disk.
			// A row of its own because the typing stops while it is up: `y`
			// is a key here because nothing is being typed, and ctrl+s means
			// save and leave rather than save.
			Name:     "the editor pane's leave question",
			Section:  "docs/interface/surfaces.md#the-editor-pane",
			Position: Takeover,
			Reached:  Bracket(Editor.Back) + " in the editor pane with the buffer modified",
			Bindings: Editor.Leaving(),
		},
		OnDiff: {
			Name:     "the full-screen diff",
			Section:  "docs/interface/surfaces.md#the-diff-view",
			Position: Takeover,
			Reached:  "the key that opens it",
			Bindings: []Binding{
				Diff.Scroll, Diff.Hunk, Diff.SideBySide, Diff.Back, Diff.Leave,
			},
		},
		OnOutput: {
			Name:     "the full-screen output",
			Section:  "docs/interface/surfaces.md#the-activity-row",
			Position: Takeover,
			Reached:  "the key that opens it",
			Bindings: []Binding{
				Output.Scroll, Output.PageUp, Output.PageDown,
				Output.Collapse, Output.Back, Output.Leave,
			},
		},
		OnPreview: {
			Name:     "the staged attachment preview",
			Section:  "docs/interface/surfaces.md#a-staged-attachment",
			Position: Takeover,
			Reached:  "a chip in reading mode, a click on one, or /paste show <handle>",
			Bindings: Preview.All(),
		},
		OnKeyList: {
			// The list `?` opens over a card or reading mode. It holds the
			// keyboard while it is up, and every key it answers takes the
			// reader back to the surface it was opened over, except the
			// arrows that read a list longer than the pane.
			Name:     "the key list",
			Section:  "docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard",
			Position: Takeover,
			Reached:  Bracket(Screen.List) + " on a card that holds the keyboard, or in reading mode",
			Bindings: []Binding{Screen.Move, Screen.List, Screen.Quit},
		},
		OnRetry: {
			Name:     "the retry countdown",
			Section:  "docs/interface/surfaces.md#the-recovery-row",
			Position: Takeover,
			Reached:  "it opens on its own and takes the keyboard",
			Bindings: []Binding{Wait.Fallback, Wait.Stop},
		},
		OnPressure: {
			Name:     "the context-pressure card",
			Section:  "docs/interface/surfaces.md#the-recovery-row",
			Position: Takeover,
			Reached:  "it opens on its own and takes the keyboard",
			Bindings: []Binding{Wait.Compact, Wait.NewSession, Wait.KeepGoing, Screen.List},
		},
		OnMaskedKey: {
			Name:     "the masked key prompt",
			Section:  "docs/interface/surfaces.md#the-recovery-row",
			Position: Takeover,
			Reached:  Bracket(Row.Key) + " on an auth failure's row",
			Bindings: []Binding{Wait.UseKey, Wait.KeepKey},
		},
	}
}

// Programs are the keyed surfaces outside a chat session: the supporting
// TUIs, the one-shot's action bar and the saved-chat browser.
// Each is its own Bubble Tea program with its own key row and its own `?`,
// so none of them is part of a session's answer to that key — but a key is a
// key, and the register is not worth having if it is only most of them.
func Programs() []Surface {
	return []Surface{
		{
			Name:     "shhh config",
			Section:  "docs/interface/surfaces.md#the-supporting-screens",
			Position: Takeover,
			Reached:  "shhh config",
			Bindings: []Binding{
				Screen.Move, Screen.Take, Screen.Filter, Screen.ClearQ, Query.Rub,
				Screen.Reset, Screen.Write, Screen.Scope, Screen.List, Screen.Quit,
			},
		},
		{
			Name:     "a setting's picker or field",
			Section:  "docs/interface/surfaces.md#the-supporting-screens",
			Position: Takeover,
			Reached:  Bracket(Screen.Take) + " on a setting",
			Bindings: []Binding{
				Select.Move, Screen.Take, Screen.Filter, Screen.ClearQ, Query.Rub,
				Select.Alt, Screen.Scope, Screen.Keep,
			},
		},
		{
			Name:     "shhh history",
			Section:  "docs/interface/surfaces.md#the-supporting-screens",
			Position: Takeover,
			Reached:  "shhh history",
			Bindings: []Binding{
				Screen.Move, Screen.Rerun, Screen.Copy, Screen.Snippet,
				Screen.Delete, Screen.Filter, Screen.ClearQ, Query.Rub,
				Screen.List, Screen.Quit,
			},
		},
		{
			Name:     "shhh doctor",
			Section:  "docs/interface/surfaces.md#the-supporting-screens",
			Position: Takeover,
			Reached:  "shhh doctor",
			Bindings: []Binding{
				Screen.Move, Screen.Fix, Screen.Copy, Screen.Again,
				Screen.List, Screen.Quit,
			},
		},
		{
			Name:     "shhh metrics",
			Section:  "docs/interface/surfaces.md#the-supporting-screens",
			Position: Takeover,
			Reached:  "shhh metrics",
			Bindings: []Binding{Screen.List, Screen.Quit},
		},
		{
			// The one supporting screen that asks rather than reports, which
			// is why its three answers are its whole register: there is
			// nothing on it to move between, and the way out is the way out
			// of every other one.
			Name:     "shhh rate",
			Section:  "docs/interface/surfaces.md#the-supporting-screens",
			Position: Takeover,
			Reached:  "shhh rate",
			Bindings: []Binding{
				Screen.Worked, Screen.Failed, Screen.Skip, Screen.List, Screen.Quit,
			},
		},
		{
			Name:     "the one-shot's action bar",
			Section:  "docs/interface/surfaces.md#the-one-shot-result",
			Position: Takeover,
			Reached:  "shhh cmd <prompt>",
			Bindings: []Binding{
				OneShot.Run, OneShot.Confirm, OneShot.Step, OneShot.DryRun, OneShot.Edit,
				OneShot.Revise, OneShot.Back, OneShot.Alternatives,
				OneShot.Explain, OneShot.Copy, OneShot.Save, OneShot.Quit,
			},
		},
		{
			Name:     "first contact and the provider card",
			Section:  "docs/interface/surfaces.md#the-start-screen",
			Position: Takeover,
			Reached:  "a session with no key to run on",
			Bindings: []Binding{Setup.Wizard, Setup.Paste, Setup.Local},
		},
		{
			Name:     "shhh snippets",
			Section:  "docs/interface/surfaces.md#the-supporting-screens",
			Position: Takeover,
			Reached:  "shhh snippets",
			Bindings: []Binding{
				Screen.Move, Screen.Rerun, Screen.Copy, Screen.Rename,
				Screen.Delete, Screen.Filter, Screen.ClearQ, Query.Rub,
				Screen.List, Screen.Quit,
			},
		},
		{
			Name:     "the saved-chat browser",
			Section:  "docs/interface/surfaces.md#the-supporting-screens, docs/capabilities/sessions-and-memory.md#housekeeping",
			Position: Takeover,
			Reached:  "shhh chats, or --resume on shhh chat and shhh code",
			Bindings: []Binding{
				Screen.Move, Screen.Take, Screen.Rename, Screen.Delete,
				Screen.Filter, Screen.ClearQ, Query.Rub, Screen.List, Screen.Quit,
			},
		},
		{
			// The rename row both of those open, which is a row of its own
			// for the reason a selector being typed into is: a line being
			// typed into keeps every letter as text, so none of the screen's
			// bare letters are live while it is up and enter means something
			// else here than it does on the list underneath.
			Name:     "a rename row",
			Section:  "docs/interface/surfaces.md#the-supporting-screens",
			Position: Takeover,
			Reached:  Bracket(Screen.Rename) + " on a snippet or a saved chat",
			Bindings: []Binding{Screen.Take, Screen.ClearQ, Query.Rub, Screen.Keep},
		},
	}
}
