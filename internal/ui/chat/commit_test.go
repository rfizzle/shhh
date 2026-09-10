package chat

// The commit offer and the card behind it. The two facts these tests exist
// for are the two the card promises and a reader cannot check afterwards:
// the commit carries exactly the turn's own changeset, and nothing is pushed.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// commitRepo is a repository with one committed file, one the turn is about
// to change, and one the reader changed by hand and never staged.
func commitRepo(t *testing.T) (Model, string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	write("loop.go", "package agent\n")
	write("README.md", "# project\n")
	run("add", "loop.go", "README.md")
	run("commit", "-q", "-m", "seed")
	// The reader's own morning, changed by hand and never staged.
	mine := write("README.md", "# project\n\nmine\n")

	m := gatedModel(t, nil, nil).WithWorkspace(root).
		WithChangeset(nil, changeset.NewTracker(root))
	m.state = stateInput
	m = sendText(t, m, "cap rounds at the limit instead of erroring")
	m = applyWrite(t, m, filepath.Join(root, "round.go"), "package agent\n\nfunc Round() {}\n", "y")
	m = finishTurn(t, m)
	return m, root, mine
}

// focusLastClose puts the cursor on the close row the turn left, which is how
// every one of its offers is reached.
func focusLastClose(t *testing.T, m Model) Model {
	t.Helper()
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].kind == entryTurnClose {
			m.state, m.focusIdx = stateFocus, i
			return m
		}
	}
	t.Fatal("the finished turn appended no close row")
	return m
}

// typeLetter sends one bare letter through the session's own routing.
func typeLetter(t *testing.T, m Model, key string) (Model, tea.Cmd) {
	t.Helper()
	return pressKey(t, m, tea.KeyPressMsg{Code: []rune(key)[0], Text: key})
}

// settle runs the commands a press produced and feeds their messages back,
// which is what makes the commit land in a test the way it lands in a session.
func settle(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for _, msg := range drain(cmd) {
		updated, next := m.Update(msg)
		m = updated.(Model)
		if next != nil {
			m = settle(t, m, next)
		}
	}
	return m
}

func TestCommitOffer_TheChangedFilesRowOffersReviewCommitAndUndo(t *testing.T) {
	m, _, _ := commitRepo(t)
	c := lastClose(t, m)
	if c.Changes == nil {
		t.Fatal("a turn that wrote a file gets a changeset row")
	}
	view := ansi.Strip(c.View(120))
	for _, want := range []string{
		keys.Bracket(keys.Row.Review) + " review",
		keys.Bracket(keys.Row.Commit) + " commit",
		keys.Bracket(keys.Row.Undo) + " undo turn",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the row should offer %q, got:\n%s", want, view)
		}
	}
	// The order is review, keep, take back: the three things a changeset can
	// become, in the order a reader meets them.
	vi := strings.Index(view, keys.Bracket(keys.Row.Review))
	gi := strings.Index(view, keys.Bracket(keys.Row.Commit))
	ui := strings.Index(view, keys.Bracket(keys.Row.Undo))
	if vi >= gi || gi >= ui {
		t.Fatalf("the offers are out of order in:\n%s", view)
	}
}

func TestCommitCard_StatesTheTurnsOwnFilesAndLeavesTheReadersAlone(t *testing.T) {
	m, _, _ := commitRepo(t)
	m = focusLastClose(t, m)
	m, _ = typeLetter(t, m, keys.Shown(keys.Row.Commit))
	if m.state != stateCommitCard || m.commit == nil {
		t.Fatalf("the commit key should open the card, got state %v", m.state)
	}
	// The whole of what will be staged, by name.
	if got := relPaths(m.commit.staging); len(got) != 1 || got[0] != "round.go" {
		t.Fatalf("the commit stages the turn's own paths, got %v", got)
	}
	// And the reader's own edit is on the card as what is being left behind.
	if got := m.commit.leaves; len(got) != 1 || got[0] != "README.md" {
		t.Fatalf("the reader's uncommitted edit should be named, got %v", got)
	}
	view := ansi.Strip(m.commitCard().View(120))
	for _, want := range []string{
		"Commit this turn", "stages", "1 file", "leaves", "README.md",
		"branch", "hooks", "push", "no", "shhh never pushes",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the card should state %q, got:\n%s", want, view)
		}
	}
}

// The boundary, stated as an argument rather than as a promise: the paths the
// commit is built from are the turn's records, so a file the reader changed
// cannot reach the index however the card is answered.
func TestCommitCard_NeverStagesAPathTheTurnDidNotWrite(t *testing.T) {
	m, root, mine := commitRepo(t)
	m = focusLastClose(t, m)
	m, _ = typeLetter(t, m, keys.Shown(keys.Row.Commit))
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = settle(t, next.(Model), cmd)
	if m.commit == nil || m.commit.banked == nil {
		t.Fatalf("the commit should have landed: %+v", m.commit)
	}
	out, code := git(root, "show", "--name-only", "--format=", "HEAD")
	if code != 0 {
		t.Fatalf("git show: %s", out)
	}
	if strings.Contains(out, "README.md") {
		t.Fatalf("the reader's own file reached the commit: %s", out)
	}
	if !strings.Contains(out, "round.go") {
		t.Fatalf("the turn's own file is missing from the commit: %s", out)
	}
	// And it is still theirs, still uncommitted, untouched.
	body, err := os.ReadFile(mine)
	if err != nil || !strings.Contains(string(body), "mine") {
		t.Fatalf("the reader's edit was not left alone: %v %q", err, body)
	}
	if out, _ := git(root, "status", "--porcelain"); !strings.Contains(out, "README.md") {
		t.Fatalf("the reader's edit should still be uncommitted, got %q", out)
	}
}

// The same boundary, on the file the turn itself wrote. `git add` stages
// what is on disk and not what a record says should be there, so a path the
// reader has edited since the turn wrote it carries their bytes as well as
// the turn's. It is left out of the commit and said to be, rather than
// carried under a card that claims it is "exactly what this turn changed".
func TestCommitCard_LeavesOutAFileTheReaderChangedSinceTheTurn(t *testing.T) {
	m, root, _ := commitRepo(t)
	// The reader's own hand on the turn's own file, after the turn closed.
	if err := os.WriteFile(filepath.Join(root, "round.go"),
		[]byte("package agent\n\nfunc Round() int { return 99 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = focusLastClose(t, m)
	next, _ := typeLetter(t, m, keys.Shown(keys.Row.Commit))
	m = next

	// One file, and it was the only one: the card cannot be opened at all,
	// and the notice names the file rather than saying nothing happened.
	if m.state == stateCommitCard {
		t.Fatalf("the card should not open with nothing of the turn's own left: %+v", m.commit)
	}
	notice := lastSystemNotice(t, m)
	if !strings.Contains(notice, "round.go") || !strings.Contains(notice, "changed since") {
		t.Fatalf("the notice should name the file and why, got %q", notice)
	}
	if out, _ := git(root, "log", "--oneline"); strings.Count(out, "\n") != 0 {
		t.Fatalf("nothing should have been committed, got:\n%s", out)
	}
}

// And the same check again at the moment the index is touched, because the
// card can sit on screen for as long as the reader likes.
func TestCommitCard_RefusesWhenTheTreeMovedUnderTheCard(t *testing.T) {
	m, root, _ := commitRepo(t)
	m = focusLastClose(t, m)
	m, _ = typeLetter(t, m, keys.Shown(keys.Row.Commit))
	if m.state != stateCommitCard {
		t.Fatalf("the card should be up, got %v", m.state)
	}
	// The reader edits the file while the card is on screen.
	if err := os.WriteFile(filepath.Join(root, "round.go"),
		[]byte("package agent\n\nfunc Round() int { return 99 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = settle(t, next.(Model), cmd)

	if m.commit == nil || m.commit.banked != nil {
		t.Fatalf("nothing should have been committed, got %+v", m.commit)
	}
	if !strings.Contains(m.commit.failure, "round.go") {
		t.Fatalf("the card should name the file that moved, got %q", m.commit.failure)
	}
	if out, _ := git(root, "diff", "--cached", "--name-only"); out != "" {
		t.Fatalf("a refused commit stages nothing, got %q", out)
	}
	if out, _ := git(root, "log", "--oneline"); strings.Count(out, "\n") != 0 {
		t.Fatalf("history should not have moved, got:\n%s", out)
	}
}

// The other boundary. Every git this surface runs itself is a read, save the
// one that puts the index back after a cancelled commit; the only thing that
// writes the repository is the shared commit, whose argv is the write tool's
// four verbs and has no field for a push. A verb added here that is not on
// this list is a surface reaching further than the card said it would.
func TestCommitCard_RunsNoGitThatWrites(t *testing.T) {
	body, err := os.ReadFile("commit.go")
	if err != nil {
		t.Fatal(err)
	}
	reads := map[string]bool{
		"branch": true, "rev-parse": true, "rev-list": true, "log": true,
		// The undo of a staging a hook refused, which writes the index and
		// nothing else, and only the paths it names.
		"restore": true,
	}
	calls := regexp.MustCompile(`git\(\w+, (?:append\(\[\]string\{)?"([a-z-]+)"`).
		FindAllStringSubmatch(string(body), -1)
	if len(calls) < 4 {
		t.Fatalf("the readings this card is built from are gone; check what replaced them: %v", calls)
	}
	for _, c := range calls {
		if !reads[c[1]] {
			t.Fatalf("git %q is not a reading; this surface runs no git that writes", c[1])
		}
	}
	for _, spelling := range []string{"git push", "--set-upstream", "--force"} {
		if strings.Contains(string(body), spelling) {
			t.Fatalf("the commit surface must never spell %q", spelling)
		}
	}
}

// The receipt lands on the row that offered the key, and the two offers a
// committed changeset no longer has come off it.
func TestCommit_TheReceiptLandsOnTheCloseRowAndWithdrawsUndo(t *testing.T) {
	m, _, _ := commitRepo(t)
	m = focusLastClose(t, m)
	row := m.focusIdx
	m, _ = typeLetter(t, m, keys.Shown(keys.Row.Commit))
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = settle(t, next.(Model), cmd)

	c := m.transcript[row].close
	if c == nil || c.Commit == nil {
		t.Fatalf("the close row should carry the receipt, got %+v", c)
	}
	if !strings.Contains(c.Commit.Receipt, "committed 1 file as ") {
		t.Fatalf("the receipt is the tool's own wording, got %q", c.Commit.Receipt)
	}
	view := ansi.Strip(c.View(120))
	if strings.Contains(view, keys.Bracket(keys.Row.Undo)) {
		t.Fatalf("a committed changeset offers no undo key, got:\n%s", view)
	}
	if strings.Contains(view, keys.Bracket(keys.Row.Commit)) {
		t.Fatalf("the commit key has been spent, got:\n%s", view)
	}
	if !strings.Contains(view, keys.Bracket(keys.Row.Review)) {
		t.Fatalf("review survives a commit, got:\n%s", view)
	}
	// And the rail says what was banked and what is still floating.
	ch := m.inspectorChanges()
	if ch == nil || ch.Committed == nil || ch.Committed.SHA == "" {
		t.Fatalf("the CHANGES block should name the commit, got %+v", ch)
	}
	if len(ch.Foreign) != 1 || ch.Foreign[0] != "README.md" {
		t.Fatalf("the CHANGES block should name what is still yours, got %v", ch.Foreign)
	}
}

// A pre-commit hook that exits non-zero cancels the whole thing, which is
// what the card said it would do: no commit, the changeset where it was, and
// the reason on the card rather than in a notice the reader has to go find.
func TestCommit_AFailingHookCancelsAndChangesNothing(t *testing.T) {
	m, root, _ := commitRepo(t)
	hooks := filepath.Join(root, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"),
		[]byte("#!/bin/sh\necho 'gofmt -l found 2 files' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	before, _ := git(root, "rev-parse", "HEAD")

	m = focusLastClose(t, m)
	row := m.focusIdx
	m, _ = typeLetter(t, m, keys.Shown(keys.Row.Commit))
	// The hooks the card promises to run are the checkout's, and they run
	// only where the checkout is trusted.
	m.commit.hooks = true
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = settle(t, next.(Model), cmd)

	if m.state != stateCommitCard {
		t.Fatalf("a refused commit leaves the card up, got state %v", m.state)
	}
	if m.commit == nil || m.commit.failure == "" {
		t.Fatalf("the card should carry the reason, got %+v", m.commit)
	}
	if m.commit.banked != nil {
		t.Fatalf("nothing was committed, so nothing was banked: %+v", m.commit.banked)
	}
	if after, _ := git(root, "rev-parse", "HEAD"); after != before {
		t.Fatalf("history moved: %s → %s", before, after)
	}
	if out, _ := git(root, "diff", "--cached", "--name-only"); out != "" {
		t.Fatalf("a cancelled commit leaves nothing staged, got %q", out)
	}
	if c := m.transcript[row].close; c == nil || c.Commit != nil {
		t.Fatalf("the close row gets no receipt for a commit that did not happen: %+v", c)
	}
}

// A commit that has been asked for and not come back answers nothing. Esc
// here would take the card down while git still had the question, and the
// receipt would land on a row that had stopped listening for it.
func TestCommitCard_AnswersNothingWhileGitHasIt(t *testing.T) {
	m, _, _ := commitRepo(t)
	m = focusLastClose(t, m)
	m, _ = typeLetter(t, m, keys.Shown(keys.Row.Commit))
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.commit == nil || !m.commit.running {
		t.Fatalf("enter should mark the commit in flight, got %+v", m.commit)
	}
	// The card says so where the keys were, rather than dimming them and
	// leaving the reader to work out why nothing happens.
	if !strings.Contains(ansi.Strip(m.commitCard().View(120)), "committing") {
		t.Fatal("the card should say the keys are not live and why")
	}
	for _, press := range []tea.KeyPressMsg{
		{Code: tea.KeyEscape}, {Code: 'e', Text: "e"}, {Code: tea.KeyEnter},
	} {
		after, _ := m.Update(press)
		m = after.(Model)
		if m.state != stateCommitCard || m.commit == nil {
			t.Fatalf("%v answered a card that is not listening", press)
		}
	}
	// And the commit still lands on the row that asked for it.
	m = settle(t, m, cmd)
	if c := m.transcript[m.focusIdx].close; c == nil || c.Commit == nil {
		t.Fatalf("the receipt should still reach the row, got %+v", c)
	}
}

func TestCommitCard_EscLeavesTheChangesetAndTheOffer(t *testing.T) {
	m, root, _ := commitRepo(t)
	before, _ := git(root, "rev-parse", "HEAD")
	m = focusLastClose(t, m)
	row := m.focusIdx
	m, _ = typeLetter(t, m, keys.Shown(keys.Row.Commit))
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)

	if m.state != stateFocus {
		t.Fatalf("esc goes back to the row that offered the key, got %v", m.state)
	}
	if m.commit != nil {
		t.Fatalf("nothing was committed, so nothing is kept: %+v", m.commit)
	}
	if after, _ := git(root, "rev-parse", "HEAD"); after != before {
		t.Fatalf("esc wrote history: %s → %s", before, after)
	}
	view := ansi.Strip(m.transcript[row].close.View(120))
	if !strings.Contains(view, keys.Bracket(keys.Row.Commit)) {
		t.Fatalf("the offer should still be on the row, got:\n%s", view)
	}
}

func TestCommitMessage_IsADraftThatKeepsItsEditsOnEsc(t *testing.T) {
	m, _, _ := commitRepo(t)
	m = focusLastClose(t, m)
	m, _ = typeLetter(t, m, keys.Shown(keys.Row.Commit))
	proposed := m.commit.message
	if proposed == "" {
		t.Fatal("the card opens with a proposal")
	}
	m, _ = typeLetter(t, m, keys.Shown(keys.Commit.Edit))
	if m.state != stateCommitMessage || m.commit.field == nil {
		t.Fatalf("[e] opens the message as a draft, got state %v", m.state)
	}
	// Every letter is text while the field has the keyboard, including the
	// ones the card answers to.
	for _, r := range "!s" {
		next, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = next.(Model)
	}
	if m.state != stateCommitMessage {
		t.Fatalf("a letter typed into the field must not answer the card, got %v", m.state)
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if m.state != stateCommitCard {
		t.Fatalf("esc comes back to the card, got %v", m.state)
	}
	if m.commit.message != proposed+"!s" {
		t.Fatalf("the edits are kept, got %q", m.commit.message)
	}
	if !strings.Contains(ansi.Strip(m.commitCard().View(120)), m.commit.message) {
		t.Fatal("the card should show the message as it now stands")
	}
}

// The proposal is mechanical: the turn's own question under a lead read off
// the paths it changed, and no lead at all where the history has none.
func TestProposedCommitMessage_ReadsTheTurnAndThePaths(t *testing.T) {
	m, _, _ := commitRepo(t)
	m = focusLastClose(t, m)
	got := m.proposedCommitMessage(m.focusIdx, []string{"loop.go"})
	if !strings.Contains(got, "cap rounds at the limit instead of erroring") {
		t.Fatalf("the proposal should carry the turn's own words, got %q", got)
	}
	// The seed commit's subject carries no lead, so neither does this.
	if strings.Contains(got, ": ") {
		t.Fatalf("a repository whose subjects have no lead gets none, got %q", got)
	}
	if got := commitScope([]string{"internal/agent/loop.go", "internal/agent/round.go"}); got != "agent" {
		t.Fatalf("the scope is the shared directory's last segment, got %q", got)
	}
	if got := commitScope([]string{"internal/agent/loop.go", "docs/product.md"}); got != "" {
		t.Fatalf("paths with nothing in common have no scope, got %q", got)
	}
}

// The checks row's own offer, which is present only where there is a suite to
// run again: re-running a command the turn happened to run would be shhh
// executing a line nobody is looking at.
func TestChecksRow_OffersTheRerunOnlyForASuite(t *testing.T) {
	gate := []entry{{kind: entryTool, toolName: quality.ToolName,
		toolResult: `Quality gate "default": PASS — 4/4 checks passed (1s)`}}
	if c := turnChecksRow(gate, true); c == nil || len(c.Keys) != 1 ||
		c.Keys[0].Key != keys.Bracket(keys.Row.Rerun) {
		t.Fatalf("a gate verdict offers the rerun, got %+v", c)
	}
	if c := turnChecksRow(gate, false); c == nil || len(c.Keys) != 0 {
		t.Fatalf("a session with no gate offers nothing, got %+v", c)
	}
	cmdRow := []entry{{kind: entryCommand, text: "go test ./internal/agent/..."}}
	if c := turnChecksRow(cmdRow, true); c == nil || len(c.Keys) != 0 {
		t.Fatalf("a command verdict is never re-run from a row, got %+v", c)
	}
	if got := suiteOfTurn(gate); got != "default" {
		t.Fatalf("the suite is read back off the gate row, got %q", got)
	}
}

// And the offer reaches the suite through the door /gate already opens, so
// there is one way to start a run whoever asked for it.
func TestChecksRow_TheRerunReachesTheSameRunAsTheCommand(t *testing.T) {
	m, _ := closeGateModel(t, quality.VerdictPass)
	asked := ""
	gate := m.gate
	gate.Manage = func(args []string) string {
		asked = strings.Join(args, " ")
		return "the suite is running"
	}
	m = m.WithGate(gate)
	m = closeTurnWithGate(t, startEditedTurn(t, m))
	m = focusLastClose(t, m)
	next, _ := typeLetter(t, m, keys.Shown(keys.Row.Rerun))
	if asked != "run fast" {
		t.Fatalf("the rerun should ask for the suite the verdict came from, got %q", asked)
	}
	if !strings.Contains(lastSystemNotice(t, next), "the suite is running") {
		t.Fatal("the reader should be told the run started")
	}
}

// lastSystemNotice is the newest notice the session left in the transcript.
func lastSystemNotice(t *testing.T, m Model) string {
	t.Helper()
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].kind == entrySystem {
			return m.transcript[i].text
		}
	}
	t.Fatal("the session left no notice")
	return ""
}

// The keys the register gave these two surfaces are the ones the surfaces
// answer to, which is the whole of what the register is for.
func TestCommitKeys_AreTheRegistersOwn(t *testing.T) {
	if got := keys.Shown(keys.Row.Commit); got != "g" {
		t.Fatalf("the row's commit key is %q; [c] is continue-from-here", got)
	}
	var card components.CommitCard
	view := ansi.Strip(card.View(110))
	for _, b := range keys.Commit.All() {
		if !strings.Contains(view, keys.Bracket(b)) {
			t.Fatalf("the card should offer %s, got:\n%s", keys.Bracket(b), view)
		}
	}
}
