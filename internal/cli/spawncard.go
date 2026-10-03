package cli

import (
	"strings"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/web"
)

// hostReach is the session's reachable hosts as a card states them.
type hostReach struct{ hosts []string }

// value is the hosts in one line, or the phrase for a session that has
// answered for none — a child with no grants is not a child with no web, it
// is a child whose every fetch comes back here as a card.
func (h *hostReach) value() string {
	if h == nil || len(h.hosts) == 0 {
		return "no host granted yet"
	}
	return strings.Join(h.hosts, ", ")
}

// detail finishes the sentence the value opens. The two halves are a field's
// value and the clause it is read with, not two statements sharing a row: a
// row that said "no host granted yet — every fetch asks you" and then "the
// hosts granted here; any other asks you" beside it was one slot carrying two
// sentences written for it (docs/interface/surfaces.md#the-approval-card).
func (h *hostReach) detail() string {
	if h == nil || len(h.hosts) == 0 {
		return "every fetch comes back to you as a card"
	}
	return "any other host asks you"
}

// spawnSubject names the child a card is about: its own name where the call
// gave it one, and otherwise the role — the supervisor names an unnamed child
// when it starts it, and a name invented here would be a different one.
func spawnSubject(plan subagent.Spawn) string {
	if plan.Name != "" {
		return plan.Name
	}
	return string(plan.Role)
}

// childReachesWeb reports whether a child of this role is given the fetch
// tool at all: the built-in roles get whatever web toolset the session
// opened, and a profile from a file gets it only where its own permissions
// and tool list say so (subagents.go, profileEnv).
func childReachesWeb(session chatSession, agents *agentProfiles, role subagent.Role) bool {
	if session.web == nil || agents == nil {
		return false
	}
	def, ok := agents.definitions[string(role)]
	if !ok {
		return true
	}
	return def.Has(config.PermissionWeb) && def.Allows(web.FetchToolName)
}
