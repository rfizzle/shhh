package markdown

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// plainRows is a layout's rows as a reader sees them, padding off.
func plainRows(rows []string) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = strings.TrimRight(ansi.Strip(row), " ")
	}
	return out
}

// A fenced block is headed by a row naming its language at the block's own
// indent, one row above it, and the renderer says which rows each block took
// — through a list item's hang and a quote's rail, which shift and prefix the
// rows without the block knowing.
func TestBlocks_ReportTheRowsEachFenceOccupies(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
		// fences is each block's heading row and its code rows, [start, end).
		fences [][3]int
	}{
		{
			name:   "a tagged block",
			src:    "Run it:\n\n```sh\nmake build\nmake test\n```\n",
			want:   []string{"  Run it:", "", "    sh", "    make build", "    make test"},
			fences: [][3]int{{2, 3, 5}},
		},
		{
			name:   "a bare block is called code",
			src:    "```\nx\n```\n",
			want:   []string{"    code", "    x"},
			fences: [][3]int{{0, 1, 2}},
		},
		{
			name:   "two blocks one after the other",
			src:    "```go\na\n```\n```py\nb\n```\n",
			want:   []string{"    go", "    a", "", "    py", "    b"},
			fences: [][3]int{{0, 1, 2}, {3, 4, 5}},
		},
		{
			name:   "inside a list item",
			src:    "1. Build:\n\n   ```sh\n   make\n   ```\n2. Done.\n",
			want:   []string{"  1. Build:", "", "       sh", "       make", "", "  2. Done."},
			fences: [][3]int{{2, 3, 4}},
		},
		{
			name:   "inside a quote",
			src:    "> The test:\n>\n> ```go\n> func f() {}\n> ```\n",
			want:   []string{"  | The test:", "  |", "  |   go", "  |   func f() {}"},
			fences: [][3]int{{2, 3, 4}},
		},
		{
			name: "an indented block has no fence to head",
			src:  "Text.\n\n    indented\n",
			want: []string{"  Text.", "", "    indented"},
		},
		{
			name: "a fence that never closed is not a block yet",
			src:  "Text.\n\n```sh\nmake bu",
			want: []string{"  Text.", "", "    make bu"},
		},
		{
			name:   "an empty block is still headed",
			src:    "```sh\n```\n",
			want:   []string{"    sh"},
			fences: [][3]int{{0, 1, 1}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, fences := Layout(tc.src, Options{Width: 60, Mono: true})
			wants(t, plainRows(rows), tc.want)
			if len(fences) != len(tc.fences) {
				t.Fatalf("reported %d fences, want %d: %+v", len(fences), len(tc.fences), fences)
			}
			for i, f := range fences {
				w := tc.fences[i]
				if f.Index != i || f.Heading != w[0] || f.Start != w[1] || f.End != w[2] {
					t.Errorf("fence %d = %+v, want index %d heading %d rows [%d,%d)", i, f, i, w[0], w[1], w[2])
				}
			}
		})
	}
}

// A line too long for the pane folds onto more rows, and every one of them
// stays inside its block's range: the range is what a click and a drag read,
// and a fold that leaked out of it would hand the next row to nothing.
func TestBlocks_AFoldedLineStaysInItsBlock(t *testing.T) {
	long := strings.Repeat("x", 70)
	src := "```go\n" + long + "\nshort\n```\n\nafter\n"
	rows, fences := Layout(src, Options{Width: 40, Mono: true})
	if len(fences) != 1 {
		t.Fatalf("reported %+v", fences)
	}
	f := fences[0]
	var code strings.Builder
	for _, row := range plainRows(rows[f.Start:f.End]) {
		code.WriteString(strings.TrimPrefix(row, "    "))
	}
	if got := code.String(); got != long+"short" {
		t.Fatalf("the block's rows hold %q, want the folded line and the next", got)
	}
	if got := strings.TrimSpace(ansi.Strip(rows[f.End+1])); got != "after" {
		t.Fatalf("the row after the block's blank is %q, want the paragraph", got)
	}
}

// The copy by number counts the blocks the renderer heads, in the same
// order, so block n is the block under the n-th heading — a quoted block, a
// longer fence holding a shorter one, and an unclosed fence included.
func TestFenceTexts_CountTheBlocksLayoutHeads(t *testing.T) {
	src := "> ```go\n> quoted\n> ```\n\n" +
		"````md\n```sh\ninner\n```\n````\n\n" +
		"- item\n\n  ```py\n  \tx = 1\n  ```\n\n" +
		"```sh\nnever closed"
	texts := FenceTexts(src)
	want := []FenceText{
		{Lang: "go", Body: "quoted"},
		{Lang: "md", Body: "```sh\ninner\n```"},
		{Lang: "py", Body: "\tx = 1"},
	}
	if len(texts) != len(want) {
		t.Fatalf("FenceTexts = %q, want %q", texts, want)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Errorf("block %d = %q, want %q", i+1, texts[i], want[i])
		}
	}
	_, fences := Layout(src, Options{Width: 60, Mono: true})
	if len(fences) != len(texts) {
		t.Fatalf("Layout heads %d blocks and FenceTexts counts %d", len(fences), len(texts))
	}
}

// In colour the heading word is the only thing on its row and carries a tone;
// in mono it is the word and nothing else — no escape, as no mono row has one.
func TestBlocks_TheHeadingIsOneWord(t *testing.T) {
	for _, mono := range []bool{false, true} {
		rows, fences := Layout("```typescript\nlet x = 1\n```\n", Options{Width: 60, Mono: mono})
		head := rows[fences[0].Heading]
		if got := strings.TrimSpace(ansi.Strip(head)); got != "typescript" {
			t.Errorf("mono=%v: heading %q", mono, got)
		}
		if mono && head != ansi.Strip(head) {
			t.Errorf("a mono heading carries an escape: %q", head)
		}
		if strings.Contains(head, "shhh-fence") {
			t.Errorf("mono=%v: the mark left the package: %q", mono, head)
		}
	}
}

// A source that holds the mark a heading row is found by is still laid out
// with exactly the blocks it has: quoted text cannot pass for a heading.
func TestBlocks_TheSourceCannotForgeAHeading(t *testing.T) {
	forged := headingMark("")
	for _, src := range []string{
		"a " + forged + " b\n\n```go\nx\n```\n",
		"```go\nx " + forged + " y\n```\n",
		"no fence at all " + forged + "\n",
	} {
		_, fences := Layout(src, Options{Width: 60, Mono: true})
		if len(fences) != len(FenceTexts(src)) {
			t.Errorf("%q: %d headings for %d blocks", src, len(fences), len(FenceTexts(src)))
		}
	}
}
