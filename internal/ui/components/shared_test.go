package components

import (
	"testing"

	"github.com/charmbracelet/colorprofile"
)

// A palette swap rebuilds every style exactly once, and a host's callback sees
// the shared set the swap just installed rather than the one it replaced.
func TestPaletteSwapRebuildsStylesOnce(t *testing.T) {
	withColorProfile(t, colorprofile.TrueColor)
	was := ThemeName()
	t.Cleanup(func() { _ = SetTheme(was) })
	if err := SetTheme(ThemeDark); err != nil {
		t.Fatal(err)
	}

	var calls int
	var seen SharedStyles
	hooks := paletteHooks
	t.Cleanup(func() { paletteHooks = hooks })
	OnPaletteChange(func() { calls++; seen = Shared })

	before := styleBuilds
	if err := SetTheme(ThemeLight); err != nil {
		t.Fatal(err)
	}
	if got := styleBuilds - before; got != 1 {
		t.Errorf("theme switch built the style set %d times, want 1", got)
	}
	if calls != 1 {
		t.Errorf("theme switch ran the host callback %d times, want 1", calls)
	}
	if seen.Dim.GetForeground() != Palette.Dim.Color() {
		t.Error("host callback saw a shared set from the palette that was replaced")
	}

	before = styleBuilds
	calls = 0
	SetMono(true)
	t.Cleanup(func() { SetMono(false) })
	if got := styleBuilds - before; got != 1 || calls != 1 {
		t.Errorf("mono switch: builds %d, callbacks %d, want 1 and 1", got, calls)
	}
	if seen.Dim.GetForeground() != MonoDim.Color() {
		t.Error("host callback did not see the mono set")
	}
}
