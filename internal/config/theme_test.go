package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var testThemes = []string{"auto", "dark", "light", "charm"}

// themeDir is a global config directory holding a config.toml and, where text
// is not empty, a theme.toml beside it. It answers with the config path.
func themeDir(t *testing.T, config, theme string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if theme != "" {
		if err := os.WriteFile(filepath.Join(dir, "theme.toml"), []byte(theme), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestTheme_AbsentFileAndAbsentKeyAreAuto(t *testing.T) {
	for name, text := range map[string]string{"no file": "", "no key": "# nothing yet\n"} {
		t.Run(name, func(t *testing.T) {
			path := themeDir(t, "", text)
			state := ReadTheme([]string{themeFileBeside(path)}, "", testThemes)
			if state.Err != nil || state.Name != "auto" || state.Source != ThemeFromDefault {
				t.Fatalf("got %+v, want auto from the default", state)
			}
		})
	}
}

func TestTheme_TheFileNamesTheTableAndWinsOverTheConfig(t *testing.T) {
	path := themeDir(t, "", "name = \"charm\"\n")
	state := ReadTheme([]string{themeFileBeside(path)}, "light", testThemes)
	if state.Name != "charm" || state.Source != ThemeFromFile || state.Migrate || state.Err != nil {
		t.Fatalf("got %+v, want charm from the file", state)
	}
}

// config.toml's appearance.theme is read once as the fallback for a file with
// no name, and says it is waiting to move.
func TestTheme_TheConfigIsTheFallbackForAFileWithNoName(t *testing.T) {
	path := themeDir(t, "", "# empty\n")
	state := ReadTheme([]string{themeFileBeside(path)}, "light", testThemes)
	if state.Name != "light" || state.Source != ThemeFromConfig || !state.Migrate {
		t.Fatalf("got %+v, want light from the config, to migrate", state)
	}
	none := ReadTheme([]string{filepath.Join(t.TempDir(), "theme.toml")}, "light", testThemes)
	if none.Name != "light" || !none.Migrate {
		t.Fatalf("with no file at all: %+v", none)
	}
}

func TestTheme_UnknownNameRefused(t *testing.T) {
	path := themeDir(t, "", "name = \"solarized\"\n")
	state := ReadTheme([]string{themeFileBeside(path)}, "light", testThemes)
	if state.Err == nil {
		t.Fatal("a name no table answers to was taken")
	}
	for _, want := range []string{"theme.toml", `"solarized"`, "auto, dark, light, charm"} {
		if !strings.Contains(state.Err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, state.Err)
		}
	}
	if state.Name != "auto" {
		t.Errorf("the default did not run: %q", state.Name)
	}
}

func TestTheme_FileIsAppliedWholeOrRefusedWhole(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"a parse error", "name = \n", ""},
		{"a value that is not text", "name = 3\n", ""},
		{"a key the file has no use for", "name = \"dark\"\nacent = \"#fff\"\n", "acent"},
		{"a table", "name = \"dark\"\n[colors]\nbody = \"#fff\"\n", "colors"},
		{"a bad name beside good keys", "name = \"nope\"\n", "nope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := themeDir(t, "", tc.text)
			state := ReadTheme([]string{themeFileBeside(path)}, "light", testThemes)
			if state.Err == nil {
				t.Fatalf("the file was taken: %+v", state)
			}
			if state.Name != "auto" || state.Source != ThemeFromDefault {
				t.Errorf("part of a refused file ran: %+v", state)
			}
			if !strings.Contains(state.Err.Error(), state.Path) || !strings.Contains(state.Err.Error(), tc.want) {
				t.Errorf("the refusal does not name the file and %q: %v", tc.want, state.Err)
			}
		})
	}
}

// A checkout's directory is never a place the search goes: every path is
// beside a global config file, and a theme.toml in a repository changes
// nothing about what is read.
func TestTheme_ACheckoutDoesNotLayerOne(t *testing.T) {
	global := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", global)
	t.Setenv("HOME", t.TempDir())
	for _, p := range ThemePaths() {
		if filepath.Base(p) != "theme.toml" || filepath.Base(filepath.Dir(p)) != "shhh" {
			t.Errorf("%s is not beside a config file", p)
		}
	}
	project := writeProject(t, "[appearance]\nmouse = false\n")
	checkout := filepath.Join(filepath.Dir(project), "theme.toml")
	if err := os.WriteFile(checkout, []byte("name = \"light\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range ThemePaths() {
		if filepath.Dir(p) == filepath.Dir(checkout) {
			t.Fatalf("the search reaches the checkout: %s", p)
		}
	}
	state := ReadTheme(ThemePaths(), "", testThemes)
	if state.Name != "auto" || state.Path != "" {
		t.Fatalf("the checkout's theme.toml was read: %+v", state)
	}
	cfg, _, err := LayerProject(Config{}, project)
	if err != nil || cfg.Appearance.Theme != "" {
		t.Fatalf("layering a checkout moved the theme: %q, %v", cfg.Appearance.Theme, err)
	}
}

func TestProject_RefusesAppearanceTheme(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path := writeProject(t, "[appearance]\ntheme = \"light\"\n")
	_, _, err := LayerProject(Config{}, path)
	var refused *ProjectKeyError
	if !errors.As(err, &refused) {
		t.Fatalf("the checkout's file loaded: %v", err)
	}
	if len(refused.Keys) != 1 || refused.Keys[0].Key != "appearance.theme" {
		t.Fatalf("the refusal does not name the theme: %+v", refused.Keys)
	}
	msg := refused.Error()
	if !strings.Contains(msg, "is not read from a checkout's file — ") || !strings.Contains(msg, "theme.toml") {
		t.Errorf("not the allow-list's note, or not pointing at the theme file: %s", msg)
	}
	if RefusedInProject("appearance.theme") == "" {
		t.Error("a write to the checkout is not refused")
	}
	if RefusedInProject("appearance.verbosity") != "" {
		t.Error("the rest of the appearance table became refused")
	}
}

// A write of appearance.theme, from any door, lands in theme.toml and leaves
// config.toml byte for byte as it was.
func TestSettings_ThemeIsWrittenToThemeToml(t *testing.T) {
	path := themeDir(t, handWritten, "# my colours\n")
	if err := Write(path, Edit{Key: "appearance.theme", Value: "charm"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != handWritten {
		t.Errorf("config.toml was touched:\n%s", got)
	}
	got, _ := os.ReadFile(themeFileBeside(path))
	if !strings.HasPrefix(string(got), "# my colours\n") || !strings.HasSuffix(string(got), "name = \"charm\"\n") {
		t.Errorf("theme.toml:\n%s", got)
	}
	// With another edit beside it, each goes to its own file.
	if err := Write(path, Edit{Key: "appearance.theme", Value: "dark"}, Edit{Key: "appearance.mouse", Value: "true"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); strings.Contains(string(got), "theme") || !strings.Contains(string(got), "mouse = true") {
		t.Errorf("config.toml after a mixed write:\n%s", got)
	}
	if got, _ := os.ReadFile(themeFileBeside(path)); !strings.Contains(string(got), `name = "dark"`) {
		t.Errorf("theme.toml after a mixed write:\n%s", got)
	}
	// A file that does not parse is refused untouched.
	bad := themeDir(t, "", "name = \n")
	if err := Write(bad, Edit{Key: "appearance.theme", Value: "dark"}); err == nil {
		t.Error("a file that does not parse was written over")
	}
	if got, _ := os.ReadFile(themeFileBeside(bad)); string(got) != "name = \n" {
		t.Errorf("the refused file changed: %q", got)
	}
	// And with no file yet, a write creates one.
	fresh := themeDir(t, "", "")
	if err := Write(fresh, Edit{Key: "appearance.theme", Value: "light"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(themeFileBeside(fresh)); string(got) != "name = \"light\"\n" {
		t.Errorf("a new theme.toml:\n%s", got)
	}
}
