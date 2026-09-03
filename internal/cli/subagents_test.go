package cli

import (
	"testing"

	"github.com/rfizzle/shhh/internal/changeset"
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
