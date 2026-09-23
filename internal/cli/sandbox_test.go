package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/logs"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/tools"
)

// A session whose commands are not contained says so in the log as well as on
// the screen. The status line and the approval card are both gone by morning,
// and a run left alone overnight would otherwise end with a profile named in
// a config file and nothing anywhere saying it was never in force.
//
// An empty PATH is what makes the interesting branch the one that runs:
// bubblewrap is looked for there, so a host that has it reports none for the
// length of this test. Seatbelt lives at a fixed path and is not reached that
// way, so the assertion is still written against what the host offers — the
// contained branch has to stay silent just as firmly.
func TestBuildContainment_TheUnconfinedFallbackIsWrittenDown(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("PATH", "")
	path := filepath.Join(t.TempDir(), "shhh.log")
	logs.To(path)
	t.Cleanup(func() { logs.To("") })

	sc, errs := scope.New(t.TempDir())
	if len(errs) > 0 {
		t.Fatalf("scope: %v", errs)
	}
	cfg := config.Config{Sandbox: config.SandboxConfig{Profile: string(sandbox.ProfileWorkspaceNetless)}}
	c, err := buildContainment(cfg, sc, nil)
	if err != nil {
		t.Fatalf("build containment: %v", err)
	}

	body, err := os.ReadFile(path)
	if c.Mechanism != "" {
		if err == nil {
			t.Errorf("a contained session wrote to the log:\n%s", body)
		}
		return
	}
	if err != nil {
		t.Fatalf("nothing was written to the log: %v", err)
	}
	written := string(body)
	for _, want := range []string{"commands run unconfined", "profile=workspace-netless"} {
		if !strings.Contains(written, want) {
			t.Errorf("the line does not say %s:\n%s", want, written)
		}
	}
	// Why this host has no mechanism is a sentence about the host, often
	// naming the binary it looked for and where. It belongs on the status
	// line and in `shhh doctor`, not accumulated in a shared file.
	if c.Detail != "" && strings.Contains(written, c.Detail) {
		t.Errorf("the line carries the host's own detail:\n%s", written)
	}
}

// The chip on the title rail is what a person reads while a session works,
// and where nothing contains the commands it has to say so rather than name
// the profile a config file asked for. A tool that reports a posture it does
// not have is worse than one with none, because it is believed — and the
// pieces the approval card draws from have to agree with it: no mechanism, no
// profile, no wrap, and a network nothing is restricting.
//
// Emptying PATH is how the mechanism is taken away, since bubblewrap is
// looked for there. Seatbelt lives at a fixed path and is not reachable that
// way, so a host that contains commands anyway skips by name instead of
// asserting a branch it is not on.
func TestBuildContainment_TheChipSaysUnconfinedWhenNothingContains(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	sup := newTestProcessSupervisor(t)
	t.Setenv("PATH", "")

	sc, errs := scope.New(t.TempDir())
	if len(errs) > 0 {
		t.Fatalf("scope: %v", errs)
	}
	cfg := config.Config{Sandbox: config.SandboxConfig{Profile: string(sandbox.ProfileWorkspaceNetless)}}
	c, err := buildContainment(cfg, sc, sup)
	if err != nil {
		t.Fatalf("build containment: %v", err)
	}
	if c.Mechanism != "" {
		t.Skipf("this host contains commands with %s, which emptying PATH does not remove", c.Mechanism)
	}

	if !strings.HasPrefix(c.Status, "unconfined") {
		t.Errorf("the chip reads %q", c.Status)
	}
	// Why, on the chip and not only in the doctor: the reader who sees it is
	// the one who can install the mechanism.
	if c.Detail == "" || !strings.Contains(c.Status, c.Detail) {
		t.Errorf("the chip %q does not carry the reason %q", c.Status, c.Detail)
	}
	if strings.Contains(c.Status, string(sandbox.ProfileWorkspaceNetless)) || c.Profile != "" {
		t.Errorf("the chip names a profile that is not in force: %q / %q", c.Status, c.Profile)
	}
	if !c.Network {
		t.Error("a netless profile nothing enforces still leaves the network open")
	}
	if c.Run != nil {
		t.Error("an unconfined session was given a wrapped runner")
	}
	// A start is the session's other way of spawning a command, and a
	// supervisor told a mechanism it has no wrap for would report one to
	// every surface that asks it.
	if got := sup.Contained(); got != "" {
		t.Errorf("the process supervisor reports %q", got)
	}
}

// The knob turns fail-open into a choice: a session told to require
// containment on a host that has none answers every assistant command with
// the refusal, and the refusal carries the doctor's own instruction for
// installing the mechanism rather than a second wording of it.
//
// Emptying PATH is how the mechanism is taken away, since bubblewrap is
// looked for there; Seatbelt lives at a fixed path, so a host that contains
// commands anyway asserts the other half — nothing is refused where
// something is in force.
func TestBuildContainment_RequireRefusesWhereNothingContains(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("PATH", "")
	sc, errs := scope.New(t.TempDir())
	if len(errs) > 0 {
		t.Fatalf("scope: %v", errs)
	}
	cfg := config.Config{Sandbox: config.SandboxConfig{Require: true}}
	c, err := buildContainment(cfg, sc, nil)
	if err != nil {
		t.Fatalf("build containment: %v", err)
	}
	if c.Mechanism != "" {
		if c.Refusal != "" {
			t.Errorf("a contained session refuses nothing, got %q", c.Refusal)
		}
		if !c.Required {
			t.Error("a required session that has its mechanism should say so")
		}
		return
	}
	if !strings.Contains(c.Refusal, "requires containment") {
		t.Fatalf("an unconfined required session must refuse, got %q", c.Refusal)
	}
	if !strings.Contains(c.Refusal, doctorSandbox(sandbox.Detect(), sandbox.Policy{}, runtime.GOOS).Fix[0]) {
		t.Errorf("the refusal should carry the doctor's own fix, got %q", c.Refusal)
	}
}

// Without the knob nothing is refused: the honesty of the unconfined chip is
// the default, and requiring containment is the thing somebody has to ask for.
func TestBuildContainment_WithoutTheKnobNothingIsRefused(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("PATH", "")
	sc, errs := scope.New(t.TempDir())
	if len(errs) > 0 {
		t.Fatalf("scope: %v", errs)
	}
	c, err := buildContainment(config.Config{}, sc, nil)
	if err != nil {
		t.Fatalf("build containment: %v", err)
	}
	if c.Refusal != "" || c.Required {
		t.Errorf("nothing was required, so nothing is refused: %q / %v", c.Refusal, c.Required)
	}
}

// Every command path resolves its policy through the same builder, so the
// environment a contained command may carry is decided once: a value the
// session declared reaches a sub-agent's command and the quality gate's check
// exactly as it reaches the session's own, and a variable nobody named
// reaches none of them.
func TestSandboxPolicy_CarriesTheSessionsDeclaredValues(t *testing.T) {
	t.Setenv("SHHH_TEST_UNNAMED", "inherited")
	runner.SetSessionEnv([]string{"SHHH_TEST_DECLARED=named"})
	t.Cleanup(func() { runner.SetSessionEnv(nil) })

	p, err := sandboxPolicy(config.Config{})
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	if !slices.Contains(p.SecretNames, "SHHH_TEST_DECLARED") {
		t.Fatalf("the declared name should be on the allowlist, got %v", p.SecretNames)
	}
	if !slices.Contains(p.Env, "SHHH_TEST_DECLARED=named") {
		t.Fatal("the declared value should be among the pairs the policy offers")
	}
	if !slices.Contains(p.Env, "SHHH_TEST_UNNAMED=inherited") {
		t.Fatal("the policy offers the whole session environment; the allowlist is what narrows it")
	}
	if slices.Contains(p.SecretNames, "SHHH_TEST_UNNAMED") {
		t.Fatal("nobody named this one, so nothing may put it on the allowlist")
	}
}

// A child's command is the assistant's, and a child has no card to refuse on
// and nobody to refuse to. A session that requires containment refuses there
// as well, or the requirement is one a fan-out walks around.
func TestChildCommandRunner_RequiredContainmentRefusesToo(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("PATH", "")
	dir := t.TempDir()
	sc, errs := scope.New(dir)
	if len(errs) > 0 {
		t.Fatalf("scope: %v", errs)
	}
	if sandbox.Detect().OK {
		t.Skip("this host contains commands anyway, so there is no fallback to refuse")
	}

	run := childCommandRunnerUnbounded(config.Config{}, dir, sc)
	if got := run(context.Background(), "echo ran"); got.ExitCode != 0 || !strings.Contains(got.Output, "ran") {
		t.Fatalf("without the knob a child's command still runs: %q (%d)", got.Output, got.ExitCode)
	}

	run = childCommandRunnerUnbounded(config.Config{Sandbox: config.SandboxConfig{Require: true}}, dir, sc)
	got := run(context.Background(), "echo ran")
	out, code := got.Output, got.ExitCode
	if code == 0 || strings.Contains(out, "ran") || got.Outcome != tools.ExecDidNotStart {
		t.Fatalf("a required session must refuse a child's command, got %q (%d)", out, code)
	}
	if !strings.Contains(out, "requires containment") {
		t.Fatalf("the refusal should say why, got %q", out)
	}
}

// The wiring the symptom came through. A start's `env` reaches the supervisor,
// the supervisor hands it to the wrap, and the wrap has to put it in the
// policy — the mechanism rebuilds the environment from that policy alone, so
// a pair left anywhere else is gone before the command reads it, and a server
// told PORT=3001 comes up on 3000 while the model probes 3001 and debugs a
// process that is running fine. It runs under whichever mechanism the host
// has and skips by name where there is none.
func TestBuildContainment_AStartCarriesItsOwnEnv(t *testing.T) {
	avail := sandbox.Detect()
	if !avail.OK {
		t.Skipf("no containment mechanism here: %s", avail.Detail)
	}
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := t.TempDir()
	sc, errs := scope.New(dir)
	if len(errs) > 0 {
		t.Fatalf("scope: %v", errs)
	}
	sup, err := process.New(dir, nil)
	if err != nil {
		t.Fatalf("process.New: %v", err)
	}
	t.Cleanup(sup.Close)

	if _, err := buildContainment(config.Config{}, sc, sup); err != nil {
		t.Fatalf("build containment: %v", err)
	}
	if sup.Contained() == "" {
		t.Fatal("this host has a mechanism, so the supervisor should be under it")
	}
	if _, err := sup.Execute(json.RawMessage(
		`{"action":"start","name":"env","command":"echo port=$PORT","env":{"PORT":"3001"}}`)); err != nil {
		t.Fatalf("start: %v", err)
	}

	var out string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, _ = sup.Execute(json.RawMessage(`{"action":"read","name":"env","stream":"stdout","offset":0}`))
		if strings.Contains(out, "port=") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(out, "port=3001") {
		t.Fatalf("the start's own env must reach the contained process:\n%s", out)
	}
}

// fakeMechanismEnv makes the test binary a containment mechanism's last step
// (TestMain): it execs the argv it is handed, and when that exec fails it
// reports it in the words and with the status the named mechanism does.
const fakeMechanismEnv = "SHHH_TEST_FAKE_MECHANISM"

// fakeMechanism is bubblewrap's execvp, or the env Seatbelt runs in front of
// the shell, with nothing contained: the exec is real, so a shell that cannot
// be executed fails here exactly where it fails inside a sandbox, and only
// the report is the stand-in's.
func fakeMechanism(mechanism string, argv []string) int {
	err := syscall.Exec(argv[0], argv, os.Environ())
	if mechanism == "bwrap" {
		fmt.Fprintf(os.Stderr, "bwrap: execvp %s: %v\n", argv[0], err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "env: %s: %v\n", argv[0], err)
	return 127
}

// A contained command whose shell cannot be executed never started, and says
// so in the category the bare spawn names: the mechanism started, so the
// runner sees a process that exited, and the mechanism's own line is what
// reads it back. A command that ran and exited with the same status is left
// as the command's own failure.
func TestAContainedShellThatCannotExecDidNotStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no containment mechanism on windows")
	}
	bin := t.TempDir()
	// The execution shell is looked up on PATH; this one's interpreter is
	// gone, so an exec of it fails with the file plainly there.
	missing := filepath.Join(bin, "bash")
	if err := os.WriteFile(missing, []byte("#!/gone/interpreter\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	for _, mechanism := range []string{"bwrap", "sandbox-exec"} {
		t.Run(mechanism, func(t *testing.T) {
			t.Setenv(fakeMechanismEnv, mechanism)
			argv := []string{os.Args[0], missing, "-c", "echo hi"}
			got := readContained(mechanism, runner.RunCaptureArgvInResult(context.Background(), "", "echo hi", argv))
			if got.Outcome != tools.ExecDidNotStart || got.Prereq != tools.PrereqShell {
				t.Fatalf("got %+v, want an execution shell that did not start", got)
			}
			if !strings.Contains(got.Output, missing) {
				t.Fatalf("the mechanism's words should name the shell:\n%s", got.Output)
			}
		})
	}

	// The same status from a command that ran is not the mechanism's: a
	// shell answering "command not found" exits 127 too.
	ran := tools.ExecResult{Output: "bash: nosuch: command not found\n", ExitCode: 127, Outcome: tools.ExecExited}
	if got := readContained("sandbox-exec", ran); got.Outcome != tools.ExecExited {
		t.Fatalf("a command that ran was read as unstarted: %+v", got)
	}
	// Nor is the mechanism's line after output a started shell printed, nor
	// one mechanism's words under the other.
	line := "env: " + missing + ": No such file or directory\n"
	after := tools.ExecResult{Output: "hi\n" + line, ExitCode: 127, Outcome: tools.ExecExited}
	if got := readContained("sandbox-exec", after); got.Outcome != tools.ExecExited {
		t.Fatalf("output after a started shell was read as unstarted: %+v", got)
	}
	other := tools.ExecResult{Output: line, ExitCode: 127, Outcome: tools.ExecExited}
	if got := readContained("bwrap", other); got.Outcome != tools.ExecExited {
		t.Fatalf("Seatbelt's words were read under bubblewrap: %+v", got)
	}
}

// A child's contained command reads its ending the way the session's does: a
// shell the mechanism could not exec is a command that never started, not one
// that ran and exited. It runs under whichever mechanism the host has, and
// skips by name where there is none.
func TestAChildsContainedShellThatCannotExecDidNotStart(t *testing.T) {
	avail := sandbox.Detect()
	if !avail.OK {
		t.Skipf("no containment mechanism here: %s", avail.Detail)
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := t.TempDir()
	sc, errs := scope.New(dir)
	if len(errs) > 0 {
		t.Fatalf("scope: %v", errs)
	}
	// Inside the workspace, so the mechanism's own reads reach it: the file
	// is plainly there and only its interpreter is gone.
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(bin, "bash")
	if err := os.WriteFile(missing, []byte("#!/gone/interpreter\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	got := childCommandRunnerUnbounded(config.Config{}, dir, sc)(context.Background(), "echo hi")
	if got.Outcome != tools.ExecDidNotStart || got.Prereq != tools.PrereqShell {
		t.Fatalf("got %+v, want an execution shell that did not start", got)
	}
}

// A child's worktree removed under it is the child's working directory gone,
// not a containment the host is missing: the wrap fails because its policy
// cannot be built over a directory that is not there, and what is asked about
// is the directory the child's command was bound for rather than this
// process's own, which is still there. The mechanism is stood in, since
// nothing past the policy is reached.
func TestAChildsRemovedWorktreeIsItsWorkingDirectory(t *testing.T) {
	restore := childContainment
	t.Cleanup(func() { childContainment = restore })
	childContainment = func() sandbox.Availability {
		return sandbox.Availability{Mechanism: "bwrap", OK: true, Detail: "stand-in"}
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sc, errs := scope.New(dir)
	if len(errs) > 0 {
		t.Fatalf("scope: %v", errs)
	}
	run := childCommandRunnerUnbounded(config.Config{}, dir, sc)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	got := run(context.Background(), "echo hi")
	if got.Outcome != tools.ExecDidNotStart || got.Prereq != tools.PrereqWorkingDir {
		t.Fatalf("got %+v, want a working directory that was not there", got)
	}
}
