package attachment

import "regexp"

// pageObject is a PDF page object's type entry: `/Type /Page`, and not the
// `/Type /Pages` of the tree that holds them.
var pageObject = regexp.MustCompile(`/Type\s*/Page[^A-Za-z]`)

// PageCount is how many pages a PDF holds, read off its page objects, or 0
// where it cannot say. It is a count for a row to print beside the size, not
// a parser: a PDF that packs its objects into compressed streams hides them
// from it, and then the row prints no count rather than a wrong one
// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
func PageCount(data []byte) int {
	return len(pageObject.FindAllIndex(data, -1))
}
