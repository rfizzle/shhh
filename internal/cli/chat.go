package cli

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/update"
	"github.com/spf13/cobra"
)

func newChatCmd() *cobra.Command {
	var flags resolve.Opts

	cmd := &cobra.Command{
		Use:   "chat [prompt]",
		Short: "Start an interactive chat session",
		Long:  "Open a multi-turn conversation with the LLM to explore complex tasks iteratively.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFrom(cmd.Context())

			flags.ConfigProvider = cfg.Provider.Default
			flags.ConfigModel = cfg.Provider.Model

			resolved := resolve.Resolve(flags)

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
			sysPrompt := prompt.BuildChat(info, promptExtra)

			messages := []provider.Message{
				{Role: provider.RoleSystem, Content: sysPrompt},
			}

			compOpts := provider.CompletionOpts{
				Model:      resolved.Model,
				Tools:      tools.DefinitionsWithExec(),
				ToolChoice: "auto",
			}

			stream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
				ctx, cancel := context.WithCancel(cmd.Context())
				ev, sErr := p.StreamCompletion(ctx, msgs, compOpts)
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
				WithToolExecutor(tools.Execute).
				WithDB(db).
				WithPricing(prices, resolved.Model).
				WithRunner(runner.RunCapture)
			if r := update.CheckCached(version); r != nil {
				model = model.WithUpdateNotice("update: " + r.Latest)
			}
			if len(args) > 0 {
				model = model.WithInitialPrompt(args[0])
			}
			program := tea.NewProgram(model, tea.WithAltScreen())
			if _, err := program.Run(); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}

			fmt.Fprintln(os.Stderr, "Chat session ended.")
			return nil
		},
	}

	cmd.Flags().StringVar(&flags.FlagProvider, "provider", "", "LLM provider")
	cmd.Flags().StringVar(&flags.FlagModel, "model", "", "model name to use")
	cmd.Flags().StringVar(&flags.FlagAPIKey, "api-key", "", "API key (overrides env var)")

	return cmd
}
