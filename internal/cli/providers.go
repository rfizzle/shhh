package cli

// `shhh providers` — what this machine can talk to, and whether it is ready
// to. A gateway profile fails in quiet ways (an env var that isn't exported,
// a model the catalog no longer serves, a rewrite rule whose path stopped
// matching), so the listing is written to make each of those visible without
// starting a session.

import (
	"fmt"
	"io"
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
			"<config-dir>/providers.toml or <config-dir>/providers/*.toml — with each profile's endpoints, " +
			"auth, declared models, and rewrite rules.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			dirs := profile.Dirs(config.Paths())
			profiles, errs := profile.Load(dirs)

			names := provider.Available()
			sort.Strings(names)
			fmt.Fprintf(out, "Providers: %s\n", strings.Join(names, ", "))

			fmt.Fprintf(out, "\nProfile sources (search order):\n")
			for i, dir := range dirs {
				printSource(out, profile.Files(dirs)[i])
				printSource(out, dir)
			}

			for _, err := range errs {
				fmt.Fprintf(out, "\nERROR: %v\n", err)
			}

			if len(profiles) == 0 {
				fmt.Fprintf(out, "\nNo gateway profiles. Write a providers.toml at the first path above to add one.\n")
				return nil
			}

			prices := loadPricing()
			for _, p := range profiles {
				printProfile(cmd, p, prices)
			}
			if plan := profile.Plan(dirs); plan.NeedsWork() {
				fmt.Fprintf(out, "\n%d file(s) beside %s are now redundant; `shhh providers migrate` folds them in.\n",
					len(plan.Redundant()), plan.Target)
			}
			return nil
		},
	}
	cmd.AddCommand(newProvidersMigrateCmd())
	return cmd
}

// printSource lists one search location and whether anything is there.
func printSource(out io.Writer, path string) {
	marker := ""
	if _, err := os.Stat(path); err != nil {
		marker = "  (absent)"
	}
	fmt.Fprintf(out, "  %s%s\n", path, marker)
}

// newProvidersMigrateCmd folds every profile file into one providers.toml.
// It writes nothing without being asked twice — once by being run, once by
// --prune before it removes anything it has replaced.
func newProvidersMigrateCmd() *cobra.Command {
	var prune bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Fold every gateway profile into one providers.toml",
		Long: "Read every profile this machine has — <config-dir>/providers.toml and <config-dir>/providers/*.toml, " +
			"in load order — and write them as one file of [[provider]] blocks. The originals are left in place " +
			"unless --prune is given.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			plan := profile.Plan(profile.Dirs(config.Paths()))

			for _, err := range plan.Errors {
				fmt.Fprintf(out, "ERROR: %v\n", err)
			}
			for _, s := range plan.Shadowed {
				fmt.Fprintf(out, "SKIP:  %s in %s — %s already declares that name\n", s.Name, s.Path, s.Kept)
			}
			if len(plan.Profiles) == 0 {
				fmt.Fprintf(out, "No gateway profiles to migrate.\n")
				return nil
			}
			if !plan.NeedsWork() {
				fmt.Fprintf(out, "Already one file: %s holds %d provider(s).\n", plan.Target, len(plan.Profiles))
				return nil
			}

			fmt.Fprintf(out, "Consolidating %d provider(s) into %s; %d file(s) become redundant:\n",
				len(plan.Profiles), plan.Target, len(plan.Redundant()))
			for _, p := range plan.Profiles {
				fmt.Fprintf(out, "  %-20s %s\n", p.Name, p.Path)
			}

			if dryRun {
				fmt.Fprintf(out, "\n--- %s ---\n%s", plan.Target, profile.Encode(plan.Profiles))
				return nil
			}
			if err := plan.Write(); err != nil {
				return err
			}
			fmt.Fprintf(out, "\nWrote %s.\n", plan.Target)

			if !prune {
				fmt.Fprintf(out, "The originals still load and are now redundant; `shhh providers migrate --prune` removes them:\n")
				for _, path := range plan.Redundant() {
					fmt.Fprintf(out, "  %s\n", path)
				}
				return nil
			}
			removed, err := plan.Prune()
			for _, path := range removed {
				fmt.Fprintf(out, "Removed %s\n", path)
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&prune, "prune", false, "remove the files the new providers.toml replaces")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the file that would be written and change nothing")
	return cmd
}

// printProfile reports one profile: where it points, whether its key is
// resolvable, what it declares, and what its rules do. A profile with
// endpoints prints one block per endpoint, because a mis-globbed route is
// exactly the quiet failure this listing exists to catch.
func printProfile(cmd *cobra.Command, p profile.Profile, prices *pricing.Table) {
	out := cmd.OutOrStdout()
	routes := p.Routes()
	fmt.Fprintf(out, "\n%s (%s)\n", p.Name, p.API)
	fmt.Fprintf(out, "  file:     %s\n", p.Path)
	if len(routes) == 1 {
		printEndpoint(out, routes[0], "  ", prices)
		return
	}
	fmt.Fprintf(out, "  routing:  %d endpoints; a model no endpoint claims goes to the default\n", len(routes))
	for _, e := range routes {
		fmt.Fprintf(out, "\n  ── %s (%s)\n", e.Label, e.API)
		if len(e.Match) > 0 {
			fmt.Fprintf(out, "    match:    %s\n", strings.Join(e.Match, ", "))
		}
		printEndpoint(out, e, "    ", prices)
	}
}

// printEndpoint reports one address: where it points, how it authenticates,
// what it hosts and what it rewrites.
func printEndpoint(out io.Writer, e profile.Endpoint, indent string, prices *pricing.Table) {
	fmt.Fprintf(out, "%sbase url: %s\n", indent, e.BaseURL)
	if e.ModelsPath != "" {
		fmt.Fprintf(out, "%scatalog:  %s\n", indent, e.ModelsPath)
	}

	switch {
	case e.APIKey != "":
		fmt.Fprintf(out, "%sauth:     literal api_key\n", indent)
	case e.APIKeyEnv == "":
		fmt.Fprintf(out, "%sauth:     none configured\n", indent)
	case os.Getenv(e.APIKeyEnv) == "":
		fmt.Fprintf(out, "%sauth:     %s — WARNING: not set in this environment\n", indent, e.APIKeyEnv)
	default:
		fmt.Fprintf(out, "%sauth:     %s (set)\n", indent, e.APIKeyEnv)
	}

	if len(e.Headers) > 0 {
		keys := make([]string, 0, len(e.Headers))
		for k := range e.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(out, "%sheaders:  %s\n", indent, strings.Join(keys, ", "))
	}

	if len(e.Models) == 0 {
		fmt.Fprintf(out, "%smodels:   none declared (discovery only; no pricing or context metadata)\n", indent)
	} else {
		fmt.Fprintf(out, "%smodels:   %d declared\n", indent, len(e.Models))
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintf(w, "%s  MODEL\tCONTEXT\tINPUT $/Mtok\tOUTPUT $/Mtok\tSOURCE\n", indent)
		for _, m := range e.Models {
			fmt.Fprintf(w, "%s  %s\t%s\t%s\t%s\t%s\n", indent, m.ID, contextCell(m, prices), inputCell(m, prices), outputCell(m, prices), sourceCell(m, prices))
		}
		w.Flush()
	}

	if len(e.Rewrite) == 0 {
		return
	}
	fmt.Fprintf(out, "%srules:    %d\n", indent, len(e.Rewrite))
	for _, r := range e.Rewrite {
		scope := "all models"
		if r.When.Model != "" {
			scope = r.When.Model
		}
		fmt.Fprintf(out, "%s  [%s] %s %s (%s)\n", indent, r.Direction, r.Op, r.Path, scope)
		if r.Note != "" {
			fmt.Fprintf(out, "%s    %s\n", indent, r.Note)
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
