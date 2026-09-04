package sandbox

import (
	"fmt"
	"strings"
)

// Report describes the containment mechanism and resolved policy for `shhh
// code doctor` and the /sandbox slash command: which mechanism (and why not,
// when unavailable), the profile, how many of the session's long-running
// processes are running under it, and the resolved write grants and deny
// mask.
//
// running is counted because a command is over by the time its result is
// read and a started process is not: it is the part of the session still
// running when the report is asked for, so a report that named only the
// mechanism would describe what nothing is doing.
func Report(avail Availability, p Policy, running int) string {
	var b strings.Builder
	b.WriteString("Command containment:\n")
	if avail.OK {
		fmt.Fprintf(&b, "  mechanism: %s — %s\n", avail.Mechanism, avail.Detail)
	} else {
		fmt.Fprintf(&b, "  mechanism: unavailable — %s\n", avail.Detail)
		b.WriteString("  agent commands run unconfined in this session\n")
	}

	profile := p.Profile
	if profile == "" {
		profile = ProfileWorkspace
	}
	network := "preserved"
	if profile == ProfileWorkspaceNetless {
		network = "disabled"
	}
	fmt.Fprintf(&b, "  profile:   %s (network %s)\n", profile, network)
	fmt.Fprintf(&b, "  processes: %s\n", processLine(avail.OK, running))

	s, err := resolvePolicy(p)
	if err != nil {
		fmt.Fprintf(&b, "  policy:    %v\n", err)
		if avail.OK {
			b.WriteString("  contained commands will fail until the policy is fixed; they never run bare\n")
		}
		return strings.TrimRight(b.String(), "\n")
	}

	fmt.Fprintf(&b, "  writable:  %s\n", pathList(s.write))
	fmt.Fprintf(&b, "  variables: %s\n", envList(s.env))
	masked := append(append([]string{}, s.denyDirs...), s.denyFiles...)
	if len(masked) == 0 {
		b.WriteString("  masked:    (none of the deny-mask paths exist)\n")
	} else {
		fmt.Fprintf(&b, "  masked:    %s\n", pathList(masked))
	}
	return strings.TrimRight(b.String(), "\n")
}

// processLine says what the session's long-running processes are running
// under, in the mechanism's own terms: contained where one is in force,
// unconfined where none is, and neither claim when none are running.
func processLine(contained bool, running int) string {
	switch {
	case running == 0:
		return "none running"
	case contained:
		return fmt.Sprintf("%s running under it", plural(running))
	}
	return fmt.Sprintf("%s running unconfined", plural(running))
}

func plural(n int) string {
	if n == 1 {
		return "1 process"
	}
	return fmt.Sprintf("%d processes", n)
}

// envList names the variables a contained command carries, without their
// values: the report is read out loud and pasted into issues, and the point
// of the allowlist is which names crossed rather than what they said.
func envList(env []string) string {
	if len(env) == 0 {
		return "(none)"
	}
	names := make([]string, 0, len(env))
	for _, pair := range env {
		name, _, _ := strings.Cut(pair, "=")
		names = append(names, name)
	}
	return strings.Join(names, " ")
}

func pathList(paths []string) string {
	return strings.Join(paths, "\n             ")
}
