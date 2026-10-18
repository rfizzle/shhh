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
	Provider          provider.Provider
	Model             string
	Prompt            string
	SystemPromptExtra string
	// Shell, when set, is the shell the prompt names in place of the one
	// detected. It exists so a caller that has to be the same on every machine
	// (the eval suite) can say which shell it asked for; shhh cmd leaves it
	// empty and gets the user's own.
	Shell  string
	Stdout io.Writer
	Stderr io.Writer
}

// SystemPrompt is the prompt a raw run goes out under. It is exported so a
// caller can fingerprint what it ran under without rebuilding it: two
// constructions of one prompt drift apart, and a drifted fingerprint splits
// a cohort that never changed.
func SystemPrompt(info shell.Info, extra string) string {
	return prompt.Build(info, extra)
}

// Generate puts the prompt to the model and returns the answer as it would be
// printed: fences stripped and trimmed. It is the whole of a raw run but the
// printing, so the shell widget and the eval suite ask through one function
// and cannot drift apart.
func Generate(ctx context.Context, opts Opts) (string, error) {
	info := shell.Detect()
	if opts.Shell != "" {
		info.Shell = opts.Shell
	}
	sysPrompt := SystemPrompt(info, opts.SystemPromptExtra)

	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: sysPrompt},
		{Role: provider.RoleUser, Content: opts.Prompt},
	}

	compOpts := provider.CompletionOpts{Model: opts.Model}

	events, err := opts.Provider.StreamCompletion(ctx, messages, compOpts)
	if err != nil {
		return "", err
	}

	var output strings.Builder
	for ev := range events {
		if ev.Err != nil {
			return "", ev.Err
		}
		if ev.Done {
			break
		}
		output.WriteString(ev.Token)
	}

	return strings.TrimSpace(ui.StripFences(output.String())), nil
}

// Run is Generate with the answer printed on a line of its own.
func Run(ctx context.Context, opts Opts) error {
	result, err := Generate(ctx, opts)
	if err != nil {
		return err
	}
	fmt.Fprintln(opts.Stdout, result)
	return nil
}
