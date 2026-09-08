package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/tools"
)

func testScope(t *testing.T, root string) *scope.Scope {
	t.Helper()
	sc, problems := scope.New(root)
	if sc == nil {
		t.Fatalf("scope.New(%q): %v", root, problems)
	}
	return sc
}

func TestHeadlessScopeCheckStaysInsideWithoutYes(t *testing.T) {
	sc := testScope(t, t.TempDir())
	outside := t.TempDir()
	deny, ok := headlessScopeCheck(sc, false, []string{filepath.Join(outside, "out.txt")})
	if ok {
		t.Fatal("a headless run without --yes must not widen its own scope")
	}
	if !strings.Contains(deny, "--add-dir") {
		t.Fatalf("the refusal should name the flag that fixes it, got %q", deny)
	}
	if len(sc.Dirs()) != 0 {
		t.Fatalf("nothing should have been granted, got %v", sc.Dirs())
	}
}

func TestHeadlessScopeCheckGrantsOrdinaryDirectoriesUnderYes(t *testing.T) {
	sc := testScope(t, t.TempDir())
	outside := t.TempDir()
	if deny, ok := headlessScopeCheck(sc, true, []string{filepath.Join(outside, "out.txt")}); !ok {
		t.Fatalf("--yes should let an unattended run add an ordinary directory, got %q", deny)
	}
	if !sc.Contains(filepath.Join(outside, "other.txt")) {
		t.Fatal("the granted directory must be in scope afterwards, or the sandbox will still refuse the write")
	}
}

func TestHeadlessScopeCheckNeverGrantsSensitiveDirectories(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory on this host")
	}
	sc := testScope(t, t.TempDir())
	deny, ok := headlessScopeCheck(sc, true, []string{filepath.Join(home, "notes.txt")})
	if ok {
		t.Fatal("--yes must not add a sensitive directory")
	}
	if !strings.Contains(deny, "sensitive") {
		t.Fatalf("the refusal should say what kind of directory it is, got %q", deny)
	}
}

func TestHeadlessScopeCheckIsQuietInsideTheScope(t *testing.T) {
	root := t.TempDir()
	sc := testScope(t, root)
	if deny, ok := headlessScopeCheck(sc, false, []string{filepath.Join(root, "sub", "file.txt")}); !ok {
		t.Fatalf("a path inside the scope needs no flags, got %q", deny)
	}
}

func TestSessionScopeTakesConfigAndFlags(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	cfg := config.Config{}
	cfg.Behavior.ScopeDirs = []string{dir}
	sc, err := sessionScope(cfg, []string{other})
	if err != nil {
		t.Fatalf("sessionScope = %v", err)
	}
	if len(sc.Dirs()) != 2 {
		t.Fatalf("both config and --add-dir should be in scope, got %v", sc.Dirs())
	}
}

func TestSessionScopeFailsOnABadFlagAndSurvivesABadConfigEntry(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	if _, err := sessionScope(config.Config{}, []string{missing}); err == nil {
		t.Fatal("a --add-dir the user typed must fail the session rather than being dropped")
	}
	cfg := config.Config{}
	cfg.Behavior.ScopeDirs = []string{missing}
	sc, err := sessionScope(cfg, nil)
	if err != nil || sc == nil {
		t.Fatalf("a stale config entry must not stop a session starting: %v", err)
	}
	if len(sc.Dirs()) != 0 {
		t.Fatalf("the stale entry should have been skipped, got %v", sc.Dirs())
	}
}

func TestSandboxPolicyMakesTheScopeWritable(t *testing.T) {
	dir := t.TempDir()
	policy, err := sandboxPolicy(config.Config{}, dir)
	if err != nil {
		t.Fatalf("sandboxPolicy = %v", err)
	}
	found := false
	for _, w := range policy.WriteExtra {
		if w == dir {
			found = true
		}
	}
	if !found {
		t.Fatalf("a directory in the working scope must be writable inside containment, grants = %v", policy.WriteExtra)
	}
}

func TestScopePromptBlockNamesTheBoundaryAndTheWayOut(t *testing.T) {
	root := t.TempDir()
	sc := testScope(t, root)
	block := scopePromptBlock(sc)
	if !strings.Contains(block, root) || !strings.Contains(block, "/add-dir") {
		t.Fatalf("the model should be told where the work is and how to ask for more, got:\n%s", block)
	}
	if scopePromptBlock(nil) != "" {
		t.Error("a session with no scope tells the model nothing about one")
	}
}

func TestCommandEnvironmentBlock_NetlessSaysThereIsNoNetwork(t *testing.T) {
	block := commandEnvironmentBlock(commandEnvironment{
		Mechanism:   "bwrap",
		Profile:     "workspace-netless",
		Network:     false,
		Ceiling:     10 * time.Minute,
		Backgrounds: true,
	})
	for _, want := range []string{"bwrap", "workspace-netless", "no network", "10 minutes", "background"} {
		if !strings.Contains(block, want) {
			t.Errorf("a contained netless session should be told %q:\n%s", want, block)
		}
	}
	// The failure this exists to stop: a name lookup read as a broken host.
	if !strings.Contains(block, "resolver") {
		t.Errorf("the block should say what a failed lookup is not:\n%s", block)
	}
	// And its half on the filesystem's side: a refused read arrives as
	// whatever the program that met it calls an unreadable path, so a session
	// that has not been told debugs the program.
	if !strings.Contains(block, "never says sandbox") {
		t.Errorf("the block should say what a refused read is not:\n%s", block)
	}
}

func TestCommandEnvironmentBlock_AnUncontainedSessionHasNoDenialToExplain(t *testing.T) {
	block := commandEnvironmentBlock(commandEnvironment{Mechanism: "", Ceiling: time.Minute})
	if strings.Contains(block, "never says sandbox") {
		t.Errorf("nothing refusing a read means no refusal to account for:\n%s", block)
	}
}

func TestCommandEnvironmentBlock_SaysWhenNothingContainsACommand(t *testing.T) {
	block := commandEnvironmentBlock(commandEnvironment{Ceiling: time.Minute})
	if !strings.Contains(block, "Nothing contains") {
		t.Errorf("an uncontained session is told so plainly:\n%s", block)
	}
	if strings.Contains(block, "no network") {
		t.Errorf("nothing wrapping the command means nothing restricting it:\n%s", block)
	}
	if !strings.Contains(block, "1 minute") {
		t.Errorf("the ceiling is spelled as a sentence needs it:\n%s", block)
	}
}

func TestCommandEnvironmentBlock_TheCeilingSaysWhatThisSurfaceDoes(t *testing.T) {
	moved := commandEnvironmentBlock(commandEnvironment{Ceiling: time.Minute, Backgrounds: true})
	if !strings.Contains(moved, "moved to the background") {
		t.Errorf("a surface with a supervisor moves the command:\n%s", moved)
	}
	stopped := commandEnvironmentBlock(commandEnvironment{Ceiling: time.Minute})
	if strings.Contains(stopped, "moved to the background") || !strings.Contains(stopped, "is stopped") {
		t.Errorf("a surface with nowhere to move one stops it:\n%s", stopped)
	}
	if unbounded := commandEnvironmentBlock(commandEnvironment{}); strings.Contains(unbounded, "still running after") {
		t.Errorf("a session that bounds nothing claims no ceiling:\n%s", unbounded)
	}
}

func TestCommandEnvironmentBlock_RefusalIsSaidInsteadOfAProfile(t *testing.T) {
	block := commandEnvironmentBlock(commandEnvironment{Refused: true, Ceiling: time.Minute})
	if !strings.Contains(block, "refused before it runs") {
		t.Errorf("a session that requires containment it cannot get says so:\n%s", block)
	}
	if strings.Contains(block, "still running after") {
		t.Errorf("a command that cannot run has no ceiling to reach:\n%s", block)
	}
}

func TestOffersCommands_AConversationIsToldNothingAboutRunningOne(t *testing.T) {
	if offersCommands(tools.Definitions()) {
		t.Error("a read-only toolset has no command to describe the environment of")
	}
	if !offersCommands(tools.DefinitionsWithExec()) {
		t.Error("a toolset with execute_command does")
	}
}
