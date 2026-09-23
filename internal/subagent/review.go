package subagent

// The bounded review. A reviewer is held to its declared evidence by what it
// is handed and where it is stopped, not by prose asking it nicely: the paths
// the spawn declared and the workspace diff under them open its first turn,
// and its round cap ends in a report rather than in a wider pass.
//
// Prose alone did not hold. A reviewer told to "read the declared files
// first" spent its opening rounds re-reading the repository's instruction
// files and surveying the tree around the change, and reached its budget with
// the diff still unread — the failure the token floor cannot catch, because
// the budget was never the thing that was short.
// See docs/capabilities/subagents.md#a-review-is-bounded-by-what-it-is-given.

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// reviewEvidenceBytes bounds the diff a review opens with. It is a byte
	// count and not a token estimate because the cut has to land on a line
	// of the diff, and the thing being cut is measured in lines.
	//
	// 60,000 bytes is roughly 15,000 tokens: a large change, well inside the
	// working reserve admission keeps, and past the size where a reviewer
	// reading every hunk was going to produce a useful ranking anyway. A
	// diff longer than this is truncated and says so, which is a reviewer
	// that knows it saw part of the change — the outcome to prefer over one
	// that silently reviewed the first half.
	reviewEvidenceBytes = 60_000
	// reviewReportRounds is what a review gets to write its report with once
	// its inspection pass is spent. It is not a second pass: a report cites
	// what the pass already read, and three rounds is room to re-open a file
	// it wants to quote exactly, not room to start a new line of inquiry.
	// A review that spends this too stops with what it has.
	reviewReportRounds = 3
)

// reviewReportDirective is what a review is told when its inspection pass
// ends. It closes the pass rather than widening it, which is the whole
// difference between this and the check-in every other child gets: a child
// taking stock is asked what it will do next, and a review at its cap has
// nothing further it is allowed to do.
const reviewReportDirective = `Your inspection pass is over. Do not open any file you have not already read, and do not start a new line of inquiry.

Write your report now from the evidence you have examined. Rank the findings by severity, cite file:line for each, say "no findings" for an empty section, name anything you did not get to rather than implying you covered it, and end on the verdict line your instructions name: ` + "`Verdict: <word>`" + `, on a line of its own.`

// declaredEvidence is what a bounded review's first turn opens with: the
// paths the spawn declared and the workspace diff under them, so the child
// starts from the change rather than from a survey it has to conduct to find
// it. Empty when the spawn declared no paths, which is a review whose task
// text is expected to carry the change itself.
//
// A diff it cannot produce — no git, no repository, a path that has never
// been committed — is not an error: the paths are still the evidence, and
// the child reads them from the workspace instead.
func declaredEvidence(root string, paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Declared evidence\n\nYour review is scoped to: ")
	b.WriteString(strings.Join(paths, ", "))
	b.WriteString("\n\n")
	if d := workspaceDiff(root, paths); d != "" {
		b.WriteString("The workspace change under those paths, as it stands now:\n\n```diff\n")
		b.WriteString(d)
		if !strings.HasSuffix(d, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n\nRead this diff, then the files it touches, then the tests that cover them. ")
	} else {
		b.WriteString("Nothing is uncommitted under those paths, so the change is already in the workspace: read those paths, then the tests that cover them. ")
	}
	b.WriteString("Report when that pass is done, rather than broadening it.\n\n")
	return b.String()
}

// workspaceDiff is `git diff HEAD` narrowed to the declared paths, truncated
// at a line boundary once it passes reviewEvidenceBytes. It returns empty for
// anything git could not answer, so a caller can treat "no repository" and
// "nothing changed" the same way: neither is evidence, and neither is a
// reason to refuse the spawn.
func workspaceDiff(root string, paths []string) string {
	args := append([]string{"diff", "HEAD", "--"}, paths...)
	out, err := runGit(root, args...)
	if err != nil {
		return ""
	}
	out = strings.TrimRight(out, "\n")
	if out == "" || len(out) <= reviewEvidenceBytes {
		return out
	}
	cut := strings.LastIndex(out[:reviewEvidenceBytes], "\n")
	if cut <= 0 {
		// A diff whose first line is longer than the whole allowance — a
		// minified file, a generated blob — has no line to cut at, so the
		// cut lands wherever the byte count falls and is walked back to a
		// rune start. Half a rune renders as a replacement character in the
		// middle of the evidence, which reads as a corrupt diff.
		cut = reviewEvidenceBytes
		for cut > 0 && !utf8.RuneStart(out[cut]) {
			cut--
		}
	}
	return fmt.Sprintf("%s\n[diff truncated: %d of %d bytes shown]",
		out[:cut], cut, len(out))
}
