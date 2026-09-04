package runner

import (
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/tools"
)

// withSupervisor installs a process supervisor as the ceiling's adopter, the
// way a session does, and takes it away again.
func withSupervisor(t *testing.T) *process.Supervisor {
	t.Helper()
	t.Setenv("SHELL", "/bin/sh")
	sup, err := process.New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("process.New: %v", err)
	}
	SetAdopter(func(h Handover) (string, io.Writer, error) {
		return sup.Adopt(process.Adoption{Command: h.Command, PID: h.PID, Started: h.Started, Wait: h.Wait})
	})
	t.Cleanup(func() {
		SetAdopter(nil)
		sup.Close()
	})
	return sup
}

func needShell(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell")
	}
}

// The common mistake: a server started in the foreground. It is working and
// it will never return, so the ceiling moves it rather than killing it, and
// what comes back names the process so the model's existing verbs apply.
func TestACommandStillPrintingAtItsCeilingIsMovedNotKilled(t *testing.T) {
	needShell(t)
	sup := withSupervisor(t)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	out, code := RunCapture(ctx, "echo listening; sleep 1; echo later; sleep 30")

	if !strings.Contains(out, "listening") {
		t.Errorf("what it printed before the ceiling has to come back: %q", out)
	}
	if !strings.Contains(out, "moved to the background") || !strings.Contains(out, `"echo"`) {
		t.Errorf("the result has to name the process it became: %q", out)
	}
	if !strings.Contains(out, "has not finished") {
		t.Errorf("a backgrounded command has neither failed nor finished, and must say so: %q", out)
	}
	if code != 0 {
		t.Errorf("a command that was moved did not fail, and an exit code that says it did sends the model debugging: %d", code)
	}

	// It is still running, and what it prints after the handover is captured
	// where a read will find it.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(read(t, sup, "echo"), "later") {
		if time.Now().After(deadline) {
			t.Fatalf("output after the handover was lost: %q", read(t, sup, "echo"))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if stopped := execute(t, sup, `{"action":"stop","name":"echo"}`); !strings.Contains(stopped, "exited") {
		t.Errorf("stop should end it: %q", stopped)
	}
}

// A command that printed nothing has nothing to hand over and nothing that
// would ever explain it, so it is stopped exactly as it was before.
func TestASilentCommandAtItsCeilingIsStopped(t *testing.T) {
	needShell(t)
	sup := withSupervisor(t)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	out, code := RunCapture(ctx, "sleep 30")

	if !strings.Contains(out, "did not finish") {
		t.Errorf("the notice must distinguish stopped from failed: %q", out)
	}
	if !strings.Contains(out, "command_timeout_seconds") {
		t.Errorf("the notice should name the way out: %q", out)
	}
	if code == 0 {
		t.Error("a command that was killed did not exit cleanly")
	}
	if list := sup.List(); !strings.Contains(list, "No processes") {
		t.Errorf("nothing should have been kept: %q", list)
	}
}

// With nowhere to put one — every session before the supervisor is open, and
// every surface that has none — the ceiling behaves as it always did.
func TestWithNoAdopterTheCeilingStillStops(t *testing.T) {
	needShell(t)
	t.Setenv("SHELL", "/bin/sh")
	SetAdopter(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	out, code := RunCapture(ctx, "echo started; sleep 30")
	if !strings.Contains(out, "started") || !strings.Contains(out, "did not finish") {
		t.Errorf("got %q", out)
	}
	if code == 0 {
		t.Error("a command that was killed did not exit cleanly")
	}
}

// A reader's cancel is not the ceiling, and must not be reported as one.
func TestACancelledCommandIsNotToldItHitALimit(t *testing.T) {
	needShell(t)
	withSupervisor(t)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	out, _ := RunCapture(ctx, "echo running; sleep 30")
	if strings.Contains(out, "time limit") || strings.Contains(out, "moved to the background") {
		t.Errorf("a cancellation is neither a ceiling nor a handover: %q", out)
	}
}

// One command's output cannot take the turn's memory with it: the burst is
// bounded as it arrives, far above anything a reader or a model would see.
func TestAChattyCommandIsBoundedWhileItRuns(t *testing.T) {
	needShell(t)
	t.Setenv("SHELL", "/bin/sh")
	if _, err := exec.LookPath("tr"); err != nil {
		t.Skip("no tr")
	}
	SetAdopter(nil)

	out, code := RunCapture(context.Background(), "head -c 4194304 /dev/zero | tr '\\0' 'x'")
	if code != 0 {
		t.Fatalf("the command itself should succeed: %d %q", code, out[:min(len(out), 200)])
	}
	if len(out) > tools.MaxCapturedOutputBytes+1024 {
		t.Errorf("held %d bytes, want the bound %d", len(out), tools.MaxCapturedOutputBytes)
	}
	if !strings.Contains(out, "dropped") {
		t.Error("the output has to say bytes went missing, or the gap reads as silence")
	}
}

func read(t *testing.T, sup *process.Supervisor, name string) string {
	t.Helper()
	return execute(t, sup, `{"action":"read","name":"`+name+`"}`)
}

func execute(t *testing.T, sup *process.Supervisor, args string) string {
	t.Helper()
	out, err := sup.Execute(json.RawMessage(args))
	if err != nil {
		t.Fatalf("process %s: %v", args, err)
	}
	return out
}
