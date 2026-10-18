package components

import "testing"

func TestEscGlossOnList(t *testing.T) {
	for _, g := range []string{"back", "keeps waiting", "back — nothing restored", "keeps waiting — nothing is committed"} {
		if !EscGlossOnList(g) {
			t.Errorf("%q is on the closed list and reads as off it", g)
		}
	}
	for _, g := range []string{"leave", "stay", "dismiss", "cancel", "back to the shell", "keep gpt-5", "backward", "back —", "keeps waiting, nothing written", "stop"} {
		if EscGlossOnList(g) {
			t.Errorf("%q is off the closed list and reads as on it", g)
		}
	}
}
