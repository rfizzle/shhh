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
// waiting for the card before it presses anything (interrupt.go). The sleep
// is a lower bound on elapsed time, which a clock can promise, not a guess
// about how fast a machine draws.
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
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if f := tm.frame.Load(); f != nil && !strings.Contains(*f, s) {
			return
		}
		time.Sleep(10 * time.Millisecond)
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
// the keyboard by arriving: its own [y] runs the command with no handover,
// and the row and the reply after it land on the frame.
func TestProgram_ACardOnAnEmptyDraftAnswersToItsOwnKey(t *testing.T) {
	var ran []string
	hold, release := quietHold(t)
	m, _ := scriptedSession(
		programTurn{hold: hold, calls: []provider.ToolCall{call("c1", tools.ExecCommandName, `{"command":"echo hi from the script"}`)}},
		programTurn{text: "Done: the command printed a greeting"},
	)
	m = m.WithRunner(legacyRunner(func(_ context.Context, cmd string) (string, int) {
		ran = append(ran, cmd)
		return "hi from the script", 0
	}))
	tm := runProgram(t, m)

	send(tm, "run something")
	release()
	waitForText(t, tm, "[y] run it once")
	tm.Send(programAllow)
	waitForText(t, tm, "Done: the command printed")

	frame := finalFrame(t, tm)
	if len(ran) != 1 {
		t.Fatalf("the card's own key did not run the command, ran %v:\n%s", ran, frame)
	}
	frameHas(t, frame, "$ run", "echo hi from the script", "Done: the command printed a greeting")
}

// [n] on a card refuses the call: nothing runs, the row says it was the
// reader's refusal, and the turn carries on to its last sentence.
func TestProgram_ACardsNoRefusesTheCall(t *testing.T) {
	var ran []string
	hold, release := quietHold(t)
	m, _ := scriptedSession(
		programTurn{hold: hold, calls: []provider.ToolCall{call("c1", tools.ExecCommandName, `{"command":"go test ./internal/agent/..."}`)}},
		programTurn{text: "That is the shape of it"},
	)
	m = m.WithRunner(legacyRunner(func(_ context.Context, cmd string) (string, int) {
		ran = append(ran, cmd)
		return "", 0
	}))
	tm := runProgram(t, m)

	send(tm, "now run the agent tests")
	release()
	waitForText(t, tm, "[n]")
	tm.Send(programDeny)
	waitForText(t, tm, "That is the shape of it")

	frame := finalFrame(t, tm)
	if len(ran) != 0 {
		t.Fatalf("a refused command ran: %v\n%s", ran, frame)
	}
	frameHas(t, frame, "go test ./internal/agent/...", "denied", "That is the shape of it")
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
	frameHas(t, frame, "✎ edit", "approved by you", "The rounds are capped at the limit now")
}

// programKey is one keystroke spelled the way the register spells it —
// "ctrl+o", "shift+up", "alt+v", "j" — and checked against the spelling the
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

// A turn that only read folds its reads to one counted row, and reading
// mode's cursor reaches that row and opens it in place.
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
	waitForAll(t, tm, "nowhere else", "12 reads")
	programPress(t, tm, "ctrl+o", "k", "k", "enter")
	waitForText(t, tm, "eleven.go")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "one.go", "eleven.go", "back to the prompt")
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
