package chat

// Routes over what a turn changed: the close that offers review and commit,
// the commit card behind it, and the rewind (program_routes_test.go says what
// these are for).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/provider"
)

// programRepo is a repository with loop.go and README.md committed in it,
// plus whatever the caller writes over them after the commit.
func programRepo(t *testing.T, after map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := fixtureDir(t, map[string]string{"loop.go": "package agent\n\nconst limit = 25\n", "README.md": "# project\n"})
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"add", "loop.go", "README.md"},
		{"commit", "-q", "-m", "seed"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	for name, body := range after {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// changesSession is a session over root with its changeset tracked the way a
// coding session's is.
func changesSession(root string, turns ...programTurn) Model {
	return readingSession(root, turns...).WithChangeset(nil, changeset.NewTracker(root))
}

// editTurn is a turn that edits one file under root. The path is absolute:
// the write is dispatched straight to the mutating tools, which resolve a
// relative path against the process's directory.
func editTurn(root, name, old, new string) programTurn {
	return programTurn{calls: []provider.ToolCall{call("e-"+new, "edit_file",
		fmt.Sprintf(`{"path":%q,"old_text":%q,"new_text":%q}`, filepath.Join(root, name), old, new))}}
}

// allowEdit answers the edit card the turn is waiting on, once it shows.
func allowEdit(t *testing.T, tm *program, shows string) {
	t.Helper()
	waitForText(t, tm, shows)
	tm.Send(programHandover)
	tm.Send(programAllow)
}

// The turn's close offers the commit; the commit key opens the card, its
// message opens and closes, and the card's enter commits the turn's own file
// and nothing the reader changed by hand.
func TestProgram_TheCloseOffersTheCommitAndTheCardCommits(t *testing.T) {
	root := programRepo(t, map[string]string{"README.md": "# project\n\nnotes of my own\n"})
	tm := runProgram(t, changesSession(root,
		programTurn{calls: reads("loop.go")},
		editTurn(root, "loop.go", "const limit = 25", "const limit = 50"),
		programTurn{text: "The rounds are capped at the limit now."},
	))

	send(tm, "cap rounds at the limit instead of erroring")
	allowEdit(t, tm, "const limit = 50")
	waitForText(t, tm, "[alt+g] commit")
	programPress(t, tm, "alt+g")
	waitForText(t, tm, "Commit this turn")
	programPress(t, tm, "e")
	waitForText(t, tm, "COMMIT MESSAGE")
	programPress(t, tm, "esc")
	waitForText(t, tm, "pick hunks")
	programPress(t, tm, "enter")
	waitForText(t, tm, "committed 1 file as")

	frameHas(t, finalFrame(t, tm), "committed 1 file as")
	out, err := exec.Command("git", "-C", root, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "M README.md" {
		t.Fatalf("the commit should carry the turn's file and leave the reader's, status is %q", got)
	}
}

// The close's review key opens the review of the turn's files, and leaving
// it changes nothing.
func TestProgram_TheCloseOpensTheReview(t *testing.T) {
	root := programRepo(t, nil)
	tm := runProgram(t, changesSession(root,
		programTurn{calls: reads("loop.go")},
		editTurn(root, "loop.go", "const limit = 25", "const limit = 50"),
		programTurn{text: "The rounds are capped at the limit now."},
	))

	send(tm, "cap rounds at the limit instead of erroring")
	allowEdit(t, tm, "const limit = 50")
	waitForText(t, tm, "[alt+w] review")
	programPress(t, tm, "alt+w")
	waitForText(t, tm, "leave, change nothing")

	frameHas(t, finalFrame(t, tm), "loop.go", "leave, change nothing")
}

// /rewind opens the timeline, a turn is picked, the card asks what back
// means, and the act puts the files back to that turn.
func TestProgram_TheRewindPutsTheFilesBackToATurn(t *testing.T) {
	root := programRepo(t, nil)
	tm := runProgram(t, changesSession(root,
		programTurn{calls: reads("loop.go")},
		programTurn{text: "The rounds are counted in loop.go."},
		editTurn(root, "loop.go", "const limit = 25", "const limit = 50"),
		programTurn{text: "Capped at fifty now."},
		editTurn(root, "loop.go", "const limit = 50", "const limit = 100"),
		programTurn{text: "Raised again."},
	))

	send(tm, "find where rounds are counted")
	waitForText(t, tm, "counted in loop.go")
	send(tm, "cap rounds at the limit")
	allowEdit(t, tm, "const limit = 50")
	waitForText(t, tm, "Capped at fifty now")
	send(tm, "raise the cap")
	allowEdit(t, tm, "const limit = 100")
	waitForText(t, tm, "Raised again")
	send(tm, "/rewind")
	waitForText(t, tm, "pick a turn to return to")
	programPress(t, tm, "down", "enter")
	waitForText(t, tm, "Rewind to turn 2")
	programPress(t, tm, "b")
	waitForText(t, tm, "Undo turn")
	programPress(t, tm, "y")
	waitForText(t, tm, "files back to turn 2")

	frameHas(t, finalFrame(t, tm), "files back to turn 2")
	got, err := os.ReadFile(filepath.Join(root, "loop.go"))
	if err != nil || !strings.Contains(string(got), "const limit = 50") {
		t.Fatalf("the rewind should leave the file as turn 2 left it (%v): %q", err, got)
	}
}
