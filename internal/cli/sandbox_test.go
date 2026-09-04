package cli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/logs"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/scope"
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
	if !strings.Contains(c.Refusal, doctorSandbox(sandbox.Detect(), "", runtime.GOOS).Fix[0]) {
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
	if out, code := run(context.Background(), "echo ran"); code != 0 || !strings.Contains(out, "ran") {
		t.Fatalf("without the knob a child's command still runs: %q (%d)", out, code)
	}

	run = childCommandRunnerUnbounded(config.Config{Sandbox: config.SandboxConfig{Require: true}}, dir, sc)
	out, code := run(context.Background(), "echo ran")
	if code == 0 || strings.Contains(out, "ran") {
		t.Fatalf("a required session must refuse a child's command, got %q (%d)", out, code)
	}
	if !strings.Contains(out, "requires containment") {
		t.Fatalf("the refusal should say why, got %q", out)
	}
}
