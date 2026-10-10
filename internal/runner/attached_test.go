//go:build !windows

package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/tools"
)

// farEndEnv makes this test binary a client whose far end takes a while to
// stop once its stdin hangs up, and which says whether it was signalled in
// the meantime (TestMain).
const farEndEnv = "SHHH_TEST_FAR_END"

func TestMain(m *testing.M) {
	if os.Getenv(farEndEnv) == "1" {
		os.Exit(farEnd())
	}
	os.Exit(m.Run())
}

// farEnd stands in for a client whose helper stops its group on the
// hang-up: it reads its stdin to the end, takes a second over the stop — the
// far end's grace — and exits by itself, saying whether a signal reached it
// first.
func farEnd() int {
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	fmt.Println("ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
	time.Sleep(time.Second)
	select {
	case sig := <-sigs:
		fmt.Println("signalled before it ended:", sig)
	default:
		fmt.Println("ended by itself")
	}
	return 0
}

// An attached command's stop starts by hanging its stdin up, and then
// leaves the client alone while the far end stops the command: an engine
// ends the exec's one process — the helper — when its client goes, so a
// client signalled at once takes the helper with it before the helper has
// stopped what the command started. A client that ends inside the far end's
// grace is a stop that arrived, and nothing more is done.
func TestAttachedStopWaitsForTheFarEnd(t *testing.T) {
	t.Setenv(farEndEnv, "1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var cancelled time.Time
	result := RunCaptureArgvTailAttached(ctx, "the command", []string{os.Args[0]}, nil, func(line string) {
		if line == "ready" {
			cancelled = time.Now()
			cancel()
		}
	})
	if !strings.Contains(result.Output, "ended by itself") {
		t.Fatalf("the client was signalled while its far end was stopping:\n%s", result.Output)
	}
	if result.Outcome != tools.ExecStopped {
		t.Errorf("outcome = %v, want stopped", result.Outcome)
	}
	if took := time.Since(cancelled); took >= farGrace {
		t.Errorf("the stop took %s, past the far end's grace %s", took, farGrace)
	}
}

// A client still there when the far end's grace is up gets the second exec
// and then the kill, and the whole of it is bounded: a cancel returns within
// the grace, the second exec's own timeout and the kill, however the client
// behaves.
func TestAttachedStopRunsTheSecondExecThenKills(t *testing.T) {
	marker := t.TempDir() + "/second-exec"
	stop := []string{"/bin/sh", "-c", "echo ran > " + marker}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	argv := []string{"/bin/sh", "-c", "trap '' INT TERM HUP; echo ready; while :; do sleep 0.1; done"}
	var cancelled time.Time
	result := RunCaptureArgvTailAttached(ctx, "deaf", argv, stop, func(line string) {
		if line == "ready" {
			cancelled = time.Now()
			cancel()
		}
	})
	took := time.Since(cancelled)
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the second exec did not run: %v", err)
	}
	if took < farGrace {
		t.Errorf("the client was killed after %s, inside the far end's grace %s", took, farGrace)
	}
	if limit := farGrace + stopExecTimeout + 2*time.Second; took > limit {
		t.Errorf("the stop took %s, past its ceiling %s", took, limit)
	}
	if result.Outcome != tools.ExecStopped {
		t.Errorf("outcome = %v, want stopped", result.Outcome)
	}
}

// A quit's drain gives an attached command the order a cancel gives it:
// the hang-up, the far end's grace, the second exec, the kill — never an
// interrupt to the client first, which would take the far end's helper with
// it. A client that ends inside the grace is left to end by itself; clients
// still there get their second execs at once rather than one after another,
// so the drain is bounded by one grace, one second exec's timeout and the
// kill however many there are.
func TestStopCapturedWaitsForTheFarEnd(t *testing.T) {
	t.Run("the far end ends", func(t *testing.T) {
		t.Setenv(farEndEnv, "1")
		ready := make(chan struct{})
		done := make(chan tools.ExecResult, 1)
		go func() {
			done <- RunCaptureArgvTailAttached(context.Background(), "the command", []string{os.Args[0]}, nil, func(line string) {
				if line == "ready" {
					close(ready)
				}
			})
		}()
		<-ready
		drained := time.Now()
		StopCaptured()
		if took := time.Since(drained); took >= farGrace {
			t.Errorf("the drain took %s, past the far end's grace %s", took, farGrace)
		}
		result := <-done
		if !strings.Contains(result.Output, "ended by itself") {
			t.Fatalf("the drain signalled the client while its far end was stopping:\n%s", result.Output)
		}
	})

	t.Run("the hang-up does not land", func(t *testing.T) {
		const clients = 3
		dir := t.TempDir()
		argv := []string{"/bin/sh", "-c", "trap '' INT TERM HUP; echo ready; while :; do sleep 0.1; done"}
		ready := make(chan struct{}, clients)
		done := make(chan tools.ExecResult, clients)
		for i := range clients {
			// Each second exec takes most of a second, so three run one
			// after another would show as a drain three seconds longer.
			stop := []string{"/bin/sh", "-c", fmt.Sprintf("sleep 1; echo ran > %s/second-exec-%d", dir, i)}
			go func() {
				done <- RunCaptureArgvTailAttached(context.Background(), "deaf", argv, stop, func(line string) {
					if line == "ready" {
						ready <- struct{}{}
					}
				})
			}()
		}
		for range clients {
			<-ready
		}
		drained := time.Now()
		StopCaptured()
		took := time.Since(drained)
		for i := range clients {
			if _, err := os.Stat(fmt.Sprintf("%s/second-exec-%d", dir, i)); err != nil {
				t.Errorf("client %d's second exec did not run: %v", i, err)
			}
		}
		if took < farGrace {
			t.Errorf("the clients were killed after %s, inside the far end's grace %s", took, farGrace)
		}
		if limit := farGrace + 2*time.Second; took > limit {
			t.Errorf("the drain took %s, past one grace and one second exec (%s): the clients were stopped one after another", took, limit)
		}
		for range clients {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("a drained client's capture did not return")
			}
		}
	})
}

// The attached forms report and capture exactly as the plain ones do, and a
// command that ends by itself ends with its own status: the held stdin is
// what the command does not read, not a reason for it to wait.
func TestAttachedTailReportsLines(t *testing.T) {
	var lines []string
	argv := []string{"/bin/sh", "-c", "echo one; sleep 0.1; echo two; sleep 0.1; echo three; exit 4"}
	result := RunCaptureArgvTailAttached(context.Background(), "three lines", argv, nil, func(line string) {
		lines = append(lines, line)
	})
	if strings.Join(lines, ",") != "one,two,three" {
		t.Errorf("lines = %q", lines)
	}
	if result.ExitCode != 4 || result.Outcome != tools.ExecExited || !strings.Contains(result.Output, "three") {
		t.Errorf("result = %+v", result)
	}

	done := make(chan tools.ExecResult, 1)
	go func() {
		done <- RunCaptureArgvInAttached(context.Background(), t.TempDir(), "pwd", []string{"/bin/sh", "-c", "echo in; exit 0"}, nil)
	}()
	select {
	case got := <-done:
		if got.ExitCode != 0 || !strings.Contains(got.Output, "in") {
			t.Errorf("result = %+v", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("an attached command that ended by itself did not return")
	}
}

// A plain command has the empty stdin it always had: a read finds the end at
// once, rather than waiting on a stream nobody writes to.
func TestPlainCaptureHasNoStdin(t *testing.T) {
	for name, run := range map[string]func() tools.ExecResult{
		"shell": func() tools.ExecResult { return RunCaptureResult(context.Background(), "cat; echo end-of-input") },
		"argv": func() tools.ExecResult {
			return RunCaptureArgvInResult(context.Background(), "", "cat", []string{"/bin/sh", "-c", "cat; echo end-of-input"})
		},
		"tail": func() tools.ExecResult {
			return RunCaptureArgvTailResult(context.Background(), "cat", []string{"/bin/sh", "-c", "cat; echo end-of-input"}, nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			done := make(chan tools.ExecResult, 1)
			go func() { done <- run() }()
			select {
			case got := <-done:
				if !strings.Contains(got.Output, "end-of-input") {
					t.Errorf("output = %q", got.Output)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("a plain command's read of its stdin blocked")
			}
		})
	}
}
