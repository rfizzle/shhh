package cli

// Resolving the session's provider, and what happens when there isn't one
// (docs/interface/surfaces.md#the-recovery-row).
//
// Every entry point used to answer a missing provider the same way: return
// the dialect's own "SHHH_API_KEY or OPENAI_API_KEY is not set" and exit. It
// is the first thing a new user sees and it names two of the four places shhh
// actually looked. resolveProvider replaces it with the card that names all
// four and offers the three things that can be done about it — and, where
// there is nobody at the terminal to press a key, with the same information
// printed plainly.

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-isatty"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/ui"
	"github.com/spf13/cobra"
)

// providerRequest is one attempt to resolve a provider: the names that were
// chosen and the credentials that were found for them.
type providerRequest struct {
	Provider string
	Model    string
	APIKey   string
	BaseURL  string
}

// providerFlagUsage is what --provider says it takes. It is generated rather
// than written down because the two drift: the flag named five providers for
// as long as the registry had six, and a gateway profile — which resolves
// exactly like a built-in — was never mentioned at all. Profiles are named
// generically because they are registered by PersistentPreRunE, after the
// command tree that carries this text was built. The names go on a line of
// their own: a shell completion menu shows the first line of a description
// and nothing else, and a sentence is more use there than half a list.
// See docs/interface/surfaces.md#outside-the-tui.
func providerFlagUsage() string {
	names := provider.Available()
	sort.Strings(names)
	return "send the request to a built-in provider or to a gateway profile from `shhh providers`:\n" +
		strings.Join(names, ", ")
}

// addModelFlags declares the four flags that decide where a request goes and
// how hard the model thinks about it. They belong to the commands that talk
// to a model and to no others, so they are declared per command rather than
// persistently on the root: a flag on a command that cannot use it is a
// promise the command does not keep.
// See docs/interface/surfaces.md#outside-the-tui.
func addModelFlags(cmd *cobra.Command, flags *resolve.Opts) {
	cmd.Flags().StringVar(&flags.FlagProvider, "provider", "", providerFlagUsage())
	cmd.Flags().StringVar(&flags.FlagModel, "model", "", "model name to use")
	cmd.Flags().StringVar(&flags.FlagAPIKey, "api-key", "", "key for the provider, overriding the env var")
	cmd.Flags().StringVar(&flags.FlagReasoning, "reasoning", "",
		"reasoning effort: off, low, medium, high, xhigh, max (default medium; fitted to the model)")
}

// resolveProvider resolves the request, and on failure asks the card. It
// returns the provider, the request that built it (the model may have moved,
// if the card chose a different provider), and an error only when there was
// still no way in.
func resolveProvider(ctx context.Context, cfg config.Config, req providerRequest) (provider.Provider, providerRequest, error) {
	p, err := provider.Resolve(req.Provider, provider.ResolveOpts{
		APIKey:        req.APIKey,
		Model:         req.Model,
		BaseURL:       req.BaseURL,
		ConfigAPIKey:  cfg.ProviderAPIKey(),
		ConfigBaseURL: cfg.ProviderBaseURL(),
		ConfigName:    cfg.ProviderDisplayName(),
		CacheTTL:      cfg.ProviderCacheTTL(),
	})
	if err == nil {
		return p, req, nil
	}

	survey := resolve.SurveyPlaces(ctx, resolve.SurveyOpts{
		Provider:        req.Provider,
		ConfigAPIKey:    cfg.ProviderAPIKey(),
		ConfigAPIKeyEnv: cfg.Provider.APIKeyEnv,
		ConfigPaths:     config.Paths(),
	})
	choice, ok := askProvider(survey)
	if !ok {
		// The card said everything there is to say, including which place is
		// the likely fix. Letting cobra add `Error: SHHH_API_KEY is not set`
		// underneath it would be the old message back, at the bottom of the
		// new one.
		os.Exit(1)
	}

	next := req
	next.Provider = choice.Provider
	next.APIKey = choice.APIKey
	if choice.BaseURL != "" {
		next.BaseURL = choice.BaseURL
	}
	if choice.Model != "" {
		next.Model = choice.Model
	} else if model := provider.Defaults(next.Provider).Model; model != "" && next.Provider != req.Provider {
		next.Model = model
	}

	p, err = provider.Resolve(next.Provider, provider.ResolveOpts{
		APIKey:        next.APIKey,
		Model:         next.Model,
		BaseURL:       next.BaseURL,
		ConfigAPIKey:  cfg.ProviderAPIKey(),
		ConfigBaseURL: cfg.ProviderBaseURL(),
		ConfigName:    cfg.ProviderDisplayName(),
		CacheTTL:      cfg.ProviderCacheTTL(),
	})
	if err != nil {
		return nil, req, err
	}
	if choice.Save {
		saveProviderChoice(ProjectConfigFrom(ctx), next)
	}
	return p, next, nil
}

// askProvider runs the card, or prints its plain form when there is no
// terminal to run it in. It reports false when nothing was chosen, which
// leaves the caller with the failure it already had.
func askProvider(survey resolve.Survey) (ui.ProviderChoice, bool) {
	// The card draws on stderr and is answered from stdin, so those are the
	// two that decide whether there is anybody to answer it. Stdout is not
	// consulted: `shhh "…" | pbcopy` still has a human at the terminal.
	if !isTerminal(os.Stderr) || !isTerminal(os.Stdin) {
		fmt.Fprint(os.Stderr, ui.PlainProviderReport(survey))
		return ui.ProviderChoice{}, false
	}
	names := provider.Available()
	sort.Strings(names)
	final, err := newProgram(ui.NewProviderSetup(survey, names), tea.WithOutput(os.Stderr)).Run()
	if err != nil {
		fmt.Fprint(os.Stderr, ui.PlainProviderReport(survey))
		return ui.ProviderChoice{}, false
	}
	setup, ok := final.(ui.ProviderSetup)
	if !ok {
		return ui.ProviderChoice{}, false
	}
	choice := setup.Choice()
	return choice, choice.Chosen && choice.Provider != ""
}

// saveProviderChoice writes what the card chose to the config file. A write
// that fails is reported and nothing else: the session was already started on
// the choice, and refusing to run because the preference could not be saved
// would be the wrong trade.
func saveProviderChoice(proj config.Project, req providerRequest) {
	edits := []config.Edit{{Key: "provider.default", Value: req.Provider}}
	// The name is written where the key is already exported under one, and
	// the key itself only where it is not: the file that names a variable is
	// safe to commit, and the file that holds a key is what this says out
	// loud rather than leaving to be discovered in a backup.
	// See docs/capabilities/secrets.md#where-a-value-comes-from.
	suggest := ""
	if req.APIKey != "" {
		vars := resolve.KeyVars(req.Provider)
		if name := exportedAs(req.APIKey, vars); name != "" {
			edits = append(edits, config.Edit{Key: "provider.api_key_env", Value: name})
		} else {
			edits = append(edits, config.Edit{Key: "provider.api_key", Value: req.APIKey})
			// The provider's own variable where it has one, and the generic
			// one otherwise, which is the order KeyVars lists them in.
			suggest = vars[len(vars)-1]
		}
	}
	if req.BaseURL != "" {
		edits = append(edits, config.Edit{Key: "provider.base_url", Value: req.BaseURL})
	}
	if req.Model != "" {
		edits = append(edits, config.Edit{Key: "provider.model", Value: req.Model})
	}
	note, err := writeConfigEdits(proj, config.WritePath(), edits...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shhh: could not save the provider to %s: %v\n", config.WritePath(), err)
		return
	}
	fmt.Fprintf(os.Stderr, "shhh: saved the provider to %s\n", config.WritePath())
	if note != "" {
		fmt.Fprintf(os.Stderr, "shhh: %s\n", note)
	}
	if suggest != "" {
		// One line, and it says where the key went before it says what to do
		// instead: the person pasted a key thirty seconds ago, and the fact
		// they need first is that a copy of it is now on disk.
		fmt.Fprintf(os.Stderr, "shhh: %s now holds the key itself — export %s and set provider.api_key_env to %s to keep it out of the file\n",
			config.WritePath(), suggest, suggest)
	}
}

// exportedAs is the variable, of the ones the dialects read, that already
// holds this key. It is what lets the card write a name rather than a key on
// the common path: somebody who exported the variable and then pasted the
// same key into the card meant one credential, not two.
//
// The comparison is on the value because that is the only question worth
// asking — a variable holding a different key is a different credential, and
// naming it would save the wrong one.
func exportedAs(key string, vars []string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	for _, name := range vars {
		if strings.TrimSpace(os.Getenv(name)) == key {
			return name
		}
	}
	return ""
}

// reportFailure prints a classified provider failure the way the surface it
// happened on should show it — the failure row on a terminal, one line into a
// pipe — and ends the process. It does not return the error: cobra would
// print it again underneath, and the second rendering is the raw one this
// whole story exists to stop showing. An error that is not a provider failure
// is handed back untouched for cobra to report as it always has.
func reportFailure(err error, model string) error {
	if err == nil {
		return nil
	}
	if _, ok := provider.AsFailure(err); !ok {
		return err
	}
	if isTerminal(os.Stderr) {
		if report, ok := ui.FailureReport(err, model); ok {
			fprintStyled(os.Stderr, report)
			os.Exit(1)
		}
	}
	if line, ok := ui.FailureLine(err); ok {
		fmt.Fprintln(os.Stderr, line)
		os.Exit(1)
	}
	return err
}

// isTerminal reports whether a file is a terminal, cygwin's included.
func isTerminal(f *os.File) bool {
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}
