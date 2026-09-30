package project

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// grammarDoc is the document the grammar's section lives in, from this
// package's own directory: the test binary runs where its package is.
const grammarDoc = "../../docs/capabilities/containment.md"

const (
	grammarBegin = "<!-- BEGIN generated toolchain grammar — written by `make docs` from internal/project/toolchain_grammar.md; edit that, not this. -->"
	grammarEnd   = "<!-- END generated toolchain grammar -->"
)

// grammarIn is doc with the region between the markers replaced by the
// grammar the drafter is handed, and whether that changed anything.
func grammarIn(doc string) (string, bool, error) {
	i := strings.Index(doc, grammarBegin)
	j := strings.Index(doc, grammarEnd)
	if i < 0 || j < i {
		return "", false, errors.New("the toolchain grammar markers are not in the document")
	}
	region := grammarBegin + "\n\n" + strings.TrimSpace(ToolchainGrammar) + "\n\n"
	out := doc[:i] + region + doc[j:]
	return out, out != doc, nil
}

// The documentation's section and the drafter's instruction are one text: a
// person who edits the grammar and does not run the generator finds out
// here, rather than the model being told one grammar and the reader another.
func TestReference_ToolchainGrammarIsCurrent(t *testing.T) {
	raw, err := os.ReadFile(grammarDoc)
	if err != nil {
		t.Fatal(err)
	}
	out, stale, err := grammarIn(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !stale {
		return
	}
	if os.Getenv("SHHH_UPDATE_DOCS") == "" {
		t.Fatalf("%s no longer carries the toolchain grammar the drafter is handed — run: make docs", grammarDoc)
	}
	if err := os.WriteFile(grammarDoc, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A document with no markers is refused rather than appended to: a grammar
// spliced under whichever heading happened to be last would be worse than
// none.
func TestReference_ToolchainGrammarNeedsItsMarkers(t *testing.T) {
	if _, _, err := grammarIn("# Containment\n\nnothing here\n"); err == nil {
		t.Fatal("a document with no markers should be refused")
	}
}
