package radius

// Reach is the one-shot's containment line (S-113, DESIGN-TUI.md §18b). The
// approval cards spend a row each on what a command touches, what could undo
// it and what the sandbox allows, because a session has the room for three
// rows and the reader is deciding twenty times an hour. The front door has
// one line and one decision, so the same resolution is folded into a phrase:
// what it writes, whether it leaves the machine, and whose privileges it
// runs with.
//
// It is the same reading, so it inherits the same honesty: a verb shhh could
// not account for makes the network facet `unknown` rather than quiet, since
// a command nobody resolved is not one anybody can promise stays local.

import (
	"fmt"
	"path"
	"strings"
)

// Reach describes the command's reach as one line, three facets separated by
// the middle dot the rest of the UI uses. It never claims more than Resolve
// found: `read-only` means the resolver accounted for every segment and none
// of them wrote.
func (c Command) Reach() string {
	return strings.Join([]string{c.writeFacet(), c.netFacet(), c.sudoFacet()}, " · ")
}

// writeFacet names what the command changes on disk. An unresolved segment is
// never folded into "read-only" — that is the whole point of the resolver.
func (c Command) writeFacet() string {
	switch {
	case len(c.Writes) == 0 && len(c.Unresolved) == 0:
		return "read-only"
	case len(c.Writes) == 0:
		return "writes unknown"
	}
	facet := "writes " + c.Writes[0].Path
	if len(c.Writes) > 1 {
		facet = fmt.Sprintf("writes %d paths", len(c.Writes))
	}
	if len(c.Unresolved) > 0 {
		facet += ", plus unknown"
	}
	return facet
}

// netFacet reports the network. shhh reads verbs, not sockets, so a resolved
// command that names no network verb gets `no network` and an unresolved one
// gets `network unknown`.
func (c Command) netFacet() string {
	switch {
	case c.Net:
		return "network"
	case len(c.Unresolved) > 0:
		return "network unknown"
	}
	return "no network"
}

// sudoFacet is the one facet that is always knowable: escalation is a word in
// the command or it is not there.
func (c Command) sudoFacet() string {
	if c.Sudo {
		return "sudo"
	}
	return "no sudo"
}

// netVerbs are the commands whose whole purpose is to leave the machine.
var netVerbs = map[string]bool{
	"curl": true, "wget": true, "ssh": true, "scp": true, "sftp": true,
	"rsync": true, "ftp": true, "nc": true, "netcat": true, "telnet": true,
	"dig": true, "host": true, "nslookup": true, "ping": true, "ping6": true,
	"traceroute": true, "whois": true, "http": true, "https": true,
	"httpie": true, "aws": true, "gcloud": true, "az": true, "kubectl": true,
	"helm": true, "terraform": true, "gh": true, "glab": true,
}

// netSubcommands are the verbs that only reach the network for some of what
// they do. `git status` is local and `git push` is not, and a resolver that
// cannot tell them apart says "network" about every repository command in the
// checkout — which trains the reader to stop reading the line.
var netSubcommands = map[string]map[string]bool{
	"git": {
		"clone": true, "fetch": true, "pull": true, "push": true,
		"remote": true, "submodule": true, "ls-remote": true,
	},
	"go":     {"get": true, "install": true, "mod": true, "download": true},
	"docker": {"pull": true, "push": true, "login": true, "search": true},
	"podman": {"pull": true, "push": true, "login": true, "search": true},
	"npm": {
		"install": true, "i": true, "ci": true, "publish": true,
		"update": true, "audit": true, "outdated": true, "view": true,
	},
	"pnpm":  {"install": true, "i": true, "add": true, "update": true, "publish": true},
	"yarn":  {"install": true, "add": true, "upgrade": true, "publish": true},
	"pip":   {"install": true, "download": true, "wheel": true},
	"pip3":  {"install": true, "download": true, "wheel": true},
	"cargo": {"install": true, "publish": true, "fetch": true, "update": true},
	"gem":   {"install": true, "update": true, "push": true, "fetch": true},
	"brew":  {"install": true, "upgrade": true, "update": true, "fetch": true, "tap": true},
	"apt":   {"install": true, "update": true, "upgrade": true, "download": true},
	"apt-get": {
		"install": true, "update": true, "upgrade": true, "download": true,
		"dist-upgrade": true,
	},
	"dnf": {"install": true, "update": true, "upgrade": true, "download": true},
	"yum": {"install": true, "update": true, "upgrade": true},
	"apk": {"add": true, "update": true, "upgrade": true, "fetch": true},
}

// reachesNetwork answers for one segment's verb and its operands. A verb with
// a subcommand table needs the subcommand to say yes; everything else is a
// straight lookup.
func reachesNetwork(verb string, rest []token) bool {
	if netVerbs[verb] {
		return true
	}
	subs, ok := netSubcommands[verb]
	if !ok {
		return false
	}
	for _, t := range rest {
		if strings.HasPrefix(t.text, "-") {
			continue
		}
		return subs[path.Base(t.text)]
	}
	return false
}
