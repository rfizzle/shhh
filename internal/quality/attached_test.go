//go:build !windows

package quality

import (
	"context"
	"strings"
	"testing"
)

// A runner whose wrap runs a check in a container is told so (StopArgv): the
// check is spawned with a stdin held open, the far end's cue to keep going,
// where a plain check has none; its executable is the container's to find,
// so a tool this machine lacks still reaches the wrap; and the result says
// the container mounts the workspace writable whether or not the suite asked.
func TestRunner_ACheckInAContainerIsAttachedAndFoundThere(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, `{"suites": {"default": {"checks": [
		{"name": "piped", "exe": "sh", "args": ["-c", "test -p /dev/stdin"]},
		{"name": "elsewhere", "exe": "shhh-test-tool-only-the-image-has"}]}}}`)

	var wrapped [][]string
	r := &Runner{Workspace: ws, Mechanism: "container sandbox"}
	r.Wrap = func(argv []string, _ bool) ([]string, error) {
		wrapped = append(wrapped, argv)
		if argv[0] == "shhh-test-tool-only-the-image-has" {
			return []string{"true"}, nil
		}
		return argv, nil
	}
	r.StopArgv = func([]string) []string { return nil }

	res, err := r.Run(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictPass {
		t.Fatalf("verdict %s (%s): %+v", res.Verdict, res.Reason, res.Checks)
	}
	if want := "container sandbox, workspace writable (the container mounts it so)"; res.Contained != want {
		t.Errorf("contained = %q, want %q", res.Contained, want)
	}
	if len(wrapped) < 2 || wrapped[1][0] != "shhh-test-tool-only-the-image-has" {
		t.Errorf("wrapped %v: the name was resolved on this machine", wrapped)
	}

	// The same runner with a plain wrap keeps the old words and the old
	// resolution.
	r.StopArgv = nil
	res, err = r.Run(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(res.Contained, "read-only workspace") {
		t.Errorf("contained = %q", res.Contained)
	}
	if res.Verdict != VerdictBlocked {
		t.Errorf("a tool this machine lacks ran: %s", res.Verdict)
	}
}
