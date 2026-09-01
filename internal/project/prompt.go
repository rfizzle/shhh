package project

// Handing the survey to the model as well as to the screen.
//
// Survey answers four cheap questions at session start — the ecosystem, the
// toolchain, the branch, how much is already changed — and for a long time
// only the start screen read them. The model was told the shell, the OS and
// the working directory, and had to spend rounds on git for the rest.
//
// This is the same facts as a prompt block, built by the package that owns
// them, the way every other optional block is built by its own package.
// See docs/capabilities/coding-agent.md#the-agent-knows-where-and-when-it-is-standing.

import (
	"fmt"
	"strings"
)

// PromptBlock describes the checkout to the model: what it is built with, and
// what git says about it. Empty when the survey learned nothing worth saying,
// so the section costs nothing in a directory that is not a project.
//
// It states no path. The working directory is already in the environment
// section of every prompt that has one, and naming it twice invites the two
// to disagree.
func PromptBlock(info Info) string {
	var lines []string

	if info.Language != "" {
		lang := info.Language
		if info.Toolchain != "" {
			lang += " " + info.Toolchain
		}
		lines = append(lines, "- Language: "+lang)
	}

	// The git clauses are findings, so they need a survey to have happened.
	// A zero Info is "nobody looked", which is not the same as "no repository"
	// — and reporting the second for the first would warn about unrecoverable
	// edits in a checkout that is perfectly recoverable.
	surveyed := info.Dir != ""
	switch {
	case !surveyed:
	case !info.Repo:
		// Worth a line of its own: it is the fact that makes an edit
		// unrecoverable, and the agent is the one being asked to make edits.
		lines = append(lines, "- Not a git repository — there is nothing to restore a file from, so be correspondingly careful with overwrites and deletions.")
	case info.Detached:
		lines = append(lines, "- Git: detached HEAD — a commit here belongs to no branch. Say so before making one.")
	case info.Branch != "":
		lines = append(lines, "- Git branch: "+info.Branch)
	}

	if surveyed && info.Repo && info.Dirty > 0 {
		// The one line here that stops a wrong action rather than saving a
		// round. Without it an agent reads its own diff, finds changes it
		// cannot remember making, and sets about explaining or reverting
		// them.
		lines = append(lines, fmt.Sprintf(
			"- %s already changed before this session started. That work is not yours: leave it alone unless asked, and do not count it as part of what you did.",
			dirtyPaths(info.Dirty)))
	}

	if len(lines) == 0 {
		return ""
	}
	return "# Workspace\n" + strings.Join(lines, "\n")
}

// dirtyPaths is n uncommitted paths, in words.
func dirtyPaths(n int) string {
	if n == 1 {
		return "1 uncommitted path"
	}
	return fmt.Sprintf("%d uncommitted paths", n)
}
