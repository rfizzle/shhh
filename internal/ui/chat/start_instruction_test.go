package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/project"
)

// instructionCheckout is a repository root in a temporary directory holding
// the given files, so the walk the check makes ends there.
func instructionCheckout(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const assessAgents = "assess AGENTS.md against what this session does with it"

func TestStart_SaysWhatTheInstructionBlockDropped(t *testing.T) {
	text := "# Notes\n\n## Head\n" + strings.Repeat("a line of the head\n", 200) +
		"## End\n" + strings.Repeat("a line of the end\n", 10)
	files := []project.Instruction{{Display: "AGENTS.md", Text: text}}
	info := startFixture()

	// The count is the one the prompt's own block was built with.
	_, dropped := project.InstructionBlockCut(files, 1200)
	if dropped == 0 {
		t.Fatal("the fixture was not cut")
	}
	info.Project.Instruction = project.InstructionCheck{File: "AGENTS.md", Modified: startNow, Dropped: dropped}
	view := startText(startModel(t, info))
	if want := fmt.Sprintf("in the system prompt · cut to fit · %d lines dropped", dropped); !strings.Contains(view, want) {
		t.Fatalf("the context line lacks %q:\n%s", want, view)
	}

	// A file that fits says nothing more.
	_, dropped = project.InstructionBlockCut(files, len(text))
	info.Project.Instruction.Dropped = dropped
	view = startText(startModel(t, info))
	if strings.Contains(view, "cut to fit") || !strings.Contains(view, "in the system prompt") {
		t.Fatalf("a file that fits drew a cut:\n%s", view)
	}
}

func TestStart_CountsTheInstructionFilesDeadPaths(t *testing.T) {
	root := instructionCheckout(t, map[string]string{
		"internal/store/store.go": "package store\n",
		"AGENTS.md":               "The store is internal/store/store.go; the cache was internal/cache/cache.go, documented in docs/cache.md.\n",
	})
	info := startFixture()
	info.Recent, info.Project.Dirty = StartRecent{}, 0
	info.Project.Instruction = project.CheckInstructions(project.Instructions(root, ""), root, 0)
	info.Project.Instruction.Modified = startNow

	m := startModel(t, info)
	if view := startText(m); !strings.Contains(view, "names 2 files that are gone") {
		t.Fatalf("the context line lacks the dead paths:\n%s", view)
	}
	// A count that is not zero is the offer's reason, however fresh the file.
	if titles, _ := startRows(m); titles[0] != assessAgents {
		t.Fatalf("offers = %q, want the assessment", titles)
	}

	info.Project.Instruction.Gone = 1
	if view := startText(startModel(t, info)); !strings.Contains(view, "names 1 file that is gone") {
		t.Fatalf("one dead path is not said in the singular:\n%s", view)
	}

	// No instruction file: nothing checked, nothing said, nothing offered.
	bare := instructionCheckout(t, nil)
	info.Project.ContextFiles = nil
	info.Project.Instruction = project.CheckInstructions(project.Instructions(bare, ""), bare, 0)
	if view := startText(startModel(t, info)); strings.Contains(view, "gone") || strings.Contains(view, "assess") {
		t.Fatalf("a checkout with no instruction file was checked:\n%s", view)
	}
}

func TestStart_OffersTheAssessmentWhenItIsDue(t *testing.T) {
	info := startFixture()
	info.Recent, info.Project.Dirty = StartRecent{}, 0
	offered := func(c project.InstructionCheck) []string {
		info.Project.Instruction = c
		titles, _ := startRows(startModel(t, info))
		return titles
	}

	// Fourteen days from the file's last write, on the screen's held clock:
	// a fresh edit is not asked about.
	if titles := offered(project.InstructionCheck{File: "AGENTS.md", Modified: startNow.Add(-13 * 24 * time.Hour)}); slices.Contains(titles, assessAgents) {
		t.Fatalf("a file edited 13 days ago was offered: %q", titles)
	}
	titles := offered(project.InstructionCheck{File: "AGENTS.md", Modified: startNow.Add(-14 * 24 * time.Hour)})
	if titles[0] != assessAgents {
		t.Fatalf("offers = %q, want the assessment of a file 14 days old", titles)
	}
	if view := startText(startModel(t, info)); !strings.Contains(view, "reads only, then reports") {
		t.Fatalf("the offer lacks its cost:\n%s", view)
	}

	// A cut is a reason on its own; the offer names the file it is about.
	for _, file := range []string{".shhh/project.md", "CLAUDE.md"} {
		titles := offered(project.InstructionCheck{File: file, Modified: startNow, Dropped: 4})
		if want := "assess " + file + " against what this session does with it"; titles[0] != want {
			t.Fatalf("offers = %q, want %q", titles, want)
		}
	}

	// The work the checkout states outranks the upkeep of its instructions.
	info.Ready = StartReady{Present: true, Slug: "cache-ttl", Title: "Give the cache a lifetime"}
	titles = offered(project.InstructionCheck{File: "AGENTS.md", Modified: startNow, Gone: 1})
	if len(titles) != 3 || titles[0] != "read cache-ttl and say what it would take" || titles[1] != assessAgents {
		t.Fatalf("offers = %q, want the item, then the assessment", titles)
	}
}

func TestStart_TheAssessmentPromptIsTheRubric(t *testing.T) {
	info := startFixture()
	info.Recent, info.Project.Dirty = StartRecent{}, 0
	info.Project.Instruction = project.InstructionCheck{File: "AGENTS.md", Modified: startNow, Gone: 1}
	_, actions := startRows(startModel(t, info))
	action := actions[0]
	if !strings.HasPrefix(action, assessAgents) || strings.HasPrefix(action, "/") {
		t.Fatalf("action = %q, want a prompt that assesses the file", action)
	}

	// The documentation's own list, read from the document a person reads
	// it in: every item of it is in the prompt, whole.
	raw, err := os.ReadFile("../../../docs/capabilities/coding-agent.md")
	if err != nil {
		t.Fatal(err)
	}
	_, region, ok := strings.Cut(string(raw), "<!-- BEGIN generated instruction rubric")
	region, _, ok2 := strings.Cut(region, "<!-- END generated instruction rubric -->")
	if !ok || !ok2 {
		t.Fatal("the rubric's list is not in the document")
	}
	var items []string
	for _, line := range strings.Split(region, "\n")[1:] {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "- "):
			items = append(items, strings.TrimPrefix(line, "- "))
		case line != "" && len(items) > 0:
			items[len(items)-1] += " " + line
		}
	}
	if len(items) == 0 {
		t.Fatal("the rubric's list is empty")
	}
	for _, it := range items {
		if !strings.Contains(action, it) {
			t.Fatalf("the prompt lacks the rubric's %q:\n%s", it, action)
		}
	}
}
