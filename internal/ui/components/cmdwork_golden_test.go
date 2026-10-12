package components

import (
	"testing"

	"github.com/rfizzle/shhh/internal/ui/golden"
)

// TestGolden_CommandWork captures what a command's work reads as where the
// turn is closed and where it would be taken back: the close row counting
// the files a command changed in their own words, and the undo confirm
// naming exactly those files as the ones it cannot put back.
func TestGolden_CommandWork(t *testing.T) {
	captureGolden(t, "command-work", "a command's files on the close row and the undo confirm", goldenWidths, func(width int) []golden.Panel {
		closed := func(ch TurnChanges) string {
			c := TurnClose{Steps: 2, Tools: 4, Elapsed: "21.6s", Spend: "$0.05", Changes: &ch}
			return c.View(width)
		}
		return []golden.Panel{
			// An edit and two files the generator made, one of them the
			// project's declared output: counted in the files, in no line
			// count, and said not to come back.
			{Label: "close · an edit beside what commands made", View: closed(TurnChanges{
				Files: 3, Added: 12, Removed: 2, ByCommand: 1, Generated: 1,
				Keys: []TurnKey{reviewTurnOffer(), commitTurnOffer()},
				Note: "2 tracked · 1 new", Back: "/undo 4 takes it back",
			})},
			{Label: "close · only what commands made", View: closed(TurnChanges{
				Files: 3, ByCommand: 3,
				Keys: []TurnKey{reviewTurnOffer(), commitTurnOffer()},
				Note: "all new to git", Back: "/undo 4 takes it back",
			})},
			{Label: "close · a claim answered on the card", View: closed(TurnChanges{
				Files: 1, Claimed: 1, Note: "all new to git",
			})},
			{Label: "undo · restores the edit, names what it cannot", View: UndoConfirm{
				Confirm: Confirm{Prompt: "Undo turn 4?", KeyList: true}, Restores: 1,
				Kept: []string{"gen/kms/v1/kms.pb.go", "go.sum"},
			}.View(width)},
			{Label: "undo · nothing it can put back", View: UndoConfirm{
				Confirm: Confirm{Prompt: "Undo turn 5?", KeyList: true},
				Kept:    []string{"gen/kms/v1/kms.pb.go", "gen/kms/v1/kms_grpc.pb.go", "go.sum", "go.mod"},
			}.View(width)},
		}
	})
}
