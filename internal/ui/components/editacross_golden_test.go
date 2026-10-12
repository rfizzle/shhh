package components

import (
	"testing"

	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// TestGolden_EditAcrossFiles captures the one card an edit across a list of
// files asks on: the act naming how many files, the rule as it was written
// with whether it is a pattern said in words, each file under its own row
// with its hunk count and lines, a file the pattern leaves alone saying so,
// and the call's totals where a single file's stats line goes.
func TestGolden_EditAcrossFiles(t *testing.T) {
	captureGolden(t, "edit-across-files", "one card for an edit across a list of files", goldenWidths, func(width int) []golden.Panel {
		files := []EditFile{
			{Path: "internal/agent/loop.go", Hunks: diff.Compute(
				"func run() {\n\tstep(ctx)\n}\n",
				"func run() {\n\tstep(ctx, budget)\n}\n")},
			{Path: "internal/agent/round.go", Hunks: diff.Compute(
				"a := 1\nstep(ctx)\nb := 2\nc := 3\nd := 4\ne := 5\nf := 6\nstep(ctx)\n",
				"a := 1\nstep(ctx, budget)\nb := 2\nc := 3\nd := 4\ne := 5\nf := 6\nstep(ctx, budget)\n")},
			{Path: "internal/agent/doc.go"},
		}
		card := func(mut func(*ApprovalCard)) string {
			c := ApprovalCard{
				Variant:  ApprovalEdit,
				Title:    "Approve edit",
				ActGlyph: "✎",
				Act:      "edit 3 files",
				Answer:   "apply the change",
				Severity: SeverityMedium, SeverityReason: "edits 3 files",
				Files:         files,
				Reversibility: "undo yes — each file recorded on its own",
				Footnote:      "[a] always — not offered: an edit across files is answered once",
			}
			mut(&c)
			return c.View(width)
		}
		return []golden.Panel{
			{Label: "a pattern across three files, one left alone", View: card(func(c *ApprovalCard) {
				c.Fields = []CardField{
					{Label: "find", Value: `step\((\w+)\)`, Tone: ToneNeutral},
					{Label: "replace", Value: "step($1, budget)", Tone: ToneNeutral},
					{Label: "match", Value: "regular expression", Detail: "RE2, case-sensitive; $1 and ${name} are its groups", Tone: ToneNeutral},
				}
			})},
			{Label: "exact text across the same files", View: card(func(c *ApprovalCard) {
				c.Fields = []CardField{
					{Label: "find", Value: "step(ctx)", Tone: ToneNeutral},
					{Label: "replace", Value: "step(ctx, budget)", Tone: ToneNeutral},
					{Label: "match", Value: "exact text", Tone: ToneNeutral},
				}
			})},
		}
	})
}
