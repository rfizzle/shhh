package chat

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/project"
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
	m, _ := scriptedSessionWith(Wiring{Start: &info}, programTurn{text: "It would take a lifetime field and an eviction pass."})
	tm := runProgramAt(t, m, 110, 40)

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

// TestProgram_TheStartScreenSaysTheInstructionFileNamesAFileThatIsGone is the
// same scene's second reading: the checkout's AGENTS.md names a file that is
// not there, and the line naming what was read into the system prompt says
// so. The assessment ranks after the branch and the item, so with both on
// the screen it takes no row.
func TestProgram_TheStartScreenSaysTheInstructionFileNamesAFileThatIsGone(t *testing.T) {
	info := startFixture()
	info.Recent = StartRecent{}
	info.Project.Dirty = 0
	info.Branch = StartBranch{Ahead: 2}
	info.Ready = StartReady{Present: true, Slug: "cache-ttl", Title: "Give the cache a lifetime", Noun: "story"}
	info.Project.Instruction = project.InstructionCheck{File: "AGENTS.md", Modified: startNow, Gone: 1}
	m, _ := scriptedSessionWith(Wiring{Start: &info}, programTurn{text: "Nothing is sent."})
	tm := runProgramAt(t, m, 110, 40)

	waitForAll(t, tm, "Some things worth doing first",
		"AGENTS.md — in the system prompt · names 1 file that is gone",
		"review what this branch changes before it goes up", "read cache-ttl and say what it would take")
	frame := finalFrame(t, tm)
	if !strings.Contains(frame, "Some things worth doing first") || strings.Contains(frame, "assess AGENTS.md") {
		t.Fatalf("the assessment outranked the work the checkout states:\n%s", frame)
	}
}

// TestProgram_AReadingWritesTheStartScreensReadOnlyRows is the start-offers
// scene's last reading: the start screen draws its fixed rows, the reading
// asked once the screen has a size lands with two offers naming the branch
// and the ready item, and each takes the row of the thing it names — the
// facts, the notes and the approval row untouched. Choosing the first sends
// its line, priced as the slot is.
func TestProgram_AReadingWritesTheStartScreensReadOnlyRows(t *testing.T) {
	info := startFixture()
	info.Recent = StartRecent{}
	info.Project.Dirty = 0
	info.Project.Branch = "cache-work"
	info.Branch = StartBranch{Ahead: 2}
	info.Ready = StartReady{Present: true, Slug: "cache-ttl", Title: "Give the cache a lifetime", Noun: "story"}
	written := &suggestProvider{line: `{"offers":[{"title":"say what cache-work still lacks for its lifetime","prompt":"Read the commits on cache-work and say what the lifetime still lacks."},{"title":"check the cache-ttl test against the cache package","prompt":"Read cache-ttl and the cache package and say whether TestTTL exists."}]}`}
	m, _ := scriptedSession(programTurn{text: "The lifetime still lacks an eviction pass."})
	m.start = new(info)
	m.suggest.writer, m.suggest.on = nil, true
	m.startOffers.writer, m.startOffers.gather = agent.NewStartOfferer(written, agent.StartOffersConfig{Model: "fast"}), nil
	tm := runProgramAt(t, m, 110, 40)

	waitForAll(t, tm, "say what cache-work still lacks for its lifetime",
		"check the cache-ttl test against the cache package", "go 1.24 · clean tree · 41 packages",
		"run the default quality gate and triage what fails")
	programPress(t, tm, "enter")
	waitForText(t, tm, "an eviction pass")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "Read the commits on cache-work")
	frameHas(t, frame, "Change nothing")
}
