// Package golden captures the rendered output of a TUI surface in a
// checked-in file so a layout regression is a failing test rather than
// something noticed three commits later (S-096,
// docs/interface/principles.md#one-grid).
//
// The component tests around it assert substrings — that a row contains
// "denied · you", that the rail carries a CONTEXT block. Substrings are blind
// to exactly the thing the column grid is about: a verb field that grew by
// one column, a right edge that drifted, a block that moved above another. A
// golden file is the whole render, so any of those shows up as a diff.
//
// A golden holds one surface at one width in one palette, and is written in
// two blocks: the render with ANSI stripped, which is the layout a reviewer
// reads in the diff, and the same bytes with ESC written as ␛, which is where
// a colour assignment changing shows up. Both are compared, so neither a
// moved column nor a recoloured glyph can land unnoticed.
//
// Only test files import this package; it lives outside them because the two
// hosts with surfaces to capture — internal/ui/components and
// internal/ui/chat — would otherwise each carry a copy.
package golden

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Dir is where goldens live, relative to the package under test — `go test`
// runs a test binary in its own package directory, so the same path serves
// every host.
const Dir = "testdata/golden"

// updateFlag rewrites the goldens from the current render instead of
// comparing against them. Passing it to `go test ./...` fails in packages
// that do not define the flag, so SHHH_UPDATE_GOLDEN=1 does the same job for
// a whole-tree run.
var updateFlag = flag.Bool("update-golden", false,
	"rewrite the checked-in golden renders from the current output")

// updating reports whether this run rewrites goldens rather than checking
// them.
func updating() bool { return *updateFlag || os.Getenv("SHHH_UPDATE_GOLDEN") != "" }

// escSymbol stands in for ESC in the ansi block. It is reversible and it
// keeps the file greppable, which a raw 0x1b does not.
const escSymbol = "␛"

// Panel is one labelled render inside a golden. A surface with several states
// — every activity row kind, the three diff modes — captures them as panels
// of one file rather than as a file each, so the states are read side by side
// in the diff the way the design-system artboards show them.
type Panel struct {
	Label string
	View  string
}

// Case is what a golden records: which surface, rendered how wide, in which
// palette, and the panels themselves.
type Case struct {
	// Surface names what was rendered, for the file's header.
	Surface string
	// Width is the column count the panels were rendered at. The header
	// states it so a reviewer knows what the right edge should be.
	Width int
	// Mono says the render used the two-grey palette (S-095). It picks the
	// `.mono` file rather than being a field the caller can get wrong: every
	// surface is captured in both palettes, and the pair is named for it.
	Mono   bool
	Panels []Panel
}

// asserted is every golden this run touched, so Run can tell a stale file
// from a live one.
var asserted struct {
	sync.Mutex
	paths map[string]bool
}

func record(path string) {
	asserted.Lock()
	defer asserted.Unlock()
	if asserted.paths == nil {
		asserted.paths = map[string]bool{}
	}
	asserted.paths[path] = true
}

// Path is the file a golden lands in: the mono variant of a surface sits
// beside its coloured one under the same name.
func Path(name string, mono bool) string {
	if mono {
		name += ".mono"
	}
	return filepath.Join(Dir, name+".txt")
}

// Assert compares one surface's render against its checked-in golden, or
// rewrites it when the run is updating. A missing golden is a failure with
// the flag to run rather than a silently created file: a new surface should
// be reviewed the first time it lands, not the second.
func Assert(t *testing.T, name string, c Case) {
	t.Helper()
	path := Path(name, c.Mono)
	record(path)
	want := Format(name, c)

	if updating() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("golden %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatalf("golden %s: %v", path, err)
		}
		return
	}

	have, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s is missing (%v)\nrun: go test %s -update-golden",
			path, err, currentPackage())
		return
	}
	if string(have) == want {
		return
	}
	t.Errorf("%s renders differently than %s:\n%s\nif the change is intended: go test %s -update-golden",
		c.Surface, path, firstDifference(string(have), want), currentPackage())
}

// Format lays out a golden file: a header naming what is in it, the layout
// block, then the ansi block. Nothing parses this back — comparison is over
// the whole file — so the header is free to explain itself.
func Format(name string, c Case) string {
	palette := "color"
	if c.Mono {
		palette = "mono (two greys, S-095)"
	}
	body := renderPanels(c.Panels)

	var b strings.Builder
	fmt.Fprintf(&b, "# golden:  %s\n", name)
	fmt.Fprintf(&b, "# surface: %s\n", c.Surface)
	fmt.Fprintf(&b, "# width:   %d columns\n", c.Width)
	fmt.Fprintf(&b, "# palette: %s\n", palette)
	b.WriteString("#\n")
	b.WriteString("# Generated by go test -update-golden; do not edit by hand.\n")
	b.WriteString("# The layout block is the render with ANSI stripped — read it for the\n")
	b.WriteString("# columns. The ansi block is the same bytes with ESC written as " + escSymbol + " — read\n")
	b.WriteString("# it for the colour assignments. Trailing spaces are significant in both.\n")
	b.WriteString("\n" + sectionRule("layout") + "\n")
	b.WriteString(ansi.Strip(body) + "\n")
	b.WriteString("\n" + sectionRule("ansi") + "\n")
	b.WriteString(escape(body) + "\n")
	return b.String()
}

// sectionRule is a block separator wide enough to stand out in a diff and
// narrow enough to fit beside the widest surface captured (130 columns).
func sectionRule(label string) string {
	rule := "── " + label + " "
	return rule + strings.Repeat("─", max(0, 60-len([]rune(rule))))
}

// renderPanels joins a case's panels, each under its own label. A single
// unlabelled panel renders as bare output, so a one-state surface has no
// chrome around it.
func renderPanels(panels []Panel) string {
	var out []string
	for i, p := range panels {
		if i > 0 {
			out = append(out, "")
		}
		if p.Label != "" {
			out = append(out, "· "+p.Label)
		}
		out = append(out, strings.Split(p.View, "\n")...)
	}
	return strings.Join(out, "\n")
}

// escape makes the escape bytes visible without losing them.
func escape(s string) string { return strings.ReplaceAll(s, "\x1b", escSymbol) }

// firstDifference reports where the render parted company with the golden,
// as a line number and the two lines quoted — %q keeps a trailing space or a
// stray escape visible, which is usually the whole story.
func firstDifference(have, want string) string {
	hl, wl := strings.Split(have, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(hl) || i < len(wl); i++ {
		h, w := lineAt(hl, i), lineAt(wl, i)
		if h == w {
			continue
		}
		return fmt.Sprintf("  line %d\n    golden: %q\n    render: %q", i+1, h, w)
	}
	return "  the files differ only in their trailing newline"
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "<end of file>"
}

// currentPackage is the directory the test binary runs in, for the message
// telling the reader what to re-run.
func currentPackage() string {
	wd, err := os.Getwd()
	if err != nil {
		return "..."
	}
	if i := strings.Index(wd, "/internal/"); i >= 0 {
		return "." + wd[i:]
	}
	return filepath.Base(wd)
}

// Run wraps a host package's TestMain so a golden nobody asserts any more is
// reported rather than left to rot: a surface that was renamed or deleted
// should take its captures with it. A filtered run (-run) touches only some
// of them, so the check stands down; an updating run deletes them.
func Run(m *testing.M) int {
	code := m.Run()
	if code != 0 {
		return code
	}
	if f := flag.Lookup("test.run"); f != nil && f.Value.String() != "" {
		return code
	}
	stale, err := orphans()
	if err != nil || len(stale) == 0 {
		return code
	}
	if updating() {
		for _, path := range stale {
			_ = os.Remove(path)
		}
		return code
	}
	fmt.Fprintf(os.Stderr,
		"golden: %d checked-in render(s) no longer belong to any test:\n  %s\nremove them, or run: go test %s -update-golden\n",
		len(stale), strings.Join(stale, "\n  "), currentPackage())
	return 1
}

// orphans is every file under Dir that this run did not assert.
func orphans() ([]string, error) {
	entries, err := os.ReadDir(Dir)
	if err != nil {
		return nil, err
	}
	asserted.Lock()
	defer asserted.Unlock()
	var stale []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(Dir, e.Name())
		if !asserted.paths[path] {
			stale = append(stale, path)
		}
	}
	sort.Strings(stale)
	return stale, nil
}
