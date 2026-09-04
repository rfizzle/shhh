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
		//
		// Which sentence it is turns on where the count came from, and the
		// two say different things. A count read at launch is work that
		// predates the conversation, all of it somebody else's. A count read
		// again an hour later has the session's own edits in it, and telling
		// a model none of that is its own would have it disown the file it
		// wrote ten minutes ago.
		if info.Reread.IsZero() {
			lines = append(lines, fmt.Sprintf(
				"- %s already changed before this session started. That work is not yours: leave it alone unless asked, and do not count it as part of what you did.",
				dirtyPaths(info.Dirty)))
		} else {
			lines = append(lines, fmt.Sprintf(
				"- %s as of %s, counted again when this conversation was rebuilt. Your own edits are in that count now; what you cannot account for is not yours, so leave it alone unless asked.",
				dirtyPaths(info.Dirty), info.Reread.Local().Format(SiblingClock)))
		}
	}

	if !info.Sibling.IsZero() {
		// The tree can move for a reason no diff explains, and a model
		// meeting a change it cannot remember making sets about explaining
		// or reverting it. Naming the other session before that happens
		// turns the reflex into a question. Nothing about it is named but
		// the hour it started: what it is doing is its own business, and
		// this session only needs to know that somebody is there.
		lines = append(lines, fmt.Sprintf(
			"- Another session has been open in this checkout since %s. Changes you did not make are most likely its work: ask before you revert, re-fix or explain them.",
			info.Sibling.Local().Format(SiblingClock)))
	}

	if len(lines) == 0 {
		return ""
	}
	return blockHeading + "\n" + strings.Join(lines, "\n")
}

// blockHeading opens the block, and is also how a prompt that already carries
// one is found again when the block is rebuilt.
const blockHeading = "# Workspace"

// ReplaceBlock swaps the workspace block inside a system prompt for block,
// which is how a conversation rebuilt from a stored message — compacted, or
// loaded out of the store — comes back describing the checkout in front of it
// rather than the one it opened on.
//
// A prompt with no block is answered unchanged. The sections of a prompt are
// assembled by whoever built it and this one only ever appears because that
// assembly put it there, so a prompt without it is not a prompt with a gap to
// fill: it is one whose builder had nothing to say about the checkout, and
// guessing at a place to insert a section would put the block somewhere no
// assembly would have chosen.
//
// The block is found by the last line that is exactly its heading, and it
// ends at the blank line that separates prompt sections — the block itself
// never contains one. The last rather than the first, because the heading is
// an ordinary line of markdown and a project instruction file, injected ahead
// of this section, is free to contain the same words.
//
// The match is exact, which assumes a prompt assembled in this process out of
// "\n". That is what every builder here does; a prompt that had been round-
// tripped through a file with carriage returns would find no heading and come
// back unchanged, which is the safe way to be wrong — it leaves the reading
// the conversation already had rather than cutting somewhere by guess.
func ReplaceBlock(sysPrompt, block string) string {
	// Nothing to put there. Every regeneration of a surveyed workspace says
	// at least one thing about it, so an empty block here is a caller with no
	// reading at all, and the reading already in the prompt beats none.
	if block == "" {
		return sysPrompt
	}
	lines := strings.Split(sysPrompt, "\n")
	start := -1
	for i, line := range lines {
		if line == blockHeading {
			start = i
		}
	}
	if start < 0 {
		return sysPrompt
	}
	end := start + 1
	for end < len(lines) && lines[end] != "" {
		end++
	}
	rebuilt := make([]string, 0, len(lines))
	rebuilt = append(rebuilt, lines[:start]...)
	rebuilt = append(rebuilt, strings.Split(block, "\n")...)
	rebuilt = append(rebuilt, lines[end:]...)
	return strings.Join(rebuilt, "\n")
}

// SiblingClock is how the workspace block writes a moment: the other
// session's start time wherever it is named, and the moment a re-read dirty
// count was taken. The hour and the minute and nothing else: the question a
// reader has of either is "before or after the thing I am looking at", which
// a clock answers and a date does not.
const SiblingClock = "15:04"

// dirtyPaths is n uncommitted paths, in words.
func dirtyPaths(n int) string {
	if n == 1 {
		return "1 uncommitted path"
	}
	return fmt.Sprintf("%d uncommitted paths", n)
}
