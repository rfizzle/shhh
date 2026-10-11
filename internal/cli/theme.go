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
	return config.ReadTheme(config.ThemePaths(), cfg.Appearance.Theme, components.ThemeNames(), components.OverrideTokenNames())
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

// themeOverrides is the file's tokens as the palette reads them.
func themeOverrides(state config.ThemeState) []components.TokenOverride {
	out := make([]components.TokenOverride, len(state.Overrides))
	for i, o := range state.Overrides {
		out[i] = components.TokenOverride{Token: o.Token, Hex: o.Hex, ANSI256: o.ANSI256, ANSI16: o.ANSI16}
	}
	return out
}

// applyThemeOverrides lays the file's tokens over the table now in effect,
// and says once on stderr which of them fall under the contrast floor: a
// warning and not a refusal, because the file is the person's and a pair they
// need may be an unusual one. Call it after the theme is set.
func applyThemeOverrides() {
	state := themeHeld.Load()
	if state == nil || state.Err != nil || len(state.Overrides) == 0 {
		return
	}
	o := themeOverrides(*state)
	if err := components.SetOverrides(o); err != nil {
		fmt.Fprintln(os.Stderr, "shhh: theme overrides not applied:", err)
		return
	}
	if low := components.LowContrast(components.ResolvedTheme(), o); len(low) > 0 {
		words := make([]string, len(low))
		for i, s := range low {
			words[i] = s.String()
		}
		fmt.Fprintln(os.Stderr, "shhh: theme.toml:", strings.Join(words, "; "), "- applied as written")
	}
}

// probeTheme reads the file again rather than what the process held, because
// the doctor is the command that runs when the load failed, and is cheap.
func probeTheme(_ context.Context, cfg config.Config) doctorFinding {
	state := readThemeFor(cfg)
	var low []components.Shortfall
	if state.Err == nil {
		low = components.LowContrast(components.ResolvedTheme(), themeOverrides(state))
	}
	return doctorTheme(state, components.ThemeName(), components.ResolvedTheme(), low)
}

// doctorTheme is the theme row: the file, the table in effect, whether auto
// chose it, and any refusal. A refused file is a warning whose consequence is
// that the default runs, since the person asked for something else. Tokens the
// file changes are counted on the row, and any text token of them under the
// contrast floor is named as a warning that says it was applied: the person
// asked for it.
func doctorTheme(state config.ThemeState, asked, table string, low []components.Shortfall) doctorFinding {
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
	if n := len(state.Overrides); n > 0 {
		f.Detail = joinDetail(f.Detail, fmt.Sprintf("%d over it", n))
	}
	if len(low) > 0 {
		f.Outcome, f.State = "low contrast", components.DoctorWarned
		f.Consequence = "applied as written; under 4.5:1 is hard to read"
		f.FixLabel = "lift the colour"
		for _, s := range low {
			f.Fix = append(f.Fix, s.String())
		}
	}
	return f
}
