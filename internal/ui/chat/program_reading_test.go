package chat

// Routes into the transcript that has already been written: reading mode,
// the pointer, the folds, the receipts a session leaves, and the surfaces
// opened from the draft (program_routes_test.go says what these are for).

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
)

// One applied edit opened to its depths from reading mode: the card it is
// in, opened onto its calls, the edit's row, the change in place under it,
// and the whole screen — and back to in place.
func TestProgram_AnEditRowOpensToEachDepth(t *testing.T) {
	dir := fixtureDir(t, map[string]string{
		"loop.go":  "package agent\n\nconst limit = 25\n\nfunc round(a *Agent) error {\n    if a.round >= limit {\n        return ErrRoundLimit\n    }\n    a.round++\n    return nil\n}\n",
		"notes.md": "The cap is read from one place.\n",
	})
	loop := filepath.Join(dir, "loop.go")
	tm := runProgramAt(t, readingSession(dir,
		programTurn{calls: reads("loop.go")},
		programTurn{calls: []provider.ToolCall{call("e1", "edit_file", fmt.Sprintf(`{"path":%q,"old_text":"        return ErrRoundLimit","new_text":"        return &RoundsExhausted{Round: a.round, Max: limit}"}`, loop))}},
		programTurn{calls: reads("notes.md")},
		programTurn{text: "The cap is where the file says it is."},
	), 130, 40)

	send(tm, "raise the round cap")
	waitForText(t, tm, "RoundsExhausted")
	tm.Send(programHandover)
	tm.Send(programAllow)
	waitForText(t, tm, "where the file says")
	// The three calls are one card: the cursor reaches it, enter opens it
	// onto them, the two reads under one line and the edit a row of its own
	// after it.
	programPress(t, tm, "ctrl+o", "k", "k")
	waitForText(t, tm, "1 of 3")
	programPress(t, tm, "enter")
	waitForText(t, tm, "1 of 4")
	programPress(t, tm, "j")
	waitForText(t, tm, "2 of 4")
	programPress(t, tm, "enter")
	waitForText(t, tm, "@@ -4,7 +4,7 @@")
	programPress(t, tm, "enter")
	waitForText(t, tm, "diff · [↑↓/jk] move")
	programPress(t, tm, "esc")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "@@ -4,7 +4,7 @@", "RoundsExhausted")
	if strings.Contains(frame, "diff · [↑↓/jk] move") {
		t.Fatalf("esc did not come back from the full screen:\n%s", frame)
	}
}

// The pointer reads the pane while the draft keeps the keyboard: shift+↑
// walks to a read and shift+→ opens it, shift+↓ and shift+→ open the next,
// and esc on the empty draft folds every row the reader opened — then,
// pressed twice with nothing left to fold, opens the rewind.
func TestProgram_EscFoldsWhatThePointerOpened(t *testing.T) {
	dir := fixtureDir(t, map[string]string{
		"loop.go":  "package agent\n\nfunc loop() {}\n",
		"round.go": "package agent\n\nconst limit = 25\n",
	})
	// A read and a glob are a group of one call each, so each is a row the
	// pointer can stand on once the card is open. The second is a glob and not
	// a search because a search names every match by its absolute path, which
	// the pane cuts at a width the temporary directory decides; a glob names
	// them relative to its root.
	tm := runProgram(t, readingSession(dir,
		programTurn{calls: []provider.ToolCall{reads("loop.go")[0], call("g1", "glob", `{"pattern":"*.go"}`)}},
		programTurn{text: "The limit is a checkpoint, not a wall."},
	))

	send(tm, "how is the round limit counted")
	waitForText(t, tm, "not a wall")
	programPress(t, tm, "shift+up", "shift+up", "shift+up", "shift+up", "shift+right", "shift+down", "shift+right")
	waitForText(t, tm, "round.go")
	programPress(t, tm, "esc")
	programPress(t, tm, "esc")
	waitForText(t, tm, "folded 2 rows")
	programPress(t, tm, "esc", "esc")
	waitForText(t, tm, "pick a turn to return to")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "pick a turn to return to")
	if strings.Contains(frame, "round.go") {
		t.Fatalf("the rows the pointer opened are still open:\n%s", frame)
	}
}

// /compact leaves a receipt on the grid and marks the turns it replaced as
// out of the window.
func TestProgram_CompactLeavesAReceiptOverTheTurnsItFolded(t *testing.T) {
	dir := fixtureDir(t, map[string]string{"loop.go": "package agent\n", "round.go": "package agent\n"})
	tm := runProgram(t, readingSession(dir,
		programTurn{text: "Locate the round accounting\n", calls: reads("loop.go", "round.go")},
		programTurn{text: "The limit is read from a constant in loop.go."},
		programTurn{text: "round.go declares the constant, so the two files disagree about who owns it."},
		programTurn{text: "The turns so far established loop.go as the owner."},
	))

	send(tm, "where is the round limit counted")
	waitForText(t, tm, "constant in loop.go")
	send(tm, "and who declares it")
	waitForText(t, tm, "who owns it")
	send(tm, "/compact")
	waitForAll(t, tm, "compacted turn", "out of the window")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "compacted turn", "out of the window")
}

// A paste too big for the draft is staged as a token, its chip opens onto
// the paste — reading mode lands on the strip when there is nothing above it
// — and the sent message keeps it as a row that expands.
func TestProgram_APasteTooBigForTheDraftIsAToken(t *testing.T) {
	var log strings.Builder
	log.WriteString("=== RUN   TestRoundLimit\n")
	for i := 1; i <= 211; i++ {
		fmt.Fprintf(&log, "    loop_test.go:44: round %d reached after 2.1s\n", i)
	}
	log.WriteString("--- FAIL: TestRoundLimit (2.11s)\n")
	m, _ := scriptedSession(programTurn{text: "The loop never leaves the round."})
	tm := runProgram(t, m)

	tm.Send(tea.PasteMsg{Content: "this test log says the loop never stops "})
	tm.Send(tea.PasteMsg{Content: log.String()})
	waitForText(t, tm, "will cost")
	programPress(t, tm, "ctrl+o", "enter")
	waitForText(t, tm, "PASTE#1")
	programPress(t, tm, "esc")
	waitForText(t, tm, "back to the draft")
	programPress(t, tm, "esc", "enter")
	waitForText(t, tm, "never leaves the round")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "Paste#1", "never leaves the round")
}

// Three files staged by path each take a handle of their kind, and the strip
// leads every chip with it: two pictures both called clipboard.png are
// Image#1 and Image#2, and the text file beside them is File#1.
func TestProgram_StagedFilesLeadWithTheirHandles(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for _, sub := range []string{"a", "b"} {
		p := filepath.Join(dir, sub, "clipboard.png")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, pngHeader, 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	notes := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notes, []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths = append(paths, notes)
	m, _ := scriptedSession(programTurn{text: "nothing to do"})
	tm := runProgram(t, m)

	for i, p := range paths {
		send(tm, "/paste "+p)
		waitForText(t, tm, []string{"attached Image#1", "attached Image#2", "attached File#1"}[i])
	}

	frame := finalFrame(t, tm)
	frameHas(t, frame, "▣ Image#1", "▣ Image#2", "≡ File#1")
}

// A picture dragged into the middle of a sentence leaves its fold where it
// landed, the sentence goes out folds and all, and the sent message keeps the
// fold and draws a tray row for the picture under it.
func TestProgram_ADraggedPictureLeavesAFoldInTheSentence(t *testing.T) {
	shot := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(shot, pictureToFold(t).Data, 0o600); err != nil {
		t.Fatal(err)
	}
	m, _ := scriptedSession(programTurn{text: "The dialog is clipped at the edge."})
	tm := runProgram(t, m)

	tm.Send(tea.PasteMsg{Content: "this is the error "})
	tm.Send(tea.PasteMsg{Content: shot})
	waitForText(t, tm, "⟨Image#1 · 32×16⟩")
	tm.Send(tea.PasteMsg{Content: " on the settings screen"})
	programPress(t, tm, "enter")
	waitForText(t, tm, "clipped at the edge")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "this is the error ⟨Image#1 · 32×16⟩ on the settings screen", "⟨▣ Image#1⟩  shot.png · 32×16")
}

// The palette and a picker both open from the draft and both give it back:
// ctrl+/ lists the commands, and /model opens the list of models to pick.
func TestProgram_ThePaletteAndTheModelPickerOpenFromTheDraft(t *testing.T) {
	m, _ := scriptedSession(programTurn{text: "nothing to do"})
	// The palette lists recent files; this one has none, so what it lists
	// does not depend on the directory the suite runs in.
	m.recentFiles = func() []project.RecentFile { return nil }
	m.picker.models.options = []string{"scripted-large", "scripted-mini"}
	m.wiring.SwitchModel = func(string) {}
	tm := runProgram(t, m)

	programPress(t, tm, "ctrl+/")
	waitForText(t, tm, "COMMANDS")
	programPress(t, tm, "esc")
	send(tm, "/model")
	waitForText(t, tm, "scripted-mini")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "scripted-large", "scripted-mini")
	if strings.Contains(frame, "COMMANDS") {
		t.Fatalf("esc did not close the palette:\n%s", frame)
	}
}

// A screenshot staged by path, then half a sentence: reading mode reaches the
// chip from the sentence, enter opens the card, q comes back to the strip, x
// drops the chip, and esc goes back to the sentence still in the draft.
func TestProgram_AStagedChipIsReachedOpenedAndDropped(t *testing.T) {
	var shot bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 32, 16))
	for i := range img.Pix {
		img.Pix[i] = 0xc0
	}
	if err := png.Encode(&shot, img); err != nil {
		t.Fatal(err)
	}
	dir := fixtureDir(t, map[string]string{"shot.png": shot.String()})
	tm := runProgramAt(t, readingSession(dir, programTurn{text: "unused"}), 110, 40)

	send(tm, "/paste shot.png")
	waitForText(t, tm, "reaches it to look or")
	tm.Send(tea.PasteMsg{Content: "this is the error I"})
	programPress(t, tm, "ctrl+o", "j")
	waitForText(t, tm, "chip 1 of 1")
	programPress(t, tm, "enter")
	waitForText(t, tm, "32×16")
	programPress(t, tm, "esc")
	waitForText(t, tm, "chip 1 of 1")
	programPress(t, tm, "d")
	waitForText(t, tm, "dropped Image#1")
	programPress(t, tm, "esc")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "this is the error I", "dropped Image#1")
	if strings.Contains(frame, "▣ Image#1") {
		t.Fatalf("the dropped chip is still on the strip:\n%s", frame)
	}
}
