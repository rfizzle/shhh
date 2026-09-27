package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// TestKeyRunOption_OnlyTheMacKeyboardNamesTheOptionSetting: the sentence
// about the Option key is a fact about a Mac's terminal, so it is decided by
// the keyboard in force (keys.Platform) and not by the host. The same alt
// chord, offered first, carries it under the Mac's keyboard and nothing under
// the Linux one — which is a README picture drawn on a Mac with the Linux
// keyboard (docs/interface/reserved-keys.md#a-mac-ships-without-alt).
func TestKeyRunOption_OnlyTheMacKeyboardNamesTheOptionSetting(t *testing.T) {
	run := []TurnKey{{Key: "[u]", Label: "undo turn", Chord: "[alt+z]"}}

	t.Run("the Mac's keyboard", func(t *testing.T) {
		t.Cleanup(keys.UsePlatform("darwin"))
		if got := ansi.Strip(KeyRunOption(run, true, true)); !strings.Contains(got, optionRow) {
			t.Fatalf("an alt chord under the Mac's keyboard should name the Option setting, got %q", got)
		}
	})

	t.Run("the Linux keyboard", func(t *testing.T) {
		t.Cleanup(keys.UsePlatform("linux"))
		if got := KeyRunOption(run, true, true); got != "" {
			t.Fatalf("an alt chord under the Linux keyboard names a Mac's setting: %q", ansi.Strip(got))
		}
	})
}
