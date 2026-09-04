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
