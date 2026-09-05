package chat

// The one-time notice a keymap change rides in on (
// docs/interface/surfaces.md#the-input-frame). Rebinding a key a hand has
// already learned is a real cost, and paying it silently turns the first
// session after an upgrade into a series of surfaces nobody asked for — so
// the release that moves keys says so once, on the notice rail, and then
// never again. The CLI decides the "once" (it holds the marker); this file
// owns the words, built from the register so the notice cannot name a key
// the dispatch stopped answering.

import (
	"strings"

	"github.com/rfizzle/shhh/internal/ui/keys"
)

// KeysChangedNotice is the notice-rail row for this release's rebinds. One
// row, newest change first, ending with the door to the full list.
//
// One row is the whole budget, and it is what decides the length of this
// list: a row that runs past the terminal's edge loses the newest change,
// which is the one entry it exists for. So the oldest entries go as new ones
// arrive — the two prefixes that gained a meaning rather than keys that moved
// were the first to, being the two nobody has had to relearn in releases.
func KeysChangedNotice() string {
	changes := []string{
		keys.Shown(keys.Draft.Queue) + " queue",
		keys.Shown(keys.Draft.Agents) + " agents",
		keys.Shown(keys.Draft.PointDown) + " pointer",
		keys.Shown(keys.Draft.Palette) + " palette",
		keys.Shown(keys.Draft.Pause) + " hold",
		keys.Shown(keys.Draft.Reading) + " reading",
	}
	return "keys changed: " + strings.Join(changes, " · ") + " — /help keys"
}

// WithKeysNotice puts the notice on the rail for this session. The caller
// has already decided it is due; the model only shows it.
func (m Model) WithKeysNotice(notice string) Model {
	m.keysNotice = notice
	return m
}
