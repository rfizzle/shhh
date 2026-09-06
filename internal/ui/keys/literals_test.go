package keys

// No key literals outside the register (
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
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
// Two things are deliberately not policed here, and the reasons are
// different.
//
// Bare letters and navigation keys written as plain strings are not: `j`,
// `k`, `up`, `enter`, `backspace` and their like appear as ordinary
// characters all over this tree — in a textarea's own handling, in a filter
// row reading text, in a digit jump — and a test that could not tell those
// from a key offer would be a test people turn off. The chords are what is
// worth policing here: no sentence produces one, so a chord in the source is
// always somebody answering a key. The second test below catches the bare
// letters where it matters, which is where one is *compared against a
// keystroke*.
//
// Prose is not, and neither are test files: `/help` explains a chord in a
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
	"regexp"
	"slices"
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

// A keystroke is matched against the register, never against a letter (
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// The test above reads strings; this one reads comparisons, which is the
// half that was actually drifting. Sixty handlers under the movement hints
// asked `pressed == "j"`, so a keymap file that moved `screen.move` changed
// the footer and nothing else: the hint and the handler were two facts
// again, exactly as they were before the register existed.
//
// So a keystroke may not be compared to a literal. `keys.Is` says which
// binding a press is, `keys.Step` says which half of a pair, and both read
// the declaration a file can move. The shape is what is refused, not the
// letter: `pressed == "j"`, and `switch pressed { case "j": }`, and the same
// with `msg.String()` written out in place.
//
// It is a comparison against a *keystroke* and not against any string, which
// is what leaves the slash-command argument switches alone: `/verbosity low`
// and `/theme dark` switch on a word the reader typed as an argument, which
// is text and not a key, and no register has anything to say about it.
//
// A range is refused as well as an equality. The plan card used to take its
// row from `pressed >= "1" && pressed <= "5"`, which is the same fact about
// the same five keystrokes written with a different operator, and a check
// that only knew about `==` would have watched it go by.
func TestNoKeystrokeIsComparedToALiteral(t *testing.T) {
	compares := []token.Token{token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ}
	fset := token.NewFileSet()
	err := filepath.Walk("..", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		named := keystrokeNames(f)
		report := func(n ast.Node, text string) {
			t.Errorf("%s: a keystroke is compared to %s; match it against a binding "+
				"in internal/ui/keys instead", fset.Position(n.Pos()), text)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BinaryExpr:
				if !slices.Contains(compares, n.Op) {
					return true
				}
				if lit, ok := stringLit(n.Y); ok && keystroke(n.X, named) {
					report(n, lit)
				}
				if lit, ok := stringLit(n.X); ok && keystroke(n.Y, named) {
					report(n, lit)
				}
			case *ast.SwitchStmt:
				if n.Tag == nil || !keystroke(n.Tag, named) {
					return true
				}
				for _, stmt := range n.Body.List {
					clause, ok := stmt.(*ast.CaseClause)
					if !ok {
						continue
					}
					for _, expr := range clause.List {
						if lit, ok := stringLit(expr); ok {
							report(expr, lit)
						}
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A key row is built, never typed (
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// The two tests above police chords and comparisons. This one polices the
// third shape the drift took: a hint row written out as prose — `enter run ·
// tab complete · ↑↓ move · esc dismiss` — where every spelling is a bare key
// no chord test can see and no handler is being compared to. Five rows in the
// product were written that way, and they were the five that had never been
// bracketed either, because a row nothing reads from the register is a row
// nothing holds to the product's notation.
//
// What is refused is a literal that opens with a key and then names an act:
// that shape is a key row and nothing else says it. Prose about a key does
// not open with one, and a row built from the register does not reach the
// source as a literal at all.
var typedKeyRow = regexp.MustCompile(
	`^(enter|esc|tab|↑↓)[a-z/↑↓ ]* (run|complete|move|dismiss|apply|cancel|` +
		`choose|select|jump|save|back)`)

func TestNoKeyRowIsTypedByHand(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.Walk("..", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return err
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
			if uerr != nil || !typedKeyRow.MatchString(v) {
				return true
			}
			t.Errorf("%s: %q is a key row written by hand; build it from a "+
				"binding in this package", fset.Position(lit.Pos()), v)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// stringLit is the literal an expression is, where it is one.
func stringLit(n ast.Node) (string, bool) {
	lit, ok := n.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return strconv.Quote(v), true
}

// keystroke reports that an expression is a key press reduced to its string:
// a `msg.String()` written out in place, or a name this file bound one to.
// Anything else is a string about something other than the keyboard.
func keystroke(n ast.Node, named map[string]bool) bool {
	switch n := n.(type) {
	case *ast.Ident:
		return named[n.Name]
	case *ast.CallExpr:
		return pressString(n)
	}
	return false
}

// pressString reports that a call is a keystroke being reduced to its string.
func pressString(n *ast.CallExpr) bool {
	sel, ok := n.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "String" || len(n.Args) != 0 {
		return false
	}
	recv, ok := sel.X.(*ast.Ident)
	return ok && (recv.Name == "msg" || recv.Name == "key" || recv.Name == "msgKey")
}

// keystrokeNames is what a keystroke is called in one file: `pressed`, which
// is the name this tree gives it everywhere, and whatever else was assigned
// a `msg.String()` in that file.
//
// The second half is why the check does not rest on a spelling. A handler
// that wrote `k := msg.String()` and then compared `k` would be doing the
// thing this test refuses, under a name the test had never heard of; the
// assignment is what says it is a keystroke, so the assignment is what is
// read. A parameter is still matched by name — a function taking a press
// takes it as `pressed` here, and following one across a call is a type
// checker's job rather than a gate's.
func keystrokeNames(f *ast.File) map[string]bool {
	named := map[string]bool{"pressed": true}
	// To a fixed point, because a keystroke can be handed along: `k :=
	// pressed` is the same value under a second name, and a pass that read
	// each assignment once would learn about it only if it happened to come
	// after the one that named it.
	for grew := true; grew; {
		grew = false
		ast.Inspect(f, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for i, rhs := range assign.Rhs {
				if i >= len(assign.Lhs) || !isPress(rhs, named) {
					continue
				}
				if lhs, ok := assign.Lhs[i].(*ast.Ident); ok && !named[lhs.Name] {
					named[lhs.Name], grew = true, true
				}
			}
			return true
		})
	}
	return named
}

// isPress reports that an expression yields a keystroke: the call that
// reduces one to its string, or a name already known to hold one.
func isPress(n ast.Expr, named map[string]bool) bool {
	switch n := n.(type) {
	case *ast.CallExpr:
		return pressString(n)
	case *ast.Ident:
		return named[n.Name]
	}
	return false
}
