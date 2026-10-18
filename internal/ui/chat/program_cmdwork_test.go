package chat

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rfizzle/llm"
	"github.com/rfizzle/shhh/internal/changeset"
)

// What the model's command made is the turn's work: the command's card is
// answered, the command writes a file the tree did not have, and the row it
// leaves and the turn's close both count that file in its own words — with
// the reader's own uncommitted file left out of it.
// See docs/capabilities/approvals-and-safety.md#the-writing-half-of-git-is-a-tool-too.
func TestProgram_ACommandsFileIsCountedOnTheClose(t *testing.T) {
	root := programRepo(t, map[string]string{"README.md": "# project\n\nnotes of my own\n"})
	m := readingSessionWith(root, Wiring{
		Tracker: changeset.NewTracker(root),
		Runner: legacyRunner(func(context.Context, string) (string, int) {
			if err := os.WriteFile(filepath.Join(root, "gen.pb.go"), []byte("package agent\n"), 0o644); err != nil {
				return err.Error(), 1
			}
			return "", 0
		}),
	},
		programTurn{calls: []llm.ToolCall{
			{ID: "call_gen", Name: "execute_command", Arguments: `{"command":"make gen"}`},
		}},
		programTurn{text: "The protobufs are generated."},
	)
	tm := runProgram(t, m)

	send(tm, "regenerate the protobufs")
	allowEdit(t, tm, "Approve command")
	waitForText(t, tm, "The protobufs are generated.")
	frame := finalFrame(t, tm)
	frameHas(t, frame, "1 file recorded")
	frameHas(t, frame, "1 file changed · 1 by a command · not restorable")
}
