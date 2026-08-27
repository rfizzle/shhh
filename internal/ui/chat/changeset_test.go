package chat

// Per-turn changeset recording (S-097): what the session applies, it records.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
)

// applyWrite runs one write_file call to completion, answering the approval
// prompt with the given key ("" applies without one, for the modes that do
// not prompt).
func applyWrite(t *testing.T, m Model, path, content, key string) Model {
	t.Helper()
	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file",
			Arguments: fmt.Sprintf(`{"path":%q,"content":%q}`, path, content)},
	}})
	m = updated.(Model)
	if key != "" {
		// The card arrives without the keyboard (S-117, §7b); ctrl+g is what
		// hands it over before any of its letters mean anything.
		m = handover(t, m)
		updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		m = updated.(Model)
	}
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(approvedToolDoneMsg); ok {
			updated, _ = m.Update(msg)
			m = updated.(Model)
		}
	}
	return m
}

func TestChangeset_RecordsAnApprovedWrite(t *testing.T) {
	m := gatedModel(t, nil, nil)
	m.turnCount = 7
	path := filepath.Join(t.TempDir(), "code.go")
	m = applyWrite(t, m, path, "package main\n", "y")

	turn, ok := m.changes.Turn(7)
	if !ok {
		t.Fatal("the applied write should be recorded against the turn")
	}
	r, ok := turn.Record(path)
	if !ok {
		t.Fatalf("expected a record for %s, got %+v", path, turn.Records)
	}
	if r.After != "package main\n" || r.Before != "" {
		t.Fatalf("the record should carry both sides, got before=%q after=%q", r.Before, r.After)
	}
	if !r.Created() {
		t.Fatal("a write to a path that did not exist is a creation")
	}
	if len(r.Hunks) == 0 || r.Added != 1 {
		t.Fatalf("the record should carry computed hunks, got %d hunks +%d", len(r.Hunks), r.Added)
	}
	if r.Agent != changeset.MainAgent || r.Origin != changeset.Approved {
		t.Fatalf("a write the user approved is the session's own, got agent=%q origin=%v", r.Agent, r.Origin)
	}
	if turn.Files() != 1 || turn.Added != 1 {
		t.Fatalf("the turn should aggregate to 1 file +1, got %d files +%d", turn.Files(), turn.Added)
	}
}

func TestChangeset_AutoApprovedEditRecordsHowItWasApplied(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)
	m.mode = agent.ModeAcceptEdits
	m.turnCount = 2
	path := filepath.Join(t.TempDir(), "a.txt")
	m = applyWrite(t, m, path, "one\n", "")

	turn, ok := m.changes.Turn(2)
	if !ok {
		t.Fatal("an auto-applied edit is still recorded")
	}
	r, _ := turn.Record(path)
	if r.Origin != changeset.AutoApproved || r.Origin.String() != "auto-approved" {
		t.Fatalf("accept-edits applies on the user's behalf, got %v", r.Origin)
	}
}

func TestChangeset_DeclinedAndFailedCallsRecordNothing(t *testing.T) {
	m := gatedModel(t, nil, nil)
	dir := t.TempDir()
	m = applyWrite(t, m, filepath.Join(dir, "no.go"), "package main\n", "n")
	if _, ok := m.changes.Latest(); ok {
		t.Fatal("a declined call changed nothing, so it records nothing")
	}

	// An edit whose old_text is not in the file fails and leaves the file
	// untouched — nothing changed, so nothing is recorded.
	path := filepath.Join(dir, "code.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_e", Name: "edit_file",
			Arguments: fmt.Sprintf(`{"path":%q,"old_text":"missing","new_text":"x"}`, path)},
	}})
	m = updated.(Model)
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(approvedToolDoneMsg); ok {
			updated, _ = m.Update(msg)
			m = updated.(Model)
		}
	}
	if _, ok := m.changes.Latest(); ok {
		t.Fatal("a failed edit records nothing")
	}
}

func TestChangeset_RepeatedEditsInOneTurnCollapse(t *testing.T) {
	m := gatedModel(t, nil, nil)
	m.turnCount = 3
	path := filepath.Join(t.TempDir(), "code.go")
	m = applyWrite(t, m, path, "one\n", "y")
	m = applyWrite(t, m, path, "one\ntwo\n", "y")

	turn, _ := m.changes.Turn(3)
	if turn.Files() != 1 {
		t.Fatalf("two writes of one file are one record, got %d", turn.Files())
	}
	r, _ := turn.Record(path)
	if r.Before != "" || r.After != "one\ntwo\n" || !r.Created() {
		t.Fatalf("the net record should span both writes, got before=%q after=%q", r.Before, r.After)
	}
	if turn.Added != 2 {
		t.Fatalf("the turn should report the net +2, got +%d", turn.Added)
	}
}

func TestChangeset_NotesWhetherGitKnewTheFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@e.com"}, {"config", "user.name", "t"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	tracked := filepath.Join(dir, "tracked.go")
	if err := os.WriteFile(tracked, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "add", "tracked.go").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}

	m := gatedModel(t, nil, nil).WithChangeset(nil, changeset.NewTracker(dir))
	m.turnCount = 1
	m = applyWrite(t, m, tracked, "package main\n\nfunc main() {}\n", "y")
	m = applyWrite(t, m, filepath.Join(dir, "scratch.txt"), "notes\n", "y")

	turn, _ := m.changes.Turn(1)
	if r, _ := turn.Record(tracked); r.Track != changeset.TrackTracked {
		t.Fatalf("a file git knew about should be recorded as tracked, got %v", r.Track)
	}
	if r, _ := turn.Record(filepath.Join(dir, "scratch.txt")); r.Track != changeset.TrackUntracked {
		t.Fatalf("a file git never saw should be recorded as untracked, got %v", r.Track)
	}
}

// Without a tracker there is no repository to be tracked by, and unknown is
// the honest answer — the record is kept all the same.
func TestChangeset_WithoutGitTheAnswerIsUnknown(t *testing.T) {
	m := gatedModel(t, nil, nil)
	m.turnCount = 1
	path := filepath.Join(t.TempDir(), "code.go")
	m = applyWrite(t, m, path, "package main\n", "y")

	turn, _ := m.changes.Turn(1)
	if r, _ := turn.Record(path); r.Track != changeset.TrackUnknown {
		t.Fatalf("outside a repository the answer is unknown, got %v", r.Track)
	}
}

func TestChangeset_EvictionIsAnnouncedInTheTranscript(t *testing.T) {
	m := gatedModel(t, nil, nil)
	m = m.WithChangeset(changeset.New(64), nil)
	m.changes.Add(1, changeset.Record{Path: "old.go", After: strings.Repeat("x\n", 100), AfterExists: true})
	m.turnCount = 2
	m = applyWrite(t, m, filepath.Join(t.TempDir(), "new.go"), strings.Repeat("y\n", 100), "y")

	if _, ok := m.changes.Turn(1); ok {
		t.Fatal("the oldest turn should have been evicted past the bound")
	}
	found := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "changeset store is full") {
			found = true
		}
	}
	if !found {
		t.Fatalf("eviction should be visible in the transcript, got %+v", m.transcript)
	}
}

func TestChangeset_ChildPatchIsAttributedToItsAgent(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	m.turnCount = 4

	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{
		Kind: subagent.EventPatch,
		Patch: &subagent.PatchApplied{
			Agent: "writer-1",
			Files: []subagent.PatchedFile{
				{Path: "docs/loop.md", After: "hello\n", AfterExists: true},
				{Path: "agent/loop.go", Before: "one\n", After: "one\ntwo\n", BeforeExists: true, AfterExists: true},
			},
		},
	}})
	m = updated.(Model)

	turn, ok := m.changes.Turn(4)
	if !ok {
		t.Fatal("an applied child patch is what the session changed, so it is recorded")
	}
	if turn.Files() != 2 || turn.Added != 2 {
		t.Fatalf("expected 2 files +2, got %d files +%d", turn.Files(), turn.Added)
	}
	r, _ := turn.Record("docs/loop.md")
	if r.Agent != "writer-1" || r.Origin != changeset.ChildPatch {
		t.Fatalf("a child's patch is attributed to it, got agent=%q origin=%v", r.Agent, r.Origin)
	}
	if len(turn.Agents) != 1 || turn.Agents[0].Files != 2 {
		t.Fatalf("per-agent attribution should credit writer-1 with both files, got %+v", turn.Agents)
	}
}
