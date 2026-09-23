package sandbox

import (
	"fmt"
	"net"
	"strings"
)

// ParseHosts reads sandbox.allow_hosts: each entry an exact host name or an
// IP address, lower-cased, with a trailing dot dropped and duplicates folded.
// A scheme, a port, a path or a wildcard is refused rather than read as
// something near it, because the list is matched exactly — a `*.npmjs.org`
// that quietly meant `npmjs.org` would be a list that allows what nobody
// wrote.
// See docs/capabilities/containment.md#a-contained-commands-network-can-be-a-list-of-hosts.
func ParseHosts(entries []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, e := range entries {
		h := normalHost(e)
		if h == "" {
			continue
		}
		if !validHost(h) {
			return nil, fmt.Errorf("%q is not a host name: name the host alone — no scheme, port, path or wildcard", strings.TrimSpace(e))
		}
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out, nil
}

// normalHost is the one spelling a host is compared in, for the list and for
// the address a proxy request names alike: lower case, no brackets around an
// IPv6 literal, no trailing dot.
func normalHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	return strings.TrimSuffix(h, ".")
}

func validHost(h string) bool {
	if net.ParseIP(h) != nil {
		return true
	}
	for label := range strings.SplitSeq(h, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			letter, digit := r >= 'a' && r <= 'z', r >= '0' && r <= '9'
			if !letter && !digit && r != '-' && r != '_' {
				return false
			}
		}
	}
	return true
}

// HoldsHosts reports whether the mechanism can confine a command to a list
// of hosts. Both of the process mechanisms can, and each the only way its
// platform allows: Seatbelt's network filter takes `*` or `localhost` as a
// host and nothing else, so neither of them names a host to the kernel — the
// command gets no network but a proxy on loopback, and the proxy is what
// reads the list. Anything else — no mechanism at all, or a disposable
// container, whose network is a switch — runs the profile's switch instead,
// and says so wherever the network is reported.
func HoldsHosts(mechanism string) bool {
	return mechanism == "bwrap" || mechanism == "sandbox-exec"
}

// NetworkWords is what a contained command's network is, in the words every
// report of it uses: preserved, disabled, or the number of hosts it may
// reach. A list the session cannot hold is said beside the switch it runs
// instead, because a report that named the list there would describe a
// boundary that is not in force.
// See docs/capabilities/containment.md#what-is-reported-is-what-is-in-force.
func NetworkWords(avail Availability, p Policy) string {
	switch {
	case p.Profile == ProfileWorkspaceNetless && len(p.AllowHosts) > 0:
		return "network disabled; sandbox.allow_hosts is not read under this profile"
	case p.Profile == ProfileWorkspaceNetless:
		return "network disabled"
	case len(p.AllowHosts) == 0:
		return "network preserved"
	case !avail.OK || !HoldsHosts(avail.Mechanism):
		return "network preserved; sandbox.allow_hosts is not in force, because nothing here can hold it"
	}
	return "network: " + HostCount(len(p.AllowHosts)) + " — " + strings.Join(p.AllowHosts, ", ")
}

// HostCount is "1 host" or "n hosts", the value the approval card's network
// field carries.
func HostCount(n int) string {
	if n == 1 {
		return "1 host"
	}
	return fmt.Sprintf("%d hosts", n)
}
