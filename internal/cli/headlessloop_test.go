package cli

import (
	"os"
	"strings"
	"testing"
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
