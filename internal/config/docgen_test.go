package config

import (
	"os"
	"strings"
	"testing"
)

// referenceDoc is the document the reference section lives in, from this
// package's own directory. The test binary runs where its package is, so the
// path is relative to that rather than to wherever the suite was started.
const referenceDoc = "../../docs/capabilities/configuration.md"

// The document and the table cannot disagree about a default. A person who
// adds a key and does not run the generator finds out here rather than
// leaving the reference section quietly describing a machine that no longer
// exists.
func TestReference_DocumentIsCurrent(t *testing.T) {
	stale, err := WriteReference(referenceDoc, updatingDocs())
	if err != nil {
		t.Fatal(err)
	}
	if stale && !updatingDocs() {
		t.Errorf("%s no longer matches the settings table — run: make docs", referenceDoc)
	}
}

// updatingDocs reports whether this run writes the generated sections rather
// than checking them. It is an environment variable rather than a flag for
// the reason the goldens' is: a flag passed to `go test ./...` fails in every
// package that does not declare it.
func updatingDocs() bool { return os.Getenv("SHHH_UPDATE_DOCS") != "" }

// Every key reaches the reference, and its default and its sentence reach it
// with the key. A row that lost its last column would still be a table.
func TestReference_HoldsEveryKey(t *testing.T) {
	ref := Reference()
	for _, s := range settings {
		group := s.Group()
		key := strings.ReplaceAll(strings.TrimPrefix(s.Key, group+"."), RoleWildcard, "<role>")
		if !strings.Contains(ref, "`"+key+"`") {
			t.Errorf("%s is not in the reference", s.Key)
		}
		if !strings.Contains(ref, s.Desc) {
			t.Errorf("%s reaches the reference without its sentence", s.Key)
		}
	}
	for _, group := range []string{"provider", "behavior", "sandbox", "web", "lsp",
		"appearance", "history", "reports", "agents", "summary", "secrets", "mcp",
		"prompts", "todo"} {
		if !strings.Contains(ref, "**`["+group+"]`**") {
			t.Errorf("the [%s] table is not in the reference", group)
		}
	}
}

// A document with no markers is an error rather than an append: a table
// spliced under whichever heading happened to be last would be worse than no
// table.
func TestReference_RefusesADocumentWithNoMarkers(t *testing.T) {
	if _, _, err := ReferenceIn("# Configuration\n\nnothing here\n"); err == nil {
		t.Fatal("a document with no markers should be refused")
	}
}

// What is outside the markers is a person's and survives a regeneration.
func TestReference_LeavesTheProseAround(t *testing.T) {
	doc := "before\n" + referenceBegin + "\nOLD REGION\n" + referenceEnd + "\nafter\n"
	out, changed, err := ReferenceIn(doc)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if !strings.HasPrefix(out, "before\n") || !strings.HasSuffix(out, "\nafter\n") {
		t.Fatalf("the prose around the region did not survive:\n%s", out)
	}
	if strings.Contains(out, "OLD REGION") {
		t.Error("the old region survived the regeneration")
	}
}
