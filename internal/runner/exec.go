package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/rfizzle/shhh/internal/history"
)

func Run(command string) (exitCode int) {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}

	cmd := exec.Command(filepath.Clean(sh), "-c", command)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		for sig := range sigCh {
			if cmd.Process != nil {
				cmd.Process.Signal(sig)
			}
		}
	}()

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 1
	}

	go history.Append(filepath.Base(sh), command)

	return 0
}

// RunCapture executes command through the user's shell with stdout and stderr
// captured instead of inherited, for callers that display the output
// themselves (e.g. the chat TUI). Cancelling the context kills the command;
// a non-exit failure (including a kill) reports exit code -1.
func RunCapture(ctx context.Context, command string) (output string, exitCode int) {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}

	cmd := exec.CommandContext(ctx, filepath.Clean(sh), "-c", command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return string(out), exitErr.ExitCode()
		}
		return string(out), -1
	}
	return string(out), 0
}

// lineWriter captures combined output while reporting each completed line,
// so a caller can render a live tail while the command runs (S-075).
type lineWriter struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	partial bytes.Buffer
	onLine  func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.mu.Lock()
	w.buf.Write(p)
	var lines []string
	for {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			w.partial.Write(p)
			break
		}
		w.partial.Write(p[:i])
		lines = append(lines, w.partial.String())
		w.partial.Reset()
		p = p[i+1:]
	}
	w.mu.Unlock()
	if w.onLine != nil {
		for _, l := range lines {
			w.onLine(l)
		}
	}
	return n, nil
}

func (w *lineWriter) output() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// runTail runs a prepared command with its combined output both captured and
// reported line-by-line to onLine.
func runTail(cmd *exec.Cmd, onLine func(string)) (string, int) {
	w := &lineWriter{onLine: onLine}
	cmd.Stdout = w
	cmd.Stderr = w
	err := cmd.Run()
	out := w.output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return out, exitErr.ExitCode()
		}
		return out, -1
	}
	return out, 0
}

// RunCaptureTail is RunCapture reporting each completed output line to onLine
// as it appears, so callers can show a live tail (S-075). onLine runs on the
// command's output goroutines and must be safe to call concurrently.
func RunCaptureTail(ctx context.Context, command string, onLine func(string)) (string, int) {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	cmd := exec.CommandContext(ctx, filepath.Clean(sh), "-c", command)
	return runTail(cmd, onLine)
}

// RunCaptureArgvTail is RunCaptureArgv with the same live-line reporting, for
// pre-built invocations like contained commands (S-062).
func RunCaptureArgvTail(ctx context.Context, argv []string, onLine func(string)) (string, int) {
	if len(argv) == 0 {
		return "error: empty command", -1
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	return runTail(cmd, onLine)
}

// RunCaptureArgv executes an explicit argv (no shell) with output captured,
// for pre-built invocations like sandbox-wrapped commands (S-062). A spawn
// failure — e.g. the containment binary vanished — reports the error in the
// output with exit code -1, so the command fails visibly instead of running
// bare.
// RunCaptureIn is RunCapture with an explicit working directory, for
// sub-agent commands that must run inside their own workspace (S-068).
func RunCaptureIn(ctx context.Context, dir, command string) (output string, exitCode int) {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}

	cmd := exec.CommandContext(ctx, filepath.Clean(sh), "-c", command)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return string(out), exitErr.ExitCode()
		}
		return strings.TrimSpace(string(out) + "\nerror: " + err.Error()), -1
	}
	return string(out), 0
}

func RunCaptureArgv(ctx context.Context, argv []string) (output string, exitCode int) {
	return RunCaptureArgvIn(ctx, "", argv)
}

// RunCaptureArgvIn is RunCaptureArgv with an explicit working directory
// (empty keeps the process cwd), for sandbox-wrapped sub-agent commands whose
// mechanism does not chdir itself (S-068).
func RunCaptureArgvIn(ctx context.Context, dir string, argv []string) (output string, exitCode int) {
	if len(argv) == 0 {
		return "error: empty command", -1
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return string(out), exitErr.ExitCode()
		}
		return strings.TrimSpace(string(out) + "\nerror: " + err.Error()), -1
	}
	return string(out), 0
}
