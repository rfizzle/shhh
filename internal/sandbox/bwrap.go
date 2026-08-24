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
	if !s.network {
		argv = append(argv, "--unshare-net")
	}
	argv = append(argv, "--die-with-parent")
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
