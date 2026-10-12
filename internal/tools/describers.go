package tools

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/receipt/describe"
)

// Describers is what every tool this package defines declares about how its
// calls read, by tool name: the base toolset's readers and writers, and the
// command.
func Describers() map[string]describe.Describer {
	out := map[string]describe.Describer{ExecCommandName: execCommandReceipt}
	for _, d := range append(ReadOnly(), Mutating()...) {
		out[d.Tool.Name] = d.Receipt
	}
	return out
}

// ResultLines is a bounded reader's result as the lines it found: the last
// line is dropped, and more reported, where it is the tool's own notice that
// it stopped rather than a thing it found — a paged read is `2000+ lines` and
// not 2001. It is how a count of a result is read for every tool whose
// answer is one line per thing.
func ResultLines(result string) (lines []string, more bool) {
	lines = strings.Split(strings.TrimRight(result, "\n"), "\n")
	more = TruncationNotice(lines[len(lines)-1])
	if more {
		lines = lines[:len(lines)-1]
	}
	return lines, more
}

// countRead counts a read's lines. A read of part of a file opens with the
// whole file's size, which is the tool's own line about the file and not one
// of the lines it read.
func countRead(c describe.Call) (int, string, bool) {
	lines, more := ResultLines(c.Result)
	if len(lines) > 1 && IsSizeLine(lines[0]) {
		lines = lines[1:]
	}
	return describe.Measured(len(lines), more, "line", "lines")
}

// CountItems is the count of a result that is one path per line — a
// listing, a glob, an fd search — in the items it found.
func CountItems(c describe.Call) (int, string, bool) {
	lines, more := ResultLines(c.Result)
	return describe.Measured(len(lines), more, "item", "items")
}

// countSearch counts what a search found rather than what it printed: the
// result carries context lines around every match and a notice when the tool
// stopped at its cap, so counting lines described a truncated fifty-match
// answer as `298 matches`. It is measured by the tool that wrote the format.
// See docs/interface/principles.md#one-grid.
func countSearch(c describe.Call) (int, string, bool) {
	size := MeasureSearch(c.Result)
	if size.Files {
		return describe.Measured(size.N, size.Truncated, "file", "files")
	}
	return describe.Measured(size.N, size.Truncated, "match", "matches")
}

// editSubject is what an edit was about: the file, or for an edit across a
// list the files it changed, joined, which the row clips as it clips any long
// subject. A file of the list the pattern did not match is not on the row —
// the row says what was written, and that file was not. Before there is a
// result, or where it is not the list's, the row names every file listed. An
// edit of one file reads as it always did.
func editSubject(c describe.Call) string {
	paths := callPaths(c.Args)
	if len(paths) < 2 {
		return c.Arg
	}
	var changed []string
	for _, line := range strings.Split(c.Result, "\n") {
		path, rest, ok := strings.Cut(strings.TrimPrefix(line, "  "), ": ")
		if ok && strings.HasPrefix(line, "  ") && strings.Contains(rest, " replacement(s), file now ") {
			changed = append(changed, path)
		}
	}
	if strings.HasPrefix(c.Result, "Edited ") && len(changed) > 0 {
		return strings.Join(changed, ", ")
	}
	return strings.Join(paths, ", ")
}

// countEdited counts an edit across a list in the files it changed, read off
// the header its result opens with, so the activity row and the close row
// count the same thing — files. Any other edit is counted in its result's
// lines, as every edit was.
func countEdited(c describe.Call) (int, string, bool) {
	var n int
	if len(callPaths(c.Args)) > 1 {
		if _, err := fmt.Sscanf(c.Result, "Edited %d", &n); err == nil {
			return describe.Measured(n, false, "file", "files")
		}
	}
	lines, more := ResultLines(c.Result)
	return describe.Measured(len(lines), more, "line", "lines")
}
