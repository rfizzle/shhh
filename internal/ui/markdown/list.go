package markdown

import (
	"strconv"
	"strings"

	gast "github.com/yuin/goldmark/ast"
	xast "github.com/yuin/goldmark/extension/ast"
)

// The block glamour got most wrong, and the reason this package exists.
//
// Two rules carry it. A wrapped line hangs under the text rather than under
// the marker, because a continuation that starts at the bullet's column reads
// as a new item. And an item's blocks are separated the way a document's are
// — a loose item's second paragraph is a paragraph, not more of the first
// one, which is the bug that produced `2. secondnested paragraph under item
// two`.

// list renders a bullet or ordered list, and every list nested inside it.
func (r *renderer) list(n *gast.List, width int) []string {
	var rows []string
	number := n.Start
	if number == 0 {
		number = 1
	}
	for item := n.FirstChild(); item != nil; item = item.NextSibling() {
		marker := r.marker(n, number)
		number++
		// The hang is the marker's own width, so the text of every line of
		// the item begins at one column.
		hang := len([]rune(marker))
		// A task item's marker is its checkbox, which the inline renderer has
		// already put at the front of the item's first line. Drawing a bullet
		// as well gives it two, so the bullet goes and the hang is the box's.
		if isTask(item) {
			marker, hang = "", TaskBoxWidth
		}
		body := r.itemBody(item, max(width-hang, 1))
		for i, row := range body {
			if i == 0 {
				rows = append(rows, r.sty.marker.Render(marker)+row)
				continue
			}
			rows = append(rows, strings.Repeat(" ", hang)+row)
		}
		// A loose list puts a blank row between its items, the way it puts
		// one between an item's own blocks. A tight one does not, and that
		// difference is the author's.
		if !n.IsTight && item.NextSibling() != nil {
			rows = append(rows, "")
		}
	}
	return rows
}

// isTask reports whether an item opens with a checkbox, which is how the GFM
// extension marks `- [ ]`.
func isTask(item gast.Node) bool {
	first := item.FirstChild()
	if first == nil || first.FirstChild() == nil {
		return false
	}
	_, ok := first.FirstChild().(*xast.TaskCheckBox)
	return ok
}

// marker is the item's own prefix, ending in the space that sets the hang.
func (r *renderer) marker(n *gast.List, number int) string {
	if !n.IsOrdered() {
		return "• "
	}
	return strconv.Itoa(number) + string(n.Marker) + " "
}

// itemBody renders one item's blocks, separated by a blank row wherever the
// item is loose enough to have more than one.
//
// The separator is the fix. An item holding a paragraph and then another
// paragraph is two blocks and reads as two; running them together is not a
// tighter layout, it is a different sentence.
func (r *renderer) itemBody(item gast.Node, width int) []string {
	var rows []string
	for c := item.FirstChild(); c != nil; c = c.NextSibling() {
		block := r.block(c, width)
		if len(block) == 0 {
			continue
		}
		if len(rows) > 0 {
			// A nested list follows its parent item's line directly: the
			// indent already says it is nested, and a blank row above it
			// would read as the parent list ending.
			if _, nested := c.(*gast.List); !nested {
				rows = append(rows, "")
			}
		}
		rows = append(rows, block...)
	}
	return rows
}
