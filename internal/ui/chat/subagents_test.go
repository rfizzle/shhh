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

// billedEnv is blockingEnv with a bill: the stream reports one request's
// usage and then blocks, so a test can observe a child that is both running
// and has spent something — which is the state a rail scoped to a child has
// to be able to report.
func billedEnv(u provider.Usage) subagent.EnvFactory {
	return func(ctx context.Context, spec subagent.Spec) (subagent.Env, error) {
		stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			ch := make(chan provider.StreamEvent)
			go func() {
				select {
				case ch <- provider.StreamEvent{Usage: &u}:
				case <-ctx.Done():
				}
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

	exec := sup.WrapExecutor("", nil)
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

// A child asking is drawn twice already: the card that routes its request
// names it on its title rail, and its lane in the transcript says what it is
// waiting on. The compact row between the two is a third drawing of one
// child, and it is in the rows the reader has to look past to reach the
// answer they came to give — so it goes while the card is up and comes back
// when the queue is empty. What counts children rather than drawing them
// stays either way (docs/interface/surfaces.md#the-input-frame).
func TestAgentRowsGoUnderARoutedCard(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)

	if got := m.agentRowsHeight(); got != 1 {
		t.Fatalf("agentRowsHeight = %d with nothing on the card, want 1", got)
	}
	// And the row joins the child's name to its task with the separator
	// every other row in the product joins two facts with.
	if row := ansi.Strip(m.renderAgentRows(100)); !strings.Contains(row, "researcher-1 · long survey") {
		t.Fatalf("the row does not join name and task with the separator: %q", row)
	}

	ask := subagent.NewAsk("researcher-1", subagent.AskCommand, "run echo hi")
	ask.Command = "echo hi"
	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventAsk, Ask: ask}})
	m = updated.(Model)
	if got := m.agentRowsHeight(); got != 0 {
		t.Fatalf("agentRowsHeight = %d under a routed card, want 0", got)
	}
	if bar := ansi.Strip(m.renderStatusBar(120)); !strings.Contains(bar, "1 agent") {
		t.Fatalf("the vitals chip stopped counting the session's agents: %q", bar)
	}
	if got := m.waitingCount(); got != 1 {
		t.Fatalf("waitingCount = %d under a routed card, want the queue's one", got)
	}

	m = handover(t, m)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	if got := m.agentRowsHeight(); got != 1 {
		t.Fatalf("agentRowsHeight = %d once the card is answered, want 1", got)
	}
}

// A child of a round that fanned out settles into the words on its own lane,
// so nothing is appended under the block to say the same thing again. A child
// that ran alone has no lane — only the row its spawn left — and there the
// notice is the whole account of how it ended.
func TestDoneNoticeIsTheLanelessChildsAlone(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	finish := func(m Model, name string) Model {
		t.Helper()
		st, ok := sup.Get(name)
		if !ok {
			t.Fatalf("%s never reached the supervisor", name)
		}
		st.Detail = "done · 3 tools"
		updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventDone, Status: st}})
		return updated.(Model)
	}

	m.beginSpawnBatch()
	for _, task := range []string{"survey the loop", "survey the tests"} {
		spawnInto(t, sup, `{"role":"researcher","task":"`+task+`"}`)
		m.appendSpawnEntry(spawnRowEntry(task))
	}
	m = finish(m, "researcher-1")
	if transcriptContains(m, "Agent researcher-1") {
		t.Fatal("a child with a lane had its ending said twice")
	}

	m.beginSpawnBatch()
	spawnInto(t, sup, `{"role":"researcher","task":"survey the folds"}`)
	m.appendSpawnEntry(spawnRowEntry("survey the folds"))
	m = finish(m, "researcher-3")
	if !transcriptContains(m, "Agent researcher-3: done · 3 tools") {
		t.Fatal("a child with no lane lost the only account of how it ended")
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
		"⛨         bwrap · workspace",
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
	if !strings.Contains(before, "more lines · [shift+↓]") {
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

// A kill takes the subtree, so the confirm counts it: answering "yes" to one
// name is answering for every agent under that name, and a prompt that named
// only the one would be understating what it is about to do. A leaf keeps the
// confirm it always had.
// See docs/capabilities/subagents.md#what-nesting-does-to-the-rest-of-it.
func TestKillConfirmCountsTheSubtree(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	// Spawned through the child's own chain, which is what writes the parent.
	exec := sup.WrapExecutor("researcher-1", nil)
	if _, err := exec(subagent.SpawnToolName,
		json.RawMessage(`{"role":"researcher","task":"a piece","name":"reader"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		st, ok := sup.Get("reader")
		return ok && st.State == subagent.StateRunning
	})

	if got := m.killPrompt("reader"); !strings.HasPrefix(got, "Kill reader? Its turn stops") {
		t.Errorf("a leaf's confirm is %q, and there is nothing under it to count", got)
	}
	got := m.killPrompt("researcher-1")
	if !strings.HasPrefix(got, "Kill researcher-1 and 1 agent under it? ") {
		t.Errorf("the confirm over a subtree is %q, and never says how many go with it", got)
	}
	if !strings.Contains(got, "the transcripts stay and the other agents keep running") {
		t.Errorf("the confirm over a subtree is %q, and never says what survives", got)
	}
}

// [a] on a routed command card makes the grant the session would have made at
// its own card: the turn's, over the shape of the command, and the supervisor
// has it the moment it is made — which is the half of the rule that was
// missing, since a parent's grants already travel to its children.
func TestChildAskAlwaysGrantsTheCommandForTheTurn(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	ask := subagent.NewAsk("writer-1", subagent.AskCommand, "run go test ./...")
	ask.Command = "go test ./..."
	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventAsk, Ask: ask}})
	m = handover(t, updated.(Model))

	const offer = `[a] allow "go test" for every agent, this turn`
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, offer) {
		t.Fatalf("the routed command card offers the grant and names its end:\n%s", view)
	}
	updated, _ = m.Update(key('a'))
	m = updated.(Model)

	if approved, answered := ask.Answered(); !answered || !approved {
		t.Fatalf("[a] answers the request it grants over (answered=%v approved=%v)", answered, approved)
	}
	if got := m.policy.turn.Commands; len(got) != 1 || got[0] != "go test" {
		t.Fatalf("turn grants = %v, want the command's shape once", got)
	}
	if !transcriptContains(m,
		`Commands starting "go test" will run for every agent without asking until this turn ends.`) {
		t.Fatal("a grant nobody can read is a grant nobody can revoke")
	}
}

// A flagged command is the one place the key is missing, here as on the
// session's own card — and the card says why rather than dropping the row,
// which is the whole reason the footnote exists.
func TestChildAskOffersNoGrantOnAFlaggedCommand(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	ask := subagent.NewAsk("writer-1", subagent.AskCommand, "run rm -rf build")
	ask.Command = "rm -rf build"
	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventAsk, Ask: ask}})
	m = handover(t, updated.(Model))

	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, "[a] allow") {
		t.Fatalf("a safety-flagged command is never pre-approved:\n%s", view)
	}
	if !strings.Contains(view, "[a] always — not offered: a safety-flagged command is never pre-approved") {
		t.Fatalf("the missing key states its reason:\n%s", view)
	}
}

// Attached to one child, another child's request is nowhere on the screen —
// the card is narrowed to the agent whose transcript this is. The rail is
// where the session says so, with the chord that reaches the manager beside
// it.
func TestAttachedRailNamesAnotherAgentWaiting(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)
	spawnInto(t, sup, `{"role":"writer","task":"write"}`)
	m.attach("writer-2")
	if got := m.othersWaiting(); got != 0 {
		t.Fatalf("nothing is queued yet, othersWaiting = %d", got)
	}

	ask := subagent.NewAsk("researcher-1", subagent.AskCommand, "run go test ./...")
	ask.Command = "go test ./..."
	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventAsk, Ask: ask}})
	m = updated.(Model)
	if m.activeChildAsk() != nil {
		t.Fatal("attached, another child's request draws no card here")
	}
	if got := m.othersWaiting(); got != 1 {
		t.Fatalf("othersWaiting = %d, want 1", got)
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "⚠ 1 other agent waiting") {
		t.Fatalf("the attached rail states what is waiting elsewhere:\n%s", view)
	}
	if !strings.Contains(view, "[alt+a] agents") {
		t.Fatalf("the rail keeps the chord that reaches it:\n%s", view)
	}
	// The child the reader is attached to is not an "other": its own request
	// draws the card in place, which is the drawing this rail stands in for.
	own := subagent.NewAsk("writer-2", subagent.AskCommand, "run go build ./...")
	own.Command = "go build ./..."
	updated, _ = m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventAsk, Ask: own}})
	m = updated.(Model)
	if got := m.othersWaiting(); got != 1 {
		t.Fatalf("othersWaiting = %d after the attached child's own request, want 1", got)
	}
}

// keptRepo is a committed repository for a writer to copy, since a writer's
// isolation is a linked worktree and one needs a commit to hang off.
func keptRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
		{"add", "main.go"}, {"commit", "-q", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git setup failed: %v (%s)", err, out)
		}
	}
	return repo
}

// keptWriterEnv is a writer whose first round writes kept.go in its own copy
// of the checkout. With answer set its second round finishes, so the patch
// goes to a card; without it the second round waits on the child's context,
// so a kill is what ends it.
func keptWriterEnv(answer bool) subagent.EnvFactory {
	return func(ctx context.Context, spec subagent.Spec) (subagent.Env, error) {
		round := 0
		stream := func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			round++
			ch := make(chan provider.StreamEvent, 2)
			switch {
			case round == 1:
				ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{
					{ID: "w1", Name: "write_file", Arguments: `{"path":"kept.go"}`},
				}}
			case answer:
				ch <- provider.StreamEvent{Token: "wrote kept.go"}
				ch <- provider.StreamEvent{Done: true}
			default:
				go func() {
					<-ctx.Done()
					close(ch)
				}()
				return ch, func() {}, nil
			}
			close(ch)
			return ch, func() {}, nil
		}
		return subagent.Env{
			SystemPrompt: "sys",
			Stream:       stream,
			Executor: func(string, json.RawMessage) (string, error) {
				return "written", os.WriteFile(filepath.Join(spec.Root, "kept.go"), []byte("package kept\n"), 0o644)
			},
		}, nil
	}
}

// The kill confirm says what survives the kill, and a writer's change is one
// of those things now: it is kept rather than discarded with the workspace.
// A child with nothing to keep is told nothing about a patch.
// See docs/capabilities/subagents.md#a-failed-child-leaves-a-handoff.
func TestKillConfirmSaysAPatchIsKeptOnlyWhereThereIsOne(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: keptRepo(t), NewEnv: keptWriterEnv(false)})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnChild(t, sup, subagent.RoleWriter, "writer-1")
	waitFor(t, func() bool { return sup.PatchToKeep("writer-1") })

	want := "Kill writer-1? Its turn stops and its isolated workspace is discarded and its patch is kept; "
	if got := m.killPrompt("writer-1"); !strings.HasPrefix(got, want) {
		t.Errorf("the confirm over a writer with work is %q, want it to open %q", got, want)
	}
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	if got := m.killPrompt("researcher-1"); strings.Contains(got, "patch") {
		t.Errorf("the confirm over a child with nothing to keep promises a patch: %q", got)
	}
}

// [p] on a row holding a kept patch opens the patch on the surface [d] opens
// from a live card, headed with whose it is, and the card behind it is the
// patch card with its two answers: apply lands the change the way a finishing
// writer's does, and the row stops offering it.
func TestAKeptPatchIsReviewedFromTheManager(t *testing.T) {
	repo := keptRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	sup := subagent.New(ctx, subagent.Options{Root: repo, NewEnv: keptWriterEnv(true)})
	t.Cleanup(sup.Close)
	t.Cleanup(cancel)
	// The finishing writer's own card is declined off-screen, which is one of
	// the ends that keeps a patch; everything after it is the manager's.
	go func() {
		for {
			select {
			case ev := <-sup.Events():
				if ev.Kind == subagent.EventAsk {
					ev.Ask.Respond(false)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	m := newSubagentModel(t, sup)
	// Spawned without waiting to see it running: it finishes as soon as it
	// starts, and a wait for the running state could miss it.
	if _, err := sup.WrapExecutor("", nil)(subagent.SpawnToolName,
		json.RawMessage(`{"role":"writer","task":"add kept.go"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		st, ok := sup.Get("writer-1")
		return ok && st.PatchKept
	})

	updated, _ := m.openAgentList()
	m = updated.(Model)
	rows, _ := m.buildAgentRows()
	if len(rows) < 2 || !rows[1].PatchKept {
		t.Fatalf("the writer's row should offer its kept patch, got %+v", rows)
	}
	m.agentList.Focus = 1
	m = press(t, m, "p")
	if m.state != stateDiffFull || m.fullDiff == nil || m.fullDiff.Path != "writer-1's patch" {
		t.Fatalf("[p] should open the patch full screen, headed with whose it is; state %v, diff %+v", m.state, m.fullDiff)
	}
	ask := m.listAnswerAsk()
	if ask == nil || ask.Kind != subagent.AskPatch {
		t.Fatalf("the patch card should be waiting behind the diff, got %+v", ask)
	}
	m = press(t, m, "esc")
	if view := m.View().Content; !strings.Contains(view, "Apply patch") {
		t.Fatalf("leaving the diff should land on the patch card over the list:\n%s", view)
	}
	m = press(t, m, "y")
	waitFor(t, func() bool {
		_, err := os.Stat(filepath.Join(repo, "kept.go"))
		return err == nil
	})
	waitFor(t, func() bool {
		st, _ := sup.Get("writer-1")
		return !st.PatchKept
	})
	if m.listAnswerAsk() != nil {
		t.Fatal("an answered review is still over the list")
	}
}

// A sentence typed while the session waits on its children ends the wait, so
// the redirect is read at the next round rather than behind the slowest child.
// See docs/capabilities/subagents.md#a-wait-only-ever-points-down-the-tree.
func TestTypedSteeringEndsTheSessionsWaitOnItsChildren(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	m = sendText(t, m, "do the task")
	spawnInto(t, sup, `{"role":"researcher","task":"survey one","name":"one"}`)

	out := make(chan string, 1)
	go func() {
		text, _ := sup.WrapExecutor("", nil)(subagent.ReportToolName, json.RawMessage(`{"names":["one"]}`))
		out <- text
	}()
	select {
	case text := <-out:
		t.Fatalf("the wait returned before anything happened:\n%s", text)
	case <-time.After(50 * time.Millisecond):
	}
	m = sendText(t, m, "look at the importer instead")
	if len(m.steering) != 1 {
		t.Fatalf("the sentence should be queued as steering, got %v", m.steering)
	}
	select {
	case text := <-out:
		if !strings.HasPrefix(text, "Woken by a steer") {
			t.Fatalf("the wait should say a steer woke it:\n%s", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("typed steering did not end the session's wait")
	}
}
