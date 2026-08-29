package cli

// Resolving the session's provider, and what happens when there isn't one
// (S-106, docs/interface/surfaces.md#the-recovery-row).
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

	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-isatty"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/ui"
)

// providerRequest is one attempt to resolve a provider: the names that were
// chosen and the credentials that were found for them.
type providerRequest struct {
	Provider string
	Model    string
	APIKey   string
	BaseURL  string
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
	})
	if err == nil {
		return p, req, nil
	}

	survey := resolve.SurveyPlaces(ctx, resolve.SurveyOpts{
		Provider:     req.Provider,
		ConfigAPIKey: cfg.ProviderAPIKey(),
		ConfigPaths:  config.Paths(),
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
	})
	if err != nil {
		return nil, req, err
	}
	if choice.Save {
		saveProviderChoice(cfg, next)
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
func saveProviderChoice(cfg config.Config, req providerRequest) {
	updates := [][2]string{{"provider.default", req.Provider}}
	if req.APIKey != "" {
		updates = append(updates, [2]string{"provider.api_key", req.APIKey})
	}
	if req.BaseURL != "" {
		updates = append(updates, [2]string{"provider.base_url", req.BaseURL})
	}
	if req.Model != "" {
		updates = append(updates, [2]string{"provider.model", req.Model})
	}
	for _, kv := range updates {
		if err := config.Set(&cfg, kv[0], kv[1]); err != nil {
			fmt.Fprintf(os.Stderr, "shhh: could not set %s: %v\n", kv[0], err)
			return
		}
	}
	if err := config.Save(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "shhh: could not save the provider to %s: %v\n", config.WritePath(), err)
		return
	}
	fmt.Fprintf(os.Stderr, "shhh: saved the provider to %s\n", config.WritePath())
}

// reportFailure prints a classified provider failure the way the surface it
// happened on should show it — the §17a row on a terminal, one line into a
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
