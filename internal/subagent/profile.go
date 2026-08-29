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
	// MaxTokens and MaxRounds are the defaults for a spawn that names
	// neither; zero means the package defaults.
	MaxTokens int64
	MaxRounds int
}

// Profiles is the set of roles a session can spawn, keyed by name.
type Profiles map[Role]Profile

// BuiltinProfiles are the two roles every session has.
func BuiltinProfiles() Profiles {
	return Profiles{
		RoleResearcher: {
			Name:        RoleResearcher,
			Description: "read-only tools plus web; use for parallel research and codebase surveys",
		},
		RoleWriter: {
			Name:        RoleWriter,
			Description: "full tools against an isolated copy of the workspace; its file changes come back as a single patch the user reviews before anything touches the real checkout",
			Writes:      true,
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
		if name != RoleResearcher && name != RoleWriter {
			custom = append(custom, string(name))
		}
	}
	sort.Strings(custom)
	var out []string
	for _, name := range []Role{RoleResearcher, RoleWriter} {
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
