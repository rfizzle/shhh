package runner

// What happens to a captured command when its ceiling arrives.
//
// The ceiling exists because a command that will not finish holds whatever
// was waiting on it — a headless run, a sub-agent, a turn. Killing it is the
// right answer for the command that was never going to print anything again:
// a prompt nobody will answer, a network read with no timeout of its own.
// It is the wrong answer for the mistake that is far more common, which is a
// dev server or a watcher started in the foreground. That command is working;
// it is only never going to return, and killing it throws away a running
// server and teaches the model nothing it can act on.
//
// So the ceiling is a choice and not a kill, and it is made here because this
// is the only code holding the running command. A command that has printed
// something is offered to whoever can keep it running; one that has printed
// nothing is stopped exactly as it was before. Either way the reason is
// appended to the output in words, because output and an exit code cannot
// tell a command that was stopped from one that broke, and a model given only
// those debugs a command that was working fine.
// See docs/capabilities/containment.md#a-command-that-will-not-finish-is-not-waited-on-forever.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/tools"
)

// Handover is a command that is still running, offered to whatever can keep
// it running. Taking it transfers ownership: the taker decides when it stops,
// and nothing here signals it again.
type Handover struct {
	// Command is what was asked to run, for the surfaces that list it. It is
	// the text the model wrote and not the argv that was spawned, which for a
	// contained command is mostly the mechanism's own flags.
	Command string
	// PID leads the command's process group, which is what a stop has to
	// signal: the shell holding it is not the work.
	PID int
	// Started is when it was spawned, so an uptime counts from the command
	// and not from the moment it changed hands.
	Started time.Time
	// Wait blocks until the command exits and reports how, exactly once. The
	// run already has a wait in flight — os/exec allows only one — so the
	// taker must use this rather than waiting on the command itself.
	Wait func() error
}

// AdoptFunc takes a handover and returns the name the command is known by
// from now on, plus the writer its remaining output belongs in. An error
// declines it, and a declined command is stopped as if nothing had offered.
type AdoptFunc func(Handover) (name string, output io.Writer, err error)

// The adopter is package state for the reason the session environment above
// is: a session runs commands through half a dozen paths — plain, tailed,
// contained, in a sub-agent's worktree — and a ceiling that backgrounded a
// command on some of them and killed it on the others would be a dev server
// that survives or dies depending on whether containment happened to be
// available on the machine.
var (
	adopterMu sync.RWMutex
	adopter   AdoptFunc
)

// SetAdopter installs what a command still printing at its ceiling is handed
// to. nil is a session with nowhere to put one, which is the default and
// stops such a command as it always did.
func SetAdopter(fn AdoptFunc) {
	adopterMu.Lock()
	adopter = fn
	adopterMu.Unlock()
}

func currentAdopter() AdoptFunc {
	adopterMu.RLock()
	defer adopterMu.RUnlock()
	return adopter
}

// captureWriter is where a captured command's combined output goes: bounded
// in memory, reported line by line for a live tail, and redirectable in one
// step when the command changes hands.
type captureWriter struct {
	mu     sync.Mutex
	buf    *tools.CaptureBuffer
	line   []byte
	onLine func(string)
	// mirror is set when the command has been handed on. From then on the
	// output belongs to whoever took it, and neither the buffer nor the tail
	// is fed again — the row that was showing it is closed.
	mirror io.Writer
}

func newCaptureWriter(onLine func(string)) *captureWriter {
	return &captureWriter{buf: tools.NewCaptureBuffer(tools.MaxCapturedOutputBytes), onLine: onLine}
}

func (w *captureWriter) Write(p []byte) (int, error) {
	wrote := len(p)
	w.mu.Lock()
	if w.mirror != nil {
		mirror := w.mirror
		w.mu.Unlock()
		// The taker's own bound applies from here; a short write there is
		// still not this command's problem.
		_, _ = mirror.Write(p)
		return wrote, nil
	}
	_, _ = w.buf.Write(p)
	onLine := w.onLine
	if onLine == nil {
		// Nobody is showing a live tail, so nothing here has to find the
		// line breaks — and holding a partial line for a command printing
		// megabytes without one would put back the memory the buffer above
		// is there to bound.
		w.mu.Unlock()
		return wrote, nil
	}
	var lines []string
	rest := p
	for {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			w.line = appendBounded(w.line, rest)
			break
		}
		lines = append(lines, string(appendBounded(w.line, rest[:i])))
		w.line = w.line[:0]
		rest = rest[i+1:]
	}
	w.mu.Unlock()
	for _, l := range lines {
		onLine(l)
	}
	return wrote, nil
}

// maxTailLine bounds the line held for the live tail. The tail is one row on
// a terminal, so nothing past this was going to be drawn — and a command that
// prints a megabyte without a newline would otherwise hold all of it here,
// beside the buffer that exists to stop exactly that.
const maxTailLine = 4096

func appendBounded(line, p []byte) []byte {
	if room := maxTailLine - len(line); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		return append(line, p...)
	}
	return line
}

func (w *captureWriter) output() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// printed reports whether the command has produced any output at all. It is
// the whole test for whether a command at its ceiling is doing something: a
// command that has printed nothing in ten minutes has nothing to hand over
// and nothing to read afterwards, and keeping it would leave a process on the
// machine that no output will ever explain.
func (w *captureWriter) printed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Len() > 0
}

// handOff seeds dst with everything printed so far and sends everything after
// it there too, under one lock so that no byte is written twice and none is
// lost between the two. It returns what it seeded, which is what the result
// still has to show.
func (w *captureWriter) handOff(dst io.Writer) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.buf.String()
	_, _ = dst.Write(w.buf.Bytes())
	w.mirror = dst
	w.onLine = nil
	return out
}

// capture runs one invocation with its combined output captured and its
// ceiling — the deadline on ctx, when there is one — answered here.
//
// The command's own context carries no deadline of its own: reaching the
// ceiling is a decision this function makes, and os/exec would otherwise have
// killed the command before there was anything to decide. The caller's
// cancellation still travels, through the watch below, and stops the command
// the way it always did.
func capture(ctx context.Context, dir, command string, argv []string, onLine func(string)) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("empty command")
	}
	inner, cancel := context.WithCancel(context.WithoutCancel(ctx))
	cmd := prepare(exec.CommandContext(inner, argv[0], argv[1:]...), dir)
	w := newCaptureWriter(onLine)
	cmd.Stdout, cmd.Stderr = w, w
	started := time.Now()
	if err := cmd.Start(); err != nil {
		cancel()
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	watch := ctx.Done()
	for {
		select {
		case err := <-done:
			cancel()
			return w.output(), err
		case <-watch:
			// Done fires once; a nil channel parks this arm for the wait that
			// follows rather than spinning on a context that stays done.
			watch = nil
			if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				// A reader's cancel, or a caller giving up: stopped, and not
				// at any limit, so nothing is appended and nothing is kept.
				cancel()
				continue
			}
			// A command that finished in the instant the ceiling arrived is
			// finished, not late: select picks either arm when both are
			// ready, and the pid of a command already waited on is a number
			// the machine is free to hand to something else. Ask once,
			// without blocking, before anything is decided about it.
			select {
			case err := <-done:
				cancel()
				return w.output(), err
			default:
			}
			limit := ceilingOf(ctx, started)
			if name, out, ok := handOver(w, cmd, command, started, done, cancel); ok {
				return appendNotice(out, backgroundedNotice(name, limit)), nil
			}
			cancel()
			return stoppedOutput(w.output(), limit), <-done
		}
	}
}

// handOver offers a command that is still printing to the session's adopter.
// It reports false when there is nobody to take it, when it has printed
// nothing, or when the taker declines — each of which leaves the command to
// be stopped.
func handOver(w *captureWriter, cmd *exec.Cmd, command string, started time.Time, done chan error, cancel context.CancelFunc) (string, string, bool) {
	adopt := currentAdopter()
	if adopt == nil || !w.printed() || cmd.Process == nil {
		return "", "", false
	}
	name, sink, err := adopt(Handover{
		Command: command,
		PID:     cmd.Process.Pid,
		Started: started,
		// The run's own wait is already in flight and os/exec allows only
		// one, so the taker is handed that one. Releasing the command's
		// context afterwards is what keeps a handed-on command from holding
		// it for the life of the process.
		Wait: func() error { err := <-done; cancel(); return err },
	})
	if err != nil || sink == nil {
		return "", "", false
	}
	return name, w.handOff(sink), true
}

// stoppedOutput is what a killed command comes back as: what it printed, and
// the reason, because the exit code cannot carry one.
func stoppedOutput(out string, limit time.Duration) string {
	return appendNotice(out, fmt.Sprintf(
		"… command stopped after %s: it reached the time limit for one command and was cancelled, along with everything it had started. "+
			"It did not fail — it did not finish. Run it in a way that completes (narrow the work, or start it as a background process), "+
			"or raise behavior.command_timeout_seconds.",
		limit))
}

// backgroundedNotice names the process the command became, so that the verbs
// the model already has for a process — status, read, input, stop — apply to
// the thing it accidentally started in the foreground.
func backgroundedNotice(name string, limit time.Duration) string {
	return fmt.Sprintf(
		"… command reached the %s time limit and was still running, so it was moved to the background instead of being stopped: "+
			"it is the process %q from now on, and everything it prints is captured there. It did not fail and it has not finished. "+
			"Read it with the process tool (status, read), end it with stop, and expect the session's end to stop it either way. "+
			"Raise behavior.command_timeout_seconds to give a command like this longer in the foreground.",
		limit, name)
}

// ceilingOf is the limit the ceiling was set at, rounded to the second: the
// deadline the caller put on the context, measured from the spawn rather than
// from now, so the notice says the number the setting holds and not however
// long it took to notice.
//
// The notices below render it with the standard library's own duration format
// rather than the shared wall-clock one the rail and the status rows use. That
// one belongs to the screen, and this is prose a model reads; reaching up into
// the interface layer from the code that runs commands would be a worse trade
// than two audiences having their own rendering of "ten minutes".
func ceilingOf(ctx context.Context, started time.Time) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return time.Since(started).Round(time.Second)
	}
	return deadline.Sub(started).Round(time.Second)
}

func appendNotice(out, notice string) string {
	if trimmed := strings.TrimRight(out, "\r\n"); trimmed != "" {
		return trimmed + "\n" + notice
	}
	return notice
}
