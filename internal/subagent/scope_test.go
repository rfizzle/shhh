package subagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/tools"
)

// commandAction is the action a child's execute_command call becomes before
// the scope is read into it.
func commandAction(t *testing.T, command string) agent.Action {
	t.Helper()
	args, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	a, err := actionFor(tools.ExecCommandName, args)
	if err != nil {
		t.Fatalf("actionFor(%q): %v", command, err)
	}
	return a
}

// scopeFixture is a parent checkout, a writer's copy of it elsewhere and a
// supervisor wired the way the session wires it: the scope a child answers to
// is the directories the person added, never the parent's root.
func scopeFixture(t *testing.T, added ...string) (root, worktree string, sup *Supervisor) {
	t.Helper()
	root, worktree = t.TempDir(), t.TempDir()
	sc, problems := scope.New(root, added...)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	sup = New(context.Background(), Options{Root: root, ScopeDirs: sc.Dirs})
	t.Cleanup(sup.Close)
	return root, worktree, sup
}

// A writer's scope is its own copy plus the directories the person added,
// which is the set its contained runner may write to; the parent's own
// checkout is not in it
// (docs/capabilities/subagents.md#a-child-inherits-its-scope-not-more).
func TestWriterCommandIntoTheParentCheckoutIsOutOfScope(t *testing.T) {
	root, worktree, sup := scopeFixture(t)
	other := t.TempDir()
	writer := &child{role: RoleWriter, root: worktree, worktree: worktree}

	for _, command := range []string{
		"echo x > " + filepath.Join(root, "f"),
		"cd " + root + " && touch f",
	} {
		got := sup.scopedAction(writer, commandAction(t, command))
		if len(got.OutOfScope) == 0 {
			t.Errorf("%q: a writer's command into the parent's checkout is in scope: %+v", command, got)
			continue
		}
		if !got.ScopeSensitive || got.ScopeReason != parentCheckoutReason {
			t.Errorf("%q: the parent's checkout is not marked as the person's to grant: %+v", command, got)
		}
	}
	if got := sup.scopedAction(writer, commandAction(t, "echo x > "+filepath.Join(other, "f"))); len(got.OutOfScope) == 0 {
		t.Errorf("a writer's command into an ungranted directory is in scope: %+v", got)
	}
}

// What a writer writes in its own copy — by a relative path, which is how
// nearly every command names one — stays in scope, and a command that only
// reads the parent's checkout is not flagged.
func TestWriterCommandInItsOwnCopyOrReadingTheCheckoutIsInScope(t *testing.T) {
	root, worktree, sup := scopeFixture(t)
	if err := os.Mkdir(filepath.Join(worktree, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writer := &child{role: RoleWriter, root: worktree, worktree: worktree}
	for _, command := range []string{
		"echo x > f",
		"touch f",
		"cd sub && echo x > f",
		"echo x > " + filepath.Join(worktree, "f"),
		"cat " + filepath.Join(root, "f"),
		"git -C " + root + " log",
	} {
		if got := sup.scopedAction(writer, commandAction(t, command)); len(got.OutOfScope) > 0 {
			t.Errorf("%q: flagged out of scope: %v", command, got.OutOfScope)
		}
	}
}

// A directory the person added that holds the parent's checkout brings the
// checkout into a writer's scope: the person answered for it.
func TestWriterCommandIntoAnAddedCheckoutIsInScope(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "checkout")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sc, problems := scope.New(root, base)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	sup := New(context.Background(), Options{Root: root, ScopeDirs: sc.Dirs})
	t.Cleanup(sup.Close)
	worktree := t.TempDir()
	writer := &child{role: RoleWriter, root: worktree, worktree: worktree}
	if got := sup.scopedAction(writer, commandAction(t, "echo x > "+filepath.Join(root, "f"))); len(got.OutOfScope) > 0 {
		t.Errorf("a checkout the person added is out of a writer's scope: %v", got.OutOfScope)
	}
}

// The command reaches the parent's card before the mode is read: in auto
// mode, where the gap let a call through with nobody asked, the static policy
// asks and a classifier yes does not stand.
func TestWriterCommandIntoTheParentCheckoutAsksInEveryMode(t *testing.T) {
	root, worktree, sup := scopeFixture(t)
	command := "echo x > " + filepath.Join(root, "f")
	for _, mode := range []agent.Mode{agent.ModeManual, agent.ModeAcceptEdits, agent.ModeAuto} {
		sup.SetParentMode(mode)
		writer := &child{role: RoleWriter, root: worktree, worktree: worktree, mode: mode}
		action := sup.scopedAction(writer, commandAction(t, command))
		policy := sup.childPolicy(writer)
		policy.AllowCommands = true
		if got, _ := policy.Decide(action); got != agent.Ask {
			t.Errorf("%s mode: Decide = %v; want Ask", mode, got)
		}
		if mode != agent.ModeAuto {
			continue
		}
		if got, _ := agent.ResolveAuto(action, agent.ClassifierVerdict{Decision: agent.Allow}); got != agent.Ask {
			t.Errorf("auto mode: a classifier yes resolved to %v; want Ask", got)
		}
	}
}

// A researcher and a reviewer run in the parent's checkout itself, so the
// check has nothing to say about their actions.
func TestReadOnlyChildActionsAreUnchangedByTheScope(t *testing.T) {
	root, _, sup := scopeFixture(t)
	for _, role := range []Role{RoleResearcher, RoleReviewer} {
		c := &child{role: role, root: root}
		for _, command := range []string{
			"echo x > " + filepath.Join(root, "f"),
			"cat " + filepath.Join(root, "f"),
		} {
			in := commandAction(t, command)
			if got := sup.scopedAction(c, in); !reflect.DeepEqual(got, in) {
				t.Errorf("%s %q: action changed: %+v", role, command, got)
			}
		}
	}
}
