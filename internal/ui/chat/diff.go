package chat

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type diffLineKind int

const (
	diffContext diffLineKind = iota
	diffAdd
	diffDel
	diffHunk
)

// diffLine is one rendered line of a unified diff; text already carries its
// leading marker (" ", "+", "-", or an "@@" header).
type diffLine struct {
	kind diffLineKind
	text string
}

const diffContextLines = 3

// maxDiffCells bounds the LCS table size; beyond it the diff degrades to a
// whole-file replacement rather than allocating unbounded memory.
const maxDiffCells = 4_000_000

// unifiedDiff computes a unified diff (hunk headers plus context lines)
// between two texts. Returns nil when the texts are line-identical.
func unifiedDiff(oldText, newText string) []diffLine {
	ops := diffOps(splitDiffLines(oldText), splitDiffLines(newText))
	changed := false
	for _, op := range ops {
		if op.kind != diffContext {
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}
	return buildHunks(ops)
}

// splitDiffLines splits text into lines, treating a trailing newline as a
// terminator rather than an extra empty line.
func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// diffOps aligns two line slices into an ordered sequence of context, delete,
// and add operations (unmarked text; markers are added by buildHunks).
func diffOps(a, b []string) []diffLine {
	n, m := len(a), len(b)
	if n > 0 && m > 0 && n*m > maxDiffCells {
		ops := make([]diffLine, 0, n+m)
		for _, l := range a {
			ops = append(ops, diffLine{diffDel, l})
		}
		for _, l := range b {
			ops = append(ops, diffLine{diffAdd, l})
		}
		return ops
	}

	// lcs[i][j] = length of the longest common subsequence of a[i:] and b[j:].
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}

	ops := make([]diffLine, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffLine{diffContext, a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffLine{diffDel, a[i]})
			i++
		default:
			ops = append(ops, diffLine{diffAdd, b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffLine{diffDel, a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffLine{diffAdd, b[j]})
	}
	return ops
}

// buildHunks groups ops into hunks with diffContextLines of surrounding
// context and prefixes each line with its unified-diff marker.
func buildHunks(ops []diffLine) []diffLine {
	include := make([]bool, len(ops))
	for i, op := range ops {
		if op.kind == diffContext {
			continue
		}
		for j := max(0, i-diffContextLines); j <= min(len(ops)-1, i+diffContextLines); j++ {
			include[j] = true
		}
	}

	var out []diffLine
	oldNo, newNo := 1, 1
	i := 0
	for i < len(ops) {
		if !include[i] {
			// Only context ops can be excluded.
			oldNo++
			newNo++
			i++
			continue
		}
		j := i
		for j < len(ops) && include[j] {
			j++
		}
		oldStart, newStart := oldNo, newNo
		var oldCount, newCount int
		body := make([]diffLine, 0, j-i)
		for _, op := range ops[i:j] {
			switch op.kind {
			case diffContext:
				oldCount++
				newCount++
				body = append(body, diffLine{diffContext, " " + op.text})
			case diffDel:
				oldCount++
				body = append(body, diffLine{diffDel, "-" + op.text})
			case diffAdd:
				newCount++
				body = append(body, diffLine{diffAdd, "+" + op.text})
			}
		}
		oldNo += oldCount
		newNo += newCount
		hs, ns := oldStart, newStart
		if oldCount == 0 {
			hs = oldStart - 1
		}
		if newCount == 0 {
			ns = newStart - 1
		}
		out = append(out, diffLine{diffHunk, fmt.Sprintf("@@ -%d,%d +%d,%d @@", hs, oldCount, ns, newCount)})
		out = append(out, body...)
		i = j
	}
	return out
}

// renderDiffLines colors a unified diff and truncates it to at most maxLines
// rows, the last of which is a truncation notice when lines were dropped.
func renderDiffLines(diff []diffLine, width, maxLines int) []string {
	if maxLines < 1 {
		maxLines = 1
	}
	if len(diff) == 0 {
		return []string{systemMsgStyle.Render("(no changes)")}
	}
	shown := diff
	if len(diff) > maxLines {
		shown = diff[:maxLines-1]
	}
	lines := make([]string, 0, len(shown)+1)
	for _, dl := range shown {
		text := clipLine(strings.ReplaceAll(dl.text, "\t", "    "), width)
		switch dl.kind {
		case diffAdd:
			lines = append(lines, diffAddStyle.Render(text))
		case diffDel:
			lines = append(lines, diffDelStyle.Render(text))
		case diffHunk:
			lines = append(lines, diffHunkStyle.Render(text))
		default:
			lines = append(lines, diffContextStyle.Render(text))
		}
	}
	if extra := len(diff) - len(shown); extra > 0 {
		lines = append(lines, systemMsgStyle.Render(fmt.Sprintf("… (+%d more diff lines)", extra)))
	}
	return lines
}

// clipLine hard-truncates a single line to the given display width. Rune
// count approximates cell width, which is fine for a preview clip.
func clipLine(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	r := []rune(s)
	if len(r) > width-1 {
		r = r[:width-1]
	}
	return string(r) + "…"
}
