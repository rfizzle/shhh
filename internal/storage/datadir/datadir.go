// Package datadir names where shhh keeps its data, and nothing else.
//
// It is a leaf of its own so that a package which only has to know the state
// directory, such as the sandbox that masks it, does not depend on the
// store and everything the store reaches. storage.Dir returns it.
package datadir

import (
	"os"
	"path/filepath"
)

// Dir is one layout on every platform: XDG_DATA_HOME if it is set, then
// ~/.local/share/shhh. macOS used to put the store under ~/Library/Application
// Support alongside the config file, which mixed two kinds of state — settings
// a person edits and a database they never touch — into one directory.
// See docs/capabilities/configuration.md#one-layout-everywhere. A machine
// still holding the old directory is detected by `shhh doctor`, which offers
// to move it (internal/migrate).
func Dir() (string, error) {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "shhh"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "shhh"), nil
}
