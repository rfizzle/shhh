package config

// The theme file. Which colour table every surface draws with is chosen in a
// file of its own, `theme.toml`, beside config.toml and keybindings.toml: read
// once as the process starts, the user's alone, applied whole or refused
// whole. See docs/capabilities/configuration.md#the-theme-file.

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// ThemeKey is the settings key the theme file stands in for. The settings
// table keeps a row for it so the screen still lists it, but a write to it
// goes to the theme file (Write) and a checkout may not set it
// (projectRefusals).
const ThemeKey = "appearance.theme"

// ThemeAuto is the name in effect when neither file says one: the table
// chosen for the ground the terminal reports.
const ThemeAuto = "auto"

// ThemePaths returns the theme file's paths in the config search order, one
// beside each config file, so the directory that holds config.toml holds
// everything the user wrote for shhh. A checkout does not layer one: nothing
// here takes a checkout's path.
func ThemePaths() []string {
	var out []string
	for _, p := range Paths() {
		out = append(out, themeFileBeside(p))
	}
	return out
}

// themeFileBeside is the theme file that sits next to a config file.
func themeFileBeside(config string) string {
	return filepath.Join(filepath.Dir(config), "theme.toml")
}

// ThemeSource says where the name in effect came from.
type ThemeSource int

const (
	// ThemeFromDefault is neither file saying anything: auto.
	ThemeFromDefault ThemeSource = iota
	// ThemeFromFile is the theme file's own name.
	ThemeFromFile
	// ThemeFromConfig is config.toml's appearance.theme, read as the fallback
	// for a theme file with no name.
	ThemeFromConfig
)

// ThemeState is what the theme file made of this process's choice: the file
// that was read, the word in effect and where it came from, and the refusal
// if the file was refused. A refused file leaves the default running.
type ThemeState struct {
	// Path is the file that was read, "" where none exists.
	Path string
	// Name is the word in effect, never empty.
	Name   string
	Source ThemeSource
	// Err is why the file was refused. Name is auto when it is set.
	Err error
	// Migrate is true when the choice came from config.toml's appearance.theme:
	// the file is where it belongs now.
	Migrate bool
}

// ReadTheme reads the first theme file that exists in paths. fallback is
// config.toml's appearance.theme, used only when the file has no name; known
// is the words the theme registry ships, so a name no table answers to is
// refused with the file rather than later without it.
//
// The file is applied whole or refused whole, the keymap file's rule: a parse
// error, a key a theme file does not have or a name no table answers to
// refuses all of it, and the default runs, so a typo is never a line that does
// nothing.
func ReadTheme(paths []string, fallback string, known []string) ThemeState {
	state := ThemeState{Name: ThemeAuto}
	for _, p := range paths {
		name, err := readThemeFile(p, known)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		state.Path = p
		if err != nil {
			state.Err = fmt.Errorf("%s: %w", p, err)
			return state
		}
		if name != "" {
			state.Name, state.Source = name, ThemeFromFile
			return state
		}
		break
	}
	if fallback != "" {
		state.Name, state.Source, state.Migrate = fallback, ThemeFromConfig, true
	}
	return state
}

// readThemeFile is one file's name, "" where the file does not set one.
func readThemeFile(path string, known []string) (string, error) {
	var file struct {
		Name string `toml:"name"`
	}
	meta, err := toml.DecodeFile(path, &file)
	if err != nil {
		return "", err
	}
	if und := meta.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return "", fmt.Errorf("%s is not a key a theme file has (it has name)", strings.Join(keys, ", "))
	}
	name := strings.TrimSpace(file.Name)
	if name != "" && !slices.Contains(known, name) {
		return "", fmt.Errorf("name %q is not a theme (%s)", name, strings.Join(known, ", "))
	}
	return name, nil
}

// writeTheme puts name in the theme file beside the config file at config,
// through the same line edit and atomic replace every other file takes, so a
// comment the person added is kept. An empty name takes the line out. A file
// that does not parse is refused untouched.
func writeTheme(config, name string) error {
	path, _, after, err := edited(themeFileBeside(config), func(doc *document) error {
		if name == "" {
			return doc.removeKey([]string{"name"})
		}
		return doc.setKey([]string{"name"}, strconv.Quote(name))
	})
	if err != nil {
		return err
	}
	if name == "" && after == "" {
		return nil
	}
	return replaceFile(path, after)
}
