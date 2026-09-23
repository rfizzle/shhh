package chat

// Routes through a turn that is still running: how a command ends, what the
// reader can do while it runs, and the cards a turn puts in front of them
// (program_routes_test.go says what these are for).

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

// commandTurn is a turn that asks to run one command.
func commandTurn(hold chan struct{}, command string) programTurn {
	return programTurn{hold: hold, calls: []provider.ToolCall{
		call("c-"+command, tools.ExecCommandName, fmt.Sprintf(`{"command":%q}`, command)),
	}}
}

// A line the reader starts with ! is their own command: it is put on the
// confirm it summoned, [y] runs it, and its row is in the transcript with no
// model turn behind it.
func TestProgram_ABangLineRunsOnItsConfirm(t *testing.T) {
	var ran []string
	m, p := scriptedSession(programTurn{text: "nobody asked the model"})
	m = m.WithRunner(legacyRunner(func(_ context.Context, cmd string) (string, int) {
		ran = append(ran, cmd)
		return "hi", 0
	}))
	tm := runProgram(t, m)

	send(tm, "!echo hi")
	waitForText(t, tm, "run it once")
	tm.Send(programAllow)
	waitForGone(t, tm, "run it once")

	frame := finalFrame(t, tm)
	if len(ran) != 1 || ran[0] != "echo hi" {
		t.Fatalf("the confirmed command did not reach the runner, got %v\n%s", ran, frame)
	}
	frameHas(t, frame, "echo hi")
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.asked) != 0 {
		t.Fatalf("the reader's own command was sent to the model: %v", p.asked)
	}
}

// A command that exits non-zero and one that reaches the ceiling each leave a
// row that says how it ended, in words and not in a code.
func TestProgram_ABrokenCommandsRowSaysHowItEnded(t *testing.T) {
	hold, release := quietHold(t)
	m, _ := scriptedSession(
		commandTurn(hold, "make lint"),
		programTurn{text: "The failed command is recorded."},
		commandTurn(nil, "sleep 5"),
		programTurn{text: "The timed-out command is recorded."},
	)
	m = m.WithCommandTimeout(50 * time.Millisecond).WithRunner(legacyRunner(func(ctx context.Context, cmd string) (string, int) {
		if cmd == "sleep 5" {
			<-ctx.Done()
			return "", -9
		}
		return "lint: broken rule", 1
	}))
	tm := runProgram(t, m)

	send(tm, "run the checks")
	release()
	waitForText(t, tm, "[y] run it once")
	tm.Send(programAllow)
	waitForText(t, tm, "The failed command is recorded")
	send(tm, "wait for the checks")
	waitForText(t, tm, "sleep 5")
	tm.Send(programHandover)
	tm.Send(programAllow)
	waitForText(t, tm, "The timed-out command is recorded")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "exit 1", "timed out")
}

// The cancel chord while a command runs stops the command, the row says it
// was stopped rather than broken, and the turn goes on.
func TestProgram_TheCancelChordStopsARunningCommand(t *testing.T) {
	hold, release := quietHold(t)
	started := make(chan struct{})
	m, _ := scriptedSession(
		commandTurn(hold, "for i in 1 2 3; do echo round $i counted; sleep 1; done"),
		programTurn{text: "The limit is counted in one place."},
	)
	m = m.WithRunner(legacyRunner(func(ctx context.Context, _ string) (string, int) {
		close(started)
		<-ctx.Done()
		return "", -2
	}))
	tm := runProgram(t, m)

	send(tm, "how is the round limit counted")
	release()
	waitForText(t, tm, "[y] run it once")
	tm.Send(programAllow)
	<-started
	waitForText(t, tm, "running…")
	tm.Send(programCancel)
	waitForText(t, tm, "stopped")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "stopped")
}

// A long command's row tails what it prints while it runs, and settles when
// it ends.
func TestProgram_ARunningCommandTailsIntoItsRow(t *testing.T) {
	hold, release := quietHold(t)
	next := make(chan struct{})
	m, _ := scriptedSession(
		commandTurn(hold, "make test"),
		programTurn{text: "The command finished and its row settled."},
	)
	// The tailed runner is what an assistant command runs through; the plain
	// one beside it is what makes execute_command a tool this session has.
	m = m.WithRunner(legacyRunner(func(context.Context, string) (string, int) {
		t.Error("the command ran without its tail")
		return "", 0
	})).WithTailRunner(legacyTailRunner(func(_ context.Context, _ string, onLine func(string)) (string, int) {
		onLine("ok  github.com/rfizzle/shhh/internal/agent/loop.go  0.412s")
		<-next
		return "ok  github.com/rfizzle/shhh/internal/agent/loop.go  0.412s", 0
	}))
	tm := runProgram(t, m)

	send(tm, "run the checks")
	release()
	waitForText(t, tm, "[y] run it once")
	tm.Send(programAllow)
	waitForAll(t, tm, "running…", "0.412s")
	close(next)
	waitForText(t, tm, "its row settled")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "make test", "The command finished and its row settled.")
}

// The hold key parks the turn at the next round boundary and the frame says
// so before and after; the cancel chord, pressed twice, ends the held turn
// and the frame goes back to the idle rail.
func TestProgram_AHoldParksTheTurnAndTheChordEndsIt(t *testing.T) {
	hold, release := quietHold(t)
	finish := make(chan struct{})
	m, _ := scriptedSession(
		commandTurn(hold, "sleep 8"),
		programTurn{text: "The rail says what each key does."},
	)
	m = m.WithRunner(legacyRunner(func(ctx context.Context, _ string) (string, int) {
		select {
		case <-finish:
		case <-ctx.Done():
		}
		return "", 0
	}))
	tm := runProgram(t, m)

	send(tm, "watch the suite while it runs")
	release()
	waitForText(t, tm, "[y] run it once")
	tm.Send(programAllow)
	waitForText(t, tm, "stop the run")
	programPress(t, tm, "ctrl+p")
	waitForText(t, tm, "holding after this round")
	close(finish)
	waitForText(t, tm, "⏸ held")
	tm.Send(programCancel)
	waitForText(t, tm, "again cancels the turn")
	tm.Send(programCancel)
	waitForText(t, tm, "[ctrl+d] ×2 quit")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "[enter] send")
	if strings.Contains(frame, "The rail says what each key does") {
		t.Fatalf("the held turn ran on after it was cancelled:\n%s", frame)
	}
}

// The always-allow key opens the grants the card can make, leaving that list
// grants nothing, and the card is still there to be refused.
func TestProgram_TheGrantKeyOffersGrantsAndLeavingGrantsNothing(t *testing.T) {
	var ran []string
	m, _ := scriptedSession(
		commandTurn(nil, "npm test --watch"),
		programTurn{text: "That settles it."},
	)
	m = m.WithRunner(legacyRunner(func(_ context.Context, cmd string) (string, int) {
		ran = append(ran, cmd)
		return "", 0
	}))
	tm := runProgram(t, m)

	send(tm, "go on then")
	waitForText(t, tm, "npm test --watch")
	tm.Send(programHandover)
	programPress(t, tm, "a")
	waitForText(t, tm, "until this turn ends")
	programPress(t, tm, "esc")
	waitForText(t, tm, "edit the command")
	tm.Send(programDeny)
	waitForText(t, tm, "That settles it")

	frame := finalFrame(t, tm)
	if len(ran) != 0 {
		t.Fatalf("leaving the grants list let the command run: %v\n%s", ran, frame)
	}
	frameHas(t, frame, "denied", "That settles it.")
}

// While the turn runs, the completion menu and the palette both keep a
// command the turn puts out of reach, greyed and with the reason beside it.
func TestProgram_AMenuGreysWhatTheTurnCannotOffer(t *testing.T) {
	hold, release := quietHold(t)
	m, _ := scriptedSession(
		commandTurn(hold, "sleep 15"),
		programTurn{text: "The menu is what the reader has while the turn runs."},
	)
	m = m.WithRunner(legacyRunner(func(ctx context.Context, _ string) (string, int) {
		<-ctx.Done()
		return "", -2
	}))
	// The palette lists recent files; this one has none, so what it lists
	// does not depend on the directory the suite runs in.
	m.recentFiles = func() []project.RecentFile { return nil }
	tm := runProgram(t, m)

	send(tm, "hold the turn open")
	release()
	waitForText(t, tm, "[y] run it once")
	tm.Send(programAllow)
	waitForText(t, tm, "running…")
	tm.Type("/comp")
	waitForText(t, tm, "idle only")
	programPress(t, tm, "ctrl+/")
	waitForText(t, tm, "matches")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "idle only", "matches")
}

// Shift+Tab walks the permission modes, and each one is written on the frame
// in the mode's own word.
func TestProgram_ShiftTabWalksTheModes(t *testing.T) {
	m, _ := scriptedSession(programTurn{text: "nothing to do"})
	tm := runProgram(t, m)

	for _, word := range []string{"⏵⏵ accept edits", "⏵⏵ auto", "⏸ read-only", "⏸ plan"} {
		programPress(t, tm, "shift+tab")
		waitForText(t, tm, word)
	}

	frame := finalFrame(t, tm)
	frameHas(t, frame, "⏸ plan")
}

// A plan written in plan mode is a card, and [n] on it starts a new session
// carrying the plan's steps.
func TestProgram_APlanCardCarriesThePlanIntoANewSession(t *testing.T) {
	m, _ := scriptedSession(programTurn{text: "Here is what I would do.\n\n## Plan: make the round limit recoverable\n\n1. Locate the round accounting\n   files: loop.go\n   action: read\n2. Return a sentinel when the rounds run out\n   files: loop.go\n   action: edit\n"})
	tm := runProgram(t, m)

	for range 4 {
		programPress(t, tm, "shift+tab")
	}
	waitForText(t, tm, "⏸ plan")
	send(tm, "plan the round counter change")
	waitForText(t, tm, "make the round limit recoverable")
	tm.Send(programHandover)
	waitForText(t, tm, "new session, carry the plan")
	programPress(t, tm, "n")
	waitForText(t, tm, "carried")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "carried · 2 steps")
}

// Several questions in one call are one card of tabs: the handover gives it
// the keyboard, each answer moves to the next tab, and the submit tab sends
// the set.
func TestProgram_AQuestionCardIsAnsweredTabByTab(t *testing.T) {
	m, _ := scriptedSession(
		programTurn{calls: []provider.ToolCall{call("q1", ask.ToolName, `{"questions":[{"question":"Which store should the cache use?","shape":"choose","options":[{"label":"SQLite","detail":"in the checkout already","recommended":true},{"label":"Postgres","detail":"one more service to run"}]},{"question":"Should the migration be reversible?","shape":"confirm"}]}`)}},
		programTurn{text: "Thank you, that settles both."},
	)
	tm := runProgram(t, m.WithAsk())

	send(tm, "go on then")
	waitForText(t, tm, "Which store should the cache use?")
	tm.Send(programHandover)
	waitForText(t, tm, "1 of 2")
	programPress(t, tm, "enter")
	waitForText(t, tm, "2 of 2")
	programPress(t, tm, "y")
	waitForText(t, tm, "all answered")
	programPress(t, tm, "enter")
	waitForText(t, tm, "that settles both")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "Thank you, that settles both.")
}

// A yes-or-no question that lands on an empty draft takes its letter with no
// handover, the way a card on a quiet keyboard does.
func TestProgram_AYesOrNoQuestionTakesItsLetter(t *testing.T) {
	hold, release := quietHold(t)
	m, _ := scriptedSession(
		programTurn{hold: hold, calls: []provider.ToolCall{call("q1", ask.ToolName, `{"question":"Should the migration be reversible?","shape":"confirm"}`)}},
		programTurn{text: "Reversible it is."},
	)
	tm := runProgram(t, m.WithAsk())

	send(tm, "fix the round limit")
	release()
	waitForText(t, tm, "Should the migration be reversible?")
	programPress(t, tm, "y")
	waitForText(t, tm, "Reversible it is")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "Reversible it is.")
}

// A free answer that lands on an empty draft takes the keyboard into its
// field while the rail stays up beside it, and what is typed there is the
// answer the model reads back.
func TestProgram_AFreeAnswerIsTypedBesideTheRail(t *testing.T) {
	hold, release := quietHold(t)
	m, p := scriptedSession(
		programTurn{hold: hold, calls: []provider.ToolCall{call("q1", ask.ToolName, `{"question":"What should the flag be called?","shape":"text"}`)}},
		programTurn{text: "max-rounds it is."},
	)
	tm := runProgramAt(t, m.WithAsk(), 130, 40)

	send(tm, "fix the round limit")
	release()
	waitForText(t, tm, "What should the flag be called?")
	// The rail's own heading, drawn with the card up: a card that took the
	// screen would have taken the rail with it.
	waitForText(t, tm, "CONTEXT")
	programPress(t, tm, "m", "a", "x", "enter")
	waitForText(t, tm, "max-rounds it is")

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.asked) < 2 || !strings.Contains(p.asked[1].Content, `"max"`) {
		t.Fatalf("the typed sentence should be the answer the model reads: %+v", p.asked)
	}
}
