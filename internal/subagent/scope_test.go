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
	// shhh's own directories move off the temporary directory, which a
	// session's own tmpdir puts inside them: a checkout under them is shhh's
	// own state to the scope before it is the parent's, and the reason a test
	// reads back would be the wrong one.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
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

// A grant of the checkout itself — /add-dir <root>, --add-dir or
// behavior.scope_dirs naming it — brings it into a writer's scope, as a
// directory that encloses it does, without granting anything above it.
func TestWriterCommandIntoAGrantedCheckoutIsInScope(t *testing.T) {
	root := t.TempDir()
	sc, problems := scope.New(root)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	sup := New(context.Background(), Options{Root: root, ScopeDirs: sc.Dirs})
	t.Cleanup(sup.Close)
	worktree := t.TempDir()
	writer := &child{role: RoleWriter, root: worktree, worktree: worktree}
	command := "echo x > " + filepath.Join(root, "f")
	if got := sup.scopedAction(writer, commandAction(t, command)); len(got.OutOfScope) == 0 {
		t.Fatalf("before the grant the checkout is in a writer's scope: %+v", got)
	}

	if _, err := sc.Add(root); err != nil {
		t.Fatalf("granting the checkout: %v", err)
	}
	for _, command := range []string{command, "cd " + root + " && touch f"} {
		if got := sup.scopedAction(writer, commandAction(t, command)); len(got.OutOfScope) > 0 {
			t.Errorf("%q: a checkout the person granted is out of a writer's scope: %v", command, got.OutOfScope)
		}
	}
	if got := sup.scopedAction(writer, commandAction(t, "echo x > "+filepath.Join(filepath.Dir(root), "f"))); len(got.OutOfScope) == 0 {
		t.Errorf("a grant of the checkout brought its parent into a writer's scope: %+v", got)
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

// In auto mode a child's reach outside its scope goes to the person and not
// to the classifier, whatever the directory: a yes from the classifier adds
// nothing to what the child may write, so the contained runner would refuse
// the command it had just been told was approved. A command inside the scope
// is still the classifier's.
// See docs/capabilities/subagents.md#a-child-inherits-its-scope-not-more.
func TestChildOutOfScopeCommandSkipsTheClassifierInAutoMode(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	sc, problems := scope.New(root)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	run := func(command string) (*Supervisor, *scriptedEnv, *verdictProvider) {
		env := &scriptedEnv{
			steps:   gatedCommandSteps(command),
			gated:   map[string]bool{tools.ExecCommandName: true},
			execOut: "ok",
		}
		judge := &verdictProvider{decision: "allow", reason: "the task asked for it"}
		sup := New(context.Background(), Options{
			Root:       root,
			ScopeDirs:  sc.Dirs,
			NewEnv:     env.factory(),
			Classifier: agent.NewClassifier(judge, agent.ClassifierConfig{Model: "judge"}),
		})
		t.Cleanup(sup.Close)
		sup.SetParentMode(agent.ModeAuto)
		execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"write the file"}`)
		return sup, env, judge
	}

	sup, env, judge := run("echo x > " + filepath.Join(other, "f"))
	ask := nextAsk(t, sup)
	if judge.calls.Load() != 0 {
		t.Fatalf("the classifier was asked about a command outside the child's scope (%d calls)", judge.calls.Load())
	}
	ask.Respond(false)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if env.ranCommand.Load() {
		t.Fatal("a declined out-of-scope command ran")
	}

	sup, env, judge = run("echo x > " + filepath.Join(root, "f"))
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if judge.calls.Load() == 0 || !env.ranCommand.Load() {
		t.Fatalf("an in-scope command was not the classifier's: calls=%d ran=%v", judge.calls.Load(), env.ranCommand.Load())
	}
}
