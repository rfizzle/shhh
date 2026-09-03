package chat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
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

// Where the mode stops. A patch that only made a script executable carries
// its whole change in the two modes, and they reach the parent — but the
// session's history records content and existence, so a change that moved
// neither is not a turn to undo and no entry appears. Anything that teaches
// the history about modes has to come back through here.
func TestRecordChildPatch_AModeOnlyPatchIsNotATurnToUndo(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "script.sh")
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

	if turn, ok := m.changes.Latest(); ok {
		t.Fatalf("a patch that changed no content should leave the history alone, got %+v", turn)
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
