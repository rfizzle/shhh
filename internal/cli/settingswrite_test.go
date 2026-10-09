package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/ui/components"
)

func stageTwo(t *testing.T, session interface {
	Answer(bool, components.ConfigResult) string
}) {
	t.Helper()
	for _, c := range []components.ConfigChange{
		{Key: "provider.model", Value: "claude-sonnet-5"},
		{Key: "behavior.command_timeout_seconds", Value: "90"},
	} {
		c := c
		if note := session.Answer(false, components.ConfigResult{Change: &c}); note != "" {
			t.Fatalf("staging %s left a row: %q", c.Key, note)
		}
	}
}

type answerer func(bool, components.ConfigResult) string

func (a answerer) Answer(done bool, r components.ConfigResult) string { return a(done, r) }

// A write answers with the file and the keys it changed, on the screen's foot
// row and as the one row a session keeps, and leaves nothing staged.
func TestSettings_AWriteHasAReceipt(t *testing.T) {
	path := pointConfigAt(t, "")
	session, err := configSessionOpener(nil)(nil)
	must(t, err)
	stageTwo(t, answerer(session.Answer))

	note := session.Answer(false, components.ConfigResult{Write: true})
	receipt := "wrote 2 changes to " + session.Screen.Path + " · "
	if !strings.HasPrefix(note, receipt) || !strings.Contains(note, "provider.model") ||
		!strings.Contains(note, "behavior.command_timeout_seconds") {
		t.Fatalf("the transcript row reads %q, want it to open with %q and name both keys", note, receipt)
	}
	if got := session.Screen.Notice; !strings.HasPrefix(got, receipt) || strings.Contains(got, "\n") {
		t.Errorf("the foot row reads %q, want one line opening with %q", got, receipt)
	}
	if session.Screen.Changed != 0 {
		t.Errorf("%d changes still standing after the write", session.Screen.Changed)
	}
	got, err := os.ReadFile(path)
	must(t, err)
	if !strings.Contains(string(got), "claude-sonnet-5") || !strings.Contains(string(got), "90") {
		t.Fatalf("the file does not hold the edits:\n%s", got)
	}
	// A second press has nothing left to write and says so.
	if note := session.Answer(false, components.ConfigResult{Write: true}); note == "" ||
		session.Screen.Notice != "nothing staged to write" {
		t.Errorf("a repeat write answered %q, foot row %q", note, session.Screen.Notice)
	}
}

// A write that cannot land says why, names the path, and keeps every change
// staged so the key can be pressed again.
func TestSettings_AFailedWriteKeepsTheChanges(t *testing.T) {
	path := pointConfigAt(t, "")
	session, err := configSessionOpener(nil)(nil)
	must(t, err)
	stageTwo(t, answerer(session.Answer))
	must(t, os.Mkdir(path, 0o755)) // a directory where the file belongs

	note := session.Answer(false, components.ConfigResult{Write: true})
	want := "could not write " + session.Screen.Path + ": "
	if !strings.HasPrefix(note, want) || session.Screen.Notice != note {
		t.Fatalf("transcript %q, foot row %q, want both opening with %q", note, session.Screen.Notice, want)
	}
	if session.Screen.Changed != 2 {
		t.Errorf("%d changes staged after a failed write, want 2", session.Screen.Changed)
	}
}
