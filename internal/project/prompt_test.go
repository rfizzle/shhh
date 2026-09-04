package project

import (
	"strings"
	"testing"
	"time"
)

func TestPromptBlockNamesTheLanguageAndItsToolchain(t *testing.T) {
	got := PromptBlock(Info{Dir: "/work", Language: "go", Toolchain: "1.26.2", Repo: true, Branch: "master"})
	if !strings.Contains(got, "Language: go 1.26.2") {
		t.Errorf("the ecosystem and its version belong together:\n%s", got)
	}
	if !strings.Contains(got, "Git branch: master") {
		t.Errorf("the branch should be stated:\n%s", got)
	}
}

// The line that stops a wrong action: an agent that meets pre-existing
// changes in its own diff will otherwise try to account for them.
func TestPromptBlockDisownsChangesItDidNotMake(t *testing.T) {
	got := PromptBlock(Info{Dir: "/work", Repo: true, Branch: "master", Dirty: 12})
	if !strings.Contains(got, "12 uncommitted paths") {
		t.Errorf("the dirty count should be stated:\n%s", got)
	}
	if !strings.Contains(got, "not yours") {
		t.Errorf("the count is useless without saying whose the work is:\n%s", got)
	}
}

func TestPromptBlockCountsOnePathInTheSingular(t *testing.T) {
	got := PromptBlock(Info{Dir: "/work", Repo: true, Branch: "master", Dirty: 1})
	if !strings.Contains(got, "1 uncommitted path ") {
		t.Errorf("one path is not paths:\n%s", got)
	}
}

func TestPromptBlockSaysNothingAboutACleanTree(t *testing.T) {
	got := PromptBlock(Info{Dir: "/work", Repo: true, Branch: "master"})
	if strings.Contains(got, "uncommitted") {
		t.Errorf("a clean tree has nothing to disown:\n%s", got)
	}
}

func TestPromptBlockWarnsWhereThereIsNothingToRestoreFrom(t *testing.T) {
	got := PromptBlock(Info{Dir: "/work", Language: "go"})
	if !strings.Contains(got, "Not a git repository") {
		t.Errorf("the absence of a repository is the fact that makes an edit unrecoverable:\n%s", got)
	}
	if strings.Contains(got, "branch") {
		t.Errorf("there is no branch to name outside a repository:\n%s", got)
	}
}

func TestPromptBlockNamesADetachedHead(t *testing.T) {
	got := PromptBlock(Info{Dir: "/work", Repo: true, Detached: true})
	if !strings.Contains(got, "detached HEAD") {
		t.Errorf("a detached HEAD changes what a commit means:\n%s", got)
	}
}

// A zero Info is nobody having looked, which is not the same as there being
// no repository — and warning about unrecoverable edits in a checkout that is
// perfectly recoverable is the wrong way to be wrong.
func TestPromptBlockSaysNothingWhenNoSurveyRan(t *testing.T) {
	if got := PromptBlock(Info{}); got != "" {
		t.Errorf("an unsurveyed workspace has no findings, got %q", got)
	}
}

// The working directory is already in the environment section of every
// prompt; naming it twice invites the two to disagree.
func TestPromptBlockStatesNoPath(t *testing.T) {
	got := PromptBlock(Info{Dir: "/tmp/somewhere", Display: "~/somewhere", Language: "go", Repo: true, Branch: "main"})
	if strings.Contains(got, "/tmp/somewhere") || strings.Contains(got, "~/somewhere") {
		t.Errorf("the block must not restate the working directory:\n%s", got)
	}
}

// A change nobody in the transcript made has an author, and the model that
// is told so asks instead of reverting.
func TestPromptBlockNamesTheOtherSessionInThisCheckout(t *testing.T) {
	since := time.Date(2026, 9, 4, 10, 32, 0, 0, time.Local)
	got := PromptBlock(Info{Dir: "/work", Repo: true, Branch: "master", Sibling: since})
	if !strings.Contains(got, "Another session has been open in this checkout since 10:32") {
		t.Errorf("the other session and when it opened should be stated:\n%s", got)
	}
	if !strings.Contains(got, "ask before you revert") {
		t.Errorf("naming the session is only useful with what to do about it:\n%s", got)
	}
}

func TestPromptBlockSaysNothingWhenNobodyElseIsHere(t *testing.T) {
	got := PromptBlock(Info{Dir: "/work", Repo: true, Branch: "master"})
	if strings.Contains(got, "Another session") {
		t.Errorf("a session alone in a checkout has no sibling to name:\n%s", got)
	}
}

// After a re-reading the count is not somebody else's work any more: the
// session has been editing for an hour, and its own files are in it.
func TestPromptBlockDatesACountItTookAgain(t *testing.T) {
	at := time.Date(2026, 9, 4, 14, 32, 0, 0, time.Local)
	got := PromptBlock(Info{Dir: "/work", Repo: true, Branch: "master", Dirty: 3, Reread: at})
	if !strings.Contains(got, "3 uncommitted paths as of 14:32") {
		t.Errorf("a re-read count is dated:\n%s", got)
	}
	if strings.Contains(got, "before this session started") {
		t.Errorf("a count taken an hour in did not predate the session:\n%s", got)
	}
	if !strings.Contains(got, "Your own edits are in that count now") {
		t.Errorf("a model told none of it is its own disowns the file it just wrote:\n%s", got)
	}
}

func TestReplaceBlockSwapsTheBlockAndLeavesTheRestAlone(t *testing.T) {
	before := PromptBlock(Info{Dir: "/work", Repo: true, Branch: "master", Dirty: 1})
	sysPrompt := "# Environment\nShell: bash\n\n" + before + "\n\n# Tools\nread_file\n"
	after := PromptBlock(Info{Dir: "/work", Repo: true, Branch: "side"})

	got := ReplaceBlock(sysPrompt, after)
	if !strings.Contains(got, "Git branch: side") {
		t.Errorf("the new block should be in there:\n%s", got)
	}
	if strings.Contains(got, "Git branch: master") || strings.Contains(got, "uncommitted") {
		t.Errorf("the old block should be gone:\n%s", got)
	}
	if !strings.Contains(got, "# Environment\nShell: bash") || !strings.Contains(got, "# Tools\nread_file") {
		t.Errorf("the sections either side are not this function's to touch:\n%s", got)
	}
}

// A project instruction file is injected ahead of this section and is free to
// contain the same heading; the block is the last one, not the first.
func TestReplaceBlockTakesTheLastHeading(t *testing.T) {
	quoted := "# Project instructions\n\n# Workspace\nThe team calls the repo root the workspace.\n"
	sysPrompt := quoted + "\n" + PromptBlock(Info{Dir: "/work", Repo: true, Branch: "master"}) + "\n"

	got := ReplaceBlock(sysPrompt, PromptBlock(Info{Dir: "/work", Repo: true, Branch: "side"}))
	if !strings.Contains(got, "The team calls the repo root the workspace.") {
		t.Errorf("somebody else's heading is not the block:\n%s", got)
	}
	if !strings.Contains(got, "Git branch: side") {
		t.Errorf("the block should have been replaced:\n%s", got)
	}
}

// A prompt whose builder had nothing to say about the checkout is not a
// prompt with a gap to fill.
func TestReplaceBlockLeavesAPromptWithoutOneAlone(t *testing.T) {
	sysPrompt := "# Environment\nShell: bash\n"
	if got := ReplaceBlock(sysPrompt, PromptBlock(Info{Dir: "/work", Repo: true, Branch: "side"})); got != sysPrompt {
		t.Errorf("nothing to replace, so nothing changes:\n%s", got)
	}
	if got := ReplaceBlock(sysPrompt, ""); got != sysPrompt {
		t.Errorf("no reading at all is worse than the one already there:\n%s", got)
	}
}
