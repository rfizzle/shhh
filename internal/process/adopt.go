package process

// Taking over a command that is already running.
//
// The other way into this supervisor is a start: the model names a process
// and the supervisor spawns it. This is the way in for the mistake — a dev
// server, a watcher, a `tail -f` run as an ordinary command, which is working
// but will never return. Killing it at the command ceiling throws away a
// running server; restarting it here would be a second spawn, and a port
// already bound or a build already half done makes that a different command.
// So the running one is taken as it is, and the only thing that changes is
// who is holding it.
// See docs/capabilities/containment.md#a-command-that-will-not-finish-is-not-waited-on-forever.

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
)

// Adoption is a command that is already running, offered to the supervisor.
// The offer is described in plain values rather than an *exec.Cmd so that
// this package still knows nothing about how the session runs a command —
// through a shell, through a containment mechanism, in a sub-agent's
// worktree — and can take one from any of them.
type Adoption struct {
	// Command is what was asked to run, which is what the process list and
	// the status block show.
	Command string
	// PID leads the command's process group. A stop signals the group, not
	// this process: the shell holding the work is not the work.
	PID int
	// Started is when it was spawned, so its uptime counts from the command
	// and not from the moment it changed hands.
	Started time.Time
	// Wait blocks until it exits and reports how, exactly once. It is the
	// wait the caller already has in flight, because os/exec allows only one
	// and the supervisor cannot open a second on a command it did not spawn.
	Wait func() error
}

// Adopt takes over a running command under a generated name and returns that
// name and the writer its output belongs in from now on. The caller keeps
// writing there; the supervisor pages it, stores it and stops the process
// like any other.
//
// It refuses rather than improvises: a full supervisor, a shut-down session
// or an offer with no wait behind it all come back as an error, and the
// caller's own answer to that — stopping the command — is the one it would
// have given had nothing been listening at all.
func (s *Supervisor) Adopt(a Adoption) (string, io.Writer, error) {
	if a.Wait == nil {
		return "", nil, fmt.Errorf("a command can only be adopted with the wait its caller holds")
	}
	if a.PID <= 0 {
		return "", nil, fmt.Errorf("a command can only be adopted while it is running")
	}
	started := a.Started
	if started.IsZero() {
		started = time.Now()
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", nil, fmt.Errorf("the process supervisor is shut down")
	}
	if len(s.procs) >= MaxProcesses {
		s.mu.Unlock()
		return "", nil, fmt.Errorf("too many processes (max %d)", MaxProcesses)
	}
	name := s.freeName(processName(a.Command))
	p := &proc{
		name:    name,
		command: a.Command,
		pid:     a.PID,
		started: started,
		// A command that was running in the foreground had no input to
		// write to, and giving it one now would mean a pipe nothing is on
		// the other end of. The input action says so by name instead.
		stdin:  nil,
		stdout: newStreamBuf(s.ringBytes, s.spoolBytes, s.scrub),
		// Everything a captured command printed arrived on one stream, its
		// two having been combined before this supervisor ever saw it, so
		// stderr stays empty rather than claiming a split that was lost.
		stderr:   newStreamBuf(s.ringBytes, s.spoolBytes, s.scrub),
		exited:   make(chan struct{}),
		evidence: map[string]string{},
	}
	s.procs[name] = p
	s.mu.Unlock()

	go s.reap(p, a.Wait)
	return name, p.stdout, nil
}

// freeName returns base, or base with a number after it, whichever is not
// taken. The caller holds the lock.
func (s *Supervisor) freeName(base string) string {
	if _, taken := s.procs[base]; !taken {
		return base
	}
	for n := 2; ; n++ {
		name := fmt.Sprintf("%s-%d", base, n)
		if _, taken := s.procs[name]; !taken {
			return name
		}
	}
}

// processName is the name a command is known by once it is a process: the
// program it runs, which is the word the reader would have used for it
// anyway. Everything the name rule does not allow is dropped rather than
// replaced, so `./scripts/dev.sh` becomes `dev.sh` and not `--scripts-dev-sh`.
func processName(command string) string {
	field := strings.TrimSpace(command)
	if i := strings.IndexAny(field, " \t\n"); i >= 0 {
		field = field[:i]
	}
	field = filepath.Base(field)
	var b strings.Builder
	for _, r := range field {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	name := strings.Trim(b.String(), ".-_")
	if len(name) > 32 {
		name = name[:32]
	}
	if !nameRe.MatchString(name) {
		return "command"
	}
	return name
}
