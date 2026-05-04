package cli

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/chat"
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
			flags.ConfigProviderModel = cfg.ProviderModel(resolved.Provider)
			resolved = resolve.Resolve(flags)
			resolved.APIKey = resolve.First(
				flags.FlagAPIKey,
				cfg.ProviderAPIKey(resolved.Provider),
				"",
			)

			p, err := provider.Resolve(resolved.Provider, provider.ResolveOpts{
				APIKey:  resolved.APIKey,
				Model:   resolved.Model,
				BaseURL: cfg.ProviderBaseURL(resolved.Provider),
				Name:    cfg.ProviderName(resolved.Provider),
			})
			if err != nil {
				return err
			}

			info := shell.Detect()
			sysPrompt := prompt.BuildChat(info)

			messages := []provider.Message{
				{Role: provider.RoleSystem, Content: sysPrompt},
			}

			compOpts := provider.CompletionOpts{
				Model:      resolved.Model,
				Tools:      tools.Definitions(),
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

			model := chat.New(messages, stream).WithToolExecutor(tools.Execute).WithDB(db)
			if len(args) > 0 {
				model = model.WithInitialPrompt(args[0])
			}
			program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
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
