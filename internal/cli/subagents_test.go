package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
)

func TestAgentProfilesReaders(t *testing.T) {
	all := &agentProfiles{profiles: subagent.BuiltinProfiles()}
	all.profiles["scribe"] = subagent.Profile{Name: "scribe", Writes: true}
	all.profiles["analyst"] = subagent.Profile{Name: "analyst"}
	got := all.readers()
	for _, want := range []subagent.Role{subagent.RoleResearcher, subagent.RoleReviewer, "analyst"} {
		if _, ok := got.profiles[want]; !ok {
			t.Errorf("readers dropped %q", want)
		}
	}
	for _, absent := range []subagent.Role{subagent.RoleWriter, "scribe"} {
		if _, ok := got.profiles[absent]; ok {
			t.Errorf("readers kept writer %q", absent)
		}
	}
}

// A writer starts from the parent's tree, and the half of that tree git
// cannot describe is the files the session made itself. The session's own
// record is what names them: a checkout is full of untracked files nobody in
// the conversation put there, and copying those into every writer's worktree
// would carry the person's desk rather than their work. A file the session
// created and then deleted has nothing left to carry.
func TestSessionUntracked(t *testing.T) {
	store := changeset.New(changeset.DefaultMaxBytes)
	add := func(turn int64, path string, track changeset.Tracking, after string, exists bool) {
		store.Add(turn, changeset.Record{
			Path: path, After: after, AfterExists: exists,
			Agent: changeset.MainAgent, Track: track,
		})
	}
	add(1, "internal/new.go", changeset.TrackUntracked, "package internal\n", true)
	add(1, "internal/old.go", changeset.TrackTracked, "package internal\n", true)
	add(2, "internal/new.go", changeset.TrackUntracked, "package internal\n\nvar x = 1\n", true)
	add(2, "scratch.txt", changeset.TrackUntracked, "", false)
	add(3, "notes/todo.md", changeset.TrackUntracked, "one\n", true)

	got := sessionUntracked(store)
	want := []string{"internal/new.go", "notes/todo.md"}
	if len(got) != len(want) {
		t.Fatalf("sessionUntracked = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sessionUntracked = %v, want %v", got, want)
		}
	}
	if paths := sessionUntracked(nil); paths != nil {
		t.Fatalf("a session with no changeset carries nothing, got %v", paths)
	}
}

// A child that is told nothing about the checkout has to spend rounds on git
// to learn what the session was handed for free, and a writer that is told
// only the parent's facts finds its own git disagreeing with them.
func TestChildExtraCarriesTheWorkspaceAndWhereTheChildStands(t *testing.T) {
	workspace := project.PromptBlock(project.Info{Dir: "/work", Repo: true, Branch: "side", Dirty: 2})

	reader := childExtra("", "# Project\nbe helpful", workspace, false)
	if !strings.Contains(reader, "Git branch: side") || !strings.Contains(reader, "2 uncommitted paths") {
		t.Errorf("a child should be handed the checkout the session was handed:\n%s", reader)
	}
	if !strings.Contains(reader, "be helpful") {
		t.Errorf("the project's own instructions still come with it:\n%s", reader)
	}
	if strings.Contains(reader, "isolated copy") {
		t.Errorf("a reader stands in the parent's own directory:\n%s", reader)
	}

	writer := childExtra("", "", workspace, true)
	if !strings.Contains(writer, "Git branch: side") {
		t.Errorf("a writer is told the branch too:\n%s", writer)
	}
	if !strings.Contains(writer, "isolated copy") || !strings.Contains(writer, "belongs to no branch") {
		t.Errorf("a writer whose git contradicts the block has to be told why:\n%s", writer)
	}
	if strings.Index(writer, "isolated copy") < strings.Index(writer, "Git branch: side") {
		t.Errorf("the correction follows the facts it corrects:\n%s", writer)
	}
}

// A child is handed the project's instructions against its own budget, not
// the session's. A checkout whose instruction file runs to a few hundred
// kilobytes — this one's does — otherwise put a sixth of a child's whole
// token budget into its system prompt before it had read a line of code, and
// again for every child in a fan-out.
func TestChildInstructionsAreCutToTheChildsOwnBudget(t *testing.T) {
	dir := t.TempDir()
	var text strings.Builder
	text.WriteString("# Fixture\n\n## Overview\n\n")
	for text.Len() < 200<<10 {
		text.WriteString("a line about how this project builds\n")
	}
	text.WriteString("\n## Gotchas\n\nthe rule nobody guesses\n")
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(text.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	files := project.Instructions(dir, "")
	child := project.InstructionBlock(files, prompt.ChildInstructionBudget)
	session := project.InstructionBlock(files, prompt.InstructionBudget)

	if len(child) >= len(session) {
		t.Fatalf("a child's block (%d bytes) is not smaller than the session's (%d)", len(child), len(session))
	}
	extra := childExtra("", child, "", false)
	if len(extra) > prompt.ChildInstructionBudget+2000 {
		t.Fatalf("a child's standing context came to %d bytes against a budget of %d",
			len(extra), prompt.ChildInstructionBudget)
	}
	if !strings.Contains(extra, "the rule nobody guesses") {
		t.Fatalf("the end of the instruction file did not survive the cut:\n%s", extra[:2000])
	}
}

// A session assembled without a survey says nothing about the tree rather
// than stopping the child that asked.
func TestSessionEnvWorkspaceBlockIsOptional(t *testing.T) {
	if got := (&sessionEnv{}).workspaceBlock(); got != "" {
		t.Errorf("no reading of the tree is no block, got %q", got)
	}
	env := &sessionEnv{workspace: func() string { return "# Workspace\n- Git branch: side" }}
	if got := env.workspaceBlock(); got != "# Workspace\n- Git branch: side" {
		t.Errorf("the block should come through as it was built, got %q", got)
	}
}

// Every child is wired into the parent's notebook the same way, and a
// writer is the case that has to be said out loud: it works in an isolated
// copy of the checkout and still writes into the session's notebook, under
// its own name, because the notebook belongs to the session and not to a
// tree. It can add and read; there is no tool that removes anything.
func TestChildWritesIntoTheParentsNotebook(t *testing.T) {
	nb := notebook.New(nil)
	nb.SetTurn(4)
	_, _, _ = nb.Write(notebook.Orchestrator, "The gate lives in mode.go", "policy.Decide reads the deny list")

	passed := errors.New("passed on")
	next := func(string, json.RawMessage) (string, error) { return "", passed }

	for _, child := range []string{"writer-1", "researcher-1", "reviewer-1"} {
		defs, exec, sysPrompt := withNotebook(nb, child, tools.Definitions(), next, "# Environment")
		var have []string
		for _, d := range defs {
			have = append(have, d.Name)
		}
		for _, want := range []string{notebook.WriteToolName, notebook.ReadToolName} {
			if !slices.Contains(have, want) {
				t.Errorf("%s was not given %s", child, want)
			}
		}
		if !strings.Contains(sysPrompt, "The gate lives in mode.go") {
			t.Errorf("%s was not told what the notebook already holds:\n%s", child, sysPrompt)
		}
		if !strings.Contains(sysPrompt, "# Environment") {
			t.Errorf("%s lost the prompt the block was added to:\n%s", child, sysPrompt)
		}
		args := json.RawMessage(`{"title":"` + child + ` found it","body":"in internal/cli"}`)
		if _, err := exec(notebook.WriteToolName, args); err != nil {
			t.Fatalf("%s could not write: %v", child, err)
		}
		// Nothing a child can call removes a note; an unknown name is
		// passed on down the chain rather than answered here.
		if _, err := exec("delete_note", json.RawMessage(`{"id":1}`)); !errors.Is(err, passed) {
			t.Errorf("%s reached something that deletes", child)
		}
	}

	notes := notebook.WrittenIn(nb.List(), 4, notebook.Orchestrator)
	if len(notes) != 3 {
		t.Fatalf("the parent's notebook holds %d of its children's notes", len(notes))
	}
	for i, want := range []string{"writer-1", "researcher-1", "reviewer-1"} {
		if notes[i].Author != want {
			t.Errorf("note %d signed %q, want %q", i, notes[i].Author, want)
		}
	}
	// The orchestrator's own note is still there and is not counted as a
	// child's.
	if nb.Len() != 4 {
		t.Fatalf("the notebook holds %d notes", nb.Len())
	}
}

// A session with no notebook is left exactly as it was: the block is what
// tells a child the tools exist, so it must never be added without them.
func TestChildWithoutANotebookIsUnchanged(t *testing.T) {
	base := agent.ToolExecutor(func(string, json.RawMessage) (string, error) { return "ok", nil })
	defs, exec, sysPrompt := withNotebook(nil, "writer-1", tools.Definitions(), base, "# Environment")
	if len(defs) != len(tools.Definitions()) || sysPrompt != "# Environment" {
		t.Errorf("a session with no notebook still handed one out: %d defs, prompt %q", len(defs), sysPrompt)
	}
	if out, err := exec(notebook.WriteToolName, nil); err != nil || out != "ok" {
		t.Errorf("the chain was wrapped anyway: %q %v", out, err)
	}
}
