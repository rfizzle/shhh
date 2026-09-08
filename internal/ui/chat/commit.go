package chat

// Committing a turn from the row that closed it
// (docs/interface/surfaces.md#the-turns-close).
//
// A commit was already something this product could do — the model asks for
// one through the write tool, and the unattended runner makes one at the end
// of an item — but the person watching a turn end had neither. They typed
// `git commit` into a shell in another window, or they asked the agent to do
// it and paid a round for the privilege. So the changed-files row offers the
// key, and the key opens a card.
//
// The card is where the two boundaries are stated before the commit is made,
// because they are the two a reader is entitled to check and cannot check
// afterwards: **the commit carries exactly this turn's changeset and never the
// reader's own uncommitted work**, and **nothing is pushed**. Both are
// properties of the arguments rather than promises — the paths are the turn's
// records, named one by one, and there is no push verb here to spell.
// See docs/capabilities/approvals-and-safety.md#the-writing-half-of-git-is-a-tool-too.
//
// The commit itself is run.Commit's, which is the unattended runner's: a
// commit is the one act of a turn that cannot be taken back, and two spellings
// of it are two places the rule about what it may carry can quietly disagree.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// commitState is the commit surface's own state: the card while it is up, the
// message while it is being typed, and — after the commit has landed — the
// two facts the rail goes on showing, which is why it outlives the card.
type commitState struct {
	// turn is the turn being committed. The receipt lands on that turn's
	// close row rather than at the bottom of the transcript, because the row
	// is where the reader asked the question.
	turn int64
	// message is the proposal as it now stands, edited or not.
	message string
	// field is the message as a draft; nil while the card has the keyboard.
	field *textinput.Model
	// staging is exactly what will be committed, each path with the bytes
	// the turn left there. Both counts on the card are read off this, so
	// what the card promises and what git is handed are the same list.
	staging []commitPath
	// added and removed are that list's counts, which are the turn's own
	// less anything left out of it.
	added, removed int
	// drifted are the turn's paths this commit will not carry, because what
	// is on disk is no longer what the turn wrote there.
	drifted []string
	// hooks is the checkout's trust answer, which is what decides whether
	// the repository's own commit hooks run.
	hooks bool
	// branch and ahead are what the card says about where this lands.
	branch, ahead string
	// leaves are the reader's own uncommitted paths — the ones this commit
	// deliberately does not carry.
	leaves []string
	// failure is why the last attempt made no commit, drawn on the card.
	failure string
	// running marks a commit that has been asked for and has not come back.
	// The card stays up and answers nothing while it is set: git is making
	// the commit or refusing it, and a card that let esc close it in between
	// would leave the commit landing on a row that had stopped listening for
	// the receipt.
	running bool
	// ret is the surface the card came from, which esc goes back to.
	ret state
	// banked is the commit once it has landed, for the rail's CHANGES block.
	banked *components.InspectorCommit
}

// commitPath is one file this commit will carry: the name git is given, the
// file it names, and the bytes the turn left in it.
//
// The bytes are carried rather than looked up again because they are what
// makes the card's promise checkable. `git add` stages whatever is on disk
// at the moment it runs, not what a record says should be there, so the only
// way to promise a commit carries the turn's work and nothing else is to
// know what the turn's work was and refuse anything that is no longer it.
type commitPath struct {
	// rel is the path as git is given it, relative to the workspace.
	rel string
	// abs is the file itself.
	abs string
	// want and exists are the file as the turn left it.
	want   string
	exists bool
}

// relPaths is the staging as git takes it, in order.
func relPaths(staging []commitPath) []string {
	out := make([]string, 0, len(staging))
	for _, p := range staging {
		out = append(out, p.rel)
	}
	return out
}

// driftedNow names the staged paths whose bytes are no longer the turn's. It
// is the same reading an undo takes before it writes (changeset.PlanUndo),
// taken a second time at the moment the commit is made: the card was drawn
// against a tree, and a tree that moved between the drawing and the answer
// makes the answer one the reader never gave.
func driftedNow(staging []commitPath) []string {
	var out []string
	for _, p := range staging {
		data, err := os.ReadFile(p.abs)
		if (err == nil) != p.exists || (err == nil && string(data) != p.want) {
			out = append(out, p.rel)
		}
	}
	return out
}

// commitDoneMsg is one commit coming back.
type commitDoneMsg struct {
	turn   int64
	sha    string
	branch string
	ahead  string
	files  int
	hooks  bool
	leaves []string
	err    error
}

// commitKey answers the changed-files row's commit offer. It claims the key
// only on a close row whose turn still has an uncommitted changeset, so the
// letter stays the draft's everywhere else.
func (m Model) commitKey(pressed string) (tea.Model, tea.Cmd, bool) {
	if !keys.Is(pressed, keys.Row.Commit) {
		return m, nil, false
	}
	e, ok := m.focusedClose()
	if !ok || e.close == nil || e.close.Changes == nil || e.close.Commit != nil {
		return m, nil, false
	}
	next, cmd := m.openCommitCard(m.focusIdx, e.turn)
	return next, cmd, true
}

// openCommitCard reads the repository once and puts the card up. Everything
// on it is read here rather than as it is drawn: a card is a decision, and a
// decision whose facts moved between being read and being answered is not the
// decision the reader took.
func (m Model) openCommitCard(row int, turn int64) (tea.Model, tea.Cmd) {
	t, ok := m.changes.Recall(turn)
	if !ok || t.Files() == 0 {
		return m.systemNotice(fmt.Sprintf("Turn %d changed no files; there is nothing to commit.", turn))
	}
	root := m.workspace
	// A path the reader has edited since the turn wrote it is left out
	// rather than carried, because `git add` stages what is on disk and what
	// is on disk is no longer only the turn's work. That is the same reading
	// an undo takes before it writes, and it has the same answer: the
	// content the record never saw is not this commit's to move.
	drift := map[string]bool{}
	for _, f := range changeset.PlanUndo(t, nil).Files {
		drift[f.Path()] = f.Drifted
	}
	var staging []commitPath
	var carried, left []string
	var added, removed int
	for _, r := range t.Records {
		if !r.Changed() {
			continue
		}
		rel := runRelPath(root, r.Path)
		if rel == "" {
			continue
		}
		if drift[r.Path] {
			left = append(left, rel)
			continue
		}
		staging = append(staging, commitPath{rel: rel, abs: r.Path, want: r.After, exists: r.AfterExists})
		carried = append(carried, rel)
		added, removed = added+r.Added, removed+r.Removed
	}
	if len(staging) == 0 {
		if len(left) > 0 {
			return m.systemNotice(fmt.Sprintf(
				"Every file turn %d wrote has changed since; there is nothing of the turn's own left to commit: %s.",
				turn, strings.Join(left, ", ")))
		}
		return m.systemNotice(fmt.Sprintf(
			"Turn %d changed nothing under %s, so there is nothing here to commit.", turn, root))
	}
	branch, ahead := commitBranch(root)
	st := &commitState{
		turn:    turn,
		message: m.proposedCommitMessage(row, carried),
		staging: staging,
		added:   added,
		removed: removed,
		drifted: left,
		hooks:   m.trust().Granted,
		branch:  branch,
		ahead:   ahead,
		// A drifted path is dirty too, and it has a field of its own that
		// says why it is being left out. Naming it in both would be the same
		// file twice under two headings.
		leaves: commitLeaves(root, append(append([]string{}, carried...), left...)),
		ret:    m.state,
	}
	m.commit = st
	m.enterSurface(stateCommitCard)
	m.syncViewport()
	return m, nil
}

// commitCard is the card as it now stands.
func (m Model) commitCard() components.CommitCard {
	st := m.commit
	if st == nil {
		return components.CommitCard{}
	}
	return components.CommitCard{
		Message: st.message,
		Files:   len(st.staging),
		Added:   st.added,
		Removed: st.removed,
		// The counts are the staging's own rather than the turn's, so the
		// row says what will be committed and not what was written. They are
		// the same number until something is left out of the commit, and the
		// moment they are not is the moment the difference matters.
		StagesNote: "exactly what this turn changed",
		Fields:     st.fields(),
		Failure:    st.failure,
		Running:    st.running,
	}
}

// fields are the statements under the counts, in the order a reader checks
// them: what is being left behind, where this lands, what runs on the way,
// and what does not happen at all.
//
// There is a fifth on the one card that needs it. A turn's own file the
// reader has edited since is neither of the first two things — it is not
// theirs and it is not the turn's any more — and a card that folded it into
// either would be saying something false about the file it is least safe to
// be wrong about.
func (st *commitState) fields() []components.CardField {
	leaves := components.CardField{
		Label: "leaves", Value: "nothing", Tone: components.ToneSafe,
		Detail: "your tree holds no other uncommitted work",
	}
	if n := len(st.leaves); n > 0 {
		// Named rather than counted, and named in the detail so the count
		// survives a narrow terminal: what the reader is checking is that a
		// particular file of theirs is not about to be swept into somebody
		// else's commit.
		leaves = components.CardField{
			Label: "leaves", Value: "your " + plural(n, "uncommitted edit"),
			Tone:   components.ToneOpen,
			Detail: strings.Join(st.leaves, ", ") + " changed by you, never staged",
		}
	}
	branch := components.CardField{
		Label: "branch", Value: st.branch, Tone: components.ToneNeutral, Detail: st.ahead,
	}
	if st.branch == "" {
		branch.Value, branch.Detail = "detached", "HEAD is on no branch"
	}
	hooks := components.CardField{
		Label: "hooks", Value: "pre-commit runs", Tone: components.ToneNeutral,
		Detail: "a hook failure cancels the commit and changes nothing",
	}
	if !st.hooks {
		// The same sentence the tool's receipt gives, because it is the same
		// fact: a checkout nobody trusts does not get to run programs as you
		// (docs/capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs).
		hooks.Value, hooks.Tone = "skipped", components.ToneChrome
		hooks.Detail = "this checkout is not trusted to run its own programs — /trust runs them"
	}
	fields := []components.CardField{leaves}
	if n := len(st.drifted); n > 0 {
		fields = append(fields, components.CardField{
			Label: "drifted", Value: plural(n, "file") + " left out",
			Tone:   components.ToneOpen,
			Detail: strings.Join(st.drifted, ", ") + " changed since the turn wrote it",
		})
	}
	return append(fields, branch, hooks, components.CardField{
		Label: "push", Value: "no", Tone: components.ToneSafe,
		Detail: "shhh never pushes; the remote is yours",
	})
}

// commitBranch is where the commit will land and how far that leaves the
// branch from its upstream. A branch tracking nothing has no distance to
// report, and says so rather than reporting one from nowhere
// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
func commitBranch(root string) (branch, ahead string) {
	branch, code := git(root, "branch", "--show-current")
	if code != 0 {
		return "", ""
	}
	upstream, code := git(root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if code != 0 || upstream == "" {
		return branch, "tracks no remote branch"
	}
	count, code := git(root, "rev-list", "--count", upstream+"..HEAD")
	if code != 0 {
		return branch, ""
	}
	n := 0
	if _, err := fmt.Sscanf(count, "%d", &n); err != nil {
		return branch, ""
	}
	return branch, fmt.Sprintf("will be %d ahead of %s", n+1, upstream)
}

// commitLeaves is the reader's own uncommitted work: what the tree reports
// changed, less what this commit is about to carry. It is the field the card
// exists to be able to state — the promise that a commit made on somebody's
// behalf does not sweep their morning into it.
func commitLeaves(root string, staging []string) []string {
	carrying := map[string]bool{}
	for _, p := range staging {
		carrying[filepath.ToSlash(p)] = true
	}
	var out []string
	for _, p := range run.DirtyPaths(root) {
		if !carrying[filepath.ToSlash(p)] {
			out = append(out, p)
		}
	}
	return out
}

// conventionalLead matches a subject that opens with a scope or a
// conventional-commit type — `agent: `, `feat(ui/chat): `. It is how the
// proposal reads whether this repository leads its subjects with one.
var conventionalLead = regexp.MustCompile(`^[a-z0-9][a-z0-9./-]*(\([^)]+\))?!?: `)

// proposedCommitMessage is the message the card opens with: what the reader
// asked this turn for, under a lead read off the paths it changed.
//
// It is mechanical on purpose. A message worth committing is the writer's,
// and a proposal that went to the model for one would spend a request and a
// wait on a line the reader is about to edit anyway. What it can do without
// asking anybody is put the two things it knows in the shape this repository
// already uses — so the lead is added only where the last twenty subjects use
// one, and left off where they do not.
func (m Model) proposedCommitMessage(row int, paths []string) string {
	subject := firstLine(m.turnAskAbove(row))
	if subject == "" {
		subject = "the changes from this turn"
	}
	subject = strings.TrimRight(strings.TrimSpace(subject), ".")
	if r := []rune(subject); len(r) > 0 {
		subject = strings.ToLower(string(r[0])) + string(r[1:])
	}
	if scope := commitScope(paths); scope != "" && m.leadsSubjectsWithAScope() {
		return scope + ": " + subject
	}
	return subject
}

// turnAskAbove is what the reader asked for, read backwards from the close
// row the key was pressed on. The transcript is walked rather than the
// agent's history because a close row belongs to one turn and the entry above
// it is that turn's own question, whatever has happened since.
func (m Model) turnAskAbove(row int) string {
	es := *m.entries()
	if row < 0 || row >= len(es) {
		return ""
	}
	for i := row; i >= 0; i-- {
		if es[i].kind == entryUser {
			return es[i].text
		}
	}
	return ""
}

// commitScope is the area the turn worked in, as the shortest path prefix
// every changed file shares, named by its last segment. Files with nothing in
// common have no scope, which is the honest answer — a scope invented from
// the first file would name a place the commit is only half about.
func commitScope(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	common := strings.Split(filepath.ToSlash(filepath.Dir(paths[0])), "/")
	for _, p := range paths[1:] {
		segs := strings.Split(filepath.ToSlash(filepath.Dir(p)), "/")
		for len(common) > len(segs) {
			common = common[:len(common)-1]
		}
		for i := range common {
			if common[i] != segs[i] {
				common = common[:i]
				break
			}
		}
	}
	if len(common) == 0 {
		return ""
	}
	last := common[len(common)-1]
	if last == "." || last == "" {
		return ""
	}
	return last
}

// commitHistoryDepth is how many subjects the proposal reads to decide
// whether this repository leads with a scope. Twenty is enough for a house
// style to show and short enough that a repository that changed its mind six
// months ago is not still deciding this.
const commitHistoryDepth = 20

// leadsSubjectsWithAScope reports whether most of the recent history opens its
// subjects with a scope or a conventional type. A repository with no history
// to read answers no: a lead invented for the first commit in a tree is a
// convention nobody chose.
func (m Model) leadsSubjectsWithAScope() bool {
	out, code := git(m.workspace, "log", fmt.Sprintf("-%d", commitHistoryDepth), "--format=%s")
	if code != 0 {
		return false
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	led, total := 0, 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		total++
		if conventionalLead.MatchString(l) {
			led++
		}
	}
	return total > 0 && led*2 > total
}

// updateCommitCard answers the card's keys.
func (m Model) updateCommitCard(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.commit == nil {
		return m.closeCommitCard()
	}
	if m.commit.running {
		// The keys are drawn and none of them is live, and the card says so
		// in words. A press here is not swallowed silently: what it means is
		// on the row it would have landed on (components/commitcard.go).
		return m, nil
	}
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Commit.Cancel):
		return m.closeCommitCard()
	case keys.Is(pressed, keys.Commit.Take):
		return m.makeCommit()
	case keys.Is(pressed, keys.Commit.Edit):
		return m.openCommitMessage()
	case keys.Is(pressed, keys.Commit.Hunks):
		// The staging surface /diff and a review open, rather than a second
		// one: what may be committed is one question with one answer.
		turn := m.commit.turn
		next, _ := m.closeCommitCard()
		return next.(Model).openReview(turn)
	}
	return m, nil
}

// closeCommitCard takes the card down without writing anything and hands the
// screen back to the row that offered it. The state survives only where a
// commit has already landed on it, because that is what the rail goes on
// reading.
func (m Model) closeCommitCard() (tea.Model, tea.Cmd) {
	ret := stateInput
	if st := m.commit; st != nil {
		ret = st.ret
		if st.banked == nil {
			m.commit = nil
		} else {
			kept := *st
			kept.field = nil
			m.commit = &kept
		}
	}
	if ret.isSurface() {
		m.state = ret
	} else {
		m.leaveSurface()
	}
	m.syncViewport()
	if m.state == stateFocus {
		m.refreshFocusView()
	}
	return m, nil
}

// openCommitMessage opens the proposal as a draft.
func (m Model) openCommitMessage() (tea.Model, tea.Cmd) {
	field := components.NewTextInput()
	field.Prompt = ""
	// The terminal's own cursor rather than a painted one: this session
	// places a real cursor wherever it is being typed into, and a field
	// painting a second one would draw two.
	field.SetVirtualCursor(false)
	field.SetValue(m.commit.message)
	field.CursorEnd()
	cmd := field.Focus()
	st := *m.commit
	st.field = &field
	m.commit = &st
	m.state = stateCommitMessage
	m.syncViewport()
	return m, cmd
}

// updateCommitMessage routes keys while the field has them. Every letter is
// text, which is why the register gives this field a row of its own: the two
// keys that are not letters are the whole of what it answers.
func (m Model) updateCommitMessage(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.commit == nil || m.commit.field == nil {
		return m.closeCommitCard()
	}
	st := *m.commit
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Select.Cancel):
		st.message = strings.TrimSpace(st.field.Value())
		st.field = nil
		m.commit = &st
		m.state = stateCommitCard
		m.syncViewport()
		return m, nil
	case keys.Is(pressed, keys.Select.Take):
		st.message = strings.TrimSpace(st.field.Value())
		st.field = nil
		m.commit = &st
		m.state = stateCommitCard
		return m.makeCommit()
	}
	field := *st.field
	field, cmd := field.Update(msg)
	st.field = &field
	m.commit = &st
	return m, cmd
}

// makeCommit stages the turn's own paths and commits them. An empty message
// is refused on the card rather than turned into a commit nobody can read.
func (m Model) makeCommit() (tea.Model, tea.Cmd) {
	st := *m.commit
	if strings.TrimSpace(st.message) == "" {
		st.failure = "a commit needs a message; [e] writes one"
		m.commit = &st
		return m, nil
	}
	st.failure, st.running = "", true
	m.commit = &st
	root, turn := m.workspace, st.turn
	staging, message, hooks := st.staging, st.message, st.hooks
	paths := relPaths(staging)
	// The way through when a repository will not take a commit is to leave
	// the changeset where it is, which is what esc already does — so the
	// sentence names the key rather than a flag nobody typed.
	without := "esc leaves the changeset uncommitted"
	return m, func() tea.Msg {
		// The last thing before the index is touched: the card was drawn
		// against a tree, and the reader has had it on screen for as long as
		// they wanted. A file that moved in that window is one this commit
		// was never asked to carry, and the honest answer is to make no
		// commit at all rather than to quietly carry less than the card said.
		if moved := driftedNow(staging); len(moved) > 0 {
			return commitDoneMsg{turn: turn, err: fmt.Errorf(
				"%s changed since this card was drawn; nothing was committed — press the commit key again to read the tree afresh",
				strings.Join(moved, ", "))}
		}
		files, err := run.Commit(root, paths, message, without, hooks)
		if err != nil {
			// The card said a hook failure cancels and changes nothing, and
			// the staging is a change: git add succeeds and git commit is
			// what the hook refuses, so a tree left alone here would be one
			// whose index the reader never asked for. The paths are the ones
			// this attempt staged and no others, and the index was empty
			// before it — run.Commit refuses a tree that already holds
			// staged work it did not make.
			if stuck := unstage(root, paths); stuck != "" {
				err = fmt.Errorf("%w; %s", err, stuck)
			}
			return commitDoneMsg{turn: turn, err: err}
		}
		sha, _ := git(root, "rev-parse", "--short", "HEAD")
		branch, ahead := commitBranch(root)
		return commitDoneMsg{
			turn: turn, sha: sha, branch: branch, ahead: ahead,
			files: len(files), hooks: hooks, leaves: commitLeaves(root, nil),
		}
	}
}

// unstage puts the index back the way a cancelled commit found it. It is the
// one git this surface runs that writes anything, it writes only the index,
// and it names the paths it takes out — the same rule the staging obeys, in
// reverse. The file itself is untouched: `--staged` is what makes this an
// undo of the add rather than an undo of the work.
//
// What it says when it cannot is the point of the return value. A refused
// commit that also failed to put the index back leaves a tree the next
// attempt will refuse for the wrong reason — run.Commit sees staged work and
// says it belongs to somebody else — so the reader is told here, on the card
// they are already looking at, rather than being sent to work it out from a
// sentence about a stranger's index.
func unstage(root string, paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	out, code := git(root, append([]string{"restore", "--staged", "--"}, paths...)...)
	if code == 0 {
		return ""
	}
	return "the staging could not be undone either, so the index still holds " +
		plural(len(paths), "file") + ": " + out
}

// finishCommit applies the outcome. A commit that failed leaves the card up
// with the reason on it and the changeset untouched — a pre-commit hook that
// exits non-zero cancels the whole thing, which is what the card said it
// would do.
func (m Model) finishCommit(msg commitDoneMsg) (tea.Model, tea.Cmd) {
	st := m.commit
	if st == nil || st.turn != msg.turn {
		return m, nil
	}
	if msg.err != nil {
		next := *st
		next.running = false
		next.failure = strings.TrimSpace(msg.err.Error())
		m.commit = &next
		m.state = stateCommitCard
		m.syncViewport()
		return m, nil
	}
	next := *st
	next.running, next.field = false, nil
	next.leaves = msg.leaves
	next.banked = &components.InspectorCommit{SHA: msg.sha, Files: msg.files, Ahead: bankedAhead(msg.ahead)}
	m.commit = &next
	m.bankTurnClose(msg)
	updated, cmd := m.closeCommitCard()
	m = updated.(Model)
	m.invalidateRenderCache()
	m.syncViewport()
	if m.state == stateFocus {
		m.refreshFocusView()
	} else {
		m.viewport.SetLines(m.renderHistoryLines())
	}
	return m, cmd
}

// bankTurnClose puts the receipt on the row that offered the key and takes
// off it the offers a committed changeset no longer has.
//
// The row is found by the turn it closed rather than by the index the card
// was opened at: a transcript can grow or be trimmed while git has the
// question, and a commit that landed on whatever row had moved into position
// 14 would be the worst kind of wrong — a receipt about work it is not about.
func (m *Model) bankTurnClose(msg commitDoneMsg) {
	es := m.entries()
	for i := range *es {
		e := &(*es)[i]
		if e.kind != entryTurnClose || e.turn != msg.turn || e.close == nil {
			continue
		}
		e.close.Commit = &components.TurnCommit{
			Receipt: firstLine(structural.CommitReceipt(msg.files, msg.sha, msg.branch, msg.hooks)),
		}
		if e.close.Changes != nil {
			// The two offers a committed changeset no longer has: undo,
			// because the honest way back from a commit is `git revert` and
			// the receipt row says so, and the commit key, which is spent.
			e.close.Changes.Keys = []components.TurnKey{
				{Key: keys.Bracket(keys.Row.Review), Label: keys.Words(keys.Row.Review)},
			}
		}
		return
	}
}

// bankedAhead is the rail's wording for the same distance the card stated in
// the future tense. The card said what a commit would do and the rail says
// what it did, so the verb moves and the number does not.
func bankedAhead(ahead string) string {
	return strings.TrimPrefix(ahead, "will be ")
}

// commitCardLines is the card as the panel draws it.
func (m Model) commitCardLines() []string {
	if m.commit == nil {
		return nil
	}
	return strings.Split(m.commitCard().View(m.contentWidth()), "\n")
}

// commitMessageLines is the message as a draft under its own labelled rail,
// which is what says the keyboard is in the field and not on the card
// (docs/interface/surfaces.md#reading-mode).
func (m Model) commitMessageLines() []string {
	st := m.commit
	if st == nil || st.field == nil {
		return nil
	}
	width := m.contentWidth()
	field := m.commitField(width)
	editor := components.CommitMessage{
		Subject: firstLine(field.Value()),
		Rows:    strings.Split(field.View(), "\n"),
	}
	lines := []string{keyboardRail("COMMIT MESSAGE", width)}
	return append(lines, strings.Split(editor.View(width), "\n")...)
}

// commitField is the field as the editor draws it: sized to the room the
// frame leaves it and repainted from the palette as it stands now. Both the
// render and the cursor go through here, because a field's caret is clamped
// to its width and an unsized copy puts the caret past the frame's edge the
// moment the line outgrows the row.
func (m Model) commitField(width int) textinput.Model {
	field := *m.commit.field
	field.SetWidth(max(components.FieldWidth(width)-2, 8))
	components.StyleTextInput(&field)
	return field
}

// commitCursor puts the terminal's own cursor in the field.
func (m Model) commitCursor(int) *tea.Cursor {
	if m.commit == nil || m.commit.field == nil {
		return nil
	}
	width := m.contentWidth()
	cur := m.commitField(width).Cursor()
	if cur == nil {
		return nil
	}
	// The rail, the frame's top edge, and the `▸ ` the row is led with.
	cur.X += 2 + 2
	cur.Y += 2
	return cur
}
