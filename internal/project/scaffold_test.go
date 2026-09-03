package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNeedsScaffold_OnlyWhereTheModelHasBeenToldNothing(t *testing.T) {
	dir := t.TempDir()
	if !NeedsScaffold(dir) {
		t.Fatal("an empty directory could take the scaffold")
	}

	if _, err := Scaffold(dir); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if NeedsScaffold(dir) {
		t.Fatal("a scaffolded checkout was offered the scaffold again")
	}
}

// A checkout whose context is an AGENTS.md has already said what it is, and
// shhh's own file would win over it — so scaffolding there would replace
// what the model reads with a template that says nothing.
func TestNeedsScaffold_IsSilentWhereAnotherContextFileIsRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# this project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if NeedsScaffold(dir) {
		t.Fatal("a checkout with an AGENTS.md was offered a template that would outrank it")
	}

	// And from a subdirectory, because that is where the file is read from
	// too: the walk goes up.
	sub := filepath.Join(dir, "internal")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if NeedsScaffold(sub) {
		t.Fatal("the walk up the tree was not consulted")
	}
}

// A checkout still holding the old single .shhh file is the doctor's, not
// the offer's: writing a directory over it is impossible, and replacing it
// would lose what it says.
func TestNeedsScaffold_IsSilentOnTheOldLayout(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, StateDir), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if NeedsScaffold(dir) {
		t.Fatal("the old layout was offered a scaffold it cannot take")
	}
	if _, err := Scaffold(dir); err == nil {
		t.Fatal("Scaffold overwrote a file from the old layout")
	}
}

func TestScaffold_WritesTheContextFileAndRefusesToReplaceIt(t *testing.T) {
	dir := t.TempDir()
	path, err := Scaffold(dir)
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if want := filepath.Join(dir, ContextFile); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) == 0 {
		t.Fatal("the scaffolded file is empty")
	}
	// Every line is a comment, so a file nobody edits adds nothing to the
	// prompt it is read into.
	for _, line := range splitLines(string(body)) {
		if line != "" && line[0] != '#' {
			t.Fatalf("the template carries a non-comment line: %q", line)
		}
	}

	if err := os.WriteFile(path, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Scaffold(dir); err == nil {
		t.Fatal("Scaffold replaced a context file that was already there")
	}
	if body, _ := os.ReadFile(path); string(body) != "mine\n" {
		t.Fatalf("the existing file was rewritten: %q", body)
	}
}

// The paths the card lists are the paths the write creates.
func TestScaffoldPaths_AreWhatScaffoldCreates(t *testing.T) {
	dir := t.TempDir()
	if _, err := Scaffold(dir); err != nil {
		t.Fatal(err)
	}
	for _, rel := range ScaffoldPaths(dir, dir) {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(strings.TrimSuffix(rel, "/")))); err != nil {
			t.Fatalf("the card lists %q, which the write did not create: %v", rel, err)
		}
	}
}

// Read from somewhere else, the paths say where the write is actually
// going: a bare `.shhh/` from a subdirectory names a directory nothing is
// about to touch.
func TestScaffoldPaths_NameTheWriteFromWhereItIsRead(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "internal", "cli")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	here := ScaffoldPaths(root, root)
	if here[0] != StateDir+"/" || here[1] != ContextFile {
		t.Fatalf("from the root the paths should be bare, got %v", here)
	}
	away := ScaffoldPaths(root, sub)
	for i, p := range away {
		if p == here[i] {
			t.Fatalf("path %d reads the same from two directories (%q); it names the wrong one from one of them", i, p)
		}
	}
}

func TestRoot_IsTheRepositoryElseTheDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "internal", "cli")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Root(sub); got != sub {
		t.Fatalf("Root outside a repository = %q, want %q", got, sub)
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Root(sub); got != dir {
		t.Fatalf("Root = %q, want the repository root %q", got, dir)
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// A checkout the rest of the field has already instructed has said what it
// is, so the offer is not made over the top of it.
func TestNeedsScaffold_ClaudeMdCounts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("build with make\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if NeedsScaffold(dir) {
		t.Error("a checkout with a CLAUDE.md is offered a template that would say less than the file it has")
	}
}

// An offer that cannot be accepted is not made: a context file nobody filled
// in is no longer read into the prompt, but Scaffold will not write over it.
func TestNeedsScaffold_ABlankContextFileIsStillAFile(t *testing.T) {
	dir := t.TempDir()
	writeContext(t, dir, "")
	if NeedsScaffold(dir) {
		t.Fatal("the offer was made over a file Scaffold refuses to replace")
	}
	if _, err := Scaffold(dir); err == nil {
		t.Fatal("Scaffold wrote over an existing file")
	}
}
