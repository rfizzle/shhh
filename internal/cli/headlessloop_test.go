package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// The loop's settings are written once, by the applier the screen's
// constructor uses. A tail that sets one by hand again is a second copy that
// agrees only on the day it is written, so each tail is held to a single call
// of the applier and to none of the setters it stands for.
func TestServe_AppliesTheLoopSettingsOnce(t *testing.T) {
	holdsToTheApplier(t, "serve.go")
}

func TestPrint_AppliesTheLoopSettingsOnce(t *testing.T) {
	holdsToTheApplier(t, "print.go")
}

func holdsToTheApplier(t *testing.T, file string) {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if n := strings.Count(src, "chat.ApplyLoop("); n != 1 {
		t.Errorf("%s calls the loop applier %d times, want once", file, n)
	}
	for _, set := range []string{
		"a.SetExecutor(", "a.SetMaxRounds(", "a.SetSteering(", "a.SetProgressIntervals(",
		"a.SetScrub(", "a.StoreElided(", "a.KeepResults(",
	} {
		if strings.Contains(src, set) {
			t.Errorf("%s sets the loop by hand with %s; the applier does that", file, set)
		}
	}
}

// A served session has no settings screen, so a live key it takes is one
// written to the file: the next turn's start reads the file again and sets
// the loop through the screen's applier, and a key the session wires when it
// opens is left as it opened.
func TestHeadlessLoop_ALiveKeyLandsAtTheNextBoundary(t *testing.T) {
	for _, key := range loopKeys {
		if liveTakers[key] == nil {
			t.Fatalf("the loop key %s has no taker", key)
		}
	}
	path := pointConfigAt(t, "[behavior]\nmax_tool_rounds = 30\n")
	a := agent.New(nil, nil)
	opened := chat.Wiring{MaxToolRounds: 30, Steering: agent.Steering{SteerTargetChars: 77}}
	chat.ApplyLoop(a, opened)
	env := &sessionEnv{provName: "anthropic", modelName: "session-model"}
	retake := retakeLoop(a, opened, env, "", 0, false)

	must(t, os.WriteFile(path, []byte("[behavior]\nmax_tool_rounds = 40\ncheck_in_interval_rounds = 9\n"+
		"[summary]\nsteer_target_chars = 5\ninterval_rounds = 6\nmodel = \"reader-model\"\n"), 0o644))
	if got := a.MaxRounds(); got != 30 {
		t.Fatalf("the loop moved before the boundary: %d rounds", got)
	}
	retake()
	if got := a.MaxRounds(); got != 40 {
		t.Errorf("the next turn runs on %d rounds, want the written 40", got)
	}
	if got := a.Steering().CheckInInterval; got != 9 {
		t.Errorf("the next turn checks in every %d rounds, want the written 9", got)
	}
	if got := a.Steering().SteerTargetChars; got != 77 {
		t.Errorf("a key read at the open moved to %d at a turn boundary", got)
	}
	// A key the session's readers ask for at the call is held for them.
	if rounds, _ := env.cadenceAt(config.Config{})(); rounds != 6 {
		t.Errorf("the next reading is scheduled every %d rounds, want the written 6", rounds)
	}
	if got := env.flowModelAt(config.Config{}, flowReading)(); got != "reader-model" {
		t.Errorf("the reading asks %q, want the written model", got)
	}

	// A round cap the session was served with outranks the file, as it did
	// when it opened.
	flagged := agent.New(nil, nil)
	retakeLoop(flagged, opened, nil, "", 12, true)()
	if got := flagged.MaxRounds(); got != 12 {
		t.Errorf("a --max-rounds session runs on %d rounds after the boundary, want its own 12", got)
	}
}
