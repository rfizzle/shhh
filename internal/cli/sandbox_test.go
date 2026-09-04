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
