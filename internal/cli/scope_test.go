package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/scope"
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
