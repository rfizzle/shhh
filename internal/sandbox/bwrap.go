package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// detectBwrap looks for bubblewrap and verifies it can actually create a
// sandbox here — unprivileged user namespaces are disabled on some kernels,
// and a bwrap that cannot run must be reported unavailable, not assumed.
func detectBwrap() Availability {
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return Availability{Mechanism: "bwrap", Detail: "bubblewrap (bwrap) not found on PATH"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--unshare-user", "--ro-bind", "/", "/", "true").CombinedOutput()
	if err != nil {
		return Availability{
			Mechanism: "bwrap",
			Detail:    fmt.Sprintf("bwrap probe failed (unprivileged user namespaces unavailable?): %v: %s", err, probeLine(out)),
		}
	}
	return Availability{Mechanism: "bwrap", OK: true, Detail: "bubblewrap with unprivileged user namespaces"}
}

// bwrapPrefix builds the bubblewrap invocation up to the contained command:
// the whole filesystem read-only, write grants bound over it, and the deny
// masks mounted last so they outrank every grant — masked directories read as
// empty tmpfs, masked files as /dev/null. Stdio is inherited and the exit
// code passes through.
//
// The namespaces and the environment are the half that needs nothing
// configured. A contained command used to see every process on the machine,
// share its IPC and its hostname, and inherit the whole environment it was
// launched from; the mask took the user's keys off the filesystem and left
// the agent socket that signs with them in a variable. See
// docs/capabilities/containment.md#a-contained-command-carries-almost-no-environment.
//
// The environment is rebuilt here rather than left to the caller because the
// caller is not always the same one: the quality gate and the process
// supervisor both wrap through this package with an environment they set
// themselves, and a wrap that inherited whatever it was handed would be
// contained on one path and open on the next. The cost is that a value on
// the allowlist is in this argv, where the process table can read it, which
// is the other reason the allowlist is as short as it is.
func bwrapPrefix(s spec) []string {
	argv := []string{"bwrap", "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc"}
	for _, w := range s.write {
		argv = append(argv, "--bind", w, w)
	}
	for _, d := range s.denyDirs {
		argv = append(argv, "--tmpfs", d)
	}
	for _, f := range s.denyFiles {
		argv = append(argv, "--ro-bind", "/dev/null", f)
	}
	if s.agentSocket != "" {
		// After the write grants, so an agent listening in a directory the
		// command may write to is still masked. The path is known to exist
		// by the time it gets here, which is what makes this safe: bwrap
		// cannot create a mount point on the read-only root, so a bind at a
		// path nothing is listening on would fail the whole wrap.
		argv = append(argv, "--ro-bind", "/dev/null", s.agentSocket)
	}
	if !s.network {
		argv = append(argv, "--unshare-net")
	}
	argv = append(argv, "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--die-with-parent")
	argv = append(argv, "--clearenv")
	for _, pair := range s.env {
		name, value, _ := strings.Cut(pair, "=")
		argv = append(argv, "--setenv", name, value)
	}
	if s.cwd != "" {
		argv = append(argv, "--chdir", s.cwd)
	}
	return append(argv, "--")
}

// bwrapArgv runs a shell command string contained: it rides as one argv
// element after `sh -c`, never parsed or re-quoted.
func bwrapArgv(s spec, command string) []string {
	return append(bwrapPrefix(s), s.shell, "-c", command)
}

// probeLine bounds probe output to one short line for the availability report.
func probeLine(out []byte) string {
	line := strings.TrimSpace(string(out))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if len(line) > 200 {
		line = line[:200] + "…"
	}
	if line == "" {
		return "(no output)"
	}
	return line
}
