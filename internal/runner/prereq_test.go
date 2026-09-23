package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/tools"
)

// Every category a spawn can fail in, produced by an actual failed spawn
// wherever a test can arrange one. The command never runs in any of them, so
// what the reader is handed is entirely the harness's account of itself: the
// category, the operating system's words, and what is still possible.
func TestASpawnFailureNamesThePrerequisiteThatFailed(t *testing.T) {
	needShell(t)
	gone := filepath.Join(t.TempDir(), "worktree-that-was-removed")
	noexec := filepath.Join(t.TempDir(), "not-executable")
	if err := os.WriteFile(noexec, []byte("#!/bin/sh\ntrue\n"), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	cases := []struct {
		name   string
		run    func() tools.ExecResult
		prereq tools.ExecPrereq
		detail string
	}{
		{
			// The one this story was filed for: a child's worktree removed
			// while the child was still working in it.
			name:   "the working directory is gone",
			run:    func() tools.ExecResult { return RunCaptureInResult(context.Background(), gone, "echo hi") },
			prereq: tools.PrereqWorkingDir,
			detail: gone,
		},
		{
			// The shell cannot be uninstalled for the length of a test, so
			// the argv is built the way the shell form builds it and the
			// program is one that is not there.
			name: "the execution shell is gone",
			run: func() tools.ExecResult {
				return capture(context.Background(), "", "echo hi",
					[]string{filepath.Join(t.TempDir(), "no-shell-here"), "-c", "echo hi"}, spawnShell, nil)
			},
			prereq: tools.PrereqShell,
			detail: "no-shell-here",
		},
		{
			// A pre-built argv is a mechanism wrapped around the command, so
			// the same missing program is a different prerequisite.
			name: "the containment mechanism is gone",
			run: func() tools.ExecResult {
				return RunCaptureArgvInResult(context.Background(), "", "echo hi",
					[]string{filepath.Join(t.TempDir(), "no-bwrap-here"), "echo", "hi"})
			},
			prereq: tools.PrereqContainment,
			detail: "no-bwrap-here",
		},
		{
			name: "the operating system refuses it",
			run: func() tools.ExecResult {
				return RunCaptureArgvInResult(context.Background(), "", "the fixture", []string{noexec})
			},
			prereq: tools.PrereqPermission,
			detail: noexec,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.run()
			if got.Outcome != tools.ExecDidNotStart {
				t.Fatalf("outcome = %q, want %q (%+v)", got.Outcome, tools.ExecDidNotStart, got)
			}
			if got.Prereq != tc.prereq {
				t.Fatalf("prereq = %q, want %q; output:\n%s", got.Prereq, tc.prereq, got.Output)
			}
			if got.ExitCode != -1 {
				t.Errorf("exit code = %d, want -1: a command that never ran has no status", got.ExitCode)
			}
			if !strings.Contains(got.Output, tc.detail) {
				t.Errorf("the operating system's own detail is gone from:\n%s", got.Output)
			}
			// The same category reaches the surfaces that read the formatted
			// result and the ones still on the older output/status pair —
			// the chat's command path, a contained runner, a child's own
			// command.
			if p := tools.ExecPrereqOf(tools.FormatExecResult(got)); p != tc.prereq {
				t.Errorf("the formatted result reads as %q, want %q", p, tc.prereq)
			}
			label, _, _ := strings.Cut(tools.ExecPrereqReport(tc.prereq, ""), "\n")
			text, code := legacyResult(got)
			if code != -1 || !strings.Contains(text, label) {
				t.Errorf("the tuple form lost the account: %d %q", code, text)
			}
		})
	}
}

// The category is a fact about the machine, so a command that ran and failed
// never has one — however much its own output sounds like a broken host.
func TestACommandThatRanIsNeverAHarnessFailure(t *testing.T) {
	needShell(t)
	got := RunCaptureResult(context.Background(), "shhh-no-such-program-xyz")
	if got.Outcome != tools.ExecExited {
		t.Fatalf("outcome = %q, want %q (%+v)", got.Outcome, tools.ExecExited, got)
	}
	if got.Prereq != "" {
		t.Fatalf("a shell saying `command not found` was classified as %q:\n%s", got.Prereq, got.Output)
	}
	if !strings.Contains(strings.ToLower(got.Output), "not found") {
		t.Errorf("the shell's own words are gone from:\n%s", got.Output)
	}
	if p := tools.ExecPrereqOf(tools.FormatExecResult(got)); p != "" {
		t.Errorf("the formatted result reads as harness failure %q", p)
	}
}

// A spawn error none of the four named prerequisites explains is still one of
// the vocabulary rather than nothing: a category nothing falls out of is a
// category that has begun guessing.
func TestAnUnrecognisedSpawnErrorIsStillClassified(t *testing.T) {
	if got := classifyStart("", spawnShell, errors.New("resource temporarily unavailable")); got != tools.PrereqSpawn {
		t.Errorf("classifyStart() = %q, want %q", got, tools.PrereqSpawn)
	}
}

// A working directory that is there but is not a directory is the same
// failure as one that is gone, and the error the operating system reports
// names the shell rather than the path — which is why the directory is asked
// about rather than read out of the message.
func TestAWorkingDirectoryThatIsAFileIsAWorkingDirectoryFailure(t *testing.T) {
	needShell(t)
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	got := RunCaptureInResult(context.Background(), file, "echo hi")
	if got.Prereq != tools.PrereqWorkingDir {
		t.Fatalf("prereq = %q, want %q; output:\n%s", got.Prereq, tools.PrereqWorkingDir, got.Output)
	}
}

// A wrap that could not be built is a command that never started: the
// containment's own failure, unless what failed under it was the working
// directory the policy starts from, which is a checkout removed under the
// session and not a missing mechanism. The pair a legacy caller reads keeps
// the category in its text.
func TestAWrapFailureIsClassifiedLikeASpawn(t *testing.T) {
	restore := getwd
	t.Cleanup(func() { getwd = restore })

	here := t.TempDir()
	getwd = func() (string, error) { return here, nil }
	contained := WrapFailure(errors.New("wrap unsupported: bwrap vanished"))
	if contained.Outcome != tools.ExecDidNotStart || contained.Prereq != tools.PrereqContainment || contained.ExitCode != -1 {
		t.Fatalf("got %+v, want a containment failure that did not start", contained)
	}
	if !strings.Contains(contained.Output, "bwrap vanished") {
		t.Fatalf("the operating system's words should be kept:\n%s", contained.Output)
	}

	getwd = func() (string, error) { return "", errors.New("getwd: no such file or directory") }
	gone := WrapFailure(errors.New("getwd: no such file or directory"))
	if gone.Prereq != tools.PrereqWorkingDir {
		t.Fatalf("prereq = %q, want %q", gone.Prereq, tools.PrereqWorkingDir)
	}
	// A directory the platform still names after it was removed is gone too.
	removed := filepath.Join(here, "removed-checkout")
	getwd = func() (string, error) { return removed, nil }
	if got := WrapFailure(errors.New("cannot resolve workspace")); got.Prereq != tools.PrereqWorkingDir {
		t.Fatalf("prereq = %q, want %q", got.Prereq, tools.PrereqWorkingDir)
	}

	out, code := LegacyRunner(func(context.Context, string) tools.ExecResult { return contained })(context.Background(), "x")
	if code != -1 || !strings.HasPrefix(out, "error: containment unavailable: ") ||
		tools.ExecPrereqOf("error: command did not start\noutput:\n"+out) != tools.PrereqContainment {
		t.Fatalf("the pair should carry the category in its text, got %d %q", code, out)
	}
}
