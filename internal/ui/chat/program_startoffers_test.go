package chat

import (
	"strings"
	"testing"
)

// TestProgram_TheStartScreenOffersTheBranchAndTheReadyItem is the route of the
// start-offers scene (scripts/tui/scenes/start-offers): a clean checkout on a
// branch two commits ahead with nothing pushed, and a backlog with one ready
// item. The branch is the work in hand and takes the first read-only row, the
// item the second, and choosing the item sends a prompt naming it.
func TestProgram_TheStartScreenOffersTheBranchAndTheReadyItem(t *testing.T) {
	info := startFixture()
	info.Recent = StartRecent{}
	info.Project.Dirty = 0
	info.Branch = StartBranch{Ahead: 2}
	info.Ready = StartReady{Present: true, Slug: "cache-ttl", Title: "Give the cache a lifetime", Noun: "story"}
	m, _ := scriptedSession(programTurn{text: "It would take a lifetime field and an eviction pass."})
	tm := runProgramAt(t, m.WithStartScreen(info), 110, 40)

	waitForAll(t, tm, "Some things worth doing first",
		"review what this branch changes before it goes up", "2 commits ahead · not pushed",
		"read cache-ttl and say what it would take", "Give the cache a lifetime")
	programPress(t, tm, "down", "enter")
	waitForText(t, tm, "It would take a lifetime field")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "story cache-ttl")
	if strings.Contains(frame, "Some things worth doing first") {
		t.Fatalf("the offers stayed after one was taken:\n%s", frame)
	}
}
