package chat

// Routes over what a turn changed: the close that offers review and commit,
// the commit card behind it, and the rewind (program_routes_test.go says what
// these are for).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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
	// The close offers nothing until it is selected; the pointer's first
	// press lands on it, the newest row.
	waitForText(t, tm, "1 file changed")
	programPress(t, tm, "shift+up")
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

// twoEditTurns is a session whose two turns each edit loop.go, so the
// transcript holds two turns' closes with the same words on them.
func twoEditTurns(root string, after ...programTurn) Model {
	turns := []programTurn{
		editTurn(root, "loop.go", "const limit = 25", "const limit = 50"),
		{text: "Capped at fifty now."},
		editTurn(root, "loop.go", "const limit = 50", "const limit = 100"),
		{text: "Raised again."},
	}
	return changesSession(root, append(turns, after...)...).WithMouse(true)
}

// runTwoTurns drives both turns to their closes.
func runTwoTurns(t *testing.T, tm *program) {
	t.Helper()
	send(tm, "cap rounds at the limit")
	allowEdit(t, tm, "const limit = 50")
	waitForText(t, tm, "Capped at fifty now")
	send(tm, "raise the cap")
	allowEdit(t, tm, "const limit = 100")
	waitForText(t, tm, "Raised again")
}

// closeLines is the frame's lines that state a turn's change, oldest first:
// the two closes carry the same words, so which one is which is where it is.
func closeLines(frame string) []int {
	var at []int
	for i, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, "1 file changed") {
			at = append(at, i)
		}
	}
	return at
}

// waitForFrame blocks until the frame answers a question, the way waitForText
// waits for a phrase.
func waitForFrame(t *testing.T, tm *program, what string, ok func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if f := tm.frame.Load(); f != nil && ok(*f) {
			return *f
		}
		time.Sleep(10 * time.Millisecond)
	}
	last := ""
	if f := tm.frame.Load(); f != nil {
		last = *f
	}
	t.Fatalf("the program never drew %s; the last frame was:\n%s", what, last)
	return ""
}

// readingPosition is where reading mode's rail says the cursor stands, as
// the "READING 3/6" the frame draws, or 0 where it draws none.
var readingPosition = regexp.MustCompile(`READING (\d+)/(\d+)`)

// stepUp walks reading mode's cursor up a row at a time until the frame
// answers the question, waiting at each press for the rail to say the
// cursor moved: a press queued behind one that already answered it would
// walk the cursor past the row it was looking for.
func stepUp(t *testing.T, tm *program, what string, ok func(string) bool) {
	t.Helper()
	for i := 0; i < 40; i++ {
		f := waitForFrame(t, tm, "reading mode's position", readingPosition.MatchString)
		if ok(f) {
			return
		}
		pos := readingPosition.FindStringSubmatch(f)
		n, _ := strconv.Atoi(pos[1])
		if n <= 1 {
			break
		}
		programPress(t, tm, "k")
		waitForText(t, tm, fmt.Sprintf("READING %d/%s", n-1, pos[2]))
	}
	waitForFrame(t, tm, what, ok)
}

// cursorOnFirstClose reports that reading mode's cursor stands on the older
// of the two closes: the pointer mark leads the block's first line, which is
// the line above the one stating the change.
func cursorOnFirstClose(frame string) bool {
	at := closeLines(frame)
	lines := strings.Split(frame, "\n")
	return len(at) == 2 && at[0] > 0 && strings.HasPrefix(strings.TrimLeft(lines[at[0]-1], " "), "❯")
}

// The two historical closes are two targets. Selected and opened with enter,
// each opens its own turn's review — the older one included, which a key
// that fell to the newest row could not reach.
func TestProgram_EnterOnASelectedCloseOpensThatTurnsReview(t *testing.T) {
	root := programRepo(t, nil)
	tm := runProgramAt(t, twoEditTurns(root), 120, 50)
	runTwoTurns(t, tm)

	// The pointer lights on the newest close, and enter on it reviews turn 2.
	programPress(t, tm, "shift+up")
	waitForText(t, tm, "[enter] review turn")
	programPress(t, tm, "enter")
	waitForAll(t, tm, "leave, change nothing", "turn 2")
	programPress(t, tm, "esc")
	waitForText(t, tm, "Raised again")

	// Reading mode's cursor walked back to the older close, and enter there
	// reviews turn 1.
	programPress(t, tm, "ctrl+o")
	stepUp(t, tm, "the cursor on turn 1's close", cursorOnFirstClose)
	programPress(t, tm, "enter")
	waitForAll(t, tm, "leave, change nothing", "turn 1")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "turn 1")
	if strings.Contains(frame, "turn 2") {
		t.Fatalf("enter on turn 1's close opened another turn:\n%s", frame)
	}
}

// A click on a close opens the turn clicked, from a half-typed line: the
// keyboard is never handed over for it, and the sentence is still there when
// the review is left.
func TestProgram_AClickOnACloseOpensThatTurnsReview(t *testing.T) {
	root := programRepo(t, nil)
	tm := runProgramAt(t, twoEditTurns(root), 120, 50)
	runTwoTurns(t, tm)
	tm.Send(tea.PasteMsg{Content: draftSentence})
	frame := waitForFrame(t, tm, "both closes", func(f string) bool {
		return len(closeLines(f)) == 2 && strings.Contains(f, draftSentence)
	})

	y := closeLines(frame)[0]
	x := strings.Index(strings.Split(frame, "\n")[y], "1 file changed")
	x = len([]rune(strings.Split(frame, "\n")[y][:x]))
	tm.Send(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
	tm.Send(tea.MouseReleaseMsg{Button: tea.MouseNone, X: x, Y: y})
	waitForAll(t, tm, "leave, change nothing", "turn 1")
	programPress(t, tm, "esc")
	waitForText(t, tm, draftSentence)

	frameHas(t, finalFrame(t, tm), draftSentence)
}

// With a sentence in the draft, enter sends it — the pointer on a close does
// not take enter from the draft — and the pointer's own open still reaches
// the review without emptying it.
func TestProgram_ADraftKeepsEnterWhileACloseIsSelected(t *testing.T) {
	root := programRepo(t, nil)
	tm := runProgramAt(t, twoEditTurns(root, programTurn{text: "Sent from beside the pointer."}), 120, 50)
	runTwoTurns(t, tm)

	programPress(t, tm, "shift+up")
	waitForText(t, tm, "[enter] review turn")
	tm.Send(tea.PasteMsg{Content: draftSentence})
	waitForText(t, tm, draftSentence)
	programPress(t, tm, "shift+right")
	waitForAll(t, tm, "leave, change nothing", "turn 2")
	programPress(t, tm, "esc")
	waitForText(t, tm, draftSentence)
	programPress(t, tm, "enter")
	waitForText(t, tm, "Sent from beside the pointer")

	frame := finalFrame(t, tm)
	if strings.Contains(frame, "leave, change nothing") {
		t.Fatalf("enter with a sentence in the draft opened the review:\n%s", frame)
	}
}

// A selected row that makes no offer keeps the chord: the undo a close under
// it offers is not reached from an edit row the cursor stands on. Enter on
// that edit row is its own — it cycles the edit's diff rather than opening a
// turn's review.
func TestProgram_ASelectedEditRowKeepsItsOwnEnter(t *testing.T) {
	root := programRepo(t, nil)
	tm := runProgramAt(t, twoEditTurns(root), 120, 50)
	runTwoTurns(t, tm)

	programPress(t, tm, "ctrl+o")
	onEdit := func(f string) bool {
		for _, l := range strings.Split(f, "\n") {
			if strings.HasPrefix(strings.TrimLeft(l, " "), "❯") && strings.Contains(l, "edit") {
				return true
			}
		}
		return false
	}
	stepUp(t, tm, "the cursor on an edit row", onEdit)
	programPress(t, tm, "alt+z")
	programPress(t, tm, "enter")
	waitForText(t, tm, "@@ -1,3 +1,3 @@")

	frame := finalFrame(t, tm)
	for _, never := range []string{"Undo turn", "leave, change nothing"} {
		if strings.Contains(frame, never) {
			t.Fatalf("the edit row answered with another row's act (%q):\n%s", never, frame)
		}
	}
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

// The picker's diff key reads what a rewind to a row takes back and comes
// back to the picker; the turns taken back fold above the row, and the fold's
// reapply puts them — and the files they wrote — back.
func TestProgram_TheRewoundTurnsFoldAndReapply(t *testing.T) {
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
	programPress(t, tm, "ctrl+u", "down", "d")
	waitForText(t, tm, "+1 −1 · 1 file")
	programPress(t, tm, "esc")
	waitForText(t, tm, "what the turns after it changed")
	programPress(t, tm, "enter")
	waitForText(t, tm, "Rewind to turn 2")
	programPress(t, tm, "b")
	waitForText(t, tm, "Undo turn")
	programPress(t, tm, "y")
	waitForText(t, tm, "turn 3 · rewound")
	// The reapply is the fold's own offer, so it is live once the pointer
	// selects the fold: the first press lands on the newest close, the rewind's
	// own, and the second on the fold above it. Selected, the fold draws the
	// chord live on its own line.
	programPress(t, tm, "shift+up", "shift+up")
	waitForText(t, tm, "read them · [alt+r] reapply")
	programPress(t, tm, "alt+r")
	waitForText(t, tm, "reapplied turn 3")
	programPress(t, tm, "y")
	waitForText(t, tm, "undo of turn")

	frameHas(t, finalFrame(t, tm), "rewound, then reapplied below")
	got, err := os.ReadFile(filepath.Join(root, "loop.go"))
	if err != nil || !strings.Contains(string(got), "const limit = 100") {
		t.Fatalf("the reapply should leave the file as turn 3 left it (%v): %q", err, got)
	}
}
