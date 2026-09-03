package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeContext(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".shhh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".shhh", "project.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindFrom_InCurrentDir(t *testing.T) {
	dir := t.TempDir()
	content := "This project uses Docker Compose for services."
	writeContext(t, dir, content)

	_, got := FindFrom(dir)
	if got != content {
		t.Errorf("FindFrom() = %q, want %q", got, content)
	}
}

func TestFindFrom_InParentDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "src", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	content := "Use make test for tests."
	writeContext(t, root, content)

	_, got := FindFrom(sub)
	if got != content {
		t.Errorf("FindFrom() = %q, want %q", got, content)
	}
}

func TestFindFrom_NotPresent(t *testing.T) {
	dir := t.TempDir()

	_, got := FindFrom(dir)
	if got != "" {
		t.Errorf("FindFrom() = %q, want empty string", got)
	}
}

// A caller with no directory to name gets nothing, rather than the process's
// own directory dressed up as an answer to a question about somewhere stated.
func TestFindFrom_NoDirectoryReadsNothing(t *testing.T) {
	if p, c := FindFrom(""); p != "" || c != "" {
		t.Errorf("FindFrom(\"\") = %q, %q, want nothing", p, c)
	}
	if got := FindContextFrom(""); got != "" {
		t.Errorf("FindContextFrom(\"\") = %q, want nothing", got)
	}
}

func TestFindFrom_AgentsMd(t *testing.T) {
	dir := t.TempDir()
	content := "# Agent notes\nRun make ci before committing."
	must(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0o644))

	_, got := FindFrom(dir)
	if got != content {
		t.Errorf("FindFrom() = %q, want %q", got, content)
	}
}

func TestFindFrom_ShhhBeatsAgentsMd(t *testing.T) {
	dir := t.TempDir()
	writeContext(t, dir, "shhh context")
	must(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agents context"), 0o644))

	_, got := FindFrom(dir)
	if got != "shhh context" {
		t.Errorf("FindFrom() = %q, want %q", got, "shhh context")
	}
}

func TestFindFrom_AgentsMdInParentDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	content := "Monorepo: services live under src/."
	must(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(content), 0o644))

	_, got := FindFrom(sub)
	if got != content {
		t.Errorf("FindFrom() = %q, want %q", got, content)
	}
}

func TestFindFrom_NearestWins(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "child")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	writeContext(t, root, "root context")
	writeContext(t, sub, "child context")

	_, got := FindFrom(sub)
	if got != "child context" {
		t.Errorf("FindFrom() = %q, want %q", got, "child context")
	}
}

func TestFindFrom_OldSingleFileIsNotRead(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, ".shhh"), []byte("old layout"), 0o644))

	if _, got := FindFrom(dir); got != "" {
		t.Errorf("FindFrom() = %q, want empty: the old file is the doctor's to move", got)
	}
}

// must fails the test on an error from setting it up.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Without a repository the root is the nearest directory that already holds
// a shhh directory, so two terminals opened at different depths of one
// project key their backlog and their refused offers on one directory.
func TestRoot_WithoutARepositoryFindsTheStateDirectory(t *testing.T) {
	base := t.TempDir()
	proj := filepath.Join(base, "proj")
	sub := filepath.Join(proj, "a", "b")
	mustMkdir(t, filepath.Join(proj, StateDir))
	mustMkdir(t, sub)

	if got := Root(sub); got != proj {
		t.Errorf("Root(%s) = %s, want %s", sub, got, proj)
	}
	if got := Root(proj); got != proj {
		t.Errorf("Root at the state directory itself = %s, want %s", got, proj)
	}
	if InRepo(sub) {
		t.Error("a directory with no .git anywhere above it is not in a repository")
	}
}

// A repository still wins: the state directory is the answer only when the
// walk finds no .git at all, so nothing about an existing checkout moves.
func TestRoot_ARepositoryBeatsAStateDirectory(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	sub := filepath.Join(repo, "pkg")
	mustMkdir(t, filepath.Join(repo, ".git"))
	mustMkdir(t, filepath.Join(sub, StateDir))

	if got := Root(sub); got != repo {
		t.Errorf("Root(%s) = %s, want the repository %s", sub, got, repo)
	}
	if !InRepo(sub) {
		t.Error("a directory under a .git is in a repository")
	}
}

// The old layout's single .shhh file is not a root: a checkout still holding
// one has a doctor migration waiting, and reading it as a root would key the
// project on it for as long as the migration is unmade.
func TestRoot_AStateFileIsNotARoot(t *testing.T) {
	base := t.TempDir()
	proj := filepath.Join(base, "proj")
	sub := filepath.Join(proj, "a")
	mustMkdir(t, sub)
	if err := os.WriteFile(filepath.Join(proj, StateDir), []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Root(sub); got != sub {
		t.Errorf("Root(%s) = %s, want the directory itself", sub, got)
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

// A checkout the rest of the field has instructed is not read as bare: a
// CLAUDE.md is an instruction file like the other two.
func TestInstructions_ClaudeMdAlone(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, ".git"))
	must(t, os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("Run make ci.\n"), 0o644))

	got := Instructions(dir, "")
	if len(got) != 1 || got[0].Display != "CLAUDE.md" || got[0].Text != "Run make ci.\n" {
		t.Fatalf("Instructions() = %+v, want the one CLAUDE.md", got)
	}
}

// Root first, cwd last: a nested file refines what the root said, so it is
// stated after it and has the last word.
func TestInstructions_RootFirstThenNested(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "svc")
	mustMkdir(t, filepath.Join(root, ".git"))
	mustMkdir(t, sub)
	must(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("root rules\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(sub, "CLAUDE.md"), []byte("service rules\n"), 0o644))

	got := Instructions(sub, "")
	if len(got) != 2 {
		t.Fatalf("Instructions() = %+v, want both files", got)
	}
	if got[0].Display != "AGENTS.md" || got[1].Display != "svc/CLAUDE.md" {
		t.Errorf("displays = %q, %q, want them stated from the root", got[0].Display, got[1].Display)
	}
	if got[0].Text != "root rules\n" || got[1].Text != "service rules\n" {
		t.Errorf("order = %q then %q, want the root first", got[0].Text, got[1].Text)
	}
}

// One directory contributes one file, in the recognised order, so a checkout
// keeping the same instructions under three names is not told them thrice.
func TestInstructions_OneFilePerDirectory(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, ".git"))
	writeContext(t, dir, "shhh context")
	must(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agents"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("claude"), 0o644))

	got := Instructions(dir, "")
	if len(got) != 1 || got[0].Text != "shhh context" {
		t.Fatalf("Instructions() = %+v, want shhh's own file alone", got)
	}
}

// The walk stops at the root: a file above the repository was not written
// about this project.
func TestInstructions_StopsAtTheRepositoryRoot(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	mustMkdir(t, filepath.Join(repo, ".git"))
	must(t, os.WriteFile(filepath.Join(base, "AGENTS.md"), []byte("someone else's\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("this project's\n"), 0o644))

	got := Instructions(repo, "")
	if len(got) != 1 || got[0].Text != "this project's\n" {
		t.Fatalf("Instructions() = %+v, want only the file inside the repository", got)
	}
}

// With neither a repository nor a shhh directory there is nothing to say
// where the project begins, and the nearest file above is still read rather
// than the project being reported as bare.
func TestInstructions_WithoutARootReadsTheNearestFileAbove(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "src")
	mustMkdir(t, sub)
	must(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("monorepo\n"), 0o644))

	got := Instructions(sub, "")
	if len(got) != 1 || got[0].Text != "monorepo\n" {
		t.Fatalf("Instructions() = %+v, want the file one directory up", got)
	}
}

// The user's own file is read ahead of the project's, so the project has the
// last word on anything the two both mention.
func TestInstructions_UserFileComesFirst(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, ".git"))
	must(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("project\n"), 0o644))
	user := filepath.Join(t.TempDir(), "instructions.md")
	must(t, os.WriteFile(user, []byte("mine\n"), 0o644))

	got := Instructions(dir, user)
	if len(got) != 2 || got[0].Text != "mine\n" || got[1].Text != "project\n" {
		t.Fatalf("Instructions() = %+v, want the user's file first", got)
	}
}

// A file with nothing in it is stepped over rather than taken, so the next
// recognised name in the same directory is still read.
func TestInstructions_AnEmptyFileIsNotAnInstruction(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, ".git"))
	must(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("   \n"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("read me\n"), 0o644))

	got := Instructions(dir, "")
	if len(got) != 1 || got[0].Text != "read me\n" {
		t.Fatalf("Instructions() = %+v, want the file that says something", got)
	}
}

func TestInstructionBlock_NamesEachFileAndKeepsTheOrder(t *testing.T) {
	block := InstructionBlock([]Instruction{
		{Display: "AGENTS.md", Text: "root rules\n"},
		{Display: "svc/CLAUDE.md", Text: "service rules\n"},
	}, 1000)

	want := "# Project instructions"
	if !strings.HasPrefix(block, want) {
		t.Fatalf("block does not start with %q:\n%s", want, block)
	}
	root := strings.Index(block, "## AGENTS.md")
	svc := strings.Index(block, "## svc/CLAUDE.md")
	if root < 0 || svc < 0 || root > svc {
		t.Fatalf("headings are missing or out of order:\n%s", block)
	}
	if !strings.Contains(block, "root rules") || !strings.Contains(block, "service rules") {
		t.Fatalf("block lost a file's text:\n%s", block)
	}
}

// Nothing collected is no section at all, not an empty heading.
func TestInstructionBlock_EmptyWithoutFiles(t *testing.T) {
	if got := InstructionBlock(nil, 1000); got != "" {
		t.Errorf("InstructionBlock(nil) = %q, want nothing", got)
	}
}

// The cap cuts the farthest-from-cwd file first and says so, so a model
// following half an instruction can see that the other half existed.
func TestInstructionBlock_CutsTheOutermostFileFirstAndSaysSo(t *testing.T) {
	outer := strings.Repeat("outer line\n", 40)
	inner := strings.Repeat("inner line\n", 10)
	block := InstructionBlock([]Instruction{
		{Display: "AGENTS.md", Text: outer},
		{Display: "svc/CLAUDE.md", Text: inner},
	}, len(inner)+100)

	if !strings.Contains(block, "Cut to fit the budget") {
		t.Fatalf("a cut block never says it was cut:\n%s", block)
	}
	if strings.Count(block, "outer line") >= 40 {
		t.Errorf("the outermost file was not cut:\n%s", block)
	}
	if strings.Count(block, "inner line") != 10 {
		t.Errorf("the nearest file was cut before the outermost one:\n%s", block)
	}
}

// Over budget by more than the outermost file holds, that file is named with
// nothing under it rather than dropped without a word.
func TestInstructionBlock_AFileThatDoesNotFitIsStillNamed(t *testing.T) {
	block := InstructionBlock([]Instruction{
		{Display: "AGENTS.md", Text: strings.Repeat("outer\n", 20)},
		{Display: "CLAUDE.md", Text: strings.Repeat("inner\n", 20)},
	}, 60)

	if !strings.Contains(block, "## AGENTS.md") || strings.Contains(block, "outer\n") {
		t.Fatalf("the dropped file is not named, or was read anyway:\n%s", block)
	}
	if !strings.Contains(block, "Not read") {
		t.Fatalf("a dropped file says nothing about being dropped:\n%s", block)
	}
}

// A whole file must not be reported as cut. Every text file ends in a
// newline and the newline goes before the next heading, so a block that
// decided this by comparing lengths would tell the model its instructions
// were incomplete in every session.
func TestInstructionBlock_AWholeFileIsNotReportedAsCut(t *testing.T) {
	block := InstructionBlock([]Instruction{{Display: "AGENTS.md", Text: "root rules\n"}}, 1000)
	if strings.Contains(block, "Cut to fit") || strings.Contains(block, "Not read") {
		t.Fatalf("a file well inside the budget is reported as cut:\n%s", block)
	}
	if !strings.Contains(block, "root rules") {
		t.Fatalf("the file's text is missing:\n%s", block)
	}
}

// A boundary something in the tree marked is not climbed past, however
// little was found inside it: a sibling checkout's file, or one sitting in a
// home directory, is not this project's instruction.
func TestInstructions_ARepositoryWithNothingInItReadsNothingAbove(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	sub := filepath.Join(repo, "pkg")
	mustMkdir(t, filepath.Join(repo, ".git"))
	mustMkdir(t, sub)
	must(t, os.WriteFile(filepath.Join(base, "AGENTS.md"), []byte("someone else's\n"), 0o644))

	if got := Instructions(sub, ""); len(got) != 0 {
		t.Fatalf("Instructions() = %+v, want nothing from outside the repository", got)
	}
}

// The same for a project marked by a shhh directory rather than a checkout.
func TestInstructions_AStateDirectoryBoundsTheWalkToo(t *testing.T) {
	base := t.TempDir()
	proj := filepath.Join(base, "proj")
	mustMkdir(t, filepath.Join(proj, StateDir))
	must(t, os.WriteFile(filepath.Join(base, "CLAUDE.md"), []byte("someone else's\n"), 0o644))

	if got := Instructions(proj, ""); len(got) != 0 {
		t.Fatalf("Instructions() = %+v, want nothing from outside the project", got)
	}
}

// The names on screen come from the one list that decides what is read, so a
// fourth name reaches the surfaces without a second edit.
func TestInstructionNames(t *testing.T) {
	got := InstructionNames()
	for _, name := range contextFilenames {
		if !strings.Contains(got, filepath.ToSlash(name)) {
			t.Errorf("InstructionNames() = %q, missing %q", got, name)
		}
	}
}
