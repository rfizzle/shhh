package cli

import (
	"context"
	"fmt"
	"os"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/stdin"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/browse"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/update"
	"github.com/spf13/cobra"
)

// chatSession parameterizes the shared chat TUI entry point: `shhh chat` and
// `shhh code` run the same Bubble Tea model and differ only in system prompt,
// registered toolset, and title.
type chatSession struct {
	title        string
	buildPrompt  func(shell.Info, ...string) string
	toolDefs     []provider.Tool
	flags        *resolve.Opts
	continueLast bool
	resumePick   bool
}

func newChatCmd() *cobra.Command {
	var flags resolve.Opts
	var continueLast bool
	var resumePick bool

	cmd := &cobra.Command{
		Use:   "chat [prompt]",
		Short: "Start an interactive chat session",
		Long:  "Open a multi-turn conversation with the LLM to explore complex tasks iteratively.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChatSession(cmd, args, chatSession{
				title:        "shhh chat",
				buildPrompt:  prompt.BuildChat,
				toolDefs:     tools.DefinitionsWithExec(),
				flags:        &flags,
				continueLast: continueLast,
				resumePick:   resumePick,
			})
		},
	}

	cmd.Flags().BoolVarP(&continueLast, "continue", "c", false, "resume the most recent chat session")
	cmd.Flags().BoolVarP(&resumePick, "resume", "r", false, "pick a saved chat to resume")
	cmd.Flags().StringVar(&flags.FlagProvider, "provider", "", "LLM provider")
	cmd.Flags().StringVar(&flags.FlagModel, "model", "", "model name to use")
	cmd.Flags().StringVar(&flags.FlagAPIKey, "api-key", "", "API key (overrides env var)")

	return cmd
}

func runChatSession(cmd *cobra.Command, args []string, session chatSession) error {
	cfg := ConfigFrom(cmd.Context())

	flags := session.flags
	flags.ConfigProvider = cfg.Provider.Default
	flags.ConfigModel = cfg.Provider.Model

	resolved := resolve.Resolve(*flags)

	p, err := provider.Resolve(resolved.Provider, provider.ResolveOpts{
		APIKey:        flags.FlagAPIKey,
		Model:         resolved.Model,
		ConfigAPIKey:  cfg.ProviderAPIKey(),
		ConfigBaseURL: cfg.ProviderBaseURL(),
		ConfigName:    cfg.ProviderDisplayName(),
	})
	if err != nil {
		return err
	}

	info := shell.Detect()
	promptExtra := prompt.CombineExtra(cfg.Behavior.SystemPromptExtra, project.FindContext())
	sysPrompt := session.buildPrompt(info, promptExtra)

	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: sysPrompt},
	}

	compOpts := provider.CompletionOpts{
		Model:      resolved.Model,
		Tools:      session.toolDefs,
		ToolChoice: "auto",
	}

	// /model switches this mid-session; the stream closure runs in a
	// background goroutine, so guard the read.
	var modelMu sync.Mutex
	currentModel := resolved.Model

	stream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		ctx, cancel := context.WithCancel(cmd.Context())
		opts := compOpts
		modelMu.Lock()
		opts.Model = currentModel
		modelMu.Unlock()
		ev, sErr := p.StreamCompletion(ctx, msgs, opts)
		if sErr != nil {
			cancel()
			return nil, nil, sErr
		}
		return ev, cancel, nil
	}

	db, err := storage.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: chat persistence unavailable: %v\n", err)
	}
	if db != nil {
		defer db.Close()
	}

	prices, _ := pricing.Load()

	model := chat.New(messages, stream).
		WithTitle(session.title).
		WithToolExecutor(tools.Execute).
		WithDB(db).
		WithPricing(prices, resolved.Model).
		WithRunner(runner.RunCapture).
		WithMaxToolRounds(cfg.Behavior.MaxToolRounds).
		WithCommandAllowlist(cfg.Behavior.CommandAllowlist).
		WithModelSwitcher(func(name string) {
			modelMu.Lock()
			currentModel = name
			modelMu.Unlock()
		})

	if session.continueLast || session.resumePick {
		if db == nil {
			return fmt.Errorf("chat persistence is unavailable, cannot resume")
		}
		name := chat.AutosaveName
		if session.resumePick {
			picked, err := pickSavedChat(db)
			if err != nil {
				return err
			}
			if picked == "" {
				return nil
			}
			name = picked
		}
		resumed, err := db.LoadChat(name)
		if err != nil {
			if session.continueLast {
				fmt.Fprintln(os.Stderr, "No previous session found, starting fresh.")
			} else {
				return err
			}
		} else {
			// Refresh the system prompt so shell/cwd context is current.
			if len(resumed) > 0 && resumed[0].Role == provider.RoleSystem {
				resumed[0].Content = sysPrompt
			}
			model = model.WithResumedMessages(resumed)
		}
	}
	if r := update.CheckCached(version); r != nil {
		model = model.WithUpdateNotice("update: " + r.Latest)
	}

	initialPrompt := ""
	if len(args) > 0 {
		initialPrompt = args[0]
	}

	// Piped stdin becomes context for the first message; the TUI then
	// reads keys from the terminal directly.
	programOpts := []tea.ProgramOption{tea.WithAltScreen()}
	stdinIsTTY := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	if !stdinIsTTY {
		maxChars := cfg.EffectiveContextMaxTokens() * 4
		content, err := stdin.Read(os.Stdin, maxChars)
		if err != nil {
			return err
		}
		if content != "" {
			if initialPrompt == "" {
				initialPrompt = "Take a look at this."
			}
			initialPrompt = stdin.FormatPromptWithContext(initialPrompt, content)
		}
		tty, err := os.Open("/dev/tty")
		if err != nil {
			return fmt.Errorf("chat needs a terminal for input: %w", err)
		}
		defer tty.Close()
		programOpts = append(programOpts, tea.WithInput(tty))
	}
	if initialPrompt != "" {
		model = model.WithInitialPrompt(initialPrompt)
	}

	program := tea.NewProgram(model, programOpts...)
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	fmt.Fprintln(os.Stderr, "Chat session ended.")
	return nil
}

// pickSavedChat shows the saved-chat picker and returns the chosen session
// name, or "" if the user backed out.
func pickSavedChat(db *storage.DB) (string, error) {
	entries, err := db.ListChats()
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "No saved chats yet.")
		return "", nil
	}

	items := make([]browse.Item, len(entries))
	for i, e := range entries {
		items[i] = browse.Item{
			ID:      e.Name,
			Title:   e.Name,
			Preview: fmt.Sprintf("%d turns, %s", e.Turns, e.UpdatedAt.Local().Format("Jan 2 15:04")),
			Detail: fmt.Sprintf("Name:     %s\nTurns:    %d\nUpdated:  %s",
				e.Name, e.Turns, e.UpdatedAt.Local().Format("2006-01-02 15:04:05")),
		}
	}

	model := browse.New(items, []browse.ActionDef{{Label: "Open", Shortcut: "o"}})
	p := tea.NewProgram(model, tea.WithAltScreen())
	result, err := p.Run()
	if err != nil {
		return "", err
	}
	m := result.(browse.Model)
	if m.Result == nil {
		return "", nil
	}
	return m.Result.Item.ID, nil
}
