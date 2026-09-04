package cli

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/subagent"
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
