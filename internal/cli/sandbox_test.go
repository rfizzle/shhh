package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/logs"
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
