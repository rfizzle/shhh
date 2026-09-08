package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// blockingEnv builds children whose stream blocks until the child context is
// cancelled, so tests can observe a "running" child deterministically.
func blockingEnv() subagent.EnvFactory {
	return func(ctx context.Context, spec subagent.Spec) (subagent.Env, error) {
		stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			ch := make(chan provider.StreamEvent)
			go func() {
				<-ctx.Done()
				close(ch)
			}()
			return ch, func() {}, nil
		}
		return subagent.Env{
			SystemPrompt: "sys",
			Stream:       stream,
			Executor:     func(string, json.RawMessage) (string, error) { return "", errors.New("unused") },
		}, nil
	}
}

func newSubagentModel(t *testing.T, sup *subagent.Supervisor) Model {
	t.Helper()
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream).WithSubagents(sup)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return updated.(Model)
}

func TestChildAskApprove(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	ask := subagent.NewAsk("writer-1", subagent.AskCommand, "run echo hi")
	ask.Command = "echo hi"
	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventAsk, Ask: ask}})
	m = updated.(Model)

	if m.activeChildAsk() == nil {
		t.Fatal("routed ask should be presentable")
	}
	view := m.View().Content
	if !strings.Contains(view, "writer-1") || !strings.Contains(view, "echo hi") {
		t.Fatalf("view missing labeled child ask:\n%s", view)
	}

	m = handover(t, m)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	approved, ok := ask.Answered()
	if !ok || !approved {
		t.Fatalf("ask not approved: approved=%v ok=%v", approved, ok)
	}
	if m.activeChildAsk() != nil {
		t.Fatal("answered ask should be popped")
	}
	if !transcriptContains(m, "Approved writer-1") {
		t.Fatal("transcript missing the approval entry")
	}
}

// Esc on a routed card hands the keyboard back and leaves the request
// waiting; [n] is what declines it.
func TestChildAskDecline(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	ask := subagent.NewAsk("researcher-1", subagent.AskGeneric, "use web_fetch")
	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventAsk, Ask: ask}})
	m = updated.(Model)
	m = handover(t, m)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if _, answered := ask.Answered(); answered {
		t.Fatal("esc leaves a routed request waiting, it does not answer it")
	}
	if m.activeChildAsk() != ask {
		t.Fatal("the request stays on screen after esc")
	}

	m = handover(t, m)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = updated.(Model)

	approved, ok := ask.Answered()
	if !ok || approved {
		t.Fatalf("[n] should decline: approved=%v ok=%v", approved, ok)
	}
	if !transcriptContains(m, "Declined researcher-1") {
		t.Fatal("transcript missing the decline entry")
	}
}

func TestChildAskDefersToParentPrompt(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	m.childAsks = []*subagent.Ask{subagent.NewAsk("writer-1", subagent.AskCommand, "run x")}
	m.state = stateConfirmRun
	if m.activeChildAsk() != nil {
		t.Fatal("child ask must defer while the parent's own prompt is up")
	}
	m.state = stateStreaming
	if m.activeChildAsk() == nil {
		t.Fatal("child ask should present while the parent streams")
	}
}

func TestEventDoneAddsTranscriptEntry(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	ev := subagent.Event{Kind: subagent.EventDone, Status: subagent.Status{Name: "researcher-1", Detail: "done · 3 tools"}}
	updated, _ := m.Update(subagentEventMsg{ev: ev})
	m = updated.(Model)
	if !transcriptContains(m, "Agent researcher-1: done · 3 tools") {
		t.Fatal("transcript missing the completion entry")
	}
}

func TestAgentRowsAndBadge(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	exec := sup.WrapExecutor(nil)
	if _, err := exec(subagent.SpawnToolName, json.RawMessage(`{"role":"researcher","task":"long survey"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { a, _ := sup.ActiveCounts(); return a == 1 })

	view := m.View().Content
	if !strings.Contains(view, "researcher-1") {
		t.Fatalf("view missing the agent progress row:\n%s", view)
	}
	if bar := m.renderStatusBar(120); !strings.Contains(bar, "1 agent") {
		t.Fatalf("status bar missing the agent badge: %q", bar)
	}
	if m.agentRowsHeight() != 1 {
		t.Fatalf("agentRowsHeight = %d, want 1", m.agentRowsHeight())
	}

	// Cancelling the tree clears the rows once the child finishes.
	m.cancelSubagents()
	waitFor(t, func() bool { a, _ := sup.ActiveCounts(); return a == 0 })
	if m.agentRowsHeight() != 0 {
		t.Fatal("finished children must not occupy progress rows")
	}
}

// A child's patch reaches the changeset by a different road than the
// session's own edits, and until it carried the mode it was the road that
// lost the execute bit: undoing a turn that accepted a child's deletion gave
// the script back at the default mode, and running it failed with permission
// denied.
func TestRecordChildPatch_UndoRestoresADeletedScriptsMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "script.sh")
	sup := subagent.New(context.Background(), subagent.Options{Root: dir, NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	// The patch has already landed, so the script is gone from the workspace
	// and the record is the only account left of what it was.
	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventPatch, Patch: &subagent.PatchApplied{
		Agent: "writer-1",
		Files: []subagent.PatchedFile{{
			Path:         script,
			Before:       "#!/bin/sh\necho hi\n",
			BeforeExists: true,
			BeforeMode:   0o755,
		}},
	}}})
	m = updated.(Model)

	turn, ok := m.changes.Latest()
	if !ok || len(turn.Records) != 1 {
		t.Fatalf("the child's patch should be one record in the turn, got %+v", turn)
	}
	if turn.Records[0].BeforeMode != 0o755 {
		t.Fatalf("the record dropped the mode, got %o", turn.Records[0].BeforeMode)
	}

	out := changeset.PlanUndo(turn, nil).Apply(false)
	if len(out.Failed) != 0 {
		t.Fatalf("putting the script back should not fail: %+v", out.Failed)
	}
	fi, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("the restored script should be executable, got %o", fi.Mode().Perm())
	}
}

// A patch that only made a script executable carries its whole change in the
// two modes: git writes one as a header with no hunk, so the bytes are
// identical on both sides and nothing but the modes says anything happened.
// The turn is one to undo like any other, and its row states the modes where
// a file with a diff states its counts.
func TestRecordChildPatch_AModeOnlyPatchIsATurnToUndo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sup := subagent.New(context.Background(), subagent.Options{Root: dir, NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventPatch, Patch: &subagent.PatchApplied{
		Agent: "writer-1",
		Files: []subagent.PatchedFile{{
			Path:         script,
			Before:       "#!/bin/sh\necho hi\n",
			After:        "#!/bin/sh\necho hi\n",
			BeforeExists: true,
			AfterExists:  true,
			BeforeMode:   0o644,
			AfterMode:    0o755,
		}},
	}}})
	m = updated.(Model)

	turn, ok := m.changes.Latest()
	if !ok || len(turn.Records) != 1 {
		t.Fatalf("a patch that changed a mode should be one record in the turn, got %+v", turn)
	}
	if got := turn.Records[0].ModeChange(); got != "mode 0644 → 0755" {
		t.Fatalf("the record should carry both modes, got %q", got)
	}

	row := m.turnChangesRow(false)
	if row == nil || row.Mode != "mode 0644 → 0755" {
		t.Fatalf("the changeset row should state the mode, got %+v", row)
	}
	view := plainView(&components.TurnClose{Changes: row}, 100)
	if !strings.Contains(view, "1 file changed") || !strings.Contains(view, "mode 0644 → 0755") {
		t.Fatalf("the row should state the mode where its counts would be, got:\n%s", view)
	}

	// The review of the turn has no hunks to show for the file, so it says
	// the same thing the row does rather than a bare `+0 −0`.
	rv := &components.ReviewView{Files: reviewFiles(turn), Height: 12}
	if !strings.Contains(ansi.Strip(rv.View(100)), "mode 0644 → 0755") {
		t.Fatalf("the review should state the mode change, got:\n%s", ansi.Strip(rv.View(100)))
	}

	out := changeset.PlanUndo(turn, nil).Apply(false)
	if len(out.Failed) != 0 || len(out.Skipped) != 0 {
		t.Fatalf("taking the chmod back should not fail: %+v", out)
	}
	fi, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("the undo should have put 0644 back, got %o", fi.Mode().Perm())
	}
}

// A patch that changed the lines and the mode restores both, so the review
// says both: the counts it has, and the permissions an undo would put back
// alongside them. Stating only the counts hides half of what taking the turn
// back would do.
func TestRecordChildPatch_AModeChangedWithContentIsStatedBesideTheCounts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho two\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sup := subagent.New(context.Background(), subagent.Options{Root: dir, NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventPatch, Patch: &subagent.PatchApplied{
		Agent: "writer-1",
		Files: []subagent.PatchedFile{{
			Path:         script,
			Before:       "#!/bin/sh\necho one\n",
			After:        "#!/bin/sh\necho two\n",
			BeforeExists: true,
			AfterExists:  true,
			BeforeMode:   0o644,
			AfterMode:    0o755,
		}},
	}}})
	m = updated.(Model)

	turn, ok := m.changes.Latest()
	if !ok || len(turn.Records) != 1 {
		t.Fatalf("the patch should be one record in the turn, got %+v", turn)
	}
	if turn.Records[0].ModeOnly() {
		t.Fatal("a record with a diff changed more than its mode")
	}
	rv := &components.ReviewView{Files: reviewFiles(turn), Height: 12}
	view := ansi.Strip(rv.View(100))
	if !strings.Contains(view, "mode 0644 → 0755") || !strings.Contains(view, "+1") {
		t.Fatalf("the review should state the counts and the mode, got:\n%s", view)
	}

	// The turn's own row keeps its counts: the mode stands in for them only
	// where there are none.
	if row := m.turnChangesRow(false); row == nil || row.Mode != "" {
		t.Fatalf("a turn with lines to count states them, got %+v", row)
	}

	if out := changeset.PlanUndo(turn, nil).Apply(false); len(out.Failed) != 0 || len(out.Skipped) != 0 {
		t.Fatalf("the undo should have put both back: %+v", out)
	}
	fi, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("the undo should have put 0644 back, got %o", fi.Mode().Perm())
	}
	if data, err := os.ReadFile(script); err != nil || string(data) != "#!/bin/sh\necho one\n" {
		t.Fatalf("the undo should have put the lines back, got %q %v", data, err)
	}
}

// A file the child's patch created has no before side to have had a mode,
// and undo takes it away rather than putting a default-moded file back.
func TestRecordChildPatch_ACreatedFileIsStillRemovedByUndo(t *testing.T) {
	dir := t.TempDir()
	created := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(created, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sup := subagent.New(context.Background(), subagent.Options{Root: dir, NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventPatch, Patch: &subagent.PatchApplied{
		Agent: "writer-1",
		Files: []subagent.PatchedFile{{
			Path:        created,
			After:       "hello\n",
			AfterExists: true,
			AfterMode:   0o644,
		}},
	}}})
	m = updated.(Model)

	turn, ok := m.changes.Latest()
	if !ok || len(turn.Records) != 1 {
		t.Fatalf("the child's patch should be one record in the turn, got %+v", turn)
	}
	if turn.Records[0].BeforeMode != 0 {
		t.Fatalf("a file that did not exist had no mode, got %o", turn.Records[0].BeforeMode)
	}
	if out := changeset.PlanUndo(turn, nil).Apply(false); len(out.Failed) != 0 {
		t.Fatalf("undoing a creation should not fail: %+v", out.Failed)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("undo should have removed the created file, got %v", err)
	}
}

func transcriptContains(m Model, s string) bool {
	for _, e := range m.transcript {
		if strings.Contains(e.text, s) {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}

// --- what the routed card carries ---

// longPatchAsk is a writer's finished patch as the supervisor routes one: a
// body far longer than the panel, the files it names in the reader's own
// checkout, and that checkout as its root.
func longPatchAsk(root string) *subagent.Ask {
	var before, after strings.Builder
	for i := range 40 {
		fmt.Fprintf(&before, "line %d\nkeep %d\n", i, i)
		fmt.Fprintf(&after, "line %d changed\nkeep %d\n", i, i)
	}
	ask := subagent.NewAsk("writer-1", subagent.AskPatch, "apply patch (+40 −40, 2 file(s))")
	ask.Hunks = diff.Compute(before.String(), after.String())
	ask.Root = root
	ask.Files = []string{"internal/agent/loop.go", "internal/agent/mode.go"}
	return ask
}

// routedModel is a session with one child request on screen, holding the
// keyboard the way a reader who answered the handover would.
func routedModel(t *testing.T, ask *subagent.Ask) Model {
	t.Helper()
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	m = m.WithChangeset(changeset.New(64), nil)
	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventAsk, Ask: ask}})
	return handover(t, updated.(Model))
}

// A patch is the one child request that writes the reader's own files, so its
// card says the four things the session's own card says about an edit: where
// it lands, what it touches, whether it can be taken back, and [d] into the
// whole of it.
func TestChildAskPatchCardCarriesWhatTheSessionsCardCarries(t *testing.T) {
	dir := t.TempDir()
	m := routedModel(t, longPatchAsk(dir))
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"lands in  your workspace",
		"touches   internal/agent/loop.go and 1 more",
		"writes 2 files under internal/agent",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the routed patch card should state %q:\n%s", want, view)
		}
	}
	// Reversibility rides the stats line under the diff, where it costs the
	// diff no rows — which on a body this long is past the fold.
	card := m.childAskCard(m.activeChildAsk())
	if want := "undo yes — applying records every file on both sides"; card.Reversibility != want {
		t.Fatalf("Reversibility = %q, want %q", card.Reversibility, want)
	}
	if !card.FullDiff {
		t.Fatal("a patch with hunks offers [d] into the whole of it")
	}
}

// The block is resolved against the checkout the request's paths live in, and
// a child's command is contained by the session's own mechanism — so the card
// names the same containment the session's would.
func TestChildAskCommandCardStatesContainmentAndRadius(t *testing.T) {
	dir := t.TempDir()
	ask := subagent.NewAsk("writer-1", subagent.AskCommand, "run rm -rf build")
	ask.Command = "rm -rf build"
	ask.Root, ask.Worktree = dir, true

	sup := subagent.New(context.Background(), subagent.Options{Root: dir, NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup).WithContainment(Containment{
		Status: "bwrap · workspace", Mechanism: "bwrap", Profile: "workspace",
	})
	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventAsk, Ask: ask}})
	m = handover(t, updated.(Model))

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"⛨ bwrap · workspace",
		"lands in  the agent's worktree",
		"touches   build",
		"network   closed",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the routed command card should state %q:\n%s", want, view)
		}
	}
}

// The card is bounded like every other, so a forty-hunk patch counts what the
// bound swallowed — and the chord that names the count moves the body.
func TestChildAskScrollsItsBoundedBody(t *testing.T) {
	m := routedModel(t, longPatchAsk(t.TempDir()))
	before := ansi.Strip(m.View().Content)
	if !strings.Contains(before, "more lines · shift+↓") {
		t.Fatalf("the bounded routed card should count its scrolled-off rows:\n%s", before)
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	m = updated.(Model)
	if m.cardScroll != 1 {
		t.Fatalf("shift+↓ should move the routed card's body, cardScroll = %d", m.cardScroll)
	}
	if after := ansi.Strip(m.View().Content); after == before {
		t.Fatalf("shift+↓ changed nothing on screen:\n%s", after)
	}
}

// [d] opens the child's change full screen with the request still waiting
// behind it; esc comes back to the card, which kept the keyboard.
func TestChildAskDiffKeyOpensTheWholeChange(t *testing.T) {
	ask := longPatchAsk(t.TempDir())
	m := routedModel(t, ask)
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m = updated.(Model)
	if m.state != stateDiffFull || m.fullDiff == nil {
		t.Fatalf("[d] should open the full-screen diff, state %v", m.state)
	}
	if _, answered := ask.Answered(); answered {
		t.Fatal("opening the diff must not answer the request")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.activeChildAsk() != ask {
		t.Fatal("esc should come back to the request, still waiting")
	}
	if !m.decisionGated() {
		t.Fatal("the card keeps the keyboard it was handed across its own surface")
	}
}

// A card that took the keyboard by arriving claims the two answers and
// nothing else, so it advertises nothing else either: [g] shown while gated
// is a key that would put a letter in the draft.
func TestChildAskHeldOnArrivalOffersNoExtraKeys(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	ask := subagent.NewAsk("writer-1", subagent.AskCommand, "run make")
	ask.Command = "make"
	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventAsk, Ask: ask}})
	m = updated.(Model)
	if !m.heldOnArrival {
		t.Fatal("an ask landing on an empty draft holds the keyboard by arrival")
	}
	if view := ansi.Strip(m.View().Content); strings.Contains(view, "attach to writer-1") {
		t.Fatalf("a held-on-arrival card must not offer [g]:\n%s", view)
	}
	// The handover buys it, and then it is shown.
	if view := ansi.Strip(handover(t, m).View().Content); !strings.Contains(view, "[g] attach to writer-1") {
		t.Fatalf("the handed-over card offers [g]:\n%s", view)
	}
}

// A key the card offers and the surface routes elsewhere must not reach the
// answer on its way: every result that is not an approval is a decline, so
// [d] pressed to read a patch before deciding would have declined it by
// asking to read it — and the child would have been told so.
func TestChildAskDiffFromTheListDoesNotAnswer(t *testing.T) {
	ask := longPatchAsk(t.TempDir())
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventAsk, Ask: ask}})
	m = updated.(Model)
	m.answerAgent = ask.Agent
	opened, _ := m.openAgentList()
	m = opened.(Model)

	updated, _ = m.updateListAnswer(tea.KeyPressMsg{Code: 'd', Text: "d"}, ask)
	m = updated.(Model)
	if _, answered := ask.Answered(); answered {
		t.Fatal("[d] must open the diff, not answer the request")
	}
	if m.state != stateDiffFull {
		t.Fatalf("[d] over the list should open the full-screen diff, state %v", m.state)
	}
	if len(m.childAsks) != 1 {
		t.Fatalf("the request stays queued while its diff is open, %d left", len(m.childAsks))
	}
}

// A worktree is a different checkout, so the `undo` row is asked of it and
// not of the session's own. Reading the session's tracker instead states an
// answer about the reader's tree as though it were about the child's — and it
// is stated with the same confidence either way.
func TestChildAskCommandAsksTheTreeItRunsIn(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	// The session is open outside any repository; the child works in one.
	session, worktree := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "kept.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "kept.txt"}} {
		if out, err := exec.Command("git", append([]string{"-C", worktree}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git setup failed: %v (%s)", err, out)
		}
	}

	ask := subagent.NewAsk("writer-1", subagent.AskCommand, "run rm kept.txt")
	ask.Command = "rm kept.txt"
	ask.Root, ask.Worktree = worktree, true

	sup := subagent.New(context.Background(), subagent.Options{Root: session, NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup).WithWorkspace(session)
	m = m.WithChangeset(changeset.New(64), changeset.NewTracker(session))
	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventAsk, Ask: ask}})
	m = handover(t, updated.(Model))

	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "undo      git") {
		t.Fatalf("the path is tracked in the tree the command runs in, and the card should say so:\n%s", view)
	}
}

// The block is read where the request arrives, not where the card is drawn: a
// card is rebuilt every frame, and this one stats the filesystem and shells
// out to git.
func TestChildAskRadiusIsResolvedOnceOnArrival(t *testing.T) {
	ask := longPatchAsk(t.TempDir())
	m := routedModel(t, ask)
	if _, ok := m.childBlast[ask]; !ok {
		t.Fatal("the arriving request should have left its resolved block behind")
	}
	// And it goes when the request does, so a session that answers a hundred
	// is not still holding a hundred readings.
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if _, ok := updated.(Model).childBlast[ask]; ok {
		t.Fatal("an answered request should not keep its block")
	}
}

// A child's edit lands in the agent's own worktree, and the changeset records
// what this session writes — which a child's edits are not. Only the patch it
// finishes with ever reaches the store, so the row says none rather than the
// yes an edit on this side of the boundary would have earned.
func TestChildAskEditPromisesNoUndoItCannotKeep(t *testing.T) {
	ask := subagent.NewAsk("writer-1", subagent.AskEdit, "edit internal/agent/loop.go")
	ask.Path, ask.Root, ask.Worktree = "internal/agent/loop.go", t.TempDir(), true
	ask.Hunks = diff.Compute("a\nb\nc\n", "a\nB\nc\n")
	m := routedModel(t, ask)

	card := m.childAskCard(ask)
	if want := "undo none here — this session records the agent's patch, not its edits"; card.Reversibility != want {
		t.Fatalf("Reversibility = %q, want %q", card.Reversibility, want)
	}
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"lands in  the agent's worktree",
		"medium · edits one file under internal/agent",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the routed edit card should state %q:\n%s", want, view)
		}
	}
}

// The manager's chord is a draft key, so it is live wherever the draft is —
// including on a card holding the keyboard by arriving, which is the one
// state where the card advertises nothing but its two answers. The row it
// loses is the safe direction of that trade; a key that did nothing would be
// the other one.
func TestChildAskHeldOnArrivalStillReachesTheManager(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	ask := subagent.NewAsk("writer-1", subagent.AskCommand, "run make")
	ask.Command = "make"
	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventAsk, Ask: ask}})
	m = updated.(Model)
	if !m.heldOnArrival {
		t.Fatal("an ask landing on an empty draft holds the keyboard by arrival")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt})
	if next := updated.(Model); next.agentList == nil {
		t.Fatal("the manager's chord opens the manager from a held card too")
	}
}
