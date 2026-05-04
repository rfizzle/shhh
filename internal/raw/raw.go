package raw

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/ui"
)

type Opts struct {
	Provider provider.Provider
	Model    string
	Prompt   string
	Stdout   io.Writer
	Stderr   io.Writer
}

func Run(ctx context.Context, opts Opts) error {
	info := shell.Detect()
	sysPrompt := prompt.Build(info)

	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: sysPrompt},
		{Role: provider.RoleUser, Content: opts.Prompt},
	}

	compOpts := provider.CompletionOpts{Model: opts.Model}

	events, err := opts.Provider.StreamCompletion(ctx, messages, compOpts)
	if err != nil {
		return err
	}

	var output strings.Builder
	for ev := range events {
		if ev.Err != nil {
			return ev.Err
		}
		if ev.Done {
			break
		}
		output.WriteString(ev.Token)
	}

	result := strings.TrimSpace(ui.StripFences(output.String()))
	fmt.Fprintln(opts.Stdout, result)
	return nil
}
