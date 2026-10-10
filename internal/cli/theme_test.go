package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// The settings table lists the words the picker offers; the palette's
// registry is where a theme is added, so the two cannot drift.
func TestTheme_TheSettingsRowOffersTheRegistrysWords(t *testing.T) {
	s, ok := config.Lookup(config.ThemeKey)
	if !ok {
		t.Fatal("no appearance.theme row")
	}
	if !slices.Equal(s.Values, components.ThemeNames()) {
		t.Errorf("the row offers %v and the registry ships %v", s.Values, components.ThemeNames())
	}
}

func TestTheme_TheRowReadsThemeTomlAndTheFileDecidesTheLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(dir, "shhh"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "shhh", "theme.toml")
	if err := os.WriteFile(file, []byte("name = \"charm\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := withThemeFile(config.Config{})
	if cfg.Appearance.Theme != "charm" {
		t.Fatalf("the file's name did not reach the settings: %q", cfg.Appearance.Theme)
	}
	rows := configRows(cfg, cfg, config.Project{})
	for _, r := range rows {
		if r.Key == config.ThemeKey && (r.Value != "charm" || r.Source != "theme.toml") {
			t.Errorf("the row reads %q from %q", r.Value, r.Source)
		}
	}
	// A refused file leaves auto, whatever config.toml says.
	if err := os.WriteFile(file, []byte("name = \"nope\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg = withThemeFile(config.Config{Appearance: config.AppearanceConfig{Theme: "light"}})
	if cfg.Appearance.Theme != "auto" {
		t.Errorf("a refused file did not leave the default: %q", cfg.Appearance.Theme)
	}
	if st := themeHeld.Load(); st == nil || st.Err == nil {
		t.Error("the refusal was not held for the doctor and stderr")
	}
}

func TestDoctorTheme_NamesTheFileTheTableAndTheRefusal(t *testing.T) {
	refused := doctorTheme(config.ThemeState{
		Path: "/h/shhh/theme.toml", Err: os.ErrInvalid,
	}, "auto", "dark")
	if refused.Outcome != "refused" || refused.State != components.DoctorWarned ||
		!strings.Contains(refused.Detail, "auto → dark") || !strings.Contains(refused.Consequence, "default") {
		t.Errorf("refused: %+v", refused)
	}
	ok := doctorTheme(config.ThemeState{Path: "/h/shhh/theme.toml", Name: "charm"}, "charm", "charm")
	if ok.Outcome != "ok" || ok.Detail != "charm" {
		t.Errorf("ok: %+v", ok)
	}
	moved := doctorTheme(config.ThemeState{Name: "light", Migrate: true}, "light", "light")
	if !strings.Contains(moved.Detail, "set in config.toml") {
		t.Errorf("no migration note: %+v", moved)
	}
}
