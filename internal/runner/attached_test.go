//go:build !windows

package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/tools"
)

// stdinAtInterruptEnv makes this test binary a command that says, when it is
// interrupted, whether its stdin had already hung up (TestMain).
const stdinAtInterruptEnv = "SHHH_TEST_STDIN_AT_INTERRUPT"

func TestMain(m *testing.M) {
	if os.Getenv(stdinAtInterruptEnv) == "1" {
		os.Exit(stdinAtInterrupt())
	}
	os.Exit(m.Run())
}

// stdinAtInterrupt waits for an interrupt and then reads its stdin without
// blocking: nothing to read and no writer left is a stdin that hung up before
// the signal arrived; a read that would block is one still held open.
func stdinAtInterrupt() int {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	fmt.Println("ready")
	<-sigs
	if err := syscall.SetNonblock(0, true); err != nil {
		fmt.Println("stdin:", err)
		return 2
	}
	n, err := syscall.Read(0, make([]byte, 1))
	switch {
	case n == 0 && err == nil:
		fmt.Println("stdin closed at the interrupt")
	case errors.Is(err, syscall.EAGAIN):
		fmt.Println("stdin open at the interrupt")
	default:
		fmt.Println("stdin:", n, err)
	}
	return 0
}

// An attached command's stdin is the stream its far end watches, and the
// stop starts there: by the time the interrupt reaches the command, the
// stdin has already hung up — so a helper in a container is stopping the
// work before the client it is attached to has even been signalled.
func TestAttachedCaptureClosesStdinBeforeTheSignal(t *testing.T) {
	t.Setenv(stdinAtInterruptEnv, "1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := RunCaptureArgvTailAttached(ctx, "the command", []string{os.Args[0]}, func(line string) {
		if line == "ready" {
			cancel()
		}
	})
	if !strings.Contains(result.Output, "stdin closed at the interrupt") {
		t.Fatalf("the signal reached the command before its stdin hung up:\n%s", result.Output)
	}
	if result.Outcome != tools.ExecStopped {
		t.Errorf("outcome = %v, want stopped", result.Outcome)
	}
}

// The attached forms report and capture exactly as the plain ones do, and a
// command that ends by itself ends with its own status: the held stdin is
// what the command does not read, not a reason for it to wait.
func TestAttachedTailReportsLines(t *testing.T) {
	var lines []string
	argv := []string{"/bin/sh", "-c", "echo one; sleep 0.1; echo two; sleep 0.1; echo three; exit 4"}
	result := RunCaptureArgvTailAttached(context.Background(), "three lines", argv, func(line string) {
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
		done <- RunCaptureArgvInAttached(context.Background(), t.TempDir(), "pwd", []string{"/bin/sh", "-c", "echo in; exit 0"})
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
