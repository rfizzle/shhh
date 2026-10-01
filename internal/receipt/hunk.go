package receipt

import "github.com/rfizzle/shhh/internal/diff"

// HunkHead is the head of the change an edit applied: the file, the first
// line the edit changed, and how many lines it added and removed in all.
type HunkHead struct {
	Path string
	// Line is the first changed line's number, on the side it is on: the new
	// file's for a line added, the old file's for a line removed.
	Line int
	// Change is whether that line was added or removed, and Text is the line
	// itself, without the diff's marker.
	Change diff.Kind
	Text   string
	// Added and Removed count every changed line of the edit, not only the
	// first hunk's.
	Added, Removed int
}

// Marked is the first changed line as a diff marks it: `+ ` or `- ` and the
// line. The marker is the diff's own, which is what lets the line stand as
// evidence without a sentence composed about it.
func (h HunkHead) Marked() string {
	if h.Change == diff.Del {
		return "- " + h.Text
	}
	return "+ " + h.Text
}

// hunkHead reads the head of an edit's change from its hunks. A change with
// no line added or removed — a mode change alone — has no head, since there
// is no line to show.
func hunkHead(path string, hunks []diff.Hunk) *HunkHead {
	for _, h := range hunks {
		for _, l := range h.Lines {
			if l.Kind == diff.Context {
				continue
			}
			head := &HunkHead{Path: path, Change: l.Kind, Text: l.Text, Line: l.NewNo}
			if l.Kind == diff.Del {
				head.Line = l.OldNo
			}
			head.Added, head.Removed = diff.Stats(hunks)
			return head
		}
	}
	return nil
}
