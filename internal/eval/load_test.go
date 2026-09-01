package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCase lays out a case directory the way a suite author would.
func writeCase(t *testing.T, root, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, WorkspaceDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, CaseFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const goodCase = `
prompt = "make the tests pass"
check = ["go", "test", "./..."]
`

func TestLoadCaseNamesItselfAfterItsDirectory(t *testing.T) {
	dir := writeCase(t, t.TempDir(), "fix-the-thing", goodCase)
	c, err := LoadCase(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "fix-the-thing" {
		t.Errorf("name = %q, want the directory's", c.Name)
	}
	if c.Prompt != "make the tests pass" {
		t.Errorf("prompt = %q", c.Prompt)
	}
	if len(c.Check) != 3 {
		t.Errorf("check = %v", c.Check)
	}
}

func TestLoadCaseRefusesACaseWithNothingToDecideIt(t *testing.T) {
	dir := writeCase(t, t.TempDir(), "no-check", "prompt = \"do a thing\"\n")
	_, err := LoadCase(dir)
	if err == nil {
		t.Fatal("a case with no check has no verdict and must be refused")
	}
	if !strings.Contains(err.Error(), "check") {
		t.Errorf("the error should name the missing field: %v", err)
	}
}

func TestLoadCaseRefusesACaseWithNoTask(t *testing.T) {
	dir := writeCase(t, t.TempDir(), "no-prompt", "check = [\"true\"]\n")
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("a case with no prompt is not a task")
	}
}

func TestLoadCaseRefusesACaseWithNoWorkspace(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bare")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, CaseFile), []byte(goodCase), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadCase(dir)
	if err == nil {
		t.Fatal("a case needs somewhere to do the work")
	}
	if !strings.Contains(err.Error(), WorkspaceDir) {
		t.Errorf("the error should name what is missing: %v", err)
	}
}

// A suite keeps notes and shared fixtures beside its cases, so a directory
// that is not a case must not be one that stops the load.
func TestLoadIgnoresADirectoryThatIsNotACase(t *testing.T) {
	root := t.TempDir()
	writeCase(t, root, "real", goodCase)
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# suite"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 || cases[0].Name != "real" {
		t.Fatalf("loaded %v", cases)
	}
}

// The report has to read the same way twice, so the order cannot be the
// filesystem's.
func TestLoadIsInNameOrder(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"charlie", "alpha", "bravo"} {
		writeCase(t, root, n, goodCase)
	}
	cases, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range cases {
		got = append(got, c.Name)
	}
	if strings.Join(got, ",") != "alpha,bravo,charlie" {
		t.Errorf("order = %v", got)
	}
}

func TestLoadRefusesASuiteWithNoCases(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("an empty suite measures nothing and should say so")
	}
}

// A case that needs a toolchain the machine lacks is skipped and says why.
// Failing it would blame the agent for the machine.
func TestACaseIsSkippedRatherThanFailedWhenTheMachineCannotRunIt(t *testing.T) {
	old := lookPath
	lookPath = func(name string) bool { return name != "cargo" }
	t.Cleanup(func() { lookPath = old })

	dir := writeCase(t, t.TempDir(), "rusty", goodCase+"\nrequires = [\"go\", \"cargo\"]\n")
	c, err := LoadCase(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Skip == "" {
		t.Fatal("a case needing a missing toolchain should be skipped")
	}
	if !strings.Contains(c.Skip, "cargo") {
		t.Errorf("the skip reason should name what is missing: %q", c.Skip)
	}
	if strings.Contains(c.Skip, ",") {
		t.Errorf("only what is missing should be named, not every requirement: %q", c.Skip)
	}
}

func TestACaseWhoseRequirementsAreMetIsNotSkipped(t *testing.T) {
	old := lookPath
	lookPath = func(string) bool { return true }
	t.Cleanup(func() { lookPath = old })

	dir := writeCase(t, t.TempDir(), "fine", goodCase+"\nrequires = [\"go\"]\n")
	c, err := LoadCase(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Skip != "" {
		t.Errorf("skip = %q, want none", c.Skip)
	}
}
