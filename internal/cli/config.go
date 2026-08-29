package cli

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View and edit configuration",
		Long:  "Interactive configuration wizard. Use 'config set <key> <value>' for non-interactive changes.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			p := newProgram(newConfigModel(cfg))
			finalModel, err := p.Run()
			if err != nil {
				return err
			}
			result := finalModel.(configModel)
			if result.err != nil {
				return result.err
			}
			if result.saved {
				fmt.Fprintln(cmd.OutOrStderr(), "Configuration saved to "+config.WritePath())
			}
			return nil
		},
	}

	cmd.AddCommand(newConfigSetCmd())
	return cmd
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a config value",
		Long:  "Set a configuration key. Example: shhh config set provider.default openai",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if err := config.Set(&cfg, args[0], args[1]); err != nil {
				return err
			}
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\n", args[0], args[1])
			return nil
		},
	}
}

// configModel hosts the config screen (
// docs/interface/surfaces.md#the-supporting-screens). It owns everything the
// screen deliberately does not: what a setting means, what its default is,
// which answers it offers, and when any of it reaches the file.
//
// Two copies of the config are held. base is what was loaded and is what a
// row is compared against to say where its value came from; cfg is what the
// staged edits have made of it. Nothing is written until [w] — which is the
// rule the old wizard broke by saving on every keystroke.
type configModel struct {
	base  config.Config
	cfg   config.Config
	saved bool
	err   error
	width int

	screen components.ConfigScreen
}

// defaultConfigWidth is what the screen is drawn at before the terminal has
// said how wide it is — the working width the artboard is drawn at.
const defaultConfigWidth = 110

func newConfigModel(cfg config.Config) configModel {
	m := configModel{base: cfg, cfg: cfg, width: defaultConfigWidth}
	m.screen.Path = shortPath(config.WritePath())
	m.refresh()
	return m
}

func (m configModel) Init() tea.Cmd { return nil }

func (m configModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.screen.MaxLines = msg.Width, msg.Height
		return m, nil
	case tea.KeyPressMsg:
		m.screen.Notice = ""
		done, result := m.screen.Update(msg)
		if change, ok := result.(components.ConfigChange); ok {
			m.apply(change)
		}
		if !done {
			return m, nil
		}
		if r, ok := result.(components.ConfigResult); ok && r.Write {
			if err := config.Save(m.cfg); err != nil {
				m.err = err
			} else {
				m.saved = true
			}
		}
		return m, tea.Quit
	}
	return m, nil
}

// View is the frame: the config screen, on the alt screen it takes over. In
// v2 that state is a field on the view rather than an option the host passes
// to NewProgram.
func (m configModel) View() tea.View {
	v := tea.NewView(m.screen.View(m.width))
	v.AltScreen = true
	return v
}

// apply stages one edit and rebuilds the rows, so the screen redraws from the
// config rather than from what it thinks it changed.
func (m *configModel) apply(change components.ConfigChange) {
	value := change.Value
	if change.Reset {
		value = ""
	}
	if err := config.Set(&m.cfg, change.Key, value); err != nil {
		m.screen.Notice = err.Error()
		return
	}
	if change.Reset {
		m.screen.Notice = change.Key + " is back to its default"
	}
	m.refresh()
}

// refresh rebuilds every row from the staged config and recounts what is
// standing against the file.
func (m *configModel) refresh() {
	m.screen.Rows = configRows(m.cfg, m.base)
	changed := 0
	for _, s := range configSettings() {
		if s.read(m.cfg) != s.read(m.base) {
			changed++
		}
	}
	m.screen.Changed = changed
}

// configSetting is one row's worth of knowledge: where it sits, what it is
// called, how to read it out of a Config, and what — if anything — [enter]
// offers instead of a field to type into.
type configSetting struct {
	group string
	key   string
	label string
	// read is the raw stored value, which is what "is this overridden"
	// compares and what a picker's options are matched against.
	read func(config.Config) string
	// show renders the row: the value as it reads, how it should be toned,
	// and the dim note that qualifies it. An empty value takes the default
	// treatment instead.
	show func(string) (value string, tone components.FieldTone, detail string)
	// options are the answers, or nil for a field.
	options func(config.Config) []components.SelectOption
	// fallback is what the row shows when nothing is set.
	fallback string
	secret   bool
}

// configRows renders every setting against the staged config, sourcing each
// one from the loaded file: every row states where its value came from,
// because "why is this on" is the only question a config screen is ever
// asked.
func configRows(cfg, base config.Config) []components.ConfigRow {
	settings := configSettings()
	rows := make([]components.ConfigRow, 0, len(settings))
	for _, s := range settings {
		raw := s.read(cfg)
		row := components.ConfigRow{
			Group: s.group, Key: s.key, Label: s.label,
			Secret: s.secret, Options: s.options(cfg),
		}
		switch {
		case s.secret && raw != "":
			row.Value = components.MaskSecret(raw)
		case raw == "":
			row.Value, row.ValueTone = s.fallback, components.ToneNeutral
		case s.show != nil:
			row.Value, row.ValueTone, row.Detail = s.show(raw)
		default:
			row.Value = raw
		}
		row.Source, row.SourceTone = configSource(raw, s.read(base))
		if s.key == "provider.model" {
			if n := len(row.Options); n > 0 {
				row.Source += fmt.Sprintf(" · %d available", n)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// configSource is the right-hand field: where this answer came from. There
// are three readings and no more, because there are only ever three: nothing
// is set and the built-in default stands, the loaded file set it, or this
// session has staged something the file does not have yet.
func configSource(staged, loaded string) (string, components.FieldTone) {
	switch {
	case staged != loaded:
		return "unwritten", components.ToneOpen
	case staged == "":
		return "default", components.ToneNeutral
	}
	return "user", components.ToneNeutral
}

// boolOptions is the answer pair a flag offers. They are words rather than
// true/false on the row because a row that reads `on` is read faster than one
// that reads `true`, and the value written is still the value the file wants.
func boolOptions(on, off string) []components.SelectOption {
	return []components.SelectOption{
		{Label: "true", Desc: on},
		{Label: "false", Desc: off},
	}
}

func noOptions(config.Config) []components.SelectOption { return nil }

// modeShow renders a permission mode the way the cockpit's own mode segment
// does: `⏵⏵` and add for the two that let work through, `⏸` and accent
// for the two that gate it. The glyph carries the distinction, so the colour
// is never carrying it alone.
func modeShow(raw string) (string, components.FieldTone, string) {
	mode, err := agent.ParseMode(raw)
	if err != nil {
		return raw, components.ToneRisk, "not a mode — manual stands until this is fixed"
	}
	name := strings.ReplaceAll(mode.String(), "-", " ")
	if mode == agent.ModeAuto || mode == agent.ModeAcceptEdits {
		return "⏵⏵ " + name, components.ToneSafe, mode.Describe()
	}
	return "⏸ " + name, components.ToneOpen, mode.Describe()
}

// configSettings is the table the screen is drawn from, in the order and
// the three rails the artboard gives it.
func configSettings() []configSetting {
	str := func(f func(config.Config) string) func(config.Config) string { return f }
	num := func(f func(config.Config) int) func(config.Config) string {
		return func(c config.Config) string {
			if n := f(c); n > 0 {
				return strconv.Itoa(n)
			}
			return ""
		}
	}
	flag := func(f func(config.Config) bool) func(config.Config) string {
		return func(c config.Config) string {
			if f(c) {
				return "true"
			}
			return ""
		}
	}

	return []configSetting{{
		group: "SESSION", key: "behavior.default_mode", label: "permission mode",
		read:     str(func(c config.Config) string { return c.Behavior.DefaultMode }),
		show:     modeShow,
		fallback: "⏸ manual",
		options: func(config.Config) []components.SelectOption {
			opts := make([]components.SelectOption, 0, 4)
			for _, mode := range agent.DefaultCycle() {
				opts = append(opts, components.SelectOption{Label: mode.String(), Desc: mode.Describe()})
			}
			return opts
		},
	}, {
		group: "SESSION", key: "behavior.max_tool_rounds", label: "round limit",
		// Not num(): a negative here is the one number that is not a
		// ceiling. It is how a config file says what `--max-rounds 0` says
		// on the command line, and the screen has to read it back as the
		// absence of a limit rather than as "-1".
		read: func(c config.Config) string {
			switch n := c.Behavior.MaxToolRounds; {
			case n < 0:
				return "no bound — turns run until they are done"
			case n > 0:
				return strconv.Itoa(n)
			}
			return ""
		},
		fallback: strconv.Itoa(agent.DefaultMaxToolRounds),
		options:  noOptions,
	}, {
		group: "SESSION", key: "behavior.context_max_tokens", label: "context budget",
		read:     num(func(c config.Config) int { return c.Behavior.ContextMaxTokens }),
		fallback: strconv.Itoa(config.DefaultContextMaxTokens) + " tokens",
		options:  noOptions,
	}, {
		group: "SESSION", key: "behavior.safety_warnings", label: "safety warnings",
		read: func(c config.Config) string {
			if c.Behavior.SafetyWarnings == nil {
				return ""
			}
			return strconv.FormatBool(*c.Behavior.SafetyWarnings)
		},
		show: func(raw string) (string, components.FieldTone, string) {
			if raw == "false" {
				return "off", components.ToneRisk, "a destructive command is approved like any other"
			}
			return "on", components.ToneSafe, ""
		},
		fallback: "on",
		options: func(config.Config) []components.SelectOption {
			return boolOptions("a destructive command says so before it is approved",
				"a destructive command is approved like any other")
		},
	}, {
		group: "SESSION", key: "behavior.silent_mode", label: "silent mode",
		read: flag(func(c config.Config) bool { return c.Behavior.SilentMode }),
		show: func(string) (string, components.FieldTone, string) {
			return "on", components.ToneNeutral, "generated commands print and nothing else"
		},
		fallback: "off",
		options: func(config.Config) []components.SelectOption {
			return boolOptions("generated commands print and nothing else", "the full UI")
		},
	}, {
		group: "MODEL", key: "provider.default", label: "provider",
		read:     str(func(c config.Config) string { return c.Provider.Default }),
		fallback: "(not set)",
		options: func(config.Config) []components.SelectOption {
			names := provider.Available()
			sort.Strings(names)
			opts := make([]components.SelectOption, 0, len(names))
			for _, name := range names {
				opt := components.SelectOption{Label: name}
				if d := provider.Defaults(name); d.Model != "" {
					opt.Desc = "default model " + d.Model
				}
				opts = append(opts, opt)
			}
			return opts
		},
	}, {
		group: "MODEL", key: "provider.model", label: "model",
		read: str(func(c config.Config) string { return c.Provider.Model }),
		options: func(c config.Config) []components.SelectOption {
			models := provider.KnownModels(c.Provider.Default)
			opts := make([]components.SelectOption, 0, len(models))
			for _, name := range models {
				opts = append(opts, components.SelectOption{Label: name})
			}
			return opts
		},
		fallback: "(the provider's own default)",
	}, {
		group: "MODEL", key: "provider.reasoning", label: "reasoning",
		read:     str(func(c config.Config) string { return c.Provider.Reasoning }),
		fallback: provider.DefaultEffort.String(),
		options: func(config.Config) []components.SelectOption {
			opts := make([]components.SelectOption, 0, len(provider.EffortCycle()))
			for _, e := range provider.EffortCycle() {
				opts = append(opts, components.SelectOption{Label: e.String(), Desc: e.Describe()})
			}
			return opts
		},
	}, {
		group: "MODEL", key: "provider.api_key", label: "api key",
		read:     str(func(c config.Config) string { return c.Provider.APIKey }),
		fallback: "(from the environment)",
		options:  noOptions,
		secret:   true,
	}, {
		group: "MODEL", key: "provider.base_url", label: "base url",
		read:     str(func(c config.Config) string { return c.Provider.BaseURL }),
		fallback: "(the provider's own)",
		options:  noOptions,
	}, {
		group: "MODEL", key: "provider.name", label: "display name",
		read:     str(func(c config.Config) string { return c.Provider.Name }),
		fallback: "(the provider's own)",
		options:  noOptions,
	}, {
		group: "MODEL", key: "agents.model", label: "sub-agent model",
		read:     str(func(c config.Config) string { return c.Agents.Model }),
		fallback: config.InheritModel,
		options: func(c config.Config) []components.SelectOption {
			opts := []components.SelectOption{
				{Label: config.InheritModel, Desc: "children run the session's own model"},
			}
			for _, name := range provider.KnownModels(c.Provider.Default) {
				opts = append(opts, components.SelectOption{Label: name})
			}
			return opts
		},
	}, {
		// The session summary's model. It sits under MODEL rather
		// than SESSION because what it is is a second model the session runs,
		// and the fallback says the thing worth knowing: unset means it runs
		// on the expensive one.
		group: "MODEL", key: "summary.model", label: "summary model",
		read:     str(func(c config.Config) string { return c.Summary.Model }),
		fallback: "(the session's own — a faster one costs less)",
		options: func(c config.Config) []components.SelectOption {
			opts := make([]components.SelectOption, 0, 8)
			for _, name := range provider.KnownModels(c.Provider.Default) {
				opts = append(opts, components.SelectOption{Label: name})
			}
			return opts
		},
	}, {
		group: "SESSION", key: "summary.disabled", label: "session summary",
		read: flag(func(c config.Config) bool { return c.Summary.Disabled }),
		show: func(string) (string, components.FieldTone, string) {
			return "off", components.ToneNeutral, "no reading is taken and the rail draws no SUMMARY block"
		},
		fallback: "on",
		options: func(config.Config) []components.SelectOption {
			return boolOptions("no reading is taken and no requests are made",
				"a reading every few tool rounds, drawn in the inspector rail")
		},
	}, {
		group: "WORKSPACE", key: "sandbox.profile", label: "sandbox",
		read: str(func(c config.Config) string { return c.Sandbox.Profile }),
		show: func(raw string) (string, components.FieldTone, string) {
			if raw == "workspace-netless" {
				return "⛨ " + raw, components.ToneSafe, "no network inside containment"
			}
			return "⛨ " + raw, components.ToneNeutral, ""
		},
		fallback: "⛨ workspace",
		options: func(config.Config) []components.SelectOption {
			return []components.SelectOption{
				{Label: "workspace", Desc: "the workspace is writable, the network is not touched"},
				{Label: "workspace-netless", Desc: "the same, with the network closed"},
			}
		},
	}, {
		group: "WORKSPACE", key: "web.allow_private", label: "network",
		read: flag(func(c config.Config) bool { return c.Web.AllowPrivate }),
		show: func(string) (string, components.FieldTone, string) {
			return "private hosts reachable", components.ToneOpen, "intranet and localhost fetches are allowed"
		},
		fallback: "public hosts only",
		options: func(config.Config) []components.SelectOption {
			return boolOptions("intranet and localhost fetches are allowed",
				"private, loopback and metadata addresses are refused")
		},
	}, {
		group: "WORKSPACE", key: "behavior.memory_disabled", label: "memory",
		read: flag(func(c config.Config) bool { return c.Behavior.MemoryDisabled }),
		show: func(string) (string, components.FieldTone, string) {
			return "off", components.ToneNeutral, "nothing is remembered between sessions"
		},
		fallback: "on",
		options: func(config.Config) []components.SelectOption {
			return boolOptions("nothing is remembered between sessions",
				"memories are injected into the system prompt")
		},
	}, {
		group: "WORKSPACE", key: "behavior.shell", label: "shell",
		read:     str(func(c config.Config) string { return c.Behavior.Shell }),
		fallback: "(your login shell)",
		options:  noOptions,
	}, {
		group: "WORKSPACE", key: "history.retention_days", label: "history retention",
		read:     num(func(c config.Config) int { return c.History.RetentionDays }),
		fallback: strconv.Itoa(config.DefaultRetentionDays) + " days",
		options:  noOptions,
	}, {
		group: "WORKSPACE", key: "appearance.accent_color", label: "accent colour",
		read:     str(func(c config.Config) string { return c.Appearance.AccentColor }),
		fallback: "(the palette's own)",
		options:  noOptions,
	}, {
		group: "WORKSPACE", key: "appearance.mouse", label: "mouse reporting",
		read:     flag(func(c config.Config) bool { return c.Appearance.Mouse }),
		fallback: "off — the terminal keeps click-drag selection; on, shhh selects the transcript itself",
		options:  noOptions,
	}, {
		group: "WORKSPACE", key: "appearance.paste_lines", label: "paste staged taller than",
		// Not num(): a negative is the one number that is not a threshold. It
		// is how the file says "never on this count", and the screen has to
		// read it back as that rather than as "-1" — the same distinction
		// behavior.max_tool_rounds makes.
		read: func(c config.Config) string {
			switch n := c.Appearance.PasteLines; {
			case n < 0:
				return "never on line count — a paste of any height types"
			case n > 0:
				return strconv.Itoa(n) + " lines"
			}
			return ""
		},
		fallback: strconv.Itoa(attachment.DefaultPasteLines) + " lines",
		options:  noOptions,
	}, {
		group: "WORKSPACE", key: "appearance.paste_columns", label: "paste staged wider than",
		read: func(c config.Config) string {
			switch n := c.Appearance.PasteColumns; {
			case n < 0:
				return "never on width — a line of any length types"
			case n > 0:
				return strconv.Itoa(n) + " columns"
			}
			return ""
		},
		fallback: strconv.Itoa(attachment.DefaultPasteColumns) + " columns",
		options:  noOptions,
	}, {
		group: "WORKSPACE", key: "appearance.notify", label: "desktop notifications",
		// Not flag(): the default is on, so an unset file and a file that
		// says true are different facts the screen has to keep apart — the
		// same reason behavior.safety_warnings reads its pointer.
		read: func(c config.Config) string {
			if c.Appearance.Notify == nil {
				return ""
			}
			return strconv.FormatBool(*c.Appearance.Notify)
		},
		show: func(raw string) (string, components.FieldTone, string) {
			if raw == "false" {
				return "off", components.ToneNeutral, "a turn that stops while you are elsewhere waits silently"
			}
			return "on", components.ToneSafe, ""
		},
		fallback: "on",
		options: func(config.Config) []components.SelectOption {
			return boolOptions("a turn that stops while the window is not in front raises one notification",
				"a turn that stops while you are elsewhere waits silently")
		},
	}}
}

// shortPath writes a path under the home directory as ~/… , which is how
// every other surface in the product states one.
func shortPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(path, home) {
		return path
	}
	return "~" + path[len(home):]
}

// configWriter persists one config key from an interactive session (/model
// default, /model agents). It re-reads the file first so a concurrent edit
// elsewhere is not clobbered by this session's stale copy.
func configWriter() func(key, value string) error {
	return func(key, value string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if err := config.Set(&cfg, key, value); err != nil {
			return err
		}
		return config.Save(cfg)
	}
}
