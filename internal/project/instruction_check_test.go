package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The count is the cut's own: lines a whole file keeps are not counted, and
// the lines a cut file loses are the lines of the original that the block no
// longer carries.
func TestInstructionBlockCut_CountsTheLinesTheCutDropped(t *testing.T) {
	text := "# Title\n\n## One\n" + strings.Repeat("a line of the head\n", 200) +
		"## Two\n" + strings.Repeat("a line of the end\n", 10)
	files := []Instruction{{Display: "AGENTS.md", Text: text}}

	block, dropped := InstructionBlockCut(files, 0)
	if dropped != 0 || block != InstructionBlock(files, 0) {
		t.Fatalf("an unbounded block dropped %d lines", dropped)
	}
	if _, dropped := InstructionBlockCut(files, len(text)); dropped != 0 {
		t.Fatalf("a file that fits dropped %d lines", dropped)
	}

	block, dropped = InstructionBlockCut(files, 1200)
	if block != InstructionBlock(files, 1200) {
		t.Fatal("the counting call built a different block from the prompt's")
	}
	kept := strings.Count(block, "a line of the head\n") + strings.Count(block, "a line of the end")
	if want := 210 - kept; dropped != want || dropped == 0 {
		t.Fatalf("dropped = %d, want %d (the block kept %d of 210 body lines)", dropped, want, kept)
	}

	// A file none of which fits loses every line it has.
	two := []Instruction{{Display: "outer.md", Text: "x\ny\nz\n"}, {Display: "AGENTS.md", Text: text}}
	if _, dropped := InstructionBlockCut(two, len(text)); dropped != 3 {
		t.Fatalf("dropped = %d, want the outer file's 3 lines", dropped)
	}
}

// checkout is a repository root in a temporary directory holding the given
// files, so the walk the check makes ends there and nowhere on the machine.
func checkout(t *testing.T, files map[string]string) string {
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

func TestGonePaths_CountsWhatTheFileNamesThatIsNotThere(t *testing.T) {
	root := checkout(t, map[string]string{
		"internal/store/store.go": "package store\n",
		"docs/guide.md":           "# Guide\n",
		"cmd/tool/main.go":        "package main\n",
		"AGENTS.md": strings.Join([]string{
			"The store is `internal/store/store.go`; the guide is [docs/guide.md](docs/guide.md#setup).",
			"The cache lived in `internal/cache/cache.go`, and its notes in docs/cache.md.",
			"Run scripts/lint.sh before you push, and see cmd/tool/main.go and cmd/old/main.go.",
			"Named twice is counted once: internal/cache/cache.go. Emphasis is not a pattern: **docs/old-design.md**.",
			// Not paths in this checkout: a shape, a URL, an import path, a
			// bare name, an absolute path, a path written from elsewhere, and
			// a file type.
			"Each `internal/<pkg>` has a doc.go; see https://example.com/docs/x.md and github.com/acme/tool/internal/x.",
			"Edit start.go or /etc/hosts or tools/tools.go; captures are .txt and internal/... is everything.",
		}, "\n"),
	})
	files := Instructions(root, "")
	if len(files) != 1 {
		t.Fatalf("files = %d, want the one AGENTS.md", len(files))
	}
	// internal/cache/cache.go, docs/cache.md, scripts/lint.sh,
	// cmd/old/main.go, docs/old-design.md.
	if got := GonePaths(files, root); got != 5 {
		t.Fatalf("gone = %d, want 5", got)
	}

	check := CheckInstructions(files, root, 7)
	if check.File != "AGENTS.md" || check.Gone != 5 || check.Dropped != 7 || check.Modified.IsZero() {
		t.Fatalf("check = %+v", check)
	}
}

// The check reads the project's own files and names the nearest of them; a
// file from outside the project, the user's own, is neither.
func TestCheckInstructions_IsTheProjectsOwnFiles(t *testing.T) {
	root := checkout(t, map[string]string{"CLAUDE.md": "See docs/gone.md.\n"})
	user := filepath.Join(t.TempDir(), "instructions.md")
	if err := os.WriteFile(user, []byte("My notes are in docs/mine.md.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	check := CheckInstructions(Instructions(root, user), root, 0)
	if check.File != "CLAUDE.md" || check.Gone != 1 {
		t.Fatalf("check = %+v, want CLAUDE.md naming one path that is gone", check)
	}

	shhh := checkout(t, map[string]string{".shhh/project.md": "Notes.\n", "AGENTS.md": "Other notes.\n"})
	if check := CheckInstructions(Instructions(shhh, ""), shhh, 0); check.File != ".shhh/project.md" {
		t.Fatalf("file = %q, want the state directory's own", check.File)
	}

	bare := checkout(t, nil)
	if check := CheckInstructions(Instructions(bare, user), bare, 0); check != (InstructionCheck{}) {
		t.Fatalf("a checkout with no instruction file was checked: %+v", check)
	}
}
