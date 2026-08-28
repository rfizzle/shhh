package chat

import (
	"strings"
	"testing"
)

func TestSuppressAlternateScroll_SavesDisablesAndRestores(t *testing.T) {
	var w strings.Builder
	restore := SuppressAlternateScroll(&w)

	on := w.String()
	if !strings.Contains(on, saveAlternateScroll) {
		t.Errorf("setup did not save the terminal's own setting: %q", on)
	}
	if !strings.Contains(on, disableAlternateScroll) {
		t.Errorf("setup did not disable alternate scroll: %q", on)
	}
	// The save has to precede the disable, or what gets restored is our value
	// rather than the user's.
	if strings.Index(on, saveAlternateScroll) > strings.Index(on, disableAlternateScroll) {
		t.Errorf("saved after disabling, so the restore would put back our own value: %q", on)
	}
	if strings.Contains(on, restoreAlternateScroll) {
		t.Errorf("setup restored the setting it had just disabled: %q", on)
	}

	restore()
	if !strings.Contains(w.String(), restoreAlternateScroll) {
		t.Errorf("restore did not put the setting back: %q", w.String())
	}
}

// A nil writer is what a headless or piped session has; asking it to suppress
// must not panic, and the restore it hands back must stay callable.
func TestSuppressAlternateScroll_NilWriter(t *testing.T) {
	restore := SuppressAlternateScroll(nil)
	if restore == nil {
		t.Fatal("SuppressAlternateScroll(nil) returned no restore function")
	}
	restore()
}
