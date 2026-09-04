package runner

import (
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

// sessionEnv is what every captured command's environment carries beyond
// the process's own: the session's secrets, as NAME=value. It is
// package state because the session runs commands through half a dozen
// paths — plain, tailed, contained, in a sub-agent's worktree — and a value
// present in some of them is a bug that surfaces as "the variable is unset"
// only in the one that mattered.
// See docs/capabilities/secrets.md#a-secret-is-an-environment-variable.
var (
	sessionMu   sync.RWMutex
	sessionEnv  []string
	sessionMask func(name string) bool
)

// SetSessionEnv replaces the NAME=value pairs every captured command gets
// on top of the inherited environment. Later pairs win over earlier ones
// and over the inherited value of the same name.
func SetSessionEnv(env []string) {
	sessionMu.Lock()
	sessionEnv = append([]string(nil), env...)
	sessionMu.Unlock()
}

// SetEnvMask installs the test an inherited variable's name is put to
// before a captured command sees it: true drops it. nil inherits
// everything, which is a session that turned the mask off.
//
// It is a predicate rather than a list of names or a package that knows
// what a credential looks like, for the same reason the scrub is a
// function: this package runs commands and has no business holding the
// vocabulary of secrets. The session's own pairs are appended after the
// mask has run, so a variable the user declared as a secret reaches the
// command even though its name is one the mask would have dropped.
// See docs/capabilities/secrets.md#the-names-that-do-not-travel.
func SetEnvMask(mask func(name string) bool) {
	sessionMu.Lock()
	sessionMask = mask
	sessionMu.Unlock()
}

// SessionEnvNames names the session's own pairs, in the order SetSessionEnv
// was given them. It is what the containment allowlist asks for: a variable
// the person declared as a secret crosses into a contained command because
// they declared it, and this is the list of what they declared; nothing
// reads the shape of a name.
// See docs/capabilities/containment.md#a-contained-command-carries-almost-no-environment.
func SessionEnvNames() []string {
	sessionMu.RLock()
	defer sessionMu.RUnlock()
	names := make([]string, 0, len(sessionEnv))
	for _, pair := range sessionEnv {
		if name, _, ok := strings.Cut(pair, "="); ok {
			names = append(names, name)
		}
	}
	return names
}

// Environ is the inherited environment, masked, plus the session's pairs —
// or nil when there is neither a mask nor a pair, which leaves exec.Cmd to
// inherit exactly as before.
func Environ() []string {
	sessionMu.RLock()
	env, mask := sessionEnv, sessionMask
	sessionMu.RUnlock()
	if len(env) == 0 && mask == nil {
		return nil
	}
	// os.Environ allocates a fresh slice on every call, so filtering it in
	// place cannot reach anything but this command's own copy.
	inherited := os.Environ()
	if mask != nil {
		kept := inherited[:0]
		for _, pair := range inherited {
			if name, _, ok := strings.Cut(pair, "="); ok && mask(name) {
				continue
			}
			kept = append(kept, pair)
		}
		inherited = kept
	}
	return append(inherited, env...)
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

// RunCapture executes command through the execution shell with stdout and
// stderr captured instead of inherited, for callers that display the output
// themselves (e.g. the chat TUI). Cancelling the context kills the command;
// a non-exit failure (including a kill) reports exit code -1.
func RunCapture(ctx context.Context, command string) (output string, exitCode int) {
	out, err := capture(ctx, "", command, shellArgv(command), nil)
	return noteSpawnFailure(out, err), resultCode(err)
}

// RunCaptureTail is RunCapture reporting each completed output line to onLine
// as it appears, so callers can show a live tail. onLine runs on the
// command's output goroutine and must be safe to call concurrently.
func RunCaptureTail(ctx context.Context, command string, onLine func(string)) (string, int) {
	out, err := capture(ctx, "", command, shellArgv(command), onLine)
	return noteSpawnFailure(out, err), resultCode(err)
}

// RunCaptureArgvTail is RunCaptureArgv with the same live-line reporting, for
// pre-built invocations like contained commands. command is the text the argv
// was built from: it is what a surface listing this command shows, and a
// mechanism's own flags are not that.
func RunCaptureArgvTail(ctx context.Context, command string, argv []string, onLine func(string)) (string, int) {
	out, err := capture(ctx, "", command, argv, onLine)
	return noteSpawnFailure(out, err), resultCode(err)
}

// RunCaptureArgv executes an explicit argv (no shell) with output captured,
// for pre-built invocations like sandbox-wrapped commands. A spawn
// failure — e.g. the containment binary vanished — reports the error in the
// output with exit code -1, so the command fails visibly instead of running
// bare.
// RunCaptureIn is RunCapture with an explicit working directory, for
// sub-agent commands that must run inside their own workspace.
func RunCaptureIn(ctx context.Context, dir, command string) (output string, exitCode int) {
	out, err := capture(ctx, dir, command, shellArgv(command), nil)
	return noteSpawnFailure(out, err), resultCode(err)
}

// RunCaptureArgv executes an explicit argv (no shell) with output captured,
// for pre-built invocations like sandbox-wrapped commands. A spawn failure —
// the containment binary vanished, say — reports the error in the output with
// exit code -1, so the command fails visibly instead of running bare.
// command is the text the argv was built from, for the surfaces that list it.
func RunCaptureArgv(ctx context.Context, command string, argv []string) (output string, exitCode int) {
	return RunCaptureArgvIn(ctx, "", command, argv)
}

// RunCaptureArgvIn is RunCaptureArgv with an explicit working directory
// (empty keeps the process cwd), for sandbox-wrapped sub-agent commands whose
// mechanism does not chdir itself.
func RunCaptureArgvIn(ctx context.Context, dir, command string, argv []string) (output string, exitCode int) {
	out, err := capture(ctx, dir, command, argv, nil)
	return noteSpawnFailure(out, err), resultCode(err)
}

// shellArgv is a command line as the execution shell's argv (shell.Execution).
// Every captured form goes through it so that none of them can end up
// spelling the invocation itself (shell.Shell).
//
// Captured means shhh composed it and shhh reads the output back: the agent's
// execute_command, /run, a sub-agent's command. That is the whole of the
// execution shell's case, and the reason Run above is the one function here
// that does not use it.
func shellArgv(command string) []string { return shell.Execution().Argv(command) }

// resultCode is the exit code a captured command reports: its own where it
// ran and exited, and -1 for everything else — a command that could not be
// spawned, one killed by a signal, one whose ceiling stopped it.
func resultCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// noteSpawnFailure puts a failure that is not the command's own exit into the
// output, where the callers that read only text can see it. A command that
// exited — including one a signal ended — says everything it has to say in
// its output and its code.
func noteSpawnFailure(out string, err error) string {
	var exitErr *exec.ExitError
	if err == nil || errors.As(err, &exitErr) {
		return out
	}
	return strings.TrimSpace(out + "\nerror: " + err.Error())
}
