package components

import (
	"strings"
	"testing"
)

// TestPlannedCard_ADeleteSaysDelete: a step that removes a file is not the
// pencil of one that rewrites it, so the two stay apart with the colours
// stripped.
func TestPlannedCard_ADeleteSaysDelete(t *testing.T) {
	edit := stripANSI(PlannedStep{Number: 1, Title: "Edit", Writes: true, File: "loop.go"}.mark())
	if edit != "✎ loop.go" {
		t.Errorf("an edit reads %q, want the pencil and its file", edit)
	}
	del := stripANSI(PlannedStep{Number: 2, Title: "Drop", Writes: true, Delete: true, File: "shim.go", More: 1}.mark())
	if del != "delete shim.go +1" {
		t.Errorf("a delete reads %q, want the word and its file", del)
	}
	if strings.Contains(del, "✎") {
		t.Errorf("a delete wears no pencil: %q", del)
	}
	if bare := stripANSI(PlannedStep{Writes: true, Delete: true}.mark()); bare != "delete" {
		t.Errorf("a delete that names no file still says delete, got %q", bare)
	}
}
