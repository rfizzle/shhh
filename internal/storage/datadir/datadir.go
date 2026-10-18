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
func Dir() (string, error) { return xdgDir("XDG_DATA_HOME", ".local", "share") }

// CacheDir is where shhh caches what it can download again: XDG_CACHE_HOME if
// it is set, then ~/.cache/shhh, the same rule the config and data
// directories follow (docs/capabilities/configuration.md#one-layout-everywhere).
// It holds only the price table and the host lists, which are fetched when
// they are missing, so a Mac that still has the old ~/Library/Caches/shhh
// loses nothing by ignoring it. The price table's package asks for it by
// way of pricing.SetCacheDir.
func CacheDir() (string, error) { return xdgDir("XDG_CACHE_HOME", ".cache") }

// xdgDir is the one rule both follow: shhh under the XDG variable when it is
// set, otherwise under the home-relative default.
func xdgDir(env string, home ...string) (string, error) {
	if xdg := os.Getenv(env); xdg != "" {
		return filepath.Join(xdg, "shhh"), nil
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append(append([]string{dir}, home...), "shhh")...), nil
}
