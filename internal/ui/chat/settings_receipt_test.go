package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// A write from the settings screen leaves one transcript notice under the
// mark, the receipt the foot row carries, and the screen stays up.
func TestSettings_AWriteHasAReceiptInTheTranscript(t *testing.T) {
	h := newFakeConfigHost()
	m := sendText(t, configModelWith(t, h), "/config")
	m = stageOne(t, m, "3")
	before := len(m.transcript)

	m = pressKeys(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if m.state != stateConfig {
		t.Fatal("the screen closed on the write")
	}
	if got := len(m.transcript) - before; got != 1 {
		t.Fatalf("a write left %d notices, want 1", got)
	}
	want := "wrote 1 change to ~/.config/shhh/config.toml · behavior.check_in_rounds"
	if last := lastNote(m); !strings.HasPrefix(last, want) {
		t.Errorf("the notice reads %q, want it to open with %q", last, want)
	}
	if h.screen.Notice != want {
		t.Errorf("the foot row reads %q, want %q", h.screen.Notice, want)
	}
}

// A write that cannot land says so the same way and keeps the changes.
func TestSettings_AFailedWriteKeepsTheChanges(t *testing.T) {
	h := newFakeConfigHost()
	h.failWith = "permission denied"
	m := sendText(t, configModelWith(t, h), "/config")
	m = stageOne(t, m, "3")

	m = pressKeys(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if m.state != stateConfig || h.screen.Changed != 1 || h.writes != 0 {
		t.Fatalf("a failed write keeps the screen and the edit: state %v, changed %d, writes %d", m.state, h.screen.Changed, h.writes)
	}
	want := "could not write ~/.config/shhh/config.toml: permission denied"
	if h.screen.Notice != want || lastNote(m) != want {
		t.Errorf("foot row %q, transcript %q, want both %q", h.screen.Notice, lastNote(m), want)
	}
}

// The picker's [d] writes provider.model at once and says so in the
// settings screen's own sentence.
func TestModelPicker_ASetDefaultHasAReceipt(t *testing.T) {
	m := readyModel(t).
		WithModelSwitcher(func(string) {}).
		WithConfigWriter(func(string, string) error { return nil }).
		WithDefaults(Defaults{File: "~/.config/shhh/config.toml"}).
		WithPricing(nil, "m1").
		WithModelOptions([]string{"m1", "m2"})
	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	updated, _ = updated.(Model).Update(ctrlU)
	updated, _ = updated.(Model).Update(tea.KeyPressMsg{Code: tea.KeyDown})
	alt := keys.Shown(keys.Select.Alt)
	updated, _ = updated.(Model).Update(tea.KeyPressMsg{Code: []rune(alt)[0], Text: alt})
	last := lastNote(updated.(Model))
	if want := "wrote 1 change to ~/.config/shhh/config.toml · provider.model"; !strings.Contains(last, want) {
		t.Fatalf("the notice reads %q, want %q in it", last, want)
	}
}
