//go:build !windows

package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/scope"
)

func writeGateSuite(t *testing.T, ws, exe string, args ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(ws, ".shhh"), 0o700); err != nil {
		t.Fatal(err)
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = `"` + a + `"`
	}
	suite := `{"suites": {"default": {"checks": [{"name": "c", "exe": "` + exe + `", "args": [` + strings.Join(quoted, ",") + `]}]}}}`
	if err := os.WriteFile(filepath.Join(ws, ".shhh", "quality.json"), []byte(suite), 0o600); err != nil {
		t.Fatal(err)
	}
}

// In a sandbox session every check execs in the session's container, through
// the helper like the commands beside it: the engine is asked for it, a tool
// only the image has is not looked for on this machine, the result names the
// container as what contained it in the words the command's row uses, and a
// generator's tree, which the container does not mount, is refused rather
// than run on the host.
func TestGateRunsInTheSandboxContainer(t *testing.T) {
	log := fakeSandboxEngine(t, helperAnswers)
	cfg, sc, ws := sandboxTestSetup(t)
	withProjectTrust(t, project.Trust{Root: ws, Granted: true})

	gate := openQualityGate(cfg, nil, sc, true)
	if gate == nil {
		t.Fatal("a trusted checkout has its gate")
	}
	// The suite is in a temp tree rather than the one the test runs in.
	gate.Workspace = ws
	if gate.Wrap != nil || gate.WrapIn != nil {
		t.Fatal("a sandbox session's gate was wrapped by the host's mechanism")
	}
	_, cleanup, err := sandboxContainment(context.Background(), cfg, ws, sc, nil, gate)
	if err != nil {
		t.Fatalf("sandboxContainment: %v", err)
	}
	defer cleanup()
	if gate.Wrap == nil || gate.StopArgv == nil || gate.Mechanism != sandboxMechanism {
		t.Fatalf("the gate was not given the container: mechanism %q", gate.Mechanism)
	}

	writeGateSuite(t, ws, "echo", "from-the-container")
	res, err := gate.Run(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != quality.VerdictPass || !strings.Contains(res.Checks[0].Output, "from-the-container") {
		t.Fatalf("verdict %s (%s): %+v", res.Verdict, res.Reason, res.Checks)
	}
	if want := sandboxMechanism + ", workspace writable (the container mounts it so)"; res.Contained != want {
		t.Errorf("contained = %q, want %q", res.Contained, want)
	}
	// The exec carries a token for the second stop, and runs the check under
	// the helper, from the workspace.
	calls := engineCalls(t, log)
	if !strings.Contains(calls, "exec -i --workdir") || !strings.Contains(calls, sandbox.ExecArg+" --id=") ||
		!strings.Contains(calls, "-- echo from-the-container") {
		t.Errorf("the check did not exec in the container:\n%s", calls)
	}

	// A tool only the image carries is the container's to find.
	writeGateSuite(t, ws, "shhh-test-tool-only-the-image-has")
	if _, err := gate.Run(context.Background(), "default"); err != nil {
		t.Fatal(err)
	}
	if calls := engineCalls(t, log); !strings.Contains(calls, "-- shhh-test-tool-only-the-image-has") {
		t.Errorf("a name this machine lacks never reached the container:\n%s", calls)
	}

	// The second leg of a stop is reachable from the argv the gate spawns.
	wrapped, err := gate.Wrap([]string{"true"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if stop := gate.StopArgv(wrapped); len(stop) == 0 || !strings.HasPrefix(stop[len(stop)-1], "--stop=") {
		t.Errorf("a cancelled check has no second stop: %v", stop)
	}
	if _, err := gate.WrapIn(ws, []string{"true"}); err == nil || err.Error() != gateGeneratorRefusal {
		t.Errorf("a generator was not refused in a sentence: %v", err)
	}
}

// Without a sandbox the gate is what it was: the host's mechanism where one
// is available and nothing where none is, never the container's attached
// wrap, and the result's words are the host's.
func TestGateContainmentUnchangedWithoutSandbox(t *testing.T) {
	ws := t.TempDir()
	withProjectTrust(t, project.Trust{Root: ws, Granted: true})
	sc, errs := scope.New(ws)
	if len(errs) > 0 {
		t.Fatalf("scope: %v", errs)
	}
	gate := openQualityGate(config.Config{}, nil, sc, false)
	if gate == nil {
		t.Fatal("a trusted checkout has its gate")
	}
	gate.Workspace = ws
	avail := sandbox.Detect()
	if gate.StopArgv != nil {
		t.Error("a host gate was told its checks are attached")
	}
	if avail.OK != (gate.Wrap != nil) || avail.OK != (gate.WrapIn != nil) || (avail.OK && gate.Mechanism != avail.Mechanism) {
		t.Errorf("available %v (%s): wrap %v, wrapIn %v, mechanism %q", avail.OK, avail.Mechanism, gate.Wrap != nil, gate.WrapIn != nil, gate.Mechanism)
	}
	if !avail.OK {
		writeGateSuite(t, ws, "true")
		res, err := gate.Run(context.Background(), "default")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(res.Contained, "unconfined") {
			t.Errorf("contained = %q", res.Contained)
		}
	}
}
