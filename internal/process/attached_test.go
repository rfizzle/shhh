//go:build !windows

package process

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// attachedWrap is a containment whose wrap hands back the argv as it is,
// with a second exec that leaves a marker behind: a container client and its
// stop, with this machine standing in for the far end.
func attachedWrap(marker string) Containment {
	return Containment{
		Mechanism: "testengine",
		Wrap: func(_ string, argv, _ []string) ([]string, []string, error) {
			return argv, []string{"/bin/sh", "-c", "echo ran > " + marker}, nil
		},
	}
}

// A process whose wrap names a second exec is a container engine's client,
// and its stop is the attached one. A client that ends once its stream is
// hung up — the helper at the far end heard it and stopped the program — is
// left to end by itself, inside the far end's grace, with no second exec.
func TestStop_AnAttachedClientEndsOnTheHangup(t *testing.T) {
	s := newTestSupervisor(t, nil)
	marker := filepath.Join(t.TempDir(), "second-exec")
	s.SetContainment(attachedWrap(marker))
	// cat ends on its stdin's end; a TERM it was sent first would be the
	// client signalled before its far end had stopped the program.
	execute(t, s, `{"action":"start","name":"client","command":"trap 'echo signalled; exit 1' TERM; cat; echo ended by itself"}`)

	stopped := time.Now()
	execute(t, s, `{"action":"stop","name":"client"}`)
	if took := time.Since(stopped); took >= farGrace {
		t.Errorf("the stop took %s, past the far end's grace %s", took, farGrace)
	}
	if out := streams(t, s, "client"); !strings.Contains(out, "ended by itself") || strings.Contains(out, "signalled") {
		t.Errorf("the client did not end by itself on the hang-up:\n%s", out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a second exec was made although the hang-up landed")
	}
}

// A client still there when the far end's grace is up — the engine held the
// stream open — gets the second exec and then the kill, and the stop is
// bounded by the grace, the second exec's timeout and the kill. The session's
// end stops several such processes at once, not one after another.
func TestStop_AnAttachedClientThatStaysGetsTheSecondExec(t *testing.T) {
	s := newTestSupervisor(t, nil)
	dir := t.TempDir()
	deaf := `trap '' INT TERM HUP; exec 0</dev/null; while :; do sleep 0.1; done`

	marker := filepath.Join(dir, "second-exec")
	s.SetContainment(attachedWrap(marker))
	execute(t, s, `{"action":"start","name":"deaf","command":"`+deaf+`"}`)
	stopped := time.Now()
	execute(t, s, `{"action":"stop","name":"deaf"}`)
	took := time.Since(stopped)
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the second exec did not run: %v", err)
	}
	if took < farGrace {
		t.Errorf("the client was killed after %s, inside the far end's grace %s", took, farGrace)
	}
	if limit := farGrace + stopExecTimeout + waitDelay; took > limit {
		t.Errorf("the stop took %s, past its ceiling %s", took, limit)
	}
	if st := execute(t, s, `{"action":"status","name":"deaf"}`); !strings.Contains(st, "exited") {
		t.Errorf("the client is still running after the stop:\n%s", st)
	}

	for _, name := range []string{"one", "two", "three"} {
		s.SetContainment(attachedWrap(filepath.Join(dir, name)))
		execute(t, s, `{"action":"start","name":"`+name+`","command":"`+deaf+`"}`)
	}
	closed := time.Now()
	s.Close()
	if took := time.Since(closed); took > farGrace+stopExecTimeout {
		t.Errorf("the session's end took %s for three clients: they were stopped one after another", took)
	}
	for _, name := range []string{"one", "two", "three"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s's second exec did not run: %v", name, err)
		}
	}
}
