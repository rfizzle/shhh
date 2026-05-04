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
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/spf13/cobra"
)

func newChatCmd() *cobra.Command {
	var flags resolve.Opts

	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Start an interactive chat session",
		Long:  "Open a multi-turn conversation with the LLM to explore complex tasks iteratively.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFrom(cmd.Context())

			resolved := resolve.Resolve(flags)
			resolved.APIKey = resolve.First(
				flags.FlagAPIKey,
				cfg.ProviderAPIKey(resolved.Provider),
				"",
			)

			p, err := provider.Resolve(resolved.Provider, provider.ResolveOpts{
				APIKey:  resolved.APIKey,
				Model:   resolved.Model,
				BaseURL: cfg.ProviderBaseURL(resolved.Provider),
			})
			if err != nil {
				return err
			}

			info := shell.Detect()
			sysPrompt := prompt.BuildChat(info)

			messages := []provider.Message{
				{Role: provider.RoleSystem, Content: sysPrompt},
			}

			compOpts := provider.CompletionOpts{Model: resolved.Model}

			stream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
				ctx, cancel := context.WithCancel(cmd.Context())
				ev, sErr := p.StreamCompletion(ctx, msgs, compOpts)
				if sErr != nil {
					cancel()
					return nil, nil, sErr
				}
				return ev, cancel, nil
			}

			model := chat.New(messages, stream)
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
