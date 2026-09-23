package chat

// Routes where the session speaks for itself over a turn: the gate's verdict
// settling what broke, a call refused before a card, the tree moving under
// the turn, and the check-in (program_routes_test.go says what these are
// for).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
)

// A command that broke is answered by the suite the turn runs after it: the
// broken row stays in the feed and the close says the gate is passing.
func TestProgram_TheGatesPassSettlesTheFailureBeforeIt(t *testing.T) {
	hold, release := quietHold(t)
	m, _ := scriptedSession(
		programTurn{hold: hold, text: "Check where the rounds are counted\n", calls: []provider.ToolCall{
			call("c1", "execute_command", `{"command":"make test"}`),
		}},
		programTurn{text: "Off by one at the loop bound. Verifying against the suite\n", calls: []provider.ToolCall{
			call("g1", quality.ToolName, `{"action":"run","suite":"default"}`),
		}},
		programTurn{text: "The bound is fixed and the suite is green."},
	)
	pass := &quality.Result{Suite: "default", Verdict: quality.VerdictPass, Trusted: true,
		Checks: []quality.CheckResult{{Name: "test"}, {Name: "vet"}}}
	m = m.WithRunner(legacyRunner(func(context.Context, string) (string, int) {
		return "--- FAIL: TestLoopRounds\n    loop_test.go:214: want 3 rounds, got 4", 1
	})).WithToolExecutor(func(name string, _ json.RawMessage) (string, error) {
		if name != quality.ToolName {
			t.Errorf("an unexpected auto-run call: %s", name)
		}
		return pass.Format(pass.Fingerprint), nil
	})
	tm := runProgramAt(t, m, 130, 40)

	send(tm, "stop the loop double-counting rounds")
	release()
	waitForText(t, tm, "[y] run it once")
	tm.Send(programAllow)
	waitForText(t, tm, "suite is green")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "exit 1", "quality gate default passing · 2/2 checks")
}

// A write the queue will not put to anybody — here an edit naming no file —
// is refused before a card, and the refusal is the call's own row.
func TestProgram_ACallRefusedBeforeItsCardIsItsOwnRow(t *testing.T) {
	m, _ := scriptedSession(
		programTurn{calls: []provider.ToolCall{call("e1", "edit_file", `{"old_text":"round >= cap","new_text":"round > cap"}`)}},
		programTurn{text: "The edit was refused before it ran."},
	)
	tm := runProgram(t, m)

	send(tm, "raise the round cap by one")
	waitForText(t, tm, "refused before it ran")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "(no path)", "The edit was refused before it ran.")
	if strings.Contains(frame, "[y]") {
		t.Fatalf("a refused call reached a card:\n%s", frame)
	}
}

// Somebody else changing a tracked file between two turns is a notice at the
// next turn's boundary, naming the file.
func TestProgram_TheTreeMovingUnderASessionIsANotice(t *testing.T) {
	ws := treeRepo(t)
	tm := runProgram(t, readingSession(ws,
		programTurn{text: "The build is clean."},
		programTurn{calls: reads("a.txt")},
		programTurn{text: "Nothing I did touched it, so somebody else has it open."},
	).WithChangeset(changeset.New(changeset.DefaultMaxBytes), nil).
		WithTreeCheck(&agent.TreeCheck{Dir: ws}))

	send(tm, "build it")
	waitForText(t, tm, "The build is clean")
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("and a line from the next terminal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	send(tm, "anything else?")
	waitForText(t, tm, "somebody else has it open")

	frameHas(t, finalFrame(t, tm), "tree moved", "a.txt")
}

// The session takes stock on its own clock: a turn that runs past the
// check-in interval draws the check-in row in the transcript.
func TestProgram_ALongTurnDrawsTheCheckIn(t *testing.T) {
	dir := fixtureDir(t, map[string]string{"one.go": "package fixture\n", "two.go": "package fixture\n", "three.go": "package fixture\n"})
	tm := runProgram(t, readingSession(dir,
		programTurn{calls: reads("one.go")},
		programTurn{calls: reads("two.go")},
		programTurn{calls: reads("three.go")},
		programTurn{text: "The constant has one home now."},
	).WithSteering(agent.Steering{CheckInInterval: 2}))

	send(tm, "hold the round limit in one place")
	waitForText(t, tm, "one home now")

	frameHas(t, finalFrame(t, tm), "Check-in —")
}
