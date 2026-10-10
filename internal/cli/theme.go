package cli

// The theme file's half of the top of the process: reading it beside the
// settings, saying a refusal once, and the doctor's row. The words a file may
// name come from the palette's own registry (components.ThemeNames), so a new
// table is one row and one word there and nothing here.
// See docs/capabilities/configuration.md#the-theme-file.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// themeHeld is what the last load made of the theme file, kept so the stderr
// line is said once per process however many times the settings are read
// again.
var themeHeld atomic.Pointer[config.ThemeState]

// readThemeFor reads the theme file beside the settings, with the settings'
// own appearance.theme as the fallback for a file that names nothing.
func readThemeFor(cfg config.Config) config.ThemeState {
	return config.ReadTheme(config.ThemePaths(), cfg.Appearance.Theme, components.ThemeNames())
}

// withThemeFile puts the theme the file chose where the rest of the product
// reads it: a name the file gave replaces the setting, and a refused file
// leaves auto. A checkout never reaches this: its layer is applied after, and
// may not set the key (config.RefusedInProject).
func withThemeFile(cfg config.Config) config.Config {
	state := readThemeFor(cfg)
	themeHeld.Store(&state)
	switch {
	case state.Err != nil:
		cfg.Appearance.Theme = config.ThemeAuto
	case state.Source == config.ThemeFromFile:
		cfg.Appearance.Theme = state.Name
	}
	return cfg
}

// sayThemeRefusal is the one line on stderr when the file was refused, in the
// keymap's words and for its reason: a session quietly running neither the
// file's colours nor the ones it expected is the failure.
func sayThemeRefusal() {
	if state := themeHeld.Load(); state != nil && state.Err != nil {
		fmt.Fprintln(os.Stderr, "shhh: theme refused:", state.Err, "- the default theme runs")
	}
}

// probeTheme reads the file again rather than what the process held, because
// the doctor is the command that runs when the load failed, and is cheap.
func probeTheme(_ context.Context, cfg config.Config) doctorFinding {
	return doctorTheme(readThemeFor(cfg), components.ThemeName(), components.ResolvedTheme())
}

// doctorTheme is the theme row: the file, the table in effect, whether auto
// chose it, and any refusal. A refused file is a warning whose consequence is
// that the default runs, since the person asked for something else.
func doctorTheme(state config.ThemeState, asked, table string) doctorFinding {
	in := table
	if asked == config.ThemeAuto {
		in = "auto → " + table
	}
	if state.Err != nil {
		return doctorFinding{
			Subject: shortPath(state.Path), Outcome: "refused", State: components.DoctorWarned,
			Detail:      in,
			Consequence: "the default theme runs instead of this file",
			FixLabel:    "fix the file",
			Fix: []string{
				strings.TrimPrefix(state.Err.Error(), state.Path+": "),
				"it takes one key, name, one of " + strings.Join(components.ThemeNames(), ", "),
			},
		}
	}
	f := doctorFinding{Subject: "no theme.toml", Detail: in, Outcome: "ok"}
	if state.Path != "" {
		f.Subject = shortPath(state.Path)
	}
	if state.Migrate {
		f.Detail = joinDetail(in, "set in config.toml, move it")
	}
	return f
}
