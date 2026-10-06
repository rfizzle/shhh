package chat

// The routes the driven scenes walk, run through the real program.
//
// Every committed scene under scripts/tui/scenes names at its head the test
// here (or in program_test.go) that walks its route — the keys to the
// surface and the stream that feeds it — or says why no program test can.
// The scene is what a terminal sees; this is the same route as a go test
// verdict, on every platform and inside a contained session, so a key that
// stopped reaching its surface fails the gate before anybody opens tmux.
// TestScenes_EachNamesItsRoute holds the two together.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
)

// More keys a reader presses, beside the three program_test.go spells.
var (
	programDeny   = tea.KeyPressMsg{Code: 'n', Text: "n"}
	programCancel = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
)

// scriptedSession is a session over the scripted provider, with the system
// prompt every fixture here starts from.
func scriptedSession(turns ...programTurn) (Model, *programProvider) {
	p := &programProvider{turns: turns}
	return New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, streamOf(p)), p
}

// quietHold is a hold released once the keyboard has been quiet for the
// grace window's beat, so the card the held turn brings arrives on a cold
// keyboard and takes it with no window open — the arrival a scene gets by
// waiting for the card before it presses anything (interrupt.go). No fact to
// wait on: a quiet keyboard is time passing and nothing else, so the sleep is
// a lower bound on elapsed time, which a clock can promise, not a guess about
// how fast a machine draws.
func quietHold(t *testing.T) (chan struct{}, func()) {
	t.Helper()
	hold := make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	t.Cleanup(release)
	return hold, func() {
		time.Sleep(graceQuiet + 50*time.Millisecond)
		release()
	}
}

// send types a line into the draft and sends it.
func send(tm *program, line string) {
	tm.Send(tea.PasteMsg{Content: line})
	tm.Send(programEnter)
}

// fixtureDir is a workspace of the test's own with the named files in it.
func fixtureDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// waitForAll waits for each of several phrases to be on the frame.
func waitForAll(t *testing.T, tm *program, phrases ...string) {
	t.Helper()
	for _, s := range phrases {
		waitForText(t, tm, s)
	}
}

// waitForGone blocks until the program draws a frame without s: the wait for
// a surface or a state to go away.
func waitForGone(t *testing.T, tm *program, s string) {
	t.Helper()
	if eventually(func() bool { f := tm.frame.Load(); return f != nil && !strings.Contains(*f, s) }) {
		return
	}
	t.Fatalf("the program went on drawing %q:\n%s", s, *tm.frame.Load())
}

// call is one scripted tool call.
func call(id, name, args string) provider.ToolCall {
	return provider.ToolCall{ID: id, Name: name, Arguments: args}
}

// frameHas fails the test for every phrase the frame does not carry.
func frameHas(t *testing.T, frame string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(frame, want) {
			t.Fatalf("the final frame does not carry %q:\n%s", want, frame)
		}
	}
}

// The way out: the quit chord arms on its first press and says so on the
// frame, and the second press ends the program with the banner the session
// leaves behind it — every scene closes on this route. The banner's parting
// row is drawn only to a terminal (exitbanner.go), so what is asked of it
// here is the row that names the session.
func TestProgram_TheQuitChordArmsThenLeaves(t *testing.T) {
	m, _ := scriptedSession(programTurn{text: "nothing to do"})
	tm := runProgram(t, m)

	send(tm, "say hi")
	waitForText(t, tm, "nothing to do")
	tm.Send(programCancel)
	waitForText(t, tm, "again quits")
	tm.Send(programCancel)
	tm.WaitFinished(t, teatest.WithFinalTimeout(10*time.Second))

	final := tm.FinalModel(t).(watched).Model
	if banner := stripANSI(final.ExitBanner("").View(120)); !strings.Contains(banner, "session") {
		t.Fatalf("the program left without its banner:\n%s", banner)
	}
}

// A command card that arrives on an empty draft and a quiet keyboard holds
// the keyboard by arriving, so its own keys answer it with no handover: [y]
// runs the command and the row and the reply after it land on the frame;
// [n] refuses the call, nothing runs, the row says it was the reader's
// refusal, and the turn carries on to its last sentence.
func TestProgram_ACardAnswersToItsOwnKeys(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		reply   string
		ask     string
		output  string
		await   string
		key     tea.KeyPressMsg
		settle  string
		runs    int
		ranMsg  string
		wants   []string
	}{
		{
			name:    "yes runs it",
			command: "echo hi from the script",
			reply:   "Done: the command printed a greeting",
			ask:     "run something",
			output:  "hi from the script",
			await:   "[y] run it once",
			key:     programAllow,
			settle:  "Done: the command printed",
			runs:    1,
			ranMsg:  "the card's own key did not run the command, ran %v:\n%s",
			wants:   []string{"$ ran", "echo hi from the script", "Done: the command printed a greeting"},
		},
		{
			name:    "no refuses it",
			command: "go test ./internal/agent/...",
			reply:   "That is the shape of it",
			ask:     "now run the agent tests",
			output:  "",
			await:   "[n]",
			key:     programDeny,
			settle:  "That is the shape of it",
			runs:    0,
			ranMsg:  "a refused command ran: %v\n%s",
			wants:   []string{"go test ./internal/agent/...", "denied", "That is the shape of it"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ran []string
			hold, release := quietHold(t)
			m, _ := scriptedSession(
				programTurn{hold: hold, calls: []provider.ToolCall{call("c1", tools.ExecCommandName, `{"command":"`+tc.command+`"}`)}},
				programTurn{text: tc.reply},
			)
			m = m.WithRunner(legacyRunner(func(_ context.Context, cmd string) (string, int) {
				ran = append(ran, cmd)
				return tc.output, 0
			}))
			tm := runProgram(t, m)

			send(tm, tc.ask)
			release()
			waitForText(t, tm, tc.await)
			tm.Send(tc.key)
			waitForText(t, tm, tc.settle)

			frame := finalFrame(t, tm)
			if len(ran) != tc.runs {
				t.Fatalf(tc.ranMsg, ran, frame)
			}
			frameHas(t, frame, tc.wants...)
		})
	}
}

// An edit is gated: the card shows the change, the handover and [y] apply
// it to the file on disk, and the row the transcript keeps is the edit's.
func TestProgram_AnEditCardAppliesTheEdit(t *testing.T) {
	dir := fixtureDir(t, map[string]string{"loop.go": "package agent\n\nconst limit = 25\n"})
	loop := filepath.Join(dir, "loop.go")
	m, _ := scriptedSession(
		programTurn{calls: []provider.ToolCall{call("r1", tools.ReadFileName, fmt.Sprintf(`{"path":%q}`, loop))}},
		programTurn{calls: []provider.ToolCall{call("e1", "edit_file", fmt.Sprintf(`{"path":%q,"old_text":"const limit = 25","new_text":"const limit = 50"}`, loop))}},
		programTurn{text: "The rounds are capped at the limit now"},
	)
	m = m.WithWorkspace(dir).WithToolExecutor(tools.Execute)
	tm := runProgram(t, m)

	send(tm, "cap rounds at the limit")
	waitForText(t, tm, "const limit = 50")
	tm.Send(programHandover)
	tm.Send(programAllow)
	waitForText(t, tm, "The rounds are capped")

	frame := finalFrame(t, tm)
	got, err := os.ReadFile(loop)
	if err != nil || !strings.Contains(string(got), "const limit = 50") {
		t.Fatalf("the allowed edit did not reach the file (%v): %q\n%s", err, got, frame)
	}
	// The read and the edit are one card, whose glyph is the write's.
	frameHas(t, frame, "✎ read", "wrote 1 file", "The rounds are capped at the limit now")
}

// programKey is one keystroke spelled the way the register spells it —
// "ctrl+o", "shift+up", "f12", "j" — and checked against the spelling the
// runtime gives it back, so a key this file sends is the key a binding names.
func programKey(t *testing.T, s string) tea.KeyPressMsg {
	t.Helper()
	parts := strings.Split(s, "+")
	name := parts[len(parts)-1]
	var msg tea.KeyPressMsg
	for _, mod := range parts[:len(parts)-1] {
		switch mod {
		case "ctrl":
			msg.Mod |= tea.ModCtrl
		case "alt":
			msg.Mod |= tea.ModAlt
		case "shift":
			msg.Mod |= tea.ModShift
		}
	}
	named := map[string]rune{
		"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "space": tea.KeySpace,
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"backspace": tea.KeyBackspace,
		"f7":        tea.KeyF7, "f8": tea.KeyF8, "f12": tea.KeyF12,
	}
	if code, ok := named[name]; ok {
		msg.Code = code
	} else {
		msg.Code = []rune(name)[0]
		if msg.Mod == 0 {
			msg.Text = name
		}
	}
	if got := msg.String(); got != s {
		t.Fatalf("the key %q is spelled %q by the runtime", s, got)
	}
	return msg
}

// programPress sends keystrokes in order, each spelled as programKey spells it.
func programPress(t *testing.T, tm *program, keys ...string) {
	t.Helper()
	for _, k := range keys {
		tm.Send(programKey(t, k))
	}
}

// reads is one read_file call per named file, named as the scenes name them:
// relative, so the row's target column shows the file and not a temporary
// directory.
func reads(names ...string) []provider.ToolCall {
	calls := make([]provider.ToolCall, len(names))
	for i, name := range names {
		calls[i] = call(fmt.Sprintf("r%d-%s", i, name), tools.ReadFileName, fmt.Sprintf(`{"path":%q}`, name))
	}
	return calls
}

// readingSession is a session over dir whose auto-run reads run for real.
// The tools resolve a path against the process's directory, which a test may
// never change, so the executor roots a relative path at dir the way a
// child's is rooted at its workspace — the one thing a session started in
// dir gets for free.
func readingSession(dir string, turns ...programTurn) Model {
	m, _ := scriptedSession(turns...)
	return m.WithWorkspace(dir).WithToolExecutor(subagent.RootedExecutor(dir, tools.Execute))
}

var twelve = []string{"one.go", "two.go", "three.go", "four.go", "five.go", "six.go", "seven.go", "eight.go", "nine.go", "ten.go", "eleven.go", "twelve.go"}

// A turn that only read is one card stating its reads, and reading mode's
// cursor reaches the card and opens it onto them in place: the reads under
// their verb, rolled up by directory.
func TestProgram_AFoldedRunOfReadsOpensInPlace(t *testing.T) {
	files := map[string]string{}
	for _, f := range twelve {
		files[f] = "package fixture\n"
	}
	dir := fixtureDir(t, files)
	tm := runProgramAt(t, readingSession(dir,
		programTurn{calls: reads(twelve...)},
		programTurn{text: "The round limit is counted in the loop and nowhere else."},
	), 130, 40)

	send(tm, "how is the round limit counted")
	waitForAll(t, tm, "nowhere else", "read 12 files")
	programPress(t, tm, "ctrl+o", "k", "k", "enter")
	waitForText(t, tm, "./ one.go")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "▾ read 12 files", "./ one.go · two.go", "back to the prompt")
}

// The route to one call: reading mode opens a card of twelve reads onto its
// groups, and a click on the fourth glyph of its strip puts the strip's
// cursor on the fourth read, which the bar then names.
func TestProgram_AClickOnAStripGlyphMovesTheCursor(t *testing.T) {
	files := map[string]string{}
	for _, f := range twelve {
		files[f] = "package fixture\n"
	}
	dir := fixtureDir(t, files)
	tm := runProgramAt(t, readingSession(dir,
		programTurn{calls: reads(twelve...)},
		programTurn{text: "The round limit is counted in the loop and nowhere else."},
	).WithMouse(true), 130, 40)

	send(tm, "how is the round limit counted")
	waitForAll(t, tm, "nowhere else", "read 12 files")
	programPress(t, tm, "ctrl+o", "k", "k", "enter")
	frame := waitForFrame(t, tm, "the open card's strip", func(f string) bool { return strings.Contains(f, "in order ") })
	x, y := -1, -1
	for i, l := range strings.Split(frame, "\n") {
		if at := strings.Index(l, "in order "); at >= 0 {
			x, y = len([]rune(l[:at]))+len("in order ")+3, i
		}
	}
	tm.Send(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
	tm.Send(tea.MouseReleaseMsg{Button: tea.MouseNone, X: x, Y: y})
	waitForText(t, tm, "tool 4 of 12")

	frameHas(t, finalFrame(t, tm), "along the strip", "open that tool")
}

// frameCell is the cell where s starts on a frame, by rune.
func frameCell(t *testing.T, frame, s string) (x, y int) {
	t.Helper()
	for i, l := range strings.Split(frame, "\n") {
		if at := strings.Index(l, s); at >= 0 {
			return len([]rune(l[:at])), i
		}
	}
	t.Fatalf("no %q on the frame:\n%s", s, frame)
	return 0, 0
}

// programClick is a press and a release on one cell.
func programClick(tm *program, x, y int) {
	tm.Send(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
	tm.Send(tea.MouseReleaseMsg{Button: tea.MouseNone, X: x, Y: y})
}

// clickText clicks the cell where s starts on the frame.
func clickText(t *testing.T, tm *program, frame, s string) {
	t.Helper()
	x, y := frameCell(t, frame, s)
	programClick(tm, x, y)
}

// The pointer's route to a card: from the draft, a click on a card's header
// opens it onto its groups, a click on the sentence under a header does
// nothing, and a second click on the header closes it back to the normal
// card (docs/interface/surfaces.md#the-step).
func TestProgram_AClickOnTheHeaderOpensThenCloses(t *testing.T) {
	dir := fixtureDir(t, map[string]string{"loop.go": "package agent\n", "round.go": "package agent\n"})
	tm := runProgramAt(t, readingSession(dir,
		programTurn{text: "Locate the round accounting\n", calls: reads("loop.go", "round.go")},
		programTurn{text: "The limit is a checkpoint, not a wall."},
	).WithMouse(true), 110, 40)

	send(tm, "how is the round limit counted")
	waitForAll(t, tm, "not a wall", "Locate the round accounting")

	// The sentence first: nothing moves, so the header click after it is
	// what the open is waited on.
	frame := waitForFrame(t, tm, "the card", func(f string) bool { return strings.Contains(f, "read 2 files") })
	clickText(t, tm, frame, "Locate the round accounting")
	clickText(t, tm, frame, "read 2 files")
	frame = waitForFrame(t, tm, "the open card", func(f string) bool {
		return strings.Contains(f, "▾ read 2 files") && strings.Contains(f, "Locate the round accounting")
	})
	if strings.Contains(frame, "▸ ⚙") {
		t.Errorf("the open card draws a fold mark:\n%s", frame)
	}
	clickText(t, tm, frame, "  ⚙ read 2 files")
	frame = waitForFrame(t, tm, "the closed card", func(f string) bool {
		return !strings.Contains(f, "▾ read 2 files") && strings.Contains(f, "Locate the round accounting")
	})
	frameHas(t, frame, "  ⚙ read 2 files", "not a wall")
	if strings.Contains(frame, "▸ ⚙") {
		t.Errorf("the closed card draws a fold mark:\n%s", frame)
	}
}

// The pointer's route to one call: a click on the header opens the card, a
// click on a call's row inside it opens that call's own view — the same
// view enter on the strip opens — and esc comes back to the card, still
// open (docs/interface/surfaces.md#the-step).
func TestProgram_AClickOnACallRowOpensItsView(t *testing.T) {
	dir := fixtureDir(t, map[string]string{"loop.go": "package agent\n", "round.go": "package agent\n"})
	calls := append(reads("loop.go", "round.go"), call("g0-glob", tools.GlobName, `{"pattern":"*.go"}`))
	tm := runProgramAt(t, readingSession(dir,
		programTurn{text: "Locate the round accounting\n", calls: calls},
		programTurn{text: "The limit is a checkpoint, not a wall."},
	).WithMouse(true), 110, 40)

	send(tm, "how is the round limit counted")
	waitForAll(t, tm, "not a wall", "Locate the round accounting")
	frame := waitForFrame(t, tm, "the card", func(f string) bool { return strings.Contains(f, "read 2 files") })
	clickText(t, tm, frame, "read 2 files")
	frame = waitForFrame(t, tm, "the open card", func(f string) bool {
		return strings.Contains(f, "▾ read 2 files") && strings.Contains(f, "in order ")
	})
	// The glob's row is the call's own: a group of one has no line over it.
	clickText(t, tm, frame, "glob *.go")
	view := waitForFrame(t, tm, "the call's view", func(f string) bool {
		return strings.Contains(f, "round.go") && !strings.Contains(f, "▾ read 2 files")
	})
	frameHas(t, view, "loop.go", "round.go")
	programPress(t, tm, "esc")
	back := waitForFrame(t, tm, "the open card again", func(f string) bool {
		return strings.Contains(f, "▾ read 2 files") && strings.Contains(f, "not a wall")
	})
	frameHas(t, back, "in order ")
}

// The transcript's search counts what is folded away and walks into it: the
// file is on a row two folds down, and the search still finds it.
func TestProgram_TheSearchReachesInsideTheFolds(t *testing.T) {
	dir := fixtureDir(t, map[string]string{"loop.go": "package agent\n", "round.go": "package agent\n", "errors.go": "package agent\n", "limit.go": "package agent\n"})
	tm := runProgram(t, readingSession(dir,
		programTurn{text: "Locate the round accounting\n", calls: reads("loop.go", "round.go", "errors.go", "limit.go")},
		programTurn{text: "The limit is a checkpoint, not a wall."},
	))

	send(tm, "how is the round limit counted")
	waitForText(t, tm, "not a wall")
	programPress(t, tm, "ctrl+o", "/")
	tm.Send(tea.PasteMsg{Content: "errors.go"})
	waitForText(t, tm, "match inside")
	programPress(t, tm, "enter")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "errors.go", "match inside")
}
