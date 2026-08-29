package keys

// No key literals outside the register (S-153, DESIGN-TUI.md §7d).
//
// Every other test in this package checks the register against itself. This
// one checks the tree against the register, which is the half that actually
// closes Finding 3: a key written as a string in a handler somewhere is a key
// that can disagree with the hint offering it, and no amount of care about
// the register prevents the twenty-first file from doing it again.
//
// So the source is read. A chord written down anywhere under internal/ui
// outside this package fails, and the fix is always the same — declare it
// here and match against the binding.
//
// Two things are deliberately not policed, and the reasons are different.
//
// Bare letters and navigation keys are not: `j`, `k`, `up`, `enter`,
// `backspace` and their like appear as ordinary characters all over this tree
// — in a textarea's own handling, in a filter row reading text, in a digit
// jump — and a test that could not tell those from a key offer would be a
// test people turn off. The chords are what is worth policing: no sentence
// produces one, so a chord in the source is always somebody answering a key.
//
// Prose is not, and neither are test files: `/help` explains ctrl+g in a
// paragraph, a notice names the chord it is about, and a test says which key
// it pressed when it fails. Those are sentences, and what this test looks for
// is a *key row segment* — a chord, alone or with the two or three words a
// hint puts beside it. It cannot see a chord buried mid-sentence in a
// non-test file, which is the one gap; /help's own completeness is asserted
// separately, beside the text (chat/help_test.go).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// chord is a keystroke no sentence can produce: something with a modifier in
// it. These are the ones a surface answers deliberately.
func chord(s string) bool {
	for _, prefix := range []string{"ctrl+", "alt+", "shift+", "meta+", "super+"} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// keyRowSegment is a string that is a key rather than a sentence about one: a
// chord on its own, or a chord and the handful of words a hint row puts
// beside it, with none of the punctuation prose carries.
func keyRowSegment(s string) bool {
	fields := strings.Fields(s)
	if len(fields) == 0 || len(fields) > 3 || !chord(fields[0]) {
		return false
	}
	return !strings.ContainsAny(s, ".,;:—%")
}

func TestNoChordLiteralsOutsideTheRegister(t *testing.T) {
	root := ".."
	fset := token.NewFileSet()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		// This package is where they are supposed to be, and a golden file's
		// expectations are a rendering, not a handler.
		if filepath.Base(filepath.Dir(path)) == "keys" ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v, uerr := strconv.Unquote(lit.Value)
			if uerr != nil || !keyRowSegment(v) {
				return true
			}
			t.Errorf("%s: %q is written here rather than declared in internal/ui/keys",
				fset.Position(lit.Pos()), v)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
