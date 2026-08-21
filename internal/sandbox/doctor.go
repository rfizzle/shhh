package sandbox

import (
	"fmt"
	"strings"
)

// Report describes the containment mechanism and resolved policy for `shhh
// code doctor` and the /sandbox slash command: which mechanism (and why not,
// when unavailable), the profile, and the resolved write grants and deny
// mask.
func Report(avail Availability, p Policy) string {
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

	s, err := resolvePolicy(p)
	if err != nil {
		fmt.Fprintf(&b, "  policy:    %v\n", err)
		if avail.OK {
			b.WriteString("  contained commands will fail until the policy is fixed; they never run bare\n")
		}
		return strings.TrimRight(b.String(), "\n")
	}

	fmt.Fprintf(&b, "  writable:  %s\n", pathList(s.write))
	masked := append(append([]string{}, s.denyDirs...), s.denyFiles...)
	if len(masked) == 0 {
		b.WriteString("  masked:    (none of the deny-mask paths exist)\n")
	} else {
		fmt.Fprintf(&b, "  masked:    %s\n", pathList(masked))
	}
	return strings.TrimRight(b.String(), "\n")
}

func pathList(paths []string) string {
	return strings.Join(paths, "\n             ")
}
