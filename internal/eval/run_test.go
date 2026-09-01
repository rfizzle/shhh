package eval

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeShhh is a script standing in for the binary under measurement: it
// writes a transcript on stdout and does whatever the case needs doing to the
// workspace, so the runner can be measured without a provider.
func fakeShhh(t *testing.T, body string) string {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell")
	}
	path := filepath.Join(t.TempDir(), "fake-shhh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func suite(t *testing.T, check []string, files map[string]string) []Case {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "sample")
	ws := filepath.Join(dir, WorkspaceDir)
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	argv := "[\"" + strings.Join(check, "\", \"") + "\"]"
	body := "prompt = \"do the thing\"\ncheck = " + argv + "\n"
	if err := os.WriteFile(filepath.Join(dir, CaseFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cases, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return cases
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

const emptyTranscript = `printf '{"success":true,"final":"done","usage":{"prompt_tokens":10,"completion_tokens":2},"messages":[]}'`

// The verdict is the check's, and nothing else. A session that claims success
// and changes nothing fails a case whose check says otherwise.
func TestASessionThatDidNothingFailsTheCheck(t *testing.T) {
	requireGit(t)
	bin := fakeShhh(t, emptyTranscript)
	cases := suite(t, []string{"test", "-f", "fixed"}, map[string]string{"a.txt": "x"})

	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	if got := sum.Results[0].Verdict(); got != Failed {
		t.Fatalf("verdict = %v, want Failed", got)
	}
}

// And a session that did the work passes it.
func TestASessionThatDidTheWorkPasses(t *testing.T) {
	requireGit(t)
	bin := fakeShhh(t, "touch fixed\n"+emptyTranscript)
	cases := suite(t, []string{"test", "-f", "fixed"}, map[string]string{"a.txt": "x"})

	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	if got := sum.Results[0].Verdict(); got != Passed {
		t.Fatalf("verdict = %v, want Passed: %+v", got, sum.Results[0].Attempts)
	}
}

// The fixture is copied, so a case that edits its workspace does not edit
// itself — otherwise the second attempt grades the first one's leftovers.
func TestAnAttemptCannotEditTheCaseItself(t *testing.T) {
	requireGit(t)
	bin := fakeShhh(t, "echo changed > a.txt\n"+emptyTranscript)
	cases := suite(t, []string{"true"}, map[string]string{"a.txt": "original"})

	if _, err := Run(context.Background(), cases, Options{Binary: bin, Repeat: 2}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(cases[0].Workspace, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("the fixture was edited by the run: %q", got)
	}
}

// The session runs in a repository, because a session that is not in one
// behaves differently — its prompt, its undo and its grants all change.
func TestTheWorkspaceIsARepository(t *testing.T) {
	requireGit(t)
	bin := fakeShhh(t, "git rev-parse --show-toplevel > /dev/null 2>&1 || exit 3\n"+emptyTranscript)
	cases := suite(t, []string{"true"}, map[string]string{"a.txt": "x"})

	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	if a := sum.Results[0].Attempts[0]; a.Err != nil {
		t.Fatalf("the session was not in a repository: %v", a.Err)
	}
}

// A session that fails is a run that broke, not a task that was failed, and
// the check must not even run.
func TestASessionThatFailsIsNotACaseThatFailed(t *testing.T) {
	requireGit(t)
	bin := fakeShhh(t, "echo 'no provider' >&2; exit 1")
	cases := suite(t, []string{"true"}, map[string]string{"a.txt": "x"})

	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	if got := sum.Results[0].Verdict(); got != Errored {
		t.Fatalf("verdict = %v, want Errored", got)
	}
	if a := sum.Results[0].Attempts[0]; a.Err == nil || !strings.Contains(a.Err.Error(), "no provider") {
		t.Errorf("the reason should reach the row: %v", a.Err)
	}
}

// The rounds are counted from the transcript rather than from anything the
// session says about itself.
func TestRoundsAndCallsAreReadFromTheTranscript(t *testing.T) {
	requireGit(t)
	const transcript = `printf '{"success":true,"usage":{"prompt_tokens":100,"completion_tokens":20},"messages":[` +
		`{"role":"user","content":"go"},` +
		`{"role":"assistant","tool_calls":[{"name":"read_file"},{"name":"search"}]},` +
		`{"role":"tool","content":"x"},` +
		`{"role":"assistant","tool_calls":[{"name":"edit_file"}]},` +
		`{"role":"assistant","content":"done"}]}'`
	bin := fakeShhh(t, transcript)
	cases := suite(t, []string{"true"}, map[string]string{"a.txt": "x"})

	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	a := sum.Results[0].Attempts[0]
	if a.Rounds != 2 {
		t.Errorf("rounds = %d, want 2 — a message with calls is one round however many it asked for", a.Rounds)
	}
	if a.Calls != 3 {
		t.Errorf("calls = %d, want 3", a.Calls)
	}
	if a.TokensIn != 100 || a.TokensOut != 20 {
		t.Errorf("usage = %d/%d, want 100/20", a.TokensIn, a.TokensOut)
	}
}

func TestRepeatRunsEachCaseThatManyTimes(t *testing.T) {
	requireGit(t)
	bin := fakeShhh(t, emptyTranscript)
	cases := suite(t, []string{"true"}, map[string]string{"a.txt": "x"})

	sum, err := Run(context.Background(), cases, Options{Binary: bin, Repeat: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(sum.Results[0].Attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

// A case the machine cannot run costs no attempts at all.
func TestASkippedCaseIsNotRun(t *testing.T) {
	bin := fakeShhh(t, "exit 9")
	cases := suite(t, []string{"true"}, map[string]string{"a.txt": "x"})
	cases[0].Skip = "not on PATH: cargo"

	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Results[0].Attempts) != 0 {
		t.Fatal("a skipped case must not be attempted")
	}
	if got := sum.Results[0].Verdict(); got != Skipped {
		t.Fatalf("verdict = %v, want Skipped", got)
	}
}

// A fixture that could reach outside its own directory could edit the suite
// it is being graded by.
func TestAFixtureMayNotContainASymlink(t *testing.T) {
	requireGit(t)
	cases := suite(t, []string{"true"}, map[string]string{"a.txt": "x"})
	link := filepath.Join(cases[0].Workspace, "escape")
	if err := os.Symlink("/etc", link); err != nil {
		t.Skip("cannot create a symlink here")
	}

	bin := fakeShhh(t, emptyTranscript)
	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	a := sum.Results[0].Attempts[0]
	if a.Err == nil || !strings.Contains(a.Err.Error(), "symlink") {
		t.Fatalf("a symlinked fixture should be refused, got %v", a.Err)
	}
}
