package cli

import (
	"os"
	"path/filepath"

	"github.com/rfizzle/agentcore/skill"
)

// Where skills are looked for.
//
// Two scopes, each with shhh's own directory and the two cross-client
// conventions. The project scope walks from the working directory up to the
// repository root, nearest first, so a monorepo's package can carry skills
// of its own without the whole tree seeing them.
// See docs/capabilities/skills.md#where-skills-live.

// dirNames are the skill directories looked for under each scope root, in
// precedence order: shhh's own, then the cross-client convention, then the
// directory most existing skills happen to be installed in.
var dirNames = []string{
	filepath.Join(".shhh", "skills"),
	filepath.Join(".agents", "skills"),
	filepath.Join(".claude", "skills"),
}

// draftSkillsDir is where a proposed skill is written under a checkout's
// root: shhh's own project skills directory, the first one the reader
// searches.
func draftSkillsDir(root string) string {
	return filepath.Join(root, dirNames[0])
}

// projectSkillRoots are the project-scope directories for a session opened in
// cwd: every ancestor up to and including the repository root, nearest
// first. Without a repository only cwd itself is scanned — walking to the
// filesystem root would read skills out of a home directory as if the
// project had written them.
func projectSkillRoots(cwd string) []skill.Root {
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil
	}
	var dirs []string
	dir := cwd
	for {
		dirs = append(dirs, dir)
		if isRepoRoot(dir) {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// No repository above cwd: only cwd counts.
			dirs = dirs[:1]
			break
		}
		dir = parent
	}
	var out []skill.Root
	for _, d := range dirs {
		for _, name := range dirNames {
			out = append(out, skill.Root{Path: filepath.Join(d, name), Scope: skill.ScopeProject})
		}
	}
	return out
}

// userSkillRoots are the user-scope directories: native are shhh's own skills
// directories beside the config file (the caller knows the config layout;
// the skill package does not), followed by the cross-client conventions in the
// home directory.
func userSkillRoots(native []string) []skill.Root {
	var out []skill.Root
	for _, d := range native {
		out = append(out, skill.Root{Path: d, Scope: skill.ScopeUser})
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out,
			skill.Root{Path: filepath.Join(home, ".agents", "skills"), Scope: skill.ScopeUser},
			skill.Root{Path: filepath.Join(home, ".claude", "skills"), Scope: skill.ScopeUser},
		)
	}
	return out
}

// skillRoots is the full search order for a session: project first, so a
// checkout's skill shadows a user one of the same name.
//
// projectTrusted is the person's answer about this checkout, and without it
// the project scope is not searched at all. A skill is instructions the
// model follows and files it reads on the strength of them, and a clone
// arrived with whatever it says; the answer is a parameter rather than
// something this package reads, because nothing inside a checkout may
// decide it (docs/capabilities/skills.md#a-skill-cannot-grant-itself-anything).
func skillRoots(cwd string, native []string, projectTrusted bool) []skill.Root {
	if !projectTrusted {
		return userSkillRoots(native)
	}
	return append(projectSkillRoots(cwd), userSkillRoots(native)...)
}

func isRepoRoot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}
