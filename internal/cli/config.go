package cli

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/provider"
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
			model := newConfigModel(cfg)
			p := tea.NewProgram(model)
			finalModel, err := p.Run()
			if err != nil {
				return err
			}
			result := finalModel.(configModel)
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

type configPhase int

const (
	configPhaseMenu configPhase = iota
	configPhaseEdit
	configPhaseSelect
)

type menuItem struct {
	key   string
	label string
}

var menuItems = []menuItem{
	{"provider.default", "Default provider"},
	{"provider.model", "Default model"},
	{"provider.api_key", "API key"},
	{"provider.base_url", "Base URL"},
	{"provider.name", "Display name"},
	{"behavior.silent_mode", "Silent mode"},
	{"behavior.shell", "Shell override"},
	{"behavior.safety_warnings", "Safety warnings"},
	{"behavior.system_prompt_extra", "Extra system prompt"},
	{"appearance.accent_color", "Accent color"},
}

type configModel struct {
	cfg     config.Config
	cursor  int
	phase   configPhase
	input   string
	saved   bool
	options []string
	optIdx  int
}

func newConfigModel(cfg config.Config) configModel {
	return configModel{cfg: cfg}
}

func (m configModel) Init() tea.Cmd { return nil }

func (m configModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.phase {
	case configPhaseMenu:
		return m.updateMenu(msg)
	case configPhaseEdit:
		return m.updateEdit(msg)
	case configPhaseSelect:
		return m.updateSelect(msg)
	}
	return m, nil
}

func (m configModel) optionsForKey(key string) []string {
	switch key {
	case "provider.default":
		opts := provider.Available()
		sort.Strings(opts)
		return opts
	case "behavior.silent_mode", "behavior.safety_warnings":
		return []string{"true", "false"}
	}
	return nil
}

func (m configModel) updateMenu(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(menuItems)-1 {
				m.cursor++
			}
		case "enter":
			key := menuItems[m.cursor].key
			if opts := m.optionsForKey(key); opts != nil {
				m.phase = configPhaseSelect
				m.options = opts
				m.optIdx = 0
				current := m.currentValue()
				for i, o := range opts {
					if o == current {
						m.optIdx = i
						break
					}
				}
			} else {
				m.phase = configPhaseEdit
				m.input = m.currentValue()
			}
		case "q", "esc":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m configModel) updateEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			_ = config.Set(&m.cfg, menuItems[m.cursor].key, m.input)
			if err := config.Save(m.cfg); err == nil {
				m.saved = true
			}
			m.phase = configPhaseMenu
			return m, nil
		case tea.KeyEscape:
			m.phase = configPhaseMenu
			return m, nil
		case tea.KeyBackspace:
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.input += string(msg.Runes)
			} else if msg.Type == tea.KeySpace {
				m.input += " "
			}
		}
	}
	return m, nil
}

func (m configModel) updateSelect(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.optIdx > 0 {
				m.optIdx--
			}
		case "down", "j":
			if m.optIdx < len(m.options)-1 {
				m.optIdx++
			}
		case "enter":
			_ = config.Set(&m.cfg, menuItems[m.cursor].key, m.options[m.optIdx])
			if err := config.Save(m.cfg); err == nil {
				m.saved = true
			}
			m.phase = configPhaseMenu
			return m, nil
		case "esc":
			m.phase = configPhaseMenu
			return m, nil
		}
	}
	return m, nil
}

func (m configModel) currentValue() string {
	return m.currentValueForKey(menuItems[m.cursor].key)
}

func maskKey(s string) string {
	if len(s) <= 6 {
		return strings.Repeat("*", len(s))
	}
	return s[:3] + "..." + strings.Repeat("*", 3)
}

func (m configModel) defaultForKey(key string) string {
	d := provider.Defaults(m.cfg.Provider.Default)
	switch key {
	case "provider.model":
		return d.Model
	case "provider.base_url":
		return d.BaseURL
	}
	return ""
}

func (m configModel) displayValue(idx int) string {
	item := menuItems[idx]
	val := m.currentValueForKey(item.key)
	if val == "" {
		if def := m.defaultForKey(item.key); def != "" {
			return fmt.Sprintf("(default: %s)", def)
		}
		return "(not set)"
	}
	if strings.HasSuffix(item.key, "api_key") {
		return maskKey(val)
	}
	return val
}

func (m configModel) currentValueForKey(key string) string {
	switch key {
	case "provider.default":
		return m.cfg.Provider.Default
	case "provider.model":
		return m.cfg.Provider.Model
	case "provider.api_key":
		return m.cfg.Provider.APIKey
	case "provider.base_url":
		return m.cfg.Provider.BaseURL
	case "provider.name":
		return m.cfg.Provider.Name
	case "behavior.silent_mode":
		if m.cfg.Behavior.SilentMode {
			return "true"
		}
		return "false"
	case "behavior.shell":
		return m.cfg.Behavior.Shell
	case "behavior.safety_warnings":
		if m.cfg.Behavior.SafetyWarnings == nil {
			return "true"
		}
		if *m.cfg.Behavior.SafetyWarnings {
			return "true"
		}
		return "false"
	case "behavior.system_prompt_extra":
		return m.cfg.Behavior.SystemPromptExtra
	case "appearance.accent_color":
		return m.cfg.Appearance.AccentColor
	}
	return ""
}

func (m configModel) View() string {
	var b strings.Builder

	switch m.phase {
	case configPhaseMenu:
		b.WriteString("shhh configuration\n\n")
		for i, item := range menuItems {
			cursor := "  "
			if i == m.cursor {
				cursor = "> "
			}
			b.WriteString(fmt.Sprintf("%s%-30s %s\n", cursor, item.label, m.displayValue(i)))
		}
		b.WriteString("\n↑/↓ navigate · enter edit · q quit")
	case configPhaseEdit:
		item := menuItems[m.cursor]
		b.WriteString(fmt.Sprintf("Editing: %s\n\n", item.label))
		display := m.input
		if strings.HasSuffix(item.key, "api_key") && len(m.input) > 0 {
			display = strings.Repeat("*", len(m.input)-1) + string(m.input[len(m.input)-1])
		}
		b.WriteString(fmt.Sprintf("> %s█\n", display))
		if def := m.defaultForKey(item.key); def != "" && m.input == "" {
			b.WriteString(fmt.Sprintf("  default: %s\n", def))
		}
		b.WriteString("\nenter save · esc cancel")
	case configPhaseSelect:
		item := menuItems[m.cursor]
		b.WriteString(fmt.Sprintf("Select: %s\n\n", item.label))
		for i, opt := range m.options {
			cursor := "  "
			if i == m.optIdx {
				cursor = "> "
			}
			b.WriteString(fmt.Sprintf("%s%s\n", cursor, opt))
		}
		b.WriteString("\n↑/↓ navigate · enter select · esc cancel")
	}

	return b.String()
}
