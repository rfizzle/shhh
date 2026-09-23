package chat

// Session branching and rewind. A checkpoint marks the start of each
// user turn; /rewind truncates the working conversation back to a chosen
// checkpoint and preserves the abandoned tail as a branch session in storage,
// parent-linked to the current session. /branches opens a picker over the
// current session's branch family and switches between branches. Each
// checkpoint records the git HEAD + dirty status at the time so the message
// can show what diverged since.
//
// A rewind to turn N returns to how things stood when turn N ended: turns
// 1–N stay and every turn after it is taken back. The picker's rows, the
// numbered command, the card's figures and the frame's `at turn N` all name
// that one moment. Turn 0 is the start of the session, before anything was
// said.
//
// A rewind offers the files as well as the conversation. Going back past a
// turn and leaving on disk everything that turn wrote is a state neither the
// person nor the model asked for, so the card asks which of the two — or both
// — is meant, and the file half is the session's own records put back through
// the confirm an undo goes through: the same drift check, and the same
// deliberate second answer for a file that changed since. What a command
// changed is not in those records and is not put back; the card says so,
// because a restore that quietly missed half of a turn's work would be worse
// than one that never offered.
// See docs/capabilities/coding-agent.md#a-rewind-can-put-the-files-back.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// GitSnapshot is the workspace's git state at a checkpoint: HEAD plus a
// digest of the porcelain status. The zero value means "not a git repo".
type GitSnapshot struct {
	Repo       bool
	Head       string
	StatusHash string
	DirtyPaths int
	// Unhashed marks a reading taken over a tree with more changed content
	// than the digest covers. The hash then stands for the path list and
	// only as much content as was reached, so two of them can be equal over
	// files that differ — carrying the flag is what keeps the divergence
	// reading from mistaking that equality for an unchanged tree.
	Unhashed bool
}

// checkpoint marks where one user turn starts in the conversation.
type checkpoint struct {
	index   int    // conversation index of the turn's user message
	preview string // first line of the user text
	git     GitSnapshot
	hasGit  bool // a snapshot function was wired when this was recorded
	// turn is the changeset turn this checkpoint opens, and is what says
	// which records a file restore would put back. Zero for a checkpoint
	// derived from a stored conversation: the messages say where a turn
	// began and not what it was numbered, and guessing would offer to put
	// back somebody else's edits.
	turn int64
	// at is when the turn started, which is what the picker's rows report as
	// their age. Zero for a checkpoint rebuilt from a stored conversation:
	// the messages say what was said and not when, and a row that dated one
	// from the moment the session reopened would age every turn to the same
	// minute (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
	at time.Time
}

// WithGitSnapshots wires the git-state capture recorded on each rewind
// checkpoint; nil (the default) records no git state.
func (m Model) WithGitSnapshots(fn func() GitSnapshot) Model {
	m.gitSnapshot = fn
	return m
}

// recordCheckpoint marks the start of a user turn. Call it before the user
// message joins the conversation, so the checkpoint index points at it.
func (m *Model) recordCheckpoint(text string) {
	cp := checkpoint{index: len(m.agent.Messages()), preview: firstLine(text),
		turn: m.turnCount, at: time.Now()}
	if m.gitSnapshot != nil {
		cp.git = m.gitSnapshot()
		cp.hasGit = true
	}
	m.checkpoints = append(m.checkpoints, cp)
	// A new turn is what makes the frame's `at turn N` untrue: the session
	// no longer stands where the rewind left it, it is moving on from there
	// (docs/interface/surfaces.md#the-rewind). It also moves the
	// conversation a rewound fold would be put back onto.
	m.rewoundTo = nil
	m.retireRewoundFolds()
}

// checkpointsFromMessages derives checkpoints from a stored conversation:
// every user message the reader typed is a rewind point. Git snapshots are
// unknown for rebuilt checkpoints — the rewind message says so instead of
// guessing.
//
// A message the session wrote for itself is not a turn
// (provider.Message.Machine). Counting one would put a check-in in the list
// under the reader's name, and — because the list is numbered and "turn 3"
// cuts the conversation at the fourth of these — it would also truncate a
// resumed conversation in the middle of the turn the check-in interrupted.
func checkpointsFromMessages(msgs []provider.Message) []checkpoint {
	var cps []checkpoint
	for i, msg := range msgs {
		if msg.Role == provider.RoleUser && !msg.Machine {
			cps = append(cps, checkpoint{index: i, preview: firstLine(msg.Content)})
		}
	}
	return cps
}

// openRewindPick opens the interactive /rewind picker over the recorded
// checkpoints, latest first.
//
// It is a card of the selector family and not one of its own: a list of turns
// is a catalog, and the first thing a reader looking for one does is name
// what they were doing — so it opens with the query row the family's search
// cards open with, and the terminal's own cursor sits on it
// (docs/interface/surfaces.md#selectors). Its own Select was the same card
// with none of that: no filter, no cursor, and a second key handler and a
// second render to keep in step with the family's.
func (m Model) openRewindPick() (tea.Model, tea.Cmd) {
	if len(m.checkpoints) == 0 {
		m.appendEntry(entry{kind: entrySystem, text: "No checkpoints to rewind to yet."})
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m, nil
	}
	// The number on a row is the turn's own and not the row's position in the
	// list, so it counts down as the list is read and a reader comparing it
	// with a close row's `turn 6` is comparing the same figure.
	//
	// No row says where HEAD stood at the time. A row is a turn — its words,
	// what it changed, how long ago — and the checkpoint's git snapshot is
	// not something a reader picks a turn by; what it is read for is the
	// divergence a restore is judged against, which gitDivergence states
	// once, on the rewind's own message.
	opts := make([]components.SelectOption, 0, len(m.checkpoints))
	for i := len(m.checkpoints) - 1; i >= 0; i-- {
		cp := m.checkpoints[i]
		// Returning to the end of turn i+1 takes back the turns after it, so
		// whether the files can come back is a question about the next
		// checkpoint's records and not this one's. The latest turn has none
		// after it and is never blocked.
		blocked := ""
		if cut, ok := m.cutAt(i + 1); ok {
			blocked = m.restoreBlocked(cut)
		}
		opts = append(opts, components.SelectOption{
			Number: i + 1,
			Label:  cp.preview,
			Detail: m.checkpointDetail(cp, blocked),
			Meta:   checkpointAge(cp),
			Dim:    blocked != "",
		})
	}
	return m.openRewindPicker(opts, func(m *Model, idx int) (string, tea.Cmd) {
		// The options are latest-first, so the row is counted back from
		// the end rather than into it.
		turn := len(m.checkpoints) - idx
		// The autosave rides along whether or not the conversation moved:
		// where it did, the slot has to be written before anything else
		// can be added to it, and where the card opened instead there is
		// nothing new to write and the save appends nothing.
		return m.rewindToTurn(turn), m.autosaveCmd()
	})
}

// rewindRailLabel names the surface on the rule above the picker, the way
// DRAFT, DECISION and READING name theirs. It says what the list is and what
// taking a row means, because the picker itself never acts — the card that
// follows does (docs/interface/surfaces.md#the-rewind).
const rewindRailLabel = "REWIND · pick a turn to return to"

// openRewindPicker opens the timeline: the selector family's search card,
// under the rail that names it.
func (m Model) openRewindPicker(opts []components.SelectOption, apply func(*Model, int) (string, tea.Cmd)) (tea.Model, tea.Cmd) {
	updated, cmd := m.openSearchPicker("", opts, 0, apply)
	next := updated.(Model)
	next.picker.Rail = rewindRailLabel
	// A digit here is part of a turn's number rather than a jump to the
	// third row down, and the card opens with its query row already taking
	// every letter, so there is nothing for the numbering column to address.
	next.picker.Unnumbered = true
	next.picker.Actions = []keys.Binding{keys.Rewind.Diff}
	return next, cmd
}

// updateRewindPick answers the picker's own key before the card sees it: the
// change a rewind to the row under the pointer would take back, full screen,
// with esc coming back to the picker as it was left
// (docs/interface/surfaces.md#the-rewind). Like every letter on a card that
// opens as a search, it is text while the query row is open.
func (m Model) updateRewindPick(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if m.picker == nil || m.picker.Rail != rewindRailLabel {
		return m, nil, false
	}
	// The card's one line about the last press lasts until the next one.
	m.picker.Warning = ""
	if m.picker.Filtering || !keys.Match(msg, keys.Rewind.Diff) {
		return m, nil, false
	}
	row := m.picker.Focus
	if row < 0 || row >= len(m.pickerIndex) {
		return m, nil, true
	}
	// The options are latest-first, as the picker's own apply counts them.
	next, cmd := m.openRewindDiff(len(m.checkpoints) - m.pickerIndex[row])
	return next, cmd, true
}

// openRewindDiff opens what a rewind to the end of turn n would take back —
// every turn after it, folded into one net change per file, the reading the
// scope card's code field states in figures. Where there is nothing of the
// kind to show, the card says why on its warning line and stays up.
func (m Model) openRewindDiff(n int) (tea.Model, tea.Cmd) {
	cp, ok := m.cutAt(n)
	if !ok {
		m.picker.Warning = fmt.Sprintf("The session stands at the end of turn %d — nothing after it to show.", n)
		return m, nil
	}
	if blocked := m.restoreBlocked(cp); blocked != "" {
		m.picker.Warning = blocked + "."
		return m, nil
	}
	folded := changeset.Fold(m.rewindTurns(cp))
	if folded.Files() == 0 {
		m.picker.Warning = fmt.Sprintf("Nothing on record was written after %s.", turnPoint(n))
		return m, nil
	}
	files := make([]diff.File, 0, len(folded.Records))
	for _, r := range folded.Records {
		files = append(files, diff.File{Path: r.Path, Hunks: r.Hunks})
	}
	return m.openDiffFull(&components.DiffView{
		Path:      turnSpanPhrase(n+1, len(m.checkpoints)),
		Verb:      "edit",
		Files:     files,
		SyntaxFor: diffSyntax,
	}, statePick)
}

// checkpointDetail is the row's continuation: what the turn changed, or the
// reason a rewind past it cannot bring the files back. The mutation mark
// leads it in the accent every write on the transcript is marked with, the
// counts are the diff's own two registers, and the words between them are
// chrome (docs/interface/surfaces.md#the-rewind).
func (m Model) checkpointDetail(cp checkpoint, blocked string) []components.DetailSpan {
	if blocked != "" {
		return []components.DetailSpan{{Text: blocked, Tone: components.ToneQuiet}}
	}
	// The turn's own change and not the run from it onwards: a row is a turn,
	// and a reader comparing two rows is comparing what each turn did. What
	// the run adds up to is the scope card's field, where it is what the
	// answer would actually restore.
	if cp.turn == 0 {
		// A turn rebuilt from a stored conversation has no number to ask the
		// records about, so what it changed is not reported rather than
		// borrowed from some other turn
		// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
		return nil
	}
	folded, _ := m.changes.Recall(cp.turn)
	if folded.Files() == 0 {
		// A turn that touched nothing says so in words rather than in an
		// empty column: the column is what the reader is scanning, and a
		// blank in it reads as a row that has not been measured.
		return []components.DetailSpan{{Text: readsOnlyPhrase, Tone: components.ToneQuiet}}
	}
	spans := []components.DetailSpan{
		{Text: mutationMark, Tone: components.ToneOpen},
		{Text: plural(folded.Files(), "file") + " ", Tone: components.ToneQuiet},
	}
	return append(spans, components.DiffStatSpans(folded.Added, folded.Removed)...)
}

// mutationMark is the rail glyph a write carries on the transcript, reused
// here as a mark on the row: the picker's rows are turns, and the ones that
// wrote are the ones a restore is about
// (docs/interface/principles.md#weight-tracks-risk).
const mutationMark = "▎"

// readsOnlyPhrase is what a turn that changed nothing says where its
// diffstat would be.
const readsOnlyPhrase = "reads only, nothing changed"

// restoreBlocked is why a rewind past this checkpoint cannot put the files
// back, or "" where it can. The row stays in the list and states it — inert,
// with the reason where the diffstat would be — because a missing option with
// no explanation reads as a bug, and because talk only still works past the
// boundary: the conversation is shhh's to give back, and what the records
// never held never was (docs/interface/surfaces.md#the-rewind).
func (m Model) restoreBlocked(cp checkpoint) string {
	if cp.turn == 0 {
		return "code can't be restored past this — this conversation came back without its records"
	}
	for _, n := range m.changes.Evicted() {
		if n >= cp.turn {
			return fmt.Sprintf(
				"code can't be restored past this — turn %d's records were dropped to stay inside the store's limit", n)
		}
	}
	return ""
}

// checkpointAge is how long ago the turn started, in the short field at the
// end of the row. A checkpoint with no moment of its own reports none rather
// than a made-up one
// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
func checkpointAge(cp checkpoint) string {
	if cp.at.IsZero() {
		return ""
	}
	d := time.Since(cp.at)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(int(d.Seconds()), 0))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours())/24)
}

// cutAt is the checkpoint a rewind to turn n cuts the conversation at: the
// start of turn n+1, which is the first turn the rewind takes back. False
// where turn n is the latest, and nothing comes after it to take back.
func (m Model) cutAt(n int) (checkpoint, bool) {
	if n < 0 || n >= len(m.checkpoints) {
		return checkpoint{}, false
	}
	return m.checkpoints[n], true
}

// turnPoint names the moment a rewind to turn n returns to: the end of that
// turn, or the start of the session for turn 0.
func turnPoint(n int) string {
	if n == 0 {
		return "the start of the session"
	}
	return fmt.Sprintf("turn %d", n)
}

// rewindUsage is the refusal for a turn number the session does not have.
func (m Model) rewindUsage() string {
	return fmt.Sprintf("Usage: /rewind [0-%d]", len(m.checkpoints))
}

// rewindToTurn asks what a rewind to the end of turn n (turn 0 being the
// start of the session) should put back, and answers it directly when there
// is only one thing it could mean. Nothing is written here where the offer
// opens: the card is the question, and for the files the confirm behind it
// is a second one.
func (m *Model) rewindToTurn(n int) string {
	if n == len(m.checkpoints) && n > 0 {
		// The latest turn's end is where the session already stands, so
		// there is nothing after it for a rewind to take back.
		return fmt.Sprintf("The session already stands at the end of turn %d — there is nothing after it to rewind.", n)
	}
	cp, ok := m.cutAt(n)
	if !ok {
		return m.rewindUsage()
	}
	if blocked := m.restoreBlocked(cp); blocked != "" {
		// Talk only still works past the boundary, and it is the whole of
		// what a rewind can mean here, so it is done rather than asked
		// about: the conversation is shhh's to give back, and what the
		// records never held never was.
		return m.rewindTalkOnly(n, "Only the conversation was rewound — "+blocked+".")
	}
	turns := m.rewindTurns(cp)
	if len(turns) == 0 {
		// Nothing on record was written after this checkpoint, so the
		// conversation is the whole of what a rewind can mean and offering a
		// choice between it and nothing would be a card that asks a question
		// with one answer.
		return m.rewindTalkOnly(n, conversationOnlyNote)
	}
	m.openRewindScope(n, turns)
	return ""
}

// rewindTurns is what the session recorded from the checkpoint onwards —
// the turns a file restore would put back, oldest first. A checkpoint with no
// turn number of its own answers with none: it came from a stored
// conversation, whose messages say where a turn began and not what it was
// numbered.
func (m *Model) rewindTurns(cp checkpoint) []changeset.Turn {
	if cp.turn == 0 {
		return nil
	}
	var turns []changeset.Turn
	for _, t := range m.changes.Since(cp.turn) {
		if t.Files() > 0 {
			turns = append(turns, t)
		}
	}
	return turns
}

// rewindScope is the scope card while it is up: the point picked, the turns
// a restore would put back, and the card as it will be drawn. It is a
// pointer on the model and nil while the card is down, because it is one
// surface's state and nothing else reads it.
type rewindScope struct {
	turn  int
	turns []changeset.Turn
	ret   rewindReturn
	card  components.RewindCard
}

// rewindReturn is what the row a rewind lands as says about the conversation
// half. It is carried through the file half's confirm where there is one, so
// the two halves of one act land as one row rather than as two
// (docs/interface/surfaces.md#the-rewind).
type rewindReturn struct {
	// turn is the rewind point: the session stands at the end of it.
	turn int
	// first and last are the turns that left the window; zero where none
	// did, which is the code-only answer.
	first, last int
	// was and now are the window's occupancy either side of the rewind.
	was, now int
	at       time.Time
	// fold is the fold the conversation half left, told which turn the file
	// half landed as so that reapplying it can take that turn back too. Nil
	// where the conversation did not move.
	fold *rewoundFold
}

// openRewindScope puts the two readings of a rewind to the reader on a card
// with three answers. The conversation and the files are separable and the
// person is the only one who knows which they meant — going back to before a
// turn and leaving what it wrote on disk is a real answer, and so is putting
// the files back while keeping the conversation that produced them
// (docs/interface/surfaces.md#the-rewind).
func (m *Model) openRewindScope(n int, turns []changeset.Turn) {
	folded := changeset.Fold(turns)
	scope := &rewindScope{turn: n, turns: turns, ret: m.rewindReturnFor(n)}
	scope.card = components.RewindCard{
		Title: "Rewind to " + turnPoint(n),
		Code: components.CardField{
			Label: "code", Tone: components.ToneNeutral,
			Value: fmt.Sprintf("%s restored %s", plural(folded.Files(), "file"),
				components.DiffStat(folded.Added, folded.Removed)),
			// The shell is the hole in the offer and the card is where it
			// has to be said: the records hold what the file tools wrote, so
			// a command that moved, generated or deleted something is not in
			// them and does not come back with the rest.
			Detail: fmt.Sprintf("the reverse of %s; what a command changed was never recorded",
				turnSpanPhrase(scope.ret.first, scope.ret.last)),
		},
		Talk: components.CardField{
			Label: "talk", Tone: components.ToneNeutral,
			Value:  turnSpanPhrase(scope.ret.first, scope.ret.last) + leaveVerb(scope.ret.first, scope.ret.last),
			Detail: fmt.Sprintf("ctx %d%% → %d%% · kept as a branch, /branches to switch back", scope.ret.was, scope.ret.now),
		},
		Undo: components.CardField{
			Label: "undo", Tone: components.ToneSafe, Value: "yes",
			Detail: "a rewind is a turn, and " + keys.Bracket(keys.Row.Undo) + " takes it back",
		},
	}
	m.rewindScope = scope
	m.enterSurface(stateRewindScope)
	m.syncViewport()
}

// rewindReturnFor reads what the conversation half of a rewind to turn n
// would do, before it does it: which turns leave the window, and what the
// window's occupancy is either side of them.
func (m Model) rewindReturnFor(n int) rewindReturn {
	r := rewindReturn{turn: n, first: n + 1, last: len(m.checkpoints), was: m.contextPercent(), at: time.Now()}
	r.now = r.was
	cp := m.checkpoints[n]
	if cp.index > len(m.agent.Messages()) {
		return r
	}
	// The cut drops the provider's report, since it counted the turns the
	// cut takes out (rewindConversation), so what the window holds afterwards
	// is contextAccounting's reading with no report: the corrected estimate
	// of the kept prefix. The card states that figure rather than a report
	// less an estimate of the tail, because the row reads the window after
	// the cut and a card that predicted any other arithmetic would name a
	// figure the row then contradicts.
	r.now = m.windowShare(m.correctedEstimateTo(cp.index).total())
	return r
}

// settled is the return with its second figure read off the window as it now
// stands rather than off the card's prediction of it. The card had to
// predict; the row states what happened, which is the reading a compaction
// receipt takes for the same reason (context.go).
func (m Model) settled(r rewindReturn) rewindReturn {
	r.now = m.contextPercent()
	return r
}

// turnSpanPhrase names the run of turns leaving the window, in the wording a
// compaction receipt already folds turns in, so one act's account and the
// other's are read the same way.
func turnSpanPhrase(first, last int) string {
	if first >= last {
		return fmt.Sprintf("turn %d", first)
	}
	return fmt.Sprintf("turns %d–%d", first, last)
}

// leaveVerb agrees with turnSpanPhrase: returning to the turn before the
// latest is the usual rewind, and it takes one turn back rather than several.
func leaveVerb(first, last int) string {
	if first >= last {
		return " leaves the window"
	}
	return " leave the window"
}

// updateRewindScope routes keys while the scope card is up. Every letter on
// it is live: the picker that opened it had already taken the keyboard
// (internal/ui/keys/register.go).
func (m *Model) updateRewindScope(msg tea.KeyPressMsg) (bool, overlayAction) {
	scope := m.rewindScope
	if scope == nil {
		return true, overlayAction{close: true}
	}
	switch {
	case keys.Match(msg, keys.Rewind.Cancel):
		// Esc is the safe answer: the card goes and nothing it offered
		// happened, so the host's own close is the whole of it.
		m.rewindScope = nil
		return true, overlayAction{close: true}
	case keys.Match(msg, keys.Rewind.Code):
		m.closeRewindScope()
		m.armRewindRestore(scope.turn, scope.turns, nil)
		return true, overlayAction{}
	case keys.Match(msg, keys.Rewind.Talk):
		m.closeRewindScope()
		if note := m.rewindTalkOnly(scope.turn, filesKeptNote); note != "" {
			m.appendEntry(entry{kind: entrySystem, text: note})
		}
		m.showTranscriptEnd()
		return true, overlayAction{run: m.autosaveCmd()}
	case keys.Match(msg, keys.Rewind.Both):
		ret := scope.ret
		m.closeRewindScope()
		// The conversation this session would be reopened on has just
		// changed shape, so the slot is written before anything else can be
		// added to it. The row the act lands as waits for the file half:
		// one act, one row.
		note := m.rewindConversation(scope.turn, "")
		ret.fold = m.newestRewoundFold()
		if note != "" {
			m.appendEntry(entry{kind: entrySystem, text: note})
		}
		m.armRewindRestore(scope.turn, scope.turns, &ret)
		m.showTranscriptEnd()
		return true, overlayAction{run: m.autosaveCmd()}
	}
	return false, overlayAction{}
}

// closeRewindScope takes the card down and hands the panel back before the
// answer takes it again. The three answers all leave for somewhere — the
// confirm behind the files, or the transcript — so the surface is left here
// rather than through the host's own close, which would run after the answer
// had already entered the next one.
func (m *Model) closeRewindScope() {
	m.rewindScope = nil
	m.leaveSurface()
	m.syncViewport()
}

// showTranscriptEnd puts the pane back on the live end after an answer
// changed what the transcript holds.
func (m *Model) showTranscriptEnd() {
	m.invalidateRenderCache()
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
}

// rewindScopeLines renders the scope card, one row per line.
func (m Model) rewindScopeLines() []string {
	if m.rewindScope == nil {
		return nil
	}
	return strings.Split(m.rewindScope.card.View(m.contentWidth()), "\n")
}

// rewindTalkOnly rewinds the conversation alone and lands the row that says
// so. It is the answer three paths reach — the card's talk key, a checkpoint
// with nothing on record after it, and one past the boundary a restore
// cannot cross — so what it does and what it leaves behind is written once.
func (m *Model) rewindTalkOnly(n int, filesNote string) string {
	if _, ok := m.cutAt(n); !ok {
		return m.rewindUsage()
	}
	ret := m.rewindReturnFor(n)
	note := m.rewindConversation(n, filesNote)
	m.appendRewindRow(m.settled(ret), changeset.Turn{})
	return note
}

// What a rewind that moved no files says about them. The first is for a
// session with nothing on record after the checkpoint; the second for one
// where there was something and the person chose to keep it.
const (
	conversationOnlyNote = "Only the conversation was rewound — files on disk were not restored."
	filesKeptNote        = "Only the conversation was rewound — the files those turns changed were left as they are."
)

// rewindVerb is the row's verb, out of the same closed vocabulary every other
// act's verb comes from.
const rewindVerb = "rewind"

// appendRewindRow lands the act as a row: what came back on disk, what left
// the window, and what the window costs now. A rewind that restored files
// carries the mutation rail and the edit glyph, because it wrote to the
// machine; one that only moved the window carries neither, for the reason a
// compaction carries neither — it rewrote what the model remembers and
// touched nothing (docs/interface/principles.md#weight-tracks-risk).
func (m *Model) appendRewindRow(r rewindReturn, folded changeset.Turn) {
	row := &components.ActivityRow{
		Kind:     components.ActivityCompaction,
		Verb:     rewindVerb,
		Duration: activityDuration(time.Since(r.at)),
	}
	var parts []string
	if folded.Files() > 0 {
		row.Kind = components.ActivityEdit
		row.Counts = fmt.Sprintf("%s · +%d −%d", plural(folded.Files(), "file"), folded.Added, folded.Removed)
		parts = append(parts, "files back to "+turnPoint(r.turn))
	}
	if r.first > 0 {
		parts = append(parts, turnSpanPhrase(r.first, r.last)+" out of the window",
			fmt.Sprintf("ctx %d%% → %d%%", r.was, r.now))
	}
	if len(parts) == 0 {
		parts = append(parts, "back to "+turnPoint(r.turn))
	}
	row.Target = strings.Join(parts, " · ")
	m.appendEntry(entry{kind: entrySystem, notice: &components.ActivityNotice{Act: row}})
	// The frame's top rail says where the reader stands until the next turn
	// makes it true by default.
	if r.first > 0 {
		turn := r.turn
		m.rewoundTo = &turn
	}
}

// armRewindRestore puts the file half of a rewind to the undo confirm. The
// turns from the checkpoint on are folded into one net change per path —
// the earliest before side and the latest after side — so the workspace is
// answered for once rather than a turn at a time, and the drift the confirm
// reports is measured against where the run of turns actually left each file.
// ret is the conversation half already taken, waiting for this one so the
// two land as one row; nil where the reader asked for the files alone.
func (m *Model) armRewindRestore(n int, turns []changeset.Turn, ret *rewindReturn) {
	folded := changeset.Fold(turns)
	plan := changeset.PlanUndo(folded, nil)
	if plan.Empty() {
		m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf(
			"Nothing on record was written after turn %d, so there are no files to put back.", n)})
		if ret != nil {
			m.appendRewindRow(m.settled(*ret), changeset.Turn{})
		}
		return
	}
	// The confirm names the first turn being taken back, which for a run of
	// several is the earliest of them. It reads as an understatement where
	// the run is long — the card that offered this has just said how many
	// turns it covers, and the close row names the rewind rather than a turn
	// — and it is where a wording for a run of turns would go if the confirm
	// ever grew one.
	of := undoSubject{
		since: "the rewind point",
		note:  "rewind to " + turnPoint(n),
		again: fmt.Sprintf("/rewind %d", n),
	}
	if ret == nil {
		// The files alone: the row still says where the workspace was put
		// back to, and says nothing about a window nothing moved.
		of.rewind = &rewindReturn{turn: n, at: time.Now()}
	} else {
		of.rewind = ret
	}
	m.armUndo(plan, of, folded.N, stateInput)
}

// rewindConversation truncates the conversation back to the end of turn n
// (0 being the start of the session), preserving the abandoned tail as a
// branch, and returns the message for the transcript. filesNote is what the
// message says about the files, and is empty where a restore of them is about
// to be put to the user: the card asks the question and a flat statement under
// it would answer it first.
func (m *Model) rewindConversation(n int, filesNote string) string {
	cp, ok := m.cutAt(n)
	if !ok {
		return m.rewindUsage()
	}
	msgs := m.agent.Messages()
	if cp.index > len(msgs) {
		return "Rewind failed: the checkpoint no longer matches the conversation."
	}
	full := make([]provider.Message, len(msgs))
	copy(full, msgs)
	dropped := len(full) - cp.index
	// What the cut takes back becomes a fold beside the branch: the rows the
	// turns left, the messages and the checkpoints, so the turns can be read
	// where they were and put back from there
	// (docs/interface/surfaces.md#the-rewind). The records are asked before
	// anything is restored, while they still describe those turns.
	fold := &rewoundFold{
		first: n + 1, last: len(m.checkpoints),
		tail:        append([]provider.Message(nil), full[cp.index:]...),
		at:          cp.index,
		checkpoints: append([]checkpoint(nil), m.checkpoints[n:]...),
		slot:        m.sessionName,
	}
	if cp.turn != 0 {
		fold.files, fold.filesKnown = changeset.Fold(m.rewindTurns(cp)).Files(), true
	}
	keptRows, rows, split := m.rewoundSplit(len(m.checkpoints) - n)

	branchNote := "Chat persistence is unavailable, so the abandoned tail was discarded."
	if m.db != nil {
		branch := branchName(m.sessionName, n)
		if err := m.db.SaveChatBranch(m.sessionName, branch, stripResumeContext(full)); err != nil {
			branchNote = "Failed to preserve the abandoned tail as a branch: " + err.Error()
		} else {
			branchNote = fmt.Sprintf("The abandoned tail (%d message(s)) is kept as branch %q — /branches to switch back.", dropped, branch)
		}
	}

	// loadConversation rebuilds checkpoints from the messages alone; restore
	// the live ones so their git snapshots survive the rewind.
	kept := append([]checkpoint(nil), m.checkpoints[:n]...)
	m.loadConversation(full[:cp.index])
	m.checkpoints = kept
	if split {
		// The turns that stay keep the rows they already have, as a
		// compaction's kept turns do: a turn redrawn from its messages is
		// prose and nothing else, and a fold an earlier rewind left above
		// the cut is part of what happened (context.go).
		m.resetTranscript()
		m.appendEntries(keptRows)
	} else {
		// A transcript that does not hold a row for every turn the
		// checkpoints number is rebuilt, and so are the fold's rows.
		start := len(m.transcript)
		m.appendMessageEntries(fold.tail)
		rows = append([]entry(nil), m.transcript[start:]...)
		m.transcript = m.transcript[:start]
		m.invalidateRenderCache()
	}
	for i := range rows {
		rows[i].outOfWindow = true
	}
	fold.rows = rows
	m.appendEntry(entry{kind: entryRewound, rewound: fold})
	// The rewound conversation is not the one the provider reported on.
	m.contextTokens = 0
	m.resetRounds()

	back := "Rewound to the start of the session."
	if n > 0 {
		back = fmt.Sprintf("Rewound to the end of turn %d (%q).", n, kept[n-1].preview)
	}
	lines := []string{back, branchNote}
	if filesNote != "" {
		lines = append(lines, filesNote)
	}
	if g := m.gitDivergence(cp); g != "" {
		lines = append(lines, g)
	}
	return strings.Join(lines, "\n")
}

// branchName names the branch that preserves an abandoned tail. The timestamp
// keeps names unique per rewind (SaveChat overwrites same-named sessions).
func branchName(session string, turn int) string {
	return fmt.Sprintf("%s@turn%d-%s", session, turn, time.Now().Format("20060102-150405.000"))
}

// gitDivergence describes how the workspace's git state has moved since the
// checkpoint; empty when there is nothing meaningful to say.
func (m Model) gitDivergence(cp checkpoint) string {
	if m.gitSnapshot == nil {
		return ""
	}
	if !cp.hasGit {
		return "Git: no snapshot was recorded for this checkpoint, so divergence is unknown."
	}
	now := m.gitSnapshot()
	switch {
	case !cp.git.Repo || !now.Repo:
		return ""
	case cp.git.Head != now.Head:
		return fmt.Sprintf("Git: HEAD has moved since this checkpoint (%s → %s).", shortHead(cp.git.Head), shortHead(now.Head))
	case cp.git.StatusHash != now.StatusHash:
		return fmt.Sprintf("Git: the working tree has changed since this checkpoint (%d dirty path(s) then, %d now).", cp.git.DirtyPaths, now.DirtyPaths)
	// Two hashes that differ differ over something real, so the line above
	// stands whether or not the content was digested. Equality is the one
	// reading a partial digest cannot support: past the bound the hash covers
	// the path list and only as much content as was reached, so a run of
	// edits confined to files that were already dirty leaves it identical.
	// Saying the tree matches on that evidence is the reading a restore gets
	// decided on, and it is the one that has to be withheld — as a warning,
	// not a refusal: the restore is still offered, and the person is told
	// what the sentence under it could not check.
	// See docs/capabilities/coding-agent.md#a-rewind-can-put-the-files-back.
	case cp.git.Unhashed || now.Unhashed:
		return fmt.Sprintf("Git: the tree holds more changed content than can be digested (past the %s bound), so whether it still matches this checkpoint cannot be read — check the files a restore would write before taking it.", quality.ContentBound())
	default:
		return fmt.Sprintf("Git: HEAD %s and the working tree match this checkpoint.", shortHead(now.Head))
	}
}

func shortHead(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// branchFamily is the session's branch family, or the one line saying why
// there is none to pick from. The picker and the text answer of /branches
// both ask it, so what counts as a family, and what is said when there is
// not one, is defined once.
func (m Model) branchFamily() ([]storage.ChatBranch, string) {
	if m.db == nil {
		return nil, "Chat persistence is unavailable."
	}
	branches, err := m.db.ListChatBranches(m.sessionName)
	if err != nil {
		return nil, "Error: " + err.Error()
	}
	if len(branches) < 2 {
		return nil, "This session has no branches yet — /rewind creates one."
	}
	return branches, ""
}

// switchBranch resolves the /branches argument — a number or an exact name —
// to a branch and switches to it. The number addresses the family in the
// order the picker draws it, so the digit typed at the card and the digit
// typed after the command mean the same branch.
func (m *Model) switchBranch(branches []storage.ChatBranch, arg string) string {
	target := ""
	if n, err := strconv.Atoi(arg); err == nil {
		if n < 1 || n > len(branches) {
			return fmt.Sprintf("Usage: /branches [1-%d]", len(branches))
		}
		target = branches[n-1].Name
	} else {
		for _, b := range branches {
			if b.Name == arg {
				target = b.Name
				break
			}
		}
		if target == "" {
			return fmt.Sprintf("No branch %q in this session's family.", arg)
		}
	}
	return m.switchToBranch(target)
}

// switchToBranch saves the current conversation to its own slot (nothing is
// lost on switch), then loads the named branch as the working conversation.
// The /branches picker selects a branch by name, so it comes here
// directly rather than through switchBranch's number-or-name resolution.
func (m *Model) switchToBranch(target string) string {
	if target == m.sessionName {
		return fmt.Sprintf("Already on %q.", target)
	}
	if len(m.agent.Messages()) > 1 {
		if err := m.db.SaveChat(m.sessionName, stripResumeContext(m.agent.Messages())); err != nil {
			return "Error saving the current branch before switching: " + err.Error()
		}
	}
	msgs, err := m.db.LoadChat(target)
	if err != nil {
		return "Error: " + err.Error()
	}
	m.loadConversation(msgs)
	m.adoptSlot(target)
	// The title stays: a branch is the same conversation, and the next
	// autosave stamps it on the branch's row so the listing shows both
	// members of the family under the same words.
	//
	// The handoff does not: it is the summary of one branch's past, and the
	// next save would otherwise stamp the branch just left onto the branch
	// just opened, over the one the fork carried there (reopen.go). No
	// reading of the checkout is taken either, unlike opening a conversation
	// by name — this is a move inside one sitting, on the tree that sitting
	// already surveyed, so there is nothing new to say about it.
	m.compactSummary = storedChatSummary(m.db, target)
	m.contextTokens = 0
	m.resetRounds()
	return fmt.Sprintf("Switched to branch %q (%d messages).", target, len(msgs))
}

// rewoundFold is what a rewind took back, held on the transcript as one fold
// beside the branch that also keeps it: the rows those turns left, the
// messages the cut took out of the conversation, and the checkpoints that
// numbered them (docs/interface/surfaces.md#the-rewind).
//
// The rows are the fold's own rather than the transcript's. They are out of
// the window — the model is never sent them — and a reader counting turns,
// verdicts or alerts off the transcript would otherwise count turns the
// session no longer has. Opening the fold draws them; [r] puts the messages
// back where the cut took them from and the rows back on the transcript.
type rewoundFold struct {
	// first and last are the turns taken back.
	first, last int
	// files is how many files those turns wrote, where the records can say:
	// filesKnown is false for turns rebuilt from a stored conversation,
	// whose records were never this session's to count.
	files      int
	filesKnown bool
	// rows are the transcript rows the turns left, marked out of the window.
	rows []entry
	// tail is what the cut took out of the conversation, in order, and at is
	// the conversation length it was cut at. A reapply appends the tail to a
	// conversation that is still exactly that long, which is what keeps every
	// checkpoint below a true conversation index (checkpoint.index).
	tail []provider.Message
	at   int
	// checkpoints are the rewound turns' own, git snapshots and ages kept.
	checkpoints []checkpoint
	// slot is the conversation the rewind was taken in: a branch switched to
	// since is a different conversation, and the tail was cut from this one.
	slot string
	// restored is the turn the rewind's file half landed as, or zero where
	// the files were left alone. Reapplying undoes that turn, through the
	// confirm every undo goes through.
	restored int64
	// spent is set once the fold can no longer be put back — the session
	// moved on, or it already was — and reapplied once it was.
	spent, reapplied bool
	// searchOpened marks a fold the transcript search opened onto a match,
	// which clearing the query folds back (search.go).
	searchOpened bool
}

// rewoundSplit divides the transcript at the row that starts the first turn a
// rewind takes back: the rows staying, and the rows the fold will hold. turns
// is how many turns are being taken back, counted off the reader's own rows
// from the end the way a compaction's split counts them (compactSplit) —
// skipping rows a compaction already took out of the window, which the
// checkpoints no longer number. False where the transcript holds fewer of
// those rows than the checkpoints claim, which leaves the caller to rebuild
// both halves from the messages instead.
func (m Model) rewoundSplit(turns int) (kept, rows []entry, ok bool) {
	end := len(m.transcript)
	for end > 0 && turns > 0 {
		end--
		if e := m.transcript[end]; e.kind == entryUser && !e.outOfWindow {
			turns--
		}
	}
	if turns > 0 {
		return nil, nil, false
	}
	// Copies both: the transcript they came from is rebuilt before either
	// is put back.
	kept = append([]entry(nil), m.transcript[:end]...)
	rows = append([]entry(nil), m.transcript[end:]...)
	return kept, rows, true
}

// newestRewoundFold is the fold the latest rewind left, or nil.
func (m Model) newestRewoundFold() *rewoundFold {
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if f := m.transcript[i].rewound; f != nil {
			return f
		}
	}
	return nil
}

// retireRewoundFolds marks every fold still offering [r] as spent: the
// conversation it would be put back onto has moved, so there is no longer a
// place to put it. The fold stays readable — it is still what happened.
func (m *Model) retireRewoundFolds() {
	retired := false
	for _, e := range m.transcript {
		if f := e.rewound; f != nil && !f.spent {
			f.spent, retired = true, true
		}
	}
	if retired {
		m.invalidateRenderCache()
	}
}

// reapplicable reports whether a fold can still be put back: nothing has been
// said since the rewind, the conversation is the one it was cut from, and it
// is still exactly as long as the cut left it. The last is the one that
// matters to the checkpoints — theirs are indices into a conversation whose
// prefix has to be the same one (checkpoint.index).
func (m Model) reapplicable(f *rewoundFold) bool {
	return f != nil && !f.spent && !m.working() && m.sessionName == f.slot &&
		len(m.agent.Messages()) == f.at && len(m.checkpoints) == f.first-1
}

// worth is what the fold says the turns held: how many there were and how
// many files they wrote, in the reader's own units.
func (f *rewoundFold) worth() string {
	turns := plural(f.last-f.first+1, "turn")
	switch {
	case !f.filesKnown:
		return turns
	case f.files == 0:
		return turns + ", nothing written"
	case f.files == 1:
		return turns + ", 1 file's worth of work"
	}
	return fmt.Sprintf("%s, %d files' worth of work", turns, f.files)
}

// rewoundOffers are the keys the fold carries: `[r]` while it can still be put
// back, with this row's own words — keys.Row.Retry is one keystroke whose
// meaning each row states, the way a dropped stream says "ask again".
func (m Model) rewoundOffers(e entry) []components.TurnKey {
	if !m.reapplicable(e.rewound) {
		return nil
	}
	return []components.TurnKey{rowOffer(keys.Row.Retry, "reapply — undo the rewind")}
}

// rewoundBlock draws the fold: one line saying which turns are behind it,
// what they held and the two keys, and — while it is open — the rows
// themselves. It sits on the grid a field short, as the compaction's fold
// line does, so the transcript's two folds of turns line up
// (docs/interface/principles.md#fold-never-hide).
func (m Model) rewoundBlock(e entry, width int, keysLive bool) string {
	f := e.rewound
	const sep = " · "
	lead := strings.Repeat(" ", components.GridVerbColumn-2)
	if f.reapplied {
		// The turns are back on the transcript below, so the fold is only the
		// record that they were taken back once.
		return components.Clip(lead+sty.SystemMsg.Render(
			turnSpanPhrase(f.first, f.last)+sep+"rewound, then reapplied below"), width)
	}
	open := e.expanded && len(f.rows) > 0
	mark, label := "▸", "read them"
	if open {
		mark, label = "▾", "fold them back up"
	}
	held := mark + " " + turnSpanPhrase(f.first, f.last) + sep + "rewound"
	size := sep + f.worth()
	if !open && m.viewport.Searching() {
		if n := m.rewoundMatches(f, width); n > 0 {
			size += sep + matchesInside(n)
		}
	}
	enter := sty.Hint.Key.Render(keys.Bracket(keys.Reading.Expand) + " " + label)
	offers := m.rewoundOffers(e)
	run := components.KeyRun(offers, !keysLive, m.rowHandover(keysLive))
	room := width - lipgloss.Width(lead)
	fits := func(parts ...string) bool {
		w := 0
		for _, p := range parts {
			w += lipgloss.Width(p)
		}
		return w <= room
	}
	var lines []string
	switch {
	case run != "" && fits(held+size+sep, enter, sep, run):
		lines = []string{lead + sty.SystemMsg.Render(held+size+sep) + enter + sty.SystemMsg.Render(sep) + run}
	default:
		// The reapply goes to a line of its own before anything is dropped:
		// a fold that does not say how to open it, or how to put it back, is
		// a row the reader has to guess at. Past that the size goes, because
		// the turn range already says what the fold holds.
		first := components.Clip(lead+sty.SystemMsg.Render(held), width)
		for _, dim := range []string{held + size, held} {
			if fits(dim+sep, enter) {
				first = lead + sty.SystemMsg.Render(dim+sep) + enter
				break
			}
		}
		lines = []string{first}
		if run != "" {
			lines = append(lines, components.Clip(lead+run, width))
		}
	}
	// Where this is the session's first chord, what alt costs on a stock
	// macOS terminal — a line of its own, as on every other row.
	if option := components.KeyRunOption(offers, !keysLive, m.namesOptionRow(e)); option != "" {
		lines = append(lines, lead+option)
	}
	if !open {
		return strings.Join(lines, "\n")
	}
	block := strings.Join(lines, "\n") + "\n"
	var prev entry
	had := false
	for _, r := range f.rows {
		row := m.renderEntry(r, width)
		if row == "" {
			continue
		}
		gap := "\n"
		if had {
			gap = separatorBefore(prev, r)
		}
		block += gap + row
		prev, had = r, true
	}
	return strings.TrimRight(block, "\n")
}

// rewoundMatches counts what the transcript search would find behind a closed
// fold, drawing its rows exactly as opening it would draw them, so the count
// on the fold's row is the number of marks that appear when it opens
// (search.go).
func (m Model) rewoundMatches(f *rewoundFold, width int) int {
	query := strings.ToLower(m.viewport.SearchQuery())
	if f == nil || query == "" {
		return 0
	}
	n := 0
	for _, r := range f.rows {
		n += countMatches(m.renderEntry(r, width), query)
	}
	return n
}

// focusedRewound is the fold the reading cursor stands on. Like every other
// row offer it is the session's own transcript only.
func (m Model) focusedRewound() (entry, bool) {
	if m.attachedTo != "" || m.focusIdx < 0 || m.focusIdx >= len(m.transcript) {
		return entry{}, false
	}
	e := m.transcript[m.focusIdx]
	if e.kind != entryRewound || e.rewound == nil {
		return entry{}, false
	}
	return e, true
}

// reapplyKey routes [r] to the focused fold, reporting false where the row is
// not claiming it: a fold that can no longer be put back offers no key and
// answers none, so the letter goes back to the draft.
func (m Model) reapplyKey(pressed string) (tea.Model, tea.Cmd, bool) {
	if !keys.Is(pressed, keys.Row.Retry) {
		return m, nil, false
	}
	e, ok := m.focusedRewound()
	if !ok || !m.reapplicable(e.rewound) {
		return m, nil, false
	}
	next, cmd := m.reapplyRewind(m.focusIdx)
	return next, cmd, true
}

// reapplyRewind undoes a rewind: the messages the cut took out go back where
// it took them from, the checkpoints that numbered them come back with their
// snapshots and ages, and the rows come back onto the transcript. The fold
// stays where it was as the record that the turns were once taken back, and
// the rows are appended rather than spliced in above the rewind's own row:
// the transcript's reader rows are what its turns are numbered by, and the
// conversation carries on from the last of them.
//
// Where the rewind also put files back, the turn that restore landed as is
// taken back too — through the undo confirm, with its drift check, because
// the files are the machine's and the conversation is not
// (docs/interface/surfaces.md#the-rewind).
func (m Model) reapplyRewind(idx int) (tea.Model, tea.Cmd) {
	f := m.transcript[idx].rewound
	msgs := append(append([]provider.Message(nil), m.agent.Messages()...), f.tail...)
	m.agent.SetMessages(msgs)
	m.checkpoints = append(m.checkpoints, f.checkpoints...)
	m.recallFromMessages(msgs)
	// The provider's last report counted the shorter conversation.
	m.contextTokens = 0
	m.rewoundTo = nil
	rows := f.rows
	f.rows, f.spent, f.reapplied = nil, true, true
	m.transcript[idx].expanded = false
	for _, r := range rows {
		r.outOfWindow = false
		m.appendEntry(r)
	}
	m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf(
		"Reapplied %s — the rewind is undone, and the conversation stands at the end of turn %d again.",
		turnSpanPhrase(f.first, f.last), f.last)})
	m.invalidateRenderCache()
	if m.state == stateFocus {
		m.refreshFocusView()
	} else {
		m.showTranscriptEnd()
	}
	save := m.autosaveCmd()
	if f.restored > 0 {
		// The file half: the restore was a turn, and undoing that turn is
		// what puts back what the rewound turns wrote.
		next, cmd := m.undoTurn(f.restored, nil)
		return next, tea.Batch(save, cmd)
	}
	return m, save
}
