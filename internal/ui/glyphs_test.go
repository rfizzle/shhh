package ui

// The glyph set is closed (docs/interface/principles.md#closed-vocabularies).
//
// The design system's guideline pages carry twenty-one glyphs, and the whole
// value of that number is that it is one somebody learns once. A twenty-second
// mark costs every reader of every screen a lookup, and it never arrives as a
// decision — it arrives as one line in one renderer that wanted to say
// something slightly different from what the kit could say, and then the next
// one copies it.
//
// So the source is read, the way the key register's own test reads it. Every
// rune a UI package can put on the screen is checked against three sets: the
// kit, the drawing kit the cards and meters are made of, and the plain
// typography a sentence is made of. Anything else has to be written down in
// docs/interface/departures.md with its reason and then listed here, which is
// the whole of the cost: a mark the product keeps for a reason is a mark the
// next reader can find the reason for.
//
// It reads string literals rather than rendered output, and that is on
// purpose. Goldens only cover the states a fixture happens to build, and the
// mark this test exists to catch is precisely the one that shows up in the
// state nobody goldened. Comments are not read: a comment about `⏎` is prose
// about a mark, not the mark.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// kitGlyphs is guidelines/glyphs-states, -tools and -session: the twenty-one.
// `$` and the ASCII in them need no listing here — this test is about the
// runes a reader has to be taught.
var kitGlyphs = map[rune]string{
	// States.
	'✓': "done",
	'✗': "failed",
	'▸': "running, and a folded entry",
	'⊘': "denied or skipped",
	'⚠': "risk",
	'✦': "the classifier deciding",
	'·': "queued",
	// Tool kinds.
	'⚙': "a read-only tool",
	'✎': "an edit or a write",
	'⇄': "a call to a server nobody vouched for",
	'⛁': "a published report page",
	'◇': "a sub-agent",
	'✻': "the model thinking",
	'≡': "a reading of the session",
	// Session and structure.
	'⛨': "containment",
	'⏵': "permissive mode",
	'⏸': "gated mode",
	'❯': "you, and the cursor",
	'▎': "this row changed the machine",
	'▾': "expanded",
}

// drawingGlyphs is guidelines/glyphs-drawing: the material cards, rules,
// meters and spinners are made of. It is not the glyph set — nothing here is
// a mark a reader reads, and a card drawn out of it says nothing at all.
var drawingGlyphs = map[rune]string{
	'─': "rule", '│': "pane edge",
	'╭': "card corner", '╮': "card corner", '╯': "card corner", '╰': "card corner",
	'├': "card tee", '┤': "card tee", '┬': "card tee", '┴': "card tee",
	'┌': "corner", '┐': "corner", '└': "corner", '┘': "corner", '┼': "cross",
	'┄': "a field's label inside a card",
	'▌': "the two-column gutter rail",
	'┃': "the scroll thumb",
	'▰': "a meter's filled cell", '▱': "a meter's empty cell",
	'▁': "sparkline", '▂': "sparkline", '▃': "sparkline", '▄': "sparkline",
	'▅': "sparkline", '▆': "sparkline", '▇': "sparkline", '█': "sparkline, and the painted cursor",
	'⠋': "spinner", '⠙': "spinner", '⠹': "spinner", '⠸': "spinner",
	'⠼': "spinner", '⠴': "spinner", '⠦': "spinner", '⠧': "spinner",
}

// typography is what a sentence is made of rather than what a mark is made
// of: separators, dashes, the arrows an overflow row and a link use, and the
// signs a figure carries. A reader does not learn these; they read them.
var typography = map[rune]string{
	'—': "the em dash, and an empty duration field",
	'–': "the en dash, in a range",
	'…': "what a clip or a fold ends on",
	'↑': "up, on an overflow row and a key",
	'↓': "down, on an overflow row and a key",
	'→': "a link, and a value becoming another value",
	'←': "back",
	'↔': "a span between two values",
	'−': "the minus sign of a diffstat",
	'×': "a multiplier, as in [ctrl+c] ×2",
	'∞': "an unbounded count",
	'≥': "at least",
	'⌘': "the command key, where a terminal names it",
	'␛': "escape, written out where a byte is being shown as text",
	'›': "a menu path, where a sentence quotes another product's menus",
}

// additions are the marks the binary draws that the guideline pages do not
// list. Every one of them is argued in docs/interface/departures.md, and the
// anchor is the entry that argues it: a mark with nowhere to point at is a
// mark that has not been decided, only written.
var additions = map[rune]string{
	'●': "departures.md#the-current-one-is-marked-and-four-other-marks-the-pages-do-not-list",
	'○': "departures.md#the-current-one-is-marked-and-four-other-marks-the-pages-do-not-list",
	'⋮': "departures.md#the-current-one-is-marked-and-four-other-marks-the-pages-do-not-list",
	'↵': "departures.md#the-current-one-is-marked-and-four-other-marks-the-pages-do-not-list",
	'↺': "departures.md#the-backlog-runs-row-has-no-artboard",
	'⟨': "departures.md#the-paste-fold-is-written-in-angle-quotes",
	'⟩': "departures.md#the-paste-fold-is-written-in-angle-quotes",
	'▣': "departures.md#the-attachment-chips-mark-what-kind-of-file-is-staged",
	'▤': "departures.md#the-attachment-chips-mark-what-kind-of-file-is-staged",
	'▀': "departures.md#two-surfaces-draw-with-blocks-rather-than-in-them",
	'░': "departures.md#two-surfaces-draw-with-blocks-rather-than-in-them",
	'▒': "departures.md#two-surfaces-draw-with-blocks-rather-than-in-them",
	'▓': "departures.md#two-surfaces-draw-with-blocks-rather-than-in-them",
	'↳': "departures.md#a-line-that-answers-the-one-above-hangs-from-it",
}

// screenSources is every tree this test reads, relative to internal/ui. The
// whole of internal/ui is read: every package under it draws, including the
// one-shot's own surface, the markdown renderer and the image rasteriser. A
// package outside it is listed when it composes text a surface draws as it
// stands — the sub-agent supervisor writes a child's transcript rows, which
// the attached view renders without rewording — because a mark written one
// package away from the renderer is still a mark the reader has to learn. A
// package that writes screen text and is not listed is one whose marks
// nobody has decided.
var screenSources = []string{".", "../subagent"}

func TestTheGlyphSetIsClosed(t *testing.T) {
	fset := token.NewFileSet()
	for _, root := range screenSources {
		walkGlyphs(t, fset, root)
	}
}

// walkGlyphs checks every string literal in the non-test Go files under root.
func walkGlyphs(t *testing.T, fset *token.FileSet, root string) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		switch {
		case err != nil:
			return err
		case info.IsDir():
			if info.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		case !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go"):
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
			// A literal that is not text is not a mark: the terminal
			// capability probes write raw bytes, and a string terminator is a
			// wire protocol with nothing drawn in it.
			if uerr != nil || !utf8.ValidString(v) {
				return true
			}
			for _, r := range v {
				if r < 0x80 || !unicode.IsPrint(r) || known(r) {
					continue
				}
				t.Errorf("%s: %q draws %q (U+%04X), which is not in the glyph kit, "+
					"the drawing kit or the recorded additions. Use a mark the kit "+
					"already has, or record it in docs/interface/departures.md and "+
					"list it in additions here.",
					fset.Position(lit.Pos()), v, string(r), r)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// known reports whether a rune has been accounted for somewhere.
func known(r rune) bool {
	if _, ok := kitGlyphs[r]; ok {
		return true
	}
	if _, ok := drawingGlyphs[r]; ok {
		return true
	}
	if _, ok := typography[r]; ok {
		return true
	}
	_, ok := additions[r]
	return ok
}

// Every addition points at a document, and the four sets do not overlap: a
// mark listed twice is a mark with two meanings, which is the thing the
// closed set exists to prevent.
func TestTheGlyphSetsDoNotOverlap(t *testing.T) {
	seen := map[rune]string{}
	for name, set := range map[string]map[rune]string{
		"the kit": kitGlyphs, "the drawing kit": drawingGlyphs,
		"typography": typography, "the recorded additions": additions,
	} {
		for r := range set {
			if was, dup := seen[r]; dup {
				t.Errorf("%q is in %s and in %s", string(r), was, name)
			}
			seen[r] = name
		}
	}
	if len(kitGlyphs) != 20 {
		t.Errorf("the kit lists %d non-ASCII glyphs; the guideline pages carry "+
			"twenty-one, of which `$` is the one that is ASCII", len(kitGlyphs))
	}
}
