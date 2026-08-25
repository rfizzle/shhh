package cli

// `shhh providers` — what this machine can talk to, and whether it is ready
// to. A gateway profile fails in quiet ways (an env var that isn't exported,
// a model the catalog no longer serves, a rewrite rule whose path stopped
// matching), so the listing is written to make each of those visible without
// starting a session.

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/profile"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/spf13/cobra"
)

func newProvidersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "providers",
		Short: "List providers and check gateway profiles",
		Long: "Show every provider this machine can resolve — the built-in ones and any gateway profile in " +
			"<config-dir>/providers/*.toml — with each profile's endpoint, auth, declared models, and rewrite rules.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			profiles, errs := profile.Load(profile.Dirs(config.Paths()))

			names := provider.Available()
			sort.Strings(names)
			fmt.Fprintf(out, "Providers: %s\n", strings.Join(names, ", "))

			fmt.Fprintf(out, "\nProfile directories (search order):\n")
			for _, dir := range profile.Dirs(config.Paths()) {
				marker := ""
				if _, err := os.Stat(dir); err != nil {
					marker = "  (absent)"
				}
				fmt.Fprintf(out, "  %s%s\n", dir, marker)
			}

			for _, err := range errs {
				fmt.Fprintf(out, "\nERROR: %v\n", err)
			}

			if len(profiles) == 0 {
				fmt.Fprintf(out, "\nNo gateway profiles. Drop a .toml in the first directory above to add one.\n")
				return nil
			}

			prices := loadPricing()
			for _, p := range profiles {
				printProfile(cmd, p, prices)
			}
			return nil
		},
	}
	return cmd
}

// printProfile reports one profile: where it points, whether its key is
// resolvable, what it declares, and what its rules do.
func printProfile(cmd *cobra.Command, p profile.Profile, prices *pricing.Table) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\n%s (%s)\n", p.Name, p.API)
	fmt.Fprintf(out, "  file:     %s\n", p.Path)
	fmt.Fprintf(out, "  base url: %s\n", p.BaseURL)
	if p.ModelsPath != "" {
		fmt.Fprintf(out, "  catalog:  %s\n", p.ModelsPath)
	}

	switch {
	case p.APIKey != "":
		fmt.Fprintf(out, "  auth:     literal api_key\n")
	case p.APIKeyEnv == "":
		fmt.Fprintf(out, "  auth:     none configured\n")
	case os.Getenv(p.APIKeyEnv) == "":
		fmt.Fprintf(out, "  auth:     %s — WARNING: not set in this environment\n", p.APIKeyEnv)
	default:
		fmt.Fprintf(out, "  auth:     %s (set)\n", p.APIKeyEnv)
	}

	if len(p.Headers) > 0 {
		keys := make([]string, 0, len(p.Headers))
		for k := range p.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(out, "  headers:  %s\n", strings.Join(keys, ", "))
	}

	if len(p.Models) == 0 {
		fmt.Fprintf(out, "  models:   none declared (discovery only; no pricing or context metadata)\n")
	} else {
		fmt.Fprintf(out, "  models:   %d declared\n", len(p.Models))
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "    MODEL\tCONTEXT\tINPUT $/Mtok\tOUTPUT $/Mtok\tSOURCE")
		for _, m := range p.Models {
			fmt.Fprintf(w, "    %s\t%s\t%s\t%s\t%s\n", m.ID, contextCell(m, prices), inputCell(m, prices), outputCell(m, prices), sourceCell(m, prices))
		}
		w.Flush()
	}

	if len(p.Rewrite) == 0 {
		return
	}
	fmt.Fprintf(out, "  rules:    %d\n", len(p.Rewrite))
	for _, r := range p.Rewrite {
		scope := "all models"
		if r.When.Model != "" {
			scope = r.When.Model
		}
		fmt.Fprintf(out, "    [%s] %s %s (%s)\n", r.Direction, r.Op, r.Path, scope)
		if r.Note != "" {
			fmt.Fprintf(out, "      %s\n", r.Note)
		}
	}
}

// The metadata cells fall back to the public pricing table, which is the
// point of the fallback: a profile only has to declare what the public table
// gets wrong or has never heard of.

func contextCell(m profile.Model, prices *pricing.Table) string {
	if m.ContextWindow > 0 {
		return fmt.Sprintf("%d", m.ContextWindow)
	}
	if prices != nil {
		if window, ok := prices.ContextWindow(m.ID); ok {
			return fmt.Sprintf("%d", window)
		}
	}
	return "-"
}

func inputCell(m profile.Model, prices *pricing.Table) string {
	if m.Cost.HasPricing() {
		return fmt.Sprintf("%.2f", m.Cost.Input)
	}
	if in, _, ok := publicCost(prices, m.ID); ok {
		return fmt.Sprintf("%.2f", in)
	}
	return "-"
}

func outputCell(m profile.Model, prices *pricing.Table) string {
	if m.Cost.HasPricing() {
		return fmt.Sprintf("%.2f", m.Cost.Output)
	}
	if _, outCost, ok := publicCost(prices, m.ID); ok {
		return fmt.Sprintf("%.2f", outCost)
	}
	return "-"
}

func sourceCell(m profile.Model, prices *pricing.Table) string {
	switch {
	case m.Cost.HasPricing():
		return "profile"
	case hasPublicCost(prices, m.ID):
		return "public table"
	default:
		return "unpriced"
	}
}

// publicCost reads a model's public per-million-token prices.
func publicCost(prices *pricing.Table, model string) (float64, float64, bool) {
	if prices == nil {
		return 0, 0, false
	}
	in, out, ok := prices.Cost(model, 1_000_000, 1_000_000)
	return in, out, ok
}

func hasPublicCost(prices *pricing.Table, model string) bool {
	_, _, ok := publicCost(prices, model)
	return ok
}
