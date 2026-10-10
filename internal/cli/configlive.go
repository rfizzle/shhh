package cli

// What a running session does with a key it reads at a turn boundary.
//
// The settings table says which keys a session takes at its next turn
// (config.Live); this is how each of them is taken: read from the config the
// way the session read it when it opened, and set on the field of the wiring
// the session was opened with, so the session moves through the same path a
// command moves it on. A key the session reads at the call — a flow's model,
// the readings' cadence — is held where its readers ask instead (heldKey).
// See docs/interface/surfaces.md#the-settings-screen.

import (
	"slices"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// liveTaker moves w onto c's value of one key. env is the session the
// settings screen was opened from.
type liveTaker func(c config.Config, env *sessionEnv, w *chat.Wiring)

// liveTakers is every key a session takes at its next turn that is not held
// at the call. A test holds it to the table: a live key with no taker would
// be a row that says `unwritten` and a session that never moves.
var liveTakers = map[string]liveTaker{
	"behavior.default_mode": func(c config.Config, _ *sessionEnv, w *chat.Wiring) {
		// The file's value was judged as it was staged; an empty one is the
		// key reset, which is manual, as a session opens on.
		w.Mode = agent.ModeManual
		if mode, err := agent.ParseMode(c.Behavior.DefaultMode); err == nil && c.Behavior.DefaultMode != "" {
			w.Mode = mode
		}
	},
	"provider.model": func(c config.Config, env *sessionEnv, w *chat.Wiring) {
		w.ModelName = sessionResolved(c, env).Model
	},
	"provider.reasoning": func(c config.Config, env *sessionEnv, w *chat.Wiring) {
		if e, err := provider.ParseEffort(sessionResolved(c, env).Reasoning); err == nil {
			w.Effort = e
		}
	},
	// A round limit staged on the screen is the person's choice for this
	// session, so it is read from the file alone, past a --max-rounds the
	// session was opened with.
	"behavior.max_tool_rounds": func(c config.Config, _ *sessionEnv, w *chat.Wiring) {
		w.MaxToolRounds = maxRoundsFor(c, 0, false)
	},
	"behavior.check_in_interval_rounds": func(c config.Config, _ *sessionEnv, w *chat.Wiring) {
		w.Steering.CheckInInterval = c.Behavior.CheckInIntervalRounds
	},
	"behavior.check_in_max_doublings": func(c config.Config, _ *sessionEnv, w *chat.Wiring) {
		w.Steering.CheckInDoublings = c.Behavior.CheckInMaxDoublings
	},
	"behavior.progress_interval_calls": func(c config.Config, _ *sessionEnv, w *chat.Wiring) {
		w.ProgressCalls = c.Behavior.ProgressIntervalCalls
	},
	"behavior.progress_interval_seconds": func(c config.Config, _ *sessionEnv, w *chat.Wiring) {
		w.ProgressElapsed = time.Duration(c.Behavior.ProgressIntervalSeconds) * time.Second
	},
	"behavior.suggestions": func(c config.Config, _ *sessionEnv, w *chat.Wiring) {
		w.Suggestions = c.SuggestionsEnabled()
	},
	// The palette is the package's, not the session's
	// (components.SetTheme), so it moves here; the session draws again
	// because something moved.
	"appearance.theme": func(c config.Config, _ *sessionEnv, _ *chat.Wiring) {
		_ = components.SetTheme(c.Appearance.Theme)
	},
	"appearance.verbosity": func(c config.Config, _ *sessionEnv, w *chat.Wiring) {
		w.Verbosity = c.Appearance.Verbosity
	},
	"appearance.mouse": func(c config.Config, _ *sessionEnv, w *chat.Wiring) {
		w.MouseOff = !c.MouseEnabled()
	},
	"appearance.notify": func(c config.Config, _ *sessionEnv, w *chat.Wiring) {
		w.NotifyOff = !c.NotifyEnabled()
	},
	"appearance.window_title": func(c config.Config, _ *sessionEnv, w *chat.Wiring) {
		w.WindowTitleOff = !c.WindowTitleEnabled()
	},
	"appearance.paste_lines": func(c config.Config, _ *sessionEnv, w *chat.Wiring) {
		w.PasteLines = c.Appearance.PasteLines
	},
	"appearance.paste_columns": func(c config.Config, _ *sessionEnv, w *chat.Wiring) {
		w.PasteColumns = c.Appearance.PasteColumns
	},
	"appearance.rail_width": func(c config.Config, _ *sessionEnv, w *chat.Wiring) {
		w.RailWidth = components.RailWidthOrAuto(c.Appearance.RailWidth)
	},
}

// sessionResolved is the model and the level the session would have opened on
// over c, in the order it was opened with (sessionEnv.resolveWith).
func sessionResolved(c config.Config, env *sessionEnv) resolve.Resolved {
	if env != nil && env.resolveWith != nil {
		return env.resolveWith(c)
	}
	return resolve.Resolved{Model: c.Provider.Model, Reasoning: c.Provider.Reasoning}
}

// heldAtExit is what a session ending says of the values it took from the
// settings screen that no file holds: they end with the session, and the next
// one starts on the file's, which would otherwise be a surprise. It names
// each key once and reads the files as they stand now, so a value written
// since, or equal to the file's, is not named. Empty when there is nothing
// to say.
func heldAtExit(env *sessionEnv, cfg config.Config) string {
	var names []string
	for _, key := range slices.Compact(slices.Sorted(slices.Values(
		slices.Concat(env.live.names(), env.flows.names())))) {
		held, ok := env.live.value(key)
		if !ok {
			held, _ = env.flows.value(key)
		}
		if held != configLoaded(cfg, key) {
			names = append(names, key)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "this session ends with " + strings.Join(names, ", ") +
		" held for it alone; no file holds them, and the next session starts on the file's"
}
