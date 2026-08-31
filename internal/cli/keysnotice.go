package cli

// The "once" behind the keymap-change notice (internal/ui/chat/keysnotice.go).
// A release that moves keys says so on the first launch after the upgrade and
// then never again; what makes it once is a generation number in the data
// directory. The generation is bumped by the release that rebinds, not per
// key — one notice per rebinding release, however many keys it moved.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/storage"
)

// keymapGeneration numbers the most recent release that moved keys. Bump it
// when a release rebinds again, and the notice shows once more.
const keymapGeneration = 1

const keymapMarkerFile = "keymap_notice"

// keymapNoticeDue reports whether this launch should carry the notice, and
// records that it did. A machine whose data directory cannot be resolved or
// written shows nothing: a notice that cannot be marked seen would show on
// every launch, which teaches the reader to stop reading notices.
func keymapNoticeDue() bool {
	dir, err := storage.Dir()
	if err != nil {
		return false
	}
	path := filepath.Join(dir, keymapMarkerFile)
	if data, err := os.ReadFile(path); err == nil {
		if seen, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && seen >= keymapGeneration {
			return false
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(keymapGeneration)+"\n"), 0o644); err != nil {
		return false
	}
	return true
}
