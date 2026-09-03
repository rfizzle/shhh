package cli

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View and edit configuration",
		Long: "Interactive configuration screen. `config list` prints every setting with the value in force " +
			"and where it came from, `config get <key>` prints one, and `config set <key> <value>` changes one " +
			"without opening the screen.",
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
				_ = report.Fprintln(cmd.OutOrStderr(), report.Done("wrote", config.WritePath()))
			}
			return nil
		},
	}

	cmd.AddCommand(newConfigSetCmd(), newConfigListCmd(), newConfigGetCmd())
	return cmd
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a config value",
		Long:  "Set a configuration key. Example: shhh config set provider.default openai",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := writeConfigEdits(config.WritePath(), config.Edit{Key: args[0], Value: args[1]}); err != nil {
				return err
			}
			return report.Fprintln(cmd.OutOrStdout(), report.Done("set", args[0]+" = "+args[1]))
		},
	}
}

// writeConfigEdits is the one door onto the config file from this command
// and every surface it wires: each value is judged before any of it is
// written, so a file is never left holding half an edit that the next key
// turned out to fail on
// (docs/capabilities/configuration.md#a-value-is-refused-before-it-is-written).
func writeConfigEdits(path string, edits ...config.Edit) error {
	for _, e := range edits {
		if err := checkConfigValue(e.Key, e.Value); err != nil {
			return err
		}
	}
	return config.Write(path, edits...)
}

// checkConfigValue refuses a key no setting reads and a value that is not
// one of the words its key takes. The shapes — a number, a boolean, a list —
// are the config package's to parse; a word is checked here because what a
// key may say is a permission mode, a reasoning level, a cache lifetime, a
// rail width or a containment setting, and those vocabularies belong to the
// packages that own them, which config must not import. Asking the same
// parsers the session itself asks is what makes `config set`, the screen and
// the slash commands refuse the same values, rather than three lists drifting
// apart.
//
// Which keys need judging is the table's answer rather than a list kept here:
// a key that names a vocabulary is judged, and a word key whose vocabulary no
// other package owns is judged against the table's own list.
//
// An empty value is a reset, not an answer, and is left to the write to
// interpret as the key going out of the file.
func checkConfigValue(key, value string) error {
	s, ok := config.Lookup(key)
	if !ok {
		return fmt.Errorf("%s", config.UnknownKeyMessage(key))
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	judge, ok := configJudges[key]
	if !ok {
		if s.Kind != config.KindEnum {
			return nil
		}
		judge = wordFromTheTable(s)
	}
	if err := judge(value); err != nil {
		return fmt.Errorf("config key %s: %w", key, err)
	}
	return nil
}

// configJudges are the keys whose vocabulary another package owns. Each asks
// that package's own parser, so a word the session would not accept cannot be
// saved for it to read later — and an alias the parser takes (`med` for
// medium, `accept_edits`) is taken here too, which a membership test against
// the table's list would refuse.
var configJudges = map[string]func(string) error{
	"behavior.default_mode": func(v string) error { _, err := agent.ParseMode(v); return err },
	"behavior.mode_cycle": func(v string) error {
		for name := range strings.SplitSeq(v, ",") {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			if _, err := agent.ParseMode(name); err != nil {
				return err
			}
		}
		return nil
	},
	"provider.reasoning":        func(v string) error { _, err := provider.ParseEffort(v); return err },
	"provider.cache_ttl":        func(v string) error { _, err := provider.ParseCacheTTL(v); return err },
	"appearance.rail_width":     func(v string) error { _, err := components.ParseRailWidth(v); return err },
	"sandbox.profile":           func(v string) error { _, err := sandbox.ParseProfile(v); return err },
	"sandbox.container_engine":  func(v string) error { _, err := sandbox.ParseEngine(v); return err },
	"sandbox.require_isolation": func(v string) error { _, err := sandbox.ParseIsolation(v); return err },
}

// wordFromTheTable judges a word key whose vocabulary no other package owns:
// the table's own list is the whole of it, and a key that grows a word grows
// it in one place.
func wordFromTheTable(s config.Setting) func(string) error {
	return func(value string) error {
		if slices.ContainsFunc(s.Values, func(w string) bool { return strings.EqualFold(w, value) }) {
			return nil
		}
		return fmt.Errorf("unknown value %q (valid: %s)", value, strings.Join(s.Values, ", "))
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
// rule the old wizard broke by saving on every keystroke — and what [w]
// writes is the staged keys alone, each as the value it was staged with, so
// the file keeps every line the screen did not touch.
type configModel struct {
	base  config.Config
	cfg   config.Config
	saved bool
	err   error
	width int
	// staged is the value each edited key was given, as typed or picked,
	// which is what the write needs: a row's read is the screen's rendering
	// of the value and is not what goes in the file.
	staged map[string]string

	screen components.ConfigScreen
}

// defaultConfigWidth is what the screen is drawn at before the terminal has
// said how wide it is — the working width the artboard is drawn at.
const defaultConfigWidth = 110

func newConfigModel(cfg config.Config) configModel {
	m := configModel{base: cfg, cfg: cfg, width: defaultConfigWidth, staged: map[string]string{}}
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
			if err := writeConfigEdits(config.WritePath(), m.edits()...); err != nil {
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
	if err := checkConfigValue(change.Key, value); err != nil {
		m.screen.Notice = err.Error()
		return
	}
	if err := config.Set(&m.cfg, change.Key, value); err != nil {
		m.screen.Notice = err.Error()
		return
	}
	m.staged[change.Key] = value
	if change.Reset {
		m.screen.Notice = change.Key + " is back to its default"
	}
	m.refresh()
}

// edits is what [w] writes: every key whose staged value differs from the
// loaded one, in the screen's order. A key edited and then put back is not
// among them, so its line in the file is not rewritten either.
func (m configModel) edits() []config.Edit {
	var edits []config.Edit
	for _, s := range configSettings(m.cfg, m.base) {
		staged, _ := config.Value(m.cfg, s.Key)
		loaded, _ := config.Value(m.base, s.Key)
		if staged == loaded {
			continue
		}
		value, ok := m.staged[s.Key]
		if !ok {
			continue
		}
		edits = append(edits, config.Edit{Key: s.Key, Value: value})
	}
	return edits
}

// refresh rebuilds every row from the staged config and recounts what is
// standing against the file.
func (m *configModel) refresh() {
	m.screen.Rows = configRows(m.cfg, m.base)
	changed := 0
	for _, s := range configSettings(m.cfg, m.base) {
		staged, _ := config.Value(m.cfg, s.Key)
		if loaded, _ := config.Value(m.base, s.Key); staged != loaded {
			changed++
		}
	}
	m.screen.Changed = changed
}

// configSetting is one row of the settings table with the screen's own
// additions: what to call it, how to read its value back in words, and what —
// if anything — [enter] offers instead of a field to type into. Everything
// else about the key, the value included, comes from the table
// (docs/capabilities/configuration.md#every-setting).
//
// A key that needs none of this gets a row anyway, which is the point: the
// screen used to be a hand-kept list, and the keys nobody added to it were
// reachable only by opening the file — over half of them, at the end.
type configSetting struct {
	config.Setting
	label string
	// show renders the row: the value as it reads, how it should be toned,
	// and the dim note that qualifies it. Nil takes the raw value.
	show func(string) (value string, tone components.FieldTone, detail string)
	// options are the answers, or nil to take whatever the kind offers.
	options func(config.Config) []components.SelectOption
	// fallback overrides what the row shows when nothing is set, for the
	// handful of rows that say the default differently from the reference —
	// a glyph in front of it, or the feature named rather than the key.
	fallback string
}

// configRows renders every setting against the staged config, sourcing each
// one from the loaded file: every row states where its value came from,
// because "why is this on" is the only question a config screen is ever
// asked.
func configRows(cfg, base config.Config) []components.ConfigRow {
	settings := configSettings(cfg, base)
	rows := make([]components.ConfigRow, 0, len(settings))
	for _, s := range settings {
		raw, _ := config.Value(cfg, s.Key)
		row := components.ConfigRow{
			Group: strings.ToUpper(s.Group()), Key: s.Key, Label: s.label,
			Secret: s.Secret, Options: s.answers(cfg),
		}
		switch {
		case s.Secret && raw != "":
			row.Value = components.MaskSecret(raw)
		case raw == "":
			row.Value, row.ValueTone = s.unset(), components.ToneNeutral
		case s.show != nil:
			row.Value, row.ValueTone, row.Detail = s.show(raw)
		default:
			row.Value = raw
		}
		loaded, _ := config.Value(base, s.Key)
		row.Source, row.SourceTone = configSource(raw, loaded)
		if s.Key == "provider.model" {
			if n := len(row.Options); n > 0 {
				row.Source += fmt.Sprintf(" · %d available", n)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// unset is what the row shows when nothing has set the key.
func (s configSetting) unset() string {
	if s.fallback != "" {
		return s.fallback
	}
	return s.Default
}

// answers is what [enter] offers: whatever the row was given, and otherwise
// whatever its kind implies — the words a word key takes, the pair a flag
// takes, and a field to type into for everything else. Wiring the picker to
// the kind is what lets a key gain a row without gaining a case.
func (s configSetting) answers(cfg config.Config) []components.SelectOption {
	if s.options != nil {
		return s.options(cfg)
	}
	switch s.Kind {
	case config.KindEnum:
		opts := make([]components.SelectOption, 0, len(s.Values))
		for _, v := range s.Values {
			opts = append(opts, components.SelectOption{Label: v})
		}
		return opts
	case config.KindBool:
		return []components.SelectOption{{Label: "true"}, {Label: "false"}}
	}
	return nil
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

// countShow reads a number back in the words the row wants: the unit after
// the figure, and for the keys where a negative is not a bound, the sentence
// it stands for. A row that showed `-1` would be showing the reader the one
// thing they cannot act on.
func countShow(unit, negative string) func(string) (string, components.FieldTone, string) {
	return func(raw string) (string, components.FieldTone, string) {
		if n, err := strconv.Atoi(raw); err == nil && n < 0 {
			return negative, components.ToneNeutral, ""
		}
		return raw + unit, components.ToneNeutral, ""
	}
}

// offShow is the reading of a `*_disabled` key: the row is named for the
// feature, so a key that is set reads as the feature being off.
func offShow(consequence string) func(string) (string, components.FieldTone, string) {
	return func(string) (string, components.FieldTone, string) {
		return "off", components.ToneNeutral, consequence
	}
}

// flagShow is the reading of a tri-state whose default is on: only `false` is
// a fact worth drawing differently, and it says what it costs.
func flagShow(off string, offTone components.FieldTone) func(string) (string, components.FieldTone, string) {
	return func(raw string) (string, components.FieldTone, string) {
		if raw == "false" {
			return "off", offTone, off
		}
		return "on", components.ToneSafe, ""
	}
}

// roleModelOptions are the answers a role's model row offers: inherit, which
// for a role means the sub-agent default rather than the session model
// directly, then whatever models the configured provider is known to have.
func roleModelOptions(c config.Config) []components.SelectOption {
	opts := []components.SelectOption{
		{Label: config.InheritModel, Desc: "this role runs whatever the sub-agent model resolves to"},
	}
	for _, name := range provider.KnownModels(c.Provider.Default) {
		opts = append(opts, components.SelectOption{Label: name})
	}
	return opts
}

// modelOptions is the provider's own catalog, for the rows that name a model.
func modelOptions(c config.Config) []components.SelectOption {
	models := provider.KnownModels(c.Provider.Default)
	opts := make([]components.SelectOption, 0, len(models))
	for _, name := range models {
		opts = append(opts, components.SelectOption{Label: name})
	}
	return opts
}

// builtinRoles are the roles a spawn can ask for without the file naming one.
// They are listed so all three have a row even where the file names none: a
// screen that offers a model for one role and not its siblings reads as the
// others not being settable. A role the file names beside them gets a row
// too, because the key exists the moment somebody writes it.
func builtinRoles() []string {
	return []string{
		string(subagent.RoleResearcher),
		string(subagent.RoleWriter),
		string(subagent.RoleReviewer),
	}
}

// configEntries is the settings table with the per-role keys resolved. Every
// other key is one entry; `agents.profiles.<role>.model` is one per role,
// because the table declares the shape and the roles are the person's.
func configEntries(cfgs ...config.Config) []config.Setting {
	roles := builtinRoles()
	var extra []string
	for _, c := range cfgs {
		for role := range c.Agents.Profiles {
			if !slices.Contains(roles, role) && !slices.Contains(extra, role) {
				extra = append(extra, role)
			}
		}
	}
	sort.Strings(extra)
	roles = append(roles, extra...)

	out := make([]config.Setting, 0, len(config.Settings())+len(roles))
	for _, s := range config.Settings() {
		if !strings.Contains(s.Key, config.RoleWildcard) {
			out = append(out, s)
			continue
		}
		for _, role := range roles {
			entry := s
			entry.Key = strings.Replace(s.Key, config.RoleWildcard, role, 1)
			out = append(out, entry)
		}
	}
	return out
}

// configLabel is what a row is called. The curated names below say what the
// setting is for rather than what the key is spelled; anything else reads its
// own key with the underscores taken out, which is what makes a key that
// lands in the table land on the screen without a second edit.
func configLabel(key string) string {
	if label, ok := configLabels[key]; ok {
		return label
	}
	if role, ok := strings.CutPrefix(key, "agents.profiles."); ok {
		if role, ok := strings.CutSuffix(role, ".model"); ok {
			return role + " model"
		}
	}
	_, tail, _ := strings.Cut(key, ".")
	return strings.ReplaceAll(tail, "_", " ")
}

// configLabels are the rows whose name is not their key. A `*_disabled` key
// is named for the feature it turns off, because a row reading `session
// summary · off` is read faster than one reading `summary disabled · true`.
var configLabels = map[string]string{
	"behavior.default_mode":                "permission mode",
	"behavior.max_tool_rounds":             "round limit",
	"behavior.context_max_tokens":          "context budget",
	"behavior.memory_disabled":             "memory",
	"behavior.provider_retries":            "retries per stall",
	"provider.default":                     "provider",
	"provider.api_key":                     "api key",
	"provider.base_url":                    "base url",
	"provider.name":                        "display name",
	"agents.model":                         "sub-agent model",
	"summary.model":                        "summary model",
	"summary.disabled":                     "session summary",
	"summary.title":                        "session titles",
	"summary.intervene_cooldown_intervals": "steer cooldown",
	"summary.steer_target_chars":           "steer quotes at most",
	"lsp.disabled":                         "language servers",
	"mcp.disabled":                         "mcp servers",
	"web.allow_private":                    "network",
	"sandbox.profile":                      "sandbox",
	"appearance.accent_color":              "accent colour",
	"appearance.mouse":                     "mouse reporting",
	"appearance.notify":                    "desktop notifications",
	"appearance.paste_lines":               "paste staged taller than",
	"appearance.paste_columns":             "paste staged wider than",
	"appearance.rail_width":                "inspector rail width",
	"history.retention_days":               "history retention",
	"reports.retention_days":               "report retention",
	"prompts.steer":                        "steer wording",
	"prompts.check_in":                     "check-in wording",
	"prompts.summary":                      "reading wording",
	"prompts.classifier":                   "classifier wording",
	"todo.commit":                          "backlog run commits",
}

// configShows are the rows whose value is read back in words rather than as
// the file holds it — a mode with its glyph, a negative that is not a bound,
// a `*_disabled` key named for its feature.
var configShows = map[string]func(string) (string, components.FieldTone, string){
	"behavior.default_mode": modeShow,
	// A negative here is the one number that is not a ceiling: it is how a
	// config file says what `--max-rounds 0` says on the command line.
	"behavior.max_tool_rounds": countShow("", "no bound — turns run until they are done"),
	"behavior.check_in_max_doublings": countShow(" doublings",
		"never — one interval from first round to last"),
	"summary.steer_target_chars": countShow(" characters",
		"the whole instruction, however long it was"),
	"appearance.paste_lines": countShow(" lines",
		"never on line count — a paste of any height types"),
	"appearance.paste_columns": countShow(" columns",
		"never on width — a line of any length types"),
	// Unset and zero are different answers on the retry bound, and zero is
	// the one worth reading back in words: a run that ends on the first rate
	// limit rather than waiting one out is a choice somebody made.
	"behavior.provider_retries": func(raw string) (string, components.FieldTone, string) {
		if raw == "0" {
			return "none — a request that failed is not asked again", components.ToneNeutral, ""
		}
		return raw + " attempts", components.ToneNeutral, ""
	},
	"behavior.safety_warnings": flagShow("a destructive command is approved like any other", components.ToneRisk),
	"appearance.mouse":         flagShow("the terminal keeps its native click-drag selection", components.ToneNeutral),
	"appearance.notify":        flagShow("a turn that stops while you are elsewhere waits silently", components.ToneNeutral),
	"appearance.window_title":  flagShow("the tab keeps whatever your terminal puts there", components.ToneNeutral),
	"behavior.silent_mode":     offShow("generated commands print and nothing else"),
	"behavior.memory_disabled": offShow("nothing is remembered between sessions"),
	"summary.disabled":         offShow("no reading is taken and the rail draws no SUMMARY block"),
	"lsp.disabled":             offShow("no servers are started and the navigation tools are not registered"),
	"mcp.disabled":             offShow("no server is started and no MCP tool is registered"),
	"summary.title": func(raw string) (string, components.FieldTone, string) {
		if raw == "false" {
			return "off", components.ToneNeutral, "no session is asked for a title"
		}
		return "on", components.ToneNeutral, "an unnamed session is titled by the summary model after its first turn"
	},
	"web.allow_private": func(string) (string, components.FieldTone, string) {
		return "private hosts reachable", components.ToneOpen, "intranet and localhost fetches are allowed"
	},
	"sandbox.profile": func(raw string) (string, components.FieldTone, string) {
		if raw == "workspace-netless" {
			return "⛨ " + raw, components.ToneSafe, "no network inside containment"
		}
		return "⛨ " + raw, components.ToneNeutral, ""
	},
}

// configFallbacks are the rows that say their default differently from the
// reference: a glyph in front of it, or the feature named rather than the
// key, so `session summary · on` is what an unset `summary.disabled` reads as.
var configFallbacks = map[string]string{
	"behavior.default_mode":    "⏸ manual",
	"behavior.silent_mode":     "off",
	"behavior.memory_disabled": "on",
	"summary.disabled":         "on",
	"lsp.disabled":             "on",
	"mcp.disabled":             "on",
	"sandbox.profile":          "⛨ workspace",
	"web.allow_private":        "public hosts only",
}

// configOptions are the rows whose answers are a catalog or a sentence rather
// than the bare words the kind implies: a model list the provider knows, a
// pair of booleans each saying what it costs.
var configOptions = map[string]func(config.Config) []components.SelectOption{
	"provider.default": func(config.Config) []components.SelectOption {
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
	"provider.model":            modelOptions,
	"summary.model":             modelOptions,
	"behavior.classifier_model": modelOptions,
	"provider.reasoning": func(config.Config) []components.SelectOption {
		opts := make([]components.SelectOption, 0, len(provider.EffortCycle()))
		for _, e := range provider.EffortCycle() {
			opts = append(opts, components.SelectOption{Label: e.String(), Desc: e.Describe()})
		}
		return opts
	},
	"provider.cache_ttl": func(config.Config) []components.SelectOption {
		opts := make([]components.SelectOption, 0, len(provider.CacheTTLCycle()))
		for _, ttl := range provider.CacheTTLCycle() {
			opts = append(opts, components.SelectOption{Label: string(ttl), Desc: ttl.Describe()})
		}
		return opts
	},
	"behavior.default_mode": func(config.Config) []components.SelectOption {
		opts := make([]components.SelectOption, 0, 4)
		for _, mode := range agent.DefaultCycle() {
			opts = append(opts, components.SelectOption{Label: mode.String(), Desc: mode.Describe()})
		}
		return opts
	},
	"agents.model": func(c config.Config) []components.SelectOption {
		opts := []components.SelectOption{
			{Label: config.InheritModel, Desc: "children run the session's own model"},
		}
		return append(opts, modelOptions(c)...)
	},
	"behavior.safety_warnings": func(config.Config) []components.SelectOption {
		return boolOptions("a destructive command says so before it is approved",
			"a destructive command is approved like any other")
	},
	"behavior.silent_mode": func(config.Config) []components.SelectOption {
		return boolOptions("generated commands print and nothing else", "the full UI")
	},
	"summary.disabled": func(config.Config) []components.SelectOption {
		return boolOptions("no reading is taken and no requests are made",
			"a reading every few tool rounds, drawn in the inspector rail")
	},
	"summary.title": func(config.Config) []components.SelectOption {
		return boolOptions("an unnamed session is titled after its first turn (on the summary model, or the session's own)",
			"no session is asked for a title")
	},
	"behavior.tree_check": func(config.Config) []components.SelectOption {
		return boolOptions("a turn is told when the tree moved in a way its own edits do not explain",
			"the tree is surveyed once, at session start, and never again")
	},
	"todo.commit": func(config.Config) []components.SelectOption {
		return boolOptions("a run that verifies and reviews ends in a commit, and a directory with no repository refuses the run",
			"a run ends after the review and leaves the change in the working tree")
	},
	"web.allow_private": func(config.Config) []components.SelectOption {
		return boolOptions("intranet and localhost fetches are allowed",
			"private, loopback and metadata addresses are refused")
	},
	"behavior.memory_disabled": func(config.Config) []components.SelectOption {
		return boolOptions("nothing is remembered between sessions",
			"memories are injected into the system prompt")
	},
	"appearance.mouse": func(config.Config) []components.SelectOption {
		return boolOptions("the wheel scrolls the transcript and shhh selects text itself",
			"the terminal keeps native click-drag selection; navigation is keyboard only")
	},
	"appearance.notify": func(config.Config) []components.SelectOption {
		return boolOptions("a turn that stops while the window is not in front raises one notification",
			"a turn that stops while you are elsewhere waits silently")
	},
	"appearance.window_title": func(config.Config) []components.SelectOption {
		return boolOptions("the terminal's tab says the command, the directory and a waiting decision",
			"the tab keeps whatever your terminal puts there")
	},
	"sandbox.profile": func(config.Config) []components.SelectOption {
		return []components.SelectOption{
			{Label: "workspace", Desc: "the workspace is writable, the network is not touched"},
			{Label: "workspace-netless", Desc: "the same, with the network closed"},
		}
	},
	// The offered numbers are the two ends of the range: a rail is set to fit
	// a pane somebody chose the size of, and the ends are what they are
	// choosing between.
	"appearance.rail_width": func(config.Config) []components.SelectOption {
		return []components.SelectOption{
			{Label: components.RailWidthAuto, Desc: "the rail widens with the terminal"},
			{Label: strconv.Itoa(components.InspectorWidth), Desc: "the narrowest rail — the most transcript"},
			{Label: strconv.Itoa(components.InspectorMaxWidth), Desc: "the widest rail a terminal has room for"},
		}
	},
}

// configSettings is every row the screen draws, in the order and the groups
// the file itself uses. It is the settings table with the screen's own
// wording attached, which is why a key that lands in the table lands here.
func configSettings(cfgs ...config.Config) []configSetting {
	entries := configEntries(cfgs...)
	out := make([]configSetting, 0, len(entries))
	for _, s := range entries {
		row := configSetting{Setting: s, label: configLabel(s.Key), fallback: configFallbacks[s.Key]}
		row.show = configShows[s.Key]
		row.options = configOptions[s.Key]
		if row.options == nil && strings.HasPrefix(s.Key, "agents.profiles.") {
			row.options = roleModelOptions
		}
		out = append(out, row)
	}
	return out
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
// default, /model agents, /reasoning, the /ui toggles). It edits the file's
// text for that key alone, so an edit made elsewhere since the session
// started is not clobbered by this session's stale copy.
func configWriter() func(key, value string) error {
	return func(key, value string) error {
		return writeConfigEdits(config.WritePath(), config.Edit{Key: key, Value: value})
	}
}
