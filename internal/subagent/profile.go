package subagent

// Profiles: what a spawn_agent role names. The two roles that ship — a
// researcher that reads and a writer that changes things in a worktree —
// are profiles like any the user writes to the agents directory; a custom
// one differs in what it may touch, what it runs on and what it is told,
// not in kind. See docs/capabilities/subagents.md#a-profile-is-a-file.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
)

// Profile is what the supervisor needs to know about a role. Everything
// about a child's toolset and prompt lives in the Env its factory builds;
// what is here is what the supervisor itself decides by — whether the child
// gets its own worktree and a patch, the mode it starts in, and the budgets
// a spawn that names none falls back to.
type Profile struct {
	// Name is the role a spawn names.
	Name Role
	// Description is the one-line account shown to the orchestrating model.
	Description string
	// Writes marks a child that can change something: it works in an
	// isolated worktree, may claim paths, and hands back a patch.
	Writes bool
	// Mode is the permission mode the child starts in when HasMode is set;
	// otherwise it inherits the parent's. Either way it is clamped to the
	// parent's mode.
	Mode    agent.Mode
	HasMode bool
	// Reviews marks a child whose declared paths are evidence rather than a
	// write claim: it is handed those paths and their diff before its task,
	// nothing is reserved against other agents on its behalf, and its round
	// cap ends the inspection in a report instead of widening it. Only a
	// profile that changes nothing can carry it — a claim and a reading of
	// the same paths are opposite meanings for one field.
	Reviews bool
	// MaxTokens and MaxRounds are the defaults for a spawn that names
	// neither; zero means the package defaults.
	MaxTokens int64
	MaxRounds int
	// Inherit is how many of its parent's last turns a spawn of this role is
	// handed ahead of its task when the call does not say; a call may lower
	// it, to zero included. Zero, the default for every role shhh ships,
	// hands a child its task alone.
	// See docs/capabilities/subagents.md#what-they-share.
	Inherit int
}

// Profiles is the set of roles a session can spawn, keyed by name.
type Profiles map[Role]Profile

// BuiltinProfiles are the two roles every session has.
func BuiltinProfiles() Profiles {
	return Profiles{
		RoleResearcher: {
			Name:        RoleResearcher,
			Description: "the session's own read-only toolset plus web; use for parallel research and codebase surveys",
		},
		RoleWriter: {
			Name:        RoleWriter,
			Description: "full tools against an isolated copy of the workspace; its file changes come back as a single patch the user reviews before anything touches the real checkout",
			Writes:      true,
		},
		RoleReviewer: {
			Name:        RoleReviewer,
			Description: "reviews a change for correctness and clarity; reads only, changes nothing; declare its paths and it opens on their diff",
			// Read-only rather than plan: the child is never handed plan mode's
			// instructions, so plan would name a job it was not given, and a
			// refused call would send it to present a plan nobody approves.
			Mode:      agent.ModeReadOnly,
			HasMode:   true,
			Reviews:   true,
			MaxTokens: DefaultMaxTokens,
			// Twenty rounds is the inspection pass: the declared diff arrives
			// with the task, so the rounds are spent on the files it touches
			// and their tests rather than on finding the change. It is a stop
			// and not a check-in — reviewReportDirective is what follows it.
			MaxRounds: 20,
		},
	}
}

// Parse maps a spawn_agent role argument to its profile.
func (p Profiles) Parse(s string) (Profile, error) {
	name := Role(strings.ToLower(strings.TrimSpace(s)))
	if prof, ok := p[name]; ok {
		return prof, nil
	}
	return Profile{}, fmt.Errorf("unknown role %q (valid: %s)", s, strings.Join(p.Names(), ", "))
}

// Names lists the roles, built-ins first and the rest alphabetically, so
// the enum the model sees is stable across sessions.
func (p Profiles) Names() []string {
	var custom []string
	for name := range p {
		if name != RoleResearcher && name != RoleWriter && name != RoleReviewer {
			custom = append(custom, string(name))
		}
	}
	sort.Strings(custom)
	var out []string
	for _, name := range []Role{RoleResearcher, RoleWriter, RoleReviewer} {
		if _, ok := p[name]; ok {
			out = append(out, string(name))
		}
	}
	return append(out, custom...)
}

// describe renders the role list for the spawn tool's description: one
// clause per role, so the model choosing between profiles reads what each
// is for rather than only its name.
func (p Profiles) describe() string {
	var parts []string
	for _, name := range p.Names() {
		prof := p[Role(name)]
		desc := strings.TrimSpace(prof.Description)
		if desc == "" {
			if prof.Writes {
				desc = "changes files in an isolated copy of the workspace; its changes come back as a patch"
			} else {
				desc = "reads and reports; changes nothing"
			}
		}
		parts = append(parts, fmt.Sprintf("'%s' (%s)", name, desc))
	}
	return strings.Join(parts, "; ")
}
