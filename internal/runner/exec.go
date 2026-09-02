package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/rfizzle/shhh/internal/history"
	"github.com/rfizzle/shhh/internal/shell"
)

// shellCommand is a command line as an *exec.Cmd through the execution shell
// (shell.Execution). Every captured form goes through it so that none of them
// can end up spelling the invocation itself (shell.Shell).
//
// Captured means shhh composed it and shhh reads the output back: the agent's
// execute_command, /run, a sub-agent's command. That is the whole of the
// execution shell's case, and the reason Run below is the one function here
// that does not use it.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	argv := shell.Execution().Argv(command)
	return exec.CommandContext(ctx, argv[0], argv[1:]...)
}

// sessionEnv is what every captured command's environment carries beyond
// the process's own: the session's secrets, as NAME=value. It is
// package state because the session runs commands through half a dozen
// paths — plain, tailed, contained, in a sub-agent's worktree — and a value
// present in some of them is a bug that surfaces as "the variable is unset"
// only in the one that mattered.
// See docs/capabilities/secrets.md#a-secret-is-an-environment-variable.
var (
	sessionMu  sync.RWMutex
	sessionEnv []string
)

// SetSessionEnv replaces the NAME=value pairs every captured command gets
// on top of the inherited environment. Later pairs win over earlier ones
// and over the inherited value of the same name.
func SetSessionEnv(env []string) {
	sessionMu.Lock()
	sessionEnv = append([]string(nil), env...)
	sessionMu.Unlock()
}

// Environ is the inherited environment plus the session's pairs, or nil
// when there are none, which leaves exec.Cmd to inherit exactly as before.
func Environ() []string {
	sessionMu.RLock()
	defer sessionMu.RUnlock()
	if len(sessionEnv) == 0 {
		return nil
	}
	return append(os.Environ(), sessionEnv...)
}

// Run executes a command with the terminal inherited, and it is the one
// runner that stays on the user's own shell (shell.Current): its only caller
// is `shhh cmd`, whose output is a command written for that shell, run in
// front of the user and appended to that shell's history below.
func Run(command string) (exitCode int) {
	sh := shell.Current()
	argv := sh.Argv(command)

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		for sig := range sigCh {
			if cmd.Process != nil {
				// A process that has already exited cannot be signalled, and
				// that is the one failure this can have.
				_ = cmd.Process.Signal(sig)
			}
		}
	}()

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 1
	}

	// Best effort: the shell's history file is a convenience, and a command
	// that ran is not made to fail by a history file that could not be
	// written.
	go func() { _ = history.Append(sh.Name, command) }()

	return 0
}

// RunCapture executes command through the user's shell with stdout and stderr
// captured instead of inherited, for callers that display the output
// themselves (e.g. the chat TUI). Cancelling the context kills the command;
// a non-exit failure (including a kill) reports exit code -1.
func RunCapture(ctx context.Context, command string) (output string, exitCode int) {
	cmd := prepare(shellCommand(ctx, command), "")
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
// so a caller can render a live tail while the command runs.
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
// as it appears, so callers can show a live tail. onLine runs on the
// command's output goroutines and must be safe to call concurrently.
func RunCaptureTail(ctx context.Context, command string, onLine func(string)) (string, int) {
	return runTail(prepare(shellCommand(ctx, command), ""), onLine)
}

// RunCaptureArgvTail is RunCaptureArgv with the same live-line reporting, for
// pre-built invocations like contained commands.
func RunCaptureArgvTail(ctx context.Context, argv []string, onLine func(string)) (string, int) {
	if len(argv) == 0 {
		return "error: empty command", -1
	}
	return runTail(prepare(exec.CommandContext(ctx, argv[0], argv[1:]...), ""), onLine)
}

// RunCaptureArgv executes an explicit argv (no shell) with output captured,
// for pre-built invocations like sandbox-wrapped commands. A spawn
// failure — e.g. the containment binary vanished — reports the error in the
// output with exit code -1, so the command fails visibly instead of running
// bare.
// RunCaptureIn is RunCapture with an explicit working directory, for
// sub-agent commands that must run inside their own workspace.
func RunCaptureIn(ctx context.Context, dir, command string) (output string, exitCode int) {
	cmd := prepare(shellCommand(ctx, command), dir)
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
// mechanism does not chdir itself.
func RunCaptureArgvIn(ctx context.Context, dir string, argv []string) (output string, exitCode int) {
	if len(argv) == 0 {
		return "error: empty command", -1
	}
	cmd := prepare(exec.CommandContext(ctx, argv[0], argv[1:]...), dir)
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
