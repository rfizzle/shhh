package chat

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/ui/keys"
)

// The chat size's table is the register's pane screens and nothing else, and
// a screen that names a key surface is named by that surface's own row: the
// row is a takeover and says the command that opens the screen.
func TestScreens_ChatTableMatchesTheRegister(t *testing.T) {
	table := chatScreens()
	if len(table) != 16 {
		t.Errorf("the chat table has %d screens, want 16", len(table))
	}
	seen := map[state]bool{}
	for _, spec := range table {
		if seen[spec.state] {
			t.Errorf("state %d is in the table twice", spec.state)
		}
		seen[spec.state] = true
		o := overlayFor(spec.state)
		if o == nil {
			t.Errorf("state %d is in the table and not in the register", spec.state)
			continue
		}
		if o.place != spec.place || o.place != placePane {
			t.Errorf("state %d places %d, table says %d, want a pane", spec.state, o.place, spec.place)
		}
		if o.command != spec.command {
			t.Errorf("state %d opens by %q, table says %q", spec.state, o.command, spec.command)
		}
		if spec.surface == keys.NoSurface {
			continue
		}
		surface := spec.surface.Surface()
		if surface.Position != keys.Takeover {
			t.Errorf("state %d is a pane screen and its surface %q is not a takeover", spec.state, surface.Name)
		}
		if spec.command != "" && !strings.Contains(surface.Reached, spec.command) {
			t.Errorf("surface %q is reached by %q, and its screen opens by %s", surface.Name, surface.Reached, spec.command)
		}
	}
	// Every pane row of the register that is not a viewer is in the table.
	viewers := map[state]bool{stateDiffFull: true, stateOutputFull: true, stateKeyList: true, statePreview: true, statePasteView: true}
	for s, o := range overlays() {
		if o.place == placePane && !viewers[s] && !seen[s] {
			t.Errorf("state %d takes the pane and the chat table does not offer it", s)
		}
	}
}
