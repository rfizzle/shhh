package skill

import (
	"os"
	"path/filepath"
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

// ProjectRoots are the project-scope directories for a session opened in
// cwd: every ancestor up to and including the repository root, nearest
// first. Without a repository only cwd itself is scanned — walking to the
// filesystem root would read skills out of a home directory as if the
// project had written them.
func ProjectRoots(cwd string) []Root {
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
	var out []Root
	for _, d := range dirs {
		for _, name := range dirNames {
			out = append(out, Root{Path: filepath.Join(d, name), Scope: ScopeProject})
		}
	}
	return out
}

// UserRoots are the user-scope directories: native are shhh's own skills
// directories beside the config file (the caller knows the config layout;
// this package does not), followed by the cross-client conventions in the
// home directory.
func UserRoots(native []string) []Root {
	var out []Root
	for _, d := range native {
		out = append(out, Root{Path: d, Scope: ScopeUser})
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out,
			Root{Path: filepath.Join(home, ".agents", "skills"), Scope: ScopeUser},
			Root{Path: filepath.Join(home, ".claude", "skills"), Scope: ScopeUser},
		)
	}
	return out
}

// Roots is the full search order for a session: project first, so a
// checkout's skill shadows a user one of the same name.
func Roots(cwd string, native []string) []Root {
	return append(ProjectRoots(cwd), UserRoots(native)...)
}

func isRepoRoot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}
