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
	"regexp"
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

// ThemeOverride is one token the file changes: the truecolor hex, and the
// 256-colour and sixteen-colour rungs as numbers where the file gives them,
// "" where the shipped table's own stands.
type ThemeOverride struct {
	Token, Hex, ANSI256, ANSI16 string
}

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
	// Overrides are the tokens the file changes over the table, in token
	// order, none where it changes none or was refused.
	Overrides []ThemeOverride
	// Migrate is true when the choice came from config.toml's appearance.theme:
	// the file is where it belongs now.
	Migrate bool
}

// ReadTheme reads the first theme file that exists in paths. fallback is
// config.toml's appearance.theme, used only when the file has no name; known
// is the words the theme registry ships, so a name no table answers to is
// refused with the file rather than later without it, and tokens is the names
// the palette lets a file override, for the same reason.
//
// The file is applied whole or refused whole, the keymap file's rule: a parse
// error, a key a theme file does not have, a name no table answers to or a
// token that is not one refuses all of it, and the default runs, so a typo is
// never a line that does nothing.
func ReadTheme(paths []string, fallback string, known, tokens []string) ThemeState {
	state := ThemeState{Name: ThemeAuto}
	for _, p := range paths {
		file, err := readThemeFile(p, known, tokens)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		state.Path = p
		if err != nil {
			state.Err = fmt.Errorf("%s: %w", p, err)
			return state
		}
		state.Overrides = file.overrides
		if file.name != "" {
			state.Name, state.Source = file.name, ThemeFromFile
			return state
		}
		break
	}
	if fallback != "" {
		state.Name, state.Source, state.Migrate = fallback, ThemeFromConfig, true
	}
	return state
}

// themeFile is what one theme file said: the table it names, by name or by
// base, and the tokens it changes.
type themeFile struct {
	name      string
	overrides []ThemeOverride
}

var themeHex = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// readThemeFile is one file's reading, or why the whole file is refused. The
// keys a file has are name, base (the same word, for a file that overrides
// tokens and reads better saying what they are over) and the [tokens] table,
// where each token is a "#rrggbb" or a table of hex with ansi256 and ansi16
// beside it.
func readThemeFile(path string, known, tokens []string) (themeFile, error) {
	var raw struct {
		Name   string         `toml:"name"`
		Base   string         `toml:"base"`
		Tokens map[string]any `toml:"tokens"`
	}
	meta, err := toml.DecodeFile(path, &raw)
	if err != nil {
		return themeFile{}, err
	}
	// [tokens] is read by hand below, so what the decoder left undecoded under
	// it is not a stray key.
	var stray []string
	for _, k := range meta.Undecoded() {
		if k[0] != "tokens" {
			stray = append(stray, k.String())
		}
	}
	if len(stray) > 0 {
		return themeFile{}, fmt.Errorf("%s is not a key a theme file has (it has name, base, tokens)", strings.Join(stray, ", "))
	}
	name, base := strings.TrimSpace(raw.Name), strings.TrimSpace(raw.Base)
	if name != "" && base != "" {
		return themeFile{}, errors.New("name and base both name the table; use one")
	}
	if name == "" {
		name = base
	}
	if name != "" && !slices.Contains(known, name) {
		return themeFile{}, fmt.Errorf("name %q is not a theme (%s)", name, strings.Join(known, ", "))
	}
	overrides, err := readTokens(raw.Tokens, tokens)
	if err != nil {
		return themeFile{}, err
	}
	return themeFile{name: name, overrides: overrides}, nil
}

// readTokens is the [tokens] table as overrides in token order, or the first
// thing wrong with it: a name the palette has no token for, a value that is
// not a #rrggbb, or a rung outside its range.
func readTokens(table map[string]any, tokens []string) ([]ThemeOverride, error) {
	names := make([]string, 0, len(table))
	for n := range table {
		names = append(names, n)
	}
	slices.Sort(names)
	var out []ThemeOverride
	for _, n := range names {
		if !slices.Contains(tokens, n) {
			return nil, fmt.Errorf("tokens.%s is not a token (%s)", n, strings.Join(tokens, ", "))
		}
		o, err := readToken(n, table[n])
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

// readToken is one token's value: a hex, or a table of hex with the rungs.
func readToken(name string, value any) (ThemeOverride, error) {
	o := ThemeOverride{Token: name}
	switch v := value.(type) {
	case string:
		o.Hex = v
	case map[string]any:
		for k := range v {
			if k != "hex" && k != "ansi256" && k != "ansi16" {
				return o, fmt.Errorf("tokens.%s.%s is not a key a token has (it has hex, ansi256, ansi16)", name, k)
			}
		}
		o.Hex, _ = v["hex"].(string)
		var err error
		if o.ANSI256, err = rung(name, "ansi256", v, 255); err != nil {
			return o, err
		}
		if o.ANSI16, err = rung(name, "ansi16", v, 15); err != nil {
			return o, err
		}
	default:
		return o, fmt.Errorf(`tokens.%s is not "#rrggbb" or a table of hex, ansi256 and ansi16`, name)
	}
	if !themeHex.MatchString(o.Hex) {
		return o, fmt.Errorf("tokens.%s is %q, not a #rrggbb colour", name, o.Hex)
	}
	o.Hex = strings.ToLower(o.Hex)
	return o, nil
}

// rung is one of a token's index rungs as the number the palette reads, ""
// where the file leaves it to the shipped table.
func rung(token, key string, v map[string]any, hi int64) (string, error) {
	raw, ok := v[key]
	if !ok {
		return "", nil
	}
	n, isInt := raw.(int64)
	if !isInt || n < 0 || n > hi {
		return "", fmt.Errorf("tokens.%s.%s is %v, not a number from 0 to %d", token, key, raw, hi)
	}
	return strconv.FormatInt(n, 10), nil
}

// writeTheme puts name in the theme file beside the config file at config,
// through the same line edit and atomic replace every other file takes, so a
// comment the person added is kept. An empty name takes the line out. A file
// that does not parse is refused untouched. A file that says base keeps
// saying it, since both keys would refuse it.
func writeTheme(config, name string) error {
	path, _, after, err := edited(themeFileBeside(config), func(doc *document) error {
		key := "name"
		if _, ok := doc.findKey([]string{"base"}); ok {
			key = "base"
		}
		if name == "" {
			return doc.removeKey([]string{key})
		}
		return doc.setTopKey(key, strconv.Quote(name))
	})
	if err != nil {
		return err
	}
	if name == "" && after == "" {
		return nil
	}
	return replaceFile(path, after)
}

// setTopKey sets a key above every table in the file, where setKey would add
// it after the last table's header and make it a token.
func (d *document) setTopKey(key, literal string) error {
	if _, ok := d.findKey([]string{key}); !ok {
		for _, s := range d.spans {
			if s.kind == spanHeader {
				d.splice(s.start, s.start, key+" = "+literal+d.nl+d.nl)
				return nil
			}
		}
	}
	return d.setKey([]string{key}, literal)
}
