package project

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// rubricDoc is the document the rubric's list lives in, from this package's
// own directory: the test binary runs where its package is.
const rubricDoc = "../../docs/capabilities/coding-agent.md"

const (
	rubricBegin = "<!-- BEGIN generated instruction rubric — written by `make docs` from internal/project/instruction_rubric.md; edit that, not this. -->"
	rubricEnd   = "<!-- END generated instruction rubric -->"
)

// rubricIn is doc with the region between the markers replaced by the
// rubric the assessment offer is built from, and whether that changed
// anything.
func rubricIn(doc string) (string, bool, error) {
	i := strings.Index(doc, rubricBegin)
	j := strings.Index(doc, rubricEnd)
	if i < 0 || j < i {
		return "", false, errors.New("the instruction rubric markers are not in the document")
	}
	region := rubricBegin + "\n\n" + strings.TrimSpace(InstructionRubric) + "\n\n"
	out := doc[:i] + region + doc[j:]
	return out, out != doc, nil
}

// The documentation's list and the assessment's questions are one text: a
// person who edits the rubric and does not run the generator finds out
// here, rather than the model being asked one rubric and the reader shown
// another.
func TestReference_InstructionRubricIsCurrent(t *testing.T) {
	raw, err := os.ReadFile(rubricDoc)
	if err != nil {
		t.Fatal(err)
	}
	out, stale, err := rubricIn(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !stale {
		return
	}
	if os.Getenv("SHHH_UPDATE_DOCS") == "" {
		t.Fatalf("%s no longer carries the instruction rubric the assessment is built from — run: make docs", rubricDoc)
	}
	if err := os.WriteFile(rubricDoc, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The list is read item by item, a wrapped item joined back into one line,
// so a question the rubric holds is a question the prompt asks whole.
func TestRubricItems_JoinsEachItemsWrappedLines(t *testing.T) {
	items := RubricItems()
	if len(items) < 6 {
		t.Fatalf("items = %d, want every question of the rubric: %q", len(items), items)
	}
	for _, it := range items {
		if strings.Contains(it, "\n") || strings.HasPrefix(it, "-") || strings.Contains(it, "  ") {
			t.Fatalf("item %q is not one joined line", it)
		}
	}
	if items[0] != "whether it says how to build, test and lint the project, and whether those commands exist" {
		t.Fatalf("first item = %q", items[0])
	}
}
